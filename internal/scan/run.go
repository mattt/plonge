package scan

import (
	"context"
	"path/filepath"
	"runtime"
	"sync"

	"github.com/mattt/plonge/internal/rules"
)

// Workers bounds how many roots and caches plonge reads at the same time.
var Workers = max(4, runtime.NumCPU())

// parallel calls fn for each index in [0, n) with at most Workers calls at a time.
func parallel(n int, fn func(i int)) {
	sem := make(chan struct{}, Workers)
	var wg sync.WaitGroup
	for i := range n {
		sem <- struct{}{}
		wg.Add(1)
		go func() {
			defer func() { <-sem; wg.Done() }()
			fn(i)
		}()
	}
	wg.Wait()
}

// Run scans each root and returns the results in the order of roots.
func Run(ctx context.Context, roots []string, r rules.Rules) []Root {
	out := make([]Root, len(roots))
	parallel(len(roots), func(i int) {
		out[i] = Detect(ctx, roots[i], r)
	})
	return out
}

// WorkTrees reports which of dirs are in a Git work tree.
// The keys are the cleaned paths.
func WorkTrees(ctx context.Context, dirs []string) map[string]bool {
	in := make([]bool, len(dirs))
	parallel(len(dirs), func(i int) {
		_, _, err := git(ctx, dirs[i], nil, "rev-parse", "--show-toplevel")
		in[i] = err == nil
	})
	out := make(map[string]bool, len(dirs))
	for i, dir := range dirs {
		out[filepath.Clean(dir)] = in[i]
	}
	return out
}
