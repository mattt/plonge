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
	// noUnlink reports the macOS system flag SF_NOUNLINK,
	// which ls -lO shows as sunlnk. macOS sets it on folders that
	// System Integrity Protection guards, and refuses to rename or remove them.
	// It is always false on Linux.
	noUnlink bool
}

// stat returns the system status of the file that info describes,
// through the hook that a test sets, or statOf.
func (r Runner) stat(info fs.FileInfo) (fileStat, bool) {
	if r.statHook != nil {
		return r.statHook(info)
	}
	return statOf(info)
}
