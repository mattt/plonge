package global

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"

	"github.com/mattt/plonge/internal/rules"
)

// miseDoctor prints mise's directories as JSON.
// mise has no command that prints its installs directory itself.
const miseDoctor = "mise doctor --json"

// rulePath returns the directory that a rule measures.
// For mise's installed versions, it is the installs directory that mise uses.
// When mise cannot report that directory, the rule's path is used with a note.
func (r Runner) rulePath(ctx context.Context, e *Entry, g rules.Global, t tool) (string, error) {
	if g.ID != rules.MiseToolsRuleID || g.Tool != "mise" {
		return r.cachePath(ctx, g, t)
	}
	dir, err := r.miseInstallsDir(ctx, t)
	if err == nil {
		return dir, nil
	}
	path, pathErr := r.cachePath(ctx, g, t)
	if pathErr != nil {
		return "", pathErr
	}
	e.addNote("path is the rule's default: cannot read mise's installs directory: " + err.Error())
	return path, nil
}

// miseInstallsDir returns $MISE_INSTALLS_DIR, or the installs directory
// in the data directory that mise reports, as mise itself chooses it.
func (r Runner) miseInstallsDir(ctx context.Context, t tool) (string, error) {
	if dir := os.Getenv("MISE_INSTALLS_DIR"); dir != "" {
		dir = r.expand(dir)
		if !filepath.IsAbs(dir) {
			return "", fmt.Errorf("MISE_INSTALLS_DIR is %q, which is not an absolute path", dir)
		}
		return filepath.Clean(dir), nil
	}
	lookup, cancel := context.WithTimeout(ctx, lookupTimeout)
	defer cancel()
	argv, _ := command(t, miseDoctor)
	out, err := r.run(lookup, argv)
	// mise doctor exits 1 when it finds a problem in the setup,
	// but it still prints its directories.
	var exit *exec.ExitError
	if err != nil && (!errors.As(err, &exit) || exit.ExitCode() != 1) {
		return "", err
	}
	var report struct {
		Dirs struct {
			Data string `json:"data"`
		} `json:"dirs"`
	}
	if jsonErr := json.Unmarshal([]byte(out), &report); jsonErr != nil {
		if err != nil {
			return "", err
		}
		return "", fmt.Errorf("%s printed invalid JSON: %w", miseDoctor, jsonErr)
	}
	if !filepath.IsAbs(report.Dirs.Data) {
		return "", fmt.Errorf("%s printed data directory %q, which is not an absolute path", miseDoctor, report.Dirs.Data)
	}
	return filepath.Join(report.Dirs.Data, "installs"), nil
}

// addNote adds a note to the entry's notes.
func (e *Entry) addNote(note string) {
	if e.Note != "" {
		e.Note += "; "
	}
	e.Note += note
}
