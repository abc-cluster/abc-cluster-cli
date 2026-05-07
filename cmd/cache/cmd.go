// Package cache registers the `abc cache` command group: status, migrate, vacuum.
//
// Manages the local SQLite at ~/.abc/state.db (the per-user CLI cache that
// stores projects, investigations, runs, the cli_audit trail, and forward-
// compatible substrate for the freezes / container-digest / pipeline-metadata
// tables consumed by `abc freeze`).
package cache

import (
	"fmt"
	"os"
	"path/filepath"
	"text/tabwriter"

	"github.com/abc-cluster/abc-cluster-cli/internal/state"
	"github.com/abc-cluster/abc-cluster-cli/internal/state/migrations"
	"github.com/spf13/cobra"
)

// NewCmd returns the `abc cache` command group.
func NewCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "cache",
		Short: "Inspect and maintain the local SQLite cache at ~/.abc/state.db",
	}
	cmd.AddCommand(newStatusCmd())
	cmd.AddCommand(newMigrateCmd())
	cmd.AddCommand(newVacuumCmd())
	return cmd
}

func newStatusCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "status",
		Short: "Print binary version, DB path, schema version, pending/future migrations, table row counts, WAL size",
		RunE: func(c *cobra.Command, _ []string) error {
			db, err := state.Open()
			if err != nil {
				return err
			}
			path, _ := state.DefaultPath()

			fmt.Fprintf(c.OutOrStdout(), "Binary version: %s\n", state.CLIVersion)
			fmt.Fprintf(c.OutOrStdout(), "Path:           %s\n", path)

			// Highest applied migration = schema version.
			applied, err := migrations.AppliedVersions(db)
			if err != nil {
				return err
			}
			schemaVersion := "(none)"
			if n := len(applied); n > 0 {
				schemaVersion = applied[n-1].Version
			}
			fmt.Fprintf(c.OutOrStdout(), "Schema version: %s (%d applied)\n", schemaVersion, len(applied))

			pending, err := migrations.Pending(db)
			if err != nil {
				return err
			}
			if len(pending) == 0 {
				fmt.Fprintln(c.OutOrStdout(), "Pending:        none")
			} else {
				fmt.Fprintf(c.OutOrStdout(), "Pending:        %d (%v) — run `abc cache migrate` to apply\n",
					len(pending), pending)
			}

			future, err := migrations.Future(db)
			if err != nil {
				return err
			}
			if len(future) == 0 {
				fmt.Fprintln(c.OutOrStdout(), "Future:         none")
			} else {
				fmt.Fprintf(c.OutOrStdout(), "Future:         %d (%v) — DB AHEAD OF BINARY; UPGRADE abc CLI\n",
					len(future), future)
			}

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

			if len(applied) > 0 {
				fmt.Fprintln(c.OutOrStdout(), "")
				fmt.Fprintln(c.OutOrStdout(), "Applied migrations:")
				tw2 := tabwriter.NewWriter(c.OutOrStdout(), 0, 0, 2, ' ', 0)
				fmt.Fprintln(tw2, "  VERSION\tAPPLIED AT\tBY CLI VERSION")
				for _, a := range applied {
					byv := a.AppliedByCLIVersion
					if byv == "" {
						byv = "(unrecorded)"
					}
					fmt.Fprintf(tw2, "  %s\t%d\t%s\n", a.Version, a.AppliedAt, byv)
				}
				tw2.Flush()
			}
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
			path, _ := state.DefaultPath()
			if err := migrations.Apply(db, path, state.CLIVersion); err != nil {
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
