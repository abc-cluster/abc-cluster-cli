package pipeline

import (
	"encoding/json"
	"fmt"
	"strings"

	"github.com/abc-cluster/abc-cluster-cli/cmd/utils"
	"github.com/hashicorp/hcl/v2/hclwrite"
	"github.com/zclconf/go-cty/cty"
)

type Spec struct {
	Name string

	WorkDir string
	Params  map[string]any

	CPU             int
	MemoryMB        int
	NfVersion       string
	NfPluginVersion string

	Namespace   string
	Datacenters []string

	Repository  string
	Revision    string
	Profile     string
	ExtraConfig string

	// Resume appends -resume to the nextflow run command (checkpoint restart).
	Resume bool
	// SessionID resumes a specific Nextflow session (implies Resume).
	SessionID string

	// HostVolume is the Nomad host volume name used for the shared work directory.
	// Defaults to "nextflow-work". Override with the name of any host volume
	// available on the target nodes (e.g. "scratch").
	// Set to "-" to skip the host volume block entirely (use with S3 work dirs).
	HostVolume string

	// NodeConstraint pins the head job to a specific Nomad node hostname.
	// When set, a constraint { attribute = "${attr.unique.hostname}" value = "<node>" }
	// block is added to the group.
	NodeConstraint string

	// StaticEnv is merged into the task env block as literal strings (abc-nodes
	// enhanced floor: Loki, Prometheus, Grafana Alloy).
	StaticEnv map[string]string
}

