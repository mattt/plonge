package global

import (
	"context"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/mattt/plonge/internal/clean"
	"github.com/mattt/plonge/internal/procs"
	"github.com/mattt/plonge/internal/rules"
	"github.com/mattt/plonge/internal/scan"
)

// The temporary directory has no cleanup command, so this rule is built in.
// It selects only the entries directly in the directory that $TMPDIR names,
// or /tmp when TMPDIR is not set, after it resolves symlinks.
// An entry is stale when it and everything in it last changed,
// by modification time or status change time, at least TmpAge ago.
// The rule keeps the names in tmpManagedNames without looking at them,
// and, on macOS, an entry with the system flag SF_NOUNLINK.
// It refuses a stale entry that is a symlink,
// is or holds a file with SF_NOUNLINK,
// is or holds a file of another user, or a file whose owner it cannot read,
// is or holds a mount point, by its device or the mount table,
// is used by a running process, as the shared process inspector reports,
// is or holds a socket, a named pipe, or a device,
// holds an entry that changed more recently,
// or is or holds a Git repository, and reports it in RefusedFolders.

// tmpManagedNames are patterns, in the syntax of filepath.Match,
// for entries that the operating system creates and manages.
var tmpManagedNames = []string{
	// macOS frameworks, launchd, XPC services, and app sandboxes
	// keep per-process data in directories named for their bundle IDs.
	"com.apple.*",
	// Foundation stages documents that apps are saving here.
	"TemporaryItems",
	// Contacts keeps its lock files here.
	".AddressBookLocks",
	// macOS stages cryptex personalization data here.
	"cryptex*",
	// X11 and the session manager listen on sockets in these directories,
	// which systemd-tmpfiles creates at boot.
	".X11-unix", ".ICE-unix", ".XIM-unix", ".font-unix", ".Test-unix",
	// The X server locks its display number with these files.
	".X*-lock",
	// systemd gives each service with PrivateTmp= its own directory here.
	"systemd-private-*",
	// snapd keeps the private temporary directories of snaps here.
	"snap-private-tmp",
}

// tmpManaged reports whether name is in tmpManagedNames.
func tmpManaged(name string) bool {
	return slices.ContainsFunc(tmpManagedNames, func(pattern string) bool {
		ok, _ := filepath.Match(pattern, name)
		return ok
	})
}

// DefaultTmpAge is how long ago an entry must have last changed
// when the rules give no tmpdir_age_hours.
const DefaultTmpAge = 24 * time.Hour

// tmpCommand is the Command of the tmpdir rule when it deletes n entries.
func tmpCommand(n int) string {
	return fmt.Sprintf("delete %d stale entries in the temporary directory (built in)", n)
}

// tmpEntry returns the rule for stale entries in the temporary directory,
// or false when it does not apply on this machine.
func (r Runner) tmpEntry(ctx context.Context) (Entry, bool) {
	if r.TmpDir == "" || (r.GOOS != "darwin" && r.GOOS != "linux") {
		return Entry{}, false
	}
	e := Entry{ID: rules.TmpDirRuleID, Tool: "lsof", Kind: rules.KindPrune, Path: r.TmpDir, stale: []string{}}
	t, mounts, reason := r.tmpState(ctx)
	if reason != "" {
		e.Skipped = reason
		return e, true
	}
	defer t.Close()
	e.Path = t.path
	names, err := readNames(t.root)
	if err != nil {
		e.Skipped = "cannot read the temporary directory: " + err.Error()
		return e, true
	}
	cutoff := r.clock().Add(-r.TmpAge)
	var candidates []string
	recent, managed := 0, 0
	for _, name := range names {
		path := filepath.Join(t.path, name)
		if tmpManaged(name) {
			managed++
			continue
		}
		info, err := t.root.Lstat(name)
		if errors.Is(err, fs.ErrNotExist) {
			continue
		}
		// An entry whose status cannot be read stays a candidate,
		// so that the report lists it with the reason.
		if err == nil {
			if st, ok := r.stat(info); ok {
				// macOS refuses to remove an entry with SF_NOUNLINK,
				// such as a folder that it creates for one of its services,
				// whatever its name.
				if st.noUnlink {
					managed++
					continue
				}
				if lastChange(info, st).After(cutoff) {
					recent++
					continue
				}
			}
		}
		candidates = append(candidates, path)
	}
	snap, err := r.processes().Snapshot(ctx)
	if err != nil {
		e.Skipped = "cannot list processes: " + err.Error()
		return e, true
	}
	var aliases []string
	for _, path := range candidates {
		aliases = append(aliases, r.tmpAliases(path)...)
	}
	used := snap.Find(aliases)
	var checked []string
	for _, path := range candidates {
		reason := r.tmpInUse(path, func(p string) string {
			if ms := used[p]; len(ms) > 0 {
				return procs.Reason(p, ms)
			}
			return ""
		})
		if reason == "" {
			reason = r.tmpCheck(t.root, filepath.Base(path), "", r.tmpPolicy(t, filepath.Base(path), cutoff, mounts), map[string]fileID{})
		}
		if reason != "" {
			e.refuseFolder(path, reason)
			continue
		}
		checked = append(checked, path)
	}
	guard := make([]bool, len(checked))
	for i := range guard {
		guard[i] = true
	}
	sizes, reasons := scan.GuardedSizes(checked, guard, nil)
	for i, path := range checked {
		if reasons[i] != "" {
			e.refuseFolder(path, reasons[i])
			continue
		}
		e.stale = append(e.stale, path)
		e.Bytes += sizes[i]
	}
	e.Command = tmpCommand(len(e.stale))
	e.Note = fmt.Sprintf("kept without checks: %d changed in the last %s, %d managed by the system",
		recent, ageText(r.TmpAge), managed)
	return e, true
}

