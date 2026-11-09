package global

import (
	"context"
	"errors"
	"fmt"
	"io/fs"
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
	argv   []string
	env    []string
	refuse string
	reason string
}

func newGuard(t tool, g rules.Global) *guard {
	argv, _ := command(t, g.Check)
	reason := g.RefuseReason
	if reason == "" {
		reason = "the check refused this rule"
	}
	return &guard{argv: argv, env: g.CheckEnv, refuse: g.Refuse, reason: reason}
}

// passes runs the entry's check, if it has one.
// When the check does not pass, the entry is marked skipped and cannot run.
func (r Runner) passes(ctx context.Context, e *Entry) bool {
	if e.guard == nil {
		return true
	}
	reason, refused := r.evaluate(ctx, e.guard)
	if reason == "" {
		return true
	}
	e.Skipped, e.Refused = reason, refused
	e.Command, e.argv = "", nil
	return false
}

// evaluate returns why the check refuses the rule, or an empty reason.
func (r Runner) evaluate(ctx context.Context, g *guard) (reason string, refused []string) {
	limit, cancel := context.WithTimeout(ctx, checkTimeout)
	defer cancel()
	out, err := r.runCombined(limit, g.argv, g.env)
	if err != nil {
		return "check failed: " + err.Error(), nil
	}
	if g.refuse == "" {
		return "", nil
	}
	pattern, err := regexp.Compile(g.refuse)
	if err != nil {
		return "invalid refuse pattern: " + err.Error(), nil
	}
	group := pattern.SubexpIndex("path")
	for _, line := range strings.Split(ansiEscape.ReplaceAllString(out, ""), "\n") {
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
