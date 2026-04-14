# `abc` CLI — Command Design Specification v6

> **Purpose:** This document is the authoritative specification for an AI agent implementing the next
> iteration of the `abc` CLI. It incorporates all design decisions resolved from v5 review comments.
> Decisions are stated clearly; open questions are marked **[TBD]** with context.
> The existing codebase is at `github.com/abc-cluster/abc-cluster-cli`.

---

## 0. What Changed from v5 → v6

A summary of every resolved decision so an agent can diff against the current codebase.

### 0.1 Command tree restructure

| v5 command group | v6 command group | Action |
|-----------------|-----------------|--------|
| `abc node *` | `abc infra node *` | Move under new `abc infra` group |
| `abc storage *` | `abc infra storage *` | Move under `abc infra` |
| `abc compute *` | `abc infra compute *` | Move under `abc infra` |
| `abc compute hpc status <b>` | `abc infra node status` | Fold into node status |
| `abc compute hpc jobs <b>` | `abc infra node jobs` (TBD) | Classify by type: cloud / hpc / local |
| `abc ssh` | `abc infra node ssh --id <node-id>` | Move under `abc infra node` |
| `abc budget *` | `abc cost *` | Rename (see §0.3) |
| `abc compliance *` | `abc admin services jurist *` | Move (plumbing; see §0.4) |
| `abc namespace *` | `abc admin services nomad --namespaces` | Surfaced under admin for now |
| `abc service *` | `abc admin services *` | Move under admin |
| `abc join` | TBD | Move to TBD section (overlaps with `abc infra node add`) |

### 0.2 `abc submit` — Remove `--conda` and `--pixi` CLI flags

`--conda` and `--pixi` are removed from `abc submit` flags.
These package-manager modes are supported **only in job script preambles** (`#ABC --conda=<spec>`).

Updated detection table (rows 2 and 2a are removed):

| Priority | Condition | Dispatches to |
|----------|-----------|---------------|
| 1 | `--type pipeline\|job\|module` | forced |
| 2 | `<target>` is a local file | `job run --submit` |
| 3 | `<target>` starts with `http://` or `https://` | `pipeline run` |
| 4 | `<target>` has ≥ 3 path segments (e.g. `nf-core/cat/fastq`) | `module run` |
| 5 | `<target>` matches `owner/repo` (exactly one `/`) | `pipeline run` |
| 6 | Nomad Variables lookup `nomad/pipelines/<target>` succeeds | `pipeline run` |
| — | no match | error — use `--type` |

**Module syntax [TBD]:** A module is a type of pipeline, but the exact syntax for disambiguating
nf-core modules from nf-core pipelines (both can have the `nf-core/<name>` shape) is still being
decided. For now, ≥ 3 path segments is the module heuristic (e.g. `nf-core/cat/fastq`).

**Implementation change:** Remove `--conda`, `--conda-solver`, `--pixi`, `--tool-arg` flags from
`cmd/submit/cmd.go`. Remove `generateCondaWrapper`, `generatePixiWrapper` from `cmd/submit/conda.go`
(or keep as internal helpers callable from `job run` path only).
Update USAGE.md submit section accordingly.

### 0.3 `abc budget` → `abc cost`

Rename the command group to `abc cost` (or `abc expenses` — `cost` preferred).
Data source is the XTDB ledger database via the ABC control plane.
Scope expands to include **node-level** storage, network, and CPU expenses — not just pipeline/job
level expenses.

File change: `cmd/budget/` → `cmd/cost/`; update `cmd/root.go` registration.
Command names stay the same internally: `summary`, `list`, `show`, `report`, `logs`.
Add `set` under admin, not under `cost`.

### 0.4 `abc compliance` → `abc admin services jurist`

The compliance commands are plumbing-level — they surface raw Jurist policy evaluation results.
Move them under `abc admin services jurist` until a polished porcelain interface is designed.

`abc policy *` stays as-is (already user-facing policy validation).
`abc compliance status/audit/residency/dta/report` → `abc admin services jurist status/audit/...`

### 0.5 Nomad API client — corrected description

The v5 text incorrectly says the CLI uses `github.com/hashicorp/nomad/api` (the Nomad SDK).
**Correction:** The CLI implements its own lightweight HTTP client (`cmd/utils/nomad_client.go`)
that talks directly to the Nomad REST API. The SDK is NOT a dependency.

Two operating modes:
1. **Control-plane mode (normal):** CLI → ABC control plane (jurist / cloud gateway) → Nomad.
   The control plane proxies and enriches requests.
