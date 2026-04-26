// Package terraform implements the "abc admin services terraform" command group.
//
// The cli subcommand is a passthrough to the local terraform binary with Nomad
// credentials from the active abc config context auto-injected as TF_VAR_*
// environment variables, so terraform plan/apply picks them up without a
// tfvars file or manual export.
//
// Usage:
//
//	abc admin services terraform cli -- --version
//	abc admin services terraform cli -- init
//	abc admin services terraform cli -- plan
//	abc admin services terraform cli -- apply -auto-approve
//	abc admin services terraform cli -- output -json
//	abc admin services terraform cli --nomad-addr http://... -- plan
package terraform

import (
	"github.com/abc-cluster/abc-cluster-cli/cmd/utils"
	"github.com/spf13/cobra"
)

// NewCmd returns the "terraform" subcommand group.
func NewCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "terraform",
		Short: "Terraform CLI passthrough with abc-nodes credential injection",
		Long: `Commands for running Terraform against the abc-nodes deployment.

Nomad credentials (address, token, region) are resolved from the active abc
config context and injected as TF_VAR_* environment variables so you never
need to export them manually or maintain a separate tfvars file.

  abc admin services terraform cli -- --version
  abc admin services terraform cli -- init
  abc admin services terraform cli -- plan
  abc admin services terraform cli -- apply -auto-approve
  abc admin services terraform cli -- output -json

Override credentials for a single invocation via persistent flags:
  abc admin services terraform cli --nomad-addr http://100.70.185.46:4646 -- plan`,
	}

	// Persistent Nomad connection flags — mirror the nomad command group so
	// users can override credentials on the command line when needed.
	cmd.PersistentFlags().String("nomad-addr", utils.EnvOrDefault("ABC_ADDR", "NOMAD_ADDR"),
		"Nomad API address injected as TF_VAR_nomad_address (or set ABC_ADDR/NOMAD_ADDR)")
	cmd.PersistentFlags().String("nomad-token", utils.EnvOrDefault("ABC_TOKEN", "NOMAD_TOKEN"),
		"Nomad ACL token injected as TF_VAR_nomad_token (or set ABC_TOKEN/NOMAD_TOKEN)")
	cmd.PersistentFlags().String("nomad-region", utils.EnvOrDefault("ABC_REGION", "NOMAD_REGION"),
		"Nomad region injected as TF_VAR_nomad_region (or set ABC_REGION/NOMAD_REGION)")

	cmd.AddCommand(newCLICmd())

	return cmd
}
