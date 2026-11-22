package global

import (
	"io/fs"
	"time"
)

// fileID identifies a file by its device and inode numbers.
type fileID struct {
	dev, ino uint64
}

// fileStat is what the tmpdir rule reads from a file's system status.
type fileStat struct {
	uid uint32
	id  fileID
	// changed is the time that the file's status last changed.
	changed time.Time
}

// stat returns the system status of the file that info describes,
// through the hook that a test sets, or statOf.
func (r Runner) stat(info fs.FileInfo) (fileStat, bool) {
	if r.statHook != nil {
		return r.statHook(info)
	}
	return statOf(info)
}
