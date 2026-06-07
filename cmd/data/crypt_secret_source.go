package data

// crypt_secret_source.go — routes the crypt password/salt used by
// `abc data encrypt/decrypt` through the secret broker when the active context's
// secret_source is a broker tier (seedling/v1), so the password is portable
// across machines. `--unsafe-local` forces local-config storage for a run.
//
// Spec: specs/active/abc-user-secret-portability.md

import (
	"errors"
	"fmt"

	abccfg "github.com/abc-cluster/abc-cluster-cli/internal/config"
	"github.com/abc-cluster/abc-cluster-cli/internal/secretsource"
	"github.com/spf13/cobra"
)

// Broker keys for the crypt material (client-only secrets; no group).
const (
	cryptPasswordKey = "crypt-password"
	cryptSaltKey     = "crypt-salt"
)

// isBrokerSecretSource reports whether the active context routes user secrets
// through the broker (secret_source = a */v1 tier). Empty/"local" → false.
func isBrokerSecretSource(cfg *abccfg.Config) bool {
	ctx, ok := cfg.ContextNamed(cfg.ResolveContextName(cfg.ActiveContext))
	if !ok {
		return false
	}
	switch ctx.SecretSource {
	case "", "local":
		return false
	default:
		return true
	}
}

// resolveCryptViaBroker stores (when a password is provided) or fetches (when
// not) the crypt password+salt via the secret broker, mirroring the local
// path's "provided → persist; absent → reuse" contract. Returns the password +
// salt to use for this run.
func resolveCryptViaBroker(cmd *cobra.Command, cfg *abccfg.Config, providedPW, providedSalt string) (pw, salt string, err error) {
	ctx, ok := cfg.ContextNamed(cfg.ResolveContextName(cfg.ActiveContext))
	if !ok {
		return "", "", fmt.Errorf("no active context — run 'abc auth login' or 'abc auth claim' first")
	}
	cl, err := secretsource.NewClient(ctx)
	if err != nil {
		return "", "", err
	}
	c := cmd.Context()

	if providedPW != "" {
		// Store (idempotent rotate): the broker overwrites. Unlike the local
		// path we don't error on an existing value — passing a new
		// --crypt-password rotates the managed crypt password.
		if err := cl.Put(c, cryptPasswordKey, providedPW, ""); err != nil {
			return "", "", fmt.Errorf("store crypt password via broker: %w", err)
		}
		if providedSalt != "" {
			if err := cl.Put(c, cryptSaltKey, providedSalt, ""); err != nil {
				return "", "", fmt.Errorf("store crypt salt via broker: %w", err)
			}
		}
		fmt.Fprintln(cmd.ErrOrStderr(), "  [secrets] crypt password stored via broker (portable across machines)")
		return providedPW, providedSalt, nil
	}

	// No password provided → fetch the managed one.
	pw, err = cl.Get(c, cryptPasswordKey)
	if err != nil {
		if errors.Is(err, secretsource.ErrNotFound) {
			return "", "", fmt.Errorf(
				"no managed crypt password stored for this identity.\n" +
					"  Run once with --crypt-password <p> to store it via the broker;\n" +
					"  it is then available on any machine you log in from.")
		}
		return "", "", fmt.Errorf("fetch crypt password via broker: %w", err)
	}
	salt, err = cl.Get(c, cryptSaltKey)
	if err != nil {
		if errors.Is(err, secretsource.ErrNotFound) {
			salt = ""
		} else {
			return "", "", fmt.Errorf("fetch crypt salt via broker: %w", err)
		}
	}
	return pw, salt, nil
}
