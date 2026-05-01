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
	// block is added to the head group.
	NodeConstraint string

	// PinWorkers, when true AND NodeConstraint is set, also emits the per-process
	// nf-nomad constraint `process { constraints = { node { unique = [name: '<node>'] } } }`
	// — pinning EVERY child Nomad task to the same node. Use this for runs without
	// a shared filesystem when nf-rclone or similar isn't available. Default false:
	// `--node` only pins the head; workers spread freely across the cluster.
	PinWorkers bool

	// PluginBundleURL, when non-empty, makes the head job pull this artifact zip
	// (typically the nf-abc-cluster-dev meta-plugin bundle) and extract it into
	// $NXF_HOME/plugins before running Nextflow. Used to ship development plugin
	// variants that are not on the public registry.
	PluginBundleURL string

	// Plugins is the ordered list of `id "<id>@<version>"` lines emitted in the
	// generated nextflow.headjob.config plugins { ... } block. When empty, the
	// existing single-line "nf-nomad@<NfPluginVersion>" behaviour is preserved
	// for backwards compatibility.
	Plugins []PluginRef

	// ExtraBinaries is an ordered list of (name, source URL) pairs for
	// additional cluster tool binaries the head task needs at runtime —
	// e.g. `rclone` when the nf-rclone plugin is loaded. Each entry produces
	// a Nomad `artifact` stanza pulling into local/bin/<name> + chmod +x;
	// local/bin is prepended to PATH in the entrypoint.
	ExtraBinaries []ToolBinary

	// StaticEnv is merged into the task env block as literal strings (abc-nodes
	// enhanced floor: Loki, Prometheus, Grafana Alloy).
	StaticEnv map[string]string
}

// PluginRef is one entry in a Nextflow plugins { ... } block.
type PluginRef struct {
	ID      string
	Version string
}

