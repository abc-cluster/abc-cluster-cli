# `abc` CLI — Command Design Specification v3

> **Status:** Design draft — no implementation
> **Prototype baseline:** v0.1.4 (`pipeline run`, `job run`, `data upload/encrypt/decrypt`)
> **Language:** Go (Cobra + Viper)
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

## 2. Global Flags

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

## 3. Environment Variables

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

## 4. Command Tree

```
abc
├── auth        login · logout · whoami · token · refresh
├── config      init · set · get · list · unset
├── context     list · show · add · use · remove
├── workspace   list · show · create · delete · use · members (list/add/remove)
├── status
├── pipeline    run · list · show · cancel · resume · delete · logs · params (show/validate)
├── job         run · list · show · stop · dispatch · logs · status
├── data        upload · download · list · show · delete · move · stat · logs · encrypt · decrypt
├── automation  list · show · create · enable · disable · delete · logs · runs · triggers
├── storage     buckets (list/create/delete/stat) · objects (list/get/put/delete/stat)
├── compute     nodes (list/show/drain/undrain) · datacenters (list/show)
│               allocations (list/show/logs) · hpc (list/status/jobs)
├── policy      list · show · validate · audit · logs · residency
├── budget      summary · list · show · report · logs
├── compliance  status · audit · residency · dta (list/show/validate) · report
├── admin       users (list/create/delete/token) · health · audit · backup · version
├── join
├── chat
└── version
```

---

## 5. Command Reference

---

### 5.1 `abc auth`

#### `abc auth login`

```
$ abc auth login

  ABC-cluster login

  API endpoint [https://api.abc-cluster.io]: https://api.abc.za-site.example
  Access token: ••••••••••••••••••••••••••••••••

  ✓ Authenticated as admin@za-site.example
  ✓ Default workspace: ws-za-01 (ZA Genomics Lab)
  ✓ Default region:    za-cpt
  ✓ Context saved as:  za-primary
```

#### `abc auth logout`

```
$ abc auth logout

  ✓ Token revoked
  ✓ Context za-primary cleared
```

#### `abc auth whoami`

```
$ abc auth whoami

  User        admin@za-site.example
  Name        ZA Site Admin
  Role        admin
  Plan        pro  (chat enabled)
  Workspace   ws-za-01  (ZA Genomics Lab)
  Region      za-cpt
  Endpoint    https://api.abc.za-site.example
  Context     za-primary
  Token       eyJ...c3Rh  (expires 2025-09-01)
```

#### `abc auth token`

```
$ abc auth token
eyJhbGciOiJIUzI1NiIsInR5cCI6IkpXVCJ9.eyJzdWIiOiJhZG1pbkB6YS1zaXRlLmV4YW1wbGUifQ.c3Rh
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

  API endpoint [https://api.abc-cluster.io]: https://api.abc.za-site.example
  Access token: ••••••••••••••••••••••••••••••••
  Default workspace [ws-za-01]:
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
  active_context                         za-primary
  defaults.output                        table
  defaults.region                        za-cpt
  defaults.workspace                     ws-za-01
  contexts.za-primary.url                https://api.abc.za-site.example
  contexts.za-primary.access_token       eyJ...•••• (masked)
  contexts.ke-primary.url                https://api.abc.ke-site.example
  contexts.ke-primary.access_token       eyJ...•••• (masked)
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

  NAME        ENDPOINT                           WORKSPACE    REGION    ACTIVE
  za-primary  https://api.abc.za-site.example    ws-za-01     za-cpt    *
  ke-primary  https://api.abc.ke-site.example    ws-ke-01     ke-nbi
  mz-primary  https://api.abc.mz-site.example    ws-mz-01     mz-map
  be-primary  https://api.abc.be-site.example    ws-be-01     be-bru
```

#### `abc context show`

```
$ abc context show

  Context     za-primary  (active)
  Endpoint    https://api.abc.za-site.example
  Workspace   ws-za-01
  Region      za-cpt
  Token       eyJ...c3Rh  (expires 2025-09-01)
```

#### `abc context add <n>`

```
$ abc context add ke-primary \
    --url https://api.abc.ke-site.example \
    --token eyJ... \
    --workspace ws-ke-01 \
    --region ke-nbi

  ✓ Context ke-primary added
```

#### `abc context use <n>`

```
$ abc context use ke-primary

  ✓ Active context → ke-primary  (ke-nbi / ws-ke-01)
```

#### `abc context remove <n>`

```
$ abc context remove mz-primary

  Remove context mz-primary? [y/N]: y
  ✓ Context mz-primary removed
```

---

### 5.4 `abc workspace`

#### `abc workspace list`

```
$ abc workspace list

  ID             NAME                   REGION    MEMBERS  PIPELINES  CREATED
  ws-za-01       ZA Genomics Lab        za-cpt    12       48         2024-01-15
  ws-ke-01       KE Surveillance Lab    ke-nbi    6        19         2024-03-02
  ws-mz-01       MZ Research Centre     mz-map    4        11         2024-04-10
  ws-be-01       BE Research Institute  be-bru    8        27         2024-02-20
  ws-consortium  Consortium Project     za-cpt    22       83         2024-01-15
```

#### `abc workspace show`

```
$ abc workspace show ws-za-01

  ID          ws-za-01
  Name        ZA Genomics Lab
  Region      za-cpt
  Description Primary genomics research workspace — ZA site
  Members     12
  Pipelines   48
  Runs        1,204
  Created     2024-01-15
  Owner       admin@za-site.example
```

#### `abc workspace create <n>`

```
$ abc workspace create "ZA National Institute" \
    --description "National public health genomics workspace" \
    --region za-jhb

  ✓ Workspace created
  ID    ws-za-02
```

#### `abc workspace use <id>`

```
$ abc workspace use ws-consortium

  ✓ Active workspace → ws-consortium  (Consortium Project / za-cpt)
```

#### `abc workspace members list`

```
$ abc workspace members list

  USER                        ROLE        ADDED
  admin@za-site.example       admin       2024-01-15
  maintainer@za-site.example  maintainer  2024-01-15
  analyst@za-site.example     launcher    2024-02-01
  viewer1@za-site.example     viewer      2024-03-10
  student1@za-site.example    viewer      2024-05-20
```

#### `abc workspace members add <user>`

```
$ abc workspace members add user1@ke-site.example --role launcher

  ✓ user1@ke-site.example added to ws-za-01 as launcher
```

#### `abc workspace members remove <user>`

```
$ abc workspace members remove student1@za-site.example

  Remove student1@za-site.example from ws-za-01? [y/N]: y
  ✓ Removed
```

---

### 5.5 `abc status`

User-facing dashboard. Scoped to the authenticated user's own workspace and profile. Not an infrastructure health check — that is `abc admin health`.

