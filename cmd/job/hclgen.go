package job

import (
	"fmt"
	"path/filepath"
	"strings"

	"github.com/abc-cluster/abc-cluster-cli/cmd/utils"
	"github.com/hashicorp/hcl/v2/hclwrite"
	"github.com/zclconf/go-cty/cty"
)

// generateHCL builds a Nomad HCL job spec from spec, embedding scriptContent
// as a Nomad template so the driver can execute it from local/<scriptName>.
func generateHCL(spec *jobSpec, scriptName, scriptContent string) string {
	f := hclwrite.NewEmptyFile()
	root := f.Body()

	jobBlock := root.AppendNewBlock("job", []string{spec.Name})
	jobBody := jobBlock.Body()
	jobBody.SetAttributeValue("type", cty.StringVal("batch"))
	jobBody.SetAttributeValue("priority", cty.NumberIntVal(int64(spec.Priority)))
	if spec.Region != "" {
		jobBody.SetAttributeValue("region", cty.StringVal(spec.Region))
	}
	if spec.Namespace != "" {
		jobBody.SetAttributeValue("namespace", cty.StringVal(spec.Namespace))
	}
	if len(spec.Datacenters) > 0 {
		dcs := make([]cty.Value, len(spec.Datacenters))
		for i, dc := range spec.Datacenters {
			dcs[i] = cty.StringVal(dc)
		}
		jobBody.SetAttributeValue("datacenters", cty.ListVal(dcs))
	}
	for _, c := range spec.Constraints {
		b := jobBody.AppendNewBlock("constraint", nil).Body()
		b.SetAttributeValue("attribute", cty.StringVal(c.Attribute))
		b.SetAttributeValue("operator", cty.StringVal(c.Operator))
		b.SetAttributeValue("value", cty.StringVal(c.Value))
	}
	for _, a := range spec.Affinities {
		b := jobBody.AppendNewBlock("affinity", nil).Body()
		b.SetAttributeValue("attribute", cty.StringVal(a.Attribute))
		b.SetAttributeValue("operator", cty.StringVal(a.Operator))
		b.SetAttributeValue("value", cty.StringVal(a.Value))
		b.SetAttributeValue("weight", cty.NumberIntVal(int64(a.Weight)))
	}
	if len(spec.Meta) > 0 {
		metaBody := jobBody.AppendNewBlock("meta", nil).Body()
		for _, k := range sortedKeys(spec.Meta) {
			metaBody.SetAttributeValue(k, cty.StringVal(spec.Meta[k]))
		}
	}

	groupBody := jobBody.AppendNewBlock("group", []string{"main"}).Body()
	groupBody.SetAttributeValue("count", cty.NumberIntVal(int64(spec.Nodes)))

	if spec.NoNetwork {
		groupBody.AppendNewBlock("network", nil).Body().
			SetAttributeValue("mode", cty.StringVal("none"))
	} else if len(spec.Ports) > 0 {
		netBody := groupBody.AppendNewBlock("network", nil).Body()
		for _, p := range spec.Ports {
			netBody.AppendNewBlock("port", []string{p})
		}
	}

	if spec.Depend != "" {
		waitBody := groupBody.AppendNewBlock("task", []string{"wait-dependency"}).Body()
		waitBody.SetAttributeValue("driver", cty.StringVal(spec.Driver))
		lc := waitBody.AppendNewBlock("lifecycle", nil).Body()
		lc.SetAttributeValue("hook", cty.StringVal("prestart"))
		lc.SetAttributeValue("sidecar", cty.BoolVal(false))
		cfg := waitBody.AppendNewBlock("config", nil).Body()
		cfg.SetAttributeValue("command", cty.StringVal("/bin/sh"))
		cfg.SetAttributeValue("args", cty.ListVal([]cty.Value{
			cty.StringVal("-c"),
			cty.StringVal(fmt.Sprintf("echo Waiting for dependency: %s", spec.Depend)),
		}))
	}

	mainBody := groupBody.AppendNewBlock("task", []string{"main"}).Body()
	mainBody.SetAttributeValue("driver", cty.StringVal(spec.Driver))

	cfgBody := mainBody.AppendNewBlock("config", nil).Body()
	appendTaskConfig(cfgBody, spec, scriptName)

	// Embed the script as a Nomad template so it's available at local/<name>.
	tmplBody := mainBody.AppendNewBlock("template", nil).Body()
	tmplBody.SetAttributeValue("data", cty.StringVal(scriptContent))
	tmplBody.SetAttributeValue("destination", cty.StringVal(
		filepath.ToSlash(filepath.Join("local", scriptName))))
	tmplBody.SetAttributeValue("perms", cty.StringVal("0755"))

	if spec.Cores > 0 || spec.MemoryMB > 0 || spec.GPUs > 0 {
		resBody := mainBody.AppendNewBlock("resources", nil).Body()
		if spec.Cores > 0 {
			resBody.SetAttributeValue("cores", cty.NumberIntVal(int64(spec.Cores)))
		}
		if spec.MemoryMB > 0 {
			resBody.SetAttributeValue("memory", cty.NumberIntVal(int64(spec.MemoryMB)))
		}
		if spec.GPUs > 0 {
			resBody.AppendNewBlock("device", []string{"nvidia/gpu"}).Body().
				SetAttributeValue("count", cty.NumberIntVal(int64(spec.GPUs)))
		}
	}

	appendEnvBlock(mainBody, spec)

	return string(f.Bytes())
}