// tmpState resolves and opens the temporary directory,
// checks that plonge may delete entries in it, and returns its device.
// It returns a reason when the rule cannot run;
// otherwise the caller must close the directory.
func (r Runner) tmpState(ctx context.Context) (t *tmpDir, mounts map[string]bool, reason string) {
	if !filepath.IsAbs(r.TmpDir) {
		return nil, nil, fmt.Sprintf("the temporary directory %q is not an absolute path", r.TmpDir)
	}
	dir, err := filepath.EvalSymlinks(r.TmpDir)
	if err != nil {
		return nil, nil, "cannot resolve the temporary directory: " + err.Error()
	}
	// Open the directory before the checks, and make sure afterward
	// that its path still names it, so the checks apply to what is open.
	t, err = openTmpDir(dir)
	if err != nil {
		return nil, nil, "cannot open the temporary directory: " + err.Error()
	}
	reason = r.refuseDelete(ctx, dir)
	if reason != "" {
		reason = "temporary directory " + r.tilde(dir) + ": " + reason
	} else if !t.unchanged() {
		reason = "the temporary directory changed while plonge checked it"
	}
	if reason == "" {
		// Without the mount table, plonge cannot tell whether an entry
		// holds a mount point on the same device, so the rule cannot run.
		if mounts, err = r.mountsUnder(dir); err != nil {
			reason = "cannot list mount points: " + err.Error()
		}
	}
	if reason != "" {
		t.Close()
		return nil, nil, reason
	}
	return t, mounts, ""
}

// processes returns the inspector that the tmpdir rule asks
// which processes use an entry: the one that a test sets, or procs.System.
func (r Runner) processes() procs.Inspector {
	if r.inspector != nil {
		return r.inspector
	}
	return procs.System()
}

// tmpAliases returns path, an entry in the resolved temporary directory,
// and the same entry under TmpDir when TmpDir goes through a symlink,
// because a command line can name either.
func (r Runner) tmpAliases(path string) []string {
	alias := filepath.Join(filepath.Clean(r.TmpDir), filepath.Base(path))
	if alias == path {
		return []string{path}
	}
	return []string{path, alias}
}

// tmpInUse returns why a process uses the entry at path, by any of its
// aliases, as inUse reports, or "" when none does.
func (r Runner) tmpInUse(path string, inUse func(string) string) string {
	for _, p := range r.tmpAliases(path) {
		if reason := inUse(p); reason != "" {
			return reason
		}
	}
	return ""
}

// recordedInspector remembers the error of the last snapshot it took,
// so that the tmpdir rule can stop when the processes cannot be listed.
type recordedInspector struct {
	procs.Inspector
	err error
}

func (i *recordedInspector) Snapshot(ctx context.Context) (*procs.Snapshot, error) {
	snap, err := i.Inspector.Snapshot(ctx)
	i.err = err
	return snap, err
}

// tmpInfoRefusal returns why the file that info describes keeps
// a stale entry from deletion, or "" when it does not.
// rel is the file's path in the stale entry, or "" for the entry itself.
func (r Runner) tmpInfoRefusal(info fs.FileInfo, rel string, pol tmpPolicy) string {
	is := "is"
	if rel != "" {
		is = "holds " + rel + ", which is"
	}
	if kind := specialKind(info.Mode().Type()); kind != "" {
		return is + " " + kind
	}
	st, ok := r.stat(info)
	if !ok {
		if rel == "" {
			return "cannot read its status"
		}
		return "cannot read the status of " + rel
	}
	if st.uid != pol.uid {
		if rel == "" {
			return "belongs to another user"
		}
		return "holds " + rel + ", which belongs to another user"
	}
	// A different device inside the entry is a mount point,
	// and removal would delete files on the mounted file system.
	if st.id.dev != pol.dev {
		return is + " on another file system"
	}
	if st.noUnlink {
		return is + " protected by macOS (sunlnk)"
	}
	if lastChange(info, st).After(pol.cutoff) {
		if rel == "" {
			return "changed in the last " + ageText(r.TmpAge)
		}
		return "holds " + rel + ", which changed in the last " + ageText(r.TmpAge)
	}
	return ""
}

