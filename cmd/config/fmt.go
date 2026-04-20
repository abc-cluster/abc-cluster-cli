package config

import (
	"bytes"
	"fmt"
	"os"

	cfg "github.com/abc-cluster/abc-cluster-cli/internal/config"
	"github.com/spf13/cobra"
)

func newFmtCmd() *cobra.Command {
	var pathFlag string
	var check bool
	cmd := &cobra.Command{
		Use:   "fmt",
		Short: "Validate and rewrite config with sorted keys",
		Long: `Load the configuration file, validate it, and write it back using the canonical YAML layout
(lexicographically sorted keys at each mapping level, stable top-level key order).

The file is the same as other abc config commands: ABC_CONFIG_FILE if set, otherwise ~/.abc/config.yaml.

Use --check to only validate and verify the file already matches canonical output; the command exits with
a non-zero status if validation fails or the file would change.`,
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			path := pathFlag
			if path == "" {
				path = cfg.DefaultConfigPath()
			}
			if fi, err := os.Stat(path); err != nil {
				if os.IsNotExist(err) {
					return fmt.Errorf("config file %q does not exist", path)
				}
				return fmt.Errorf("stat config file: %w", err)
			} else if fi.IsDir() {
				return fmt.Errorf("config path %q is a directory", path)
			}

			raw, err := os.ReadFile(path)
			if err != nil {
				return fmt.Errorf("read config: %w", err)
			}

			c, err := cfg.LoadFrom(path)
			if err != nil {
				return fmt.Errorf("parse config: %w", err)
			}
			if err := c.Validate(); err != nil {
				return err
			}
			formatted, err := c.MarshalDocumentYAML()
			if err != nil {
				return fmt.Errorf("marshal config: %w", err)
			}

			if check {
				if !bytes.Equal(bytes.TrimSpace(raw), bytes.TrimSpace(formatted)) {
					return fmt.Errorf("config file %q is not formatted or differs from canonical output (run without --check to rewrite)", path)
				}
				return nil
			}

			if bytes.Equal(bytes.TrimSpace(raw), bytes.TrimSpace(formatted)) {
				quiet, _ := cmd.Root().PersistentFlags().GetBool("quiet")
				if !quiet {
					fmt.Fprintf(cmd.ErrOrStderr(), "%s: already formatted\n", path)
				}
				return nil
			}

			if err := c.SaveTo(path); err != nil {
				return err
			}
			quiet, _ := cmd.Root().PersistentFlags().GetBool("quiet")
			if !quiet {
				fmt.Fprintf(cmd.ErrOrStderr(), "formatted %s\n", path)
			}
			return nil
		},
	}
	cmd.Flags().StringVar(&pathFlag, "path", "", "config file path (default: ABC_CONFIG_FILE or ~/.abc/config.yaml)")
	cmd.Flags().BoolVar(&check, "check", false, "validate only; fail if the file is invalid or not already canonical")
	return cmd
}
