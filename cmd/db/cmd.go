// Package db registers the `abc db` command group: status, migrate, vacuum.
package db

import (
	"fmt"
	"os"
	"path/filepath"
	"text/tabwriter"

	"github.com/abc-cluster/abc-cluster-cli/internal/state"
	"github.com/abc-cluster/abc-cluster-cli/internal/state/migrations"
	"github.com/spf13/cobra"
)

// NewCmd returns the `abc db` command group.
func NewCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "db",
		Short: "Inspect and maintain the local SQLite state at ~/.abc/state.db",
	}
	cmd.AddCommand(newStatusCmd())
	cmd.AddCommand(newMigrateCmd())
	cmd.AddCommand(newVacuumCmd())
	return cmd
}

func newStatusCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "status",
		Short: "Print DB path, schema version, table row counts, WAL size",
		RunE: func(c *cobra.Command, _ []string) error {
			db, err := state.Open()
			if err != nil {
				return err
			}
			path, _ := state.DefaultPath()
			fmt.Fprintf(c.OutOrStdout(), "Path:           %s\n", path)
			// Schema version = highest applied migration.
			rows, err := db.Query(`SELECT version FROM schema_migrations ORDER BY version DESC LIMIT 1`)
			if err != nil {
				return err
			}
			version := "(none)"
			if rows.Next() {
				_ = rows.Scan(&version)
			}
			rows.Close()
			fmt.Fprintf(c.OutOrStdout(), "Schema version: %s\n", version)
			// WAL size.
			if info, err := os.Stat(path + "-wal"); err == nil {
				fmt.Fprintf(c.OutOrStdout(), "WAL size:       %d bytes\n", info.Size())
			} else {
				fmt.Fprintf(c.OutOrStdout(), "WAL size:       (none)\n")
			}
			fmt.Fprintf(c.OutOrStdout(), "DB size:        %s\n", fileSize(path))
			fmt.Fprintln(c.OutOrStdout(), "")
			fmt.Fprintln(c.OutOrStdout(), "Table row counts:")
			tw := tabwriter.NewWriter(c.OutOrStdout(), 0, 0, 2, ' ', 0)
			for _, t := range []string{
				"projects", "investigations", "annotations", "runs",
				"active_pointers", "cli_audit", "citations",
				"freezes", "container_digests", "pipeline_metadata",
				"telemetry_queue",
			} {
				var n int
				if err := db.QueryRow(`SELECT COUNT(*) FROM ` + t).Scan(&n); err != nil {
					fmt.Fprintf(tw, "  %s\t(error: %v)\n", t, err)
					continue
				}
				fmt.Fprintf(tw, "  %s\t%d\n", t, n)
			}
			tw.Flush()
			return nil
		},
	}
}

func newMigrateCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "migrate",
		Short: "Apply pending schema migrations",
		RunE: func(c *cobra.Command, _ []string) error {
			db, err := state.Open()
			if err != nil {
				return err
			}
			if err := migrations.Apply(db); err != nil {
				return err
			}
			versions, _ := migrations.List()
			fmt.Fprintf(c.OutOrStdout(), "Migrations up to date (%d known).\n", len(versions))
			return nil
		},
	}
}

func newVacuumCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "vacuum",
		Short: "Reclaim space with VACUUM",
		RunE: func(c *cobra.Command, _ []string) error {
			db, err := state.Open()
			if err != nil {
				return err
			}
			if _, err := db.Exec("VACUUM"); err != nil {
				return fmt.Errorf("vacuum: %w (another process may hold a lock)", err)
			}
			fmt.Fprintln(c.OutOrStdout(), "VACUUM complete.")
			return nil
		},
	}
}

func fileSize(path string) string {
	info, err := os.Stat(path)
	if err != nil {
		return "(missing)"
	}
	return fmt.Sprintf("%d bytes (%s)", info.Size(), filepath.Base(path))
}
