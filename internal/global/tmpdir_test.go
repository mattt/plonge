//go:build darwin || linux

package global

import (
	"context"
	"errors"
	"io/fs"
	"maps"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"slices"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/mattt/plonge/internal/procs"
	"github.com/mattt/plonge/internal/scan"
)

// fakeProcs is a process inspector whose one process uses the files in files.
// onSnapshot, when set, runs before each snapshot.
type fakeProcs struct {
	files      []procs.File
	err        error
	onSnapshot func()
}

func (f *fakeProcs) Snapshot(context.Context) (*procs.Snapshot, error) {
	if f.onSnapshot != nil {
		f.onSnapshot()
	}
	if f.err != nil {
		return nil, f.err
	}
	return &procs.Snapshot{Processes: []procs.Process{{PID: 42, Name: "tool", Files: f.files}}}, nil
}

// use makes the fake process use each path in the way that use names.
func (f *fakeProcs) use(use string, paths ...string) {
	for _, p := range paths {
		f.files = append(f.files, procs.File{Use: use, Path: p})
	}
}

// tmpSetup makes a fake temporary directory and a Runner whose clock
// is 30 days ahead, so that every file the test makes is old
// unless the test sets a later modification time.
func tmpSetup(t *testing.T) (r Runner, tmp string, insp *fakeProcs) {
	t.Helper()
	bin := t.TempDir()
	withGit(t, bin)
	home := realTempDir(t)
	tmp = filepath.Join(home, "T")
	if err := os.Mkdir(tmp, 0o700); err != nil {
		t.Fatal(err)
	}
	now := time.Now().Add(30 * 24 * time.Hour)
	insp = &fakeProcs{}
	r = Runner{
		Home: home, GOOS: runtime.GOOS, TmpDir: tmp, TmpAge: DefaultTmpAge,
		inspector: insp,
		now:       func() time.Time { return now },
	}
	return r, tmp, insp
}

// touchRecent gives path a modification time inside the rule's period.
func touchRecent(t *testing.T, r Runner, path string) {
	t.Helper()
	recent := r.clock().Add(-time.Hour)
	if err := os.Chtimes(path, recent, recent); err != nil {
		t.Fatal(err)
	}
}

func tmpEntryOf(t *testing.T, r Runner) Entry {
	t.Helper()
	entries := r.List(context.Background(), nil)
	i := slices.IndexFunc(entries, func(e Entry) bool { return e.ID == "tmpdir" })
	if i < 0 {
		t.Fatalf("no tmpdir entry in %+v", entries)
	}
	return entries[i]
}

func mkfile(t *testing.T, path, body string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
}

func refusedReasons(e Entry) map[string]string {
	reasons := map[string]string{}
	for _, f := range e.RefusedFolders {
		reasons[filepath.Base(f.Path)] = f.Reason
	}
	return reasons
}

