// Package cli implements the "abc admin services cli" command group.
//
// This is the inverted form of the per-service "abc admin services <tool> cli"
// pattern. Instead of:
//
//	abc admin services pulumi   cli -- up --yes
//	abc admin services terraform cli -- plan
//	abc admin services nomad    cli -- job status
//
// you write:
//
//	abc admin services cli pulumi    -- up --yes
//	abc admin services cli terraform -- plan
//	abc admin services cli nomad     -- job status
//
// Credentials are resolved from the active abc config context and injected as
// environment variables exactly as the per-service wrappers do. The underlying
// binary resolution, --binary-location support, and passthrough behaviour are
// identical.
//
// Persistent flags on this command (--nomad-addr, --nomad-token) override the
// config for services that accept Nomad credentials (terraform, pulumi, nomad,
// nomad-pack).
package cli

import (
	"github.com/abc-cluster/abc-cluster-cli/cmd/utils"
	"github.com/spf13/cobra"
)

// NewCmd returns the "cli" subcommand group.
func NewCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "cli",
		Short: "Run any service CLI tool with abc context credentials pre-loaded",
		Long: `Unified CLI passthrough: run any service binary with credentials from the
active abc config context injected as environment variables.

Usage: abc admin services cli <tool> [--binary-location <path>] [--] [tool-args...]

Available tools and the binaries they invoke:

  pulumi        pulumi        NOMAD_ADDR, NOMAD_TOKEN, MINIO_*, PULUMI_*
  terraform     terraform     NOMAD_ADDR, NOMAD_TOKEN, TF_VAR_*
  nomad         nomad         NOMAD_ADDR, NOMAD_TOKEN, NOMAD_NAMESPACE
  nomad-pack    nomad-pack    NOMAD_ADDR, NOMAD_TOKEN, NOMAD_NAMESPACE
  minio         mcli / mc     AWS_*, MINIO_ROOT_*
  vault         vault/bao     VAULT_ADDR, VAULT_TOKEN
  loki          logcli        LOKI_ADDR
  rustfs        rustfs        AWS_*, MINIO_ROOT_*
  rclone        rclone        (passthrough — no cred injection)
  boundary      boundary      (passthrough)
  consul        consul        (passthrough)
  eget          eget          (passthrough)
  nebula        nebula        (passthrough)
  ntfy          ntfy          (passthrough)
  grafana       grafana-cli   (passthrough)
  tailscale     tailscale     (passthrough)
  traefik       traefik       (passthrough)
  hashi-up      hashi-up      (passthrough)
  postgres      psql          (passthrough)

Examples:
  abc admin services cli pulumi    -- up --yes
  abc admin services cli terraform -- plan
  abc admin services cli nomad     -- job status -short
  abc admin services cli minio     -- ls local
  abc admin services cli vault     status

Override Nomad credentials for a single run:
  abc admin services cli --nomad-addr http://... --nomad-token <tok> pulumi -- up --yes`,
	}

	// Persistent Nomad credential flags — subcommands resolve these via cmd.Parent().
	cmd.PersistentFlags().String("nomad-addr", utils.EnvOrDefault("ABC_ADDR", "NOMAD_ADDR"),
		"Nomad API address (overrides config; or set ABC_ADDR/NOMAD_ADDR)")
	cmd.PersistentFlags().String("nomad-token", utils.EnvOrDefault("ABC_TOKEN", "NOMAD_TOKEN"),
		"Nomad ACL token (overrides config; or set ABC_TOKEN/NOMAD_TOKEN)")

	RegisterServices(cmd)

	return cmd
}
