package scan

import (
	"context"
	"encoding/json"
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"strings"
)

// packageLayout says where a cache holds the Git repositories
// that Swift Package Manager manages, and which file pins them.
// SwiftPM keeps a clone of each remote dependency in checkouts/<identity>
// and a bare clone in repositories/<name>; "swift package resolve"
// and "xcodebuild -resolvePackageDependencies" make both again.
type packageLayout struct {
	dir string // the directory that holds checkouts and repositories
	// resolved is the Package.resolved file that pins the clones,
	// or "" when there is no pin file to check the clones against.
	resolved string
	// pinsName names resolved in a reason, relative to the project folder.
	pinsName string
	// unpinned, when not "", is why no one Package.resolved pins the clones.
	// checkManaged then refuses any clone.
	unpinned string
}

// swiftPMLayout returns the layout of c when c is a SwiftPM cache:
// .build beside Package.swift, or DerivedData beside Package.swift
// or beside an Xcode workspace or project,
// which holds the same layout in SourcePackages.
// It returns nil for any other cache.
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
	dir := filepath.Join(c.Path, sub)
	if _, err := os.Lstat(filepath.Join(project, "Package.swift")); err == nil {
		return &packageLayout{
			dir:      dir,
			resolved: filepath.Join(project, "Package.resolved"),
			pinsName: "Package.resolved",
		}
	}
	if sub == "" {
		return nil
	}
	return xcodeLayout(project, dir)
}

