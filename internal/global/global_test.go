package global

import (
	"context"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"unicode/utf8"

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

// runner returns a Runner with a temporary home directory.
// It clears mise's directory variables,
// so that no test measures an inherited installs directory.
func runner(t *testing.T) Runner {
	t.Setenv("MISE_DATA_DIR", "")
	t.Setenv("MISE_INSTALLS_DIR", "")
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
	// uv's policy removes only part of the cache, so Bytes is not space to free.
	if !e.WholeCache {
		t.Errorf("a prune rule's cache size is not labeled: %+v", e)
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
	if entries[0].WholeCache {
		t.Errorf("a clear rule's size is labeled as a prune rule's cache: %+v", entries[0])
	}

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

func TestPathField(t *testing.T) {
	bin := fakeTools(t)
	cache := cacheDir(t, 10)
	addTool(t, bin, "ok", fmt.Sprintf(`printf '{\n  "version": 1,\n  "dir": %q\n}\n'`, cache))
	addTool(t, bin, "text", fmt.Sprintf(`echo %q`, cache))
	addTool(t, bin, "missing", `echo '{"other": "/x"}'`)
	addTool(t, bin, "number", `echo '{"dir": 3}'`)
	addTool(t, bin, "relative", `echo '{"dir": "x"}'`)
	rule := func(tool string) rules.Global {
		return rules.Global{ID: tool, Tool: tool, Path: tool + " info", PathField: "dir", Clean: tool + " clean", Kind: rules.KindPrune}
	}
	entries := runner(t).List(context.Background(), []rules.Global{
		rule("ok"), rule("text"), rule("missing"), rule("number"), rule("relative"),
	})
	if e := entries[0]; e.Skipped != "" || e.Path != cache || e.Bytes < 10 {
		t.Errorf("ok = %+v", e)
	}
	for i, want := range []string{"invalid JSON", `no "dir" field`, "not a string", "not an absolute path"} {
		if e := entries[i+1]; !strings.Contains(e.Skipped, want) || e.Command != "" {
			t.Errorf("%s = %+v, want %q", e.ID, e, want)
		}
	}
}

func TestPathLabel(t *testing.T) {
	bin := fakeTools(t)
	cache := cacheDir(t, 10)
	addTool(t, bin, "labeled", fmt.Sprintf(`echo "Welcome"; echo "info : store: %s/"; echo "done"`, cache))
	addTool(t, bin, "plain", fmt.Sprintf(`echo "store: %s"`, cache))
	addTool(t, bin, "nolabel", fmt.Sprintf(`echo %q`, cache))
	addTool(t, bin, "relative", `echo "store: cache"`)
	addTool(t, bin, "twice", fmt.Sprintf(`echo "store: %s"; echo "store: %s/"; echo "store: /other"`, cache, cache))
	addTool(t, bin, "same", fmt.Sprintf(`echo "store: %s"; echo "info : store: %s/"`, cache, cache))
	addTool(t, bin, "emptyfirst", fmt.Sprintf(`echo "store: "; echo "store: %s"`, cache))
	addTool(t, bin, "emptylast", fmt.Sprintf(`echo "store: %s"; echo "info : store:  "`, cache))
	r := runner(t)
	entries := r.List(context.Background(), []rules.Global{
		{ID: "labeled", Tool: "labeled", Path: "labeled list", PathLabel: "store", Clean: "labeled c", Kind: rules.KindClear},
		{ID: "plain", Tool: "plain", Path: "plain list", PathLabel: "store", Clean: "plain c", Kind: rules.KindClear},
		{ID: "nolabel", Tool: "nolabel", Path: "nolabel list", PathLabel: "store", Clean: "nolabel c", Kind: rules.KindClear},
		{ID: "relative", Tool: "relative", Path: "relative list", PathLabel: "store", Clean: "relative c", Kind: rules.KindClear},
		{ID: "twice", Tool: "twice", Path: "twice list", PathLabel: "store", Clean: "twice c", Kind: rules.KindClear},
		{ID: "same", Tool: "same", Path: "same list", PathLabel: "store", Clean: "same c", Kind: rules.KindClear},
		{ID: "emptyfirst", Tool: "emptyfirst", Path: "emptyfirst list", PathLabel: "store", Clean: "emptyfirst c", Kind: rules.KindClear},
		{ID: "emptylast", Tool: "emptylast", Path: "emptylast list", PathLabel: "store", Clean: "emptylast c", Kind: rules.KindClear},
	})
	for _, e := range entries[:2] {
		if e.Skipped != "" || e.Path != cache || e.Bytes < 10 {
			t.Errorf("%s = %+v", e.ID, e)
		}
	}
	if e := entries[2]; !strings.Contains(e.Skipped, `no line with "store: "`) || e.Command != "" {
		t.Errorf("nolabel = %+v", e)
	}
	if e := entries[3]; !strings.Contains(e.Skipped, "not an absolute path") {
		t.Errorf("relative = %+v", e)
	}
	if e := entries[4]; !strings.Contains(e.Skipped, "more than one path") || e.Command != "" {
		t.Errorf("twice = %+v", e)
	}
	if e := entries[5]; e.Skipped != "" || e.Path != cache {
		t.Errorf("same = %+v", e)
	}
	// A labeled line with no path is rejected wherever it appears.
	for _, e := range entries[6:] {
		if !strings.Contains(e.Skipped, `no path after "store: "`) || e.Command != "" {
			t.Errorf("%s = %+v", e.ID, e)
		}
	}
}

// TestDefaultRulesRunOnlyFakes checks the default rules with only fake tools on PATH.
func TestDefaultRulesRunOnlyFakes(t *testing.T) {
	bin := fakeTools(t)
	cache := cacheDir(t, 10)
	addTool(t, bin, "pip", fmt.Sprintf(`[ "$*" = "cache dir" ] && echo %q`, cache))
	packages, http := cacheDir(t, 10), cacheDir(t, 10)
	addTool(t, bin, "dotnet", fmt.Sprintf(`case "$*" in
"nuget locals global-packages --list") echo "info : global-packages: %s/" ;;
"nuget locals http-cache --list") echo "info : http-cache: %s" ;;
*) exit 9 ;;
esac`, packages, http))
	r := runner(t)
	got := map[string]Entry{}
	for _, e := range r.List(context.Background(), rules.Default().Global) {
		got[e.ID] = e
	}
	if e := got["pip"]; e.Skipped != "" || e.Path != cache || e.Command != "pip cache purge" || e.Kind != "clear" {
		t.Errorf("pip = %+v", e)
	}
	if e := got["nuget-packages"]; e.Skipped != "" || e.Path != packages ||
		e.Command != "dotnet nuget locals global-packages --clear" || e.Kind != "clear" {
		t.Errorf("nuget-packages = %+v", e)
	}
	if e := got["nuget-http-cache"]; e.Skipped != "" || e.Path != http ||
		e.Command != "dotnet nuget locals http-cache --clear" || e.Kind != "clear" {
		t.Errorf("nuget-http-cache = %+v", e)
	}
	if e := got["uv"]; e.Skipped != "tool not found" {
		t.Errorf("uv = %+v", e)
	}
	if e := got["zig-global-cache"]; e.Skipped != "tool not found" || e.Kind != "clear" {
		t.Errorf("zig-global-cache = %+v", e)
	}
	if e := got["rubygems-cache"]; e.Skipped != "tool not found" || e.Kind != "clear" {
		t.Errorf("rubygems-cache = %+v", e)
	}
}

// TestDefaultZigRule resolves the Zig global cache through a fake zig env
// and deletes its contents, keeping the directory.
func TestDefaultZigRule(t *testing.T) {
	r, tmp, _, _ := deleteSetup(t)
	bin := filepath.SplitList(os.Getenv("PATH"))[0]
	cache := filepath.Join(r.Home, ".cache", "zig")
	addFile(t, filepath.Join(cache, "p", "pkg", "build.zig"))
	env := filepath.Join(tmp, "env.zon")
	if err := os.WriteFile(env, []byte(zigEnv(".cache/zig")), 0o644); err != nil {
		t.Fatal(err)
	}
	log := filepath.Join(tmp, "zig.log")
	addTool(t, bin, "zig", fmt.Sprintf(`echo "$*" >> %q; [ "$*" = env ] && /bin/cat %q`, log, env))
	var rule rules.Global
	for _, g := range rules.Default().Global {
		if g.ID == "zig-global-cache" {
			rule = g
		}
	}
	ctx := context.Background()
	entries := r.List(ctx, []rules.Global{rule})
	e := entries[0]
	if e.Skipped != "" || e.Path != cache || e.Command != "delete contents of ~/.cache/zig (built in)" {
		t.Fatalf("entry = %+v", e)
	}
	res := r.Clean(ctx, entries, Options{Apply: true, IncludeClear: true})
	if o := res.Outcomes[0]; o.Status != Ran {
		t.Errorf("outcome = %+v", o)
	}
	if ents, err := os.ReadDir(cache); err != nil || len(ents) != 0 {
		t.Errorf("cache entries = %v, err = %v", ents, err)
	}
	data, err := os.ReadFile(log)
	if err != nil || strings.TrimSpace(string(data)) != "env" {
		t.Errorf("zig calls = %q, err = %v", data, err)
	}
}

// rubyGemsSetup makes a fake gem whose env gemdir prints a gem directory
// under a temporary directory, and returns the default rubygems-cache rule.
// The fake records its calls in log.
func rubyGemsSetup(t *testing.T) (r Runner, gemdir, log string, rule rules.Global) {
	t.Helper()
	r, tmp, _, _ := deleteSetup(t)
	bin := filepath.SplitList(os.Getenv("PATH"))[0]
	gemdir = filepath.Join(tmp, "gems", "4.0.0")
	log = filepath.Join(tmp, "gem.log")
	addTool(t, bin, "gem", fmt.Sprintf(`echo "$*" >> %q; [ "$*" = "env gemdir" ] && echo %q`, log, gemdir))
	return r, gemdir, log, defaultRule(t, "rubygems-cache")
}

// TestDefaultRubyGemsRule finds RubyGems' cache below the gem directory
// that a fake gem env gemdir prints, and deletes only its contents.
func TestDefaultRubyGemsRule(t *testing.T) {
	r, gemdir, log, rule := rubyGemsSetup(t)
	cache := filepath.Join(gemdir, "cache")
	installed := filepath.Join(gemdir, "gems", "rack-3.1.0", "lib", "rack.rb")
	addFile(t, filepath.Join(cache, "rack-3.1.0.gem"))
	addFile(t, installed)
	ctx := context.Background()
	entries := r.List(ctx, []rules.Global{rule})
	e := entries[0]
	if e.Skipped != "" || e.Path != cache || e.Kind != "clear" ||
		e.Command != "delete contents of "+cache+" except bundler (built in)" {
		t.Fatalf("entry = %+v", e)
	}
	if res := r.Clean(ctx, entries, Options{Apply: true}); res.Outcomes[0].Status == Ran {
		t.Errorf("ran without --include-clear: %+v", res.Outcomes[0])
	}
	res := r.Clean(ctx, entries, Options{Apply: true, IncludeClear: true})
	if o := res.Outcomes[0]; o.Status != Ran {
		t.Errorf("outcome = %+v", o)
	}
	if ents, err := os.ReadDir(cache); err != nil || len(ents) != 0 {
		t.Errorf("cache entries = %v, err = %v", ents, err)
	}
	if _, err := os.Stat(installed); err != nil {
		t.Errorf("installed gem: %v", err)
	}
	data, err := os.ReadFile(log)
	if err != nil || strings.TrimSpace(string(data)) != "env gemdir" {
		t.Errorf("gem calls = %q, err = %v", data, err)
	}
}

// bundlerGitClone makes a repository with one commit and clones it bare
// into dir, as Bundler clones a gem's Git source into its cache.
func bundlerGitClone(t *testing.T, dir string) {
	t.Helper()
	src := filepath.Join(t.TempDir(), "rack")
	commitRepo(t, src)
	if err := os.MkdirAll(filepath.Dir(dir), 0o755); err != nil {
		t.Fatal(err)
	}
	gitRun(t, filepath.Dir(dir), "clone", "--bare", "--no-hardlinks", "--quiet", "--", src, dir)
}

// TestRubyGemsRuleKeepsBundlerGitClones deletes the .gem files in the gem
// cache and leaves cache/bundler, where Bundler keeps a bare clone of each
// gem's Git source, which can hold commits that no remote has any longer.
func TestRubyGemsRuleKeepsBundlerGitClones(t *testing.T) {
	r, gemdir, _, rule := rubyGemsSetup(t)
	cache := filepath.Join(gemdir, "cache")
	gem := filepath.Join(cache, "rack-3.1.0.gem")
	if err := os.MkdirAll(cache, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(gem, make([]byte, 100_000), 0o644); err != nil {
		t.Fatal(err)
	}
	clone := filepath.Join(cache, "bundler", "git", "rack-0123456789abcdef0123456789abcdef01234567")
	bundlerGitClone(t, clone)
	// The kept directory is left out of the size.
	if err := os.WriteFile(filepath.Join(cache, "bundler", "big"), make([]byte, 1_000_000), 0o644); err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	entries := r.List(ctx, []rules.Global{rule})
	e := entries[0]
	if e.Skipped != "" || e.Bytes < 100_000 || e.Bytes >= 1_000_000 {
		t.Fatalf("entry = %+v", e)
	}
	res := r.Clean(ctx, entries, Options{Apply: true, IncludeClear: true})
	if o := res.Outcomes[0]; o.Status != Ran || o.FreedBytes < 100_000 {
		t.Errorf("outcome = %+v", o)
	}
	if _, err := os.Lstat(gem); !errors.Is(err, fs.ErrNotExist) {
		t.Errorf("kept the .gem file: %v", err)
	}
	gitRun(t, clone, "rev-parse", "--verify", "--quiet", "HEAD^{commit}")
}

// TestRubyGemsRuleRefusesOtherRepositories skips the rule when a Git
// repository is in the gem cache anywhere other than cache/bundler,
// even one that looks like Bundler's bare clones.
func TestRubyGemsRuleRefusesOtherRepositories(t *testing.T) {
	cases := map[string]struct {
		make func(t *testing.T, cache string)
		want string
	}{
		"repository": {
			func(t *testing.T, cache string) { commitRepo(t, filepath.Join(cache, "work")) },
			"contains a Git repository at work",
		},
		"bare clone outside cache/bundler": {
			func(t *testing.T, cache string) {
				bundlerGitClone(t, filepath.Join(cache, "git", "rack-0123456789abcdef0123456789abcdef01234567"))
			},
			"contains a Git directory at git/rack-0123456789abcdef0123456789abcdef01234567",
		},
	}
	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			r, gemdir, _, rule := rubyGemsSetup(t)
			cache := filepath.Join(gemdir, "cache")
			gem := filepath.Join(cache, "rack-3.1.0.gem")
			addFile(t, gem)
			bundlerGitClone(t, filepath.Join(cache, "bundler", "git", "rack-0123456789abcdef0123456789abcdef01234567"))
			tc.make(t, cache)
			ctx := context.Background()
			entries := r.List(ctx, []rules.Global{rule})
			if e := entries[0]; e.Skipped != tc.want || e.Command != "" {
				t.Fatalf("entry = %+v, want skipped %q", e, tc.want)
			}
			res := r.Clean(ctx, entries, Options{Apply: true, IncludeClear: true})
			if o := res.Outcomes[0]; o.Status != Skipped || o.FreedBytes != 0 {
				t.Errorf("outcome = %+v", o)
			}
			if _, err := os.Stat(gem); err != nil {
				t.Errorf("deleted the cache of a refused rule: %v", err)
			}
		})
	}
}

// TestPathSubdirMissing skips a delete_contents rule
// when the directory below the printed path does not exist.
func TestPathSubdirMissing(t *testing.T) {
	r, tmp, _, _ := deleteSetup(t)
	bin := filepath.SplitList(os.Getenv("PATH"))[0]
	gemdir := filepath.Join(tmp, "gems")
	addFile(t, filepath.Join(gemdir, "gems", "x"))
	addTool(t, bin, "gem", fmt.Sprintf(`echo %q`, gemdir))
	rule := rules.Global{ID: "g", Tool: "gem", Path: "gem env gemdir", PathSubdir: "cache", DeleteContents: true, Kind: rules.KindClear}
	e := r.List(context.Background(), []rules.Global{rule})[0]
	if e.Skipped == "" || e.Path != filepath.Join(gemdir, "cache") {
		t.Errorf("entry = %+v", e)
	}
}

// TestSwiftPMRules checks that each system gets its own SwiftPM rule,
// and that purge-cache runs from the home directory with a scratch path
// that resolves to the cache directory, so that it does not make ~/.build.
func TestSwiftPMRules(t *testing.T) {
	for _, tc := range []struct {
		goos, id, other, cache string
	}{
		{"darwin", "swiftpm-macos", "swiftpm-linux", "Library/Caches/org.swift.swiftpm"},
		{"linux", "swiftpm-linux", "swiftpm-macos", ".cache/org.swift.swiftpm"},
	} {
		t.Run(tc.goos, func(t *testing.T) {
			bin := fakeTools(t)
			r := runner(t)
			r.GOOS = tc.goos
			cache := filepath.Join(r.Home, tc.cache)
			if err := os.MkdirAll(cache, 0o755); err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(filepath.Join(cache, "blob"), make([]byte, 50_000), 0o644); err != nil {
				t.Fatal(err)
			}
			log := filepath.Join(t.TempDir(), "log")
			// The fake follows SwiftPM: --cache-path, or else the XDG cache on Linux.
			xdg := t.TempDir()
			t.Setenv("XDG_CACHE_HOME", xdg)
			addFile(t, filepath.Join(xdg, "org.swift.swiftpm", "blob"))
			addTool(t, bin, "swift", fmt.Sprintf(`echo "$PWD $*" >> %q
cache=""
while [ $# -gt 0 ]; do
  [ "$1" = --cache-path ] && cache=$2
  shift
done
[ -n "$cache" ] || cache="$XDG_CACHE_HOME/org.swift.swiftpm"
/bin/rm -f "$cache/blob"`, log))
			ctx := context.Background()
			got := map[string]Entry{}
			var picked []Entry
			for _, e := range r.List(ctx, rules.Default().Global) {
				got[e.ID] = e
				if e.ID == tc.id {
					picked = append(picked, e)
				}
			}
			want := "swift package purge-cache --cache-path " + tc.cache + " --scratch-path " + tc.cache
			e := got[tc.id]
			if e.Skipped != "" || e.Path != cache || e.Command != want || e.Kind != rules.KindClear || e.Bytes < 50_000 {
				t.Errorf("%s = %+v", tc.id, e)
			}
			if _, ok := got[tc.other]; ok {
				t.Errorf("%s listed on %s", tc.other, tc.goos)
			}
			res := r.Clean(ctx, picked, Options{Apply: true})
			if res.Outcomes[0].Status != Skipped {
				t.Errorf("without --include-clear: %+v", res)
			}
			if _, err := os.Stat(log); err == nil {
				t.Error("swift ran without --include-clear")
			}
			res = r.Clean(ctx, picked, Options{Apply: true, IncludeClear: true})
			if res.Outcomes[0].Status != Ran || res.FreedBytes < 50_000 {
				t.Errorf("result = %+v", res)
			}
			data, err := os.ReadFile(log)
			if err != nil {
				t.Fatal(err)
			}
			if line := strings.TrimSpace(string(data)); line != r.Home+" package purge-cache --cache-path "+tc.cache+" --scratch-path "+tc.cache {
				t.Errorf("swift ran as %q", line)
			}
			if _, err := os.Stat(filepath.Join(r.Home, ".build")); err == nil {
				t.Error("~/.build exists")
			}
			if _, err := os.Stat(filepath.Join(xdg, "org.swift.swiftpm", "blob")); err != nil {
				t.Errorf("the XDG cache changed: %v", err)
			}
		})
	}
}

// TestCargoRulesFollowCargoHome resolves the Cargo rules from CARGO_HOME,
// or from ~/.cargo when it is not set, and deletes only the caches' contents.
func TestCargoRulesFollowCargoHome(t *testing.T) {
	for _, tc := range []struct {
		name string
		env  func(r Runner, tmp string) (cargoHome, value string)
	}{
		{"default", func(r Runner, _ string) (string, string) { return filepath.Join(r.Home, ".cargo"), "" }},
		{"CARGO_HOME", func(_ Runner, tmp string) (string, string) {
			dir := filepath.Join(tmp, "cargo-home")
			return dir, dir
		}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			r, tmp, _, _ := deleteSetup(t)
			bin := filepath.SplitList(os.Getenv("PATH"))[0]
			log := filepath.Join(tmp, "cargo.log")
			addTool(t, bin, "cargo", fmt.Sprintf(`echo "$*" >> %q`, log))
			cargoHome, value := tc.env(r, tmp)
			t.Setenv("CARGO_HOME", value)
			// A default Cargo home elsewhere must not be touched.
			other := filepath.Join(r.Home, ".cargo", "registry", "cache", "x.crate")
			if value != "" {
				addFile(t, other)
			}
			kept := []string{
				filepath.Join(cargoHome, "git", "checkouts", "dep-1", "abc", "Cargo.toml"),
				filepath.Join(cargoHome, "git", "db", "dep-1", "HEAD"),
				filepath.Join(cargoHome, "bin", "cargo-tool"),
				filepath.Join(cargoHome, "config.toml"),
				filepath.Join(cargoHome, ".crates.toml"),
			}
			for _, p := range kept {
				addFile(t, p)
			}
			addFile(t, filepath.Join(cargoHome, "registry", "cache", "index.crates.io-1", "serde-1.0.0.crate"))
			addFile(t, filepath.Join(cargoHome, "registry", "src", "index.crates.io-1", "serde-1.0.0", "lib.rs"))
			ctx := context.Background()
			entries := r.List(ctx, []rules.Global{defaultRule(t, "cargo-registry")})
			for i, sub := range []string{"registry"} {
				e := entries[i]
				path := filepath.Join(cargoHome, sub)
				if e.Skipped != "" || e.Path != path || e.Command != r.deleteAction(path, nil) {
					t.Fatalf("%s = %+v", sub, e)
				}
			}
			res := r.Clean(ctx, entries, Options{Apply: true, IncludeClear: true})
			for _, o := range res.Outcomes {
				if o.Status != Ran {
					t.Errorf("outcome = %+v", o)
				}
			}
			for _, sub := range []string{"registry"} {
				if ents, err := os.ReadDir(filepath.Join(cargoHome, sub)); err != nil || len(ents) != 0 {
					t.Errorf("%s entries = %v, err = %v", sub, ents, err)
				}
			}
			for _, p := range append(kept, other) {
				if value == "" && p == other {
					continue
				}
				if _, err := os.Stat(p); err != nil {
					t.Errorf("%s: %v", p, err)
				}
			}
			if _, err := os.Stat(log); err == nil {
				t.Error("cargo ran")
			}
		})
	}
}

