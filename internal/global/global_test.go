package global

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/mattt/plonge/internal/rules"
)

// fakeTools replaces PATH with a directory of fake commands,
// so that no test can reach a real tool, and returns the directory.
func fakeTools(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	t.Setenv("PATH", dir)
	return dir
}

// addTool writes a fake command that runs a sh script.
func addTool(t *testing.T, dir, name, script string) {
	t.Helper()
	body := "#!/bin/sh\n" + script + "\n"
	if err := os.WriteFile(filepath.Join(dir, name), []byte(body), 0o755); err != nil {
		t.Fatal(err)
	}
}

func cacheDir(t *testing.T, size int) string {
	t.Helper()
	dir := filepath.Join(t.TempDir(), "cache")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "blob"), make([]byte, size), 0o644); err != nil {
		t.Fatal(err)
	}
	return dir
}

// fakeUV prints dir as its cache and removes the blob on prune.
func fakeUV(dir, log string) string {
	return fmt.Sprintf(`case "$*" in
"cache dir") echo %q ;;
"cache prune") echo "$*" >> %q; /bin/rm %q/blob ;;
*) exit 9 ;;
esac`, dir, log, dir)
}

var uvRule = rules.Global{ID: "uv", Tool: "uv", Path: "uv cache dir", Clean: "uv cache prune", Kind: rules.KindPrune}

func runner(t *testing.T) Runner {
	return Runner{Home: t.TempDir(), GOOS: "testos"}
}

func TestListAndCleanFromPath(t *testing.T) {
	bin := fakeTools(t)
	cache := cacheDir(t, 100_000)
	log := filepath.Join(t.TempDir(), "log")
	addTool(t, bin, "uv", fakeUV(cache, log))
	ctx := context.Background()
	r := runner(t)

	entries := r.List(ctx, []rules.Global{uvRule})
	if len(entries) != 1 {
		t.Fatalf("entries = %+v", entries)
	}
	e := entries[0]
	if e.Skipped != "" || e.Path != cache || e.Bytes < 100_000 || e.Command != "uv cache prune" || e.Kind != "prune" {
		t.Fatalf("entry = %+v", e)
	}

	dry := r.Clean(ctx, entries, Options{})
	if dry.Applied || dry.Outcomes[0].Status != WouldRun || dry.FreedBytes != 0 {
		t.Errorf("dry run = %+v", dry)
	}
	if _, err := os.Stat(log); !os.IsNotExist(err) {
		t.Errorf("the dry run ran the command: %v", err)
	}

	res := r.Clean(ctx, entries, Options{Apply: true})
	if res.Outcomes[0].Status != Ran || res.FreedBytes < 100_000 || res.Failed != 0 {
		t.Errorf("result = %+v", res)
	}
	if data, _ := os.ReadFile(log); string(data) != "cache prune\n" {
		t.Errorf("log = %q", data)
	}
}

func TestThroughMise(t *testing.T) {
	bin := fakeTools(t)
	log := filepath.Join(t.TempDir(), "log")
	cache := cacheDir(t, 10)
	toolsDir := t.TempDir()
	addTool(t, toolsDir, "uv", fakeUV(cache, log))
	addTool(t, bin, "mise", fmt.Sprintf(`case "$1" in
which) [ "$2" = uv ] ;;
exec) shift 2; echo "exec $*" >> %q; exec %q/"$@" ;;
*) exit 9 ;;
esac`, log, toolsDir))

	r := runner(t)
	entries := r.List(context.Background(), []rules.Global{uvRule})
	e := entries[0]
	if e.Skipped != "" || e.Command != "mise exec -- uv cache prune" || e.Path != cache {
		t.Fatalf("entry = %+v", e)
	}
	r.Clean(context.Background(), entries, Options{Apply: true})
	data, _ := os.ReadFile(log)
	if want := "exec uv cache dir\nexec uv cache prune\ncache prune\n"; string(data) != want {
		t.Errorf("log = %q, want %q", data, want)
	}
}

func TestSkipsMiseShimOfInactiveTool(t *testing.T) {
	bin := fakeTools(t)
	shims := filepath.Join(t.TempDir(), "mise", "shims")
	if err := os.MkdirAll(shims, 0o755); err != nil {
		t.Fatal(err)
	}
	system := t.TempDir()
	t.Setenv("PATH", strings.Join([]string{shims, bin, system}, string(os.PathListSeparator)))
	addTool(t, shims, "uv", "exit 7")
	addTool(t, bin, "mise", "exit 1")
	cache := cacheDir(t, 10)
	addTool(t, system, "uv", fakeUV(cache, os.DevNull))

	e := runner(t).List(context.Background(), []rules.Global{uvRule})[0]
	if e.Skipped != "" || e.Path != cache || e.Command != "uv cache prune" {
		t.Errorf("entry = %+v", e)
	}

	// With only the shim, the tool counts as missing.
	t.Setenv("PATH", shims+string(os.PathListSeparator)+bin)
	e = runner(t).List(context.Background(), []rules.Global{uvRule})[0]
	if e.Skipped != "tool not found" {
		t.Errorf("entry = %+v", e)
	}
}

