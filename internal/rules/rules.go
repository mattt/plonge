// Package rules defines which directories plonge reports as caches.
package rules

import (
	_ "embed"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"slices"
	"strings"

	"github.com/BurntSushi/toml"
)

//go:embed default.toml
var defaultTOML string

// Rules are the cache names, their marker files, and the quiet period.
type Rules struct {
	// Names are directory names that are caches.
	Names []string
	// Beside maps a name to project files; the name counts only
	// when one of them is in the same parent directory.
	Beside map[string][]string
	// QuietHours is how long a project must be inactive to be quiet.
	QuietHours float64
	// Skip are directory names that the walk never enters.
	// A name in Names still matches; plonge reports it and does not enter it.
	Skip []string
	// Global are the caches outside any project.
	Global []Global
}

// The kinds of a global rule.
const (
	KindPrune = "prune" // removes only what nothing uses
	KindClear = "clear" // removes everything; the next build downloads it again
)

// XcodeRuleID names the built-in rule for stale Xcode DerivedData folders.
const XcodeRuleID = "xcode-derived-data"

// Global describes a cache that a tool's own command cleans.
type Global struct {
	ID    string `toml:"id"`
	Tool  string `toml:"tool"`  // the command that must be available
	Path  string `toml:"path"`  // a command that prints the path, or a fixed path
	Clean string `toml:"clean"` // the command that cleans the cache
	Kind  string `toml:"kind"`
	OS    string `toml:"os"` // when set, the rule applies only on this GOOS
}

type file struct {
	Names      []string            `toml:"names"`
	QuietHours *float64            `toml:"quiet_hours"`
	Beside     map[string][]string `toml:"beside"`
	Skip       []string            `toml:"skip"`
	Global     []Global            `toml:"global"`
}

// versionControlDirs hold a project's history, so no rule may name them as caches.
var versionControlDirs = []string{".git", ".hg", ".jj", ".svn"}

func validName(n string) bool {
	return n != "" && n != "." && n != ".." && !strings.ContainsRune(n, '/')
}

func parse(data, name string) (file, error) {
	var f file
	md, err := toml.Decode(data, &f)
	if err != nil {
		return f, fmt.Errorf("%s: %w", name, err)
	}
	if undecoded := md.Undecoded(); len(undecoded) > 0 {
		keys := make([]string, len(undecoded))
		for i, k := range undecoded {
			keys[i] = k.String()
		}
		return f, fmt.Errorf("%s: unknown keys: %s", name, strings.Join(keys, ", "))
	}
	for _, n := range f.Names {
		if !validName(n) {
			return f, fmt.Errorf("%s: invalid name %q", name, n)
		}
		if slices.Contains(versionControlDirs, n) {
			return f, fmt.Errorf("%s: %q holds version control data and cannot be a cache", name, n)
		}
	}
	for _, n := range f.Skip {
		if !validName(n) {
			return f, fmt.Errorf("%s: invalid skip name %q", name, n)
		}
	}
	seen := map[string]bool{}
	for _, g := range f.Global {
		if err := g.validate(); err != nil {
			return f, fmt.Errorf("%s: %w", name, err)
		}
		if seen[g.ID] {
			return f, fmt.Errorf("%s: global rule %q appears twice", name, g.ID)
		}
		seen[g.ID] = true
	}
	if f.QuietHours != nil && *f.QuietHours < 0 {
		return f, fmt.Errorf("%s: quiet_hours must not be negative", name)
	}
	return f, nil
}

func (g Global) validate() error {
	if g.ID == "" {
		return fmt.Errorf("a global rule has no id")
	}
	if g.ID == XcodeRuleID {
		return fmt.Errorf("global rule id %q is reserved for a built-in rule", g.ID)
	}
	if g.Tool == "" || len(strings.Fields(g.Tool)) != 1 {
		return fmt.Errorf("global rule %q: tool must be one command name", g.ID)
	}
	if g.Kind != KindPrune && g.Kind != KindClear {
		return fmt.Errorf("global rule %q: kind must be %q or %q", g.ID, KindPrune, KindClear)
	}
	if !g.fixedPath() && !g.startsWithTool(g.Path) {
		return fmt.Errorf("global rule %q: path must be an absolute path, a ~/ path, or a %s command", g.ID, g.Tool)
	}
	if !g.startsWithTool(g.Clean) {
		return fmt.Errorf("global rule %q: clean must be a %s command", g.ID, g.Tool)
	}
	return nil
}

func (g Global) fixedPath() bool {
	return filepath.IsAbs(g.Path) || strings.HasPrefix(g.Path, "~/")
}

func (g Global) startsWithTool(cmd string) bool {
	words := strings.Fields(cmd)
	return len(words) > 0 && words[0] == g.Tool
}

// Default returns the built-in rules.
func Default() Rules {
	f, err := parse(defaultTOML, "default rules")
	if err != nil {
		panic(err)
	}
	return merge(Rules{Beside: map[string][]string{}}, f)
}

// merge adds the names and skip names in f to r, replaces each beside entry
// and each global rule with the same id, adds new global rules,
// and sets the quiet period when f has one.
func merge(r Rules, f file) Rules {
	out := Rules{
		Names:      slices.Clone(r.Names),
		Beside:     make(map[string][]string, len(r.Beside)+len(f.Beside)),
		QuietHours: r.QuietHours,
		Skip:       slices.Clone(r.Skip),
		Global:     slices.Clone(r.Global),
	}
	for k, v := range r.Beside {
		out.Beside[k] = v
	}
	for _, n := range f.Names {
		if !slices.Contains(out.Names, n) {
			out.Names = append(out.Names, n)
		}
	}
	for k, v := range f.Beside {
		out.Beside[k] = v
	}
	for _, n := range f.Skip {
		if !slices.Contains(out.Skip, n) {
			out.Skip = append(out.Skip, n)
		}
	}
	if f.QuietHours != nil {
		out.QuietHours = *f.QuietHours
	}
	for _, g := range f.Global {
		i := slices.IndexFunc(out.Global, func(o Global) bool { return o.ID == g.ID })
		if i >= 0 {
			out.Global[i] = g
		} else {
			out.Global = append(out.Global, g)
		}
	}
	slices.Sort(out.Names)
	slices.Sort(out.Skip)
	return out
}

// UserPath returns $XDG_CONFIG_HOME/plonge/rules.toml, or ~/.config/plonge/rules.toml.
func UserPath(xdgConfigHome, home string) string {
	if xdgConfigHome != "" {
		return filepath.Join(xdgConfigHome, "plonge", "rules.toml")
	}
	return filepath.Join(home, ".config", "plonge", "rules.toml")
}

// Load returns the default rules merged with the user file at path.
// A missing user file gives the defaults.
func Load(path string) (Rules, error) {
	r := Default()
	data, err := os.ReadFile(path)
	if errors.Is(err, fs.ErrNotExist) {
		return r, nil
	}
	if err != nil {
		return r, err
	}
	f, err := parse(string(data), path)
	if err != nil {
		return r, err
	}
	return merge(r, f), nil
}

// Match returns the rule that a directory named name matches,
// given the names of the entries in its parent directory.
// The result is empty when nothing matches.
func (r Rules) Match(name string, siblings func(string) bool) string {
	if !slices.Contains(r.Names, name) {
		return ""
	}
	markers, ok := r.Beside[name]
	if !ok {
		return name
	}
	for _, m := range markers {
		if siblings(m) {
			return name + " beside " + m
		}
	}
	return ""
}
