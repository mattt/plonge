package report

import (
	"bytes"
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/mattt/plonge/internal/clean"
	"github.com/mattt/plonge/internal/disk"
	"github.com/mattt/plonge/internal/global"
	"github.com/mattt/plonge/internal/scan"
)

var now = time.Date(2026, 3, 10, 12, 0, 0, 0, time.UTC)

func sample() Report {
	recent := now.Add(-2 * time.Hour)
	old := now.Add(-72 * time.Hour)
	roots := []scan.Root{
		{
			Path:         "/home/u/code/new",
			LastActivity: recent,
			Candidates: []scan.Candidate{
				{Path: "/home/u/code/new/node_modules", Rule: "node_modules"},
				{Path: "/home/u/code/new/dist", Rule: "dist beside package.json"},
			},
		},
		{Path: "/home/u/plain", Skipped: "not in a Git work tree"},
		{
			Path:         "/home/u/code/old",
			LastActivity: old,
			Errors:       []string{"git failed"},
			Candidates:   []scan.Candidate{{Path: "/home/u/code/old/.venv", Rule: ".venv"}},
		},
	}
	sizes := [][]int64{{4096, 1024}, nil, {4096}}
	return Build(roots, sizes, 24, 3, now)
}

func TestBuild(t *testing.T) {
	r := sample()
	var order []string
	for _, c := range r.Caches {
		order = append(order, c.Path)
	}
	// Larger first; for equal sizes, the older project first.
	want := []string{"/home/u/code/old/.venv", "/home/u/code/new/node_modules", "/home/u/code/new/dist"}
	if strings.Join(order, " ") != strings.Join(want, " ") {
		t.Errorf("order = %q, want %q", order, want)
	}
	if !r.Caches[0].Quiet || r.Caches[1].Quiet {
		t.Errorf("quiet flags = %v, %v; want true, false", r.Caches[0].Quiet, r.Caches[1].Quiet)
	}
	wantTotals := Totals{Caches: 3, Bytes: 9216, QuietCaches: 1, QuietBytes: 4096}
	if r.Totals != wantTotals {
		t.Errorf("totals = %+v, want %+v", r.Totals, wantTotals)
	}
	if len(r.SkippedRoots) != 1 || r.BrokenLinks != 3 || len(r.Errors) != 1 {
		t.Errorf("skipped %v, broken %d, errors %v", r.SkippedRoots, r.BrokenLinks, r.Errors)
	}
}

func TestWriteJSON(t *testing.T) {
	var buf bytes.Buffer
	if err := WriteJSON(&buf, sample()); err != nil {
		t.Fatal(err)
	}
	var got struct {
		QuietHours float64 `json:"quiet_hours"`
		Caches     []struct {
			Path         string    `json:"path"`
			Bytes        int64     `json:"bytes"`
			Project      string    `json:"project"`
			LastActivity time.Time `json:"last_activity"`
			Quiet        bool      `json:"quiet"`
			Rule         string    `json:"rule"`
		} `json:"caches"`
		Totals       Totals        `json:"totals"`
		Roots        int           `json:"roots"`
		SkippedRoots []SkippedRoot `json:"skipped_roots"`
		BrokenLinks  int           `json:"broken_links"`
	}
	if err := json.Unmarshal(buf.Bytes(), &got); err != nil {
		t.Fatalf("%v\n%s", err, buf.String())
	}
	c := got.Caches[0]
	if c.Path != "/home/u/code/old/.venv" || c.Bytes != 4096 || c.Project != "/home/u/code/old" ||
		!c.LastActivity.Equal(now.Add(-72*time.Hour)) || !c.Quiet || c.Rule != ".venv" {
		t.Errorf("first cache = %+v", c)
	}
	if got.Totals.QuietBytes != 4096 || got.Roots != 3 || got.BrokenLinks != 3 || got.QuietHours != 24 {
		t.Errorf("got %+v", got)
	}
	if len(got.SkippedRoots) != 1 || got.SkippedRoots[0].Reason != "not in a Git work tree" {
		t.Errorf("skipped roots = %+v", got.SkippedRoots)
	}
}

func TestWriteJSONEmpty(t *testing.T) {
	var buf bytes.Buffer
	if err := WriteJSON(&buf, Build(nil, nil, 24, 0, now)); err != nil {
		t.Fatal(err)
	}
	for _, key := range []string{`"caches": []`, `"skipped_roots": []`, `"errors": []`} {
		if !strings.Contains(buf.String(), key) {
			t.Errorf("JSON lacks %s:\n%s", key, buf.String())
		}
	}
}

func TestWriteTable(t *testing.T) {
	var buf bytes.Buffer
	if err := WriteTable(&buf, sample(), "/home/u"); err != nil {
		t.Fatal(err)
	}
	out := buf.String()
	for _, want := range []string{
		"Path", "~/code/old/.venv", "4.0 KiB", "~/code/old", "3d ago", "yes", "dist beside package.json",
		"Total: 9.0 KiB in 3 caches",
		"Quiet for 24h or more: 4.0 KiB in 1 cache",
		"Roots: 2 scanned, 1 skipped",
		"Broken mise links: 3",
		"Git errors: 1",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("table lacks %q:\n%s", want, out)
		}
	}
}

func TestTilde(t *testing.T) {
	tests := []struct{ path, home, want string }{
		{"/home/u/x", "/home/u", "~/x"},
		{"/home/u", "/home/u", "~"},
		{"/home/user/x", "/home/u", "/home/user/x"},
		{"/x", "", "/x"},
	}
	for _, tt := range tests {
		if got := Tilde(tt.path, tt.home); got != tt.want {
			t.Errorf("Tilde(%q, %q) = %q, want %q", tt.path, tt.home, got, tt.want)
		}
	}
}

