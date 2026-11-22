package procs

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"runtime"
	"strings"
	"testing"
	"time"
)

func TestFind(t *testing.T) {
	// Resolve the temporary directory, which is under a symlink on macOS,
	// so that the only symlink is the one that the test makes.
	dir, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	app := filepath.Join(dir, "app")
	venv := filepath.Join(app, ".venv")
	nm := filepath.Join(app, "node_modules")
	if err := os.MkdirAll(venv, 0o755); err != nil {
		t.Fatal(err)
	}
	// link/.venv reaches app/.venv through a symlinked parent,
	// so it has the users of app/.venv.
	link := filepath.Join(dir, "link")
	if err := os.Symlink(app, link); err != nil {
		t.Fatal(err)
	}
	s := &Snapshot{Processes: []Process{
		{PID: 30, Name: "node", Files: []File{
			{UseArgs, filepath.Join(nm, ".bin", "vite")},
			{UseOpen, filepath.Join(nm, "esbuild", "bin", "esbuild")},
		}},
		{PID: 10, Name: "python3.12", Files: []File{
			{UseRuns, "/usr/bin/python3.12"},
			{UseCwd, app},
			{UseArgs, filepath.Join(venv, "bin", "python")},
		}},
		{PID: 20, Name: "sh", Files: []File{{UseCwd, filepath.Join(venv, "lib")}}},
		{PID: 40, Name: "python", Files: []File{{UseRuns, filepath.Join(app, ".venv2", "bin", "python")}}},
		{PID: 50, Name: "relative", Files: []File{{UseOpen, ".venv/x"}}},
	}}
	linked := filepath.Join(link, ".venv")
	got := s.Find([]string{venv, nm, linked, filepath.Join(app, "build")})
	wantVenv := []Match{
		{PID: 10, Name: "python3.12", Use: UseArgs, Path: filepath.Join(venv, "bin", "python")},
		{PID: 20, Name: "sh", Use: UseCwd, Path: filepath.Join(venv, "lib")},
	}
	if !reflect.DeepEqual(got[venv], wantVenv) {
		t.Errorf(".venv: %+v", got[venv])
	}
	// The open file ranks above the command line word.
	wantNM := []Match{{PID: 30, Name: "node", Use: UseOpen, Path: filepath.Join(nm, "esbuild", "bin", "esbuild")}}
	if !reflect.DeepEqual(got[nm], wantNM) {
		t.Errorf("node_modules: %+v", got[nm])
	}
	if !reflect.DeepEqual(got[linked], wantVenv) {
		t.Errorf("symlinked .venv: %+v", got[linked])
	}
	if len(got) != 3 {
		t.Errorf("got %d paths: %+v", len(got), got)
	}
}

func TestReason(t *testing.T) {
	path := "/code/app/.venv"
	tests := []struct {
		m    Match
		more int
		want string
	}{
		{Match{PID: 4312, Name: "python3.12", Use: UseArgs, Path: path + "/bin/python"}, 0,
			"in use by python3.12 (PID 4312), whose command line names bin/python"},
		{Match{PID: 911, Name: "node", Use: UseOpen, Path: path + "/esbuild/bin/esbuild"}, 2,
			"in use by node (PID 911), which has esbuild/bin/esbuild open, and 2 more"},
		{Match{PID: 7, Name: "zsh", Use: UseCwd, Path: path}, 0, "in use by zsh (PID 7), which works in it"},
		{Match{PID: 8, Name: "zsh", Use: UseCwd, Path: path + "/lib"}, 0, "in use by zsh (PID 8), which works in lib"},
		{Match{PID: 9, Name: "ruff", Use: UseRuns, Path: path + "/bin/ruff"}, 0, "in use by ruff (PID 9), which runs bin/ruff"},
		{Match{PID: 10, Name: "x", Use: UseRoot, Path: path}, 0, "in use by x (PID 10), whose root directory is it"},
	}
	for _, tt := range tests {
		ms := []Match{tt.m}
		for range tt.more {
			ms = append(ms, Match{PID: 1, Name: "other", Use: UseOpen, Path: path})
		}
		if got := Reason(path, ms); got != tt.want {
			t.Errorf("Reason = %q, want %q", got, tt.want)
		}
	}
}

