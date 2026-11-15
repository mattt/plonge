package scan

import (
	"encoding/binary"
	"errors"
	"math/rand/v2"
	"os"
	"os/exec"
	"path/filepath"
	"syscall"
	"testing"

	"golang.org/x/sys/unix"
)

const cloneSize = 1 << 20

// randomFile writes cloneSize random bytes, which a file system cannot store
// in fewer blocks.
func randomFile(t *testing.T, path string) {
	t.Helper()
	body := make([]byte, cloneSize)
	r := rand.New(rand.NewPCG(1, 2))
	for i := range body {
		body[i] = byte(r.Uint32())
	}
	write(t, path, string(body))
}

// clone makes dst a clone of src, or skips the test on a volume without clones.
func clone(t *testing.T, src, dst string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(dst), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := unix.Clonefile(src, dst, unix.CLONE_NOFOLLOW); err != nil {
		if errors.Is(err, unix.ENOTSUP) {
			t.Skip("the temporary directory's volume does not support clones")
		}
		t.Fatal(err)
	}
}

func TestDiskUsageCountsClonesOnMacOS(t *testing.T) {
	tmp := t.TempDir()
	original := filepath.Join(tmp, "store", "f")
	randomFile(t, original)

	// A clone of a file outside the cache frees nothing while the file remains,
	// as when uv clones files from its cache into a virtual environment.
	cache := filepath.Join(tmp, "cache")
	clone(t, original, filepath.Join(cache, "f"))
	if got := DiskUsage(cache); got >= cloneSize/2 {
		t.Errorf("DiskUsage = %d for a clone of a file outside the cache, want about 0", got)
	}

	// A file and its clone in the same cache share blocks that deleting both frees once.
	both := filepath.Join(tmp, "both")
	randomFile(t, filepath.Join(both, "a"))
	clone(t, filepath.Join(both, "a"), filepath.Join(both, "b"))
	if got := DiskUsage(both); got < cloneSize || got >= 2*cloneSize {
		t.Errorf("DiskUsage = %d for a file and its clone, want the size of one, %d", got, cloneSize)
	}

	// After the file outside goes, the clone holds its blocks alone.
	if err := os.Remove(original); err != nil {
		t.Fatal(err)
	}
	if got := DiskUsage(cache); got < cloneSize {
		t.Errorf("DiskUsage = %d for a clone whose original is gone, want at least %d", got, cloneSize)
	}
}

func TestDiskUsageCountsCompressedFilesOnMacOS(t *testing.T) {
	ditto, err := exec.LookPath("ditto")
	if err != nil {
		t.Skip("ditto is not available")
	}
	tmp := t.TempDir()
	plain := filepath.Join(tmp, "plain")
	body := make([]byte, 4<<20)
	for i := range body {
		body[i] = byte(i % 7)
	}
	write(t, plain, string(body))
	cache := filepath.Join(tmp, "cache")
	if out, err := exec.Command(ditto, "--hfsCompression", "--noclone", plain, filepath.Join(cache, "f")).CombinedOutput(); err != nil {
		t.Skipf("ditto cannot compress here: %v\n%s", err, out)
	}
	var st syscall.Stat_t
	if err := syscall.Lstat(filepath.Join(cache, "f"), &st); err != nil {
		t.Fatal(err)
	}
	if st.Flags&ufCompressed == 0 || st.Blocks == 0 {
		t.Skip("the volume did not compress the file")
	}
	// APFS reports no private size for a compressed file, so plonge uses its blocks.
	if got, want := DiskUsage(cache), st.Blocks*512; got < want {
		t.Errorf("DiskUsage = %d for a compressed file, want at least its %d allocated bytes", got, want)
	}
}

