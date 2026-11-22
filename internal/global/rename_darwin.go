package global

import (
	"os"

	"golang.org/x/sys/unix"
)

// renameNoReplace renames from to to in dir, and fails without a change
// when to exists, with renameatx_np(2) and RENAME_EXCL.
func renameNoReplace(dir *os.Root, from, to string) error {
	f, err := dir.Open(".")
	if err != nil {
		return err
	}
	defer f.Close()
	fd := int(f.Fd())
	if err := unix.RenameatxNp(fd, from, fd, to, unix.RENAME_EXCL); err != nil {
		return &os.LinkError{Op: "renameatx_np", Old: from, New: to, Err: err}
	}
	return nil
}
