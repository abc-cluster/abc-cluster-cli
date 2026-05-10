// hello_cluster.go — built-in stress-ng workload for cluster verification and load testing.
// Invoked via: abc job run hello-cluster [flags]
//
// Randomises CPU, VM, and I/O stressor counts and a run duration at CLI time
// so each submission exercises a different resource profile.  The chosen
// parameters are stamped into Nomad meta so operators can inspect them via
// `abc job show` without reading logs.
//
// Driver selection (when capabilities have been synced via `abc cluster capabilities sync`):
//
//	docker            → uses the stress-ng container image directly
//	containerd-driver → uses the stress-ng container image directly (default)
//	podman            → uses the stress-ng container image directly
//	nomad-driver-apptainer → uses docker://<image> (apptainer pulls from registry)
//	exec / raw_exec   → downloads stress-ng from the abc-tools MinIO bucket
//	                    via Nomad artifact stanza; requires operator to have run:
//	                    abc admin tools fetch stress-ng && abc admin tools push stress-ng
//
// The container image contains both stress-ng and hyperfine:
//
//	community.wave.seqera.io/library/hyperfine_stress-ng:4c75e186a00376f8
package job

import (
	"fmt"
	"math/rand"
	"strings"
	"time"

	admtools "github.com/abc-cluster/abc-cluster-cli/cmd/admin/tools"
	"github.com/abc-cluster/abc-cluster-cli/cmd/utils"
	"github.com/abc-cluster/abc-cluster-cli/internal/config"
)

// helloClusterDriverMode classifies how hello-cluster will run its workload.
type helloClusterDriverMode int

const (
	helloClusterModeContainer  helloClusterDriverMode = iota // docker, containerd, podman
	helloClusterModeApptainer                                // apptainer / singularity
	helloClusterModeExec                                     // exec / raw_exec — stress-ng from tools bucket
)

// helloClusterDriverPriority defines the order in which drivers are preferred
// for hello-cluster. The first driver found healthy on any node wins.
// Container drivers are tried before apptainer, which is tried before exec,
// to keep the richest execution environment while still working on bare HPC
// nodes that have no container runtime installed.
var helloClusterDriverPriority = []struct {
	name string
	mode helloClusterDriverMode
}{
	{"docker", helloClusterModeContainer},
	{utils.NormalizeNomadTaskDriver("containerd"), helloClusterModeContainer}, // "containerd-driver"
	{"podman", helloClusterModeContainer},
	{"nomad-driver-apptainer", helloClusterModeApptainer},
	{"apptainer", helloClusterModeApptainer}, // alternate registration name
	{"exec", helloClusterModeExec},
	{"raw_exec", helloClusterModeExec},
}

// adaptHelloClusterForAvailableDriver rewrites the hello-cluster spec to match
// the best available driver on the cluster. It is called from the hello-cluster
// submission path AFTER spec merging and CLI-flag application so that an
// explicit --driver flag is never overridden.
//
// If capabilities have not been synced (nil or empty Nodes), the spec is left
// untouched so the original containerd default takes effect and the user gets
// Nomad's own "no eligible client" error if containerd isn't present.
//
// Driver mode effects:
//
//	container  — driver name updated (docker/containerd-driver/podman); DriverConfig["image"] kept
//	apptainer  — driver set to apptainer; image prefixed with "docker://" for registry pull
//	exec       — driver set to exec; DriverConfig["image"] removed; stress-ng Nomad artifact added
func adaptHelloClusterForAvailableDriver(spec *jobSpec) {
	// Only adapt when the driver is still the default containerd-driver.
	// An explicit --driver flag means the user knows what they want.
	if spec.Driver != utils.NormalizeNomadTaskDriver("containerd") {
		return
	}
	cfg, err := config.Load()
	if err != nil {
		return // best effort
	}
	ctx := cfg.ActiveCtx()
	if ctx.Capabilities == nil || len(ctx.Capabilities.Nodes) == 0 {
		return // no capability data; leave default, let Nomad error if needed
	}

	available := map[string]bool{}
	for _, node := range ctx.Capabilities.Nodes {
		for _, d := range node.Drivers {
			available[d] = true
		}
	}

	for _, dp := range helloClusterDriverPriority {
		if !available[dp.name] {
			continue
		}
		switch dp.mode {
		case helloClusterModeContainer:
			// Just update the driver name; DriverConfig["image"] is already correct.
			spec.Driver = dp.name
		case helloClusterModeApptainer:
			spec.Driver = dp.name
			// Apptainer / nomad-driver-apptainer uses docker:// prefix to pull
			// from OCI registries at job start.
			if img, ok := spec.DriverConfig["image"]; ok {
				spec.DriverConfig["image"] = "docker://" + img
			}
		case helloClusterModeExec:
			spec.Driver = utils.NormalizeNomadTaskDriver("exec")
			delete(spec.DriverConfig, "image")
			// stress-ng comes from the abc-tools MinIO bucket via Nomad artifact.
			// Requires operator to have run:
			//   abc admin tools fetch stress-ng && abc admin tools push stress-ng
			if artifactURL, aErr := admtools.ArtifactURL("stress-ng", ""); aErr == nil {
				spec.Artifacts = append(spec.Artifacts, artifactSpec{
					Source:      artifactURL,
					Destination: "local/stress-ng",
				})
			}
			// If ArtifactURL fails (tools not pushed yet), the artifact is omitted
			// and the exec script will fail at runtime with a clear error from the
			// chmod/execute step — the user gets a message pointing to admin tools push.
		}
		return
	}
	// No driver matched in capabilities — leave spec as-is (original containerd default).
}

