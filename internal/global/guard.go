package global

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"maps"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/mattt/plonge/internal/rules"
)

// checkTimeout limits a rule's check command.
const checkTimeout = 2 * time.Minute

// ansiEscape matches the terminal color codes that a tool may print.
var ansiEscape = regexp.MustCompile(`\x1b\[[0-9;]*[A-Za-z]`)

// guard is the check that a rule must pass before it runs.
type guard struct {
	argv    []string
	display string // the check command for people to read
	env     []string
	refuse  string
	reason  string
	require string
}

func newGuard(t tool, c rules.CheckSpec) *guard {
	argv, display := command(t, c.Check)
	reason := c.RefuseReason
	if reason == "" {
		reason = "the check refused this rule"
	}
	return &guard{argv: argv, display: display, env: c.CheckEnv, refuse: c.Refuse, reason: reason, require: c.Require}
}

// passes runs the entry's checks in order, if it has any.
// When one does not pass, the entry is marked skipped and cannot run,
// and the later checks do not run.
func (r Runner) passes(ctx context.Context, e *Entry) bool {
	for _, g := range e.guards {
		reason, refused := r.evaluate(ctx, g)
		if reason != "" {
			e.Skipped, e.Refused = reason, refused
			e.Command, e.argv = "", nil
			return false
		}
	}
	return true
}

// evaluate returns why the check refuses the rule, or an empty reason.
func (r Runner) evaluate(ctx context.Context, g *guard) (reason string, refused []string) {
	limit, cancel := context.WithTimeout(ctx, checkTimeout)
	defer cancel()
	out, err := r.runCombined(limit, g.argv, g.env)
	if err != nil {
		return "check failed: " + err.Error(), nil
	}
	lines := strings.Split(ansiEscape.ReplaceAllString(out, ""), "\n")
	if g.require != "" {
		pattern, err := regexp.Compile(g.require)
		if err != nil {
			return "invalid require pattern: " + err.Error(), nil
		}
		matches := 0
		for _, line := range lines {
			if pattern.MatchString(line) {
				matches++
			}
		}
		switch {
		case matches == 0:
			return fmt.Sprintf("%s printed no line that matches %s", g.display, g.require), nil
		case matches > 1:
			return fmt.Sprintf("%s printed more than one line that matches %s", g.display, g.require), nil
		}
	}
	if g.refuse == "" {
		return "", nil
	}
	pattern, err := regexp.Compile(g.refuse)
	if err != nil {
		return "invalid refuse pattern: " + err.Error(), nil
	}
	group := pattern.SubexpIndex("path")
	for _, line := range lines {
		m := pattern.FindStringSubmatch(line)
		if m == nil {
			continue
		}
		if group < 0 {
			return g.reason + ": " + strings.TrimSpace(line), nil
		}
		if path, ok := r.reportedFile(m[group]); ok && !slices.Contains(refused, path) {
			refused = append(refused, path)
		}
	}
	if len(refused) > 0 {
		slices.Sort(refused)
		return g.reason + ": " + strconv.Itoa(len(refused)), refused
	}
	return "", nil
}

// listTargets records what the rule would remove.
// When that cannot be known, the rule keeps the size of its whole path and says so,
// and Clean does not run it.
func (r Runner) listTargets(ctx context.Context, e *Entry, t tool, g rules.Global) {
	names, paths, err := r.targets(ctx, t, g)
	if err != nil {
		e.addNote("size is the whole directory: cannot list what would be removed: " + err.Error())
		e.targetsErr = err.Error()
		return
	}
	e.Targets, e.targetPaths, e.targeted = names, paths, true
}

func (r Runner) targets(ctx context.Context, t tool, g rules.Global) (names, paths []string, err error) {
	lookup, cancel := context.WithTimeout(ctx, lookupTimeout)
	defer cancel()
	argv, _ := command(t, g.Targets)
	out, err := r.run(lookup, argv)
	if err != nil {
		return nil, nil, err
	}
	var listed map[string][]struct {
		Version     string `json:"version"`
		InstallPath string `json:"install_path"`
	}
	if err := json.Unmarshal([]byte(out), &listed); err != nil {
		return nil, nil, fmt.Errorf("invalid JSON: %w", err)
	}
	for _, name := range slices.Sorted(maps.Keys(listed)) {
		for _, item := range listed[name] {
			if item.Version == "" || !filepath.IsAbs(item.InstallPath) {
				return nil, nil, fmt.Errorf("%s has no version or absolute install_path", name)
			}
			names = append(names, name+"@"+item.Version)
			paths = append(paths, item.InstallPath)
		}
	}
	return names, paths, nil
}

// runCombined runs argv from the home directory and returns everything that it printed.
func (r Runner) runCombined(ctx context.Context, argv, env []string) (string, error) {
	cmd := exec.CommandContext(ctx, argv[0], argv[1:]...)
	cmd.Dir = r.Home
	cmd.Env = append(os.Environ(), env...)
	out, err := cmd.CombinedOutput()
	if err != nil {
		if msg := lastLine(string(out)); msg != "" {
			return string(out), fmt.Errorf("%w: %s", err, msg)
		}
		return string(out), err
	}
	return string(out), nil
}

// reportedFile finds the file that text names, where text may continue
// after the path with ": " and a message. The result is false when the file
// does not exist. Text that is not a path counts as an existing file,
// because plonge cannot show that the file is gone.
func (r Runner) reportedFile(text string) (string, bool) {
	text = strings.TrimSpace(text)
	if !strings.HasPrefix(text, "~/") && !filepath.IsAbs(text) {
		return text, true
	}
	for i := 0; i <= len(text); {
		end := len(text)
		if n := strings.Index(text[i:], ": "); n >= 0 {
			end = i + n
		}
		path := r.expand(text[:end])
		if exists(path) {
			return r.tilde(path), true
		}
		i = end + 2
	}
	return "", false
}

// exists reports whether path may exist. Permission errors count as existing.
func exists(path string) bool {
	_, err := os.Stat(path)
	return err == nil || errors.Is(err, fs.ErrPermission)
}

func (r Runner) expand(path string) string {
	if rest, ok := strings.CutPrefix(path, "~/"); ok {
		return filepath.Join(r.Home, rest)
	}
	return path
}

func (r Runner) tilde(path string) string {
	if rest, ok := strings.CutPrefix(path, r.Home+string(filepath.Separator)); ok {
		return "~/" + rest
	}
	return path
}
