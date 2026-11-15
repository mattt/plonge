package disk

import (
	"errors"
	"testing"
)

func TestParseSize(t *testing.T) {
	tests := []struct {
		in   string
		want int64
		bad  bool
	}{
		{in: "4096", want: 4096},
		{in: "0", want: 0},
		{in: "20GiB", want: 20 << 30},
		{in: "20 gib", want: 20 << 30},
		{in: "1.5G", want: 3 << 29},
		{in: "500MB", want: 500_000_000},
		{in: "2k", want: 2048},
		{in: "1TiB", want: 1 << 40},
		{in: "", bad: true},
		{in: "GiB", bad: true},
		{in: "-1G", bad: true},
		{in: "1XB", bad: true},
		{in: "1.2.3G", bad: true},
		{in: "99999999999TB", bad: true},
	}
	for _, tt := range tests {
		got, err := ParseSize(tt.in)
		if (err != nil) != tt.bad || got != tt.want {
			t.Errorf("ParseSize(%q) = %d, %v", tt.in, got, err)
		}
	}
}

func TestFree(t *testing.T) {
	got, err := Free(t.TempDir())
	if err != nil {
		t.Skip(err)
	}
	if got <= 0 {
		t.Errorf("Free = %d", got)
	}
	if _, err := Free("/nonexistent/plonge-test"); err == nil {
		t.Error("Free on a missing path gave no error")
	}
}

func TestTrack(t *testing.T) {
	defer func(f func(string) (int64, error)) { free = f }(free)
	readings := []int64{100, 160}
	var paths []string
	free = func(path string) (int64, error) {
		paths = append(paths, path)
		n := readings[0]
		readings = readings[1:]
		return n, nil
	}
	ran := false
	got := Track("/vol", func() {
		if len(paths) != 1 {
			t.Errorf("fn ran after %d readings, want 1", len(paths))
		}
		ran = true
	})
	want := Change{FreeBefore: 100, FreeAfter: 160, GainedBytes: 60}
	if !ran || got != want || len(paths) != 2 || paths[0] != "/vol" || paths[1] != "/vol" {
		t.Errorf("Track = %+v, ran %v, paths %q; want %+v", got, ran, paths, want)
	}

	free = func(string) (int64, error) { return 0, errors.New("no volume") }
	ran = false
	got = Track("/vol", func() { ran = true })
	if !ran || got != (Change{Error: "no volume"}) {
		t.Errorf("Track with a failing volume = %+v, ran %v", got, ran)
	}
}
