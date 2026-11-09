package main

import (
	"bytes"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// setup gives the command a temporary home, mise state, and Git configuration.
func setup(t *testing.T) (home string) {
	t.Helper()
	home = t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("XDG_STATE_HOME", filepath.Join(home, "state"))
	t.Setenv("XDG_CONFIG_HOME", filepath.Join(home, "config"))
	for _, name := range []string{"MISE_STATE_DIR", "MISE_CONFIG_DIR", "MISE_GLOBAL_CONFIG_FILE"} {
		t.Setenv(name, "")
	}
	t.Setenv("GIT_CONFIG_GLOBAL", os.DevNull)
	t.Setenv("GIT_CONFIG_NOSYSTEM", "1")
	return home
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

func gitInit(t *testing.T, dir string) {
	t.Helper()
	if out, err := exec.Command("git", "init", "-q", dir).CombinedOutput(); err != nil {
		t.Fatalf("git init: %v\n%s", err, out)
	}
}

func TestScanJSON(t *testing.T) {
	home := setup(t)
	project := filepath.Join(home, "code", "app")
	gitInit(t, project)
	mkfile(t, filepath.Join(project, "mise.toml"), "")
	mkfile(t, filepath.Join(project, ".gitignore"), "node_modules/\n.cache/\n")
	mkfile(t, filepath.Join(project, "node_modules", "x.js"), "x")
	mkfile(t, filepath.Join(project, ".cache", "y"), "y")
	extra := filepath.Join(home, "extra")
	mkfile(t, filepath.Join(extra, "node_modules", "z.js"), "")

	tracked := filepath.Join(home, "state", "mise", "tracked-configs")
	if err := os.MkdirAll(tracked, 0o755); err != nil {
		t.Fatal(err)
	}
	for name, target := range map[string]string{
		"app":    filepath.Join(project, "mise.toml"),
		"broken": filepath.Join(home, "gone", "mise.toml"),
	} {
		if err := os.Symlink(target, filepath.Join(tracked, name)); err != nil {
			t.Fatal(err)
		}
	}
	// A user rule adds .cache.
	mkfile(t, filepath.Join(home, "config", "plonge", "rules.toml"), "names = [\".cache\"]\n")

	var stdout, stderr bytes.Buffer
	t.Setenv("GIT_CEILING_DIRECTORIES", home)
	code := run([]string{"scan", "--json", "--root", extra}, &stdout, &stderr)
	if code != 0 {
		t.Fatalf("exit %d: %s", code, stderr.String())
	}
	var got struct {
		Caches []struct {
			Path, Project, Rule string
		} `json:"caches"`
		Totals struct {
			Caches int `json:"caches"`
		} `json:"totals"`
		Roots        int `json:"roots"`
		SkippedRoots []struct {
			Path string `json:"path"`
		} `json:"skipped_roots"`
		BrokenLinks int `json:"broken_links"`
	}
	if err := json.Unmarshal(stdout.Bytes(), &got); err != nil {
		t.Fatalf("%v\n%s", err, stdout.String())
	}
	rules := map[string]string{}
	for _, c := range got.Caches {
		if c.Project != project {
			t.Errorf("cache %s has project %s", c.Path, c.Project)
		}
		rules[strings.TrimPrefix(c.Path, project+"/")] = c.Rule
	}
	if len(rules) != 2 || rules["node_modules"] != "node_modules" || rules[".cache"] != ".cache" {
		t.Errorf("caches = %v", rules)
	}
	if got.Roots != 2 || len(got.SkippedRoots) != 1 || got.SkippedRoots[0].Path != extra || got.BrokenLinks != 1 {
		t.Errorf("roots %d, skipped %v, broken %d", got.Roots, got.SkippedRoots, got.BrokenLinks)
	}
}

func TestScanTable(t *testing.T) {
	home := setup(t)
	var stdout, stderr bytes.Buffer
	if code := run([]string{"scan", "--no-mise", "--root", home}, &stdout, &stderr); code != 0 {
		t.Fatalf("exit %d: %s", code, stderr.String())
	}
	if !strings.Contains(stdout.String(), "Total: 0 B in 0 caches") {
		t.Errorf("output:\n%s", stdout.String())
	}
}

func TestDisk(t *testing.T) {
	setup(t)
	tests := []struct {
		name string
		args []string
		code int
		out  string
		errs string
	}{
		{"plain", []string{"disk"}, 0, "Free space on ~: ", ""},
		{"below the size", []string{"disk", "--warn-below", "1000000TiB"}, 0, "Warning: only", ""},
		{"above the size", []string{"disk", "--warn-below", "0"}, 0, "", ""},
		{"bad size still exits 0", []string{"disk", "--warn-below", "lots"}, 0, "", "invalid size"},
	}
	for _, tt := range tests {
		var stdout, stderr bytes.Buffer
		code := run(tt.args, &stdout, &stderr)
		if code != tt.code || !strings.Contains(stdout.String(), tt.out) || !strings.Contains(stderr.String(), tt.errs) {
			t.Errorf("%s: exit %d, stdout %q, stderr %q", tt.name, code, stdout.String(), stderr.String())
		}
		if tt.out == "" && stdout.Len() != 0 {
			t.Errorf("%s: stdout %q, want none", tt.name, stdout.String())
		}
	}
}

func TestDiskWarnBelowExitsZeroWhenHomeIsMissing(t *testing.T) {
	setup(t)
	t.Setenv("HOME", filepath.Join(t.TempDir(), "gone"))
	var stdout, stderr bytes.Buffer
	if code := run([]string{"disk", "--warn-below", "1GiB"}, &stdout, &stderr); code != 0 {
		t.Errorf("exit %d, want 0", code)
	}
	if code := run([]string{"disk"}, &stdout, &stderr); code != 1 {
		t.Errorf("without --warn-below, exit %d, want 1", code)
	}
}

func TestUsage(t *testing.T) {
	tests := []struct {
		args []string
		code int
		out  string
	}{
		{[]string{"--help"}, 0, "Usage: plonge"},
		{[]string{"scan", "--help"}, 0, "--no-mise"},
		{nil, 2, ""},
		{[]string{"clean"}, 2, ""},
		{[]string{"scan", "--no-mise"}, 2, ""},
		{[]string{"scan", "extra"}, 2, ""},
		{[]string{"disk", "--help"}, 0, "--warn-below"},
		{[]string{"disk", "extra"}, 2, ""},
	}
	for _, tt := range tests {
		var stdout, stderr bytes.Buffer
		code := run(tt.args, &stdout, &stderr)
		if code != tt.code || !strings.Contains(stdout.String(), tt.out) {
			t.Errorf("run(%q) = %d, stdout %q", tt.args, code, stdout.String())
		}
	}
}
