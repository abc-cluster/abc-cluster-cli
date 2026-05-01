package pipeline

import (
	"github.com/abc-cluster/abc-cluster-cli/cmd/admin/tools"
	cfg "github.com/abc-cluster/abc-cluster-cli/internal/config"
	hclpipeline "github.com/abc-cluster/abc-cluster-cli/internal/hclgen/pipeline"
)

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
		HostVolume:      spec.HostVolume,
		NodeConstraint:  spec.NodeConstraint,
		PinWorkers:      spec.PinWorkers,
		PluginBundleURL: spec.PluginBundleURL,
		Plugins:         plugins,
		ExtraBinaries:   bins,
		StaticEnv:       staticEnv,
	}, nomadAddr, nomadToken, runUUID)
}
