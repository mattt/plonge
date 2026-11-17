// Package procs finds the running processes that use a path.
//
// A process uses a path when it has a file open under it,
// maps a library from it, works in it, runs an executable from it,
// or names it on its command line.
// A virtual environment's bin/python is a symlink to the base interpreter,
// so a process started from it shows the interpreter's path as its executable;
// only its command line names the environment.
package procs

import (
	"context"
	"fmt"
	"path/filepath"
	"sort"
	"strings"
	"time"
)

// The uses of a path, from the most to the least specific.
const (
	UseRuns = "runs" // the process runs an executable from the path
	UseOpen = "open" // the process has a file open or a library mapped
	UseCwd  = "cwd"  // the process works in the path
	UseRoot = "root" // the process has the path as its root directory
	UseArgs = "args" // the process names the path on its command line
)

var useRank = map[string]int{UseRuns: 0, UseOpen: 1, UseCwd: 2, UseRoot: 3, UseArgs: 4}

// File is a path that a process uses, and how.
type File struct {
	Use  string
	Path string
}

// Process is one running process and the paths that it uses.
type Process struct {
	PID   int
	Name  string
	User  string
	Files []File
}

// Snapshot is the list of processes at one time.
type Snapshot struct {
	Processes []Process
}

// Inspector takes a snapshot of the running processes.
type Inspector interface {
	Snapshot(ctx context.Context) (*Snapshot, error)
}

// Match is a process that uses a path.
type Match struct {
	PID  int    `json:"pid"`
	Name string `json:"name"`
	User string `json:"user,omitempty"`
	Use  string `json:"use"`
	// Path is the path that the process uses, at or below the matched path.
	Path string `json:"path"`
}

// Find returns, for each of paths, the processes that use it, ordered by PID.
// Each process appears once per path, with its most specific use.
// A path matches its own entries and, when it is or is inside a symlink,
// the entries at its resolved path.
// A path with no users is not in the result.
func (s *Snapshot) Find(paths []string) map[string][]Match {
	// Two paths can share a key, such as a path and a symlink to it.
	keys := map[string][]string{}
	minLen := -1
	add := func(key, path string) {
		keys[key] = append(keys[key], path)
		if minLen < 0 || len(key) < minLen {
			minLen = len(key)
		}
	}
	for _, p := range paths {
		p = filepath.Clean(p)
		add(p, p)
		if real, err := filepath.EvalSymlinks(p); err == nil && real != p {
			add(real, p)
		}
	}
	out := map[string][]Match{}
	if len(keys) == 0 {
		return out
	}
	for _, proc := range s.Processes {
		best := map[string]Match{}
		for _, f := range proc.Files {
			if !filepath.IsAbs(f.Path) {
				continue
			}
			for p := filepath.Clean(f.Path); len(p) >= minLen; p = filepath.Dir(p) {
				for _, path := range keys[p] {
					m, seen := best[path]
					if !seen || useRank[f.Use] < useRank[m.Use] {
						best[path] = Match{PID: proc.PID, Name: proc.Name, User: proc.User, Use: f.Use, Path: f.Path}
					}
				}
				if p == "/" {
					break
				}
			}
		}
		for path, m := range best {
			out[path] = append(out[path], m)
		}
	}
	for _, ms := range out {
		sort.Slice(ms, func(a, b int) bool { return ms[a].PID < ms[b].PID })
	}
	return out
}

// Reason says why a process in ms keeps path from deletion,
// such as "in use by node (PID 911), which has esbuild/bin/esbuild open, and 2 more".
// ms must not be empty.
func Reason(path string, ms []Match) string {
	m := ms[0]
	what := relative(path, m.Path)
	var how string
	switch m.Use {
	case UseRuns:
		how = "which runs " + what
	case UseOpen:
		how = "which has " + what + " open"
	case UseCwd:
		how = "which works in " + what
	case UseRoot:
		how = "whose root directory is " + what
	default:
		how = "whose command line names " + what
	}
	s := fmt.Sprintf("in use by %s (PID %d), %s", m.Name, m.PID, how)
	if n := len(ms) - 1; n > 0 {
		s += fmt.Sprintf(", and %d more", n)
	}
	return s
}

// relative returns used relative to path, or "it" when they are the same.
// When used is under path's resolved location, it is relative to that.
func relative(path, used string) string {
	for _, base := range []string{path, resolved(path)} {
		if rel, err := filepath.Rel(base, used); err == nil && !strings.HasPrefix(rel, "..") {
			if rel == "." {
				return "it"
			}
			return rel
		}
	}
	return used
}

func resolved(path string) string {
	if p, err := filepath.EvalSymlinks(path); err == nil {
		return p
	}
	return path
}

// argPaths returns the paths that the words of a command line name.
// A word counts when it is an absolute path, or a relative path with a slash,
// which is joined to cwd; with no cwd, a relative path does not count.
// In a word with an =, as in --flag=path, the text after the first = counts,
// and the whole word counts only when it is an absolute path.
// Paths are not resolved, because a symlink such as bin/python
// leads out of the directory that it names.
func argPaths(words []string, cwd string) []string {
	var out []string
	for _, w := range words {
		forms := []string{w}
		if _, after, ok := strings.Cut(w, "="); ok {
			forms = []string{after}
			if filepath.IsAbs(w) {
				forms = append([]string{w}, after)
			}
		}
		for _, f := range forms {
			switch {
			case filepath.IsAbs(f):
				out = append(out, filepath.Clean(f))
			case strings.Contains(f, "/") && cwd != "":
				out = append(out, filepath.Join(cwd, f))
			}
		}
	}
	return out
}

// trimDeleted removes the " (deleted)" that Linux and lsof add
// to the name of a file that was removed while open.
func trimDeleted(name string) string {
	return strings.TrimSuffix(name, " (deleted)")
}

// Timeout limits each command that takes a snapshot. Tests shorten it.
var Timeout = 30 * time.Second