// TestGradleCachesFollowGradleUserHome resolves gradle-caches from
// GRADLE_USER_HOME, or from ~/.gradle when it is not set, deletes only the
// contents of caches, and keeps the configuration beside it.
func TestGradleCachesFollowGradleUserHome(t *testing.T) {
	for _, tc := range []struct {
		name string
		env  func(r Runner, tmp string) (userHome, value string)
	}{
		{"default", func(r Runner, _ string) (string, string) { return filepath.Join(r.Home, ".gradle"), "" }},
		{"GRADLE_USER_HOME", func(_ Runner, tmp string) (string, string) {
			dir := filepath.Join(tmp, "gradle-home")
			return dir, dir
		}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			r, tmp, _, _ := deleteSetup(t)
			bin := filepath.SplitList(os.Getenv("PATH"))[0]
			log := filepath.Join(tmp, "java.log")
			addTool(t, bin, "java", fmt.Sprintf(`echo "$*" >> %q`, log))
			userHome, value := tc.env(r, tmp)
			t.Setenv("GRADLE_USER_HOME", value)
			// The default user home must not be touched when the variable moves it.
			other := filepath.Join(r.Home, ".gradle", "caches", "modules-2", "x.jar")
			if value != "" {
				addFile(t, other)
			}
			kept := []string{
				filepath.Join(userHome, "gradle.properties"),
				filepath.Join(userHome, "init.d", "cleanup.gradle"),
				filepath.Join(userHome, "wrapper", "dists", "gradle-9.9.1-bin", "x.zip"),
				filepath.Join(userHome, "jdks", "jdk-21", "release"),
			}
			for _, p := range kept {
				addFile(t, p)
			}
			caches := filepath.Join(userHome, "caches")
			addFile(t, filepath.Join(caches, "modules-2", "files-2.1", "g", "a", "1.0", "h", "a-1.0.jar"))
			addFile(t, filepath.Join(caches, "9.9.1", "kotlin-dsl", "x"))
			ctx := context.Background()
			entries := r.List(ctx, []rules.Global{defaultRule(t, "gradle-caches")})
			if e := entries[0]; e.Skipped != "" || e.Path != caches || e.Kind != rules.KindClear ||
				e.Command != r.deleteAction(caches, nil) {
				t.Fatalf("entry = %+v", e)
			}
			if res := r.Clean(ctx, entries, Options{Apply: true}); res.Outcomes[0].Status != Skipped {
				t.Errorf("without --include-clear: %+v", res)
			}
			res := r.Clean(ctx, entries, Options{Apply: true, IncludeClear: true})
			if o := res.Outcomes[0]; o.Status != Ran {
				t.Errorf("outcome = %+v", o)
			}
			if ents, err := os.ReadDir(caches); err != nil || len(ents) != 0 {
				t.Errorf("caches entries = %v, err = %v", ents, err)
			}
			if value != "" {
				kept = append(kept, other)
			}
			for _, p := range kept {
				if _, err := os.Stat(p); err != nil {
					t.Errorf("%s: %v", p, err)
				}
			}
			if _, err := os.Stat(log); err == nil {
				t.Error("java ran")
			}
		})
	}
	t.Run("relative GRADLE_USER_HOME", func(t *testing.T) {
		r, _, _, _ := deleteSetup(t)
		bin := filepath.SplitList(os.Getenv("PATH"))[0]
		addTool(t, bin, "java", "exit 9")
		addFile(t, filepath.Join(r.Home, ".gradle", "caches", "x"))
		t.Setenv("GRADLE_USER_HOME", "gradle-home")
		e := r.List(context.Background(), []rules.Global{defaultRule(t, "gradle-caches")})[0]
		if !strings.Contains(e.Skipped, "not an absolute path") || e.Command != "" {
			t.Errorf("entry = %+v", e)
		}
	})
}