2. **Standalone mode (user node):** User deploys a bare Nomad dev server without the control plane.
   CLI → Nomad REST API directly. Fewer features (no cost tracking, no policy, no namespace management).
   NOMAD_ADDR / NOMAD_TOKEN env vars stored locally, encrypted via `mozilla/sops` using the
   control-plane-issued encryption key in the config file.

### 0.6 `abc job run` — New features (phased)

These are additions to `abc job run` ordered by priority:

**Phase 1 (implement now):**
- `--ssh` / `--ssh-timeout <dur>` — after submit, open an interactive shell into the allocation
  via `nomad alloc exec`. Uses the Nomad API `AllocFS.Exec()` endpoint.

**Phase 2 (design ready, implement next sprint):**
- Named and positional parameters: `--param key=value` (named) or `--param $1` (unnamed, auto-enumerated).
  Parameters are injected as meta into the job and optionally used to spawn data-staging prestart tasks.
- `abc://` URI scheme: a URI type for data tracked by the ABC cluster.
  When a param value starts with `abc://`, the CLI resolves it to a concrete path + injects a
  prestart lifecycle task that stages the data before the main task starts.
- Output copy: default output is `NOMAD_TASK_DIR`. If `--output-dir <path>` is provided, a
  poststart lifecycle task copies task outputs to the non-default path after the main task completes.

**Phase 3 (TBD — needs control plane support):**
- `#ABC --green` preamble directive: signals that the job can be deferred to a green energy window.
  Control plane holds the job until conditions are met.
- `#ABC --budget=<cap>` preamble directive: job-level spend cap in the workspace currency.
  Control plane enforces and stops/pauses the job if the cap is reached.
- Enhanced `--dry-run`: estimate cost (ZAR) and carbon footprint via ML model trained on XTDB
  historical data. Control plane provides the estimate; CLI displays it.
- HPC direct submission: `--hpc <backend>` flag to submit a native SLURM/PBS script to a
  connected HPC backend without going through Nomad. Migration path for existing HPC users.

### 0.7 `abc job trace` — New command (TBD)

New subcommand `abc job trace <id>` that shows the full post-submission lifecycle of a job:
events emitted by the control plane (reschedule, scale, resume, cost events) as an audit trail.

```
abc job trace bwa-align-batch

  TIMESTAMP             EVENT         DETAIL
  2024-11-01 08:14:32   submitted     eval b3c4d5e6
  2024-11-01 08:22:11   alloc.placed  a1b2c3d4 → hpc-a-node-014
  2024-11-01 09:14:05   rescheduled   alloc c3d4e5f6 (OOM) → hpc-a-node-007
  2024-11-01 10:45:01   complete      exit 0, 48/48 succeeded
```

Flags: `--data` shows which data objects were generated during the run.
Partially overlaps with `abc pipeline monitor` (future); keep separate at the job level.

### 0.8 Pipeline enhancements

**`abc pipeline show <id>`** — data currently comes from Nomad Variables.
Future: merge with Seqera Tower / Nextflow Web API for richer task-level monitoring.

**`abc pipeline status <id>`** [TBD] — lightweight command showing only task-level execution
counts (SUBMITTED / RUNNING / SUCCEEDED / FAILED / CACHED per process). Faster than `show`.

**`abc pipeline monitor <id>`** [TBD] — porcelain follow command:
streams status updates for a running pipeline, can trigger automations or send notifications
(email/webhook) on completion or failure.

**`abc pipeline delete <id>`** — add `--with-data` flag:
when `--sudo` is present, optionally delete all data objects generated during the run.
```
abc pipeline delete run-a1b2c3 --sudo --with-data
```

**`abc pipeline params show`** — auto-select the latest execution of the named pipeline
if `--id` is not provided (query Nomad Variables for the most recent run metadata).

**Child job naming convention:** Jobs spawned by a Nextflow head job (via nf-nomad plugin) should
include the run UUID in their Nomad job ID so they can be traced back to the head job.
Proposed format: `nf-<run-uuid-prefix>-<process-slug>`.
This enables `abc job list --run <run-uuid>` to filter child jobs of a pipeline run.

### 0.9 `abc infra node ssh` (moved from `abc ssh`)

The top-level `abc ssh` command moves to `abc infra node ssh`.
Flags and behaviour are unchanged.

Additionally, `abc job run` gains `--ssh` / `--ssh-timeout` for interactive debugging of a
running job allocation (separate from node SSH — this is `nomad alloc exec`).

### 0.10 `abc admin services` — expanded service list

The service health/ping/version commands move under `abc admin services`.
Add the following services to the status surface (currently missing):

| Service key | Description |
|-------------|-------------|
| `tailscale` | Tailscale VPN connectivity |
| `xtdb` | XTDB ledger database (cost/audit events) |
| `supabase` | Supabase (auth, workspace metadata) |
| `khan` | ABC control-panel / API gateway |

