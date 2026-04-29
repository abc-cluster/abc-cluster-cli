package module

import (
	"bytes"
	"encoding/base64"
	"fmt"
	"path/filepath"
	"strings"
	"text/template"

	"github.com/abc-cluster/abc-cluster-cli/cmd/utils"
	"github.com/hashicorp/hcl/v2/hclwrite"
	"github.com/zclconf/go-cty/cty"
)

type Spec struct {
	JobName string
	Module  string

	Profile      string
	WorkDir      string
	HostVolume   string
	OutputPrefix string

	// TaskDriver controls the Nomad task driver for both prestart and run
	// tasks. Default "docker"; set to "containerd-driver" to target nodes
	// like aither that don't run docker. Also propagated into the cluster
	// nextflow.config as `nomad.jobs.taskDriver` so nf-nomad emits child
	// Nomad jobs with the matching driver.
	TaskDriver string

	// NfPluginZipURL: when set, the run task curl-fetches this zip and
	// unpacks it under NXF_PLUGINS_DIR before nextflow starts. Lets us ship
	// a patched nf-nomad plugin alongside the JAR mirror.
	NfPluginZipURL string

	PipelineGenRepo    string
	PipelineGenVersion string
	// PipelineGenURLBase, when set, makes the prestart fetch the JAR directly
	// from <base>/<version>/pipeline-gen.jar instead of resolving via the
	// GitHub releases API. Mirrors the abc-node-probe RustFS-mirror pattern.
	PipelineGenURLBase string

	// PipelineGenURLResolve: optional `host:port:ip` override passed to curl
	// via `--resolve`. Useful when the URL hostname only resolves via
	// Tailscale magicDNS on the host but containerd-driver containers don't
	// have the Tailscale resolver in their /etc/resolv.conf.
	PipelineGenURLResolve string
	ModuleRevision     string
	GitHubToken        string

	CPU      int
	MemoryMB int

	NfVersion       string
	NfPluginVersion string

	Namespace   string
	Datacenters []string

	S3Endpoint  string
	S3AccessKey string
	S3SecretKey string

	ParamsYAMLContent string
	ConfigYAMLContent string

	// SamplesheetCSVContent is the user's local samplesheet (CSV bytes), to
	// be staged into the prestart task and validated against the module's
	// meta.yml before driver generation. Empty disables the validation step.
	SamplesheetCSVContent string

	// PipelineGenNoRunManifest, when true, passes --no-run-manifest to nf-pipeline-gen.
	PipelineGenNoRunManifest bool

	// TestMode runs the module against its bundled tests/main.nf.test fixtures
	// (staged from nf-core/test-datasets). Sets ABC_MODULE_TEST_MODE=1 in the
	// prestart task so the script emits a minimal valid params stub and lets the
	// generated test profile drive inputs at Nextflow runtime.
	TestMode bool

}

// ---------------------------------------------------------------------------
// HCL generation
// ---------------------------------------------------------------------------