// TestMavenRepositoryRefusesInstalledArtifacts deletes the contents of
// ~/.m2/repository when it holds only downloads, keeps the settings beside it,
// and refuses a repository that holds an artifact that mvn install put there,
// when it lists the rule and again right before it deletes.
func TestMavenRepositoryRefusesInstalledArtifacts(t *testing.T) {
	setup := func(t *testing.T) (Runner, string, []string, string) {
		r, tmp, _, _ := deleteSetup(t)
		bin := filepath.SplitList(os.Getenv("PATH"))[0]
		log := filepath.Join(tmp, "java.log")
		addTool(t, bin, "java", fmt.Sprintf(`echo "$*" >> %q`, log))
		repo := filepath.Join(r.Home, ".m2", "repository")
		ga := filepath.Join(repo, "org", "example", "lib")
		addFile(t, filepath.Join(ga, "1.0", "lib-1.0.jar"))
		addFile(t, filepath.Join(ga, "1.0", "_remote.repositories"))
		addFile(t, filepath.Join(ga, "maven-metadata-central.xml"))
		addFile(t, filepath.Join(repo, ".locks", "x.lock"))
		kept := []string{
			filepath.Join(r.Home, ".m2", "settings.xml"),
			filepath.Join(r.Home, ".m2", "settings-security.xml"),
			filepath.Join(r.Home, ".m2", "toolchains.xml"),
		}
		for _, p := range kept {
			addFile(t, p)
		}
		return r, repo, kept, log
	}
	installed := filepath.Join("com", "corp", "driver", "maven-metadata-local.xml")
	ctx := context.Background()

	t.Run("downloads only", func(t *testing.T) {
		r, repo, kept, log := setup(t)
		entries := r.List(ctx, []rules.Global{defaultRule(t, "maven-repository")})
		if e := entries[0]; e.Skipped != "" || e.Path != repo || e.Kind != rules.KindClear ||
			e.Command != "delete contents of ~/.m2/repository (built in)" {
			t.Fatalf("entry = %+v", e)
		}
		res := r.Clean(ctx, entries, Options{Apply: true, IncludeClear: true})
		if o := res.Outcomes[0]; o.Status != Ran {
			t.Errorf("outcome = %+v", o)
		}
		if ents, err := os.ReadDir(repo); err != nil || len(ents) != 0 {
			t.Errorf("repository entries = %v, err = %v", ents, err)
		}
		for _, p := range kept {
			if _, err := os.Stat(p); err != nil {
				t.Errorf("%s: %v", p, err)
			}
		}
		if _, err := os.Stat(log); err == nil {
			t.Error("java ran")
		}
	})

	want := "contains com/corp/driver/maven-metadata-local.xml, which refuse_names \"maven-metadata-local.xml\" protects"
	t.Run("installed artifact", func(t *testing.T) {
		r, repo, _, _ := setup(t)
		addFile(t, filepath.Join(repo, installed))
		entries := r.List(ctx, []rules.Global{defaultRule(t, "maven-repository")})
		if e := entries[0]; e.Skipped != want || e.Command != "" {
			t.Fatalf("entry = %+v", e)
		}
		res := r.Clean(ctx, entries, Options{Apply: true, IncludeClear: true})
		if o := res.Outcomes[0]; o.Status != Skipped || o.Reason != want {
			t.Errorf("outcome = %+v", o)
		}
		if _, err := os.Stat(filepath.Join(repo, "org", "example", "lib", "1.0", "lib-1.0.jar")); err != nil {
			t.Error(err)
		}
	})

	t.Run("installed after listing", func(t *testing.T) {
		r, repo, _, _ := setup(t)
		entries := r.List(ctx, []rules.Global{defaultRule(t, "maven-repository")})
		if e := entries[0]; e.Skipped != "" {
			t.Fatalf("entry = %+v", e)
		}
		addFile(t, filepath.Join(repo, installed))
		res := r.Clean(ctx, entries, Options{Apply: true, IncludeClear: true})
		if o := res.Outcomes[0]; o.Status != Skipped || o.Reason != want {
			t.Errorf("outcome = %+v", o)
		}
		if _, err := os.Stat(filepath.Join(repo, installed)); err != nil {
			t.Error(err)
		}
	})

	t.Run("unreadable directory", func(t *testing.T) {
		if os.Geteuid() == 0 {
			t.Skip("root can read any directory")
		}
		r, repo, _, _ := setup(t)
		locked := filepath.Join(repo, "com", "corp")
		addFile(t, filepath.Join(locked, "x"))
		if err := os.Chmod(locked, 0o000); err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() { _ = os.Chmod(locked, 0o755) })
		e := r.List(ctx, []rules.Global{defaultRule(t, "maven-repository")})[0]
		if !strings.HasPrefix(e.Skipped, "cannot check for maven-metadata-local.xml: ") || e.Command != "" {
			t.Errorf("entry = %+v", e)
		}
	})
}

