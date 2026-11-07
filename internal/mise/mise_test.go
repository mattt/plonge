package mise

import (
	"os"
	"path/filepath"
	"slices"
	"testing"
)

func TestProjectRoot(t *testing.T) {
	tests := []struct {
		file string
		want string
	}{
		{"/p/mise.toml", "/p"},
		{"/p/.mise.toml", "/p"},
		{"/p/mise.local.toml", "/p"},
		{"/p/.tool-versions", "/p"},
		{"/p/.config/mise.toml", "/p"},
		{"/p/.config/mise/config.toml", "/p"},
		{"/p/.config/mise/conf.d/node.toml", "/p"},
		{"/p/mise/config.toml", "/p"},
		{"/p/mise/config.local.toml", "/p"},
		{"/p/.mise/config.toml", "/p"},
		// A project named mise keeps its own root.
		{"/code/mise/mise.toml", "/code/mise"},
		// A file in .config that is not a mise file stays where it is.
		{"/p/.config/other.toml", "/p/.config"},
	}
	for _, tt := range tests {
		if got := ProjectRoot(tt.file); got != tt.want {
			t.Errorf("ProjectRoot(%q) = %q, want %q", tt.file, got, tt.want)
		}
	}
}

func TestDropNested(t *testing.T) {
	tests := []struct {
		name  string
		roots []string
		want  []string
	}{
		{"empty", nil, nil},
		{"duplicates", []string{"/a", "/a/"}, []string{"/a"}},
		{"nested", []string{"/a/b", "/a", "/a/b/c"}, []string{"/a"}},
		{"shared prefix is not nesting", []string{"/a", "/ab", "/a b"}, []string{"/a", "/a b", "/ab"}},
		{"nested after sibling", []string{"/a", "/a b", "/a/c"}, []string{"/a", "/a b"}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := DropNested(tt.roots); !slices.Equal(got, tt.want) {
				t.Errorf("DropNested(%q) = %q, want %q", tt.roots, got, tt.want)
			}
		})
	}
}

func TestTrackedConfigsDir(t *testing.T) {
	tests := []struct {
		name string
		env  Env
		want string
	}{
		{"default", Env{Home: "/h"}, "/h/.local/state/mise/tracked-configs"},
		{"xdg", Env{Home: "/h", XDGStateHome: "/x"}, "/x/mise/tracked-configs"},
		{"mise", Env{Home: "/h", XDGStateHome: "/x", MiseStateDir: "/m"}, "/m/tracked-configs"},
	}
	for _, tt := range tests {
		if got := tt.env.TrackedConfigsDir(); got != tt.want {
			t.Errorf("%s: got %q, want %q", tt.name, got, tt.want)
		}
	}
}

func touch(t *testing.T, path string) string {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, nil, 0o644); err != nil {
		t.Fatal(err)
	}
	return path
}

func TestReadProjects(t *testing.T) {
	tmp := t.TempDir()
	home := filepath.Join(tmp, "home")
	env := Env{Home: home, MiseStateDir: filepath.Join(tmp, "state"), MiseSystemConfDir: filepath.Join(tmp, "etc")}
	dir := env.TrackedConfigsDir()
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	links := map[string]string{
		"a":        touch(t, filepath.Join(home, "code", "a", "mise.toml")),
		"a-nested": touch(t, filepath.Join(home, "code", "a", "sub", "mise.toml")),
		"b":        touch(t, filepath.Join(home, "code", "b", ".config", "mise", "config.toml")),
		"b-again":  touch(t, filepath.Join(home, "code", "b", ".mise.toml")),
		"global":   touch(t, filepath.Join(home, ".config", "mise", "config.toml")),
		"home":     touch(t, filepath.Join(home, ".tool-versions")),
		"system":   touch(t, filepath.Join(tmp, "etc", "config.toml")),
		"missing":  filepath.Join(home, "code", "gone", "mise.toml"),
		"dir":      filepath.Join(home, "code"),
	}
	for name, target := range links {
		if err := os.Symlink(target, filepath.Join(dir, name)); err != nil {
			t.Fatal(err)
		}
	}
	// A relative link resolves from the tracked-configs directory.
	rel, err := filepath.Rel(dir, touch(t, filepath.Join(tmp, "c", "mise.toml")))
	if err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(rel, filepath.Join(dir, "relative")); err != nil {
		t.Fatal(err)
	}
	// A plain file is not a link and is ignored.
	touch(t, filepath.Join(dir, "plain"))

	got, err := ReadProjects(env)
	if err != nil {
		t.Fatal(err)
	}
	want := []string{
		filepath.Join(tmp, "c"),
		filepath.Join(home, "code", "a"),
		filepath.Join(home, "code", "b"),
	}
	slices.Sort(want)
	if !slices.Equal(got.Roots, want) {
		t.Errorf("roots = %q, want %q", got.Roots, want)
	}
	if got.BrokenLinks != 2 {
		t.Errorf("broken links = %d, want 2", got.BrokenLinks)
	}
}

func TestReadProjectsMissingDir(t *testing.T) {
	got, err := ReadProjects(Env{MiseStateDir: filepath.Join(t.TempDir(), "none")})
	if err != nil || len(got.Roots) != 0 || got.BrokenLinks != 0 {
		t.Errorf("got %+v, %v; want nothing", got, err)
	}
}