func Generate(spec Spec, nomadAddr, nomadToken, runUUID string) string {
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

	groupBody := jobBody.AppendNewBlock("group", []string{"run"}).Body()

	// Eviction tolerance — overrides Nomad batch defaults (1 reschedule, 3
	// restarts in fail mode). When a task lands on a spot node that gets
	// reclaimed mid-run, the batch default would fail the whole job after
	// the local restarts ran out. With these settings:
	//   - up to 5 fresh allocations (rescheduling) over 1h, attempting a
	//     completely new placement each time;
	//   - 2 in-place restarts before each reschedule (covers transient
	//     OOM / image-pull / network blips).
	rescheduleBody := groupBody.AppendNewBlock("reschedule", nil).Body()
	rescheduleBody.SetAttributeValue("attempts", cty.NumberIntVal(5))
	rescheduleBody.SetAttributeValue("interval", cty.StringVal("1h"))
	rescheduleBody.SetAttributeValue("delay", cty.StringVal("30s"))
	rescheduleBody.SetAttributeValue("delay_function", cty.StringVal("exponential"))
	rescheduleBody.SetAttributeValue("max_delay", cty.StringVal("5m"))
	rescheduleBody.SetAttributeValue("unlimited", cty.BoolVal(false))

	restartBody := groupBody.AppendNewBlock("restart", nil).Body()
	restartBody.SetAttributeValue("attempts", cty.NumberIntVal(2))
	restartBody.SetAttributeValue("interval", cty.StringVal("30m"))
	restartBody.SetAttributeValue("delay", cty.StringVal("30s"))
	restartBody.SetAttributeValue("mode", cty.StringVal("fail"))

	volBody := groupBody.AppendNewBlock("volume", []string{spec.HostVolume}).Body()
	volBody.SetAttributeValue("type", cty.StringVal("host"))
	volBody.SetAttributeValue("source", cty.StringVal(spec.HostVolume))

	// ---- prestart: generate module driver with nf-pipeline-gen ----
	genTaskBody := groupBody.AppendNewBlock("task", []string{"generate"}).Body()
	genTaskBody.SetAttributeValue("driver", cty.StringVal(spec.TaskDriver))

	lifecycle := genTaskBody.AppendNewBlock("lifecycle", nil).Body()
	lifecycle.SetAttributeValue("hook", cty.StringVal("prestart"))
	lifecycle.SetAttributeValue("sidecar", cty.BoolVal(false))

	genMount := genTaskBody.AppendNewBlock("volume_mount", nil).Body()
	genMount.SetAttributeValue("volume", cty.StringVal(spec.HostVolume))
	genMount.SetAttributeValue("destination", cty.StringVal(spec.WorkDir))
	genMount.SetAttributeValue("read_only", cty.BoolVal(false))

	genScriptTmpl := genTaskBody.AppendNewBlock("template", nil).Body()
	genScriptTmpl.SetAttributeValue("destination", cty.StringVal("local/generate.sh"))
	genScriptTmpl.SetAttributeValue("perms", cty.StringVal("755"))
	genScriptTmpl.SetAttributeValue("data", cty.StringVal(buildGenerateScript(spec)))

	genCfg := genTaskBody.AppendNewBlock("config", nil).Body()
	genCfg.SetAttributeValue("image", cty.StringVal("nextflow/nextflow:"+spec.NfVersion))
	genCfg.SetAttributeValue("command", cty.StringVal("bash"))
	genCfg.SetAttributeValue("args", cty.ListVal([]cty.Value{cty.StringVal("/local/generate.sh")}))

	genEnv := genTaskBody.AppendNewBlock("env", nil).Body()
	genEnv.SetAttributeValue("GITHUB_TOKEN", cty.StringVal(spec.GitHubToken))
	if spec.PipelineGenURLBase != "" {
		genEnv.SetAttributeValue("ABC_PIPELINE_GEN_URL_BASE", cty.StringVal(spec.PipelineGenURLBase))
	}
	if spec.PipelineGenURLResolve != "" {
		genEnv.SetAttributeValue("ABC_PIPELINE_GEN_URL_RESOLVE", cty.StringVal(spec.PipelineGenURLResolve))
	}
	if spec.ModuleRevision != "" {
		genEnv.SetAttributeValue("ABC_MODULE_REVISION", cty.StringVal(spec.ModuleRevision))
	}
	if spec.ParamsYAMLContent != "" {
		genEnv.SetAttributeValue("ABC_MODULE_PARAMS_B64", cty.StringVal(base64.StdEncoding.EncodeToString([]byte(spec.ParamsYAMLContent))))
	}
	if spec.ConfigYAMLContent != "" {
		genEnv.SetAttributeValue("ABC_MODULE_CONFIG_B64", cty.StringVal(base64.StdEncoding.EncodeToString([]byte(spec.ConfigYAMLContent))))
	}
	if spec.SamplesheetCSVContent != "" {
		genEnv.SetAttributeValue("ABC_SAMPLESHEET_B64", cty.StringVal(base64.StdEncoding.EncodeToString([]byte(spec.SamplesheetCSVContent))))
	}
	if spec.TestMode {
		genEnv.SetAttributeValue("ABC_MODULE_TEST_MODE", cty.StringVal("1"))
	}

	genRes := genTaskBody.AppendNewBlock("resources", nil).Body()
	genRes.SetAttributeValue("cpu", cty.NumberIntVal(800))
	// Prestart just downloads tarballs and runs the JAR; 1 GB is plenty.
	genRes.SetAttributeValue("memory", cty.NumberIntVal(1024))

	// ---- main: run generated module driver ----
	runTaskBody := groupBody.AppendNewBlock("task", []string{"nextflow"}).Body()
	runTaskBody.SetAttributeValue("driver", cty.StringVal(spec.TaskDriver))

	runMount := runTaskBody.AppendNewBlock("volume_mount", nil).Body()
	runMount.SetAttributeValue("volume", cty.StringVal(spec.HostVolume))
	runMount.SetAttributeValue("destination", cty.StringVal(spec.WorkDir))
	runMount.SetAttributeValue("read_only", cty.BoolVal(false))

	// S3 creds (if any) are injected directly into the run task env block
	// below, alongside NF_S3_ENDPOINT. No Nomad Variable lookup, no secrets/
	// templates. This keeps the credential flow explicit and per-submit.

	runCfgTmpl := runTaskBody.AppendNewBlock("template", nil).Body()
	runCfgTmpl.SetAttributeValue("destination", cty.StringVal("local/module-run.nextflow.config"))
	runCfgTmpl.SetAttributeValue("data", cty.StringVal(buildClusterNextflowConfig(spec)))

	runScriptTmpl := runTaskBody.AppendNewBlock("template", nil).Body()
	runScriptTmpl.SetAttributeValue("destination", cty.StringVal("local/run.sh"))
	runScriptTmpl.SetAttributeValue("perms", cty.StringVal("755"))
	runScriptTmpl.SetAttributeValue("data", cty.StringVal(buildRunScript(spec)))

	runCfg := runTaskBody.AppendNewBlock("config", nil).Body()
	runCfg.SetAttributeValue("image", cty.StringVal("nextflow/nextflow:"+spec.NfVersion))
	runCfg.SetAttributeValue("command", cty.StringVal("bash"))
	runCfg.SetAttributeValue("args", cty.ListVal([]cty.Value{cty.StringVal("/local/run.sh")}))

	runEnv := runTaskBody.AppendNewBlock("env", nil).Body()
	runEnv.SetAttributeValue("NOMAD_ADDR", cty.StringVal(nomadAddr))
	runEnv.SetAttributeValue("NOMAD_TOKEN", cty.StringVal(nomadToken))
	runEnv.SetAttributeValue("NXF_ANSI_LOG", cty.StringVal("false"))
	if spec.S3Endpoint != "" {
		runEnv.SetAttributeValue("NF_S3_ENDPOINT", cty.StringVal(spec.S3Endpoint))
	}
	if spec.S3AccessKey != "" {
		runEnv.SetAttributeValue("AWS_ACCESS_KEY_ID", cty.StringVal(spec.S3AccessKey))
	}
	if spec.S3SecretKey != "" {
		runEnv.SetAttributeValue("AWS_SECRET_ACCESS_KEY", cty.StringVal(spec.S3SecretKey))
	}

	runRes := runTaskBody.AppendNewBlock("resources", nil).Body()
	runRes.SetAttributeValue("cpu", cty.NumberIntVal(int64(spec.CPU)))
	runRes.SetAttributeValue("memory", cty.NumberIntVal(int64(spec.MemoryMB)))

	return utils.PrettyPrintHCL(string(f.Bytes()))
}

