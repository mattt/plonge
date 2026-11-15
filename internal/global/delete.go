package global

import (
	"context"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"

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
	homes := []string{filepath.Clean(r.Home)}
	if real, err := filepath.EvalSymlinks(r.Home); err == nil && real != homes[0] {
		homes = append(homes, real)
	}
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

// applyDelete checks the path again and deletes its contents.
func (r Runner) applyDelete(ctx context.Context, o *Outcome) {
	if reason := r.refuseDelete(ctx, o.Path); reason != "" {
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