// helloWorldDefaultNamespace returns the Nomad namespace the built-in
// hello-cluster workload should target. Picks (in order):
//  1. active abc context's admin.abc_nodes.nomad_namespace
//  2. "default" — fallback for clusters without a pinned namespace.
func helloWorldDefaultNamespace() string {
	cfg, err := config.Load()
	if err == nil && cfg != nil {
		ctx := cfg.ActiveCtx()
		if ctx.Admin.ABCNodes != nil {
			if v := strings.TrimSpace(ctx.Admin.ABCNodes.NomadNamespace); v != "" {
				return v
			}
		}
		if ns := strings.TrimSpace(ctx.AbcNodesNomadNamespaceForCLI()); ns != "" {
			return ns
		}
	}
	return "default"
}

const (
	helloClusterScriptBase = "hello-cluster.sh"

	// helloClusterScriptBody is the template for container-driver runs
	// (docker, containerd-driver, podman, apptainer).
	// stress-ng is already in the container image; __STRESS_CMD__ is replaced
	// with the randomised stress-ng invocation at CLI time.
	helloClusterScriptBody = `#!/bin/sh
set -eu

echo "=== hello-cluster ==="
echo "timestamp=$(date -u +%Y-%m-%dT%H:%M:%SZ)"
echo "node=$(hostname)"
echo "alloc=${NOMAD_ALLOC_ID:-unknown}"
echo "scenario=${NOMAD_META_random_scenario:-unknown}"
echo ""

__STRESS_CMD__

echo ""
echo "=== hello-cluster done ==="
`

	// helloClusterExecScriptBody is the template for exec / raw_exec runs.
	// stress-ng is NOT assumed to be installed on the host OS. Instead it is
	// downloaded at job start via a Nomad artifact stanza pointing at the
	// abc-tools MinIO bucket (abc-reserved/binary_tools/stress-ng-linux-<arch>).
	// The artifact destination is always "local/stress-ng" regardless of arch.
	//
	// Operator prerequisite (one-time, per cluster):
	//   abc admin tools fetch stress-ng
	//   abc admin tools push stress-ng
	helloClusterExecScriptBody = `#!/bin/sh
set -eu

# stress-ng is fetched via Nomad artifact stanza from the abc-tools MinIO bucket.
# Artifact destination: ${NOMAD_TASK_DIR}/local/stress-ng
STRESS_NG="${NOMAD_TASK_DIR}/local/stress-ng"
if [ ! -f "${STRESS_NG}" ]; then
  echo "ERROR: stress-ng binary not found at ${STRESS_NG}"
  echo "  The abc-tools bucket may not have been provisioned for this cluster."
  echo "  Run on the operator machine:"
  echo "    abc admin tools fetch stress-ng"
  echo "    abc admin tools push stress-ng"
  exit 1
fi
chmod +x "${STRESS_NG}"

echo "=== hello-cluster (exec) ==="
echo "timestamp=$(date -u +%Y-%m-%dT%H:%M:%SZ)"
echo "node=$(hostname)"
echo "alloc=${NOMAD_ALLOC_ID:-unknown}"
echo "scenario=${NOMAD_META_random_scenario:-unknown}"
echo ""

__STRESS_CMD__

echo ""
echo "=== hello-cluster done ==="
`

	// helloClusterImage is the container image used by hello-cluster container-driver runs.
	helloClusterImage = "community.wave.seqera.io/library/hyperfine_stress-ng:4c75e186a00376f8"
)