```
$ abc status

  ╔══════════════════════════════════════════════════════════════════════════╗
  ║  ABC-cluster  ·  admin@za-site.example  ·  ws-za-01  ·  za-cpt         ║
  ║  2024-11-01 09:38:22                                                    ║
  ╚══════════════════════════════════════════════════════════════════════════╝

  PIPELINE RUNS
  Status        Count   Last run
  Running       1       viralrecon-batch-47      started 1h 24m ago
  Succeeded     3       rnaseq-za-nov            finished 2024-10-29
  Failed        1       taxprofiler-mz-003       2024-10-30  →  abc pipeline resume run-g7h8i9
  Cancelled     0       —

  DATA
  Objects       1,204   objects in ws-za-01
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
    --params-file params/viralrecon-batch-47.yaml \
    --work-dir minio://za-cpt/genomics-work/runs \
    --region za-cpt \
    --name viralrecon-batch-47

  Submitting pipeline run...

  ✓ Pipeline run submitted
  ID        run-a1b2c3
  Name      viralrecon-batch-47
  Pipeline  nf-core/viralrecon @ 2.6.0
  Region    za-cpt
  Work dir  minio://za-cpt/genomics-work/runs/run-a1b2c3

  Track progress:
    abc pipeline logs run-a1b2c3 --follow
    abc pipeline show run-a1b2c3
```

#### `abc pipeline list`

```
$ abc pipeline list

  ID          NAME                  PIPELINE              STATUS     REGION   STARTED              DURATION
  run-a1b2c3  viralrecon-batch-47   nf-core/viralrecon    RUNNING    za-cpt   2024-11-01 08:14     1h 22m
  run-d4e5f6  bactmap-ke-batch-12   nf-core/bactmap       SUCCEEDED  ke-nbi   2024-10-31 14:00     3h 07m
  run-g7h8i9  taxprofiler-mz-003    nf-core/taxprofiler   FAILED     mz-map   2024-10-30 09:30     0h 41m
  run-j1k2l3  rnaseq-za-nov         nf-core/rnaseq        SUCCEEDED  za-cpt   2024-10-29 11:15     5h 52m
  run-m4n5o6  viralrecon-batch-46   nf-core/viralrecon    SUCCEEDED  za-cpt   2024-10-28 08:00     2h 18m
```

#### `abc pipeline show <id>`

```
$ abc pipeline show run-a1b2c3

  ID          run-a1b2c3
  Name        viralrecon-batch-47
  Pipeline    nf-core/viralrecon
  Revision    2.6.0
  Profile     test,singularity
  Status      RUNNING
  Region      za-cpt
  Datacenter  za-cpt-dc1  (ZA HPC Primary)
  Work dir    minio://za-cpt/genomics-work/runs/run-a1b2c3
  Started     2024-11-01 08:14:32
  Duration    1h 22m 14s
  Submitted   admin@za-site.example

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

  Cancel run-a1b2c3 (viralrecon-batch-47)? [y/N]: y
  ✓ Cancellation requested
  ✓ 12 running allocations signalled
```

#### `abc pipeline resume <id>`

```
$ abc pipeline resume run-g7h8i9

  Resuming run-g7h8i9 (taxprofiler-mz-003)...

  ✓ Resumed as run-p9q0r1
  ID        run-p9q0r1
  Resuming  run-g7h8i9
  Cached    14 tasks reused from previous run
```

#### `abc pipeline delete <id>`

```
$ abc pipeline delete run-j1k2l3

  Delete run-j1k2l3 (rnaseq-za-nov)? Work directory will NOT be deleted. [y/N]: y
  ✓ Run record deleted
```

#### `abc pipeline logs <id>`

```
$ abc pipeline logs run-a1b2c3 --follow

  [08:14:32] executor   > nf-nomad
  [08:14:33] executor   > Submitting job viralrecon-a1b2c3-fastqc-0 to za-cpt
  [08:14:35] process    > VIRALRECON:FASTQC (ZA-2024-001)   [  0%] 0 of 48
  [08:15:01] process    > VIRALRECON:FASTQC (ZA-2024-001)   [ 12%] 6 of 48
  [08:16:44] process    > VIRALRECON:FASTQC (ZA-2024-012)   [ 25%] 12 of 48
  [08:22:10] process    > VIRALRECON:FASTQC                  [100%] 48 of 48 ✓
  [08:22:11] process    > VIRALRECON:TRIM_GALORE (ZA-2024-001)  [  0%] 0 of 48
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
    --params-file params/viralrecon-batch-47.yaml

  Validating params/viralrecon-batch-47.yaml against nf-core/viralrecon schema...

  PARAMETER    STATUS   VALUE
  input        ✓ OK     samplesheets/batch-47.csv
  platform     ✓ OK     illumina
  genome       ✓ OK     MN908947.3
  primer_set   ✓ OK     artic
  outdir       ✓ OK     minio://za-cpt/genomics-results/batch-47

  ✓ All parameters valid
```

---

### 5.7 `abc job`

#### `abc job run <script>`

```
$ abc job run scripts/custom-analysis.sh --submit --region za-cpt

  Generating Nomad HCL from scripts/custom-analysis.sh...
  Submitting to Nomad (za-cpt)...

  ✓ Job submitted
  Nomad job ID   custom-analysis
  Evaluation ID  b3c4d5e6-f789-0abc-def1-234567890abc
```

#### `abc job list`

```
$ abc job list

  NOMAD JOB ID           STATUS     REGION   DATACENTERS    SUBMITTED            DURATION
  viralrecon-batch-47    running    za-cpt   za-cpt-dc1     2024-11-01 08:14     1h 24m
  bactmap-ke-batch-12    complete   ke-nbi   ke-nbi-dc1     2024-10-31 14:00     3h 07m
  custom-analysis        complete   za-cpt   za-cpt-dc1     2024-10-30 16:45     0h 12m
  taxprofiler-mz-003     dead       mz-map   mz-map-dc1     2024-10-30 09:30     0h 41m
```

#### `abc job show <id>`

```
$ abc job show viralrecon-batch-47

  Nomad Job ID   viralrecon-batch-47
  Type           batch
  Status         running
  Region         za-cpt
  Datacenter     za-cpt-dc1  (ZA HPC Primary)
  Submitted      2024-11-01 08:14:32
  Duration       1h 24m 08s

  TASK GROUPS
  GROUP         DESIRED  RUNNING  SUCCEEDED  FAILED
  fastqc        48       0        48         0
  trim_galore   48       12       36         0
  bowtie2       0        0        0          0

  COST (ZAR)
  Compute   R 44.80
  Storage   R  3.12
  Total     R 47.92  (estimated)
```

#### `abc job stop <id>`

```
$ abc job stop taxprofiler-mz-003

  Stop job taxprofiler-mz-003? [y/N]: y
  ✓ Stop signal sent
  ✓ Job deregistered from Nomad
```

#### `abc job dispatch <id>`

```
$ abc job dispatch viralrecon-parameterized \
    --meta sample=ZA-2024-055 \
    --meta batch=48

  ✓ Dispatched
  Nomad job ID   viralrecon-parameterized/dispatch-1730450123-a1b2c3
  Evaluation ID  c4d5e6f7-a8b9-0cde-f123-456789012bcd
```

#### `abc job logs <id>`

