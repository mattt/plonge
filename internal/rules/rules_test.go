package rules

import (
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"testing"
)

func TestDefault(t *testing.T) {
	r := Default()
	if len(r.Names) != 27 || r.QuietHours != 24 {
		t.Errorf("got %d names and %g quiet hours", len(r.Names), r.QuietHours)
	}
	if !slices.Equal(r.Beside["target"], []string{"Cargo.toml", "pom.xml"}) {
		t.Errorf("beside target = %q", r.Beside["target"])
	}
	if !slices.Equal(r.Beside["build"], []string{"pyproject.toml", "setup.py"}) {
		t.Errorf("beside build = %q", r.Beside["build"])
	}
	if !slices.Equal(r.Beside["_build"], []string{"mix.exs"}) {
		t.Errorf("beside _build = %q", r.Beside["_build"])
	}
	if !slices.Equal(r.RefuseInside["_build"], []string{"*/rel", "*/lib/*/src"}) {
		t.Errorf("refuse_inside _build = %q", r.RefuseInside["_build"])
	}
	// deps can hold edited dependency sources, so no default rule names it.
	if slices.Contains(r.Names, "deps") {
		t.Error("default names include deps")
	}
}

func writeRules(t *testing.T, body string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "rules.toml")
	if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
	return path
}

