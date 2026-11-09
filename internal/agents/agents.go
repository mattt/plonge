// Package agents marks project roots where a coding agent works.
package agents

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	"github.com/mattt/plonge/internal/scan"
)

// The names of the activity sources.
const (
	SourceClaude = "claude"
	SourcePaseo  = "paseo"
)

const paseoTimeout = 15 * time.Second

// Apply adds agent activity to roots and reports what happened to each source.
// A source that is missing or fails is skipped.
func Apply(ctx context.Context, roots []scan.Root, home string, now time.Time) []scan.Source {
	sources := []scan.Source{applyClaude(roots, home)}
	cwds, source := busyDirs(ctx)
	sources = append(sources, source)
	for i := range roots {
		if roots[i].Skipped == "" && busyIn(roots[i].Path, cwds) {
			roots[i].Busy = true
			roots[i].LastActivity = now
			roots[i].ActivitySource = SourcePaseo
		}
	}
	return sources
}

// Busy asks Paseo again whether an agent works in root.
// It returns false when Paseo is missing or fails.
func Busy(ctx context.Context, root string) bool {
	cwds, _ := busyDirs(ctx)
	return busyIn(root, cwds)
}

func skipped(name, reason string) scan.Source {
	return scan.Source{Name: name, Status: scan.SourceSkipped, Reason: reason}
}

// busyDirs returns the working directories of Paseo agents that are running or starting.
func busyDirs(ctx context.Context) ([]string, scan.Source) {
	if _, err := exec.LookPath("paseo"); err != nil {
		return nil, skipped(SourcePaseo, "paseo is not on PATH")
	}
	ctx, cancel := context.WithTimeout(ctx, paseoTimeout)
	defer cancel()
	// Without --global, paseo ls lists only the current directory's agents.
	cmd := exec.CommandContext(ctx, "paseo", "ls", "--json", "--global", "--background")
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	out, err := cmd.Output()
	if err != nil {
		reason := "paseo ls failed: " + err.Error()
		if msg := strings.TrimSpace(stderr.String()); msg != "" {
			reason = "paseo ls failed: " + msg
		}
		return nil, skipped(SourcePaseo, reason)
	}
	var list []struct {
		Cwd    string `json:"cwd"`
		Status string `json:"status"`
	}
	if err := json.Unmarshal(out, &list); err != nil {
		return nil, skipped(SourcePaseo, fmt.Sprintf("cannot read paseo ls output: %v", err))
	}
	var cwds []string
	for _, a := range list {
		if a.Cwd != "" && (a.Status == "running" || a.Status == "initializing") {
			cwds = append(cwds, real(a.Cwd))
		}
	}
	return cwds, scan.Source{Name: SourcePaseo, Status: scan.SourceUsed}
}

// busyIn reports whether a directory in cwds is at or below root.
func busyIn(root string, cwds []string) bool {
	root = real(root)
	for _, cwd := range cwds {
		if cwd == root || strings.HasPrefix(cwd, strings.TrimSuffix(root, "/")+"/") {
			return true
		}
	}
	return false
}

func real(path string) string {
	if p, err := filepath.EvalSymlinks(path); err == nil {
		return p
	}
	return filepath.Clean(path)
}

// applyClaude uses the newest transcript of each root as its activity.
func applyClaude(roots []scan.Root, home string) scan.Source {
	dir := filepath.Join(home, ".claude", "projects")
	if config := os.Getenv("CLAUDE_CONFIG_DIR"); config != "" {
		dir = filepath.Join(config, "projects")
	}
	ents, err := os.ReadDir(dir)
	if os.IsNotExist(err) {
		return skipped(SourceClaude, "no Claude Code projects directory")
	}
	if err != nil {
		return skipped(SourceClaude, err.Error())
	}
	var folders []string
	for _, ent := range ents {
		if ent.IsDir() {
			folders = append(folders, ent.Name())
		}
	}
	for i := range roots {
		if roots[i].Skipped != "" {
			continue
		}
		var newest time.Time
		for _, name := range matchingFolders(folders, roots[i].Path) {
			if t := newestTranscript(filepath.Join(dir, name)); t.After(newest) {
				newest = t
			}
		}
		if newest.After(roots[i].LastActivity) {
			roots[i].LastActivity = newest
			roots[i].ActivitySource = SourceClaude
		}
	}
	return scan.Source{Name: SourceClaude, Status: scan.SourceUsed}
}

// encodeFolder returns the folder name that Claude Code uses for a directory.
// Every character except an ASCII letter or digit becomes a hyphen.
func encodeFolder(path string) string {
	return strings.Map(func(r rune) rune {
		if (r >= 'a' && r <= 'z') || (r >= 'A' && r <= 'Z') || (r >= '0' && r <= '9') {
			return r
		}
		return '-'
	}, path)
}

// matchingFolders returns the folders of root and of the directories below it.
// The encoding is not reversible, so a sibling such as "app-two" also matches
// a root named "app". That only makes the root look more active.
func matchingFolders(folders []string, root string) []string {
	keys := []string{encodeFolder(root), encodeFolder(real(root))}
	var out []string
	for _, name := range folders {
		for _, key := range keys {
			if name == key || strings.HasPrefix(name, key+"-") {
				out = append(out, name)
				break
			}
		}
	}
	return out
}

// newestTranscript returns the latest modification time of a .jsonl file under dir.
func newestTranscript(dir string) time.Time {
	var newest time.Time
	_ = filepath.WalkDir(dir, func(path string, d fs.DirEntry, err error) error {
		if err != nil || d.IsDir() || filepath.Ext(path) != ".jsonl" {
			return nil
		}
		if info, err := d.Info(); err == nil && info.ModTime().After(newest) {
			newest = info.ModTime()
		}
		return nil
	})
	return newest
}
