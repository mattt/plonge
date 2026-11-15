package global

import (
	"context"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"strings"

	"github.com/mattt/plonge/internal/clean"
	"github.com/mattt/plonge/internal/scan"
)

// A rule with delete_contents has no cleanup command,
// so plonge deletes what is in the rule's path itself.
// It keeps the directory and refuses a path that could hold more than a cache.

// deleteAction is the Command of a rule that deletes the contents of path.
func (r Runner) deleteAction(path string) string {
	return fmt.Sprintf("delete contents of %s (built in)", r.tilde(path))
}

// refuseDelete returns why plonge must not delete the contents of path,
// or an empty reason.
func (r Runner) refuseDelete(ctx context.Context, path string) string {
	if !filepath.IsAbs(path) {
		return "path is not absolute"
	}
	path = filepath.Clean(path)
	if path == string(filepath.Separator) {
		return "path is the root directory"
	}
	homes, reason := r.homes()
	if reason != "" {
		return reason
	}
	// These string checks give a reason even for a path that does not exist.
	// The identity check below is the one that decides.
	for _, home := range homes {
		if path == home {
			return "path is the home directory"
		}
		if scan.Inside(home, path) {
			return "path contains the home directory"
		}
	}
	info, err := os.Lstat(path)
	if errors.Is(err, fs.ErrNotExist) {
		return "path does not exist"
	}
	if err != nil {
		return "cannot read path: " + err.Error()
	}
	if info.Mode()&fs.ModeSymlink != 0 {
		return "path is a symlink"
	}
	if !info.IsDir() {
		return "path is not a directory"
	}
	if reason := sameAsHomeOrAncestor(info, homes); reason != "" {
		return reason
	}
	real, err := filepath.EvalSymlinks(path)
	if err != nil {
		return "cannot resolve path: " + err.Error()
	}
	if real != path {
		return "path resolves through a symlink to " + r.tilde(real)
	}
	inRepo, err := scan.InRepository(ctx, path)
	if err != nil {
		return "cannot check Git: " + err.Error()
	}
	if inRepo {
		return "path is in a Git repository"
	}
	return ""
}

// refuseNames walks dir without following symlinks and returns why plonge
// must not delete its contents: the first entry with one of names,
// or a directory that it cannot read, which could hide one.
// It returns "" when names is empty or dir holds none of them.
// A project rule's refuse_inside paths match only at fixed positions below
// a match; a name here can be at any depth, such as the metadata file below
// a Maven group ID, whose number of directories varies.
func refuseNames(dir string, names []string) string {
	if len(names) == 0 {
		return ""
	}
	var reason string
	_ = filepath.WalkDir(dir, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			reason = "cannot check for " + strings.Join(names, ", ") + ": " + err.Error()
			return fs.SkipAll
		}
		if path != dir && slices.Contains(names, d.Name()) {
			rel, _ := filepath.Rel(dir, path)
			reason = "contains " + filepath.ToSlash(rel) + ", which refuse_names " + strconv.Quote(d.Name()) + " protects"
			return fs.SkipAll
		}
		return nil
	})
	return reason
}

// applyDelete checks the path again and deletes its contents.
func (r Runner) applyDelete(ctx context.Context, o *Outcome) {
	reason := r.refuseDelete(ctx, o.Path)
	if reason == "" {
		reason = refuseNames(o.Path, o.refuseNames)
	}
	if reason != "" {
		o.Status, o.Reason = Skipped, reason
		return
	}
	err := clean.RemoveContents(o.Path)
	o.FreedBytes = max(0, o.Bytes-usage(o.Path))
	if err != nil {
		o.Status, o.Reason = Failed, err.Error()
		return
	}
	o.Status = Ran
}

// homes returns the home directory as given and with symlinks resolved,
// or a reason when plonge cannot use it to protect the home directory.
func (r Runner) homes() ([]string, string) {
	if !filepath.IsAbs(r.Home) {
		return nil, "cannot check the home directory: it is not an absolute path"
	}
	homes := []string{filepath.Clean(r.Home)}
	real, err := filepath.EvalSymlinks(r.Home)
	if err != nil {
		return nil, "cannot check the home directory: " + err.Error()
	}
	if real != homes[0] {
		homes = append(homes, real)
	}
	return homes, ""
}

// sameAsHomeOrAncestor compares the file info of a path with the home
// directory and each of its ancestors by file identity, not by name,
// so that an alias such as a case change on a case-insensitive volume
// cannot pass. It returns a reason when the path is one of them,
// or when an ancestor cannot be read.
func sameAsHomeOrAncestor(info fs.FileInfo, homes []string) string {
	for _, home := range homes {
		for dir := home; ; dir = filepath.Dir(dir) {
			di, err := os.Stat(dir)
			if err != nil {
				return "cannot check the home directory: " + err.Error()
			}
			if os.SameFile(info, di) {
				if dir == home {
					return "path is the home directory"
				}
				return "path contains the home directory"
			}
			if dir == filepath.Dir(dir) {
				break
			}
		}
	}
	return ""
}
