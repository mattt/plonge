package main

import (
	"bytes"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// binDir is the only directory on PATH during a test.
// It holds Git and fake tools, so that no test can run a real tool.
func binDir(t *testing.T) string {
	t.Helper()
	git, err := exec.LookPath("git")
	if err != nil {
		t.Fatal(err)
	}
	dir := t.TempDir()
	if err := os.Symlink(git, filepath.Join(dir, "git")); err != nil {
		t.Fatal(err)
	}
	return dir
}

// setup gives the command a temporary home, mise state, and Git configuration,
// and replaces PATH.
func setup(t *testing.T) (home string) {
	t.Helper()
	t.Setenv("PATH", binDir(t))
	home = t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("XDG_STATE_HOME", filepath.Join(home, "state"))
	t.Setenv("XDG_CONFIG_HOME", filepath.Join(home, "config"))
	for _, name := range []string{"MISE_STATE_DIR", "MISE_CONFIG_DIR", "MISE_GLOBAL_CONFIG_FILE", "CLAUDE_CONFIG_DIR"} {
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

// quietRepo makes a repository with an ignored node_modules and no commits,
// so that its activity is old once HEAD's time is moved back.
func quietRepo(t *testing.T, dir string) {
	t.Helper()
	gitInit(t, dir)
	mkfile(t, filepath.Join(dir, ".gitignore"), "node_modules/\n")
	mkfile(t, filepath.Join(dir, "node_modules", "x.js"), "x")
	old := time.Now().Add(-72 * time.Hour)
	if err := os.Chtimes(filepath.Join(dir, ".git", "HEAD"), old, old); err != nil {
		t.Fatal(err)
	}
}

func TestClean(t *testing.T) {
	home := setup(t)
	t.Setenv("GIT_CEILING_DIRECTORIES", home)
	project := filepath.Join(home, "code", "app")
	quietRepo(t, project)
	cache := filepath.Join(project, "node_modules")

	var stdout, stderr bytes.Buffer
	args := []string{"clean", "--no-mise", "--root", project}
	if code := run(args, &stdout, &stderr); code != 0 {
		t.Fatalf("exit %d: %s", code, stderr.String())
	}
	out := stdout.String()
	for _, want := range []string{"~/code/app/node_modules", "would delete", "Would delete:", "in 1 cache", "Dry run"} {
		if !strings.Contains(out, want) {
			t.Errorf("dry run lacks %q:\n%s", want, out)
		}
	}
	if _, err := os.Stat(cache); err != nil {
		t.Fatalf("the dry run touched the cache: %v", err)
	}

	stdout.Reset()
	if code := run(append(args, "--apply"), &stdout, &stderr); code != 0 {
		t.Fatalf("exit %d: %s", code, stderr.String())
	}
	if !strings.Contains(stdout.String(), "Freed: ") {
		t.Errorf("output:\n%s", stdout.String())
	}
	if _, err := os.Stat(cache); !os.IsNotExist(err) {
		t.Errorf("the cache is still there: %v", err)
	}
	if _, err := os.Stat(filepath.Join(project, ".gitignore")); err != nil {
		t.Errorf("a project file is gone: %v", err)
	}
}

func TestCleanActiveAndJSON(t *testing.T) {
	home := setup(t)
	t.Setenv("GIT_CEILING_DIRECTORIES", home)
	project := filepath.Join(home, "app")
	quietRepo(t, project)
	now := time.Now()
	if err := os.Chtimes(filepath.Join(project, ".git", "HEAD"), now, now); err != nil {
		t.Fatal(err)
	}
	cache := filepath.Join(project, "node_modules")

	var stdout, stderr bytes.Buffer
	args := []string{"clean", "--no-mise", "--root", project, "--json", "--apply"}
	if code := run(args, &stdout, &stderr); code != 0 {
		t.Fatalf("exit %d: %s", code, stderr.String())
	}
	var got struct {
		Applied     bool
		NotSelected struct {
			Active struct{ Caches int }
		} `json:"not_selected"`
	}
	if err := json.Unmarshal(stdout.Bytes(), &got); err != nil {
		t.Fatalf("%v\n%s", err, stdout.String())
	}
	if !got.Applied || got.NotSelected.Active.Caches != 1 {
		t.Errorf("result = %+v", got)
	}
	if _, err := os.Stat(cache); err != nil {
		t.Fatalf("an active cache was deleted: %v", err)
	}

	stdout.Reset()
	if code := run(append(args, "--include-active"), &stdout, &stderr); code != 0 {
		t.Fatalf("exit %d: %s", code, stderr.String())
	}
	if _, err := os.Stat(cache); !os.IsNotExist(err) {
		t.Errorf("--include-active kept the cache: %v", err)
	}
}

func TestCleanExitsNonZeroOnFailure(t *testing.T) {
	home := setup(t)
	t.Setenv("GIT_CEILING_DIRECTORIES", home)
	project := filepath.Join(home, "app")
	quietRepo(t, project)
	mkfile(t, filepath.Join(project, ".gitignore"), "node_modules/\n")
	mkfile(t, filepath.Join(project, "locked", "node_modules", "y.js"), "y")
	locked := filepath.Join(project, "locked")
	if err := os.Chmod(locked, 0o555); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Chmod(locked, 0o755) })

	var stdout, stderr bytes.Buffer
	code := run([]string{"clean", "--no-mise", "--root", project, "--apply"}, &stdout, &stderr)
	if code != 1 || !strings.Contains(stdout.String(), "Failed: 1") {
		t.Errorf("exit %d:\n%s%s", code, stdout.String(), stderr.String())
	}
	if _, err := os.Stat(filepath.Join(project, "node_modules")); !os.IsNotExist(err) {
		t.Errorf("clean stopped at the failure: %v", err)
	}
}

func TestCleanKeepsBusyRootAndReportsSources(t *testing.T) {
	home := setup(t)
	t.Setenv("GIT_CEILING_DIRECTORIES", home)
	project := filepath.Join(home, "app")
	quietRepo(t, project)
	fakeTool(t, "paseo", fmt.Sprintf(`echo '[{"cwd":%q,"status":"running"}]'`, project))

	var stdout, stderr bytes.Buffer
	args := []string{"clean", "--no-mise", "--root", project, "--apply", "--include-active"}
	if code := run(args, &stdout, &stderr); code != 0 {
		t.Fatalf("exit %d: %s", code, stderr.String())
	}
	out := stdout.String()
	for _, want := range []string{"an agent is working there", "1 cache", "Activity source skipped: claude", "Freed: 0 B"} {
		if !strings.Contains(out, want) {
			t.Errorf("output lacks %q:\n%s", want, out)
		}
	}
	if _, err := os.Stat(filepath.Join(project, "node_modules")); err != nil {
		t.Errorf("a cache in a busy root was deleted: %v", err)
	}

	stdout.Reset()
	if code := run([]string{"scan", "--no-mise", "--root", project, "--json"}, &stdout, &stderr); code != 0 {
		t.Fatalf("exit %d: %s", code, stderr.String())
	}
	var rep struct {
		Caches []struct {
			Busy           bool
			Quiet          bool
			ActivitySource string `json:"activity_source"`
		} `json:"caches"`
		Sources []struct{ Name, Status string } `json:"activity_sources"`
	}
	if err := json.Unmarshal(stdout.Bytes(), &rep); err != nil {
		t.Fatal(err)
	}
	if len(rep.Caches) != 1 || !rep.Caches[0].Busy || rep.Caches[0].Quiet || rep.Caches[0].ActivitySource != "paseo" {
		t.Errorf("caches = %+v", rep.Caches)
	}
	if len(rep.Sources) != 2 {
		t.Errorf("sources = %+v", rep.Sources)
	}
}

func TestScanTableShowsActivitySource(t *testing.T) {
	home := setup(t)
	t.Setenv("GIT_CEILING_DIRECTORIES", home)
	project := filepath.Join(home, "app")
	quietRepo(t, project)
	var stdout, stderr bytes.Buffer
	if code := run([]string{"scan", "--no-mise", "--root", project}, &stdout, &stderr); code != 0 {
		t.Fatalf("exit %d: %s", code, stderr.String())
	}
	for _, want := range []string{"SOURCE", " git ", "Activity source skipped: paseo (paseo is not on PATH)"} {
		if !strings.Contains(stdout.String(), want) {
			t.Errorf("output lacks %q:\n%s", want, stdout.String())
		}
	}
}

func fakeTool(t *testing.T, name, script string) {
	t.Helper()
	dir := strings.Split(os.Getenv("PATH"), string(os.PathListSeparator))[0]
	body := "#!/bin/sh\n" + script + "\n"
	if err := os.WriteFile(filepath.Join(dir, name), []byte(body), 0o755); err != nil {
		t.Fatal(err)
	}
}

// fakeGo adds a fake go and returns its build cache, which holds one blob.
// The fake removes the blob when asked to clean the cache.
func fakeGo(t *testing.T, home string) string {
	t.Helper()
	cache := filepath.Join(home, "gocache")
	mkfile(t, filepath.Join(cache, "blob"), strings.Repeat("x", 100_000))
	fakeTool(t, "go", fmt.Sprintf(`case "$*" in
"env GOCACHE") echo %q ;;
"clean -cache") /bin/rm %q/blob ;;
*) exit 9 ;;
esac`, cache, cache))
	return cache
}

func TestScanGlobal(t *testing.T) {
	home := setup(t)
	fakeGo(t, home)
	var stdout, stderr bytes.Buffer
	if code := run([]string{"scan", "--no-mise", "--root", home, "--global"}, &stdout, &stderr); code != 0 {
		t.Fatalf("exit %d: %s", code, stderr.String())
	}
	out := stdout.String()
	for _, want := range []string{
		"Global caches:", "go-build", "~/gocache", "clear", "go clean -cache",
		"Skipped uv: tool not found", "Global total:", "deleted nothing",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("output lacks %q:\n%s", want, out)
		}
	}

	stdout.Reset()
	if code := run([]string{"scan", "--no-mise", "--root", home, "--global", "--json"}, &stdout, &stderr); code != 0 {
		t.Fatalf("exit %d: %s", code, stderr.String())
	}
	var got struct {
		Global []struct {
			ID, Path, Kind, Command, Skipped string
			Bytes                            int64
		} `json:"global"`
	}
	if err := json.Unmarshal(stdout.Bytes(), &got); err != nil {
		t.Fatalf("%v\n%s", err, stdout.String())
	}
	found := false
	for _, e := range got.Global {
		if e.ID == "go-build" {
			found = e.Path == filepath.Join(home, "gocache") && e.Bytes >= 100_000 && e.Kind == "clear" && e.Skipped == ""
		}
	}
	if !found {
		t.Errorf("global = %+v", got.Global)
	}
}

