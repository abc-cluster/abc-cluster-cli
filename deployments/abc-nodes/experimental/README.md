# Experimental abc-nodes workloads (legacy / pre-Terraform)

> **Heads-up — there are TWO experimental directories with similar names:**
>
> - **THIS directory** (`deployments/abc-nodes/experimental/`) is the
>   **legacy / manual-deploy** tree. Jobs here are run with `abc admin
>   services nomad cli -- job run …` directly. **Vault** initialization
>   below still uses this path. Most other content here has been
>   superseded.
> - **`deployments/abc-nodes/nomad/experimental/`** is the **current
>   Terraform-managed** opt-in tier. `postgres`, `redis`, `wave`,
>   `supabase`, `xtdb`, and `caddy_tailscale` live there now and are
>   enabled with `enable_<name>` Terraform variables — see
>   `../terraform/README.md`.
>
> If you came here looking for Supabase / Wave / Postgres / Redis: those
> are now Terraform-managed in the **other** directory. Use that.

Nomad jobs, scripts, and examples for **HashiCorp Vault**, **Supabase**, **Seqera Wave**, and **faasd** live here. They are **not** part of the default gateway (Caddy `Caddyfile.lan`) or Traefik static routes, and are **not** assumed by the main `deployments/abc-nodes/nomad/README.md` deployment order.

## Layout

| Path | Purpose |
|------|---------|
| `experimental/nomad/vault.nomad.hcl` | Vault (Raft) |
| `experimental/nomad/supabase.nomad.hcl` | Supabase stack |
| `experimental/nomad/wave.nomad.hcl` | Wave service |
| `experimental/nomad/faasd.nomad.hcl` | OpenFaaS/faasd gateway (on hold) |
| `experimental/nomad/tests/vault.nomad.hcl` | Batch test for Vault |
| `experimental/scripts/init-vault.sh` | Init / unseal; writes `experimental/acl/vault-keys.env` |
| `experimental/scripts/init-supabase-secrets.sh` | Nomad Variables + `experimental/acl/supabase-secrets.env` |
| `experimental/scripts/setup-minio-faasd-events.sh` | Wire MinIO event webhooks to faasd functions |
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

# faasd (on hold by default in job spec; see blockers in header)
abc admin services nomad cli -- job run deployments/abc-nodes/experimental/nomad/faasd.nomad.hcl
bash deployments/abc-nodes/experimental/scripts/setup-minio-faasd-events.sh
```

Re-enable LAN path routing or `*.aither` hostnames by restoring the Vault / Supabase / Wave / faasd blocks from git history into `caddy/Caddyfile.lan` and the matching routers in `nomad/traefik.nomad.hcl` after the jobs are running.

**Supabase + Traefik:** postgres-meta is exposed on host **:8082** (container :8081) so it does not collide with `abc-nodes-traefik` web entry **:8081**.
