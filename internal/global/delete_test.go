package global

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/mattt/plonge/internal/rules"
)

// realTempDir returns a temporary directory without symlinks in its path,
// as the deletion guard requires.
func realTempDir(t *testing.T) string {
	t.Helper()
	dir, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	return dir
}

// withGit puts Git among the fake tools and keeps the user's Git configuration out.
func withGit(t *testing.T, bin string) {
	t.Helper()
	git, err := exec.LookPath("git")
	if err != nil {
		t.Skip("git not found")
	}
	t.Setenv("PATH", bin)
	if err := os.Symlink(git, filepath.Join(bin, "git")); err != nil {
		t.Fatal(err)
	}
	t.Setenv("GIT_CONFIG_GLOBAL", os.DevNull)
	t.Setenv("GIT_CONFIG_NOSYSTEM", "1")
}

// deleteSetup makes fake tools with Git, a home directory, and a cache under tmp.
// The fake tool records every call in the returned log.
func deleteSetup(t *testing.T) (r Runner, tmp, cache, log string) {
	t.Helper()
	bin := t.TempDir()
	withGit(t, bin)
	tmp = realTempDir(t)
	log = filepath.Join(tmp, "log")
	addTool(t, bin, "mytool", "echo \"$*\" >> "+log)
	cache = filepath.Join(tmp, "cache")
	if err := os.Mkdir(cache, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(cache, "blob"), make([]byte, 100_000), 0o644); err != nil {
		t.Fatal(err)
	}
	home := filepath.Join(tmp, "home")
	if err := os.Mkdir(home, 0o755); err != nil {
		t.Fatal(err)
	}
	return Runner{Home: home, GOOS: "testos"}, tmp, cache, log
}

func deleteRule(path string) rules.Global {
	return rules.Global{ID: "mycache", Tool: "mytool", Path: path, Kind: rules.KindClear, DeleteContents: true}
}

func TestDeleteContents(t *testing.T) {
	r, _, cache, log := deleteSetup(t)
	addFile(t, filepath.Join(cache, "locked", "f"))
	for _, p := range []string{"locked/f", "locked"} {
		if err := os.Chmod(filepath.Join(cache, p), 0o555); err != nil {
			t.Fatal(err)
		}
	}
	ctx := context.Background()
	entries := r.List(ctx, []rules.Global{deleteRule(cache)})
	e := entries[0]
	want := "delete contents of " + cache + " (built in)"
	if e.Skipped != "" || e.Command != want || e.Kind != rules.KindClear || e.Bytes < 100_000 {
		t.Fatalf("entry = %+v", e)
	}

	if res := r.Clean(ctx, entries, Options{Apply: true}); res.Outcomes[0].Status != Skipped {
		t.Errorf("without --include-clear: %+v", res.Outcomes[0])
	}
	if res := r.Clean(ctx, entries, Options{IncludeClear: true}); res.Outcomes[0].Status != WouldRun {
		t.Errorf("dry run: %+v", res.Outcomes[0])
	}
	if ents, _ := os.ReadDir(cache); len(ents) != 2 {
		t.Fatalf("a dry run changed the cache: %v", ents)
	}

	res := r.Clean(ctx, entries, Options{Apply: true, IncludeClear: true})
	o := res.Outcomes[0]
	if o.Status != Ran || o.FreedBytes < 100_000 || res.Failed != 0 {
		t.Errorf("outcome = %+v", o)
	}
	ents, err := os.ReadDir(cache)
	if err != nil || len(ents) != 0 {
		t.Errorf("cache entries = %v, err = %v", ents, err)
	}
	if ran(t, log) {
		t.Error("the tool ran")
	}
}

func TestDeleteContentsUnderHome(t *testing.T) {
	r, _, _, _ := deleteSetup(t)
	addFile(t, filepath.Join(r.Home, ".mycache", "blob"))
	entries := r.List(context.Background(), []rules.Global{deleteRule("~/.mycache")})
	if e := entries[0]; e.Skipped != "" || e.Command != "delete contents of ~/.mycache (built in)" {
		t.Fatalf("entry = %+v", e)
	}
}

