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

func TestRequireNeedsOneMatchingLine(t *testing.T) {
	rule := rules.Global{ID: "gc", Tool: "gc", Path: "~/cache", Clean: "gc run", Kind: rules.KindPrune, Check: "gc dry", Require: `^2\.`}
	cases := map[string]bool{
		`echo 2.1.0`:                       true,
		`printf '\033[1m2.1.0\033[0m\n'`:   true,
		`echo "a warning" >&2; echo 2.1.0`: true,
		`echo 1.9.0`:                       false,
		`true`:                             false,
		`echo "version 2.1.0"`:             false,
		`echo 2.1.0; echo 2.2.0`:           false,
	}
	for output, ok := range cases {
		bin := fakeTools(t)
		log := filepath.Join(t.TempDir(), "log")
		addTool(t, bin, "gc", fakeGC(output, log))
		r := runner(t)
		ctx := context.Background()
		entries := r.List(ctx, []rules.Global{rule})
		e := entries[0]
		if ok {
			if e.Skipped != "" {
				t.Errorf("%s: skipped: %s", output, e.Skipped)
			}
			continue
		}
		want := `gc dry printed no line that matches ^2\.`
		if strings.Count(output, "2.") > 1 {
			want = `gc dry printed more than one line that matches ^2\.`
		}
		if e.Skipped != want || e.Command != "" {
			t.Errorf("%s: entry = %+v, want %q", output, e, want)
		}
		if res := r.Clean(ctx, entries, Options{Apply: true}); res.Outcomes[0].Status != Skipped || ran(t, log) {
			t.Errorf("%s: result = %+v", output, res)
		}
	}
}

func TestRequireRunsAgainBeforeApply(t *testing.T) {
	bin := fakeTools(t)
	log := filepath.Join(t.TempDir(), "log")
	addTool(t, bin, "gc", fakeGC(`echo 2.1.0`, log))
	rule := rules.Global{ID: "gc", Tool: "gc", Path: "~/cache", Clean: "gc run", Kind: rules.KindPrune, Check: "gc dry", Require: `^2\.`}
	r := runner(t)
	ctx := context.Background()
	entries := r.List(ctx, []rules.Global{rule})
	if entries[0].Skipped != "" {
		t.Fatalf("entry = %+v", entries[0])
	}
	addTool(t, bin, "gc", fakeGC(`echo 1.0.0`, log))
	res := r.Clean(ctx, entries, Options{Apply: true})
	if o := res.Outcomes[0]; o.Status != Skipped || !strings.Contains(o.Reason, "no line that matches") || ran(t, log) {
		t.Errorf("outcome = %+v", o)
	}
}

func TestEveryCheckMustPass(t *testing.T) {
	bin := fakeTools(t)
	log := filepath.Join(t.TempDir(), "log")
	state := filepath.Join(t.TempDir(), "state")
	addTool(t, bin, "gc", fmt.Sprintf(`echo "$*" >> %q
case "$*" in
version) echo 2.1.0 ;;
setting) /bin/cat %q ;;
run) echo run >> %q ;;
*) exit 9 ;;
esac`, log, state, log))
	rule := rules.Global{
		ID: "gc", Tool: "gc", Path: "~/cache", Clean: "gc run", Kind: rules.KindPrune,
		Check: "gc version", Require: `^2\.`,
		Checks: []rules.CheckSpec{{Check: "gc setting", Require: `^on$`}},
	}
	write := func(v string) {
		if err := os.WriteFile(state, []byte(v+"\n"), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	r := runner(t)
	ctx := context.Background()

	write("off")
	if e := r.List(ctx, []rules.Global{rule})[0]; e.Skipped != "gc setting printed no line that matches ^on$" {
		t.Errorf("second check: %+v", e)
	}
	write("on")
	entries := r.List(ctx, []rules.Global{rule})
	if entries[0].Skipped != "" {
		t.Fatalf("both pass: %+v", entries[0])
	}
	write("off")
	if o := r.Clean(ctx, entries, Options{Apply: true}).Outcomes[0]; o.Status != Skipped {
		t.Errorf("recheck: %+v", o)
	}
	data, _ := os.ReadFile(log)
	if strings.Contains(string(data), "run") {
		t.Errorf("log = %q", data)
	}

	// A failing first check stops the later ones.
	if err := os.Remove(log); err != nil {
		t.Fatal(err)
	}
	first := rule
	first.Require = `^3\.`
	r.List(ctx, []rules.Global{first})
	if data, _ := os.ReadFile(log); strings.Contains(string(data), "setting") {
		t.Errorf("later check ran: %q", data)
	}
}
