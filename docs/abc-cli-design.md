# `abc` CLI — Command Design Specification v5

TODO:
-  `abc job run --submit` to be `abc script run` i.e. the `--submit` should be the default behaviour


> **Status:** Design draft — no implementation
> **Prototype baseline:** v0.1.4 (`pipeline run`, `job run`, `data upload/encrypt/decrypt`)
> **Language:** Go (Cobra + Viper)
> **Nomad client:** `github.com/hashicorp/nomad/api` — same package used by the Nomad CLI itself
> **ID convention:** Prefixed short IDs for ABC resources (`run-`, `ds-`, `ws-`, `auto-`, `dta-`, `pol-`, `usr-`); Nomad-native IDs for jobs and allocations

---

## 1. Design Principles

| Principle | Rationale |
|---|---|
| **Persona-aware surface** | Commands are grouped by concern, not by internal component |
| **Sovereignty-first flags** | `--region` is a first-class global flag on every command that touches data or compute |
| **Pipe-friendly** | Commands that create resources print the resource ID to stdout; everything else goes to stderr |
| **Dry-run everywhere** | Any mutating command accepts `--dry-run` |
| **Context-aware** | A context holds endpoint + token + workspace + region; operators switch contexts, not flags |
| **Consistent verbs** | `list`, `show`, `create`, `delete`, `use` across all resource groups |
| **User-scoped by default** | All commands operate on the authenticated user's profile. Admins can pass `--user` to act on behalf of others. |

---

## 2. Nomad API Client

All `abc job`, `abc compute`, and Nomad-layer operations are backed by `github.com/hashicorp/nomad/api` — the same Go package used internally by the Nomad CLI. Pin it to a commit matching your deployed Nomad server version in `go.mod`:

```
require github.com/hashicorp/nomad/api v0.0.0-<commit-matching-nomad-v1.9.4>
```

The client is constructed from the ABC-cluster resolved config rather than Nomad's own env vars, to avoid silent conflicts between `NOMAD_ADDR`/`NOMAD_TOKEN` and ABC-cluster's `ABC_API_ENDPOINT`/`ABC_ACCESS_TOKEN`:

```go
cfg := api.DefaultConfig()
cfg.Address  = resolvedEndpoint  // from ABC_API_ENDPOINT or active context
cfg.SecretID = resolvedToken     // from ABC_ACCESS_TOKEN or active context
cfg.Region   = resolvedRegion    // from --region flag or context default
```

Key API surface per `abc` command group:

| `abc` command | Nomad API call | Notes |
|---|---|---|
| `abc job run --submit` | `Jobs().ParseHCL()` → `Jobs().Register()` | HCL string → `*api.Job` → submit; avoids implementing a second HCL parser |
| `abc job run --dry-run` | `Jobs().Plan()` | Server-side feasibility check; accounts for real cluster state, node constraints, and resource pressure |
| `abc job list` | `Jobs().List()` | Returns `[]*api.JobListStub`; supports blocking queries via `WaitIndex` |
| `abc job show` | `Jobs().Info()` + `Jobs().Allocations()` | Full job struct plus allocation list |
| `abc job stop` | `Jobs().Deregister()` | `purge=true` when `--purge` is passed |
| `abc job dispatch` | `Jobs().Dispatch()` | Supports `idPrefixTemplate` (Nomad ≥1.6) for deterministic dispatch IDs in `abc automation` |
| `abc job status` | `Jobs().Info()` + `Evaluations().List()` | Compact one-liner for scripting and CI |
| `abc job logs` | `AllocFS().Logs()` | Returns `<-chan *api.StreamFrame`; correct streaming primitive for `--follow` |
| `abc compute nodes *` | `Nodes().List()` / `Info()` / `UpdateDrain()` | Node lifecycle |
| `abc compute allocations *` | `Allocations().List()` / `Info()` | Allocation detail |
| `abc status --watch` | Blocking queries: `QueryOptions{WaitIndex, WaitTime}` | State change polling without busy-waiting |

---

## 3. Global Flags

| Flag | Short | Env var | Description |
|---|---|---|---|
| `--url` | `-u` | `ABC_API_ENDPOINT` | ABC-cluster API endpoint URL |
| `--access-token` | `-t` | `ABC_ACCESS_TOKEN` | Access token |
| `--workspace` | `-w` | `ABC_WORKSPACE_ID` | Workspace ID |
| `--region` | `-r` | `ABC_REGION` | Nomad region (= jurisdiction boundary) |
| `--output` | `-o` | `ABC_OUTPUT` | `table` (default), `json`, `yaml` |
| `--context` | | `ABC_CONTEXT` | Named config context |
| `--dry-run` | | | Print what would happen without executing |
| `--verbose` | `-v` | | Debug output |
| `--no-color` | | `NO_COLOR` | Disable terminal colours |

---

## 4. Environment Variables

| Variable | Description |
|---|---|
| `ABC_API_ENDPOINT` | API base URL |
| `ABC_ACCESS_TOKEN` | Primary access token |
| `ABC_UPLOAD_TOKEN` | Dedicated tus upload token (falls back to `ABC_ACCESS_TOKEN`) |
| `ABC_WORKSPACE_ID` | Default workspace |
| `ABC_REGION` | Default Nomad region / jurisdiction |
| `ABC_CONTEXT` | Active config context name |
| `ABC_OUTPUT` | Default output format |
| `ABC_CONFIG_FILE` | Override config file path (default: `~/.abc/config.yaml`) |
| `ABC_CRYPT_PASSWORD` | rclone crypt password |
| `ABC_CRYPT_SALT` | rclone crypt salt |

---

## 5. Command Tree

```
abc
├── auth        login · logout · whoami · token · refresh
├── config      init · set · get · list · unset
├── context     list · show · add · use · remove
├── workspace   list · show · create · delete · use · members (list/add/remove)
├── secret      list · show · create · delete · logs
├── ssh         connect to (or print SSH command for) an accessible node; filter by datacenter or pool
├── status
├── pipeline    run · list · show · cancel · resume · delete · logs · params (show/validate)
├── job         run [--submit|--dry-run] · translate · list · show · stop · dispatch · logs · status
├── data        upload · download · list · show · delete · move · stat · logs · encrypt · decrypt
├── automation  list · show · create · enable · disable · delete · logs · runs · triggers
├── storage     buckets (list/create/delete/stat) · objects (list/get/put/delete/stat)
├── compute     nodes (list/show/drain/undrain) · datacenters (list/show)
│               allocations (list/show/logs) · hpc (list/status/jobs)
├── policy      list · show · validate · logs · audit · residency
├── budget      summary · list · show · report · logs
├── compliance  status · audit · residency · dta (list/show/validate) · report
├── admin       users (list/create/delete/token) · health · audit · backup · version
├── join
├── chat
└── version
```

---

## 6. Command Reference

---

### 5.1 `abc auth`

#### `abc auth login`

```
$ abc auth login

  ABC-cluster login

  API endpoint [https://api.abc-cluster.io]: https://api.org-a.example
  Access token: ••••••••••••••••••••••••••••••••

  ✓ Authenticated as admin@org-a.example
  ✓ Default workspace: ws-org-a-01 (Org-A Genomics)
  ✓ Default region:    za-cpt
  ✓ Context saved as:  org-a-za-cpt
```

#### `abc auth logout`

```
$ abc auth logout

  ✓ Token revoked
  ✓ Context org-a-za-cpt cleared
```

#### `abc auth whoami`

```
$ abc auth whoami

  User        admin@org-a.example
  Role        admin
  Plan        pro  (chat enabled)
  Workspace   ws-org-a-01  (Org-A Genomics)
  Region      za-cpt
  Endpoint    https://api.org-a.example
  Context     org-a-za-cpt
  Token       eyJ...c3Rh  (expires 2025-09-01)
```

#### `abc auth token`

```
$ abc auth token
eyJhbGciOiJIUzI1NiIsInR5cCI6IkpXVCJ9.eyJzdWIiOiJhZG1pbkBvcmctYS5leGFtcGxlIn0.c3Rh
```

#### `abc auth refresh`

```
$ abc auth refresh

  ✓ Token refreshed
  ✓ New expiry: 2025-12-01
```

---

### 5.2 `abc config`

#### `abc config init`

```
$ abc config init

  ABC-cluster configuration setup

  API endpoint [https://api.abc-cluster.io]: https://api.org-a.example
  Access token: ••••••••••••••••••••••••••••••••
  Default workspace [ws-org-a-01]:
  Default region [za-cpt]:
  Default output format (table/json/yaml) [table]:

  ✓ Config written to ~/.abc/config.yaml
```

#### `abc config set <key> <value>`

```
$ abc config set defaults.output json

  ✓ Set defaults.output = json
```

#### `abc config get <key>`

```
$ abc config get defaults.region

  za-cpt
```

#### `abc config list`

```
$ abc config list

  KEY                                    VALUE
  active_context                         org-a-za-cpt
  defaults.output                        table
  defaults.region                        za-cpt
  defaults.workspace                     ws-org-a-01
  contexts.org-a-za-cpt.url             https://api.org-a.example
  contexts.org-a-za-cpt.access_token    eyJ...•••• (masked)
  contexts.org-b-ke-nbi.url             https://api.org-b.example
  contexts.org-b-ke-nbi.access_token    eyJ...•••• (masked)
```

#### `abc config unset <key>`

```
$ abc config unset defaults.output

  ✓ Unset defaults.output (reverts to environment variable or built-in default)
```

---

### 5.3 `abc context`

#### `abc context list`

```
$ abc context list

  NAME            ENDPOINT                   WORKSPACE       REGION    ACTIVE
  org-a-za-cpt    https://api.org-a.example  ws-org-a-01     za-cpt    *
  org-b-ke-nbi    https://api.org-b.example  ws-org-b-01     ke-nbi
  org-c-mz-map    https://api.org-c.example  ws-org-c-01     mz-map
  org-d-be-bru    https://api.org-d.example  ws-org-d-01     be-bru
```

#### `abc context show`

```
$ abc context show

  Context     org-a-za-cpt  (active)
  Endpoint    https://api.org-a.example
  Workspace   ws-org-a-01
  Region      za-cpt
  Token       eyJ...c3Rh  (expires 2025-09-01)
```

#### `abc context add <n>`

```
$ abc context add org-b-ke-nbi \
    --url https://api.org-b.example \
    --token eyJ... \
    --workspace ws-org-b-01 \
    --region ke-nbi

  ✓ Context org-b-ke-nbi added
```

#### `abc context use <n>`

```
$ abc context use org-b-ke-nbi

  ✓ Active context → org-b-ke-nbi  (ke-nbi / ws-org-b-01)
```

#### `abc context remove <n>`

```
$ abc context remove org-c-mz-map

  Remove context org-c-mz-map? [y/N]: y
  ✓ Context org-c-mz-map removed
```

---

### 5.4 `abc workspace`

#### `abc workspace list`

```
$ abc workspace list

  ID              NAME                  REGION    MEMBERS  PIPELINES  CREATED
  ws-org-a-01     Org-A Genomics        za-cpt    12       48         2024-01-15
  ws-org-b-01     Org-B Surveillance    ke-nbi    6        19         2024-03-02
  ws-org-c-01     Org-C Research        mz-map    4        11         2024-04-10
  ws-org-d-01     Org-D Research        be-bru    8        27         2024-02-20
  ws-consortium   Consortium-A          za-cpt    22       83         2024-01-15
```

#### `abc workspace show`

```
$ abc workspace show ws-org-a-01

  ID          ws-org-a-01
  Name        Org-A Genomics
  Region      za-cpt
  Description Genomics research workspace — Org-A
  Members     12
  Pipelines   48
  Runs        1,204
  Created     2024-01-15
  Owner       admin@org-a.example
```

#### `abc workspace create <n>`

```
$ abc workspace create "Org-E Research" \
    --description "New partner institution workspace" \
    --region za-jhb

  ✓ Workspace created
  ID    ws-org-e-01
```

#### `abc workspace use <id>`

```
$ abc workspace use ws-consortium

  ✓ Active workspace → ws-consortium  (Consortium-A / za-cpt)
```

#### `abc workspace members list`

```
$ abc workspace members list

  USER                       ROLE          ADDED
  admin@org-a.example        admin         2024-01-15
  maintainer@org-a.example   maintainer    2024-01-15
  launcher@org-a.example     launcher      2024-02-01
  viewer@org-a.example       viewer        2024-03-10
  student@org-a.example      viewer        2024-05-20
```

#### `abc workspace members add <user>`

```
$ abc workspace members add launcher@org-b.example --role launcher

  ✓ launcher@org-b.example added to ws-org-a-01 as launcher
```

#### `abc workspace members remove <user>`

```
$ abc workspace members remove student@org-a.example

  Remove student@org-a.example from ws-org-a-01? [y/N]: y
  ✓ Removed
```


### 5.6 `abc secret`

