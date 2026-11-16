package scan

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/mattt/plonge/internal/rules"
)

func gitOut(t *testing.T, dir string, args ...string) string {
	t.Helper()
	cmd := exec.Command("git", args...)
	cmd.Dir = dir
	out, err := cmd.Output()
	if err != nil {
		t.Fatalf("git %s: %v", strings.Join(args, " "), err)
	}
	return strings.TrimSpace(string(out))
}

// swiftPackage is a project with a SwiftPM cache that holds a checkout
// and a bare clone of one dependency, laid out as SwiftPM 6.4 lays them out.
type swiftPackage struct {
	root     string // the project repository
	upstream string // the dependency's source repository
	cache    string // .build, or DerivedData
	checkout string // the dependency's checkout in the cache
	mirror   string // the dependency's bare clone in the cache
	first    string // the dependency's first commit
	pinned   string // the commit that Package.resolved pins
}

// newSwiftPackage makes a project whose cache, at cacheName,
// holds the repositories in packages, relative to the cache.
// The dependency has two commits; Package.resolved pins the second,
// and the checkout is at it, with HEAD detached as SwiftPM leaves it.
func newSwiftPackage(t *testing.T, cacheName, packages string) swiftPackage {
	t.Helper()
	tmp := t.TempDir()
	upstream := filepath.Join(tmp, "dep")
	newRepo(t, upstream, map[string]string{
		".gitignore":            "*.log\n",
		"Package.swift":         "// swift-tools-version:5.9\n",
		"Sources/Dep/Dep.swift": "public let x = 1\n",
	})
	first := gitOut(t, upstream, "rev-parse", "HEAD")
	write(t, filepath.Join(upstream, "Sources", "Dep", "Dep.swift"), "public let x = 2\n")
	run(t, upstream, "commit", "-q", "-am", "two")
	run(t, upstream, "tag", "1.0.0")
	pinned := gitOut(t, upstream, "rev-parse", "HEAD")

	p := swiftPackage{root: filepath.Join(tmp, "app"), upstream: upstream, first: first, pinned: pinned}
	newRepo(t, p.root, map[string]string{
		".gitignore":    "/" + cacheName + "/\n",
		"Package.swift": "// swift-tools-version:5.9\n",
	})
	p.writeResolved(t, p.upstream, pinned)
	p.cache = filepath.Join(p.root, cacheName)
	base := filepath.Join(p.cache, packages)
	p.mirror = filepath.Join(base, "repositories", "dep-95d00fdd")
	p.checkout = filepath.Join(base, "checkouts", "dep")
	if err := os.MkdirAll(filepath.Dir(p.mirror), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Dir(p.checkout), 0o755); err != nil {
		t.Fatal(err)
	}
	run(t, tmp, "clone", "-q", "--mirror", upstream, p.mirror)
	run(t, tmp, "clone", "-q", "--shared", "--no-checkout", p.mirror, p.checkout)
	run(t, p.checkout, "checkout", "-q", "--detach", pinned)
	write(t, filepath.Join(p.cache, "CACHEDIR.TAG"), "Signature: 8a477f597d28d172789f06886806bc55\n")
	return p
}

// writeResolved writes a Package.resolved that pins dep at location and revision.
func (p swiftPackage) writeResolved(t *testing.T, location, revision string) {
	t.Helper()
	write(t, filepath.Join(p.root, "Package.resolved"), fmt.Sprintf(`{
  "pins" : [
    {
      "identity" : "dep",
      "kind" : "localSourceControl",
      "location" : %q,
      "state" : { "revision" : %q, "version" : "1.0.0" }
    }
  ],
  "version" : 3
}
`, location, revision))
}

// useSpacedOrigin points the bare clone's origin at a separate repository
// whose path is the upstream's path with a final space.
func (p swiftPackage) useSpacedOrigin(t *testing.T) {
	t.Helper()
	spaced := p.upstream + " "
	run(t, p.root, "clone", "-q", "--bare", p.upstream, spaced)
	run(t, p.mirror, "config", "remote.origin.url", spaced)
}

// useRelativeLocation sets the pin's location and the bare clone's origin
// to the same relative path.
func (p swiftPackage) useRelativeLocation(t *testing.T) {
	t.Helper()
	p.writeResolved(t, "dep", p.pinned)
	run(t, p.mirror, "config", "remote.origin.url", "dep")
}

