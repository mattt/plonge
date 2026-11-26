package scan

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// submodules are the submodules of a swiftPackage's dependency,
// set up as SwiftPM 6.4 leaves them after "swift package resolve":
// lib, at Vendor/lib in the checkout, and inner, at deps/inner in lib.
// Each one's name in .gitmodules differs from its path.
type submodules struct {
	libSource   string // the repository that .gitmodules names for lib
	lib         string // lib's work tree in the checkout
	libGitDir   string // lib's Git directory in the checkout's .git/modules
	inner       string // inner's work tree in lib
	innerGitDir string // inner's Git directory in lib's Git directory
}

// fileProtocol lets Git clone a submodule from a local path,
// which it refuses by default.
var fileProtocol = []string{"-c", "protocol.file.allow=always"}

// addSubmodules adds a commit with the submodules to the dependency,
// pins it, and brings the cache's clones to it.
// It returns the package with the new pin.
func addSubmodules(t *testing.T, p swiftPackage) (swiftPackage, submodules) {
	t.Helper()
	tmp := filepath.Dir(p.upstream)
	innerSource := filepath.Join(tmp, "inner")
	newRepo(t, innerSource, map[string]string{".gitignore": "*.log\n", "inner.c": "int inner;\n"})
	libSource := filepath.Join(tmp, "lib")
	newRepo(t, libSource, map[string]string{".gitignore": "*.log\n", "lib.c": "int lib = 1;\n"})
	run(t, libSource, append(fileProtocol, "submodule", "add", "-q", "--name", "inner-name", innerSource, "deps/inner")...)
	run(t, libSource, "commit", "-q", "-m", "add inner")
	write(t, filepath.Join(libSource, "lib.c"), "int lib = 2;\n")
	run(t, libSource, "commit", "-q", "-am", "two")

	run(t, p.upstream, append(fileProtocol, "submodule", "add", "-q", "--name", "lib-name", libSource, "Vendor/lib")...)
	// The dependency records lib's first commit, which is not at a branch tip.
	run(t, filepath.Join(p.upstream, "Vendor", "lib"), "checkout", "-q", "--detach", "HEAD^")
	run(t, p.upstream, "add", "Vendor/lib")
	run(t, p.upstream, "commit", "-q", "-m", "add lib")
	p.pinned = gitOut(t, p.upstream, "rev-parse", "HEAD")
	p.writeResolved(t, p.upstream, p.pinned)

	run(t, p.mirror, "remote", "update")
	run(t, p.checkout, "fetch", "-q", "origin")
	run(t, p.checkout, "checkout", "-q", "--detach", p.pinned)
	run(t, p.checkout, append(fileProtocol, "submodule", "update", "-q", "--init", "--recursive")...)

	s := submodules{libSource: libSource, lib: filepath.Join(p.checkout, "Vendor", "lib")}
	s.libGitDir = filepath.Join(p.checkout, ".git", "modules", "lib-name")
	s.inner = filepath.Join(s.lib, "deps", "inner")
	s.innerGitDir = filepath.Join(s.libGitDir, "modules", "inner-name")
	for _, dir := range []string{s.libGitDir, s.innerGitDir} {
		if !isDir(filepath.Join(dir, "objects")) {
			t.Fatalf("%s is not a Git directory", dir)
		}
	}
	return p, s
}

// localCommit makes a commit in the work tree at dir
// and leaves HEAD at the commit that it had.
func localCommit(t *testing.T, dir, file string) {
	t.Helper()
	head := gitOut(t, dir, "rev-parse", "HEAD")
	write(t, filepath.Join(dir, file), "local\n")
	run(t, dir, "add", file)
	run(t, dir, "commit", "-q", "-m", "local")
	run(t, dir, "checkout", "-q", "--detach", head)
}

