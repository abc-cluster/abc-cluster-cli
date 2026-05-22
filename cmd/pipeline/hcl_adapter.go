package pipeline

import (
	"github.com/abc-cluster/abc-cluster-cli/cmd/admin/tools"
	"github.com/abc-cluster/abc-cluster-cli/cmd/utils"
	cfg "github.com/abc-cluster/abc-cluster-cli/internal/config"
	hclpipeline "github.com/abc-cluster/abc-cluster-cli/internal/hclgen/pipeline"
)

// resolveIdentity gathers the user/workspace/pipeline correlation context.
// The user/workspace base is sourced from the shared utils.ResolveUserIdentity
// helper so that `abc job run` and `abc pipeline run` agree on field semantics
// and meta-key shape; pipeline-specific fields (PipelineURL, PipelineRevision,
// RunName) are layered on top. Absent fields degrade cleanly — the generator
// omits empty meta keys.
func resolveIdentity(spec *PipelineSpec) hclpipeline.Identity {
	var ctxPtr *cfg.Context
	if c, err := cfg.Load(); err == nil {
		ctx := c.ActiveCtx()
		ctxPtr = &ctx
	}
	base := utils.ResolveUserIdentity(ctxPtr, spec.Namespace)
	return hclpipeline.Identity{
		UserWhoami:       base.UserWhoami,
		UserUUID:         base.UserUUID,
		Workspace:        base.Workspace,
		WorkspaceType:    base.WorkspaceType,
		Tenant:           base.Tenant,
		SubmittedAt:      base.SubmittedAt,
		CLIVersion:       base.CLIVersion,
		PipelineURL:      spec.Repository,
		PipelineRevision: spec.Revision,
		RunName:          spec.Name,
	}
}

func generateHeadJobHCL(spec *PipelineSpec, nomadAddr, nomadToken, runUUID string) string {
	if spec == nil {
		return ""
	}
	var staticEnv map[string]string
	skipNomadVarCreds := false
	if c, err := cfg.Load(); err == nil {
		actx := c.ActiveCtx()
		staticEnv = cfg.AbcNodesMonitoringEnv(actx)
		// Inject MinIO / S3 env vars from admin.services.minio.* so the
		// Nextflow aws { } block can reach the cluster's MinIO instance.
		// When credentials are present here, skip the nomadVar template that
		// reads from nomad/jobs/secrets — that template blocks task startup
		// on clusters where the Nomad Variable doesn't exist.
		if ep := actx.MinioS3APIEndpoint(); ep != "" {
			if staticEnv == nil {
				staticEnv = map[string]string{}
			}
			staticEnv["NF_MINIO_ENDPOINT"] = ep
		}
		if ak, ok := cfg.GetAdminFloorField(&actx.Admin.Services, "minio", "access_key"); ok && ak != "" {
			if staticEnv == nil {
				staticEnv = map[string]string{}
			}
			staticEnv["AWS_ACCESS_KEY_ID"] = ak
			skipNomadVarCreds = true
		}
		if sk, ok := cfg.GetAdminFloorField(&actx.Admin.Services, "minio", "secret_key"); ok && sk != "" {
			if staticEnv == nil {
				staticEnv = map[string]string{}
			}
			staticEnv["AWS_SECRET_ACCESS_KEY"] = sk
		}
	}
	return generateHeadJobHCLWithStaticEnvAndFlags(spec, nomadAddr, nomadToken, runUUID, staticEnv, skipNomadVarCreds)
}

func generateHeadJobHCLWithStaticEnv(spec *PipelineSpec, nomadAddr, nomadToken, runUUID string, staticEnv map[string]string) string {
	return generateHeadJobHCLWithStaticEnvAndFlags(spec, nomadAddr, nomadToken, runUUID, staticEnv, false)
}

func generateHeadJobHCLWithStaticEnvAndFlags(spec *PipelineSpec, nomadAddr, nomadToken, runUUID string, staticEnv map[string]string, skipNomadVarCreds bool) string {
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
		StaticEnv:         staticEnv,
		SkipNomadVarCreds: skipNomadVarCreds,
		WaveEndpoint:      spec.WaveEndpoint,
		FusionEnabled:     spec.FusionEnabled,
		ContainerRuntime:  spec.ContainerRuntime,
		Identity:          resolveIdentity(spec),
	}, nomadAddr, nomadToken, runUUID)
}
