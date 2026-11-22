package global

import (
	"slices"
	"testing"
)

func TestParseMountInfo(t *testing.T) {
	got, err := parseMountInfo("22 1 8:1 / / rw,relatime shared:1 - ext4 /dev/sda1 rw\n" +
		"40 22 8:1 /home/u/src /tmp/a\\040b rw - ext4 /dev/sda1 rw\n")
	if err != nil {
		t.Fatal(err)
	}
	if want := []string{"/", "/tmp/a b"}; !slices.Equal(got, want) {
		t.Errorf("mount points = %q, want %q", got, want)
	}
	if _, err := parseMountInfo("short line\n"); err == nil {
		t.Error("a short line parsed")
	}
}
