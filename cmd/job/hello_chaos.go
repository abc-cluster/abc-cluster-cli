// hello_chaos.go — built-in stress-ng workload for chaos/load testing.
// Invoked via: abc job run hello-chaos [flags]
//
// Randomises CPU, VM, and I/O stressor counts and a run duration at CLI time
// so each submission exercises a different resource profile.  The chosen
// parameters are stamped into Nomad meta so operators can inspect them via
// `abc job show` without reading logs.
//
// The image already contains both stress-ng and hyperfine and is shared with
// the hello-world workload:
//
//	community.wave.seqera.io/library/hyperfine_stress-ng:4c75e186a00376f8
package job

import (
	"fmt"
	"math/rand"
	"strings"
	"time"

	"github.com/abc-cluster/abc-cluster-cli/cmd/utils"
)

const (
	helloChaosScriptBase = "hello-chaos.sh"

	// helloChaosScriptBody is a template.  The final script is built by
	// finalizeHelloChaos, which replaces the __STRESS_CMD__ placeholder with
	// the randomised stress-ng invocation.
	helloChaosScriptBody = `#!/bin/sh
set -eu

echo "=== hello-chaos ==="
echo "timestamp=$(date -u +%Y-%m-%dT%H:%M:%SZ)"
echo "node=$(hostname)"
echo "alloc=${NOMAD_ALLOC_ID:-unknown}"
echo "scenario=${NOMAD_META_chaos_scenario:-unknown}"
echo ""

__STRESS_CMD__

echo ""
echo "=== hello-chaos done ==="
`

	// chaosImage is the container image used by hello-chaos (same as hello-world).
	chaosImage = "community.wave.seqera.io/library/hyperfine_stress-ng:4c75e186a00376f8"
)

// chaosParams holds the randomised stress-ng parameters chosen at CLI time.
type chaosParams struct {
	CPUStressors int    // --cpu N
	VMStressors  int    // --vm N
	VMBytes      string // --vm-bytes <size>
	IOStressors  int    // --io N
	TimeoutSecs  int    // --timeout Ns
}

// newChaosParams returns randomly chosen stress-ng parameters.
// Uses a seeded local RNG so the same seed always produces the same scenario.
func newChaosParams(r *rand.Rand) chaosParams {
	// CPU stressors: 1–4
	cpu := r.Intn(4) + 1

	// VM stressors: 0–2; VM bytes from a fixed set (64M, 128M, 256M, 512M)
	vm := r.Intn(3)
	vmSizes := []string{"64M", "128M", "256M", "512M"}
	vmBytes := vmSizes[r.Intn(len(vmSizes))]

	// I/O stressors: 0–2
	io := r.Intn(3)

	// Duration: 30–180 seconds
	timeout := 30 + r.Intn(151)

	return chaosParams{
		CPUStressors: cpu,
		VMStressors:  vm,
		VMBytes:      vmBytes,
		IOStressors:  io,
		TimeoutSecs:  timeout,
	}
}

// scenarioLabel returns a compact human-readable label for the chosen params,
// e.g. "cpu:2,vm:1:128M,io:0,t:90s".  Stored in Nomad meta.
func (p chaosParams) scenarioLabel() string {
	return fmt.Sprintf("cpu:%d,vm:%d:%s,io:%d,t:%ds",
		p.CPUStressors, p.VMStressors, p.VMBytes, p.IOStressors, p.TimeoutSecs)
}

// stressCmd builds the stress-ng shell command for the chosen params.
func (p chaosParams) stressCmd() string {
	var args []string
	args = append(args, "stress-ng")
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

// buildHelloChaosSpec returns the default jobSpec for a hello-chaos workload.
// Resource limits are sized to accommodate 4 CPU stressors + 2 × 512 MB VM
// stressors in the worst case.
func buildHelloChaosSpec() *jobSpec {
	return &jobSpec{
		Name:     "hello-chaos",
		// Namespace resolved the same way as hello-world: from the active abc
		// context's admin.abc_nodes.nomad_namespace, falling back to "default".
		Namespace: helloWorldDefaultNamespace(),
		Driver:       utils.NormalizeNomadTaskDriver("containerd"),
		DriverConfig: map[string]string{"image": chaosImage},
		Cores:        4,
		MemoryMB:     1536, // 3 × 512 MB to absorb worst-case VM stressors
		WalltimeSecs: 10 * 60,
		Meta: map[string]string{
			"workload": "hello-chaos",
			"scenario": "pending", // overwritten by finalizeHelloChaos
		},
		ExposeNamespaceEnv: true,
		ExposeJobName:      true,
		ExposeTaskName:     true,
		ExposeAllocID:      true,
	}
}

// finalizeHelloChaos stamps submission metadata and bakes the randomised
// stress-ng command into the script body.
func finalizeHelloChaos(spec *jobSpec) (string, error) {
	if spec == nil {
		spec = buildHelloChaosSpec()
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
	//nolint:gosec // non-cryptographic RNG intentional for chaos parameter selection
	r := rand.New(rand.NewSource(seed))
	params := newChaosParams(r)

	spec.Meta["chaos_scenario"] = params.scenarioLabel()
	spec.Meta["chaos_cpu"] = fmt.Sprintf("%d", params.CPUStressors)
	spec.Meta["chaos_vm"] = fmt.Sprintf("%d", params.VMStressors)
	spec.Meta["chaos_vm_bytes"] = params.VMBytes
	spec.Meta["chaos_io"] = fmt.Sprintf("%d", params.IOStressors)
	spec.Meta["chaos_timeout_secs"] = fmt.Sprintf("%d", params.TimeoutSecs)

	if spec.Name != "" {
		base := spec.Name
		if !strings.HasPrefix(base, "script-job-") {
			base = "script-job-" + base
		}
		spec.Name = fmt.Sprintf("%s-%s", base, submissionID[:8])
	}

	// Splice the randomised stress-ng command into the script template.
	script := strings.ReplaceAll(helloChaosScriptBody, "__STRESS_CMD__", params.stressCmd())

	return FinalizeJobScript(spec, helloChaosScriptBase, script)
}