// xcodeLayout returns the layout of a DerivedData folder, whose clones
// are in dir, beside the Xcode workspaces and projects in project,
// or nil when project has neither.
// Xcode keeps a workspace's pins in
// <name>.xcworkspace/xcshareddata/swiftpm/Package.resolved
// and a project's pins in the workspace inside it, in
// <name>.xcodeproj/project.xcworkspace/xcshareddata/swiftpm/Package.resolved.
// One workspace takes precedence over any projects.
// With more than one workspace, or no workspace and more than one project,
// plonge cannot tell which one the folder belongs to,
// so the layout refuses every clone.
func xcodeLayout(project, dir string) *packageLayout {
	entries, err := os.ReadDir(project)
	if err != nil {
		return &packageLayout{dir: dir, unpinned: "its folder cannot be read for Xcode projects: " + err.Error()}
	}
	var workspaces, projects []string
	for _, e := range entries {
		switch filepath.Ext(e.Name()) {
		case ".xcworkspace":
			workspaces = append(workspaces, e.Name())
		case ".xcodeproj":
			projects = append(projects, e.Name())
		}
	}
	var pinsName string
	switch {
	case len(workspaces) > 1:
		return &packageLayout{dir: dir, unpinned: "its folder holds more than one Xcode workspace, so no one Package.resolved pins it"}
	case len(workspaces) == 1:
		pinsName = filepath.Join(workspaces[0], "xcshareddata", "swiftpm", "Package.resolved")
	case len(projects) > 1:
		return &packageLayout{dir: dir, unpinned: "its folder holds more than one Xcode project and no workspace, so no one Package.resolved pins it"}
	case len(projects) == 1:
		pinsName = filepath.Join(projects[0], "project.xcworkspace", "xcshareddata", "swiftpm", "Package.resolved")
	default:
		return nil
	}
	return &packageLayout{
		dir:      dir,
		resolved: filepath.Join(project, pinsName),
		pinsName: filepath.ToSlash(pinsName),
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
	return guardTree(ctx, c.Path, swiftPMLayout(c), measure)
}

// guardTree measures dir, with measure, and returns why dir must not be deleted,
// or "" when it may be.
// It refuses a Git repository at any depth, as walkCache does,
// except for the repositories that layout lets through,
// which checkManaged then checks with Git. The size of a refused dir is 0.
func guardTree(ctx context.Context, dir string, layout *packageLayout, measure bool) (int64, string) {
	size, reason, repos := walkCache(dir, nil, measure, true, layout)
	if reason == "" && len(repos) > 0 {
		reason = checkManaged(ctx, dir, layout, repos)
	}
	if reason != "" {
		size = 0
	}
	return size, reason
}

// GuardDerivedData measures each folder in dirs, with measure,
// and returns at the same index why the folder must not be deleted, or "".
// Each folder is one that Xcode keeps for a workspace in
// ~/Library/Developer/Xcode/DerivedData, and the caller has found
// that its workspace no longer exists, so there is no Package.resolved
// to check its SwiftPM clones against.
// It refuses a Git repository at any depth and a directory that it cannot read,
// as walkCache describes, except for the clones that Xcode's SwiftPM keeps
// in SourcePackages, which it checks as checkManaged describes,
// without the pin checks.
// The size of a refused folder is 0.
func GuardDerivedData(ctx context.Context, dirs []string, measure bool) ([]int64, []string) {
	sizes := make([]int64, len(dirs))
	reasons := make([]string, len(dirs))
	parallel(len(dirs), func(i int) {
		layout := &packageLayout{dir: filepath.Join(dirs[i], "SourcePackages")}
		sizes[i], reasons[i] = guardTree(ctx, dirs[i], layout, measure)
	})
	return sizes, reasons
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
// When the layout has no Package.resolved, it skips the checks against pins.
func checkManaged(ctx context.Context, dir string, layout *packageLayout, repos []managedRepo) string {
	first := func() string {
		what := "a Git repository"
		if repos[0].bare {
			what = "a Git directory"
		}
		return repoReason(dir, repos[0].path, what)
	}
	if layout.unpinned != "" {
		return first() + ", and " + layout.unpinned
	}
	var pins map[string]pin
	if layout.resolved != "" {
		var err error
		if pins, err = readPins(layout.resolved); err != nil {
			return first() + ", and " + layout.pinsName + " cannot be read: " + err.Error()
		}
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
		if why := checkCheckout(ctx, r.path, pins, mirrors); why != "" {
			return repoReason(dir, r.path, "a Git repository") + " " + why
		}
	}
	return ""
}

// checkMirror returns the bare clone at gitDir, or why it is not one
// that SwiftPM can make again.
// It must be bare, have no linked worktrees, and have one remote, origin,
// set up as a mirror of a location that a pin in Package.resolved names,
// unless pins is nil, which skips that check.
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
	url, err := originURL(g)
	if err != nil {
		return fail(err)
	}
	if pins != nil && !pinned(url, pins) {
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
	if string(out) != "origin\n" {
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

// originURL returns the one URL of the origin remote, byte for byte.
// It is an error when origin has no URL or more than one.
func originURL(g func(args ...string) ([]byte, int, error)) (string, error) {
	out, _, err := g("config", "-z", "--get-all", "remote.origin.url")
	if err != nil {
		return "", err
	}
	values := strings.Split(string(out), "\x00")
	if len(values) != 2 || values[1] != "" {
		return "", errors.New("remote.origin.url must have exactly one value")
	}
	return values[0], nil
}

// schemeURL matches a URL with a scheme, such as https:// or file://.
var schemeURL = regexp.MustCompile(`^[A-Za-z][A-Za-z0-9+.-]*://`)

// isRemoteURL reports whether s is a URL with a scheme,
// or an scp-style address such as git@host:path,
// which Git recognizes by a colon before the first slash.
func isRemoteURL(s string) bool {
	if schemeURL.MatchString(s) {
		return true
	}
	colon := strings.IndexByte(s, ':')
	slash := strings.IndexByte(s, '/')
	return colon > 0 && (slash < 0 || colon < slash)
}

// sameLocation reports whether a clone's origin URL is a pin's location.
// Two absolute paths must resolve, through symlinks, to the same directory.
// Two remote URLs must be equal, except for one trailing slash on either.
// Any other pair fails, including a relative path,
// whose directory depends on where Git runs.
// Any other difference, such as case, white space, or a .git suffix,
// can name a different repository, so plonge refuses the clone
// rather than delete it.
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
	if !isRemoteURL(url) || !isRemoteURL(location) {
		return false
	}
	return strings.TrimSuffix(url, "/") == strings.TrimSuffix(location, "/")
}

// checkCheckout returns why the checkout at dir has work that
// "swift package resolve" would not restore, or "".
// Its HEAD must be the revision that pins holds for its identity,
// the name of dir in lower case, unless pins is nil, which skips that check.
// Its one remote, origin, must be one of mirrors.
// A commit counts as one that SwiftPM can restore only when
// a ref in refs/remotes/origin, a tag with the same object in the mirror,
// or, when HEAD is at its pin, HEAD holds it.
// Without pins, HEAD itself must be a commit that those refs hold. A tag that only the checkout has, a local branch, or a reflog
// does not count, and a push from the checkout to the mirror refuses it.
func checkCheckout(ctx context.Context, dir string, pins map[string]pin, mirrors []mirror) string {
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
	if pins != nil {
		// SwiftPM names the folder after the last element of the location,
		// such as GRDB.swift, and the identity is that name in lower case.
		revision := pins[strings.ToLower(filepath.Base(dir))].revision
		if revision == "" || strings.TrimSpace(string(out)) != strings.ToLower(revision) {
			return "whose HEAD is not a commit that Package.resolved pins for it"
		}
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
	url, err := originURL(g)
	if err != nil {
		return fail(err)
	}
	var m *mirror
	if !filepath.IsAbs(url) {
		// EvalSymlinks would resolve a relative path from plonge's directory.
	} else if real, err := filepath.EvalSymlinks(url); err == nil {
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
	// --all includes HEAD, so without a pin a commit at HEAD that only
	// the checkout holds is found, whether HEAD is detached or not.
	args := []string{"rev-list", "-n", "1", "--all", "--reflog", "--not", "--glob=refs/remotes/origin/*"}
	if pins != nil {
		args = append(args, "HEAD")
	}
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
		if pins == nil {
			return "that has commits that only its HEAD, local branches, tags, or reflogs hold"
		}
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
// The map is never nil, so that checkMirror and checkCheckout check against it.
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