// ToolBinary is one cluster tool binary pulled into the head container as a
// Nomad artifact. SourceURL must already include any per-arch interpolation
// (e.g. `${attr.kernel.name}-${attr.cpu.arch}`); resolved by the caller via
// `tools.ArtifactURL(name, "")`.
type ToolBinary struct {
	Name      string
	SourceURL string
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

	// Plugin bundle artifact — pulled and unpacked into local/plugins-bundle/.
	// The `?archive=zip` query hint forces go-getter (Nomad's artifact engine)
	// to treat the response as a zip even when the URL lacks a .zip extension
	// (e.g. our `<name>-any` S3 keys). Avoids needing `unzip` in the head image.
	if spec.PluginBundleURL != "" {
		src := spec.PluginBundleURL
		if !strings.Contains(src, "?archive=") {
			joiner := "?"
			if strings.Contains(src, "?") {
				joiner = "&"
			}
			src = src + joiner + "archive=zip"
		}
		artBody := taskBody.AppendNewBlock("artifact", nil).Body()
		artBody.SetAttributeValue("source", cty.StringVal(src))
		artBody.SetAttributeValue("destination", cty.StringVal("local/plugins-bundle"))
		artBody.SetAttributeValue("mode", cty.StringVal("any"))
	}

	// Extra cluster tool binaries (e.g. rclone, when nf-rclone is in the bundle).
	// Each one pulled as a single file into local/bin/<name>; PATH is updated
	// in the entrypoint so the head Nextflow process and any spawned
	// subprocesses can invoke them.
	for _, tb := range spec.ExtraBinaries {
		if tb.Name == "" || tb.SourceURL == "" {
			continue
		}
		binArt := taskBody.AppendNewBlock("artifact", nil).Body()
		binArt.SetAttributeValue("source", cty.StringVal(tb.SourceURL))
		binArt.SetAttributeValue("destination", cty.StringVal("local/bin/"+tb.Name))
		binArt.SetAttributeValue("mode", cty.StringVal("file"))
	}

	// Template: nextflow config
	nfCfgTmpl := taskBody.AppendNewBlock("template", nil).Body()
	nfCfgTmpl.SetAttributeValue("destination", cty.StringVal("local/nextflow.headjob.config"))
	nfCfgTmpl.SetAttributeValue("data", cty.StringVal(buildNextflowConfig(spec)))

	// Template: AWS credentials from Nomad Variables.
	//
	// Single source of truth shared with the per-process secrets directive that
	// nf-nomad uses for child tasks (see `nomad.jobs.secrets.path` in extra
	// configs). Both head and workers read AWS_ACCESS_KEY_ID /
	// AWS_SECRET_ACCESS_KEY from `nomad/jobs/secrets/<NAME>` — the operator
	// seeds these once, no per-job-name var seeding required.
	//
	// Each `with nomadVar` is independent so the head still starts even when
	// only one of the two creds is present (e.g. read-only debugging mode).
	awsTmpl := taskBody.AppendNewBlock("template", nil).Body()
	awsTmpl.SetAttributeValue("destination", cty.StringVal("secrets/aws.env"))
	awsTmpl.SetAttributeValue("env", cty.BoolVal(true))
	awsTmpl.SetAttributeValue("data", cty.StringVal(
		"{{- with nomadVar \"nomad/jobs/secrets/AWS_ACCESS_KEY_ID\" -}}\n"+
			"AWS_ACCESS_KEY_ID={{ .AWS_ACCESS_KEY_ID }}\n"+
			"{{- end }}\n"+
			"{{- with nomadVar \"nomad/jobs/secrets/AWS_SECRET_ACCESS_KEY\" -}}\n"+
			"AWS_SECRET_ACCESS_KEY={{ .AWS_SECRET_ACCESS_KEY }}\n"+
			"{{- end }}\n",
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
	if spec.NodeConstraint != "" && spec.PinWorkers {
		processConstraint = fmt.Sprintf(`

process {
  constraints = { node { unique = [name: '%s'] } }
}
`, spec.NodeConstraint)
	}

	// Build the plugins { ... } block. Default (no spec.Plugins) keeps the
	// historical single nf-nomad line so non-dev runs are byte-identical to
	// pre-bundle behaviour.
	pluginsBody := fmt.Sprintf(`  id "nf-nomad@%s"`, spec.NfPluginVersion)
	if len(spec.Plugins) > 0 {
		var lines []string
		for _, p := range spec.Plugins {
			if p.Version != "" {
				lines = append(lines, fmt.Sprintf(`  id "%s@%s"`, p.ID, p.Version))
			} else {
				lines = append(lines, fmt.Sprintf(`  id "%s"`, p.ID))
			}
		}
		pluginsBody = strings.Join(lines, "\n")
	}

	fmt.Fprintf(&sb, `plugins {
%s
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
%s`, pluginsBody, spec.WorkDir, spec.Namespace, volumesLine, processConstraint)

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

	// Move the auto-extracted plugin bundle into NXF_HOME/plugins before
	// invoking nextflow. Nomad's artifact stanza (with ?archive=zip) unpacks
	// the zip into local/plugins-bundle/<plugin>-<version>/... directly, so
	// we only need a recursive copy here — no `unzip` dependency in the image.
	if spec.PluginBundleURL != "" {
		fmt.Fprintf(&sb, "mkdir -p \"%s/plugins\"\n", nxfHome)
		fmt.Fprintf(&sb, "cp -r /local/plugins-bundle/. \"%s/plugins/\"\n\n", nxfHome)
	}

	// Make any tool binaries pulled by ExtraBinaries executable and on PATH.
	// /local is the task working dir (set in the docker config), so /local/bin
	// matches the artifact destinations emitted above.
	if len(spec.ExtraBinaries) > 0 {
		sb.WriteString("if [ -d /local/bin ]; then chmod +x /local/bin/*; export PATH=\"/local/bin:$PATH\"; fi\n\n")
	}

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
