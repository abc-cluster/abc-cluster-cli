package data

import (
	"fmt"

	"github.com/abc-cluster/abc-cluster-cli/internal/config"
)

func loadOrCreateConfig() (*config.Config, error) {
	cfg, err := config.Load()
	if err != nil {
		return nil, fmt.Errorf("failed to load config: %w", err)
	}
	// Ensure defaults are initialized
	if cfg.Defaults == (config.Defaults{}) {
		cfg.Defaults = config.Defaults{}
	}
	return cfg, nil
}
