package global

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/mattt/plonge/internal/rules"
)

// defaultRule returns the default global rule with the given id.
func defaultRule(t *testing.T, id string) rules.Global {
	t.Helper()
	for _, g := range rules.Default().Global {
		if g.ID == id {
			return g
		}
	}
	t.Fatalf("no default rule %q", id)
	return rules.Global{}
}

// fakeBun prints version and dir as its cache, logs each command,
// and removes the blob on prune.
func fakeBun(version, dir, log string) string {
	return fmt.Sprintf(`echo "$*" >> %q
case "$*" in
--version) echo %q ;;
"pm cache") printf %%s %q ;;
"pm cache prune") /bin/rm %q/blob ;;
*) exit 9 ;;
esac`, log, version, dir, dir)
}

func TestBunPrunesItsCache(t *testing.T) {
	bin := fakeTools(t)
	cache := cacheDir(t, 20_000)
	log := filepath.Join(t.TempDir(), "log")
	addTool(t, bin, "bun", fakeBun("1.4.4", cache, log))
	r := runner(t)
	ctx := context.Background()

	entries := r.List(ctx, []rules.Global{defaultRule(t, "bun")})
	e := entries[0]
	if e.Skipped != "" || e.Path != cache || e.Command != "bun pm cache prune" || e.Kind != rules.KindClear || e.Bytes < 20_000 {
		t.Fatalf("entry = %+v", e)
	}
	if o := r.Clean(ctx, entries, Options{Apply: true}).Outcomes[0]; o.Status != Skipped {
		t.Errorf("ran without --include-clear: %+v", o)
	}
	res := r.Clean(ctx, entries, Options{Apply: true, IncludeClear: true})
	if o := res.Outcomes[0]; o.Status != Ran || o.FreedBytes < 20_000 {
		t.Errorf("outcome = %+v", o)
	}
	data, _ := os.ReadFile(log)
	if want := "pm cache\n--version\n--version\npm cache prune\n"; string(data) != want {
		t.Errorf("log = %q, want %q", data, want)
	}
}

func TestBunBefore144IsSkipped(t *testing.T) {
	cases := map[string]bool{
		"1.3.14":             false,
		"1.4.0":              false,
		"1.4.3":              false,
		"1.4.3-canary.1+abc": false,
		"0.8.1":              false,
		"":                   false,
		"bun":                false,
		"1.4.4":              true,
		"1.4.10":             true,
		"1.10.0":             true,
		"2.0.0":              true,
	}
	for version, ok := range cases {
		bin := fakeTools(t)
		cache := cacheDir(t, 10)
		log := filepath.Join(t.TempDir(), "log")
		addTool(t, bin, "bun", fakeBun(version, cache, log))
		r := runner(t)
		entries := r.List(context.Background(), []rules.Global{defaultRule(t, "bun")})
		e := entries[0]
		if ok {
			if e.Skipped != "" {
				t.Errorf("%s: skipped: %s", version, e.Skipped)
			}
			continue
		}
		if !strings.Contains(e.Skipped, "before 1.4.4") && !strings.Contains(e.Skipped, "no line that matches") || e.Command != "" {
			t.Errorf("%s: entry = %+v", version, e)
		}
		r.Clean(context.Background(), entries, Options{Apply: true, IncludeClear: true})
		if data, _ := os.ReadFile(log); strings.Contains(string(data), "prune") {
			t.Errorf("%s: log = %q", version, data)
		}
	}
}

