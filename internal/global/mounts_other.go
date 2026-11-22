//go:build !darwin && !linux

package global

import "errors"

// mountPoints fails: the tmpdir rule supports only macOS and Linux.
func mountPoints() ([]string, error) {
	return nil, errors.New("cannot list mounts on this system")
}
