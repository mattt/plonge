package scan

import (
	"context"
	"encoding/json"
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
)

// packageLayout says where a cache holds the Git repositories
// that Swift Package Manager manages, and which file pins them.
// SwiftPM keeps a clone of each remote dependency in checkouts/<identity>
// and a bare clone in repositories/<name>; "swift package resolve"
// and "xcodebuild -resolvePackageDependencies" make both again.
type packageLayout struct {
	dir      string // the directory that holds checkouts and repositories
	resolved string // the Package.resolved file beside Package.swift
}

// swiftPMLayout returns the layout of c when c is a SwiftPM cache:
// .build beside Package.swift, or DerivedData beside Package.swift,
// which holds the same layout in SourcePackages.
// It returns nil for any other cache.
// A DerivedData folder beside an Xcode project or workspace is left out,
// because its pins are in a file inside the project or workspace,
// and a folder can hold several of them.
func swiftPMLayout(c Candidate) *packageLayout {
	var sub string
	switch c.key {
	case ".build":
	case "DerivedData":
		sub = "SourcePackages"
	default:
		return nil
	}
	project := filepath.Dir(c.Path)
	if _, err := os.Lstat(filepath.Join(project, "Package.swift")); err != nil {
		return nil
	}
	return &packageLayout{
		dir:      filepath.Join(c.Path, sub),
		resolved: filepath.Join(project, "Package.resolved"),
	}
}

// isCheckout reports whether repo is directly in the checkouts directory.
func (l *packageLayout) isCheckout(repo string) bool {
	return l != nil && filepath.Dir(repo) == filepath.Join(l.dir, "checkouts")
}

// isMirror reports whether gitDir is directly in the repositories directory.
func (l *packageLayout) isMirror(gitDir string) bool {
	return l != nil && filepath.Dir(gitDir) == filepath.Join(l.dir, "repositories")
}

// managedRepo is a repository that the walk let through
// because SwiftPM manages it. Git must still check it.
type managedRepo struct {
	path string // the work tree of a checkout, or the bare clone
	bare bool
}

// guardCache measures c, with measure, and returns why c must not be deleted,
// or "" when it may be.
// It refuses a Git repository at any depth, as walkCache does,
// except for the repositories that SwiftPM manages in a SwiftPM cache,
// which checkManaged then checks with Git.
func guardCache(ctx context.Context, c Candidate, measure bool) (int64, string) {
	layout := swiftPMLayout(c)
	size, reason, repos := walkCache(c.Path, measure, true, layout)
	if reason == "" && len(repos) > 0 {
		reason = checkManaged(ctx, c.Path, layout, repos)
	}
	if reason != "" {
		size = 0
	}
	return size, reason
}

// checkManaged returns why one of repos would lose work if plonge deleted it,
// or "" when SwiftPM can make each one again.
// A bare clone must be bare and have no linked worktrees.
// A checkout must have no linked worktrees and no stash,
// be at the commit that Package.resolved pins for it,
// have no changed, untracked, or ignored files,
// and have no commits that only its local branches or reflogs hold.
func checkManaged(ctx context.Context, dir string, layout *packageLayout, repos []managedRepo) string {
	var pins map[string]string
	var pinErr error
	for _, r := range repos {
		if r.bare {
			if why := checkMirror(ctx, r.path); why != "" {
				return repoReason(dir, r.path, "a Git directory") + " " + why
			}
			continue
		}
		if pins == nil && pinErr == nil {
			pins, pinErr = readPins(layout.resolved)
		}
		if pinErr != nil {
			return repoReason(dir, r.path, "a Git repository") + ", and Package.resolved cannot be read: " + pinErr.Error()
		}
		if why := checkCheckout(ctx, r.path, pins[filepath.Base(r.path)]); why != "" {
			return repoReason(dir, r.path, "a Git repository") + " " + why
		}
	}
	return ""
}

