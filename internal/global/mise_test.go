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

func miseToolsRule(t *testing.T) rules.Global {
	t.Helper()
	i := slices.IndexFunc(rules.Default().Global, func(g rules.Global) bool { return g.ID == "mise-tools" })
	if i < 0 {
		t.Fatal("no mise-tools rule")
	}
	return rules.Default().Global[i]
}

// fakeMiseDoctor answers "mise doctor --json" with $MISE_DATA_DIR, as mise does.
const fakeMiseDoctor = `"doctor --json") printf '{"version":"2026.10.14","dirs":{"data":"%s"}}\n' "$MISE_DATA_DIR" ;;`

// useMiseData points mise at a data directory in the home directory
// and returns the installs directory in it.
func useMiseData(t *testing.T, r Runner) (installs string) {
	t.Helper()
	data := filepath.Join(r.Home, ".local", "share", "mise")
	t.Setenv("MISE_DATA_DIR", data)
	t.Setenv("MISE_INSTALLS_DIR", "")
	return filepath.Join(data, "installs")
}

// fakeMise logs its arguments and runs dryRun for the dry run.
func fakeMise(t *testing.T, r Runner, dryRun string) (log string) {
	t.Helper()
	useMiseData(t, r)
	log = filepath.Join(t.TempDir(), "log")
	addTool(t, fakeTools(t), "mise", fmt.Sprintf(`echo "$*" >> %q
case "$*" in
%s
"prune --tools --dry-run") %s ;;
esac`, log, fakeMiseDoctor, dryRun))
	return log
}

func miseCalls(t *testing.T, log string) []string {
	t.Helper()
	data, err := os.ReadFile(log)
	if err != nil {
		t.Fatal(err)
	}
	return strings.Split(strings.TrimSpace(string(data)), "\n")
}

const (
	miseDryRun     = "prune --tools --dry-run"
	miseDoctorCall = "doctor --json"
)

func TestMiseLoadWarningSkipsPrune(t *testing.T) {
	r := runner(t)
	addFile(t, filepath.Join(r.Home, "game", "mise.toml"))
	log := fakeMise(t, r, `echo "mise WARN  error loading tracked config file ~/game/mise.toml: failed to parse template" >&2`)
	ctx := context.Background()

	entries := r.List(ctx, []rules.Global{miseToolsRule(t)})
	e := entries[0]
	if e.Skipped != "tracked configs that mise cannot load: 1" || strings.Join(e.Refused, "|") != "~/game/mise.toml" || e.Command != "" {
		t.Fatalf("entry = %+v", e)
	}

	res := r.Clean(ctx, entries, Options{Apply: true})
	if res.Outcomes[0].Status != Skipped || res.Failed != 0 {
		t.Errorf("result = %+v", res)
	}
	if calls := miseCalls(t, log); !slices.Equal(calls, []string{miseDoctorCall, miseDryRun}) {
		t.Errorf("calls = %q", calls)
	}
}

func TestMiseCleanDryRunLeadsToYes(t *testing.T) {
	r := runner(t)
	log := fakeMise(t, r, `echo "mise bun@1.3.10 is prunable: nothing requires bun" >&2`)
	ctx := context.Background()

	entries := r.List(ctx, []rules.Global{miseToolsRule(t)})
	if e := entries[0]; e.Skipped != "" || e.Command != "mise prune --tools --yes" {
		t.Fatalf("entry = %+v", e)
	}

	dry := r.Clean(ctx, entries, Options{})
	if dry.Outcomes[0].Status != WouldRun {
		t.Errorf("dry = %+v", dry.Outcomes[0])
	}
	if calls := miseCalls(t, log); slices.Contains(calls, "prune --tools --yes") {
		t.Errorf("the dry run pruned: %q", calls)
	}

	res := r.Clean(ctx, entries, Options{Apply: true})
	if res.Outcomes[0].Status != Ran {
		t.Errorf("result = %+v", res.Outcomes[0])
	}
	want := []string{miseDoctorCall, miseDryRun, "ls --prunable --json", miseDryRun, "prune --tools --yes"}
	if calls := miseCalls(t, log); !slices.Equal(calls, want) {
		t.Errorf("calls = %q, want %q", calls, want)
	}
}