func TestDenoDeletesOnlyItsCaches(t *testing.T) {
	r, tmp, _, log := deleteSetup(t)
	bin := filepath.SplitList(os.Getenv("PATH"))[0]
	denoDir := filepath.Join(tmp, "deno")
	for _, p := range []string{"remote/m", "npm/p", "gen/g", "location_data/local_storage", "deno_history.txt"} {
		addFile(t, filepath.Join(denoDir, p))
	}
	addTool(t, bin, "deno", fmt.Sprintf(`echo "$*" >> %q
[ "$*" = "info --json" ] || exit 9
d=%q
printf '{\n  "version": 1,\n  "denoDir": "%%s",\n  "modulesCache": "%%s/remote",\n  "npmCache": "%%s/npm",\n  "typescriptCache": "%%s/gen",\n  "originStorage": "%%s/location_data"\n}\n' "$d" "$d" "$d" "$d" "$d"`, log, denoDir))
	var rs []rules.Global
	for _, id := range []string{"deno-remote", "deno-npm", "deno-gen"} {
		rs = append(rs, defaultRule(t, id))
	}
	ctx := context.Background()
	entries := r.List(ctx, rs)
	for i, dir := range []string{"remote", "npm", "gen"} {
		e := entries[i]
		want := "delete contents of " + filepath.Join(denoDir, dir) + " (built in)"
		if e.Skipped != "" || e.Command != want || e.Kind != rules.KindClear {
			t.Errorf("entry = %+v", e)
		}
	}
	res := r.Clean(ctx, entries, Options{Apply: true, IncludeClear: true})
	if res.Failed != 0 {
		t.Errorf("result = %+v", res)
	}
	for _, dir := range []string{"remote", "npm", "gen"} {
		if ents, err := os.ReadDir(filepath.Join(denoDir, dir)); err != nil || len(ents) != 0 {
			t.Errorf("%s: entries = %v, err = %v", dir, ents, err)
		}
	}
	for _, p := range []string{"location_data/local_storage", "deno_history.txt"} {
		if _, err := os.Stat(filepath.Join(denoDir, p)); err != nil {
			t.Errorf("deleted user state %s: %v", p, err)
		}
	}
	data, _ := os.ReadFile(log)
	if strings.Contains(string(data), "clean") {
		t.Errorf("log = %q", data)
	}
}

// fakeYarn prints version and answers both Yarn rules' path commands with dir,
// so only the checks keep the two rules apart. It reports enableGlobalCache as true.
func fakeYarn(version, dir, log string) string {
	return fmt.Sprintf(`echo "$*" >> %q
case "$*" in
--version) printf '%%b\n' %q ;;
"cache dir"|"config get cacheFolder") echo %q ;;
"config get enableGlobalCache") echo true ;;
"cache clean") /bin/rm -f %q/blob ;;
*) exit 9 ;;
esac`, log, version, dir, dir)
}

func TestOnlyOneYarnRuleRuns(t *testing.T) {
	cases := []struct {
		version string
		want    string // the rule that can run, or empty
	}{
		{"1.22.22", "yarn"},
		{"4.18.1", "yarn-berry"},
		{"2.4.3", "yarn-berry"},
		{"10.0.0", "yarn-berry"},
		{"0.27.5", ""},
		{"warning: something\n1.22.22", "yarn"},
		{"! Corepack is about to download yarn\n4.18.1", "yarn-berry"},
		{"yarn 4.18.1", ""},
		{"", ""},
		{"1.22.22\n4.18.1", ""},
		{"4.18.1\n1.22.22", ""},
		{"1.22.22\n1.22.19", ""},
		{"4.18.1\n4.17.0", ""},
	}
	for _, c := range cases {
		bin := fakeTools(t)
		cache := cacheDir(t, 10)
		log := filepath.Join(t.TempDir(), "log")
		addTool(t, bin, "yarn", fakeYarn(c.version, cache, log))
		r := runner(t)
		ctx := context.Background()
		entries := r.List(ctx, []rules.Global{defaultRule(t, "yarn"), defaultRule(t, "yarn-berry")})
		var runnable []string
		for _, e := range entries {
			if e.Skipped == "" {
				runnable = append(runnable, e.ID)
			} else if !strings.HasPrefix(e.Skipped, "yarn --version") {
				t.Errorf("%q: %s: %s", c.version, e.ID, e.Skipped)
			}
		}
		if c.want == "" && len(runnable) != 0 || c.want != "" && (len(runnable) != 1 || runnable[0] != c.want) {
			t.Errorf("%q: runnable = %v, want %q", c.version, runnable, c.want)
		}
		res := r.Clean(ctx, entries, Options{Apply: true, IncludeClear: true})
		data, _ := os.ReadFile(log)
		if n := strings.Count(string(data), "cache clean"); c.want == "" && n != 0 || c.want != "" && n != 1 {
			t.Errorf("%q: ran cache clean %d times; result = %+v", c.version, n, res)
		}
	}
}

