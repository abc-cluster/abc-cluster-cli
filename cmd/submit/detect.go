package submit

import (
	"context"
	"fmt"
	"os"
	"strings"

	"github.com/abc-cluster/abc-cluster-cli/cmd/pipeline"
	"github.com/abc-cluster/abc-cluster-cli/cmd/utils"
)

type targetType int

const (
	typeUnknown  targetType = iota
	typePipeline            // abc pipeline run
	typeModule              // abc module run
	typeJob                 // abc job run --submit
)

// detectTargetType determines the dispatch mode for target.
// Priority order (first match wins):
//
//  1. forceType != "" → parse and return
//  2. conda != "" or pixi → job (wrapper mode)
//  3. local file exists → job
//  4. target starts with http:// or https:// → pipeline
//  5. target has ≥ 3 path segments → module  (e.g. nf-core/modules/bwa/mem)
//  6. target has exactly one "/" → pipeline  (owner/repo pattern)
//  7. Nomad Variables lookup of nomad/pipelines/<target> → pipeline
//  8. return typeUnknown
func detectTargetType(ctx context.Context, nc *utils.NomadClient, target, forceType, conda string, pixi bool, namespace string) (targetType, error) {
	// 1 — forced
	if forceType != "" {
		switch forceType {
		case "pipeline":
			return typePipeline, nil
		case "module":
			return typeModule, nil
		case "job":
			return typeJob, nil
		default:
			return typeUnknown, fmt.Errorf("unknown --type %q: must be pipeline, job, or module", forceType)
		}
	}

	// 2 — conda or pixi wrapper forces job mode
	if conda != "" || pixi {
		return typeJob, nil
	}

	// 3 — local file
	if _, err := os.Stat(target); err == nil {
		return typeJob, nil
	}

	// 4 — explicit URL
	if strings.HasPrefix(target, "http://") || strings.HasPrefix(target, "https://") {
		return typePipeline, nil
	}

	// 5 — ≥ 3 path segments → module (e.g. nf-core/modules/bwa/mem or nf-core/cat/fastq)
	if strings.Count(target, "/") >= 2 {
		return typeModule, nil
	}

	// 6 — exactly one "/" → owner/repo → pipeline
	if strings.Count(target, "/") == 1 {
		return typePipeline, nil
	}

	// 7 — Nomad Variables lookup
	if nc != nil {
		saved, err := pipeline.LoadPipeline(ctx, nc, target, namespace)
		if err == nil && saved != nil {
			return typePipeline, nil
		}
	}

	return typeUnknown, nil
}