func TestMiseWarningAboutMissingFileDoesNotSkip(t *testing.T) {
	r := runner(t)
	log := fakeMise(t, r, `echo "mise WARN  error loading tracked config file ~/deleted/mise.toml: No such file or directory" >&2`)
	ctx := context.Background()

	entries := r.List(ctx, []rules.Global{miseToolsRule(t)})
	if e := entries[0]; e.Skipped != "" || len(e.Refused) != 0 {
		t.Fatalf("entry = %+v", e)
	}
	if res := r.Clean(ctx, entries, Options{Apply: true}); res.Outcomes[0].Status != Ran {
		t.Errorf("result = %+v", res.Outcomes[0])
	}
	if calls := miseCalls(t, log); !slices.Contains(calls, "prune --tools --yes") {
		t.Errorf("calls = %q", calls)
	}
}

func TestMiseCheckOverridesQuietSettings(t *testing.T) {
	r := runner(t)
	addFile(t, filepath.Join(r.Home, "game", "mise.toml"))
	t.Setenv("MISE_QUIET", "1")
	t.Setenv("MISE_LOG_LEVEL", "error")
	fakeMise(t, r, `[ "$MISE_QUIET" = 0 ] && [ "$MISE_LOG_LEVEL" = info ] &&
echo "mise WARN  error loading tracked config file ~/game/mise.toml: bad" >&2; true`)

	e := r.List(context.Background(), []rules.Global{miseToolsRule(t)})[0]
	if e.Skipped != "tracked configs that mise cannot load: 1" {
		t.Errorf("entry = %+v", e)
	}
}

// fakeMiseTargets answers the dry run with dryRun and the list command with listing.
func fakeMiseTargets(t *testing.T, r Runner, dryRun, listing string) (log string) {
	t.Helper()
	useMiseData(t, r)
	log = filepath.Join(t.TempDir(), "log")
	addTool(t, fakeTools(t), "mise", fmt.Sprintf(`echo "$*" >> %q
case "$*" in
%s
"prune --tools --dry-run") %s ;;
"ls --prunable --json") %s ;;
esac`, log, fakeMiseDoctor, dryRun, listing))
	return log
}

