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
}

type file struct {
	Names      []string            `toml:"names"`
	QuietHours *float64            `toml:"quiet_hours"`
	Beside     map[string][]string `toml:"beside"`
	Skip       []string            `toml:"skip"`
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
	if f.QuietHours != nil && *f.QuietHours < 0 {
		return f, fmt.Errorf("%s: quiet_hours must not be negative", name)
	}
	return f, nil
}

// Default returns the built-in rules.
func Default() Rules {
	f, err := parse(defaultTOML, "default rules")
	if err != nil {
		panic(err)
	}
	return merge(Rules{Beside: map[string][]string{}}, f)
}

// merge adds the names and skip names in f to r, replaces each beside entry in f,
// and sets the quiet period when f has one.
func merge(r Rules, f file) Rules {
	out := Rules{
		Names:      slices.Clone(r.Names),
		Beside:     make(map[string][]string, len(r.Beside)+len(f.Beside)),
		QuietHours: r.QuietHours,
		Skip:       slices.Clone(r.Skip),
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