func TestArgPaths(t *testing.T) {
	words := []string{"python", "-m", "app", ".venv/bin/gunicorn", "--config=/etc/app.conf", "/opt/x/../y", "--venv=env/bin", "plain=word"}
	want := []string{"/srv/app/.venv/bin/gunicorn", "/etc/app.conf", "/opt/y", "/srv/app/env/bin"}
	if got := argPaths(words, "/srv/app"); !reflect.DeepEqual(got, want) {
		t.Errorf("with cwd: %q", got)
	}
	want = []string{"/etc/app.conf", "/opt/y"}
	if got := argPaths(words, ""); !reflect.DeepEqual(got, want) {
		t.Errorf("without cwd: %q", got)
	}
}

const lsofOutput = `p100
cnode
Lmattt
fcwd
n/code/web
frtd
n/
ftxt
n/opt/homebrew/bin/node
ftxt
n/code/web/node_modules/fsevents/fsevents.node
f3
n/code/web/node_modules/esbuild/bin/esbuild
f4
n->0x1234
f5
n/code/web/tmp/old.log (deleted)
p200
cpython3.12
Lmattt
fcwd
n/code/app
ftxt
n/Users/mattt/.local/share/uv/python/bin/python3.12
pbad
cskipped
p300
cplonge
Lmattt
fcwd
n/code/app/.venv
`

const psOutput = `  100 mattt            node node_modules/.bin/vite --port=5173
  200 mattt            /code/app/.venv/bin/python -m app
  300 mattt            plonge clean
  400 root             /srv/api/.venv/bin/python /srv/api/main.py --config=conf/api.toml
  500 _www
`

func TestLsofAndPS(t *testing.T) {
	s := mergePS(parseLsof(strings.NewReader(lsofOutput)), strings.NewReader(psOutput), 300)
	want := []Process{
		{PID: 100, Name: "node", User: "mattt", Files: []File{
			{UseCwd, "/code/web"},
			{UseRuns, "/opt/homebrew/bin/node"},
			{UseOpen, "/code/web/node_modules/fsevents/fsevents.node"},
			{UseOpen, "/code/web/node_modules/esbuild/bin/esbuild"},
			{UseOpen, "/code/web/tmp/old.log"},
			{UseArgs, "/code/web/node_modules/.bin/vite"},
		}},
		{PID: 200, Name: "python3.12", User: "mattt", Files: []File{
			{UseCwd, "/code/app"},
			{UseRuns, "/Users/mattt/.local/share/uv/python/bin/python3.12"},
			{UseArgs, "/code/app/.venv/bin/python"},
		}},
		// Another user's process has no lsof entry and no known cwd.
		{PID: 400, Name: "python", User: "root", Files: []File{
			{UseArgs, "/srv/api/.venv/bin/python"},
			{UseArgs, "/srv/api/main.py"},
		}},
	}
	if !reflect.DeepEqual(s.Processes, want) {
		t.Errorf("processes:\n%+v\nwant\n%+v", s.Processes, want)
	}
}

