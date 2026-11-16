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

// paseoTimeout limits each paseo ls call. Tests shorten it.
var paseoTimeout = 15 * time.Second

// Apply adds agent activity to roots and reports what happened to each source.
// A missing source is skipped, and a Claude source that fails is skipped too.
// When paseo is on PATH but Plonge cannot read its status,
// the Paseo source fails and every root is busy,
// because an agent might work in any of them.
func Apply(ctx context.Context, roots []scan.Root, home string, now time.Time) []scan.Source {
	sources := []scan.Source{applyClaude(roots, home)}
	cwds, source := busyDirs(ctx, home)
	sources = append(sources, source)
	for i := range roots {
		if roots[i].Skipped != "" {
			continue
		}
		switch {
		case source.Status == scan.SourceFailed:
			roots[i].Busy = true
		case busyIn(roots[i].Path, cwds):
			roots[i].Busy = true
			roots[i].LastActivity = now
			roots[i].ActivitySource = SourcePaseo
		}
	}
	return sources
}

// Busy asks Paseo again whether an agent might work in root.
// It returns why root is busy, or "" when it is not.
// Root is busy when Paseo reports an agent there,
// or when paseo is on PATH but Plonge cannot read its status.
// Root is not busy when paseo is not on PATH.
// Paseo writes paths inside home with a leading ~.
func Busy(ctx context.Context, root, home string) string {
	cwds, source := busyDirs(ctx, home)
	switch {
	case source.Status == scan.SourceFailed:
		return "Paseo status unknown: " + source.Reason
	case busyIn(root, cwds):
		return "Paseo now reports a running or initializing agent in this project"
	}
	return ""
}

func skipped(name, reason string) scan.Source {
	return scan.Source{Name: name, Status: scan.SourceSkipped, Reason: reason}
}

func failed(name, reason string) scan.Source {
	return scan.Source{Name: name, Status: scan.SourceFailed, Reason: reason}
}

// paseoStatuses lists the agent statuses that Paseo 0.15.4 defines
// in the agent status schema of its daemon protocol:
// "initializing", "idle", "running", "error", and "closed".
// The value tells whether an agent with that status might work in its directory.
var paseoStatuses = map[string]bool{
	"initializing": true,
	"running":      true,
	"idle":         false,
	"error":        false,
	"closed":       false,
}

// busyDirs returns the working directories of Paseo agents that are running or starting.
// The source fails when the output is not a JSON array,
// when an entry is not a JSON object, has no string status, or has a status
// that paseoStatuses does not list,
// or when a running or starting agent has no absolute working directory after ~ expansion,
// because Plonge then cannot tell which projects the agents work in.
func busyDirs(ctx context.Context, home string) ([]string, scan.Source) {
	if _, err := exec.LookPath("paseo"); err != nil {
		return nil, skipped(SourcePaseo, "paseo is not on PATH")
	}
	ctx, cancel := context.WithTimeout(ctx, paseoTimeout)
	defer cancel()
	// Without --global, paseo ls lists only the current directory's agents.
	cmd := exec.CommandContext(ctx, "paseo", "ls", "--json", "--global", "--background")
	// A child process that keeps the output open must not outlast the timeout.
	cmd.WaitDelay = time.Second
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	out, err := cmd.Output()
	if err != nil {
		reason := "paseo ls failed: " + err.Error()
		if ctx.Err() == context.DeadlineExceeded {
			reason = fmt.Sprintf("paseo ls did not finish in %v", paseoTimeout)
		} else if msg := strings.TrimSpace(stderr.String()); msg != "" {
			reason = "paseo ls failed: " + msg
		}
		return nil, failed(SourcePaseo, reason)
	}
	var list []json.RawMessage
	if err := json.Unmarshal(out, &list); err != nil {
		return nil, failed(SourcePaseo, fmt.Sprintf("cannot read paseo ls output: %v", err))
	}
	// JSON null decodes without an error but leaves list nil.
	if list == nil {
		return nil, failed(SourcePaseo, "cannot read paseo ls output: not a JSON array")
	}
	var cwds []string
	for i, raw := range list {
		status, cwd, problem := readAgent(raw)
		if problem != "" {
			return nil, failed(SourcePaseo, fmt.Sprintf("Paseo agent %d of %d %s", i+1, len(list), problem))
		}
		if !paseoStatuses[status] {
			continue
		}
		path := expandHome(cwd, home)
		if !filepath.IsAbs(path) {
			return nil, failed(SourcePaseo, fmt.Sprintf("a %s Paseo agent has no absolute working directory: %q", status, cwd))
		}
		cwds = append(cwds, real(path))
	}
	return cwds, scan.Source{Name: SourcePaseo, Status: scan.SourceUsed}
}

// readAgent reads the status and working directory of one paseo ls entry.
// It returns a problem, such as "has no status", when Plonge cannot use the entry.
// Keys must match exactly, so a "Status" key does not count as "status".
// A missing or null cwd reads as "".
func readAgent(raw json.RawMessage) (status, cwd, problem string) {
	var fields map[string]json.RawMessage
	// A JSON null decodes without an error but leaves fields nil.
	if err := json.Unmarshal(raw, &fields); err != nil || fields == nil {
		return "", "", "is not a JSON object"
	}
	rawStatus, ok := fields["status"]
	if !ok {
		return "", "", "has no status"
	}
	if !isJSONString(rawStatus) || json.Unmarshal(rawStatus, &status) != nil {
		return "", "", "has a status that is not a string"
	}
	if _, known := paseoStatuses[status]; !known {
		return "", "", fmt.Sprintf("has an unknown status %q", shorten(status, 40))
	}
	if rawCwd, ok := fields["cwd"]; ok && !bytes.Equal(bytes.TrimSpace(rawCwd), []byte("null")) {
		if !isJSONString(rawCwd) || json.Unmarshal(rawCwd, &cwd) != nil {
			return "", "", "has a cwd that is not a string"
		}
	}
	return status, cwd, ""
}

// isJSONString reports whether raw holds a JSON string, rather than null or another type.
func isJSONString(raw json.RawMessage) bool {
	raw = bytes.TrimSpace(raw)
	return len(raw) > 0 && raw[0] == '"'
}

// shorten cuts s to at most n runes, so that a reason stays short.
func shorten(s string, n int) string {
	if r := []rune(s); len(r) > n {
		return string(r[:n]) + "..."
	}
	return s
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

// expandHome replaces a leading ~ or ~/ in path with home.
// It leaves other paths, including ~user forms, unchanged.
// With an empty home, it leaves path unchanged, so the path stays relative.
func expandHome(path, home string) string {
	if home == "" {
		return path
	}
	if path == "~" {
		return home
	}
	if rest, ok := strings.CutPrefix(path, "~/"); ok {
		return filepath.Join(home, rest)
	}
	return path
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
