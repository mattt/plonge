package global

import (
	"io/fs"
	"syscall"
	"time"

	"golang.org/x/sys/unix"
)

// statOf returns the system status of the file that info describes.
// The result is false when info has no system status.
func statOf(info fs.FileInfo) (fileStat, bool) {
	st, ok := info.Sys().(*syscall.Stat_t)
	if !ok {
		return fileStat{}, false
	}
	return fileStat{
		uid:      st.Uid,
		id:       fileID{dev: uint64(st.Dev), ino: uint64(st.Ino)},
		changed:  time.Unix(st.Ctimespec.Unix()),
		noUnlink: st.Flags&unix.SF_NOUNLINK != 0,
	}, true
}