`abc admin services list` — show all known services and their current status.
`abc admin services ping <service>` — test connectivity to a specific service.
`abc admin services version <service>` — get the version of a specific service.

Access requires cluster-admin privilege (`--sudo`).

### 0.11 `abc cost set` (moved from `abc budget set`)

Budget cap management moves to `abc admin services` or `abc cost set --sudo`.
The `cost` group is user-facing (read-only spend reports); `cost set` requires `--sudo` or `--cloud`.

### 0.12 `abc join` → TBD

`abc join` overlaps significantly with `abc infra node add` (SSH + preflight + register).
Move to TBD pending a decision on the exact interface.

Open question: if a user wants to share access to their personal node with another user,
the interface should support `abc infra node share --id <node-id> --user <email>` (future).

### 0.13 Elevation model — clarifications

**`--sudo` additionally enables user impersonation for admins:**
```
abc job list --sudo --user researcher@org-a.example
```
An admin can inspect or act on behalf of another user. The control plane verifies the caller
has impersonation permission; the CLI signals intent via `X-ABC-As-User: <email>` header.

**Tiers 1 (group-admin) and 2 (cluster-admin) [TBD]:**
Both are activated by `--sudo` today. Whether these need separate flags or remain merged under
`--sudo` (with the server deciding) needs further discussion. Keep as-is for now.

**`abc chat` — no `--sudo` needed:**
The chat command's permission scope is determined entirely by the server based on the caller's
token. Users do not pass `--sudo` to `abc chat`. The assistant silently widens its access to
whatever the caller's token permits.

**Encryption/decryption login requirement:**
`abc data encrypt` and `abc data decrypt` require an authenticated session (control-plane token
used as SOPS encryption key). Without internet access, offline nodes cannot encrypt/decrypt.
This is an acceptable trade-off for initial releases. Revisit for air-gapped deployments.

### 0.14 `abc chat` — baseline prompt presets

Provide a discoverable preset menu when the user opens `abc chat` without a `--prompt` argument:

```
Suggested questions:
  1. Why did my last pipeline fail?
  2. How much have I spent this month?
  3. Is my data in <region> compliant?
  4. What jobs are currently running?
  5. (type your own question)
```

Presets serve double duty: they guide new users and capture structured feedback that improves
the assistant without requiring explicit opt-in.

### 0.15 `abc job dispatch` → `abc job template` [TBD]

Rename/rebrand `abc job dispatch` to `abc job template` to better communicate its purpose:
a parameterized job definition stored in the cluster workspace that can be triggered by any
authorized user with only a parameters file.

Stored templates are: workspace-scoped, version-controlled, and triggerable via
`abc job template run <name> --params <file>`.

Design overlaps with `abc pipeline run` (both are "run a stored definition with params").
Resolve by scoping: `pipeline run` = Nextflow pipelines; `job template run` = arbitrary scripts.

---

## 1. Persona-based command surface

Commands are organized by user persona. Each persona has a distinct concern; operators switch
personas by context (their token grants the right access).

| Persona | Primary commands | Elevation needed |
|---------|-----------------|-----------------|
| 0 — Generic user | `auth`, `config`, `context`, `version`, `chat` | none |
| 1 — Bioinformatician / ML user | `submit`, `pipeline`, `module`, `job`, `data`, `secret`, `workspace`, `automation` | none |
| 2 — Legal / compliance officer | `policy` | none (read); `--sudo` (write) |
| 3 — Accountant | `cost` | none |
| 4 — Infrastructure operator | `infra node`, `infra storage`, `infra compute` | `--sudo` or `--cloud` |
| 5 — Cluster admin / developer | `admin services`, `admin users` | `--sudo` |
| 6 — Project manager | `workspace`, `automation` | none |

---

## 2. Command Tree (v6)

