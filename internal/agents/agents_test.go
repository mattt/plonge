package agents

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/mattt/plonge/internal/scan"
)

// fakePaseo puts a fake paseo alone on PATH that writes a JSON list.
func fakePaseo(t *testing.T, script string) {
	t.Helper()
	dir := t.TempDir()
	t.Setenv("PATH", dir)
	body := "#!/bin/sh\n" + script + "\n"
	if err := os.WriteFile(filepath.Join(dir, "paseo"), []byte(body), 0o755); err != nil {
		t.Fatal(err)
	}
}

// noPaseo leaves PATH empty of tools.
func noPaseo(t *testing.T) {
	t.Helper()
	t.Setenv("PATH", t.TempDir())
}

func mkdir(t *testing.T, path string) string {
	t.Helper()
	if err := os.MkdirAll(path, 0o755); err != nil {
		t.Fatal(err)
	}
	return path
}

func source(t *testing.T, sources []scan.Source, name string) scan.Source {
	t.Helper()
	for _, s := range sources {
		if s.Name == name {
			return s
		}
	}
	t.Fatalf("no source %q in %+v", name, sources)
	return scan.Source{}
}

func TestPaseoBusy(t *testing.T) {
	t.Setenv("CLAUDE_CONFIG_DIR", "")
	tmp := t.TempDir()
	home := mkdir(t, filepath.Join(tmp, "home"))
	running := mkdir(t, filepath.Join(tmp, "running"))
	starting := mkdir(t, filepath.Join(tmp, "starting"))
	idle := mkdir(t, filepath.Join(tmp, "idle"))
	other := mkdir(t, filepath.Join(tmp, "running-other"))
	sub := mkdir(t, filepath.Join(running, "pkg"))
	// The link makes the agent's directory differ from the root's spelling.
	link := filepath.Join(tmp, "link")
	if err := os.Symlink(starting, link); err != nil {
		t.Fatal(err)
	}
	fakePaseo(t, `[ "$*" = "ls --json --global --background" ] || exit 9
/bin/cat <<EOF
[{"cwd":"`+sub+`","status":"running"},
 {"cwd":"`+link+`","status":"initializing"},
 {"cwd":"`+idle+`","status":"idle"}]
EOF`)
	now := time.Now()
	old := now.Add(-72 * time.Hour)
	roots := []scan.Root{
		{Path: running, LastActivity: old, ActivitySource: "git"},
		{Path: starting, LastActivity: old, ActivitySource: "git"},
		{Path: idle, LastActivity: old, ActivitySource: "git"},
		{Path: other, LastActivity: old, ActivitySource: "git"},
	}
	sources := Apply(context.Background(), roots, home, now)
	for i, want := range []bool{true, true, false, false} {
		if roots[i].Busy != want {
			t.Errorf("%s busy = %v", filepath.Base(roots[i].Path), roots[i].Busy)
		}
		if want && (!roots[i].LastActivity.Equal(now) || roots[i].ActivitySource != SourcePaseo) {
			t.Errorf("%s: %+v", filepath.Base(roots[i].Path), roots[i])
		}
		if !want && roots[i].ActivitySource != "git" {
			t.Errorf("%s: %+v", filepath.Base(roots[i].Path), roots[i])
		}
	}
	if s := source(t, sources, SourcePaseo); s.Status != scan.SourceUsed {
		t.Errorf("paseo source = %+v", s)
	}
	if !Busy(context.Background(), running) || Busy(context.Background(), idle) {
		t.Error("Busy disagrees with Apply")
	}
}

func TestPaseoSkipped(t *testing.T) {
	t.Setenv("CLAUDE_CONFIG_DIR", "")
	home := t.TempDir()
	tests := []struct {
		name   string
		setup  func(t *testing.T)
		reason string
	}{
		{"missing", noPaseo, "not on PATH"},
		{"fails", func(t *testing.T) { fakePaseo(t, `echo "no daemon" >&2; exit 1`) }, "no daemon"},
		{"bad output", func(t *testing.T) { fakePaseo(t, `echo not json`) }, "cannot read"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			tt.setup(t)
			root := t.TempDir()
			roots := []scan.Root{{Path: root}}
			s := source(t, Apply(context.Background(), roots, home, time.Now()), SourcePaseo)
			if s.Status != scan.SourceSkipped || !strings.Contains(s.Reason, tt.reason) {
				t.Errorf("source = %+v", s)
			}
			if roots[0].Busy || Busy(context.Background(), root) {
				t.Error("a root is busy without a source")
			}
		})
	}
}

