package module

import hclmodule "github.com/abc-cluster/abc-cluster-cli/internal/hclgen/module"

func generateModuleRunHCL(spec *RunSpec, nomadAddr, nomadToken, runUUID string) string {
	if spec == nil {
		return ""
	}
	return hclmodule.Generate(hclmodule.Spec{
		JobName:            spec.JobName,
		Module:             spec.Module,
		Profile:            spec.Profile,
		WorkDir:            spec.WorkDir,
		OutputPrefix:       spec.OutputPrefix,
		PipelineGenRepo:    spec.PipelineGenRepo,
		PipelineGenVersion: spec.PipelineGenVersion,
		ModuleRevision:     spec.ModuleRevision,
		GitHubToken:        spec.GitHubToken,
		CPU:                spec.CPU,
		MemoryMB:           spec.MemoryMB,
		NfVersion:          spec.NfVersion,
		NfPluginVersion:    spec.NfPluginVersion,
		Namespace:          spec.Namespace,
		Datacenters:        spec.Datacenters,
		MinioEndpoint:      spec.MinioEndpoint,
		ParamsYAMLContent:        spec.ParamsYAMLContent,
		ConfigYAMLContent:        spec.ConfigYAMLContent,
		PipelineGenNoRunManifest: spec.PipelineGenNoRunManifest,
	}, nomadAddr, nomadToken, runUUID)
}
