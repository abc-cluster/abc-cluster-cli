// Package job — nomad_client.go
//
// Type aliases so all existing job code continues to reference the shared
// NomadClient without modification. The implementation lives in cmd/utils.
package job

import (
	"github.com/abc-cluster/abc-cluster-cli/cmd/utils"
)

// nomadClient is a package-local alias for the shared client type.
type nomadClient = utils.NomadClient

func newNomadClient(addr, token, region string) *nomadClient {
	return utils.NewNomadClient(addr, token, region)
}

// Re-export wire types so other packages (tests, etc.) that import cmd/job
// can still reference them without importing cmd/utils directly.
type (
	NomadJobStub            = utils.NomadJobStub
	NomadJob                = utils.NomadJob
	NomadTaskGroup          = utils.NomadTaskGroup
	NomadTask               = utils.NomadTask
	NomadAllocStub          = utils.NomadAllocStub
	NomadTaskState          = utils.NomadTaskState
	NomadEvaluation         = utils.NomadEvaluation
	NomadRegisterResponse   = utils.NomadRegisterResponse
	NomadDeregisterResponse = utils.NomadDeregisterResponse
	NomadDispatchResponse   = utils.NomadDispatchResponse
	NomadPlanResponse       = utils.NomadPlanResponse
	NomadPlanAnnotations    = utils.NomadPlanAnnotations
	NomadDesiredUpdates     = utils.NomadDesiredUpdates
	NomadJobDiff            = utils.NomadJobDiff
	NomadLogFrame           = utils.NomadLogFrame
)
