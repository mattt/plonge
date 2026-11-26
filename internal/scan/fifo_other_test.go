//go:build !unix

package scan

import "testing"

// mkfifo skips the test, because this system has no named pipes.
func mkfifo(t *testing.T, path string) {
	t.Skip("named pipes need a Unix system")
}