func TestMiseSizeIsThePrunableVersions(t *testing.T) {
	r := runner(t)
	ctx := context.Background()
	installs := filepath.Join(r.Home, ".local", "share", "mise", "installs")
	for _, d := range []struct {
		dir  string
		size int
	}{{"bun/1.3.10", 40_000}, {"bun/1.3.13", 900_000}, {"node/20", 900_000}} {
		dir := filepath.Join(installs, d.dir)
		if err := os.MkdirAll(dir, 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(dir, "blob"), make([]byte, d.size), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	// A symlinked version frees only the link, so the size of its target does not count.
	link := filepath.Join(installs, "rust", "1.81.0")
	if err := os.MkdirAll(filepath.Dir(link), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(filepath.Join(installs, "node", "20"), link); err != nil {
		t.Fatal(err)
	}
	listing := fmt.Sprintf(`echo '{"rust":[{"version":"1.81.0","install_path":%q}],"bun":[{"version":"1.3.10","install_path":%q}]}'`, link, filepath.Join(installs, "bun", "1.3.10"))
	fakeMiseTargets(t, r, ":", listing)

	entries := r.List(ctx, []rules.Global{miseToolsRule(t)})
	e := entries[0]
	if strings.Join(e.Targets, "|") != "bun@1.3.10|rust@1.81.0" || e.Note != "" || e.WholeCache {
		t.Fatalf("entry = %+v", e)
	}
	if e.Bytes < 40_000 || e.Bytes >= 900_000 {
		t.Errorf("Bytes = %d, want only the size of bun 1.3.10", e.Bytes)
	}

}

func TestMiseFallsBackToFolderSize(t *testing.T) {
	for name, listing := range map[string]string{
		"failing": `echo "no such flag" >&2; exit 2`,
		"garbage": `echo "not json"`,
	} {
		t.Run(name, func(t *testing.T) {
			r := runner(t)
			installs := filepath.Join(r.Home, ".local", "share", "mise", "installs", "bun", "1.3.10")
			if err := os.MkdirAll(installs, 0o755); err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(filepath.Join(installs, "blob"), make([]byte, 50_000), 0o644); err != nil {
				t.Fatal(err)
			}
			fakeMiseTargets(t, r, ":", listing)
			e := r.List(context.Background(), []rules.Global{miseToolsRule(t)})[0]
			if e.Skipped != "" || e.Bytes < 50_000 || len(e.Targets) != 0 || !strings.Contains(e.Note, "whole directory") || !e.WholeCache {
				t.Errorf("entry = %+v", e)
			}
		})
	}
}

func TestMiseRefusalDoesNotListTargets(t *testing.T) {
	r := runner(t)
	addFile(t, filepath.Join(r.Home, "game", "mise.toml"))
	log := fakeMiseTargets(t, r, `echo "error loading tracked config file ~/game/mise.toml: bad" >&2`, `echo '{}'`)
	e := r.List(context.Background(), []rules.Global{miseToolsRule(t)})[0]
	if e.Skipped == "" || e.Bytes != 0 {
		t.Errorf("entry = %+v", e)
	}
	if calls := miseCalls(t, log); !slices.Equal(calls, []string{miseDoctorCall, miseDryRun}) {
		t.Errorf("calls = %q", calls)
	}
}

func TestMiseFreedBytesUseTheWholeFolder(t *testing.T) {
	r := runner(t)
	ctx := context.Background()
	version := filepath.Join(r.Home, ".local", "share", "mise", "installs", "bun", "1.3.10")
	if err := os.MkdirAll(version, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(version, "blob"), make([]byte, 60_000), 0o644); err != nil {
		t.Fatal(err)
	}
	fakeMiseCases(t, fmt.Sprintf(`%s
"ls --prunable --json") echo '{"bun":[{"version":"1.3.10","install_path":%q}]}' ;;
"prune --tools --yes") /bin/rm -r %q ;;`, fakeMiseDoctor, version, version))
	useMiseData(t, r)

	entries := r.List(ctx, []rules.Global{miseToolsRule(t)})
	res := r.Clean(ctx, entries, Options{Apply: true})
	if o := res.Outcomes[0]; o.Status != Ran || o.Note != "" || o.FreedBytes < 60_000 {
		t.Errorf("outcome = %+v", o)
	}
}

// fakeMiseCases logs its arguments and runs the shell case items in cases.
func fakeMiseCases(t *testing.T, cases string) (log string) {
	t.Helper()
	log = filepath.Join(t.TempDir(), "log")
	addTool(t, fakeTools(t), "mise", fmt.Sprintf(`echo "$*" >> %q
case "$*" in
%s
esac`, log, cases))
	return log
}

// addVersion writes an installed version of size bytes and returns its directory.
func addVersion(t *testing.T, installs, version string, size int) string {
	t.Helper()
	dir := filepath.Join(installs, version)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "blob"), make([]byte, size), 0o644); err != nil {
		t.Fatal(err)
	}
	return dir
}

// customMiseData points mise at a data directory outside the home directory,
// puts a large version in the default installs directory, and returns
// the custom installs directory.
func customMiseData(t *testing.T, r Runner) (installs string) {
	t.Helper()
	data := filepath.Join(t.TempDir(), "mise-data")
	t.Setenv("MISE_DATA_DIR", data)
	t.Setenv("MISE_INSTALLS_DIR", "")
	addVersion(t, filepath.Join(r.Home, ".local", "share", "mise", "installs"), "node/20", 900_000)
	return filepath.Join(data, "installs")
}

func defaultInstalls(r Runner) string {
	return filepath.Join(r.Home, ".local", "share", "mise", "installs")
}

func TestMiseInstallsDirComesFromMiseDataDir(t *testing.T) {
	r := runner(t)
	installs := customMiseData(t, r)
	addVersion(t, installs, "bun/1.3.10", 70_000)
	fakeMiseCases(t, fakeMiseDoctor+`
"ls --prunable --json") exit 2 ;;`)

	e := r.List(context.Background(), []rules.Global{miseToolsRule(t)})[0]
	if e.Skipped != "" || e.Path != installs || strings.Contains(e.Note, "default") {
		t.Fatalf("entry = %+v, want path %s", e, installs)
	}
	if e.Bytes < 70_000 || e.Bytes >= 900_000 {
		t.Errorf("Bytes = %d, want only the custom installs directory", e.Bytes)
	}
}

func TestMiseInstallsDirAcceptsDoctorProblems(t *testing.T) {
	r := runner(t)
	installs := customMiseData(t, r)
	fakeMiseCases(t, `"doctor --json") printf '{"dirs":{"data":"%s"},"errors":["bad config"]}\n' "$MISE_DATA_DIR"; exit 1 ;;`)

	e := r.List(context.Background(), []rules.Global{miseToolsRule(t)})[0]
	if e.Path != installs || strings.Contains(e.Note, "default") {
		t.Errorf("entry = %+v, want path %s", e, installs)
	}
}

func TestMiseInstallsDirFromEnvironment(t *testing.T) {
	r := runner(t)
	customMiseData(t, r)
	elsewhere := filepath.Join(t.TempDir(), "installs")
	for name, tc := range map[string]struct{ env, want string }{
		"absolute": {elsewhere, elsewhere},
		"tilde":    {"~/tools/installs", filepath.Join(r.Home, "tools", "installs")},
	} {
		t.Run(name, func(t *testing.T) {
			t.Setenv("MISE_INSTALLS_DIR", tc.env)
			log := fakeMiseCases(t, fakeMiseDoctor+`
"ls --prunable --json") echo '{}' ;;`)
			e := r.List(context.Background(), []rules.Global{miseToolsRule(t)})[0]
			if e.Path != tc.want || e.Note != "" {
				t.Errorf("entry = %+v, want path %s", e, tc.want)
			}
			// mise does not need to report its data directory.
			if calls := miseCalls(t, log); slices.Contains(calls, miseDoctorCall) {
				t.Errorf("calls = %q", calls)
			}
		})
	}
}

func TestMiseInstallsDirFallsBackToRulePath(t *testing.T) {
	for name, tc := range map[string]struct{ env, doctor string }{
		"failing":       {"", `"doctor --json") echo "unknown command" >&2; exit 2 ;;`},
		"garbage":       {"", `"doctor --json") echo "not json" ;;`},
		"problem":       {"", `"doctor --json") echo "not json"; exit 1 ;;`},
		"relative data": {"", `"doctor --json") echo '{"dirs":{"data":"mise"}}' ;;`},
		"no data":       {"", `"doctor --json") echo '{"version":"2026.10.14"}' ;;`},
		"relative env":  {"installs", fakeMiseDoctor},
	} {
		t.Run(name, func(t *testing.T) {
			r := runner(t)
			customMiseData(t, r)
			t.Setenv("MISE_INSTALLS_DIR", tc.env)
			fakeMiseCases(t, tc.doctor)
			e := r.List(context.Background(), []rules.Global{miseToolsRule(t)})[0]
			if e.Skipped != "" || e.Path != defaultInstalls(r) || e.Bytes < 900_000 ||
				!strings.Contains(e.Note, "path is the rule's default: cannot read mise's installs directory") {
				t.Errorf("entry = %+v", e)
			}
		})
	}
}

func TestMiseFallbackNotesJoin(t *testing.T) {
	r := runner(t)
	customMiseData(t, r)
	fakeMiseCases(t, `"doctor --json") exit 2 ;;
"ls --prunable --json") exit 2 ;;`)
	e := r.List(context.Background(), []rules.Global{miseToolsRule(t)})[0]
	if !strings.Contains(e.Note, "rule's default") || !strings.Contains(e.Note, "; size is the whole directory") {
		t.Errorf("note = %q", e.Note)
	}
}

func TestMiseFreedBytesUseTheCustomInstallsDir(t *testing.T) {
	r := runner(t)
	ctx := context.Background()
	installs := customMiseData(t, r)
	version := addVersion(t, installs, "bun/1.3.10", 60_000)
	fakeMiseCases(t, fmt.Sprintf(`%s
"ls --prunable --json") echo '{"bun":[{"version":"1.3.10","install_path":%q}]}' ;;
"prune --tools --yes") /bin/rm -r %q ;;`, fakeMiseDoctor, version, version))

	entries := r.List(ctx, []rules.Global{miseToolsRule(t)})
	res := r.Clean(ctx, entries, Options{Apply: true})
	if o := res.Outcomes[0]; o.Status != Ran || o.Path != installs || o.FreedBytes < 60_000 {
		t.Errorf("outcome = %+v", o)
	}
}