// TestDefaultBEAMPackageRules deletes the contents of the Hex and Rebar3
// package caches without running the tools, and keeps the folders beside them.
func TestDefaultBEAMPackageRules(t *testing.T) {
	for _, tc := range []struct {
		id, tool, cache, kept string
	}{
		{"hex-packages", "mix", ".hex/packages", ".hex/hex.config"},
		{"rebar3-hex", "rebar3", ".cache/rebar3/hex", ".cache/rebar3/bin/rebar3"},
	} {
		t.Run(tc.id, func(t *testing.T) {
			r, _, _, log := deleteSetup(t)
			bin := filepath.SplitList(os.Getenv("PATH"))[0]
			addTool(t, bin, tc.tool, fmt.Sprintf(`echo "$*" >> %q`, log))
			cache := filepath.Join(r.Home, tc.cache)
			addFile(t, filepath.Join(cache, "hexpm", "pkg-1.0.0.tar"))
			kept := filepath.Join(r.Home, tc.kept)
			addFile(t, kept)
			var rule rules.Global
			for _, g := range rules.Default().Global {
				if g.ID == tc.id {
					rule = g
				}
			}
			ctx := context.Background()
			entries := r.List(ctx, []rules.Global{rule})
			e := entries[0]
			if e.Skipped != "" || e.Path != cache || e.Kind != rules.KindClear ||
				e.Command != "delete contents of ~/"+tc.cache+" (built in)" {
				t.Fatalf("entry = %+v", e)
			}
			if res := r.Clean(ctx, entries, Options{Apply: true}); res.Outcomes[0].Status != Skipped {
				t.Errorf("without --include-clear: %+v", res)
			}
			res := r.Clean(ctx, entries, Options{Apply: true, IncludeClear: true})
			if o := res.Outcomes[0]; o.Status != Ran {
				t.Errorf("outcome = %+v", o)
			}
			if ents, err := os.ReadDir(cache); err != nil || len(ents) != 0 {
				t.Errorf("cache entries = %v, err = %v", ents, err)
			}
			if _, err := os.Stat(kept); err != nil {
				t.Errorf("%s: %v", tc.kept, err)
			}
			if _, err := os.Stat(log); err == nil {
				t.Errorf("%s ran", tc.tool)
			}
		})
	}
}

