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

	hclSpec := jobhcl.Spec{
		Name:                spec.Name,
		Namespace:           spec.Namespace,
		Region:              spec.Region,
		Datacenters:         spec.Datacenters,
		Priority:            spec.Priority,
		Nodes:               spec.Nodes,
		Cores:               spec.Cores,
		MemoryMB:            spec.MemoryMB,
		GPUs:                spec.GPUs,
		WalltimeSecs:        spec.WalltimeSecs,
		ChDir:               spec.ChDir,
		Depend:              spec.Depend,
		Driver:              spec.Driver,
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
		IncludeHPCCompatEnv: spec.IncludeHPCCompatEnv,
		Meta:                mergeJobMetaForMonitoringFloor(spec.Meta, staticEnv),
		Conda:               spec.Conda,
		Pixi:                spec.Pixi,
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
	}
	return jobhcl.Generate(hclSpec, scriptName, scriptContent)
}