// randomParams holds the randomised stress-ng parameters chosen at CLI time.
type randomParams struct {
	CPUStressors int    // --cpu N
	VMStressors  int    // --vm N
	VMBytes      string // --vm-bytes <size>
	IOStressors  int    // --io N
	TimeoutSecs  int    // --timeout Ns
}

// helloClusterMaxStressSecs is the upper bound for the random stress-ng
// --timeout. Combined with helloClusterWalltimeSecs (the Nomad kill_timeout
// safety net) this guarantees `abc job run hello-cluster` never runs longer
// than ~5 minutes wall-clock even in the worst case (slow image pull +
// maximum stress duration).
const helloClusterMaxStressSecs = 180 // 3 minutes — stress-ng natural timeout

// helloClusterWalltimeSecs is the Nomad walltime budget; if stress-ng has
// not finished by then (e.g. image pull stalled) Nomad kills the alloc.
const helloClusterWalltimeSecs = 5 * 60 // 5 minutes hard wall-clock cap

// newRandomParams returns randomly chosen stress-ng parameters.
// Uses a seeded local RNG so the same seed always produces the same scenario.
func newRandomParams(r *rand.Rand) randomParams {
	// CPU stressors: 1–4
	cpu := r.Intn(4) + 1

	// VM stressors: 0–2; VM bytes from a fixed set (64M, 128M, 256M, 512M)
	vm := r.Intn(3)
	vmSizes := []string{"64M", "128M", "256M", "512M"}
	vmBytes := vmSizes[r.Intn(len(vmSizes))]

	// I/O stressors: 0–2
	io := r.Intn(3)

	// Duration: 30 .. helloClusterMaxStressSecs (currently 30–180s).
	// Capped at 180s so the total alloc lifetime — image pull (variable) +
	// startup + stress-ng + wind-down — stays under helloClusterWalltimeSecs.
	timeout := 30 + r.Intn(helloClusterMaxStressSecs-30+1)

	return randomParams{
		CPUStressors: cpu,
		VMStressors:  vm,
		VMBytes:      vmBytes,
		IOStressors:  io,
		TimeoutSecs:  timeout,
	}
}

// scenarioLabel returns a compact human-readable label for the chosen params,
// e.g. "cpu:2,vm:1:128M,io:0,t:90s".  Stored in Nomad meta.
func (p randomParams) scenarioLabel() string {
	return fmt.Sprintf("cpu:%d,vm:%d:%s,io:%d,t:%ds",
		p.CPUStressors, p.VMStressors, p.VMBytes, p.IOStressors, p.TimeoutSecs)
}

// stressCmd builds the stress-ng shell command for container-driver runs
// (docker, containerd-driver, podman, apptainer). stress-ng is in the container
// image PATH so it is invoked directly.
func (p randomParams) stressCmd() string {
	return p.stressCmdWithBin("stress-ng")
}

// stressCmdExec builds the stress-ng command for exec / raw_exec runs where
// stress-ng is not on the host PATH. The binary is fetched as a Nomad artifact
// and placed at ${NOMAD_TASK_DIR}/local/stress-ng; the STRESS_NG shell variable
// (set in helloClusterExecScriptBody) is used to reference it.
func (p randomParams) stressCmdExec() string {
	return p.stressCmdWithBin(`"${STRESS_NG}"`)
}

