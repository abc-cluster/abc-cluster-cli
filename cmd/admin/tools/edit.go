package tools

import (
	"fmt"
	"os"
	"os/exec"

	"github.com/spf13/cobra"
)

func newEditCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "edit",
		Short: "Open tools.toml in $EDITOR",
		Long: `Open ~/.abc/binaries/tools.toml in $EDITOR.

If tools.toml does not exist yet, it is created from the bundled default first
(equivalent to running 'abc admin tools init').

The EDITOR environment variable is consulted; falls back to vi.`,
		RunE: func(cmd *cobra.Command, args []string) error {
			return runEdit(cmd)
		},
	}
}

func runEdit(cmd *cobra.Command) error {
	path, err := toolsConfigPath()
	if err != nil {
		return err
	}

	// Bootstrap tools.toml if missing.
	if _, err := os.Stat(path); os.IsNotExist(err) {
		fmt.Fprintf(cmd.OutOrStdout(), "[abc] tools.toml not found; creating from bundled default...\n")
		if err := runInit(cmd, false, false); err != nil {
			return err
		}
	}

	editor := os.Getenv("EDITOR")
	if editor == "" {
		editor = os.Getenv("VISUAL")
	}
	if editor == "" {
		editor = "vi"
	}

	editorCmd := exec.Command(editor, path)
	editorCmd.Stdin = os.Stdin
	editorCmd.Stdout = os.Stdout
	editorCmd.Stderr = os.Stderr
	return editorCmd.Run()
}
