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

	// Host volume for the shared work directory.
	volBody := groupBody.AppendNewBlock("volume", []string{"nextflow-work"}).Body()
	volBody.SetAttributeValue("type", cty.StringVal("host"))
	volBody.SetAttributeValue("source", cty.StringVal("nextflow-work"))

	taskBody := groupBody.AppendNewBlock("task", []string{"nextflow"}).Body()
	taskBody.SetAttributeValue("driver", cty.StringVal("docker"))

	// Resources
	resBody := taskBody.AppendNewBlock("resources", nil).Body()
	resBody.SetAttributeValue("cpu", cty.NumberIntVal(int64(spec.CPU)))
	resBody.SetAttributeValue("memory", cty.NumberIntVal(int64(spec.MemoryMB)))

	// Volume mount
	mountBody := taskBody.AppendNewBlock("volume_mount", nil).Body()
	mountBody.SetAttributeValue("volume", cty.StringVal("nextflow-work"))
	mountBody.SetAttributeValue("destination", cty.StringVal(spec.WorkDir))
	mountBody.SetAttributeValue("read_only", cty.BoolVal(false))

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

// buildNextflowConfig generates the Groovy nextflow config embedded in the
// head job. It closely mirrors nextflow.headjob.config from the infra scripts.
func buildNextflowConfig(spec Spec) string {
	var sb strings.Builder
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
    namespace                = "default"
    deleteOnCompletion       = false
    cpuMode                  = "cores"
    failOnPlacementFailure   = true
    placementFailureTimeout  = "5m"
    volumes = [{ type "host" name "nextflow-work" path "%s" }]
    failures = [
      restart   : [attempts: 1, mode: "fail"],
      reschedule: [attempts: 1]
    ]
  }
}
`, spec.NfPluginVersion, spec.WorkDir, spec.WorkDir)

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
	fmt.Fprintf(&sb, "export NXF_ANSI_LOG=false\nexport NXF_HOME=%s/.nxf-home\n\n", spec.WorkDir)
	fmt.Fprintf(&sb, "nextflow run %s \\\n", spec.Repository)
	sb.WriteString("  -c /local/nextflow.headjob.config")
	if spec.Revision != "" {
		fmt.Fprintf(&sb, " \\\n  -revision %s", spec.Revision)
	}
	if spec.Profile != "" {
		fmt.Fprintf(&sb, " \\\n  -profile %s", spec.Profile)
	}
	if len(spec.Params) > 0 {
		sb.WriteString(" \\\n  -params-file /local/params.json")
	}
	sb.WriteString("\n")
	return sb.String()
}
