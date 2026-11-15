package scan

import (
	"context"
	"fmt"
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
			name: ".NET project output",
			files: map[string]string{
				".gitignore":              "bin/\nobj/\n",
				"App/App.csproj":          "",
				"App/bin/Debug/App.dll":   "",
				"App/obj/project.assets":  "",
				"Lib/Lib.fsproj":          "",
				"Lib/obj/x":               "",
				"Vb/Vb.vbproj":            "",
				"Vb/bin/x":                "",
				"tools/bin/run.sh":        "", // no project file
				"App.sln":                 "",
				"bin/x":                   "", // beside a solution only
				"App/bin/Debug/sub/obj/x": "", // inside a candidate
			},
			want: map[string]string{
				"App/bin": "bin beside *.csproj",
				"App/obj": "obj beside *.csproj",
				"Lib/obj": "obj beside *.fsproj",
				"Vb/bin":  "bin beside *.vbproj",
			},
		},
		{
			name: "Python test environments",
			files: map[string]string{
				".gitignore":               ".tox/\n.nox/\n",
				"tox.ini":                  "",
				".tox/py313/bin/python":    "",
				".tox/.pkg/x":              "",
				"noxfile.py":               "",
				".nox/tests/bin/python":    "",
				"sub/.nox/lint/bin/python": "", // nox has no fixed project file
				".tox/py313/lib/.nox/x":    "", // inside a candidate
			},
			want: map[string]string{
				".tox":     ".tox",
				".nox":     ".nox",
				"sub/.nox": ".nox",
			},
		},
		{
			name: "Python build and dist output",
			files: map[string]string{
				".gitignore":                          "build/\ndist/\n__pycache__/\n",
				"lib/dist/pkg-1.0-py3-none-any.whl":   "",
				"old/dist/pkg-1.0.tar.gz":             "",
				"docs/dist/x":                         "", // no project file
				"lib/pyproject.toml":                  "",
				"lib/build/lib/pkg/m.py":              "",
				"lib/build/lib/pkg/__pycache__/m.pyc": "", // inside a candidate
				"old/setup.py":                        "",
				"old/build/temp/x.o":                  "",
				"web/package.json":                    "{}",
				"web/build/index.html":                "", // not a Python project
				"app/build.gradle":                    "",
				"app/build/classes/x":                 "", // not a Python project
				"docs/build/html/index.html":          "", // no project file
				"src/pkg/__pycache__/m.pyc":           "",
			},
			want: map[string]string{
				"lib/build":           "build beside pyproject.toml",
				"old/build":           "build beside setup.py",
				"lib/dist":            "dist beside pyproject.toml",
				"old/dist":            "dist beside setup.py",
				"src/pkg/__pycache__": "__pycache__",
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

func TestDetectBesidePattern(t *testing.T) {
	isolateGit(t)
	r := rules.Default()
	r.Names = append(r.Names, "out")
	r.Beside["out"] = []string{"*.proj"}
	root := filepath.Join(t.TempDir(), "repo")
	newRepo(t, root, map[string]string{
		".gitignore":      "out/\n",
		"app/App.proj":    "",
		"app/out/x":       "",
		"lib/proj":        "", // does not match *.proj
		"lib/out/x":       "",
		"dir/sub.proj/x":  "", // a directory also matches, as a literal name does
		"dir/out/x":       "",
		"none/out/x":      "",
		"none/x.proj.bak": "",
	})
	got := Detect(context.Background(), root, r)
	if got.Skipped != "" || len(got.Errors) > 0 {
		t.Fatalf("skipped %q, errors %q", got.Skipped, got.Errors)
	}
	want := map[string]string{
		"app/out": "out beside *.proj",
		"dir/out": "out beside *.proj",
	}
	if f := found(got); !mapsEqual(f, want) {
		t.Errorf("found %v, want %v", f, want)
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

func TestDetectSkipList(t *testing.T) {
	isolateGit(t)
	root := filepath.Join(t.TempDir(), "repo")
	newRepo(t, root, map[string]string{
		".gitignore":                          ".cache/\n__pycache__/\nvendor/\n",
		".cache/uv/env/lib/__pycache__/m.pyc": "",
		"vendor/lib/__pycache__/m.pyc":        "",
		"src/__pycache__/m.pyc":               "",
	})
	r := rules.Default()
	r.Skip = append(r.Skip, "vendor")
	got := found(Detect(context.Background(), root, r))
	want := map[string]string{"src/__pycache__": "__pycache__"}
	if !mapsEqual(got, want) {
		t.Errorf("found %v, want %v", got, want)
	}

	// A name that is both a rule and in the skip list is a cache.
	r.Names = append(r.Names, ".cache")
	got = found(Detect(context.Background(), root, r))
	want[".cache"] = ".cache"
	if !mapsEqual(got, want) {
		t.Errorf("with a .cache rule, found %v, want %v", got, want)
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

func TestInRepository(t *testing.T) {
	isolateGit(t)
	tmp := t.TempDir()
	repo := filepath.Join(tmp, "repo")
	newRepo(t, repo, map[string]string{".gitignore": "cache/\n"})
	outside := filepath.Join(tmp, "outside")
	for _, dir := range []string{filepath.Join(repo, "cache", "x"), outside} {
		if err := os.MkdirAll(dir, 0o755); err != nil {
			t.Fatal(err)
		}
	}
	ctx := context.Background()
	for dir, want := range map[string]bool{
		repo:                              true,
		filepath.Join(repo, "cache", "x"): true,
		filepath.Join(repo, ".git"):       true,
		outside:                           false,
	} {
		got, err := InRepository(ctx, dir)
		if err != nil || got != want {
			t.Errorf("InRepository(%s) = %v, %v; want %v", dir, got, err, want)
		}
	}
	if _, err := InRepository(ctx, filepath.Join(tmp, "missing")); err == nil {
		t.Error("no error for a missing directory")
	}
}

func TestInRepositoryIgnoresInheritedDiscoveryLimits(t *testing.T) {
	isolateGit(t)
	repo := filepath.Join(t.TempDir(), "repo")
	newRepo(t, repo, map[string]string{"cache/tracked": "x"})
	t.Setenv("GIT_CEILING_DIRECTORIES", repo)
	t.Setenv("GIT_DISCOVERY_ACROSS_FILESYSTEM", "0")
	got, err := InRepository(context.Background(), filepath.Join(repo, "cache"))
	if err != nil || !got {
		t.Errorf("InRepository = %v, %v; want true", got, err)
	}
}

func TestInRepositoryRefusesBrokenMetadata(t *testing.T) {
	isolateGit(t)
	tmp := t.TempDir()
	brokenFile := filepath.Join(tmp, "file")
	write(t, filepath.Join(brokenFile, ".git"), "gitdir: "+filepath.Join(tmp, "missing")+"\n")
	brokenDir := filepath.Join(tmp, "dir")
	if err := os.MkdirAll(filepath.Join(brokenDir, ".git"), 0o755); err != nil {
		t.Fatal(err)
	}
	for _, root := range []string{brokenFile, brokenDir} {
		sub := filepath.Join(root, "cache")
		if err := os.MkdirAll(sub, 0o755); err != nil {
			t.Fatal(err)
		}
		if got, err := InRepository(context.Background(), sub); err == nil {
			t.Errorf("InRepository(%s) = %v with no error", sub, got)
		}
	}
}

func TestInRepositoryRefusesUnexpectedOutput(t *testing.T) {
	dir := t.TempDir()
	bin := t.TempDir()
	t.Setenv("PATH", bin)
	for _, out := range []string{"", "true", "maybe\nfalse", "false\nfalse\nfalse", "FALSE\nfalse"} {
		script := fmt.Sprintf("#!/bin/sh\nprintf '%%s\\n' %q\n", out)
		if err := os.WriteFile(filepath.Join(bin, "git"), []byte(script), 0o755); err != nil {
			t.Fatal(err)
		}
		if got, err := InRepository(context.Background(), dir); err == nil {
			t.Errorf("output %q: InRepository = %v with no error", out, got)
		}
	}
	if err := os.WriteFile(filepath.Join(bin, "git"), []byte("#!/bin/sh\nprintf 'false\\nfalse\\n'\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	if got, err := InRepository(context.Background(), dir); err != nil || got {
		t.Errorf("false, false: InRepository = %v, %v", got, err)
	}
}

func TestMeasureRefusesGitRepositories(t *testing.T) {
	isolateGit(t)
	tests := []struct {
		name   string
		setup  func(t *testing.T, root string) // adds a repository to a cache
		reason string
	}{
		{
			name: "nested repository with a commit in build",
			setup: func(t *testing.T, root string) {
				newRepo(t, filepath.Join(root, "build", "dep"), map[string]string{"work.txt": "keep"})
			},
			reason: "contains a Git repository at dep",
		},
		{
			name: "cache that is a work tree",
			setup: func(t *testing.T, root string) {
				newRepo(t, filepath.Join(root, "build"), map[string]string{"work.txt": "keep"})
			},
			reason: "is a Git repository",
		},
		{
			name: "worktree-style .git file",
			setup: func(t *testing.T, root string) {
				write(t, filepath.Join(root, "node_modules", "pkg", ".git"), "gitdir: /elsewhere/.git/worktrees/pkg\n")
			},
			reason: "contains a Git repository at pkg",
		},
		{
			name: "dangling .git symlink",
			setup: func(t *testing.T, root string) {
				dir := filepath.Join(root, "node_modules", "a", "b")
				if err := os.MkdirAll(dir, 0o755); err != nil {
					t.Fatal(err)
				}
				if err := os.Symlink("/nonexistent", filepath.Join(dir, ".git")); err != nil {
					t.Fatal(err)
				}
			},
			reason: "contains a Git repository at a/b",
		},
		{
			name: "bare repository",
			setup: func(t *testing.T, root string) {
				run(t, root, "init", "-q", "--bare", filepath.Join(root, "node_modules", "mirror.git"))
			},
			reason: "contains a Git directory at mirror.git",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			root := filepath.Join(t.TempDir(), "repo")
			newRepo(t, root, map[string]string{
				".gitignore":     "build/\nnode_modules/\n.venv/\n",
				"pyproject.toml": "",
			})
			write(t, filepath.Join(root, ".venv", "bin", "python"), "")
			write(t, filepath.Join(root, "build", "lib", "m.py"), "")
			write(t, filepath.Join(root, "node_modules", "x", "index.js"), "")
			tt.setup(t, root)

			roots := []Root{Detect(context.Background(), root, rules.Default())}
			sizes := Measure(roots)
			got := roots[0]
			if len(got.Refused) != 1 {
				t.Fatalf("refused %+v, want one", got.Refused)
			}
			r := got.Refused[0]
			if r.Reason != tt.reason || r.Project != root {
				t.Errorf("refusal %+v, want reason %q", r, tt.reason)
			}
			// The other caches stay, each with its size.
			if len(got.Candidates) != 2 || len(sizes[0]) != 2 {
				t.Fatalf("candidates %+v, sizes %v", got.Candidates, sizes)
			}
			for _, c := range got.Candidates {
				if c.Path == r.Path {
					t.Errorf("%s is both refused and a candidate", c.Path)
				}
			}
		})
	}
}

func TestMeasureRefusesUnreadableDirectory(t *testing.T) {
	isolateGit(t)
	if os.Geteuid() == 0 {
		t.Skip("root can read every directory")
	}
	root := filepath.Join(t.TempDir(), "repo")
	newRepo(t, root, map[string]string{".gitignore": "node_modules/\n"})
	locked := filepath.Join(root, "node_modules", "locked")
	write(t, filepath.Join(locked, "x.js"), "")
	if err := os.Chmod(locked, 0); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Chmod(locked, 0o755) })

	roots := []Root{Detect(context.Background(), root, rules.Default())}
	Measure(roots)
	if len(roots[0].Refused) != 1 || !strings.Contains(roots[0].Refused[0].Reason, "cannot check for Git repositories") {
		t.Errorf("refused %+v", roots[0].Refused)
	}
}

func TestMeasureRefusesInsidePaths(t *testing.T) {
	isolateGit(t)
	tests := []struct {
		name   string
		setup  func(t *testing.T, cache string)
		reason string // empty when the cache stays a candidate
	}{
		{
			name:   "matching directory",
			setup:  func(t *testing.T, cache string) { write(t, filepath.Join(cache, "prod", "rel", "app", "COOKIE"), "x") },
			reason: `contains prod/rel, which refuse_inside "*/rel" protects`,
		},
		{
			name:   "matching file",
			setup:  func(t *testing.T, cache string) { write(t, filepath.Join(cache, "dev", "rel"), "") },
			reason: `contains dev/rel, which refuse_inside "*/rel" protects`,
		},
		{
			name:  "no match at that depth",
			setup: func(t *testing.T, cache string) { write(t, filepath.Join(cache, "rel", "x"), "") },
		},
		{
			name: "symlinked directory is not followed",
			setup: func(t *testing.T, cache string) {
				outside := t.TempDir()
				write(t, filepath.Join(outside, "rel", "x"), "")
				write(t, filepath.Join(cache, "dev", "lib", "x"), "")
				if err := os.Symlink(outside, filepath.Join(cache, "linked")); err != nil {
					t.Fatal(err)
				}
			},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			r := rules.Default()
			r.Names = append(r.Names, "out")
			r.RefuseInside["out"] = []string{"*/rel"}
			root := filepath.Join(t.TempDir(), "repo")
			newRepo(t, root, map[string]string{".gitignore": "out/\nnode_modules/\n"})
			write(t, filepath.Join(root, "node_modules", "dev", "rel"), "") // another rule
			cache := filepath.Join(root, "out")
			write(t, filepath.Join(cache, "dev", "lib", "app.beam"), "")
			tt.setup(t, cache)

			roots := []Root{Detect(context.Background(), root, r)}
			Measure(roots)
			got := roots[0]
			if tt.reason == "" {
				if len(got.Refused) != 0 || len(got.Candidates) != 2 {
					t.Fatalf("refused %+v, candidates %+v", got.Refused, got.Candidates)
				}
				return
			}
			if len(got.Refused) != 1 || got.Refused[0].Path != cache || got.Refused[0].Reason != tt.reason {
				t.Fatalf("refused %+v, want %s: %s", got.Refused, cache, tt.reason)
			}
			if len(got.Candidates) != 1 || got.Candidates[0].Path != filepath.Join(root, "node_modules") {
				t.Errorf("candidates %+v, want only node_modules", got.Candidates)
			}
		})
	}
}

func TestMeasureRefusesUnreadableInsidePath(t *testing.T) {
	isolateGit(t)
	if os.Geteuid() == 0 {
		t.Skip("root can read every directory")
	}
	r := rules.Default()
	r.Names = append(r.Names, "out")
	r.RefuseInside["out"] = []string{"*/rel"}
	root := filepath.Join(t.TempDir(), "repo")
	newRepo(t, root, map[string]string{".gitignore": "out/\n"})
	locked := filepath.Join(root, "out", "prod")
	write(t, filepath.Join(locked, "x"), "")
	if err := os.Chmod(locked, 0); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Chmod(locked, 0o755) })

	roots := []Root{Detect(context.Background(), root, r)}
	Measure(roots)
	if len(roots[0].Refused) != 1 || !strings.Contains(roots[0].Refused[0].Reason, `cannot check for */rel`) {
		t.Errorf("refused %+v", roots[0].Refused)
	}
}

func TestVerifyRefusesInsidePath(t *testing.T) {
	isolateGit(t)
	r := rules.Default()
	r.Names = append(r.Names, "out")
	r.RefuseInside["out"] = []string{"*/rel"}
	root := filepath.Join(t.TempDir(), "repo")
	newRepo(t, root, map[string]string{".gitignore": "out/\n"})
	write(t, filepath.Join(root, "out", "dev", "lib", "app.beam"), "")

	roots := []Root{Detect(context.Background(), root, r)}
	Measure(roots)
	if len(roots[0].Candidates) != 1 {
		t.Fatalf("candidates %+v", roots[0].Candidates)
	}
	c := roots[0].Candidates[0]
	if reason, err := Verify(context.Background(), root, c); err != nil || reason != "" {
		t.Fatalf("before release: reason %q, err %v", reason, err)
	}
	// A release built after the scan must still stop the deletion.
	write(t, filepath.Join(root, "out", "prod", "rel", "app", "COOKIE"), "x")
	reason, err := Verify(context.Background(), root, c)
	if err != nil || reason != `contains prod/rel, which refuse_inside "*/rel" protects` {
		t.Errorf("after release: reason %q, err %v", reason, err)
	}
}

func TestDetectBEAMBuild(t *testing.T) {
	isolateGit(t)
	root := filepath.Join(t.TempDir(), "repo")
	newRepo(t, root, map[string]string{
		".gitignore":         "_build/\ndeps/\n",
		"mix/mix.exs":        "",
		"rebar/rebar.config": "",
		"rel/mix.exs":        "",
		"other/x":            "",
	})
	write(t, filepath.Join(root, "mix", "_build", "dev", "lib", "app", "ebin", "app.beam"), "")
	write(t, filepath.Join(root, "mix", "deps", "dep", "lib", "dep.ex"), "")
	write(t, filepath.Join(root, "rebar", "_build", "default", "lib", "app", "ebin", "app.beam"), "")
	write(t, filepath.Join(root, "rel", "_build", "prod", "rel", "app", "releases", "COOKIE"), "x")
	write(t, filepath.Join(root, "other", "_build", "x"), "")

	roots := []Root{Detect(context.Background(), root, rules.Default())}
	Measure(roots)
	want := map[string]string{
		"mix/_build":   "_build beside mix.exs",
		"rebar/_build": "_build beside rebar.config",
	}
	if f := found(roots[0]); !mapsEqual(f, want) {
		t.Errorf("found %v, want %v", f, want)
	}
	refused := roots[0].Refused
	if len(refused) != 1 || refused[0].Path != filepath.Join(root, "rel", "_build") ||
		refused[0].Reason != `contains prod/rel, which refuse_inside "*/rel" protects` {
		t.Errorf("refused %+v", refused)
	}
}