// ---------------------------------------------------------------------------
// Text templates for the embedded shell / Groovy scripts
// ---------------------------------------------------------------------------

// shellQuote wraps a string in double quotes, escaping as Go %q does. This
// produces valid POSIX shell string literals for values that may contain spaces.
func shellQuote(s string) string { return fmt.Sprintf("%q", s) }

var tmplFuncs = template.FuncMap{"shellQuote": shellQuote}

// generateScriptTmpl is the prestart bash script that downloads nf-pipeline-gen,
// fetches the nf-core/modules tarball, optionally validates a user-supplied
// samplesheet, and then generates the driver pipeline.
//
// `{{ .JarFetch }}` and `{{ .ModulesFetch }}` are filled in from
// PipelineGenJarFetchScript / NfCoreModulesFetchScript so the same fetch
// logic can be reused by the `samplesheet emit` job (jarfetch.go).
var generateScriptTmpl = template.Must(
	template.New("generate").Funcs(tmplFuncs).Parse(
		`#!/usr/bin/env bash
set -euo pipefail

RUN_ID="$(date -u +%Y%m%dT%H%M%SZ)-$$"
MODULES_DIR="/local/modules-src"
JAR_PATH="/local/pipeline-gen.jar"
MODULES_TGZ="/local/modules.tgz"
PARAMS_FILE="/local/params.yml"
MODULE_CONFIG="/local/module.config"
SAMPLESHEET_FILE="/local/samplesheet.csv"
OUTDIR={{ shellQuote .GenOutdirExpr }}
STATE_FILE={{ shellQuote .StateFile }}
PIPELINE_GEN_REPO={{ shellQuote .PipelineGenRepo }}
PIPELINE_GEN_VERSION={{ shellQuote .PipelineGenVersion }}
MODULE_NAME={{ shellQuote .Module }}
OUTPUT_PREFIX={{ shellQuote .OutputPrefix }}

rm -rf "$OUTDIR" "$MODULES_DIR" "$JAR_PATH" "$MODULES_TGZ" "$SAMPLESHEET_FILE"

{{ .JarFetch }}
{{ .ModulesFetch }}
MODULE_DIR="$MODULES_DIR/modules/$MODULE_NAME"
if [ ! -d "$MODULE_DIR" ]; then
  MODULE_DIR="$MODULES_DIR/$MODULE_NAME"
fi
if [ ! -d "$MODULE_DIR" ]; then
  echo "Module source not found for $MODULE_NAME" >&2
  exit 1
fi
META_FILE="$MODULE_DIR/meta.yml"
if [ ! -f "$META_FILE" ]; then
  echo "Missing meta.yml for $MODULE_NAME" >&2
  exit 1
fi

if [ "${ABC_MODULE_TEST_MODE:-}" = "1" ]; then
  echo ">> Test mode: bundled module tests will run; fixtures are staged from nf-core/test-datasets at runtime."
  if [ ! -d "$MODULE_DIR/tests" ]; then
    echo "WARNING: $MODULE_NAME has no tests/ directory; test profile will be empty." >&2
  fi
fi

if [ -n "${ABC_MODULE_PARAMS_B64:-}" ]; then
  python3 - "$PARAMS_FILE" <<'PY'
import base64
import os
import sys
raw = os.environ.get("ABC_MODULE_PARAMS_B64", "")
with open(sys.argv[1], "wb") as fh:
    fh.write(base64.b64decode(raw))
PY
else
  python3 - "$META_FILE" "$PARAMS_FILE" "$OUTPUT_PREFIX" "$RUN_ID" <<'PY'
import re
import sys

meta_file, params_file, output_prefix, run_id = sys.argv[1:5]
names = []
in_input = False
with open(meta_file, encoding='utf-8') as fh:
    for raw in fh:
        s = raw.strip()
        if s.startswith('input:'):
            in_input = True
            continue
        if in_input and s.startswith('output:'):
            break
        if not in_input or not s.startswith('-'):
            continue
        m = re.search(r'([A-Za-z0-9_]+):\s*$', s)
        if not m:
            continue
        name = m.group(1)
        if name not in names:
            names.append(name)

prefix = output_prefix.rstrip('/')
with open(params_file, 'w', encoding='utf-8') as out:
    out.write('meta:\n')
    out.write('  id: test\n')
    out.write('  single_end: false\n')
    for name in names:
        if name == 'meta':
            continue
        out.write(f'{name}: "placeholder.txt"\n')
    out.write(f'outdir: "{prefix}/{run_id}"\n')
    out.write(f'output_dir: "{prefix}/{run_id}"\n')
PY
fi

if [ -n "${ABC_MODULE_CONFIG_B64:-}" ]; then
  python3 - "$MODULE_CONFIG" <<'PY'
import base64
import os
import sys
raw = os.environ.get("ABC_MODULE_CONFIG_B64", "")
with open(sys.argv[1], "wb") as fh:
    fh.write(base64.b64decode(raw))
PY
else
  : > "$MODULE_CONFIG"
fi

# Cluster-side samplesheet validation. Decoded from ABC_SAMPLESHEET_B64 (set
# by the CLI when the user passed --samplesheet). The JAR's
# --validate-samplesheet re-reads meta.yml and re-checks every cell — the Go
# side only does a shallow shape check before submission. If we're here and
# the sheet is bad, fail the alloc fast before driver gen / Nextflow.
if [ -n "${ABC_SAMPLESHEET_B64:-}" ]; then
  python3 - "$SAMPLESHEET_FILE" <<'PY'
import base64
import os
import sys
raw = os.environ.get("ABC_SAMPLESHEET_B64", "")
with open(sys.argv[1], "wb") as fh:
    fh.write(base64.b64decode(raw))
PY
  echo ">> Validating samplesheet against $MODULE_NAME meta.yml..."
  if ! java -jar "$JAR_PATH" module "$MODULE_NAME" \
        --modules-dir "$MODULES_DIR" \
        --validate-samplesheet "$SAMPLESHEET_FILE"; then
    echo "Samplesheet validation failed; aborting before driver generation." >&2
    exit 1
  fi
  echo ">> Samplesheet OK"
fi

java -jar "$JAR_PATH" module \
  --modules-dir "$MODULES_DIR" \
  --params-file "$PARAMS_FILE" \
  --config-file "$MODULE_CONFIG" \
  --revision "$MODULES_REVISION" \
  --outdir "$OUTDIR" \
{{- if .PipelineGenNoRunManifest }}
  --no-run-manifest \
{{- end }}
  "$MODULE_NAME"
test -d "$OUTDIR"
test -f "$OUTDIR/main.nf"
test -f "$OUTDIR/nextflow.config"
mkdir -p "$(dirname "$STATE_FILE")"
echo "$OUTDIR" > "$STATE_FILE"
echo "Generated outdir: $OUTDIR"
`))

