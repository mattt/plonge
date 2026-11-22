package global

import (
	"os"
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
