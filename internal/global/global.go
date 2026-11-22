// Package global lists and cleans caches that live outside any project.
package global

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"slices"
	"strings"
	"time"

	"github.com/mattt/plonge/internal/disk"
	"github.com/mattt/plonge/internal/procs"
	"github.com/mattt/plonge/internal/rules"
	"github.com/mattt/plonge/internal/scan"
)

// lookupTimeout limits the commands that only print a path or a location.
const lookupTimeout = 30 * time.Second

// Entry is one global rule and the cache that it found on this machine.
type Entry struct {
	ID      string `json:"id"`
	Tool    string `json:"tool"`
	Kind    string `json:"kind"`
	Path    string `json:"path,omitempty"`
	Bytes   int64  `json:"bytes"`
	Command string `json:"command,omitempty"`
	// Skipped is why plonge cannot use this rule here, or empty.
	Skipped string `json:"skipped,omitempty"`
	// Refused are the files that made the rule's check refuse it, with ~ for the home directory.
	Refused []string `json:"refused,omitempty"`
	// Targets are the items that the rule's command would remove.
	// When the rule lists them, Bytes is their size and not the size of Path.
	Targets []string `json:"targets,omitempty"`
	// Note says why Path is a fallback, why Bytes is the size of Path
	// although the rule has targets, or, for the tmpdir rule,
	// how many entries it kept without checking them.
	Note string `json:"note,omitempty"`
	// WholeCache is true when Bytes is the size of the whole cache
	// of a prune rule, whose tool removes only the part that its policy selects.
	// It is false for a clear rule, for a rule whose targets give Bytes,
	// and for the Xcode and tmpdir rules, whose Bytes is the size of what they delete.
	WholeCache bool `json:"whole_cache,omitempty"`
	// RefusedFolders are the folders that the rule would remove
	// but keeps, with the reason: for the Xcode rule, because they are
	// or hold a Git repository, and for the tmpdir rule, because they fail
	// one of its checks. They are not in Bytes or Command.
	// Only these two rules set them.
	RefusedFolders []scan.Refusal `json:"refused_folders,omitempty"`

	argv   []string
	guards []*guard
	// targeted is true when targetPaths, and not Path, gives Bytes.
	targeted    bool
	targetPaths []string
	// targetsErr is why the rule's targets command failed, or empty.
	// Clean does not run a rule whose targets it cannot list.
	targetsErr string
	stale      []string // for the built-in Xcode and tmpdir rules, what to delete
	// deleteContents is true when plonge deletes what is in Path itself.
	deleteContents bool
	// refuseNames are the rule's refuse_names.
	refuseNames []string
	// keepNames are the rule's keep_names, which plonge leaves in Path.
	keepNames []string
}

// Inputs are what plonge gives a rule's action, and what it listed
// for that action to remove. Each list is a separate field,
// so two lists match only when they hold the same items.
// An external command still decides what it removes when it runs;
// Inputs record only the command, its path, and what plonge listed.
type Inputs struct {
	Kind        string   `json:"kind"`
	Path        string   `json:"path"`
	Action      string   `json:"action"`
	Targets     []string `json:"targets"`
	TargetPaths []string `json:"target_paths"`
	KeepNames   []string `json:"keep_names"`
	Folders     []string `json:"folders"`
	// FolderBytes is the size of Folders for the Xcode and tmpdir rules,
	// whose folders and entries plonge deletes itself, and 0 for every other rule.
	FolderBytes int64 `json:"folder_bytes"`
}

// Inputs returns the inputs of the entry's action.
func (e Entry) Inputs() Inputs {
	in := Inputs{
		Kind:        e.Kind,
		Path:        e.Path,
		Action:      e.Command,
		Targets:     slices.Clone(e.Targets),
		TargetPaths: slices.Clone(e.targetPaths),
		KeepNames:   slices.Clone(e.keepNames),
		Folders:     slices.Clone(e.stale),
	}
	if e.ID == rules.XcodeRuleID || e.ID == rules.TmpDirRuleID {
		in.FolderBytes = e.Bytes
	}
	return in
}

// String describes the inputs with each text in Go's quoted form,
// as strconv.Quote writes it. The quoted form keeps every byte of a path,
// including bytes that are not valid UTF-8, which JSON would replace,
// and the boundaries of each list,
// so two Inputs have the same string only when every field is equal.
// The result is valid UTF-8, so it can be shown as it is.
func (in Inputs) String() string {
	return fmt.Sprintf("kind=%q path=%q action=%q targets=%q target_paths=%q keep_names=%q folders=%q folder_bytes=%d",
		in.Kind, in.Path, in.Action, in.Targets, in.TargetPaths, in.KeepNames, in.Folders, in.FolderBytes)
}

