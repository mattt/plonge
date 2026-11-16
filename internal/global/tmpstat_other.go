//go:build !darwin && !linux

package global

import (
	"io/fs"
	"time"
)

// statOwner returns false: the tmpdir rule supports only macOS and Linux.
func statOwner(fs.FileInfo) (uint32, time.Time, bool) {
	return 0, time.Time{}, false
}