type generateScriptData struct {
	StateFile                string
	GenOutdirExpr            string // includes literal shell ${RUN_ID} suffix
	PipelineGenRepo          string
	PipelineGenVersion       string
	Module                   string
	OutputPrefix             string
	PipelineGenNoRunManifest bool
	JarFetch                 string
	ModulesFetch             string
}

func buildGenerateScript(spec Spec) string {
	// Namespace state file by job name — the host volume is shared across all
	// module-runs landing on the same node, so a flat path collides between
	// concurrent jobs and one alloc reads another's outdir.
	stateFile := filepath.ToSlash(filepath.Join(spec.WorkDir, spec.JobName, "state.txt"))
	genOutPrefix := filepath.ToSlash(filepath.Join(spec.WorkDir, "generated-"+moduleSlug(spec.Module)))
	outputPrefix := trimTrailingSlash(spec.OutputPrefix)

	data := generateScriptData{
		StateFile:                stateFile,
		GenOutdirExpr:            genOutPrefix + "-${RUN_ID}",
		PipelineGenRepo:          spec.PipelineGenRepo,
		PipelineGenVersion:       spec.PipelineGenVersion,
		Module:                   spec.Module,
		OutputPrefix:             outputPrefix,
		PipelineGenNoRunManifest: spec.PipelineGenNoRunManifest,
		JarFetch:                 PipelineGenJarFetchScript(),
		ModulesFetch:             NfCoreModulesFetchScript(),
	}
	var buf bytes.Buffer
	if err := generateScriptTmpl.Execute(&buf, data); err != nil {
		// Template is static and validated at init; panic is appropriate.
		panic("generateScriptTmpl: " + err.Error())
	}
	return buf.String()
}