func TestMeasureSwiftPMSubmodules(t *testing.T) {
	isolateGit(t)
	const checkout = "contains a Git repository at checkouts/dep "
	const lib = "contains a Git repository at checkouts/dep/Vendor/lib "
	const inner = "contains a Git repository at checkouts/dep/Vendor/lib/deps/inner "
	const changed = "that has changed, untracked, or ignored files"
	tests := []struct {
		name   string
		cache  string // the cache name; .build when empty
		setup  func(t *testing.T, p swiftPackage, s submodules)
		reason string // "" when the cache is allowed
	}{
		{name: "clean submodules"},
		{name: "clean submodules in DerivedData", cache: "DerivedData"},
		{
			name: "submodule that is not initialized",
			setup: func(t *testing.T, p swiftPackage, s submodules) {
				run(t, p.checkout, "submodule", "deinit", "-q", "-f", "--all")
				if err := os.RemoveAll(filepath.Join(p.checkout, ".git", "modules")); err != nil {
					t.Fatal(err)
				}
			},
		},
		{
			name: "submodule on a local branch at the recorded commit",
			setup: func(t *testing.T, p swiftPackage, s submodules) {
				run(t, s.lib, "switch", "-q", "-c", "work")
			},
		},
		{
			name: "submodule whose URL insteadOf rewrites",
			setup: func(t *testing.T, p swiftPackage, s submodules) {
				// Git keeps the URL as .gitmodules has it and rewrites it for each transfer.
				config := filepath.Join(filepath.Dir(p.root), "gitconfig")
				write(t, config, "[url \"https://example.com/lib.git\"]\n\tinsteadOf = "+s.libSource+"\n")
				t.Setenv("GIT_CONFIG_GLOBAL", config)
			},
		},
		{
			name: "submodule with a changed file",
			setup: func(t *testing.T, p swiftPackage, s submodules) {
				write(t, filepath.Join(s.lib, "lib.c"), "int lib = 3;\n")
			},
			// git status in the checkout reports the submodule.
			reason: checkout + changed,
		},
		{
			name: "submodule with an untracked file",
			setup: func(t *testing.T, p swiftPackage, s submodules) {
				write(t, filepath.Join(s.lib, "new.c"), "")
			},
			reason: checkout + changed,
		},
		{
			name: "submodule with an ignored file",
			setup: func(t *testing.T, p swiftPackage, s submodules) {
				write(t, filepath.Join(s.lib, "build.log"), "")
			},
			reason: lib + changed,
		},
		{
			name: "submodule with an empty ignored folder",
			setup: func(t *testing.T, p swiftPackage, s submodules) {
				mkdirAll(t, filepath.Join(s.lib, "logs.log", "old"))
			},
		},
		{
			name: "nested submodule with an ignored file",
			setup: func(t *testing.T, p swiftPackage, s submodules) {
				write(t, filepath.Join(s.inner, "build.log"), "")
			},
			reason: inner + changed,
		},
		{
			name: "submodule at another commit",
			setup: func(t *testing.T, p swiftPackage, s submodules) {
				run(t, s.lib, "checkout", "-q", "--detach", "origin/HEAD")
			},
			reason: checkout + changed,
		},
		{
			name: "submodule at another commit that diff.ignoreSubmodules hides",
			setup: func(t *testing.T, p swiftPackage, s submodules) {
				run(t, s.lib, "checkout", "-q", "--detach", "origin/HEAD")
				run(t, p.checkout, "config", "diff.ignoreSubmodules", "all")
				run(t, p.checkout, "config", "submodule.lib-name.ignore", "all")
			},
			reason: checkout + changed,
		},
		{
			name: "submodule with a commit that only a local branch holds",
			setup: func(t *testing.T, p swiftPackage, s submodules) {
				run(t, s.lib, "switch", "-q", "-c", "work")
				localCommit(t, s.lib, "work.c")
			},
			reason: lib + "that has commits that only its local branches, tags, or reflogs hold",
		},
		{
			name: "submodule with a commit that only a reflog holds",
			setup: func(t *testing.T, p swiftPackage, s submodules) {
				localCommit(t, s.lib, "work.c")
			},
			reason: lib + "that has commits that only its local branches, tags, or reflogs hold",
		},
		{
			name: "submodule with a commit that only a tag holds",
			setup: func(t *testing.T, p swiftPackage, s submodules) {
				localCommit(t, s.lib, "work.c")
				run(t, s.lib, "tag", "mine", "HEAD@{1}")
				run(t, s.lib, "reflog", "expire", "--expire=all", "--all")
			},
			reason: lib + "that has commits that only its local branches, tags, or reflogs hold",
		},
		{
			name: "nested submodule with a commit that only a reflog holds",
			setup: func(t *testing.T, p swiftPackage, s submodules) {
				localCommit(t, s.inner, "work.c")
			},
			reason: inner + "that has commits that only its local branches, tags, or reflogs hold",
		},
		{
			name: "submodule with a stash",
			setup: func(t *testing.T, p swiftPackage, s submodules) {
				write(t, filepath.Join(s.lib, "lib.c"), "int lib = 3;\n")
				run(t, s.lib, "stash", "-q")
			},
			reason: lib + "that has a stash",
		},
		{
			name: "submodule with a file marked skip-worktree",
			setup: func(t *testing.T, p swiftPackage, s submodules) {
				run(t, s.lib, "update-index", "--skip-worktree", "lib.c")
			},
			reason: lib + "that has files marked assume-unchanged or skip-worktree",
		},
		{
			name: "submodule whose origin is another repository",
			setup: func(t *testing.T, p swiftPackage, s submodules) {
				run(t, s.lib, "remote", "set-url", "origin", p.upstream)
			},
			reason: lib + "whose origin is not the URL that .gitmodules gives it",
		},
		{
			name: "submodule with another remote",
			setup: func(t *testing.T, p swiftPackage, s submodules) {
				run(t, s.lib, "remote", "add", "fork", p.upstream)
			},
			reason: lib + "that does not have exactly one remote, origin",
		},
		{
			name: "submodule whose URL in the checkout's configuration is another repository",
			setup: func(t *testing.T, p swiftPackage, s submodules) {
				run(t, p.checkout, "config", "submodule.lib-name.url", p.upstream)
			},
			reason: lib + "whose URL in the configuration of the repository above it is not the URL that .gitmodules gives it",
		},
		{
			name: "submodule that pushed to its origin",
			setup: func(t *testing.T, p swiftPackage, s submodules) {
				run(t, s.lib, "switch", "-q", "-c", "work")
				run(t, s.lib, "push", "-q", "origin", "work")
				run(t, s.lib, "checkout", "-q", "--detach")
				run(t, s.lib, "branch", "-q", "-D", "work")
			},
			reason: lib + "that has pushed commits to its origin",
		},
		{
			name: "Git directory of a submodule that is no longer checked out",
			setup: func(t *testing.T, p swiftPackage, s submodules) {
				run(t, p.checkout, "submodule", "deinit", "-q", "-f", "--all")
			},
			reason: "contains a Git directory at checkouts/dep/.git/modules/lib-name that no submodule in its checkout uses",
		},
		{
			name: "files in the folder of a submodule that is not initialized",
			setup: func(t *testing.T, p swiftPackage, s submodules) {
				run(t, p.checkout, "submodule", "deinit", "-q", "-f", "--all")
				if err := os.RemoveAll(filepath.Join(p.checkout, ".git", "modules")); err != nil {
					t.Fatal(err)
				}
				// git status in the checkout does not report this file.
				write(t, filepath.Join(s.lib, "notes.txt"), "keep")
			},
			reason: checkout + "that has files in Vendor/lib, the folder of a submodule that is not initialized",
		},
		{
			name: "submodule whose Git directory has commondir",
			setup: func(t *testing.T, p swiftPackage, s submodules) {
				write(t, filepath.Join(s.libGitDir, "commondir"), p.mirror+"\n")
			},
			// The walk refuses a Git directory that shares another one.
			reason: "contains a Git directory at checkouts/dep/.git/modules/lib-name",
		},
		{
			name: "submodule with a linked worktree",
			setup: func(t *testing.T, p swiftPackage, s submodules) {
				run(t, s.lib, "worktree", "add", "-q", "--detach", filepath.Join(filepath.Dir(p.root), "wt"))
			},
			reason: "contains a Git directory at checkouts/dep/.git/modules/lib-name/worktrees/wt",
		},
		{
			name: "submodule whose Git directory is outside the checkout",
			setup: func(t *testing.T, p swiftPackage, s submodules) {
				outside := filepath.Join(filepath.Dir(p.root), "lib.git")
				if err := os.CopyFS(outside, os.DirFS(s.libGitDir)); err != nil {
					t.Fatal(err)
				}
				run(t, filepath.Dir(p.root), "config", "-f", filepath.Join(outside, "config"), "core.worktree", s.lib)
				write(t, filepath.Join(s.lib, ".git"), "gitdir: "+outside+"\n")
			},
			reason: lib + "whose .git file does not name its Git directory in the Git directory above it",
		},
		{
			name: "submodule whose Git directory names another work tree",
			setup: func(t *testing.T, p swiftPackage, s submodules) {
				other := filepath.Join(filepath.Dir(p.root), "other")
				if err := os.CopyFS(other, os.DirFS(s.lib)); err != nil {
					t.Fatal(err)
				}
				// The copy of inner's .git file names no Git directory.
				remove(t, filepath.Join(other, "deps", "inner", ".git"))
				remove(t, filepath.Join(other, "deps", "inner", "inner.c"))
				remove(t, filepath.Join(other, "deps", "inner", ".gitignore"))
				run(t, s.lib, "config", "core.worktree", other)
			},
			reason: lib + "whose Git directory names another work tree",
		},
		{
			name: "submodule with a .git directory of its own",
			setup: func(t *testing.T, p swiftPackage, s submodules) {
				remove(t, filepath.Join(s.inner, ".git"))
				if err := os.Rename(s.innerGitDir, filepath.Join(s.inner, ".git")); err != nil {
					t.Fatal(err)
				}
			},
			reason: "contains a Git repository at checkouts/dep/Vendor/lib/deps/inner",
		},
		{
			name: "work tree with a .git file that is not a submodule",
			setup: func(t *testing.T, p swiftPackage, s submodules) {
				// git status does not look inside the checkout's Git directory.
				write(t, filepath.Join(p.checkout, ".git", "wt", ".git"), "gitdir: "+s.libGitDir+"\n")
			},
			reason: "contains a Git repository at checkouts/dep/.git/wt that is not a submodule that SwiftPM set up",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			cache, packages := ".build", ""
			if tt.cache == "DerivedData" {
				cache, packages = "DerivedData", "SourcePackages"
			}
			p, s := addSubmodules(t, newSwiftPackage(t, cache, packages))
			if tt.setup != nil {
				tt.setup(t, p, s)
			}
			c, reason := measureOne(t, p.root)
			want := strings.ReplaceAll(tt.reason, " checkouts/", " "+filepath.ToSlash(filepath.Join(packages, "checkouts"))+"/")
			if reason != want && (want == "" || !strings.HasPrefix(reason, want+" ") && !strings.HasPrefix(reason, want+":")) {
				t.Errorf("reason %q, want %q", reason, want)
			}
			if c != nil && c.Path != p.cache {
				t.Errorf("candidate %s, want %s", c.Path, p.cache)
			}
		})
	}
}

