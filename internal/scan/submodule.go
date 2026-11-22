package scan

import (
	"context"
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
)

// SwiftPM runs "git submodule update --init --recursive" when it makes
// a checkout, so a dependency's submodules are part of the cache.
// Git keeps a submodule's Git directory in the parent repository's
// Git directory, at modules/<name>, and puts a .git file that names it
// in the submodule's work tree.
// "swift package resolve" makes both again when nothing in them is local.

// gitlink is a submodule entry in a commit's tree.
type gitlink struct {
	path string // relative to the work tree, with slashes
	oid  string // the commit that the tree records
}

// module is what .gitmodules records for one submodule.
type module struct {
	name string
	url  string
}

// checkSubmodules checks each submodule that the HEAD of the repository
// at tree, whose Git directory is gitDir, records, and then its submodules.
// The caller has already checked the repository itself.
// It returns the work tree that fails, which is tree or a submodule,
// and why, or "" when SwiftPM can make each submodule again.
// It adds the work tree and the Git directory of each submodule that passes
// to claimed, so that the caller can refuse any other one that the walk found.
//
// A submodule with no .git entry is not initialized;
// its folder must be empty, because git status does not look inside it.
// Any other submodule must pass these checks:
//
//   - .gitmodules at the parent's HEAD gives exactly one name to its path,
//     and its .git file names <gitDir>/modules/<name>.
//   - That Git directory shares no other one, has no linked worktrees,
//     and has a core.worktree that names the submodule's work tree.
//   - Its HEAD is the commit that the parent's HEAD records for its path.
//   - It passes the checks of changesReason, and has no stash.
//   - Its one remote, origin, and submodule.<name>.url in the parent's
//     configuration are the URL in .gitmodules, as sameLocation decides.
//     A relative URL, which Git resolves against the parent's origin,
//     therefore fails.
//   - Every commit is in HEAD or in refs/remotes/origin.
//     A submodule has no bare clone in the cache to compare its tags with,
//     so a tag does not count, and neither does a local branch or a reflog.
func checkSubmodules(ctx context.Context, tree, gitDir string, claimed map[string]bool) (string, string) {
	g := workTreeGit(ctx, tree, gitDir)
	links, err := gitlinks(g)
	if err != nil {
		return tree, gitFail(err)
	}
	var modules map[string][]module // by path; read when a submodule needs it
	for _, l := range links {
		rel := filepath.FromSlash(l.path)
		sub := filepath.Join(tree, rel)
		if !filepath.IsLocal(rel) {
			return tree, "that has a submodule at a path outside it"
		}
		switch ok, err := plainDir(tree, rel); {
		case errors.Is(err, fs.ErrNotExist):
			continue
		case err != nil:
			return tree, "whose submodule folder " + l.path + " cannot be checked: " + err.Error()
		case !ok:
			return tree, "whose submodule folder " + l.path + " is not a directory"
		}
		dotGit := filepath.Join(sub, ".git")
		info, err := os.Lstat(dotGit)
		if errors.Is(err, fs.ErrNotExist) {
			entries, err := os.ReadDir(sub)
			if err != nil {
				return tree, "whose submodule folder " + l.path + " cannot be checked: " + err.Error()
			}
			if len(entries) > 0 {
				return tree, "that has files in " + l.path + ", the folder of a submodule that is not initialized"
			}
			continue
		}
		if err != nil {
			return sub, "that cannot be checked: " + err.Error()
		}
		if !info.Mode().IsRegular() {
			return sub, "whose .git entry is not a file"
		}

		if modules == nil {
			if modules, err = readModules(g); err != nil {
				return tree, gitFail(err)
			}
		}
		named := modules[l.path]
		if len(named) != 1 || named[0].url == "" {
			return sub, "that .gitmodules does not give exactly one name and URL"
		}
		m := named[0]
		nameRel := filepath.FromSlash(m.name)
		modulesDir := filepath.Join(gitDir, "modules")
		subGitDir := filepath.Join(modulesDir, nameRel)
		if !filepath.IsLocal(nameRel) {
			return sub, "whose name in .gitmodules is not a path in the Git directory above it"
		}
		if ok, err := plainDir(modulesDir, nameRel); err != nil || !ok {
			return sub, "that has no Git directory for its name in the Git directory above it"
		}
		if !namesDir(dotGit, sub, subGitDir) {
			return sub, "whose .git file does not name its Git directory in the Git directory above it"
		}
		if sharesGitDir(subGitDir) {
			return sub, "whose Git directory shares another one"
		}
		if why := worktreesReason(subGitDir); why != "" {
			return sub, why
		}

		sg := workTreeGit(ctx, sub, subGitDir)
		// Git finds the work tree of a submodule through core.worktree.
		// A value that names another folder means that the Git directory
		// may hold the index of work that is not in this cache.
		worktree, err := oneValue(sg, "core.worktree")
		if err != nil {
			return sub, gitFail(err)
		}
		if !filepath.IsAbs(worktree) {
			worktree = filepath.Join(subGitDir, worktree)
		}
		if !sameDir(worktree, sub) {
			return sub, "whose Git directory names another work tree"
		}

		if why := stashReason(sg); why != "" {
			return sub, why
		}
		out, _, err := sg("rev-parse", "--verify", "HEAD^{commit}")
		if err != nil {
			return sub, gitFail(err)
		}
		if strings.TrimSpace(string(out)) != l.oid {
			return sub, "whose HEAD is not the commit that the repository above it records"
		}
		if why := changesReason(sg); why != "" {
			return sub, why
		}

		if why, err := originReason(sg); err != nil {
			return sub, gitFail(err)
		} else if why != "" {
			return sub, why
		}
		url, err := originURL(sg)
		if err != nil {
			return sub, gitFail(err)
		}
		if !sameLocation(url, m.url) {
			return sub, "whose origin is not the URL that .gitmodules gives it"
		}
		// "git submodule update" clones from this value, not from .gitmodules.
		url, err = oneValue(g, "--local", "submodule."+m.name+".url")
		if err != nil {
			return sub, gitFail(err)
		}
		if !sameLocation(url, m.url) {
			return sub, "whose URL in the configuration of the repository above it is not the URL that .gitmodules gives it"
		}

		if why := pushReason(sg); why != "" {
			return sub, why + " to its origin"
		}
		out, _, err = sg("rev-list", "-n", "1", "--all", "--reflog", "--not", "--glob=refs/remotes/origin/*", "HEAD")
		if err != nil {
			return sub, gitFail(err)
		}
		if len(out) > 0 {
			return sub, "that has commits that only its local branches, tags, or reflogs hold"
		}

		claimed[sub], claimed[subGitDir] = true, true
		if path, why := checkSubmodules(ctx, sub, subGitDir, claimed); why != "" {
			return path, why
		}
	}
	return "", ""
}