// TestPathEnvMustBeAbsolute skips a rule whose variable holds a relative path,
// because the tool would resolve it from a working directory that plonge cannot know.
func TestPathEnvMustBeAbsolute(t *testing.T) {
	bin := fakeTools(t)
	addTool(t, bin, "mytool", "exit 9")
	t.Setenv("MYTOOL_HOME", "relative/home")
	r := runner(t)
	rule := rules.Global{ID: "mycache", Tool: "mytool", Path: "~/.mytool", PathEnv: "MYTOOL_HOME",
		PathSubdir: "cache", Kind: rules.KindClear, DeleteContents: true}
	entries := r.List(context.Background(), []rules.Global{rule})
	e := entries[0]
	if e.Skipped != `MYTOOL_HOME is "relative/home", which is not an absolute path` || e.Command != "" {
		t.Errorf("entry = %+v", e)
	}
	res := r.Clean(context.Background(), entries, Options{Apply: true, IncludeClear: true})
	if o := res.Outcomes[0]; o.Status != Skipped {
		t.Errorf("outcome = %+v", o)
	}
}

// TestPathEnvReplacesFixedPath uses the variable when it is set and not empty,
// and the fixed path otherwise, and joins path_subdir to either one.
func TestPathEnvReplacesFixedPath(t *testing.T) {
	bin := fakeTools(t)
	addTool(t, bin, "mytool", "exit 9")
	r := runner(t)
	rule := rules.Global{ID: "mycache", Tool: "mytool", Path: "~/.mytool", PathEnv: "MYTOOL_HOME",
		PathSubdir: "cache", Clean: "mytool gc", Kind: rules.KindClear}
	dir := t.TempDir()
	for value, want := range map[string]string{
		"":  filepath.Join(r.Home, ".mytool", "cache"),
		dir: filepath.Join(dir, "cache"),
	} {
		t.Setenv("MYTOOL_HOME", value)
		if e := r.List(context.Background(), []rules.Global{rule})[0]; e.Path != want || e.Skipped != "" {
			t.Errorf("MYTOOL_HOME=%q: %+v", value, e)
		}
	}
}

