package global

import (
	"context"
	"fmt"
	"os"
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