Manage named secrets stored in the ABC-cluster Vault backend. Access is scoped to the authenticated user's workspace. Secret values are masked by default in all terminal output.

> **Backend note:** Secrets are stored in Vault and accessed through the ABC API layer. The CLI never communicates with Vault directly.

#### `abc secret list`

```
$ abc secret list

  NAME                  CREATED               UPDATED
  db-password           2024-10-01 08:12:00   2024-11-01 09:00:00
  api-key-ncbi          2024-09-15 14:30:00   2024-10-30 09:12:44
  gcp-service-account   2024-10-20 11:45:00   2024-10-25 07:22:00

  3 secrets in ws-za-01
```

#### `abc secret show <name>`

```
$ abc secret show api-key-ncbi

  Name      api-key-ncbi
  Version   3
  Created   2024-09-15 14:30:00
  Updated   2024-10-30 09:12:44
  Value     ••••••••••••••••  (use --reveal to display)

$ abc secret show api-key-ncbi --reveal

  Name      api-key-ncbi
  Version   3
  Created   2024-09-15 14:30:00
  Updated   2024-10-30 09:12:44
  Value     ABCD1234XYZ-secret-value
```

Flags:

| Flag | Description |
|---|---|
| `--reveal` | Print the secret value in plaintext instead of masking it |
| `--version <n>` | Show a specific historical version |

#### `abc secret create <name>`

Creates a new secret, or rotates (adds a new version of) an existing one.

```
$ abc secret create db-password --value=S3cr3t!
  ✓ Secret db-password created  (version 1)

$ echo "S3cr3t!" | abc secret create db-password
  ✓ Secret db-password rotated  (version 2)

$ abc secret create gcp-service-account --from-file=./sa.json
  ✓ Secret gcp-service-account created  (version 1)

$ abc secret create ncbi-key --from-env=NCBI_API_KEY
  ✓ Secret ncbi-key created  (version 1)
```

Flags:

| Flag | Description |
|---|---|
| `--value <v>` | Secret value inline (prefer stdin pipe for sensitive values) |
| `--from-file <path>` | Read value from a local file |
| `--from-env <VAR>` | Capture current value of a local environment variable |
| `--dry-run` | Validate input without writing |

#### `abc secret delete <name>`

Deletes a secret and all its versions.

```
$ abc secret delete db-password

  Delete secret db-password and all 2 versions? [y/N]: y

  ✓ Secret db-password deleted
```

Flags:

| Flag | Description |
|---|---|
| `--confirm` | Skip the confirmation prompt |
| `--dry-run` | Show what would be deleted without deleting |

#### `abc secret logs <name>`

Combined audit and rotation log for a single secret — access events (READ, USE) and lifecycle events (CREATE, ROTATE, DELETE) in one stream.

```
$ abc secret logs api-key-ncbi

  TIMESTAMP             EVENT    VERSION   ACTOR                      SOURCE IP
  2024-11-01 09:14:02   READ     3         admin@za-site.example      100.104.12.88
  2024-10-30 09:12:44   ROTATE   3         admin@za-site.example      100.104.12.88
  2024-10-15 07:00:11   READ     2         pipeline-runner@internal   —
  2024-09-15 14:30:00   CREATE   1         admin@za-site.example      100.104.12.88
```

Flags:

| Flag | Description |
|---|---|
| `--limit <n>` | Number of entries to return (default: 50) |
| `--since <time>` | Show events since a point in time (e.g. `2024-10-01`, `24h`, `7d`) |
| `--event <type>` | Filter by event type: `READ`, `ROTATE`, `CREATE`, `DELETE` |
| `--follow` | Stream new events as they arrive |

---

### 5.7 `abc ssh`

Opens an interactive SSH session to a node the user has access to, or prints the equivalent SSH command for scripting. Node discovery is via the ABC API.

When a TTY is present the CLI replaces itself with the SSH process (`exec`), giving the terminal fully to SSH — window resize, `Ctrl-C`, and all signals work natively. When stdout is not a TTY, the SSH command is printed instead.

> **Prerequisite:** Nodes are only reachable via Tailscale. Ensure `tailscale status` shows the target node as reachable before connecting. SSH key authorization is out of scope — the user's `~/.ssh` keys must already be authorised on target nodes.

#### Connect (auto-select when one node matches)

```
$ abc ssh --datacenter za-cpt-dc2 --pool gpu-nodes

  Connecting to za-node-104  (100.104.12.88)...
  [ssh session begins]
```

#### Interactive node selector (multiple matches)

```
$ abc ssh

  Select a node:
  > za-node-104   za-cpt-dc2   gpu-nodes    ready
    za-node-105   za-cpt-dc2   gpu-nodes    ready
    ke-node-012   ke-nbi-dc1   compute      ready

  [↑↓ to move, Enter to select, / to filter, q to quit]
```

#### Connect to a named node directly

```
$ abc ssh za-node-104

  Connecting to za-node-104  (100.104.12.88)...
  [ssh session begins]
```

#### Print command (non-TTY / `--print` flag)

```
$ abc ssh za-node-104 --print
ssh ubuntu@100.104.12.88

$ abc ssh za-node-104 --print | pbcopy   # copy to clipboard
```

When stdout is not a TTY (e.g. piped to a script), `--print` behaviour is the default regardless of the flag.

Flags:

| Flag | Description |
|---|---|
| `--datacenter <dc>` | Filter candidate nodes by datacenter |
| `--pool <pool>` | Filter candidate nodes by node pool |
| `--print` | Print SSH command to stdout instead of executing it |
| `--user <u>` | Override the SSH username (default: from API node record) |
| `--port <p>` | Override the SSH port (default: from API node record, usually 22) |

---

---

### 5.5 `abc status`

User-facing dashboard scoped to the authenticated user's own workspace and profile. Not an infrastructure health check — that is `abc admin health`.

```
$ abc status

  ╔══════════════════════════════════════════════════════════════════════════╗
  ║  ABC-cluster  ·  admin@org-a.example  ·  ws-org-a-01  ·  za-cpt        ║
  ║  2024-11-01 09:38:22                                                    ║
  ╚══════════════════════════════════════════════════════════════════════════╝

  PIPELINE RUNS
  Status        Count   Last run
  Running       1       batch-47             started 1h 24m ago
  Succeeded     3       rnaseq-nov           finished 2024-10-29
  Failed        1       taxprofiler-003      2024-10-30  →  abc pipeline resume run-g7h8i9
  Cancelled     0       —

  DATA
  Objects       1,204   objects in ws-org-a-01
  Total size    56.2 TB
  Residency     ✓ compliant  (0 issues)
  Encrypted     ✓ all objects

  AUTOMATIONS
  Active        3       (2 scheduled, 1 event-triggered)
  Last fired    viralrecon-weekly-za    2024-10-28 06:00
  Warnings      0

  BUDGET  (November 2024, month to date)
  Compute       R   47.92  (run-a1b2c3 in progress)
  Storage       R  441.20
  Total         R  489.12  of R 5,000.00 budget  (9.8%)

  COMPLIANCE
  POPIA         ✓ compliant
  Kenya DPA     ⚠ dta-za-ke-001 expires in 14 days
  GDPR          ✓ compliant

  ALERTS
  ⚠  DTA dta-za-ke-001 expires 2024-11-15 — run: abc compliance dta show dta-za-ke-001
  ⚠  MinIO mz-map-dc1 disk at 94% — contact your Server Manager
```

Flags:

| Flag | Description |
|---|---|
| `--watch` | Refresh every 5 seconds (live dashboard mode) |
| `--refresh <n>` | Set refresh interval in seconds when using `--watch` |

---

### 5.6 `abc pipeline`

#### `abc pipeline run`

```
$ abc pipeline run \
    --pipeline nf-core/viralrecon \
    --revision 2.6.0 \
    --profile test,singularity \
    --params-file params/batch-47.yaml \
    --work-dir minio://za-cpt/consortium-work/runs \
    --region za-cpt \
    --name batch-47

  Submitting pipeline run...

  ✓ Pipeline run submitted
  ID        run-a1b2c3
  Name      batch-47
  Pipeline  nf-core/viralrecon @ 2.6.0
  Region    za-cpt
  Work dir  minio://za-cpt/consortium-work/runs/run-a1b2c3

  Track progress:
    abc pipeline logs run-a1b2c3 --follow
    abc pipeline show run-a1b2c3
```

#### `abc pipeline list`

```
$ abc pipeline list

  ID          NAME              PIPELINE              STATUS     REGION   STARTED              DURATION
  run-a1b2c3  batch-47          nf-core/viralrecon    RUNNING    za-cpt   2024-11-01 08:14     1h 22m
  run-d4e5f6  tb-ke-batch-12    nf-core/bactmap       SUCCEEDED  ke-nbi   2024-10-31 14:00     3h 07m
  run-g7h8i9  taxprofiler-003   nf-core/taxprofiler   FAILED     mz-map   2024-10-30 09:30     0h 41m
  run-j1k2l3  rnaseq-nov        nf-core/rnaseq        SUCCEEDED  za-cpt   2024-10-29 11:15     5h 52m
  run-m4n5o6  batch-46          nf-core/viralrecon    SUCCEEDED  za-cpt   2024-10-28 08:00     2h 18m
```

#### `abc pipeline show <id>`

```
$ abc pipeline show run-a1b2c3

  ID          run-a1b2c3
  Name        batch-47
  Pipeline    nf-core/viralrecon
  Revision    2.6.0
  Profile     test,singularity
  Status      RUNNING
  Region      za-cpt
  Datacenter  za-cpt-dc1
  Work dir    minio://za-cpt/consortium-work/runs/run-a1b2c3
  Started     2024-11-01 08:14:32
  Duration    1h 22m 14s
  Submitted   admin@org-a.example

  TASKS
  PROCESS                          SUBMITTED  RUNNING  SUCCEEDED  FAILED  CACHED
  VIRALRECON:FASTQC                48         0        48         0       0
  VIRALRECON:TRIM_GALORE           48         12       36         0       0
  VIRALRECON:BOWTIE2_ALIGN         0          0        0          0       0
  VIRALRECON:IVAR_VARIANTS         0          0        0          0       0

  COST (ZAR)
  Compute      R 42.18
  Storage      R  3.07
  Total        R 45.25  (estimated, run in progress)
```

#### `abc pipeline cancel <id>`

```
$ abc pipeline cancel run-a1b2c3

  Cancel run-a1b2c3 (batch-47)? [y/N]: y
  ✓ Cancellation requested
  ✓ 12 running allocations signalled
```

#### `abc pipeline resume <id>`

```
$ abc pipeline resume run-g7h8i9

  Resuming run-g7h8i9 (taxprofiler-003)...

  ✓ Resumed as run-p9q0r1
  ID        run-p9q0r1
  Resuming  run-g7h8i9
  Cached    14 tasks reused from previous run
```

#### `abc pipeline delete <id>`

```
$ abc pipeline delete run-j1k2l3

  Delete run-j1k2l3 (rnaseq-nov)? Work directory will NOT be deleted. [y/N]: y
  ✓ Run record deleted
```

#### `abc pipeline logs <id>`

```
$ abc pipeline logs run-a1b2c3 --follow

  [08:14:32] executor   > nf-nomad
  [08:14:33] executor   > Submitting job viralrecon-a1b2c3-stage-0 to za-cpt
  [08:14:35] process    > VIRALRECON:FASTQC (ZA-INST-2024-001)   [  0%] 0 of 48
  [08:15:01] process    > VIRALRECON:FASTQC (ZA-INST-2024-001)   [ 12%] 6 of 48
  [08:16:44] process    > VIRALRECON:FASTQC (ZA-INST-2024-012)   [ 25%] 12 of 48
  [08:22:10] process    > VIRALRECON:FASTQC                       [100%] 48 of 48 ✓
  [08:22:11] process    > VIRALRECON:TRIM_GALORE (ZA-INST-2024-001)  [  0%] 0 of 48
  ...
```

Flags:

| Flag | Description |
|---|---|
| `--follow` / `-f` | Stream logs in real time |
| `--task` | Filter to a specific Nextflow process name |
| `--since` | Show logs from this timestamp onward |

#### `abc pipeline params show <pipeline>`

```
$ abc pipeline params show nf-core/viralrecon

  PARAMETER          TYPE     REQUIRED  DEFAULT    DESCRIPTION
  input              string   yes       —          Samplesheet CSV path
  platform           string   yes       —          illumina / nanopore
  genome             string   no        —          Reference genome key
  primer_set         string   no        artic      Primer scheme
  primer_set_version integer  no        3          Primer scheme version
  skip_fastqc        boolean  no        false      Skip FastQC
  skip_variants      boolean  no        false      Skip variant calling
  outdir             string   yes       —          Output directory
```

#### `abc pipeline params validate <pipeline>`