func TestDeleteContentsRefusals(t *testing.T) {
	r, tmp, cache, _ := deleteSetup(t)

	link := filepath.Join(tmp, "link")
	if err := os.Symlink(cache, link); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(tmp, filepath.Join(tmp, "dirlink")); err != nil {
		t.Fatal(err)
	}
	file := filepath.Join(tmp, "file")
	addFile(t, file)
	repo := filepath.Join(tmp, "repo")
	addFile(t, filepath.Join(repo, "cache", "blob"))
	if out, err := exec.Command("git", "init", "-q", repo).CombinedOutput(); err != nil {
		t.Fatalf("git init: %v\n%s", err, out)
	}

	ctx := context.Background()
	cases := map[string]string{
		"relative/cache":                       "path is not absolute",
		"/":                                    "path is the root directory",
		r.Home:                                 "path is the home directory",
		r.Home + "/":                           "path is the home directory",
		tmp:                                    "path contains the home directory",
		filepath.Join(tmp, "missing"):          "path does not exist",
		file:                                   "path is not a directory",
		link:                                   "path is a symlink",
		filepath.Join(tmp, "dirlink", "cache"): "path resolves through a symlink to " + cache,
		filepath.Join(repo, "cache"):           "path is in a Git repository",
		filepath.Join(repo, ".git"):            "path is in a Git repository",
	}
	for path, want := range cases {
		if got := r.refuseDelete(ctx, path); got != want {
			t.Errorf("refuseDelete(%s) = %q, want %q", path, got, want)
		}
	}

	// The report shows the reason, and nothing is deleted.
	entries := r.List(ctx, []rules.Global{deleteRule(filepath.Join(repo, "cache"))})
	if e := entries[0]; e.Skipped != "path is in a Git repository" || e.Command != "" || e.Bytes != 0 {
		t.Errorf("entry = %+v", e)
	}
	res := r.Clean(ctx, entries, Options{Apply: true, IncludeClear: true})
	if o := res.Outcomes[0]; o.Status != Skipped || o.Reason != "path is in a Git repository" {
		t.Errorf("outcome = %+v", o)
	}
	if _, err := os.Stat(filepath.Join(repo, "cache", "blob")); err != nil {
		t.Error("deleted a file in a Git repository")
	}
}

func TestDeleteContentsRefusesWithoutGit(t *testing.T) {
	r, _, cache, _ := deleteSetup(t)
	if err := os.Remove(filepath.Join(filepath.SplitList(os.Getenv("PATH"))[0], "git")); err != nil {
		t.Fatal(err)
	}
	if got := r.refuseDelete(context.Background(), cache); !strings.HasPrefix(got, "cannot check Git: ") {
		t.Errorf("reason = %q", got)
	}
}

func TestDeleteContentsChecksAgainBeforeDeleting(t *testing.T) {
	r, tmp, cache, _ := deleteSetup(t)
	ctx := context.Background()
	entries := r.List(ctx, []rules.Global{deleteRule(cache)})
	if entries[0].Skipped != "" {
		t.Fatalf("entry = %+v", entries[0])
	}
	// Between the listing and the deletion, the cache becomes a link to other data.
	other := filepath.Join(tmp, "other")
	addFile(t, filepath.Join(other, "keep"))
	if err := os.RemoveAll(cache); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(other, cache); err != nil {
		t.Fatal(err)
	}
	res := r.Clean(ctx, entries, Options{Apply: true, IncludeClear: true})
	if o := res.Outcomes[0]; o.Status != Skipped || o.Reason != "path is a symlink" || o.FreedBytes != 0 {
		t.Errorf("outcome = %+v", o)
	}
	if _, err := os.Stat(filepath.Join(other, "keep")); err != nil {
		t.Error("deleted through a symlink")
	}
}

