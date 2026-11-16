package global

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

const plistFormat = `<?xml version="1.0" encoding="UTF-8"?>
<!DOCTYPE plist PUBLIC "-//Apple//DTD PLIST 1.0//EN" "http://www.apple.com/DTDs/PropertyList-1.0.dtd">
<plist version="1.0">
<dict>
	<key>LastAccessedDate</key>
	<date>2026-01-01T00:00:00Z</date>
	<key>WorkspacePath</key>
	<string>%s</string>
</dict>
</plist>
`

func derivedFolder(t *testing.T, root, name, plist string) string {
	t.Helper()
	dir := filepath.Join(root, name)
	if err := os.MkdirAll(filepath.Join(dir, "Build"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "Build", "blob"), make([]byte, 50_000), 0o644); err != nil {
		t.Fatal(err)
	}
	if plist != "" {
		if err := os.WriteFile(filepath.Join(dir, "info.plist"), []byte(plist), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	return dir
}

func TestXcodeDerivedData(t *testing.T) {
	fakeTools(t)
	r := Runner{Home: t.TempDir(), GOOS: "darwin"}
	root := filepath.Join(r.Home, "Library", "Developer", "Xcode", "DerivedData")
	alive := filepath.Join(r.Home, "App.xcworkspace")
	if err := os.MkdirAll(alive, 0o755); err != nil {
		t.Fatal(err)
	}
	gone := filepath.Join(r.Home, "Gone.xcworkspace")

	stale := derivedFolder(t, root, "Gone-abc", fmt.Sprintf(plistFormat, gone))
	kept := []string{
		derivedFolder(t, root, "App-abc", fmt.Sprintf(plistFormat, alive)),
		derivedFolder(t, root, "NoPlist-abc", ""),
		derivedFolder(t, root, "ModuleCache.noindex", ""),
		derivedFolder(t, root, "Binary-abc", "bplist00\x00\x01binary"),
		derivedFolder(t, root, "NoKey-abc", "<plist><dict><key>Other</key><string>/x</string></dict></plist>"),
		derivedFolder(t, root, "Relative-abc", fmt.Sprintf(plistFormat, "relative/path")),
		derivedFolder(t, root, "Unmounted-abc", fmt.Sprintf(plistFormat, "/Volumes/plonge-no-such-volume/App.xcworkspace")),
	}
	target := derivedFolder(t, r.Home, "elsewhere", fmt.Sprintf(plistFormat, gone))
	if err := os.Symlink(target, filepath.Join(root, "Link-abc")); err != nil {
		t.Fatal(err)
	}

	ctx := context.Background()
	entries := r.List(ctx, nil)
	if len(entries) != 1 {
		t.Fatalf("entries = %+v", entries)
	}
	e := entries[0]
	if e.ID != "xcode-derived-data" || e.Kind != "prune" || e.Path != root || e.Bytes < 50_000 || e.Bytes > 200_000 ||
		!strings.Contains(e.Command, "delete 1 stale") {
		t.Errorf("entry = %+v", e)
	}

	if res := r.Clean(ctx, entries, Options{}); res.Outcomes[0].Status != WouldRun {
		t.Errorf("dry run = %+v", res)
	}
	if _, err := os.Stat(stale); err != nil {
		t.Fatalf("the dry run deleted a folder: %v", err)
	}

	res := r.Clean(ctx, entries, Options{Apply: true})
	if res.Outcomes[0].Status != Ran || res.FreedBytes < 50_000 {
		t.Errorf("result = %+v", res)
	}
	if _, err := os.Stat(stale); !os.IsNotExist(err) {
		t.Errorf("the stale folder is still there: %v", err)
	}
	for _, dir := range append(kept, target) {
		if _, err := os.Stat(dir); err != nil {
			t.Errorf("%s is gone: %v", filepath.Base(dir), err)
		}
	}
}

// TestXcodeRechecksBeforeDeleting restores a workspace after the listing.
func TestXcodeRechecksBeforeDeleting(t *testing.T) {
	fakeTools(t)
	r := Runner{Home: t.TempDir(), GOOS: "darwin"}
	root := filepath.Join(r.Home, "Library", "Developer", "Xcode", "DerivedData")
	workspace := filepath.Join(r.Home, "Back.xcworkspace")
	folder := derivedFolder(t, root, "Back-abc", fmt.Sprintf(plistFormat, workspace))
	ctx := context.Background()
	entries := r.List(ctx, nil)
	if err := os.MkdirAll(workspace, 0o755); err != nil {
		t.Fatal(err)
	}
	if res := r.Clean(ctx, entries, Options{Apply: true}); res.FreedBytes != 0 {
		t.Errorf("result = %+v", res)
	}
	if _, err := os.Stat(folder); err != nil {
		t.Errorf("the folder is gone: %v", err)
	}
}

func TestXcodeOnlyOnDarwinWithTheDirectory(t *testing.T) {
	fakeTools(t)
	home := t.TempDir()
	if got := (Runner{Home: home, GOOS: "darwin"}).List(context.Background(), nil); len(got) != 0 {
		t.Errorf("no directory: %+v", got)
	}
	derivedFolder(t, filepath.Join(home, "Library", "Developer", "Xcode", "DerivedData"), "X-abc", "")
	if got := (Runner{Home: home, GOOS: "linux"}).List(context.Background(), nil); len(got) != 0 {
		t.Errorf("linux: %+v", got)
	}
}

// staleWithClones makes a stale DerivedData folder in root whose
// SourcePackages hold a bare mirror of a dependency and a checkout of it,
// laid out as Xcode's SwiftPM lays them out. The checkout is detached
// at the dependency's first commit, which no Package.resolved pins,
// because the workspace that held the pins is gone.
// It returns the folder and the checkout.
func staleWithClones(t *testing.T, home, root, name string) (folder, checkout string) {
	t.Helper()
	gone := filepath.Join(home, name+".xcworkspace")
	folder = derivedFolder(t, root, name+"-abc", fmt.Sprintf(plistFormat, gone))
	upstream := filepath.Join(t.TempDir(), "dep")
	commitRepo(t, upstream)
	first := strings.TrimSpace(gitOutput(t, upstream, "rev-parse", "HEAD"))
	addFile(t, filepath.Join(upstream, "more"))
	gitRun(t, upstream, "add", ".")
	gitRun(t, upstream, "-c", "user.name=T", "-c", "user.email=t@example.com", "commit", "-q", "-m", "two")

	packages := filepath.Join(folder, "SourcePackages")
	mirror := filepath.Join(packages, "repositories", "dep-95d00fdd")
	checkout = filepath.Join(packages, "checkouts", "dep")
	for _, dir := range []string{filepath.Dir(mirror), filepath.Dir(checkout)} {
		if err := os.MkdirAll(dir, 0o755); err != nil {
			t.Fatal(err)
		}
	}
	gitRun(t, packages, "clone", "-q", "--mirror", upstream, mirror)
	gitRun(t, packages, "clone", "-q", "--shared", "--no-checkout", mirror, checkout)
	gitRun(t, checkout, "checkout", "-q", "--detach", first)
	return folder, checkout
}

func gitOutput(t *testing.T, dir string, args ...string) string {
	t.Helper()
	cmd := exec.Command("git", args...)
	cmd.Dir = dir
	out, err := cmd.Output()
	if err != nil {
		t.Fatalf("git %v: %v", args, err)
	}
	return string(out)
}

// TestXcodeRefusesFoldersWithWork lists stale folders whose Git repositories
// hold work that Git cannot restore, keeps them, and deletes the one
// whose SwiftPM clones Xcode can make again.
func TestXcodeRefusesFoldersWithWork(t *testing.T) {
	bin := t.TempDir()
	withGit(t, bin)
	r := Runner{Home: realTempDir(t), GOOS: "darwin"}
	root := filepath.Join(r.Home, "Library", "Developer", "Xcode", "DerivedData")

	clean, _ := staleWithClones(t, r.Home, root, "Clean")

	modified, checkout := staleWithClones(t, r.Home, root, "Modified")
	if err := os.WriteFile(filepath.Join(checkout, "work"), []byte("edit"), 0o644); err != nil {
		t.Fatal(err)
	}

	committed, checkout := staleWithClones(t, r.Home, root, "Committed")
	head := strings.TrimSpace(gitOutput(t, checkout, "rev-parse", "HEAD"))
	gitRun(t, checkout, "switch", "-q", "-c", "local")
	addFile(t, filepath.Join(checkout, "local"))
	gitRun(t, checkout, "add", ".")
	gitRun(t, checkout, "-c", "user.name=T", "-c", "user.email=t@example.com", "commit", "-q", "-m", "local")
	gitRun(t, checkout, "checkout", "-q", "--detach", head)

	unrelated, _ := staleWithClones(t, r.Home, root, "Unrelated")
	commitRepo(t, filepath.Join(unrelated, "Build", "Intermediates.noindex", "notes"))

	unreadable := derivedFolder(t, root, "Unreadable-abc", fmt.Sprintf(plistFormat, filepath.Join(r.Home, "Unreadable.xcworkspace")))
	locked := filepath.Join(unreadable, "Build", "Locked")
	if err := os.Mkdir(locked, 0o000); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Chmod(locked, 0o755) })
	if _, err := os.ReadDir(locked); err == nil {
		t.Skip("a directory with mode 0 is readable, as it is for root")
	}

	ctx := context.Background()
	entries := r.List(ctx, nil)
	if len(entries) != 1 {
		t.Fatalf("entries = %+v", entries)
	}
	e := entries[0]
	if !strings.Contains(e.Command, "delete 1 stale") || e.Bytes < 50_000 || e.Bytes > 1_000_000 {
		t.Errorf("entry = %+v", e)
	}
	want := map[string]string{
		modified:   "contains a Git repository at SourcePackages/checkouts/dep that has changed, untracked, or ignored files",
		committed:  "contains a Git repository at SourcePackages/checkouts/dep that has commits that only its local branches, tags, or reflogs hold",
		unrelated:  "contains a Git repository at Build/Intermediates.noindex/notes",
		unreadable: "cannot check for Git repositories: ",
	}
	if len(e.RefusedFolders) != len(want) {
		t.Errorf("refused = %+v", e.RefusedFolders)
	}
	for _, f := range e.RefusedFolders {
		if w, ok := want[f.Path]; !ok || !strings.HasPrefix(f.Reason, w) || f.Rule != e.ID {
			t.Errorf("refusal %+v, want reason %q", f, w)
		}
	}

	res := r.Clean(ctx, entries, Options{Apply: true})
	if o := res.Outcomes[0]; o.Status != Ran || len(o.RefusedFolders) != len(want) {
		t.Errorf("outcome = %+v", o)
	}
	if _, err := os.Stat(clean); !os.IsNotExist(err) {
		t.Errorf("the folder with clean clones is still there: %v", err)
	}
	for folder := range want {
		if _, err := os.Stat(filepath.Join(folder, "Build", "blob")); err != nil {
			t.Errorf("%s lost its files: %v", filepath.Base(folder), err)
		}
	}
}