// gitlinks returns the submodule entries in the tree of HEAD.
func gitlinks(g gitFunc) ([]gitlink, error) {
	out, _, err := g("ls-tree", "-r", "-z", "HEAD")
	if err != nil {
		return nil, err
	}
	var links []gitlink
	for _, entry := range splitNUL(out) {
		// Each entry is "<mode> <type> <object>\t<path>".
		meta, path, ok := strings.Cut(entry, "\t")
		fields := strings.Fields(meta)
		if !ok || len(fields) != 3 {
			return nil, errors.New("unexpected output from git ls-tree")
		}
		if fields[0] == "160000" {
			links = append(links, gitlink{path: path, oid: fields[2]})
		}
	}
	return links, nil
}

// readModules returns what .gitmodules in the tree of HEAD records,
// by submodule path. A path has more than one module when
// two names have it, and a module's URL is "" when .gitmodules gives
// its name no URL or more than one.
func readModules(g gitFunc) (map[string][]module, error) {
	out, _, err := g("config", "-z", "--blob", "HEAD:.gitmodules", "--list")
	if err != nil {
		return nil, err
	}
	paths := map[string][]string{}
	urls := map[string][]string{}
	for _, entry := range splitNUL(out) {
		// Each entry is "<key>\n<value>". Git writes the section
		// and the variable in lower case and keeps the case of the name.
		key, value, _ := strings.Cut(entry, "\n")
		rest, ok := strings.CutPrefix(key, "submodule.")
		if !ok {
			continue
		}
		if name, ok := strings.CutSuffix(rest, ".path"); ok {
			paths[name] = append(paths[name], value)
		} else if name, ok := strings.CutSuffix(rest, ".url"); ok {
			urls[name] = append(urls[name], value)
		}
	}
	modules := map[string][]module{}
	for name, ps := range paths {
		m := module{name: name}
		if len(urls[name]) == 1 {
			m.url = urls[name][0]
		}
		for _, p := range ps {
			modules[p] = append(modules[p], m)
		}
	}
	return modules, nil
}

// oneValue returns the one value of a configuration variable, byte for byte.
// args are options for git config and then the variable's name.
// It is an error when the variable has no value or more than one.
func oneValue(g gitFunc, args ...string) (string, error) {
	out, _, err := g(append([]string{"config", "-z", "--get-all"}, args...)...)
	if err != nil {
		return "", err
	}
	values := strings.Split(string(out), "\x00")
	if len(values) != 2 || values[1] != "" {
		return "", errors.New(args[len(args)-1] + " must have exactly one value")
	}
	return values[0], nil
}

// plainDir reports whether rel, below base, is a directory
// that no symlink leads to: each element of rel must be a directory itself.
// The error is fs.ErrNotExist when an element does not exist.
func plainDir(base, rel string) (bool, error) {
	path := base
	for elem := range strings.SplitSeq(rel, string(filepath.Separator)) {
		path = filepath.Join(path, elem)
		info, err := os.Lstat(path)
		if err != nil {
			return false, err
		}
		if !info.IsDir() {
			return false, nil
		}
	}
	return true, nil
}

// namesDir reports whether the .git file dotGit, in the work tree sub,
// holds one line, "gitdir: <path>", that names the directory gitDir.
// Git resolves a relative path from the work tree.
func namesDir(dotGit, sub, gitDir string) bool {
	data, err := os.ReadFile(dotGit)
	if err != nil {
		return false
	}
	path, ok := strings.CutPrefix(string(data), "gitdir: ")
	path, hasNewline := strings.CutSuffix(path, "\n")
	if !ok || !hasNewline || path == "" || strings.ContainsAny(path, "\n\r\x00") {
		return false
	}
	if !filepath.IsAbs(path) {
		path = filepath.Join(sub, path)
	}
	return sameDir(path, gitDir)
}

// sameDir reports whether two paths resolve, through symlinks,
// to the same existing directory.
func sameDir(a, b string) bool {
	a, err := filepath.EvalSymlinks(a)
	if err != nil {
		return false
	}
	b, err = filepath.EvalSymlinks(b)
	if err != nil {
		return false
	}
	ai, err := os.Stat(a)
	if err != nil || !ai.IsDir() {
		return false
	}
	bi, err := os.Stat(b)
	return err == nil && os.SameFile(ai, bi)
}
