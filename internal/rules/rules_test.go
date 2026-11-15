package rules

import (
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
)

func TestDefault(t *testing.T) {
	r := Default()
	if len(r.Names) != 19 || r.QuietHours != 24 {
		t.Errorf("got %d names and %g quiet hours", len(r.Names), r.QuietHours)
	}
	if !slices.Equal(r.Beside["target"], []string{"Cargo.toml", "pom.xml"}) {
		t.Errorf("beside target = %q", r.Beside["target"])
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
		{name: "name with slash", body: `names = ["a/b"]`, wantErr: true},
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
	if !slices.Equal(Default().Beside["dist"], []string{"package.json"}) {
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
		{"target", []string{"pom.xml"}, "target beside pom.xml"},
		{"target", []string{"package.json"}, ""},
		{"src", []string{"package.json"}, ""},
		{"bin", nil, ""},
		{"obj", []string{"package.json"}, ""},
	}
	for _, tt := range tests {
		got := r.Match(tt.name, func(m string) bool { return slices.Contains(tt.siblings, m) })
		if got != tt.want {
			t.Errorf("Match(%q, %q) = %q, want %q", tt.name, tt.siblings, got, tt.want)
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
	for _, body := range []string{`skip = ["a/b"]`, `skip = [""]`, `names = [".git"]`, `names = [".jj"]`} {
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