```
$ abc pipeline params validate nf-core/viralrecon \
    --params-file params/batch-47.yaml

  Validating params/batch-47.yaml against nf-core/viralrecon schema...

  PARAMETER    STATUS   VALUE
  input        ✓ OK     samplesheets/batch-47.csv
  platform     ✓ OK     illumina
  genome       ✓ OK     MN908947.3
  primer_set   ✓ OK     artic
  outdir       ✓ OK     minio://za-cpt/consortium-results/batch-47

  ✓ All parameters valid
```

---

### 5.7 `abc job`

Ad-hoc Nomad batch jobs submitted from annotated shell scripts. The `run` sub-command is the HCL generator; the remaining sub-commands manage job lifecycle. All operations use `github.com/hashicorp/nomad/api` directly.

---

#### `#ABC` script preamble

Scripts declare job configuration via `#ABC` comment directives at the top of the file. The `abc job run` command parses these directives, generates Nomad HCL, then optionally submits it via `Jobs().ParseHCL()` → `Jobs().Register()`.

There are three classes of directive, each with distinct semantics:

**Class 1 — Scheduler directives:** Configure how Nomad places the job. Map directly to HCL stanza fields.

| Directive | Type | HCL field | Description |
|---|---|---|---|
| `--name=<string>` | string | `job "<n>"` | Job name (**required**) |
| `--region=<string>` | string | `region = "<region>"` | Nomad region / jurisdiction boundary |
| `--namespace=<string>` | string | `namespace = "<ns>"` | Nomad namespace |
| `--dc=<string>` | string | `datacenters = ["<dc>"]` | Restrict to a specific datacenter (repeatable) |
| `--nodes=<int>` | int | `count = <n>` | Parallel group instances |
| `--cores=<int>` | int | `resources { cores = <n> }` | Reserved CPU cores per task |
| `--mem=<size>` | string | `resources { memory = <mb> }` | Memory per task (K/M/G suffix) |
| `--gpus=<int>` | int | `device "nvidia/gpu" { count = <n> }` | GPU count per task |
| `--time=<HH:MM:SS>` | string | `timeout` wrapper on command | Walltime limit |
| `--chdir=<path>` | string | `config { chdir = "<path>" }` | Working directory inside the task sandbox |
| `--driver=<string>` | string | `driver = "<driver>"` | Task driver: `exec2` (default), `hpc-bridge`, `docker` |
| `--depend=<type:jobid>` | string | prestart lifecycle hook | Block on another job before this task starts |
| `--priority=<int>` | int | `priority = <n>` | Nomad scheduler priority (default: 50) |

**Class 2 — Runtime exposure directives:** Boolean flags whose presence tells the HCL generator to inject the corresponding Nomad runtime variable into the task `env` block. These are readable by the script at execution time.

`NOMAD_REGION` is always injected automatically by Nomad — no directive needed.

*Task identity*

| Directive | Injects | Notes |
|---|---|---|
| `--alloc_id` | `NOMAD_ALLOC_ID` | Full allocation UUID — use as a unique output path component |
| `--short_alloc_id` | `NOMAD_SHORT_ALLOC_ID` | 8-character short ID — use in log prefixes |
| `--alloc_name` | `NOMAD_ALLOC_NAME` | `<job>.<group>[<index>]` — human-readable alloc label |
| `--alloc_index` | `NOMAD_ALLOC_INDEX` | 0-based index within the group — **use to shard parallel array jobs across a samplesheet** |
| `--job_id` | `NOMAD_JOB_ID` | Nomad job ID |
| `--job_name` | `NOMAD_JOB_NAME` | Nomad job name |
| `--parent_job_id` | `NOMAD_JOB_PARENT_ID` | Parent job ID — set only on dispatched parameterized jobs |
| `--group_name` | `NOMAD_GROUP_NAME` | Task group name |
| `--task_name` | `NOMAD_TASK_NAME` | Task name within the group |
| `--namespace` | `NOMAD_NAMESPACE` | Nomad namespace — env exposure only; use `--namespace=<ns>` for scheduler placement |
| `--dc` | `NOMAD_DC` | Datacenter the alloc landed in — use for storage endpoint routing |

*Resources*

| Directive | Injects | Notes |
|---|---|---|
| `--cpu_limit` | `NOMAD_CPU_LIMIT` | Allocated CPU in MHz (1024 = 1 GHz) |
| `--cpu_cores` | `NOMAD_CPU_CORES` | Reserved core count — **use to set `-t` for samtools, BWA, STAR** |
| `--mem_limit` | `NOMAD_MEMORY_LIMIT` | Allocated memory in MB — use for JVM heap sizing, Kraken2 DB loading |
| `--mem_max_limit` | `NOMAD_MEMORY_MAX_LIMIT` | Hard memory ceiling (with oversubscription) |

*Task directories*

| Directive | Injects | Notes |
|---|---|---|
| `--alloc_dir` | `NOMAD_ALLOC_DIR` | Shared directory across all tasks in the group |
| `--task_dir` | `NOMAD_TASK_DIR` | Private local scratch directory for this task |
| `--secrets_dir` | `NOMAD_SECRETS_DIR` | Private in-memory secrets directory (noexec) |

**Class 3 — Meta directives:** Pass arbitrary key-value pairs through Nomad's `meta` block. Accessible inside the script as `NOMAD_META_<KEY>` (key uppercased). Repeatable.

```bash
#ABC --meta sample_id=ZA-INST-2024-001
#ABC --meta batch=48
#ABC --meta pipeline_run=run-a1b2c3
```

**Network directives:** Declare named ports for MPI inter-node communication or sidecar patterns. Uncommon for batch bioinformatics jobs. `--port <label>` emits a `network { port "<label>" {} }` stanza and injects `NOMAD_IP_<label>`, `NOMAD_PORT_<label>`, and `NOMAD_ADDR_<label>`. `HOST_IP`, `HOST_PORT`, `HOST_ADDR`, and `ALLOC_PORT` variants are Docker-specific and not supported.

**Precedence:** `#ABC` overrides `#NOMAD`, which overrides `NOMAD_*` env vars read at CLI invocation time.

---

#### `abc job run <script>`

```
$ abc job run scripts/bwa-align.sh --submit --region za-cpt

  Parsing #ABC directives from scripts/bwa-align.sh...
  Generating Nomad HCL...
  Validating via Jobs().Plan() (za-cpt)...

  PLAN
  + job "bwa-align-batch"
    + group "main" (48 instances)
      + task "main" (exec2, 8 cores, 32 GB)
      Placement: za-cpt-dc1 (48 eligible nodes, 0 constraints failed)

  Submitting via Jobs().Register() (za-cpt)...

  ✓ Job submitted
  Nomad job ID   bwa-align-batch
  Evaluation ID  b3c4d5e6-f789-0abc-def1-234567890abc
```

#### `abc job translate <script>`

Translate a dedicated scheduler script (SLURM/PBS) into an ABC script containing
`#ABC` directives. Unknown directives are preserved with notes to avoid data loss.

```
# from SLURM
abc job translate --executor slurm scripts/bwa-align.slurm.sh > scripts/bwa-align.abc.sh

# preserve unmapped directives for manual audit
abc job translate --strict scripts/bwa-align.slurm.sh
```

Dry-run (no submission — uses `Jobs().Plan()` only):

```
$ abc job run scripts/bwa-align.sh --dry-run --region za-cpt

  Parsing #ABC directives from scripts/bwa-align.sh...
  Generating Nomad HCL...

  --- GENERATED HCL ---
  job "bwa-align-batch" {
    region      = "za-cpt"
    datacenters = ["za-cpt-dc1"]
    type        = "batch"
    priority    = 50

    group "main" {
      count = 48

      task "main" {
        driver = "hpc-bridge"
        config {
          command = "/bin/bash"
          args    = ["${NOMAD_TASK_DIR}/bwa-align.sh"]
        }
        resources {
          cores  = 8
          memory = 32768
        }
        env {
          NOMAD_ALLOC_ID    = "${NOMAD_ALLOC_ID}"
          NOMAD_ALLOC_INDEX = "${NOMAD_ALLOC_INDEX}"
          NOMAD_ALLOC_DIR   = "${NOMAD_ALLOC_DIR}"
          NOMAD_TASK_DIR    = "${NOMAD_TASK_DIR}"
          NOMAD_CPU_CORES   = "${NOMAD_CPU_CORES}"
          NOMAD_DC          = "${NOMAD_DC}"
        }
        meta {
          pipeline_run = "run-a1b2c3"
          reference    = "MN908947.3"
        }
      }
    }
  }
  ---------------------

  Dry-run plan (za-cpt):
  Placement: za-cpt-dc1 (48 eligible nodes, 0 constraints failed)
  Estimated cost: R 12.40 – R 18.60 (8 cores × 48 nodes × est. 2–3h)

  ✓ Dry-run complete. Use --submit to register.
```

Print HCL to stdout without submitting (for piping to `nomad job run -`):

```
$ abc job run scripts/bwa-align.sh --region za-cpt
job "bwa-align-batch" {
  region      = "za-cpt"
  ...
}

$ abc job run scripts/bwa-align.sh | nomad job run -
```

Flags:

| Flag | Description |
|---|---|
| `--submit` | Submit directly via `Jobs().Register()` instead of printing HCL to stdout |
| `--dry-run` | Run `Jobs().Plan()` and print placement feasibility + estimated cost; do not submit |
| `--region` | Override `--region` scheduler directive from preamble |
| `--output-file` | Write generated HCL to a file instead of stdout |
| `--watch` | After `--submit`, stream logs immediately (equivalent to piping to `abc job logs --follow`) |

**Annotated preamble example — BWA-MEM array job:**

```bash
#!/bin/bash
# ── Scheduler directives ─────────────────────────────────────
#ABC --name=bwa-align-batch
#ABC --region=za-cpt
#ABC --dc=za-cpt-dc1
#ABC --nodes=48
#ABC --cores=8
#ABC --mem=32G
#ABC --time=04:00:00
#ABC --driver=hpc-bridge
#ABC --priority=60
# ── Runtime exposure directives ──────────────────────────────
#ABC --alloc_id
#ABC --alloc_index
#ABC --alloc_dir
#ABC --task_dir
#ABC --cpu_cores
#ABC --dc
# ── Meta passthrough ─────────────────────────────────────────
#ABC --meta pipeline_run=run-a1b2c3
#ABC --meta reference=MN908947.3

# NOMAD_ALLOC_INDEX is the 0-based sample index across the 48-node array
# (replaces $PBS_ARRAY_INDEX / $SLURM_ARRAY_TASK_ID)
SAMPLE=$(sed -n "$((NOMAD_ALLOC_INDEX + 1))p" "${NOMAD_TASK_DIR}/samplesheet.csv")
THREADS=$NOMAD_CPU_CORES

bwa mem -t "$THREADS" \
  "${NOMAD_TASK_DIR}/reference/${NOMAD_META_REFERENCE}.fa" \
  "${NOMAD_ALLOC_DIR}/fastq/${SAMPLE}_R1.fastq.gz" \
  "${NOMAD_ALLOC_DIR}/fastq/${SAMPLE}_R2.fastq.gz" \
  > "${NOMAD_TASK_DIR}/output/${SAMPLE}.sam"

# Write output path for ABC cost accounting
echo "output:${NOMAD_TASK_DIR}/output/${SAMPLE}.sam alloc:${NOMAD_ALLOC_ID}"
```

---

#### `abc job list`

```
$ abc job list

  NOMAD JOB ID          STATUS     REGION   DATACENTERS    SUBMITTED            DURATION
  bwa-align-batch       running    za-cpt   za-cpt-dc1     2024-11-01 08:14     1h 24m
  bactmap-ke-batch   complete   ke-nbi   ke-nbi-dc1     2024-10-31 14:00     3h 07m
  analysis              complete   za-cpt   za-cpt-dc1     2024-10-30 16:45     0h 12m
  taxprofiler-003       dead       mz-map   mz-map-dc1     2024-10-30 09:30     0h 41m
```

Flags:

| Flag | Description |
|---|---|
| `--status` | Filter: `running`, `complete`, `dead`, `pending` |
| `--region` | Filter by Nomad region |
| `--limit` | Max results (default: 20) |

---

#### `abc job show <id>`

```
$ abc job show bwa-align-batch

  Nomad Job ID   bwa-align-batch
  Type           batch
  Status         running
  Region         za-cpt
  Datacenter     za-cpt-dc1
  Driver         hpc-bridge
  Submitted      2024-11-01 08:14:32
  Duration       1h 24m 08s
  Priority       60

  TASK GROUPS
  GROUP   DESIRED  RUNNING  SUCCEEDED  FAILED
  main    48       12       36         0

  RECENT ALLOCATIONS
  ALLOC ID          NODE             STATUS     STARTED     DURATION
  a1b2c3d4-e5f6     hpc-a-node-014   running    08:22:11    1h 14m
  a2c3d4e5-f6a7     hpc-a-node-007   running    08:22:14    1h 14m
  b1c2d3e4-f5a6     hpc-a-node-001   complete   08:14:35    0h 58m

  META
  pipeline_run   run-a1b2c3
  reference      MN908947.3

  COST (ZAR)
  Compute   R 44.80
  Storage   R  3.12
  Total     R 47.92  (estimated)
```

