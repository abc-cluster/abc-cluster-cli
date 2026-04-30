package tools

// context.go — thin wrappers around internal/config that keep the rest of the
// tools package from depending on it directly for simple operations.

import (
	"github.com/abc-cluster/abc-cluster-cli/internal/config"
)

// loadConfig loads the abc config file and returns the active context.
// Exported as a package-level helper so list.go / status.go / fetch.go
// can share one call site.
func loadConfig() (config.Context, error) {
	cfg, err := config.Load()
	if err != nil {
		return config.Context{}, err
	}
	return cfg.ActiveCtx(), nil
}
