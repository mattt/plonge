// Package scan finds cache directories in project roots and measures them.
package scan

import (
	"context"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/mattt/plonge/internal/rules"
)

// Candidate is a cache directory that a rule matched.
type Candidate struct {
	Path string // absolute path of the directory
	Rule string // the rule that matched, such as "dist beside package.json"
	repo string // the directory in which to run Git for this candidate
}

// Root is the result of scanning one project root.
type Root struct {
	Path       string
	Skipped    string // why the root was skipped, or empty
	Candidates []Candidate
	Errors     []string // Git failures; the candidates they affect are left out
	// LastActivity is the modification time of the Git HEAD log, else of HEAD.
	LastActivity time.Time
}

// Detect walks root and returns the candidates that Git ignores
// and that hold no tracked files.
// It does not follow symlinks, does not enter .git,
// and does not descend into a candidate.
func Detect(ctx context.Context, root string, r rules.Rules) Root {
	res := Root{Path: root}
	info, err := os.Stat(root)
	if err != nil || !info.IsDir() {
		res.Skipped = "not a directory"
		return res
	}
	out, _, err := git(ctx, root, nil, "rev-parse", "--show-toplevel", "--git-path", "logs/HEAD", "--git-path", "HEAD")
	if err != nil {
		res.Skipped = "not in a Git work tree"
		return res
	}
	if lines := strings.Split(strings.TrimSpace(string(out)), "\n"); len(lines) == 3 {
		res.LastActivity = lastActivity(root, lines[1:])
	}

	found, repos := walk(root, r)
	byRepo := map[string][]Candidate{}
	for _, c := range found {
		c.repo = repoFor(c.Path, root, repos)
		byRepo[c.repo] = append(byRepo[c.repo], c)
	}
	repoDirs := make([]string, 0, len(byRepo))
	for dir := range byRepo {
		repoDirs = append(repoDirs, dir)
	}
	sort.Strings(repoDirs)
	for _, dir := range repoDirs {
		kept, err := filterIgnored(ctx, dir, byRepo[dir])
		if err != nil {
			res.Errors = append(res.Errors, err.Error())
			continue
		}
		res.Candidates = append(res.Candidates, kept...)
	}
	return res
}

// walk finds the directories under root that a rule matches,
// and the nested repositories (directories with a .git entry) under root.
func walk(root string, r rules.Rules) ([]Candidate, []string) {
	var found []Candidate
	var repos []string
	_ = filepath.WalkDir(root, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			if d != nil && d.IsDir() && path != root {
				return fs.SkipDir
			}
			return nil
		}
		if !d.IsDir() {
			return nil
		}
		name := d.Name()
		if name == ".git" {
			return fs.SkipDir
		}
		if path == root {
			return nil
		}
		parent := filepath.Dir(path)
		rule := r.Match(name, func(marker string) bool {
			_, err := os.Lstat(filepath.Join(parent, marker))
			return err == nil
		})
		if rule != "" {
			found = append(found, Candidate{Path: path, Rule: rule})
			return fs.SkipDir
		}
		if _, err := os.Lstat(filepath.Join(path, ".git")); err == nil {
			repos = append(repos, path)
		}
		return nil
	})
	return found, repos
}

// repoFor returns the innermost nested repository that holds path, or root.
func repoFor(path, root string, repos []string) string {
	best := root
	for _, repo := range repos {
		if strings.HasPrefix(path, repo+string(filepath.Separator)) && len(repo) > len(best) {
			best = repo
		}
	}
	return best
}

// argBatch limits how many paths go on one Git command line.
const argBatch = 256

// filterIgnored keeps the candidates that Git in dir ignores
// and that have no tracked files.
func filterIgnored(ctx context.Context, dir string, cands []Candidate) ([]Candidate, error) {
	rel := make(map[string]Candidate, len(cands))
	var input []byte
	for _, c := range cands {
		p, err := filepath.Rel(dir, c.Path)
		if err != nil {
			return nil, err
		}
		p = filepath.ToSlash(p)
		rel[p] = c
		input = append(input, p...)
		input = append(input, 0)
	}
	out, code, err := git(ctx, dir, input, "check-ignore", "--stdin", "-z")
	if err != nil && code != 1 { // 1 means that no path is ignored
		return nil, err
	}
	var ignored []string
	for _, p := range splitNUL(out) {
		if _, ok := rel[p]; ok {
			ignored = append(ignored, p)
		}
	}
	sort.Strings(ignored)

	tracked := map[string]bool{}
	for i := 0; i < len(ignored); i += argBatch {
		batch := ignored[i:min(i+argBatch, len(ignored))]
		args := append([]string{"ls-files", "-z", "--"}, batch...)
		out, _, err := gitLiteral(ctx, dir, args...)
		if err != nil {
			return nil, err
		}
		for _, f := range splitNUL(out) {
			for _, p := range batch {
				if f == p || strings.HasPrefix(f, p+"/") {
					tracked[p] = true
				}
			}
		}
	}

	var kept []Candidate
	for _, p := range ignored {
		if !tracked[p] {
			kept = append(kept, rel[p])
		}
	}
	return kept, nil
}
