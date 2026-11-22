package global

import (
	"context"
	"encoding/xml"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"slices"
	"strings"

	"github.com/mattt/plonge/internal/clean"
	"github.com/mattt/plonge/internal/rules"
	"github.com/mattt/plonge/internal/scan"
)

// Xcode has no prune command, so this one rule is built in.
// A folder in DerivedData is stale when its info.plist names a WorkspacePath
// that no longer exists. Plonge keeps every other folder.
// It also refuses a stale folder that holds a Git repository
// with work that Git cannot restore, as scan.GuardDerivedData describes,
// and reports it in the entry's RefusedFolders.

// xcodeEntry returns the rule for stale DerivedData folders,
// or false when it does not apply on this machine.
func (r Runner) xcodeEntry(ctx context.Context) (Entry, bool) {
	if r.GOOS != "darwin" {
		return Entry{}, false
	}
	dir := filepath.Join(r.Home, "Library", "Developer", "Xcode", "DerivedData")
	ents, err := os.ReadDir(dir)
	if err != nil {
		return Entry{}, false
	}
	e := Entry{ID: rules.XcodeRuleID, Tool: "xcode", Kind: rules.KindPrune, Path: dir, stale: []string{}}
	var stale []string
	for _, ent := range ents {
		if folder := filepath.Join(dir, ent.Name()); ent.IsDir() && staleFolder(folder) {
			stale = append(stale, folder)
		}
	}
	sizes, reasons := scan.GuardDerivedData(ctx, stale, true)
	for i, folder := range stale {
		if reasons[i] != "" {
			e.refuseFolder(folder, reasons[i])
			continue
		}
		e.stale = append(e.stale, folder)
		e.Bytes += sizes[i]
	}
	e.Command = xcodeCommand(len(e.stale))
	return e, true
}

// Folders returns the folders that the Xcode rule would delete,
// or nil for every other rule.
func (e Entry) Folders() []string {
	return slices.Clone(e.stale)
}

// TargetPaths returns the installation paths that the rule's targets command listed,
// or nil when the rule has no targets.
func (e Entry) TargetPaths() []string {
	return slices.Clone(e.targetPaths)
}

// xcodeCommand is the Command of the Xcode rule when it deletes n folders.
func xcodeCommand(n int) string {
	return fmt.Sprintf("delete %d stale DerivedData directories (built in)", n)
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

// refuseFolder records a stale folder that plonge must not delete.
func (e *Entry) refuseFolder(folder, reason string) {
	e.RefusedFolders = append(e.RefusedFolders, scan.Refusal{Path: folder, Rule: e.ID, Reason: reason})
}

// applyXcode deletes the stale folders, checking each one again first.
// A folder that now holds a Git repository with work that Git cannot restore
// is refused and kept, and the other folders are still deleted.
// The outcome's size, action, and folders are rebuilt from the folders
// that pass this final check, and its size is what they held right before.
func applyXcode(ctx context.Context, o *Outcome) {
	// The outcome shares its slices with the listed entry; do not write to them.
	o.RefusedFolders = slices.Clone(o.RefusedFolders)
	listed := o.stale
	o.stale, o.Bytes = []string{}, 0
	var failures []string
	for _, folder := range listed {
		info, err := os.Lstat(folder)
		if err != nil || !info.IsDir() || !staleFolder(folder) {
			continue
		}
		if _, reasons := scan.GuardDerivedData(ctx, []string{folder}, false); reasons[0] != "" {
			o.refuseFolder(folder, reasons[0])
			continue
		}
		before := scan.DiskUsage(folder)
		o.stale = append(o.stale, folder)
		o.Bytes += before
		if err := clean.RemoveTree(folder); err != nil {
			failures = append(failures, err.Error())
		}
		o.FreedBytes += max(0, before-scan.DiskUsage(folder))
	}
	o.Command = xcodeCommand(len(o.stale))
	if len(failures) > 0 {
		o.Status, o.Reason = Failed, strings.Join(failures, "; ")
		return
	}
	o.Status = Ran
}
