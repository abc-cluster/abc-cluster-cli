package workbench

import (
	"fmt"

	"github.com/abc-cluster/abc-cluster-cli/internal/config"
	"github.com/abc-cluster/abc-cluster-cli/internal/state"
	wbinternal "github.com/abc-cluster/abc-cluster-cli/internal/workbench"
	"github.com/spf13/cobra"
)

func newImportHistoryCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "import-history <slot>",
		Short: "Import atuin shell history for a workbench slot",
		Long: `Read the atuin history.db for a workbench slot from the platform node via SSH
and bulk-insert any new rows into the local state.db shell_history table.

Idempotent — rows already present (by atuin_id) are silently skipped.
Returns (0, 0, nil) when the atuin DB does not exist (user never opened a terminal).

  abc admin services workbench import-history calm-dassie
  abc admin services workbench import-history calm-dassie --node sun-aither`,
		Args: cobra.ExactArgs(1),
		RunE: runImportHistory,
	}
	cmd.Flags().String("node", "", "SSH host alias for the platform node (overrides config; default: sun-<node> from config or sun-aither)")
	return cmd
}

func runImportHistory(cmd *cobra.Command, args []string) error {
	slot := args[0]

	cfg, err := config.Load()
	if err != nil {
		return fmt.Errorf("load config: %w", err)
	}
	actx := cfg.ActiveCtx()

	nodeOverride, _ := cmd.Flags().GetString("node")
	node := nodeFromCtx(actx, nodeOverride)

	db, err := state.Open()
	if err != nil {
		return fmt.Errorf("open local db: %w", err)
	}
	defer db.Close()

	fmt.Fprintf(cmd.ErrOrStderr(), "Reading atuin history for slot %q from %s...\n", slot, node.Host)

	rows, err := wbinternal.ReadAtuinFromNode(node, slot)
	if err != nil {
		return fmt.Errorf("read atuin history: %w", err)
	}
	if len(rows) == 0 {
		fmt.Fprintf(cmd.OutOrStdout(), "No atuin history found for slot %q (user may not have opened a terminal yet).\n", slot)
		return nil
	}

	already, err := state.ImportedAtuinIDs(db, slot)
	if err != nil {
		return fmt.Errorf("query imported IDs: %w", err)
	}

	cmds, skipped := wbinternal.AtuinRowsToCommands(rows, already, slot)

	inserted, err := state.InsertShellCommands(db, cmds)
	if err != nil {
		return fmt.Errorf("insert shell commands: %w", err)
	}

	fmt.Fprintf(cmd.OutOrStdout(),
		"Slot %q: %d rows in atuin DB — %d inserted, %d already present.\n",
		slot, len(rows), inserted, skipped,
	)
	return nil
}