// generateHeadJobHCL produces a Nomad HCL job spec for a Nextflow head job
// from the given PipelineSpec and runtime credentials. runUUID must be a fresh
// unique identifier on every submission (prevents Nomad duplicate-job skip).
func Generate(spec Spec, nomadAddr, nomadToken, runUUID string) string {
	f := hclwrite.NewEmptyFile()
	root := f.Body()

	jobName := "nextflow-head"
	if spec.Name != "" {
		jobName = spec.Name
	}

	dcs := make([]cty.Value, len(spec.Datacenters))
	for i, dc := range spec.Datacenters {
		dcs[i] = cty.StringVal(dc)
	}

	jobBlock := root.AppendNewBlock("job", []string{jobName})
	jobBody := jobBlock.Body()
	jobBody.SetAttributeValue("datacenters", cty.ListVal(dcs))
	jobBody.SetAttributeValue("type", cty.StringVal("batch"))
	if spec.Namespace != "" && spec.Namespace != "default" {
		jobBody.SetAttributeValue("namespace", cty.StringVal(spec.Namespace))
	}

	// run_uuid forces a new allocation on each submission.
	metaBody := jobBody.AppendNewBlock("meta", nil).Body()
	metaBody.SetAttributeValue("run_uuid", cty.StringVal(runUUID))
	if len(spec.StaticEnv) > 0 {
		metaBody.SetAttributeValue("abc_monitoring_floor", cty.StringVal("enhanced"))
	}

	groupBody := jobBody.AppendNewBlock("group", []string{"head"}).Body()

	// Node hostname constraint — pins the head job to a specific Nomad client.
	if spec.NodeConstraint != "" {
		cBody := groupBody.AppendNewBlock("constraint", nil).Body()
		cBody.SetAttributeValue("attribute", cty.StringVal("${attr.unique.hostname}"))
		cBody.SetAttributeValue("value", cty.StringVal(spec.NodeConstraint))
	}

	// Host volume for the shared work directory (optional; skip when using S3 work dir).
	hostVol := spec.HostVolume
	if hostVol == "" {
		hostVol = "nextflow-work" // default
	}
	useHostVol := spec.HostVolume != "-" // "-" explicitly disables the host volume
	if useHostVol {
		volBody := groupBody.AppendNewBlock("volume", []string{hostVol}).Body()
		volBody.SetAttributeValue("type", cty.StringVal("host"))
		volBody.SetAttributeValue("source", cty.StringVal(hostVol))
	}

	taskBody := groupBody.AppendNewBlock("task", []string{"nextflow"}).Body()
	taskBody.SetAttributeValue("driver", cty.StringVal("docker"))

	// Resources
	resBody := taskBody.AppendNewBlock("resources", nil).Body()
	resBody.SetAttributeValue("cpu", cty.NumberIntVal(int64(spec.CPU)))
	resBody.SetAttributeValue("memory", cty.NumberIntVal(int64(spec.MemoryMB)))

	// Volume mount (only when a host volume is in use).
	if useHostVol && spec.WorkDir != "" && !isS3URI(spec.WorkDir) {
		mountBody := taskBody.AppendNewBlock("volume_mount", nil).Body()
		mountBody.SetAttributeValue("volume", cty.StringVal(hostVol))
		mountBody.SetAttributeValue("destination", cty.StringVal(spec.WorkDir))
		mountBody.SetAttributeValue("read_only", cty.BoolVal(false))
	}

	// Template: nextflow config
	nfCfgTmpl := taskBody.AppendNewBlock("template", nil).Body()
	nfCfgTmpl.SetAttributeValue("destination", cty.StringVal("local/nextflow.headjob.config"))
	nfCfgTmpl.SetAttributeValue("data", cty.StringVal(buildNextflowConfig(spec)))

	// Template: AWS credentials from Nomad Variables (gracefully absent if not set)
	awsVarPath := fmt.Sprintf("nomad/jobs/%s/head/nextflow", jobName)
	awsTmpl := taskBody.AppendNewBlock("template", nil).Body()
	awsTmpl.SetAttributeValue("destination", cty.StringVal("secrets/aws.env"))
	awsTmpl.SetAttributeValue("env", cty.BoolVal(true))
	awsTmpl.SetAttributeValue("data", cty.StringVal(
		fmt.Sprintf("{{- with nomadVar %q -}}\nAWS_ACCESS_KEY_ID={{ .AWS_ACCESS_KEY_ID }}\nAWS_SECRET_ACCESS_KEY={{ .AWS_SECRET_ACCESS_KEY }}\n{{- end }}\n", awsVarPath),
	))

	// Template: params.json (only when pipeline params are provided)
	if len(spec.Params) > 0 {
		paramsJSON, _ := json.Marshal(spec.Params)
		paramsTmpl := taskBody.AppendNewBlock("template", nil).Body()
		paramsTmpl.SetAttributeValue("destination", cty.StringVal("local/params.json"))
		paramsTmpl.SetAttributeValue("data", cty.StringVal(string(paramsJSON)))
	}

	// Template: entrypoint script
	entrypointTmpl := taskBody.AppendNewBlock("template", nil).Body()
	entrypointTmpl.SetAttributeValue("destination", cty.StringVal("local/entrypoint.sh"))
	entrypointTmpl.SetAttributeValue("perms", cty.StringVal("755"))
	entrypointTmpl.SetAttributeValue("data", cty.StringVal(buildEntrypoint(spec)))

	// Docker config
	cfgBody := taskBody.AppendNewBlock("config", nil).Body()
	cfgBody.SetAttributeValue("image", cty.StringVal("nextflow/nextflow:"+spec.NfVersion))
	cfgBody.SetAttributeValue("work_dir", cty.StringVal("/local"))
	cfgBody.SetAttributeValue("command", cty.StringVal("bash"))
	cfgBody.SetAttributeValue("args", cty.ListVal([]cty.Value{cty.StringVal("/local/entrypoint.sh")}))

	// Environment
	envBody := taskBody.AppendNewBlock("env", nil).Body()
	envBody.SetAttributeValue("NOMAD_ADDR", cty.StringVal(nomadAddr))
	envBody.SetAttributeValue("NOMAD_TOKEN", cty.StringVal(nomadToken))
	for _, k := range utils.SortedKeys(spec.StaticEnv) {
		envBody.SetAttributeValue(k, cty.StringVal(spec.StaticEnv[k]))
	}

	return utils.PrettyPrintHCL(string(f.Bytes()))
}

// isS3URI returns true if the path starts with s3:// or s3a://.
func isS3URI(path string) bool {
	return strings.HasPrefix(path, "s3://") || strings.HasPrefix(path, "s3a://")
}

