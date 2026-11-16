package clean

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
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
	roots := []scan.Root{got}
	return roots, scan.Measure(context.Background(), roots)
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

func TestRunListsSkippedRoots(t *testing.T) {
	root := repoWith(t)
	roots, sizes := scanOne(t, root)
	plain := t.TempDir()
	roots = append(roots, scan.Root{Path: plain, Skipped: "not in a Git work tree"})
	sizes = append(sizes, nil)
	res := Run(context.Background(), roots, sizes, quietOpts(false))
	want := []scan.SkippedRoot{{Path: plain, Reason: "not in a Git work tree"}}
	if res.Roots != 2 || !reflect.DeepEqual(res.SkippedRoots, want) || res.Selected.Caches != 2 {
		t.Errorf("roots %d, skipped %+v, selected %d", res.Roots, res.SkippedRoots, res.Selected.Caches)
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

func TestBusyRootIsNeverSelected(t *testing.T) {
	root := repoWith(t)
	roots, sizes := scanOne(t, root)
	roots[0].Busy = true
	opts := quietOpts(true)
	opts.IncludeActive = true
	res := Run(context.Background(), roots, sizes, opts)
	if len(res.Items) != 0 || res.NotSelected.Busy.Caches != 2 {
		t.Errorf("result = %+v", res)
	}
	if !exists(filepath.Join(root, "node_modules")) {
		t.Error("a cache in a busy root was deleted")
	}
}

func TestStillBusyStopsDeletion(t *testing.T) {
	root := repoWith(t)
	roots, sizes := scanOne(t, root)
	calls := 0
	opts := quietOpts(true)
	opts.StillBusy = func(string) string { calls++; return "Paseo status unknown: no daemon" }
	res := Run(context.Background(), roots, sizes, opts)
	if calls != 1 || res.Skipped != 2 || res.Items[0].Status != Skipped || res.Items[0].Reason != "Paseo status unknown: no daemon" {
		t.Errorf("%d calls, result = %+v", calls, res)
	}
	if !exists(filepath.Join(root, "node_modules")) {
		t.Error("a cache was deleted after an agent started")
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
			name: "holds a repository with a commit",
			change: func(t *testing.T, root string) {
				dep := filepath.Join(root, "node_modules", "dep")
				write(t, filepath.Join(dep, "work.txt"), "uncommitted")
				git(t, dep, "init", "-q")
				git(t, dep, "commit", "-q", "--allow-empty", "-m", "work")
			},
			reason: "contains a Git repository at dep",
			keep:   "node_modules/dep/work.txt",
		},
		{
			name: "holds a worktree-style .git file",
			change: func(t *testing.T, root string) {
				write(t, filepath.Join(root, "node_modules", "a", ".git"), "gitdir: /elsewhere\n")
			},
			reason: "contains a Git repository at a",
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

func TestApplyKeepsNestedRepository(t *testing.T) {
	root := repoWith(t)
	dep := filepath.Join(root, "node_modules", "dep")
	write(t, filepath.Join(dep, "work.txt"), "uncommitted")
	git(t, dep, "init", "-q")
	git(t, dep, "commit", "-q", "--allow-empty", "-m", "work")

	roots, sizes := scanOne(t, root)
	res := Run(context.Background(), roots, sizes, quietOpts(true))
	if !exists(filepath.Join(dep, "work.txt")) || !exists(filepath.Join(dep, ".git")) {
		t.Fatal("the nested repository was deleted")
	}
	if len(res.Refused) != 1 || res.Refused[0].Path != filepath.Join(root, "node_modules") ||
		res.Refused[0].Reason != "contains a Git repository at dep" {
		t.Errorf("refused %+v", res.Refused)
	}
	if exists(filepath.Join(root, ".venv")) || res.Selected.Caches != 1 {
		t.Errorf("the other cache was not deleted: %+v", res)
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

func TestRemoveContentsKeepsTheDirectory(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "cache")
	write(t, filepath.Join(dir, "a", "b", "f"), "x")
	write(t, filepath.Join(dir, "top"), "x")
	outside := filepath.Join(t.TempDir(), "outside")
	write(t, filepath.Join(outside, "keep"), "x")
	if err := os.Symlink(outside, filepath.Join(dir, "link")); err != nil {
		t.Fatal(err)
	}
	for _, p := range []string{"a/b", "a", "."} {
		if err := os.Chmod(filepath.Join(dir, p), 0o555); err != nil {
			t.Fatal(err)
		}
	}
	if err := RemoveContents(dir); err != nil {
		t.Fatal(err)
	}
	ents, err := os.ReadDir(dir)
	if err != nil || len(ents) != 0 {
		t.Errorf("entries = %v, err = %v", ents, err)
	}
	if !exists(filepath.Join(outside, "keep")) {
		t.Error("deleted through a symlink")
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

// TestRemoveContentsKeepsNamedEntries leaves the entries directly in dir
// that keep names, and deletes an entry with the same name deeper down.
func TestRemoveContentsKeepsNamedEntries(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "cache")
	write(t, filepath.Join(dir, "bundler", "git", "f"), "x")
	write(t, filepath.Join(dir, "a.gem"), "x")
	write(t, filepath.Join(dir, "other", "bundler", "f"), "x")
	if err := RemoveContents(dir, "bundler"); err != nil {
		t.Fatal(err)
	}
	ents, err := os.ReadDir(dir)
	if err != nil || len(ents) != 1 || ents[0].Name() != "bundler" {
		t.Errorf("entries = %v, err = %v", ents, err)
	}
	if !exists(filepath.Join(dir, "bundler", "git", "f")) {
		t.Error("deleted a kept entry")
	}
}
