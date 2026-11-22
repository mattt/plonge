//go:build !darwin && !linux

package global

import (
	"errors"
	"os"
)

// renameNoReplace fails: the tmpdir rule supports only macOS and Linux.
func renameNoReplace(*os.Root, string, string) error {
	return errors.New("cannot rename without replacing on this system")
}
