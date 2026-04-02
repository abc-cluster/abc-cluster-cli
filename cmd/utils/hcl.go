package utils

import (
	"fmt"
	"sort"
	"strconv"
	"strings"
)

// SortedKeys returns the keys of m in sorted order.
func SortedKeys(m map[string]string) []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	return keys
}

// ParseMemoryMB converts a human memory string (4G, 512M, 8192K, or bare MB
// integer) to megabytes.
func ParseMemoryMB(s string) (int, error) {
	upper := strings.ToUpper(strings.TrimSpace(s))
	if upper == "" {
		return 0, fmt.Errorf("empty memory value")
	}
	switch {
	case strings.HasSuffix(upper, "G"):
		n, err := strconv.Atoi(upper[:len(upper)-1])
		if err != nil || n < 1 {
			return 0, fmt.Errorf("invalid memory value %q", s)
		}
		return n * 1024, nil
	case strings.HasSuffix(upper, "M"):
		n, err := strconv.Atoi(upper[:len(upper)-1])
		if err != nil || n < 1 {
			return 0, fmt.Errorf("invalid memory value %q", s)
		}
		return n, nil
	case strings.HasSuffix(upper, "K"):
		n, err := strconv.Atoi(upper[:len(upper)-1])
		if err != nil || n < 1 {
			return 0, fmt.Errorf("invalid memory value %q", s)
		}
		return (n + 1023) / 1024, nil
	default:
		n, err := strconv.Atoi(upper)
		if err != nil || n < 1 {
			return 0, fmt.Errorf("invalid memory value %q", s)
		}
		return n, nil
	}
}

// WalltimeToSeconds parses HH:MM:SS and returns total seconds.
func WalltimeToSeconds(t string) (int, error) {
	parts := strings.Split(t, ":")
	if len(parts) != 3 {
		return 0, fmt.Errorf("invalid --time value %q: expected HH:MM:SS", t)
	}
	h, err := strconv.Atoi(parts[0])
	if err != nil {
		return 0, fmt.Errorf("invalid --time value %q: %w", t, err)
	}
	m, err := strconv.Atoi(parts[1])
	if err != nil {
		return 0, fmt.Errorf("invalid --time value %q: %w", t, err)
	}
	sec, err := strconv.Atoi(parts[2])
	if err != nil {
		return 0, fmt.Errorf("invalid --time value %q: %w", t, err)
	}
	return h*3600 + m*60 + sec, nil
}