func TestEncodeFolder(t *testing.T) {
	tests := map[string]string{
		"/Users/me/Code/app":          "-Users-me-Code-app",
		"/Users/me/.paseo/worktrees":  "-Users-me--paseo-worktrees",
		"/Users/me/my_app, v2?/a b.c": "-Users-me-my-app--v2--a-b-c",
	}
	for in, want := range tests {
		if got := encodeFolder(in); got != want {
			t.Errorf("encodeFolder(%q) = %q, want %q", in, got, want)
		}
	}
}

func transcript(t *testing.T, projects, cwd, name string, at time.Time) {
	t.Helper()
	path := filepath.Join(projects, encodeFolder(cwd), name)
	mkdir(t, filepath.Dir(path))
	if err := os.WriteFile(path, []byte("{}\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.Chtimes(path, at, at); err != nil {
		t.Fatal(err)
	}
}

func TestClaudeActivity(t *testing.T) {
	noPaseo(t)
	t.Setenv("CLAUDE_CONFIG_DIR", "")
	home := t.TempDir()
	projects := filepath.Join(home, ".claude", "projects")
	now := time.Now().Truncate(time.Second)
	old := now.Add(-72 * time.Hour)
	recent := now.Add(-time.Hour)
	older := now.Add(-100 * time.Hour)

	newer := filepath.Join(home, "code", "newer")
	stale := filepath.Join(home, "code", "stale")
	none := filepath.Join(home, "code", "none")
	below := filepath.Join(home, "code", "below")
	transcript(t, projects, newer, "a.jsonl", older)
	transcript(t, projects, newer, "b.jsonl", recent)
	transcript(t, projects, newer, "ignored.txt", now)
	transcript(t, projects, stale, "a.jsonl", older)
	transcript(t, projects, filepath.Join(below, "pkg"), "a.jsonl", recent)

	roots := []scan.Root{
		{Path: newer, LastActivity: old, ActivitySource: "git"},
		{Path: stale, LastActivity: old, ActivitySource: "git"},
		{Path: none, LastActivity: old, ActivitySource: "git"},
		{Path: below, LastActivity: old, ActivitySource: "git"},
		{Path: filepath.Join(home, "skipped"), Skipped: "not a directory"},
	}
	sources := Apply(context.Background(), roots, home, now)
	if s := source(t, sources, SourceClaude); s.Status != scan.SourceUsed {
		t.Errorf("claude source = %+v", s)
	}
	if s := source(t, sources, SourcePaseo); s.Status != scan.SourceSkipped {
		t.Errorf("paseo source = %+v", s)
	}
	checks := []struct {
		at     time.Time
		source string
	}{{recent, SourceClaude}, {old, "git"}, {old, "git"}, {recent, SourceClaude}}
	for i, c := range checks {
		if !roots[i].LastActivity.Equal(c.at) || roots[i].ActivitySource != c.source {
			t.Errorf("%s: %v from %q, want %v from %q", filepath.Base(roots[i].Path),
				roots[i].LastActivity, roots[i].ActivitySource, c.at, c.source)
		}
	}
}

func TestClaudeConfigDirAndMissing(t *testing.T) {
	noPaseo(t)
	home := t.TempDir()
	config := t.TempDir()
	root := filepath.Join(home, "app")
	at := time.Now().Add(-time.Hour).Truncate(time.Second)
	transcript(t, filepath.Join(config, "projects"), root, "a.jsonl", at)

	t.Setenv("CLAUDE_CONFIG_DIR", config)
	roots := []scan.Root{{Path: root}}
	Apply(context.Background(), roots, home, time.Now())
	if !roots[0].LastActivity.Equal(at) || roots[0].ActivitySource != SourceClaude {
		t.Errorf("root = %+v", roots[0])
	}

	t.Setenv("CLAUDE_CONFIG_DIR", "")
	s := source(t, Apply(context.Background(), []scan.Root{{Path: root}}, home, time.Now()), SourceClaude)
	if s.Status != scan.SourceSkipped {
		t.Errorf("source = %+v", s)
	}
}
