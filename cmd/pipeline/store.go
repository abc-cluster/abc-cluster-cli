package pipeline

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"github.com/abc-cluster/abc-cluster-cli/cmd/utils"
)

const pipelineVarPrefix = "nomad/pipelines/"

// varPath returns the Nomad Variable path for a named pipeline.
func varPath(name string) string {
	return pipelineVarPrefix + name
}

// savePipeline creates or replaces a pipeline spec in Nomad Variables.
func savePipeline(ctx context.Context, nc *utils.NomadClient, spec *PipelineSpec) error {
	data, err := json.Marshal(spec)
	if err != nil {
		return fmt.Errorf("serialising pipeline spec: %w", err)
	}
	return nc.PutVariable(ctx, varPath(spec.Name), spec.Namespace, map[string]string{
		"spec": string(data),
	})
}

// loadPipeline fetches and deserialises a pipeline spec from Nomad Variables.
// Returns nil, nil if the variable does not exist (404).
func loadPipeline(ctx context.Context, nc *utils.NomadClient, name, namespace string) (*PipelineSpec, error) {
	v, err := nc.GetVariable(ctx, varPath(name), namespace)
	if err != nil {
		if strings.Contains(err.Error(), "404") {
			return nil, nil
		}
		return nil, fmt.Errorf("fetching pipeline %q: %w", name, err)
	}
	raw, ok := v.Items["spec"]
	if !ok {
		return nil, fmt.Errorf("pipeline variable %q has no 'spec' key", name)
	}
	var spec PipelineSpec
	if err := json.Unmarshal([]byte(raw), &spec); err != nil {
		return nil, fmt.Errorf("parsing pipeline spec for %q: %w", name, err)
	}
	return &spec, nil
}

// listPipelines returns all saved pipeline stubs from Nomad Variables.
func listPipelines(ctx context.Context, nc *utils.NomadClient, namespace string) ([]pipelineStub, error) {
	stubs, err := nc.ListVariables(ctx, pipelineVarPrefix, namespace)
	if err != nil {
		return nil, fmt.Errorf("listing pipelines: %w", err)
	}
	var out []pipelineStub
	for _, s := range stubs {
		name := strings.TrimPrefix(s.Path, pipelineVarPrefix)
		out = append(out, pipelineStub{
			Name:       name,
			Path:       s.Path,
			ModifyTime: time.Unix(0, s.ModifyTime),
		})
	}
	return out, nil
}

// deletePipeline removes a pipeline spec from Nomad Variables.
func deletePipeline(ctx context.Context, nc *utils.NomadClient, name, namespace string) error {
	return nc.DeleteVariable(ctx, varPath(name), namespace)
}

// pipelineStub is a lightweight summary for listing.
type pipelineStub struct {
	Name       string
	Path       string
	ModifyTime time.Time
}