---

#### `abc job stop <id>`

```
$ abc job stop taxprofiler-003

  Stop job taxprofiler-003? [y/N]: y
  ✓ Stop signal sent  (Jobs().Deregister)
  ✓ Job deregistered from Nomad
```

Flags:

| Flag | Description |
|---|---|
| `--purge` | Remove job definition from Nomad after stopping |
| `--detach` | Return immediately without waiting for allocations to drain |

---

#### `abc job dispatch <id>`

Dispatches an instance of a parameterized Nomad batch job. Uses `Jobs().Dispatch()` with `idPrefixTemplate` for deterministic IDs when triggered from `abc automation`.

```
$ abc job dispatch viralrecon-parameterized \
    --meta sample=ZA-INST-2024-055 \
    --meta batch=48

  ✓ Dispatched  (Jobs().Dispatch)
  Nomad job ID   viralrecon-parameterized/dispatch-1730450123-a1b2c3
  Evaluation ID  c4d5e6f7-a8b9-0cde-f123-456789012bcd
```

Flags:

| Flag | Description |
|---|---|
| `--meta <key=value>` | Nomad meta key-value pair passed to the dispatched job (repeatable) |
| `--input <file>` | Payload file passed as the dispatch payload |
| `--detach` | Do not wait for the dispatched allocation to start |

---

#### `abc job logs <id>`

Streams task logs via `AllocFS().Logs()`, which returns a `<-chan *api.StreamFrame`. Each frame carries stdout or stderr bytes and a file offset. The `--follow` flag holds the channel open — the CLI selects on it until cancelled.

```
$ abc job logs bwa-align-batch --alloc a1b2c3d4 --task main --follow

  [08:22:11] Task started on hpc-a-node-014
  [08:22:12] Sample ZA-INST-2024-013: loading reference MN908947.3
  [08:22:14] Sample ZA-INST-2024-013: bwa mem -t 8 ...
  [08:45:33] Sample ZA-INST-2024-013: alignment complete
  [08:45:34] output:/alloc/task/output/ZA-INST-2024-013.sam alloc:a1b2c3d4-e5f6
  [08:45:34] Task complete (exit 0)
```

Flags:

| Flag | Description |
|---|---|
| `--follow` / `-f` | Stream in real time via `AllocFS().Logs()` channel |
| `--alloc` | Filter to a specific allocation ID |
| `--task` | Filter to a specific task name |
| `--type` | `stdout` or `stderr` (default: both) |
| `--since` | Show logs from this timestamp onward |

---

#### `abc job status <id>`

Compact one-liner via `Jobs().Info()` + `Evaluations().List()`. Designed for scripting and CI polling.

```
$ abc job status bwa-align-batch

  bwa-align-batch  running  za-cpt  allocs: 12 running / 36 succeeded / 0 failed

$ abc job status bwa-align-batch
0    # exit 0 = job complete with no failures

$ abc job status taxprofiler-003
1    # exit 1 = job dead/failed
```

Exit codes for `abc job status`: `0` = complete/succeeded, `1` = dead/failed, `2` = still running.

---

### 5.8 `abc data`

#### `abc data upload <path>`

```
$ abc data upload ./samplesheets/batch-48.csv \
    --region za-cpt \
    --label data-class=samplesheet \
    --label project=consortium-a

  Uploading batch-48.csv (4.2 KB) → minio://za-cpt/ws-org-a-01/...

  batch-48.csv ████████████████████████████████ 100%  4.2 KB / 4.2 KB

  ✓ Upload complete
  ID        ds-002def
  Region    za-cpt  (POPIA jurisdiction)
  Checksum  sha256:3a7f...c921
```

#### `abc data download <id> [dest]`

```
$ abc data download ds-002def ./local/batch-48.csv

  Downloading ds-002def (4.2 KB) → ./local/batch-48.csv

  batch-48.csv ████████████████████████████████ 100%  4.2 KB / 4.2 KB

  ✓ Download complete
  ✓ Checksum verified  sha256:3a7f...c921
```

#### `abc data download --tool=<tool> --source=<uri> --dest=<path> [--url-file=<list>]`

```
$ abc data download --tool=aria2 --source=https://example.com/large-file.fastq --dest=/tmp/data --parallel=16

  Submitting aria2 download job...
  Job submitted: run-xxxxxxxx

$ abc data download --tool=rclone --source=s3://my-bucket/corpus --dest=/mnt/corpus --parallel=64

  Submitting rclone download job...
  Job submitted: run-yyyyyyyy
```

Flags:

| Flag | Description |
|---|---|
| `--tool` | Download tool to use: `nextflow` (default), `aria2`, `rclone`, `wget`, `s5cmd` |
| `--driver` | `exec` (host) or `docker` (container) |
| `--source` | Source URI (HTTP(s), S3 path, or bucket prefix) |
| `--dest` | Destination path inside task container (for non-nextflow tools) |
| `--url-file` | Local path containing newline-separated source URLs |
| `--parallel` | Parallelism (worker/task-level concurrency) |
| `--tool-args` | Extra tool-specific flags passed through to the selected download program |

##### Docker image mapping (pinned)

- `aria2` => `quay.io/biocontainers/aria2:1.36.0--he1b5a44_0`
- `rclone` => `quay.io/rclone/rclone:1.77.0`
- `wget` => `busybox:1.36.0`
- `s5cmd` => `quay.io/s5cmd/s5cmd:2.1.0`
- `nextflow` => `nextflow/nextflow:25.10.4`

##### Two-stage job flow (tool + upload)

`abc data download` submits a data job that effectively has two steps:
1. tool task — run language specific downloader (`aria2`, `rclone`, `wget`, `s5cmd`, or `nextflow`) to local destination
2. upload task — locate downloaded files in same workspace and upload to TUS endpoint via `abc data upload`

In the current implementation this is a single Nomad task script with ordered commands, but it is architected to evolve into a true two-task Nomad group (download + upload) in the cluster job spec.

##### Resume/retry design for aria2

- When using `aria2`, the command includes a reusable destination like `/tmp/abc-data-download` and can apply resume flags.
- To enable resume across Nomad retries, use a volume/host mount that preserves download artifacts between re-schedules.
- Enable/force `aria2` options: `--continue=true --auto-file-renaming=false --allow-overwrite=true`.
- A Nomad job retry policy (`--reschedule-mode=delay --reschedule-attempts=3 --reschedule-interval=30s`) helps restart after transient failure.

#### `abc data list [prefix]`

```
$ abc data list samplesheets/

  ID          NAME                           SIZE     REGION   CLASS          UPLOADED
  ds-001abc   samplesheets/batch-47.csv      3.9 KB   za-cpt   samplesheet    2024-10-28
  ds-002def   samplesheets/batch-48.csv      4.2 KB   za-cpt   samplesheet    2024-11-01
  ds-003ghi   samplesheets/ke-batch-12.csv   2.1 KB   ke-nbi   samplesheet    2024-10-31
```

#### `abc data show <id>`

```
$ abc data show ds-001abc

  ID              ds-001abc
  Name            samplesheets/batch-47.csv
  Size            3.9 KB
  Checksum        sha256:7b2e...f401
  Region          za-cpt
  Datacenter      za-cpt-dc1
  Jurisdiction    POPIA
  Data class      samplesheet
  Project         consortium-a
  Uploaded by     admin@org-a.example
  Uploaded at     2024-10-28 07:41:03
  Retention       2027-10-28  (1096 days, POLICY-06)
  Residency       COMPLIANT  (POLICY-01)
  Encrypted       yes  (rclone crypt, AES-256)
```

#### `abc data delete <id>`

```
$ abc data delete ds-003ghi

  Delete ds-003ghi (samplesheets/ke-batch-12.csv)?
  Region: ke-nbi  |  Jurisdiction: Kenya DPA  |  Size: 2.1 KB
  [y/N]: y
  ✓ Deleted
  ✓ Deletion event written to audit log
```

#### `abc data move <src-id> <dst-region>`

```
$ abc data move ds-001abc be-bru

  Checking cross-border transfer policy...

  SOURCE      za-cpt  (POPIA)
  DEST        be-bru  (GDPR / Belgium — adequate per POPIA §57)
  DATA CLASS  samplesheet

  POLICY     DECISION  NOTE
  POLICY-01  ALLOW     Samplesheet only; raw sequence not involved
  POLICY-04  ALLOW     Belgium is an adequate country under POPIA
  POLICY-05  ALLOW     DTA not required for adequate destination

  ✓ Transfer approved. Transfer initiated.
```

#### `abc data stat <id>`

```
$ abc data stat ds-001abc

  ID              ds-001abc
  Current region  be-bru  (transfer complete)
  Jurisdiction    GDPR  (Belgium)
  Residency       COMPLIANT

  TRANSFER HISTORY
  TIMESTAMP             FROM     TO       DTA            DECISION
  2024-10-28 07:41:03   —        za-cpt   —              Created
  2024-11-02 09:15:44   za-cpt   be-bru   not required   ALLOW (adequate country)
```

#### `abc data logs <id>`

Full access and event log for a data object — who touched it, when, from where, and what decision was made. Intended for Data Managers and compliance audits.

```
$ abc data logs ds-001abc

  TIMESTAMP             USER                    EVENT           REGION    DECISION  DETAIL
  2024-10-28 07:41:03   admin@org-a.example     upload          za-cpt    —         Created, 3.9 KB
  2024-10-28 09:00:12   admin@org-a.example     read            za-cpt    —         pipeline run-a1b2c3
  2024-10-29 14:22:08   user@org-a.example      read            za-cpt    —         manual download
  2024-11-01 06:44:17   launcher@org-b.example  read            ke-nbi    ALLOW     pipeline run-d4e5f6 (DTA dta-za-ke-001)
  2024-11-02 09:15:44   admin@org-a.example     transfer        be-bru    ALLOW     adequate country
  2024-11-02 09:15:44   system                  residency-check be-bru    PASS      POLICY-02 satisfied
```

Flags:

| Flag | Description |
|---|---|
| `--follow` / `-f` | Stream new events as they occur |
| `--from` | ISO 8601 start time |
| `--to` | ISO 8601 end time |
| `--event` | Filter by event type: `upload`, `download`, `transfer`, `read`, `delete`, `residency-check` |
| `--user` | Filter by user |

#### `abc data encrypt <path>`

```
$ abc data encrypt ./fastq/ZA-INST-2024-001_R1.fastq.gz \
    --crypt-password "••••••••" --crypt-salt "••••••••"

  ✓ Encrypted → ./fastq/ZA-INST-2024-001_R1.fastq.gz.encrypted
  Size before   2.1 GB
  Size after    2.1 GB  (rclone crypt, AES-256 CTR)
```

#### `abc data decrypt <path>`

```
$ abc data decrypt ./fastq/ZA-INST-2024-001_R1.fastq.gz.encrypted \
    --crypt-password "••••••••" --crypt-salt "••••••••" \
    --output ./fastq/ZA-INST-2024-001_R1.fastq.gz

  ✓ Decrypted → ./fastq/ZA-INST-2024-001_R1.fastq.gz
  ✓ Checksum verified
```

---

### 5.9 `abc automation`

Umbrella command group for all scheduled, event-triggered, and DAG-orchestrated workflows active on the user's profile. Corresponds to the Control plugin backend.

Automation types:

| Type | Description |
|---|---|
| `schedule` | Cron-based: run a pipeline or job on a fixed cadence |
| `trigger` | Event-driven: fire when a data upload, run completion, or external webhook occurs |
| `dag` | Multi-pipeline DAG: a named graph of pipelines with dependency edges |

#### `abc automation list`

```
$ abc automation list

  ID          NAME                   TYPE       STATUS    LAST FIRED            NEXT RUN
  auto-001    viralrecon-weekly-za   schedule   active    2024-10-28 06:00      2024-11-04 06:00
  auto-002    bactmap-on-upload-ke   trigger    active    2024-10-31 14:00      on event
  auto-003    consortium-full-dag    dag        active    2024-10-29 08:00      manual
  auto-004    taxprofiler-monthly    schedule   disabled  2024-09-30 06:00      —
```

#### `abc automation show <id>`

```
$ abc automation show auto-001

  ID            auto-001
  Name          viralrecon-weekly-za
  Type          schedule
  Status        active
  Pipeline      nf-core/viralrecon @ 2.6.0
  Region        za-cpt
  Schedule      0 6 * * MON  (every Monday at 06:00 SAST)
  Params file   minio://za-cpt/consortium-config/viralrecon-weekly.yaml
  Work dir      minio://za-cpt/consortium-work/scheduled
  Created by    admin@org-a.example
  Created       2024-01-20
  Last fired    2024-10-28 06:00  → run-m4n5o6  (SUCCEEDED)
  Next run      2024-11-04 06:00
  Runs total    42  (41 succeeded, 1 failed)
```

