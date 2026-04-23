# Experimental abc-nodes workloads

Nomad jobs, scripts, and examples for **HashiCorp Vault**, **Supabase**, and **Seqera Wave** live here. They are **not** part of the default gateway (Caddy `Caddyfile.lan`) or Traefik static routes, and are **not** assumed by the main `deployments/abc-nodes/nomad/README.md` deployment order.

## Layout

| Path | Purpose |
|------|---------|
| `experimental/nomad/vault.nomad.hcl` | Vault (Raft) |
| `experimental/nomad/supabase.nomad.hcl` | Supabase stack |
| `experimental/nomad/wave.nomad.hcl` | Wave service |
| `experimental/nomad/tests/vault.nomad.hcl` | Batch test for Vault |
| `experimental/scripts/init-vault.sh` | Init / unseal; writes `experimental/acl/vault-keys.env` |
| `experimental/scripts/init-supabase-secrets.sh` | Nomad Variables + `experimental/acl/supabase-secrets.env` |
| `experimental/acl/vault-keys.env.example` | Example env for Vault tests / CLI |

## Opt-in deploy (from repo root)

```bash
abc context use aither
export NOMAD_TOKEN=<management-token>

# Vault
abc admin services nomad cli -- job run deployments/abc-nodes/experimental/nomad/vault.nomad.hcl
bash deployments/abc-nodes/experimental/scripts/init-vault.sh

# Supabase (+ Wave DB secret in Nomad Variables)
bash deployments/abc-nodes/experimental/scripts/init-supabase-secrets.sh
abc admin services nomad cli -- job run deployments/abc-nodes/experimental/nomad/supabase.nomad.hcl

# Wave (requires image URI / credentials per job header)
abc admin services nomad cli -- job run deployments/abc-nodes/experimental/nomad/wave.nomad.hcl
```

Re-enable LAN path routing or `*.aither` hostnames by restoring the Vault / Supabase / Wave blocks from git history into `caddy/Caddyfile.lan` and the matching routers in `nomad/traefik.nomad.hcl` after the jobs are running.