```
$ abc job logs bactmap-ke-batch-12 --task main

  [14:00:01] Starting bactmap pipeline
  [14:00:03] Sample KE-2024-047: FastQC started
  [14:02:18] Sample KE-2024-047: FastQC complete
  [14:08:44] Sample KE-2024-047: BWA-MEM alignment started
  ...
  [17:07:31] Pipeline complete. Results at minio://ke-nbi/ke-results/bactmap-ke-batch-12
```

#### `abc job status <id>`

```
$ abc job status viralrecon-batch-47

  viralrecon-batch-47  running  za-cpt  allocs: 12 running / 48 succeeded / 0 failed
```

---

### 5.8 `abc data`

#### `abc data upload <path>`

```
$ abc data upload ./samplesheets/batch-48.csv \
    --region za-cpt \
    --label data-class=samplesheet \
    --label project=consortium

  Uploading batch-48.csv (4.2 KB) → minio://za-cpt/ws-za-01/...

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

#### `abc data list [prefix]`

```
$ abc data list samplesheets/

  ID          NAME                          SIZE     REGION   CLASS          UPLOADED
  ds-001abc   samplesheets/batch-47.csv     3.9 KB   za-cpt   samplesheet    2024-10-28
  ds-002def   samplesheets/batch-48.csv     4.2 KB   za-cpt   samplesheet    2024-11-01
  ds-003ghi   samplesheets/ke-batch-12.csv  2.1 KB   ke-nbi   samplesheet    2024-10-31
```

#### `abc data show <id>`

```
$ abc data show ds-001abc

  ID              ds-001abc
  Name            samplesheets/batch-47.csv
  Size            3.9 KB
  Checksum        sha256:7b2e...f401
  Region          za-cpt
  Datacenter      za-cpt-dc1  (ZA HPC Primary)
  Jurisdiction    POPIA
  Data class      samplesheet
  Project         consortium
  Uploaded by     admin@za-site.example
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
  Track with: abc data stat ds-001abc
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

Full access and event log for a data object — who accessed it, when, from where, and what decision was made. Intended for Data Managers and compliance audits.

```
$ abc data logs ds-001abc

  TIMESTAMP             USER                        EVENT           REGION   DECISION  DETAIL
  2024-10-28 07:41:03   admin@za-site.example       upload          za-cpt   —         Created, 3.9 KB
  2024-10-28 09:00:12   admin@za-site.example       read            za-cpt   —         pipeline run-a1b2c3
  2024-10-29 14:22:08   analyst@za-site.example     read            za-cpt   —         manual download
  2024-11-01 06:44:17   user1@ke-site.example       read            ke-nbi   ALLOW     pipeline run-d4e5f6 (DTA dta-za-ke-001)
  2024-11-02 09:15:44   admin@za-site.example       transfer        be-bru   ALLOW     adequate country
  2024-11-02 09:15:44   system                      residency-check be-bru   PASS      POLICY-02 satisfied

$ abc data logs ds-001abc --follow
  (streams new events in real time)
```

Flags:

| Flag | Description |
|---|---|
| `--follow` / `-f` | Stream new events as they occur |
| `--from` | ISO 8601 start time |
| `--to` | ISO 8601 end time |
| `--event` | Filter: `upload`, `download`, `transfer`, `read`, `delete`, `residency-check` |
| `--user` | Filter by user |

#### `abc data encrypt <path>`

```
$ abc data encrypt ./fastq/ZA-2024-001_R1.fastq.gz \
    --crypt-password "••••••••" --crypt-salt "••••••••"

  ✓ Encrypted → ./fastq/ZA-2024-001_R1.fastq.gz.encrypted
  Size before   2.1 GB
  Size after    2.1 GB  (rclone crypt, AES-256 CTR)
```

#### `abc data decrypt <path>`

```
$ abc data decrypt ./fastq/ZA-2024-001_R1.fastq.gz.encrypted \
    --crypt-password "••••••••" --crypt-salt "••••••••" \
    --output ./fastq/ZA-2024-001_R1.fastq.gz

  ✓ Decrypted → ./fastq/ZA-2024-001_R1.fastq.gz
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

  ID          NAME                       TYPE       STATUS    LAST FIRED            NEXT RUN
  auto-001    viralrecon-weekly-za       schedule   active    2024-10-28 06:00      2024-11-04 06:00
  auto-002    bactmap-on-upload-ke       trigger    active    2024-10-31 14:00      on event
  auto-003    consortium-full-dag        dag        active    2024-10-29 08:00      manual
  auto-004    taxprofiler-monthly-mz     schedule   disabled  2024-09-30 06:00      —
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
  Schedule      0 6 * * MON  (every Monday at 06:00)
  Params file   minio://za-cpt/genomics-config/viralrecon-weekly.yaml
  Work dir      minio://za-cpt/genomics-work/scheduled
  Created by    admin@za-site.example
  Created       2024-01-20
  Last fired    2024-10-28 06:00  → run-m4n5o6  (SUCCEEDED)
  Next run      2024-11-04 06:00
  Runs total    42  (41 succeeded, 1 failed)
```

#### `abc automation create`

```
$ abc automation create \
    --name "bactmap-on-upload-mz" \
    --type trigger \
    --event data.upload \
    --filter "data-class=raw-sequence,region=mz-map" \
    --pipeline nf-core/bactmap \
    --region mz-map \
    --params-file minio://mz-map/mz-config/bactmap.yaml

  ✓ Automation created
  ID    auto-005
```

Flags:

| Flag | Description |
|---|---|
| `--name` | Human-readable name |
| `--type` | `schedule`, `trigger`, or `dag` |
| `--pipeline` | Pipeline to run (schedule and trigger types) |
| `--job` | Job script to run instead of a pipeline |
| `--region` | Nomad region for executions |
| `--params-file` | Parameters file path (MinIO path or local) |
| `--schedule` | Cron expression (schedule type) |
| `--event` | Event type (trigger type): `data.upload`, `pipeline.succeeded`, `pipeline.failed`, `webhook` |
| `--filter` | Key=value filters on the trigger event (repeatable) |
| `--dag-file` | Path to DAG definition file (dag type) |
| `--disabled` | Create in disabled state |

#### `abc automation enable <id>`

```
$ abc automation enable auto-004

  ✓ auto-004 (taxprofiler-monthly-mz) enabled
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

  TIMESTAMP             EVENT                 DETAIL                         OUTCOME
  2024-10-31 13:58:44   trigger:data.upload   ds-003ghi uploaded (ke-nbi)    matched filter
  2024-10-31 13:58:45   pipeline:submit       run-d4e5f6  nf-core/bactmap    submitted
  2024-10-31 17:07:31   pipeline:complete     run-d4e5f6                     SUCCEEDED
  2024-10-30 08:11:02   trigger:data.upload   ds-008xyz uploaded (za-cpt)    no match (region filter)
  2024-10-29 11:00:01   trigger:data.upload   ds-007stu uploaded (ke-nbi)    matched filter
  2024-10-29 11:00:02   pipeline:submit       run-j1k2l3  nf-core/bactmap    submitted
  2024-10-29 14:55:18   pipeline:complete     run-j1k2l3                     SUCCEEDED

$ abc automation logs auto-002 --follow
  (streams new events as they occur)
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

  RUN ID      NAME                  STATUS     REGION   STARTED              DURATION
  run-m4n5o6  viralrecon-batch-46   SUCCEEDED  za-cpt   2024-10-28 06:00     2h 18m
  run-b5c6d7  viralrecon-batch-45   SUCCEEDED  za-cpt   2024-10-21 06:00     2h 04m
  run-e8f9a0  viralrecon-batch-44   SUCCEEDED  za-cpt   2024-10-14 06:00     2h 31m
  run-q1r2s3  viralrecon-batch-43   FAILED     za-cpt   2024-10-07 06:00     0h 22m
  ...  (42 total)
```

