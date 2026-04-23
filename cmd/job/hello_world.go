package job

import (
	"fmt"
	"strings"
	"time"

	"github.com/abc-cluster/abc-cluster-cli/cmd/utils"
)

const (
	helloWorldScriptBase = "hello-world.sh"
	helloWorldScriptBody = `#!/bin/sh
set -eu

echo "hello from abc-nodes"
echo "timestamp=$(date -u +%Y-%m-%dT%H:%M:%SZ)"
echo "namespace=${NOMAD_NAMESPACE:-unknown}"
echo "job_name=${NOMAD_JOB_NAME:-unknown}"
echo "node_name=${NOMAD_NODE_NAME:-unknown}"
echo "alloc_id=${NOMAD_ALLOC_ID:-unknown}"
echo "task_name=${NOMAD_TASK_NAME:-unknown}"
echo "done"
`
)

func buildHelloWorldSpec() *jobSpec {
	return &jobSpec{
		Name:         "hello-world",
		Namespace:    "default",
		Driver:       utils.NormalizeNomadTaskDriver("containerd"),
		DriverConfig: map[string]string{"image": "community.wave.seqera.io/library/hyperfine_stress-ng:4c75e186a00376f8"},
		Cores:        1,
		MemoryMB:     256,
		WalltimeSecs: 3 * 60,
		Meta: map[string]string{
			"workload": "hello-world",
			"scenario": "cli_smoke",
		},
		ExposeNamespaceEnv: true,
		ExposeJobName:      true,
		ExposeTaskName:     true,
		ExposeAllocID:      true,
	}
}

func finalizeHelloWorld(spec *jobSpec) (string, error) {
	if spec == nil {
		spec = buildHelloWorldSpec()
	}
	if spec.Meta == nil {
		spec.Meta = map[string]string{}
	}
	submissionID := newSubmissionID()
	spec.Meta["abc_submission_id"] = submissionID
	spec.Meta["abc_submission_time"] = time.Now().UTC().Format(time.RFC3339)
	if spec.Name != "" {
		base := spec.Name
		if !strings.HasPrefix(base, "script-job-") {
			base = "script-job-" + base
		}
		spec.Name = fmt.Sprintf("%s-%s", base, submissionID[:8])
	}
	return FinalizeJobScript(spec, helloWorldScriptBase, helloWorldScriptBody)
}
