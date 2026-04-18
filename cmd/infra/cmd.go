// Package infra implements the "abc infra" command group.
//
// infra groups compute and storage operations under a single surface.
// The old top-level "abc node" and "abc storage" names remain available
// as deprecated aliases that forward to the same implementations.
package infra

import (
	"github.com/abc-cluster/abc-cluster-cli/cmd/compute"
	"github.com/abc-cluster/abc-cluster-cli/cmd/storage"
	"github.com/spf13/cobra"
)

// NewCmd returns the "infra" subcommand group.
func NewCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "infra",
		Short: "Manage infrastructure: compute and storage",
		Long: `Commands for managing cluster infrastructure.

  abc infra compute add --remote <ip>        Add a compute resource
  abc infra compute list                     List registered compute resources
  abc infra compute show <id>                Show compute resource details
  abc infra compute node debug --remote <h>  Linux Nomad bridge/CNI diagnostics over SSH
  abc infra storage size                     Show storage usage

Compute resource drain/undrain operations are available under:
  abc admin services nomad node drain <id>
  abc admin services nomad node undrain <id>`,
	}

	cmd.AddCommand(compute.NewCmd())
	cmd.AddCommand(storage.NewCmd())
	return cmd
}
