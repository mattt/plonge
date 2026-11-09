package mcpserver

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"
)

func mkfile(t *testing.T, path, body string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
}

// env makes a temporary home whose only project has a quiet node_modules,
// with only Git and a fake go on PATH. It returns the home and the project.
func env(t *testing.T) (home, project string) {
	t.Helper()
	git, err := exec.LookPath("git")
	if err != nil {
		t.Fatal(err)
	}
	bin := t.TempDir()
	if err := os.Symlink(git, filepath.Join(bin, "git")); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", bin)
	home = t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("XDG_STATE_HOME", filepath.Join(home, "state"))
	t.Setenv("XDG_CONFIG_HOME", filepath.Join(home, "config"))
	t.Setenv("GIT_CONFIG_GLOBAL", os.DevNull)
	t.Setenv("GIT_CONFIG_NOSYSTEM", "1")
	t.Setenv("GIT_CEILING_DIRECTORIES", home)
	for _, name := range []string{"MISE_STATE_DIR", "MISE_CONFIG_DIR", "MISE_GLOBAL_CONFIG_FILE", "CLAUDE_CONFIG_DIR"} {
		t.Setenv(name, "")
	}

	project = filepath.Join(home, "code", "app")
	if out, err := exec.Command("git", "init", "-q", project).CombinedOutput(); err != nil {
		t.Fatalf("git init: %v\n%s", err, out)
	}
	mkfile(t, filepath.Join(project, "mise.toml"), "")
	mkfile(t, filepath.Join(project, ".gitignore"), "node_modules/\n")
	mkfile(t, filepath.Join(project, "node_modules", "x.js"), "x")
	old := time.Now().Add(-72 * time.Hour)
	if err := os.Chtimes(filepath.Join(project, ".git", "HEAD"), old, old); err != nil {
		t.Fatal(err)
	}
	tracked := filepath.Join(home, "state", "mise", "tracked-configs")
	if err := os.MkdirAll(tracked, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(filepath.Join(project, "mise.toml"), filepath.Join(tracked, "app")); err != nil {
		t.Fatal(err)
	}

	cache := filepath.Join(home, "gocache")
	mkfile(t, filepath.Join(cache, "blob"), strings.Repeat("x", 50_000))
	script := fmt.Sprintf(`#!/bin/sh
case "$*" in
"env GOCACHE") echo %q ;;
"clean -cache") /bin/rm %q/blob ;;
*) exit 9 ;;
esac
`, cache, cache)
	if err := os.WriteFile(filepath.Join(bin, "go"), []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	return home, project
}

func connect(t *testing.T) *mcp.ClientSession {
	t.Helper()
	ctx := context.Background()
	serverSide, clientSide := mcp.NewInMemoryTransports()
	if _, err := New().Connect(ctx, serverSide, nil); err != nil {
		t.Fatal(err)
	}
	client := mcp.NewClient(&mcp.Implementation{Name: "test", Version: "1"}, nil)
	session, err := client.Connect(ctx, clientSide, nil)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = session.Close() })
	return session
}

// call runs a tool and decodes its JSON text into out.
// It returns the error text when the tool reports an error.
func call(t *testing.T, s *mcp.ClientSession, name string, args map[string]any, out any) string {
	t.Helper()
	res, err := s.CallTool(context.Background(), &mcp.CallToolParams{Name: name, Arguments: args})
	if err != nil {
		t.Fatal(err)
	}
	text := res.Content[0].(*mcp.TextContent).Text
	if res.IsError {
		return text
	}
	if err := json.Unmarshal([]byte(text), out); err != nil {
		t.Fatalf("%v\n%s", err, text)
	}
	return ""
}

func TestToolDescriptionsAskForApproval(t *testing.T) {
	env(t)
	s := connect(t)
	list, err := s.ListTools(context.Background(), nil)
	if err != nil {
		t.Fatal(err)
	}
	byName := map[string]*mcp.Tool{}
	for _, tool := range list.Tools {
		byName[tool.Name] = tool
	}
	if len(byName) != 3 || byName["scan"] == nil || byName["disk"] == nil {
		t.Fatalf("tools = %v", byName)
	}
	clean := byName["clean"]
	for _, want := range []string{"dry run", "approval", "apply"} {
		if !strings.Contains(clean.Description, want) {
			t.Errorf("clean description lacks %q", want)
		}
	}
	if clean.Annotations.ReadOnlyHint || !byName["scan"].Annotations.ReadOnlyHint {
		t.Error("read-only hints are wrong")
	}
}