```
abc
│
│  ── Generic ────────────────────────────────────────────────────────────
├── auth         login · logout · whoami · token · refresh
├── config       init · set · get · list · unset
├── context      list · show · add · use · remove
├── version
├── chat         [--prompt <text>] [--context <id>]
│
│  ── Bioinformatician / ML user ─────────────────────────────────────────
├── submit       <target> [flags]        (porcelain dispatch — auto-detects)
├── pipeline     run · add · update · list · info · delete · export · import
│                status (TBD) · monitor (TBD)
├── module       run
├── job          run · list · show · stop · logs · status · trace (TBD)
│                template (TBD, replaces dispatch)
├── data         upload · download · list · show · delete · encrypt · decrypt
├── secret       list · show · create · delete · logs
├── workspace    list · show · create · delete · use · members
├── automation   list · show · create · enable · disable · delete · logs · runs · triggers
│
│  ── Legal / Policy ──────────────────────────────────────────────────────
├── policy       list · show · validate · logs · audit · residency
│
│  ── Accountant ──────────────────────────────────────────────────────────
├── cost         summary · list · show · report · logs          (was: budget)
│                set (requires --sudo or --cloud)
│
│  ── Infrastructure operator ─────────────────────────────────────────────
├── infra
│   ├── node     add · list · show · ssh · drain · undrain · terminate
│   ├── storage  size                                (users see size only)
│   │            buckets list/create/stat            (plumbing — hidden by default)
│   │            objects list/get/put/stat            (plumbing — hidden by default)
│   └── compute  nodes · allocations · datacenters · hpc
│
│  ── Cluster administration ──────────────────────────────────────────────
├── admin
│   ├── services list · ping · version
│   │            services: nomad · jurist · minio · tus · xtdb ·
│   │                       supabase · tailscale · khan · cloud-gateway
│   │            jurist subcommands: status · audit · residency · dta · report
│   │                                (was: abc compliance *)
│   │            nomad subcommands:  namespaces (list/show/create/delete)
│   │                                (was: abc namespace *)
│   └── users    list · create · delete · token
│
│  ── TBD / Future ───────────────────────────────────────────────────────
└── join         (TBD — overlaps with abc infra node add; deferred)
```

---

## 3. Design Principles (updated)

| Principle | Statement |
|-----------|-----------|
| **Persona-aware surface** | Commands grouped by concern and user role, not internal component |
| **Sovereignty-first flags** | `--region` is a first-class flag on every command that touches data or compute |
| **Pipe-friendly** | Resource creation → ID to stdout; everything else → stderr |
| **Dry-run everywhere** | Every mutating command accepts `--dry-run` |
| **Context-aware** | A context holds endpoint + token + workspace + region; switch contexts, not flags |
| **Consistent verbs** | `list`, `show`, `create`, `delete`, `use` across all resource groups |
| **User-scoped by default** | Commands operate on the authenticated user's profile. Admins use `--sudo --user <email>` to act on behalf of others |
| **Plumbing hidden** | Implementation details (buckets, raw allocations, internal services) are reachable but not highlighted in help output |

---

## 4. Nomad HTTP Client (corrected)

The CLI does **not** use `github.com/hashicorp/nomad/api`. It implements a minimal HTTP client
(`cmd/utils/nomad_client.go`) that speaks directly to the Nomad v1 REST API.

### Two operating modes

**Control-plane mode (default):**
```
abc CLI
  │ X-Nomad-Token: <user-token>
  │ X-ABC-Sudo: 1   (when --sudo)
  │ X-ABC-Cloud: 1  (when --cloud)
  ▼
Cloud Gateway → Jurist → Nomad REST API
```
The control plane provides: cost tracking, policy enforcement, namespace enforcement,
credential management, and audit logging.

**Standalone mode (bare Nomad dev node, no control plane):**
```
abc CLI → Nomad REST API directly
```
Used when a user provisions a personal node via `abc infra node add --local`.
`NOMAD_ADDR` and `NOMAD_TOKEN` stored in `~/.abc/config.yaml`, with sensitive fields encrypted
via `mozilla/sops` using a key derived from the user's control-plane token.
Features unavailable in standalone mode: cost tracking, policy, namespace management, chat.

---

## 5. Global Flags (v6)

| Flag | Short | Env var | Description |
|------|-------|---------|-------------|
| `--url` | `-u` | `ABC_API_ENDPOINT` | ABC control-plane API endpoint |
| `--access-token` | `-t` | `ABC_ACCESS_TOKEN` | User access token |
| `--workspace` | `-w` | `ABC_WORKSPACE_ID` | Workspace ID |
| `--region` | `-r` | `ABC_REGION` | Nomad region (jurisdiction boundary) |
| `--output` | `-o` | `ABC_OUTPUT` | `table` (default), `json`, `yaml` |
| `--context` | | `ABC_CONTEXT` | Named config context |
| `--quiet` / `-q` | | | Suppress informational stderr output (banners, progress) |
| `--dry-run` | | | Print what would happen without executing |
| `--debug[=N]` | | `ABC_DEBUG` | Write structured JSON debug log (0=off, 1=default, 2=verbose, 3=max) |
| `--sudo` | | `ABC_CLI_SUDO_MODE` | Cluster-admin elevation; also enables `--user` for impersonation |
| `--cloud` | | `ABC_CLI_CLOUD_MODE` | Infrastructure elevation; enables fleet-wide and cloud provider ops |
| `--exp` | | `ABC_CLI_EXP_MODE` | Enable experimental features |
| `--cluster` | | `ABC_CLUSTER` | Target a specific named cluster (requires `--cloud`) |
| `--user <email>` | | | Act on behalf of another user (requires `--sudo` + impersonation permission) |

