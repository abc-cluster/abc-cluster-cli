// Package pulumi implements the "abc admin services pulumi" command group.
//
// pulumi is a passthrough to the local pulumi binary with Nomad and MinIO
// credentials from the active abc config context auto-injected as environment
// variables, so pulumi up / destroy picks them up without manual export.
//
// Credentials injected (from active context, lower priority than any existing
// env vars so operators can always override):
//
//	NOMAD_ADDR          — from admin.services.nomad.nomad_addr
//	NOMAD_TOKEN         — from admin.services.nomad.nomad_token
//	MINIO_SERVER        — from admin.services.minio.cred_source.local.endpoint (host:port)
//	MINIO_USER          — from admin.services.minio.cred_source.local.user
//	MINIO_PASSWORD      — from admin.services.minio.cred_source.local.password
//	PULUMI_ACCESS_TOKEN — from admin.services.pulumi.access_token
//	PULUMI_CONFIG_PASSPHRASE — from admin.services.pulumi.config_passphrase
//
// The working directory is changed to admin.services.pulumi.deploy_dir before
// exec, and --stack defaults to admin.services.pulumi.stack when unset.
//
// Usage:
//
//	abc admin services pulumi cli -- --version
//	abc admin services pulumi cli -- stack select prod
//	abc admin services pulumi cli -- up --yes
//	abc admin services pulumi cli -- destroy --yes
//	abc admin services pulumi cli --nomad-addr http://... -- up --yes
package pulumi

import (
	"github.com/abc-cluster/abc-cluster-cli/cmd/utils"
	"github.com/spf13/cobra"
)

// NewCmd returns the "pulumi" subcommand group.
func NewCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "pulumi",
		Short: "Pulumi CLI passthrough with abc context credentials pre-loaded",
		Long: `Commands for running Pulumi against the abc-nodes deployment.

Nomad and MinIO credentials from the active abc config context are injected as
environment variables so that pulumi up / destroy picks them up without manual
export or editing Pulumi.<stack>.yaml.

  abc admin services pulumi cli -- --version
  abc admin services pulumi cli -- stack select prod
  abc admin services pulumi cli -- up --yes
  abc admin services pulumi cli -- destroy --yes
  abc admin services pulumi cli -- stack output

Override credentials for a single invocation via persistent flags:
  abc admin services pulumi cli --nomad-addr http://... --nomad-token <tok> -- up --yes`,
	}

	// Persistent Nomad connection flags — mirror the nomad command group so
	// users can override credentials on the command line when needed.
	cmd.PersistentFlags().String("nomad-addr", utils.EnvOrDefault("ABC_ADDR", "NOMAD_ADDR"),
		"Nomad API address injected as NOMAD_ADDR (or set ABC_ADDR/NOMAD_ADDR)")
	cmd.PersistentFlags().String("nomad-token", utils.EnvOrDefault("ABC_TOKEN", "NOMAD_TOKEN"),
		"Nomad ACL token injected as NOMAD_TOKEN (or set ABC_TOKEN/NOMAD_TOKEN)")

	cmd.AddCommand(newCLICmd())

	return cmd
}
