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
	OutputPrefix string

	PipelineGenRepo    string
	PipelineGenVersion string
	ModuleRevision     string
	GitHubToken        string

	CPU      int
	MemoryMB int

	NfVersion       string
	NfPluginVersion string

	Namespace   string
	Datacenters []string

	MinioEndpoint string

	ParamsYAMLContent string
	ConfigYAMLContent string
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

	volBody := groupBody.AppendNewBlock("volume", []string{"nextflow-work"}).Body()
	volBody.SetAttributeValue("type", cty.StringVal("host"))
	volBody.SetAttributeValue("source", cty.StringVal("nextflow-work"))

	// ---- prestart: generate module driver with nf-pipeline-gen ----
	genTaskBody := groupBody.AppendNewBlock("task", []string{"generate"}).Body()
	genTaskBody.SetAttributeValue("driver", cty.StringVal("docker"))

	lifecycle := genTaskBody.AppendNewBlock("lifecycle", nil).Body()
	lifecycle.SetAttributeValue("hook", cty.StringVal("prestart"))
	lifecycle.SetAttributeValue("sidecar", cty.BoolVal(false))

	genMount := genTaskBody.AppendNewBlock("volume_mount", nil).Body()
	genMount.SetAttributeValue("volume", cty.StringVal("nextflow-work"))
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
	if spec.ModuleRevision != "" {
		genEnv.SetAttributeValue("ABC_MODULE_REVISION", cty.StringVal(spec.ModuleRevision))
	}
	if spec.ParamsYAMLContent != "" {
		genEnv.SetAttributeValue("ABC_MODULE_PARAMS_B64", cty.StringVal(base64.StdEncoding.EncodeToString([]byte(spec.ParamsYAMLContent))))
	}
	if spec.ConfigYAMLContent != "" {
		genEnv.SetAttributeValue("ABC_MODULE_CONFIG_B64", cty.StringVal(base64.StdEncoding.EncodeToString([]byte(spec.ConfigYAMLContent))))
	}

	genRes := genTaskBody.AppendNewBlock("resources", nil).Body()
	genRes.SetAttributeValue("cpu", cty.NumberIntVal(1200))
	genRes.SetAttributeValue("memory", cty.NumberIntVal(3072))

	// ---- main: run generated module driver ----
	runTaskBody := groupBody.AppendNewBlock("task", []string{"nextflow"}).Body()
	runTaskBody.SetAttributeValue("driver", cty.StringVal("docker"))

	runMount := runTaskBody.AppendNewBlock("volume_mount", nil).Body()
	runMount.SetAttributeValue("volume", cty.StringVal("nextflow-work"))
	runMount.SetAttributeValue("destination", cty.StringVal(spec.WorkDir))
	runMount.SetAttributeValue("read_only", cty.BoolVal(false))

	awsVarPath := fmt.Sprintf("nomad/jobs/%s/run/nextflow", spec.JobName)
	awsTmpl := runTaskBody.AppendNewBlock("template", nil).Body()
	awsTmpl.SetAttributeValue("destination", cty.StringVal("secrets/aws.env"))
	awsTmpl.SetAttributeValue("env", cty.BoolVal(true))
	awsTmpl.SetAttributeValue("data", cty.StringVal(
		fmt.Sprintf("{{- with nomadVar %q -}}\nAWS_ACCESS_KEY_ID={{ .AWS_ACCESS_KEY_ID }}\nAWS_SECRET_ACCESS_KEY={{ .AWS_SECRET_ACCESS_KEY }}\n{{- end }}\n", awsVarPath),
	))

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
	if spec.MinioEndpoint != "" {
		runEnv.SetAttributeValue("NF_MINIO_ENDPOINT", cty.StringVal(spec.MinioEndpoint))
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
// fetches the nf-core/modules tarball, and generates the driver pipeline.
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
OUTDIR={{ shellQuote .GenOutdirExpr }}
STATE_FILE={{ shellQuote .StateFile }}
PIPELINE_GEN_REPO={{ shellQuote .PipelineGenRepo }}
PIPELINE_GEN_VERSION={{ shellQuote .PipelineGenVersion }}
MODULE_NAME={{ shellQuote .Module }}
OUTPUT_PREFIX={{ shellQuote .OutputPrefix }}

rm -rf "$OUTDIR" "$MODULES_DIR" "$JAR_PATH" "$MODULES_TGZ"

if [ "$PIPELINE_GEN_VERSION" = "latest" ]; then
  RELEASE_URL="https://api.github.com/repos/${PIPELINE_GEN_REPO}/releases/latest"
else
  RELEASE_URL="https://api.github.com/repos/${PIPELINE_GEN_REPO}/releases/tags/${PIPELINE_GEN_VERSION}"
fi

ASSET_ID="$(python3 - "$RELEASE_URL" <<'PY'
import json
import os
import sys
import urllib.request

token = os.environ.get("GITHUB_TOKEN", "")
if not token:
    raise SystemExit("Missing GITHUB_TOKEN for nf-pipeline-gen release download")

release_url = sys.argv[1] if len(sys.argv) > 1 else ""
if not release_url:
    raise SystemExit("Missing RELEASE_URL")

headers = {
    "Authorization": f"Bearer {token}",
    "Accept": "application/vnd.github+json",
    "X-GitHub-Api-Version": "2022-11-28",
    "User-Agent": "abc-module-run"
}
req = urllib.request.Request(release_url, headers=headers)
with urllib.request.urlopen(req) as r:
    release = json.load(r)
assets = release.get("assets", [])
jar_assets = [a for a in assets if a.get("name", "").endswith(".jar") or "standalone" in a.get("name", "").lower()]
asset = jar_assets[0] if jar_assets else (assets[0] if assets else None)
if not asset:
    raise SystemExit("No nf-pipeline-gen release assets found")
print(asset["id"])
PY
)"
test -n "$ASSET_ID"
curl -fsSL -H "Authorization: Bearer $GITHUB_TOKEN" -H "Accept: application/octet-stream" -L -o "$JAR_PATH" \
  "https://api.github.com/repos/${PIPELINE_GEN_REPO}/releases/assets/$ASSET_ID"
