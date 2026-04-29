package module

import (
	"bytes"
	"text/template"

	"github.com/abc-cluster/abc-cluster-cli/cmd/utils"
	"github.com/hashicorp/hcl/v2/hclwrite"
	"github.com/zclconf/go-cty/cty"
)

// EmitSpec is the cluster-side input shape for `abc module samplesheet emit`.
// Keep it minimal: the emit job has no Nextflow run task, no S3 creds, no
// host-volume mount — its only side effect is writing the produced CSV
// into a Nomad Variable that the CLI reads back.
type EmitSpec struct {
	JobName string
	Module  string

	TaskDriver string

	PipelineGenRepo       string
	PipelineGenVersion    string
	PipelineGenURLBase    string
	PipelineGenURLResolve string
	GitHubToken           string

	NfVersion string

	Namespace   string
	Datacenters []string
}

// VariablePathForEmit is the Nomad Variable path under which the emit task
// publishes the CSV. Read it back via NomadClient.GetVariable. The job name
// is included so concurrent emit jobs don't clobber each other; callers
// that don't know the job name yet can compute it via the EmitSpec.
func VariablePathForEmit(jobName string) string {
	return "nomad/jobs/" + jobName + "/samplesheet/result"
}

// VariableKeyForEmit is the key inside the Items map at the variable path
// that holds the CSV content. Hardcoded to "csv" — the JSON shape is
// trivial and doesn't need a Spec field.
const VariableKeyForEmit = "csv"

func GenerateEmit(spec EmitSpec, nomadAddr, nomadToken, runUUID string) string {
	f := hclwrite.NewEmptyFile()
	root := f.Body()

	dcs := make([]cty.Value, len(spec.Datacenters))
	for i, dc := range spec.Datacenters {
		dcs[i] = cty.StringVal(dc)
	}

	jobBlock := root.AppendNewBlock("job", []string{spec.JobName})
	jobBody := jobBlock.Body()
	jobBody.SetAttributeValue("datacenters", cty.ListVal(dcs))
	jobBody.SetAttributeValue("type", cty.StringVal("batch"))
	if spec.Namespace != "" && spec.Namespace != "default" {
		jobBody.SetAttributeValue("namespace", cty.StringVal(spec.Namespace))
	}

	metaBody := jobBody.AppendNewBlock("meta", nil).Body()
	metaBody.SetAttributeValue("run_uuid", cty.StringVal(runUUID))
	metaBody.SetAttributeValue("module_name", cty.StringVal(spec.Module))
	metaBody.SetAttributeValue("kind", cty.StringVal("samplesheet-emit"))

	groupBody := jobBody.AppendNewBlock("group", []string{"emit"}).Body()

	// One reschedule + restart attempt is plenty — emit is a 30s read-only
	// scaffolding pass; if it fails twice it's a real bug worth surfacing.
	rescheduleBody := groupBody.AppendNewBlock("reschedule", nil).Body()
	rescheduleBody.SetAttributeValue("attempts", cty.NumberIntVal(1))
	rescheduleBody.SetAttributeValue("interval", cty.StringVal("10m"))
	rescheduleBody.SetAttributeValue("delay", cty.StringVal("15s"))
	rescheduleBody.SetAttributeValue("delay_function", cty.StringVal("constant"))
	rescheduleBody.SetAttributeValue("unlimited", cty.BoolVal(false))

	restartBody := groupBody.AppendNewBlock("restart", nil).Body()
	restartBody.SetAttributeValue("attempts", cty.NumberIntVal(1))
	restartBody.SetAttributeValue("interval", cty.StringVal("10m"))
	restartBody.SetAttributeValue("delay", cty.StringVal("15s"))
	restartBody.SetAttributeValue("mode", cty.StringVal("fail"))

	taskBody := groupBody.AppendNewBlock("task", []string{"emit"}).Body()
	taskBody.SetAttributeValue("driver", cty.StringVal(spec.TaskDriver))

	scriptTmpl := taskBody.AppendNewBlock("template", nil).Body()
	scriptTmpl.SetAttributeValue("destination", cty.StringVal("local/emit.sh"))
	scriptTmpl.SetAttributeValue("perms", cty.StringVal("755"))
	scriptTmpl.SetAttributeValue("data", cty.StringVal(buildEmitScript(spec)))

	cfg := taskBody.AppendNewBlock("config", nil).Body()
	cfg.SetAttributeValue("image", cty.StringVal("nextflow/nextflow:"+spec.NfVersion))
	cfg.SetAttributeValue("command", cty.StringVal("bash"))
	cfg.SetAttributeValue("args", cty.ListVal([]cty.Value{cty.StringVal("/local/emit.sh")}))

	envBody := taskBody.AppendNewBlock("env", nil).Body()
	envBody.SetAttributeValue("NOMAD_ADDR", cty.StringVal(nomadAddr))
	envBody.SetAttributeValue("NOMAD_TOKEN", cty.StringVal(nomadToken))
	// Namespace travels alongside the URL so the publish curl tags the
	// Variable correctly. Without this the Variable lands in `default`
	// while the job runs in (e.g.) `abc-services`, and the CLI's
	// GetVariable reads from the wrong namespace.
	if spec.Namespace != "" {
		envBody.SetAttributeValue("NOMAD_NAMESPACE", cty.StringVal(spec.Namespace))
	}
	envBody.SetAttributeValue("GITHUB_TOKEN", cty.StringVal(spec.GitHubToken))
	if spec.PipelineGenURLBase != "" {
		envBody.SetAttributeValue("ABC_PIPELINE_GEN_URL_BASE", cty.StringVal(spec.PipelineGenURLBase))
	}
	if spec.PipelineGenURLResolve != "" {
		envBody.SetAttributeValue("ABC_PIPELINE_GEN_URL_RESOLVE", cty.StringVal(spec.PipelineGenURLResolve))
	}

	res := taskBody.AppendNewBlock("resources", nil).Body()
	res.SetAttributeValue("cpu", cty.NumberIntVal(800))
	// Emit is a quick parse pass — JVM cold-start dominates; 512 MB is enough.
	res.SetAttributeValue("memory", cty.NumberIntVal(512))

	return utils.PrettyPrintHCL(string(f.Bytes()))
}

