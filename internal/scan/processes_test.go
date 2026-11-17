package scan

import (
	"context"
	"errors"
	"reflect"
	"testing"

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

func TestRefuseUsed(t *testing.T) {
	roots := []Root{
		{Path: "/code/app", Candidates: []Candidate{
			{Path: "/code/app/.venv", Rule: ".venv"},
			{Path: "/code/app/.venv2", Rule: ".venv"},
		}},
		{Path: "/code/gone", Skipped: "not a directory"},
	}
	insp := &fakeInspector{snap: &procs.Snapshot{Processes: []procs.Process{
		{PID: 4312, Name: "python3.12", User: "u", Files: []procs.File{
			{Use: procs.UseArgs, Path: "/code/app/.venv/bin/python"},
		}},
	}}}
	src := RefuseUsed(context.Background(), roots, insp)
	if src != (Source{Name: SourceProcesses, Status: SourceUsed}) || insp.calls != 1 {
		t.Errorf("source = %+v, %d calls", src, insp.calls)
	}
	want := []Refusal{{
		Path: "/code/app/.venv", Project: "/code/app", Rule: ".venv",
		Reason:    "in use by python3.12 (PID 4312), whose command line names bin/python",
		Processes: []procs.Match{{PID: 4312, Name: "python3.12", User: "u", Use: procs.UseArgs, Path: "/code/app/.venv/bin/python"}},
	}}
	if !reflect.DeepEqual(roots[0].Refused, want) {
		t.Errorf("refused = %+v", roots[0].Refused)
	}
	if len(roots[0].Candidates) != 1 || roots[0].Candidates[0].Path != "/code/app/.venv2" || roots[0].Busy {
		t.Errorf("root = %+v", roots[0])
	}
}

func TestRefuseUsedFailsClosed(t *testing.T) {
	roots := []Root{
		{Path: "/code/app", Candidates: []Candidate{{Path: "/code/app/.venv"}}},
		{Path: "/code/gone", Skipped: "not a directory"},
	}
	src := RefuseUsed(context.Background(), roots, &fakeInspector{err: errors.New("lsof is not installed")})
	if src != (Source{Name: SourceProcesses, Status: SourceFailed, Reason: "lsof is not installed"}) {
		t.Errorf("source = %+v", src)
	}
	if !roots[0].Busy || roots[1].Busy || len(roots[0].Candidates) != 1 {
		t.Errorf("roots = %+v", roots)
	}
}