---

## 6. Elevation Model (v6)

| Tier | Activated by | Scope | Server validates |
|------|-------------|-------|-----------------|
| 0 — user | (default) | Own namespace, own jobs | token claims |
| 1 — group/cluster admin | `--sudo` | Namespace-wide or cluster-wide (server decides based on token) | jurist |
| 2 — cloud / infrastructure | `--cloud` | Fleet-wide; cloud provider APIs | cloud gateway |

`--sudo --user <email>` additionally enables admin impersonation — acts on behalf of `<email>`.
Sends `X-ABC-As-User: <email>` header; server enforces the impersonation permission.

`--sudo` and `--cloud` may be combined. Both headers are sent; request traverses:
CLI → cloud gateway → jurist → Nomad.

`abc chat` — elevation is **never** specified by the user for chat. The server determines what
the chat assistant can see based on the caller's token. Passing `--sudo` to chat is a no-op.

---

## 7. `abc submit` (updated — no --conda / --pixi)

```
abc submit <target> [flags]
```

Auto-detects dispatch mode. `--conda` and `--pixi` removed from CLI surface
(use `#ABC --conda=<spec>` in job script preamble instead).

### Detection order

| Priority | Condition | Dispatches to |
|----------|-----------|---------------|
| 1 | `--type pipeline\|job\|module` | forced |
| 2 | `<target>` is a local file | `job run --submit` |
| 3 | `<target>` starts with `http://` or `https://` | `pipeline run` |
| 4 | `<target>` has ≥ 3 path segments | `module run` |
| 5 | `<target>` matches `owner/repo` (one `/`) | `pipeline run` |
| 6 | Nomad Variables lookup succeeds | `pipeline run` |
| — | no match | error — use `--type` |

### Flags

| Flag | Description |
|------|-------------|
| `--input <path>` | Input file/directory (→ `params.input`) |
| `--output <path>` | Output directory (→ `params.outdir`) |
| `--param key=val` | Extra parameter (repeatable) |
| `--type pipeline\|job\|module` | Force mode |
| `--revision <string>` | Git branch/tag/SHA (pipeline) |
| `--profile <string>` | Nextflow profile(s), comma-separated |
| `--config <path>` | Extra Nextflow config (pipeline) |
| `--work-dir <path>` | Nextflow work directory |
| `--nf-version <string>` | Nextflow Docker image tag |
| `--cores <int>` | CPU cores (job mode) |
| `--mem <size>` | Memory e.g. `4G` (job mode) |
| `--time <HH:MM:SS>` | Walltime (job mode) |
| `--name <string>` | Override Nomad job name |
| `--namespace <string>` | Nomad namespace |
| `--datacenter <string>` | Nomad datacenter (repeatable) |
| `--wait` | Block until job completes |
| `--logs` | Stream logs after submit |
| `--dry-run` | Print generated HCL without submitting |

---

## 8. `abc job run` (updated)

### Preamble — Conda / package manager directives

The `--conda`, `--conda-solver`, and `--pixi` activation modes are **preamble-only** — they are
not exposed as CLI flags on `abc job run`. Users declare them in the script:

```bash
#!/bin/bash
#ABC --conda=fastqc                     # activate env named "fastqc" via conda (default)
#ABC --conda=fastqc --conda-solver=mamba  # use mamba instead of conda
#ABC --pixi                             # run via pixi (reads pixi.toml)
#ABC --cores=4
```

The preamble parser in `cmd/job/directive.go` already handles `--conda` and `--conda-solver`.
`--pixi` needs to be added as a new boolean Class 1 directive.

When the control plane is enabled, it detects `abc_conda` / `abc_pixi` meta keys and:
- Selects or builds the appropriate container image (e.g. via Seqera Wave)
- Injects runtime configuration into the worker task spec

When running standalone (no control plane), the script is responsible for its own environment.

### New flags (Phase 1 — implement now)

| Flag | Description |
|------|-------------|
| `--ssh` | After submit, open an interactive shell into the first running allocation via `nomad alloc exec` |
| `--ssh-timeout <dur>` | How long to wait for the allocation to start before giving up (default: `5m`) |

### New preamble directives (Phase 2)

