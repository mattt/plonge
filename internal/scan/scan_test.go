package scan

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/mattt/plonge/internal/rules"
)

// isolateGit keeps the user's Git configuration out of the tests.
func isolateGit(t *testing.T) {
	t.Helper()
	t.Setenv("GIT_CONFIG_GLOBAL", os.DevNull)
	t.Setenv("GIT_CONFIG_NOSYSTEM", "1")
	t.Setenv("GIT_AUTHOR_NAME", "Test")
	t.Setenv("GIT_AUTHOR_EMAIL", "test@example.com")
	t.Setenv("GIT_COMMITTER_NAME", "Test")
	t.Setenv("GIT_COMMITTER_EMAIL", "test@example.com")
}

func run(t *testing.T, dir string, args ...string) {
	t.Helper()
	cmd := exec.Command("git", args...)
	cmd.Dir = dir
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("git %s: %v\n%s", strings.Join(args, " "), err, out)
	}
}

func write(t *testing.T, path, body string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
}

// newRepo makes a Git repository with the given files and commits them.
// Files whose names end in "!" are force-added despite .gitignore.
func newRepo(t *testing.T, dir string, files map[string]string) {
	t.Helper()
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	run(t, dir, "init", "-q")
	var forced []string
	for name, body := range files {
		if n, ok := strings.CutSuffix(name, "!"); ok {
			name = n
			forced = append(forced, name)
		}
		write(t, filepath.Join(dir, name), body)
	}
	run(t, dir, "add", ".")
	if len(forced) > 0 {
		run(t, dir, append([]string{"add", "-f", "--"}, forced...)...)
	}
	run(t, dir, "commit", "-q", "--allow-empty", "-m", "init")
}

func found(root Root) map[string]string {
	m := map[string]string{}
	for _, c := range root.Candidates {
		rel, _ := filepath.Rel(root.Path, c.Path)
		m[filepath.ToSlash(rel)] = c.Rule
	}
	return m
}

func TestDetect(t *testing.T) {
	isolateGit(t)
	tests := []struct {
		name  string
		files map[string]string
		want  map[string]string
	}{
		{
			name: "ignored, tracked, and not ignored",
			files: map[string]string{
				".gitignore":               "node_modules/\n.venv/\n__pycache__/\n",
				"node_modules/a/index.js":  "",
				".venv/bin/python":         "",
				"pkg/__pycache__/m.pyc!":   "", // ignored but tracked
				".turbo/cache":             "", // not ignored
				"sub/node_modules/b/x.js":  "",
				"sub/.mypy_cache/x":        "", // not ignored
				"node_modules/a/.next/obj": "", // inside a candidate
			},
			want: map[string]string{
				"node_modules":     "node_modules",
				".venv":            ".venv",
				"sub/node_modules": "node_modules",
			},
		},
		{
			name: "beside markers",
			files: map[string]string{
				".gitignore":         "dist/\ntarget/\nvenv/\n",
				"web/package.json":   "{}",
				"web/dist/app.js":    "",
				"docs/dist/x.html":   "",
				"rs/Cargo.toml":      "",
				"rs/target/debug/x":  "",
				"java/pom.xml":       "",
				"java/target/c.jar":  "",
				"other/target/x":     "",
				"py/setup.py":        "",
				"py/venv/bin/python": "",
			},
			want: map[string]string{
				"web/dist":    "dist beside package.json",
				"rs/target":   "target beside Cargo.toml",
				"java/target": "target beside pom.xml",
				"py/venv":     "venv beside setup.py",
			},
		},
		{
			name: "ignored by a parent directory rule",
			files: map[string]string{
				".gitignore":            "build/\n",
				"build/x/.gradle/cache": "",
			},
			want: map[string]string{"build/x/.gradle": ".gradle"},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			root := filepath.Join(t.TempDir(), "repo")
			newRepo(t, root, tt.files)
			got := Detect(context.Background(), root, rules.Default())
			if got.Skipped != "" || len(got.Errors) > 0 {
				t.Fatalf("skipped %q, errors %q", got.Skipped, got.Errors)
			}
			if f := found(got); !mapsEqual(f, tt.want) {
				t.Errorf("found %v, want %v", f, tt.want)
			}
		})
	}
}

func mapsEqual(a, b map[string]string) bool {
	if len(a) != len(b) {
		return false
	}
	for k, v := range a {
		if w, ok := b[k]; !ok || w != v {
			return false
		}
	}
	return true
}

func TestDetectNestedRepository(t *testing.T) {
	isolateGit(t)
	root := filepath.Join(t.TempDir(), "outer")
	newRepo(t, root, map[string]string{".gitignore": "/vendor/\n"})
	// The inner repository ignores node_modules; the outer one does not.
	inner := filepath.Join(root, "vendor", "lib")
	newRepo(t, inner, map[string]string{".gitignore": "node_modules/\n"})
	write(t, filepath.Join(inner, "node_modules", "x.js"), "")
	write(t, filepath.Join(root, "node_modules", "y.js"), "")

	got := found(Detect(context.Background(), root, rules.Default()))
	want := map[string]string{"vendor/lib/node_modules": "node_modules"}
	if !mapsEqual(got, want) {
		t.Errorf("found %v, want %v", got, want)
	}
}