func TestLoad(t *testing.T) {
	tests := []struct {
		name       string
		body       string
		wantNames  []string // names that must be present
		wantBeside map[string][]string
		wantQuiet  float64
		wantErr    bool
	}{
		{
			name:      "names add to defaults",
			body:      `names = [".cache", "node_modules"]`,
			wantNames: []string{".cache", "node_modules", "dist"},
			wantQuiet: 24,
		},
		{
			name: "beside entry replaces default",
			body: "[beside]\ndist = [\"vite.config.ts\"]\nbuild = [\"build.gradle\"]\n",
			wantBeside: map[string][]string{
				"dist":   {"vite.config.ts"},
				"build":  {"build.gradle"},
				"target": {"Cargo.toml", "pom.xml"},
			},
			wantQuiet: 24,
		},
		{name: "quiet hours", body: "quiet_hours = 72", wantQuiet: 72},
		{name: "unknown key", body: "nmaes = [\"x\"]", wantErr: true},
		{name: "path name", body: `names = ["out/gen"]`, wantNames: []string{"out/gen"}, wantQuiet: 24},
		{name: "absolute path name", body: `names = ["/a"]`, wantErr: true},
		{name: "path name with trailing slash", body: `names = ["a/"]`, wantErr: true},
		{name: "path name with empty part", body: `names = ["a//b"]`, wantErr: true},
		{name: "path name with dot dot", body: `names = ["a/../b"]`, wantErr: true},
		{name: "path name with dot", body: `names = ["./a"]`, wantErr: true},
		{name: "negative quiet hours", body: "quiet_hours = -1", wantErr: true},
		{name: "bad toml", body: "names = [", wantErr: true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			r, err := Load(writeRules(t, tt.body))
			if tt.wantErr {
				if err == nil {
					t.Fatal("want an error")
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			for _, n := range tt.wantNames {
				if !slices.Contains(r.Names, n) {
					t.Errorf("names %q lack %q", r.Names, n)
				}
			}
			if !slices.IsSorted(r.Names) || len(slices.Compact(slices.Clone(r.Names))) != len(r.Names) {
				t.Errorf("names %q are not sorted and unique", r.Names)
			}
			for k, v := range tt.wantBeside {
				if !slices.Equal(r.Beside[k], v) {
					t.Errorf("beside %s = %q, want %q", k, r.Beside[k], v)
				}
			}
			if r.QuietHours != tt.wantQuiet {
				t.Errorf("quiet hours = %g, want %g", r.QuietHours, tt.wantQuiet)
			}
		})
	}
	// Loading must not change the defaults.
	if !slices.Equal(Default().Beside["dist"], []string{"package.json", "pyproject.toml", "setup.py"}) {
		t.Error("Load changed the default rules")
	}
}

func TestLoadMissing(t *testing.T) {
	r, err := Load(filepath.Join(t.TempDir(), "none.toml"))
	if err != nil || len(r.Names) != len(Default().Names) {
		t.Errorf("got %v, %v; want the defaults", r.Names, err)
	}
}

func TestUserPath(t *testing.T) {
	if got := UserPath("/x", "/h"); got != "/x/plonge/rules.toml" {
		t.Errorf("got %q", got)
	}
	if got := UserPath("", "/h"); got != "/h/.config/plonge/rules.toml" {
		t.Errorf("got %q", got)
	}
}

func TestMatch(t *testing.T) {
	r := Default()
	tests := []struct {
		name     string
		siblings []string
		want     string
	}{
		{"node_modules", nil, "node_modules"},
		{"dist", nil, ""},
		{"dist", []string{"package.json"}, "dist beside package.json"},
		{"dist", []string{"pyproject.toml"}, "dist beside pyproject.toml"},
		{"dist", []string{"setup.py"}, "dist beside setup.py"},
		{"dist", []string{"requirements.txt"}, ""},
		{"target", []string{"pom.xml"}, "target beside pom.xml"},
		{"target", []string{"package.json"}, ""},
		{"src", []string{"package.json"}, ""},
		{".zig-cache", []string{"build.zig"}, ".zig-cache beside build.zig"},
		{"zig-cache", []string{"build.zig"}, "zig-cache beside build.zig"},
		{".zig-cache", nil, ""},
		{"zig-cache", []string{"build.zig.zon"}, ""},
		{"zig-out", []string{"build.zig"}, ""},
		{"bin", nil, ""},
		{"obj", []string{"package.json"}, ""},
		{".tox", nil, ".tox"},
		{".nox", nil, ".nox"},
		{"build", nil, ""},
		{"build", []string{"package.json", "build.gradle"}, ""},
		{"build", []string{"pyproject.toml"}, "build beside pyproject.toml"},
		{"build", []string{"setup.py"}, "build beside setup.py"},
		{"vendor/bundle", []string{"Gemfile"}, "vendor/bundle beside Gemfile"},
		{"vendor/bundle", []string{"gems.rb"}, "vendor/bundle beside gems.rb"},
		{"vendor/bundle", nil, ""},
		{"vendor/bundle", []string{"go.mod"}, ""},
		{"bundle", []string{"Gemfile"}, ""},
		{"vendor", []string{"Gemfile"}, ""},
		{"vendor/cache", []string{"Gemfile"}, ""},
		{"tmp/cache", []string{"config.ru"}, "tmp/cache beside config.ru"},
		{"tmp/cache", []string{"Gemfile"}, ""},
		{"cache", []string{"config.ru"}, ""},
		{"tmp", []string{"config.ru"}, ""},
		{"tmp/pids", []string{"config.ru"}, ""},
	}
	for _, tt := range tests {
		// The parent of a one-name path is the root.
		key, got := r.Match(tt.name, func(dir, m string) bool { return dir == "" && slices.Contains(tt.siblings, m) })
		if wantKey, _, _ := strings.Cut(tt.want, " beside "); got != tt.want || key != wantKey {
			t.Errorf("Match(%q, %q) = %q, %q; want %q", tt.name, tt.siblings, key, got, tt.want)
		}
	}
}

func TestMatchPathName(t *testing.T) {
	r, err := Load(writeRules(t, "names = [\"out/gen\", \"tmp/x\"]\n[beside]\n\"out/gen\" = [\"x.proj\"]\n"))
	if err != nil {
		t.Fatal(err)
	}
	tests := []struct {
		rel  string
		dir  string // the directory that holds the project file
		want string
	}{
		{"out/gen", "", "out/gen beside x.proj"},
		{"app/out/gen", "app", "out/gen beside x.proj"},
		{"a/b/out/gen", "a/b", "out/gen beside x.proj"},
		{"app/out/gen", "app/out", ""}, // the project file is beside gen, not beside out
		{"gen", "", ""},
		{"app/gen", "app", ""},
		{"app/myout/gen", "app", ""},
		{"out", "", ""},
		{"tmp/x", "", "tmp/x"},
		{"a/tmp/x", "", "tmp/x"},
		{"tmp/x/y", "", ""},
	}
	for _, tt := range tests {
		key, got := r.Match(tt.rel, func(dir, m string) bool { return dir == tt.dir && m == "x.proj" })
		if wantKey, _, _ := strings.Cut(tt.want, " beside "); got != tt.want || key != wantKey {
			t.Errorf("Match(%q) with x.proj in %q = %q, %q; want %q", tt.rel, tt.dir, key, got, tt.want)
		}
	}
}

func TestBesidePatterns(t *testing.T) {
	for _, m := range []string{"*.csproj", "a?.txt", "[ab].toml", `\*`} {
		if !IsPattern(m) {
			t.Errorf("IsPattern(%q) = false", m)
		}
	}
	for _, m := range []string{"package.json", "Cargo.toml"} {
		if IsPattern(m) {
			t.Errorf("IsPattern(%q) = true", m)
		}
	}
	r, err := Load(writeRules(t, "names = [\"out\"]\n[beside]\nout = [\"*.proj\"]\n"))
	if err != nil {
		t.Fatal(err)
	}
	if !slices.Equal(r.Beside["out"], []string{"*.proj"}) {
		t.Errorf("beside out = %q", r.Beside["out"])
	}
	_, err = Load(writeRules(t, "[beside]\nout = [\"[a\"]\n"))
	if err == nil || !strings.Contains(err.Error(), "invalid project file pattern") {
		t.Errorf("bad pattern: err = %v", err)
	}
}

func TestRefuseInside(t *testing.T) {
	r, err := Load(writeRules(t, "names = [\"out\"]\n[refuse_inside]\nout = [\"*/rel\", \"keep\"]\n"))
	if err != nil {
		t.Fatal(err)
	}
	if !slices.Equal(r.RefuseInside["out"], []string{"*/rel", "keep"}) {
		t.Errorf("refuse_inside out = %q", r.RefuseInside["out"])
	}
	// A user entry replaces the default entry for that name.
	r, err = Load(writeRules(t, "[refuse_inside]\n_build = [\"*/rel\", \"*/keep\"]\n"))
	if err != nil {
		t.Fatal(err)
	}
	if !slices.Equal(r.RefuseInside["_build"], []string{"*/rel", "*/keep"}) {
		t.Errorf("refuse_inside _build = %q", r.RefuseInside["_build"])
	}
	for _, p := range []string{"rel", "*/rel", "a/b/c", "[ab]*/x"} {
		if !ValidInsidePath(p) {
			t.Errorf("ValidInsidePath(%q) = false", p)
		}
	}
	for _, p := range []string{"", "/rel", "rel/", "a//b", "./rel", "../rel", "a/../b", "[a/rel", `a\b`} {
		if ValidInsidePath(p) {
			t.Errorf("ValidInsidePath(%q) = true", p)
		}
		body := "[refuse_inside]\nout = [" + strconv.Quote(p) + "]\n"
		if _, err := Load(writeRules(t, body)); err == nil || !strings.Contains(err.Error(), "refuse_inside out: invalid path") {
			t.Errorf("Load(%q): err = %v", p, err)
		}
	}
}

func TestSkip(t *testing.T) {
	want := []string{".cache", ".git", ".hg", ".jj", ".svn"}
	if got := Default().Skip; !slices.Equal(got, want) {
		t.Errorf("default skip = %q, want %q", got, want)
	}
	r, err := Load(writeRules(t, `skip = ["vendor", ".git"]`))
	if err != nil {
		t.Fatal(err)
	}
	want = []string{".cache", ".git", ".hg", ".jj", ".svn", "vendor"}
	if !slices.Equal(r.Skip, want) {
		t.Errorf("merged skip = %q, want %q", r.Skip, want)
	}
	for _, body := range []string{`skip = ["a/b"]`, `skip = [""]`, `names = [".git"]`, `names = [".jj"]`, `names = ["a/.git"]`, `names = [".svn/x"]`} {
		if _, err := Load(writeRules(t, body)); err == nil {
			t.Errorf("Load(%s) gave no error", body)
		}
	}
}

func TestGlobal(t *testing.T) {
	r, err := Load(writeRules(t, `
[[global]]
id = "uv"
tool = "uv"
path = "/fixed/uv"
clean = "uv cache clean"
kind = "clear"

[[global]]
id = "mine"
tool = "mytool"
path = "~/.mytool/cache"
clean = "mytool gc --all"
kind = "prune"
os = "darwin"
`))
	if err != nil {
		t.Fatal(err)
	}
	byID := map[string]Global{}
	for _, g := range r.Global {
		byID[g.ID] = g
	}
	if len(r.Global) != len(Default().Global)+1 {
		t.Errorf("%d global rules", len(r.Global))
	}
	if g := byID["uv"]; g.Path != "/fixed/uv" || g.Kind != KindClear {
		t.Errorf("uv = %+v", g)
	}
	if g := byID["mine"]; g.OS != "darwin" {
		t.Errorf("mine = %+v", g)
	}
	if g := byID["pnpm"]; g.Clean != "pnpm store prune" {
		t.Errorf("pnpm = %+v", g)
	}

	base := "tool = \"t\"\npath = \"t p\"\nclean = \"t c\"\nkind = \"prune\"\n"
	bad := map[string]string{
		"no id":             "[[global]]\n" + base,
		"bad kind":          "[[global]]\nid = \"a\"\ntool = \"t\"\npath = \"t p\"\nclean = \"t c\"\nkind = \"nuke\"\n",
		"other clean":       "[[global]]\nid = \"a\"\ntool = \"t\"\npath = \"t p\"\nclean = \"rm -rf /\"\nkind = \"prune\"\n",
		"other path":        "[[global]]\nid = \"a\"\ntool = \"t\"\npath = \"echo /x\"\nclean = \"t c\"\nkind = \"prune\"\n",
		"empty tool":        "[[global]]\nid = \"a\"\ntool = \"\"\npath = \"/x\"\nclean = \"t c\"\nkind = \"prune\"\n",
		"reserved id":       "[[global]]\nid = \"xcode-derived-data\"\n" + base,
		"unknown key":       "[[global]]\nid = \"a\"\nnope = 1\n" + base,
		"label, fixed path": "[[global]]\nid = \"a\"\ntool = \"t\"\npath = \"/x\"\npath_label = \"p\"\nclean = \"t c\"\nkind = \"prune\"\n",
		"duplicate id":      "[[global]]\nid = \"a\"\n" + base + "[[global]]\nid = \"a\"\n" + base,
	}
	for name, body := range bad {
		if _, err := Load(writeRules(t, body)); err == nil {
			t.Errorf("%s: no error", name)
		}
	}
}

func TestGlobalDeleteContentsValidation(t *testing.T) {
	const base = "[[global]]\nid = \"a\"\ntool = \"t\"\npath = \"~/c\"\ndelete_contents = true\n"
	cases := []struct {
		extra, want string
	}{
		{"kind = \"clear\"\n", ""},
		{"kind = \"clear\"\ncheck = \"t dry\"\n", ""},
		{"kind = \"prune\"\n", "delete_contents needs kind \"clear\""},
		{"kind = \"clear\"\nclean = \"t gc\"\n", "not both"},
		{"kind = \"clear\"\ntargets = \"t ls\"\n", "targets does not apply"},
	}
	for _, c := range cases {
		f, err := parse(base+c.extra, "test")
		if c.want == "" {
			if err != nil || !f.Global[0].DeleteContents {
				t.Errorf("%q: %+v, %v", c.extra, f.Global, err)
			}
			continue
		}
		if err == nil || !strings.Contains(err.Error(), c.want) {
			t.Errorf("%q: err = %v, want %q", c.extra, err, c.want)
		}
	}
	if _, err := parse("[[global]]\nid = \"a\"\ntool = \"t\"\npath = \"~/c\"\nkind = \"clear\"\n", "test"); err == nil {
		t.Error("a rule with neither clean nor delete_contents has no error")
	}
	labeled := "[[global]]\nid = \"a\"\ntool = \"t\"\npath = \"t where\"\npath_label = \"cache\"\ndelete_contents = true\nkind = \"clear\"\n"
	if f, err := parse(labeled, "test"); err != nil || f.Global[0].PathLabel != "cache" {
		t.Errorf("path_label with delete_contents: %+v, %v", f.Global, err)
	}
}

func TestGlobalPathSubdirValidation(t *testing.T) {
	const base = "[[global]]\nid = \"a\"\ntool = \"t\"\nclean = \"t gc\"\nkind = \"prune\"\n"
	cases := []struct {
		extra string
		ok    bool
	}{
		{"path = \"t env\"\npath_subdir = \"cache\"\n", true},
		{"path = \"t env\"\npath_subdir = \"a/cache\"\n", true},
		{"path = \"~/c\"\npath_subdir = \"cache\"\n", true},
		{"path = \"t env\"\npath_subdir = \"/cache\"\n", false},
		{"path = \"t env\"\npath_subdir = \"../cache\"\n", false},
		{"path = \"t env\"\npath_subdir = \"cache/\"\n", false},
		{"path = \"t env\"\npath_subdir = \".\"\n", false},
	}
	for _, c := range cases {
		_, err := parse(base+c.extra, "test")
		if (err == nil) != c.ok {
			t.Errorf("%q: err = %v", c.extra, err)
		}
	}
}

func TestGlobalPathZONFieldValidation(t *testing.T) {
	const base = "[[global]]\nid = \"a\"\ntool = \"t\"\nclean = \"t gc\"\nkind = \"prune\"\n"
	cases := []struct {
		extra, want string
	}{
		{"path = \"t env\"\npath_zon_field = \"global_cache_dir\"\n", ""},
		{"path = \"~/c\"\npath_zon_field = \"global_cache_dir\"\n", "needs a path command"},
		{"path = \"t env\"\npath_zon_field = \"env.HOME\"\n", "is not a field name"},
		{"path = \"t env\"\npath_zon_field = \"1st\"\n", "is not a field name"},
		{"path = \"t env\"\npath_zon_field = \"global_cache_dir\"\npath_label = \"cache\"\n", "set only one of path_label, path_zon_field"},
	}
	for _, c := range cases {
		f, err := parse(base+c.extra, "test")
		if c.want == "" {
			if err != nil || f.Global[0].PathZONField != "global_cache_dir" {
				t.Errorf("%q: %+v, %v", c.extra, f.Global, err)
			}
			continue
		}
		if err == nil || !strings.Contains(err.Error(), c.want) {
			t.Errorf("%q: err = %v, want %q", c.extra, err, c.want)
		}
	}
}

func TestGlobalCheckValidation(t *testing.T) {
	const base = "[[global]]\nid = \"a\"\ntool = \"t\"\npath = \"~/c\"\nclean = \"t gc\"\nkind = \"prune\"\n"
	cases := []struct {
		extra, want string
	}{
		{"check = \"other run\"\n", "check must be a t command"},
		{"refuse = \"x\"\n", "need check"},
		{"check_env = [\"A=1\"]\n", "need check"},
		{"check = \"t dry\"\nrefuse_reason = \"why\"\n", "refuse_reason needs refuse"},
		{"check = \"t dry\"\nrefuse = \"(\"\n", "refuse:"},
		{"require = \"x\"\n", "need check"},
		{"check = \"t dry\"\nrequire = \"(\"\n", "require:"},
		{"check = \"t dry\"\nrequire = '^1\\.'\n", ""},
		{"[[global.checks]]\ncheck = \"t more\"\n", "need check"},
		{"check = \"t dry\"\n[[global.checks]]\ncheck = \"t more\"\nrequire = '^on$'\n", ""},
		{"check = \"t dry\"\n[[global.checks]]\ncheck = \"other more\"\n", "check must be a t command"},
		{"check = \"t dry\"\n[[global.checks]]\nrequire = 'x'\n", "check must be a t command"},
		{"check = \"t dry\"\n[[global.checks]]\ncheck = \"t more\"\nrequire = \"(\"\n", "require:"},
		{"check = \"t dry\"\n[[global.checks]]\ncheck = \"t more\"\nnope = 1\n", "unknown keys"},
		{"check = \"t dry\"\ncheck_env = [\"NOVALUE\"]\n", "must be NAME=value"},
		{"check = \"t dry\"\ncheck_env = [\"=x\"]\n", "must be NAME=value"},
		{"path_label = \"x\"\n", "path_label needs a path command"},
		{"check = \"t dry\"\nrefuse = 'bad (?P<path>.+)$'\nrefuse_reason = \"why\"\ncheck_env = [\"A=1\"]\n", ""},
	}
	for _, c := range cases {
		_, err := parse(base+c.extra, "test")
		if c.want == "" {
			if err != nil {
				t.Errorf("%q: %v", c.extra, err)
			}
			continue
		}
		if err == nil || !strings.Contains(err.Error(), c.want) {
			t.Errorf("%q: err = %v, want %q", c.extra, err, c.want)
		}
	}
}

func TestGlobalPathFieldValidation(t *testing.T) {
	const rule = "[[global]]\nid = \"a\"\ntool = \"t\"\nclean = \"t c\"\nkind = \"prune\"\npath_field = \"dir\"\n"
	f, err := parse(rule+"path = \"t info --json\"\n", "test")
	if err != nil || f.Global[0].PathField != "dir" {
		t.Errorf("path command: %+v, %v", f.Global, err)
	}
	for _, path := range []string{"~/c", "/c"} {
		_, err := parse(rule+"path = \""+path+"\"\n", "test")
		if err == nil || !strings.Contains(err.Error(), "path_field needs a path command") {
			t.Errorf("%s: err = %v", path, err)
		}
	}
}

func TestGlobalPathKeysAreExclusive(t *testing.T) {
	const rule = "[[global]]\nid = \"a\"\ntool = \"t\"\npath = \"t info\"\nclean = \"t c\"\nkind = \"prune\"\n"
	cases := []struct {
		extra, want string
	}{
		{"", ""},
		{"path_field = \"dir\"\n", ""},
		{"path_label = \"cache\"\n", ""},
		{"path_field = \"dir\"\npath_label = \"cache\"\n", "set only one of path_label, path_field"},
		{"path_zon_field = \"dir\"\n", ""},
		{"path_field = \"dir\"\npath_zon_field = \"dir\"\n", "set only one of path_field, path_zon_field"},
		{"path_label = \"cache\"\npath_field = \"dir\"\npath_zon_field = \"dir\"\n", "set only one of path_label, path_field, path_zon_field"},
		{"path_label = \"a\\nb\"\n", "path_label must be one line"},
	}
	for _, c := range cases {
		_, err := parse(rule+c.extra, "test")
		if c.want == "" {
			if err != nil {
				t.Errorf("%q: %v", c.extra, err)
			}
			continue
		}
		if err == nil || !strings.Contains(err.Error(), c.want) {
			t.Errorf("%q: err = %v, want %q", c.extra, err, c.want)
		}
	}
}

func TestGlobalPathEnvValidation(t *testing.T) {
	const base = "[[global]]\nid = \"a\"\ntool = \"t\"\ndelete_contents = true\nkind = \"clear\"\n"
	cases := []struct {
		extra, want string
	}{
		{"path = \"~/.t/c\"\npath_env = \"T_HOME\"\npath_subdir = \"c\"\n", ""},
		{"path = \"/t/c\"\npath_env = \"T_HOME\"\n", ""},
		{"path = \"~/.t/c\"\npath_env = \"T_HOME\"\npath_subdir = \"c/d\"\n", ""},
		{"path = \"t where\"\npath_env = \"T_HOME\"\n", "path_env needs a fixed path"},
		{"path = \"~/.t\"\npath_subdir = \"c\"\n", ""},
		{"path = \"~/.t/c\"\npath_env = \"T-HOME\"\n", "is not an environment variable name"},
		{"path = \"~/.t/c\"\npath_env = \"1HOME\"\n", "is not an environment variable name"},
		{"path = \"~/.t/c\"\npath_env = \"T_HOME\"\npath_subdir = \"../c\"\n", "must be a relative path of directory names inside"},
		{"path = \"~/.t/c\"\npath_env = \"T_HOME\"\npath_subdir = \"/c\"\n", "must be a relative path of directory names inside"},
		{"path = \"~/.t/c\"\npath_env = \"T_HOME\"\npath_subdir = \"c/..\"\n", "must be a relative path of directory names inside"},
	}
	for _, c := range cases {
		_, err := parse(base+c.extra, "test")
		if c.want == "" {
			if err != nil {
				t.Errorf("%q: %v", c.extra, err)
			}
			continue
		}
		if err == nil || !strings.Contains(err.Error(), c.want) {
			t.Errorf("%q: err = %v, want %q", c.extra, err, c.want)
		}
	}
}

// TestCargoRulesKeepCargoHomeState checks that the Cargo rules delete only
// the registry cache, never the Git cache or the Cargo home itself.
func TestCargoRulesKeepCargoHomeState(t *testing.T) {
	want := map[string]string{"cargo-registry": "registry"}
	for _, g := range Default().Global {
		// Cargo's Git checkouts are working trees that a user can edit.
		if g.ID == "cargo-git" {
			t.Errorf("default rules include cargo-git")
		}
		sub, ok := want[g.ID]
		if !ok {
			continue
		}
		delete(want, g.ID)
		if g.PathEnv != "CARGO_HOME" || g.PathSubdir != sub || g.Path != "~/.cargo" ||
			!g.DeleteContents || g.Kind != KindClear || g.Tool != "cargo" {
			t.Errorf("%s = %+v", g.ID, g)
		}
	}
	if len(want) > 0 {
		t.Errorf("missing rules: %v", want)
	}
}