// Runner resolves tools and runs them from the home directory,
// so that a project's tool versions do not affect the result.
type Runner struct {
	Home string
	GOOS string
	// TmpDir is the temporary directory for the tmpdir rule,
	// which does not apply when TmpDir is empty.
	TmpDir string
	// TmpAge is how long ago an entry in TmpDir must have last changed.
	TmpAge time.Duration

	// inspector lists the running processes for the tmpdir rule;
	// nil uses procs.System.
	inspector procs.Inspector
	// now returns the current time; nil uses time.Now.
	now func() time.Time
	// statHook, when set, replaces statOf for the tmpdir rule.
	statHook func(fs.FileInfo) (fileStat, bool)
}

// New returns a Runner for this machine, whose tmpdir rule selects
// entries in the temporary directory that last changed tmpAgeHours ago.
// A tmpAgeHours that rules.TmpDirAge rejects, such as 0, selects DefaultTmpAge.
func New(home string, tmpAgeHours float64) Runner {
	age, err := rules.TmpDirAge(tmpAgeHours)
	if err != nil {
		age = DefaultTmpAge
	}
	return Runner{Home: home, GOOS: runtime.GOOS, TmpDir: os.TempDir(), TmpAge: age}
}

// run runs argv from the home directory and returns its standard output.
func (r Runner) run(ctx context.Context, argv []string) (string, error) {
	cmd := exec.CommandContext(ctx, argv[0], argv[1:]...)
	cmd.Dir = r.Home
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	out, err := cmd.Output()
	if err != nil {
		if msg := lastLine(stderr.String()); msg != "" {
			return string(out), fmt.Errorf("%w: %s", err, msg)
		}
		return string(out), err
	}
	return string(out), nil
}

func lastLine(s string) string {
	lines := strings.Split(strings.TrimSpace(s), "\n")
	return strings.TrimSpace(lines[len(lines)-1])
}

// tool says how to run a command.
type tool struct {
	argv    []string // the words that replace the tool's name
	display []string // the same words for people to read
}

// resolve finds how to run name: through mise exec when mise manages it,
// or from PATH. The result is false when the tool is not available.
func (r Runner) resolve(ctx context.Context, name string) (tool, bool) {
	if name != "mise" {
		if _, err := exec.LookPath("mise"); err == nil {
			lookup, cancel := context.WithTimeout(ctx, lookupTimeout)
			defer cancel()
			if _, err := r.run(lookup, []string{"mise", "which", name}); err == nil {
				words := []string{"mise", "exec", "--", name}
				return tool{argv: words, display: words}, true
			}
		}
	}
	path, ok := lookPathWithoutShims(name)
	if !ok {
		return tool{}, false
	}
	return tool{argv: []string{path}, display: []string{name}}, true
}

// lookPathWithoutShims searches PATH and passes over mise's shims,
// because a shim for a tool that mise does not activate cannot run it.
func lookPathWithoutShims(name string) (string, bool) {
	for _, dir := range filepath.SplitList(os.Getenv("PATH")) {
		if dir == "" || (filepath.Base(dir) == "shims" && filepath.Base(filepath.Dir(dir)) == "mise") {
			continue
		}
		if path, err := exec.LookPath(filepath.Join(dir, name)); err == nil {
			return path, true
		}
	}
	return "", false
}

// command replaces the tool's name at the start of a rule's command.
func command(t tool, cmd string) (argv []string, display string) {
	words := strings.Fields(cmd)[1:]
	argv = append(append([]string{}, t.argv...), words...)
	return argv, strings.Join(append(append([]string{}, t.display...), words...), " ")
}

// cachePath returns the cache directory of a rule:
// the path that basePath resolves, with path_subdir joined to it.
// An error from a path command says so; an error from path_env names the variable.
func (r Runner) cachePath(ctx context.Context, g rules.Global, t tool) (string, error) {
	path, err := r.basePath(ctx, g, t)
	if err != nil {
		return "", err
	}
	return filepath.Join(path, filepath.FromSlash(g.PathSubdir)), nil
}

