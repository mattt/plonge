package global

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/mattt/plonge/internal/rules"
)

// zigEnv is output in the form that "zig env" prints in Zig 0.17.1,
// with the global cache directory set to dir.
func zigEnv(dir string) string {
	return `.{
    .zig_exe = "/opt/zig/zig",
    .lib_dir = "/opt/zig/lib",
    .std_dir = "/opt/zig/lib/std",
    .global_cache_dir = "` + dir + `",
    .version = "0.17.1",
    .target = "aarch64-macos.15.0...15.0-none",
    .env = .{
        .ZIG_GLOBAL_CACHE_DIR = null,
        .ZIG_LOCAL_CACHE_DIR = null,
        .HOME = "/home/user",
    },
}
`
}

func TestZONField(t *testing.T) {
	cases := []struct {
		src, want string
	}{
		{zigEnv(".cache/zig"), ".cache/zig"},
		{zigEnv(`/a \"b\"\\c\x41\u{e9}\td`), "/a \"b\"\\cA\u00e9\td"},
		{`.{.global_cache_dir="/x"}`, "/x"},
		// A nested field of the same name and odd values before it do not count.
		{`.{ .a = .{ .global_cache_dir = "/nested" }, .b = '}', .c = 1, // comment }
		.@"q d" = "}", .global_cache_dir = "/top" }`, "/top"},
	}
	for _, c := range cases {
		got, err := zonField(c.src, "global_cache_dir")
		if err != nil || got != c.want {
			t.Errorf("zonField(%q) = %q, %v; want %q", c.src, got, err, c.want)
		}
	}
}

func TestZONFieldErrors(t *testing.T) {
	cases := map[string]string{
		`{"global_cache_dir": "/x"}`:                "not a ZON struct",
		"/home/user/.cache/zig\n":                   "not a ZON struct",
		`.{ .version = "0.17.1" }`:                  "no global_cache_dir field",
		`.{ .env = .{ .global_cache_dir = "/x" } }`: "no global_cache_dir field",
		`.{ .global_cache_dir = null }`:             "is not a string",
		`.{ .global_cache_dir = "/x`:                "not closed",
		`.{ .global_cache_dir = "/x\q" }`:           `unknown escape`,
		`.{ .global_cache_dir = "/x\xZZ" }`:         `two hex digits`,
		`.{ .a = .{ .b = 1 }`:                       "not closed",
	}
	for src, want := range cases {
		_, err := zonField(src, "global_cache_dir")
		if err == nil || !strings.Contains(err.Error(), want) {
			t.Errorf("zonField(%q): err = %v, want %q", src, err, want)
		}
	}
}

func TestPathFromZONField(t *testing.T) {
	bin := fakeTools(t)
	r := runner(t)
	cache := filepath.Join(r.Home, ".cache", "zig")
	if err := os.MkdirAll(cache, 0o755); err != nil {
		t.Fatal(err)
	}
	abs := cacheDir(t, 10)
	out := t.TempDir()
	for tool, text := range map[string]string{
		"relzig":   zigEnv(".cache/zig"),
		"abszig":   zigEnv(abs),
		"emptyzig": zigEnv(""),
		"oldzig":   `{"global_cache_dir": "/x"}` + "\n",
	} {
		file := filepath.Join(out, tool)
		if err := os.WriteFile(file, []byte(text), 0o644); err != nil {
			t.Fatal(err)
		}
		addTool(t, bin, tool, fmt.Sprintf(`[ "$*" = env ] && /bin/cat %q`, file))
	}
	rule := func(tool string) rules.Global {
		return rules.Global{ID: tool, Tool: tool, Path: tool + " env", PathZONField: "global_cache_dir",
			Kind: rules.KindClear, DeleteContents: true}
	}
	entries := r.List(context.Background(), []rules.Global{
		rule("relzig"), rule("abszig"), rule("emptyzig"), rule("oldzig"),
	})
	if e := entries[0]; e.Path != cache {
		t.Errorf("relative: %+v", e)
	}
	if e := entries[1]; e.Path != abs {
		t.Errorf("absolute: %+v", e)
	}
	if e := entries[2]; !strings.Contains(e.Skipped, "printed an empty global_cache_dir") {
		t.Errorf("empty: %+v", e)
	}
	if e := entries[3]; !strings.Contains(e.Skipped, "path command failed: output is not a ZON struct") {
		t.Errorf("JSON: %+v", e)
	}
}