// specialKind names a file type that a running program may still use
// without holding it open: a socket, a named pipe, or a device.
func specialKind(t fs.FileMode) string {
	switch {
	case t&fs.ModeSocket != 0:
		return "a socket"
	case t&fs.ModeNamedPipe != 0:
		return "a named pipe"
	case t&fs.ModeDevice != 0:
		return "a device"
	case t&fs.ModeIrregular != 0:
		return "a file of unknown type"
	}
	return ""
}

// lastChange returns the later of the modification time of info
// and the status change time in st. The status change time catches files
// that keep an old modification time, such as files extracted from an archive.
func lastChange(info fs.FileInfo, st fileStat) time.Time {
	if info.ModTime().After(st.changed) {
		return info.ModTime()
	}
	return st.changed
}

// ageText formats a period for the report, such as "24 hours".
func ageText(d time.Duration) string {
	return strconv.FormatFloat(d.Hours(), 'f', -1, 64) + " hours"
}

// clock returns the current time, or the time that a test sets.
func (r Runner) clock() time.Time {
	if r.now != nil {
		return r.now()
	}
	return time.Now()
}

// applyTmp checks the temporary directory and each listed entry again
// and deletes the entries that still pass.
// An entry that no longer passes is refused and kept,
// and the other entries are still deleted.
// The outcome's size, action, and entries are rebuilt from the entries
// that pass this final check, and its size is what they held right before.
func (r Runner) applyTmp(ctx context.Context, o *Outcome) {
	// The outcome shares its slices with the listed entry; do not write to them.
	o.RefusedFolders = slices.Clone(o.RefusedFolders)
	listed := o.stale
	o.stale, o.Bytes = []string{}, 0
	o.Command = tmpCommand(0)
	t, mounts, reason := r.tmpState(ctx)
	if reason == "" {
		defer t.Close()
		if t.path != o.Path {
			reason = "the temporary directory is now " + r.tilde(t.path)
		}
	}
	if reason != "" {
		o.Status, o.Reason = Skipped, reason
		return
	}
	cutoff := r.clock().Add(-r.TmpAge)
	insp := &recordedInspector{Inspector: r.processes()}
	inUse := clean.InUseChecker(ctx, insp, time.Now)
	var failures []string
	for _, path := range listed {
		name := filepath.Base(path)
		if filepath.Dir(path) != t.path {
			o.refuseFolder(path, "is not in the temporary directory")
			continue
		}
		if _, err := t.root.Lstat(name); errors.Is(err, fs.ErrNotExist) {
			continue
		}
		ids := map[string]fileID{}
		reason := r.tmpCheck(t.root, name, "", r.tmpPolicy(t, name, cutoff, mounts), ids)
		if reason == "" {
			reason = scan.GitRefusal(path)
		}
		if reason == "" {
			reason = r.tmpInUse(path, inUse)
			if insp.err != nil {
				// A failed list does not show that no process uses an entry,
				// so the rule stops here, as it would when it lists.
				failures = append(failures, "stopped: cannot list processes: "+insp.err.Error())
				break
			}
		}
		// The Git, process, and size checks read the entry by its path,
		// so make sure that the path still names the checked entry.
		if reason == "" && !t.unchanged() {
			reason = "the temporary directory " + errReplaced.Error()
		}
		if reason == "" {
			if info, err := os.Lstat(path); err != nil {
				reason = "cannot read: " + err.Error()
			} else if id, ok := identity(info); !ok || id != ids[""] {
				reason = errReplaced.Error()
			}
		}
		if reason != "" {
			o.refuseFolder(path, reason)
			continue
		}
		before := scan.DiskUsage(path)
		o.stale = append(o.stale, path)
		o.Bytes += before
		if err := tmpRemove(t.root, t.path, name, "", ids); err != nil {
			failures = append(failures, path+": "+err.Error())
		}
		o.FreedBytes += max(0, before-scan.DiskUsage(path))
	}
	o.Command = tmpCommand(len(o.stale))
	if insp.err != nil && len(o.stale) == 0 {
		o.Status, o.Reason = Skipped, "cannot list processes: "+insp.err.Error()
		return
	}
	if len(failures) > 0 {
		o.Status, o.Reason = Failed, strings.Join(failures, "; ")
		return
	}
	o.Status = Ran
}
