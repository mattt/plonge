package global

import (
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path"
	"path/filepath"
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

// tmpPolicy is what the checks of one stale entry compare against.
type tmpPolicy struct {
	// entry is the path of the stale entry in the open temporary directory.
	entry string
	// dev is the device of the temporary directory.
	dev    uint64
	uid    uint32
	cutoff time.Time
	// mounts are the mount points in the temporary directory, by path.
	mounts map[string]bool
}

// tmpPolicy returns the policy for the stale entry name in t.
func (r Runner) tmpPolicy(t *tmpDir, name string, cutoff time.Time, mounts map[string]bool) tmpPolicy {
	return tmpPolicy{entry: filepath.Join(t.path, name), dev: t.id.dev, uid: uint32(os.Getuid()), cutoff: cutoff, mounts: mounts}
}

// tmpCheck checks the file name in parent and everything in it,
// and returns why plonge must not delete the stale entry that holds it,
// or "" when it may. rel is the file's path in the stale entry,
// or "" for the entry itself.
// tmpCheck records the identity of each file that passes in ids, by rel.
func (r Runner) tmpCheck(parent *os.Root, name, rel string, pol tmpPolicy, ids map[string]fileID) string {
	info, err := parent.Lstat(name)
	if err != nil {
		return "cannot check: " + err.Error()
	}
	if rel == "" && info.Mode()&fs.ModeSymlink != 0 {
		return "is a symlink"
	}
	if reason := r.tmpInfoRefusal(info, rel, pol); reason != "" {
		return reason
	}
	// A bind mount can have the same device as the folder around it,
	// so the mount table shows the mount points that the device misses.
	if pol.mounts[filepath.Join(pol.entry, filepath.FromSlash(rel))] {
		if rel == "" {
			return "is a mount point"
		}
		return "holds " + rel + ", which is a mount point"
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
		if reason := r.tmpCheck(dir, child, path.Join(rel, child), pol, ids); reason != "" {
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
//
// Before it removes a file, tmpRemove renames it to a random name
// in the same folder and checks the identity of what has that name,
// so a file that takes the original name after the check is not removed.
// Only a process that can write to the folder, and learns the random name
// before the removal, could put another file there.
// tmpRemove makes a folder writable for its owner before it deletes from it,
// as clean.RemoveTree does, through a handle that it opened and checked.
// parentPath is the path of parent, for the report.
func tmpRemove(parent *os.Root, parentPath, name, rel string, ids map[string]fileID) error {
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
		if err := removeContents(parent, parentPath, name, rel, info, ids); err != nil {
			return err
		}
	}
	return isolateAndRemove(parent, parentPath, name, rel, want)
}

// removeContents deletes everything in the folder name in parent,
// whose status info shows the identity in ids.
func removeContents(parent *os.Root, parentPath, name, rel string, info fs.FileInfo, ids map[string]fileID) error {
	want := ids[rel]
	if perm := info.Mode().Perm(); perm&0o700 != 0o700 {
		if err := chmodChecked(parent, name, want, perm|0o700); err != nil {
			return fmt.Errorf("%s: %w", tmpName(rel), err)
		}
	}
	dir, err := openChild(parent, name, want)
	if err != nil {
		return fmt.Errorf("%s: %w", tmpName(rel), err)
	}
	defer dir.Close()
	names, err := readNames(dir)
	var errs []error
	if err != nil {
		errs = append(errs, err)
	}
	for _, child := range names {
		if err := tmpRemove(dir, filepath.Join(parentPath, name), child, path.Join(rel, child), ids); err != nil {
			errs = append(errs, err)
		}
	}
	return errors.Join(errs...)
}

// chmodChecked opens the folder name in parent, checks that it has
// the identity want, and changes its permissions through that handle.
// It needs read permission on the folder to open it.
func chmodChecked(parent *os.Root, name string, want fileID, mode fs.FileMode) error {
	f, err := parent.OpenFile(name, os.O_RDONLY, 0)
	if err != nil {
		return err
	}
	defer f.Close()
	info, err := f.Stat()
	if err != nil {
		return err
	}
	if id, ok := identity(info); !ok || id != want {
		return errReplaced
	}
	return f.Chmod(mode)
}

// tmpRemoveHook, when a test sets it, runs at two stages of isolateAndRemove:
// "isolate", after tmpRemove checks a file and before it renames it,
// and "restore", right before it renames a replaced file back.
var tmpRemoveHook func(stage, rel string)

// isolateAndRemove renames the file name in parent to a random name,
// checks that the renamed file has the identity want, and removes it.
// When the identity is different, when it cannot check it,
// or when the removal fails, as for a folder that gained a file,
// it renames the file back with a rename that fails when another file
// took the original name; then, or when that rename is not supported,
// it keeps the file under the random name and returns an error with its path.
// No failure after the first rename leaves the file under the random name
// without a report of that path.
func isolateAndRemove(parent *os.Root, parentPath, name, rel string, want fileID) error {
	if tmpRemoveHook != nil {
		tmpRemoveHook("isolate", rel)
	}
	var b [12]byte
	_, _ = rand.Read(b[:])
	temp := ".plonge-delete-" + hex.EncodeToString(b[:])
	if err := parent.Rename(name, temp); err != nil {
		return fmt.Errorf("%s: %w", tmpName(rel), err)
	}
	info, err := parent.Lstat(temp)
	if err != nil {
		return restore(parent, parentPath, temp, name, rel, fmt.Errorf("cannot check it: %w", err))
	}
	if id, ok := identity(info); !ok || id != want {
		return restore(parent, parentPath, temp, name, rel, fmt.Errorf("it %w", errReplaced))
	}
	if err := parent.Remove(temp); err != nil {
		return restore(parent, parentPath, temp, name, rel, fmt.Errorf("cannot remove it: %w", err))
	}
	return nil
}

// restore renames temp in parent back to name, unless another file has name,
// and returns an error that says why the file was kept, and where.
func restore(parent *os.Root, parentPath, temp, name, rel string, why error) error {
	if tmpRemoveHook != nil {
		tmpRemoveHook("restore", rel)
	}
	where := filepath.Join(parentPath, temp)
	if renameNoReplace(parent, temp, name) == nil {
		where = filepath.Join(parentPath, name)
	}
	return fmt.Errorf("%s: kept as %s, because %w", tmpName(rel), where, why)
}
