package global

import (
	"fmt"
	"os"
	"strings"
)

// mountPoints returns the paths where file systems are mounted,
// including bind mounts, from /proc/self/mountinfo.
func mountPoints() ([]string, error) {
	data, err := os.ReadFile("/proc/self/mountinfo")
	if err != nil {
		return nil, err
	}
	return parseMountInfo(string(data))
}

// parseMountInfo returns the mount point of each line of a mountinfo file,
// whose fifth field is the mount point, with octal escapes such as \040.
func parseMountInfo(data string) ([]string, error) {
	var paths []string
	for line := range strings.SplitSeq(data, "\n") {
		if line == "" {
			continue
		}
		f := strings.Fields(line)
		if len(f) < 5 {
			return nil, fmt.Errorf("cannot read mountinfo line %q", line)
		}
		paths = append(paths, unescapeOctal(f[4]))
	}
	return paths, nil
}
