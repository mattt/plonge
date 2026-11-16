package global

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/mattt/plonge/internal/clean"
	"github.com/mattt/plonge/internal/rules"
	"github.com/mattt/plonge/internal/scan"
)

// The temporary directory has no cleanup command, so this rule is built in.
// It selects only the entries directly in the directory that $TMPDIR names,
// or /tmp when TMPDIR is not set, after it resolves symlinks.
// An entry is stale when it and everything in it last changed,
// by modification time or status change time, at least TmpAge ago.
// The rule keeps the names in tmpManagedNames without looking at them.
// It refuses a stale entry that is a symlink, belongs to another user,
// is open in a process or is a process's working directory as lsof reports,
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
	dir, open, reason := r.tmpState(ctx)
	if reason != "" {
		e.Skipped = reason
		return e, true
	}
	e.Path = dir
	ents, err := os.ReadDir(dir)
	if err != nil {
		e.Skipped = "cannot read the temporary directory: " + err.Error()
		return e, true
	}
	cutoff := r.clock().Add(-r.TmpAge)
	var candidates []string
	recent, managed := 0, 0
	for _, ent := range ents {
		path := filepath.Join(dir, ent.Name())
		if tmpManaged(ent.Name()) {
			managed++
			continue
		}
		info, err := os.Lstat(path)
		if errors.Is(err, fs.ErrNotExist) {
			continue
		}
		// An entry whose status cannot be read stays a candidate,
		// so that the report lists it with the reason.
		if err == nil {
			if last, ok := lastChange(info); ok && last.After(cutoff) {
				recent++
				continue
			}
		}
		candidates = append(candidates, path)
	}
	var checked []string
	for _, path := range candidates {
		if reason := r.tmpRefusal(path, cutoff, open); reason != "" {
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

// tmpState resolves the temporary directory, checks that plonge may delete
// entries in it, and lists the names of the entries in it that a process
// has open or works in. It returns a reason when the rule cannot run.
func (r Runner) tmpState(ctx context.Context) (dir string, open map[string]bool, reason string) {
	if !filepath.IsAbs(r.TmpDir) {
		return "", nil, fmt.Sprintf("the temporary directory %q is not an absolute path", r.TmpDir)
	}
	dir, err := filepath.EvalSymlinks(r.TmpDir)
	if err != nil {
		return "", nil, "cannot resolve the temporary directory: " + err.Error()
	}
	if reason := r.refuseDelete(ctx, dir); reason != "" {
		return "", nil, "temporary directory " + r.tilde(dir) + ": " + reason
	}
	list := r.openFiles
	if list == nil {
		list = listOpenFiles
	}
	paths, err := list(ctx)
	if err != nil {
		return "", nil, "cannot list open files: " + err.Error()
	}
	return dir, openNames(paths, filepath.Clean(r.TmpDir), dir), ""
}

// tmpRefusal returns why plonge must not delete the stale entry at path,
// or "" when it may. It does not look for Git repositories.
func (r Runner) tmpRefusal(path string, cutoff time.Time, open map[string]bool) string {
	info, err := os.Lstat(path)
	if err != nil {
		return "cannot read: " + err.Error()
	}
	if info.Mode()&fs.ModeSymlink != 0 {
		return "is a symlink"
	}
	owner, _, ok := statOwner(info)
	if !ok {
		return "cannot read its owner"
	}
	if owner != uint32(os.Getuid()) {
		return "belongs to another user"
	}
	if open[filepath.Base(path)] {
		return "a running process has it or a file in it open, or works in it"
	}
	var reason string
	_ = filepath.WalkDir(path, func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			reason = "cannot check: " + err.Error()
			return fs.SkipAll
		}
		where := "is"
		if p != path {
			rel, _ := filepath.Rel(path, p)
			where = "holds " + filepath.ToSlash(rel) + ", which is"
		}
		if kind := specialKind(d.Type()); kind != "" {
			reason = where + " " + kind
			return fs.SkipAll
		}
		info, err := d.Info()
		if err != nil {
			reason = "cannot check: " + err.Error()
			return fs.SkipAll
		}
		last, ok := lastChange(info)
		if !ok {
			reason = "cannot read when " + filepath.ToSlash(p) + " changed"
			return fs.SkipAll
		}
		if last.After(cutoff) {
			if p == path {
				reason = "changed in the last " + ageText(r.TmpAge)
			} else {
				rel, _ := filepath.Rel(path, p)
				reason = "holds " + filepath.ToSlash(rel) + ", which changed in the last " + ageText(r.TmpAge)
			}
			return fs.SkipAll
		}
		return nil
	})
	return reason
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

// lastChange returns the later of the modification time and the status
// change time of info. The status change time catches files that keep an
// old modification time, such as files extracted from an archive.
func lastChange(info fs.FileInfo) (time.Time, bool) {
	_, changed, ok := statOwner(info)
	if !ok {
		return time.Time{}, false
	}
	if info.ModTime().After(changed) {
		return info.ModTime(), true
	}
	return changed, true
}

// ageText formats a period for the report, such as "24 hours".
func ageText(d time.Duration) string {
	return strconv.FormatFloat(d.Hours(), 'f', -1, 64) + " hours"
}

// openNames returns the names of the entries directly in each of dirs
// that hold a path in paths, or are one.
// lsof can follow a name with text such as " (deleted)",
// so a name is also recorded without that text.
func openNames(paths []string, dirs ...string) map[string]bool {
	names := map[string]bool{}
	for _, p := range paths {
		for _, dir := range dirs {
			rest, ok := strings.CutPrefix(p, dir+string(filepath.Separator))
			if !ok {
				continue
			}
			name, _, _ := strings.Cut(rest, string(filepath.Separator))
			names[name] = true
			if short, _, ok := strings.Cut(name, " ("); ok {
				names[short] = true
			}
		}
	}
	return names
}

// lsofDirs are where systems install lsof, for a PATH that leaves them out.
var lsofDirs = []string{"/usr/sbin", "/usr/bin", "/sbin"}

// listOpenFiles returns the path names that lsof reports for the files
// that processes have open, including each process's working directory.
// Without root, lsof sees only the processes of the current user.
func listOpenFiles(ctx context.Context) ([]string, error) {
	path, err := exec.LookPath("lsof")
	if err != nil {
		for _, dir := range lsofDirs {
			if p, err := exec.LookPath(filepath.Join(dir, "lsof")); err == nil {
				path = p
				break
			}
		}
	}
	if path == "" {
		return nil, errors.New("lsof is not installed")
	}
	ctx, cancel := context.WithTimeout(ctx, checkTimeout)
	defer cancel()
	// -w leaves out warnings, -n and -P leave out name lookups,
	// and -F n prints each file's name on a line that starts with n.
	cmd := exec.CommandContext(ctx, path, "-w", "-n", "-P", "-F", "n")
	cmd.WaitDelay = time.Second
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	out, err := cmd.Output()
	if err != nil {
		if msg := lastLine(stderr.String()); msg != "" {
			return nil, fmt.Errorf("lsof failed: %w: %s", err, msg)
		}
		return nil, fmt.Errorf("lsof failed: %w", err)
	}
	var paths []string
	for line := range strings.SplitSeq(string(out), "\n") {
		if name, ok := strings.CutPrefix(line, "n"); ok {
			paths = append(paths, name)
		}
	}
	return paths, nil
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
	dir, open, reason := r.tmpState(ctx)
	if reason == "" && dir != o.Path {
		reason = "the temporary directory is now " + r.tilde(dir)
	}
	if reason != "" {
		o.Status, o.Reason = Skipped, reason
		return
	}
	cutoff := r.clock().Add(-r.TmpAge)
	var failures []string
	for _, path := range listed {
		if _, err := os.Lstat(path); errors.Is(err, fs.ErrNotExist) {
			continue
		}
		reason := r.tmpRefusal(path, cutoff, open)
		if reason == "" {
			reason = scan.GitRefusal(path)
		}
		if reason != "" {
			o.refuseFolder(path, reason)
			continue
		}
		before := scan.DiskUsage(path)
		o.stale = append(o.stale, path)
		o.Bytes += before
		if err := clean.RemoveTree(path); err != nil {
			failures = append(failures, err.Error())
		}
		o.FreedBytes += max(0, before-scan.DiskUsage(path))
	}
	o.Command = tmpCommand(len(o.stale))
	if len(failures) > 0 {
		o.Status, o.Reason = Failed, strings.Join(failures, "; ")
		return
	}
	o.Status = Ran
}
