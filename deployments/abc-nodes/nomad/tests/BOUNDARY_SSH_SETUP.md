# Boundary SSH Setup — Progress & Planned Steps

## Goal

Register every Nomad client node (nomad00–04, aither) as a valid SSH target in
HashiCorp Boundary so that users can connect via:

```sh
boundary connect ssh -target-id <target-id> -username abhinavsharma
```

Boundary brokers the SSH private key from Vault — no key needs to be distributed
to end-users.

---

## Architecture

```
User machine
  └── boundary connect ssh ──► Boundary Controller (aither:9200)
                                    │
                                    │ session authorize
                                    ▼
                             Boundary Worker (PKI, Nomad system job)
                                    │
                                    │ fetch private key
                                    ▼
                               Vault (aither:8200)
                               ssh-creds/data/ubuntu-aither
                                    │
                                    │ proxy SSH connection
                                    ▼
                              Target node (Tailscale IP :22)
```

Boundary workers are deployed as a Nomad **system job** (`boundary-worker.nomad.hcl`)
on all nodes; each registers with the controller over Tailscale.

---

## Current Status (2026-04-26)

### ✅ Completed

#### Boundary objects created
| Object | Details |
|--------|---------|
| Org scope | `o_QUAa2lcTrp` — `abc-nodes-org` |
| Project scope | `p_CG8EtUHJ0W` — `abc-nodes-ssh` |
| Auth method | `ampw_NSwA02krcc` — password auth |
| Vault credential store | `csvlt_OtuSxzdBxG` — `vault-ssh-ca` (Vault at `http://100.70.185.46:8200`) |
| Credential library | `clvlt_Czglvjnh8L` — `abhinavsharma-aither-key` (`ssh-creds/data/ubuntu-aither`, vault-generic) |
| Static host catalog | `hcst_pMpVX6KeMI` — `abc-nodes-hosts` |

#### Hosts registered
| Host ID | Name | Tailscale IP |
|---------|------|-------------|
| `hst_bMTtZycYy3` | aither | 100.70.185.46 |
| `hst_43KaPeNn24` | nomad00 | 100.108.199.30 |
| `hst_UoIdmZvQbR` | nomad01 | 100.77.21.36 |
| `hst_NyTmzjxDrL` | nomad02 | 100.126.253.95 |
| `hst_SYMGPTcIZq` | nomad03 | 100.X.X.X |
| `hst_vKoWe4D0Hw` | nomad04 | 100.X.X.X |

#### Host sets and targets created
| Target ID | Name | Port | Credential library |
|-----------|------|------|-------------------|
| `ttcp_49zcy83yuj` | aither-ssh | 22 | `clvlt_Czglvjnh8L` |
| `ttcp_J0SBrS1ziW` | nomad00-ssh | 22 | `clvlt_Czglvjnh8L` |
| `ttcp_e76JpZqQln` | nomad01-ssh | 22 | `clvlt_Czglvjnh8L` |
| `ttcp_7JbVjJq68J` | nomad02-ssh | 22 | `clvlt_Czglvjnh8L` |
| `ttcp_5xS9zRPt3A` | nomad03-ssh | 22 | `clvlt_Czglvjnh8L` |
| `ttcp_CljRoTbtjL` | nomad04-ssh | 22 | `clvlt_Czglvjnh8L` |

#### Workers connected (6/6)
| Worker ID | Name | Tags | Type |
|-----------|------|------|------|
| `w_QCjFYmyYCA` | abc-nodes-worker-aither | region=default, type=abc-nodes-worker | PKI |
| `w_DT0c9kLo0b` | abc-nodes-worker-nomad00 | region=sun-nomadlab, type=abc-nodes-worker | PKI |
| `w_waSnYty6Gp` | abc-nodes-worker-nomad01 | region=sun-nomadlab, type=abc-nodes-worker | PKI |
| `w_OHcyaJxLh9` | abc-nodes-worker-nomad02 | region=sun-nomadlab, type=abc-nodes-worker | PKI |
| `w_yElgCt2iR3` | abc-nodes-worker-nomad03 | region=sun-nomadlab, type=abc-nodes-worker | PKI |
| `w_wR3F0Wh3AJ` | abc-nodes-worker-nomad04 | region=sun-nomadlab, type=abc-nodes-worker | PKI |

#### SSH public key installed (via Nomad batch jobs)
- Job: `install-boundary-ssh-key.nomad.hcl`
  - Installs `ssh-ed25519 AAAAC3NzaC1lZDI1NTE5AAAAIKKq/nGuytY3yFog+oN1KEwJJ2m49KPQ8OFL/JTedPZa boundary-brokered@abc-nodes`
  - into `~abhinavsharma/.ssh/authorized_keys` on every node
  - Status: aither ✅ (pre-existing), nomad00-04 ✅

- Job: `create-boundary-ssh-user.nomad.hcl`
  - Creates OS user `abhinavsharma` on nomad nodes (they use `abhinav` by default)
  - Re-installs key and fixes ownership (`chown abhinavsharma:abhinavsharma`)
  - Status: nomad00-04 ✅ (`CREATED: user abhinavsharma … INSTALLED: SSH key`)

#### Worker filter fix
- Original (broken): `"abc-nodes-worker-nomad01" in "/name"` — go-bexpr `in` requires a
  list on the right; `/name` is a string so no workers matched → "No workers available"
