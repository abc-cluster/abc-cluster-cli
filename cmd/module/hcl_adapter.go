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
		HostVolume:         spec.HostVolume,
		TaskDriver:         spec.TaskDriver,
		NfPluginZipURL:     spec.NfPluginZipURL,
		OutputPrefix:       spec.OutputPrefix,
		PipelineGenRepo:    spec.PipelineGenRepo,
		PipelineGenVersion: spec.PipelineGenVersion,
		PipelineGenURLBase:    spec.PipelineGenURLBase,
		PipelineGenURLResolve: spec.PipelineGenURLResolve,
		ModuleRevision:     spec.ModuleRevision,
		GitHubToken:        spec.GitHubToken,
		CPU:                spec.CPU,
		MemoryMB:           spec.MemoryMB,
		NfVersion:          spec.NfVersion,
		NfPluginVersion:    spec.NfPluginVersion,
		Namespace:          spec.Namespace,
		Datacenters:        spec.Datacenters,
		S3Endpoint:         spec.S3Endpoint,
		S3AccessKey:        spec.S3AccessKey,
		S3SecretKey:        spec.S3SecretKey,
		ParamsYAMLContent:        spec.ParamsYAMLContent,
		ConfigYAMLContent:        spec.ConfigYAMLContent,
		SamplesheetCSVContent:    spec.SamplesheetCSVContent,
		PipelineGenNoRunManifest: spec.PipelineGenNoRunManifest,
		TestMode:                 spec.TestMode,
	}, nomadAddr, nomadToken, runUUID)
}