// TestXcodeRechecksRepositoriesBeforeDeleting adds a repository to a stale
// folder after the listing, and to the clones of another one.
func TestXcodeRechecksRepositoriesBeforeDeleting(t *testing.T) {
	bin := t.TempDir()
	withGit(t, bin)
	r := Runner{Home: realTempDir(t), GOOS: "darwin"}
	root := filepath.Join(r.Home, "Library", "Developer", "Xcode", "DerivedData")
	plain := derivedFolder(t, root, "Plain-abc", fmt.Sprintf(plistFormat, filepath.Join(r.Home, "Plain.xcworkspace")))
	cloned, checkout := staleWithClones(t, r.Home, root, "Cloned")

	ctx := context.Background()
	entries := r.List(ctx, nil)
	if e := entries[0]; !strings.Contains(e.Command, "delete 2 stale") || len(e.RefusedFolders) != 0 {
		t.Fatalf("entry = %+v", e)
	}
	commitRepo(t, filepath.Join(plain, "Build", "repo"))
	if err := os.WriteFile(filepath.Join(checkout, "untracked"), nil, 0o644); err != nil {
		t.Fatal(err)
	}

	res := r.Clean(ctx, entries, Options{Apply: true})
	o := res.Outcomes[0]
	if o.Status != Ran || res.FreedBytes != 0 || len(o.RefusedFolders) != 2 {
		t.Errorf("outcome = %+v", o)
	}
	if len(entries[0].RefusedFolders) != 0 {
		t.Errorf("the listed entry changed: %+v", entries[0].RefusedFolders)
	}
	for _, folder := range []string{plain, cloned} {
		if _, err := os.Stat(filepath.Join(folder, "Build", "blob")); err != nil {
			t.Errorf("%s lost its files: %v", filepath.Base(folder), err)
		}
	}
}
