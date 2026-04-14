package pipeline

import hclpipeline "github.com/abc-cluster/abc-cluster-cli/internal/hclgen/pipeline"

func generateHeadJobHCL(spec *PipelineSpec, nomadAddr, nomadToken, runUUID string) string {
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
	}, nomadAddr, nomadToken, runUUID)
}