// skipIfCaseSensitive skips the test unless dir is on a case-insensitive volume.
func skipIfCaseSensitive(t *testing.T, dir string) {
	t.Helper()
	addFile(t, filepath.Join(dir, "probe"))
	if _, err := os.Stat(filepath.Join(dir, "PROBE")); err != nil {
		t.Skip("the test filesystem is case-sensitive")
	}
}

func TestDeleteContentsRefusesCaseAliasesOfHome(t *testing.T) {
	r, tmp, _, _ := deleteSetup(t)
	skipIfCaseSensitive(t, tmp)
	r.Home = filepath.Join(tmp, "outer", "home")
	addFile(t, filepath.Join(r.Home, "keep"))
	ctx := context.Background()
	cases := map[string]string{
		filepath.Join(tmp, "outer", "HOME"): "path is the home directory",
		filepath.Join(tmp, "OUTER", "Home"): "path is the home directory",
		filepath.Join(tmp, "OUTER"):         "path contains the home directory",
	}
	for path, want := range cases {
		if got := r.refuseDelete(ctx, path); got != want {
			t.Errorf("refuseDelete(%s) = %q, want %q", path, got, want)
		}
	}
}

func TestDeleteContentsRefusesWithoutHome(t *testing.T) {
	r, tmp, cache, _ := deleteSetup(t)
	ctx := context.Background()
	for _, home := range []string{"", "relative/home", filepath.Join(tmp, "missing")} {
		r.Home = home
		if got := r.refuseDelete(ctx, cache); !strings.HasPrefix(got, "cannot check the home directory: ") {
			t.Errorf("home %q: reason = %q", home, got)
		}
	}
}

func TestDeleteContentsIgnoresInheritedGitCeiling(t *testing.T) {
	r, tmp, _, _ := deleteSetup(t)
	repo := filepath.Join(tmp, "repo")
	addFile(t, filepath.Join(repo, "cache", "tracked"))
	for _, args := range [][]string{{"init", "-q"}, {"add", "."}, {"-c", "user.name=T", "-c", "user.email=t@example.com", "commit", "-q", "-m", "init"}} {
		cmd := exec.Command("git", args...)
		cmd.Dir = repo
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v\n%s", args, err, out)
		}
	}
	t.Setenv("GIT_CEILING_DIRECTORIES", repo)
	if got := r.refuseDelete(context.Background(), filepath.Join(repo, "cache")); got != "path is in a Git repository" {
		t.Errorf("reason = %q", got)
	}
}

// A mise-tools override that deletes contents must keep to its own path,
// not the installs directory that mise reports.
func TestDeleteContentsMiseToolsKeepsItsPath(t *testing.T) {
	r, tmp, cache, _ := deleteSetup(t)
	bin := filepath.SplitList(os.Getenv("PATH"))[0]
	addTool(t, bin, "mise", "exit 0")
	installs := filepath.Join(tmp, "installs")
	addFile(t, filepath.Join(installs, "go", "1.26.0", "bin", "go"))
	t.Setenv("MISE_INSTALLS_DIR", installs)
	t.Setenv("MISE_DATA_DIR", "")
	rule := rules.Global{ID: rules.MiseToolsRuleID, Tool: "mise", Path: cache, Kind: rules.KindClear, DeleteContents: true}
	ctx := context.Background()

	entries := r.List(ctx, []rules.Global{rule})
	if e := entries[0]; e.Skipped != "" || e.Path != cache {
		t.Fatalf("entry = %+v", e)
	}
	res := r.Clean(ctx, entries, Options{Apply: true, IncludeClear: true})
	if o := res.Outcomes[0]; o.Status != Ran {
		t.Fatalf("outcome = %+v", o)
	}
	if ents, _ := os.ReadDir(cache); len(ents) != 0 {
		t.Errorf("cache entries = %v", ents)
	}
	if _, err := os.Stat(filepath.Join(installs, "go", "1.26.0", "bin", "go")); err != nil {
		t.Errorf("the installs directory changed: %v", err)
	}
}
