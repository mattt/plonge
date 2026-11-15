// Package rules defines which directories plonge reports as caches.
package rules

import (
	_ "embed"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
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
	// A project file can be a pattern, such as "*.csproj"; see IsPattern.
	Beside map[string][]string
	// RefuseInside maps a name to paths relative to a matching directory.
	// Plonge refuses a match that holds an entry at one of them.
	// Each element of a path can be a pattern; see ValidInsidePath.
	RefuseInside map[string][]string
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
	KindPrune = "prune" // selects unused or stale data by tool policy
	KindClear = "clear" // can remove cached data still in use
)

// XcodeRuleID names the built-in rule for stale Xcode DerivedData folders.
const XcodeRuleID = "xcode-derived-data"

// MiseToolsRuleID names the rule for mise's installed tool versions.
// When its tool is mise and it runs a clean command, plonge asks mise
// for the installs directory and uses the rule's path only when mise cannot report it.
// A delete_contents rule always uses its own path.
const MiseToolsRuleID = "mise-tools"

// Global describes a cache that a tool's own command cleans,
// or whose contents plonge deletes.
type Global struct {
	ID   string `toml:"id"`
	Tool string `toml:"tool"` // the command that must be available
	Path string `toml:"path"` // a command that prints the path, or a fixed path
	// PathEnv names an environment variable that holds a tool's directory,
	// such as CARGO_HOME. When it is set and not empty, plonge uses its value,
	// joined with PathEnvSubdir, in place of the fixed Path.
	PathEnv string `toml:"path_env"`
	// PathEnvSubdir is a relative path inside the directory that PathEnv gives.
	PathEnvSubdir string `toml:"path_env_subdir"`
	// PathLabel, when set, selects the last line of the path command's output
	// that contains the label followed by ": ", and takes the path after it.
	PathLabel string `toml:"path_label"`
	// PathZONField makes plonge read the path from this field of a ZON struct
	// that the path command prints, as "zig env" does. A relative path in it
	// is relative to the home directory, where plonge runs the command.
	// A rule can set only one of PathLabel, PathZONField, and PathField.
	PathZONField string `toml:"path_zon_field"`
	Clean        string `toml:"clean"` // the command that cleans the cache
	Kind         string `toml:"kind"`
	OS           string `toml:"os"` // when set, the rule applies only on this GOOS

	// PathField names the string field that holds the path
	// when the path command prints a JSON object.
	PathField string `toml:"path_field"`

	// DeleteContents makes plonge delete what is in Path, in place of Clean.
	// It is for caches that have no cleanup command, and needs kind clear.
	DeleteContents bool `toml:"delete_contents"`
	// RefuseNames are file or directory names that hold data a download
	// cannot restore. Plonge skips a delete_contents rule when its path
	// holds an entry with one of these names at any depth.
	RefuseNames []string `toml:"refuse_names"`

	// Check is a command that must succeed before the rule runs.
	// It must not change anything.
	Check string `toml:"check"`
	// CheckEnv are NAME=value pairs added to the environment of Check.
	CheckEnv []string `toml:"check_env"`
	// Refuse is a regular expression. A line of Check's output that matches it
	// skips the rule. A group named path ignores files known to be missing;
	// permission errors and unrecognized paths still count.
	Refuse string `toml:"refuse"`
	// RefuseReason says in the report what the matches of Refuse mean.
	RefuseReason string `toml:"refuse_reason"`
	// Checks are more checks that must also pass, in order, after Check.
	// Each has the same keys as the rule's own check.
	Checks []CheckSpec `toml:"checks"`
	// Require is a regular expression. Unless exactly one line of Check's output
	// matches it, the rule is skipped.
	Require string `toml:"require"`
	// Targets is a command that prints, as JSON, what Clean would remove.
	// The rule's size is then the size of those items, not of its whole path.
	Targets string `toml:"targets"`
}

