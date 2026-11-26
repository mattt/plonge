//go:build unix

package scan

import (
	"syscall"
	"testing"
)

// mkfifo makes a named pipe at path.
func mkfifo(t *testing.T, path string) {
	t.Helper()
	if err := syscall.Mkfifo(path, 0o644); err != nil {
		t.Fatal(err)
	}
}
