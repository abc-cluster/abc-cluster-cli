package state

import (
	"github.com/abc-cluster/abc-cluster-cli/internal/config"
)

// ActiveContextName returns the active CLI context name, falling back to
// "default" when none is configured. It never errors so commands can call it
// without a Load step in tight paths; callers needing fresh config should
// load explicitly.
func ActiveContextName() string {
	cfg, err := config.Load()
	if err != nil || cfg == nil {
		return "default"
	}
	if cfg.ActiveContext == "" {
		return "default"
	}
	return cfg.ActiveContext
}
