package debuglog

import (
	"os"
	"path/filepath"
	"runtime"
)

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