| Directive | Description |
|-----------|-------------|
| `#ABC --param=key=value` | Named parameter injected as meta. Accessible as `NOMAD_META_KEY`. |
| `#ABC --param=$1` | Positional parameter (auto-enumerated as `param_1`, `param_2`, …). |
| `#ABC --output-dir=<path>` | After main task, copy task outputs to this path (poststart lifecycle hook). |

`abc://` URI handling: when a `--param` value starts with `abc://`, the CLI:
1. Resolves the URI to a concrete storage path via the ABC API
2. Injects a prestart lifecycle task that stages the data into `NOMAD_TASK_DIR` before the main task

### New preamble directives (Phase 3 — control plane required)

| Directive | Description |
|-----------|-------------|
| `#ABC --green` | Hold the job until a green energy window is available |
| `#ABC --budget=<cap>` | Job-level spend cap (e.g. `--budget=50ZAR`); control plane enforces |

### `abc job trace <id>` (new, TBD)

Post-submission audit trail showing control-plane events (reschedule, scale, resume, cost).

```
Flags:
  --data    Also list data objects generated during this job run
  --follow  Stream events in real time
```

### `abc job template` (replaces `abc job dispatch`, TBD)

Stored parameterized job definitions scoped to the workspace.
Any authorized user can trigger with a params file:
```
abc job template run <name> --params <file.yaml>
abc job template list
abc job template show <name>
abc job template create <script> --name <name>
abc job template delete <name>
```

---

## 9. `abc pipeline` (updated)

### Child job naming convention

Jobs spawned by a Nextflow head job via nf-nomad must include the pipeline run UUID in
their Nomad job ID. Proposed format: `nf-<8-char-run-uuid>-<process-slug>`.

This enables:
```
abc job list --run <run-uuid>       # list all child jobs of a pipeline run
```

The nf-nomad plugin config should be updated to use this naming template.

### New commands

**`abc pipeline status <id>`** [TBD]
Lightweight task-progress summary (SUBMITTED / RUNNING / SUCCEEDED / FAILED / CACHED per process).
Faster than `pipeline show`; suitable for CI polling.
```
Exit codes: 0=succeeded, 1=failed, 2=running, 3=cancelled
```

**`abc pipeline monitor <id>`** [TBD]
Porcelain follow mode. Streams status updates, optionally triggers automations or sends
notifications (email, webhook) on completion, failure, or stage transitions.
```
abc pipeline monitor run-a1b2c3 --on-complete "abc automation trigger weekly-report"
abc pipeline monitor run-a1b2c3 --notify email:admin@org-a.example
```

### Updated flags

**`abc pipeline delete <id>`** — add flags:

| Flag | Description |
|------|-------------|
| `--yes` | Skip confirmation prompt |
| `--with-data` | Also delete all data objects generated during this run (requires `--sudo`) |

**`abc pipeline params show`** — if `--id` is not provided, auto-select the most recent execution
of the named pipeline from Nomad Variables metadata.

---

## 10. `abc infra` (new group)

All commands require at least `--sudo`; some require `--cloud`.

### `abc infra node`

Consolidates the current `abc node *` + `abc compute nodes *` + `abc compute hpc *` commands.

| Subcommand | Description | Was |
|-----------|-------------|-----|
| `add` | Provision and register a compute node | `abc node add` |
| `list` | List all nodes (requires `--sudo`) | `abc node list` / `abc compute nodes list` |
| `show <id>` | Node detail including storage, network speed | `abc compute nodes show` |
| `ssh --id <id>` | SSH into a specific node | `abc ssh` |
| `drain <id>` | Set node to draining | `abc node drain` / `abc compute nodes drain` |
| `undrain <id>` | Cancel drain | `abc node undrain` / `abc compute nodes undrain` |
| `terminate <id>` | Destroy the underlying VM (requires `--cloud`) | `abc node terminate` |
| `status [--hpc <backend>]` | Node status including HPC backend health | `abc compute hpc status` |
| `jobs [--hpc <backend>]` | Active HPC scheduler jobs on a node/backend | `abc compute hpc jobs` |

**`abc infra node show`** additions (beyond v5):
- Display node-local disk usage and free space
- Display last network speed test result (if available)
- Show a trigger button / note: `abc infra node probe <id>` to refresh stats

**`abc infra node probe <id>`** [new] — trigger the abc-node-probe automation to collect fresh
hardware stats (CPU, memory, disk, network) for the node.

**Node classification [TBD]:**
Nodes should be classifiable as: `cloud`, `hpc`, or `local`.
`abc infra node list --type cloud|hpc|local` to filter.
This replaces the `abc compute hpc *` subgroup.

### `abc infra storage`

`abc infra storage size` — unchanged from current `abc storage size`.