// clusterNextflowConfigTmpl is the Nextflow config embedded in the main run task.
// docker.registry is set to quay.io so that biocontainers images (specified without
// a registry prefix in nf-core module process definitions) resolve to
// quay.io/biocontainers/… rather than the default docker.io/biocontainers/…,
// which does not carry all image tags.
var clusterNextflowConfigTmpl = template.Must(
	template.New("nfconfig").Parse(
		`plugins {
  id "nf-nomad@{{.NfPluginVersion}}"
}

docker {
  enabled  = true
  registry = 'quay.io'
}

process {
  executor      = "nomad"
  errorStrategy = "retry"
  maxRetries    = 1
}

workDir = "{{.WorkDir}}"

// All S3 connection details come from the run task's env block — set by the
// CLI from --s3-endpoint/--s3-access-key/--s3-secret-key (or AWS_* env vars).
// No defaults baked into the driver config, no per-job Nomad Variable lookup.
def s3Endpoint = System.getenv("NF_S3_ENDPOINT") ?: ""
def s3Protocol = s3Endpoint.startsWith("https://") ? "https" : "http"

aws {
  accessKey = System.getenv("AWS_ACCESS_KEY_ID") ?: ""
  secretKey = System.getenv("AWS_SECRET_ACCESS_KEY") ?: ""
  client {
    s3PathStyleAccess = true
    protocol          = s3Protocol
    if (s3Endpoint) {
      endpoint = s3Endpoint
    }
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
    namespace              = "default"
    deleteOnCompletion     = false
    cpuMode                = "cores"
    failOnPlacementFailure = true
    placementFailureTimeout = "5m"
    taskDriver             = "{{.TaskDriver}}"
    volumes = [{ type "host" name "{{.HostVolume}}" path "{{.WorkDir}}" workDir true }]
  }
}
`))

