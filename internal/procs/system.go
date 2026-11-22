package procs

import (
	"bufio"
	"bytes"
	"context"
	"fmt"
	"io"
	"os"
	"os/exec"
	"os/user"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"time"
)

// System returns the inspector for this system.
// On Linux, it reads /proc. Elsewhere, it runs lsof and ps.
// It leaves out Plonge's own process.
func System() Inspector {
	return system{self: os.Getpid()}
}

type system struct{ self int }

func (s system) Snapshot(ctx context.Context) (*Snapshot, error) {
	if runtime.GOOS == "linux" {
		return readProcFS("/proc", s.self)
	}
	return runLsofPS(ctx, s.self)
}

// toolDirs are where systems install lsof and ps, for a PATH that leaves them out.
var toolDirs = []string{"/usr/sbin", "/usr/bin", "/sbin", "/bin"}

func lookTool(name string) (string, error) {
	if p, err := exec.LookPath(name); err == nil {
		return p, nil
	}
	for _, dir := range toolDirs {
		if p, err := exec.LookPath(filepath.Join(dir, name)); err == nil {
			return p, nil
		}
	}
	return "", fmt.Errorf("%s is not installed", name)
}

// run runs a tool and returns its output; a nonzero exit is an error.
func run(ctx context.Context, name string, args ...string) ([]byte, error) {
	path, err := lookTool(name)
	if err != nil {
		return nil, err
	}
	ctx, cancel := context.WithTimeout(ctx, Timeout)
	defer cancel()
	cmd := exec.CommandContext(ctx, path, args...)
	// A child process that keeps the output open must not outlast the timeout.
	cmd.WaitDelay = time.Second
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	out, err := cmd.Output()
	if err != nil {
		if ctx.Err() == context.DeadlineExceeded {
			return nil, fmt.Errorf("%s did not finish in %v", name, Timeout)
		}
		if msg := lastLine(stderr.String()); msg != "" {
			return nil, fmt.Errorf("%s failed: %w: %s", name, err, msg)
		}
		return nil, fmt.Errorf("%s failed: %w", name, err)
	}
	return out, nil
}

func lastLine(s string) string {
	s = strings.TrimSpace(s)
	if i := strings.LastIndexByte(s, '\n'); i >= 0 {
		return s[i+1:]
	}
	return s
}

// runLsofPS takes a snapshot with one lsof call and one ps call.
// Without root, lsof lists only the current user's processes;
// ps gives the command lines of all of them.
func runLsofPS(ctx context.Context, self int) (*Snapshot, error) {
	// -w leaves out warnings, -n and -P leave out name lookups,
	// +c 0 prints whole command names,
	// and -F prints the process ID, command, login name, and each file's
	// descriptor and name, one field to a line.
	lsof, err := run(ctx, "lsof", "-w", "-n", "-P", "+c", "0", "-F", "pcLfn")
	if err != nil {
		return nil, err
	}
	ps, err := run(ctx, "ps", "-axww", "-o", "pid=,user=,args=")
	if err != nil {
		return nil, err
	}
	return mergePS(parseLsof(bytes.NewReader(lsof)), bytes.NewReader(ps), self), nil
}

// lsofProcess is a process that lsof listed, with its working directory.
type lsofProcess struct {
	Process
	cwd string
}

// parseLsof reads the output of lsof -F pcLfn.
// A cwd descriptor is the working directory, rtd the root directory,
// and the first txt the executable; later txt entries are mapped files.
// Every other descriptor with an absolute name is an open file.
func parseLsof(r io.Reader) []*lsofProcess {
	var procs []*lsofProcess
	var cur *lsofProcess
	fd := ""
	ranTxt := false
	sc := bufio.NewScanner(r)
	sc.Buffer(make([]byte, 64*1024), 1024*1024)
	for sc.Scan() {
		line := sc.Text()
		if line == "" {
			continue
		}
		tag, val := line[0], line[1:]
		if tag == 'p' {
			pid, err := strconv.Atoi(val)
			if err != nil {
				cur = nil
				continue
			}
			cur = &lsofProcess{Process: Process{PID: pid}}
			procs = append(procs, cur)
			fd, ranTxt = "", false
			continue
		}
		if cur == nil {
			continue
		}
		switch tag {
		case 'c':
			cur.Name = val
		case 'L':
			cur.User = val
		case 'f':
			fd = val
		case 'n':
			if !filepath.IsAbs(val) {
				continue
			}
			name := trimDeleted(val)
			use := UseOpen
			switch fd {
			case "cwd":
				use = UseCwd
				cur.cwd = name
			case "rtd":
				use = UseRoot
			case "txt":
				if !ranTxt {
					use, ranTxt = UseRuns, true
				}
			}
			if use == UseRoot && name == "/" {
				continue
			}
			cur.Files = append(cur.Files, File{Use: use, Path: name})
		}
	}
	return procs
}

