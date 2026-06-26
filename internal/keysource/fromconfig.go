package keysource

import (
	"context"
	"fmt"

	abccfg "github.com/abc-cluster/abc-cluster-cli/internal/config"
)

// FromActiveContext builds a KEK Provider by loading ~/.abc/config.yaml and
// binding to its active context. This is the self-configuring entry point used
// by age-plugin-abc (so `age -j abc` / `age -R recipients.txt` work with no
// identity string — the plugin reads the same config the abc CLI does).
//
// reqCtx is the request context threaded into the broker HTTP calls.
func FromActiveContext(reqCtx context.Context) (*Provider, error) {
	cfg, err := abccfg.Load()
	if err != nil {
		return nil, fmt.Errorf("load ~/.abc/config.yaml: %w", err)
	}
	cctx, ok := cfg.ContextNamed(cfg.ResolveContextName(cfg.ActiveContext))
	if !ok {
		return nil, fmt.Errorf("no active context — run 'abc auth login' or claim a slot first")
	}
	cl, err := NewClient(cctx)
	if err != nil {
		return nil, err
	}
	return NewProvider(reqCtx, cl), nil
}
