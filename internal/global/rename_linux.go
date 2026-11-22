package global

import (
	"os"

	"golang.org/x/sys/unix"
)

// renameNoReplace renames from to to in dir, and fails without a change
// when to exists, with renameat2(2) and RENAME_NOREPLACE.
// It also fails on a file system that does not support the flag.
func renameNoReplace(dir *os.Root, from, to string) error {
	f, err := dir.Open(".")
	if err != nil {
		return err
	}
	defer f.Close()
	fd := int(f.Fd())
	if err := unix.Renameat2(fd, from, fd, to, unix.RENAME_NOREPLACE); err != nil {
		return &os.LinkError{Op: "renameat2", Old: from, New: to, Err: err}
	}
	return nil
}
