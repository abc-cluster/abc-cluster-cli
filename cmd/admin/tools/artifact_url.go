package tools

import (
	"fmt"
	"io"
	"os"
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
		Long: `Print a ready-to-paste Nomad artifact stanza for fetching a tool binary.

Two naming conventions are used depending on the tool type:

  Arch-specific  ([tools.*] or [local.*] with paths):
    <tool>-${attr.kernel.name}-${attr.cpu.arch}
    Nomad interpolates the right binary per node at scheduling time.

  Arch-agnostic  ([local.*] with path, e.g. JARs, wheels):
    <tool>-any
    A single artifact served to all nodes regardless of architecture.

The endpoint is read from admin.tools.endpoint in the active context
(written back automatically after: abc admin tools push).

Examples:
  abc admin tools artifact-url s5cmd
  abc admin tools artifact-url nf-pipeline-gen
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

// ArtifactURL returns the cluster-side fetch URL for a tool listed in
// tools.toml. Same resolution as the `artifact-url` subcommand: arch-agnostic
// `[local.*]` entries (single `path`) get the `<name>-any` suffix; everything
// else gets the Nomad-interpolated `<name>-${attr.kernel.name}-${attr.cpu.arch}`
// suffix. Empty `endpointOverride` reads from active context's
// `admin.tools.endpoint` (populated by `abc admin tools push`).
//
// Exported so cluster-side workflows that need to embed a tool URL into a
// generated job spec (e.g. `abc module samplesheet emit`, `abc module run`)
// can reuse the resolution logic without re-implementing it.
func ArtifactURL(toolName, endpointOverride string) (string, error) {
	cfg, _, err := loadToolsConfig()
	if err != nil {
		return "", err
	}
	if !toolKnown(cfg, toolName) {
		return "", fmt.Errorf("tool %q not found in tools.toml", toolName)
	}
	ep := strings.TrimRight(endpointOverride, "/")
	if ep == "" {
		activeCfg, cfgErr := config.Load()
		if cfgErr == nil {
			ep = strings.TrimRight(activeCfg.ActiveCtx().ToolPushEndpoint(), "/")
		}
	}
	if ep == "" {
		return "", fmt.Errorf("no endpoint configured for tool %q (run `abc admin tools push` first, or pass an endpoint)", toolName)
	}
	bucket := cfg.Push.Bucket
	prefix := cfg.Push.Prefix
	// Guard against admin.tools.endpoint being written with the bucket+prefix
	// already included (e.g. "https://s3.example.com/abc-reserved/binary_tools"
	// instead of just "https://s3.example.com"). This was observed when a push
	// wrote back the full remote path rather than the storage root. Strip the
	// suffix defensively so the constructed URL doesn't double the path.
	bucketPrefix := "/" + bucket + "/" + prefix
	if strings.HasSuffix(ep, bucketPrefix) {
		ep = strings.TrimSuffix(ep, bucketPrefix)
		fmt.Fprintf(os.Stderr, "[abc] warning: admin.tools.endpoint contained bucket+prefix suffix %q — stripped. "+
			"Run: abc config set contexts.<name>.admin.tools.endpoint %s\n", bucketPrefix, ep)
	}
	if isArchAgnosticLocal(cfg, toolName) {
		return fmt.Sprintf("%s/%s/%s/%s-any", ep, bucket, prefix, toolName), nil
	}
	return fmt.Sprintf("%s/%s/%s/%s-${attr.kernel.name}-${attr.cpu.arch}",
		ep, bucket, prefix, toolName), nil
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
			"no endpoint configured.\n"+
				"Run: abc admin tools push  (writes the endpoint back to config.yaml)\n"+
				"  or: abc admin tools artifact-url %s --endpoint http://<host>:<port>",
			toolName,
		)
	}

	bucket := cfg.Push.Bucket
	prefix := cfg.Push.Prefix
	// Same defensive strip as ArtifactURL() — see comment there.
	bucketPrefixSuffix := "/" + bucket + "/" + prefix
	if strings.HasSuffix(ep, bucketPrefixSuffix) {
		ep = strings.TrimSuffix(ep, bucketPrefixSuffix)
		fmt.Fprintf(w, "[abc] warning: admin.tools.endpoint contained bucket+prefix suffix %q — stripped. "+
			"Run: abc config set contexts.<name>.admin.tools.endpoint %s\n", bucketPrefixSuffix, ep)
	}

	// Arch-agnostic local artifacts (path, no paths) use the "<name>-any" suffix.
	// Arch-specific tools and locals (paths map) use Nomad interpolation.
	var artifactURL string
	if isArchAgnosticLocal(cfg, toolName) {
		artifactURL = fmt.Sprintf("%s/%s/%s/%s-any", ep, bucket, prefix, toolName)
	} else {
		// ${attr.kernel.name} → "linux"  (matches our <tool>-linux-<arch> naming)
		// ${attr.cpu.arch}    → "amd64" or "arm64"  (matches our GOARCH naming)
		artifactURL = fmt.Sprintf("%s/%s/%s/%s-${attr.kernel.name}-${attr.cpu.arch}",
			ep, bucket, prefix, toolName)
	}

	if raw {
		fmt.Fprintln(w, artifactURL)
		return nil
	}

	fmt.Fprintf(w, `artifact {
  source = "%s"
}
`, artifactURL)
	return nil
}

// isArchAgnosticLocal reports whether name is a [local.*] entry that uses the
// single-path form (arch-agnostic artifact like a JAR or wheel), as opposed to
// the paths-map form (arch-specific native binary).
func isArchAgnosticLocal(cfg *ToolsConfig, name string) bool {
	for _, l := range cfg.Local {
		if l.Name == name {
			return len(l.Paths) == 0 && l.Path != ""
		}
	}
	return false
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