// checkMirror returns why the bare clone at gitDir is not one
// that SwiftPM can make again, or "".
func checkMirror(ctx context.Context, gitDir string) string {
	out, _, err := git(ctx, gitDir, nil, "--git-dir="+gitDir, "rev-parse", "--is-bare-repository")
	if err != nil {
		return "that Git cannot check: " + err.Error()
	}
	if strings.TrimSpace(string(out)) != "true" {
		return "that is not bare"
	}
	return worktreesReason(gitDir)
}

// checkCheckout returns why the checkout at dir has work that
// "swift package resolve" would not restore, or "".
// pin is the revision that Package.resolved pins for it, or "" when none does.
func checkCheckout(ctx context.Context, dir, pin string) string {
	gitDir := filepath.Join(dir, ".git")
	if why := worktreesReason(gitDir); why != "" {
		return why
	}
	// Name the repository so that Git never finds another one above it.
	g := func(args ...string) ([]byte, int, error) {
		return git(ctx, dir, nil, append([]string{"--git-dir=" + gitDir, "--work-tree=" + dir}, args...)...)
	}
	fail := func(err error) string { return "that Git cannot check: " + err.Error() }

	_, code, err := g("rev-parse", "--verify", "--quiet", "refs/stash")
	switch {
	case err == nil:
		return "that has a stash"
	case code != 1:
		return fail(err)
	}

	out, _, err := g("rev-parse", "--verify", "HEAD^{commit}")
	if err != nil {
		return fail(err)
	}
	if pin == "" || strings.TrimSpace(string(out)) != strings.ToLower(pin) {
		return "whose HEAD is not a commit that Package.resolved pins for it"
	}

	// Set every option that would hide a change, in case configuration changes it.
	out, _, err = g("status", "--porcelain=v1", "-z", "--untracked-files=all",
		"--ignored=matching", "--ignore-submodules=none")
	if err != nil {
		return fail(err)
	}
	if len(out) > 0 {
		return "that has changed, untracked, or ignored files"
	}

	// assume-unchanged and skip-worktree hide changes from git status.
	out, _, err = g("ls-files", "-v", "-z")
	if err != nil {
		return fail(err)
	}
	for _, f := range splitNUL(out) {
		if tag := f[0]; tag == 'S' || ('a' <= tag && tag <= 'z') {
			return "that has files marked assume-unchanged or skip-worktree"
		}
	}

	// A commit made in the checkout is in a local branch or a reflog,
	// and the clone that SwiftPM fetches from does not have it.
	out, _, err = g("rev-list", "-n", "1", "--all", "--reflog", "--not", "--remotes", "--tags", "HEAD")
	if err != nil {
		return fail(err)
	}
	if len(out) > 0 {
		return "that has commits that no remote branch or tag holds"
	}
	return ""
}

// worktreesReason returns why a repository has linked worktrees, which
// would lose their Git data with it, or "" when gitDir has no worktrees entry.
func worktreesReason(gitDir string) string {
	if _, err := os.Lstat(filepath.Join(gitDir, "worktrees")); err == nil {
		return "that has linked worktrees"
	} else if !errors.Is(err, fs.ErrNotExist) {
		return "that cannot be checked for linked worktrees: " + err.Error()
	}
	return ""
}

// readPins returns the revision that a Package.resolved file
// pins for each package identity.
// It reads versions 2 and 3, which key pins by identity.
func readPins(path string) (map[string]string, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	var f struct {
		Version int `json:"version"`
		Pins    []struct {
			Identity string `json:"identity"`
			State    struct {
				Revision string `json:"revision"`
			} `json:"state"`
		} `json:"pins"`
	}
	if err := json.Unmarshal(data, &f); err != nil {
		return nil, err
	}
	if f.Version != 2 && f.Version != 3 {
		return nil, errors.New("unsupported version")
	}
	pins := make(map[string]string, len(f.Pins))
	for _, p := range f.Pins {
		if p.Identity != "" && p.State.Revision != "" {
			pins[p.Identity] = p.State.Revision
		}
	}
	return pins, nil
}