// basePath returns the path that a rule's path_env, fixed path,
// or path command gives, before path_subdir is joined to it.
func (r Runner) basePath(ctx context.Context, g rules.Global, t tool) (string, error) {
	if g.PathEnv != "" {
		if dir := os.Getenv(g.PathEnv); dir != "" {
			// Cargo resolves a relative CARGO_HOME from its working directory,
			// which differs between commands, so plonge does not guess.
			if !filepath.IsAbs(dir) {
				return "", fmt.Errorf("%s is %q, which is not an absolute path", g.PathEnv, dir)
			}
			return filepath.Clean(dir), nil
		}
	}
	if rest, ok := strings.CutPrefix(g.Path, "~/"); ok {
		return filepath.Join(r.Home, rest), nil
	}
	if filepath.IsAbs(g.Path) {
		return filepath.Clean(g.Path), nil
	}
	lookup, cancel := context.WithTimeout(ctx, lookupTimeout)
	defer cancel()
	argv, _ := command(t, g.Path)
	out, err := r.run(lookup, argv)
	if err != nil {
		return "", fmt.Errorf("path command failed: %w", err)
	}
	path, err := printedPath(out, g)
	if err != nil {
		return "", fmt.Errorf("path command failed: %w", err)
	}
	if g.PathZONField != "" && path != "" && !filepath.IsAbs(path) {
		// Zig prints a path inside its working directory relative to it,
		// and plonge runs the command from the home directory.
		path = filepath.Join(r.Home, path)
	}
	if !filepath.IsAbs(path) {
		return "", fmt.Errorf("path command failed: printed %q, which is not an absolute path", path)
	}
	return filepath.Clean(path), nil
}

// printedPath picks the path text from the output of a rule's path command:
// the JSON field that path_field names, the ZON field that path_zon_field
// names, the text after path_label, or by default the last line.
// Rule validation allows at most one of these keys.
func printedPath(out string, g rules.Global) (string, error) {
	switch {
	case g.PathField != "":
		return jsonField(out, g.PathField)
	case g.PathZONField != "":
		return zonField(out, g.PathZONField)
	case g.PathLabel != "":
		return labeledPath(out, g.PathLabel)
	default:
		return lastLine(out), nil
	}
}

// jsonField returns the value of the string field name
// in the JSON object that out holds.
func jsonField(out, name string) (string, error) {
	var fields map[string]json.RawMessage
	if err := json.Unmarshal([]byte(out), &fields); err != nil {
		return "", fmt.Errorf("printed invalid JSON: %w", err)
	}
	raw, ok := fields[name]
	if !ok {
		return "", fmt.Errorf("printed no %q field", name)
	}
	var value string
	if err := json.Unmarshal(raw, &value); err != nil {
		return "", fmt.Errorf("printed a %q field that is not a string", name)
	}
	return value, nil
}

// labeledPath returns the text after "label: " on the lines of out
// that contain it, so that a prefix such as "info : " is allowed.
// It fails when no line has the label, when a line has the label but no path,
// or when two lines give different paths,
// because plonge cannot tell which one the tool means.
func labeledPath(out, label string) (string, error) {
	var path string
	for line := range strings.SplitSeq(out, "\n") {
		_, p, ok := strings.Cut(line, label+": ")
		if !ok {
			continue
		}
		p = strings.TrimSpace(p)
		if p == "" {
			return "", fmt.Errorf("printed no path after %q", label+": ")
		}
		if path != "" && filepath.Clean(p) != filepath.Clean(path) {
			return "", fmt.Errorf("printed more than one path for %q", label+": ")
		}
		path = p
	}
	if path == "" {
		return "", fmt.Errorf("printed no line with %q", label+": ")
	}
	return path, nil
}

// List resolves each rule for this operating system and measures its cache.
func (r Runner) List(ctx context.Context, rs []rules.Global) []Entry {
	tools := map[string]tool{}
	entries := make([]Entry, 0, len(rs))
	for _, g := range rs {
		if g.OS != "" && g.OS != r.GOOS {
			continue
		}
		e := Entry{ID: g.ID, Tool: g.Tool, Kind: g.Kind}
		t, ok := tools[g.Tool]
		if !ok {
			if t, ok = r.resolve(ctx, g.Tool); !ok {
				e.Skipped = "tool not found"
				entries = append(entries, e)
				continue
			}
			tools[g.Tool] = t
		}
		path, err := r.rulePath(ctx, &e, g, t)
		e.Path = path
		switch {
		case err != nil:
			e.Skipped = err.Error()
		case g.DeleteContents:
			e.deleteContents = true
			e.refuseNames, e.keepNames = g.RefuseNames, g.KeepNames
			if e.Skipped = r.refuseDelete(ctx, path); e.Skipped == "" {
				e.Skipped = refuseNames(path, e.refuseNames, e.keepNames)
			}
			if e.Skipped == "" {
				e.Command = r.deleteAction(path, e.keepNames)
			}
		default:
			e.argv, e.Command = command(t, g.Clean)
		}
		if e.Skipped == "" && g.Check != "" {
			for _, c := range g.CheckSpecs() {
				e.guards = append(e.guards, newGuard(t, c))
			}
			r.passes(ctx, &e)
		}
		if e.Skipped == "" && g.Targets != "" {
			r.listTargets(ctx, &e, t, g)
		}
		entries = append(entries, e)
	}
	measure(entries)
	for i := range entries {
		e := &entries[i]
		e.WholeCache = e.Skipped == "" && e.Kind == rules.KindPrune && !e.targeted
	}
	if e, ok := r.xcodeEntry(ctx); ok {
		entries = append(entries, e)
	}
	if e, ok := r.tmpEntry(ctx); ok {
		entries = append(entries, e)
	}
	return entries
}

