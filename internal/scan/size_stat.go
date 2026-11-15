//go:build !darwin

package scan

import "io/fs"

// entrySize measures the entry at path from its lstat information.
func entrySize(_ string, d fs.DirEntry) (fileSize, bool) {
	return statSize(d)
}