// fakeProc makes a process directory in a fake /proc tree.
func fakeProc(t *testing.T, root, pid string, files map[string]string, links map[string]string) string {
	t.Helper()
	dir := filepath.Join(root, pid)
	for name, body := range files {
		path := filepath.Join(dir, name)
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	for name, target := range links {
		path := filepath.Join(dir, name)
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.Symlink(target, path); err != nil {
			t.Fatal(err)
		}
	}
	return dir
}

func TestReadProcFS(t *testing.T) {
	root := t.TempDir()
	maps := "55d0-55d1 r--p 00000000 08:01 1234 /usr/bin/python3.12\n" +
		"7f00-7f01 r-xp 00000000 08:01 99 /srv/app/.venv/lib/python3.12/site-packages/_cffi.so\n" +
		"7f02-7f03 r-xp 00000000 08:01 99 /srv/app/.venv/lib/python3.12/site-packages/_cffi.so\n" +
		"7f04-7f05 rw-p 00000000 00:00 0 [heap]\n" +
		"7f06-7f07 r--p 00000000 08:01 7 /srv/app/data file.bin (deleted)\n"
	fakeProc(t, root, "42", map[string]string{
		"comm":    "python3.12\n",
		"cmdline": "/srv/app/.venv/bin/python\x00-m\x00app\x00.venv/bin/gunicorn\x00",
		"status":  "Name:\tpython3.12\nUid:\t4242424\t4242424\t4242424\t4242424\n",
		"maps":    maps,
	}, map[string]string{
		"exe":     "/usr/bin/python3.12",
		"cwd":     "/srv/app",
		"root":    "/",
		"fd/0":    "/dev/null",
		"fd/3":    "socket:[1234]",
		"fd/4":    "/srv/app/.venv/lib/log.txt (deleted)",
		"task/x":  "/ignored",
		"ns/mnt":  "mnt:[1]",
		"environ": "/ignored",
	})
	fakeProc(t, root, "7", map[string]string{"comm": "self\n"}, map[string]string{"cwd": "/srv/app/.venv"})
	fakeProc(t, root, "9", map[string]string{"cmdline": "exited before comm was read"}, nil)
	fakeProc(t, root, "self", map[string]string{"comm": "not a pid\n"}, nil)
	// Another user's process: only comm and cmdline are readable.
	other := fakeProc(t, root, "50", map[string]string{
		"comm":     "gunicorn\n",
		"cmdline":  "/opt/svc/.venv/bin/gunicorn\x00",
		"fd/.keep": "",
	}, nil)
	if os.Geteuid() != 0 {
		if err := os.Chmod(filepath.Join(other, "fd"), 0); err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() { _ = os.Chmod(filepath.Join(other, "fd"), 0o755) })
	}

	s, err := readProcFS(root, 7)
	if err != nil {
		t.Fatal(err)
	}
	want := []Process{
		{PID: 42, Name: "python3.12", User: "4242424", Files: []File{
			{UseRuns, "/usr/bin/python3.12"},
			{UseCwd, "/srv/app"},
			{UseOpen, "/dev/null"},
			{UseOpen, "/srv/app/.venv/lib/log.txt"},
			{UseOpen, "/usr/bin/python3.12"},
			{UseOpen, "/srv/app/.venv/lib/python3.12/site-packages/_cffi.so"},
			{UseOpen, "/srv/app/data file.bin"},
			{UseArgs, "/srv/app/.venv/bin/python"},
			{UseArgs, "/srv/app/.venv/bin/gunicorn"},
		}},
		{PID: 50, Name: "gunicorn", Files: []File{{UseArgs, "/opt/svc/.venv/bin/gunicorn"}}},
	}
	if !reflect.DeepEqual(s.Processes, want) {
		t.Errorf("processes:\n%+v\nwant\n%+v", s.Processes, want)
	}

	if _, err := readProcFS(filepath.Join(root, "missing"), 0); err == nil {
		t.Error("a missing /proc did not fail")
	}
}

// TestSystem starts a process that works in a directory
// and checks that the system inspector finds it.
func TestSystem(t *testing.T) {
	if runtime.GOOS != "linux" && runtime.GOOS != "darwin" {
		t.Skip("no inspector for " + runtime.GOOS)
	}
	dir := filepath.Join(t.TempDir(), ".venv")
	if err := os.Mkdir(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	cmd := exec.Command("sleep", "30")
	cmd.Dir = dir
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = cmd.Process.Kill(); _ = cmd.Wait() })

	ctx, cancel := context.WithTimeout(context.Background(), time.Minute)
	defer cancel()
	s, err := System().Snapshot(ctx)
	if err != nil {
		t.Fatal(err)
	}
	ms := s.Find([]string{dir})[dir]
	found := false
	for _, m := range ms {
		if m.PID == cmd.Process.Pid && m.Use == UseCwd {
			found = true
		}
		if m.PID == os.Getpid() {
			t.Errorf("the snapshot holds Plonge's own process: %+v", m)
		}
	}
	if !found {
		t.Errorf("sleep (PID %d) not found: %+v", cmd.Process.Pid, ms)
	}
}
