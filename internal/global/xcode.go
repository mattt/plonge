package global

import (
	"context"
	"encoding/xml"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strings"

	"github.com/mattt/plonge/internal/clean"
	"github.com/mattt/plonge/internal/rules"
	"github.com/mattt/plonge/internal/scan"
)

// Xcode has no prune command, so this one rule is built in.
// A folder in DerivedData is stale when its info.plist names a WorkspacePath
// that no longer exists. Plonge keeps every other folder.

// xcodeEntry returns the rule for stale DerivedData folders,
// or false when it does not apply on this machine.
func (r Runner) xcodeEntry() (Entry, bool) {
	if r.GOOS != "darwin" {
		return Entry{}, false
	}
	dir := filepath.Join(r.Home, "Library", "Developer", "Xcode", "DerivedData")
	ents, err := os.ReadDir(dir)
	if err != nil {
		return Entry{}, false
	}
	e := Entry{ID: rules.XcodeRuleID, Tool: "xcode", Kind: rules.KindPrune, Path: dir, stale: []string{}}
	for _, ent := range ents {
		if folder := filepath.Join(dir, ent.Name()); ent.IsDir() && staleFolder(folder) {
			e.stale = append(e.stale, folder)
		}
	}
	for _, n := range scan.Sizes(e.stale) {
		e.Bytes += n
	}
	e.Command = fmt.Sprintf("delete %d stale DerivedData directories (built in)", len(e.stale))
	return e, true
}

// staleFolder reports whether a DerivedData folder's workspace is gone.
// A folder without a readable WorkspacePath is not stale.
func staleFolder(folder string) bool {
	workspace, ok := workspacePath(filepath.Join(folder, "info.plist"))
	if !ok {
		return false
	}
	// A workspace on a volume that is not mounted may still exist.
	if rest, ok := strings.CutPrefix(workspace, "/Volumes/"); ok {
		volume, _, _ := strings.Cut(rest, "/")
		if _, err := os.Stat(filepath.Join("/Volumes", volume)); err != nil {
			return false
		}
	}
	_, err := os.Stat(workspace)
	return errors.Is(err, fs.ErrNotExist)
}

// workspacePath reads WorkspacePath from an XML property list.
func workspacePath(plist string) (string, bool) {
	data, err := os.ReadFile(plist)
	if err != nil {
		return "", false
	}
	var doc struct {
		Dict struct {
			Items []struct {
				XMLName xml.Name
				Value   string `xml:",chardata"`
			} `xml:",any"`
		} `xml:"dict"`
	}
	if err := xml.Unmarshal(data, &doc); err != nil {
		return "", false
	}
	items := doc.Dict.Items
	for i, item := range items {
		if item.XMLName.Local == "key" && item.Value == "WorkspacePath" && i+1 < len(items) {
			next := items[i+1]
			if next.XMLName.Local == "string" && filepath.IsAbs(next.Value) {
				return next.Value, true
			}
		}
	}
	return "", false
}

// applyXcode deletes the stale folders, checking each one again first.
func applyXcode(_ context.Context, o *Outcome) {
	var failures []string
	for _, folder := range o.stale {
		info, err := os.Lstat(folder)
		if err != nil || !info.IsDir() || !staleFolder(folder) {
			continue
		}
		before := scan.DiskUsage(folder)
		if err := clean.RemoveTree(folder); err != nil {
			failures = append(failures, err.Error())
		}
		o.FreedBytes += max(0, before-scan.DiskUsage(folder))
	}
	if len(failures) > 0 {
		o.Status, o.Reason = Failed, strings.Join(failures, "; ")
		return
	}
	o.Status = Ran
}
