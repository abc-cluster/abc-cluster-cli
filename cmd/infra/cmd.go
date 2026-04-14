// Package infra implements the "abc infra" command group.
//
// infra groups node and storage operations under a single surface.
// The old top-level "abc node" and "abc storage" names remain available
// as deprecated aliases that forward to the same implementations.
package infra

import (
	"github.com/abc-cluster/abc-cluster-cli/cmd/node"
	"github.com/abc-cluster/abc-cluster-cli/cmd/storage"
	"github.com/spf13/cobra"
)

// NewCmd returns the "infra" subcommand group.
func NewCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "infra",
		Short: "Manage infrastructure: nodes and storage",
		Long: `Commands for managing cluster infrastructure.

		  abc infra node add --remote <ip>        Add a compute node
  abc infra node list                   List registered nodes
  abc infra node show <id>              Show node details
  abc infra node drain <id>             Drain a node
  abc infra node undrain <id>           Undrain a node
  abc infra storage size                Show storage usage`,
	}

	cmd.AddCommand(node.NewCmd())
	cmd.AddCommand(storage.NewCmd())
	return cmd
}
