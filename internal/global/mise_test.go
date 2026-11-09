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

// fakeMise logs its arguments and runs dryRun for the dry run.
func fakeMise(t *testing.T, dryRun string) (log string) {
	t.Helper()
	log = filepath.Join(t.TempDir(), "log")
	addTool(t, fakeTools(t), "mise", fmt.Sprintf(`echo "$*" >> %q
[ "$*" = "prune --tools --dry-run" ] || exit 0
%s`, log, dryRun))
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

const miseDryRun = "prune --tools --dry-run"

func TestMiseLoadWarningSkipsPrune(t *testing.T) {
	r := runner(t)
	addFile(t, filepath.Join(r.Home, "game", "mise.toml"))
	log := fakeMise(t, `echo "mise WARN  error loading tracked config file ~/game/mise.toml: failed to parse template" >&2`)
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
	if calls := miseCalls(t, log); !slices.Equal(calls, []string{miseDryRun}) {
		t.Errorf("calls = %q", calls)
	}
}

func TestMiseCleanDryRunLeadsToYes(t *testing.T) {
	r := runner(t)
	log := fakeMise(t, `echo "mise bun@1.3.10 is prunable: nothing requires bun" >&2`)
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
	want := []string{miseDryRun, "ls --prunable --json", miseDryRun, "prune --tools --yes"}
	if calls := miseCalls(t, log); !slices.Equal(calls, want) {
		t.Errorf("calls = %q, want %q", calls, want)
	}
}

func TestMiseWarningAboutMissingFileDoesNotSkip(t *testing.T) {
	r := runner(t)
	log := fakeMise(t, `echo "mise WARN  error loading tracked config file ~/deleted/mise.toml: No such file or directory" >&2`)
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
	fakeMise(t, `[ "$MISE_QUIET" = 0 ] && [ "$MISE_LOG_LEVEL" = info ] &&
echo "mise WARN  error loading tracked config file ~/game/mise.toml: bad" >&2; true`)

	e := r.List(context.Background(), []rules.Global{miseToolsRule(t)})[0]
	if e.Skipped != "tracked configs that mise cannot load: 1" {
		t.Errorf("entry = %+v", e)
	}
}

// fakeMiseTargets answers the dry run with dryRun and the list command with listing.
func fakeMiseTargets(t *testing.T, dryRun, listing string) (log string) {
	t.Helper()
	log = filepath.Join(t.TempDir(), "log")
	addTool(t, fakeTools(t), "mise", fmt.Sprintf(`echo "$*" >> %q
case "$*" in
"prune --tools --dry-run") %s ;;
"ls --prunable --json") %s ;;
esac`, log, dryRun, listing))
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
	fakeMiseTargets(t, ":", listing)

	entries := r.List(ctx, []rules.Global{miseToolsRule(t)})
	e := entries[0]
	if strings.Join(e.Targets, "|") != "bun@1.3.10|rust@1.81.0" || e.Note != "" {
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
			fakeMiseTargets(t, ":", listing)
			e := r.List(context.Background(), []rules.Global{miseToolsRule(t)})[0]
			if e.Skipped != "" || e.Bytes < 50_000 || len(e.Targets) != 0 || !strings.Contains(e.Note, "whole directory") {
				t.Errorf("entry = %+v", e)
			}
		})
	}
}

func TestMiseRefusalDoesNotListTargets(t *testing.T) {
	r := runner(t)
	addFile(t, filepath.Join(r.Home, "game", "mise.toml"))
	log := fakeMiseTargets(t, `echo "error loading tracked config file ~/game/mise.toml: bad" >&2`, `echo '{}'`)
	e := r.List(context.Background(), []rules.Global{miseToolsRule(t)})[0]
	if e.Skipped == "" || e.Bytes != 0 {
		t.Errorf("entry = %+v", e)
	}
	if calls := miseCalls(t, log); !slices.Equal(calls, []string{miseDryRun}) {
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
	bin := fakeTools(t)
	addTool(t, bin, "mise", fmt.Sprintf(`case "$*" in
"ls --prunable --json") echo '{"bun":[{"version":"1.3.10","install_path":%q}]}' ;;
"prune --tools --yes") /bin/rm -r %q ;;
esac`, version, version))

	entries := r.List(ctx, []rules.Global{miseToolsRule(t)})
	res := r.Clean(ctx, entries, Options{Apply: true})
	if o := res.Outcomes[0]; o.Status != Ran || o.FreedBytes < 60_000 {
		t.Errorf("outcome = %+v", o)
	}
}
