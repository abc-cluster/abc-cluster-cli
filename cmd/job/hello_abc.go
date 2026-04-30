// hello_abc.go — built-in stress-ng workload for cluster verification and load testing.
// Invoked via: abc job run hello-abc [flags]
//
// Randomises CPU, VM, and I/O stressor counts and a run duration at CLI time
// so each submission exercises a different resource profile.  The chosen
// parameters are stamped into Nomad meta so operators can inspect them via
// `abc job show` without reading logs.
//
// The image already contains both stress-ng and hyperfine:
//
//	community.wave.seqera.io/library/hyperfine_stress-ng:4c75e186a00376f8
package job

import (
	"fmt"
	"math/rand"
	"strings"
	"time"

	"github.com/abc-cluster/abc-cluster-cli/cmd/utils"
	"github.com/abc-cluster/abc-cluster-cli/internal/config"
)

// helloWorldDefaultNamespace returns the Nomad namespace the built-in
// hello-abc workload should target. Picks (in order):
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
	helloAbcScriptBase = "hello-abc.sh"

	// helloAbcScriptBody is a template. The final script is built by
	// finalizeHelloAbc, which replaces the __STRESS_CMD__ placeholder with
	// the randomised stress-ng invocation.
	helloAbcScriptBody = `#!/bin/sh
set -eu

echo "=== hello-abc ==="
echo "timestamp=$(date -u +%Y-%m-%dT%H:%M:%SZ)"
echo "node=$(hostname)"
echo "alloc=${NOMAD_ALLOC_ID:-unknown}"
echo "scenario=${NOMAD_META_random_scenario:-unknown}"
echo ""

__STRESS_CMD__

echo ""
echo "=== hello-abc done ==="
`

	// helloAbcImage is the container image used by hello-abc.
	helloAbcImage = "community.wave.seqera.io/library/hyperfine_stress-ng:4c75e186a00376f8"
)

// randomParams holds the randomised stress-ng parameters chosen at CLI time.
type randomParams struct {
	CPUStressors int    // --cpu N
	VMStressors  int    // --vm N
	VMBytes      string // --vm-bytes <size>
	IOStressors  int    // --io N
	TimeoutSecs  int    // --timeout Ns
}

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

	// Duration: 30–180 seconds
	timeout := 30 + r.Intn(151)

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

// stressCmd builds the stress-ng shell command for the chosen params.
func (p randomParams) stressCmd() string {
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

// buildHelloAbcSpec returns the default jobSpec for a hello-abc workload.
// Resource limits are sized to accommodate 4 CPU stressors + 2 × 512 MB VM
// stressors in the worst case.
func buildHelloAbcSpec() *jobSpec {
	return &jobSpec{
		Name:         "hello-abc",
		Namespace:    helloWorldDefaultNamespace(),
		Driver:       utils.NormalizeNomadTaskDriver("containerd"),
		DriverConfig: map[string]string{"image": helloAbcImage},
		Cores:        4,
		MemoryMB:     1536, // 3 × 512 MB to absorb worst-case VM stressors
		WalltimeSecs: 10 * 60,
		Meta: map[string]string{
			"workload": "hello-abc",
			"scenario": "pending", // overwritten by finalizeHelloAbc
		},
		ExposeNamespaceEnv: true,
		ExposeJobName:      true,
		ExposeTaskName:     true,
		ExposeAllocID:      true,
	}
}

// finalizeHelloAbc stamps submission metadata and bakes the randomised
// stress-ng command into the script body.
func finalizeHelloAbc(spec *jobSpec) (string, error) {
	if spec == nil {
		spec = buildHelloAbcSpec()
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

	// Splice the randomised stress-ng command into the script template.
	script := strings.ReplaceAll(helloAbcScriptBody, "__STRESS_CMD__", params.stressCmd())

	return FinalizeJobScript(spec, helloAbcScriptBase, script)
}