#### `abc automation triggers`

List all available trigger event types and their filterable fields.

```
$ abc automation triggers

  EVENT                    DESCRIPTION                          FILTER FIELDS
  data.upload              A data object was uploaded           region, data-class, workspace, label.*
  data.delete              A data object was deleted            region, data-class
  pipeline.succeeded       A pipeline run completed             pipeline, region, workspace
  pipeline.failed          A pipeline run failed                pipeline, region, workspace
  job.complete             A Nomad batch job completed          region, datacenter
  job.failed               A Nomad batch job failed             region, datacenter
  compliance.dta.expiring  A DTA is within 30 days of expiry   dta-id, from-jurisdiction, to-jurisdiction
  webhook                  An external HTTP POST to the hook    header.*, body.*
```

---

### 5.10 `abc storage`

#### `abc storage buckets list`

```
$ abc storage buckets list --region za-cpt

  BUCKET                REGION   OBJECTS    SIZE      VERSIONING  LOCK   CREATED
  genomics-work         za-cpt   184,221    14.2 TB   off         off    2024-01-15
  genomics-results      za-cpt   92,048      8.7 TB   on          off    2024-01-15
  genomics-raw-seq      za-cpt   12,400     41.8 TB   on          on     2024-01-15
  ws-za-01              za-cpt   4,102       0.9 TB   off         off    2024-01-15
  abc-audit-logs        za-cpt   2,901,440   0.2 TB   on          on     2024-01-15
```

#### `abc storage buckets create <n>`

```
$ abc storage buckets create genomics-archive-2024 \
    --region za-cpt \
    --versioning \
    --lock \
    --retention-days 1095

  ✓ Bucket created
  Name         genomics-archive-2024
  Region       za-cpt
  Versioning   enabled
  Object lock  enabled  (retention: 1095 days WORM)
```

#### `abc storage buckets stat <n>`

```
$ abc storage buckets stat genomics-raw-seq

  Name            genomics-raw-seq
  Region          za-cpt
  Datacenter      za-cpt-dc1  (ZA HPC Primary)
  Objects         12,400
  Size            41.8 TB
  Versioning      enabled
  Object lock     enabled  (WORM — 1095 days)
  Jurisdiction    POPIA
  Created         2024-01-15
```

#### `abc storage objects list <bucket> [prefix]`

```
$ abc storage objects list genomics-raw-seq ZA-2024/

  KEY                              SIZE     MODIFIED              CLASS
  ZA-2024/ZA-2024-001_R1.fastq.gz 2.1 GB   2024-10-28 07:41     STANDARD
  ZA-2024/ZA-2024-001_R2.fastq.gz 2.0 GB   2024-10-28 07:41     STANDARD
  ZA-2024/ZA-2024-002_R1.fastq.gz 1.9 GB   2024-10-28 08:02     STANDARD
  ZA-2024/ZA-2024-002_R2.fastq.gz 1.8 GB   2024-10-28 08:02     STANDARD
```

#### `abc storage objects stat <bucket> <key>`

```
$ abc storage objects stat genomics-raw-seq ZA-2024/ZA-2024-001_R1.fastq.gz

  Key           ZA-2024/ZA-2024-001_R1.fastq.gz
  Bucket        genomics-raw-seq
  Size          2.1 GB
  ETag          "a3f7b2c1d4e5f6a7b8c9d0e1f2a3b4c5"
  Content type  application/gzip
  Version ID    3a7f2c1d-4e5f-6a7b-8c9d-0e1f2a3b4c5d
  Modified      2024-10-28 07:41:03
  Storage class STANDARD
  Legal hold    ON  (POLICY-01 raw sequence lock)
  Region        za-cpt
```

---

### 5.11 `abc compute`

#### `abc compute nodes list`

```
$ abc compute nodes list --region za-cpt

  NODE ID      DATACENTER    STATUS    DRIVER        CPU        MEM        ALLOCS
  za-node-001  za-cpt-dc1    ready     hpc-bridge    64 cores   256 GB     4 / 4
  za-node-007  za-cpt-dc1    ready     hpc-bridge    64 cores   256 GB     3 / 4
  za-node-014  za-cpt-dc1    ready     hpc-bridge    64 cores   256 GB     4 / 4
  za-node-101  za-cpt-dc2    ready     exec2         32 cores   128 GB     1 / 8
  za-node-102  za-cpt-dc2    ready     exec2         32 cores   128 GB     0 / 8
  za-node-103  za-cpt-dc2    draining  exec2         32 cores   128 GB     1 / 8
```

#### `abc compute nodes show <node-id>`

```
$ abc compute nodes show za-node-014

  Node ID       za-node-014
  Datacenter    za-cpt-dc1  (ZA HPC Primary)
  Region        za-cpt
  Status        ready
  Driver        hpc-bridge (PBS Pro backend)
  OS            CentOS 7.9
  CPU           64 cores  (Intel Xeon Gold 6148)
  Memory        256 GB
  Disk          2 TB  (Lustre /mnt/lustre/genomics)
  Active allocs 4 / 4

  ACTIVE ALLOCATIONS
  ALLOC ID          JOB                    TASK           CPU     MEM      STARTED
  a1b2c3d4-e5f6     viralrecon-batch-47    trim_galore    8       32 GB    08:22:11
  b2c3d4e5-f6a7     viralrecon-batch-47    trim_galore    8       32 GB    08:22:14
  c3d4e5f6-a7b8     viralrecon-batch-47    trim_galore    8       32 GB    08:22:09
  d4e5f6a7-b8c9     rnaseq-za-nov          align          40      160 GB   10:05:33
```

#### `abc compute nodes drain <node-id>`

```
$ abc compute nodes drain za-node-103 --deadline 1h

  ✓ Node za-node-103 set to draining
  ✓ New allocations will not be placed
  ✓ 1 existing allocation will migrate within 1h
```

#### `abc compute nodes undrain <node-id>`

```
$ abc compute nodes undrain za-node-103

  ✓ Node za-node-103 drain cancelled
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
  Name           ZA HPC Primary
  Region         za-cpt
  Jurisdiction   POPIA
  Scheduler      PBS Pro
  Nodes          312  (308 ready, 3 draining, 1 down)
  Active allocs  1,204
  MinIO          https://minio.za-cpt-dc1.example  (57.9 TB used / 200 TB)
  Lustre         /mnt/lustre/genomics  (90-day purge policy)
  Tailscale      za-cpt-dc1.cluster.ts.net
```