test -s "$JAR_PATH"

MODULES_COMMIT="$(python3 - <<'PY'
import json
import urllib.request
req = urllib.request.Request(
    "https://api.github.com/repos/nf-core/modules/commits/master",
    headers={
        "Accept": "application/vnd.github+json",
        "X-GitHub-Api-Version": "2022-11-28",
        "User-Agent": "abc-module-run"
    }
)
with urllib.request.urlopen(req) as r:
    data = json.load(r)
sha = data.get("sha")
if not sha:
    raise SystemExit("Unable to resolve nf-core/modules master commit")
print(sha)
PY
)"
MODULES_REVISION="$(printf '%s' "$MODULES_COMMIT" | cut -c1-12)"
if [ -n "${ABC_MODULE_REVISION:-}" ]; then
  MODULES_REVISION="$ABC_MODULE_REVISION"
fi
curl -fsSL -L -o "$MODULES_TGZ" "https://github.com/nf-core/modules/archive/${MODULES_COMMIT}.tar.gz"
test -s "$MODULES_TGZ"

mkdir -p "$MODULES_DIR"
python3 - <<'PY'
import tarfile
src = '/local/modules.tgz'
dst = '/local/modules-src'
with tarfile.open(src, 'r:gz') as tf:
    members = tf.getmembers()
    root = members[0].name.split('/')[0]
    for m in members:
        if m.name.startswith(root + '/'):
            m.name = m.name[len(root)+1:]
            if m.name:
                tf.extract(m, dst)
PY

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

java -jar "$JAR_PATH" module \
  --modules-dir "$MODULES_DIR" \
  --params-file "$PARAMS_FILE" \
  --config-file "$MODULE_CONFIG" \
  --revision "$MODULES_REVISION" \
  --outdir "$OUTDIR" \
  "$MODULE_NAME"
test -d "$OUTDIR"
test -f "$OUTDIR/main.nf"
test -f "$OUTDIR/nextflow.config"
echo "$OUTDIR" > "$STATE_FILE"
echo "Generated outdir: $OUTDIR"
`))

type generateScriptData struct {
	StateFile          string
	GenOutdirExpr      string // includes literal shell ${RUN_ID} suffix
	PipelineGenRepo    string
	PipelineGenVersion string
	Module             string
	OutputPrefix       string
}

func buildGenerateScript(spec Spec) string {
	stateFile := filepath.ToSlash(filepath.Join(spec.WorkDir, "abc-module-run-outdir.txt"))
	genOutPrefix := filepath.ToSlash(filepath.Join(spec.WorkDir, "generated-"+moduleSlug(spec.Module)))
	outputPrefix := trimTrailingSlash(spec.OutputPrefix)

	data := generateScriptData{
		StateFile:          stateFile,
		GenOutdirExpr:      genOutPrefix + "-${RUN_ID}",
		PipelineGenRepo:    spec.PipelineGenRepo,
		PipelineGenVersion: spec.PipelineGenVersion,
		Module:             spec.Module,
		OutputPrefix:       outputPrefix,
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

def minioEndpoint = System.getenv("NF_MINIO_ENDPOINT") ?: "http://localhost:9000"
def minioProtocol = minioEndpoint.startsWith("https://") ? "https" : "http"

aws {
  accessKey = System.getenv("AWS_ACCESS_KEY_ID") ?: ""
  secretKey = System.getenv("AWS_SECRET_ACCESS_KEY") ?: ""
  client {
    endpoint          = minioEndpoint
    s3PathStyleAccess = true
    protocol          = minioProtocol
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
    volumes = [{ type "host" name "nextflow-work" path "{{.WorkDir}}" }]
  }
}
`))

type clusterConfigData struct {
	NfPluginVersion string
	WorkDir         string
}

func buildClusterNextflowConfig(spec Spec) string {
	var buf bytes.Buffer
	if err := clusterNextflowConfigTmpl.Execute(&buf, clusterConfigData{
		NfPluginVersion: spec.NfPluginVersion,
		WorkDir:         spec.WorkDir,
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
set -euo pipefail
OUTDIR="$(cat {{ shellQuote .StateFile }})"
cd "$OUTDIR"
LOG_FILE="$OUTDIR/nextflow-run.log"
nextflow run main.nf \
  -profile {{ shellQuote .Profile }} \
  -params-file params.yml \
  -c module.config \
  -c /local/module-run.nextflow.config \
  -ansi-log false \
  2>&1 | tee "$LOG_FILE"
if [ -f .nextflow.log ]; then
  echo "----- .nextflow.log (tail) -----" | tee -a "$LOG_FILE"
  tail -n 200 .nextflow.log | tee -a "$LOG_FILE"
fi
`))

type runScriptData struct {
	StateFile string
	Profile   string
}

func buildRunScript(spec Spec) string {
	stateFile := filepath.ToSlash(filepath.Join(spec.WorkDir, "abc-module-run-outdir.txt"))
	var buf bytes.Buffer
	if err := runScriptTmpl.Execute(&buf, runScriptData{
		StateFile: stateFile,
		Profile:   spec.Profile,
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