#### `abc automation create`

Interactive or flag-driven. The `--type` flag determines which additional flags are required.

```
$ abc automation create \
    --name "bactmap-on-upload-mz" \
    --type trigger \
    --event data.upload \
    --filter "data-class=raw-sequence,region=mz-map" \
    --pipeline nf-core/bactmap \
    --region mz-map \
    --params-file minio://mz-map/org-c-config/bactmap.yaml

  ✓ Automation created
  ID    auto-005
```

Flags:

| Flag | Description |
|---|---|
| `--name` | Human-readable name |
| `--type` | `schedule`, `trigger`, or `dag` |
| `--pipeline` | Pipeline to run (for schedule and trigger types) |
| `--job` | Job script to run instead of a pipeline |
| `--region` | Nomad region for executions |
| `--params-file` | Parameters file path (MinIO path or local) |
| `--schedule` | Cron expression (for schedule type) |
| `--event` | Event type to listen for (for trigger type): `data.upload`, `pipeline.succeeded`, `pipeline.failed`, `webhook` |
| `--filter` | Key=value filters on the trigger event (repeatable) |
| `--dag-file` | Path to DAG definition file (for dag type) |
| `--disabled` | Create in disabled state |

#### `abc automation enable <id>`

```
$ abc automation enable auto-004

  ✓ auto-004 (taxprofiler-monthly) enabled
  Next run: 2024-12-01 06:00
```

#### `abc automation disable <id>`

```
$ abc automation disable auto-002

  ✓ auto-002 (bactmap-on-upload-ke) disabled
  In-flight run run-d4e5f6 will complete normally.
```

#### `abc automation delete <id>`

```
$ abc automation delete auto-005

  Delete auto-005 (bactmap-on-upload-mz)? [y/N]: y
  ✓ Automation deleted. Historical run records are preserved.
```

#### `abc automation logs <id>`

Lifecycle and decision log for an automation — when it fired, what triggered it, what it launched, and the outcome. Distinct from the logs of the pipeline run it launched.

```
$ abc automation logs auto-002

  TIMESTAMP             EVENT                    DETAIL                         OUTCOME
  2024-10-31 13:58:44   trigger:data.upload      ds-003ghi uploaded (ke-nbi)    matched filter
  2024-10-31 13:58:45   pipeline:submit          run-d4e5f6  nf-core/bactmap    submitted
  2024-10-31 17:07:31   pipeline:complete        run-d4e5f6                     SUCCEEDED
  2024-10-30 08:11:02   trigger:data.upload      ds-008xyz uploaded (za-cpt)    no match (region filter)
  2024-10-29 11:00:01   trigger:data.upload      ds-007stu uploaded (ke-nbi)    matched filter
  2024-10-29 11:00:02   pipeline:submit          run-j1k2l3  nf-core/bactmap    submitted
  2024-10-29 14:55:18   pipeline:complete        run-j1k2l3                     SUCCEEDED
```

Flags:

| Flag | Description |
|---|---|
| `--follow` / `-f` | Stream new events as they occur |
| `--from` | ISO 8601 start time |
| `--to` | ISO 8601 end time |
| `--event` | Filter by event type |

#### `abc automation runs <id>`

List all pipeline or job runs launched by a specific automation.

```
$ abc automation runs auto-001

  RUN ID      NAME       STATUS     REGION   STARTED              DURATION
  run-m4n5o6  batch-46   SUCCEEDED  za-cpt   2024-10-28 06:00     2h 18m
  run-b5c6d7  batch-45   SUCCEEDED  za-cpt   2024-10-21 06:00     2h 04m
  run-e8f9a0  batch-44   SUCCEEDED  za-cpt   2024-10-14 06:00     2h 31m
  run-q1r2s3  batch-43   FAILED     za-cpt   2024-10-07 06:00     0h 22m
  ...  (42 total)
```

#### `abc automation triggers`

List all available trigger event types and their filterable fields.

```
$ abc automation triggers

  EVENT                     DESCRIPTION                          FILTER FIELDS
  data.upload               A data object was uploaded           region, data-class, workspace, label.*
  data.delete               A data object was deleted            region, data-class
  pipeline.succeeded        A pipeline run completed             pipeline, region, workspace
  pipeline.failed           A pipeline run failed                pipeline, region, workspace
  job.complete              A Nomad batch job completed          region, datacenter
  job.failed                A Nomad batch job failed             region, datacenter
  compliance.dta.expiring   A DTA is within 30 days of expiry   dta-id, from-jurisdiction, to-jurisdiction
  webhook                   An external HTTP POST to the hook    header.*, body.*
```

---

### 5.10 `abc storage`

`abc storage size` relies on ABC control plane inventory services:
- `abc-node-probe` to collect compute node/local disk capacity and usage.
- central control plane / node metadata table to maintain per-node remaining space.
- `rclone size` (or equivalent bucket enumeration) for connected buckets to compute `used/total` and sync to control plane inventory.
- internal merged table used by `abc storage size` on CLI query.

#### `abc storage buckets list`

```
$ abc storage buckets list --region za-cpt

  BUCKET                  REGION   OBJECTS    SIZE      VERSIONING  LOCK   CREATED
  consortium-work         za-cpt   184,221    14.2 TB   off         off    2024-01-15
  consortium-results      za-cpt   92,048      8.7 TB   on          off    2024-01-15
  consortium-raw-seq      za-cpt   12,400     41.8 TB   on          on     2024-01-15
  ws-org-a-01             za-cpt   4,102       0.9 TB   off         off    2024-01-15
  abc-audit-logs          za-cpt   2,901,440   0.2 TB   on          on     2024-01-15
```

#### `abc storage buckets create <n>`

```
$ abc storage buckets create consortium-archive-2024 \
    --region za-cpt \
    --versioning \
    --lock \
    --retention-days 1095

  ✓ Bucket created
  Name         consortium-archive-2024
  Region       za-cpt
  Versioning   enabled
  Object lock  enabled  (retention: 1095 days WORM)
```

#### `abc storage buckets stat <n>`

```
$ abc storage buckets stat consortium-raw-seq

  Name            consortium-raw-seq
  Region          za-cpt
  Datacenter      za-cpt-dc1
  Objects         12,400
  Size            41.8 TB
  Versioning      enabled
  Object lock     enabled  (WORM — 1095 days)
  Jurisdiction    POPIA
  Created         2024-01-15
```

#### `abc storage objects list <bucket> [prefix]`

```
$ abc storage objects list consortium-raw-seq ZA-INST-2024/

  KEY                                        SIZE     MODIFIED              CLASS
  ZA-INST-2024/ZA-INST-2024-001_R1.fastq.gz   2.1 GB   2024-10-28 07:41     STANDARD
  ZA-INST-2024/ZA-INST-2024-001_R2.fastq.gz   2.0 GB   2024-10-28 07:41     STANDARD
  ZA-INST-2024/ZA-INST-2024-002_R1.fastq.gz   1.9 GB   2024-10-28 08:02     STANDARD
  ZA-INST-2024/ZA-INST-2024-002_R2.fastq.gz   1.8 GB   2024-10-28 08:02     STANDARD
```

#### `abc storage objects stat <bucket> <key>`

```
$ abc storage objects stat consortium-raw-seq \
    ZA-INST-2024/ZA-INST-2024-001_R1.fastq.gz

  Key           ZA-INST-2024/ZA-INST-2024-001_R1.fastq.gz
  Bucket        consortium-raw-seq
  Size          2.1 GB
  ETag          "a3f7b2c1d4e5f6a7b8c9d0e1f2a3b4c5"
  Content type  application/gzip
  Version ID    3a7f2c1d-4e5f-6a7b-8c9d-0e1f2a3b4c5d
  Modified      2024-10-28 07:41:03
  Storage class STANDARD
  Legal hold    ON  (POLICY-01 raw sequence lock)
  Region        za-cpt
```

#### `abc storage size [--servers|--buckets|--all]`

```
$ abc storage size --servers

  SERVER STORAGE:
  hpc-a-node-001: 12.1 TB used / 16.0 TB total (3.9 TB free)
  hpc-a-node-007:  9.7 TB used / 16.0 TB total (6.3 TB free)

$ abc storage size --buckets

  BUCKET STORAGE:
  consortium-raw-seq: 41.8 TB used / 100.0 TB total
  consortium-work: 14.2 TB used / 80.0 TB total

$ abc storage size --all

  (both server and bucket sections shown)
```

Flags:

| Flag | Description |
|---|---|
| `--servers` | Show host/node storage usage and capacity |
| `--buckets` | Show bucket storage usage and capacity |
| `--all` | Show both categories (default if none specified) |
| `--nomad-addr` | Nomad API endpoint |
| `--nomad-token` | Nomad ACL token |
| `--region` | Nomad region |
| `--namespace` | Nomad namespace |

---

### 5.11 `abc compute`

#### `abc compute nodes list`

```
$ abc compute nodes list --region za-cpt

  NODE ID          DATACENTER    STATUS    DRIVER        CPU        MEM        ALLOCS
  hpc-a-node-001   za-cpt-dc1    ready     hpc-bridge    64 cores   256 GB     4 / 4
  hpc-a-node-007   za-cpt-dc1    ready     hpc-bridge    64 cores   256 GB     3 / 4
  hpc-a-node-014   za-cpt-dc1    ready     hpc-bridge    64 cores   256 GB     4 / 4
  hpc-b-node-001   za-cpt-dc2    ready     exec2         32 cores   128 GB     1 / 8
  hpc-b-node-003   za-cpt-dc2    draining  exec2         32 cores   128 GB     1 / 8
```

#### `abc compute nodes show <node-id>`

```
$ abc compute nodes show hpc-a-node-014

  Node ID       hpc-a-node-014
  Datacenter    za-cpt-dc1
  Region        za-cpt
  Status        ready
  Driver        hpc-bridge (PBS Pro backend)
  OS            CentOS 7.9
  CPU           64 cores  (Intel Xeon Gold 6148)
  Memory        256 GB
  Disk          2 TB
  Active allocs 4 / 4

  ACTIVE ALLOCATIONS
  ALLOC ID          JOB               TASK GROUP   CPU     MEM       STARTED
  a1b2c3d4-e5f6     bwa-align-batch   main         8       32 GB     08:22:11
  b2c3d4e5-f6a7     bwa-align-batch   main         8       32 GB     08:22:14
  c3d4e5f6-a7b8     bwa-align-batch   main         8       32 GB     08:22:09
  d4e5f6a7-b8c9     rnaseq-nov        main         40      160 GB    10:05:33
```

#### `abc compute nodes drain <node-id>`

```
$ abc compute nodes drain hpc-b-node-003 --deadline 1h

  ✓ Node hpc-b-node-003 set to draining
  ✓ 1 existing allocation will migrate within 1h
```

#### `abc compute nodes undrain <node-id>`

```
$ abc compute nodes undrain hpc-b-node-003

  ✓ Node hpc-b-node-003 drain cancelled
  ✓ Node accepting new allocations
```

#### `abc compute datacenters list`

```
$ abc compute datacenters list

  DATACENTER    REGION   NODES  ALLOCS  SCHEDULER  MINIO ENDPOINT
  za-cpt-dc1    za-cpt   312    1,204   PBS Pro    https://minio.za-cpt-dc1.example
  za-cpt-dc2    za-cpt   73     441     SLURM      https://minio.za-cpt-dc2.example
  ke-nbi-dc1    ke-nbi   48     227     SLURM      https://minio.ke-nbi-dc1.example
  mz-map-dc1    mz-map   12     34      SLURM      https://minio.mz-map-dc1.example
  be-bru-dc1    be-bru   24     89      SLURM      https://minio.be-bru-dc1.example
```

#### `abc compute datacenters show <dc>`

```
$ abc compute datacenters show za-cpt-dc1

  Datacenter     za-cpt-dc1
  Region         za-cpt
  Jurisdiction   POPIA
  Scheduler      PBS Pro
  Nodes          312  (308 ready, 3 draining, 1 down)
  Active allocs  1,204
  MinIO          https://minio.za-cpt-dc1.example  (57.9 TB used / 200 TB)
  Tailscale      za-cpt-dc1.example-cluster.ts.net
```

#### `abc compute allocations list`

```
$ abc compute allocations list --job bwa-align-batch

  ALLOC ID          NODE             TASK GROUP   STATUS     STARTED     DURATION
  a1b2c3d4-e5f6     hpc-a-node-014   main         running    08:22:11    1h 14m
  a2c3d4e5-f6a7     hpc-a-node-007   main         running    08:22:14    1h 14m
  b1c2d3e4-f5a6     hpc-a-node-001   main         complete   08:14:35    0h 58m
```

#### `abc compute allocations show <alloc-id>`