// localCommit makes a commit in the checkout and leaves HEAD at the pin,
// and returns the new commit.
func (p swiftPackage) localCommit(t *testing.T) string {
	t.Helper()
	write(t, filepath.Join(p.checkout, "Sources", "Dep", "Dep.swift"), "public let x = 3\n")
	run(t, p.checkout, "commit", "-q", "-am", "local")
	c := gitOut(t, p.checkout, "rev-parse", "HEAD")
	run(t, p.checkout, "checkout", "-q", "--detach", p.pinned)
	return c
}

// useXcode replaces Package.swift with the Xcode workspaces and projects
// in bundles, such as App.xcodeproj, and moves Package.resolved
// to pins, relative to the project, or deletes it when pins is "".
func (p swiftPackage) useXcode(t *testing.T, pins string, bundles ...string) {
	t.Helper()
	if err := os.Remove(filepath.Join(p.root, "Package.swift")); err != nil {
		t.Fatal(err)
	}
	for _, b := range bundles {
		if err := os.MkdirAll(filepath.Join(p.root, b), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	resolved := filepath.Join(p.root, "Package.resolved")
	if pins == "" {
		if err := os.Remove(resolved); err != nil {
			t.Fatal(err)
		}
		return
	}
	dst := filepath.Join(p.root, pins)
	if err := os.MkdirAll(filepath.Dir(dst), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.Rename(resolved, dst); err != nil {
		t.Fatal(err)
	}
}

// measureOne detects and measures the one cache in root.
// It returns the candidate, when Measure kept it, and the refusal reason.
func measureOne(t *testing.T, root string) (*Candidate, string) {
	t.Helper()
	roots := []Root{Detect(context.Background(), root, rules.Default())}
	sizes := Measure(context.Background(), roots)
	got := roots[0]
	if len(got.Errors) > 0 {
		t.Fatalf("errors %q", got.Errors)
	}
	switch {
	case len(got.Candidates) == 1 && len(got.Refused) == 0:
		if sizes[0][0] <= 0 {
			t.Errorf("size %d, want more than 0", sizes[0][0])
		}
		return &got.Candidates[0], ""
	case len(got.Candidates) == 0 && len(got.Refused) == 1:
		return nil, got.Refused[0].Reason
	}
	t.Fatalf("candidates %+v, refused %+v, want one of either", got.Candidates, got.Refused)
	return nil, ""
}

func TestMeasureSwiftPMCheckouts(t *testing.T) {
	isolateGit(t)
	const checkout = "contains a Git repository at checkouts/dep "
	const derived = "contains a Git repository at SourcePackages/checkouts/dep"
	const projectPins = "App.xcodeproj/project.xcworkspace/xcshareddata/swiftpm/Package.resolved"
	const workspacePins = "App.xcworkspace/xcshareddata/swiftpm/Package.resolved"
	tests := []struct {
		name   string
		cache  string // the cache name; .build when empty
		setup  func(t *testing.T, p swiftPackage)
		reason string // "" when the cache is allowed
	}{
		{name: "clean pinned checkout"},
		{
			name:  "DerivedData beside Package.swift",
			cache: "DerivedData",
		},
		{
			name:  "DerivedData beside an Xcode project",
			cache: "DerivedData",
			setup: func(t *testing.T, p swiftPackage) {
				p.useXcode(t, projectPins, "App.xcodeproj")
			},
		},
		{
			name:  "DerivedData beside an Xcode workspace",
			cache: "DerivedData",
			setup: func(t *testing.T, p swiftPackage) {
				p.useXcode(t, workspacePins, "App.xcworkspace")
			},
		},
		{
			name:  "DerivedData beside a workspace and a project, pinned by the workspace",
			cache: "DerivedData",
			setup: func(t *testing.T, p swiftPackage) {
				p.useXcode(t, workspacePins, "App.xcworkspace", "App.xcodeproj")
			},
		},
		{
			name:  "DerivedData beside a workspace and a project, pinned by the project",
			cache: "DerivedData",
			setup: func(t *testing.T, p swiftPackage) {
				p.useXcode(t, projectPins, "App.xcworkspace", "App.xcodeproj")
			},
			reason: derived + ", and " + workspacePins + " cannot be read",
		},
		{
			name:  "DerivedData beside two Xcode projects",
			cache: "DerivedData",
			setup: func(t *testing.T, p swiftPackage) {
				p.useXcode(t, projectPins, "App.xcodeproj", "Other.xcodeproj")
			},
			reason: derived + ", and its folder holds more than one Xcode project and no workspace",
		},
		{
			name:  "DerivedData beside two Xcode workspaces",
			cache: "DerivedData",
			setup: func(t *testing.T, p swiftPackage) {
				p.useXcode(t, workspacePins, "App.xcworkspace", "Other.xcworkspace", "App.xcodeproj")
			},
			reason: derived + ", and its folder holds more than one Xcode workspace",
		},
		{
			name:  "DerivedData beside an Xcode project without Package.resolved",
			cache: "DerivedData",
			setup: func(t *testing.T, p swiftPackage) {
				p.useXcode(t, "", "App.xcodeproj")
			},
			reason: derived + ", and " + projectPins + " cannot be read",
		},
		{
			name:  "DerivedData beside an Xcode project with a changed checkout",
			cache: "DerivedData",
			setup: func(t *testing.T, p swiftPackage) {
				p.useXcode(t, projectPins, "App.xcodeproj")
				write(t, filepath.Join(p.checkout, "Sources", "Dep", "Dep.swift"), "public let x = 3\n")
			},
			reason: derived + " that has changed, untracked, or ignored files",
		},
		{
			name:  "DerivedData beside an Xcode project whose pins leave out the checkout",
			cache: "DerivedData",
			setup: func(t *testing.T, p swiftPackage) {
				p.useXcode(t, projectPins, "App.xcodeproj")
				if err := os.Rename(p.checkout, filepath.Join(filepath.Dir(p.checkout), "other")); err != nil {
					t.Fatal(err)
				}
			},
			reason: "contains a Git repository at SourcePackages/checkouts/other whose HEAD is not a commit that Package.resolved pins for it",
		},
		{
			name:  "DerivedData beside neither Package.swift nor an Xcode project",
			cache: "DerivedData",
			setup: func(t *testing.T, p swiftPackage) {
				p.useXcode(t, "")
			},
			reason: "contains a Git repository at SourcePackages/checkouts/dep",
		},
		{
			name: ".build without Package.swift",
			setup: func(t *testing.T, p swiftPackage) {
				if err := os.Remove(filepath.Join(p.root, "Package.swift")); err != nil {
					t.Fatal(err)
				}
			},
			reason: "contains a Git repository at checkouts/dep",
		},
		{
			name: "modified file",
			setup: func(t *testing.T, p swiftPackage) {
				write(t, filepath.Join(p.checkout, "Sources", "Dep", "Dep.swift"), "public let x = 3\n")
			},
			reason: checkout + "that has changed, untracked, or ignored files",
		},
		{
			name: "untracked file",
			setup: func(t *testing.T, p swiftPackage) {
				write(t, filepath.Join(p.checkout, "Sources", "Dep", "New.swift"), "")
			},
			reason: checkout + "that has changed, untracked, or ignored files",
		},
		{
			name: "ignored file",
			setup: func(t *testing.T, p swiftPackage) {
				write(t, filepath.Join(p.checkout, "notes.log"), "")
			},
			reason: checkout + "that has changed, untracked, or ignored files",
		},
		{
			name: "assume-unchanged file",
			setup: func(t *testing.T, p swiftPackage) {
				run(t, p.checkout, "update-index", "--assume-unchanged", "Sources/Dep/Dep.swift")
				write(t, filepath.Join(p.checkout, "Sources", "Dep", "Dep.swift"), "public let x = 3\n")
			},
			reason: checkout + "that has files marked assume-unchanged or skip-worktree",
		},
		{
			name: "unpinned commit",
			setup: func(t *testing.T, p swiftPackage) {
				run(t, p.checkout, "checkout", "-q", "--detach", p.first)
			},
			reason: checkout + "whose HEAD is not a commit that Package.resolved pins for it",
		},
		{
			name: "no pin for the checkout",
			setup: func(t *testing.T, p swiftPackage) {
				if err := os.Rename(p.checkout, filepath.Join(filepath.Dir(p.checkout), "other")); err != nil {
					t.Fatal(err)
				}
			},
			reason: "contains a Git repository at checkouts/other whose HEAD is not a commit that Package.resolved pins for it",
		},
		{
			name: "missing Package.resolved",
			setup: func(t *testing.T, p swiftPackage) {
				if err := os.Remove(filepath.Join(p.root, "Package.resolved")); err != nil {
					t.Fatal(err)
				}
			},
			reason: "contains a Git repository at checkouts/dep, and Package.resolved cannot be read",
		},
		{
			name: "stash",
			setup: func(t *testing.T, p swiftPackage) {
				write(t, filepath.Join(p.checkout, "Sources", "Dep", "Dep.swift"), "public let x = 3\n")
				run(t, p.checkout, "stash", "push", "-q", "-m", "work")
			},
			reason: checkout + "that has a stash",
		},
		{
			name:   "commit that only the reflog holds",
			setup:  func(t *testing.T, p swiftPackage) { p.localCommit(t) },
			reason: checkout + "that has commits that only its local branches, tags, or reflogs hold",
		},
		{
			name: "commit that only a local tag holds",
			setup: func(t *testing.T, p swiftPackage) {
				run(t, p.checkout, "tag", "mine", p.localCommit(t))
				run(t, p.checkout, "reflog", "expire", "--expire=all", "--all")
			},
			reason: checkout + "that has commits that only its local branches, tags, or reflogs hold",
		},
		{
			name: "upstream tag on a commit that no branch holds",
			setup: func(t *testing.T, p swiftPackage) {
				run(t, p.upstream, "checkout", "-q", "--detach")
				run(t, p.upstream, "commit", "-q", "--allow-empty", "-m", "release")
				run(t, p.upstream, "tag", "-a", "-m", "release", "2.0.0")
				run(t, p.mirror, "fetch", "-q")
				run(t, p.checkout, "fetch", "-q", "--tags")
			},
		},
		{
			name: "commit pushed to the bare clone",
			setup: func(t *testing.T, p swiftPackage) {
				run(t, p.checkout, "push", "-q", "origin", p.localCommit(t)+":refs/heads/mine")
				run(t, p.checkout, "fetch", "-q")
				// Without its reflogs, the commit is in refs/remotes/origin/mine.
				branches := strings.Fields(gitOut(t, p.checkout, "for-each-ref", "--format=%(refname)", "refs/heads"))
				run(t, p.checkout, append([]string{"reflog", "expire", "--expire=all", "HEAD"}, branches...)...)
			},
			reason: checkout + "that has pushed commits to its SwiftPM clone",
		},
		{
			name: "commit that only the bare clone's reflog holds",
			setup: func(t *testing.T, p swiftPackage) {
				branch := gitOut(t, p.mirror, "symbolic-ref", "HEAD")
				tip := gitOut(t, p.mirror, "rev-parse", branch)
				c := gitOut(t, p.mirror, "commit-tree", "-p", tip, "-m", "unpublished", tip+"^{tree}")
				run(t, p.mirror, "-c", "core.logAllRefUpdates=always", "update-ref", branch, c)
				run(t, p.mirror, "-c", "core.logAllRefUpdates=always", "update-ref", branch, tip)
			},
			reason: "contains a Git directory at repositories/dep-95d00fdd that has commits that only its reflogs hold",
		},
		{
			name: "unrelated bare repository in repositories",
			setup: func(t *testing.T, p swiftPackage) {
				backup := filepath.Join(filepath.Dir(p.mirror), "backup")
				run(t, p.root, "init", "-q", "--bare", backup)
				run(t, p.checkout, "push", "-q", backup, p.pinned+":refs/heads/master")
			},
			reason: "contains a Git directory at repositories/backup that does not have exactly one remote, origin",
		},
		{
			name: "bare clone of a location that no pin names",
			setup: func(t *testing.T, p swiftPackage) {
				p.writeResolved(t, "https://example.com/other/dep.git", p.pinned)
			},
			reason: "contains a Git directory at repositories/dep-95d00fdd whose origin no pin in Package.resolved names",
		},
		{
			name: "pin at a separate repository with a .git suffix",
			setup: func(t *testing.T, p swiftPackage) {
				run(t, p.root, "clone", "-q", "--bare", p.upstream, p.upstream+".git")
				p.writeResolved(t, p.upstream+".git", p.pinned)
			},
			reason: "contains a Git directory at repositories/dep-95d00fdd whose origin no pin in Package.resolved names",
		},
		{
			name:   "origin at a separate repository whose path ends in a space",
			setup:  func(t *testing.T, p swiftPackage) { p.useSpacedOrigin(t) },
			reason: "contains a Git directory at repositories/dep-95d00fdd whose origin no pin in Package.resolved names",
		},
		{
			name:   "equal relative paths",
			setup:  func(t *testing.T, p swiftPackage) { p.useRelativeLocation(t) },
			reason: "contains a Git directory at repositories/dep-95d00fdd whose origin no pin in Package.resolved names",
		},
		{
			name: "bare clone that is not a mirror",
			setup: func(t *testing.T, p swiftPackage) {
				run(t, p.mirror, "config", "--unset", "remote.origin.mirror")
			},
			reason: "contains a Git directory at repositories/dep-95d00fdd that is not a mirror of its origin",
		},
		{
			name: "checkout with another remote",
			setup: func(t *testing.T, p swiftPackage) {
				run(t, p.checkout, "remote", "add", "fork", p.upstream)
			},
			reason: checkout + "that does not have exactly one remote, origin",
		},
		{
			name: "checkout whose origin is not its bare clone",
			setup: func(t *testing.T, p swiftPackage) {
				run(t, p.checkout, "remote", "set-url", "origin", p.upstream)
			},
			reason: checkout + "whose origin is not a SwiftPM clone in this cache",
		},
		{
			name: "linked worktree",
			setup: func(t *testing.T, p swiftPackage) {
				run(t, p.checkout, "worktree", "add", "-q", "--detach", filepath.Join(filepath.Dir(p.root), "wt"))
			},
			reason: checkout + "that has linked worktrees",
		},
		{
			name: "worktree-style .git file in checkouts",
			setup: func(t *testing.T, p swiftPackage) {
				write(t, filepath.Join(p.cache, "checkouts", "linked", ".git"), "gitdir: /elsewhere/.git/worktrees/linked\n")
			},
			reason: "contains a Git repository at checkouts/linked",
		},
		{
			name: "repository in a checkout",
			setup: func(t *testing.T, p swiftPackage) {
				newRepo(t, filepath.Join(p.checkout, "Vendor", "sub"), nil)
			},
			reason: "contains a Git repository at checkouts/dep/Vendor/sub",
		},
		{
			name: "repository elsewhere in .build",
			setup: func(t *testing.T, p swiftPackage) {
				newRepo(t, filepath.Join(p.cache, "work", "dep"), map[string]string{"work.txt": "keep"})
			},
			reason: "contains a Git repository at work/dep",
		},
		{
			name: "bare clone elsewhere in .build",
			setup: func(t *testing.T, p swiftPackage) {
				run(t, p.root, "init", "-q", "--bare", filepath.Join(p.cache, "repositories", "nested", "x.git"))
			},
			reason: "contains a Git directory at repositories/nested/x.git",
		},
		{
			name: "bare clone with a linked worktree",
			setup: func(t *testing.T, p swiftPackage) {
				run(t, p.mirror, "worktree", "add", "-q", "--detach", filepath.Join(filepath.Dir(p.root), "wt"))
			},
			reason: "contains a Git directory at repositories/dep-95d00fdd that has linked worktrees",
		},
		{
			name: "clone that is not bare in repositories",
			setup: func(t *testing.T, p swiftPackage) {
				run(t, p.mirror, "config", "core.bare", "false")
			},
			reason: "contains a Git directory at repositories/dep-95d00fdd that is not bare",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			cache, packages := ".build", ""
			if tt.cache == "DerivedData" {
				cache, packages = "DerivedData", "SourcePackages"
			}
			p := newSwiftPackage(t, cache, packages)
			if tt.setup != nil {
				tt.setup(t, p)
			}
			c, reason := measureOne(t, p.root)
			if !strings.HasPrefix(reason, tt.reason) || (tt.reason == "") != (reason == "") {
				t.Errorf("reason %q, want %q", reason, tt.reason)
			}
			if c != nil && c.Path != p.cache {
				t.Errorf("candidate %s, want %s", c.Path, p.cache)
			}
		})
	}
}

func TestVerifySwiftPMCheckoutChange(t *testing.T) {
	isolateGit(t)
	changes := []struct {
		name   string
		change func(t *testing.T, p swiftPackage)
		reason string
	}{
		{
			name: "untracked file",
			change: func(t *testing.T, p swiftPackage) {
				write(t, filepath.Join(p.checkout, "Sources", "Dep", "New.swift"), "")
			},
			reason: "contains a Git repository at checkouts/dep that has changed, untracked, or ignored files",
		},
		{
			name: "new pin",
			change: func(t *testing.T, p swiftPackage) {
				p.writeResolved(t, p.upstream, p.first)
			},
			reason: "contains a Git repository at checkouts/dep whose HEAD is not a commit that Package.resolved pins for it",
		},
		{
			name: "commit that only a local tag holds",
			change: func(t *testing.T, p swiftPackage) {
				run(t, p.checkout, "tag", "mine", p.localCommit(t))
				run(t, p.checkout, "reflog", "expire", "--expire=all", "--all")
			},
			reason: "contains a Git repository at checkouts/dep that has commits that only its local branches, tags, or reflogs hold",
		},
		{
			name: "pin moved to a separate repository with a .git suffix",
			change: func(t *testing.T, p swiftPackage) {
				run(t, p.root, "clone", "-q", "--bare", p.upstream, p.upstream+".git")
				p.writeResolved(t, p.upstream+".git", p.pinned)
			},
			reason: "contains a Git directory at repositories/dep-95d00fdd whose origin no pin in Package.resolved names",
		},
		{
			name:   "origin moved to a separate repository whose path ends in a space",
			change: func(t *testing.T, p swiftPackage) { p.useSpacedOrigin(t) },
			reason: "contains a Git directory at repositories/dep-95d00fdd whose origin no pin in Package.resolved names",
		},
		{
			name:   "pin and origin changed to equal relative paths",
			change: func(t *testing.T, p swiftPackage) { p.useRelativeLocation(t) },
			reason: "contains a Git directory at repositories/dep-95d00fdd whose origin no pin in Package.resolved names",
		},
		{
			name: "Package.swift removed",
			change: func(t *testing.T, p swiftPackage) {
				if err := os.Remove(filepath.Join(p.root, "Package.swift")); err != nil {
					t.Fatal(err)
				}
			},
			reason: "contains a Git repository at checkouts/dep",
		},
	}
	for _, tt := range changes {
		t.Run(tt.name, func(t *testing.T) {
			p := newSwiftPackage(t, ".build", "")
			c, reason := measureOne(t, p.root)
			if c == nil {
				t.Fatalf("refused at scan: %s", reason)
			}
			if reason, err := Verify(context.Background(), p.root, *c); err != nil || reason != "" {
				t.Fatalf("before the change: reason %q, err %v", reason, err)
			}
			tt.change(t, p)
			reason, err := Verify(context.Background(), p.root, *c)
			if err != nil || reason != tt.reason {
				t.Errorf("after the change: reason %q, err %v, want %q", reason, err, tt.reason)
			}
		})
	}
}

func TestSameLocation(t *testing.T) {
	tmp := t.TempDir()
	dep := filepath.Join(tmp, "dep")
	for _, d := range []string{dep, dep + ".git", filepath.Join(tmp, "Dep")} {
		if err := os.MkdirAll(d, 0o755); err != nil {
			t.Fatal(err)
		}
	}
	link := filepath.Join(tmp, "link")
	if err := os.Symlink(dep, link); err != nil {
		t.Fatal(err)
	}
	same := [][2]string{
		{"https://github.com/apple/swift-nio.git", "https://github.com/apple/swift-nio.git"},
		{"https://github.com/apple/swift-nio.git/", "https://github.com/apple/swift-nio.git"},
		{"git@github.com:apple/swift-nio.git", "git@github.com:apple/swift-nio.git"},
		{dep, dep + "/"},
		{link, dep},
	}
	for _, pair := range same {
		if !sameLocation(pair[0], pair[1]) {
			t.Errorf("%q and %q do not match", pair[0], pair[1])
		}
	}
	different := [][2]string{
		{"https://github.com/apple/swift-nio.git", "https://github.com/apple/swift-nio"},
		{"https://github.com/apple/swift-nio", "https://github.com/Apple/swift-nio"},
		{"https://github.com/apple/swift-nio", "https://github.com:443/apple/swift-nio"},
		{"https://github.com/apple/swift-nio", "git@github.com:apple/swift-nio"},
		{"https://github.com/apple/swift-nio", "https://github.com/apple/swift-nio//"},
		{dep, dep + ".git"},
		{dep, "file://" + dep},
		{dep, filepath.Join(tmp, "missing")},
		{dep, dep + " "},
		{"dep", "dep"},
		{"../dep", "../dep"},
		{"https://github.com/apple/swift-nio", "https://github.com/apple/swift-nio "},
		{"", ""},
	}
	// On a case-insensitive volume, Dep and dep are the same directory.
	if a, b := statOrNil(dep), statOrNil(filepath.Join(tmp, "Dep")); a != nil && b != nil && !os.SameFile(a, b) {
		different = append(different, [2]string{dep, filepath.Join(tmp, "Dep")})
	}
	for _, pair := range different {
		if sameLocation(pair[0], pair[1]) {
			t.Errorf("%q and %q match", pair[0], pair[1])
		}
	}
}

func statOrNil(path string) os.FileInfo {
	info, err := os.Stat(path)
	if err != nil {
		return nil
	}
	return info
}
