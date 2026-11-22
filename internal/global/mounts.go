package global

import (
	"fmt"
	"path/filepath"
	"strings"
)

// mountsUnder returns the mount points at or below dir, by their paths.
// A bind mount can put a folder from the same device inside dir,
// so the device number alone does not show every mount point.
func (r Runner) mountsUnder(dir string) (map[string]bool, error) {
	list := mountPoints
	if r.mountsHook != nil {
		list = r.mountsHook
	}
	paths, err := list()
	if err != nil {
		return nil, err
	}
	out := map[string]bool{}
	for _, p := range paths {
		p = filepath.Clean(p)
		if p == dir || strings.HasPrefix(p, dir+string(filepath.Separator)) {
			out[p] = true
		}
	}
	return out, nil
}

// unescapeOctal replaces each backslash and three octal digits in s,
// as the kernel writes a space, tab, newline, or backslash, with its byte.
func unescapeOctal(s string) string {
	if !strings.Contains(s, `\`) {
		return s
	}
	var b strings.Builder
	for i := 0; i < len(s); i++ {
		if s[i] == '\\' && i+3 < len(s) && isOctal(s[i+1]) && isOctal(s[i+2]) && isOctal(s[i+3]) {
			b.WriteByte((s[i+1]-'0')<<6 | (s[i+2]-'0')<<3 | (s[i+3] - '0'))
			i += 3
			continue
		}
		b.WriteByte(s[i])
	}
	return b.String()
}

func isOctal(c byte) bool { return c >= '0' && c <= '7' }

// parseMountInfo returns the mount point of each line of a mountinfo file,
// as proc_pid_mountinfo(5) describes it: fields separated by ASCII spaces,
// whose fifth is the mount point. The kernel writes a space, tab, newline,
// or backslash in a path as an octal escape, such as \040, and every other
// byte as it is, so a path can hold other white space, such as U+00A0,
// which strings.Fields would split on.
func parseMountInfo(data string) ([]string, error) {
	var paths []string
	for line := range strings.SplitSeq(data, "\n") {
		if line == "" {
			continue
		}
		f := strings.Split(line, " ")
		if len(f) < 5 {
			return nil, fmt.Errorf("cannot read mountinfo line %q", line)
		}
		paths = append(paths, unescapeOctal(f[4]))
	}
	return paths, nil
}
