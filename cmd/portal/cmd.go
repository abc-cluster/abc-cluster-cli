// Package portal implements the "abc portal" command group.
//
// abc portal ls            — list all portals with their URLs
// abc portal open <name>   — open a portal in the default browser, pre-authenticated
//
// Portal auth mechanisms by portal:
//
//	nomad     — Nomad UI natively accepts ?token= query param; no server change needed
//	grafana   — magic link via abc-auth-svc /auth/cli-token (session cookie, Domain=.seedling.*)
//	workbench — magic link via abc-auth-svc /auth/cli-token
//	upload    — magic link via abc-auth-svc /auth/cli-token
//	minio     — opens URL + prints credentials to terminal (MinIO console has no URL auth)
package portal

import "github.com/spf13/cobra"

// NewCmd returns the "abc portal" parent command.
func NewCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "portal",
		Short: "Open cluster portals in the browser, pre-authenticated",
		Long: `Open cluster service portals in your default browser, already logged in.

Credentials are read from the active context in ~/.abc/config.yaml — the
same token the abc CLI uses. No manual copy-paste required.

  abc portal ls              List all portals with their URLs
  abc portal open nomad      Open the Nomad job dashboard (token injected)
  abc portal open grafana    Open Grafana (authenticated via magic link)
  abc portal open workbench  Open JupyterLab workbench (authenticated via magic link)
  abc portal open upload     Open the upload portal (authenticated via magic link)
  abc portal open minio      Open MinIO console (credentials printed to terminal)`,
	}

	cmd.AddCommand(newLsCmd())
	cmd.AddCommand(newOpenCmd())
	return cmd
}