func TestEntrySizeMatchesLstat(t *testing.T) {
	tmp := t.TempDir()
	path := filepath.Join(tmp, "f")
	randomFile(t, path)
	if err := os.Link(path, filepath.Join(tmp, "g")); err != nil {
		t.Fatal(err)
	}
	ents, err := os.ReadDir(tmp)
	if err != nil {
		t.Fatal(err)
	}
	got, ok := entrySize(path, ents[0])
	want, wantOK := statSize(ents[0])
	if !ok || !wantOK || got.id != want.id || got.linked != want.linked || got.bytes != want.bytes || got.clones != 0 {
		t.Errorf("entrySize = %+v, %v; lstat gives %+v, %v", got, ok, want, wantOK)
	}
}

// attrBuf packs the attributes that attrSize requests, as getattrlist does
// with FSOPT_PACK_INVAL_ATTRS, with the given returned bitmaps.
type attrBuf struct {
	common, file, fork      uint32
	dev, objType, flags     uint32
	fileID                  uint64
	links                   uint32
	alloc, private, cloneID uint64
	clones                  uint32
}

func (a attrBuf) bytes() []byte {
	le := binary.LittleEndian
	b := make([]byte, attrBufSize)
	le.PutUint32(b[0:], 76)
	le.PutUint32(b[4:], a.common)
	le.PutUint32(b[16:], a.file)
	le.PutUint32(b[20:], a.fork)
	v := b[attrValuesOffset:]
	le.PutUint32(v[0:], a.dev)
	le.PutUint32(v[4:], a.objType)
	le.PutUint32(v[8:], a.flags)
	le.PutUint64(v[12:], a.fileID)
	le.PutUint32(v[20:], a.links)
	le.PutUint64(v[24:], a.alloc)
	le.PutUint64(v[32:], a.private)
	le.PutUint64(v[40:], a.cloneID)
	le.PutUint32(v[48:], a.clones)
	return b
}

func TestParseAttrs(t *testing.T) {
	full := attrBuf{
		common: attrCmnReturnedAttrs | requiredCommonAttrs | attrCmnFlags,
		file:   requiredFileAttrs,
		fork:   requestedForkAttrs,
		dev:    7, objType: vreg, fileID: 42, links: 1,
		alloc: 8192, private: 4096, cloneID: 99, clones: 2,
	}
	id := inode{dev: 7, ino: 42}
	tests := []struct {
		name   string
		edit   func(*attrBuf)
		want   fileSize
		wantOK bool
	}{
		{"clone family", func(*attrBuf) {}, fileSize{bytes: 4096, id: id, family: cloneFamily{7, 99}, clones: 2, shared: 4096}, true},
		{"compressed file uses allocated size", func(a *attrBuf) { a.flags = ufCompressed; a.private = 0 },
			fileSize{bytes: 8192, id: id}, true},
		{"no private size uses allocated size", func(a *attrBuf) { a.fork = 0 }, fileSize{bytes: 8192, id: id}, true},
		{"private size without clone count", func(a *attrBuf) { a.fork = attrCmnExtPrivateSize },
			fileSize{bytes: 4096, id: id}, true},
		{"private size above allocated size", func(a *attrBuf) { a.private = 1 << 20 }, fileSize{bytes: 8192, id: id}, true},
		{"hard link", func(a *attrBuf) { a.links = 2; a.clones = 1 }, fileSize{bytes: 4096, id: id, linked: true}, true},
		{"no file ID falls back to lstat", func(a *attrBuf) { a.common &^= attrCmnFileID }, fileSize{}, false},
		{"no allocated size falls back to lstat", func(a *attrBuf) { a.file = attrFileLinkCount }, fileSize{}, false},
		{"not a regular file falls back to lstat", func(a *attrBuf) { a.objType = 2 }, fileSize{}, false},
	}
	for _, tt := range tests {
		a := full
		tt.edit(&a)
		got, ok := parseAttrs(a.bytes())
		if ok != tt.wantOK || got != tt.want {
			t.Errorf("%s: parseAttrs = %+v, %v; want %+v, %v", tt.name, got, ok, tt.want, tt.wantOK)
		}
	}
}