func TestScanTool(t *testing.T) {
	_, project := env(t)
	s := connect(t)
	var rep struct {
		Caches []struct{ Path string } `json:"caches"`
		Global []struct{ ID string }   `json:"global"`
	}
	if msg := call(t, s, "scan", map[string]any{}, &rep); msg != "" {
		t.Fatal(msg)
	}
	if len(rep.Caches) != 1 || rep.Caches[0].Path != filepath.Join(project, "node_modules") || rep.Global != nil {
		t.Errorf("report = %+v", rep)
	}
	if msg := call(t, s, "scan", map[string]any{"global": true}, &rep); msg != "" {
		t.Fatal(msg)
	}
	if len(rep.Global) == 0 {
		t.Errorf("report = %+v", rep)
	}
}

func TestCleanTool(t *testing.T) {
	_, project := env(t)
	s := connect(t)
	cache := filepath.Join(project, "node_modules")
	var res struct {
		Applied bool
		Items   []struct{ Status string }
	}
	if msg := call(t, s, "clean", map[string]any{}, &res); msg != "" {
		t.Fatal(msg)
	}
	if res.Applied || len(res.Items) != 1 || res.Items[0].Status != "would delete" {
		t.Errorf("dry run = %+v", res)
	}
	if _, err := os.Stat(cache); err != nil {
		t.Fatalf("the dry run touched the cache: %v", err)
	}
	if msg := call(t, s, "clean", map[string]any{"apply": true}, &res); msg != "" {
		t.Fatal(msg)
	}
	if !res.Applied || res.Items[0].Status != "deleted" {
		t.Errorf("result = %+v", res)
	}
	if _, err := os.Stat(cache); !os.IsNotExist(err) {
		t.Errorf("the cache is still there: %v", err)
	}
}

func TestCleanToolIncludeActive(t *testing.T) {
	_, project := env(t)
	now := time.Now()
	if err := os.Chtimes(filepath.Join(project, ".git", "HEAD"), now, now); err != nil {
		t.Fatal(err)
	}
	s := connect(t)
	var res struct{ Items []struct{ Status string } }
	if msg := call(t, s, "clean", map[string]any{"apply": true}, &res); msg != "" {
		t.Fatal(msg)
	}
	if len(res.Items) != 0 {
		t.Errorf("result = %+v", res)
	}
	if msg := call(t, s, "clean", map[string]any{"include_active": true}, &res); msg != "" {
		t.Fatal(msg)
	}
	if len(res.Items) != 1 {
		t.Errorf("include_active = %+v", res)
	}
}

func TestCleanToolGlobal(t *testing.T) {
	home, _ := env(t)
	s := connect(t)
	blob := filepath.Join(home, "gocache", "blob")
	type outcome struct{ ID, Status string }
	var res struct {
		Outcomes   []outcome
		FreedBytes int64 `json:"freed_bytes"`
	}
	status := func() string {
		for _, o := range res.Outcomes {
			if o.ID == "go-build" {
				return o.Status
			}
		}
		return ""
	}
	if msg := call(t, s, "clean", map[string]any{"global": true, "apply": true}, &res); msg != "" {
		t.Fatal(msg)
	}
	if status() != "skipped" {
		t.Errorf("without include_clear: %+v", res)
	}
	if _, err := os.Stat(blob); err != nil {
		t.Fatalf("a clear rule ran: %v", err)
	}
	if msg := call(t, s, "clean", map[string]any{"global": true, "include_clear": true}, &res); msg != "" {
		t.Fatal(msg)
	}
	if status() != "would run" {
		t.Errorf("dry run: %+v", res)
	}
	args := map[string]any{"global": true, "apply": true, "include_clear": true}
	if msg := call(t, s, "clean", args, &res); msg != "" {
		t.Fatal(msg)
	}
	if status() != "ran" || res.FreedBytes < 50_000 {
		t.Errorf("result: %+v", res)
	}
	if _, err := os.Stat(blob); !os.IsNotExist(err) {
		t.Errorf("the blob is still there: %v", err)
	}
}

func TestCleanToolRejectsBadCombinations(t *testing.T) {
	env(t)
	s := connect(t)
	var res any
	for _, args := range []map[string]any{
		{"include_clear": true},
		{"global": true, "include_active": true},
		{"global": true, "min_size": "1G"},
		{"min_size": "lots"},
	} {
		if msg := call(t, s, "clean", args, &res); msg == "" {
			t.Errorf("clean(%v) gave no error", args)
		}
	}
}

func TestDiskTool(t *testing.T) {
	env(t)
	s := connect(t)
	var out struct {
		FreeBytes int64 `json:"free_bytes"`
		Free      string
	}
	if msg := call(t, s, "disk", map[string]any{}, &out); msg != "" {
		t.Fatal(msg)
	}
	if out.FreeBytes <= 0 || out.Free == "" {
		t.Errorf("out = %+v", out)
	}
}
