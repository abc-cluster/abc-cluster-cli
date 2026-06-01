package job

import (
	"maps"

	cfg "github.com/abc-cluster/abc-cluster-cli/internal/config"
	jobhcl "github.com/abc-cluster/abc-cluster-cli/internal/hclgen/job"
)

// mergeJobMetaForMonitoringFloor returns job meta: a copy of base plus
// abc_monitoring_floor=enhanced when static monitoring env is injected.
func mergeJobMetaForMonitoringFloor(base map[string]string, staticEnv map[string]string) map[string]string {
	if len(staticEnv) == 0 {
		if len(base) == 0 {
			return nil
		}
		return maps.Clone(base)
	}
	out := maps.Clone(base)
	if out == nil {
		out = make(map[string]string)
	}
	out["abc_monitoring_floor"] = "enhanced"
	return out
}

func generateHCL(spec *jobSpec, scriptName, scriptContent string) string {
	if spec == nil {
		return ""
	}
	var static map[string]string
	if c, err := cfg.Load(); err == nil {
		static = cfg.AbcNodesMonitoringEnv(c.ActiveCtx())
	}
	return generateHCLFromSpec(spec, scriptName, scriptContent, static)
}

// generateHCLFromSpec builds Nomad HCL for a resolved jobSpec and optional
// monitoring static env (used by tests and generateHCL).
func generateHCLFromSpec(spec *jobSpec, scriptName, scriptContent string, staticEnv map[string]string) string {
	if spec == nil {
		return ""
	}

	artifacts := make([]jobhcl.ArtifactSpec, 0, len(spec.Artifacts))
	for _, a := range spec.Artifacts {
		artifacts = append(artifacts, jobhcl.ArtifactSpec{
			Source:      a.Source,
			Destination: a.Destination,
			Mode:        a.Mode,
		})
	}

	// Wave-exec: resolve token secret parts and derive the artifact source URL.
	var waveSpec jobhcl.WaveSpec
	if spec.WaveTargetImage != "" {
		tokenPath, tokenKey := parseWaveTokenSecret(spec.WaveTokenSecret)
		platform := spec.WavePlatform
		if platform == "" {
			platform = waveDefaultPlatform
		}
		waveSpec = jobhcl.WaveSpec{
			Enabled:         true,
			CondaFileContent: spec.WaveEnvContent,
			TokenSecretPath: tokenPath,
			TokenSecretKey:  tokenKey,
			Platform:        platform,
			// Base URL only — the build script appends -${kernel}-${arch} via
			// uname at runtime, matching the micromamba download pattern.
			BinarySourceURL: spec.WaveBinaryURL,
		}
	}

	var extraTemplates []jobhcl.TemplateSpec
	if spec.PixiManifestContent != "" {
		extraTemplates = append(extraTemplates, jobhcl.TemplateSpec{
			Data:        spec.PixiManifestContent,
			Destination: "local/pixi.toml",
			Perms:       "0644",
		})
	}
	if spec.PixiLockContent != "" {
		extraTemplates = append(extraTemplates, jobhcl.TemplateSpec{
			Data:        spec.PixiLockContent,
			Destination: "local/pixi.lock",
			Perms:       "0644",
		})
	}
	if spec.MicromambaEnvContent != "" {
		extraTemplates = append(extraTemplates, jobhcl.TemplateSpec{
			Data:        spec.MicromambaEnvContent,
			Destination: "local/environment.yml",
			Perms:       "0644",
		})
	}

	constraints := make([]jobhcl.Constraint, 0, len(spec.Constraints))
	for _, c := range spec.Constraints {
		constraints = append(constraints, jobhcl.Constraint{
			Attribute: c.Attribute,
			Operator:  c.Operator,
			Value:     c.Value,
		})
	}
	affinities := make([]jobhcl.Affinity, 0, len(spec.Affinities))
	for _, a := range spec.Affinities {
		affinities = append(affinities, jobhcl.Affinity{
			Attribute: a.Attribute,
			Operator:  a.Operator,
			Value:     a.Value,
			Weight:    a.Weight,
		})
	}

	var stagingSpec jobhcl.StagingSpec
	if spec.StageEnabled {
		stagingSpec = jobhcl.StagingSpec{
			Enabled:          true,
			StageInManifest:  spec.StageInManifest,
			StageOutManifest: spec.StageOutManifest,
			DestRoot:         spec.StageDestRoot,
			S5cmdPath:        spec.StageS5cmdPath,
			HostVolumeName:   spec.StageHostVolumeName,
			HostVolumeSource: spec.StageHostVolumeSource,
			HostVolumeMount:  spec.StageHostVolumeMount,
			Env:              spec.StageEnv,
		}
	}

	hclSpec := jobhcl.Spec{
		Name:                spec.Name,
		Namespace:           spec.Namespace,
		Region:              spec.Region,
		Datacenters:         spec.Datacenters,
		NodePool:            spec.NodePool,
		Priority:            spec.Priority,
		Nodes:               spec.Nodes,
		Cores:               spec.Cores,
		MemoryMB:            spec.MemoryMB,
		GPUs:                spec.GPUs,
		WalltimeSecs:        spec.WalltimeSecs,
		ChDir:               spec.ChDir,
		Depend:              spec.Depend,
		Driver:              spec.Driver,
		Shell:               spec.Shell,
		DriverConfig:        spec.DriverConfig,
		RescheduleMode:      spec.RescheduleMode,
		RescheduleAttempts:  spec.RescheduleAttempts,
		RescheduleInterval:  spec.RescheduleInterval,
		RescheduleDelay:     spec.RescheduleDelay,
		RescheduleMaxDelay:  spec.RescheduleMaxDelay,
		OutputLog:           spec.OutputLog,
		ErrorLog:            spec.ErrorLog,
		NoNetwork:           spec.NoNetwork,
		Constraints:         constraints,
		Affinities:          affinities,
		SlurmPartition:      spec.SlurmPartition,
		SlurmAccount:        spec.SlurmAccount,
		SlurmWorkDir:        spec.SlurmWorkDir,
		SlurmStdoutFile:     spec.SlurmStdoutFile,
		SlurmStderrFile:     spec.SlurmStderrFile,
		SlurmNTasks:         spec.SlurmNTasks,
		SlurmReservation:    spec.SlurmReservation,
		SlurmExtraArgs:      spec.SlurmExtraArgs,
		Spread:              spec.Spread,
		IncludeHPCCompatEnv: spec.IncludeHPCCompatEnv,
		Meta:                mergeJobMetaForMonitoringFloor(spec.Meta, staticEnv),
		Conda:               spec.Conda,
		Pixi:                spec.Pixi,
		TaskTmp:             spec.TaskTmp,
		Ports:               spec.Ports,
		ExposeAllocID:       spec.ExposeAllocID,
		ExposeShortAllocID:  spec.ExposeShortAllocID,
		ExposeAllocName:     spec.ExposeAllocName,
		ExposeAllocIndex:    spec.ExposeAllocIndex,
		ExposeJobID:         spec.ExposeJobID,
		ExposeJobName:       spec.ExposeJobName,
		ExposeParentJobID:   spec.ExposeParentJobID,
		ExposeGroupName:     spec.ExposeGroupName,
		ExposeTaskName:      spec.ExposeTaskName,
		ExposeNamespaceEnv:  spec.ExposeNamespaceEnv,
		ExposeDCEnv:         spec.ExposeDCEnv,
		ExposeCPULimit:      spec.ExposeCPULimit,
		ExposeCPUCores:      spec.ExposeCPUCores,
		ExposeMemLimit:      spec.ExposeMemLimit,
		ExposeMemMaxLimit:   spec.ExposeMemMaxLimit,
		ExposeAllocDir:      spec.ExposeAllocDir,
		ExposeTaskDir:       spec.ExposeTaskDir,
		ExposeSecretsDir:    spec.ExposeSecretsDir,
		StaticEnv:           staticEnv,
		Artifacts:           artifacts,
		ExtraTemplates:      extraTemplates,
		Wave:                waveSpec,
		Staging:             stagingSpec,
	}
	return jobhcl.Generate(hclSpec, scriptName, scriptContent)
}
