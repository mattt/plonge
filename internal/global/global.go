// Package global lists and cleans caches that live outside any project.
package global

import (
	"bytes"
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"time"

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

	argv  []string
	stale []string // for the built-in Xcode rule, the folders to delete
}

// Runner resolves tools and runs them from the home directory,
// so that a project's tool versions do not affect the result.
type Runner struct {
	Home string
	GOOS string
}

// New returns a Runner for this machine.
func New(home string) Runner {
	return Runner{Home: home, GOOS: runtime.GOOS}
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

// cachePath returns the cache directory of a rule.
func (r Runner) cachePath(ctx context.Context, g rules.Global, t tool) (string, error) {
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
		return "", err
	}
	path := lastLine(out)
	if !filepath.IsAbs(path) {
		return "", fmt.Errorf("printed %q, which is not an absolute path", path)
	}
	return filepath.Clean(path), nil
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
		e.argv, e.Command = command(t, g.Clean)
		path, err := r.cachePath(ctx, g, t)
		if err != nil {
			e.Skipped = "path command failed: " + err.Error()
			e.Command, e.argv = "", nil
		}
		e.Path = path
		entries = append(entries, e)
	}
	measure(entries)
	if e, ok := r.xcodeEntry(); ok {
		entries = append(entries, e)
	}
	return entries
}

// usage measures path, which may be a symlink to the cache directory.
func usage(path string) int64 {
	if p, err := filepath.EvalSymlinks(path); err == nil {
		path = p
	}
	return scan.DiskUsage(path)
}

func measure(entries []Entry) {
	var dirs []string
	var index []int
	for i, e := range entries {
		if e.Skipped == "" {
			if p, err := filepath.EvalSymlinks(e.Path); err == nil {
				dirs = append(dirs, p)
				index = append(index, i)
			}
		}
	}
	for k, n := range scan.Sizes(dirs) {
		entries[index[k]].Bytes = n
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
}

// Clean runs the clean command of each entry, or only lists it without Apply.
// A clear rule runs only with IncludeClear.
func (r Runner) Clean(ctx context.Context, entries []Entry, opts Options) Result {
	res := Result{Applied: opts.Apply, Outcomes: make([]Outcome, 0, len(entries))}
	for _, e := range entries {
		o := Outcome{Entry: e, Status: WouldRun}
		switch {
		case e.Skipped != "":
			o.Status, o.Reason = Skipped, e.Skipped
		case e.Kind == rules.KindClear && !opts.IncludeClear:
			o.Status, o.Reason = Skipped, "a clear rule; needs --include-clear"
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

// apply runs one rule's command and measures the cache again.
func (r Runner) apply(ctx context.Context, o *Outcome) {
	if o.ID == rules.XcodeRuleID {
		applyXcode(ctx, o)
		return
	}
	_, err := r.run(ctx, o.argv)
	o.FreedBytes = max(0, o.Bytes-usage(o.Path))
	if err != nil {
		o.Status, o.Reason = Failed, err.Error()
		return
	}
	o.Status = Ran
}
