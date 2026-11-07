//go:build unix

package scan

import (
	"io/fs"
	"syscall"
)

// allocated returns the bytes in the blocks allocated to a file,
// its inode, and whether it has more than one hard link.
func allocated(info fs.FileInfo) (int64, inode, bool) {
	st, ok := info.Sys().(*syscall.Stat_t)
	if !ok {
		return info.Size(), inode{}, false
	}
	id := inode{dev: uint64(st.Dev), ino: uint64(st.Ino)}
	return int64(st.Blocks) * 512, id, !info.IsDir() && st.Nlink > 1
}
