// Package app implements the `abc app` command group — deploying browser-facing
// scientific web apps (Streamlit, Shiny, Pode.Web, …) as persistent, auth-gated
// Nomad `service` jobs on the seedling tier.
//
// Spec: abc-universe specs/active/abc-app-deploy.md.
//
// Apps are described by an `abc-app.yaml`, run in the `abc-apps` Nomad
// namespace, are routed by subdomain (https://<project>-<name>.apps.seedling.
// abc-cluster.cloud, served at root), discovered by a Traefik Nomad-provider
// job via routing tags on the Nomad service block, and gated by forward-auth at
// the Caddy edge. The CLI emits the routing tags; it makes no out-of-band route
// registration. See internal/appgen for spec validation + HCL generation.
package app

import (
	"strings"

	"github.com/abc-cluster/abc-cluster-cli/cmd/utils"
	"github.com/abc-cluster/abc-cluster-cli/internal/appgen"
	"github.com/abc-cluster/abc-cluster-cli/internal/config"
	"github.com/spf13/cobra"
)

// NewCmd returns the "app" subcommand group.
func NewCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "app",
		Short: "Deploy and manage scientific web apps",
		Long: `Deploy a browser-facing scientific web app (Streamlit, R Shiny, Pode.Web, …)
as a persistent, auth-gated Nomad service job on the cluster.

An app is described by an abc-app.yaml in your project directory. The platform
handles Nomad job templating, subdomain routing, health checks, and per-app
MinIO data access — no Nomad HCL, router config, or policy is written by hand.

  abc app init                   Scaffold an abc-app.yaml in the cwd
  abc app validate               Validate abc-app.yaml + print resolved values
  abc app deploy                 Deploy ./abc-app.yaml
  abc app deploy --dry-run       Print the templated Nomad HCL, submit nothing
  abc app list                   List deployed apps
  abc app show <name>            Show one app's status + details
  abc app logs <name> -f         Stream an app's container logs (live with -f)
  abc app open <name>            Open an app's URL in the browser
  abc app url <name>             Print an app's URL (pipe-friendly)
  abc app config <name> ...      View/edit an app's plain env vars
  abc app exec <name> -- <cmd>   Run a one-off command in the app container
  abc app restart <name>         Re-roll a running app without spec changes
  abc app stop <name>            Stop an app (preserves the job definition)
  abc app delete <name> --confirm  Delete an app + revoke its data credentials`,
	}

	cmd.PersistentFlags().String("nomad-addr", utils.EnvOrDefault("NOMAD_ADDR"),
		"Nomad API address (or set NOMAD_ADDR; defaults to http://127.0.0.1:4646)")
	cmd.PersistentFlags().String("nomad-token", utils.EnvOrDefault("NOMAD_TOKEN"),
		"Nomad ACL token (or set NOMAD_TOKEN)")
	cmd.PersistentFlags().String("region", utils.EnvOrDefault("NOMAD_REGION"),
		"Nomad region (or set NOMAD_REGION)")
	cmd.PersistentFlags().String("namespace", "",
		"Nomad namespace for apps (default: abc-apps)")

	cmd.AddCommand(
		newInitCmd(),
		newValidateCmd(),
		newDeployCmd(),
		newListCmd(),
		newShowCmd(),
		newLogsCmd(),
		newOpenCmd(),
		newConfigCmd(),
		newExecCmd(),
		newRestartCmd(),
		newStopCmd(),
		newDeleteCmd(),
		newURLCmd(),
	)
	return cmd
}

// appNamespace resolves the Nomad namespace apps run in: the --namespace flag
// if set, otherwise the abc-apps default. Apps do NOT inherit the context's
// research namespace — they live in a dedicated namespace by design.
func appNamespace(cmd *cobra.Command) string {
	ns, _ := cmd.Flags().GetString("namespace")
	if strings.TrimSpace(ns) != "" {
		return strings.TrimSpace(ns)
	}
	return appgen.DefaultNamespace
}

// appsDoorsFromActiveContext returns the per-plane ingress door hostnames + IPs
// from the active context's admin.services.apps block. Each unset field is
// surfaced as "" — the URL composers in internal/appgen fall through cleanly.
// Any caller of Spec.URL() / Spec.URLIP() / Spec.ResolvedSummary() that lives
// inside a CLI command should use this helper so URLs reflect the user's
// configured cluster, not a build-time constant.
func appsDoorsFromActiveContext() appgen.AppsDoors {
	cfg, _ := config.Load()
	if cfg == nil {
		return appgen.AppsDoors{}
	}
	ctx := cfg.ActiveCtx()
	return appgen.AppsDoors{
		PublicDomain:  ctx.AppsPublicDomain(),
		PrivateDoor:   ctx.AppsPrivateDoor(),
		PrivateDoorIP: ctx.AppsPrivateDoorIP(),
		SharedDoor:    ctx.AppsSharedDoor(),
		SharedDoorIP:  ctx.AppsSharedDoorIP(),
	}
}

// nomadClientFromCmd builds a NomadClient from flags, falling back to the
// active context's broker-aware Nomad defaults, scoped to the app namespace.
func nomadClientFromCmd(cmd *cobra.Command) *utils.NomadClient {
	addr, _ := cmd.Flags().GetString("nomad-addr")
	if addr == "" {
		addr, _ = cmd.Root().PersistentFlags().GetString("nomad-addr")
	}
	token, _ := cmd.Flags().GetString("nomad-token")
	if token == "" {
		token, _ = cmd.Root().PersistentFlags().GetString("nomad-token")
	}
	region, _ := cmd.Flags().GetString("region")
	if region == "" {
		region, _ = cmd.Root().PersistentFlags().GetString("region")
	}
	if addr == "" || token == "" || region == "" {
		cfgAddr, cfgToken, cfgRegion := utils.NomadDefaultsFromConfig()
		if addr == "" {
			addr = cfgAddr
		}
		if token == "" {
			token = cfgToken
		}
		if region == "" {
			region = cfgRegion
		}
	}
	return utils.NewNomadClient(addr, token, region).
		WithSudo(utils.SudoFromCmd(cmd)).
		WithCloud(utils.CloudFromCmd(cmd)).
		WithNamespace(appNamespace(cmd))
}