// stressCmdWithBin builds the stress-ng invocation using the given binary
// reference (either "stress-ng" for container runs or "${STRESS_NG}" for exec).
func (p randomParams) stressCmdWithBin(bin string) string {
	var args []string
	args = append(args, bin)
	args = append(args, fmt.Sprintf("--cpu %d", p.CPUStressors))
	if p.VMStressors > 0 {
		args = append(args, fmt.Sprintf("--vm %d --vm-bytes %s", p.VMStressors, p.VMBytes))
	}
	if p.IOStressors > 0 {
		args = append(args, fmt.Sprintf("--io %d", p.IOStressors))
	}
	args = append(args, fmt.Sprintf("--timeout %ds", p.TimeoutSecs))
	args = append(args, "--metrics-brief")
	return strings.Join(args, " \\\n  ")
}

// buildHelloClusterSpec returns the default jobSpec for a hello-cluster workload.
// Resource limits are sized to accommodate 4 CPU stressors + 2 × 512 MB VM
// stressors in the worst case.
func buildHelloClusterSpec() *jobSpec {
	return &jobSpec{
		Name:         "hello-cluster",
		Namespace:    helloWorldDefaultNamespace(),
		Driver:       utils.NormalizeNomadTaskDriver("containerd"),
		DriverConfig: map[string]string{"image": helloClusterImage},
		Cores:        4,
		MemoryMB:     1536, // 3 × 512 MB to absorb worst-case VM stressors
		WalltimeSecs: helloClusterWalltimeSecs,
		Meta: map[string]string{
			"workload": "hello-cluster",
			"scenario": "pending", // overwritten by finalizeHelloCluster
		},
		ExposeNamespaceEnv: true,
		ExposeJobName:      true,
		ExposeTaskName:     true,
		ExposeAllocID:      true,
	}
}

// finalizeHelloCluster stamps submission metadata and bakes the randomised
// stress-ng command into the script body.
func finalizeHelloCluster(spec *jobSpec) (string, error) {
	if spec == nil {
		spec = buildHelloClusterSpec()
	}
	if spec.Meta == nil {
		spec.Meta = map[string]string{}
	}

	submissionID := newSubmissionID()
	spec.Meta["abc_submission_id"] = submissionID
	spec.Meta["abc_submission_time"] = time.Now().UTC().Format(time.RFC3339)

	// Seed the RNG from the submission ID so the scenario is reproducible.
	seed := int64(0)
	for i, b := range []byte(submissionID) {
		seed ^= int64(b) << (uint(i%8) * 8)
	}
	//nolint:gosec // non-cryptographic RNG intentional for random parameter selection
	r := rand.New(rand.NewSource(seed))
	params := newRandomParams(r)

	spec.Meta["random_scenario"] = params.scenarioLabel()
	spec.Meta["random_cpu"] = fmt.Sprintf("%d", params.CPUStressors)
	spec.Meta["random_vm"] = fmt.Sprintf("%d", params.VMStressors)
	spec.Meta["random_vm_bytes"] = params.VMBytes
	spec.Meta["random_io"] = fmt.Sprintf("%d", params.IOStressors)
	spec.Meta["random_timeout_secs"] = fmt.Sprintf("%d", params.TimeoutSecs)

	if spec.Name != "" {
		base := spec.Name
		if !strings.HasPrefix(base, "script-job-") {
			base = "script-job-" + base
		}
		if slug := utils.ActiveWhoamiSlug(); slug != "" {
			base = slug + "-" + base
		}
		spec.Name = fmt.Sprintf("%s-%s", base, submissionID[:8])
	}

	// Choose the script template and stress-ng invocation based on driver mode.
	// exec / raw_exec: stress-ng comes from the abc-tools MinIO bucket (artifact stanza);
	// the script references ${STRESS_NG} instead of bare "stress-ng".
	// All other drivers (container, apptainer): stress-ng is in the image.
	isExecDriver := spec.Driver == utils.NormalizeNomadTaskDriver("exec") ||
		spec.Driver == "raw_exec"
	var scriptTemplate, stressInvocation string
	if isExecDriver {
		scriptTemplate = helloClusterExecScriptBody
		stressInvocation = params.stressCmdExec()
	} else {
		scriptTemplate = helloClusterScriptBody
		stressInvocation = params.stressCmd()
	}
	script := strings.ReplaceAll(scriptTemplate, "__STRESS_CMD__", stressInvocation)

	return FinalizeJobScript(spec, helloClusterScriptBase, script)
}