func TestSkipsMissingToolAndOtherOS(t *testing.T) {
	fakeTools(t)
	r := runner(t)
	other := uvRule
	other.ID, other.OS = "other", "otheros"
	here := uvRule
	here.ID, here.OS = "here", "testos"
	entries := r.List(context.Background(), []rules.Global{uvRule, other, here})
	var ids []string
	for _, e := range entries {
		ids = append(ids, e.ID)
		if e.Skipped != "tool not found" {
			t.Errorf("entry = %+v", e)
		}
	}
	if !slices.Equal(ids, []string{"uv", "here"}) {
		t.Errorf("ids = %v", ids)
	}
	res := r.Clean(context.Background(), entries, Options{Apply: true})
	if res.Outcomes[0].Status != Skipped || res.Failed != 0 {
		t.Errorf("result = %+v", res)
	}
}

func TestClearNeedsFlag(t *testing.T) {
	bin := fakeTools(t)
	cache := cacheDir(t, 50_000)
	log := filepath.Join(t.TempDir(), "log")
	addTool(t, bin, "go", fmt.Sprintf(`case "$*" in
"env GOCACHE") echo %q ;;
"clean -cache") echo ran >> %q; /bin/rm %q/blob ;;
*) exit 9 ;;
esac`, cache, log, cache))
	rule := rules.Global{ID: "go-build", Tool: "go", Path: "go env GOCACHE", Clean: "go clean -cache", Kind: rules.KindClear}
	r := runner(t)
	ctx := context.Background()
	entries := r.List(ctx, []rules.Global{rule})

	res := r.Clean(ctx, entries, Options{Apply: true})
	if o := res.Outcomes[0]; o.Status != Skipped || !strings.Contains(o.Reason, "--include-clear") {
		t.Errorf("outcome = %+v", o)
	}
	if _, err := os.Stat(log); !os.IsNotExist(err) {
		t.Errorf("the clear rule ran: %v", err)
	}

	dry := r.Clean(ctx, entries, Options{IncludeClear: true})
	if dry.Outcomes[0].Status != WouldRun {
		t.Errorf("dry run = %+v", dry.Outcomes[0])
	}

	res = r.Clean(ctx, entries, Options{Apply: true, IncludeClear: true})
	if res.Outcomes[0].Status != Ran || res.FreedBytes < 50_000 {
		t.Errorf("result = %+v", res)
	}
}

func TestFailuresAndFixedPath(t *testing.T) {
	bin := fakeTools(t)
	r := runner(t)
	ctx := context.Background()
	fixed := filepath.Join(r.Home, "data", "installs")
	if err := os.MkdirAll(fixed, 0o755); err != nil {
		t.Fatal(err)
	}
	addTool(t, bin, "ok", `echo "a warning"; echo /no/such/cache`)
	addTool(t, bin, "relative", `echo not-absolute`)
	addTool(t, bin, "broken", `echo "no cache for you" >&2; exit 3`)
	addTool(t, bin, "failing", `echo "cannot prune" >&2; exit 4`)
	entries := r.List(ctx, []rules.Global{
		{ID: "multi", Tool: "ok", Path: "ok path", Clean: "ok clean", Kind: rules.KindPrune},
		{ID: "relative", Tool: "relative", Path: "relative path", Clean: "relative clean", Kind: rules.KindPrune},
		{ID: "broken", Tool: "broken", Path: "broken path", Clean: "broken clean", Kind: rules.KindPrune},
		{ID: "fixed", Tool: "failing", Path: "~/data/installs", Clean: "failing clean", Kind: rules.KindPrune},
	})
	if e := entries[0]; e.Skipped != "" || e.Path != "/no/such/cache" || e.Bytes != 0 {
		t.Errorf("last line of output: %+v", e)
	}
	if e := entries[1]; !strings.Contains(e.Skipped, "not an absolute path") {
		t.Errorf("relative: %+v", e)
	}
	if e := entries[2]; !strings.Contains(e.Skipped, "no cache for you") || e.Command != "" {
		t.Errorf("broken: %+v", e)
	}
	if e := entries[3]; e.Path != fixed {
		t.Errorf("fixed: %+v", e)
	}
	res := r.Clean(ctx, entries[2:], Options{Apply: true})
	if res.Failed != 1 || res.Outcomes[0].Status != Skipped || res.Outcomes[1].Status != Failed ||
		!strings.Contains(res.Outcomes[1].Reason, "cannot prune") {
		t.Errorf("result = %+v", res)
	}
}

// TestDefaultRulesRunOnlyFakes checks the default rules with only fake tools on PATH.
func TestDefaultRulesRunOnlyFakes(t *testing.T) {
	bin := fakeTools(t)
	cache := cacheDir(t, 10)
	addTool(t, bin, "pip", fmt.Sprintf(`[ "$*" = "cache dir" ] && echo %q`, cache))
	r := runner(t)
	got := map[string]Entry{}
	for _, e := range r.List(context.Background(), rules.Default().Global) {
		got[e.ID] = e
	}
	if e := got["pip"]; e.Skipped != "" || e.Path != cache || e.Command != "pip cache purge" || e.Kind != "clear" {
		t.Errorf("pip = %+v", e)
	}
	if e := got["uv"]; e.Skipped != "tool not found" {
		t.Errorf("uv = %+v", e)
	}
}