// appendTaskConfig writes the config block for the main task, handling
// walltime wrapping and optional stdout/stderr file redirection.
func appendTaskConfig(cfgBody *hclwrite.Body, spec *jobSpec, scriptName string) {
	scriptArg := fmt.Sprintf("local/%s", scriptName)
	if spec.Driver == "slurm" {
		cfgBody.SetAttributeValue("command", cty.StringVal("/bin/bash"))
		cfgBody.SetAttributeValue("args", cty.ListVal([]cty.Value{
			cty.StringVal(scriptArg),
		}))
		if spec.SlurmPartition != "" {
			cfgBody.SetAttributeValue("queue", cty.StringVal(spec.SlurmPartition))
		}
		if spec.SlurmAccount != "" {
			cfgBody.SetAttributeValue("account", cty.StringVal(spec.SlurmAccount))
		}
		workDir := spec.SlurmWorkDir
		if workDir == "" && spec.ChDir != "" {
			workDir = spec.ChDir
		}
		if workDir != "" {
			cfgBody.SetAttributeValue("work_dir", cty.StringVal(workDir))
		}
		stdoutFile := spec.SlurmStdoutFile
		if stdoutFile == "" && spec.OutputLog != "" {
			stdoutFile = spec.OutputLog
		}
		if stdoutFile != "" {
			cfgBody.SetAttributeValue("stdout_file", cty.StringVal(stdoutFile))
		}
		stderrFile := spec.SlurmStderrFile
		if stderrFile == "" && spec.ErrorLog != "" {
			stderrFile = spec.ErrorLog
		}
		if stderrFile != "" {
			cfgBody.SetAttributeValue("stderr_file", cty.StringVal(stderrFile))
		}
		if spec.SlurmNTasks > 0 {
			cfgBody.SetAttributeValue("ntasks", cty.NumberIntVal(int64(spec.SlurmNTasks)))
		}
		if spec.Cores > 0 {
			cfgBody.SetAttributeValue("cpus_per_task", cty.NumberIntVal(int64(spec.Cores)))
		}
		if spec.MemoryMB > 0 {
			cfgBody.SetAttributeValue("memory", cty.NumberIntVal(int64(spec.MemoryMB)))
		}
		if spec.WalltimeSecs > 0 {
			cfgBody.SetAttributeValue("walltime", cty.StringVal(secondsToWalltime(spec.WalltimeSecs)))
		}
	} else if spec.OutputLog != "" || spec.ErrorLog != "" {
		cmd := fmt.Sprintf("/bin/bash %s", scriptArg)
		if spec.WalltimeSecs > 0 {
			cmd = fmt.Sprintf("timeout %d %s", spec.WalltimeSecs, cmd)
		}
		if spec.OutputLog != "" {
			cmd = fmt.Sprintf(`%s 1> >(tee -a "${NOMAD_TASK_DIR}/%s")`, cmd, spec.OutputLog)
		}
		if spec.ErrorLog != "" {
			cmd = fmt.Sprintf(`%s 2> >(tee -a "${NOMAD_TASK_DIR}/%s" >&2)`, cmd, spec.ErrorLog)
		}
		cfgBody.SetAttributeValue("command", cty.StringVal("/bin/bash"))
		cfgBody.SetAttributeValue("args", cty.ListVal([]cty.Value{
			cty.StringVal("-lc"), cty.StringVal(cmd),
		}))
	} else if spec.WalltimeSecs > 0 {
		cfgBody.SetAttributeValue("command", cty.StringVal("timeout"))
		cfgBody.SetAttributeValue("args", cty.ListVal([]cty.Value{
			cty.StringVal(fmt.Sprintf("%d", spec.WalltimeSecs)),
			cty.StringVal("/bin/bash"),
			cty.StringVal(scriptArg),
		}))
	} else {
		cfgBody.SetAttributeValue("command", cty.StringVal("/bin/bash"))
		cfgBody.SetAttributeValue("args", cty.ListVal([]cty.Value{
			cty.StringVal(scriptArg),
		}))
	}

	if spec.Driver != "slurm" && spec.ChDir != "" {
		cfgBody.SetAttributeValue("work_dir", cty.StringVal(spec.ChDir))
	}
	for _, k := range sortedKeys(spec.DriverConfig) {
		cfgBody.SetAttributeValue(k, cty.StringVal(strings.TrimSpace(spec.DriverConfig[k])))
	}
}