#### `abc compute allocations list`

```
$ abc compute allocations list --job viralrecon-batch-47

  ALLOC ID          NODE           TASK GROUP    STATUS     STARTED     DURATION
  a1b2c3d4-e5f6     za-node-014    trim_galore   running    08:22:11    1h 14m
  a2c3d4e5-f6a7     za-node-022    trim_galore   running    08:22:14    1h 14m
  b1c2d3e4-f5a6     za-node-001    fastqc        complete   08:14:35    0h 07m
```

#### `abc compute hpc list`

```
$ abc compute hpc list

  BACKEND           REGION   SCHEDULER  STATUS    NODES  QUEUE  DRIVER VERSION
  za-hpc-primary    za-cpt   PBS Pro    healthy   312    47     hpc-bridge v0.1.0
  za-hpc-secondary  za-cpt   SLURM      healthy   73     12     hpc-bridge v0.1.0
  ke-hpc-primary    ke-nbi   SLURM      healthy   48     8      hpc-bridge v0.1.0
  mz-hpc-primary    mz-map   SLURM      degraded  12     0      hpc-bridge v0.1.0
```

#### `abc compute hpc status <backend>`

```
$ abc compute hpc status za-hpc-primary

  Backend        za-hpc-primary
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
$ abc compute hpc jobs za-hpc-primary

  PBS JOB ID  NOMAD JOB              TASK GROUP    STATUS  NODES  WALLTIME   STARTED
  7841234     viralrecon-batch-47    trim_galore   R       1      02:00:00   08:22:11
  7841235     viralrecon-batch-47    trim_galore   R       1      02:00:00   08:22:14
  7841289     rnaseq-za-nov          align         R       1      08:00:00   10:05:33
  7841301     custom-analysis        main          Q       4      02:00:00   —
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
$ abc policy validate params/viralrecon-batch-47.yaml --pipeline nf-core/viralrecon

  Evaluating against 10 active policies...

  POLICY    NAME                              DECISION  NOTE
  pol-001   Raw sequence absolute residence   ALLOW     Input data in za-cpt ✓
  pol-002   Controlled cross-border transfer  ALLOW     No cross-border transfer
  pol-003   DTA structural requirements       ALLOW     Not applicable
  pol-004   Derived data controlled transfer  ALLOW     Output stays in za-cpt
  pol-005   Aggregated data transfer          ALLOW     —
  pol-006   Retention limits                  ALLOW     Work dir in za-cpt ✓
  pol-007   Audit completeness                ALLOW     Audit hooks injected ✓
  pol-008   Access control and consent        ALLOW     Workspace ws-za-01 ✓
  pol-009   Breach notification readiness     ALLOW     —
  pol-010   Processor agreement requirements  ALLOW     —

  ✓ All policies passed. Job may proceed.
```

#### `abc policy audit`

Structured compliance decision records. Each entry represents a policy decision tied to a specific user action, run, or data event. Intended for compliance officers, ethics committees, and formal reporting. The audit log is append-only and tamper-evident.

```
$ abc policy audit --from 2024-11-01 --action deny

  TIMESTAMP             POLICY    DECISION  RUN / JOB           NOTE
  2024-11-01 07:12:03   pol-001   DENY      run-z9y8x7          Raw sequence transfer to ke-nbi blocked
  2024-11-01 06:44:17   pol-002   DENY      bactmap-ke-direct   No DTA registered for ke-nbi → mz-map
```

```
$ abc policy audit --from 2024-10-01 --jurisdiction popia

  TIMESTAMP             POLICY    DECISION  USER                        ACTION
  2024-11-01 08:14:32   pol-001   ALLOW     admin@za-site.example       pipeline.submit  run-a1b2c3
  2024-11-01 07:12:03   pol-001   DENY      admin@za-site.example       data.move        ds-009xyz → ke-nbi
  2024-10-31 14:00:01   pol-006   ALLOW     user1@ke-site.example       pipeline.submit  run-d4e5f6
  2024-10-30 09:30:11   pol-002   DENY      —                           pipeline.submit  run-g7h8i9
```

Flags:

| Flag | Description |
|---|---|
| `--from` | ISO 8601 start datetime |
| `--to` | ISO 8601 end datetime |
| `--policy` | Filter to a specific policy ID |
| `--action` | Filter by decision: `allow`, `deny`, `mutate` |
| `--jurisdiction` | Filter: `popia`, `kenya-dpa`, `gdpr`, `mozambique` |
| `--user` | Filter by user |
| `--run-id` | Filter by pipeline or job ID |
| `--output-file` | Write to file |

#### `abc policy logs`

Raw OPA evaluation stream — every policy evaluation request with its full input context, matched rule, decision, and latency. Distinct from `audit` in both audience and purpose.

| | `abc policy audit` | `abc policy logs` |
|---|---|---|
| **Audience** | Compliance officers, ethics committees | Platform engineers, policy authors |
| **Content** | One record per actionable user event | One record per OPA query, including internal checks |
| **Volume** | Low | High |
| **Retention** | Append-only, tamper-evident | Rotated, not part of the audit trail |
| **Use case** | Formal compliance reporting, breach evidence | Debugging policy logic, rule coverage, performance tuning |

```
$ abc policy logs --policy pol-001 --limit 5

  TIMESTAMP             POLICY    LATENCY  INPUT SUMMARY                        RESULT
  2024-11-01 09:36:01   pol-001   1.2ms    data_class=raw-sequence dest=ke-nbi  DENY
  2024-11-01 09:35:58   pol-001   0.9ms    data_class=samplesheet dest=be-bru   ALLOW
  2024-11-01 09:35:55   pol-001   0.8ms    data_class=raw-sequence dest=za-cpt  ALLOW
  2024-11-01 09:35:52   pol-001   1.1ms    data_class=derived dest=ke-nbi       ALLOW
  2024-11-01 09:35:49   pol-001   0.9ms    data_class=samplesheet dest=za-cpt   ALLOW
```

```
$ abc policy logs --policy pol-001 --result deny

  TIMESTAMP             POLICY    LATENCY  RULE MATCHED                           INPUT CONTEXT
  2024-11-01 09:36:01   pol-001   1.2ms    deny_reason: raw seq outside za-*      job=run-z9y8x7, dest=ke-nbi
  2024-10-30 07:12:03   pol-001   1.4ms    deny_reason: raw seq outside za-*      job=run-q1r2s3, dest=be-bru
```

```
$ abc policy logs --follow

  (streams OPA evaluation events in real time across all policies)
  2024-11-01 09:38:04   pol-008   0.7ms    workspace=ws-za-01 user=student1@za-site.example  ALLOW
  2024-11-01 09:38:04   pol-001   0.9ms    data_class=samplesheet dest=ke-nbi                ALLOW
  2024-11-01 09:38:05   pol-002   1.1ms    data_class=derived dest=ke-nbi dta=dta-za-ke-001  ALLOW
  ...
```

Flags:

| Flag | Description |
|---|---|
| `--follow` / `-f` | Stream OPA evaluation events in real time |
| `--policy` | Filter to a specific policy ID |
| `--result` | Filter by result: `allow`, `deny` |
| `--from` | ISO 8601 start time |
| `--to` | ISO 8601 end time |
| `--limit` | Max results (default: 50) |
| `--verbose` | Include the full OPA input document per evaluation |

#### `abc policy residency <data-id>`

```
$ abc policy residency ds-001abc

  Data ID        ds-001abc
  Name           samplesheets/batch-47.csv
  Current loc    be-bru
  Data class     samplesheet

  POLICY    NAME                              RESULT  NOTE
  pol-001   Raw sequence absolute residence   N/A     Not raw sequence
  pol-002   Controlled cross-border transfer  PASS    Transfer to Belgium (adequate) logged
  pol-004   Derived data controlled transfer  N/A     Not derived data

  Overall: COMPLIANT
```

---

### 5.13 `abc budget`

#### `abc budget summary`

```
$ abc budget summary --from 30d

  WORKSPACE    ws-za-01  (ZA Genomics Lab)
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
  DATACENTER      COMPUTE    STORAGE   TOTAL
  za-cpt-dc1      R 3,800    R 310     R 4,110
  za-cpt-dc2      R 1,012    R 131     R 1,143
```

#### `abc budget list`

```
$ abc budget list --run-id run-a1b2c3

  ENTRY ID   RUN          SAMPLE        TYPE      AMOUNT (ZAR)  TIMESTAMP
  bgt-0001   run-a1b2c3   ZA-2024-001   compute   R 0.92        2024-11-01 08:22
  bgt-0002   run-a1b2c3   ZA-2024-001   storage   R 0.06        2024-11-01 08:22
  bgt-0003   run-a1b2c3   ZA-2024-002   compute   R 0.89        2024-11-01 08:23
  bgt-0004   run-a1b2c3   ZA-2024-002   storage   R 0.06        2024-11-01 08:23
  ...
  TOTAL                                            R 47.92
```

#### `abc budget show <run-id>`

```
$ abc budget show run-a1b2c3

  Run ID       run-a1b2c3  (viralrecon-batch-47)
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

  TigerBeetle account   tb-acc-ws-za-01
  Ledger entries        bgt-0001 → bgt-0096  (96 entries)
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

Raw TigerBeetle double-entry ledger events for the current workspace. Intended for accountants and grant financial reporting — maps every compute event to a specific ledger entry.

```
$ abc budget logs --run-id run-a1b2c3

  ENTRY ID    TIMESTAMP             RUN          SAMPLE        TYPE      AMOUNT (ZAR)  ACCOUNT
  bgt-0001    2024-11-01 08:22:11   run-a1b2c3   ZA-2024-001   compute   R 0.18        tb-acc-ws-za-01
  bgt-0002    2024-11-01 08:22:11   run-a1b2c3   ZA-2024-001   compute   R 0.18        tb-acc-ws-za-01
  bgt-0003    2024-11-01 08:22:11   run-a1b2c3   ZA-2024-001   storage   R 0.06        tb-acc-ws-za-01
  bgt-0004    2024-11-01 08:22:14   run-a1b2c3   ZA-2024-002   compute   R 0.17        tb-acc-ws-za-01
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

  JURISDICTION   POLICIES  PASS  FAIL  WARN
  POPIA          7         7     0     0    ✓
  Kenya DPA      3         2     0     1    ⚠
  GDPR           3         3     0     0    ✓
  Mozambique     2         1     0     1    ⚠

  WARNINGS
  Kenya DPA     dta-za-ke-001 expires in 14 days — renewal required
  Mozambique    No DTA registered for ke-nbi → mz-map transfer (pending review)

  DATA RESIDENCY
  Objects with residency issues      0
  Raw sequence outside za-*          0
  Transfers without DTA (blocked)    2  (see: abc policy audit --action deny)

  RETENTION
  Objects approaching expiry (30d)   14
  Objects past expiry                0
```

#### `abc compliance audit`

```
$ abc compliance audit --jurisdiction kenya-dpa --from 2024-10-01

  TIMESTAMP             EVENT TYPE  DATA ID     FROM     TO       USER                   DECISION
  2024-11-01 06:44:17   transfer    ds-004jkl   za-cpt   ke-nbi   user1@ke-site.example  ALLOW  (DTA dta-za-ke-001)
  2024-10-31 14:00:01   access      ds-005mno   —        ke-nbi   student1@ke-site.example  ALLOW
  2024-10-30 11:22:08   transfer    ds-006pqr   ke-nbi   mz-map   —                      DENY   (no DTA registered)
  2024-10-29 09:14:55   deletion    ds-007stu   —        ke-nbi   user1@ke-site.example  ALLOW  (retention satisfied)
```

#### `abc compliance residency <data-id>`

```
$ abc compliance residency ds-004jkl

  Data ID     ds-004jkl
  Name        results/ke-batch-12/consensus.fasta
  Class       derived-sequence

  RESIDENCY AUDIT TRAIL
  TIMESTAMP             LOCATION  JURISDICTION  DTA            EVENT
  2024-10-31 14:00:01   ke-nbi    Kenya DPA     —              Created (derived in ke-nbi)
  2024-11-01 06:44:17   za-cpt    POPIA         dta-za-ke-001  Transfer: ke-nbi → za-cpt
  (current)             za-cpt    POPIA

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
  Parties         ZA Site  ←→  KE Site
  From            za-cpt  (POPIA)
  To              ke-nbi  (Kenya Data Protection Act)
  Status          active
  Valid from      2024-01-15
  Expires         2024-11-15  ⚠ expires in 14 days
  Reference       Consortium Data Sharing Agreement §4.2

  COVERED DATA CATEGORIES
  CATEGORY           TRANSFER TYPE    CONDITIONS
  derived-sequence   bi-directional   Pseudonymised, analysis purpose only
  samplesheet        za → ke only     Project metadata, no clinical data
  aggregated         bi-directional   No restriction

  EXCLUDED CATEGORIES
  raw-sequence       Absolute prohibition (POLICY-01)
```

#### `abc compliance dta validate <data-id> <destination-region>`

```
$ abc compliance dta validate ds-004jkl ke-nbi

  Checking: ds-004jkl (derived-sequence) → ke-nbi  (Kenya DPA)

  CHECK                  RESULT  NOTE
  Data class permitted   PASS    derived-sequence covered by dta-za-ke-001
  DTA validity           PASS    active (expires 2024-11-15)
  Transfer direction     PASS    bi-directional permitted
  POLICY-02 enforcement  PASS    DTA on record
  POLICY-07 audit ready  PASS    Audit hook will be injected

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

  USER ID   EMAIL                        NAME               ROLE    PLAN   LAST SEEN
  usr-001   admin@za-site.example        ZA Admin           admin   pro    09:35:58
  usr-002   maintainer@za-site.example   ZA Maintainer      admin   pro    2024-10-31
  usr-003   analyst@za-site.example      ZA Analyst         user    pro    2024-11-01
  usr-004   user1@ke-site.example        KE Researcher      user    basic  2024-10-31
  usr-005   student1@ke-site.example     KE Student         user    basic  2024-10-30
  usr-006   compliance@za-site.example   ZA Compliance Mgr  user    pro    2024-10-28
