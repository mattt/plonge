package clean

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/mattt/plonge/internal/rules"
	"github.com/mattt/plonge/internal/scan"
)

func isolateGit(t *testing.T) {
	t.Helper()
	t.Setenv("GIT_CONFIG_GLOBAL", os.DevNull)
	t.Setenv("GIT_CONFIG_NOSYSTEM", "1")
	t.Setenv("GIT_AUTHOR_NAME", "Test")
	t.Setenv("GIT_AUTHOR_EMAIL", "test@example.com")
	t.Setenv("GIT_COMMITTER_NAME", "Test")
	t.Setenv("GIT_COMMITTER_EMAIL", "test@example.com")
}

func git(t *testing.T, dir string, args ...string) {
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

func exists(path string) bool {
	_, err := os.Lstat(path)
	return err == nil
}

// repoWith makes a repository with an ignored node_modules and .venv,
// and returns its root.
func repoWith(t *testing.T) string {
	t.Helper()
	isolateGit(t)
	root := filepath.Join(t.TempDir(), "repo")
	if err := os.MkdirAll(root, 0o755); err != nil {
		t.Fatal(err)
	}
	git(t, root, "init", "-q")
	write(t, filepath.Join(root, ".gitignore"), "node_modules/\n.venv/\n")
	write(t, filepath.Join(root, "main.go"), "package main\n")
	git(t, root, "add", ".")
	git(t, root, "commit", "-q", "-m", "init")
	write(t, filepath.Join(root, "node_modules", "a", "index.js"), strings.Repeat("x", 100_000))
	write(t, filepath.Join(root, ".venv", "bin", "python"), "x")
	return root
}

func scanOne(t *testing.T, root string) ([]scan.Root, [][]int64) {
	t.Helper()
	got := scan.Detect(context.Background(), root, rules.Default())
	if got.Skipped != "" || len(got.Errors) > 0 {
		t.Fatalf("skipped %q, errors %q", got.Skipped, got.Errors)
	}
	var dirs []string
	for _, c := range got.Candidates {
		dirs = append(dirs, c.Path)
	}
	return []scan.Root{got}, [][]int64{scan.Sizes(dirs)}
}

// quietOpts treats every root as quiet.
func quietOpts(apply bool) Options {
	return Options{Apply: apply, QuietHours: 0, Now: time.Now().Add(time.Hour)}
}

func TestDryRunDeletesNothing(t *testing.T) {
	root := repoWith(t)
	roots, sizes := scanOne(t, root)
	res := Run(context.Background(), roots, sizes, quietOpts(false))
	if res.Applied || res.Selected.Caches != 2 || res.FreedBytes != 0 {
		t.Errorf("result = %+v", res)
	}
	for _, it := range res.Items {
		if it.Status != WouldDelete || !exists(it.Path) {
			t.Errorf("item %+v: exists %v", it, exists(it.Path))
		}
	}
}

func TestApplyDeletesAndReportsFreedSpace(t *testing.T) {
	root := repoWith(t)
	roots, sizes := scanOne(t, root)
	res := Run(context.Background(), roots, sizes, quietOpts(true))
	if res.Failed != 0 || res.Skipped != 0 || len(res.Items) != 2 {
		t.Fatalf("result = %+v", res)
	}
	if res.FreedBytes != res.Selected.Bytes || res.FreedBytes < 100_000 {
		t.Errorf("freed %d of %d", res.FreedBytes, res.Selected.Bytes)
	}
	if exists(filepath.Join(root, "node_modules")) || exists(filepath.Join(root, ".venv")) {
		t.Error("caches are still there")
	}
	if !exists(filepath.Join(root, "main.go")) {
		t.Error("tracked file is gone")
	}
}

func TestSelection(t *testing.T) {
	root := repoWith(t)
	roots, sizes := scanOne(t, root)
	now := time.Now()

	roots[0].LastActivity = now // an active root
	opts := Options{QuietHours: 24, Now: now}
	res := Run(context.Background(), roots, sizes, opts)
	if res.Selected.Caches != 0 || res.NotSelected.Active.Caches != 2 {
		t.Errorf("active root: %+v", res)
	}

	opts.IncludeActive = true
	if res = Run(context.Background(), roots, sizes, opts); res.Selected.Caches != 2 {
		t.Errorf("include active: %+v", res)
	}

	roots[0].LastActivity = time.Time{} // unknown activity is not quiet
	opts.IncludeActive = false
	if res = Run(context.Background(), roots, sizes, opts); res.Selected.Caches != 0 {
		t.Errorf("unknown activity: %+v", res)
	}

	roots[0].LastActivity = now.Add(-48 * time.Hour)
	opts = Options{QuietHours: 24, Now: now, MinSize: 50_000}
	res = Run(context.Background(), roots, sizes, opts)
	if res.Selected.Caches != 1 || res.NotSelected.BelowSize.Caches != 1 ||
		res.Items[0].Path != filepath.Join(root, "node_modules") {
		t.Errorf("min size: %+v", res)
	}
}

// TestRecheck changes a cache after the scan and before the deletion.
func TestRecheck(t *testing.T) {
	tests := []struct {
		name   string
		change func(t *testing.T, root string) // changes node_modules
		reason string
		keep   string // a path that must survive
	}{
		{
			name: "replaced by a symlink",
			change: func(t *testing.T, root string) {
				target := filepath.Join(filepath.Dir(root), "target")
				write(t, filepath.Join(target, "keep"), "")
				if err := os.RemoveAll(filepath.Join(root, "node_modules")); err != nil {
					t.Fatal(err)
				}
				if err := os.Symlink(target, filepath.Join(root, "node_modules")); err != nil {
					t.Fatal(err)
				}
			},
			reason: "symlink",
			keep:   "../target/keep",
		},
		{
			name: "removed",
			change: func(t *testing.T, root string) {
				if err := os.RemoveAll(filepath.Join(root, "node_modules")); err != nil {
					t.Fatal(err)
				}
			},
			reason: "no longer exists",
		},
		{
			name: "replaced by a file",
			change: func(t *testing.T, root string) {
				if err := os.RemoveAll(filepath.Join(root, "node_modules")); err != nil {
					t.Fatal(err)
				}
				write(t, filepath.Join(root, "node_modules"), "")
			},
			reason: "no longer a directory",
			keep:   "node_modules",
		},
		{
			name: "no longer ignored",
			change: func(t *testing.T, root string) {
				write(t, filepath.Join(root, ".gitignore"), ".venv/\npkg/\n")
			},
			reason: "no longer ignored",
			keep:   "node_modules/a/index.js",
		},
		{
			name: "holds a tracked file",
			change: func(t *testing.T, root string) {
				git(t, root, "add", "-f", "node_modules/a/index.js")
			},
			reason: "tracked file",
			keep:   "node_modules/a/index.js",
		},
		{
			name: "parent moved outside the root",
			change: func(t *testing.T, root string) {
				outside := filepath.Join(filepath.Dir(root), "outside")
				if err := os.Rename(filepath.Join(root, "pkg"), outside); err != nil {
					t.Fatal(err)
				}
				if err := os.Symlink(outside, filepath.Join(root, "pkg")); err != nil {
					t.Fatal(err)
				}
			},
			reason: "inside its root",
			keep:   "pkg/node_modules/b.js",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			root := repoWith(t)
			write(t, filepath.Join(root, "pkg", "node_modules", "b.js"), "")
			roots, sizes := scanOne(t, root)
			tt.change(t, root)
			res := Run(context.Background(), roots, sizes, quietOpts(true))
			if res.Skipped != 1 || res.Failed != 0 {
				t.Fatalf("result = %+v", res)
			}
			var reasons []string
			for _, it := range res.Items {
				if it.Status == Skipped {
					reasons = append(reasons, it.Reason)
				}
			}
			if !strings.Contains(strings.Join(reasons, ";"), tt.reason) {
				t.Errorf("reasons %q, want %q", reasons, tt.reason)
			}
			if tt.keep != "" && !exists(filepath.Join(root, tt.keep)) {
				t.Errorf("%s is gone", tt.keep)
			}
			if exists(filepath.Join(root, ".venv")) {
				t.Error("the unchanged cache was not deleted")
			}
		})
	}
}