func secondsToWalltime(seconds int) string {
	if seconds <= 0 {
		return "00:00:00"
	}
	hours := seconds / 3600
	minutes := (seconds % 3600) / 60
	secs := seconds % 60
	return fmt.Sprintf("%02d:%02d:%02d", hours, minutes, secs)
}

// appendEnvBlock emits the env block containing the HPC compatibility layer
// (PBS_*/SLURM_* aliases always present) plus any explicitly requested
// NOMAD_* runtime-exposure variables.
func appendEnvBlock(taskBody *hclwrite.Body, spec *jobSpec) {
	envBody := taskBody.AppendNewBlock("env", nil).Body()

	// HPC compatibility layer — always emitted so existing PBS/SLURM scripts
	// can read familiar environment variables without changes.
	envBody.SetAttributeValue("SLURM_JOB_ID", cty.StringVal("${NOMAD_ALLOC_ID}"))
	envBody.SetAttributeValue("PBS_JOBID", cty.StringVal("${NOMAD_ALLOC_ID}"))
	envBody.SetAttributeValue("SLURM_JOB_NAME", cty.StringVal("${NOMAD_JOB_NAME}"))
	envBody.SetAttributeValue("PBS_JOBNAME", cty.StringVal("${NOMAD_JOB_NAME}"))
	envBody.SetAttributeValue("SLURM_SUBMIT_DIR", cty.StringVal("${NOMAD_TASK_DIR}"))
	envBody.SetAttributeValue("PBS_O_WORKDIR", cty.StringVal("${NOMAD_TASK_DIR}"))
	envBody.SetAttributeValue("SLURM_ARRAY_TASK_ID", cty.StringVal("${NOMAD_ALLOC_INDEX}"))
	envBody.SetAttributeValue("PBS_ARRAYID", cty.StringVal("${NOMAD_ALLOC_INDEX}"))
	envBody.SetAttributeValue("SLURM_NTASKS", cty.StringVal("${NOMAD_GROUP_COUNT}"))
	envBody.SetAttributeValue("PBS_NP", cty.StringVal("${NOMAD_GROUP_COUNT}"))
	envBody.SetAttributeValue("SLURMD_NODENAME", cty.StringVal("${NOMAD_ALLOC_HOST}"))
	envBody.SetAttributeValue("PBS_O_HOST", cty.StringVal("${NOMAD_ALLOC_HOST}"))
	envBody.SetAttributeValue("SLURM_CPUS_ON_NODE", cty.StringVal("${NOMAD_CPU_CORES}"))
	envBody.SetAttributeValue("PBS_NUM_PPN", cty.StringVal("${NOMAD_CPU_CORES}"))
	envBody.SetAttributeValue("SLURM_MEM_PER_NODE", cty.StringVal("${NOMAD_MEMORY_LIMIT}"))
	envBody.SetAttributeValue("PBS_MEM", cty.StringVal("${NOMAD_MEMORY_LIMIT}"))

	// Explicit runtime-exposure directives.
	type runtimeVar struct {
		flag bool
		env  string
	}
	for _, e := range []runtimeVar{
		{spec.ExposeAllocID, "NOMAD_ALLOC_ID"},
		{spec.ExposeShortAllocID, "NOMAD_SHORT_ALLOC_ID"},
		{spec.ExposeAllocName, "NOMAD_ALLOC_NAME"},
		{spec.ExposeAllocIndex, "NOMAD_ALLOC_INDEX"},
		{spec.ExposeJobID, "NOMAD_JOB_ID"},
		{spec.ExposeJobName, "NOMAD_JOB_NAME"},
		{spec.ExposeParentJobID, "NOMAD_JOB_PARENT_ID"},
		{spec.ExposeGroupName, "NOMAD_GROUP_NAME"},
		{spec.ExposeTaskName, "NOMAD_TASK_NAME"},
		{spec.ExposeNamespaceEnv, "NOMAD_NAMESPACE"},
		{spec.ExposeDCEnv, "NOMAD_DC"},
		{spec.ExposeCPULimit, "NOMAD_CPU_LIMIT"},
		{spec.ExposeCPUCores, "NOMAD_CPU_CORES"},
		{spec.ExposeMemLimit, "NOMAD_MEMORY_LIMIT"},
		{spec.ExposeMemMaxLimit, "NOMAD_MEMORY_MAX_LIMIT"},
		{spec.ExposeAllocDir, "NOMAD_ALLOC_DIR"},
		{spec.ExposeTaskDir, "NOMAD_TASK_DIR"},
		{spec.ExposeSecretsDir, "NOMAD_SECRETS_DIR"},
	} {
		if e.flag {
			envBody.SetAttributeValue(e.env, cty.StringVal(fmt.Sprintf("${%s}", e.env)))
		}
	}

	for _, p := range spec.Ports {
		up := strings.ToUpper(p)
		envBody.SetAttributeValue("NOMAD_IP_"+up, cty.StringVal(fmt.Sprintf("${NOMAD_IP_%s}", p)))
		envBody.SetAttributeValue("NOMAD_PORT_"+up, cty.StringVal(fmt.Sprintf("${NOMAD_PORT_%s}", p)))
		envBody.SetAttributeValue("NOMAD_ADDR_"+up, cty.StringVal(fmt.Sprintf("${NOMAD_ADDR_%s}", p)))
	}
}

// ── Helpers (delegating to shared utils) ─────────────────────────────────────

func parseMemoryMB(s string) (int, error)     { return utils.ParseMemoryMB(s) }
func walltimeToSeconds(t string) (int, error) { return utils.WalltimeToSeconds(t) }
func sortedKeys(m map[string]string) []string { return utils.SortedKeys(m) }
