//go:build !unix

package scan

import "io/fs"

// allocated returns the apparent size where block counts are not available.
func allocated(info fs.FileInfo) (int64, inode, bool) {
	return info.Size(), inode{}, false
}