```
$ abc compute allocations show a1b2c3d4-e5f6

  Alloc ID       a1b2c3d4-e5f6
  Job            bwa-align-batch
  Task group     main
  Node           hpc-a-node-014  (za-cpt-dc1)
  Status         running
  Started        2024-11-01 08:22:11
  CPU            8 cores  (used: 6.2)
  Memory         32 GB    (used: 18.4 GB)

  LIFECYCLE EVENTS
  TIMESTAMP    EVENT        DETAIL
  08:22:09     prestart     Staging input from minio://za-cpt/consortium-work/...
  08:22:11     started      Task running
  08:22:11     poststart    —
```

#### `abc compute hpc list`

```
$ abc compute hpc list

  BACKEND         REGION   SCHEDULER  STATUS    NODES  QUEUE  DRIVER VERSION
  hpc-a-backend   za-cpt   PBS Pro    healthy   312    47     hpc-bridge v0.1.0
  hpc-b-backend   za-cpt   SLURM      healthy   73     12     hpc-bridge v0.1.0
  hpc-c-backend   ke-nbi   SLURM      healthy   48     8      hpc-bridge v0.1.0
  hpc-d-backend   mz-map   SLURM      degraded  12     0      hpc-bridge v0.1.0
```

#### `abc compute hpc status <backend>`

```
$ abc compute hpc status hpc-a-backend

  Backend        hpc-a-backend
  Region         za-cpt
  Scheduler      PBS Pro
  Status         healthy
  Nodes          312  (308 ready, 3 draining, 1 down)
  Queue depth    47 jobs pending
  Running jobs   1,204
  Driver         hpc-bridge v0.1.0
  Last heartbeat 2024-11-01 09:36:04  (4s ago)
```

#### `abc compute hpc jobs <backend>`

```
$ abc compute hpc jobs hpc-a-backend

  PBS JOB ID    NOMAD JOB           TASK GROUP   STATUS   NODES  WALLTIME   STARTED
  7841234       bwa-align-batch     main         R        1      04:00:00   08:22:11
  7841235       bwa-align-batch     main         R        1      04:00:00   08:22:14
  7841236       bwa-align-batch     main         R        1      04:00:00   08:22:09
  7841289       rnaseq-nov          main         R        1      08:00:00   10:05:33
  7841301       analysis            main         Q        4      02:00:00   —
```

---

### 5.12 `abc policy`

#### `abc policy list`

```
$ abc policy list

  ID        NAME                                   MODE      JURISDICTIONS    LAST EVAL
  pol-001   Raw sequence absolute residence        enforce   popia            09:36:01
  pol-002   Controlled cross-border transfer       enforce   popia,kenya-dpa  09:36:01
  pol-003   DTA structural requirements            enforce   popia,kenya-dpa  09:36:01
  pol-004   Derived data controlled transfer       enforce   popia            09:36:01
  pol-005   Aggregated data transfer               audit     all              09:36:01
  pol-006   Retention limits                       enforce   popia            09:36:01
  pol-007   Audit completeness                     enforce   all              09:36:01
  pol-008   Access control and consent             enforce   all              09:36:01
  pol-009   Breach notification readiness          enforce   popia,gdpr       09:36:01
  pol-010   Processor agreement requirements       enforce   popia,gdpr       09:36:01
```

#### `abc policy show <policy-id>`

```
$ abc policy show pol-001

  ID             pol-001
  Name           Raw sequence absolute residence
  Mode           enforce
  Jurisdiction   POPIA
  Description    Raw genomic sequence data must reside exclusively within South
                 Africa. No transfer, copy, or replication is permitted regardless
                 of destination or consent (POPIA §57, absolute restriction).

  REGO SOURCE
  package abc.policies.pol_001

  default allow = false

  allow {
    input.data_class != "raw-sequence"
  }
  deny_reason = "Raw sequence data may not leave za-* regions (POLICY-01)" {
    input.data_class == "raw-sequence"
    not startswith(input.destination_region, "za-")
  }

  Last evaluated   2024-11-01 09:36:01
  Evaluations      1,204,441  (1,204,440 allow / 1 deny)
```

#### `abc policy validate <job-spec-file>`

```
$ abc policy validate params/batch-47.yaml --pipeline nf-core/viralrecon

  Evaluating against 10 active policies...

  POLICY    NAME                              DECISION  NOTE
  pol-001   Raw sequence absolute residence   ALLOW     Input data in za-cpt ✓
  pol-002   Controlled cross-border transfer  ALLOW     No cross-border transfer
  pol-003   DTA structural requirements       ALLOW     Not applicable
  pol-004   Derived data controlled transfer  ALLOW     Output stays in za-cpt
  pol-005   Aggregated data transfer          ALLOW     —
  pol-006   Retention limits                  ALLOW     Work dir in za-cpt ✓
  pol-007   Audit completeness                ALLOW     Audit hooks injected ✓
  pol-008   Access control and consent        ALLOW     Workspace ws-org-a-01 ✓
  pol-009   Breach notification readiness     ALLOW     —
  pol-010   Processor agreement requirements  ALLOW     —

  ✓ All policies passed. Job may proceed.
```

Exit code: `0` (all passed), `4` (one or more enforce-mode policies denied)

#### `abc policy logs`

Raw OPA evaluation log — per-request traces of which rules were evaluated, which expressions matched, and how long each evaluation took. Intended for **policy developers** debugging policy behaviour and for performance profiling. Distinct from `abc policy audit`, which is compliance-oriented.

```
$ abc policy logs --policy pol-001 --limit 5

  TIMESTAMP             REQUEST ID    POLICY    DURATION   DECISION  INPUT SUMMARY
  2024-11-01 09:36:01   req-8f3a2b    pol-001   1.2ms      allow     data_class=samplesheet, dst=be-bru
  2024-11-01 09:35:58   req-7e2c1a    pol-001   0.9ms      allow     data_class=samplesheet, dst=za-cpt
  2024-11-01 07:12:03   req-6d1b09    pol-001   1.4ms      deny      data_class=raw-sequence, dst=ke-nbi
  2024-11-01 07:11:44   req-5c0a18    pol-001   1.1ms      allow     data_class=derived-sequence, dst=ke-nbi
  2024-11-01 06:44:17   req-4b9927    pol-001   0.8ms      allow     data_class=samplesheet, dst=ke-nbi
```

Expanded view of a single request — shows the full OPA trace:

```
$ abc policy logs --request req-6d1b09 --trace

  Request ID    req-6d1b09
  Timestamp     2024-11-01 07:12:03
  Policy        pol-001  (Raw sequence absolute residence)
  Duration      1.4ms
  Decision      deny
  Deny reason   Raw sequence data may not leave za-* regions (POLICY-01)

  INPUT
  {
    "data_class":          "raw-sequence",
    "source_region":       "za-cpt",
    "destination_region":  "ke-nbi",
    "workspace":           "ws-org-a-01",
    "user":                "admin@org-a.example"
  }

  TRACE
  Enter  data.abc.policies.pol_001.allow
    Eval   input.data_class != "raw-sequence"  →  false  (short-circuit: deny)
  Exit   data.abc.policies.pol_001.allow = false

  Enter  data.abc.policies.pol_001.deny_reason
    Eval   input.data_class == "raw-sequence"                     →  true
    Eval   not startswith(input.destination_region, "za-")        →  true
  Exit   deny_reason = "Raw sequence data may not leave za-* regions (POLICY-01)"
```

Flags:

| Flag | Description |
|---|---|
| `--policy` | Filter to a specific policy ID |
| `--request` | Show a single request by ID |
| `--trace` | Include full OPA evaluation trace (use with `--request`) |
| `--decision` | Filter by decision: `allow`, `deny` |
| `--from` / `--to` | Date range |
| `--limit` | Max results (default: 50) |
| `--follow` / `-f` | Stream new evaluation events as they occur |

#### `abc policy audit`

Compliance-oriented decision log. Records **who** triggered a policy evaluation, **what resource** was involved, and **what decision** was reached. Intended for compliance lawyers, ethics committees, and grant audit requirements. Distinct from `abc policy logs`, which is an OPA-level technical trace.

```
$ abc policy audit --from 2024-11-01

  TIMESTAMP             POLICY    DECISION  USER                    RESOURCE      NOTE
  2024-11-01 09:36:01   pol-001   ALLOW     admin@org-a.example     ds-001abc     samplesheet → be-bru
  2024-11-01 07:12:03   pol-001   DENY      admin@org-a.example     run-z9y8x7    raw-sequence → ke-nbi blocked
  2024-11-01 06:44:17   pol-002   DENY      system                  bactmap-ke    No DTA for ke-nbi → mz-map
```

Flags:

| Flag | Description |
|---|---|
| `--from` / `--to` | Date range |
| `--policy` | Filter to a specific policy |
| `--action` | Filter by decision: `allow`, `deny` |
| `--user` | Filter by user |
| `--resource` | Filter by resource ID (run, data object, job) |

#### `abc policy residency <data-id>`

```
$ abc policy residency ds-001abc

  Data ID        ds-001abc
  Name           samplesheets/batch-47.csv
  Current loc    be-bru
  Data class     samplesheet

  POLICY    NAME                              RESULT    NOTE
  pol-001   Raw sequence absolute residence   N/A       Not raw sequence
  pol-002   Controlled cross-border transfer  PASS      Transfer to Belgium (adequate) logged
  pol-004   Derived data controlled transfer  N/A       Not derived data

  Overall: COMPLIANT
```

---

### 5.13 `abc budget`

#### `abc budget summary`

```
$ abc budget summary --from 30d

  WORKSPACE    ws-org-a-01  (Org-A Genomics)
  PERIOD       2024-10-02 → 2024-11-01

  TOTAL SPEND (ZAR)
  Compute      R 4,812.40
  Storage      R   441.20
  Egress       R    38.10
  Total        R 5,291.70

  TOP PIPELINES
  PIPELINE               RUNS  COMPUTE    STORAGE   TOTAL
  nf-core/viralrecon     24    R 2,140    R 210     R 2,350
  nf-core/bactmap        8     R 1,200    R  88     R 1,288
  nf-core/rnaseq         4     R   980    R 112     R 1,092
  nf-core/taxprofiler    3     R   492    R  31     R   523

  TOP DATACENTERS
  DATACENTER    COMPUTE    STORAGE   TOTAL
  za-cpt-dc1    R 3,800    R 310     R 4,110
  za-cpt-dc2    R 1,012    R 131     R 1,143
```

#### `abc budget list`

```
$ abc budget list --run-id run-a1b2c3

  ENTRY ID      RUN          SAMPLE            TYPE      AMOUNT (ZAR)   TIMESTAMP
  bgt-0001      run-a1b2c3   ZA-INST-2024-001  compute   R 0.92         2024-11-01 08:22
  bgt-0002      run-a1b2c3   ZA-INST-2024-001  storage   R 0.06         2024-11-01 08:22
  bgt-0003      run-a1b2c3   ZA-INST-2024-002  compute   R 0.89         2024-11-01 08:23
  bgt-0004      run-a1b2c3   ZA-INST-2024-002  storage   R 0.06         2024-11-01 08:23
  ...
  TOTAL                                                   R 47.92
```

#### `abc budget show <run-id>`

```
$ abc budget show run-a1b2c3

  Run ID       run-a1b2c3  (batch-47)
  Pipeline     nf-core/viralrecon @ 2.6.0
  Region       za-cpt  (za-cpt-dc1)
  Samples      48
  Currency     ZAR

  COST BY TASK
  TASK            CPU-H    MEM-GBH   COST (ZAR)
  FASTQC          24.0     48.0      R  8.40
  TRIM_GALORE     (running)          R 22.10 est.
  Storage                            R  3.12
  Egress                             R  0.84

  TOTAL                              R 34.46  (R 47.92 est. at completion)

  TigerBeetle account      tb-acc-ws-org-a-01
  Ledger entries           bgt-0001 → bgt-0096  (96 entries)
```

#### `abc budget report`

```
$ abc budget report \
    --from 2024-10-01 --to 2024-10-31 \
    --group-by pipeline \
    --output-file reports/oct-2024-budget.md

  ✓ Report written to reports/oct-2024-budget.md
  Period      October 2024
  Total       R 4,918.40
  Pipelines   6
  Samples     312
  Runs        39
```

#### `abc budget logs`

Raw TigerBeetle double-entry ledger events for the current workspace. Maps every compute event to a specific ledger entry for grant financial reporting and accounting audit trails.

```
$ abc budget logs --run-id run-a1b2c3

  ENTRY ID    TIMESTAMP             RUN          SAMPLE            TYPE      AMOUNT (ZAR)  ACCOUNT
  bgt-0001    2024-11-01 08:22:11   run-a1b2c3   ZA-INST-2024-001  compute   R 0.18        tb-acc-ws-org-a-01
  bgt-0002    2024-11-01 08:22:11   run-a1b2c3   ZA-INST-2024-001  compute   R 0.18        tb-acc-ws-org-a-01
  bgt-0003    2024-11-01 08:22:11   run-a1b2c3   ZA-INST-2024-001  storage   R 0.06        tb-acc-ws-org-a-01
  bgt-0004    2024-11-01 08:22:14   run-a1b2c3   ZA-INST-2024-002  compute   R 0.17        tb-acc-ws-org-a-01
  ...  (96 entries)
```