func TestRemoveTreeReadOnly(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "mod")
	write(t, filepath.Join(dir, "a", "b", "f"), "x")
	outside := filepath.Join(t.TempDir(), "outside")
	write(t, outside, "x")
	if err := os.Chmod(outside, 0o444); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(outside, filepath.Join(dir, "a", "link")); err != nil {
		t.Fatal(err)
	}
	for _, p := range []string{"a/b/f", "a/b", "a", "."} {
		mode := os.FileMode(0o555)
		if p == "a/b/f" {
			mode = 0o444
		}
		if err := os.Chmod(filepath.Join(dir, p), mode); err != nil {
			t.Fatal(err)
		}
	}
	if err := RemoveTree(dir); err != nil {
		t.Fatal(err)
	}
	if exists(dir) {
		t.Error("tree is still there")
	}
	info, err := os.Stat(outside)
	if err != nil || info.Mode().Perm() != 0o444 {
		t.Errorf("symlink target changed: %v %v", info, err)
	}
}

func TestContinuesAfterError(t *testing.T) {
	root := repoWith(t)
	parent := filepath.Join(root, "locked")
	write(t, filepath.Join(parent, "node_modules", "x.js"), strings.Repeat("x", 200_000))
	roots, sizes := scanOne(t, root)
	if err := os.Chmod(parent, 0o555); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Chmod(parent, 0o755) })

	res := Run(context.Background(), roots, sizes, quietOpts(true))
	if res.Failed != 1 || res.Skipped != 0 {
		t.Fatalf("result = %+v", res)
	}
	if res.Items[0].Status != Failed || res.Items[0].Reason == "" {
		t.Errorf("first item = %+v", res.Items[0])
	}
	if exists(filepath.Join(root, "node_modules")) || exists(filepath.Join(root, ".venv")) {
		t.Error("plonge stopped after the error")
	}
}