type clusterConfigData struct {
	NfPluginVersion string
	WorkDir         string
	HostVolume      string
	TaskDriver      string
}

func buildClusterNextflowConfig(spec Spec) string {
	var buf bytes.Buffer
	if err := clusterNextflowConfigTmpl.Execute(&buf, clusterConfigData{
		NfPluginVersion: spec.NfPluginVersion,
		WorkDir:         spec.WorkDir,
		HostVolume:      spec.HostVolume,
		TaskDriver:      spec.TaskDriver,
	}); err != nil {
		panic("clusterNextflowConfigTmpl: " + err.Error())
	}
	return buf.String()
}

// runScriptTmpl is the main task entrypoint: reads the generated driver path
// written by the prestart task, then runs Nextflow.
var runScriptTmpl = template.Must(
	template.New("run").Funcs(tmplFuncs).Parse(
		`#!/usr/bin/env bash
set -uo pipefail
OUTDIR="$(cat {{ shellQuote .StateFile }})"
cd "$OUTDIR"
LOG_FILE="$OUTDIR/nextflow-run.log"
DIAG_LOG="$OUTDIR/abc-module-run-diag.log"

{
  echo "===== abc module run diagnostic ====="
  echo "date_utc: $(date -u +%Y-%m-%dT%H:%M:%SZ)"
  echo "outdir:   $OUTDIR"
  echo "profile:  {{ .Profile }}"
  echo
  echo "----- ls -la $OUTDIR -----"
  ls -la "$OUTDIR" || true
  echo
  for f in params.yml module.config nextflow.config main.nf test-profile-samplesheet.csv; do
    if [ -f "$OUTDIR/$f" ]; then
      echo "----- $f -----"
      head -c 8192 "$OUTDIR/$f"
      echo
    fi
  done
  echo "----- /local/module-run.nextflow.config -----"
  cat /local/module-run.nextflow.config 2>/dev/null || echo "(missing)"
  echo
  echo "----- nextflow -version -----"
  nextflow -version 2>&1 || true
} > "$DIAG_LOG" 2>&1

{{ if .NfPluginZipURL -}}
# Side-load a custom-built Nextflow plugin (e.g. patched nf-nomad with
# taskDriver support). We unpack the zip into NXF_PLUGINS_DIR and tell
# Nextflow to run in offline mode so it does not try the public registry.
export NXF_PLUGINS_DIR=/local/nfplugins
export NXF_PLUGINS_MODE=offline
mkdir -p "$NXF_PLUGINS_DIR"
PLUGIN_ZIP=/local/nf-plugin.zip
echo ">> Fetching Nextflow plugin: {{ .NfPluginZipURL }}" | tee -a "$DIAG_LOG"
curl -fsSL -L -o "$PLUGIN_ZIP" "{{ .NfPluginZipURL }}"
# Extract <plugin-name>-<version> dir from the zip's first top-level entry
PLUGIN_DIR_NAME="$(unzip -Z1 "$PLUGIN_ZIP" 2>/dev/null | head -1 | sed 's|/.*||')"
[ -z "$PLUGIN_DIR_NAME" ] && PLUGIN_DIR_NAME="$(basename "{{ .NfPluginZipURL }}" .zip)"
mkdir -p "$NXF_PLUGINS_DIR/$PLUGIN_DIR_NAME"
( cd "$NXF_PLUGINS_DIR/$PLUGIN_DIR_NAME" && unzip -oq "$PLUGIN_ZIP" )
echo ">> Plugin installed at $NXF_PLUGINS_DIR/$PLUGIN_DIR_NAME" | tee -a "$DIAG_LOG"
{{ end -}}

set +e
nextflow run main.nf \
  -profile {{ shellQuote .Profile }} \
  -params-file params.yml \
  -c module.config \
  -c /local/module-run.nextflow.config \
  -ansi-log false \
  > "$LOG_FILE" 2>&1
NF_EXIT=$?
set -e

if [ -f .nextflow.log ]; then
  {
    echo "----- .nextflow.log (tail 200) -----"
    tail -n 200 .nextflow.log
  } >> "$LOG_FILE"
fi

# Self-report: stash the diagnostic + tail of the run log into a Nomad Variable
# so the cluster's /v1/client/fs/* ACL doesn't matter — Variables are readable
# from anywhere with the same token used to submit the job.
if [ -n "${NOMAD_ADDR:-}" ] && [ -n "${NOMAD_TOKEN:-}" ]; then
  VAR_PATH="nomad/jobs/${NOMAD_JOB_NAME:-module-run}/diag/last-run"
  COMBINED_FILE="$(mktemp)"
  {
    cat "$DIAG_LOG" 2>/dev/null
    echo
    echo "----- nextflow-run.log (tail 200) -----"
    tail -n 200 "$LOG_FILE" 2>/dev/null
    echo
    echo "----- .nextflow.log (tail 200) -----"
    tail -n 200 .nextflow.log 2>/dev/null
    echo
    echo "----- nf_exit: $NF_EXIT -----"
  } | tail -c 250000 > "$COMBINED_FILE"

  # Build JSON payload by hand to avoid needing python/jq in the runtime image.
  PAYLOAD_FILE="$(mktemp)"
  {
    printf '{"Path":"%s","Items":{"exit_code":"%d","log":' "$VAR_PATH" "$NF_EXIT"
    # Use awk to JSON-escape the body (covers \, ", control chars, newlines, tabs).
    awk 'BEGIN{ printf "\"" }
         { gsub(/\\/, "\\\\"); gsub(/"/, "\\\""); gsub(/\t/, "\\t"); gsub(/\r/, "\\r"); printf "%s\\n", $0 }
         END  { printf "\"" }' "$COMBINED_FILE"
    printf '}}'
  } > "$PAYLOAD_FILE"

  if curl -fsSL -X PUT \
       -H "X-Nomad-Token: $NOMAD_TOKEN" \
       -H "Content-Type: application/json" \
       --data-binary "@$PAYLOAD_FILE" \
       "${NOMAD_ADDR%/}/v1/var/$VAR_PATH" > /dev/null; then
    echo "Diag log stored in Nomad Variable: $VAR_PATH" >&2
  else
    echo "Diag log upload to Nomad Variable failed" >&2
  fi
  rm -f "$COMBINED_FILE" "$PAYLOAD_FILE"
fi

exit "$NF_EXIT"
`))

