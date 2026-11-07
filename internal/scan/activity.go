package scan

import (
	"os"
	"path/filepath"
	"strings"
	"time"
)

// lastActivity returns the modification time of the first path that exists,
// from the output of git rev-parse --git-path logs/HEAD --git-path HEAD in dir.
// It does not use the index, because git status rewrites it.
func lastActivity(dir string, gitPaths []string) time.Time {
	for _, p := range gitPaths {
		p = strings.TrimSpace(p)
		if p == "" {
			continue
		}
		if !filepath.IsAbs(p) {
			p = filepath.Join(dir, p)
		}
		if info, err := os.Stat(p); err == nil {
			return info.ModTime()
		}
	}
	return time.Time{}
}

// Quiet reports whether last is at least hours before now.
// An unknown time is not quiet.
func Quiet(last, now time.Time, hours float64) bool {
	if last.IsZero() {
		return false
	}
	return now.Sub(last) >= time.Duration(hours*float64(time.Hour))
}
