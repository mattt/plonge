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

// pin is what Package.resolved records for one package.
type pin struct {
	location string // the URL or path that SwiftPM clones
	revision string
}

// mirror is a bare clone that checkMirror has accepted.
type mirror struct {
	real string            // the path with symlinks resolved
	tags map[string]string // object ID by tag ref name
}

// checkManaged returns why one of repos would lose work if plonge deleted it,
// or "" when SwiftPM can make each one again.
// It checks the bare clones first, as checkMirror describes,
// because a checkout trusts only the tags that its bare clone also holds,
// then each checkout, as checkCheckout describes.
func checkManaged(ctx context.Context, dir string, layout *packageLayout, repos []managedRepo) string {
	pins, err := readPins(layout.resolved)
	if err != nil {
		what := "a Git repository"
		if repos[0].bare {
			what = "a Git directory"
		}
		return repoReason(dir, repos[0].path, what) + ", and Package.resolved cannot be read: " + err.Error()
	}
	var mirrors []mirror
	for _, r := range repos {
		if !r.bare {
			continue
		}
		m, why := checkMirror(ctx, r.path, pins)
		if why != "" {
			return repoReason(dir, r.path, "a Git directory") + " " + why
		}
		mirrors = append(mirrors, m)
	}
	for _, r := range repos {
		if r.bare {
			continue
		}
		if why := checkCheckout(ctx, r.path, pins[filepath.Base(r.path)].revision, mirrors); why != "" {
			return repoReason(dir, r.path, "a Git repository") + " " + why
		}
	}
	return ""
}

// checkMirror returns the bare clone at gitDir, or why it is not one
// that SwiftPM can make again.
// It must be bare, have no linked worktrees, and have one remote, origin,
// set up as a mirror of a location that a pin in Package.resolved names.
// No commit may be held only by its reflogs.
// Every ref in such a mirror came from a fetch or a push,
// and a push leaves no record in a bare repository,
// so a commit pushed into it from outside the cache is not detected.
func checkMirror(ctx context.Context, gitDir string, pins map[string]pin) (mirror, string) {
	g := func(args ...string) ([]byte, int, error) {
		return git(ctx, gitDir, nil, append([]string{"--git-dir=" + gitDir}, args...)...)
	}
	fail := func(err error) (mirror, string) { return mirror{}, "that Git cannot check: " + err.Error() }

	out, _, err := g("rev-parse", "--is-bare-repository")
	if err != nil {
		return fail(err)
	}
	if strings.TrimSpace(string(out)) != "true" {
		return mirror{}, "that is not bare"
	}
	if why := worktreesReason(gitDir); why != "" {
		return mirror{}, why
	}
	if why, err := originReason(g); err != nil {
		return fail(err)
	} else if why != "" {
		return mirror{}, why
	}
	out, _, err = g("config", "--bool", "--get", "remote.origin.mirror")
	if err != nil || strings.TrimSpace(string(out)) != "true" {
		return mirror{}, "that is not a mirror of its origin"
	}
	out, _, err = g("config", "--get", "remote.origin.url")
	if err != nil {
		return fail(err)
	}
	if !pinned(strings.TrimSpace(string(out)), pins) {
		return mirror{}, "whose origin no pin in Package.resolved names"
	}
	out, _, err = g("rev-list", "-n", "1", "--reflog", "--not", "--all")
	if err != nil {
		return fail(err)
	}
	if len(out) > 0 {
		return mirror{}, "that has commits that only its reflogs hold"
	}
	tags, err := tagRefs(g)
	if err != nil {
		return fail(err)
	}
	real, err := filepath.EvalSymlinks(gitDir)
	if err != nil {
		return fail(err)
	}
	return mirror{real: real, tags: tags}, ""
}

// originReason returns why a repository does not have exactly one remote, origin.
func originReason(g func(args ...string) ([]byte, int, error)) (string, error) {
	out, _, err := g("remote")
	if err != nil {
		return "", err
	}
	if strings.TrimSpace(string(out)) != "origin" {
		return "that does not have exactly one remote, origin", nil
	}
	return "", nil
}

