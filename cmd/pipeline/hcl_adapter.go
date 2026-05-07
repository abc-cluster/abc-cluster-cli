package pipeline

import (
	"runtime/debug"
	"strings"
	"time"

	"github.com/abc-cluster/abc-cluster-cli/cmd/admin/tools"
	cfg "github.com/abc-cluster/abc-cluster-cli/internal/config"
	hclpipeline "github.com/abc-cluster/abc-cluster-cli/internal/hclgen/pipeline"
)

// resolveIdentity gathers the user/workspace/pipeline correlation context
// from the active context + spec. Each field is best-effort; absent fields
// degrade cleanly (the generator omits empty meta keys).
func resolveIdentity(spec *PipelineSpec) hclpipeline.Identity {
	id := hclpipeline.Identity{
		PipelineURL:      spec.Repository,
		PipelineRevision: spec.Revision,
		RunName:          spec.Name,
		SubmittedAt:      time.Now().UTC().Format(time.RFC3339),
		CLIVersion:       cliVersion(),
	}
	if c, err := cfg.Load(); err == nil {
		ctx := c.ActiveCtx()
		id.UserWhoami = strings.TrimSpace(ctx.Admin.Whoami)
		if id.UserWhoami == "" && ctx.Auth != nil {
			id.UserWhoami = strings.TrimSpace(ctx.Auth.Whoami)
		}
		id.UserUUID = strings.TrimSpace(ctx.Admin.ID)
		// Workspace = the Nomad namespace the head will land in (== bucket
		// name in workspaces.yaml v2).
		id.Workspace = strings.TrimSpace(spec.Namespace)
		// Tenant defaults to Workspace; future schema work resolves the real
		// parent-chain root from workspaces.yaml.
	}
	return id
}

// cliVersion reports the abc CLI's own version. Falls back to "dev" outside
// a build with embedded version info.
func cliVersion() string {
	if info, ok := debug.ReadBuildInfo(); ok {
		if v := strings.TrimSpace(info.Main.Version); v != "" && v != "(devel)" {
			return v
		}
	}
	return "dev"
}

func generateHeadJobHCL(spec *PipelineSpec, nomadAddr, nomadToken, runUUID string) string {
	if spec == nil {
		return ""
	}
	var staticEnv map[string]string
	if c, err := cfg.Load(); err == nil {
		staticEnv = cfg.AbcNodesMonitoringEnv(c.ActiveCtx())
	}
	return generateHeadJobHCLWithStaticEnv(spec, nomadAddr, nomadToken, runUUID, staticEnv)
}

func generateHeadJobHCLWithStaticEnv(spec *PipelineSpec, nomadAddr, nomadToken, runUUID string, staticEnv map[string]string) string {
	if spec == nil {
		return ""
	}
	plugins := make([]hclpipeline.PluginRef, 0, len(spec.Plugins))
	for _, p := range spec.Plugins {
		plugins = append(plugins, hclpipeline.PluginRef{ID: p.ID, Version: p.Version})
	}
	// Resolve each tool binary's cluster artifact URL up front. Names not
	// registered in tools.toml are silently skipped (logged on the head job's
	// stderr would be nicer, but the run just fails actionably at submit time).
	bins := make([]hclpipeline.ToolBinary, 0, len(spec.ExtraBinaries))
	for _, name := range spec.ExtraBinaries {
		url, err := tools.ArtifactURL(name, "")
		if err == nil {
			bins = append(bins, hclpipeline.ToolBinary{Name: name, SourceURL: url})
		}
	}
	return hclpipeline.Generate(hclpipeline.Spec{
		Name:            spec.Name,
		WorkDir:         spec.WorkDir,
		Params:          spec.Params,
		CPU:             spec.CPU,
		MemoryMB:        spec.MemoryMB,
		NfVersion:       spec.NfVersion,
		NfPluginVersion: spec.NfPluginVersion,
		Namespace:       spec.Namespace,
		Datacenters:     spec.Datacenters,
		Repository:      spec.Repository,
		Revision:        spec.Revision,
		Profile:         spec.Profile,
		ExtraConfig:     spec.ExtraConfig,
		Resume:          spec.Resume,
		SessionID:       spec.SessionID,
		RunTag:          spec.RunTag,
		PipelineSlug:    spec.PipelineSlug,
		HostVolume:      spec.HostVolume,
		NodeConstraint:    spec.NodeConstraint,
		PinWorkers:        spec.PinWorkers,
		WorkerExcludeHost: spec.WorkerExcludeHost,
		PluginBundleURL: spec.PluginBundleURL,
		NextflowBinURL:  spec.NextflowBinURL,
		Plugins:         plugins,
		ExtraBinaries:   bins,
		StaticEnv:       staticEnv,
		WaveEndpoint:     spec.WaveEndpoint,
		FusionEnabled:    spec.FusionEnabled,
		ContainerRuntime: spec.ContainerRuntime,
		Identity:         resolveIdentity(spec),
	}, nomadAddr, nomadToken, runUUID)
}