Flags:

| Flag | Description |
|---|---|
| `--run-id` | Filter to a specific pipeline or job run |
| `--sample` | Filter to a specific sample ID |
| `--from` / `--to` | Date range |
| `--type` | Filter by entry type: `compute`, `storage`, `egress` |
| `--limit` | Max results (default: 100) |

---

### 5.14 `abc compliance`

#### `abc compliance status`

```
$ abc compliance status

  JURISDICTION   POLICIES    PASS   FAIL   WARN
  POPIA          7           7      0      0    ✓
  Kenya DPA      3           2      0      1    ⚠
  GDPR           3           3      0      0    ✓
  Mozambique     2           1      0      1    ⚠

  WARNINGS
  Kenya DPA     dta-za-ke-001 expires in 14 days — renewal required
  Mozambique    No DTA registered for ke-nbi → mz-map transfer
```

#### `abc compliance audit`

```
$ abc compliance audit --jurisdiction kenya-dpa --from 2024-10-01

  TIMESTAMP             EVENT TYPE    DATA ID     FROM      TO        USER                    DECISION
  2024-11-01 06:44:17   transfer      ds-004jkl   za-cpt    ke-nbi    launcher@org-b.example  ALLOW  (dta-za-ke-001)
  2024-10-31 14:00:01   access        ds-005mno   —         ke-nbi    student@org-b.example   ALLOW
  2024-10-30 11:22:08   transfer      ds-006pqr   ke-nbi    mz-map    —                       DENY   (no DTA)
  2024-10-29 09:14:55   deletion      ds-007stu   —         ke-nbi    launcher@org-b.example  ALLOW  (retention satisfied)
```

#### `abc compliance residency <data-id>`

```
$ abc compliance residency ds-004jkl

  Data ID     ds-004jkl
  Name        results/ke-batch-12/consensus.fasta
  Class       derived-sequence

  RESIDENCY AUDIT TRAIL
  TIMESTAMP             LOCATION   JURISDICTION  DTA             EVENT
  2024-10-31 14:00:01   ke-nbi     Kenya DPA     —               Created
  2024-11-01 06:44:17   za-cpt     POPIA         dta-za-ke-001   Transfer: ke-nbi → za-cpt

  POLICY COMPLIANCE
  pol-002   Controlled cross-border transfer   PASS  (DTA on record)
  pol-007   Audit completeness                 PASS  (full trail present)
```

#### `abc compliance dta list`

```
$ abc compliance dta list

  DTA ID         FROM     TO       STATUS    EXPIRES       DATA CATEGORIES
  dta-za-ke-001  za-cpt   ke-nbi   active    2024-11-15    derived-sequence, samplesheet
  dta-za-be-001  za-cpt   be-bru   active    2025-06-01    aggregated, samplesheet
  dta-ke-mz-001  ke-nbi   mz-map   pending   —             under review
  dta-za-ke-002  za-cpt   ke-nbi   expired   2024-09-30    all categories
```

#### `abc compliance dta show <dta-id>`

```
$ abc compliance dta show dta-za-ke-001

  DTA ID          dta-za-ke-001
  Parties         Org-A  ←→  Org-B
  From            za-cpt  (POPIA)
  To              ke-nbi  (Kenya Data Protection Act)
  Status          active
  Valid from      2024-01-15
  Expires         2024-11-15  ⚠ expires in 14 days
  Reference       Consortium-A Agreement §4.2

  COVERED DATA CATEGORIES
  CATEGORY              TRANSFER TYPE      CONDITIONS
  derived-sequence      bi-directional     Pseudonymised, analysis purpose only
  samplesheet           za → ke only       Project metadata, no clinical data
  aggregated            bi-directional     No restriction

  EXCLUDED CATEGORIES
  raw-sequence          Absolute prohibition (POLICY-01)
```

#### `abc compliance dta validate <data-id> <destination-region>`

```
$ abc compliance dta validate ds-004jkl ke-nbi

  Checking: ds-004jkl (derived-sequence) → ke-nbi  (Kenya DPA)

  CHECK                     RESULT    NOTE
  Data class permitted      PASS      derived-sequence covered by dta-za-ke-001
  DTA validity              PASS      active (expires 2024-11-15)
  Transfer direction        PASS      bi-directional permitted
  POLICY-02 enforcement     PASS      DTA on record
  POLICY-07 audit ready     PASS      Audit hook will be injected

  ✓ Transfer permitted under dta-za-ke-001
  ⚠ DTA expires in 14 days — initiate renewal before next transfer window
```

Exit code: `0` (permitted), `1` (blocked), `4` (OPA policy denial)

#### `abc compliance report`

```
$ abc compliance report \
    --jurisdiction popia \
    --from 2024-10-01 --to 2024-10-31 \
    --output-file reports/popia-oct-2024.md

  ✓ Report written to reports/popia-oct-2024.md
  Jurisdiction    POPIA
  Period          October 2024
  Policy checks   48,812  (48,812 pass / 0 fail)
  Transfers       144  (144 permitted / 0 blocked)
  Residency       All raw sequence remained in za-*  ✓
  Retentions      0 violations
```

---

### 5.15 `abc admin`

#### `abc admin users list`

```
$ abc admin users list

  USER ID   EMAIL                       ROLE    PLAN   LAST SEEN
  usr-001   admin@org-a.example         admin   pro    09:35:58
  usr-002   maintainer@org-a.example    admin   pro    2024-10-31
  usr-003   user@org-a.example          user    pro    2024-11-01
  usr-004   launcher@org-b.example      user    basic  2024-10-31
  usr-005   student@org-b.example       user    basic  2024-10-30
  usr-006   compliance@org-a.example    user    basic  2024-10-28
```

#### `abc admin users create`

```
$ abc admin users create \
    --email user@org-c.example \
    --role user

  ✓ User created
  ID      usr-007
  Email   user@org-c.example
  Token   abc-token-xxxx...  (shown once — save it now)
```

#### `abc admin users token <id>`

```
$ abc admin users token usr-005 --expires 90d --description "CI pipeline token"

  ✓ Token generated for usr-005
  Token       abc-token-yyyy...  (shown once — save it now)
  Expires     2025-01-30
  Description CI pipeline token
```

#### `abc admin health`

```
$ abc admin health

  COMPONENT     STATUS    REGION    DETAIL
  ABC API       healthy   —         v0.2.0, 14ms p99
  Nomad         healthy   za-cpt    v1.9.4, 312 nodes
  Nomad         healthy   ke-nbi    v1.9.4, 48 nodes
  Nomad         healthy   mz-map    v1.9.4, 12 nodes
  Nomad         healthy   be-bru    v1.9.4, 24 nodes
  MinIO         healthy   za-cpt    57.9 TB / 200 TB
  MinIO         degraded  mz-map    Disk 94% — action required
  OPA           healthy   —         v0.68.0, 10 policies
  TigerBeetle   healthy   —         3/3 replicas, 1.2M entries
  Tailscale     healthy   —         5 nodes connected
  hpc-bridge    healthy   za-cpt    PBS Pro reachable
  hpc-bridge    degraded  mz-map    Last heartbeat 4m ago

  Overall: DEGRADED  (2 warnings)
```

#### `abc admin audit`

```
$ abc admin audit --from 2024-11-01

  TIMESTAMP             USER                    ACTION           RESOURCE      OUTCOME
  2024-11-01 09:35:58   admin@org-a.example     auth.login       —             success
  2024-11-01 08:14:32   admin@org-a.example     pipeline.run     run-a1b2c3    success
  2024-11-01 07:12:03   —                       policy.deny      run-z9y8x7    pol-001 deny
  2024-11-01 06:44:17   launcher@org-b.example  data.move        ds-004jkl     success
```

#### `abc admin backup`

```
$ abc admin backup --destination minio://za-cpt/abc-backups/2024-11-01

  ✓ Nomad snapshot       → minio://za-cpt/abc-backups/2024-11-01/nomad.snap
  ✓ TigerBeetle export   → minio://za-cpt/abc-backups/2024-11-01/tigerbeetle.tbexport
  ✓ OPA policy bundle    → minio://za-cpt/abc-backups/2024-11-01/opa-bundle.tar.gz
  ✓ Config manifests     → minio://za-cpt/abc-backups/2024-11-01/config.tar.gz

  Backup complete. Total size: 1.4 GB
```

#### `abc admin version`

```
$ abc admin version

  COMPONENT        VERSION   BUILD
  ABC API          v0.2.0    commit 4f2a1c3
  nf-nomad plugin  v0.5.1    commit 8b3c2d1
  hpc-bridge       v0.1.0    commit 1a2b3c4
  Nomad            v1.9.4    —
  OPA              v0.68.0   —
  TigerBeetle      v0.16.3   —
  MinIO            RELEASE.2024-10-02  —
  Tailscale        v1.74.1   —
```

---

### 5.16 `abc join`

Onboards the current machine into the operator's ABC-cluster. Runs node probe checks automatically, presents a full health report, and — if confirmed — registers the node with Nomad, establishes a Tailscale connection, and configures the appropriate task driver.

Requires `admin` or `maintainer` role in the target workspace.

```
$ abc join \
    --datacenter za-cpt-dc1 \
    --region za-cpt \
    --jurisdiction ZA

  ════════════════════════════════════════════════════════════════════
  ABC Node Join — Pre-flight Check
  Host: hpc-b-node-004.org-a.example  |  Region: za-cpt  |  DC: za-cpt-dc1
  ════════════════════════════════════════════════════════════════════

  Running node probe (8 checks)...

  CHECK              STATUS   DETAIL
  CPU                PASS     32 cores  (Intel Xeon Silver 4216)
  Memory             PASS     128 GB RAM available
  Disk               PASS     /scratch  1.8 TB free  (of 2.0 TB)
  Network            PASS     API reachable (https://api.org-a.example, 12ms)
                     PASS     Tailscale reachable (example-cluster.ts.net)
                     PASS     MinIO reachable (https://minio.za-cpt-dc1.example, 8ms)
  NTP sync           PASS     Offset 0.003s  (limit: 0.250s)
  OS / kernel        PASS     Ubuntu 22.04 LTS  |  kernel 5.15.0-88
  Drivers available  PASS     exec2 available
                     PASS     SLURM detected → hpc-bridge eligible
  SMART disk check   SKIP     /dev/sda — not accessible (unprivileged). Use --privileged to enable.
  Jurisdiction       PASS     ZA declared explicitly (POPIA boundary confirmed)

  ════════════════════════════════════════════════════════════════════
  Result: 9 passed  ·  1 skipped  ·  0 failed
  ════════════════════════════════════════════════════════════════════

  Node is eligible to join the cluster.

  This will:
    • Generate a Tailscale ephemeral key and connect this node to example-cluster.ts.net
    • Register with Nomad (za-cpt / za-cpt-dc1) using exec2 + hpc-bridge drivers
    • Write Nomad client config to /etc/nomad.d/client.hcl
    • Start and enable the nomad systemd service

  Continue? [y/N]: y

  ════════════════════════════════════════════════════════════════════
  Joining cluster...
  ════════════════════════════════════════════════════════════════════

  ✓ Tailscale ephemeral key issued
  ✓ Tailscale connected  (100.104.12.88 / example-cluster.ts.net)
  ✓ Nomad client config written to /etc/nomad.d/client.hcl
  ✓ nomad.service started and enabled
  ✓ Node registered with Nomad

  Node ID       hpc-b-node-004
  Datacenter    za-cpt-dc1
  Region        za-cpt
  Jurisdiction  ZA  (POPIA)
  Drivers       exec2, hpc-bridge
  Status        ready

  Verify with:
    abc compute nodes show hpc-b-node-004
```

The `--probe-only` flag runs checks without committing to join. NTP failure example:

```
$ abc join --probe-only --jurisdiction ZA

  CHECK              STATUS   DETAIL
  ...
  NTP sync           FAIL     Offset 1.847s — exceeds 0.250s limit (TigerBeetle requirement)
  ...

  Result: 7 passed  ·  1 skipped  ·  1 FAILED

  ✗ Node is NOT eligible to join.

  REQUIRED ACTIONS
  NTP sync   Offset 1.847s is too large.
             Run: sudo chronyc makestep && chronyc tracking
             Re-run after NTP has stabilised (allow 60s).
```

Flags:

| Flag | Description |
|---|---|
| `--datacenter` | Target Nomad datacenter (**required** unless `--probe-only`) |
| `--region` | Target Nomad region (**required** unless `--probe-only`) |
| `--jurisdiction` | Jurisdiction code: `ZA`, `KE`, `MZ`, `BE` (**required** — never inferred from network) |
| `--driver` | Override task driver: `exec2`, `hpc-bridge`, `docker` (auto-detected if omitted) |
| `--scheduler` | HPC scheduler for hpc-bridge: `pbs`, `slurm` (auto-detected if omitted) |
| `--privileged` | Allow SMART disk checks (requires root or disk group membership) |
| `--probe-only` | Run checks and print results without joining |
| `--skip-tailscale` | Skip Tailscale setup (node must already be on the tailnet) |
| `--dry-run` | Show what would be written and started, without making any changes |

