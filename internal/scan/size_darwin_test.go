package scan

import (
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