func TestInputsCompareEachField(t *testing.T) {
	base := Entry{ID: "r", Kind: rules.KindPrune, Path: "/c", Command: "t prune",
		Targets: []string{"a@1"}, targetPaths: []string{"/i/a/1"}, keepNames: []string{"a", "b"}}
	same := base
	same.Targets = slices.Clone(base.Targets)
	if base.Inputs().String() != same.Inputs().String() {
		t.Errorf("equal entries differ: %s", base.Inputs())
	}
	for name, change := range map[string]func(e *Entry){
		// Each change keeps the text that a joined list would show.
		"keep_names":  func(e *Entry) { e.keepNames = []string{"a, b"} },
		"target path": func(e *Entry) { e.targetPaths = []string{"/i/a/2"} },
		"target name": func(e *Entry) { e.Targets = []string{"a@2"} },
		"targets split": func(e *Entry) {
			e.Targets, e.targetPaths = []string{"a@1", ""}, []string{"/i/a/1", ""}
		},
	} {
		e := base
		change(&e)
		if e.Inputs().String() == base.Inputs().String() {
			t.Errorf("%s: %s matches the original", name, e.Inputs())
		}
	}
	// JSON would show both names as "/d/A\ufffd".
	bad, worse := base, base
	bad.Path, worse.Path = "/d/A\xff", "/d/A\xfe"
	if bad.Inputs().String() == worse.Inputs().String() {
		t.Errorf("paths with different invalid bytes match: %s", bad.Inputs())
	}
	if !utf8.ValidString(bad.Inputs().String()) || !strings.Contains(bad.Inputs().String(), `"/d/A\xff"`) {
		t.Errorf("inputs = %s", bad.Inputs())
	}
	x := Entry{ID: rules.XcodeRuleID, Kind: rules.KindPrune, stale: []string{"/d/A"}, Bytes: 10}
	y := x
	y.Bytes = 11
	if x.Inputs().String() == y.Inputs().String() {
		t.Error("the Xcode rule's folder total is not compared")
	}
}