```

#### `abc admin users create`

```
$ abc admin users create \
    --email user1@mz-site.example \
    --name "MZ Researcher" \
    --role user

  ✓ User created
  ID      usr-007
  Email   user1@mz-site.example
  Token   abc-token-xxxx...  (shown once — save it now)
```

#### `abc admin users token <id>`

```
$ abc admin users token usr-005 \
    --expires 90d \
    --description "CI pipeline token"

  ✓ Token generated for usr-005 (student1@ke-site.example)
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
  MinIO         healthy   ke-nbi    12.1 TB / 80 TB
  MinIO         degraded  mz-map    Disk 94% — action required
  OPA           healthy   —         v0.68.0, 10 policies loaded
  TigerBeetle   healthy   —         3/3 replicas, 1.2M entries
  Tailscale     healthy   —         5 nodes connected
  hpc-bridge    healthy   za-cpt    PBS Pro za-hpc-primary reachable
  hpc-bridge    healthy   za-cpt    SLURM za-hpc-secondary reachable
  hpc-bridge    degraded  mz-map    SLURM mz-hpc-primary last heartbeat 4m ago

  Overall: DEGRADED  (2 warnings — see above)
```

#### `abc admin audit`

```
$ abc admin audit --from 2024-11-01

  TIMESTAMP             USER                        ACTION           RESOURCE       OUTCOME
  2024-11-01 09:35:58   admin@za-site.example       auth.login       —              success
  2024-11-01 08:14:32   admin@za-site.example       pipeline.run     run-a1b2c3     success
  2024-11-01 07:12:03   —                           policy.deny      run-z9y8x7     pol-001 deny
  2024-11-01 06:44:17   user1@ke-site.example       data.move        ds-004jkl      success
  2024-11-01 06:44:17   user1@ke-site.example       compliance.dta   dta-za-ke-001  verified
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
  ABC API          v0.2.0    commit 4f2a1c3  (2024-10-30)
  nf-nomad plugin  v0.5.1    commit 8b3c2d1  (2024-10-15)
  hpc-bridge       v0.1.0    commit 1a2b3c4  (2024-09-20)
  Nomad            v1.9.4    —
  OPA              v0.68.0   —
  TigerBeetle      v0.16.3   —
  MinIO            RELEASE.2024-10-02  —
  Tailscale        v1.74.1   —
