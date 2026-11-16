package scan

import (
	"context"
	"io/fs"
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
)

type inode struct{ dev, ino uint64 }

// DiskUsage returns the bytes that deleting dir and everything under it
// would free, as well as plonge can tell: allocated blocks,
// each hard-linked file once, and symlinks not followed.
// On macOS, a file that shares blocks with clones counts only the blocks
// that it does not share, unless every clone in its family is under dir.
// It leaves out the entries directly in dir that keep names.
func DiskUsage(dir string, keep ...string) int64 {
	n, _, _ := walkCache(dir, keep, true, false, nil)
	return n
}

// Sizes measures each directory with at most Workers walks at a time.
func Sizes(dirs []string) []int64 {
	sizes, _ := GuardedSizes(dirs, nil, nil)
	return sizes
}

// GuardedSizes measures each directory as Sizes does.
// For each directory whose guard is true, the same walk also looks for
// a Git repository, as GitRefusal describes, and returns the reason
// at the same index; the size of a refused directory is 0.
// A nil guard checks no directory.
// Each walk leaves out the entries directly in dirs[i] that keep[i] names;
// keep can be nil.
func GuardedSizes(dirs []string, guard []bool, keep [][]string) ([]int64, []string) {
	sizes := make([]int64, len(dirs))
	reasons := make([]string, len(dirs))
	parallel(len(dirs), func(i int) {
		g := guard != nil && guard[i]
		var k []string
		if keep != nil {
			k = keep[i]
		}
		sizes[i], reasons[i], _ = walkCache(dirs[i], k, true, g, nil)
		if reasons[i] != "" {
			sizes[i] = 0
		}
	})
	return sizes, reasons
}

// Measure sizes the candidates of each root and refuses the ones
// that hold a Git repository, in the same walk,
// or that hold an entry at a refuse_inside path.
// In a SwiftPM cache, it lets through the repositories that SwiftPM manages
// and can make again, as guardCache describes.
// It moves a refused candidate from Candidates to Refused,
// and returns one size slice per root, in the order of the remaining candidates.
func Measure(ctx context.Context, roots []Root) [][]int64 {
	type job struct{ root, cand int }
	var jobs []job
	for i, r := range roots {
		for j := range r.Candidates {
			jobs = append(jobs, job{i, j})
		}
	}
	bytes := make([]int64, len(jobs))
	reasons := make([]string, len(jobs))
	parallel(len(jobs), func(k int) {
		c := roots[jobs[k].root].Candidates[jobs[k].cand]
		if reasons[k] = insideRefusal(c); reasons[k] == "" {
			bytes[k], reasons[k] = guardCache(ctx, c, true)
		}
	})
	sizes := make([][]int64, len(roots))
	k := 0
	for i := range roots {
		r := &roots[i]
		var kept []Candidate
		for _, c := range r.Candidates {
			if reasons[k] != "" {
				r.Refused = append(r.Refused, Refusal{Path: c.Path, Project: r.Path, Rule: c.Rule, Reason: reasons[k]})
			} else {
				kept = append(kept, c)
				sizes[i] = append(sizes[i], bytes[k])
			}
			k++
		}
		r.Candidates = kept
	}
	return sizes
}

// GitRefusal walks dir and returns why it must not be deleted
// because it is or holds a Git repository, or "" when it does not.
// It refuses a .git entry of any type or a Git directory at any depth,
// and a directory that it cannot read, as walkCache describes.
// It does not look inside the entries directly in dir that keep names.
func GitRefusal(dir string, keep ...string) string {
	_, reason, _ := walkCache(dir, keep, false, true, nil)
	return reason
}

// insideRefusal returns why c must not be deleted because it holds an entry
// at one of its refuse_inside paths, or "" when it holds none.
// It does not follow symlinks to directories, and it refuses c
// when it cannot read a directory that a path passes through.
func insideRefusal(c Candidate) string {
	for _, p := range c.refuse {
		found, err := matchInside(c.Path, strings.Split(p, "/"))
		if err != nil {
			return "cannot check for " + p + ": " + err.Error()
		}
		if found != "" {
			return "contains " + found + ", which refuse_inside " + strconv.Quote(p) + " protects"
		}
	}
	return ""
}

// matchInside returns the first entry below dir, as a slash-separated path
// relative to dir, whose path elements match elems, or "" when none does.
func matchInside(dir string, elems []string) (string, error) {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return "", err
	}
	for _, e := range entries {
		if ok, _ := filepath.Match(elems[0], e.Name()); !ok {
			continue
		}
		if len(elems) == 1 {
			return e.Name(), nil
		}
		if !e.IsDir() {
			continue
		}
		rest, err := matchInside(filepath.Join(dir, e.Name()), elems[1:])
		if err != nil || rest != "" {
			return e.Name() + "/" + rest, err
		}
	}
	return "", nil
}

