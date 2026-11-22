package mcpserver

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"slices"
	"strings"
	"testing"
	"time"
	"unicode/utf8"

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
	// The tmpdir rule must never see the real temporary directory.
	tmp := filepath.Join(home, "tmp")
	if err := os.Mkdir(tmp, 0o700); err != nil {
		t.Fatal(err)
	}
	t.Setenv("TMPDIR", tmp)
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
	return connectServer(t, New())
}

func connectServer(t *testing.T, server *mcp.Server) *mcp.ClientSession {
	t.Helper()
	ctx := context.Background()
	serverSide, clientSide := mcp.NewInMemoryTransports()
	if _, err := server.Connect(ctx, serverSide, nil); err != nil {
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

// dryRun runs a dry run with args and returns its preview token.
func dryRun(t *testing.T, s *mcp.ClientSession, args map[string]any) string {
	t.Helper()
	var res struct {
		Applied bool
		Token   string `json:"preview_token"`
	}
	if msg := call(t, s, "clean", args, &res); msg != "" {
		t.Fatal(msg)
	}
	if res.Applied || res.Token == "" {
		t.Fatalf("dry run %v = %+v", args, res)
	}
	return res.Token
}

// applyArgs returns args with apply and token added.
func applyArgs(args map[string]any, token string) map[string]any {
	out := map[string]any{"apply": true, "preview_token": token}
	for k, v := range args {
		out[k] = v
	}
	return out
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
	for _, want := range []string{"dry run", "approval", "apply", "preview_token"} {
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
		Token   string `json:"preview_token"`
	}
	if msg := call(t, s, "clean", map[string]any{}, &res); msg != "" {
		t.Fatal(msg)
	}
	if res.Applied || len(res.Items) != 1 || res.Items[0].Status != "would delete" || res.Token == "" {
		t.Errorf("dry run = %+v", res)
	}
	if _, err := os.Stat(cache); err != nil {
		t.Fatalf("the dry run touched the cache: %v", err)
	}
	if msg := call(t, s, "clean", applyArgs(nil, res.Token), &res); msg != "" {
		t.Fatal(msg)
	}
	if !res.Applied || res.Items[0].Status != "deleted" {
		t.Errorf("result = %+v", res)
	}
	if _, err := os.Stat(cache); !os.IsNotExist(err) {
		t.Errorf("the cache is still there: %v", err)
	}
}

// TestCleanToolKeepsCachesWhenPaseoFails checks that the clean tool
// deletes nothing when paseo is on PATH but fails.
func TestCleanToolKeepsCachesWhenPaseoFails(t *testing.T) {
	_, project := env(t)
	body := "#!/bin/sh\necho 'no daemon' >&2\nexit 1\n"
	if err := os.WriteFile(filepath.Join(os.Getenv("PATH"), "paseo"), []byte(body), 0o755); err != nil {
		t.Fatal(err)
	}
	s := connect(t)
	var res struct {
		Items       []struct{ Status string }
		NotSelected struct {
			Busy struct{ Caches int } `json:"busy"`
		} `json:"not_selected"`
		Sources []struct{ Name, Status, Reason string } `json:"activity_sources"`
	}
	for _, args := range []map[string]any{{}, applyArgs(map[string]any{"include_active": true}, dryRun(t, s, map[string]any{"include_active": true}))} {
		if msg := call(t, s, "clean", args, &res); msg != "" {
			t.Fatal(msg)
		}
		if len(res.Items) != 0 || res.NotSelected.Busy.Caches != 1 {
			t.Errorf("%v: result = %+v", args, res)
		}
		if len(res.Sources) != 3 || res.Sources[1].Status != "failed" || !strings.Contains(res.Sources[1].Reason, "no daemon") {
			t.Errorf("%v: sources = %+v", args, res.Sources)
		}
	}
	if _, err := os.Stat(filepath.Join(project, "node_modules")); err != nil {
		t.Errorf("a cache was deleted while Paseo failed: %v", err)
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
	if msg := call(t, s, "clean", applyArgs(nil, dryRun(t, s, nil)), &res); msg != "" {
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
	plain := map[string]any{"global": true}
	if msg := call(t, s, "clean", applyArgs(plain, dryRun(t, s, plain)), &res); msg != "" {
		t.Fatal(msg)
	}
	if status() != "skipped" {
		t.Errorf("without include_clear: %+v", res)
	}
	if _, err := os.Stat(blob); err != nil {
		t.Fatalf("a clear rule ran: %v", err)
	}
	clear := map[string]any{"global": true, "include_clear": true}
	if msg := call(t, s, "clean", clear, &res); msg != "" {
		t.Fatal(msg)
	}
	if status() != "would run" {
		t.Errorf("dry run: %+v", res)
	}
	if msg := call(t, s, "clean", applyArgs(clear, dryRun(t, s, clear)), &res); msg != "" {
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
		{"preview_token": "x"},
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

// TestScanToolKeepsProjectsInsideRootOutsideGit gives the folder above the
// project a mise record. That folder is not a Git work tree,
// so it must not hide the project inside it.
func TestScanToolKeepsProjectsInsideRootOutsideGit(t *testing.T) {
	home, project := env(t)
	folder := filepath.Dir(project)
	mkfile(t, filepath.Join(folder, "mise.toml"), "")
	tracked := filepath.Join(home, "state", "mise", "tracked-configs")
	if err := os.Symlink(filepath.Join(folder, "mise.toml"), filepath.Join(tracked, "folder")); err != nil {
		t.Fatal(err)
	}
	s := connect(t)
	var rep struct {
		Caches       []struct{ Path string } `json:"caches"`
		SkippedRoots []struct{ Path string } `json:"skipped_roots"`
	}
	if msg := call(t, s, "scan", map[string]any{}, &rep); msg != "" {
		t.Fatal(msg)
	}
	if len(rep.Caches) != 1 || rep.Caches[0].Path != filepath.Join(project, "node_modules") {
		t.Errorf("caches = %+v", rep.Caches)
	}
	if len(rep.SkippedRoots) != 1 || rep.SkippedRoots[0].Path != folder {
		t.Errorf("skipped roots = %+v", rep.SkippedRoots)
	}
	var res struct {
		Items []struct{ Path string } `json:"items"`
	}
	if msg := call(t, s, "clean", map[string]any{}, &res); msg != "" {
		t.Fatal(msg)
	}
	if len(res.Items) != 1 || res.Items[0].Path != filepath.Join(project, "node_modules") {
		t.Errorf("clean items = %+v", res.Items)
	}
}

func TestCleanToolApplyNeedsToken(t *testing.T) {
	_, project := env(t)
	s := connect(t)
	var res any
	for _, args := range []map[string]any{
		{"apply": true},
		{"apply": true, "global": true, "include_clear": true},
		{"apply": true, "preview_token": "made-up"},
	} {
		msg := call(t, s, "clean", args, &res)
		if msg == "" || !strings.Contains(msg, "preview_token") {
			t.Errorf("clean(%v) = %q", args, msg)
		}
	}
	if _, err := os.Stat(filepath.Join(project, "node_modules")); err != nil {
		t.Errorf("apply without a token deleted the cache: %v", err)
	}
}

func TestCleanToolTokenDeletesExactlyThePreview(t *testing.T) {
	home, project := env(t)
	other := filepath.Join(home, "code", "lib")
	if out, err := exec.Command("git", "init", "-q", other).CombinedOutput(); err != nil {
		t.Fatalf("git init: %v\n%s", err, out)
	}
	mkfile(t, filepath.Join(other, "mise.toml"), "")
	mkfile(t, filepath.Join(other, ".gitignore"), ".venv/\n")
	mkfile(t, filepath.Join(other, ".venv", "lib.py"), "x")
	old := time.Now().Add(-72 * time.Hour)
	if err := os.Chtimes(filepath.Join(other, ".git", "HEAD"), old, old); err != nil {
		t.Fatal(err)
	}
	tracked := filepath.Join(home, "state", "mise", "tracked-configs", "lib")
	if err := os.Symlink(filepath.Join(other, "mise.toml"), tracked); err != nil {
		t.Fatal(err)
	}
	s := connect(t)
	var res struct {
		Items []struct{ Path, Status string }
		Token string `json:"preview_token"`
	}
	paths := func(status string) []string {
		var out []string
		for _, it := range res.Items {
			if it.Status == status {
				out = append(out, it.Path)
			}
		}
		slices.Sort(out)
		return out
	}
	if msg := call(t, s, "clean", nil, &res); msg != "" {
		t.Fatal(msg)
	}
	token, previewed := res.Token, paths("would delete")
	if msg := call(t, s, "clean", applyArgs(nil, token), &res); msg != "" {
		t.Fatal(msg)
	}
	want := []string{filepath.Join(project, "node_modules"), filepath.Join(other, ".venv")}
	slices.Sort(want)
	if !slices.Equal(previewed, want) {
		t.Errorf("previewed %v, want %v", previewed, want)
	}
	if deleted := paths("deleted"); !slices.Equal(deleted, previewed) {
		t.Errorf("deleted %v, but the preview showed %v", deleted, previewed)
	}
	for _, p := range want {
		if _, err := os.Stat(p); !os.IsNotExist(err) {
			t.Errorf("%s is still there: %v", p, err)
		}
	}
	if msg := call(t, s, "clean", applyArgs(nil, token), &res); !strings.Contains(msg, "unknown preview_token") {
		t.Errorf("second use of the token = %q", msg)
	}
}

func TestCleanToolRefusesChangedSelection(t *testing.T) {
	cases := map[string]func(t *testing.T, home, project string){
		"cache grew": func(t *testing.T, _, project string) {
			mkfile(t, filepath.Join(project, "node_modules", "y.js"), strings.Repeat("y", 50_000))
		},
		"cache appeared": func(t *testing.T, _, project string) {
			mkfile(t, filepath.Join(project, ".gitignore"), "node_modules/\n.venv/\n")
			mkfile(t, filepath.Join(project, ".venv", "lib.py"), "x")
		},
		"cache disappeared": func(t *testing.T, _, project string) {
			if err := os.RemoveAll(filepath.Join(project, "node_modules")); err != nil {
				t.Fatal(err)
			}
		},
		// A process that starts to work in a cache after the dry run
		// makes the new scan refuse the cache, so the selection changes.
		"cache became busy": func(t *testing.T, _, project string) {
			if runtime.GOOS != "linux" && runtime.GOOS != "darwin" {
				t.Skip("no process inspector for " + runtime.GOOS)
			}
			sleep := exec.Command("/bin/sleep", "60")
			sleep.Dir = filepath.Join(project, "node_modules")
			if err := sleep.Start(); err != nil {
				t.Fatal(err)
			}
			t.Cleanup(func() { _ = sleep.Process.Kill(); _ = sleep.Wait() })
		},
	}
	for name, change := range cases {
		t.Run(name, func(t *testing.T) {
			home, project := env(t)
			s := connect(t)
			token := dryRun(t, s, nil)
			change(t, home, project)
			var res any
			msg := call(t, s, "clean", applyArgs(nil, token), &res)
			if !strings.Contains(msg, "selection changed") || !strings.Contains(msg, project) {
				t.Errorf("apply = %q", msg)
			}
			if name != "cache disappeared" {
				if _, err := os.Stat(filepath.Join(project, "node_modules")); err != nil {
					t.Errorf("the cache was deleted: %v", err)
				}
			}
		})
	}
}

func TestCleanToolRefusesTokenForOtherArguments(t *testing.T) {
	home, project := env(t)
	s := connect(t)
	var res any
	for _, args := range []map[string]any{
		{"include_active": true},
		{"min_size": "1KiB"},
		{"global": true},
	} {
		msg := call(t, s, "clean", applyArgs(args, dryRun(t, s, nil)), &res)
		if !strings.Contains(msg, "other arguments") {
			t.Errorf("%v: apply = %q", args, msg)
		}
	}
	global := map[string]any{"global": true}
	clear := map[string]any{"global": true, "include_clear": true}
	if msg := call(t, s, "clean", applyArgs(clear, dryRun(t, s, global)), &res); !strings.Contains(msg, "other arguments") {
		t.Errorf("include_clear: apply = %q", msg)
	}
	if _, err := os.Stat(filepath.Join(project, "node_modules")); err != nil {
		t.Errorf("the cache was deleted: %v", err)
	}
	if _, err := os.Stat(filepath.Join(home, "gocache", "blob")); err != nil {
		t.Errorf("a global rule ran: %v", err)
	}
}

func TestCleanToolRefusesChangedGlobalSelection(t *testing.T) {
	home, _ := env(t)
	s := connect(t)
	clear := map[string]any{"global": true, "include_clear": true}
	token := dryRun(t, s, clear)
	// The fake go now reports another cache directory.
	moved := filepath.Join(home, "gocache2")
	mkfile(t, filepath.Join(moved, "blob"), "x")
	gobin := filepath.Join(os.Getenv("PATH"), "go")
	body, err := os.ReadFile(gobin)
	if err != nil {
		t.Fatal(err)
	}
	body = []byte(strings.Replace(string(body), "echo "+fmt.Sprintf("%q", filepath.Join(home, "gocache")), "echo "+fmt.Sprintf("%q", moved), 1))
	if err := os.WriteFile(gobin, body, 0o755); err != nil {
		t.Fatal(err)
	}
	var res any
	msg := call(t, s, "clean", applyArgs(clear, token), &res)
	if !strings.Contains(msg, "changed: go-build") {
		t.Errorf("apply = %q", msg)
	}
	if _, err := os.Stat(filepath.Join(home, "gocache", "blob")); err != nil {
		t.Errorf("the rule ran: %v", err)
	}
}

func TestCleanToolTokenExpires(t *testing.T) {
	_, project := env(t)
	now := time.Now()
	s := connectServer(t, newServer(func() time.Time { return now }))
	token := dryRun(t, s, nil)
	now = now.Add(previewTTL + time.Second)
	var res any
	if msg := call(t, s, "clean", applyArgs(nil, token), &res); !strings.Contains(msg, "expired") {
		t.Errorf("apply = %q", msg)
	}
	if _, err := os.Stat(filepath.Join(project, "node_modules")); err != nil {
		t.Errorf("the cache was deleted: %v", err)
	}
}

func TestPreviewsKeepOnlyTheNewest(t *testing.T) {
	p := newPreviews(time.Now)
	first, _ := p.add(cleanArgs{}, selection{})
	for range maxPreviews {
		p.add(cleanArgs{}, selection{})
	}
	if _, err := p.take(first); err == nil {
		t.Error("the oldest preview was kept")
	}
	if len(p.tokens) != maxPreviews || len(p.order) != maxPreviews {
		t.Errorf("kept %d tokens, %d in order", len(p.tokens), len(p.order))
	}
}

func TestCleanToolGlobalPreviewListsXcodeFolders(t *testing.T) {
	if runtime.GOOS != "darwin" {
		t.Skip("the Xcode rule runs only on macOS")
	}
	home, _ := env(t)
	derived := filepath.Join(home, "Library", "Developer", "Xcode", "DerivedData")
	var folders []string
	for _, name := range []string{"Gone-abc", "Lost-def"} {
		folder := filepath.Join(derived, name)
		mkfile(t, filepath.Join(folder, "Build", "blob"), "x")
		mkfile(t, filepath.Join(folder, "info.plist"), fmt.Sprintf(`<plist version="1.0"><dict>
<key>WorkspacePath</key><string>%s</string>
</dict></plist>`, filepath.Join(home, name+".xcworkspace")))
		folders = append(folders, folder)
	}
	type outcome struct {
		ID      string
		Folders []string `json:"folders"`
		Omitted int      `json:"paths_omitted"`
	}
	var res struct{ Outcomes []outcome }
	s := connect(t)
	if msg := call(t, s, "clean", map[string]any{"global": true}, &res); msg != "" {
		t.Fatal(msg)
	}
	i := slices.IndexFunc(res.Outcomes, func(o outcome) bool { return o.ID == "xcode-derived-data" })
	if i < 0 || !slices.Equal(res.Outcomes[i].Folders, folders) || res.Outcomes[i].Omitted != 0 {
		t.Errorf("outcomes = %+v, want folders %v", res.Outcomes, folders)
	}
}

func TestFirstPathsBoundsThePreview(t *testing.T) {
	paths := make([]string, maxPreviewPaths+3)
	for i := range paths {
		paths[i] = fmt.Sprintf("/p/%d", i)
	}
	shown, omitted := firstPaths(paths)
	if len(shown) != maxPreviewPaths || shown[0] != "/p/0" || omitted != 3 {
		t.Errorf("shown %d from %s, omitted %d", len(shown), shown[0], omitted)
	}
	if shown, omitted := firstPaths(paths[:2]); len(shown) != 2 || omitted != 0 {
		t.Errorf("small list: shown %v, omitted %d", shown, omitted)
	}
	if !strings.Contains(cleanDescription, fmt.Sprintf("at most %d paths", maxPreviewPaths)) {
		t.Error("the clean description does not give the limit")
	}
}

// staleTmp sets a tmpdir age of 36 milliseconds in the rules file,
// makes an entry with a file in the temporary directory for each name,
// and waits until the entries are old enough. It returns their resolved paths.
func staleTmp(t *testing.T, home string, names ...string) []string {
	t.Helper()
	mkfile(t, filepath.Join(home, "config", "plonge", "rules.toml"), "tmpdir_age_hours = 0.00001\n")
	tmp, err := filepath.EvalSymlinks(os.Getenv("TMPDIR"))
	if err != nil {
		t.Fatal(err)
	}
	var paths []string
	for _, name := range names {
		mkfile(t, filepath.Join(tmp, name, "blob"), "x")
		paths = append(paths, filepath.Join(tmp, name))
	}
	time.Sleep(100 * time.Millisecond)
	return paths
}

func TestCleanToolGlobalPreviewListsTmpDirEntries(t *testing.T) {
	if runtime.GOOS != "darwin" && runtime.GOOS != "linux" {
		t.Skip("the tmpdir rule runs only on macOS and Linux")
	}
	home, _ := env(t)
	entries := staleTmp(t, home, "build-a", "build-b")
	type outcome struct {
		ID      string
		Status  string
		Folders []string `json:"folders"`
		Omitted int      `json:"paths_omitted"`
	}
	var res struct{ Outcomes []outcome }
	s := connect(t)
	if msg := call(t, s, "clean", map[string]any{"global": true}, &res); msg != "" {
		t.Fatal(msg)
	}
	i := slices.IndexFunc(res.Outcomes, func(o outcome) bool { return o.ID == "tmpdir" })
	if i < 0 || !slices.Equal(res.Outcomes[i].Folders, entries) || res.Outcomes[i].Omitted != 0 {
		t.Errorf("outcomes = %+v, want folders %v", res.Outcomes, entries)
	}
}

func TestCleanToolRefusesChangedTmpDirSelection(t *testing.T) {
	if runtime.GOOS != "darwin" && runtime.GOOS != "linux" {
		t.Skip("the tmpdir rule runs only on macOS and Linux")
	}
	home, _ := env(t)
	entries := staleTmp(t, home, "build-a")
	s := connect(t)
	clear := map[string]any{"global": true, "include_clear": true}
	token := dryRun(t, s, clear)

	// The selected entry gets another name, with the same size.
	renamed := filepath.Join(filepath.Dir(entries[0]), "build-z")
	if err := os.Rename(entries[0], renamed); err != nil {
		t.Fatal(err)
	}
	time.Sleep(100 * time.Millisecond)
	var res any
	msg := call(t, s, "clean", applyArgs(clear, token), &res)
	if !strings.Contains(msg, "changed: tmpdir") {
		t.Errorf("apply = %q", msg)
	}
	// No global rule ran: neither the tmpdir rule nor the go-build clear rule.
	if _, err := os.Stat(filepath.Join(renamed, "blob")); err != nil {
		t.Errorf("the tmpdir rule ran: %v", err)
	}
	if _, err := os.Stat(filepath.Join(home, "gocache", "blob")); err != nil {
		t.Errorf("the go-build rule ran: %v", err)
	}
}

func TestFirstPathsShowsNamesThatAreNotUTF8(t *testing.T) {
	shown, _ := firstPaths([]string{"/t/a\xff", "/t/a\xfe", `/t/"q"`, `"x`, "/t/é"})
	want := []string{`"/t/a\xff"`, `"/t/a\xfe"`, `/t/"q"`, `"\"x"`, "/t/é"}
	if !slices.Equal(shown, want) {
		t.Errorf("shown = %q, want %q", shown, want)
	}
	for _, s := range shown {
		if !utf8.ValidString(s) {
			t.Errorf("%q is not valid UTF-8", s)
		}
	}
	d := selection{"/t/a\xff": "x"}.diff(selection{})
	if len(d) != 1 || !strings.Contains(d[0], `"/t/a\xff"`) {
		t.Errorf("diff = %q", d)
	}
}