type runScriptData struct {
	StateFile      string
	Profile        string
	NfPluginZipURL string
}

func buildRunScript(spec Spec) string {
	// Namespace state file by job name — the host volume is shared across all
	// module-runs landing on the same node, so a flat path collides between
	// concurrent jobs and one alloc reads another's outdir.
	stateFile := filepath.ToSlash(filepath.Join(spec.WorkDir, spec.JobName, "state.txt"))
	var buf bytes.Buffer
	if err := runScriptTmpl.Execute(&buf, runScriptData{
		StateFile:      stateFile,
		Profile:        spec.Profile,
		NfPluginZipURL: spec.NfPluginZipURL,
	}); err != nil {
		panic("runScriptTmpl: " + err.Error())
	}
	return buf.String()
}

func trimTrailingSlash(s string) string {
	for len(s) > 0 && s[len(s)-1] == '/' {
		s = s[:len(s)-1]
	}
	return s
}

func moduleSlug(moduleName string) string {
	var b strings.Builder
	lastDash := false
	for _, r := range strings.ToLower(moduleName) {
		isAlphaNum := (r >= 'a' && r <= 'z') || (r >= '0' && r <= '9')
		if isAlphaNum {
			b.WriteRune(r)
			lastDash = false
			continue
		}
		if !lastDash {
			b.WriteRune('-')
			lastDash = true
		}
	}
	return strings.Trim(b.String(), "-")
}