func TestHumanBytes(t *testing.T) {
	tests := []struct {
		n    int64
		want string
	}{
		{0, "0 B"}, {1023, "1023 B"}, {1024, "1.0 KiB"}, {1536, "1.5 KiB"}, {5 << 30, "5.0 GiB"},
	}
	for _, tt := range tests {
		if got := HumanBytes(tt.n); got != tt.want {
			t.Errorf("HumanBytes(%d) = %q, want %q", tt.n, got, tt.want)
		}
	}
}

func TestAge(t *testing.T) {
	ago := func(d time.Duration) *time.Time { t := now.Add(-d); return &t }
	tests := []struct {
		t    *time.Time
		want string
	}{
		{nil, "unknown"}, {ago(10 * time.Second), "just now"}, {ago(5 * time.Minute), "5m ago"},
		{ago(30 * time.Hour), "30h ago"}, {ago(72 * time.Hour), "3d ago"},
	}
	for _, tt := range tests {
		if got := Age(tt.t, now); got != tt.want {
			t.Errorf("Age = %q, want %q", got, tt.want)
		}
	}
}

func TestWriteGlobalRefusedAndTargets(t *testing.T) {
	var buf bytes.Buffer
	err := writeGlobal(&buf, []global.Entry{
		{ID: "a", Skipped: "configs: 1", Refused: []string{"~/x/mise.toml"}},
		{ID: "b", Command: "b gc", Targets: []string{"bun@1", "go@2"}},
		{ID: "c", Command: "c gc", Note: "size is the whole directory"},
	}, "/home")
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{
		"Skipped a: configs: 1\n  ~/x/mise.toml\n",
		"b would remove: bun@1, go@2\n",
		"c: size is the whole directory\n",
	} {
		if !strings.Contains(buf.String(), want) {
			t.Errorf("missing %q in:\n%s", want, buf.String())
		}
	}
}

func TestWholeCacheLabel(t *testing.T) {
	entries := []global.Entry{
		{ID: "uv", Kind: "prune", Bytes: 3 << 30, Command: "uv cache prune", WholeCache: true},
		{ID: "pip", Kind: "clear", Bytes: 1 << 30, Command: "pip cache purge"},
	}
	var buf bytes.Buffer
	if err := writeGlobal(&buf, entries, "/home"); err != nil {
		t.Fatal(err)
	}
	scanOut := buf.String()
	buf.Reset()
	res := global.Result{Outcomes: []global.Outcome{
		{Entry: entries[0], Status: global.WouldRun},
		{Entry: entries[1], Status: global.WouldRun},
	}}
	if err := WriteGlobalClean(&buf, res, "/home"); err != nil {
		t.Fatal(err)
	}
	for name, out := range map[string]string{"scan": scanOut, "clean": buf.String()} {
		if !strings.Contains(out, "3.0 GiB (whole cache)") || strings.Contains(out, "1.0 GiB (whole cache)") {
			t.Errorf("%s: sizes are not labeled by kind:\n%s", name, out)
		}
		if !strings.Contains(out, "A size marked (whole cache)") {
			t.Errorf("%s: no note explains the label:\n%s", name, out)
		}
	}

	buf.Reset()
	if err := writeGlobal(&buf, entries[1:], "/home"); err != nil {
		t.Fatal(err)
	}
	if strings.Contains(buf.String(), "whole cache") {
		t.Errorf("a clear rule alone gets the note:\n%s", buf.String())
	}

	buf.Reset()
	if err := WriteJSON(&buf, res); err != nil {
		t.Fatal(err)
	}
	var got struct {
		Outcomes []map[string]any `json:"outcomes"`
	}
	if err := json.Unmarshal(buf.Bytes(), &got); err != nil {
		t.Fatal(err)
	}
	if got.Outcomes[0]["whole_cache"] != true || got.Outcomes[1]["whole_cache"] != nil {
		t.Errorf("JSON outcomes = %v", got.Outcomes)
	}
}

func TestWriteFreedReportsHomeVolume(t *testing.T) {
	tests := []struct {
		vol  *disk.Change
		want string
	}{
		{&disk.Change{FreeBefore: 80 << 30, FreeAfter: 94 << 30, GainedBytes: 14 << 30},
			"Free space on ~ grew by 14.0 GiB, from 80.0 GiB to 94.0 GiB; other processes and snapshots can change it\n"},
		{&disk.Change{FreeBefore: 2 << 30, FreeAfter: 1 << 30, GainedBytes: -1 << 30}, "Free space on ~ fell by 1.0 GiB"},
		{&disk.Change{FreeBefore: 5, FreeAfter: 5}, "Free space on ~ did not change"},
		{&disk.Change{Error: "no volume"}, "Free space on ~: cannot measure the change (no volume)\n"},
	}
	for _, tt := range tests {
		var buf bytes.Buffer
		if err := WriteClean(&buf, clean.Result{Applied: true, FreedBytes: 29 << 30, HomeVolume: tt.vol}, "/home"); err != nil {
			t.Fatal(err)
		}
		if !strings.Contains(buf.String(), "Estimated freed: 29.0 GiB") || !strings.Contains(buf.String(), tt.want) {
			t.Errorf("clean output lacks %q:\n%s", tt.want, buf.String())
		}
		buf.Reset()
		if err := WriteGlobalClean(&buf, global.Result{Applied: true, FreedBytes: 29 << 30, HomeVolume: tt.vol}, "/home"); err != nil {
			t.Fatal(err)
		}
		if !strings.Contains(buf.String(), "Estimated freed: 29.0 GiB") || !strings.Contains(buf.String(), tt.want) {
			t.Errorf("global output lacks %q:\n%s", tt.want, buf.String())
		}
	}
}
