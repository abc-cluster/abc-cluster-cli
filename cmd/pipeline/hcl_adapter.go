package pipeline

import (
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
		StaticEnv:       staticEnv,
	}, nomadAddr, nomadToken, runUUID)
}