func TestYarnBerryUsesCacheFolder(t *testing.T) {
	bin := fakeTools(t)
	cache := cacheDir(t, 30_000)
	log := filepath.Join(t.TempDir(), "log")
	addTool(t, bin, "yarn", fakeYarn("4.18.1", cache, log))
	r := runner(t)
	ctx := context.Background()
	entries := r.List(ctx, []rules.Global{defaultRule(t, "yarn-berry")})
	e := entries[0]
	if e.Skipped != "" || e.Path != cache || e.Command != "yarn cache clean" || e.Kind != rules.KindClear || e.Bytes < 30_000 {
		t.Fatalf("entry = %+v", e)
	}
	res := r.Clean(ctx, entries, Options{Apply: true, IncludeClear: true})
	if o := res.Outcomes[0]; o.Status != Ran || o.FreedBytes < 30_000 {
		t.Errorf("outcome = %+v", o)
	}
	data, _ := os.ReadFile(log)
	if want := "config get cacheFolder\n--version\nconfig get enableGlobalCache\n--version\nconfig get enableGlobalCache\ncache clean\n"; string(data) != want {
		t.Errorf("log = %q, want %q", data, want)
	}

	// Outside a project with enableGlobalCache off, Yarn prints no path.
	addTool(t, bin, "yarn", `case "$*" in --version) echo 4.18.1 ;; "config get enableGlobalCache") echo false ;; *) echo undefined ;; esac`)
	if e := r.List(ctx, []rules.Global{defaultRule(t, "yarn-berry")})[0]; !strings.Contains(e.Skipped, "not an absolute path") {
		t.Errorf("entry = %+v", e)
	}
}

// TestYarnBerryKeepsLocalCacheOfProjectInHome checks a home directory
// that is a Yarn project with enableGlobalCache off. Yarn then reports the
// project's .yarn/cache, which zero-installs keeps in Git, as cacheFolder.
func TestYarnBerryKeepsLocalCacheOfProjectInHome(t *testing.T) {
	bin := fakeTools(t)
	r := runner(t)
	local := filepath.Join(r.Home, ".yarn", "cache")
	zip := filepath.Join(local, "pkg-npm-1.0.0.zip")
	addFile(t, zip)
	addFile(t, filepath.Join(r.Home, "package.json"))
	if err := os.WriteFile(filepath.Join(r.Home, ".yarnrc.yml"), []byte("enableGlobalCache: false\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	log := filepath.Join(t.TempDir(), "log")
	setting := filepath.Join(t.TempDir(), "setting")
	// The fake reads the setting from a file, so the test can change it.
	addTool(t, bin, "yarn", fmt.Sprintf(`case "$*" in
--version) echo 4.18.1 ;;
"config get cacheFolder") echo "$PWD/.yarn/cache" ;;
"config get enableGlobalCache") /bin/cat %q ;;
"cache clean") echo "cache clean" >> %q; /bin/rm -rf "$PWD/.yarn/cache" ;;
*) exit 9 ;;
esac`, setting, log))
	ctx := context.Background()
	rule := defaultRule(t, "yarn-berry")
	apply := Options{Apply: true, IncludeClear: true}

	for _, value := range []string{"false", "", "undefined", "true\ntrue"} {
		if err := os.WriteFile(setting, []byte(value+"\n"), 0o644); err != nil {
			t.Fatal(err)
		}
		entries := r.List(ctx, []rules.Global{rule})
		e := entries[0]
		if e.Path != local || !strings.Contains(e.Skipped, "yarn config get enableGlobalCache printed") || e.Command != "" {
			t.Errorf("%q: entry = %+v", value, e)
		}
		if o := r.Clean(ctx, entries, apply).Outcomes[0]; o.Status != Skipped {
			t.Errorf("%q: outcome = %+v", value, o)
		}
	}

	// A setting that Yarn cannot report also skips the rule.
	if err := os.Remove(setting); err != nil {
		t.Fatal(err)
	}
	if e := r.List(ctx, []rules.Global{rule})[0]; !strings.Contains(e.Skipped, "check failed") {
		t.Errorf("unreadable setting: %+v", e)
	}

	// The setting is checked again right before cleanup.
	if err := os.WriteFile(setting, []byte("true\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	entries := r.List(ctx, []rules.Global{rule})
	if entries[0].Skipped != "" {
		t.Fatalf("with enableGlobalCache: %+v", entries[0])
	}
	if err := os.WriteFile(setting, []byte("false\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if o := r.Clean(ctx, entries, apply).Outcomes[0]; o.Status != Skipped || !strings.Contains(o.Reason, "enableGlobalCache") {
		t.Errorf("recheck: outcome = %+v", o)
	}

	if _, err := os.Stat(log); err == nil {
		data, _ := os.ReadFile(log)
		t.Errorf("yarn cache clean ran: %q", data)
	}
	if _, err := os.Stat(zip); err != nil {
		t.Errorf("the local cache changed: %v", err)
	}
}