Exit codes: `0` (joined or probe passed), `1` (probe failed), `2` (usage error), `3` (auth error)

---

### 5.17 `abc chat`

AI assistant embedded in the CLI, available to users on the **Pro plan and above**. Scoped strictly to the authenticated user's own profile — the assistant has read access to the user's runs, data, compliance posture, automations, and budget only. It does not take actions and cannot access other users' data.

```
$ abc chat

  ABC Assistant  ·  admin@org-a.example  ·  ws-org-a-01
  Pro plan  ·  Scoped to your profile only
  Type 'exit' or Ctrl+D to quit.

  ──────────────────────────────────────────────────────────────────

  You: Why did taxprofiler-003 fail?

  Assistant: Your run run-g7h8i9 (taxprofiler-003) failed on
  2024-10-30 after 41 minutes in the mz-map region. The failure
  occurred in the TAXPROFILER:KRAKEN2_KRAKEN2 process. Looking at
  the allocation logs for alloc d4e5f6a7-b8c9, the task exited with
  code 137 — that's an OOM kill. The task was allocated 32 GB but
  the Kraken2 database (PlusPF, ~60 GB uncompressed) requires more
  memory to load.

  To fix this, resume the run with a higher memory allocation:

    abc pipeline resume run-g7h8i9

  Or re-run on a datacenter with larger nodes. za-cpt-dc1 has
  nodes with 256 GB available.

  ──────────────────────────────────────────────────────────────────

  You: How much have I spent on viralrecon runs this month?

  Assistant: In November 2024 (month to date), your nf-core/viralrecon
  runs have cost R 47.92 — that is run-a1b2c3 (batch-47), which is
  still running. The estimate at completion is R 52–55 based on
  current task progress.

  ──────────────────────────────────────────────────────────────────

  You: Is my data in mz-map compliant right now?

  Assistant: Your data in mz-map has one compliance warning.
  Data object ds-006pqr (results/mz-batch-09/consensus.fasta,
  derived-sequence) was flagged because a transfer from ke-nbi to
  mz-map was attempted on 2024-10-30 without a registered DTA.
  That transfer was blocked by POLICY-02.

  The data is still in ke-nbi — it was never moved. No violation
  has occurred, but to move it you will need a DTA first:

    abc compliance dta show dta-ke-mz-001

  ──────────────────────────────────────────────────────────────────

  You: exit

  Goodbye.
```

Scope refusal example:

```
  You: What are the pipeline runs for user@org-a.example?

  Assistant: I can only access your own profile data. To view
  shared workspace activity, try:

    abc pipeline list --workspace ws-org-a-01
```

Flags:

| Flag | Description |
|---|---|
| `--prompt <text>` | Non-interactive — pass a single question, print answer to stdout |
| `--context <id>` | Pre-load a specific run or data object into the conversation context |

Non-interactive mode:

```
$ abc chat --prompt "What is the total cost of run-a1b2c3?"

  Run run-a1b2c3 (batch-47) has spent R 45.25 so far with an estimated
  final cost of R 47.92 at current task progress.
```

---

### 5.18 `abc version`

```
$ abc version

  abc  v0.2.0
  Commit        4f2a1c3
  Built         2024-10-30
  Go            1.23.2
  OS/Arch       linux/amd64
  API endpoint  https://api.org-a.example  (v0.2.0)
  Context       org-a-za-cpt

$ abc version --short
0.2.0
```

---

## 7. Exit Codes

| Code | Meaning |
|---|---|
| 0 | Success |
| 1 | General error (API error, resource not found) |
| 2 | Usage error (invalid flags or arguments) |
| 3 | Authentication error |
| 4 | Policy denial (OPA enforcement blocked the operation) |
| 5 | Data residency violation (cross-border transfer blocked) |

---

## 8. `abc policy logs` vs `abc policy audit` — Distinction

| | `abc policy logs` | `abc policy audit` |
|---|---|---|
| **Audience** | Policy developer, platform engineer | Compliance lawyer, ethics committee, grant auditor |
| **Content** | Raw OPA evaluation traces — which rules evaluated, expression results, timing | Who triggered what decision, on which resource, with what outcome |
| **Granularity** | Per-OPA-request, includes `--trace` for full expression tree | Per-compliance-event, includes user, resource, jurisdiction |
| **Primary use** | Debugging policy logic, performance profiling | Legal audit trail, grant reporting, breach investigation |
| **Retention** | Rolling window (configurable, default 7 days) | Permanent, WORM-locked per POLICY-07 |

---

## 9. Shell Composition Patterns

```bash
# Policy-check then submit
abc policy validate params/batch-47.yaml && \
  abc pipeline run --pipeline nf-core/viralrecon --params-file params/batch-47.yaml

# DTA-check then move
abc compliance dta validate ds-004jkl ke-nbi && \
  abc data move ds-004jkl ke-nbi

# Probe-only before committing to join
abc join --probe-only --jurisdiction ZA && \
  abc join --datacenter za-cpt-dc1 --region za-cpt --jurisdiction ZA

# Follow logs of the most recent automation run
RUN_ID=$(abc automation runs auto-001 --limit 1 --output json | jq -r '.[0].id')
abc pipeline logs "$RUN_ID" --follow

# Non-interactive chat in CI
abc chat --prompt "What is the total cost of run-a1b2c3?"

# Debug a policy denial with full OPA trace
abc policy logs --decision deny --limit 1 --output json | \
  jq -r '.[0].request_id' | \
  xargs -I{} abc policy logs --request {} --trace

# Dry-run a job script before committing (server-side plan via Jobs().Plan())
abc job run scripts/bwa-align.sh --dry-run --region za-cpt

# Generate HCL only and inspect before submitting
abc job run scripts/bwa-align.sh --region za-cpt > /tmp/bwa-align.hcl
cat /tmp/bwa-align.hcl
nomad job validate /tmp/bwa-align.hcl   # optional extra validation
abc job run scripts/bwa-align.sh --submit --region za-cpt

# Print HCL to stdout and pipe directly to nomad (escape hatch)
abc job run scripts/bwa-align.sh | nomad job run -

# Poll job status in CI until complete
until abc job status bwa-align-batch; do
  echo "Still running..."
  sleep 30
done
# exit 0 = succeeded, exit 1 = failed, exit 2 = still running

# Stream logs for a specific allocation immediately after dispatch
JOB_ID=$(abc job dispatch viralrecon-parameterized \
  --meta sample=ZA-INST-2024-055 --output json | jq -r '.nomad_job_id')
abc job logs "$JOB_ID" --follow

# Use NOMAD_ALLOC_INDEX inside a script to shard samples
# (set via #ABC --alloc_index in the preamble)
SAMPLE=$(sed -n "$((NOMAD_ALLOC_INDEX + 1))p" "${NOMAD_TASK_DIR}/samplesheet.csv")
```

---

## 10. Shell Completion

```
abc completion bash        > /etc/bash_completion.d/abc
abc completion zsh         > ~/.zsh/completions/_abc
abc completion fish        > ~/.config/fish/completions/abc.fish
abc completion powershell
```

Completion resolves live context names, workspace IDs, region names, run IDs, policy IDs, and automation IDs from the API.

---

## 11. Configuration File Schema

```yaml
active_context: org-a-za-cpt

contexts:
  org-a-za-cpt:
    url: https://api.org-a.example
    access_token: eyJ...
    workspace: ws-org-a-01
    region: za-cpt
    output: table

  org-b-ke-nbi:
    url: https://api.org-b.example
    access_token: eyJ...
    workspace: ws-org-b-01
    region: ke-nbi

  org-c-mz-map:
    url: https://api.org-c.example
    access_token: eyJ...
    workspace: ws-org-c-01
    region: mz-map

  org-d-be-bru:
    url: https://api.org-d.example
    access_token: eyJ...
    workspace: ws-org-d-01
    region: be-bru

defaults:
  output: table
  dry_run: false
```

---

## 12. Persona → Command Mapping

| Persona | Primary commands |
|---|---|
| **Bioinformatician** | `pipeline run/list/logs/resume`, `job run [--submit\|--dry-run]/logs/dispatch`, `data upload/download/stat`, `status`, `chat` |
| **Graduate Student** | `pipeline run/list/logs`, `data upload`, `auth login`, `status`, `chat` |
| **Data Manager** | `data *`, `storage *`, `compliance residency/dta/audit` |
| **Principal Investigator** | `pipeline list/show`, `budget summary/report`, `compliance status/report`, `status`, `chat` |
| **Accountant** | `budget list/show/report/logs` |
| **Compliance Lawyer** | `compliance *`, `policy audit/residency`, `data logs` |
| **Policy Developer** | `policy list/show/validate/logs` |
| **Server Manager** | `compute *`, `storage *`, `admin health/backup/version`, `join` |
| **Project Manager** | `workspace *`, `pipeline list`, `budget summary`, `automation list/show`, `status` |
| **Ethics Committee Member** | `compliance status/report`, `policy audit`, `data stat/logs` |
| **Trainer** | `auth login`, `config init`, `pipeline run`, `join --probe-only` |
| **External Collaborator** | `data download`, `pipeline show/logs` (read-only scoped token) |

---

## 13. Prototype Migration Notes

| Prototype command | New location | Changes |
|---|---|---|
| `abc pipeline run` | `abc pipeline run` | Add `--region`, `--datacenter`, `--watch`, `--label` |
| `abc job run <script>` | `abc job run <script>` | Preamble redesigned (see below); add `--submit`, `--dry-run`, `--output-file`, `--watch` |
| `abc data upload <path>` | `abc data upload <path>` | Add `--region`, `--tag`, `--label` |
| `abc data encrypt <path>` | `abc data encrypt <path>` | Unchanged |
| `abc data decrypt <path>` | `abc data decrypt <path>` | Unchanged |
| All global flags | All global flags | Unchanged |
| All env vars | All env vars | Unchanged |

### `#ABC` preamble migration

The old `--env=NOMAD_VAR` form is replaced by semantic boolean flags. `--region` is now a scheduler directive only — `NOMAD_REGION` is injected automatically by Nomad and needs no directive.

| Old directive | New directive | Notes |
|---|---|---|
| `#ABC --env=NOMAD_ALLOC_ID` | `#ABC --alloc_id` | — |
| `#ABC --env=NOMAD_SHORT_ALLOC_ID` | `#ABC --short_alloc_id` | — |
| `#ABC --env=NOMAD_ALLOC_NAME` | `#ABC --alloc_name` | — |
| `#ABC --env=NOMAD_ALLOC_INDEX` | `#ABC --alloc_index` | — |
| `#ABC --env=NOMAD_JOB_ID` | `#ABC --job_id` | — |
| `#ABC --env=NOMAD_JOB_NAME` | `#ABC --job_name` | — |
| `#ABC --env=NOMAD_JOB_PARENT_ID` | `#ABC --parent_job_id` | — |
| `#ABC --env=NOMAD_TASK_NAME` | `#ABC --task_name` | — |
| `#ABC --env=NOMAD_GROUP_NAME` | `#ABC --group_name` | — |
| `#ABC --env=NOMAD_NAMESPACE` | `#ABC --namespace` | Env exposure only; use `#ABC --namespace=<ns>` for scheduler placement |
| `#ABC --env=NOMAD_REGION=global` | `#ABC --region=za-cpt` | **Breaking:** `--region` is now a scheduler directive. `NOMAD_REGION` is automatic — no exposure directive needed |
| `#ABC --env=NOMAD_DC` | `#ABC --dc` | `--dc=<n>` is the scheduler directive; bare `--dc` exposes the runtime var |
| `#ABC --env=NOMAD_ALLOC_DIR` | `#ABC --alloc_dir` | — |
| `#ABC --env=NOMAD_TASK_DIR` | `#ABC --task_dir` | — |
| `#ABC --env=NOMAD_SECRETS_DIR` | `#ABC --secrets_dir` | — |
| `#ABC --env=NOMAD_CPU_LIMIT` | `#ABC --cpu_limit` | — |
| `#ABC --env=NOMAD_CPU_CORES` | `#ABC --cpu_cores` | — |
| `#ABC --env=NOMAD_MEMORY_LIMIT` | `#ABC --mem_limit` | — |
| `#ABC --env=NOMAD_IP_<label>` | `#ABC --port <label>` | Also generates `network` stanza; exposes `NOMAD_PORT_` and `NOMAD_ADDR_` |
| `#ABC --env=NOMAD_PORT_<label>` | `#ABC --port <label>` | Covered by `--port` |
| `#ABC --env=NOMAD_ADDR_<label>` | `#ABC --port <label>` | Covered by `--port` |
