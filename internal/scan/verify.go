package scan

import (
	"context"
	"os"
	"path/filepath"
	"strings"
)

// Verify checks again that a candidate is safe to delete, right before deletion.
// The candidate must come from Detect on root.
// It returns a reason when the candidate has changed and must be skipped,
// and an error when the check itself failed.
func Verify(ctx context.Context, root string, c Candidate) (reason string, err error) {
	info, err := os.Lstat(c.Path)
	if err != nil {
		if os.IsNotExist(err) {
			return "no longer exists", nil
		}
		return "", err
	}
	if info.Mode()&os.ModeSymlink != 0 {
		return "is now a symlink", nil
	}
	if !info.IsDir() {
		return "is no longer a directory", nil
	}
	realRoot, err := filepath.EvalSymlinks(root)
	if err != nil {
		return "", err
	}
	realParent, err := filepath.EvalSymlinks(filepath.Dir(c.Path))
	if err != nil {
		return "", err
	}
	if !Inside(filepath.Join(realParent, filepath.Base(c.Path)), realRoot) {
		return "is no longer inside its root", nil
	}
	kept, err := filterIgnored(ctx, c.repo, []Candidate{c})
	if err != nil {
		return "", err
	}
	if len(kept) == 0 {
		return "is no longer ignored by Git, or now contains a tracked file", nil
	}
	// The enclosing repository does not see a repository inside the candidate,
	// so look for one before deleting its commits and uncommitted work.
	if reason := gitRefusal(c.Path); reason != "" {
		return reason, nil
	}
	if reason := insideRefusal(c); reason != "" {
		return reason, nil
	}
	return "", nil
}

// Inside reports whether path is strictly below dir.
func Inside(path, dir string) bool {
	return strings.HasPrefix(path, strings.TrimSuffix(dir, string(filepath.Separator))+string(filepath.Separator))
}
