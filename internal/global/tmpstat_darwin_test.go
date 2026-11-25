package global

import (
	"io/fs"
	"syscall"
	"testing"

	"golang.org/x/sys/unix"
)

// sysInfo is a FileInfo whose system status is st.
type sysInfo struct {
	fs.FileInfo
	st *syscall.Stat_t
}

func (i sysInfo) Sys() any { return i.st }

func TestStatOfReadsNoUnlink(t *testing.T) {
	for flags, want := range map[uint32]bool{
		0:                                     false,
		unix.UF_NODUMP:                        false,
		unix.SF_NOUNLINK:                      true,
		unix.SF_NOUNLINK | unix.SF_RESTRICTED: true,
	} {
		st, ok := statOf(sysInfo{st: &syscall.Stat_t{Flags: flags}})
		if !ok || st.noUnlink != want {
			t.Errorf("flags %#x: noUnlink = %v, ok = %v, want %v", flags, st.noUnlink, ok, want)
		}
	}
}