// usage measures path, which may be a symlink to the cache directory,
// without the entries directly in it that keep names.
func usage(path string, keep ...string) int64 {
	if p, err := filepath.EvalSymlinks(path); err == nil {
		path = p
	}
	return scan.DiskUsage(path, keep...)
}

// measure sizes each entry that is not skipped.
// The walk of a delete_contents path also looks for a Git repository
// below it, and skips the entry when it finds one,
// because the deletion would lose that repository's commits and changes.
// The walk leaves out the entries that the rule keeps.
func measure(entries []Entry) {
	var dirs []string
	var guard []bool
	var keep [][]string
	var index []int
	for i, e := range entries {
		switch {
		case e.Skipped != "":
		case e.targeted:
			for _, p := range e.targetPaths {
				// A link removes only itself, so its target does not count.
				if info, err := os.Lstat(p); err == nil && info.IsDir() {
					dirs = append(dirs, p)
					guard = append(guard, false)
					keep = append(keep, nil)
					index = append(index, i)
				}
			}
		default:
			if p, err := filepath.EvalSymlinks(e.Path); err == nil {
				dirs = append(dirs, p)
				guard = append(guard, e.deleteContents)
				keep = append(keep, e.keepNames)
				index = append(index, i)
			}
		}
	}
	sizes, reasons := scan.GuardedSizes(dirs, guard, keep)
	for k, n := range sizes {
		e := &entries[index[k]]
		if reasons[k] != "" {
			e.Skipped, e.Command, e.Bytes = reasons[k], "", 0
			continue
		}
		e.Bytes += n
	}
}

// The statuses of an Outcome.
const (
	WouldRun = "would run"
	Ran      = "ran"
	Skipped  = "skipped"
	Failed   = "failed"
)

// Options choose what Clean runs.
type Options struct {
	Apply        bool
	IncludeClear bool
}

// Outcome is what happened to one rule.
type Outcome struct {
	Entry
	Status     string `json:"status"`
	Reason     string `json:"reason,omitempty"`
	FreedBytes int64  `json:"freed_bytes"`
}

// Result is the outcome of a global clean.
type Result struct {
	Applied    bool      `json:"applied"`
	Outcomes   []Outcome `json:"outcomes"`
	FreedBytes int64     `json:"freed_bytes"`
	Failed     int       `json:"failed"`
	// HomeVolume is the free space on the home directory's volume
	// before and after the actions ran; it is nil without Apply.
	HomeVolume *disk.Change `json:"home_volume,omitempty"`
}

// Clean runs the clean action of each entry, or only lists it without Apply.
// A clear rule runs only with IncludeClear.
func (r Runner) Clean(ctx context.Context, entries []Entry, opts Options) Result {
	res := Result{Applied: opts.Apply, Outcomes: make([]Outcome, 0, len(entries))}
	for _, e := range entries {
		o := Outcome{Entry: e, Status: WouldRun}
		switch {
		case e.Skipped != "":
			o.Status, o.Reason = Skipped, e.Skipped
		case e.targetsErr != "":
			// Without the list, a dry run cannot show what the command removes,
			// and a failed lookup must not look like an empty list.
			o.Status, o.Reason = Skipped, "cannot list what the rule would remove: "+e.targetsErr
		case e.Kind == rules.KindClear && !opts.IncludeClear:
			o.Status, o.Reason = Skipped, "clear rule requires --include-clear"
		case opts.Apply:
			r.apply(ctx, &o)
		}
		res.Outcomes = append(res.Outcomes, o)
		res.FreedBytes += o.FreedBytes
		if o.Status == Failed {
			res.Failed++
		}
	}
	return res
}

// apply runs one rule's command, or deletes its contents,
// and measures the cache again.
func (r Runner) apply(ctx context.Context, o *Outcome) {
	if !r.passes(ctx, &o.Entry) {
		o.Status, o.Reason = Skipped, o.Skipped
		return
	}
	switch o.ID {
	case rules.XcodeRuleID:
		applyXcode(ctx, o)
		return
	case rules.TmpDirRuleID:
		r.applyTmp(ctx, o)
		return
	}
	if o.deleteContents {
		r.applyDelete(ctx, o)
		return
	}
	before := o.Bytes
	if o.targeted {
		before = usage(o.Path)
	}
	_, err := r.run(ctx, o.argv)
	o.FreedBytes = max(0, before-usage(o.Path))
	if err != nil {
		o.Status, o.Reason = Failed, err.Error()
		return
	}
	o.Status = Ran
}
