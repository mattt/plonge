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
	want := []string{miseDryRun, miseDryRun, "prune --tools --yes"}
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