```

---

### 5.16 `abc join`

Onboards the current machine into the operator's ABC-cluster. Runs `abc-node-probe` checks automatically, presents a full health report, and — if the operator confirms — registers the node with Nomad, establishes a Tailscale connection, and configures the appropriate task driver.

Requires `admin` or `maintainer` role in the target workspace.

```
$ abc join \
    --datacenter za-cpt-dc2 \
    --region za-cpt \
    --jurisdiction ZA

  ════════════════════════════════════════════════════════════════
  ABC Node Join — Pre-flight Check
  Host: za-node-104.za-cpt-dc2.example  |  Region: za-cpt  |  DC: za-cpt-dc2
  ════════════════════════════════════════════════════════════════

  Running node probe (8 checks)...

  CHECK              STATUS   DETAIL
  CPU                PASS     32 cores  (Intel Xeon Silver 4216)
  Memory             PASS     128 GB RAM available
  Disk               PASS     /scratch  1.8 TB free  (of 2.0 TB)
  Network            PASS     API reachable (https://api.abc.za-site.example, 12ms)
                     PASS     Tailscale reachable (cluster.ts.net)
                     PASS     MinIO reachable (https://minio.za-cpt-dc2.example, 8ms)
  NTP sync           PASS     Offset 0.003s  (limit: 0.250s)
  OS / kernel        PASS     Ubuntu 22.04 LTS  |  kernel 5.15.0-88
  Drivers available  PASS     exec2 available
                     PASS     SLURM detected → hpc-bridge eligible
  SMART disk check   SKIP     /dev/sda — not accessible (unprivileged). Use --privileged to enable.
  Jurisdiction       PASS     ZA declared explicitly (POPIA boundary confirmed)

  ════════════════════════════════════════════════════════════════
  Result: 9 passed  ·  1 skipped  ·  0 failed
  ════════════════════════════════════════════════════════════════

  Node is eligible to join the cluster.

  This will:
    • Generate a Tailscale ephemeral key and connect za-node-104 to cluster.ts.net
    • Register za-node-104 with Nomad (za-cpt / za-cpt-dc2) using exec2 + hpc-bridge
    • Write Nomad client config to /etc/nomad.d/client.hcl
    • Start and enable the nomad systemd service

  Continue? [y/N]: y

  ════════════════════════════════════════════════════════════════
  Joining cluster...
  ════════════════════════════════════════════════════════════════

  ✓ Tailscale ephemeral key issued
  ✓ Tailscale connected  (100.104.12.88 / cluster.ts.net)
  ✓ Nomad client config written to /etc/nomad.d/client.hcl
  ✓ nomad.service started and enabled
  ✓ Node registered with Nomad

  Node ID       za-node-104
  Datacenter    za-cpt-dc2
  Region        za-cpt
  Jurisdiction  ZA  (POPIA)
  Drivers       exec2, hpc-bridge
  Status        ready

  Verify with:
    abc compute nodes show za-node-104
```

The `--probe-only` flag runs checks without committing to join:

```
$ abc join --probe-only --jurisdiction ZA

  Running node probe (8 checks)...

  CHECK              STATUS   DETAIL
  CPU                PASS     32 cores
  Memory             PASS     128 GB
  Disk               PASS     1.8 TB free
  Network            PASS     API, Tailscale, MinIO reachable
  NTP sync           FAIL     Offset 1.847s — exceeds 0.250s limit (TigerBeetle requirement)
  OS / kernel        PASS     Ubuntu 22.04 LTS
  Drivers available  PASS     exec2 available
  SMART disk check   SKIP     unprivileged
  Jurisdiction       PASS     ZA declared

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
| `--jurisdiction` | Jurisdiction code: `ZA`, `KE`, `MZ`, `BE` (**required** — never inferred) |
| `--driver` | Override task driver: `exec2`, `hpc-bridge`, `docker` (auto-detected if omitted) |
| `--scheduler` | HPC scheduler for hpc-bridge: `pbs`, `slurm` (auto-detected if omitted) |
| `--privileged` | Allow SMART disk checks (requires root or disk group membership) |
| `--probe-only` | Run node probe and print results without joining |
| `--skip-tailscale` | Skip Tailscale setup (node must already be on the tailnet) |
| `--dry-run` | Show what would be written and started without making changes |

Exit codes: `0` (joined or probe passed), `1` (probe failed), `2` (usage error), `3` (auth error)

---

### 5.17 `abc chat`

An AI assistant embedded in the CLI, available to users on the **Pro plan and above**. Scoped strictly to the authenticated user's own profile — the assistant has read access to the user's runs, data objects, compliance posture, automations, and budget. It cannot access other users' data.

The assistant answers natural-language questions, helps debug failed runs, explains compliance decisions, and assists with cost analysis. It is read-only and suggests commands rather than taking actions.

```
$ abc chat

  ABC Assistant  ·  admin@za-site.example  ·  ws-za-01
  Pro plan  ·  Scoped to your profile only
  Type 'exit' or Ctrl+D to quit.

  ─────────────────────────────────────────────────────────────────

  You: Why did taxprofiler-mz-003 fail?

  Assistant: Your run run-g7h8i9 (taxprofiler-mz-003) failed on
  2024-10-30 after 41 minutes in the mz-map region. The failure
  occurred in the TAXPROFILER:KRAKEN2_KRAKEN2 process. The task
  exited with code 137 — an OOM kill. The task was allocated 32 GB
  but the Kraken2 PlusPF database (~60 GB uncompressed) requires
  more memory to load.

  To fix this, resume with a higher memory allocation:

    abc pipeline resume run-g7h8i9 \
      --params-file params/taxprofiler-highmem.yaml

  Or re-run on za-cpt-dc1 where nodes have 256 GB available.

  ─────────────────────────────────────────────────────────────────

  You: How much have I spent on viralrecon this month?

  Assistant: In November 2024 (month to date), your nf-core/viralrecon
  runs have cost R 47.92. That is run-a1b2c3 (viralrecon-batch-47),
  still running — estimated final cost R 52–55 based on current
  task progress.

  ─────────────────────────────────────────────────────────────────

  You: Is my data in Mozambique compliant right now?

  Assistant: One compliance warning in mz-map. Data object ds-006pqr
  (results/mz-batch-09/consensus.fasta, derived-sequence) was flagged
  because a transfer from ke-nbi to mz-map was attempted on 2024-10-30
  without a registered DTA. That transfer was blocked by POLICY-02.
  The data remains in ke-nbi. To proceed:

    abc compliance dta show dta-ke-mz-001

  ─────────────────────────────────────────────────────────────────

  You: exit
  Goodbye.
```

Flags:

| Flag | Description |
|---|---|
| `--prompt <text>` | Non-interactive mode — pass a single question, print answer to stdout |
| `--context <id>` | Pre-load a specific run or data object ID into the conversation context |

Non-interactive example for scripting:

```
$ abc chat --prompt "What is the total cost of run-a1b2c3?"

  Run run-a1b2c3 (viralrecon-batch-47) has spent R 45.25 so far,
  estimated final cost R 47.92.
```

Scope refusal:

```
  You: What are analyst's pipeline runs?

  Assistant: I can only access your own profile data. To see shared
  workspace activity:

    abc pipeline list --workspace ws-za-01
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
  API endpoint  https://api.abc.za-site.example  (v0.2.0)
  Context       za-primary

$ abc version --short
0.2.0
```

---

## 6. Exit Codes

| Code | Meaning |
|---|---|
| 0 | Success |
| 1 | General error (API error, resource not found) |
| 2 | Usage error (invalid flags or arguments) |
| 3 | Authentication error |
| 4 | Policy denial (OPA enforcement blocked the operation) |
| 5 | Data residency violation (cross-border transfer blocked) |

---

## 7. Shell Composition Patterns

```bash
# Policy-check then submit
abc policy validate params/viralrecon-batch-47.yaml && \
  abc pipeline run --pipeline nf-core/viralrecon \
    --params-file params/viralrecon-batch-47.yaml

# DTA-check then move
abc compliance dta validate ds-004jkl ke-nbi && \
  abc data move ds-004jkl ke-nbi

# Probe only before committing to join
abc join --probe-only --jurisdiction ZA && \
  abc join --datacenter za-cpt-dc2 --region za-cpt --jurisdiction ZA

# Stream logs for the most recent automation run
RUN_ID=$(abc automation runs auto-001 --limit 1 --output json | jq -r '.[0].id')
abc pipeline logs "$RUN_ID" --follow

# Non-interactive chat in CI
abc chat --prompt "What is the total cost of run-a1b2c3?"

# Debug a policy denial using logs then cross-reference audit
abc policy logs --policy pol-001 --result deny --limit 10
abc policy audit --policy pol-001 --action deny --from 2024-11-01
```

---

## 8. Shell Completion

```
abc completion bash        > /etc/bash_completion.d/abc
abc completion zsh         > ~/.zsh/completions/_abc
abc completion fish        > ~/.config/fish/completions/abc.fish
abc completion powershell
```

Completion resolves live context names, workspace IDs, region names, and resource IDs from the API.

---

## 9. Configuration File Schema

Location: `~/.abc/config.yaml`

```yaml
active_context: za-primary

contexts:
  za-primary:
    url: https://api.abc.za-site.example
    access_token: eyJ...
    workspace: ws-za-01
    region: za-cpt
    output: table

  ke-primary:
    url: https://api.abc.ke-site.example
    access_token: eyJ...
    workspace: ws-ke-01
    region: ke-nbi

  mz-primary:
    url: https://api.abc.mz-site.example
    access_token: eyJ...
    workspace: ws-mz-01
    region: mz-map

  be-primary:
    url: https://api.abc.be-site.example
    access_token: eyJ...
    workspace: ws-be-01
    region: be-bru

defaults:
  output: table
  dry_run: false
```

---

## 10. Persona → Command Mapping

| Persona | Primary commands |
|---|---|
| **Bioinformatician** | `pipeline run/list/logs/resume`, `job run/logs`, `data upload/download/stat`, `status`, `chat` |
| **Graduate Student** | `pipeline run/list/logs`, `data upload`, `auth login`, `status`, `chat` |
| **Data Manager** | `data *`, `storage *`, `compliance residency/dta/audit` |
| **Principal Investigator** | `pipeline list/show`, `budget summary/report`, `compliance status/report`, `status`, `chat` |
| **Accountant** | `budget list/show/report/logs` |
| **Compliance Lawyer** | `compliance *`, `policy audit`, `data logs` |
| **Platform Engineer** | `policy logs`, `admin health`, `compute *`, `join` |
| **Server Manager** | `compute *`, `storage *`, `admin health/backup/version`, `join` |
| **Project Manager** | `workspace *`, `pipeline list`, `budget summary`, `automation list/show`, `status` |
| **Ethics Committee Member** | `compliance status/report`, `policy audit`, `data stat/logs` |
| **Trainer** | `auth login`, `config init`, `pipeline run`, `join --probe-only` |
| **External Collaborator** | `data download`, `pipeline show/logs` (read-only scoped token) |

---

## 11. Prototype Migration Notes

| Prototype command | New location | Changes |
|---|---|---|
| `abc pipeline run` | `abc pipeline run` | Add `--region`, `--datacenter`, `--watch`, `--label` |
| `abc job run <script>` | `abc job run <script>` | Add `--submit`, `--output-file`, `--region` |
| `abc data upload <path>` | `abc data upload <path>` | Add `--region`, `--tag`, `--label` |
| `abc data encrypt <path>` | `abc data encrypt <path>` | Unchanged |
| `abc data decrypt <path>` | `abc data decrypt <path>` | Unchanged |
| All global flags | All global flags | Unchanged |
| All env vars | All env vars | Unchanged |
