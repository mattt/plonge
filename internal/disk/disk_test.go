package disk

import "testing"

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