func TestCleanGlobal(t *testing.T) {
	home := setup(t)
	cache := fakeGo(t, home)
	blob := filepath.Join(cache, "blob")

	var stdout, stderr bytes.Buffer
	if code := run([]string{"clean", "--global"}, &stdout, &stderr); code != 0 {
		t.Fatalf("exit %d: %s", code, stderr.String())
	}
	if out := stdout.String(); !strings.Contains(out, "go clean -cache") || !strings.Contains(out, "--include-clear") ||
		!strings.Contains(out, "Dry run") {
		t.Errorf("dry run:\n%s", out)
	}

	stdout.Reset()
	if code := run([]string{"clean", "--global", "--apply"}, &stdout, &stderr); code != 0 {
		t.Fatalf("exit %d: %s", code, stderr.String())
	}
	if _, err := os.Stat(blob); err != nil {
		t.Fatalf("a clear rule ran without --include-clear: %v", err)
	}

	stdout.Reset()
	if code := run([]string{"clean", "--global", "--apply", "--include-clear"}, &stdout, &stderr); code != 0 {
		t.Fatalf("exit %d: %s", code, stderr.String())
	}
	if _, err := os.Stat(blob); !os.IsNotExist(err) {
		t.Errorf("the blob is still there: %v", err)
	}
	if out := stdout.String(); !strings.Contains(out, "Freed: ") || strings.Contains(out, "Freed: 0 B") {
		t.Errorf("output:\n%s", out)
	}
}

func TestCleanGlobalExitsNonZeroOnFailure(t *testing.T) {
	setup(t)
	fakeTool(t, "uv", `[ "$*" = "cache dir" ] && { echo /no/such/cache; exit 0; }; echo "cannot prune" >&2; exit 4`)
	var stdout, stderr bytes.Buffer
	code := run([]string{"clean", "--global", "--apply"}, &stdout, &stderr)
	if code != 1 || !strings.Contains(stdout.String(), "cannot prune") || !strings.Contains(stdout.String(), "Failed: 1") {
		t.Errorf("exit %d:\n%s", code, stdout.String())
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
		{[]string{"nope"}, 2, ""},
		{[]string{"clean", "--help"}, 0, "--include-active"},
		{[]string{"clean", "--no-mise"}, 2, ""},
		{[]string{"clean", "--min-size", "lots"}, 2, ""},
		{[]string{"clean", "--global", "--include-active"}, 2, ""},
		{[]string{"clean", "--global", "--min-size", "1G"}, 2, ""},
		{[]string{"clean", "--global", "--root", "x"}, 2, ""},
		{[]string{"clean", "--include-clear"}, 2, ""},
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
