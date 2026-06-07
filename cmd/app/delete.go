package app

import (
	"fmt"
	"strings"

	"github.com/abc-cluster/abc-cluster-cli/internal/appgen"
	"github.com/abc-cluster/abc-cluster-cli/internal/config"
	"github.com/spf13/cobra"
)

func newDeleteCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "delete <name>",
		Short: "Delete an app and revoke its data credentials",
		Long: `Delete a deployed app: stops the job, purges the Nomad job definition
(which removes the service + its Traefik routing tags from the Nomad catalog,
so Traefik drops the route within one poll interval), and revokes the app's
MinIO service account.

Requires --confirm. Without it, prints a summary of what will be destroyed and
exits non-zero.`,
		Args: cobra.ExactArgs(1),
		RunE: runDelete,
	}
	cmd.Flags().Bool("confirm", false, "Confirm destructive deletion")
	return cmd
}

func runDelete(cmd *cobra.Command, args []string) error {
	out := cmd.OutOrStdout()
	ctx := cmd.Context()
	nc := nomadClientFromCmd(cmd)

	r, err := resolveApp(ctx, nc, args[0])
	if err != nil {
		return err
	}

	saName := "abc-app-" + r.Project + "-" + r.Name
	confirm, _ := cmd.Flags().GetBool("confirm")
	if !confirm {
		fmt.Fprintf(out, "Would delete app %q:\n", r.Name)
		fmt.Fprintf(out, "  • purge Nomad job %s (removes the Traefik route)\n", r.JobID)
		fmt.Fprintf(out, "  • revoke MinIO service account %s\n", saName)
		fmt.Fprintf(out, "  • URL %s will stop resolving\n", appURLFromJob(r))
		return fmt.Errorf("re-run with --confirm to delete")
	}

	// Purge the job — removes the service + Traefik tags from the Nomad catalog.
	if _, err := nc.StopJob(ctx, r.JobID, nc.DefaultNamespace(), true); err != nil {
		return fmt.Errorf("purge app job %q: %w", r.Name, err)
	}
	fmt.Fprintf(out, "Purged Nomad job %s\n", r.JobID)

	// Revoke the MinIO service account. Build a minimal spec carrying the
	// identity + a placeholder data entry so Revoke targets the right SA even
	// though we don't re-read the original buckets.
	cfg, _ := config.Load()
	var activeCtx config.Context
	if cfg != nil {
		activeCtx = cfg.ActiveCtx()
	}
	minioCreds, mErr := appgen.ResolveMinIOCreds(activeCtx)
	if mErr != nil {
		fmt.Fprintf(out, "  note: could not resolve MinIO to revoke service account %s (%v); revoke manually if it exists\n", saName, mErr)
		return nil
	}
	prov, pErr := appgen.NewDataProvisioner(minioCreds)
	if pErr != nil {
		fmt.Fprintf(out, "  note: could not build MinIO client to revoke service account %s (%v); revoke manually if it exists\n", saName, pErr)
		return nil
	}
	revokeSpec := &appgen.Spec{
		Name:    r.Name,
		Project: r.Project,
		// One placeholder data entry makes Revoke target the SA (Revoke is a
		// no-op only when Data is empty).
		Data: []appgen.DataMount{{Bucket: "_"}},
	}
	if err := prov.Revoke(ctx, revokeSpec); err != nil {
		if !strings.Contains(strings.ToLower(err.Error()), "not exist") {
			fmt.Fprintf(out, "  note: revoke of service account %s returned: %v\n", saName, err)
		}
	} else {
		fmt.Fprintf(out, "Revoked MinIO service account %s\n", saName)
	}
	return nil
}
