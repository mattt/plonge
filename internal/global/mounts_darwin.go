package global

import (
	"fmt"
	"syscall"
)

// mountPoints returns the paths where file systems are mounted,
// from getfsstat(2), which lists every mounted file system.
func mountPoints() ([]string, error) {
	// The list can grow between the call that counts and the call that reads,
	// so read into a larger buffer and try again when it fills.
	for extra := 16; ; extra *= 2 {
		n, err := syscall.Getfsstat(nil, mntNowait)
		if err != nil {
			return nil, fmt.Errorf("getfsstat: %w", err)
		}
		buf := make([]syscall.Statfs_t, n+extra)
		n, err = syscall.Getfsstat(buf, mntNowait)
		if err != nil {
			return nil, fmt.Errorf("getfsstat: %w", err)
		}
		if n == len(buf) {
			continue
		}
		paths := make([]string, n)
		for i, fs := range buf[:n] {
			paths[i] = cString(fs.Mntonname[:])
		}
		return paths, nil
	}
}

// mntNowait is MNT_NOWAIT from <sys/mount.h>: report cached statistics
// without waiting on each file system.
const mntNowait = 2

func cString(b []int8) string {
	out := make([]byte, 0, len(b))
	for _, c := range b {
		if c == 0 {
			break
		}
		out = append(out, byte(c))
	}
	return string(out)
}