func TestTmpDirSelectsOnlyStaleEntries(t *testing.T) {
	r, tmp, insp := tmpSetup(t)
	mkfile(t, filepath.Join(tmp, "old-dir", "a", "blob"), strings.Repeat("x", 50_000))
	mkfile(t, filepath.Join(tmp, "old-file"), "old")

	mkfile(t, filepath.Join(tmp, "recent-file"), "new")
	touchRecent(t, r, filepath.Join(tmp, "recent-file"))
	mkfile(t, filepath.Join(tmp, "recent-inside", "sub", "new"), "new")
	touchRecent(t, r, filepath.Join(tmp, "recent-inside", "sub", "new"))

	mkfile(t, filepath.Join(tmp, "open-file"), "open")
	mkfile(t, filepath.Join(tmp, "open-dir", "x"), "open")
	if err := os.Mkdir(filepath.Join(tmp, "cwd-dir"), 0o755); err != nil {
		t.Fatal(err)
	}
	real, err := filepath.EvalSymlinks(tmp)
	if err != nil {
		t.Fatal(err)
	}
	insp.use(procs.UseOpen, filepath.Join(tmp, "open-file"), filepath.Join(real, "open-dir", "x"), "127.0.0.1:8080")
	insp.use(procs.UseCwd, filepath.Join(tmp, "cwd-dir"))

	if err := os.Symlink(filepath.Join(tmp, "old-dir"), filepath.Join(tmp, "link")); err != nil {
		t.Fatal(err)
	}
	if err := os.Mkdir(filepath.Join(tmp, "pipe-dir"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := syscall.Mkfifo(filepath.Join(tmp, "pipe-dir", "fifo"), 0o600); err != nil {
		t.Fatal(err)
	}
	mkfile(t, filepath.Join(tmp, "repo", ".git", "HEAD"), "ref: refs/heads/main\n")
	for _, name := range []string{"com.apple.Safari", "TemporaryItems", ".X11-unix", "systemd-private-abc"} {
		mkfile(t, filepath.Join(tmp, name, "data"), "managed")
	}

	e := tmpEntryOf(t, r)
	if e.Skipped != "" {
		t.Fatalf("skipped: %s", e.Skipped)
	}
	if e.Path != real || e.Kind != "prune" || e.WholeCache {
		t.Errorf("entry = %+v", e)
	}
	var stale []string
	for _, p := range e.stale {
		stale = append(stale, filepath.Base(p))
	}
	if !slices.Equal(stale, []string{"old-dir", "old-file"}) {
		t.Errorf("stale = %q", stale)
	}
	if e.Bytes < 50_000 || e.Bytes > 200_000 || !strings.Contains(e.Command, "delete 2 stale entries") {
		t.Errorf("bytes = %d, command = %q", e.Bytes, e.Command)
	}
	want := map[string]string{
		"recent-inside": "holds sub/new, which changed in the last 24 hours",
		"open-file":     "in use by tool (PID 42), which has it open",
		"open-dir":      "in use by tool (PID 42), which has x open",
		"cwd-dir":       "in use by tool (PID 42), which works in it",
		"link":          "is a symlink",
		"pipe-dir":      "holds fifo, which is a named pipe",
		"repo":          "Git",
	}
	got := refusedReasons(e)
	if len(got) != len(want) {
		t.Errorf("refused = %q", got)
	}
	for name, part := range want {
		if !strings.Contains(got[name], part) {
			t.Errorf("refusal of %s = %q, want %q", name, got[name], part)
		}
	}
	if e.Note != "kept without checks: 1 changed in the last 24 hours, 4 managed by the system" {
		t.Errorf("note = %q", e.Note)
	}
}

func TestTmpDirApplyChecksAgain(t *testing.T) {
	r, tmp, insp := tmpSetup(t)
	for _, name := range []string{"a", "b", "c", "d"} {
		mkfile(t, filepath.Join(tmp, name, "blob"), strings.Repeat("x", 10_000))
	}
	mkfile(t, filepath.Join(tmp, "recent"), "new")
	touchRecent(t, r, filepath.Join(tmp, "recent"))
	mkfile(t, filepath.Join(tmp, "com.apple.thing"), "managed")
	ctx := context.Background()
	entries := r.List(ctx, nil)

	// After the listing, a process opens a file in b, c gains a recent file,
	// d is removed, and a new old entry appears that the listing did not select.
	insp.use(procs.UseOpen, filepath.Join(tmp, "b", "blob"))
	mkfile(t, filepath.Join(tmp, "c", "new"), "new")
	touchRecent(t, r, filepath.Join(tmp, "c", "new"))
	if err := os.RemoveAll(filepath.Join(tmp, "d")); err != nil {
		t.Fatal(err)
	}
	mkfile(t, filepath.Join(tmp, "e"), "late")

	res := r.Clean(ctx, entries, Options{Apply: true})
	o := res.Outcomes[slices.IndexFunc(res.Outcomes, func(o Outcome) bool { return o.ID == "tmpdir" })]
	if o.Status != Ran || o.Command != tmpCommand(1) || o.FreedBytes <= 0 {
		t.Errorf("outcome = %+v", o)
	}
	for name, kept := range map[string]bool{"a": false, "b": true, "c": true, "e": true, "recent": true, "com.apple.thing": true} {
		_, err := os.Lstat(filepath.Join(tmp, name))
		if gone := errors.Is(err, os.ErrNotExist); gone == kept {
			t.Errorf("%s: kept = %v, want %v", name, !gone, kept)
		}
	}
	got := refusedReasons(o.Entry)
	if !strings.Contains(got["b"], "in use by tool") || !strings.Contains(got["c"], "holds new") {
		t.Errorf("refused = %q", got)
	}
	// The listed entry must not change.
	e := entries[slices.IndexFunc(entries, func(e Entry) bool { return e.ID == "tmpdir" })]
	if len(e.stale) != 4 || len(e.RefusedFolders) != 0 {
		t.Errorf("listed entry changed: %+v", e)
	}
}

func TestTmpDirWithoutApplyDeletesNothing(t *testing.T) {
	r, tmp, _ := tmpSetup(t)
	mkfile(t, filepath.Join(tmp, "old"), "old")
	ctx := context.Background()
	res := r.Clean(ctx, r.List(ctx, nil), Options{})
	o := res.Outcomes[slices.IndexFunc(res.Outcomes, func(o Outcome) bool { return o.ID == "tmpdir" })]
	if o.Status != WouldRun || o.Command != tmpCommand(1) {
		t.Errorf("outcome = %+v", o)
	}
	if _, err := os.Stat(filepath.Join(tmp, "old")); err != nil {
		t.Error(err)
	}
}

func TestTmpDirStatusChangeTimeCounts(t *testing.T) {
	r, tmp, _ := tmpSetup(t)
	r.now = nil
	// An extracted file can keep an old modification time,
	// but its status change time is when it was made.
	path := filepath.Join(tmp, "extracted")
	mkfile(t, path, "data")
	old := time.Date(2000, 1, 1, 0, 0, 0, 0, time.UTC)
	if err := os.Chtimes(path, old, old); err != nil {
		t.Fatal(err)
	}
	e := tmpEntryOf(t, r)
	if len(e.stale) != 0 || !strings.Contains(e.Note, "1 changed") {
		t.Errorf("entry = %+v", e)
	}
}

func TestTmpDirResolvesSymlink(t *testing.T) {
	r, tmp, _ := tmpSetup(t)
	mkfile(t, filepath.Join(tmp, "old"), "old")
	link := filepath.Join(r.Home, "link")
	if err := os.Symlink(tmp, link); err != nil {
		t.Fatal(err)
	}
	r.TmpDir = link
	e := tmpEntryOf(t, r)
	if e.Skipped != "" || e.Path != tmp || len(e.stale) != 1 {
		t.Errorf("entry = %+v", e)
	}
}

func TestTmpDirSkips(t *testing.T) {
	t.Run("home", func(t *testing.T) {
		r, _, _ := tmpSetup(t)
		r.TmpDir = r.Home
		if e := tmpEntryOf(t, r); !strings.Contains(e.Skipped, "home directory") || e.Command != "" {
			t.Errorf("entry = %+v", e)
		}
	})
	t.Run("ancestor of home", func(t *testing.T) {
		r, _, _ := tmpSetup(t)
		r.TmpDir = filepath.Dir(r.Home)
		if e := tmpEntryOf(t, r); !strings.Contains(e.Skipped, "contains the home directory") {
			t.Errorf("entry = %+v", e)
		}
	})
	t.Run("relative", func(t *testing.T) {
		r, _, _ := tmpSetup(t)
		r.TmpDir = "tmp"
		if e := tmpEntryOf(t, r); !strings.Contains(e.Skipped, "not an absolute path") {
			t.Errorf("entry = %+v", e)
		}
	})
	t.Run("missing", func(t *testing.T) {
		r, tmp, _ := tmpSetup(t)
		r.TmpDir = filepath.Join(tmp, "none")
		if e := tmpEntryOf(t, r); !strings.Contains(e.Skipped, "cannot resolve") {
			t.Errorf("entry = %+v", e)
		}
	})
	t.Run("in a Git repository", func(t *testing.T) {
		r, tmp, _ := tmpSetup(t)
		mkfile(t, filepath.Join(tmp, "old"), "old")
		if out, err := exec.Command("git", "init", "-q", r.Home).CombinedOutput(); err != nil {
			t.Fatalf("git init: %v\n%s", err, out)
		}
		if e := tmpEntryOf(t, r); !strings.Contains(e.Skipped, "Git") {
			t.Errorf("entry = %+v", e)
		}
	})
	t.Run("process list fails", func(t *testing.T) {
		r, tmp, insp := tmpSetup(t)
		mkfile(t, filepath.Join(tmp, "old"), "old")
		insp.err = errors.New("lsof is not installed")
		e := tmpEntryOf(t, r)
		if e.Skipped != "cannot list processes: lsof is not installed" {
			t.Errorf("entry = %+v", e)
		}
		res := r.Clean(context.Background(), []Entry{e}, Options{Apply: true})
		if res.Outcomes[0].Status != Skipped {
			t.Errorf("outcome = %+v", res.Outcomes[0])
		}
		if _, err := os.Stat(filepath.Join(tmp, "old")); err != nil {
			t.Error(err)
		}
	})
	t.Run("other systems", func(t *testing.T) {
		r, _, _ := tmpSetup(t)
		r.GOOS = "windows"
		if slices.ContainsFunc(r.List(context.Background(), nil), func(e Entry) bool { return e.ID == "tmpdir" }) {
			t.Error("tmpdir rule listed on windows")
		}
	})
}

func TestTmpManaged(t *testing.T) {
	for name, want := range map[string]bool{
		"com.apple.Safari": true, "TemporaryItems": true, ".AddressBookLocks": true,
		"cryptex.personalize.x": true, ".X11-unix": true, ".X0-lock": true,
		"systemd-private-1-foo.service-a": true, "snap-private-tmp": true,
		"com.google.Chrome": false, "go-build123": false, "tmp.abc": false, "TemporaryItems2": false,
	} {
		if got := tmpManaged(name); got != want {
			t.Errorf("tmpManaged(%q) = %v, want %v", name, got, want)
		}
	}
}

func TestTmpDirGitCheckMatchesScan(t *testing.T) {
	// The listing and the final check must refuse the same repositories.
	r, tmp, _ := tmpSetup(t)
	mkfile(t, filepath.Join(tmp, "nested", "dep", ".git"), "gitdir: /elsewhere\n")
	e := tmpEntryOf(t, r)
	got := refusedReasons(e)["nested"]
	if got == "" || got != scan.GitRefusal(filepath.Join(tmp, "nested")) {
		t.Errorf("refusal = %q", got)
	}
}

// foreignStat returns a stat hook that reports the file named owner
// as another user's, and the file named mount as on another device.
func foreignStat(owner, mount string) func(fs.FileInfo) (fileStat, bool) {
	return func(info fs.FileInfo) (fileStat, bool) {
		st, ok := statOf(info)
		switch info.Name() {
		case owner:
			st.uid++
		case mount:
			st.id.dev++
		}
		return st, ok
	}
}

func tmpOutcome(t *testing.T, res Result) Outcome {
	t.Helper()
	i := slices.IndexFunc(res.Outcomes, func(o Outcome) bool { return o.ID == "tmpdir" })
	if i < 0 {
		t.Fatalf("no tmpdir outcome in %+v", res.Outcomes)
	}
	return res.Outcomes[i]
}

func TestTmpDirRefusesForeignFilesInsideAnEntry(t *testing.T) {
	r, tmp, _ := tmpSetup(t)
	mkfile(t, filepath.Join(tmp, "owned", "a", "theirs"), "x")
	mkfile(t, filepath.Join(tmp, "mounted", "a", "mnt", "file"), "x")
	mkfile(t, filepath.Join(tmp, "plain", "file"), "x")
	ctx := context.Background()

	listed := r.List(ctx, nil)
	if e := tmpEntryOf(t, r); len(e.stale) != 3 {
		t.Fatalf("stale = %q", e.stale)
	}

	// After the listing, a file deep in one entry belongs to another user,
	// and a directory deep in another is a mount point.
	r.statHook = foreignStat("theirs", "mnt")
	want := map[string]string{
		"owned":   "holds a/theirs, which belongs to another user",
		"mounted": "holds a/mnt, which is on another file system",
	}
	if got := refusedReasons(tmpEntryOf(t, r)); !maps.Equal(got, want) {
		t.Errorf("listing refused = %q, want %q", got, want)
	}
	o := tmpOutcome(t, r.Clean(ctx, listed, Options{Apply: true}))
	if got := refusedReasons(o.Entry); !maps.Equal(got, want) {
		t.Errorf("apply refused = %q, want %q", got, want)
	}
	for name, kept := range map[string]bool{"owned": true, "mounted": true, "plain": false} {
		_, err := os.Lstat(filepath.Join(tmp, name))
		if gone := errors.Is(err, os.ErrNotExist); gone == kept {
			t.Errorf("%s: kept = %v, want %v", name, !gone, kept)
		}
	}
}

func TestTmpDirApplyAsksTheProcessInspector(t *testing.T) {
	r, tmp, insp := tmpSetup(t)
	mkfile(t, filepath.Join(tmp, "named", "data"), "x")
	mkfile(t, filepath.Join(tmp, "free", "data"), "x")
	link := filepath.Join(r.Home, "link")
	if err := os.Symlink(tmp, link); err != nil {
		t.Fatal(err)
	}
	r.TmpDir = link
	ctx := context.Background()
	listed := r.List(ctx, nil)

	// After the listing, a process names one entry on its command line
	// through the symlink, which lsof alone does not report.
	insp.use(procs.UseArgs, filepath.Join(link, "named", "data"))
	o := tmpOutcome(t, r.Clean(ctx, listed, Options{Apply: true}))
	if o.Status != Ran || o.Command != tmpCommand(1) {
		t.Errorf("outcome = %+v", o)
	}
	if got := refusedReasons(o.Entry)["named"]; !strings.Contains(got, "whose command line names") {
		t.Errorf("refusal = %q", got)
	}
	if _, err := os.Lstat(filepath.Join(tmp, "named")); err != nil {
		t.Errorf("named: %v", err)
	}
	if _, err := os.Lstat(filepath.Join(tmp, "free")); !errors.Is(err, os.ErrNotExist) {
		t.Errorf("free: %v", err)
	}
}

func TestTmpDirApplySkipsWhenTheProcessListFails(t *testing.T) {
	r, tmp, insp := tmpSetup(t)
	mkfile(t, filepath.Join(tmp, "old"), "old")
	ctx := context.Background()
	listed := r.List(ctx, nil)

	// The listing succeeded; the list before the deletion fails.
	insp.err = errors.New("lsof did not finish in 30s")
	o := tmpOutcome(t, r.Clean(ctx, listed, Options{Apply: true}))
	if o.Status != Skipped || o.Reason != "cannot list processes: lsof did not finish in 30s" || o.FreedBytes != 0 {
		t.Errorf("outcome = %+v", o)
	}
	if _, err := os.Lstat(filepath.Join(tmp, "old")); err != nil {
		t.Error(err)
	}
}

// replaceDir puts a new directory with a file in it at path,
// so that path names a directory with a different identity.
func replaceDir(t *testing.T, path string) {
	t.Helper()
	fresh := path + ".fresh"
	mkfile(t, filepath.Join(fresh, "new"), "new")
	if err := os.Rename(path, path+".old"); err != nil {
		t.Fatal(err)
	}
	if err := os.Rename(fresh, path); err != nil {
		t.Fatal(err)
	}
}

func TestTmpDirApplyKeepsAReplacedEntry(t *testing.T) {
	r, tmp, insp := tmpSetup(t)
	mkfile(t, filepath.Join(tmp, "a", "old"), "old")
	ctx := context.Background()
	listed := r.List(ctx, nil)

	// After the final check of a, and before its deletion,
	// another directory takes its name.
	insp.onSnapshot = func() { replaceDir(t, filepath.Join(tmp, "a")) }
	o := tmpOutcome(t, r.Clean(ctx, listed, Options{Apply: true}))
	if got := refusedReasons(o.Entry)["a"]; got != "was replaced after plonge checked it" {
		t.Errorf("refusal = %q, outcome = %+v", got, o)
	}
	if _, err := os.Stat(filepath.Join(tmp, "a", "new")); err != nil {
		t.Error(err)
	}
}

func TestTmpDirApplyKeepsAReplacedTemporaryDirectory(t *testing.T) {
	r, tmp, insp := tmpSetup(t)
	mkfile(t, filepath.Join(tmp, "a", "old"), "old")
	victim := filepath.Join(r.Home, "victim")
	mkfile(t, filepath.Join(victim, "a", "keep"), "keep")
	ctx := context.Background()
	listed := r.List(ctx, nil)

	// After the final check, the temporary directory's path
	// becomes a symlink to a directory with the same entry names.
	insp.onSnapshot = func() {
		if err := os.Rename(tmp, tmp+".old"); err != nil {
			t.Fatal(err)
		}
		if err := os.Symlink(victim, tmp); err != nil {
			t.Fatal(err)
		}
	}
	o := tmpOutcome(t, r.Clean(ctx, listed, Options{Apply: true}))
	if o.Command != tmpCommand(0) {
		t.Errorf("outcome = %+v", o)
	}
	if _, err := os.Stat(filepath.Join(victim, "a", "keep")); err != nil {
		t.Error(err)
	}
}

func TestTmpRemoveWorksInTheOpenDirectory(t *testing.T) {
	r, tmp, _ := tmpSetup(t)
	mkfile(t, filepath.Join(tmp, "a", "sub", "file"), "x")
	mkfile(t, filepath.Join(tmp, "a", "other"), "x")
	if err := os.Chmod(filepath.Join(tmp, "a", "sub"), 0o555); err != nil {
		t.Fatal(err)
	}
	d, err := openTmpDir(tmp)
	if err != nil {
		t.Fatal(err)
	}
	defer d.Close()
	ids := map[string]fileID{}
	if reason := r.tmpCheck(d.root, "a", "", r.tmpPolicy(d, "a", r.clock().Add(-r.TmpAge), nil), ids); reason != "" {
		t.Fatal(reason)
	}

	// After the check, the temporary directory's path becomes a symlink
	// to another directory with the same names, and a file in the open
	// directory is replaced.
	victim := filepath.Join(r.Home, "victim")
	mkfile(t, filepath.Join(victim, "a", "other"), "keep")
	moved := tmp + ".old"
	if err := os.Rename(tmp, moved); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(victim, tmp); err != nil {
		t.Fatal(err)
	}
	mkfile(t, filepath.Join(moved, "a", "other.new"), "new")
	if err := os.Rename(filepath.Join(moved, "a", "other.new"), filepath.Join(moved, "a", "other")); err != nil {
		t.Fatal(err)
	}

	err = tmpRemove(d.root, d.path, "a", "", ids)
	if err == nil || !strings.Contains(err.Error(), "other: kept, because it was replaced") {
		t.Errorf("err = %v", err)
	}
	if _, err := os.Stat(filepath.Join(victim, "a", "other")); err != nil {
		t.Errorf("victim: %v", err)
	}
	if _, err := os.Stat(filepath.Join(moved, "a", "other")); err != nil {
		t.Errorf("replaced file: %v", err)
	}
	if _, err := os.Lstat(filepath.Join(moved, "a", "sub")); !errors.Is(err, os.ErrNotExist) {
		t.Errorf("read-only folder: %v", err)
	}
}

func TestTmpDirRefusesABindMountInsideAnEntry(t *testing.T) {
	r, tmp, _ := tmpSetup(t)
	mkfile(t, filepath.Join(tmp, "bound", "sub", "file"), "x")
	mkfile(t, filepath.Join(tmp, "plain", "file"), "x")
	ctx := context.Background()
	listed := r.List(ctx, nil)

	// After the listing, a folder on the same device is bind-mounted
	// at bound/sub. Its device number does not change.
	real, err := filepath.EvalSymlinks(tmp)
	if err != nil {
		t.Fatal(err)
	}
	r.mountsHook = func() ([]string, error) {
		return []string{"/", filepath.Join(real, "bound", "sub"), real + "-other"}, nil
	}
	want := map[string]string{"bound": "holds sub, which is a mount point"}
	if got := refusedReasons(tmpEntryOf(t, r)); !maps.Equal(got, want) {
		t.Errorf("listing refused = %q", got)
	}
	o := tmpOutcome(t, r.Clean(ctx, listed, Options{Apply: true}))
	if got := refusedReasons(o.Entry); !maps.Equal(got, want) {
		t.Errorf("apply refused = %q", got)
	}
	if _, err := os.Stat(filepath.Join(tmp, "bound", "sub", "file")); err != nil {
		t.Error(err)
	}
	if _, err := os.Lstat(filepath.Join(tmp, "plain")); !errors.Is(err, os.ErrNotExist) {
		t.Errorf("plain: %v", err)
	}

	// When the mount table cannot be read, the rule does not run.
	r.mountsHook = func() ([]string, error) { return nil, errors.New("no access") }
	if e := tmpEntryOf(t, r); e.Skipped != "cannot list mount points: no access" {
		t.Errorf("entry = %+v", e)
	}
	o = tmpOutcome(t, r.Clean(ctx, listed, Options{Apply: true}))
	if o.Status != Skipped {
		t.Errorf("outcome = %+v", o)
	}
}

func TestMountPointsListsTheRoot(t *testing.T) {
	paths, err := mountPoints()
	if err != nil {
		t.Fatal(err)
	}
	if !slices.Contains(paths, "/") {
		t.Errorf("mount points = %q", paths)
	}
}

func TestUnescapeOctal(t *testing.T) {
	for in, want := range map[string]string{
		`/mnt/a\040b`: "/mnt/a b", `/x\134y`: `/x\y`, `/x\0`: `/x\0`, "/plain": "/plain",
	} {
		if got := unescapeOctal(in); got != want {
			t.Errorf("unescapeOctal(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestTmpRemoveKeepsAFileReplacedBeforeItsRemoval(t *testing.T) {
	r, tmp, _ := tmpSetup(t)
	mkfile(t, filepath.Join(tmp, "a", "target"), "old")
	mkfile(t, filepath.Join(tmp, "a", "other"), "old")
	d, err := openTmpDir(tmp)
	if err != nil {
		t.Fatal(err)
	}
	defer d.Close()
	ids := map[string]fileID{}
	if reason := r.tmpCheck(d.root, "a", "", r.tmpPolicy(d, "a", r.clock().Add(-r.TmpAge), nil), ids); reason != "" {
		t.Fatal(reason)
	}

	// After tmpRemove checks a/target's identity and before it removes it,
	// another file takes its name.
	target := filepath.Join(tmp, "a", "target")
	tmpRemoveHook = func(stage, rel string) {
		if stage == "isolate" && rel == "target" {
			mkfile(t, target+".new", "new")
			if err := os.Rename(target+".new", target); err != nil {
				t.Fatal(err)
			}
		}
	}
	t.Cleanup(func() { tmpRemoveHook = nil })

	err = tmpRemove(d.root, d.path, "a", "", ids)
	if err == nil || !strings.Contains(err.Error(), "target: kept as "+target+", because it was replaced") {
		t.Errorf("err = %v", err)
	}
	if body, err := os.ReadFile(target); err != nil || string(body) != "new" {
		t.Errorf("replacement = %q, %v", body, err)
	}
	if _, err := os.Lstat(filepath.Join(tmp, "a", "other")); !errors.Is(err, os.ErrNotExist) {
		t.Errorf("other: %v", err)
	}
	ents, _ := os.ReadDir(filepath.Join(tmp, "a"))
	if len(ents) != 1 {
		t.Errorf("a holds %d entries, want only the replacement", len(ents))
	}
}

func TestTmpRemoveDoesNotOverwriteAFileAtTheOriginalName(t *testing.T) {
	r, tmp, _ := tmpSetup(t)
	target := filepath.Join(tmp, "a", "target")
	mkfile(t, target, "old")
	d, err := openTmpDir(tmp)
	if err != nil {
		t.Fatal(err)
	}
	defer d.Close()
	ids := map[string]fileID{}
	if reason := r.tmpCheck(d.root, "a", "", r.tmpPolicy(d, "a", r.clock().Add(-r.TmpAge), nil), ids); reason != "" {
		t.Fatal(reason)
	}

	// A replacement takes a/target before its isolation,
	// and a third file takes the name before the replacement is restored.
	tmpRemoveHook = func(stage, rel string) {
		if rel != "target" {
			return
		}
		switch stage {
		case "isolate":
			mkfile(t, target+".new", "replacement")
			if err := os.Rename(target+".new", target); err != nil {
				t.Fatal(err)
			}
		case "restore":
			mkfile(t, target, "third")
		}
	}
	t.Cleanup(func() { tmpRemoveHook = nil })

	err = tmpRemove(d.root, d.path, "a", "", ids)
	prefix := "target: kept as " + filepath.Join(tmp, "a", ".plonge-delete-")
	if err == nil || !strings.Contains(err.Error(), prefix) {
		t.Fatalf("err = %v, want it to contain %q", err, prefix)
	}
	if body, err := os.ReadFile(target); err != nil || string(body) != "third" {
		t.Errorf("file at the original name = %q, %v", body, err)
	}
	isolated := strings.TrimPrefix(err.Error(), "target: kept as ")
	isolated, _, _ = strings.Cut(isolated, ",")
	if body, err := os.ReadFile(isolated); err != nil || string(body) != "replacement" {
		t.Errorf("isolated replacement = %q, %v", body, err)
	}
}
