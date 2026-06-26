package data

import (
	"fmt"

	"github.com/spf13/cobra"

	abccfg "github.com/abc-cluster/abc-cluster-cli/internal/config"
	"github.com/abc-cluster/abc-cluster-cli/internal/keysource"
)

// newKEKProvider builds a managed-KEK provider (abccrypt.KEKProvider) for the
// active context, backed by the broker's POST /keys/get. It performs NO network
// call by itself — the provider fetches (and caches) the group KEK lazily on the
// first Wrap/Unwrap, so adding it as a decrypt identity is free unless the file
// actually carries an abc stanza.
//
// Used by the managed-encryption default: encrypt names the abc recipient with
// the caller's own kek_id (via prov.OwnKekID); decrypt offers the abc identity
// so managed files open with no passphrase.
func newKEKProvider(cmd *cobra.Command, cfg *abccfg.Config) (*keysource.Provider, error) {
	ctx, ok := cfg.ContextNamed(cfg.ResolveContextName(cfg.ActiveContext))
	if !ok {
		return nil, fmt.Errorf("no active context — run 'abc auth login' or claim a slot first")
	}
	cl, err := keysource.NewClient(ctx)
	if err != nil {
		return nil, err
	}
	return keysource.NewProvider(cmd.Context(), cl), nil
}
