package scan

import (
	"bytes"
	"context"
	"errors"
	"os"
	"os/exec"
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
// A failure other than Git finding no repository is an error,
// so that a caller can refuse to act when Git cannot answer.
func InRepository(ctx context.Context, dir string) (bool, error) {
	out, _, err := gitWithEnv(ctx, dir, nil, []string{"LC_ALL=C"},
		"rev-parse", "--is-inside-work-tree", "--is-inside-git-dir")
	if err != nil {
		var ge *gitError
		if errors.As(err, &ge) && strings.Contains(ge.stderr, "not a git repository") {
			return false, nil
		}
		return false, err
	}
	return strings.Contains(string(out), "true"), nil
}
