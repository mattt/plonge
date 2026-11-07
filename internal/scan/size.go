package scan

import (
	"io/fs"
	"path/filepath"
)

type inode struct{ dev, ino uint64 }

// DiskUsage returns the bytes allocated to dir and everything under it,
// as du counts them: allocated blocks, each hard-linked file once,
// and symlinks not followed.
func DiskUsage(dir string) int64 {
	var total int64
	seen := map[inode]bool{}
	_ = filepath.WalkDir(dir, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			if d != nil && d.IsDir() && path != dir {
				return fs.SkipDir
			}
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
	return total
}

// Sizes measures each directory with at most Workers walks at a time.
func Sizes(dirs []string) []int64 {
	out := make([]int64, len(dirs))
	parallel(len(dirs), func(i int) {
		out[i] = DiskUsage(dirs[i])
	})
	return out
}
