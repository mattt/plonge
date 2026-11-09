// Package disk reads free space and parses sizes such as "20GiB".
package disk

import (
	"fmt"
	"math"
	"strconv"
	"strings"
)

var units = map[string]float64{
	"":    1,
	"b":   1,
	"k":   1 << 10,
	"kib": 1 << 10,
	"kb":  1e3,
	"m":   1 << 20,
	"mib": 1 << 20,
	"mb":  1e6,
	"g":   1 << 30,
	"gib": 1 << 30,
	"gb":  1e9,
	"t":   1 << 40,
	"tib": 1 << 40,
	"tb":  1e12,
}

// ParseSize returns the bytes in a size such as "20GiB", "500MB", "1.5G", or "4096".
// KiB, MiB, GiB, TiB, and the bare letters K, M, G, T are powers of 1024;
// KB, MB, GB, and TB are powers of 1000.
func ParseSize(s string) (int64, error) {
	t := strings.TrimSpace(s)
	i := len(t)
	for i > 0 && (t[i-1] < '0' || t[i-1] > '9') {
		i--
	}
	if i == 0 {
		return 0, fmt.Errorf("invalid size %q", s)
	}
	n, err := strconv.ParseFloat(t[:i], 64)
	if err != nil || n < 0 || math.IsNaN(n) || math.IsInf(n, 0) {
		return 0, fmt.Errorf("invalid size %q", s)
	}
	unit, ok := units[strings.ToLower(strings.TrimSpace(t[i:]))]
	if !ok {
		return 0, fmt.Errorf("invalid size %q: unknown unit", s)
	}
	v := n * unit
	if v >= math.MaxInt64 {
		return 0, fmt.Errorf("invalid size %q: too large", s)
	}
	return int64(v), nil
}
