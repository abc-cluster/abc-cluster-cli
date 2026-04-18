package serviceconfig

import (
	"context"
	"fmt"
	"io"
	"strings"

	"github.com/abc-cluster/abc-cluster-cli/cmd/utils"
)

// vaultAccessKeyFromNomadJob returns VAULT_DEV_ROOT_TOKEN_ID from the registered
// Nomad job spec (lab -dev Vault only). Empty if unset or job unreadable.
func vaultDevRootTokenFromJob(j *utils.NomadJob) string {
	if j == nil {
		return ""
	}
	for _, tg := range j.TaskGroups {
		for _, t := range tg.Tasks {
			if t.Env == nil {
				continue
			}
			if v := strings.TrimSpace(t.Env["VAULT_DEV_ROOT_TOKEN_ID"]); v != "" {
				return v
			}
		}
	}
	return ""
}

// vaultSyncUpdatesFromNomad fetches the registered abc-nodes-vault job and, when
// the job is running and the task env defines VAULT_DEV_ROOT_TOKEN_ID, returns a
// config update for admin.services.vault.access_key so `vault cli` picks up VAULT_TOKEN.
func vaultSyncUpdatesFromNomad(ctx context.Context, out io.Writer, nc *utils.NomadClient, ns, canon string, jobByID map[string]utils.NomadJobStub) ([]syncKV, error) {
	stub, ok := jobByID["abc-nodes-vault"]
	if !ok || !strings.EqualFold(stub.Status, "running") {
		return nil, nil
	}
	job, err := nc.GetJob(ctx, "abc-nodes-vault", ns)
	if err != nil {
		return nil, fmt.Errorf("get job abc-nodes-vault: %w", err)
	}
	tok := vaultDevRootTokenFromJob(job)
	if tok == "" {
		fmt.Fprintln(out, "skip vault token sync: job has no VAULT_DEV_ROOT_TOKEN_ID in task env (non-dev Vault?)")
		return nil, nil
	}
	fmt.Fprintln(out, "vault token sync: set admin.services.vault.access_key from Nomad job spec (lab -dev)")
	key := "contexts." + canon + ".admin.services.vault.access_key"
	return []syncKV{{key, tok}}, nil
}
