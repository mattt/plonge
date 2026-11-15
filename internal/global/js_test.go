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
		if !strings.Contains(e.Skipped, "before 1.4.4") || e.Command != "" {
			t.Errorf("%s: entry = %+v", version, e)
		}
		r.Clean(context.Background(), entries, Options{Apply: true, IncludeClear: true})
		if data, _ := os.ReadFile(log); strings.Contains(string(data), "prune") {
			t.Errorf("%s: log = %q", version, data)
		}
	}
}