func TestDetectDoesNotFollowSymlinks(t *testing.T) {
	isolateGit(t)
	tmp := t.TempDir()
	elsewhere := filepath.Join(tmp, "elsewhere")
	write(t, filepath.Join(elsewhere, "node_modules", "x.js"), "")
	root := filepath.Join(tmp, "repo")
	newRepo(t, root, map[string]string{".gitignore": "node_modules\nlink\n"})
	if err := os.Symlink(elsewhere, filepath.Join(root, "link")); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(filepath.Join(elsewhere, "node_modules"), filepath.Join(root, "node_modules")); err != nil {
		t.Fatal(err)
	}
	if got := found(Detect(context.Background(), root, rules.Default())); len(got) != 0 {
		t.Errorf("found %v through symlinks", got)
	}
}

func TestDetectSkipsRoots(t *testing.T) {
	isolateGit(t)
	tmp := t.TempDir()
	plain := filepath.Join(tmp, "plain")
	write(t, filepath.Join(plain, "node_modules", "x.js"), "")
	t.Setenv("GIT_CEILING_DIRECTORIES", tmp)
	tests := []struct {
		root string
		want string
	}{
		{plain, "not in a Git work tree"},
		{filepath.Join(tmp, "missing"), "not a directory"},
	}
	for _, tt := range tests {
		got := Detect(context.Background(), tt.root, rules.Default())
		if got.Skipped != tt.want || len(got.Candidates) != 0 {
			t.Errorf("Detect(%q) skipped %q with %d candidates, want %q", tt.root, got.Skipped, len(got.Candidates), tt.want)
		}
	}
}

func TestLastActivity(t *testing.T) {
	isolateGit(t)
	root := filepath.Join(t.TempDir(), "repo")
	newRepo(t, root, map[string]string{"a": ""})
	logs := filepath.Join(root, ".git", "logs", "HEAD")
	head := filepath.Join(root, ".git", "HEAD")
	index := filepath.Join(root, ".git", "index")
	old := time.Now().Add(-72 * time.Hour).Truncate(time.Second)
	older := old.Add(-24 * time.Hour)
	if err := os.Chtimes(logs, old, old); err != nil {
		t.Fatal(err)
	}
	if err := os.Chtimes(head, older, older); err != nil {
		t.Fatal(err)
	}
	// A fresh index, as git status leaves, must not count as activity.
	now := time.Now()
	if err := os.Chtimes(index, now, now); err != nil {
		t.Fatal(err)
	}
	if got := Detect(context.Background(), root, rules.Default()).LastActivity; !got.Equal(old) {
		t.Errorf("with a HEAD log, last activity = %v, want %v", got, old)
	}
	if err := os.Remove(logs); err != nil {
		t.Fatal(err)
	}
	if got := Detect(context.Background(), root, rules.Default()).LastActivity; !got.Equal(older) {
		t.Errorf("without a HEAD log, last activity = %v, want %v", got, older)
	}
}

func TestQuiet(t *testing.T) {
	now := time.Date(2026, 1, 2, 12, 0, 0, 0, time.UTC)
	tests := []struct {
		name  string
		last  time.Time
		hours float64
		want  bool
	}{
		{"unknown", time.Time{}, 24, false},
		{"recent", now.Add(-23 * time.Hour), 24, false},
		{"exactly the limit", now.Add(-24 * time.Hour), 24, true},
		{"old", now.Add(-48 * time.Hour), 24, true},
		{"zero hours", now, 0, true},
	}
	for _, tt := range tests {
		if got := Quiet(tt.last, now, tt.hours); got != tt.want {
			t.Errorf("%s: Quiet = %v, want %v", tt.name, got, tt.want)
		}
	}
}

func TestDiskUsage(t *testing.T) {
	tmp := t.TempDir()
	one := filepath.Join(tmp, "one")
	two := filepath.Join(tmp, "two")
	body := strings.Repeat("x", 100_000)
	write(t, filepath.Join(one, "f"), body)
	write(t, filepath.Join(two, "f"), body)
	// A hard link counts once; a symlink is not followed.
	if err := os.Link(filepath.Join(two, "f"), filepath.Join(two, "g")); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(one, filepath.Join(two, "s")); err != nil {
		t.Fatal(err)
	}
	a, b := DiskUsage(one), DiskUsage(two)
	if a < 100_000 {
		t.Errorf("DiskUsage(one) = %d, want at least the file size", a)
	}
	if b > a+64*1024 {
		t.Errorf("DiskUsage(two) = %d counts the hard link or symlink target; one is %d", b, a)
	}
	if got := Sizes([]string{one, two}); !slices.Equal(got, []int64{a, b}) {
		t.Errorf("Sizes = %v, want [%d %d]", got, a, b)
	}
}
