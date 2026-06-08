package pipeline

import (
	"strings"

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
	s5cmdSkipTLS := false
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
			// abc-seedling (and any private deployment) uses HTTPS with a
			// self-signed / private CA. Worker container images (e.g.
			// quay.io/nextflow/bash) don't carry the cluster CA, so s5cmd
			// fails with "x509: certificate signed by unknown authority"
			// unless --no-verify-ssl is set. Detect this case by checking
			// whether the endpoint is HTTPS — public cloud endpoints (AWS S3,
			// GCS) are never configured via admin.services.minio, so HTTPS
			// here always means a private deployment with a private CA.
			if strings.HasPrefix(ep, "https://") {
				s5cmdSkipTLS = true
			}
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
		// Nextflow CloudCache — persist .nextflow/cache + history on S3 so
		// `-resume` can find previously-completed tasks across head-task
		// restarts. Without this, the LevelDB session cache lives only on
		// the ephemeral head container's /local fs and is lost when the
		// alloc terminates → `-resume` always re-runs everything.
		//
		// The cache lives **inside the user's group bucket**, as a
		// sibling of workdir/ and results/ under the same scope+user
		// prefix. This inherits the user's existing IAM (no shared-write
		// surface; no cross-group abuse vector) and matches the
		// bucket-layout convention.
		//
		// Path: s3://<group-bucket>/<scope>/<user>/cache/<run-tag>/
		//   • Derived by parsing spec.WorkDir, which canonical CLI paths
		//     already follow:  s3://<bucket>/<scope>/<user>/workdir/<tag>/
		//   • On `--resume` + `--work-dir <prev>`, parsing the prev path
		//     produces the prev cache path → resume picks up the
		//     previous session's LevelDB → cached tasks skip.
		//
		// Future: a periodic ingestion job (admin-credentialed) will walk
		// each group bucket's cache/ prefixes and write task-hash → S3
		// pointers into a postgres-backed global index. That's the
		// "shared cache across users" plan, without giving any user
		// write access to a shared bucket.
		//
		// Skipped when WorkDir doesn't match the canonical pattern OR
		// isn't s3:// — operator-supplied custom paths don't get a
		// cache override (their pipelines run without -resume support,
		// which is the existing behaviour pre-cloudcache).
		if isS3URI(spec.WorkDir) {
			if cp := deriveCloudCachePath(spec.WorkDir); cp != "" {
				if staticEnv == nil {
					staticEnv = map[string]string{}
				}
				staticEnv["NXF_CLOUDCACHE_PATH"] = cp
				// The head always runs in a FRESH container, so Nextflow's
				// local run-history file (/local/.nextflow/history) is empty.
				// `-resume <uuid>` first calls HistoryFile.checkExistsById and
				// aborts with "Can't find a run with the specified id" BEFORE
				// it ever consults the cache — so cross-container resume can
				// never work without this. Resume restore itself reads only
				// the per-task hash blobs (keyed by the session UUID) from the
				// cloudcache, not the run index, so skipping the history check
				// is safe: the per-task cache lookup still validates each task.
				// Set whenever a cloudcache path exists (no effect off -resume).
				staticEnv["NXF_IGNORE_RESUME_HISTORY"] = "true"
				// Pin Nextflow's session UUID to the same deterministic value
				// used in `-resume <uuid>`. Without this, Nextflow generates a
				// fresh random UUID for each run and uses THAT to scope its
				// cache WRITES, even while reading from the pinned resume UUID.
				// Result: each run lands in a different cache namespace and
				// resume never accumulates — you always get a partial hit (only
				// the tasks whose hash happened to be written in a prior session
				// under a matching UUID). With NXF_UUID pinned to the same
				// deterministic value, every run in the work-dir lineage reads
				// AND writes under the same namespace → clean resume.
				if spec.PinnedSessionUUID != "" {
					staticEnv["NXF_UUID"] = spec.PinnedSessionUUID
				}
			}
		}
	}
	if spec.S5cmdSkipTLS {
		// Explicit override on the spec wins (e.g. saved pipeline or test).
		s5cmdSkipTLS = true
	}
	// User-supplied env (--env / --git-token) is injected last so it wins over
	// the derived monitoring/MinIO env in the head job's env block.
	for k, v := range spec.ExtraEnv {
		if staticEnv == nil {
			staticEnv = map[string]string{}
		}
		staticEnv[k] = v
	}
	return generateHeadJobHCLWithStaticEnvAndFlagsEx(spec, nomadAddr, nomadToken, runUUID, staticEnv, skipNomadVarCreds, s5cmdSkipTLS)
}

