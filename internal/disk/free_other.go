//go:build !darwin && !linux

package disk

import (
	"errors"
	"runtime"
)

// Free is not available on this platform.
func Free(string) (int64, error) {
	return 0, errors.New("free space is not available on " + runtime.GOOS)
}
