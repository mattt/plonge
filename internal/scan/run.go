package scan

import (
	"context"
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