- Fixed on nomad01-ssh: `"abc-nodes-worker" in "/tags/type"` — matches all 6 connected
  workers (all on Tailscale, all can reach any node's IP)
- **Remaining**: apply the same fix to nomad00, 02, 03, 04 targets (currently still have the broken `/name` filter)

---

### ❌ Blocked / Not Verified

#### `boundary connect ssh` still failing (abandoned at this point)

After fixing the worker filter on `nomad01-ssh`, the error changed from
"No workers available" to an SSH-level failure:

```
kex_exchange_identification: read: Connection reset by peer
```

Root cause analysis reached so far:
- Network: aither → `100.77.21.36:22` confirmed reachable (nc + verbose SSH test)
- User: `abhinavsharma` exists on nomad01, home dir and `.ssh/authorized_keys` present with correct key
- Shadow: `abhinavsharma:!:…` — password locked (normal for key-auth-only accounts on Ubuntu)
- sshd: `PubkeyAuthentication yes`, no `AllowUsers` restriction found
- Vault credential library: `token_status: current` — Boundary can read from Vault
- **Unknown**: whether the private key stored in Vault at `ssh-creds/data/ubuntu-aither` actually matches the installed public key, and whether Boundary correctly injects `credential_type=ssh_private_key` for a vault-generic library

The `kex_exchange_identification` reset happens before key exchange completes,
suggesting a lower-level rejection (possibly fail2ban, MaxStartups, or a PAM
issue with locked-password accounts). Not fully diagnosed.

---

## Planned Next Steps

### Step 1 — Fix worker filters on remaining targets

Apply the correct tag-based filter to the four remaining targets:

```sh
export BOUNDARY_ADDR=http://100.70.185.46:9200
for pair in "ttcp_J0SBrS1ziW:nomad00" "ttcp_7JbVjJq68J:nomad02" "ttcp_5xS9zRPt3A:nomad03" "ttcp_CljRoTbtjL:nomad04"; do
  tid="${pair%%:*}"
  boundary targets update tcp -id "$tid" \
    -egress-worker-filter '"abc-nodes-worker" in "/tags/type"'
done
```

To route each session through the co-located worker (lower latency, cleaner),
use a per-region tag filter once the `/name` CEL syntax is confirmed:

```
# Correct go-bexpr name equality syntax (needs verification):
"abc-nodes-worker-nomad01" in "/name"   # might work — /name treated as substring container
"/name" == "abc-nodes-worker-nomad01"   # rejected by parser (must start with value)
```

### Step 2 — Verify Vault credential content

Confirm the Vault KV secret at `ssh-creds/data/ubuntu-aither` has:
- `private_key` — the ed25519 private key matching the installed public key
- `username` — `abhinavsharma`

Requires a valid Vault admin token (current `dev-root-token` is expired/invalid).
Retrieve from Boundary controller init output or re-generate via `vault token create`.

### Step 3 — Verify credential library type

The library `clvlt_Czglvjnh8L` is `vault-generic` type. Confirm it is configured
with `credential_type = ssh_private_key` (Boundary attribute). If it is set to
`unspecified`, Boundary will not know to inject it as an SSH key.

Check via API:
```sh
boundary credential-libraries read -id clvlt_Czglvjnh8L -format json
```

If `credential_type` is wrong, update:
```sh
boundary credential-libraries update vault-generic \
  -id clvlt_Czglvjnh8L \
  -credential-type ssh_private_key
```

### Step 4 — Diagnose kex reset on nomad01

If Step 2 & 3 are verified OK, investigate the connection reset:

1. Check fail2ban: `fail2ban-client status sshd` (via Nomad raw_exec)
2. Check `sshd` MaxStartups: `sshd -T | grep maxstartups`
3. Try SSHing directly as `abhinavsharma` from aither with the Vault private key
4. Check PAM behaviour for locked-password accounts: `pam_unix` `common-account`

### Step 5 — Use per-node Vault paths for finer control (optional)

Create per-node credential libraries pointing to `ssh-creds/data/nomad00` etc.,
so each target has its own credential source. Requires the Vault policy
`boundary-ssh-controller` to allow `ssh-creds/data/*` (already done).

### Step 6 — abc config.yaml integration

Add Boundary (and Consul, Vault) connection details to `~/.abc/config.yaml`
so that the abc CLI can drive `boundary connect ssh` without requiring the user to
set `BOUNDARY_ADDR` manually.

Proposed schema addition (per context):

```yaml
contexts:
  abc-bootstrap:
    admin:
      services:
        boundary:
          cred_source:
            local:
              http: http://100.70.185.46:9200
              auth_method_id: ampw_NSwA02krcc
              login_name: admin
              # password stored in a secrets manager or prompted at runtime
        consul:
          cred_source:
            local:
              http: http://100.70.185.46:8500
              # token: <consul-acl-token>
        vault:
          cred_source:
            local:
              http: http://100.70.185.46:8200
              # token retrieved from keychain / env / file
```

---

## Files Created

| File | Purpose |
|------|---------|
| `tests/install-boundary-ssh-key.nomad.hcl` | Installs SSH public key for `abhinavsharma` on all nodes |
| `tests/create-boundary-ssh-user.nomad.hcl` | Creates `abhinavsharma` OS user on nomad00-04 (which default to `abhinav` user) |

---

## Quick Reference

```sh
# List all targets
BOUNDARY_ADDR=http://100.70.185.46:9200 boundary targets list -scope-id p_CG8EtUHJ0W

# List connected workers
BOUNDARY_ADDR=http://100.70.185.46:9200 boundary workers list -scope-id global

# Connect to a node (when working)
BOUNDARY_ADDR=http://100.70.185.46:9200 \
  boundary connect ssh -target-id ttcp_e76JpZqQln -username abhinavsharma

# Re-run SSH key install (idempotent)
nomad job run deployments/abc-nodes/nomad/tests/install-boundary-ssh-key.nomad.hcl

# Re-run user creation (idempotent)
nomad job run deployments/abc-nodes/nomad/tests/create-boundary-ssh-user.nomad.hcl
```