`abc infra storage buckets *` and `abc infra storage objects *` — plumbing commands.
Hidden from `--help` by default (`Hidden: true` in cobra). Still accessible if known.
Users do not need to know about buckets directly; the data command handles objects via the ABC API.

### `abc infra compute`

`abc infra compute nodes *`, `abc infra compute allocations *`, `abc infra compute datacenters *`
— moved from `abc compute *`. No functional changes, only path change.

---

## 11. `abc cost` (renamed from `abc budget`)

```
abc cost summary [--from <period>]
abc cost list [--run-id <id>] [--from <date>] [--to <date>]
abc cost show <run-id>
abc cost report [--from <date>] [--to <date>] [--group-by pipeline|job|user|dc]
abc cost logs [--run-id <id>]
abc cost set --namespace <ns> --cap <amount> --currency <ZAR|USD>   (requires --sudo/--cloud)
```

Data source: XTDB ledger database via ABC control plane.

Scope (expanded from v5):
- Pipeline and job expenses (as before)
- **Node-level** expenses: CPU, memory, network, storage per node
- Egress costs between regions

---

## 12. `abc admin` (new group)

Requires `--sudo` for all subcommands.

### `abc admin services`

Health, ping, and version for all ABC backend services.

```
abc admin services list
abc admin services ping <service>
abc admin services version <service>
```

Recognized service names:
`nomad`, `jurist`, `minio`, `tus`, `xtdb`, `supabase`, `tailscale`, `khan`, `cloud-gateway`

**`abc admin services jurist`** — exposes the compliance/policy-evaluation commands.
Was `abc compliance *`. Subcommands: `status`, `audit`, `residency`, `dta list/show/validate`, `report`.

**`abc admin services nomad --namespaces`** — namespace management.
Was `abc namespace *`. Subcommands: `list`, `show`, `create`, `delete`.

### `abc admin users`

```
abc admin users list
abc admin users create <email> --role <role>
abc admin users delete <email>
abc admin users token <email>
```

---

## 13. `abc status` (updated)

The top-level `abc status` shows the health of every service the CLI can reach.
Expand the service list to include the new services (§0.10):

```
SERVICE            STATUS    VERSION    LATENCY
──────────────────────────────────────────────
Nomad              healthy   1.9.4      12ms
Jurist             healthy   0.8.2       8ms
ABC REST API       healthy   2.1.0      34ms
MinIO              healthy   RELEASE    21ms
Tus upload server  healthy   1.4.0      15ms
Cloud Gateway      healthy   0.3.1      45ms
XTDB               healthy   0.16.3      9ms
Supabase           healthy   —          18ms
Tailscale          healthy   1.74.1      4ms
Khan               healthy   0.1.0      11ms
```

`abc status` is the user-facing alias. Privileged detail (`abc admin services list`) shows
additional per-service diagnostic information.

---

## 14. `abc chat` (updated)

- No `--sudo` flag needed; elevation is inferred from the caller's token by the server.
- Open `abc chat` without `--prompt` shows a preset menu:

```
  ABC Assistant  ·  admin@org-a.example  ·  ws-org-a-01

  Suggested questions:
    1. Why did my last pipeline fail?
    2. How much have I spent this month?
    3. Is my data compliant?
    4. What jobs are currently running?
    5. (type your own question)

  You:
```

Preset selection captures structured feedback that can be used to improve the assistant.
Responses improve based on user interactions (opt-in telemetry, not silent).

---

## 15. `abc secret` (updated)

Backend: Vault, accessed through the ABC API layer (CLI never talks to Vault directly).
Credentials visible to a user: self, workspace-level, org-level, cluster-level.
Other workspace credentials visible only with appropriate permissions (jurist-enforced).

Local config file secrets (NOMAD_ADDR, NOMAD_TOKEN for standalone mode): encrypted via
`mozilla/sops` using the control-plane-issued token as the encryption key.
This means: a logged-in user's local node config is automatically decryptable when online.

---

## 16. TBD Section

Items deferred for future design discussion:

| Item | Reason deferred |
|------|----------------|
| `abc join` | Overlaps with `abc infra node add`; need clear distinction. Also: node sharing between users (`abc infra node share`) |
| `abc job template` (replaces `dispatch`) | Parameterized job templates need UX for stored vs on-the-fly; overlap with `abc pipeline run` |
| `abc pipeline status` | Needs Tower/Nextflow web API integration decision |
| `abc pipeline monitor` | Needs automation trigger interface design |
| `abc job trace` | Needs control-plane event API design |
| `#ABC --green` | Needs green-window scheduling logic in control plane |
| `#ABC --budget` | Needs per-job ledger integration |
| Enhanced `--dry-run` with cost estimate | Needs ML model from XTDB data |
| HPC direct submission path | Needs `--hpc <backend>` flag design and bridge to SLURM/PBS |
| `--depend` directive | Inter-job dependency design (prestart lifecycle hook) |
| Elevation tier split (`--sudo` group-admin vs cluster-admin) | Whether these need separate flags |
| Module syntax disambiguation | `nf-core/<name>` 2-segment: pipeline or module? Needs explicit rule |
| `abc infra node jobs --type cloud|hpc|local` | Node classification taxonomy |

