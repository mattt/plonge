package scan

import (
	"io/fs"
	"os"
	"path/filepath"
	"strconv"
	"strings"
)

type inode struct{ dev, ino uint64 }

// DiskUsage returns the bytes allocated to dir and everything under it,
// as du counts them: allocated blocks, each hard-linked file once,
// and symlinks not followed.
func DiskUsage(dir string) int64 {
	n, _ := walkCache(dir, true, false)
	return n
}

// Sizes measures each directory with at most Workers walks at a time.
func Sizes(dirs []string) []int64 {
	out := make([]int64, len(dirs))
	parallel(len(dirs), func(i int) {
		out[i] = DiskUsage(dirs[i])
	})
	return out
}

// Measure sizes the candidates of each root and refuses the ones
// that hold a Git repository, in the same walk,
// or that hold an entry at a refuse_inside path.
// It moves a refused candidate from Candidates to Refused,
// and returns one size slice per root, in the order of the remaining candidates.
func Measure(roots []Root) [][]int64 {
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
			bytes[k], reasons[k] = walkCache(c.Path, true, true)
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

// gitRefusal walks dir and returns why it must not be deleted
// because it is or holds a Git repository, or "" when it does not.
func gitRefusal(dir string) string {
	_, reason := walkCache(dir, false, true)
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
// With measure, it adds up the bytes allocated, as DiskUsage describes.
// With guard, it stops at the first Git repository and returns why dir
// must not be deleted: a .git entry of any type at any depth,
// a Git directory (HEAD beside objects and refs) at any depth,
// or a directory that it cannot read, which could hide either.
func walkCache(dir string, measure, guard bool) (int64, string) {
	var total int64
	var reason string
	seen := map[inode]bool{}
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
		if guard {
			if r := gitReason(dir, path, d); r != "" {
				reason = r
				return fs.SkipAll
			}
		}
		if !measure {
			return nil
		}
		info, err := d.Info()
		if err != nil {
			return nil
		}
		bytes, id, linked := allocated(info)
		if linked {
			if seen[id] {
				return nil
			}
			seen[id] = true
		}
		total += bytes
		return nil
	})
	return total, reason
}

// gitReason returns why the entry at path makes dir a Git repository
// or one that holds a Git repository, or "" when it does not.
func gitReason(dir, path string, d fs.DirEntry) string {
	switch d.Name() {
	case ".git":
		return repoReason(dir, filepath.Dir(path), "a Git repository")
	case "HEAD":
		if d.IsDir() {
			return ""
		}
		parent := filepath.Dir(path)
		if isDir(filepath.Join(parent, "objects")) && isDir(filepath.Join(parent, "refs")) {
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
