package scan

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
)

// git runs a Git command in dir and returns its standard output.
// It removes variables that would point Git at a different repository
// or stop it from finding the repository that holds dir,
// lets Git search across filesystems,
// and sets GIT_OPTIONAL_LOCKS=0 so that Git does not rewrite the index.
func git(ctx context.Context, dir string, stdin []byte, args ...string) ([]byte, int, error) {
	return gitWithEnv(ctx, dir, stdin, nil, args...)
}

// gitLiteral is git with GIT_LITERAL_PATHSPECS=1, so that paths are never patterns.
// Use it only for commands that take pathspecs; git check-ignore rejects it.
func gitLiteral(ctx context.Context, dir string, args ...string) ([]byte, int, error) {
	return gitWithEnv(ctx, dir, nil, []string{"GIT_LITERAL_PATHSPECS=1"}, args...)
}

func gitWithEnv(ctx context.Context, dir string, stdin []byte, extra []string, args ...string) ([]byte, int, error) {
	cmd := exec.CommandContext(ctx, "git", args...)
	cmd.Dir = dir
	cmd.Env = append(gitEnv(), extra...)
	if stdin != nil {
		cmd.Stdin = bytes.NewReader(stdin)
	}
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	out, err := cmd.Output()
	if err != nil {
		if ee, ok := err.(*exec.ExitError); ok {
			return out, ee.ExitCode(), &gitError{args: args, stderr: strings.TrimSpace(stderr.String())}
		}
		return out, -1, err
	}
	return out, 0, nil
}

type gitError struct {
	args   []string
	stderr string
}

func (e *gitError) Error() string {
	return "git " + strings.Join(e.args, " ") + ": " + e.stderr
}

func gitEnv() []string {
	var env []string
	for _, kv := range os.Environ() {
		name, _, _ := strings.Cut(kv, "=")
		switch name {
		case "GIT_DIR", "GIT_WORK_TREE", "GIT_INDEX_FILE", "GIT_COMMON_DIR",
			"GIT_OBJECT_DIRECTORY", "GIT_NAMESPACE",
			"GIT_LITERAL_PATHSPECS", "GIT_GLOB_PATHSPECS", "GIT_NOGLOB_PATHSPECS",
			"GIT_ICASE_PATHSPECS", "GIT_OPTIONAL_LOCKS",
			"GIT_CEILING_DIRECTORIES", "GIT_DISCOVERY_ACROSS_FILESYSTEM":
			continue
		}
		env = append(env, kv)
	}
	return append(env, "GIT_OPTIONAL_LOCKS=0", "GIT_DISCOVERY_ACROSS_FILESYSTEM=1")
}

// splitNUL splits NUL-terminated Git output.
func splitNUL(out []byte) []string {
	var parts []string
	for _, p := range bytes.Split(out, []byte{0}) {
		if len(p) > 0 {
			parts = append(parts, string(p))
		}
	}
	return parts
}

// InRepository reports whether dir is in a Git work tree or a Git directory.
// It returns false only when Git finds no repository
// and no ancestor of dir, or dir itself, has a .git entry.
// Every other result is an error, so that a caller can refuse to act
// when Git cannot give a clear answer.
func InRepository(ctx context.Context, dir string) (bool, error) {
	out, code, err := gitWithEnv(ctx, dir, nil, []string{"LC_ALL=C"},
		"rev-parse", "--is-inside-work-tree", "--is-inside-git-dir")
	if err != nil {
		var ge *gitError
		if code != 128 || !errors.As(err, &ge) || !strings.HasPrefix(ge.stderr, "fatal: not a git repository (or any") {
			return false, err
		}
		marker, err := gitEntry(dir)
		if err != nil {
			return false, err
		}
		if marker != "" {
			return false, fmt.Errorf("git cannot use %s: %s", marker, ge.stderr)
		}
		return false, nil
	}
	lines := strings.Split(strings.TrimSuffix(string(out), "\n"), "\n")
	if len(lines) != 2 {
		return false, fmt.Errorf("unexpected output from git rev-parse: %q", out)
	}
	inside := false
	for _, line := range lines {
		switch line {
		case "true":
			inside = true
		case "false":
		default:
			return false, fmt.Errorf("unexpected output from git rev-parse: %q", out)
		}
	}
	return inside, nil
}

// gitEntry returns the first .git entry, of any type, in dir or an ancestor,
// or an empty string when there is none.
func gitEntry(dir string) (string, error) {
	dir, err := filepath.Abs(dir)
	if err != nil {
		return "", err
	}
	for {
		path := filepath.Join(dir, ".git")
		if _, err := os.Lstat(path); err == nil {
			return path, nil
		} else if !errors.Is(err, fs.ErrNotExist) {
			return "", err
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			return "", nil
		}
		dir = parent
	}
}
