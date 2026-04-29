package job

import (
	"fmt"
	"strconv"
	"strings"
	"time"

	"github.com/abc-cluster/abc-cluster-cli/cmd/utils"
)

func parseMemoryMB(s string) (int, error)     { return utils.ParseMemoryMB(s) }
func walltimeToSeconds(t string) (int, error) { return utils.WalltimeToSeconds(t) }

// parseSleepDuration parses a human-readable duration into whole seconds.
// Accepts: bare integer ("120"), Go duration strings ("5m", "90s", "1h30m"),
// and HH:MM:SS walltime format ("00:02:00").
func parseSleepDuration(s string) (int, error) {
	s = strings.TrimSpace(s)
	if s == "" {
		return 0, fmt.Errorf("empty duration")
	}
	// Bare integer → seconds.
	if n, err := strconv.Atoi(s); err == nil {
		if n < 0 {
			return 0, fmt.Errorf("sleep duration must be positive, got %d", n)
		}
		return n, nil
	}
	// HH:MM:SS walltime format.
	if strings.Count(s, ":") == 2 {
		secs, err := walltimeToSeconds(s)
		if err == nil {
			return secs, nil
		}
	}
	// Go duration string (5m, 90s, 1h, etc.).
	d, err := time.ParseDuration(s)
	if err != nil {
		return 0, fmt.Errorf("cannot parse %q as a duration (try e.g. 60, 90s, 5m, 1h)", s)
	}
	secs := int(d.Seconds())
	if secs < 0 {
		return 0, fmt.Errorf("sleep duration must be positive, got %v", d)
	}
	return secs, nil
}