// emitScriptTmpl is the bash run by the emit task. Reuses the same
// JAR-fetch + modules-tarball-fetch logic as `module run` (jarfetch.go),
// but its only post-fetch action is to invoke the JAR's
// `--emit-samplesheet` mode and stash the resulting CSV in a Nomad
// Variable. No driver gen, no Nextflow, no host volume mount.
//
// Wraps the whole pipeline in a trap so that even on failure a `diag`
// item lands in the same Variable. Without this the CLI can't read alloc
// logs (the bootstrap token doesn't grant log-stream perms in our
// abc-services namespace), so a failed emit would be silent — the only
// signal would be the alloc's terminal status.
var emitScriptTmpl = template.Must(
	template.New("emit").Funcs(tmplFuncs).Parse(
		`#!/usr/bin/env bash

MODULES_DIR="/local/modules-src"
JAR_PATH="/local/pipeline-gen.jar"
MODULES_TGZ="/local/modules.tgz"
CSV_FILE="/local/samplesheet.csv"
LOG_FILE="/local/emit.log"
PIPELINE_GEN_REPO={{ shellQuote .PipelineGenRepo }}
PIPELINE_GEN_VERSION={{ shellQuote .PipelineGenVersion }}
MODULE_NAME={{ shellQuote .Module }}
VAR_PATH={{ shellQuote .VarPath }}
VAR_KEY={{ shellQuote .VarKey }}

rm -rf "$MODULES_DIR" "$JAR_PATH" "$MODULES_TGZ" "$CSV_FILE" "$LOG_FILE"
: > "$LOG_FILE"

# publish_var: ship the current state of CSV_FILE + tail of LOG_FILE into
# the Nomad Variable. Called both as an EXIT trap (so failures surface
# even when alloc-log read is denied) AND as an explicit "alive" beacon
# right after the script starts (so the CLI sees a Variable even if a
# later step hangs the alloc completely). JSON building uses python3,
# which the nextflow image carries — far less error-prone than awk
# escapes for tabs / newlines / embedded quotes.
publish_var() {
  set +e
  python3 - "$VAR_PATH" "$VAR_KEY" "$CSV_FILE" "$LOG_FILE" "${1:-0}" >/local/payload.json 2>>"$LOG_FILE" <<'PY'
import json, os, sys
var_path, var_key, csv_file, log_file, rc = sys.argv[1:6]
csv = ""
if rc == "0" and os.path.exists(csv_file) and os.path.getsize(csv_file) > 0:
    with open(csv_file, "r", encoding="utf-8", errors="replace") as fh:
        csv = fh.read()
diag = ""
try:
    with open(log_file, "rb") as fh:
        fh.seek(0, 2); size = fh.tell()
        fh.seek(max(0, size - 16000))
        diag = fh.read().decode("utf-8", errors="replace")
except FileNotFoundError:
    pass
print(json.dumps({"Path": var_path, "Items": {var_key: csv, "diag": diag, "exit_code": rc}}))
PY
  ns_qs=""
  if [ -n "${NOMAD_NAMESPACE:-}" ]; then ns_qs="?namespace=$NOMAD_NAMESPACE"; fi
  curl -fsSL -X PUT \
    -H "X-Nomad-Token: $NOMAD_TOKEN" \
    -H "Content-Type: application/json" \
    --data-binary "@/local/payload.json" \
    "${NOMAD_ADDR%/}/v1/var/$VAR_PATH$ns_qs" >/dev/null 2>>"$LOG_FILE" \
    || echo "WARN: variable publish failed (rc=${1:-0})" >>"$LOG_FILE"
}

trap 'publish_var "$?"' EXIT

# Beacon — so even a fatal early failure has a Variable to inspect.
echo "emit task starting (module=$MODULE_NAME version=$PIPELINE_GEN_VERSION)" >>"$LOG_FILE"
publish_var "0"

set -euo pipefail
exec >>"$LOG_FILE" 2>&1

{{ .JarFetch }}
{{ .ModulesFetch }}

java -jar "$JAR_PATH" module "$MODULE_NAME" \
  --modules-dir "$MODULES_DIR" \
  --emit-samplesheet "$CSV_FILE"
`))

type emitScriptData struct {
	PipelineGenRepo    string
	PipelineGenVersion string
	Module             string
	VarPath            string
	VarKey             string
	JarFetch           string
	ModulesFetch       string
}

func buildEmitScript(spec EmitSpec) string {
	data := emitScriptData{
		PipelineGenRepo:    spec.PipelineGenRepo,
		PipelineGenVersion: spec.PipelineGenVersion,
		Module:             spec.Module,
		VarPath:            VariablePathForEmit(spec.JobName),
		VarKey:             VariableKeyForEmit,
		JarFetch:           PipelineGenJarFetchScript(),
		ModulesFetch:       NfCoreModulesFetchScript(),
	}
	var buf bytes.Buffer
	if err := emitScriptTmpl.Execute(&buf, data); err != nil {
		panic("emitScriptTmpl: " + err.Error())
	}
	return buf.String()
}