// buildNextflowConfig generates the Groovy nextflow config embedded in the
// head job. It closely mirrors nextflow.headjob.config from the infra scripts.
func buildNextflowConfig(spec Spec) string {
	var sb strings.Builder

	// nf-nomad volumes block: omit when work dir is S3 (no shared local disk needed).
	hostVol := spec.HostVolume
	if hostVol == "" || hostVol == "-" {
		hostVol = "nextflow-work"
	}
	volumesLine := fmt.Sprintf(`volumes = [{ type "host" name "%s" path "%s" }]`, hostVol, spec.WorkDir)
	if isS3URI(spec.WorkDir) || spec.HostVolume == "-" {
		volumesLine = `volumes = []`
	}

	// Per-process Nomad constraint via the `constraints` process directive.
	// Note: nf-nomad 0.4.0-edge3 requires the `constraints` value to be a Closure.
	// Nextflow's config-file parser converts `constraints { ... }` blocks to Maps,
	// so we MUST use property-assignment form (`= { ... }`) which preserves the closure.
	processConstraint := ""
	if spec.NodeConstraint != "" {
		processConstraint = fmt.Sprintf(`

process {
  constraints = { node { unique = [name: '%s'] } }
}
`, spec.NodeConstraint)
	}

	fmt.Fprintf(&sb, `plugins {
  id "nf-nomad@%s"
}

docker {
  enabled = true
}

process {
  executor      = "nomad"
  errorStrategy = "retry"
  maxRetries    = 1
}

workDir = "%s"

aws {
  accessKey = System.getenv("AWS_ACCESS_KEY_ID") ?: ""
  secretKey = System.getenv("AWS_SECRET_ACCESS_KEY") ?: ""
  client {
    endpoint         = System.getenv("NF_MINIO_ENDPOINT") ?: "http://localhost:9000"
    s3PathStyleAccess = true
    protocol         = "https"
  }
}

nomad {
  client {
    address        = System.getenv("NOMAD_ADDR") ?: "http://127.0.0.1:4646"
    token          = System.getenv("NOMAD_TOKEN") ?: ""
    pollInterval   = "2s"
    submitThrottle = "100ms"
  }
  jobs {
    namespace                = "%s"
    deleteOnCompletion       = false
    cpuMode                  = "cores"
    failOnPlacementFailure   = true
    placementFailureTimeout  = "5m"
    %s
    failures = [
      restart   : [attempts: 1, mode: "fail"],
      reschedule: [attempts: 1]
    ]
  }
}
%s`, spec.NfPluginVersion, spec.WorkDir, spec.Namespace, volumesLine, processConstraint)

	if spec.ExtraConfig != "" {
		sb.WriteString("\n")
		sb.WriteString(spec.ExtraConfig)
	}
	return sb.String()
}

// buildEntrypoint generates the bash entrypoint script for the head job.
func buildEntrypoint(spec Spec) string {
	var sb strings.Builder
	sb.WriteString("#!/usr/bin/env bash\nset -euo pipefail\ncd /local\n\n")
	// NXF_HOME must be a local writable path even when workDir is S3.
	nxfHome := spec.WorkDir + "/.nxf-home"
	if isS3URI(spec.WorkDir) {
		nxfHome = "/local/.nxf-home"
	}
	fmt.Fprintf(&sb, "export NXF_ANSI_LOG=false\nexport NXF_HOME=%s\n\n", nxfHome)
	fmt.Fprintf(&sb, "nextflow run %s \\\n", spec.Repository)
	sb.WriteString("  -c /local/nextflow.headjob.config")
	if spec.Revision != "" {
		fmt.Fprintf(&sb, " \\\n  -revision %s", spec.Revision)
	}
	if spec.Profile != "" {
		fmt.Fprintf(&sb, " \\\n  -profile %s", spec.Profile)
	}
	if spec.Resume || spec.SessionID != "" {
		sb.WriteString(" \\\n  -resume")
	}
	if spec.SessionID != "" {
		fmt.Fprintf(&sb, " \\\n  -sessionId %s", spec.SessionID)
	}
	if len(spec.Params) > 0 {
		sb.WriteString(" \\\n  -params-file /local/params.json")
	}
	sb.WriteString("\n")
	return sb.String()
}
