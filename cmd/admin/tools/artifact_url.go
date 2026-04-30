package tools

import (
	"fmt"
	"io"
	"strings"

	"github.com/abc-cluster/abc-cluster-cli/internal/config"
	"github.com/spf13/cobra"
)

func newArtifactURLCmd() *cobra.Command {
	var raw bool
	var endpoint string

	cmd := &cobra.Command{
		Use:   "artifact-url <tool>",
		Short: "Print a Nomad artifact stanza for a cluster tool",
		Long: `Print a ready-to-paste Nomad artifact stanza that fetches the correct
binary for each node's architecture at scheduling time.

Nomad interpolates ${attr.kernel.name} (e.g. "linux") and ${attr.cpu.arch}
(e.g. "amd64", "arm64") on each node.  These match the naming convention used
by abc admin tools fetch/push exactly: <tool>-linux-amd64, <tool>-linux-arm64.

The endpoint is read from admin.tools.endpoint in the active context
(written back automatically after: abc admin tools push).

Examples:
  abc admin tools artifact-url s5cmd
  abc admin tools artifact-url pixi --raw
  abc admin tools artifact-url abc-node-probe --endpoint http://rustfs.aither`,
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			return runArtifactURL(cmd.OutOrStdout(), args[0], endpoint, raw)
		},
	}

	cmd.Flags().BoolVar(&raw, "raw", false, "Print the URL only, without the HCL artifact block")
	cmd.Flags().StringVar(&endpoint, "endpoint", "", "Override S3 endpoint (default: from active context)")
	return cmd
}

func runArtifactURL(w io.Writer, toolName, endpointOverride string, raw bool) error {
	cfg, _, err := loadToolsConfig()
	if err != nil {
		return err
	}

	// Validate that the tool is known (tools or local sections).
	if !toolKnown(cfg, toolName) {
		return fmt.Errorf("tool %q not found in tools.toml (tools or local sections)", toolName)
	}

	// Resolve endpoint: flag > config.yaml > error.
	ep := strings.TrimRight(endpointOverride, "/")
	if ep == "" {
		activeCfg, cfgErr := config.Load()
		if cfgErr == nil {
			ep = strings.TrimRight(activeCfg.ActiveCtx().ToolPushEndpoint(), "/")
		}
	}
	if ep == "" {
		return fmt.Errorf(
			"no endpoint configured.\n" +
				"Run: abc admin tools push  (writes the endpoint back to config.yaml)\n" +
				"  or: abc admin tools artifact-url %s --endpoint http://<host>:<port>",
			toolName,
		)
	}

	bucket := cfg.Push.Bucket
	prefix := cfg.Push.Prefix

	// Nomad interpolation variables for node attributes.
	// ${attr.kernel.name} → "linux"  (matches our <tool>-linux-<arch> naming)
	// ${attr.cpu.arch}    → "amd64" or "arm64"  (matches our GOARCH naming)
	url := fmt.Sprintf("%s/%s/%s/%s-${attr.kernel.name}-${attr.cpu.arch}",
		ep, bucket, prefix, toolName)

	if raw {
		fmt.Fprintln(w, url)
		return nil
	}

	fmt.Fprintf(w, `artifact {
  source = "%s"
}
`, url)
	return nil
}

// toolKnown reports whether name appears in either the [tools.*] or [local.*]
// sections of the config.
func toolKnown(cfg *ToolsConfig, name string) bool {
	for _, t := range cfg.Tools {
		if t.Name == name {
			return true
		}
	}
	for _, l := range cfg.Local {
		if l.Name == name {
			return true
		}
	}
	return false
}
