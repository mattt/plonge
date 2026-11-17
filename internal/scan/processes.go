package scan

import (
	"context"

	"github.com/mattt/plonge/internal/procs"
)

// SourceProcesses is the activity source for running processes.
const SourceProcesses = "processes"

// RefuseUsed moves each candidate that a running process uses
// from Candidates to Refused, with the processes in the refusal.
// When the inspector fails, every root that was scanned is busy,
// because a process might use any cache.
func RefuseUsed(ctx context.Context, roots []Root, insp procs.Inspector) Source {
	snap, err := insp.Snapshot(ctx)
	if err != nil {
		for i := range roots {
			if roots[i].Skipped == "" {
				roots[i].Busy = true
			}
		}
		return Source{Name: SourceProcesses, Status: SourceFailed, Reason: err.Error()}
	}
	var paths []string
	for _, r := range roots {
		for _, c := range r.Candidates {
			paths = append(paths, c.Path)
		}
	}
	used := snap.Find(paths)
	for i := range roots {
		r := &roots[i]
		var kept []Candidate
		for _, c := range r.Candidates {
			if ms := used[c.Path]; len(ms) > 0 {
				r.Refused = append(r.Refused, Refusal{
					Path: c.Path, Project: r.Path, Rule: c.Rule,
					Reason: procs.Reason(c.Path, ms), Processes: ms,
				})
				continue
			}
			kept = append(kept, c)
		}
		r.Candidates = kept
	}
	return Source{Name: SourceProcesses, Status: SourceUsed}
}
