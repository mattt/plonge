package clean

import (
	"context"
	"errors"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/mattt/plonge/internal/procs"
)

// fakeInspector returns a fixed snapshot or error and counts its calls.
type fakeInspector struct {
	snap  *procs.Snapshot
	err   error
	calls int
}

func (f *fakeInspector) Snapshot(context.Context) (*procs.Snapshot, error) {
	f.calls++
	return f.snap, f.err
}

// TestInUseStopsDeletion starts a process in .venv after the scan.
func TestInUseStopsDeletion(t *testing.T) {
	root := repoWith(t)
	roots, sizes := scanOne(t, root)
	venv := filepath.Join(root, ".venv")
	insp := &fakeInspector{snap: &procs.Snapshot{Processes: []procs.Process{
		{PID: 4312, Name: "python3.12", Files: []procs.File{{Use: procs.UseArgs, Path: filepath.Join(venv, "bin", "python")}}},
	}}}
	opts := quietOpts(true)
	opts.InUse = InUseChecker(context.Background(), insp, time.Now)
	res := Run(context.Background(), roots, sizes, opts)
	if res.Skipped != 1 || res.Failed != 0 || insp.calls != 1 {
		t.Fatalf("%d calls, result = %+v", insp.calls, res)
	}
	for _, it := range res.Items {
		switch it.Path {
		case venv:
			if it.Status != Skipped || it.Reason != "in use by python3.12 (PID 4312), whose command line names bin/python" {
				t.Errorf(".venv: %+v", it)
			}
		default:
			if it.Status != Deleted {
				t.Errorf("%s: %+v", it.Path, it)
			}
		}
	}
	if !exists(venv) || exists(filepath.Join(root, "node_modules")) {
		t.Errorf(".venv exists %v, node_modules exists %v", exists(venv), exists(filepath.Join(root, "node_modules")))
	}
}

func TestInUseErrorSkipsEveryCache(t *testing.T) {
	root := repoWith(t)
	roots, sizes := scanOne(t, root)
	opts := quietOpts(true)
	opts.InUse = InUseChecker(context.Background(), &fakeInspector{err: errors.New("lsof failed: exit status 1")}, time.Now)
	res := Run(context.Background(), roots, sizes, opts)
	if res.Skipped != 2 {
		t.Fatalf("result = %+v", res)
	}
	for _, it := range res.Items {
		if it.Reason != "cannot list processes: lsof failed: exit status 1" || !exists(it.Path) {
			t.Errorf("%+v, exists %v", it, exists(it.Path))
		}
	}
}

func TestInUseCheckerRefreshes(t *testing.T) {
	insp := &fakeInspector{snap: &procs.Snapshot{}}
	now := time.Date(2026, 11, 17, 9, 0, 0, 0, time.UTC)
	check := InUseChecker(context.Background(), insp, func() time.Time { return now })
	steps := []struct {
		after time.Duration
		calls int
	}{
		{0, 1},
		{5 * time.Second, 1},
		{5 * time.Second, 1}, // exactly SnapshotAge old
		{time.Second, 2},
		{9 * time.Second, 2},
	}
	for i, s := range steps {
		now = now.Add(s.after)
		if reason := check("/code/app/.venv"); reason != "" {
			t.Errorf("step %d: reason %q", i, reason)
		}
		if insp.calls != s.calls {
			t.Errorf("step %d: %d calls, want %d", i, insp.calls, s.calls)
		}
	}
	insp.err = errors.New("ps is not installed")
	now = now.Add(time.Minute)
	if reason := check("/code/app/.venv"); !strings.Contains(reason, "ps is not installed") {
		t.Errorf("reason after a failed snapshot: %q", reason)
	}
}
