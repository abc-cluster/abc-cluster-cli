package pipeline

import (
	"context"

	"github.com/abc-cluster/abc-cluster-cli/cmd/utils"
)

// LoadPipeline fetches a saved pipeline spec from Nomad Variables.
// Returns nil, nil if the pipeline does not exist (404).
func LoadPipeline(ctx context.Context, nc *utils.NomadClient, nameOrURL, ns string) (*PipelineSpec, error) {
	return loadPipeline(ctx, nc, nameOrURL, ns)
}

// MergeSpec applies non-zero fields from override on top of base.
func MergeSpec(base, override *PipelineSpec) *PipelineSpec {
	return mergeSpec(base, override)
}

// GenerateHeadJobHCL generates the Nomad HCL for a pipeline head job.
func GenerateHeadJobHCL(spec *PipelineSpec, nomadAddr, nomadToken, runUUID string) string {
	return generateHeadJobHCL(spec, nomadAddr, nomadToken, runUUID)
}

// NewPipelineRunUUID generates a random run identifier for a pipeline job.
func NewPipelineRunUUID() string {
	return newRunUUID()
}

// Defaults fills in zero-value fields on spec with sensible defaults.
func (s *PipelineSpec) Defaults() {
	s.defaults()
}