func generateHeadJobHCLWithStaticEnv(spec *PipelineSpec, nomadAddr, nomadToken, runUUID string, staticEnv map[string]string) string {
	return generateHeadJobHCLWithStaticEnvAndFlags(spec, nomadAddr, nomadToken, runUUID, staticEnv, false)
}

func generateHeadJobHCLWithStaticEnvAndFlags(spec *PipelineSpec, nomadAddr, nomadToken, runUUID string, staticEnv map[string]string, skipNomadVarCreds bool) string {
	return generateHeadJobHCLWithStaticEnvAndFlagsEx(spec, nomadAddr, nomadToken, runUUID, staticEnv, skipNomadVarCreds, false)
}

// generateHeadJobHCLWithStaticEnvAndFlagsEx is the canonical implementation.
// All other generateHeadJobHCL* helpers funnel into this one.
// s5cmdSkipTLS controls whether the generated nomad.s5cmd.s3 block includes
// `useTLS = false` — required on abc-seedling where MinIO uses a private CA.
func generateHeadJobHCLWithStaticEnvAndFlagsEx(spec *PipelineSpec, nomadAddr, nomadToken, runUUID string, staticEnv map[string]string, skipNomadVarCreds, s5cmdSkipTLS bool) string {
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
	// When S3 credentials are injected into the head job via staticEnv (i.e.
	// SkipNomadVarCreds == true), nf-nomad must mirror them onto every worker
	// job so processes can write .exitcode and result files back to the S3
	// work dir.
	//
	// SECURITY: split into two lists. AWS_ACCESS_KEY_ID / AWS_SECRET_ACCESS_KEY
	// are secrets — they go into `secretEnvPassthrough` (worker task env ONLY,
	// never job.meta — nf-nomad refuses to mirror these to meta). The endpoint
	// is non-secret runtime config and stays in identityEnvPassthrough alongside
	// abc_* identity fields, where it's available for jurist/xtdb correlation.
	var extraPassthrough []string  // identity — env + meta (non-secret)
	var secretPassthrough []string // secret — env only, never meta
	if skipNomadVarCreds {
		if staticEnv["NF_MINIO_ENDPOINT"] != "" {
			extraPassthrough = append(extraPassthrough, "NF_MINIO_ENDPOINT")
		}
		for _, k := range []string{"AWS_ACCESS_KEY_ID", "AWS_SECRET_ACCESS_KEY"} {
			if staticEnv[k] != "" {
				secretPassthrough = append(secretPassthrough, k)
			}
		}
	}
	return hclpipeline.Generate(hclpipeline.Spec{
		Name:                     spec.Name,
		WorkDir:                  spec.WorkDir,
		Params:                   spec.Params,
		CPU:                      spec.CPU,
		MemoryMB:                 spec.MemoryMB,
		HeadDiskMB:               spec.HeadDiskMB,
		NfVersion:                spec.NfVersion,
		NfPluginVersion:          spec.NfPluginVersion,
		Namespace:                spec.Namespace,
		Datacenters:              spec.Datacenters,
		Repository:               spec.Repository,
		Revision:                 spec.Revision,
		Profile:                  spec.Profile,
		ExtraConfig:              spec.ExtraConfig,
		Resume:                   spec.Resume,
		SessionID:                spec.SessionID,
		RunTag:                   spec.RunTag,
		NextflowRunName:          spec.NextflowRunName,
		PinnedSessionUUID:        spec.PinnedSessionUUID,
		PipelineSlug:             spec.PipelineSlug,
		HostVolume:               spec.HostVolume,
		NodeConstraint:           spec.NodeConstraint,
		PinWorkers:               spec.PinWorkers,
		WorkerExcludeHost:        spec.WorkerExcludeHost,
		HeadPool:                 spec.HeadPool,
		WorkerPool:               spec.WorkerPool,
		HeadNomadAddr:            spec.HeadNomadAddr,
		PluginBundleURL:          spec.PluginBundleURL,
		NextflowBinURL:           spec.NextflowBinURL,
		Plugins:                  plugins,
		ExtraBinaries:            bins,
		StaticEnv:                staticEnv,
		SkipNomadVarCreds:        skipNomadVarCreds,
		ExtraPassthroughEnvKeys:  extraPassthrough,
		SecretPassthroughEnvKeys: secretPassthrough,
		WaveEndpoint:             spec.WaveEndpoint,
		FusionEnabled:            spec.FusionEnabled,
		ContainerRuntime:         spec.ContainerRuntime,
		S5cmdSkipTLS:             s5cmdSkipTLS,
		Identity:                 resolveIdentity(spec),
	}, nomadAddr, nomadToken, runUUID)
}
