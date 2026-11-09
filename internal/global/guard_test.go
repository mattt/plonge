package global

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/mattt/plonge/internal/rules"
)

// guardedRule cleans with "gc run" after the check "gc dry".
var guardedRule = rules.Global{
	ID: "gc", Tool: "gc", Path: "~/cache", Clean: "gc run", Kind: rules.KindPrune,
	Check: "gc dry", Refuse: `cannot read (?P<path>.+)$`, RefuseReason: "files that gc cannot read",
}

// fakeGC prints output for the check, and logs each time the clean command runs.
func fakeGC(output, log string) string {
	return fmt.Sprintf(`case "$1" in
dry) %s ;;
run) echo run >> %q ;;
*) exit 9 ;;
esac`, output, log)
}

func addFile(t *testing.T, path string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, nil, 0o644); err != nil {
		t.Fatal(err)
	}
}

func ran(t *testing.T, log string) bool {
	t.Helper()
	_, err := os.Stat(log)
	return err == nil
}

func TestCheckFailureSkipsRule(t *testing.T) {
	bin := fakeTools(t)
	log := filepath.Join(t.TempDir(), "log")
	addTool(t, bin, "gc", fakeGC(`echo "dry run broke" >&2; exit 3`, log))
	r := runner(t)
	ctx := context.Background()
	entries := r.List(ctx, []rules.Global{guardedRule})
	if e := entries[0]; !strings.Contains(e.Skipped, "check failed") || !strings.Contains(e.Skipped, "dry run broke") || e.Command != "" {
		t.Fatalf("entry = %+v", e)
	}
	res := r.Clean(ctx, entries, Options{Apply: true})
	if res.Outcomes[0].Status != Skipped || res.Failed != 0 || ran(t, log) {
		t.Errorf("result = %+v, ran = %v", res, ran(t, log))
	}
}

func TestRefuseWithoutPathGroup(t *testing.T) {
	bin := fakeTools(t)
	log := filepath.Join(t.TempDir(), "log")
	addTool(t, bin, "gc", fakeGC(`echo "database is locked"`, log))
	rule := guardedRule
	rule.Refuse, rule.RefuseReason = "is locked", ""
	r := runner(t)
	e := r.List(context.Background(), []rules.Global{rule})[0]
	if e.Skipped != "the check refused this rule: database is locked" || len(e.Refused) != 0 {
		t.Errorf("entry = %+v", e)
	}
}

func TestRefuseCountsExistingFiles(t *testing.T) {
	bin := fakeTools(t)
	log := filepath.Join(t.TempDir(), "log")
	r := runner(t)
	addFile(t, filepath.Join(r.Home, "a: b", "x.toml"))
	addFile(t, filepath.Join(r.Home, "y.toml"))
	// The color codes and the second report of y.toml must not change the result.
	addTool(t, bin, "gc", fakeGC(fmt.Sprintf(
		`printf '\033[33mWARN\033[0m cannot read ~/a: b/x.toml: bad syntax: at line 1\n'
		echo "cannot read %s/y.toml: bad" >&2
		echo "cannot read ~/y.toml: bad"
		echo "cannot read ~/gone.toml: bad"`, r.Home), log))

	e := r.List(context.Background(), []rules.Global{guardedRule})[0]
	want := "files that gc cannot read: 2"
	if e.Skipped != want || strings.Join(e.Refused, "|") != "~/a: b/x.toml|~/y.toml" {
		t.Fatalf("entry = %+v", e)
	}

	data, err := json.Marshal(e)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Contains(data, []byte(`"refused":["~/a: b/x.toml","~/y.toml"]`)) {
		t.Errorf("JSON = %s", data)
	}
}

func TestGuardRunsAgainBeforeApply(t *testing.T) {
	bin := fakeTools(t)
	log := filepath.Join(t.TempDir(), "log")
	r := runner(t)
	flag := filepath.Join(t.TempDir(), "broken")
	addTool(t, bin, "gc", fakeGC(fmt.Sprintf(`[ -e %q ] && echo "cannot read not a path"; true`, flag), log))
	ctx := context.Background()
	entries := r.List(ctx, []rules.Global{guardedRule})
	if e := entries[0]; e.Skipped != "" || e.Command != "gc run" {
		t.Fatalf("entry = %+v", e)
	}
	addFile(t, flag)
	res := r.Clean(ctx, entries, Options{Apply: true})
	o := res.Outcomes[0]
	if o.Status != Skipped || o.Reason != "files that gc cannot read: 1" || ran(t, log) {
		t.Errorf("outcome = %+v, ran = %v", o, ran(t, log))
	}
}

func TestCheckEnvReachesOnlyTheCheck(t *testing.T) {
	bin := fakeTools(t)
	log := filepath.Join(t.TempDir(), "log")
	t.Setenv("GC_QUIET", "1")
	addTool(t, bin, "gc", fakeGC(`[ "$GC_QUIET" = 1 ] || echo "cannot read not a path"`, log))
	rule := guardedRule
	r := runner(t)
	if e := r.List(context.Background(), []rules.Global{rule})[0]; e.Skipped != "" {
		t.Fatalf("without check_env: %+v", e)
	}
	rule.CheckEnv = []string{"GC_QUIET=0"}
	if e := r.List(context.Background(), []rules.Global{rule})[0]; e.Skipped == "" {
		t.Fatalf("with check_env: %+v", e)
	}
}