// mergePS adds the command lines from ps -o pid=,user=,args= to procs.
// A process that lsof did not list gets its name from its first word.
// ps joins the words of a command line with spaces,
// so a path with a space in it reads as two words.
func mergePS(procs []*lsofProcess, r io.Reader, self int) *Snapshot {
	byPID := map[int]*lsofProcess{}
	for _, p := range procs {
		byPID[p.PID] = p
	}
	sc := bufio.NewScanner(r)
	sc.Buffer(make([]byte, 64*1024), 1024*1024)
	for sc.Scan() {
		f := strings.Fields(sc.Text())
		if len(f) < 3 {
			continue
		}
		pid, err := strconv.Atoi(f[0])
		if err != nil {
			continue
		}
		p, ok := byPID[pid]
		if !ok {
			p = &lsofProcess{Process: Process{PID: pid, Name: filepath.Base(f[2]), User: f[1]}}
			byPID[pid] = p
			procs = append(procs, p)
		}
		for _, path := range argPaths(f[2:], p.cwd) {
			p.Files = append(p.Files, File{Use: UseArgs, Path: path})
		}
	}
	s := &Snapshot{}
	for _, p := range procs {
		if p.PID != self {
			s.Processes = append(s.Processes, p.Process)
		}
	}
	return s
}

// readProcFS reads each process in a Linux /proc tree at root:
// its executable, working and root directories, open files, mapped files,
// command line, name, and user.
// A process that exits during the read, or a file that Plonge may not read,
// such as another user's open files, is left out without an error.
func readProcFS(root string, self int) (*Snapshot, error) {
	ents, err := os.ReadDir(root)
	if err != nil {
		return nil, fmt.Errorf("cannot read %s: %w", root, err)
	}
	users := map[string]string{}
	s := &Snapshot{}
	for _, ent := range ents {
		pid, err := strconv.Atoi(ent.Name())
		if err != nil || pid == self {
			continue
		}
		dir := filepath.Join(root, ent.Name())
		p := Process{PID: pid}
		comm, err := os.ReadFile(filepath.Join(dir, "comm"))
		if err != nil {
			continue // the process exited
		}
		p.Name = strings.TrimSpace(string(comm))
		p.User = procUser(dir, users)
		link := func(name, use string) string {
			target, err := os.Readlink(filepath.Join(dir, name))
			if err != nil || !filepath.IsAbs(target) {
				return ""
			}
			target = trimDeleted(target)
			if !(use == UseRoot && target == "/") {
				p.Files = append(p.Files, File{Use: use, Path: target})
			}
			return target
		}
		link("exe", UseRuns)
		cwd := link("cwd", UseCwd)
		link("root", UseRoot)
		if fds, err := os.ReadDir(filepath.Join(dir, "fd")); err == nil {
			for _, fd := range fds {
				link(filepath.Join("fd", fd.Name()), UseOpen)
			}
		}
		for _, path := range mappedFiles(filepath.Join(dir, "maps")) {
			p.Files = append(p.Files, File{Use: UseOpen, Path: path})
		}
		if cmdline, err := os.ReadFile(filepath.Join(dir, "cmdline")); err == nil {
			words := strings.Split(strings.TrimRight(string(cmdline), "\x00"), "\x00")
			for _, path := range argPaths(words, cwd) {
				p.Files = append(p.Files, File{Use: UseArgs, Path: path})
			}
		}
		s.Processes = append(s.Processes, p)
	}
	return s, nil
}

// mappedFiles returns the distinct files in a /proc/<pid>/maps file.
func mappedFiles(path string) []string {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil
	}
	seen := map[string]bool{}
	var out []string
	for line := range strings.SplitSeq(string(data), "\n") {
		// The path is the last field, after the inode; it can hold spaces.
		i := strings.Index(line, " /")
		if i < 0 {
			continue
		}
		name := trimDeleted(strings.TrimSpace(line[i+1:]))
		if !seen[name] {
			seen[name] = true
			out = append(out, name)
		}
	}
	return out
}

// procUser returns the name of the user who owns the process in dir,
// or the user ID when it has no name.
func procUser(dir string, cache map[string]string) string {
	data, err := os.ReadFile(filepath.Join(dir, "status"))
	if err != nil {
		return ""
	}
	for line := range strings.SplitSeq(string(data), "\n") {
		rest, ok := strings.CutPrefix(line, "Uid:")
		if !ok {
			continue
		}
		f := strings.Fields(rest)
		if len(f) == 0 {
			return ""
		}
		uid := f[0]
		if name, ok := cache[uid]; ok {
			return name
		}
		name := uid
		if u, err := user.LookupId(uid); err == nil {
			name = u.Username
		}
		cache[uid] = name
		return name
	}
	return ""
}