// TestGuardDerivedDataSubmodules checks a folder whose workspace is gone,
// which has no Package.resolved to check its clones against.
func TestGuardDerivedDataSubmodules(t *testing.T) {
	isolateGit(t)
	p, s := addSubmodules(t, newSwiftPackage(t, "DerivedData", "SourcePackages"))
	guard := func() string {
		sizes, reasons := GuardDerivedData(context.Background(), []string{p.cache}, true)
		if (reasons[0] == "") != (sizes[0] > 0) {
			t.Errorf("size %d with reason %q", sizes[0], reasons[0])
		}
		return reasons[0]
	}
	if reason := guard(); reason != "" {
		t.Fatalf("clean submodules: reason %q", reason)
	}
	// Xcode makes an empty .swiftpm/xcode folder, which the package ignores.
	mkdirAll(t, filepath.Join(p.checkout, ".swiftpm", "xcode"))
	if reason := guard(); reason != "" {
		t.Fatalf("empty ignored folder: reason %q", reason)
	}
	write(t, filepath.Join(s.inner, "build.log"), "")
	want := "contains a Git repository at SourcePackages/checkouts/dep/Vendor/lib/deps/inner that has changed, untracked, or ignored files"
	if reason := guard(); reason != want {
		t.Errorf("reason %q, want %q", reason, want)
	}
}

// TestVerifySwiftPMSubmoduleChange changes a submodule after the scan.
func TestVerifySwiftPMSubmoduleChange(t *testing.T) {
	isolateGit(t)
	p, s := addSubmodules(t, newSwiftPackage(t, ".build", ""))
	c, reason := measureOne(t, p.root)
	if c == nil {
		t.Fatalf("refused at scan: %s", reason)
	}
	if reason, err := Verify(context.Background(), p.root, *c); err != nil || reason != "" {
		t.Fatalf("before the change: reason %q, err %v", reason, err)
	}
	write(t, filepath.Join(s.lib, "build.log"), "")
	want := "contains a Git repository at checkouts/dep/Vendor/lib that has changed, untracked, or ignored files"
	if reason, err := Verify(context.Background(), p.root, *c); err != nil || reason != want {
		t.Errorf("after the change: reason %q, err %v, want %q", reason, err, want)
	}
}
