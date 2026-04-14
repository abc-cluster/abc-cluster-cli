package job

import jobhcl "github.com/abc-cluster/abc-cluster-cli/internal/hclgen/job"

func generateHCL(spec *jobSpec, scriptName, scriptContent string) string {
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
		Meta:                spec.Meta,
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
	}
	return jobhcl.Generate(hclSpec, scriptName, scriptContent)
}
