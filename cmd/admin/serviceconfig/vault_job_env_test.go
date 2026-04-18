package serviceconfig

import (
	"testing"

	"github.com/abc-cluster/abc-cluster-cli/cmd/utils"
)

func TestVaultDevRootTokenFromJob(t *testing.T) {
	j := &utils.NomadJob{
		TaskGroups: []utils.NomadTaskGroup{
			{
				Tasks: []utils.NomadTask{
					{Name: "vault", Env: map[string]string{"VAULT_DEV_ROOT_TOKEN_ID": " lab-tok ", "OTHER": "x"}},
				},
			},
		},
	}
	if got := vaultDevRootTokenFromJob(j); got != "lab-tok" {
		t.Fatalf("got %q", got)
	}
	if vaultDevRootTokenFromJob(&utils.NomadJob{}) != "" {
		t.Fatal("expected empty")
	}
}