// tagRefs returns the object ID of each tag ref, by ref name.
func tagRefs(g func(args ...string) ([]byte, int, error)) (map[string]string, error) {
	out, _, err := g("for-each-ref", "--format=%(objectname) %(refname)", "refs/tags")
	if err != nil {
		return nil, err
	}
	tags := map[string]string{}
	for line := range strings.SplitSeq(strings.TrimSpace(string(out)), "\n") {
		if oid, name, ok := strings.Cut(line, " "); ok {
			tags[name] = oid
		}
	}
	return tags, nil
}

// pinned reports whether url names the same package as a pin's location,
// as sameLocation decides.
func pinned(url string, pins map[string]pin) bool {
	for _, p := range pins {
		if sameLocation(url, p.location) {
			return true
		}
	}
	return false
}

// sameLocation reports whether a clone's origin URL is a pin's location.
// Two absolute paths must resolve, through symlinks, to the same directory.
// Anything else must be equal, except for one trailing slash on either.
// Any other difference, such as case or a .git suffix, can name
// a different repository, so plonge refuses the clone rather than delete it.
func sameLocation(url, location string) bool {
	if filepath.IsAbs(url) && filepath.IsAbs(location) {
		a, err := filepath.EvalSymlinks(url)
		if err != nil {
			return false
		}
		b, err := filepath.EvalSymlinks(location)
		if err != nil {
			return false
		}
		ai, err := os.Stat(a)
		if err != nil {
			return false
		}
		bi, err := os.Stat(b)
		return err == nil && os.SameFile(ai, bi)
	}
	return url != "" && strings.TrimSuffix(url, "/") == strings.TrimSuffix(location, "/")
}

// checkCheckout returns why the checkout at dir has work that
// "swift package resolve" would not restore, or "".
// revision is the one that Package.resolved pins for it, or "" when none does.
// Its one remote, origin, must be one of mirrors.
// A commit counts as one that SwiftPM can restore only when HEAD,
// a ref in refs/remotes/origin, or a tag with the same object in the mirror
// holds it. A tag that only the checkout has, a local branch, or a reflog
// does not count, and a push from the checkout to the mirror refuses it.
func checkCheckout(ctx context.Context, dir, revision string, mirrors []mirror) string {
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
	if revision == "" || strings.TrimSpace(string(out)) != strings.ToLower(revision) {
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

	if why, err := originReason(g); err != nil {
		return fail(err)
	} else if why != "" {
		return why
	}
	out, _, err = g("config", "--get", "remote.origin.url")
	if err != nil {
		return fail(err)
	}
	var m *mirror
	if real, err := filepath.EvalSymlinks(strings.TrimSpace(string(out))); err == nil {
		for i := range mirrors {
			if mirrors[i].real == real {
				m = &mirrors[i]
			}
		}
	}
	if m == nil {
		return "whose origin is not a SwiftPM clone in this cache"
	}

	// A push to origin updates a remote-tracking ref, which then holds
	// a commit that the dependency's source may not have.
	out, _, err = g("log", "-g", "--format=%gs", "--glob=refs/remotes/origin/*")
	if err != nil {
		return fail(err)
	}
	for line := range strings.SplitSeq(string(out), "\n") {
		if strings.HasPrefix(line, "update by push") {
			return "that has pushed commits to its SwiftPM clone"
		}
	}

	tags, err := tagRefs(g)
	if err != nil {
		return fail(err)
	}
	args := []string{"rev-list", "-n", "1", "--all", "--reflog", "--not", "HEAD", "--glob=refs/remotes/origin/*"}
	for name, oid := range tags {
		if m.tags[name] == oid {
			args = append(args, oid)
		}
	}
	out, _, err = g(args...)
	if err != nil {
		return fail(err)
	}
	if len(out) > 0 {
		return "that has commits that only its local branches, tags, or reflogs hold"
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

// readPins returns what a Package.resolved file pins for each package identity.
// It reads versions 2 and 3, which key pins by identity.
func readPins(path string) (map[string]pin, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	var f struct {
		Version int `json:"version"`
		Pins    []struct {
			Identity string `json:"identity"`
			Location string `json:"location"`
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
	pins := make(map[string]pin, len(f.Pins))
	for _, p := range f.Pins {
		if p.Identity != "" && p.State.Revision != "" {
			pins[p.Identity] = pin{location: p.Location, revision: p.State.Revision}
		}
	}
	return pins, nil
}
