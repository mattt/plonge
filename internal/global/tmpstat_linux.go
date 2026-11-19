package global

import (
	"io/fs"
	"syscall"
	"time"
)

// statOwner returns the owner of the file that info describes
// and the time its status last changed. The result is false
// when info has no system status.
func statOwner(info fs.FileInfo) (uid uint32, changed time.Time, ok bool) {
	st, ok := info.Sys().(*syscall.Stat_t)
	if !ok {
		return 0, time.Time{}, false
	}
	return st.Uid, time.Unix(st.Ctim.Unix()), true
}