type file struct {
	Names      []string            `toml:"names"`
	QuietHours *float64            `toml:"quiet_hours"`
	Beside     map[string][]string `toml:"beside"`
	// RefuseInside is refuse_inside; see Rules.RefuseInside.
	RefuseInside map[string][]string `toml:"refuse_inside"`
	Skip         []string            `toml:"skip"`
	Global       []Global            `toml:"global"`
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
	for n, markers := range f.Beside {
		for _, m := range markers {
			if _, err := filepath.Match(m, ""); err != nil {
				return f, fmt.Errorf("%s: beside %s: invalid project file pattern %q", name, n, m)
			}
		}
	}
	for n, paths := range f.RefuseInside {
		for _, p := range paths {
			if !ValidInsidePath(p) {
				return f, fmt.Errorf("%s: refuse_inside %s: invalid path %q", name, n, p)
			}
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
	if err := g.validatePathKeys(); err != nil {
		return err
	}
	if err := g.validatePathEnv(); err != nil {
		return err
	}
	if len(g.RefuseNames) > 0 && !g.DeleteContents {
		return fmt.Errorf("global rule %q: refuse_names needs delete_contents", g.ID)
	}
	for _, n := range g.RefuseNames {
		if !validName(n) {
			return fmt.Errorf("global rule %q: refuse_names: invalid name %q", g.ID, n)
		}
	}
	if g.DeleteContents {
		if err := g.validateDelete(); err != nil {
			return err
		}
	} else if !g.startsWithTool(g.Clean) {
		return fmt.Errorf("global rule %q: clean must be a %s command", g.ID, g.Tool)
	}
	if g.Targets != "" && !g.startsWithTool(g.Targets) {
		return fmt.Errorf("global rule %q: targets must be a %s command", g.ID, g.Tool)
	}
	return g.validateCheck()
}

// pathKeys returns the keys that set how plonge reads the output
// of the path command. Each one replaces the default, the last line.
func (g Global) pathKeys() []string {
	var keys []string
	if g.PathLabel != "" {
		keys = append(keys, "path_label")
	}
	if g.PathField != "" {
		keys = append(keys, "path_field")
	}
	if g.PathZONField != "" {
		keys = append(keys, "path_zon_field")
	}
	return keys
}

// validatePathKeys checks that a rule sets at most one path key,
// and only with a path command, because a fixed path has no output to read.
func (g Global) validatePathKeys() error {
	keys := g.pathKeys()
	switch {
	case len(keys) > 1:
		return fmt.Errorf("global rule %q: set only one of %s", g.ID, strings.Join(keys, ", "))
	case len(keys) == 1 && g.fixedPath():
		return fmt.Errorf("global rule %q: %s needs a path command", g.ID, keys[0])
	case strings.ContainsAny(g.PathLabel, "\r\n"):
		return fmt.Errorf("global rule %q: path_label must be one line", g.ID)
	case g.PathZONField != "" && !validZONField(g.PathZONField):
		return fmt.Errorf("global rule %q: path_zon_field %q is not a field name", g.ID, g.PathZONField)
	}
	return nil
}

// validatePathEnv checks path_env and path_env_subdir.
// The variable replaces a fixed path, which stays as the default,
// and the subdirectory must stay inside the directory that the variable gives.
func (g Global) validatePathEnv() error {
	switch {
	case g.PathEnv == "" && g.PathEnvSubdir != "":
		return fmt.Errorf("global rule %q: path_env_subdir needs path_env", g.ID)
	case g.PathEnv == "":
		return nil
	case !validEnvName(g.PathEnv):
		return fmt.Errorf("global rule %q: path_env %q is not an environment variable name", g.ID, g.PathEnv)
	case !g.fixedPath():
		return fmt.Errorf("global rule %q: path_env needs a fixed path as its default", g.ID)
	case g.PathEnvSubdir != "" && (!filepath.IsLocal(g.PathEnvSubdir) || filepath.Clean(g.PathEnvSubdir) == "."):
		return fmt.Errorf("global rule %q: path_env_subdir %q must be a relative path inside the directory", g.ID, g.PathEnvSubdir)
	}
	return nil
}

// validEnvName reports whether name is a portable environment variable name:
// letters, digits, and underscores, not starting with a digit.
func validEnvName(name string) bool {
	for i, c := range name {
		switch {
		case c == '_', 'a' <= c && c <= 'z', 'A' <= c && c <= 'Z':
		case '0' <= c && c <= '9' && i > 0:
		default:
			return false
		}
	}
	return name != ""
}

// validateDelete checks a rule whose contents plonge deletes.
// Such a rule can remove data still in use, so it must be a clear rule.
// Targets would size only part of what plonge deletes, so it is not allowed.
func (g Global) validateDelete() error {
	if g.Clean != "" {
		return fmt.Errorf("global rule %q: use clean or delete_contents, not both", g.ID)
	}
	if g.Kind != KindClear {
		return fmt.Errorf("global rule %q: delete_contents needs kind %q", g.ID, KindClear)
	}
	if g.Targets != "" {
		return fmt.Errorf("global rule %q: targets does not apply with delete_contents", g.ID)
	}
	return nil
}

// CheckSpec is a command that must succeed, and must not change anything,
// before a rule runs, with the patterns that its output must pass.
type CheckSpec struct {
	Check        string   `toml:"check"`
	CheckEnv     []string `toml:"check_env"`
	Refuse       string   `toml:"refuse"`
	RefuseReason string   `toml:"refuse_reason"`
	Require      string   `toml:"require"`
}

// CheckSpecs returns the rule's own check, if it has one, and then its Checks.
func (g Global) CheckSpecs() []CheckSpec {
	if g.Check == "" {
		return nil
	}
	own := CheckSpec{Check: g.Check, CheckEnv: g.CheckEnv, Refuse: g.Refuse, RefuseReason: g.RefuseReason, Require: g.Require}
	return append([]CheckSpec{own}, g.Checks...)
}

func (g Global) validateCheck() error {
	if g.Check == "" {
		if g.Refuse != "" || g.RefuseReason != "" || g.Require != "" || len(g.CheckEnv) > 0 || len(g.Checks) > 0 {
			return fmt.Errorf("global rule %q: refuse, refuse_reason, require, check_env, and checks need check", g.ID)
		}
		return nil
	}
	for _, c := range g.CheckSpecs() {
		if err := g.validateCheckSpec(c); err != nil {
			return err
		}
	}
	return nil
}

func (g Global) validateCheckSpec(c CheckSpec) error {
	if !g.startsWithTool(c.Check) {
		return fmt.Errorf("global rule %q: check must be a %s command", g.ID, g.Tool)
	}
	if c.Refuse == "" && c.RefuseReason != "" {
		return fmt.Errorf("global rule %q: refuse_reason needs refuse", g.ID)
	}
	if _, err := regexp.Compile(c.Refuse); err != nil {
		return fmt.Errorf("global rule %q: refuse: %w", g.ID, err)
	}
	if _, err := regexp.Compile(c.Require); err != nil {
		return fmt.Errorf("global rule %q: require: %w", g.ID, err)
	}
	for _, kv := range c.CheckEnv {
		if name, _, ok := strings.Cut(kv, "="); !ok || name == "" {
			return fmt.Errorf("global rule %q: check_env entry %q must be NAME=value", g.ID, kv)
		}
	}
	return nil
}

// validZONField reports whether name is a plain Zig identifier,
// the form that the ZON fields plonge reads are written in.
func validZONField(name string) bool {
	for i, c := range name {
		switch {
		case c == '_', 'a' <= c && c <= 'z', 'A' <= c && c <= 'Z':
		case '0' <= c && c <= '9' && i > 0:
		default:
			return false
		}
	}
	return name != ""
}

func (g Global) fixedPath() bool {
	return filepath.IsAbs(g.Path) || strings.HasPrefix(g.Path, "~/")
}

func (g Global) startsWithTool(cmd string) bool {
	words := strings.Fields(cmd)
	return len(words) > 0 && words[0] == g.Tool
}

// ValidInsidePath reports whether p can be a refuse_inside path:
// one or more elements separated by "/", none of them empty, ".", or "..",
// where each element is a name or a pattern in the syntax of [filepath.Match].
func ValidInsidePath(p string) bool {
	if p == "" || strings.ContainsRune(p, '\\') {
		return false
	}
	for elem := range strings.SplitSeq(p, "/") {
		if !validName(elem) {
			return false
		}
		if _, err := filepath.Match(elem, ""); err != nil {
			return false
		}
	}
	return true
}

// Default returns the built-in rules.
func Default() Rules {
	f, err := parse(defaultTOML, "default rules")
	if err != nil {
		panic(err)
	}
	return merge(Rules{Beside: map[string][]string{}, RefuseInside: map[string][]string{}}, f)
}

// merge adds the names and skip names in f to r,
// replaces each beside entry, each refuse_inside entry, and each global rule with the same id, adds new global rules,
// and sets the quiet period when f has one.
func merge(r Rules, f file) Rules {
	out := Rules{
		Names:        slices.Clone(r.Names),
		Beside:       make(map[string][]string, len(r.Beside)+len(f.Beside)),
		RefuseInside: make(map[string][]string, len(r.RefuseInside)+len(f.RefuseInside)),
		QuietHours:   r.QuietHours,
		Skip:         slices.Clone(r.Skip),
		Global:       slices.Clone(r.Global),
	}
	for k, v := range r.Beside {
		out.Beside[k] = v
	}
	for k, v := range r.RefuseInside {
		out.RefuseInside[k] = v
	}
	for k, v := range f.RefuseInside {
		out.RefuseInside[k] = v
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

// IsPattern reports whether a project file in Beside is a pattern
// in the syntax of [filepath.Match], such as "*.csproj",
// and not a literal file name.
func IsPattern(marker string) bool {
	return strings.ContainsAny(marker, `*?[\`)
}

// Match returns the rule that a directory named name matches,
// given a function that says whether its parent directory holds an entry
// with a listed project file name, or one that matches a pattern.
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
