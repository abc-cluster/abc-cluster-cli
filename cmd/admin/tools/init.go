package tools

import (
	"fmt"
	"os"

	"github.com/spf13/cobra"
)

func newInitCmd() *cobra.Command {
	var reset bool
	var show bool

	cmd := &cobra.Command{
		Use:   "init",
		Short: "Create ~/.abc/assets/tools.toml from the bundled default",
		Long: `Write the bundled default tools.toml into ~/.abc/assets/.

If tools.toml already exists:
  --reset   Overwrite with the bundled default (saves existing as tools.toml.bak)
  --show    Print the bundled default to stdout without writing anything`,
		RunE: func(cmd *cobra.Command, args []string) error {
			return runInit(cmd, reset, show)
		},
	}

	cmd.Flags().BoolVar(&reset, "reset", false, "Overwrite existing tools.toml with bundled default (backs up to tools.toml.bak)")
	cmd.Flags().BoolVar(&show, "show", false, "Print the bundled default to stdout without writing")
	return cmd
}

func runInit(cmd *cobra.Command, reset, show bool) error {
	w := cmd.OutOrStdout()

	if show {
		_, err := w.Write(defaultToolsTOML)
		return err
	}

	path, err := toolsConfigPath()
	if err != nil {
		return err
	}

	// Check if file already exists.
	_, statErr := os.Stat(path)
	exists := statErr == nil

	if exists && !reset {
		fmt.Fprintf(w, "%s already exists.\n\n", path)
		fmt.Fprintln(w, "Options:")
		fmt.Fprintln(w, "  --reset   Overwrite with bundled default (backs up existing to tools.toml.bak)")
		fmt.Fprintln(w, "  --show    Print bundled default to stdout without writing")
		return nil
	}

	if exists && reset {
		backupPath := path + ".bak"
		if err := copyFile(path, backupPath); err != nil {
			return fmt.Errorf("backup existing tools.toml: %w", err)
		}
		fmt.Fprintf(w, "[abc] backed up existing tools.toml → %s\n", backupPath)
	}

	if err := os.WriteFile(path, defaultToolsTOML, 0o644); err != nil {
		return fmt.Errorf("write tools.toml: %w", err)
	}

	if exists {
		fmt.Fprintf(w, "[abc] tools.toml reset from bundled default → %s\n\n", path)
	} else {
		fmt.Fprintf(w, "[abc] tools.toml created → %s\n\n", path)
	}

	fmt.Fprintln(w, "Next steps:")
	fmt.Fprintln(w, "  1. Review and edit:    abc admin tools edit")
	fmt.Fprintln(w, "  2. Fetch to cache:     abc admin tools fetch")
	fmt.Fprintln(w, "  3. Push to cluster:    abc admin tools push")
	return nil
}

func copyFile(src, dst string) error {
	data, err := os.ReadFile(src)
	if err != nil {
		return err
	}
	return os.WriteFile(dst, data, 0o644)
}
