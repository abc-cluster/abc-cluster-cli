package secrets

// broker_backend.go — the `broker` (seedling/v1) backend of `abc secrets`.
//
// Unlike the `nomad` backend (which needs the caller's own Nomad write ACL),
// the broker backend goes through abc-auth-svc: the broker's management token
// writes/reads a Nomad Variable on the user's behalf, so an ordinary user with
// no ACL can store + fetch their own secret portably (same identity, any
// machine). Selected by `--backend broker` or, by default, by a context whose
// secret_source is a `*/v1` tier.
//
// Spec: specs/active/abc-user-secret-portability.md

import (
	"errors"
	"fmt"

	"github.com/abc-cluster/abc-cluster-cli/internal/config"
	"github.com/abc-cluster/abc-cluster-cli/internal/secretsource"
	"github.com/spf13/cobra"
)

func brokerClientFor() (*secretsource.Client, error) {
	cfg, err := config.Load()
	if err != nil {
		return nil, fmt.Errorf("load config: %w", err)
	}
	ctx, ok := cfg.ContextNamed(cfg.ResolveContextName(cfg.ActiveContext))
	if !ok {
		return nil, fmt.Errorf("no active context — run 'abc auth login' or 'abc auth claim' first")
	}
	return secretsource.NewClient(ctx)
}

func runBrokerSet(cmd *cobra.Command, name, value string) error {
	cl, err := brokerClientFor()
	if err != nil {
		return err
	}
	// The existing --namespace flag carries the (optional) job-runtime group:
	// when set, the broker writes into that namespace so a job's workload
	// identity can later read the Variable natively. Empty = client-only secret.
	group, _ := cmd.Flags().GetString("namespace")
	if err := cl.Put(cmd.Context(), name, value, group); err != nil {
		return fmt.Errorf("store secret via broker: %w", err)
	}
	where := "broker (seedling/v1)"
	if group != "" {
		where += " · group " + group
	}
	fmt.Fprintf(cmd.OutOrStdout(), "✓ stored %q via %s\n", name, where)
	return nil
}

func runBrokerGet(cmd *cobra.Command, name string) error {
	cl, err := brokerClientFor()
	if err != nil {
		return err
	}
	v, err := cl.Get(cmd.Context(), name)
	if err != nil {
		if errors.Is(err, secretsource.ErrNotFound) {
			return fmt.Errorf("no secret named %q in the broker store", name)
		}
		return fmt.Errorf("get secret via broker: %w", err)
	}
	fmt.Fprintln(cmd.OutOrStdout(), v)
	return nil
}

func runBrokerList(cmd *cobra.Command) error {
	// Listing requires a /secrets/list broker endpoint, which is out of this
	// spec slice (the endpoints shipped are /secrets/get + /secrets/put). Fail
	// with guidance rather than pretend.
	return fmt.Errorf("listing broker secrets is not supported yet — fetch a known key with 'abc secrets get <key> --backend broker'")
}
