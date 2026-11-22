package global

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path"
	"slices"
	"time"
)

// The tmpdir rule checks and deletes through open directory handles,
// so that a folder that is renamed or replaced by a symlink
// after a check cannot redirect what plonge reads or deletes.
// It records the identity, by device and inode, of every file that passes
// the check, and keeps a file whose identity is different when it deletes.

// tmpDir is the open temporary directory.
type tmpDir struct {
	path string
	root *os.Root
	id   fileID
}

// identity returns the device and inode of the file that info describes.
// It does not use the stat hook that tests set for the checks.
func identity(info fs.FileInfo) (fileID, bool) {
	st, ok := statOf(info)
	return st.id, ok
}

// openTmpDir opens the directory at path, which has no symlinks in it.
// The caller must close the result.
func openTmpDir(path string) (*tmpDir, error) {
	root, err := os.OpenRoot(path)
	if err != nil {
		return nil, err
	}
	info, err := root.Stat(".")
	if err == nil && !info.IsDir() {
		err = errors.New("not a directory")
	}
	if err != nil {
		root.Close()
		return nil, err
	}
	id, ok := identity(info)
	if !ok {
		root.Close()
		return nil, errors.New("cannot read its status")
	}
	return &tmpDir{path: path, root: root, id: id}, nil
}

// unchanged reports whether t.path still names the directory that t has open.
func (t *tmpDir) unchanged() bool {
	info, err := os.Lstat(t.path)
	if err != nil {
		return false
	}
	id, ok := identity(info)
	return ok && id == t.id
}

func (t *tmpDir) Close() error { return t.root.Close() }

// errReplaced is the error for a file whose identity changed after its check.
var errReplaced = errors.New("was replaced after plonge checked it")

// openChild opens the directory name in parent,
// and fails with errReplaced when it is not the file with identity want.
func openChild(parent *os.Root, name string, want fileID) (*os.Root, error) {
	dir, err := parent.OpenRoot(name)
	if err != nil {
		return nil, err
	}
	info, err := dir.Stat(".")
	if err != nil {
		dir.Close()
		return nil, err
	}
	if id, ok := identity(info); !ok || id != want {
		dir.Close()
		return nil, errReplaced
	}
	return dir, nil
}

// readNames returns the sorted names of the entries in dir.
func readNames(dir *os.Root) ([]string, error) {
	f, err := dir.Open(".")
	if err != nil {
		return nil, err
	}
	defer f.Close()
	names, err := f.Readdirnames(-1)
	slices.Sort(names)
	return names, err
}

// tmpCheck checks the file name in parent and everything in it,
// and returns why plonge must not delete the stale entry that holds it,
// or "" when it may. rel is the file's path in the stale entry,
// or "" for the entry itself. dev is the device of the temporary directory.
// tmpCheck records the identity of each file that passes in ids, by rel.
func (r Runner) tmpCheck(parent *os.Root, name, rel string, dev uint64, cutoff time.Time, ids map[string]fileID) string {
	info, err := parent.Lstat(name)
	if err != nil {
		return "cannot check: " + err.Error()
	}
	if rel == "" && info.Mode()&fs.ModeSymlink != 0 {
		return "is a symlink"
	}
	if reason := r.tmpInfoRefusal(info, rel, dev, uint32(os.Getuid()), cutoff); reason != "" {
		return reason
	}
	id, ok := identity(info)
	if !ok {
		return "cannot read the identity of " + tmpName(rel)
	}
	ids[rel] = id
	if !info.IsDir() {
		return ""
	}
	dir, err := openChild(parent, name, id)
	if err != nil {
		return "cannot check " + tmpName(rel) + ": " + err.Error()
	}
	defer dir.Close()
	names, err := readNames(dir)
	if err != nil {
		return "cannot check " + tmpName(rel) + ": " + err.Error()
	}
	for _, child := range names {
		if reason := r.tmpCheck(dir, child, path.Join(rel, child), dev, cutoff, ids); reason != "" {
			return reason
		}
	}
	return ""
}

// tmpName names the file at rel in a stale entry for a refusal.
func tmpName(rel string) string {
	if rel == "" {
		return "it"
	}
	return rel
}

// tmpRemove deletes the file name in parent and everything in it.
// rel is the file's path in the stale entry, and ids holds the identity
// of each file that passed the check, by rel.
// A file that was added or replaced after the check is kept,
// with the folders that hold it, and tmpRemove returns an error for it.
// tmpRemove makes a folder writable for its owner before it deletes from it,
// as clean.RemoveTree does.
func tmpRemove(parent *os.Root, name, rel string, ids map[string]fileID) error {
	info, err := parent.Lstat(name)
	if errors.Is(err, fs.ErrNotExist) {
		return nil
	}
	if err != nil {
		return err
	}
	want, checked := ids[rel]
	if id, ok := identity(info); !ok || !checked || id != want {
		if !checked {
			return fmt.Errorf("%s: kept, because it appeared after plonge checked it", tmpName(rel))
		}
		return fmt.Errorf("%s: kept, because it %w", tmpName(rel), errReplaced)
	}
	if info.IsDir() {
		if perm := info.Mode().Perm(); perm&0o700 != 0o700 {
			if err := parent.Chmod(name, perm|0o700); err != nil {
				return err
			}
		}
		dir, err := openChild(parent, name, want)
		if err != nil {
			return fmt.Errorf("%s: %w", tmpName(rel), err)
		}
		names, err := readNames(dir)
		var errs []error
		if err != nil {
			errs = append(errs, err)
		}
		for _, child := range names {
			if err := tmpRemove(dir, child, path.Join(rel, child), ids); err != nil {
				errs = append(errs, err)
			}
		}
		dir.Close()
		if len(errs) > 0 {
			return errors.Join(errs...)
		}
	}
	return parent.Remove(name)
}
