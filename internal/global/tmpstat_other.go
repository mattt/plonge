//go:build !darwin && !linux

package global

import "io/fs"

// statOf returns false: the tmpdir rule supports only macOS and Linux.
func statOf(fs.FileInfo) (fileStat, bool) {
	return fileStat{}, false
}