// walkCache walks dir without following symlinks.
// It leaves out the entries directly in dir that keep names,
// which the caller does not delete.
// With measure, it adds up the bytes, as DiskUsage describes.
// With guard, it stops at the first Git repository and returns why dir
// must not be deleted: a .git entry of any type at any depth,
// a Git directory (HEAD beside objects and refs) at any depth,
// or a directory that it cannot read, which could hide either.
// With a layout, it does not refuse the repositories that SwiftPM manages:
// a .git directory directly in a checkout, and a Git directory directly
// in repositories. It returns them so that the caller can check them with Git.
func walkCache(dir string, keep []string, measure, guard bool, layout *packageLayout) (int64, string, []managedRepo) {
	var t tally
	var reason string
	var repos []managedRepo
	_ = filepath.WalkDir(dir, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			if guard {
				reason = "cannot check for Git repositories: " + err.Error()
				return fs.SkipAll
			}
			if d != nil && d.IsDir() && path != dir {
				return fs.SkipDir
			}
			return nil
		}
		if len(keep) > 0 && path != dir && filepath.Dir(path) == filepath.Clean(dir) && slices.Contains(keep, d.Name()) {
			if d.IsDir() {
				return fs.SkipDir
			}
			return nil
		}
		if guard {
			if r := gitReason(dir, path, d, layout, &repos); r != "" {
				reason = r
				return fs.SkipAll
			}
		}
		if measure {
			t.add(path, d)
		}
		return nil
	})
	return t.sum(), reason, repos
}

// fileSize is what deleting one file would free, as entrySize measures it.
type fileSize struct {
	bytes  int64 // the bytes that only this file uses
	id     inode
	linked bool // the file has more than one hard link
	// clones is the number of files in the file's clone family,
	// itself included, and shared is the bytes that they share.
	// clones is 0 when the file shares no blocks that plonge can count.
	family cloneFamily
	clones uint32
	shared int64
}

// cloneFamily identifies files that are clones of one another.
type cloneFamily struct{ dev, id uint64 }

type family struct {
	found, clones uint32
	shared        int64
}

// tally adds up the sizes of the files in one walk.
// It counts a hard-linked file once, and the blocks that a clone family
// shares once, when the walk finds every file in the family.
type tally struct {
	total    int64
	seen     map[inode]bool
	families map[cloneFamily]*family
}

func (t *tally) add(path string, d fs.DirEntry) {
	f, ok := entrySize(path, d)
	if !ok {
		return
	}
	if f.linked {
		if t.seen[f.id] {
			return
		}
		if t.seen == nil {
			t.seen = map[inode]bool{}
		}
		t.seen[f.id] = true
	}
	t.total += f.bytes
	if f.clones < 2 || f.shared <= 0 {
		return
	}
	if t.families == nil {
		t.families = map[cloneFamily]*family{}
	}
	fam := t.families[f.family]
	if fam == nil {
		fam = &family{clones: f.clones}
		t.families[f.family] = fam
	}
	fam.found++
	fam.shared = max(fam.shared, f.shared)
}

// sum returns the total, with the shared blocks of each clone family
// whose files are all in the walk.
func (t *tally) sum() int64 {
	n := t.total
	for _, fam := range t.families {
		if fam.found >= fam.clones {
			n += fam.shared
		}
	}
	return n
}

// statSize measures a file from its lstat information.
func statSize(d fs.DirEntry) (fileSize, bool) {
	info, err := d.Info()
	if err != nil {
		return fileSize{}, false
	}
	bytes, id, linked := allocated(info)
	return fileSize{bytes: bytes, id: id, linked: linked}, true
}

// gitReason returns why the entry at path makes dir a Git repository
// or one that holds a Git repository, or "" when it does not.
// It adds to repos the repositories that it lets through for layout.
func gitReason(dir, path string, d fs.DirEntry, layout *packageLayout, repos *[]managedRepo) string {
	switch d.Name() {
	case ".git":
		repo := filepath.Dir(path)
		// A .git file or symlink can point at a repository outside the cache.
		if d.IsDir() && layout.isCheckout(repo) {
			*repos = append(*repos, managedRepo{path: repo})
			return ""
		}
		return repoReason(dir, repo, "a Git repository")
	case "HEAD":
		if d.IsDir() {
			return ""
		}
		parent := filepath.Dir(path)
		if isDir(filepath.Join(parent, "objects")) && isDir(filepath.Join(parent, "refs")) {
			// The walk reaches a checkout's .git directory only after
			// it has let the checkout through.
			if filepath.Base(parent) == ".git" && layout.isCheckout(filepath.Dir(parent)) {
				return ""
			}
			if layout.isMirror(parent) {
				*repos = append(*repos, managedRepo{path: parent, bare: true})
				return ""
			}
			return repoReason(dir, parent, "a Git directory")
		}
	}
	return ""
}

func repoReason(dir, repo, what string) string {
	if repo == dir {
		return "is " + what
	}
	rel, err := filepath.Rel(dir, repo)
	if err != nil {
		rel = repo
	}
	return "contains " + what + " at " + filepath.ToSlash(rel)
}

func isDir(path string) bool {
	info, err := os.Lstat(path)
	return err == nil && info.IsDir()
}
