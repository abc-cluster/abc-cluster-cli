package debuglog

import (
	"os"
	"path/filepath"
	"runtime"
	"sort"
	"strings"
)

// LogDir returns the platform-appropriate directory that abc debug log files
// are written to. Exported for the support-bundle assembler, which needs to
// locate the most recent debug-<ts>.log to include in a bundle.
func LogDir() string {
	return logDir()
}

// LatestLogPath returns the path of the most recently written debug-<ts>.log in
// LogDir(), or an empty string (and nil error) when none exist. Debug log files
// are named debug-<RFC3339-ish-ts>.log, so a lexical sort on the filename is a
// chronological sort. Used by `abc doctor --bundle` in passive mode (grab the
// trace the user just produced with --debug) and after a --rerun.
func LatestLogPath() (string, error) {
	dir := logDir()
	entries, err := os.ReadDir(dir)
	if err != nil {
		if os.IsNotExist(err) {
			return "", nil
		}
		return "", err
	}
	var names []string
	for _, e := range entries {
		if e.IsDir() {
			continue
		}
		n := e.Name()
		if strings.HasPrefix(n, "debug-") && strings.HasSuffix(n, ".log") {
			names = append(names, n)
		}
	}
	if len(names) == 0 {
		return "", nil
	}
	sort.Strings(names)
	return filepath.Join(dir, names[len(names)-1]), nil
}

// logDir returns the platform-appropriate directory for abc debug log files.
//
//   - macOS  → ~/Library/Logs/abc-cluster-cli
//   - Linux  → $XDG_STATE_HOME/abc-cluster-cli/logs  (falls back to ~/.local/share/…)
//   - other  → ~/.abc/logs
func logDir() string {
	home, _ := os.UserHomeDir()

	switch runtime.GOOS {
	case "darwin":
		if home != "" {
			return filepath.Join(home, "Library", "Logs", "abc-cluster-cli")
		}
	case "linux":
		if xdg := os.Getenv("XDG_STATE_HOME"); xdg != "" {
			return filepath.Join(xdg, "abc-cluster-cli", "logs")
		}
		if home != "" {
			return filepath.Join(home, ".local", "share", "abc-cluster-cli", "logs")
		}
	}

	// Fallback for Windows or when home dir is unavailable.
	if home != "" {
		return filepath.Join(home, ".abc", "logs")
	}
	// Last resort: current working directory.
	return ".abc-logs"
}
