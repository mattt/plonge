package main

import (
	"bytes"
	"runtime/debug"
	"strings"
	"testing"
)

func TestVersionCommand(t *testing.T) {
	old := version
	version = "1.2.3"
	t.Cleanup(func() { version = old })

	for _, args := range [][]string{{"--version"}, {"-version"}, {"version"}} {
		var stdout, stderr bytes.Buffer
		if code := run(args, &stdout, &stderr); code != 0 {
			t.Errorf("run(%q) = %d, stderr %q", args, code, stderr.String())
		}
		if got := stdout.String(); got != "1.2.3\n" {
			t.Errorf("run(%q) stdout = %q, want %q", args, got, "1.2.3\n")
		}
	}
}

func TestVersionUsage(t *testing.T) {
	var stdout, stderr bytes.Buffer
	if code := run([]string{"--help"}, &stdout, &stderr); code != 0 {
		t.Fatalf("run(--help) = %d", code)
	}
	if !strings.Contains(stdout.String(), "version") {
		t.Errorf("help does not list version:\n%s", stdout.String())
	}
	stdout.Reset()
	if code := run([]string{"version", "--help"}, &stdout, &stderr); code != 0 {
		t.Errorf("run(version --help) = %d", code)
	}
	if !strings.Contains(stdout.String(), "Usage: plonge version") {
		t.Errorf("version --help stdout = %q", stdout.String())
	}
	if code := run([]string{"version", "extra"}, &stdout, &stderr); code != 2 {
		t.Errorf("run(version extra) = %d, want 2", code)
	}
}

func TestResolveVersion(t *testing.T) {
	tests := []struct {
		name     string
		injected string
		module   string
		ok       bool
		want     string
	}{
		{"injected wins", "1.2.3", "v9.9.9", true, "1.2.3"},
		{"module version", "", "v1.2.3", true, "v1.2.3"},
		{"devel build", "", "(devel)", true, "dev"},
		{"empty module version", "", "", true, "dev"},
		{"no build info", "", "", false, "dev"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := resolveVersion(tt.injected, func() (*debug.BuildInfo, bool) {
				if !tt.ok {
					return nil, false
				}
				return &debug.BuildInfo{Main: debug.Module{Version: tt.module}}, true
			})
			if got != tt.want {
				t.Errorf("resolveVersion() = %q, want %q", got, tt.want)
			}
		})
	}
}