---

## 17. Implementation Checklist for AI Agent

Work items derived from §0.* above. Each item is self-contained.

### Immediate (no TBD dependency)

- [ ] **Remove `--conda`, `--conda-solver`, `--pixi`, `--tool-arg` from `cmd/submit/cmd.go`**
  Update `cmd/submit/detect.go` (remove `pixi` param), `cmd/submit/submit.go` (remove pixi/conda
  branches), `cmd/submit/conda.go` (can be deleted or kept for future use).
  Update USAGE.md submit section.

- [ ] **Add `#ABC --pixi` as a boolean preamble directive in `cmd/job/directive.go`**
  Add case `"pixi"` to `applyDirective`. Set `spec.Pixi = true`. Add `Pixi bool` to `jobSpec`.
  Emit `meta["abc_pixi"] = "true"` when true.

- [ ] **Rename `cmd/budget/` → `cmd/cost/`**
  Update package name, `cmd/root.go` import, command registration.
  Change `rootCmd.AddCommand(budget.NewCmd())` → `cost.NewCmd()`.
  Change cobra `Use: "budget"` → `Use: "cost"`.
  Add `"budget"` as a deprecated alias with a deprecation warning.

- [ ] **Create `cmd/infra/` package group with subgroups `node`, `storage`, `compute`**
  Move existing `cmd/node/` → `cmd/infra/node/`.
  Move `cmd/storage/` → `cmd/infra/storage/`.
  Register `abc infra` in `cmd/root.go`.
  Mark old top-level `abc node` and `abc storage` as deprecated aliases.

- [ ] **Move `abc ssh` → `abc infra node ssh`**
  Add `ssh` subcommand to `cmd/infra/node/`. Existing `abc ssh` in the command tree becomes a
  deprecated alias. Flags and behaviour unchanged.

- [ ] **Add `--user <email>` flag to `cmd/root.go` persistent flags**
  Only effective when `--sudo` is also set.
  Send `X-ABC-As-User: <email>` HTTP header in `cmd/utils/nomad_client.go`.

- [ ] **Create `cmd/admin/` package with `services` and `users` subgroups**
  Move `cmd/service/` → `cmd/admin/services/`. Keep `abc service` as deprecated alias.
  Add `jurist` subgroup (moved from `cmd/compliance/` if it exists, else design TBD).
  Add `nomad --namespaces` subgroup (moved from `cmd/namespace/`). Keep `abc namespace` deprecated.

- [ ] **Expand `abc status` service list**
  Add `xtdb`, `supabase`, `tailscale`, `khan` to the service ping/version registry in
  `cmd/service/cmd.go` (or `cmd/admin/services/` after move).

- [ ] **Add `--ssh` and `--ssh-timeout` flags to `abc job run` (`cmd/job/cmd.go`)**
  After `--submit`, wait for allocation to become running, then call
  `nomad alloc exec -i -t <alloc-id> -- /bin/bash`.
  Use `cmd/utils/nomad_client.go`'s `AllocExec` method (add if missing).

- [ ] **Add `--with-data` flag to `abc pipeline delete`**
  Only effective with `--sudo`. Sends additional delete request to ABC API for data objects
  tagged with the run ID.

- [ ] **Update `docs/abc-cli-design-v6.md`** — this file is the new canonical spec.
  Archive `docs/abc-cli-design.md` → `docs/abc-cli-design-v5-archived.md`.

- [ ] **Update USAGE.md** to reflect all command renames and new flag additions.

### Requires control-plane or backend changes (implement stubs now, full implementation later)

- [ ] `abc infra node probe <id>` — stub: print "triggering node probe..." + call placeholder API.
- [ ] `abc infra node show` — add storage and network fields to output (fetch from API; show N/A if missing).
- [ ] `abc job trace <id>` — stub command: print "trace not yet available" + return events from
  Nomad eval/alloc history as a fallback.
- [ ] `abc chat` — remove `--sudo` from flag list (no-op if present; document removal in USAGE.md).
- [ ] `abc chat` — add preset menu when opened without `--prompt` argument.
- [ ] `abc pipeline params show` — if no `--id`, query latest run metadata from Nomad Variables.
