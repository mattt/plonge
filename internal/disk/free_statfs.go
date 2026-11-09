//go:build darwin || linux

package disk

import "syscall"

// Free returns the bytes available to an unprivileged user on path's volume.
func Free(path string) (int64, error) {
	var st syscall.Statfs_t
	if err := syscall.Statfs(path, &st); err != nil {
		return 0, err
	}
	return int64(st.Bavail) * int64(st.Bsize), nil
}
