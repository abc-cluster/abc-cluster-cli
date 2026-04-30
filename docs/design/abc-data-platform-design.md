# abc data platform design — `abc://` namespace + data lineage index

> **Document status:** Living design document. Last updated 2026-04-30.
> Shareable — this document is self-contained. A reader with no prior context can understand
> the problem, the decisions made, and the implementation roadmap from start to finish.
> **Location:** `analysis/packages/abc-cluster-cli/docs/design/abc-data-platform-design.md`

## Revision history

| Date | Change |
|---|---|
| 2026-04-26 | Initial brainstorm: `abc data move` robustness, `abc://` namespace, XTDB-Lucene index |
| 2026-04-27 | Annotation + provenance layer (W3C PROV); Nextflow lineage integration (Phase 2.5 → Phase 9) |
| 2026-04-27 | `abc data search` full query surface (Phase 1.5 → Phase 6); restic + Garage integration (Phase 5.5) |
| 2026-04-28 | Comparative analysis (iRODS, OpenMetadata, DataHub, Pachyderm, DVC, LakeFS); Tier-2 enrichments |
| 2026-04-28 | Multi-pass scanning pipeline (fd + fclones + rhash + b3sum + s5cmd); BLAKE3 evaluation |
| 2026-04-28 | Self-hosted Storj resilience tier (Phase 5.6 a/b/c); decentralised archival appendix (parked) |
| 2026-04-29 | Executive summary + key design decisions sections; Tool Landscape Survey (Appendix B); Custom Integration Evaluations (Appendix C — CephFS, NATS, iRODS, Globus, Patroni, Tape/HSM, Nextflow lineage) |
| 2026-04-29 | System Architecture §9: Khan/Jurist/data-platform integration map; instrument-watcher (Phase 0.5); Apache Egeria evaluation (Appendix C.9, rejected) |
| 2026-04-30 | Discovery layer UX (`ls`, `find`, `grep`, `stat`, `du`, tab completion); read-policy distribution from Jurist to abc-data-api; computation reuse cache (`:prov/activity-fingerprint`) |
| 2026-04-30 | External open-source components curated additions (Appendix B.11): htsget, refget, MultiQC, Pixi, OpenTelemetry, OLS, IGV.js, CWL exporter, BioCompute Objects, LiteLLM, Mountpoint for S3, Marimo, REMS |
| 2026-04-30 | Document moved from `~/.claude/plans/` to `abc-cluster-cli/docs/design/` |
| 2026-04-30 | §10 Implementation roadmap (no-Khan baseline): XTDB-native identity model from day one, sprint sequence, Tailscale-identity auth, discovery commands promoted into Sprint 3, Khan-migration plan documented |
| 2026-04-30 | §10 auth model revised: removed Tailscale identity reliance. End-users authenticate via PAT (`abc_pat_<base32>`) bcrypt-hashed in XTDB on user entities; services authenticate via Nomad workload identity (signed JWT, `nomad_job_id` claim). xxhash-fingerprint trick for O(1) PAT lookup. Khan migration unchanged — Sanctum tokens become additional `:agent/access-tokens` entries. |
| 2026-04-30 | §2 *Multi-location data model* added: 10 use cases covered (reference-genome cache, cross-collaborator data, laptop SSDs, traveling disks, multi-tier backups, public URLs, federated DRS, encrypted-at-rest, partial copies, NFS shares). New entity kinds Backend and Host; `:location/availability` state machine; `:location/completeness` for partial copies; `:location/at-rest-encryption` for ciphertext-aware integrity. New §9.11 source-selection ladder for read-routing across multiple locations. Phase 2.6 (Backend+Host refactor, ~3d) and Phase 2.7 (availability+completeness+encryption, ~2d) added between Phase 2 and Phase 3. |

---

## Executive Summary

**What is this?**
The `abc-cluster` is a private HPC/research cluster built on Nomad (job scheduler), MinIO / RustFS (hot/warm object storage), Garage (cold archival), and Nextflow (genomics pipeline runner). The CLI entry point is `abc-cluster-cli`, a Go CLI.

**The problem today:**
`abc data move` and `abc data copy` are thin wrappers around rclone running inside a Nomad batch job. To move a file, users must know the internal rclone remote name (e.g. `minio-raw:bucket/path`). There is no shared address space, no verification after the move, no audit trail, and `rclone move` is one command away from data loss if the network drops mid-transfer.

**What this plan builds (end state):**

1. **A universal address space.** Every file the cluster can touch gets an `abc://path` address — backend-agnostic, stable, indexable. Users write `abc://raw/run42/sample.bam` and never touch a rclone remote name again.

2. **A live content-addressed index.** A single XTDB + Lucene service tracks every file: where it lives, what it contains (SHA-256 / MD5), what biological annotations it carries, and its full transfer + derivation history. The same index answers "where are all copies of this file?", "which runs produced this VCF?", and "has this sample been backed up?"

3. **Safe, verified moves.** `abc data move` is no longer a single rclone call. It uses a two-phase commit: copy → verify → delete source → write index. A NDJSON journal in object storage makes every transfer resumable. Verification uses ETag comparison (S3↔S3 via s5cmd) or checksum (all other pairs via rclone check).

4. **Smart tool routing.** S3↔S3 transfers use s5cmd (parallel, native S3 semantics). Everything else uses rclone. The routing is transparent — users don't pick the tool.

5. **Scientific data lineage.** Biological metadata (assay, organism, sample ID, QC status) attaches to content, not to paths — so it propagates to every copy automatically. W3C PROV-aligned derivation edges record which pipeline produced which file from which inputs. Nextflow lineage events (introduced in Nextflow 24.10) auto-populate the graph.

6. **Ecosystem interoperability.** OpenLineage bridge (Marquez / OpenMetadata / DataHub), GA4GH DRS (`drs://` URIs for Cromwell/WES), RO-Crate export, CycloneDX SBOM on every pipeline run.

7. **Resilient storage tiers.** Hot (MinIO) → Warm (RustFS) → Cold (Garage) → Cluster-local erasure-coded (private Storj, Phase 5.6) → Off-prem DR (optional public Storj DCS). Restic snapshots span all tiers.

8. **Natural-language interface.** An LLM chat tool (tool-calling, not RAG) over the full index so users can ask questions like "find all WGS BAMs from sample S00123 that haven't been backed up" and get exact answers from XTDB.

**Who should read which sections:**
- *New to the project?* Read this Executive Summary + §How We Got Here + §Architecture overview.
- *Implementing a specific phase?* Jump to §7 (Phased rollout) and cross-reference the schema in §2.
- *Evaluating tools?* See Appendix B (Tool Landscape Survey) and Appendix C (Custom Integrations).

---

## How We Got Here: Key Design Decisions

This section documents the major brainstorm conversations and the architectural decisions they produced. Each sub-section records what was considered, what was rejected and why, and what was chosen.

### Decision 1: Single address scheme (`abc://`) rather than multi-scheme

**Considered:** A multi-scheme namespace — `bucket://`, `node://`, `ext://` — to mirror the actual backend types. Initially proposed as `bucket://minio-raw/run42/sample.bam`, `node://gpu-03/scratch/run42/sample.bam`.

**Rejected because:**
- Users would need to know the backend type before addressing a file. If a file moves tiers, the address changes.
- Tooling (tab-completion, search, lineage) would need to handle multiple schemes with different resolution rules.
- Address instability defeats the point of a content-addressed index.

**Chosen:** A single `abc://` scheme backed by an index. `abc://raw/run42/sample.bam` resolves to whatever backend holds that path today. The user's address never changes when the cluster migrates the file between tiers.

**Bonus:** Dual surface forms — URI (`abc://path`) and shell-friendly path (`/abc/path`) — are canonically identical. The path form is designed for a future FUSE mount at `/abc/` on cluster nodes.

---

### Decision 2: XTDB + Lucene, not Meilisearch

**Considered:** Meilisearch as a read-cache in front of XTDB — XTDB for writes and bitemporal history, Meilisearch for full-text search and fuzzy matching.

**Rejected because:**
- Two services to deploy, monitor, and back up.
- Eventual consistency between XTDB writes and Meilisearch index updates creates a window where a freshly moved file is not yet findable.
- File-index workloads (prefix, glob, hash, tag, range filter) don't benefit from Meilisearch's typo-tolerance — that's a document-search feature.

**Chosen:** `xtdb-lucene` module — Lucene index updates are part of the XTDB transaction, so strong consistency is guaranteed. The same query language (Datalog + `text-search` predicate) handles exact-match, prefix, and full-text. One service.

**Trade-off accepted:** No typo-tolerance / fuzzy matching. If fuzzy search is needed later, tee a subset to Meilisearch behind a feature flag — don't pay for it now.

---

### Decision 3: Content/location split in the XTDB schema

**Considered:** A single flat entity per `abc://` path with all metadata (hashes, size, backend, bio annotations, lineage).

**Rejected because:**
- The same bytes (same SHA-256) can legitimately exist at multiple paths: reference genomes cached on every compute node, files copied for sharing, identical outputs from deterministic pipelines.
- If annotations (bio.assay, bio.organism) live on the path entity, you must update N entities every time an annotation changes. You will miss some. Annotation drift is the predictable failure.
- Lineage (wasGeneratedBy, wasDerivedFrom) would fork unnecessarily — the pipeline produced content, not a specific copy.

**Chosen:** Two entity kinds, joined by SHA-256:
- **Content entity** (`content:sha256:<hex>`) — one per unique file content. Carries hashes, size, bio annotations, PROV derivation edges. Propagates to all copies automatically.
- **Location entity** (`abc://path`) — one per path. Carries backend, mtime, tier, operational tags. Points to its content entity via `:location/content`.

This mirrors Git (blobs vs refs), restic (content chunks vs snapshots), and filesystems (inodes vs directory entries). It's the right model for a content-addressed system.

---

### Decision 4: fclones for dedup-grouping (not fdupes or rmlint)

**Background:** The multi-pass scanner (Pass A) needs to identify byte-identical file groups on local filesystems so Pass B can hash one representative per group rather than every file. Three candidates evaluated:

| | **fclones** | rmlint | fdupes |
|---|---|---|---|
| Language | Rust | C | C |
| Multi-threaded walk | Yes | Yes | Limited |
| Native JSON output | Yes | Yes | No |
| Native BLAKE3 | Yes (default) | Via plugin | No |
| Free BLAKE3 for Pass C | Yes | No | No |
| Hardlink dedup | Yes (`--hard-links`) | Yes | No |
| Reflink-aware | Yes | Yes | No |

**Chosen: fclones** — because it uses BLAKE3 as its default hash for grouping, which means Pass A's dedup sweep also pre-computes BLAKE3 at no extra I/O cost. Files that fclones groups in Pass A already have their BLAKE3 — Pass C just reads from the fclones JSON instead of re-reading the file. This is a free speedup of 1–5× on corpora with significant dedup (reference genomes, conda envs, identical tool binaries). fclones also outputs structured JSON natively, which the Go orchestrator can consume without fragile text parsing.

---

### Decision 5: Dual hash strategy (MD5 + SHA-256), BLAKE3 selective

**Considered:** SHA-256 only; BLAKE3 everywhere; or the dual MD5+SHA-256 pair.

**Chosen: MD5 + SHA-256, with selective BLAKE3:**
- **MD5** is free from S3 ETags — no download needed for single-part objects. It gives instant dedup checks between S3 buckets without reading any data.
- **SHA-256** is the canonical content identity for cross-backend lookups, PROV derivation, Nextflow `lid://` aliasing, and the `abc data check --hash` path.
- **BLAKE3** (via `b3sum` / fclones output) is computed selectively: files > 1 GiB (chunk-level heal is worth the cost) and files tagged for archival. Its Merkle tree enables streaming range-verification (`bao` outboard trees) without re-reading the whole file — useful for Phase 11 FUSE streaming.

---

### Decision 6: Private self-hosted Storj for the cold/resilience tier

**Considered:** Garage (already deployed) as the sole cold tier; public Storj DCS; or private Storj.

**Context:** The user specifically asked to self-host Storj — own satellite, own storage nodes. This is operationally more complex than Garage but delivers significantly stronger durability semantics:
- Erasure coding (4-of-7 for ~12 nodes) — any 3 nodes can fail simultaneously with zero data loss.
- Automatic repair: Storj satellite detects missing pieces and redistributes them.
- Audit proofs: cryptographic proof that nodes actually store the data they claim to.

**Hard constraints established:**
1. HA PostgreSQL (Patroni or CockroachDB) for the satellite's metainfo from day one — single-instance Postgres is a single point of failure that defeats the whole point.
2. Don't use the public-default 29-of-80 erasure scheme for a 12-node cluster — use 4-of-7.
3. Keep Garage running in parallel for ≥30 days as defense-in-depth during burn-in.
4. Public Storj DCS (Phase 5.6.c) requires explicit `--from-public-dr` consent at read time to prevent surprise egress charges.

---

### Decision 7: W3C PROV model for provenance (not custom)

**Considered:** A custom activity/lineage schema specific to this cluster.

**Rejected because:** W3C PROV is a battle-tested standard adopted by Galaxy, GA4GH WES, and RO-Crate. Using it means the lineage graph is directly expressible as PROV-N or PROV-JSON (for interop) and the Nextflow lineage subsystem (which already speaks PROV) maps cleanly onto our schema without a translation layer.

**Chosen:** Three PROV entity kinds — Entity (files), Activity (pipeline runs, moves, scans), Agent (users, pipelines, containers). `:prov/wasAssociatedWith` is always a **set** (not scalar) to correctly handle multi-agent scenarios (user + pipeline + container all participating in one run).

---

### Decision 8: Tool-calling chat (not RAG)

**Considered:** RAG (vector-embed all file metadata → semantic search → LLM generates answers).

**Rejected because:** The index is highly structured. Numeric filters (size > 1GB), time ranges, lineage walks, and aggregations need exact, deterministic answers. RAG hallucinates on these. A vector similarity search on "find the BAM for sample S00123" will return semantically similar strings — not the actual file the user needs.

**Chosen:** LLM with tool-calling over the same typed Datalog/Lucene functions the CLI already uses. Every question routes through the index; answers are auditable. Semantic similarity search (Phase 10.5) is added as a single dedicated tool for free-text annotation queries — not the default retrieval path.

---

## Context

`abc data move` and `abc data copy` exist today as thin rclone-over-Nomad wrappers. Users must know internal rclone remote names, there is no common address for files across backends (bucket vs node-local vs external), and `move` is destructive with no verification or audit trail.

This plan extends the existing commands under a single `abc://` universal namespace, backed by a live index that tracks the full lifecycle and journey of every tracked file across the cluster.

---

## Architecture overview

```
CLI                XTDB + Lucene             Cluster Execution
───                ─────────────             ─────────────────
abc data move      single index:             Nomad job
abc://a→abc://b ──► resolve both ─────────► ┌─ s5cmd   (S3↔S3, S3↔local)
                   ▲ lineage transact ◄─────┤
                   │ Lucene text-search       └─ rclone  (everything else)
                   │
               Index ingest sources:
                 • MinIO/RustFS bucket notifications (event-driven, S3)
                 • `abc data index refresh` Nomad scan job:
                     S3       → s5cmd ls --etag           (md5 only)
                     node-local → rhash -r --md5 --sha256 (both hashes)
                 • Write-through on every successful CLI move/copy (finalize phase)
                 • Lazy SHA-256 fill on first move/check of each S3 object
```

### Storage tier topology (with Phase 5.6)

```
Hot tier         : MinIO              cluster-local, sub-10ms, frequent reads
Warm tier        : RustFS             cluster-local, less I/O-intensive
Cold tier        : Garage              cluster-local archive (Phase 5.5)
                                          │ (parallel during burn-in; deprecation TBD)
                   Private Storj       cluster-local erasure-coded (Phase 5.6.a/b)
                   - own satellite + storage nodes
                   - 4-of-7 erasure scheme for ~12 nodes
                   - LAN-class retrieval, no egress charges

DR / off-prem    : Public Storj DCS    decentralized off-cluster (Phase 5.6.c, optional)
                   - tertiary insurance for true disaster recovery
                   - explicit `--from-public-dr` consent required to read
```

`abc data restore` source-selection ladder: hot → cold (Garage) → Storj private → Storj public DCS (last resort, paid egress).

---

## 1. `abc://` universal namespace (dual-form: URI + path)

**Single namespace, two equivalent surface forms.** Users may write either form; the parser canonicalises to the URI form internally so the index has exactly one identifier per file.

```
abc://<path>           # URI form  (unambiguous, self-describing)
/abc/<path>            # path form (shell-friendly, tab-completable, FUSE-mountable later)
```

These two are **always** identical:
```
abc://raw/run42/sample.bam   ≡   /abc/raw/run42/sample.bam
```

<!-- Note we could provide a tool for tab-completion of these files later on! -->

### Parsing rules

| Input | Treated as |
|---|---|
| `abc://x/y/z` | abc namespace (URI form) |
| `/abc/x/y/z` | abc namespace (path form, auto-normalised to URI) |
| `abc:/x` (single slash) | error — clear hint to use `abc://` or `/abc/` |
| `abc:x` | raw rclone passthrough (matches today's rclone remote-name pattern) |
| `/local/path` | local filesystem (unrelated) |

### Output style

Default: URI form (`abc://...`). Override via `--style path` flag, `ABC_PATH_STYLE=path` env var, or `ActiveCtx().Display.PathStyle`. Error messages echo back the form the user typed so feedback feels native.

### Future hook: FUSE mount at `/abc/`

The path form is chosen specifically to enable a future Phase implementing a FUSE filesystem at `/abc/` on cluster nodes. Same resolver, different I/O surface — `cd /abc/raw/run42 && ls` would transparently list, `cat sample.bam` would stream from MinIO. Tracked as Phase 11 (post-chat).

### Original section

`<path>` is user-managed and backend-agnostic. The index resolves it via a two-entity join (location → content; see schema in §2):

```
abc://raw/run42/sample.bam   (location entity)
  → backend: minio
  → actual:  minio-raw:raw/run42/sample.bam
  → mtime:   2026-04-15T09:12:00Z
  → tier:    :hot
  → content: content:sha256:a3f92c…   (joins to content entity)

content:sha256:a3f92c…       (content entity, shared by all locations of this file)
  → size:        12_483_021
  → md5:         d41d8c…
  → bio.assay:   WGS
  → bio.organism: "Homo sapiens"
  → wasGeneratedBy: act:nf-887f…
```

Raw rclone passthrough (`remote:path` syntax, no `abc://`) continues to work for operators who need the escape hatch.

### Resolution at transfer time

```
ParseABCPath("abc://raw/run42/") → IndexEntry{Backend: "minio", ActualPath: "minio-raw:raw/run42/"}
```

`ParseABCPath` queries XTDB directly via `internal/indexer.Resolve()` — a typed Go wrapper around the canonical Datalog `:find ?backend ?actual` query. XTDB-Lucene serves both exact-match and prefix lookups in a single index.

### Tool routing (after resolution)

| Source backend | Dest backend | Tool |
|---|---|---|
| S3 (minio/rustfs/external S3) | S3 | **s5cmd** |
| S3 | node-local | **s5cmd** |
| node-local | S3 | **s5cmd** |
| node-local | node-local | **rclone** |
| any | SFTP/GCS/HTTP/other | **rclone** |
| unresolved raw `remote:path` | any | **rclone** (today's behaviour) |

This happens inside a new `selectTool(src, dst IndexEntry) Tool` function in `cmd/data/transfer.go`.

---

## 2. Index backend: XTDB with Lucene

Single index — no separate search service. XTDB is already operational in this stack and its bitemporal model gives full lineage queries for free. The `xtdb-lucene` module adds full-text + prefix search inside the same transaction, eliminating the Meilisearch sync layer entirely.

**Why this collapses cleanly:**
- Lucene index updates are part of the XTDB transaction → strong consistency, no eventual-consistency gap.
- One service to deploy, monitor, and back up.
- Lucene's `text-search` predicate works inside Datalog queries: prefix, glob, and full-text in one query language.

**Trade-off accepted:** no typo-tolerance / fuzzy search. For file-index workloads (prefix + tag + hash lookups dominate), this is acceptable. If fuzzy matching is needed later, tee a subset to Meilisearch behind a flag — don't build for it now.

### EDN namespace conventions (read this first)

Naming and bundling rules that every entity in the index MUST follow. These prevent the predictable schema-drift failures (namespace overload, ambiguous nullability, multi-source agent claims, lineage breaks at external boundaries).

**Namespace inventory** — organized by *what the data describes*, not by which subsystem produced it:

| Namespace | Lives on | Describes |
|---|---|---|
| `:content/*` | Content entity | Bytes-level facts (hashes, size, format) — same for every copy |
| `:location/*` | Location entity | Place-level facts (backend, path, mtime, tier) — different per copy |
| `:integrity/*` | Location entity | Integrity check state (observed vs expected, healing source) |
| `:backup/*` | Location and Snapshot entities | Backup membership and integrity |
| `:archive/*` | Content entity | Public archive memberships (Storj/torrent/Arweave; deferred Phase 12) |
| `:bio/*` | Content entity | Biological annotations (assay, organism, sample, study) |
| `:compliance/*` | Content and Domain entities | Retention, sharing policy, de-ID provenance, IRB refs |
| `:prov/*` | All PROV entity kinds | W3C PROV-aligned derivation, agency |
| `:pipeline/*` | Activity entity | Pipeline name, version, params, runtime |
| `:agent/*` | Agent entity | Agent kind, version, identity |
| `:vocab/*` | Vocabulary entity | Controlled-vocabulary definitions, hierarchy, strictness |
| `:domain/*` | Domain entity | Studies, projects, cohorts |
| `:quality/*` | QualityCheck entity | Scheduled QC results |
| `:rule/*` | Rule entity | Lifecycle policy rules |
| `:nf/*` | Activity entity (NF source) | Nextflow-specific aliases (lid, task ID, container digest) |
| `:ol/*` | Any | OpenLineage facets / aliases when sourced from OL |
| `:ga4gh/*` | Content entity | DRS object ID, Crypt4GH refs |
| `:ontology/*` | Vocabulary, content | Ontology references (EFO, NCBI Taxonomy, OBO Foundry) |
| `:external/*` | External reference entities | Boundary markers for refs we don't store |
| `:audit/*` | All entities (system-managed) | Who-saw-what timestamps |
| `:chat/*` | Conversation entity | Chat conversations |
| `:search/*` | SavedSearch entity | Persisted searches |
| `:xt/*` | Reserved for XTDB | Identity, valid-time, transaction-time |

**Bundling rule (flat vs nested):**
- **Flat** for fields filtered in WHERE clauses or indexed by Lucene (`:bio/assay`, `:bio/sample-id`, `:location/tier`).
- **Nested map** for logically-grouped sub-fields rarely queried individually (`:bio/sample-detail`, `:bio/assay-detail`, `:integrity`, `:backup`, `:compliance`, `:pipeline`).
- A nested map is a single attribute from XTDB's view — it lives or moves as a unit.

**Cardinality rule:**
- **Set** (`#{...}`) for: `:bio/study`, `:tags`, `:prov/used`, `:prov/generated`, `:prov/wasDerivedFrom`, `:prov/wasGeneratedBy`, `:prov/wasAssociatedWith`, `:archive/*` lists. Cardinality-many is the safe default.
- **Vector** (`[...]`) only when order matters (paired-end FASTQ inputs).
- **Scalar** for one-fact attributes (hashes, sizes).

**Nullability rule:**
- **Never store nil values.** Omit the attribute entirely if absent.
- In Datalog, use `(missing? $ ?e :attr)` to test for absence.
- Reason: `[?e :attr nil]` is a true match in XTDB and silently breaks queries that expected absence.

**ID convention:** entity types are encoded in the `:xt/id` string prefix so any reference is debuggable at a glance.

| Entity kind | `:xt/id` shape | Example |
|---|---|---|
| Content | `"content:sha256:<hex>"` | `"content:sha256:a3f92c…"` |
| Location | `"abc://<path>"` (URI canonical form, never `/abc/...`) | `"abc://raw/run42/sample.bam"` |
| Activity | `"act:<source>:<id>"` | `"act:nf:887f…"`, `"act:job:run-2026-04-15-9"` |
| Agent | `"agent:<kind>:<id>"` | `"agent:user:abhinav"`, `"agent:pipeline:nf-core/sarek:3.4.0"`, `"agent:container:sha256:…"` |
| Snapshot | `"snap:<repo>:<snapshot-id>"` | `"snap:garage-archive:2026-04-15-daily"` |
| Domain | `"domain:<kind>:<id>"` | `"domain:study:study-abc-001"` |
| Vocabulary | `"vocab:<key>:<value>"` | `"vocab:bio.assay:WGS"` |
| QualityCheck | `"qc:<check>:<run>"` | `"qc:vcf-validity:2026-04-15-001"` |
| Conversation | `"chat:<runid>"` | `"chat:conv-2026-04-28-9f3a"` |
| SavedSearch | `"search:<name>"` | `"search:my-genomics-recent"` |
| Rule | `"rule:<name>"` | `"rule:archive-old-bams"` |
| External ref | `"external:<kind>:<id>"` | `"external:ref:GRCh38.p14"` |

Aliases from other systems (e.g., Nextflow `lid://`) live as additional attributes on the entity, never as the `:xt/id`. Cross-system joins always go through SHA-256.

**Cross-entity reference convention:** references are bare `:xt/id` strings (no wrapping). External references use the `external:` prefix so lineage walks know to stop at boundaries.

```clojure
:prov/wasGeneratedBy  #{"act:nf:887f…"}
:abc/content          "content:sha256:a3f92c…"
:prov/wasDerivedFrom  #{"content:sha256:e1d7…"
                        "external:ref:GRCh38.p14"}    ; chain stops here
```

**Edge cases the schema must handle (and how):**

| # | Edge case | Resolution in schema |
|---|---|---|
| 1 | Two activities deterministically produce same bytes | `:prov/wasGeneratedBy` is a **set** on the content entity |
| 2 | One activity has user + pipeline + container as agents | `:prov/wasAssociatedWith` is a **set** (PROV-standard); `:prov/agent` not used |
| 3 | File belongs to multiple studies | `:bio/study` is always a **set** even if usually one |
| 4 | Sample-id collides across studies | Always paired with `:bio/study`; queries scope by both |
| 5 | Integrity OK, no reason | `:integrity/reason` **omitted**, not nil |
| 6 | mtime ambiguity (storage vs discovery) | Two attributes: `:location/storage-mtime` (from backend) and `:location/discovered-at` (when we saw it) |
| 7 | Reference genome we don't store | `external:ref:` entity; lineage walk stops at the prefix |
| 8 | Nextflow `lid` ↔ `content:sha256` | SHA-256 is canonical; `lid://` stored as `:nf/lid` alias |
| 9 | Corrupted observation forensics | New content entity for observed bytes with `:content/orphan true`; not GC'd until grace period elapses |
| 10 | OpenLineage event references unknown dataset | Insert minimal content entity with `:content/discovered-by :openlineage`; reconcile on next scan |
| 11 | XTDB transaction-time vs valid-time confusion | Always use valid-time for "as-of" queries; CLI flags name it explicitly (`--at`) |
| 12 | PHI-bearing sample-id | `:compliance/data-class` flag on study domain; chat tools filter outputs accordingly |
| 13 | Right-to-be-forgotten conflict with XTDB history | `:compliance/erasure-tombstone` on the content; Datalog queries respect it; documented as design limit |
| 14 | Restic snapshot member resolves to a path no longer indexed | Snapshot entity holds string paths; cross-ref resolves to nil if missing — surfaces as integrity gap |
| 15 | Annotation propagation accidental disclosure | Bio/* on content (intentional propagation); `:tags` location-level by default; explicit `--content-level` override required |

### Schema: split content from location (critical)

The same content (same SHA-256) can legitimately exist at many `abc://` paths simultaneously — reference genomes cached on multiple nodes, files copied for sharing, identical outputs from deterministic pipelines. Conflating content and location into one entity breaks annotation propagation and forks lineage unnecessarily. The schema is therefore split into **content** and **location** entities, joined by SHA-256.

This mirrors the same separation Git uses (blobs vs refs), restic uses (blobs vs snapshots), and filesystems use (inodes vs directory entries).

**Content entity (one per unique SHA-256)** — follows the namespace conventions above:

```clojure
{:xt/id                "content:sha256:a3f92c…"

 ;; Identity (flat, indexed)
 :content/sha256       "a3f92c…"
 :content/md5          "d41d8c…"
 :content/blake3       "e1d7a9…"            ; if BLAKE3 adopted (see §2a)
 :content/bao-tree-root "…"                  ; for chunk-level verify
 :content/size         12483021
 :content/format       "bam"

 ;; Bio annotations — flat keys for filtering, nested groups for detail
 :bio/assay            "WGS"
 :bio/sample-id        "S00123"
 :bio/study            #{"study-abc-001" "study-mr-002"}    ; always a set
 :bio/qc-status        :pass
 :bio/file-format      "bam"
 :bio/sample-detail    {:donor-id        "D456"
                        :collection-site "BIDMC"
                        :collected-at    #inst "2026-03-01T00:00:00Z"
                        :tissue-type     "blood"
                        :tissue-condition "tumor"}
 :bio/assay-detail     {:library-prep   "Illumina TruSeq DNA"
                        :coverage       30.5
                        :read-length    150
                        :paired-end     true
                        :instrument     "NovaSeq 6000"}
 :bio/organism         {:taxonomy        "NCBITaxon:9606"
                        :common-name     "Homo sapiens"
                        :reference-build "GRCh38"}

 ;; Ontology refs — set, multiple ontologies allowed
 :ontology/refs        #{{:scheme "EFO"       :id "EFO:0003744"    :label "WGS"}
                         {:scheme "NCBITaxon" :id "NCBITaxon:9606" :label "Homo sapiens"}
                         {:scheme "GRCh"      :id "GRCh38.p14"     :label "GRCh38 patch 14"}}

 ;; Provenance — sets so deterministic-pipeline collisions and multi-source claims work
 :prov/wasGeneratedBy  #{"act:nf:887f…"}
 :prov/wasDerivedFrom  #{"content:sha256:e1d7…"
                         "content:sha256:b22c…"
                         "external:ref:GRCh38.p14"}    ; chain stops at external

 ;; Compliance (content-level → applies to all copies)
 :compliance           {:share-public      false
                        :de-id-policy      "v1.2 / GDPR-recital-26"
                        :de-id-applied-at  #inst "2026-04-01T00:00:00Z"
                        :retention-until   #inst "2030-12-31T00:00:00Z"
                        :legal-basis       "research-consent-v3"
                        :data-class        :restricted}

 ;; Interop aliases
 :ga4gh/drs-id         "drs://abc-cluster/content-sha256:a3f92c…"
 :nf/lid               "lid://a3f92c…"

 ;; Public archive (deferred Phase 12) — present only if archived
 :archive              {:storj-grant   "grant:abc-archive:s00123-bam"
                        :magnet-uri    nil
                        :arweave-tx-id nil
                        :sealed-at     nil}

 ;; Audit (system-managed)
 :audit                {:first-seen-at #inst "2026-04-15T09:13:00Z"
                        :first-seen-by :scan}}
```

**Location entity (one per `abc://` path)** — every attribute is location-scoped:

```clojure
{:xt/id                  "abc://raw/run42/sample.bam"

 ;; The join — never nil, never missing
 :location/content       "content:sha256:a3f92c…"

 ;; Place facts — explicitly disambiguated mtime
 :location/backend       "minio"
 :location/actual        "minio-raw:raw/run42/sample.bam"
 :location/storage-mtime #inst "2026-04-15T09:12:00Z"   ; from backend metadata
 :location/discovered-at #inst "2026-04-15T09:13:00Z"   ; when we first saw it
 :location/tier          :hot
 :location/discovered-by :scan
 :location/tags          #{"operational" "high-priority"}    ; LOCATION-level only

 ;; Integrity bundle (omit :reason entirely when ok)
 :integrity              {:status            :ok
                          :observed-content  "content:sha256:a3f92c…"
                          :observed-at       #inst "2026-04-25T02:00:00Z"
                          :source            :scan
                          :quarantine        false}

 ;; Backup bundle — snapshot membership held INVERTED on snapshot entities
 :backup                 {:last-verified  #inst "2026-04-20T03:00:00Z"
                          :integrity      {:checked #inst "2026-04-25T02:00:00Z"
                                           :status :ok
                                           :checker "restic-check-runner"}}

 ;; Audit
 :audit                  {:created-by    "agent:user:abhinav"
                          :last-touched  #inst "2026-04-25T02:00:00Z"}}
```

**Divergent location** — corrupted/truncated copy preserves forensics:

```clojure
{:xt/id              "abc://cold/archive/run40/sample.bam"
 :location/content   "content:sha256:a3f92c…"          ; what was expected
 :location/backend   "rustfs"
 :location/storage-mtime #inst "2026-04-03T00:00:00Z"
 :location/tier      :cold

 :integrity          {:status              :divergent
                      :observed-content    "content:sha256:f00d…"   ; different bytes
                      :observed-at         #inst "2026-04-25T02:00:00Z"
                      :source              :restic-check
                      :reason              "sha256 mismatch: expected a3f9… got f00d…"
                      :first-divergence-at #inst "2026-04-22T14:00:00Z"
                      :quarantine          false}}

;; Separate content entity for the corrupted observation (forensics)
{:xt/id              "content:sha256:f00d…"
 :content/sha256     "f00d…"
 :content/size       12482000
 :content/orphan     true                                ; flagged for GC after grace period
 :content/discovered-by-divergence-at "abc://cold/archive/run40/sample.bam"}
```

**Activity entity** — multi-agent provenance via `:prov/wasAssociatedWith`:

```clojure
{:xt/id                "act:nf:887f…"
 :prov/type            :activity

 :prov/started         #inst "2026-04-18T10:00:00Z"
 :prov/ended           #inst "2026-04-18T14:22:00Z"
 :prov/used            #{"content:sha256:e1d7…" "content:sha256:b22c…"}
 :prov/generated       #{"content:sha256:a3f92c…" "content:sha256:f00d…"}
 :prov/wasAssociatedWith #{"agent:user:abhinav"
                           "agent:pipeline:nf-core/sarek:3.4.0"
                           "agent:container:sha256:9a8b…"}

 :pipeline             {:name      "nf-core/sarek"
                        :version   "3.4.0"
                        :process   "BWAMEM2_MEM"
                        :params    {:tools ["haplotypecaller"] :joint_germline true}
                        :exit-code 0
                        :runtime-seconds 15720}

 :nf                   {:lid          "lid://887f…"
                        :workflow-run "run-2026-04-18-nf-9k3"
                        :task-process "BWAMEM2_MEM"
                        :work-dir     "/work/87/887f…"}

 :prov/sbom-cyclonedx  "abc://_sboms/act-887f.json"}     ; ref to SBOM doc
```

**Snapshot entity** — also a PROV activity; holds inverted member list:

```clojure
{:xt/id                "snap:garage-archive:2026-04-15-daily"
 :prov/type            :activity
 :prov/wasAssociatedWith #{"agent:service:restic:0.16.4"}
 :prov/started         #inst "2026-04-15T03:00:00Z"
 :prov/ended           #inst "2026-04-15T03:42:00Z"

 :backup/repo          "garage://abc-archive"
 :backup/host          "abc-cluster-prod"
 :backup/files         12847
 :backup/size-bytes    909647000000

 ;; Inverted relation: snapshot lists members; locations don't list snapshots
 :prov/generated       #{"abc://raw/run42/sample.bam"
                         "abc://raw/run42/sample.bai"
                         ;; … 12,845 more
                         }

 :backup/integrity     {:last-check     #inst "2026-04-25T02:00:00Z"
                        :status         :ok
                        :sample-percent 1.0}}
```

**Vocabulary entity** — controlled term with hierarchy:

```clojure
{:xt/id              "vocab:bio.assay:WGS"
 :vocab/key          :bio/assay
 :vocab/value        "WGS"
 :vocab/label        "Whole Genome Sequencing"
 :vocab/synonyms     #{"whole genome sequencing"}
 :vocab/parent       "vocab:bio.assay:DNA-seq"
 :ontology/refs      #{{:scheme "EFO" :id "EFO:0003744"}}
 :vocab/strict       true}
```

**Domain entity** — study/project/cohort:

```clojure
{:xt/id              "domain:study:study-abc-001"
 :domain/kind        :study
 :domain/name        "ABC Lung Cohort 2026"
 :domain/pi          "agent:user:dr-smith"
 :domain/started     #inst "2026-01-15T00:00:00Z"
 :domain/expected-end #inst "2027-12-31T00:00:00Z"
 :compliance         {:irb-protocol "IRB-2026-0042"
                      :consent-form "v3.1"
                      :data-class   :restricted}}
;; Members not stored here — queried via Datalog (content with :bio/study containing this id)
```

**External reference** — boundary marker:

```clojure
{:xt/id              "external:ref:GRCh38.p14"
 :external/kind      :reference-genome
 :external/source    "https://www.ncbi.nlm.nih.gov/assembly/GCF_000001405.40/"
 :external/sha256    "8b73…"
 :external/note      "Not stored in our system; derivation chains stop here."}
```

Multiple location entities can share the same `:abc/content` reference — that's how duplicates across paths/backends are represented natively. Restic snapshot membership, mtime, tier, and discovery method are all per-location; biological annotations and provenance are per-content (so they propagate automatically).

**Lucene-indexed fields:** location `:xt/id` (path), location `:abc/tags`, location `:abc/backend`, content `:bio/*` keys (so search queries spanning bio + path work in one query). Hash lookups use Datalog directly.

**Command semantics under this schema:**

| Command | Content effect | Location effect |
|---|---|---|
| `data move abc://a abc://b` | unchanged | retract a, upsert b with same `:abc/content` |
| `data copy abc://a abc://b` | unchanged | upsert b with same `:abc/content` |
| `annotate --bio.assay WGS` | upsert annotation on content | — (default for `bio.*`) |
| `annotate --tag ops` | — | upsert tag on location (default for plain tags) |
| `archive` (Phase 5.5) | unchanged | upsert location with `:abc/tier :cold`, snapshot ref |
| content modified (overwrite with new bytes) | new content entity created | location `:abc/content` rebound to new id |

CLI flag overrides: `--content-level` and `--location-level` let users force placement when the default is wrong.

**Garbage collection.** A content entity becomes collectible when (a) no location references it, (b) no PROV activity references it (`:prov/used` / `:prov/generated`), and (c) no restic snapshot member resolves to it. Periodic Nomad job runs the orphan query; conservative 30-day grace period before deletion.

**Canonical Datalog queries** (wrapped in typed Go functions in `internal/indexer/`):

```clojure
;; Resolve abc:// path → backend + actual path
[:find ?backend ?actual ?size :where
 [?l :xt/id "abc://raw/run42/sample.bam"]
 [?l :abc/backend ?backend] [?l :abc/actual ?actual]
 [?l :abc/content ?c] [?c :abc/size ?size]]

;; All locations of a content hash
[:find ?path :where
 [?c :abc/sha256 "a3f92c…"]
 [?l :abc/content ?c] [?l :xt/id ?path]]

;; List by prefix (Lucene) — returns location entities
[:find ?path :where
 [(text-search :xt/id "abc://raw/run42/*") [[?l]]]
 [?l :xt/id ?path]]

;; All WGS files with their backends — joins content annotations with location
[:find ?path ?backend :where
 [?c :bio/assay "WGS"] [?c :bio/organism "Homo sapiens"]
 [?l :abc/content ?c]
 [?l :xt/id ?path] [?l :abc/backend ?backend]]

;; Most-replicated files (dedup analysis)
[:find ?sha (count ?l) :where
 [?c :abc/sha256 ?sha] [?l :abc/content ?c]]
```

**On move/copy finalize:**
- **Move:** retract source location entity, upsert destination location entity pointing at the same `:abc/content` id. Content entity is unchanged (no rewriting; SHA-256 didn't change).
- **Copy:** upsert destination location entity pointing at the same `:abc/content` id. Source unchanged.
- For both: a single XTDB transaction → Lucene index stays consistent. XTDB preserves history; queryable by valid-time via `xt/entity-history`.

**Disk footprint estimate:** ~500 MB Lucene index per million entities (path + tags + backend indexed). Comfortable for the foreseeable corpus.

### Multi-location data model — Backend and Host entities

The content/location split solves identity vs. place. **Multi-location** — the same content existing at many heterogeneous places (cluster MinIO, compute-node SSDs, user laptops, traveling disks, restic snapshots, public URLs, peer clusters via DRS) — needs two further entity kinds so backend metadata isn't duplicated on every location and so availability/access semantics are first-class.

#### Use cases this addresses

| # | Scenario | Why the existing schema isn't enough |
|---|---|---|
| 1 | Reference genome (GRCh38) cached on every compute node | Need per-node Backend entity for source-selection ranking |
| 2 | Sample data spread across collaborators (cluster MinIO + lab NAS + portable disk + cold archive) | Need access-profile per backend so reads pick the cheapest reachable copy |
| 3 | Grad student's laptop holds copies that come and go with their connection | Need Host entity with availability pattern; locations on offline hosts marked transient |
| 4 | Traveling disk plugged into multiple hosts over time | Already partially handled (Disk entity from Phase 0.6); needs Backend abstraction for source-selection |
| 5 | Same content in multi-tier backups (Garage + Storj private + Storj public) | Need Backend per restic repo with distinct access profiles |
| 6 | Public reference data also retrievable from `https://ftp.ncbi.nlm.nih.gov/...` | Need a `:public-url` backend kind that's `:cluster-readable true` and `:requires-auth false` |
| 7 | Federated peer cluster exposes content via DRS | Need a `:drs` backend kind with auth + DTA requirements in its access profile |
| 8 | Content stored Crypt4GH-encrypted on Storj public DCS | Need `:location/at-rest-encryption` so integrity check doesn't false-positive on ciphertext |
| 9 | A 60%-downloaded BAM that's worth resuming, not restarting | Need `:location/completeness :partial` with byte ranges |
| 10 | NFS share visible to 8 compute nodes via 8 different mount paths | One Backend (the NFS server), one location per file; mount paths are observational metadata not separate locations |

#### Backend entity (one per backend instance the cluster knows about)

```clojure
;; S3-class backend (cluster-local)
{:xt/id                  "backend:minio-raw"
 :prov/type              :backend
 :backend/kind           :s3
 :backend/access         {:endpoint "https://minio.cluster.internal:9000"
                          :bucket   "abc-raw"
                          :region   "af-south-1"}
 :backend/jurisdiction   :ZA
 :backend/owner          :cluster
 :backend/access-profile {:typical-latency-ms     5
                          :egress-cost-per-gb     0.0
                          :max-throughput-mbps    10_000
                          :reliability            0.9999
                          :requires-auth          true
                          :auth-mechanism         :s3-iam}}

;; Compute node local filesystem
{:xt/id                  "backend:node:gpu-03"
 :backend/kind           :host-fs
 :backend/host-id        "host:node-gpu-03"           ; reference to Host entity
 :backend/owner          :cluster
 :backend/access-profile {:typical-latency-ms     1
                          :egress-cost-per-gb     0.0
                          :reliability            0.99}}     ; node can go down

;; Portable disk (from Phase 0.6)
{:xt/id                  "backend:disk:abc-001"
 :backend/kind           :portable-disk
 :backend/disk-serial    "WDC-WX12345-67890"
 :backend/disk-uuid      "12345678-1234-…"
 :backend/owner          "agent:user:abhinav"
 :backend/access-profile {:typical-latency-ms     2
                          :requires-mount         true
                          :availability-pattern   :intermittent}}

;; User workstation / laptop
{:xt/id                  "backend:host:abhinav-laptop"
 :backend/kind           :workstation
 :backend/host-id        "host:abhinav-laptop"
 :backend/owner          "agent:user:abhinav"
 :backend/access-profile {:requires-network            true
                          :availability-pattern         :intermittent
                          :cluster-readable             false   ; cluster can't pull from laptop
                          :reachable-when-online-only   true}}

;; Restic repository (one per repo; Snapshot entities still hold member lists)
{:xt/id                  "backend:restic:garage-archive"
 :backend/kind           :restic-repo
 :backend/repo-url       "garage://abc-archive"
 :backend/restic-key-ref "<xtdb-secret-ref>"
 :backend/access-profile {:typical-latency-ms     200
                          :requires-auth          true
                          :access-mode            :read-write}}

;; Public URL
{:xt/id                  "backend:public:ncbi-grch38"
 :backend/kind           :public-url
 :backend/url-prefix     "https://ftp.ncbi.nlm.nih.gov/genomes/all/GCA/000/001/405/"
 :backend/access-profile {:typical-latency-ms     500
                          :egress-cost-per-gb     0.0
                          :requires-auth          false
                          :reliability            0.95}}

;; Peer cluster DRS
{:xt/id                  "backend:drs:uct-genomics"
 :backend/kind           :drs
 :backend/drs-endpoint   "https://drs.uct-genomics.za/"
 :backend/jurisdiction   :ZA
 :backend/access-profile {:typical-latency-ms     50
                          :requires-auth          true
                          :auth-mechanism         :ga4gh-passport
                          :requires-dta           "DTA-001"
                          :egress-cost-per-gb     0.0}}
```

**Backend kinds enumerated:** `:s3 | :host-fs | :portable-disk | :workstation | :nas | :restic-repo | :public-url | :drs | :tape-archive | :instrument-storage`. Adding a new kind is a Backend entity insert, not a schema migration.

#### Host entity (where laptops, nodes, NAS servers live)

```clojure
{:xt/id                       "host:abhinav-laptop"
 :prov/type                   :host
 :host/name                   "abhinav-laptop"
 :host/uuid                   "f3a2…"            ; immutable; survives renames
 :host/owner                  "agent:user:abhinav"
 :host/kind                   :workstation       ; :workstation | :node | :nas-server | :portable
 :host/jurisdiction           :ZA
 :host/last-seen-at           #inst "2026-04-30T11:42:00Z"
 :host/availability-pattern   :intermittent      ; :always | :business-hours | :intermittent
 :host/mount-points-known     #{}}               ; populated by watchers when relevant
```

When a host's `:host/last-seen-at` is older than its expected interval, all backends pointing at that host are marked unavailable transitively → all locations on those backends become `:availability :offline`.

#### Location entity becomes a thin (Backend, path, integrity, availability) tuple

```clojure
{:xt/id                "abc://raw/run42/sample.bam"
 :location/content     "content:sha256:a3f9…"
 :location/backend     "backend:minio-raw"          ; reference to Backend entity
 :location/path        "run42/sample.bam"            ; bucket-key | fs-path | disk-path | snapshot-path
 :location/storage-mtime  #inst "…"
 :location/discovered-at  #inst "…"
 :location/integrity   {:status :ok …}
 :location/availability   :available
 :location/completeness   {:state :complete}
 :location/tags        #{"genomics" "run42"}
 :workspace/owner      "ws-genomics-lab-001"}
```

All backend-level metadata (jurisdiction, latency, cost, auth requirements) is reached via the `:location/backend` join — never duplicated.

#### `:location/availability` (state machine)

```clojure
:location/availability
  :available           ; reachable now
  :transient           ; portable medium, currently unmounted (Phase 0.6 disk)
  :offline             ; host known but currently offline (laptop, intermittent NAS)
  :missing-since-scan  ; was here last scan, isn't now (file was deleted out-of-band)
  :unreachable         ; access policy denies us currently
  :requires-mount      ; mount-on-demand backend, not mounted

:location/last-verified-available  #inst "…"
```

Watchers and ingesters update this attribute on every observation. Search/find queries default to `:available`; `--include-transient` opens it up.

#### `:location/completeness` (for partial / resumable copies)

```clojure
:location/completeness
  {:state          :complete                       ; :complete | :partial | :downloading
   :bytes-present  12_483_021                      ; for :complete = content size
   :ranges-present [[0 5_000_000] [6_000_000 12_483_021]]   ; sparse, for :partial only
   :resumable      true
   :resume-token   "rclone-resume:…"}
```

Most locations carry `{:state :complete}` and never look at this. Partial-state representation lets `abc data move --resume` pick up interrupted transfers and lets risk reports flag resumable-but-stale partials.

#### `:location/at-rest-encryption` (so integrity-check doesn't false-positive)

```clojure
:location/at-rest-encryption
  {:scheme                  :crypt4gh    ; :none | :crypt4gh | :age | :s3-sse | :luks-volume
   :key-ref                 "<xtdb-secret-ref>"
   :public-key              "…"          ; for crypt4gh recipient verification
   :integrity-via           :encrypted-bytes-sha256
   :verified-decryptable-at #inst "…"}
```

Crucial implication: when `:scheme` is non-`:none`, the bytes at the location aren't equal to the content's SHA-256. The integrity-check pipeline records `:integrity/observed-encrypted-content-sha256` (a separate observation) to detect ciphertext corruption without needing the key. Plaintext SHA-256 verification happens only on demand, when the user authorises a decryption.

#### Source-selection ladder

With these extensions, every read path (`abc data restore`, `abc data check --from-file` confirmation, pipeline input fetch, cache-hit lookup) ranks candidate locations using:

```
Score(location) = f(
  available?           → ∞ if not :available; else proceed
  ACL-permitted?       → ∞ if Jurist read-policy denies; else proceed
  user-has-creds?      → ∞ if backend requires auth user lacks; else proceed
  egress-cost-per-gb   → prefer 0
  typical-latency-ms   → prefer low
  reliability          → prefer high
  encryption-ok?       → small penalty if requires user-authorised decryption
)
```

Concrete ranking for a read against UC2 (sample S00123 with multiple locations):

```
1. backend:minio-raw          score: 1     cluster-hot, free, sub-10ms, available
2. backend:host:node-gpu-03   score: 2     node-local cache, free, sub-1ms, but node sometimes down
3. backend:nas:labshare       score: 5     LAN, free, ~50ms, requires mount
4. backend:disk:abc-001       score: 12    currently unmounted; transient
5. backend:storj-private      score: 20    LAN-class but cold tier, ~200ms
6. backend:drs:uct-genomics   score: 200   cross-institution, requires DTA, ~500ms
7. backend:storj-public       score: 1000  off-prem, paid egress, requires explicit consent
```

CLI defaults to the lowest score; user can override with `--from backend:disk:abc-001`. Public DCS reads (Phase 5.6.c) require explicit `--from-public-dr` consent regardless of score.

#### Use-case round trip with extensions in place

| UC | How it's modelled |
|---|---|
| **UC1 ref genome on every node** | 1 content; 13 location entities; backends `backend:minio-ref` + `backend:node:gpu-{01..12}`. Source-selection picks local node when one exists. |
| **UC2 cross-collaborator** | Multiple locations across distinct backend entities; access-profile drives source-selection |
| **UC3 grad student laptop** | 1 Host entity (`host:abhinav-laptop`) + 1 Backend (`:cluster-readable false`) + N locations. When laptop offline, locations show `:availability :offline`. Cluster knows files exist; can't pull. |
| **UC4 traveling disk** | 1 Backend per disk; `:availability :transient` when not mounted; flips to `:available` when watcher detects mount |
| **UC5 multi-tier backups** | 3+ Backend entities for restic repos; locations per snapshot member reference the appropriate restic Backend |
| **UC6 public reference** | 1 Backend for the public URL prefix; `:cluster-readable true`, `:requires-auth false` |
| **UC7 federated peer cluster** | 1 Backend for the DRS endpoint; access-profile carries `:requires-dta`; Jurist read-policy gates use |
| **UC8 encrypted-at-rest** | `:location/at-rest-encryption` records scheme + key-ref; ciphertext SHA-256 used for integrity; plaintext verification opt-in |
| **UC9 partial copies** | `:location/completeness {:state :partial :ranges-present [...]}` captures resumable state |
| **UC10 NFS server, 8 nodes** | 1 Host (NFS server); 1 Backend (the share); 1 location per file; `:host/mount-points-known` records compute nodes that mount it (observational, not authoritative) |

#### Datalog query examples

```clojure
;; All currently-reachable copies of a content (for source-selection)
[:find ?path ?backend-id ?score
 :where
 [?c :content/sha256 "a3f9…"]
 [?l :location/content ?c]
 [?l :location/availability :available]
 [?l :location/backend ?backend-id]
 [?l :xt/id ?path]
 [(score-fn ?backend-id) ?score]]

;; All single-copy data (replication risk)
[:find ?content (count ?l)
 :where
 [?l :location/content ?content]
 [?l :location/availability :available]
 [(< (count ?l) 2)]]

;; Files only on portable disks that aren't currently plugged in
[:find ?path
 :where
 [?l :xt/id ?path]
 [?l :location/availability :transient]
 (not-join [?l]
   [?other :location/content ?c]
   [?l :location/content ?c]
   [?other :location/availability :available])]

;; Public-readable copies of a content (UC6 fallback discovery)
[:find ?path ?url-prefix
 :where
 [?c :content/sha256 "a3f9…"]
 [?l :location/content ?c]
 [?l :location/backend ?b]
 [?b :backend/kind :public-url]
 [?b :backend/url-prefix ?url-prefix]
 [?l :xt/id ?path]]
```

#### Migration from string-typed `:location/backend`

Existing locations have `:location/backend "minio"` as a string (pre-Phase-2.6 schema). Migration is a one-shot Datalog script run as part of Phase 2.6 deployment:

1. Enumerate distinct strings: `[:find ?b :where [_ :location/backend ?b]]`
2. For each string, create a Backend entity with sensible defaults (manually reviewed before commit)
3. Rewrite all locations: `:location/backend "minio"` → `:location/backend "backend:minio-raw"`
4. Single XTDB transaction; idempotent retry-safe

In the no-Khan baseline, this migration runs before any production locations exist (Phase 2.6 lands before Phase 3 scanner writes millions of entities), so the migration is essentially seeding rather than rewriting.

---

## 3. Index freshness — three ingest sources

### A. Event-driven (S3 bucket notifications)
MinIO/RustFS emits `s3:ObjectCreated:*` and `s3:ObjectRemoved:*` events. A lightweight Nomad service job (or a sidecar on an existing service) consumes these and upserts/retracts XTDB entities (Lucene index updates inline as part of the same transaction). Near-realtime for bucket backends. This is **not part of the CLI** — it's a cluster-side service.

### B. Periodic scan (`abc data index refresh`) — multi-pass pipeline

A small Go orchestrator (`internal/indexer/scanner/`) coordinates four passes over established CLI tools (rhash, b3sum, fclones, s5cmd). Each pass writes a small intermediate manifest to `bucket://_abc-scan/<runid>/`; later passes consume earlier ones. Failures restart only the affected pass.

```
                ┌─────────────────────────────────────────────────┐
                │ Pass A: Enumeration + dedup grouping             │
LOCAL    ──►   │  find / stat → size+mtime+inode manifest         │
                │  fclones group --json → dedup groups (BLAKE3)   │
S3       ──►   │  s5cmd ls --etag --recursive                     │
                └─────────────────────────────────────────────────┘
                                    │
                ┌─────────────────────────────────────────────────┐
                │ Pass B: Multi-algorithm hash (MD5 + SHA-256)     │
                │  rhash --md5 --sha256 --bsd in ONE read pass on: │
                │    - one rep per fclones group (propagate to sibs)│
                │    - all singletons                              │
                │    - skipped: unchanged since last scan          │
                └─────────────────────────────────────────────────┘
                                    │
                ┌─────────────────────────────────────────────────┐
                │ Pass C: BLAKE3 augmentation (selective)          │
                │  b3sum --num-threads 0 on:                       │
                │    - files > 1 GiB                               │
                │    - files tagged for archival                   │
                │    - files flagged for deep verify               │
                │  bao encode → outboard tree (Phase 5.7+)         │
                └─────────────────────────────────────────────────┘
                                    │
                ┌─────────────────────────────────────────────────┐
                │ Pass D: Merge + ingest                           │
                │  one content entity per unique hash              │
                │  one location entity per path                    │
                │  hardlinks (same inode) → no extra hashing       │
                │  batched 10k transactions to XTDB                │
                └─────────────────────────────────────────────────┘
```

### Pass A — enumeration + dedup grouping

**Local backends — `fd` (preferred) + `fclones`:**

```
# Pattern A: parallel walk via fd, parallel stat via xargs
fd --type f --no-ignore --hidden --threads 0 -0 . /scratch \
  | xargs -0 -P 8 stat --format='%s	%Y	%i	%n' \
  > size-manifest.tsv

# fclones: parallel Rust dedup finder, native BLAKE3, JSON output
fclones group /scratch --json > dupe-groups.json
```

- **`fd`** (Rust, parallel) does the directory walk: ~5× faster than `find` on million-file trees thanks to multi-threaded walking. Single statically-linked binary — easy to add to the scanner job image.
- `fd --no-ignore --hidden` overrides fd's default `.gitignore` respect (which is surprising for systems use).
- `xargs -P 8 stat` produces the `(size, mtime, inode, path)` tuple — inode capture is critical for hardlink dedup without re-hashing.
- `fd -0` / `xargs -0` for non-UTF-8 path safety.
- **`fclones`** (Rust, multi-threaded) identifies byte-identical groups using BLAKE3 as its internal hash. Native JSON output (`--json`) emits an array of groups, each listing member paths, size, and hash — trivial for the orchestrator to parse. Crucially, `fclones` uses BLAKE3 internally for grouping, so the dedup step also pre-computes BLAKE3 for free on all grouped files, feeding directly into Pass C without a re-read.

**Fallback when fd is unavailable** (POSIX-only environments, locked-down HPC):

```
find /scratch -type f -printf '%s\t%T@\t%i\t%p\n' > size-manifest.tsv
```

The orchestrator detects fd at startup and falls back automatically. Output format from both pipelines is identical, so the parser doesn't care.

**S3 backends:**

```
s5cmd ls --etag --recursive 's3://minio-raw/*' > s3-manifest.ndjson
```

- ETag is MD5 for single-part uploads — **free from S3 metadata, no downloads**.
- Multipart uploads (>5 GB) have composite ETags (MD5-of-MD5s, not usable as file MD5) — orchestrator marks these `:content/md5-source :s3-multipart-composite` and defers real MD5 to Pass B if needed.

### Pass B — multi-algorithm hashing with rhash

```
cat representatives.txt | xargs -P 8 rhash --md5 --sha256 --bsd > hashes.bsd
```

Why rhash here:
- **MD5 + SHA-256 in one read pass** via `--md5 --sha256` — both interop hashes from a single I/O.
- BSD output (`--bsd`) is trivial to parse: `MD5 (path) = hash` / `SHA256 (path) = hash`.
- `xargs -P N` provides file-level parallelism (rhash itself is single-threaded per file, which is fine for I/O-bound workloads).

**Inputs to rhash (orchestrator-computed):**
- One representative per fclones group (hashes propagate to all members → saves N-1 reads per group). Because `fclones` already computed BLAKE3 internally, Pass B only needs to compute MD5 + SHA-256 on the representative — the BLAKE3 value flows from the fclones JSON output.
- All singletons (files with no dupe group).
- Files whose `(size, mtime)` differs from the value in XTDB (incremental skip — most files in a re-scan).

**Group propagation:** for an fclones group of N files, rhash one representative; emit content + location entities for all N referencing the same `:content/sha256`. For corpora with high dedup (reference genome caches, common tools), this is a 5–10× speedup vs hashing every file.

### Pass C — BLAKE3 augmentation (selective)

```
b3sum --num-threads 0 huge-files.txt        > blake3-large.txt
b3sum --num-threads 0 archive-candidates.txt > blake3-archive.txt
```

BLAKE3 is **opt-in per file**, computed only when its Merkle tree pays off:
- Files > 1 GiB (chunk-level heal ROI is meaningful).
- Files tagged for archival.
- Files flagged for deep verification.

Why selective and not universal:
- BLAKE3 multi-threading makes it fast *per file*, but indiscriminate application wastes I/O on small files where the tree advantage doesn't apply.
- For most files (small, frequently-changed) the MD5 + SHA-256 pair is sufficient.

If `bao` CLI is available and the file is selected for streaming verify (Phase 5.7 / Phase 11 FUSE):
```
bao encode huge.bam --outboard huge.bam.bao
```
The `.bao` outboard file is ~0.1% of file size and enables range verification against the BLAKE3 root without re-reading the whole file.

### Pass D — merge + ingest

The orchestrator merges all four intermediate manifests and emits XTDB entities:

```
size-manifest.tsv  ──┐
dupe-groups.json   ──┤   (fclones JSON: groups + BLAKE3 from Pass A)
hashes.bsd         ──┼──► merge & emit content + location entities
blake3-large.txt   ──┤
s3-manifest.ndjson ──┘
```

- One content entity per unique hash (deduplicated across fclones groups, hardlinks, and S3 ETag matches).
- One location entity per path, referencing its content.
- Hardlinks (same `(dev, ino)`) → multiple locations, single content, zero extra hashing.
- Batched in groups of 10k transactions to XTDB; Lucene index updates inline.

### Modes (selected at invocation time)

| Mode | Trigger | Passes run | Use |
|---|---|---|---|
| **Default refresh** | `abc data index refresh` | A (enumeration only, skip fclones if last-scan < 24h) → B (incremental: only changed files) → D | Frequent, cheap; periodic refresh |
| **Bootstrap** | `abc data index refresh --bootstrap` | A (full, including fclones) → B (representatives + singletons) → C (large files, BLAKE3 already from fclones) → D | First-time scan of a backend |
| **Deep / audit** | `abc data index refresh --audit` | A → B (force re-hash everything) → C (force BLAKE3 on everything tagged) → D, plus diff vs prior scan | Periodic integrity audit; Phase 5.7 |
| **S3 metadata-only** | `abc data index refresh --backend minio` | A (s5cmd) → D | S3-only fast pass; SHA-256 lazy |
| **S3 deep** | `abc data index refresh --backend minio --deep` | A → B (downloads + rhash on every object) → D | Compliance runs; expensive |

### Tool roles, summarized

| Tool | What it does in the pipeline |
|---|---|
| **fd + stat** (find as fallback) | Pass A: parallel enumeration with size + mtime + inode (hardlink detection for free); ~5× faster than find on large trees |
| **fclones** | Pass A: confirms byte-identical groups using BLAKE3 (Rust, multi-threaded); native JSON output; also pre-computes BLAKE3 for free — feeds Pass C without re-read |
| **rhash** | Pass B: MD5 + SHA-256 in one read pass; the hashing workhorse |
| **b3sum** | Pass C: selective BLAKE3 for large or archive-bound files |
| **bao** (optional) | Pass C: outboard Merkle tree for streaming/range verify |
| **s5cmd** | Pass A: parallel S3 listing with free ETag (MD5) |
| **(orchestrator, ours)** | Coordinates passes, manages incremental cache, computes "needs hashing" set, merges manifests, emits XTDB transactions |

### Edge cases (handled by the right tool at the right pass)

| # | Edge case | Pass | Tool / behaviour |
|---|---|---|---|
| 1 | Hardlinks (one inode, N paths) | A | `stat --format='%i'` captures inode → orchestrator dedupes without re-hashing |
| 2 | Symlinks | A | `fd` defaults to no-follow; `fd --follow` opt-in; `fclones group --follow-symlinks` for the dedup pass |
| 3 | Sparse files | B/C | rhash + b3sum stream-read; no special handling |
| 4 | Files being written during scan | A→B | Pass B re-stats; mtime change → flag `:integrity/source :scan-in-flux` |
| 5 | Permission errors | A/B | Stderr capture by orchestrator → emit `:location/discovery-error` entity |
| 6 | Network mounts (NFS/FUSE) | A | `--mount-allowlist` to avoid surprise crawls; retry on EIO |
| 7 | Special files (FIFOs, devices) | A | `fd --type f` filters them out |
| 8 | Non-UTF-8 paths | A | `fd -0` / `xargs -0` null-delimited; base64 fallback for storage |
| 9 | Multipart S3 ETag (composite, unusable) | A→B | Orchestrator flags; defers real MD5 to Pass B targeted re-hash |
| 10 | Very large files (>1 TB) | B/C | rhash + b3sum stream; orchestrator checkpoints progress |
| 11 | XFS reflinks / btrfs CoW | A | Inode dedup detects them as duplicates |
| 12 | Concurrent moves during scan | A→D | Atomic capture of `(path, hash, mtime)`; mismatch → re-queue |

### B'. Lazy SHA-256 fill
First time an S3 object is touched by `abc data move` or queried via `abc data check --hash` / `--from-file`, its SHA-256 is computed and persisted to XTDB. This amortises the cost — only "interesting" objects ever pay for SHA-256.

### C. Write-through (CLI finalize phase)
Every successful `abc data move` or `abc data copy` writes to XTDB as the final step before returning exit 0. This ensures CLI-initiated transfers are always indexed immediately, regardless of whether event-driven or scan has run.

---

## 4. Verification + safe move

### Copy
After the tool (s5cmd/rclone) finishes, the Nomad job runs a verify step:
- **S3↔S3**: `s5cmd ls --show-progress` both sides and compare object count + size. s5cmd supports `--checksum` for ETag comparison on same-region copies.
- **Other**: `rclone check --size-only --combined -` (deep: `--checksum`).

### Move (two-phase commit)
`move` never calls `rclone move` or `s5cmd mv`. Instead:

1. **Plan** — enumerate source objects into a NDJSON manifest (written to `abc://_journals/<runid>/manifest.ndjson`).
2. **Copy** — transfer all objects to destination.
3. **Verify** — all manifest objects must be present and size-correct at destination.
4. **Delete-source** — per-object delete; each deletion appended to journal.
5. **Finalize** — write `{"ev":"done"}` to journal; write-through to XTDB.

Journal lives in object storage (`bucket://_abc-journals/<runid>/`) for durability across Nomad reschedules. XTDB records the completed move as a bitemporal transaction.

### Failure modes

| Phase fails | Source state | Journal state | Exit code |
|---|---|---|---|
| Copy | Intact | No `deleted` events | 1 |
| Verify | Intact | Verify events present | 2 |
| Delete | Partially deleted | Each deletion journaled | 3 |

---

## 5. New CLI subcommands

```
abc data move  abc://a abc://b [--via abc://relay/...] [--verify=size|deep] [--dry-run]
abc data copy  abc://a abc://b [--verify=size|deep] [--dry-run]
abc data check <abc://path | remote:path | --hash sha256:… | --from-file /local/path>
abc data index refresh [--backend minio|rustfs|node://name|all]
abc data show  abc://path          # full XTDB lineage timeline
```

### `abc data check` — content-addressed lookup

Answers: "Is this data already known to the cluster, and where?"

**Input forms:**

| Invocation | What happens |
|---|---|
| `abc data check abc://raw/run42/x.bam` | Path lookup in index; show metadata + all known aliases |
| `abc data check minio-raw:raw/run42/x.bam` | Resolve raw rclone path → hash → content lookup |
| `abc data check --hash sha256:a3f92c…` | Direct content lookup by SHA-256 |
| `abc data check --from-file /local/path` | Hash locally (SHA-256 + MD5), query index by SHA-256 |

**Behaviour for `--from-file` (options 1 + 2 combined):**
1. Compute SHA-256 + MD5 on the local file (no cluster involvement; no upload).
2. Query the index by SHA-256.
3. If found → show all indexed locations, tags, lineage.
4. If not found → print `Not indexed.` then prompt: `Register as abc://inbox/<filename>? [y/N]`
   - `y`: submits an upload job + index write, assigns the `abc://inbox/<filename>` path.
   - `n`: exits 0 with message `Not indexed. Re-run with --register to add.`

**Output (found):**
```
✓ Indexed
  abc://raw/run42/sample.bam
    backend:  minio
    size:     12.4 MB
    sha256:   a3f92c…
    md5:      d41d8c…
    indexed:  2026-04-15 09:12 UTC
    tags:     [genomics, run42]

  Also found at:
    abc://cold/archive/run42/sample.bam  (moved from above 2026-04-20)
```

**Output (not found):**
```
✗ Not indexed.
  sha256: a3f92c…  md5: d41d8c…  size: 12.4 MB
  Register as abc://inbox/sample.bam? [y/N]
```

### Hash strategy (dual hash)

Both hashes stored per index entity:

| Hash | Role |
|---|---|
| **MD5** | S3 ETag fast-path. Free for objects already in MinIO/RustFS (no re-read). Used for fast dedup checks between S3 buckets. |
| **SHA-256** | Canonical content identity. Cross-backend primary key for content-addressed lookup. Required for `--from-file` and `--hash` queries. |

XTDB entity fields: `:abc/md5` and `:abc/sha256`. Content lookup query: `[:find ?path :where [?e :abc/sha256 <hash>] [?e :xt/id ?path]]`.

---

## 6. File-by-file change list

| File | Change | ~LOC |
|---|---|---|
| `internal/indexer/xtdb.go` (new) | XTDB HTTP client wrapper: typed Go fns over canonical Datalog queries (`Resolve`, `LookupByHash`, `ListByPrefix`, `ListByTag`, `Lineage`, `Upsert`, `Retract`) | +280 |
| `internal/indexer/xtdb_test.go` (new) | fake XTDB transactor; query round-trips; Lucene prefix test | +160 |
| `internal/indexer/scan.go` (new) | s5cmd + rhash output parsers; NDJSON → batch transactions; lazy-SHA-256 fill helper | +200 |
| `internal/indexer/scan_test.go` (new) | golden parser tests for s5cmd + rhash output | +100 |
| `cmd/data/index.go` (new) | `abc data index refresh` subcommand; submits scan Nomad job | +120 |
| `cmd/data/transfer.go` | Call `ResolvePath` on from/to; add `selectTool()`; add `--verify`, `--via`; split `runTransfer` into `runCopy`/`runMove` with phase protocol; write-through in finalize | +250 / −30 |
| `cmd/data/transfer_relay.go` (new) | Two-job relay orchestration; cleanup job submission; run ID tracking | +180 |
| `cmd/data/journal.go` (new) | NDJSON writer/reader, event types, S3-backed appender, `pickJournalBucket` | +200 |
| `cmd/data/nomad_submit.go` | Accept `HostVolumes`, s5cmd tool variant, `SelectToolScript()` helper | +60 |
| `cmd/data/check.go` (new) | `abc data check`: local hash, index query, not-found prompt + register flow | +200 |
| `cmd/data/check_test.go` (new) | found/not-found/register flows; fake index client | +120 |
| `cmd/data/cmd.go` | Wire `index refresh`, `show`, `check`, new flags | +40 |
| `internal/hclgen/job/generator.go` | `HostVolumeMount` + `HostVolumes` on `Spec`; emit `volume`/`volume_mount` blocks | +35 |
| `internal/hclgen/job/generator_test.go` | Golden-file test for host-volume HCL emission | +40 |

**Total:** ~1,985 LOC. 5–7 days for one engineer.

---

## 6a. User experience walkthrough

**Initial scan (one-time bootstrap or scheduled refresh):**
```
$ abc data index refresh --backend all
Submitted Nomad job: idx-refresh-2026-04-28-1

[minio]   s5cmd ls --etag                4,231,089 objects   1m 42s   md5 only
[rustfs]  s5cmd ls --etag                  891,224 objects      22s   md5 only
[node://gpu-03]  rhash -r --md5 --sha256  312,900 files     3m 08s   md5 + sha256

Transacting to XTDB-Lucene (batches of 10k)... ✓ 532 batches

Index now has 5,435,213 entities  (new 12,084 / updated 891 / removed 432)
```

**Content lookup from a local file (no upload):**
```
$ abc data check --from-file ~/Downloads/sample.bam
Hashing locally... md5:d41d8c… sha256:a3f92c… (12.4 MB, 1.2s)

✓ Indexed (3 locations, query 14ms)

  abc://raw/run42/sample.bam            minio   2026-04-15
  abc://cold/archive/run42/sample.bam   rustfs  2026-04-20  (moved from raw/)
  abc://node-gpu03/scratch/sample.bam   node    2026-04-25  (copy)

  Tags: [genomics, run42, qc-passed]
  Use `abc data show abc://raw/run42/sample.bam` for full timeline.
```

**Local file not in index:**
```
$ abc data check --from-file ~/Downloads/new-thing.bam
Hashing locally... md5:e1d7… sha256:b22c… (88 MB, 4.1s)

✗ Not indexed.
  Register as abc://inbox/new-thing.bam? [y/N] y
  Submitted: abc data upload + index write
  Job: data-upload-2026-04-28-7
```

**Lineage timeline (XTDB bitemporal):**
```
$ abc data show abc://raw/run42/sample.bam
  2026-04-15 09:12  CREATED  via `abc data upload`  (12.4 MB, sha256:a3f92c…)
  2026-04-15 09:13  TAGGED   +[genomics, run42]
  2026-04-18 14:22  VERIFIED sha256 matches after qc pipeline run pl-887
  2026-04-20 03:00  MOVED    → abc://cold/archive/run42/sample.bam
  2026-04-25 11:45  COPIED   → abc://node-gpu03/scratch/sample.bam (run sm-1102)
```

**Search (Lucene prefix + Datalog filters):**
```
$ abc data ls "abc://raw/run42/*"
abc://raw/run42/sample.bam     12.4 MB   minio   2026-04-15
abc://raw/run42/sample.bai     124 KB    minio   2026-04-15
abc://raw/run42/manifest.json    2 KB    minio   2026-04-15
3 results (8ms, Lucene prefix)

$ abc data ls --tag genomics --backend minio --since 2026-04-01
abc://raw/run42/sample.bam     12.4 MB   2026-04-15
abc://raw/run43/sample.bam     15.1 MB   2026-04-22
2 results (12ms, Datalog)
```

**Move with index write-through:**
```
$ abc data move abc://raw/run42/ abc://cold/archive/run42/
Resolving... ✓
  src: minio backend (4 objects, 28.6 MB)
  dst: rustfs backend
  tool: s5cmd  (S3 → S3)

Submitted Nomad job: data-move-2026-04-28-9
  Phase 1: plan + manifest    ✓ (4 objects journaled)
  Phase 2: s5cmd cp           ✓ (28.6 MB in 3.2s)
  Phase 3: verify ETags       ✓ (4/4 match)
  Phase 4: s5cmd rm source    ✓
  Phase 5: write-through XTDB ✓ (4 entities updated, lineage recorded)

Done. Journal: bucket://minio/_abc-journals/data-move-2026-04-28-9/
```

**Recommended notebook environment for new analysis work: Marimo.** When `abc data show <abc-path>` displays an output that was produced by an analysis notebook (rather than a Nextflow pipeline), the linked notebook URL points to a Marimo session by default. Jupyter remains supported for legacy notebooks; Marimo is recommended for new work because its reactive cell DAG eliminates Jupyter's hidden-state reproducibility problem and `.py` notebook files diff cleanly in code review. The system itself is notebook-agnostic — this is a recommendation, not a requirement.

---

## 6b. Annotation + provenance layer

The base entity schema covers identity (path, hash, size, backend) and CLI transfer history. Two additional entity kinds turn the index into a full data lineage system suitable for biological/scientific use.

### Annotation: `bio/*` namespace on file entities

XTDB has no enforced schema — biological metadata is added as namespaced keys on existing file entities, indexed by Lucene where useful.

```clojure
{:xt/id           "abc://raw/run42/sample.bam"
 ;; … existing :abc/* identity fields …
 :bio/organism      "Homo sapiens"
 :bio/assay         "WGS"
 :bio/library-prep  "Illumina TruSeq DNA"
 :bio/genome-build  "GRCh38"
 :bio/sample-id     "S00123"
 :bio/donor-id      "D456"
 :bio/study         "study-abc-001"
 :bio/coverage      30.5
 :bio/qc-status     :pass
 :bio/file-format   "bam"
 :bio/conditions    ["tumor" "primary"]
 :bio/ontology-refs {:assay    "EFO:0003744"
                     :organism "NCBITaxon:9606"}}
```

**Controlled vocabulary registry** — itself a set of XTDB entities (`:bio/Vocabulary`) defining allowed values per field. Validation runs in `internal/indexer/` at write time; unknown values raise an error or warning depending on field strictness. Prevents tag drift across users and teams.

**Lucene-indexed annotation fields**: `:bio/assay`, `:bio/organism`, `:bio/study`, `:bio/sample-id`, `:bio/file-format`, `:abc/tags`. Other fields are queryable via Datalog but not indexed.

### New CLI:

```
abc data annotate <abc://path | --hash sha256:…>
                  [--bio.assay WGS] [--bio.organism "Homo sapiens"]
                  [--tag qc-passed] [--location-tag ops-priority]
                  [--from-tsv samples.tsv]
                  [--content-level | --location-level]   ; explicit override
abc data ls --bio.assay WGS --bio.organism "Homo sapiens" --tag qc-passed
abc data vocab list | add | validate
```

**Default routing of annotations** (matches the content/location split in §2):
- `--bio.*` → content entity (propagates to all copies — this is what users want for biology).
- `--tag` → location entity by default (per-path operational tags).
- `--content-level` / `--location-level` flags force placement when the default is wrong.

`--from-tsv` matches rows to files by abc-path prefix OR SHA-256 column. SHA-256 matches write to content (one row updates many locations); path matches write to location (one row, one path) unless `--content-level` is set.

### Provenance: PROV-aligned activity + agent entities

Move/copy history is *transfer* provenance, not derivation provenance. Real biological lineage is a graph: raw reads → trimmed reads → alignments → variant calls. The W3C PROV model captures this with three entity kinds (Entity, Activity, Agent) — battle-tested by Galaxy, GA4GH WES, and RO-Crate.

```clojure
;; Activity (a pipeline run)
{:xt/id            "act:pipeline-run-887"
 :prov/type        :activity
 :prov/started     #inst "2026-04-18T10:00:00Z"
 :prov/ended       #inst "2026-04-18T14:22:00Z"
 :prov/used        ["abc://raw/run42/R1.fastq.gz"
                    "abc://raw/run42/R2.fastq.gz"]
 :prov/generated   ["abc://align/run42/sample.bam"
                    "abc://align/run42/sample.bai"]
 :prov/agent       "agent:nf-core-sarek-3.4.0"
 :pipeline/name    "nf-core/sarek"
 :pipeline/version "3.4.0"
 :pipeline/params  {…}}

;; File entity gains derivation edges
{:xt/id              "abc://align/run42/sample.bam"
 :prov/wasGeneratedBy "act:pipeline-run-887"
 :prov/wasDerivedFrom ["abc://raw/run42/R1.fastq.gz"
                       "abc://raw/run42/R2.fastq.gz"]
 :prov/wasAttributedTo "agent:nf-core-sarek-3.4.0"}

;; Agent (pipeline, user, or service)
{:xt/id          "agent:nf-core-sarek-3.4.0"
 :prov/type      :agent
 :agent/kind     :pipeline
 :agent/name     "nf-core/sarek"
 :agent/version  "3.4.0"}
```

**Where activities are written from:**
- `abc submit` / `abc job run` / `abc pipeline run` finalize phase — each successful run creates an `:activity` entity referencing its inputs (`:prov/used`) and outputs (`:prov/generated`).
- Bulk import from existing pipeline reports (Nextflow `trace.txt`, Snakemake reports) via `abc data lineage import`.

**New CLI:**

```
abc data lineage abc://path --upstream     # ancestors (what this was derived from)
abc data lineage abc://path --downstream   # descendants (what was derived from this)
abc data lineage abc://path --as-prov      # emit W3C PROV-N or PROV-JSON
abc data lineage import --from <trace.txt|nextflow|snakemake>
```

Datalog handles transitive graph traversal natively — no recursion in Go.

### Scalability ceiling (explicit limits)

| Dimension | Comfortable | Strained — needs sharding or change |
|---|---|---|
| File entities | < 50 M | > 100 M |
| Lineage depth | < 20 hops | > 50 hops |
| Lineage fan-out (one activity) | < 10k outputs | > 100k |
| Lucene index size | < 50 GB | > 200 GB (heap pressure) |
| Annotation write rate | thousands/sec | tens of thousands/sec |

For single-institution genomics (< 500k samples, < 10M files), this is comfortably green. Consortium-scale corpora (UK Biobank, TCGA aggregates) would warrant a sharded XTDB topology or Datomic Pro evaluation — a year-3 problem, not v1.

### Additional file changes for annotation + provenance

| File | Change | ~LOC |
|---|---|---|
| `internal/indexer/annotate.go` (new) | bio/* key validation against vocabulary; TSV parser; batch annotate | +220 |
| `internal/indexer/annotate_test.go` (new) | validation, TSV import, vocabulary mismatch | +130 |
| `internal/indexer/lineage.go` (new) | typed Go fns for upstream/downstream Datalog graph traversal; PROV-N/PROV-JSON emitters; pipeline trace importers | +260 |
| `internal/indexer/lineage_test.go` (new) | graph traversal, PROV emission round-trip | +140 |
| `cmd/data/annotate.go` (new) | `abc data annotate`, `abc data vocab` subcommands | +150 |
| `cmd/data/lineage.go` (new) | `abc data lineage` subcommand with `--upstream`/`--downstream`/`--as-prov`/`import` | +160 |
| `cmd/job/run.go`, `cmd/pipeline/run.go`, `cmd/submit/...` | finalize hook that writes `:activity` entities on successful run completion | +80 (across files) |

**Annotation + provenance subtotal:** ~1,140 LOC. Adds 4–5 days to the implementation timeline.

**Revised total:** ~3,125 LOC across both phases. Suggested split:
- **Phase 1 (5–7 days):** `abc://` namespace, indexed move/copy, safe move with journal, `abc data check`, `abc data show`.
- **Phase 2 (4–5 days):** annotation, vocabulary, PROV-aligned lineage, `abc data lineage`.

Phase 1 ships the user-facing `data move` improvements; Phase 2 layers the scientific provenance system on top of the same XTDB index.

---

## 6c. Nextflow lineage integration (Phase 2.5)

Nextflow 24.10+ has a first-class lineage subsystem (`nextflow lineage` CLI) that records every task execution as a content-addressed `lid://<sha256>` event with inputs, outputs (with hashes), container, command, params, runtime. Because Nextflow lineage IDs are SHA-256 based — the same hash we store in `:abc/sha256` — integration is mostly **mapping**, not redesign.

### Mapping

| Nextflow | XTDB | Linkage |
|---|---|---|
| `lid://<sha256>` (task) | `:xt/id "act:nf-<sha256>"` | Reuse lid as activity ID |
| `lid://<sha256>` (file output) | existing `abc://...` file entity | Match by `:abc/sha256` (no name reconciliation) |
| `task.process` | `:pipeline/process` on activity | Direct copy |
| `container` | `agent:container:<digest>` (`:prov/Agent`) | One agent per container digest |
| Workflow run | `act:nf-workflow-<runId>` | Parent activity; tasks ref via `:prov/wasInformedBy` |

### Three integration paths (recommend B + C together)

| Path | Mechanism | Trigger | Use |
|---|---|---|---|
| **A. PROV import bridge** | `nextflow lineage render --format prov` → existing `abc data lineage import --from prov` | Manual | Backfill legacy runs |
| **B. Sidecar ingester** | Nomad service watches a shared `.nextflow/lineage/` location (or `bucket://_nf-lineage/`) and ingests events to XTDB | Near-realtime, automatic | Default for all runs |
| **C. Nextflow plugin** | `nf-abc-lineage` plugin pushes events via Nextflow operator API during workflow execution | Real-time, per-task | Powers `abc data lineage --watch` |

**Recommendation: B + C together.** B is the bulletproof default (works for every Nextflow run regardless of plugin presence, including legacy). C adds real-time visibility for users watching active pipelines.

### Files for Phase 2.5

| File | Change | ~LOC |
|---|---|---|
| `internal/indexer/nflineage.go` (new) | Parse Nextflow lineage event format → XTDB activity/agent transactions; lid↔abc:// resolver via SHA-256 | +280 |
| `internal/indexer/nflineage_test.go` (new) | Golden tests on real Nextflow lineage event fixtures | +160 |
| `services/nf-lineage-ingester/` (new) | Nomad service: watch lineage dir/bucket, debounce, batch transact | +220 |
| `plugins/nf-abc-lineage/` (new, separate Nextflow plugin) | Groovy plugin hooking workflow lifecycle → POST events to XTDB | +300 |
| `cmd/data/lineage.go` | Add `--source nextflow`, `--watch`, `--activity-search`, `--pipelines-only` flags | +60 |

**Phase 2.5 subtotal:** ~1,020 LOC. 4–5 days.

### What this unlocks

Every Nextflow / nf-core run automatically becomes part of the XTDB lineage graph with task-level granularity, container digests, and parameter trees — no user action required. Cross-pipeline queries (`abc data lineage --derived-from-sample S00123 --leaves-only`) and activity-level queries (`--activity-search "container=gatk4:4.4.0.0"`) work out of the box because activities are first-class XTDB entities sharing the same query layer as files.

**Revised total with Phase 2.5:** ~4,145 LOC. Suggested rollout:
- **Phase 1 (5–7d):** `abc://` namespace, indexed move/copy, safe move + journal, `check`, `show`.
- **Phase 2 (4–5d):** annotations + vocabulary + PROV lineage + `lineage` command.
- **Phase 2.5 (4–5d):** Nextflow lineage sidecar + plugin.

---

## 6d. `abc data search` — full query surface (Phase 1.5)

`abc data ls` handles prefix/glob (Unix-familiar, fast path). `abc data search` is the **full query interface** over every dimension XTDB-Lucene knows about: paths, tags, biological annotations, hashes, sizes, time ranges, provenance edges. Both back onto the same index; `ls` is a thin wrapper for the prefix-only case.

### Design principles

- **Flags first, DSL second.** Composable filter flags handle 90% of queries. An optional `--query` Lucene-like DSL is an opt-in escape hatch for power users.
- **Don't merge `ls` and `search`.** `ls` stays simple. Users typing `abc data ls abc://raw/run42/` should not face a query DSL.
- **Server-side narrowing, client-side regex.** Lucene + Datalog narrow the candidate set; regex applies client-side after to avoid pathological server-side matches.

### CLI surface

```
abc data search [<pattern>] [filters] [--query <dsl>] [--sort <spec>]
                [--format table|json|ndjson|paths|count|stats]
                [--group-by <field>] [--limit N] [--save <name>] [--run <name>]

# Pattern forms (any one, optional)
<pattern>             glob (default if positional given)
--regex <re>          regex (client-side after narrowing)
--text <q>            Lucene full-text on annotations + path
--hash <prefix>       md5/sha256 prefix or exact
--contains <substr>   path substring

# Faceted filters (AND semantics, repeatable)
--tag <name>                       (repeatable)
--backend minio|rustfs|node://name (repeatable; OR within field)
--bio.<key> <value>                e.g. --bio.assay WGS, --bio.organism "Homo sapiens"
--format-ext bam|vcf|fastq.gz      (file format)
--qc-status pass|fail|unknown
--since <ts> / --until <ts>        mtime by default; --time-field generated-at|indexed-at|mtime
--min-size 1GB / --max-size 10GB   human units

# Provenance filters
--upstream-of <abc://path>          ancestors (transitive)
--downstream-of <abc://path>        descendants (transitive)
--leaves-only                       no further descendants
--roots-only                        no ancestors
--produced-by-pipeline <name>       e.g. "nf-core/sarek"
--produced-by-agent <agent-id>
--derived-from-sample <sample-id>

# Output
--format paths      one path per line (xargs-friendly)
--format json       JSON array
--format ndjson     one JSON object per line
--format count      just the integer
--format stats      count + total/avg size + per-facet breakdowns

# Aggregation
--group-by backend|bio.assay|bio.organism|format-ext|produced-by-pipeline
--group-by-time day|week|month        (histogram buckets on mtime)

# Saved searches (XTDB entities :xt/id "search:<name>")
--save <name>     persist current filter set
--run  <name>     execute a saved search (filters merge with new flags)
--list-saved
--describe <name>
```

### Examples

```
# Glob + facet + provenance
abc data search "*.bam" --tag genomics --bio.assay WGS \
  --backend minio --since 2026-04-01 --min-size 1GB

# Provenance leaves
abc data search --downstream-of abc://raw/run42/R1.fastq.gz --leaves-only

# Aggregation
abc data search --bio.assay WGS --group-by backend --format stats

# DSL escape hatch
abc data search --query 'tag:genomics AND bio.assay:WGS AND size:>1GB AND mtime:[2026-04-01 TO *]'

# Pipe to other tools
abc data search "*.vcf.gz" --tag genomics --format paths | xargs abc data move ... abc://archive/
```

### Output examples

```
$ abc data search "*.bam" --tag genomics --since 2026-04-01 --limit 5
PATH                                  SIZE    BACKEND  MTIME       TAGS
abc://raw/run42/sample.bam           12.4MB  minio    2026-04-15  genomics, run42
abc://raw/run43/sample.bam           15.1MB  minio    2026-04-22  genomics, run43
abc://align/run42/sample.bam        890.2MB  minio    2026-04-18  genomics, aligned
abc://align/run43/sample.bam        912.7MB  minio    2026-04-22  genomics, aligned
abc://cold/archive/run40/sample.bam  11.8MB  rustfs   2026-04-03  genomics, archived
5 results (8ms, Lucene+Datalog).

$ abc data search --bio.assay WGS --group-by backend --format stats
BACKEND   COUNT  TOTAL SIZE  AVG SIZE
minio       247   3.2 TB     13.0 GB
rustfs      891  11.4 TB     12.8 GB
node://*     12 148.3 GB     12.4 GB
Total: 1,150 files, 14.7 TB
```

### Implementation

| Component | File | LOC |
|---|---|---|
| Filter struct + flag parsing | `cmd/data/search.go` | +250 |
| `BuildQuery(Filters) []Clause` (pure flag→Datalog mapper) | `internal/indexer/search.go` | +220 |
| Lucene text-search wrappers | `internal/indexer/lucene.go` (extends Phase 1) | +60 |
| Aggregation queries (count/sum/group-by) | `internal/indexer/aggregate.go` | +140 |
| Output formatters (table/json/ndjson/paths/count/stats) | `cmd/data/search_format.go` | +160 |
| Saved-search XTDB entities + `--save`/`--run`/`--list-saved` | `internal/indexer/saved.go` | +120 |
| Optional DSL parser (`--query`) | `internal/indexer/querydsl.go` | +200 |
| Tests | `*_test.go` siblings | +280 |

**Subtotal:** ~1,430 LOC. Phase 1.5 (4–5 days). Lands after Phase 1 (index exists) and before Phase 2 (biological filters become useful once annotations are in).

**Revised rollout with all phases:**
- **Phase 1 (5–7d):** `abc://`, indexed move/copy, safe move, `check`, `show`.
- **Phase 1.5 (4–5d):** `abc data search` over the base index.
- **Phase 2 (4–5d):** annotations, vocabulary, PROV lineage, `lineage` command. (Search gains `--bio.*` and provenance filters automatically.)
- **Phase 2.5 (4–5d):** Nextflow lineage sidecar + plugin.

**Final total:** ~5,575 LOC.

---

## 7. Phased rollout — what to ship and how to test it

Each phase below is **self-contained and shippable**: a clear infra delta + CLI delta + a concrete test gate. Phases ship in order; later phases assume earlier ones are deployed and verified. Earlier sections of this document are the **design reference**; this section is the **execution checklist**.

Total estimate across all phases: **~40 engineer-days** spread over 9 phases. Each phase is sized to land within a single sprint.

---

### Phase 0.5 — Instrument watcher: automated ingest from sequencing machines ~4d

**Goal.** Eliminate the manual handoff between sequencing instruments and the cluster. When a sequencing run completes, data moves automatically to `abc://inbox/sequencing/<run-id>/` with full provenance captured — no operator action required.

**Why Phase 0.5 (before Phase 1).** This is the *entry point* for the system's data. Without automated ingest from instruments, every downstream capability (indexing, lineage, annotation, chat) requires users to manually trigger transfers. The instrument watcher makes all downstream phases genuinely automatic from the source.

**Infra.**
- New Nomad **system job** `abc-instrument-watcher` — runs on every storage/edge node that has a NAS or instrument network share mounted. System job ensures it's always present on eligible nodes.
- The watcher needs access to the NAS mount and to Khan (for submitting transfer jobs). No direct XTDB access — all writes go through abc-data-api (available from Phase 3) or the CLI write-through (available from Phase 3). In Phase 0.5, the watcher submits transfer jobs only; provenance XTDB writes are wired in Phase 3.

**How completion detection works per platform:**

| Instrument platform | Sentinel file | Location in run dir |
|---|---|---|
| Illumina (NovaSeq, NextSeq, MiSeq) | `RTAComplete.txt` | Run root |
| Oxford Nanopore (PromethION, GridION, MinION) | `final_summary_*.txt` (glob) | Run root |
| PacBio (Revio, Sequel IIe) | `.transferdone` | Run root |
| Generic rsync / SCP push | configurable sentinel filename | Configurable path |

The watcher uses `fsnotify` (Go library wrapping Linux `inotify` / macOS FSEvents) to watch configured instrument output directories for these specific file names. A configurable **stabilisation delay** (default: 60s) ensures no new files have appeared before triggering — catches platforms that write the sentinel before all data is flushed.

**What the watcher does on completion:**

```
1. Detect RTAComplete.txt (or platform equivalent)
2. Parse instrument metadata (platform-specific):
   Illumina:  RunInfo.xml  → {instrument_id, run_id, flowcell_id, cycles, read_lengths}
              SampleSheet.csv → [{sample_id, library_id, index_sequence, project}]
   Nanopore:  sequencing_summary_*.txt → {device_id, run_id, protocol, basecall_model}
              final_summary_*.txt     → {sample_id, experiment_name, reads_passed}
   PacBio:    .metadata.xml → {instrument_serial, run_name, sample_name, library_name}
3. Write provenance entity to Khan /api/v1/data/index/sequencing-run (Phase 3+)
   or queue locally for Phase 3 write (Phase 0.5 only persists to a local journal)
4. Submit abc data copy job:
   POST /v1/jobs → Khan → Jurist → Nomad
   src: /mnt/instruments/runs/<run-dir>/
   dst: abc://inbox/sequencing/<run-id>/
   --driver exec (or docker with volume mount)
   output_tags: {abc-ingest-source: "instrument", abc-run-id: "<run-id>"}
5. On job completion (polled via Nomad API):
   Mark run as transferred in local watcher state (prevents re-trigger on re-mount)
   Write transfer completion event (Phase 3+: to XTDB via abc-data-api)
```

**Provenance entity written to XTDB (Phase 3 wiring):**

```clojure
{:xt/id               "act:sequencing:<run-id>"
 :prov/type           :activity
 :prov/started        #inst "2026-04-28T09:00:00Z"   ; from RunInfo.xml
 :prov/ended          #inst "2026-04-28T21:12:00Z"   ; RTAComplete.txt mtime
 :prov/wasAssociatedWith #{"agent:instrument:novaseq-001"
                           "agent:user:lab-operator"}  ; if SampleSheet has operator
 :pipeline/name       "illumina-sequencing"
 :pipeline/params     {:flowcell     "H5JKWDSX7"
                       :chemistry    "NovaSeq Standard v1.5"
                       :cycles       [151 10 10 151]
                       :read-mode    "paired-end"
                       :instrument   "NV00123"}
 :bio/sample-detail   [{:sample-id   "S00123"
                        :library-id  "LIB-456"
                        :index-seq   "ATCACG+TTAGCC"
                        :project     "study-abc-001"}]
 :prov/generated      #{"abc://inbox/sequencing/<run-id>/"}
 :audit               {:created-by "agent:service:instrument-watcher"}}
```

**Watcher configuration** (`/etc/abc/instrument-watcher.yaml` on host, or Nomad task env):

```yaml
watch_dirs:
  - path: /mnt/novaseq-001/runs
    platform: illumina
    dest_prefix: abc://inbox/sequencing
    stabilise_secs: 60
    transfer_driver: exec          # exec uses node rclone; docker uses container image
    instrument_id: novaseq-001

  - path: /mnt/nanopore-01/data
    platform: nanopore
    dest_prefix: abc://inbox/nanopore
    stabilise_secs: 120
    sentinel_pattern: "final_summary_*.txt"   # glob for nanopore

khan_url: https://khan.abc-cluster.internal
khan_token: ${WATCHER_SERVICE_TOKEN}           ; long-lived PAT from Vault
state_file: /var/lib/abc-instrument-watcher/state.json  ; tracks transferred runs
```

**Error handling:**
- If the transfer job fails (Nomad alloc error), the watcher retries with exponential backoff (10m, 30m, 2h).
- The local state file (`state.json`) records `{run-id → status: pending|transferred|failed}` — prevents re-triggering on watcher restart.
- Operator notification: on persistent failure (3 retries), fires an ntfy notification via Khan's existing notification mechanism.

**Files.**
- `services/abc-instrument-watcher/main.go` — fsnotify watch loop, sentinel detection, stabilisation timer
- `services/abc-instrument-watcher/platform/illumina.go` — RunInfo.xml + SampleSheet.csv parsers
- `services/abc-instrument-watcher/platform/nanopore.go` — final_summary + sequencing_summary parsers
- `services/abc-instrument-watcher/platform/pacbio.go` — .metadata.xml parser
- `services/abc-instrument-watcher/state.go` — local state journal (JSON file, atomic rename writes)
- `services/abc-instrument-watcher/nomad_submit.go` — Khan job submission client
~600 LOC total, plus tests.

**Test gate.**
1. Create a temp directory simulating an Illumina run; write files incrementally; drop `RTAComplete.txt` → watcher fires within stabilisation window + 5s.
2. Parsed `RunInfo.xml` fields match expected values; sample sheet produces correct `[:bio/sample-detail]` entries.
3. Transfer job submitted to Khan with correct `src`, `dst`, `output_tags`.
4. Watcher restart (process kill + relaunch) → already-transferred run not re-submitted (state file respected).
5. Failed transfer → retry with backoff → ntfy notification fired after 3 failures.
6. Two different run directories complete simultaneously → both submitted independently without race.

---

### Phase 1 — Robust transfers (CLI only) ~5d

**Goal.** Eliminate destructive `move` failures; cover every location pair; introduce relay support. Ships value immediately without any index dependency.

**Infra.** None. Uses existing Nomad + MinIO/RustFS.

**CLI.**
- Host-volume HCL support in `internal/hclgen/job/generator.go` (new `HostVolumeMount` field, `volume`/`volume_mount` block emission, `#ABC --host-volume=name:/dest[:ro]` directive).
- `--via <rclone-remote-or-path>` single-hop relay in `cmd/data/transfer.go` + new `cmd/data/transfer_relay.go` (two-job orchestration + cleanup).
- Two-phase safe move (plan → copy → verify → delete) in `cmd/data/transfer.go`, replacing today's `rclone move`.
- NDJSON journal in object storage: new `cmd/data/journal.go`, S3-backed via `internal/floor`.
- Tool routing: `selectTool(srcRcloneRemote, dstRcloneRemote)` chooses s5cmd for S3-involved hops, rclone otherwise. `nomad_submit.go` gains s5cmd script variant.
- Verify step: `s5cmd ls --etag` comparison for S3↔S3, `rclone check --size-only` otherwise; `--verify=deep` upgrades to checksum.

**Files.** `cmd/data/transfer.go`, `cmd/data/transfer_relay.go` (new), `cmd/data/journal.go` (new), `cmd/data/nomad_submit.go`, `internal/hclgen/job/generator.go`, plus tests. ~1,000 LOC.

**Test gate.**
1. Smoke matrix — bucket↔bucket (s5cmd), bucket↔node (s5cmd + host-volume), node↔node via `--via bucket://relay/$RUN` (rclone phase-1 + s5cmd phase-2).
2. Failure injection — kill alloc mid-copy → journal lacks `done` → re-run errors with journal URI; corrupt destination object → verify exits 2, source intact.
3. Cleanup — relay phase succeeds → relay prefix empty after cleanup job.

---

### Phase 2 — Index platform (infra-heavy, no CLI surface yet) ~3d

**Goal.** Stand up XTDB-Lucene + bucket event topic. No user-facing CLI yet — this phase exists so the next phases have something to write to.

**Infra.**
- Deploy XTDB-Lucene as a Nomad service (HTTP endpoint, persistent volume for tx-log + Lucene index, backup snapshot job).
- Configure MinIO and RustFS bucket notification topics (target: a queue or HTTP webhook the Phase-5 ingester will consume).
- Provision a dedicated `_abc-journals` and `_abc-index` bucket structure.
- **Deploy OpenTelemetry collector** as a Nomad service — establishes the cross-service tracing baseline before any data-platform service ships. Every new service from this phase forward is required to emit OTel-compliant spans for its inbound RPCs and XTDB queries; CLI requests propagate a trace ID through Khan → Jurist → abc-data-api → XTDB. Without this baseline, debugging "why did this query take 3 seconds?" across 10+ services becomes archaeology.

**CLI.** Single thin command for operators: `abc data index status` — reports XTDB connectivity, Lucene index size, last successful scan/event timestamp.

**Files.** `cmd/data/index_status.go`, deployment HCL under `infra/` (or wherever this stack's infra lives — TBD by reading the deploy structure when Phase 2 starts), OTel collector job spec + initial instrumentation in shared client libraries. ~400 LOC.

**Test gate.**
1. `curl <xtdb>/_xtdb/query` round-trips a trivial Datalog query.
2. PUT a test object to MinIO → event arrives at a debug HTTP sink.
3. `abc data index status` prints non-error output with timestamps.

---

### Phase 2.6 — Backend + Host entity refactor ~3d

**Goal.** Promote `:location/backend` from a string into a reference to a Backend entity; introduce Host entity. Stabilise the schema before Phase 3 starts writing millions of locations.

**Why now (between Phase 2 and Phase 3).** The schema needs to be settled before the scanner runs at scale. Migrating string-typed backends to entity references after Phase 3 means rewriting millions of locations; doing it before Phase 3 means seeding ~20 Backend entities and a handful of Host entities, then Phase 3 writes locations against the final shape.

**Infra.** None new (just XTDB schema additions).

**Schema additions** (full content in §2 *Multi-location data model*):
- New `:backend/*` namespace; Backend entity per backend instance the cluster knows about
- New `:host/*` namespace; Host entity for laptops, compute nodes, NAS servers
- Backend kinds enumerated: `:s3 | :host-fs | :portable-disk | :workstation | :nas | :restic-repo | :public-url | :drs | :tape-archive | :instrument-storage`
- `:location/backend` becomes a reference to a Backend entity; old string values migrated

**Files.**
- `analysis/packages/abc-data-shared/entities/backend.go`, `host.go` — new entity types
- `analysis/packages/abc-data-api/internal/migrations/0001_backend_host.go` — one-shot Datalog migration
- `infra/identity/backends.yaml` — initial Backend/Host seed data (cluster MinIO, RustFS, Garage; per-node host-fs backends; reference-data public URL backends), reconciled by identity-seed (Sprint 0)
- ~600 LOC.

**Test gate.**
1. Migration script enumerates distinct existing `:location/backend` strings and creates corresponding Backend entities (no production data yet → trivial in no-Khan baseline).
2. Backend reconciler is idempotent: re-running identity-seed produces no diff.
3. A query joining `:location/backend ?b` and `:backend/access-profile ?ap` returns access metadata (latency, egress cost) without duplication.
4. Host with `:last-seen-at` older than 5 minutes flips its dependent backends to availability degraded; locations on those backends inherit the change on next read.

---

### Phase 2.7 — Location availability + completeness + at-rest encryption ~2d

**Goal.** Add availability state, partial-copy representation, and at-rest-encryption awareness so the scanner (Phase 3) and ingesters (Phase 5+) record locations correctly from day one.

**Schema additions:**
- `:location/availability` state machine (`:available | :transient | :offline | :missing-since-scan | :unreachable | :requires-mount`)
- `:location/last-verified-available  #inst "…"`
- `:location/completeness {:state :complete|:partial|:downloading :ranges-present [...] :resumable bool :resume-token "..."}`
- `:location/at-rest-encryption {:scheme :crypt4gh|:age|:s3-sse|:luks-volume|:none :key-ref "..." :public-key "..." :integrity-via :encrypted-bytes-sha256 :verified-decryptable-at #inst "..."}`

**Integrity-check pipeline change:** when a location has non-`:none` `:at-rest-encryption/scheme`, hash the ciphertext bytes and compare against `:integrity/observed-encrypted-content-sha256` instead of the content's plaintext SHA-256. This prevents false-positive divergence on every encrypted location.

**Files.**
- `analysis/packages/abc-data-api/internal/integrity/encrypted.go` — ciphertext-aware integrity check
- `analysis/packages/abc-data-api/internal/availability/state.go` — state-transition helpers
- ~200 LOC.

**Test gate.**
1. Mark a location's host as offline → location's availability flips to `:offline` within 30s; reverts to `:available` when host comes back.
2. Partial-state location with `:ranges-present [[0 N]]` is correctly resumed by `abc data move --resume`.
3. Re-hash a Crypt4GH-encrypted location → integrity reports `:ok` (ciphertext SHA-256 matches stored observation) without requiring decryption.
4. Tamper with the ciphertext → next integrity scan flags `:divergent` correctly.

---

### Phase 3 — `abc data check` + scan + write-through ~5d

**Goal.** Files become discoverable by content. Indexing works in two ingest modes (CLI scan + write-through), event-driven comes in Phase 5.

**Infra.** Optional cron schedule for `abc data index refresh` (one-line Nomad periodic job).

**CLI.**
- `internal/indexer/xtdb.go` — typed Go fns over Datalog (`Resolve`, `LookupByHash`, `ListByPrefix`, `Upsert`, `Retract`).
- `internal/indexer/scanner/` — embedded Go scanner package (default refresh mode B.1): file walker with mtime/size cache lookup, concurrent multi-hash worker pool (`io.MultiWriter` over MD5+SHA-256+BLAKE3), NDJSON emitter, throttling.
- `internal/indexer/scan.go` — output parsers: s5cmd ETag, fclones JSON (bootstrap mode B.2), hashdeep audit (mode B.3) → batched XTDB transactions.
- `cmd/data/index.go` — `abc data index refresh [--backend ... | all] [--bootstrap | --audit] [--deep] [--threads N]` submits the appropriate Nomad scan job.
- `cmd/data/check.go` — `abc data check <abc://path | /abc/path | remote:path | --hash sha256:… | --from-file /local>`. Local file mode hashes locally (no upload), queries by SHA-256, prompts to register if not found.
- Write-through hook in `cmd/data/transfer.go` finalize phase (Upsert dest, Retract source on move).
- Lazy SHA-256 fill: when a `check`/`move` touches an S3 entity that has only MD5, compute SHA-256 once and persist.

**Files.** `internal/indexer/xtdb.go`, `internal/indexer/scanner/` (embedded Go scanner ~500 LOC), `internal/indexer/scan.go` (output parsers for fclones JSON, hashdeep, s5cmd ETag), `cmd/data/index.go`, `cmd/data/check.go`, plus tests. ~1,500 LOC (was 1,200; +300 for the embedded scanner + parser variants).

**Infra dependencies.** `fclones` and `hashdeep` binaries available in the Nomad scanner job image (small additions to existing image; both are statically-linked single binaries).

**Test gate.**
1. `index refresh --backend minio` on a test bucket → entities present in XTDB matching object count + sizes + ETags.
2. `index refresh --bootstrap --backend node://gpu-03` on a test directory with deliberate duplicate files → fclones output produces one content entity per unique hash + N location entities for duplicates; hardlinks deduplicated via `(dev,ino)` detection.
3. `index refresh --backend node://gpu-03` (default mode) re-run after touching one file → only that file is re-hashed (mtime cache hit on the rest).
4. `index refresh --audit --backend node://gpu-03` against a baseline → reports 1 modified, 1 added, 0 deleted matching the actual changes.
5. `check --from-file <known-file>` returns the known abc-path.
6. `check --from-file <unknown>` shows not-found prompt; declining exits 0; accepting kicks off upload + index write.
7. After `move abc://a abc://b`, XTDB shows source retracted and destination present (queryable via `xt/entity-history`).
8. Edge cases: scan run on a tree containing a symlink loop, a hardlink set, a sparse file, a file being written, an unreadable file, a non-UTF-8 filename → all handled per the table above without scan failure.

---

### Phase 4 — `abc://` + `/abc/` universal namespace ~3d

**Goal.** Users address files in two equivalent forms — URI (`abc://...`) for unambiguity, path (`/abc/...`) for shell ergonomics. Internally canonicalised; XTDB sees one id per file. Raw `remote:path` escape hatch still works.

**Infra.** None.

**CLI.**
- `ParseABCPath(string) (IndexEntry, error)` in `internal/indexer/` — accepts both forms, normalises path-form `/abc/X` → URI-form `abc://X` before lookup, calls `xtdb.Resolve()`.
- Path style preserved on the parsed value so error messages echo the user's form.
- `cmd/data/transfer.go`: detect either prefix, resolve, route through Phase-1 transfer logic.
- `cmd/data/show.go` — `abc data show <abc://path|/abc/path>` prints transfer lineage from XTDB `entity-history` (Phase-7 will extend with PROV).
- Global `--style uri|path` flag and `ABC_PATH_STYLE` env var control output formatting; default `uri`.

**Files.** `internal/indexer/parse.go` (~120 LOC including dual-form parser + tests), `cmd/data/show.go`, edits to `cmd/data/transfer.go` + `check.go`, `cmd/root.go` for global style flag. ~280 LOC.

**Test gate.**
1. `move abc://raw/run42/x abc://cold/archive/run42/x` works.
2. `move /abc/raw/run42/x /abc/cold/archive/run42/x` produces identical XTDB transactions.
3. Mixed: `move /abc/raw/x abc://cold/archive/x` still works (both forms resolve to the same canonical id).
4. `show /abc/cold/archive/run42/x` prints lineage with `/abc/` style output (echoes user's form).
5. `show --style uri /abc/cold/archive/run42/x` overrides to URI output.
6. Mixed addressing with raw passthrough: `move /abc/raw/x minio-cold:archive/x` works.
7. Error path: `abc:/raw/x` (single slash) returns a clear "use `abc://` or `/abc/`" message.

---

### Phase 5 — Event-driven indexing (infra service) ~4d

**Goal.** Index stays current automatically — no manual `index refresh` for S3 backends. Node-local backends still use periodic scan.

**Infra.** Deploy `bucket-event-ingester` Nomad service: subscribes to MinIO/RustFS event topics from Phase 2, transforms `ObjectCreated`/`ObjectRemoved` events to XTDB upserts/retracts, batches to amortise transaction cost. Health endpoint exposes event lag.

**CLI.** Extend `abc data index status` to report event-ingester lag.

**Files.** `services/bucket-event-ingester/` (Go service, ~400 LOC), edits to `cmd/data/index_status.go`. ~450 LOC.

**Test gate.**
1. PUT a new object to MinIO → entity appears in XTDB within 5s.
2. DELETE the object → entity retracted within 5s.
3. Burst test: 10k PUTs over a minute → no event lag > 30s reported.
4. Restart the ingester mid-burst → resumes without dropping events (offset/cursor persistence).

---

### Phase 5.5 — restic + Garage integration (archival as a first-class index citizen) ~5d

**Goal.** Tie the existing restic+Garage backup/archival service into the index. Files become queryable by backup status, snapshot membership, integrity check status, and tier (hot/warm/cold). Garage becomes a first-class backend addressable via `abc://`. Snapshots become PROV activities (re-using Phase 8 lineage primitives early).

**Why now (after Phase 5, before Phase 6).** Search (Phase 6) and chat (Phase 10) both gain meaningful filters from this — `--not-backed-up`, `--in-snapshot`, `--integrity-status`. Doing it later means re-touching search and chat tools.

**Architectural alignment.** restic chunks by SHA-256, our index keys identity by SHA-256, Nextflow lineage uses SHA-256-based lids. Joining all three is "look up the hash" — no name-based reconciliation needed.

**Optional: REMS integration for DAC workflows.** When the cluster hosts datasets that require Data Access Committee approval (rather than just bilateral DTAs), the Finnish CSC `REMS` tool is the genomics-community standard for the request → committee review → entitlement workflow. Integration is conditional and additive: a new `:compliance/rems-entitlement-id` field on the compliance bundle records the approved entitlement; Jurist's DTA rule extends to read REMS entitlements from the REMS REST API alongside its existing DTA registry. Skip this entirely if the cluster never hosts DAC-controlled data. Adoption cost: deploying REMS as a Nomad service (Java) plus ~150 LOC in the Jurist DTA emitter.

**Infra.**
- `restic-index-bridge` Nomad service: walks restic snapshot manifests on a schedule, transforms membership into XTDB transactions, also persists snapshots as PROV `:activity` entities (re-using Phase 8 schema).
- `restic-check-runner` Nomad periodic job: runs `restic check --read-data-subset=1%` daily so every blob is verified over ~3 months; results stream to XTDB as `:restic/integrity` updates.
- Garage registered as a backend in the cluster context (alongside MinIO/RustFS) so `abc://` resolution can target it.

**CLI.**
- `cmd/data/archive.go` — `abc data archive abc://path [--target abc://cold/...] [--policy <name>]`. Submits a Nomad job that runs the Phase-1 safe-move pipeline into Garage **plus** triggers `restic backup` for the same content; index records both the move and the snapshot.
- `cmd/data/restore.go` — `abc data restore abc://path [--snapshot <id>] [--at <ts>]`. Resolves which snapshot best satisfies the request (latest by default, or bitemporal at `--at`), runs `restic restore` to a chosen destination, indexes the restored entity.
- `cmd/data/snapshot.go` — `abc data snapshot list|show|prune` for snapshot inspection.
- `cmd/data/backup_status.go` — `abc data backup-status abc://path` returns snapshot membership, last backup time, integrity status.
- `cmd/data/lifecycle.go` — `abc data lifecycle apply <policy.yaml>`. Policy is a YAML matcher (bio facets, mtime, tier) + action (archive/expire/migrate). Iterates matched set, runs safe-move + backup pipeline.
- `internal/indexer/restic.go` — typed Go wrappers for snapshot ingest, integrity updates, restore-target resolution.

**Schema additions in XTDB.**

```clojure
;; File entity additions
:restic/in-snapshots  [snap-ids …]   ; queryable inverted relation lives on snapshot entities (see below)
:restic/last-backup   #inst "…"
:restic/integrity     {:checked #inst "…" :status :ok|:flagged}
:abc/tier             :hot|:warm|:cold

;; Snapshot entity (also a Phase 8 PROV activity)
{:xt/id            "snap:<repo>:<id>"
 :prov/type        :activity
 :prov/agent       "agent:restic:<version>"
 :restic/repo      "garage://abc-archive"
 :restic/files     12847
 :restic/size-bytes 909_647_000_000
 :prov/generated   [abc-paths in this snapshot]}    ; one entity per snapshot, lists members
```

**Inverted membership detail.** Storing `:restic/in-snapshots` on every file would amplify writes catastrophically (10M files × N snapshots). Instead, the **snapshot entity holds the member list**, queried via Datalog when needed: `[:find ?path :where [?s :xt/id "snap:..."] [?s :prov/generated ?path]]`. Files keep only `:restic/last-backup` (cheap to update incrementally) for fast filtering.

**Search filters this unlocks (Phase 6 picks up automatically).**
- `--in-snapshot <id>`
- `--not-backed-up` / `--not-backed-up-since <duration>`
- `--integrity-status flagged|ok|unchecked`
- `--in-tier hot|warm|cold`
- `--restorable-at <ts>` (bitemporal, joins XTDB history with restic snapshots)

**Chat tools added (Phase 10 picks up automatically).**
- `get_backup_status(abc_path)`
- `list_snapshots(filters)`
- `get_snapshot(snapshot_id)`
- `find_restorable_at(timestamp, filters)`

**Files.** `services/restic-index-bridge/` (~400 LOC), `services/restic-check-runner/` (~150 LOC, mostly HCL + a small wrapper), `internal/indexer/restic.go` (~250 LOC), `cmd/data/{archive,restore,snapshot,backup_status,lifecycle}.go` (~600 LOC combined), tests (~250 LOC). ~1,650 LOC.

**Test gate.**
1. Run `restic backup` on a test path → bridge ingester writes a `snap:` entity → `abc data snapshot show` lists members.
2. `abc data backup-status abc://test/x` returns the snapshot ID and last-backup timestamp.
3. `abc data archive abc://hot/test/x` moves to `abc://cold/...` AND creates a restic snapshot — verify both via `search --in-tier cold --in-snapshot <id>`.
4. `abc data restore --at 2026-04-15 abc://test/x` resolves to the correct snapshot and restores content with matching SHA-256.
5. Run `restic check --read-data-subset=10%` → flagged entries (if any) surface in `search --integrity-status flagged`.
6. Apply a lifecycle policy → matched files migrate; XTDB shows PROV activities for each move + each snapshot.

---

### Phase 5.6 — Self-hosted Storj resilience tier ~13d (three sub-phases)

**Goal.** Adopt Storj as a *storage technology* (erasure coding, audit, repair, identity-based access) on infrastructure we own. Replaces or complements Garage as the cold + resilience tier with significantly stronger durability semantics. Optionally federates with public Storj DCS for off-prem disaster recovery.

**Framing — what this is and isn't.**
- ✅ Storj-the-software running on our hardware: private satellite + storage nodes we operate.
- ❌ Not earning STORJ tokens by contributing to public network.
- ❌ Not less operational work than Garage — it's a step UP in operational sophistication. The justification is durability semantics, not ops offload.
- The earlier "Storj DCS as resilience tier" (Appendix A spirit) is reframed here as the optional Phase 5.6.c federation step.

**Why now (after Phase 5.5, before Phase 5.7).** Phase 5.5 sets up restic+Garage; this phase adds (or replaces) the cold/resilience tier with private Storj while keeping the same restic-managed snapshot semantics. Phase 5.7's `heal` command picks up the new tier automatically.

#### Phase 5.6.a — Deploy private Storj (~7d, infra-heavy)

**Infra.**
- **Satellite** as a Nomad service. Stateful Go service backed by **HA PostgreSQL** (or CockroachDB) for metainfo. Two replicas for availability. Postgres is the durability anchor of the whole network — single-instance Postgres is unacceptable in production.
- **gateway-mt** (S3-compatible HTTP gateway) as a separate Nomad service in front of the satellite. This is what s5cmd/rclone/restic talk to.
- **Storage nodes**: 12 Nomad jobs to start, spread across racks for fault isolation. Each node is a SNO process bound to a disk allocation (typically 1–8 TB per node). Each node has its own cryptographic identity signed by the satellite.
- **Identity tooling**: certificate authority for the satellite + per-node identity creation/signing flow. Documented as a runbook so adding a node is a routine operation.
- **Monitoring**: audit pass rate, online %, repair queue depth, satellite Postgres lag, gateway-mt request latency. Alerts on repair backlog growth (the early-warning signal for cluster health).

**Erasure-scheme choice — crucial.** Storj's public default is 29-of-80, optimized for thousands of globally-distributed nodes. **For a 12-node single-DC deployment, this is wrong** — there aren't enough fault domains. Use:

| Cluster size | Erasure scheme | Storage overhead | Max simultaneous node loss |
|---|---|---|---|
| 7–12 nodes | **4-of-7** | ~75% overhead | 3 nodes |
| 12–20 nodes | 6-of-10 | ~67% overhead | 4 nodes |
| 20–50 nodes | 8-of-14 | ~75% overhead | 6 nodes |
| 50+ nodes | 20-of-30 | ~50% overhead | 10 nodes |
| 80+ nodes spread across many DCs | Storj default 29-of-80 | ~175% overhead | 51 nodes |

Start with **4-of-7** and re-shard if cluster grows substantially. The reshard is a defined Storj operation (not free, but supported).

**Repair traffic budget.** Storj's repair process consumes bandwidth when nodes go offline. Configure repair throttling on the satellite so a single failing rack can't saturate the network. Default: cap repair at 25% of inter-node bandwidth.

**CLI.** Operator-only at this stage:
- `abc storj satellite status` — health, node count, repair queue, audit lag.
- `abc storj node add <id>` / `node retire <id>` — wraps the identity-signing flow.
- `abc storj node list` — current node roster with online %, audit pass rate, free space.

**Files.** `services/storj-satellite/` (Nomad spec + Postgres + cert tooling, ~300 LOC + extensive HCL), `services/storj-gateway-mt/` (~150 LOC), `services/storj-node/` (parameterized Nomad job, ~200 LOC), `cmd/storj/` (operator CLI, ~400 LOC), runbooks (markdown). ~1,050 LOC + ops docs.

**Test gate.**
1. Satellite + gateway + 7 storage nodes deployed; `abc storj satellite status` reports all healthy.
2. Upload a 1 GiB test object via `gateway-mt` → object retrievable; storage nodes show pieces.
3. **Node-loss simulation**: kill 3 of 7 storage nodes → object still retrievable (4-of-7 scheme); satellite repair queue picks up missing pieces; new pieces redistributed to remaining nodes within configured repair window.
4. Add a new storage node via `abc storj node add` → identity signed, node joins, satellite redistributes some pieces to it.
5. Audit pass rate >99% across all nodes after 24h burn-in.

#### Phase 5.6.b — CLI + index integration (~3d, mostly drop-in)

**Goal.** Make the private Storj cluster look like any other backend in our system. From the user's perspective, `abc://` paths can resolve to it transparently.

**Infra.** None new (uses 5.6.a deployment).

**CLI.**
- Register private satellite (via gateway-mt endpoint) as a backend in cluster context. S3-compatible — drops into Phase 1 tool routing.
- `:abc/tier :storj-private` schema enum addition.
- `abc data archive --to-storj-private abc://path` — extends Phase 5.5's `archive` command. Submits a Nomad job that runs the safe-move pipeline targeting the private Storj gateway, **plus** triggers `restic backup` against the private Storj repo (Storj is natively restic-compatible).
- Polling-based freshness for Storj-side mutations (Storj does **not** emit S3 bucket notifications — same gap as public DCS). Periodic `abc data index refresh --backend storj-private` covers it. CLI write-through on every move/copy gives near-real-time freshness for our own writes.
- Add `:abc/tier :storj-private` filter to `abc data search` (inherited automatically — Phase 6 picks it up).
- `abc data restore` source-selection ladder gains the new tier: prefer local hot → local cold (Garage) → Storj private → Storj public (if 5.6.c done).

**Replace-vs-keep-Garage decision** (recorded for future maintainers): Phase 5.6.b leaves Garage in place as a parallel cold tier. Once private Storj has 30+ days of clean operation, a follow-up evaluation decides whether to deprecate Garage. Reasons to keep both: defense-in-depth across two storage stacks; reasons to deprecate Garage: one fewer system to operate. **No decision is forced now.**

**Files.** `internal/indexer/storj.go` (backend client wrapper, ~150 LOC), edits to `cmd/data/archive.go`, `cmd/data/restore.go`, `cmd/data/search.go` (~200 LOC across them), tests (~150 LOC). ~500 LOC.

**Test gate.**
1. `abc data archive --to-storj-private abc://hot/test/x` moves content to Storj private tier; index reflects new tier.
2. `abc data restore abc://test/x` automatically pulls from local cold if available, falls back to Storj private if not.
3. `abc data search --tier storj-private` returns expected entities.
4. Polling refresh detects an out-of-band write (e.g., manual restic snapshot) within next scan cycle.

#### Phase 5.6.c — Public Storj DCS federation (~3d, optional, can defer)

**Goal.** Add public Storj DCS as a tertiary off-prem tier for true disaster recovery — "what if the whole DC dies and the private satellite is gone." Pay-per-use insurance.

**Infra.** None operational (uses Storj Inc.'s public network).

**CLI.**
- Register public Storj DCS as an additional backend.
- `:abc/tier :storj-public-dr` schema enum addition.
- `abc data archive --to-public-dr` for explicit tertiary archival of critical content.
- `abc data restore --from-public-dr` with explicit egress consent (because public DCS charges per TB egress; we don't want chat tools or scans accidentally pulling TBs).
- `abc data restore --dry-run` shows estimated egress cost before pulling from public.

**Cost-aware policy.** The restore source-selection ladder becomes:
1. Local hot (MinIO)
2. Local cold (Garage, if kept)
3. Storj private (LAN-speed, free egress)
4. Storj public DCS (last resort, paid egress)

Public DCS reads require either explicit `--from-public-dr` flag OR a critical-recovery situation where everything else is unavailable.

**Files.** `internal/indexer/storj-public.go` (~80 LOC), edits to restore source-selection (~100 LOC), egress estimator (~80 LOC), tests (~100 LOC). ~360 LOC.

**Test gate.**
1. Critical dataset replicated to BOTH private satellite AND public DCS via dual-target archive command.
2. Simulate private satellite outage → `abc data restore` falls back to public DCS only with explicit consent flag.
3. `restore --dry-run` shows accurate egress cost estimate vs actual after a real pull.
4. Default restore path NEVER pulls from public DCS without explicit consent.

#### Phase 5.6 totals

| Sub-phase | Days | LOC | Type |
|---|---|---|---|
| 5.6.a — Private Storj deployment | 7 | 1,050 + HCL | Infra (heavy) |
| 5.6.b — CLI + index integration | 3 | 500 | CLI |
| 5.6.c — Public DCS federation (optional) | 3 | 360 | CLI + policy |
| **Subtotal (with 5.6.c)** | **13** | **~1,910** | Mixed |

**Hard constraints (non-negotiable, recorded so future maintainers don't get clever):**

1. **HA Postgres for the satellite from day one.** Single-instance Postgres makes the satellite's metainfo a single point of failure that defeats the entire purpose. Use Patroni, CockroachDB, or managed Postgres with replicas.
2. **Don't pick the public-default 29-of-80 erasure scheme** unless you genuinely have 80+ storage nodes spread across many fault domains. Match the scheme to the cluster.
3. **Keep Garage running for 30+ days alongside private Storj** before deprecating it. Defense-in-depth during the burn-in window.
4. **Public DCS reads require explicit `--from-public-dr` consent.** No service or tool should be able to silently incur egress costs.

---

### Phase 5.7 — Integrity, healing, and intelligence catalog ~5d

**Goal.** Detect and recover from corrupted/incomplete copies of indexed files; surface the unique cross-signal questions the schema enables (replication, risk, reproducibility, drift) as named CLI commands so users don't reinvent Datalog queries.

**Why now (between Phase 5.5 and Phase 6).** restic snapshots (5.5) provide a recovery source for `heal`; search (6) gains integrity filters automatically. Slotting integrity in here means search and chat both inherit healing-aware filters from day one.

**Infra.** None new (uses Phase 5.5's `restic-check-runner`; results land here).

**Format-summary cache via MultiQC.** `:content/format-summary` (the nested-map attribute introduced for `abc data file`/`stat`/`peek`/`head`) is populated by running **MultiQC** in a containerised step at the end of Pass C of the scanner. MultiQC already parses outputs of 100+ bioinformatics tools (samtools stats, FastQC, Picard, GATK, bcftools stats, etc.) — building per-tool stat parsers ourselves is wasted effort. Output JSON is cached to the content entity once per unique SHA-256, so all locations of the same file inherit it. Avoids re-running on every `stat` call.

**CLI.**
- `cmd/data/verify.go` — `abc data verify <path>`, `--bulk [--tier cold] [--since-check 90d]`, `--quorum`. Re-hashes locations on demand or on a schedule; updates integrity attributes.
- `cmd/data/heal.go` — `abc data heal <path>`. Strategy ladder: (1) copy from a verified-ok sibling location of the same content; (2) restic restore from the most recent snapshot containing the expected hash; (3) offer to re-run upstream PROV activity if all its inputs still exist; (4) mark unrecoverable.
- `cmd/data/quarantine.go` — `abc data quarantine <path>`. Move corrupted file to `abc://_quarantine/<runid>/...` (uses Phase 1 safe-move pipeline); preserves observed-content entity for forensics.
- `internal/indexer/integrity.go` — typed Go fns: `RecordObservation()`, `QuorumCheck(content)`, `SelectHealSource(location)`.
- `cmd/data/inspect.go` — focused per-path reports: `--replication`, `--risk`, `--provenance`, `--integrity`. ~30–50 LOC each, thin wrappers over typed Datalog.
- `cmd/data/report.go` — corpus-wide reports: `dedup`, `risk`, `drift`, `reproducibility <activity-id>`. Each is a named pre-built query exposed as a CLI verb so users don't reinvent them.

**Quorum-based corruption detection.** When N locations claim the same `:abc/content` but their `:abc/observed-sha256` values diverge, the majority is almost certainly correct (same technique used by Cassandra read-repair and torrent clients). Triggered on demand via `verify --quorum` and during `restic check` cycles.

**Search filters this unlocks (Phase 6 inherits).**
- `--integrity-status divergent|truncated|unreachable|unverified`
- `--since-check <duration>` (integrity check freshness)
- `--single-copy` (no replication redundancy)
- `--quorum-conflict` (locations of same content disagree)
- `--unrecoverable` (no verified copy and no snapshot)

**Chat tools added (Phase 10 inherits).**
- `inspect_replication(abc_path)`
- `inspect_risk(abc_path)`
- `corpus_report(kind: dedup|risk|drift|reproducibility, args)`
- `quorum_check(content_id)`

**Files.** `cmd/data/{verify,heal,quarantine,inspect,report}.go` (~600 LOC combined), `internal/indexer/integrity.go` (~250 LOC), tests (~250 LOC). ~1,100 LOC.

**Test gate.**
1. **Truncation:** truncate a destination file → `verify` flips `:abc/integrity-status :truncated`; search filter returns it; `heal` restores from a verified sibling.
2. **Bitrot simulation:** flip a byte in a cold-tier file → `restic check` (Phase 5.5) flags it → `:abc/integrity-status :divergent`; `heal` restores from the previous snapshot.
3. **Quorum:** 4 locations of one content; corrupt one → `verify --quorum` identifies the minority; `heal` rewrites it from majority.
4. **Forensics preservation:** after `heal`, the `observed-content` entity for the corrupted blob still exists for forensic queries.
5. **Inspect:** `inspect abc://x --replication` reports location count, backends, tiers, dedup ratio. `inspect --risk` reports backup status, single-copy flag, last integrity check.
6. **Reports:** `report dedup` returns top-N most-replicated; `report reproducibility <activity>` lists which inputs are still resolvable; `report drift` shows pipeline-version distribution per sample.

---

### Phase 6 — `abc data search` + saved searches ~5d

**Goal.** Power-user discovery over the indexed corpus across every dimension the index already knows.

**Infra.** None.

**CLI.**
- `cmd/data/search.go` — full flag surface (pattern, faceted filters on backend/tags/size/time, output formats, group-by).
- `internal/indexer/search.go` — `BuildQuery(Filters) []Clause` flag→Datalog mapper.
- `internal/indexer/aggregate.go` — count/sum/group-by Datalog.
- `internal/indexer/saved.go` — saved searches as XTDB entities (`:xt/id "search:<name>"`).
- `cmd/data/search_format.go` — table/json/ndjson/paths/count/stats formatters.
- Optional `--query` DSL behind a feature flag (lands now if time, otherwise Phase 6.5).

**Files.** All listed above + tests. ~1,200 LOC (DSL adds another ~200).

**Test gate.**
1. `search "*.bam" --tag x --backend minio --since 2026-04-01` returns expected paths.
2. `search --group-by backend --format stats` totals match manual count from `index refresh` output.
3. `search --save my-q ...` then `search --run my-q` reproduces results.
4. `search --format paths | xargs abc data move ... abc://archive/` round-trip works.

---

### Phase 7 — Annotation + vocabulary ~5d

**Goal.** Biological metadata layer on the index. `search` gains `--bio.*` filters automatically.

**Infra.** None.

**CLI.**
- `internal/indexer/annotate.go` — `:bio/*` validation against `:bio/Vocabulary` entities; TSV bulk-import (match by abc-path prefix or SHA-256 column); batch transactions.
- `cmd/data/annotate.go` — `abc data annotate <path|--hash> [--bio.assay …] [--tag …] [--from-tsv …]`.
- `cmd/data/vocab.go` — `abc data vocab list|add|validate` for managing controlled vocabularies.
- `cmd/data/vocab.go` (extension) — `abc data vocab sync --from-ols [--ontology efo|ncbitaxon|obo]`. Bootstraps `:vocab/*` entities from EBI's **Ontology Lookup Service**; periodic refresh as a Nomad periodic job. Drastically reduces operator effort and gets community alignment for free (e.g., `bio.assay = WGS` carries an actual EFO ID).
- Search auto-discovers `:bio/*` keys for `--bio.<key>` flag parsing.

**Files.** `internal/indexer/annotate.go`, `cmd/data/annotate.go`, `cmd/data/vocab.go`, `internal/indexer/ols_sync.go`, tests. ~850 LOC.

**Test gate.**
1. `annotate abc://x --bio.assay WGS --bio.organism "Homo sapiens"` writes attributes; `search --bio.assay WGS` returns the file.
2. `vocab add bio.assay WES` then `annotate --bio.assay WES` succeeds; `--bio.assay BOGUS` errors.
3. `annotate --from-tsv samples.tsv` applies row-wise attributes; mismatched rows reported as warnings.

---

### Phase 8 — PROV-aligned provenance (CLI + data model + run hooks) ~5d

**Goal.** First-class derivation graph. CLI captures every pipeline/job run as a PROV Activity.

**Infra.** None (hooks live in existing CLI runners).

**CLI.**
- `internal/indexer/lineage.go` — Activity/Agent entity helpers; upstream/downstream Datalog graph traversal; PROV-N + PROV-JSON emitters.
- `cmd/data/lineage.go` — `abc data lineage <abc://path> [--upstream|--downstream|--as-prov] [--leaves-only]`; `lineage import --from <prov|trace>`.
- Finalize hooks in `cmd/job/run.go`, `cmd/pipeline/run.go`, `cmd/submit/...` write `:prov/Activity` entities on successful completion (inputs, outputs, agent, params, runtime).

**Files.** `internal/indexer/lineage.go`, `cmd/data/lineage.go`, edits across run hooks, tests. ~900 LOC.

**Test gate.**
1. Submit a toy pipeline that produces `abc://out/y` from `abc://in/x` → activity entity created with both edges.
2. `lineage abc://out/y --upstream` traces back to `abc://in/x` with the activity in between.
3. `lineage abc://out/y --as-prov` emits valid PROV-N (validates against W3C tooling).
4. `lineage import --from <sample-prov>` round-trips a synthetic graph.

---

### Phase 8.5 — Lineage outlets (visual + OpenLineage + OpenMetadata) ~5d

**Goal.** Turn the rich XTDB lineage graph into shareable, embeddable, and ecosystem-compatible artefacts. Same Datalog walk feeds visual exports (SVG/Mermaid/HTML) and standards-based bridges (OpenLineage → Marquez, OpenMetadata, DataHub, Atlan).

**Why now (between Phase 8 and Phase 9).** Phase 8 gives us the lineage graph; Phase 9 (Nextflow) starts mass-producing activities. Slotting outlets in between means every Nextflow run is immediately visible in the user's chosen catalog/dashboard from day one.

**Infra.**
- `lineage-bridge` Nomad service (live streaming): subscribes to XTDB `tx-log`, filters `:prov/Activity` puts, emits OpenLineage events to one or more configured sinks. Batched, retried, offset-journaled for restart safety.
- Optional Marquez or OpenMetadata deployment guidance in docs (these are user-chosen sinks, not bundled).
- `graphviz` binary added to the cluster job image used by `lineage --as-svg|png|pdf` jobs (or run client-side if user has it).

**CLI.**
- `internal/lineage/render/` — typed graph struct + renderers: `dot.go`, `mermaid.go`, `cytoscape.go` (standalone HTML w/ embedded ~50 KB Cytoscape.js), `prov.go`, `openlineage.go`.
- Extends `cmd/data/lineage.go` from Phase 8 with `--as <format>`, `--depth`, `--leaves-only`, `--collapse-pipeline`, `--color-by`, `--group-by`, `--include-agents`, `--include-snapshots`, `-o <file>`.
- `--emit-to-openlineage <url>` and `--emit-to-openmetadata <url>` for one-shot remote pushes (OpenMetadata accepts OpenLineage natively, so the same emitter serves both).
- `abc data lineage export --since <date> --to <sink>` for bulk backfill.
- `abc data lineage bridge start|stop|status` to manage the live streaming service.

**XTDB → OpenLineage mapping.** Activities → Job + Run; content entities → Dataset (namespace=`abc`, name=first abc-path); locations → Dataset storage facet; `:bio/*` → custom `bio` dataset facet; `:restic/*` → custom `backup` dataset facet; container/agent → Job facets. Custom facets published as a versioned `abc-bio-facet` schema doc so downstream consumers can validate.

**OpenMetadata path.** Default = use OpenLineage bridge (Path A — zero extra code, native OL ingestion). Path B (custom OpenMetadata connector for richer Glossary/Pipeline-Service mapping and bidirectional sync) deferred unless Path A reveals UX gaps.

**Khan operator console — IGV.js embed.** The Khan Filament "Data Index" resource gains a "view this BAM/VCF" button that opens **IGV.js** (Broad Institute, browser-based) inline with a token-authenticated **htsget** URL (delivered in Phase 8.7). Operators and compliance reviewers spot-check data without downloading anything. Genuine UX win for data-quality auditing. ~200 LOC in Khan to embed and wire up token flow.

**Files.** `internal/lineage/render/` (~600 LOC across 5 renderers + tests), `services/lineage-bridge/` (~400 LOC: tx-log subscriber, OL transformer, sink dispatcher, retry/journal), `cmd/data/lineage.go` extensions (~150 LOC), Khan IGV.js embed (~200 LOC), tests (~200 LOC). ~1,550 LOC.

**Test gate.**
1. `lineage abc://variants/run42/sample.vcf.gz --upstream --as-svg -o out.svg` produces a valid SVG that opens in a browser and shows the expected DAG.
2. `lineage --as-mermaid` output renders correctly when pasted into a GitHub markdown preview.
3. `lineage --as-html` produces a standalone interactive page (zoom/pan/click works without network).
4. `lineage --as-openlineage` output validates against the OpenLineage JSON schema.
5. Live bridge: trigger a new activity in XTDB → corresponding OL `START`+`COMPLETE` events arrive at Marquez/OpenMetadata within 5s; UI shows the lineage edge.
6. Bridge restart mid-stream → no events lost, no duplicates (offset journal works).
7. `--collapse-pipeline` and `--leaves-only` produce visually distinct, correct subgraphs.

---

### Phase 8.7 — Standards & interop (DRS, htsget, refget, RO-Crate, SBOM, BCO, .abc pointers) ~9d

**Goal.** Make this system a first-class citizen in the broader scientific-data ecosystem rather than a silo. Adopt mature standards so external tools (workflow runners, visualization, catalogs, git workflows) interoperate without custom code on either side.

**Why now (after lineage outlets, before Nextflow integration).** OpenLineage (Phase 8.5) gets us into catalog UIs. This phase rounds out the standards story: GA4GH for genomics tools (DRS + htsget + refget), RO-Crate for sharing, SBOM for compliance, BioCompute Objects for regulated contexts, `.abc` pointers for git workflows. Phase 9's Nextflow runs immediately benefit (each becomes a DRS-discoverable, htsget-streamable, RO-Crate-exportable, SBOM-stamped artefact).

**Infra (three new servers, all read-only over the index):**
- `drs-server` Nomad service — implements GA4GH Data Repository Service v1.4 over the index. Exposes `drs://abc-cluster/<content-id>` URIs that resolve to one of the location URLs (signed presigned URLs for buckets, or direct paths for node-local). Read-only.
- **`abc-htsget-server` Nomad service** — implements the GA4GH htsget protocol for region-streaming BAM/VCF/CRAM. Pairs with DRS: DRS gives the URI, htsget streams a slice. Massive UX win for "show me TP53 in these 200 BAMs" without downloading any of them. ACL via Jurist read-policy bundle (same enforcement model as abc-data-api). ~600 LOC.
- **`abc-refget-server` Nomad service** — implements GA4GH refget for content-addressed reference sequences (SHA-512 of normalised sequence content). Solves the "every lab caches GRCh38 differently" problem. Lineage edges to reference genomes carry refget IDs instead of paths — guaranteed reproducibility across institutions. ~400 LOC.

**CLI.**
- `cmd/data/export.go` — `abc data export-rocrate <abc-path> [--include-lineage] [--include-annotations] [--depth N] -o <dir|.zip>`. Bundles a content (or a subgraph) as a W3C RO-Crate: JSON-LD manifest + the actual file payloads + embedded lineage as PROV-JSON.
- `cmd/data/export.go` (extension) — `abc data export-bco <activity-id> -o bco.json`. Emits an IEEE 2791-2020 **BioCompute Object** for regulated/clinical contexts. We already capture every required field (inputs, outputs, parameters, container digests, software versions); BCO emission is a JSON template fill, not a data-collection effort. ~150 LOC.
- `cmd/data/pin.go` — `abc data pin <abc-path> > experiment.abc`. Writes a small JSON pointer file (sha256, size, abc-path, timestamp). `abc data resolve <file.abc>` fetches/verifies. Pattern lifted from DVC.
- `cmd/data/drs.go` — `abc data drs resolve drs://...`, `abc data drs whoami` (capabilities), thin client for testing the local DRS server.
- `cmd/data/htsget.go` — `abc data htsget abc://x.bam --region chr17:7676000-7677000 -o slice.bam`, thin client wrapping the htsget protocol.

**SBOM emission with Pixi-aware fingerprinting.** SBOM emission hook in Phase 8 PROV activity finalize: every `:prov/Activity` writes a CycloneDX 1.5 SBOM listing every container image (by OCI digest), every package (with versions). The emitter **prefers `pixi.lock` when available** and falls back to conda-lockfile / conda env hash otherwise. The lockfile SHA-256 becomes a component of the activity fingerprint introduced in Phase 11 — `pixi.lock` is byte-stable, so "same env" is a one-line comparison instead of "let's hope conda resolved the same way."

**Activity fingerprint contract** (consumed by Phase 11 cache):
```
:prov/activity-fingerprint = sha256(
  pipeline-name + pipeline-version + container-digest +
  canonical-params-json +
  sorted(input-content-sha256s) +
  refget-ids-of-references +              ; (from refget-server when used)
  pixi-lock-sha256 OR conda-env-hash +    ; (Pixi preferred)
  sbom-cyclonedx-sha256
)
```

**Files.** `services/drs-server/` (~400 LOC), `services/abc-htsget-server/` (~600 LOC), `services/abc-refget-server/` (~400 LOC), `cmd/data/{export,pin,drs,htsget}.go` + `internal/lineage/rocrate.go` + BCO emitter (~750 LOC combined), SBOM emitter in `internal/indexer/sbom.go` (~250 LOC including Pixi-aware path), tests (~400 LOC). ~2,800 LOC. ~9 engineer-days (was 5; +4 for htsget + refget + Pixi-fingerprint + BCO).

**Test gate.**
1. `drs whoami` returns valid DRS service info; resolving a known content via `drs://abc-cluster/<id>` returns a working presigned URL or direct path.
2. Cromwell (or any GA4GH-aware workflow runner) submits a job using `drs://` inputs and reads them successfully.
3. **htsget range request** for `chr17:7676000-7677000` against an indexed BAM returns correct bytes (verified against `samtools view` of the same region).
4. **refget resolves** a known sequence (e.g., chr1 of GRCh38) by SHA-512 and returns the expected bytes.
5. `export-rocrate abc://variants/run42/sample.vcf.gz --include-lineage -o run42.crate.zip` produces a valid RO-Crate (validates against the Crate spec); unzipping it shows the file + a PROV-JSON manifest + a JSON-LD `ro-crate-metadata.json`.
6. `pin abc://x > x.abc` writes a pointer; `resolve x.abc` fetches and verifies SHA-256.
7. Every newly-created activity carries a `:prov/sbom-cyclonedx` document; querying it returns a valid CycloneDX 1.5 JSON.
8. SBOM diff between two runs of the same pipeline shows tool-version drift.
9. **`export-bco` validates** against the IEEE 2791 schema.
10. **Activity fingerprint is deterministic**: two runs with identical `pixi.lock`, container digest, params, and inputs produce the same `:prov/activity-fingerprint`.

---

### Phase 9 — Nextflow lineage integration ~5d

**Goal.** Every Nextflow / nf-core run automatically becomes part of the lineage graph with task-level granularity. Zero user effort.

**Infra.**
- Deploy `nf-lineage-ingester` Nomad service: watches a shared `.nextflow/lineage/` location (or `bucket://_nf-lineage/`) and ingests events to XTDB. Default for all runs.
- Distribute `nf-abc-lineage` Nextflow plugin via plugins.json (or local install). Provides real-time task-level visibility.

**CLI.**
- `internal/indexer/nflineage.go` — Nextflow lineage event format parser → XTDB activity/agent transactions; `lid://` ↔ `abc://` resolver via SHA-256.
- `cmd/data/lineage.go` extensions — `--source nextflow`, `--watch`, `--activity-search "container=…"`, `--pipelines-only`, `--derived-from-sample <id>`.
- `cmd/data/lineage.go` — **`abc data lineage export --as-cwl <activity>`** runs `nextflow inspect -format cwl` against the recorded workflow definition and emits a Common Workflow Language descriptor. Enables workflow portability to Cromwell, Toil, Arvados. Documented limitation: Nextflow's CWL export is not 100% complete (some Nextflow features don't translate); the CLI surfaces this with a warning if any feature is dropped.

**Files.** `internal/indexer/nflineage.go`, `services/nf-lineage-ingester/`, `plugins/nf-abc-lineage/`, edits to `cmd/data/lineage.go` (including CWL export wrapper, ~50 LOC). ~1,050 LOC.

**Test gate.**
1. Run a small nf-core pipeline (`nf-core/sarek` test profile) → all task activities appear in XTDB within 30s of completion (ingester path).
2. `lineage abc://variants/.../sample.vcf.gz --source nextflow --upstream` shows the full task DAG with container digests and pipeline versions.
3. `lineage --activity-search "container=quay.io/biocontainers/gatk4*"` returns matching activities across all runs.
4. With plugin installed, `lineage --watch` streams task events as a live pipeline runs.

---

### Phase 10 — Natural-language chat over the index ~6d

**Goal.** Conversational interface to the entire indexed corpus — search, lineage, annotations, provenance — backed by tool-calling over the existing typed query layer. This is the equivalent of Meilisearch's chat feature, but tailored to structured metadata rather than free-text documents.

**Why tool-calling, not RAG.** The index is already highly structured. Numeric filters, time ranges, lineage walks, and aggregations all need exact answers — RAG hallucinates on these. Tool-calling routes every question through the same Datalog/Lucene functions the CLI already uses, so chat answers are auditable and always live (no reindex lag). Semantic search on annotations is added as a **single dedicated tool**, not the default retrieval path.

**Infra.**
- Deploy `abc-chat` Nomad service: stateless HTTP orchestrator that proxies LLM calls and dispatches tool calls back to `internal/indexer/`.
- **Deploy LiteLLM as a Nomad service** in front of `abc-chat-svc`. LiteLLM exposes a unified OpenAI-style API across providers (Anthropic, OpenAI, Bedrock, Ollama, vLLM). The chat service talks only to LiteLLM; switching between Anthropic API (cloud) and local Llama (air-gapped) is a LiteLLM configuration change, not an `abc-chat-svc` code change. Eliminates per-provider adapter sprawl.
- Cluster secret: LLM provider API key(s) stored in Vault; LiteLLM reads them at startup.
- Optional: vector store (LanceDB or Qdrant) for `semantic_search` tool — only if Phase 10.5 is taken.

**CLI.**
- `cmd/data/chat.go` — `abc data chat` (REPL), `--one-shot "<q>"` (non-interactive), `--resume <conv-id>`, `--explain` (shows tool calls), `--list`, `--share`.

**Tool surface (LLM-callable).** Direct wrappers over Phase 3–8 indexer functions. No new business logic.

| Tool | Wraps |
|---|---|
| `search_files(filters)` | `internal/indexer.Search` |
| `get_entity(abc_path)` | `Resolve` + entity attributes |
| `lookup_by_hash(hash)` | `LookupByHash` |
| `get_lineage(path, direction, leaves_only?)` | `lineage.Upstream` / `Downstream` |
| `get_activity(activity_id)` | PROV entity fetch (Phase 8) |
| `aggregate(group_by, filters)` | `aggregate.go` |
| `list_pipelines()` | distinct `:pipeline/name` across activities |
| `list_samples()` | distinct `:bio/sample-id` across files |
| `semantic_search(text)` | optional Phase 10.5; vector search over annotations |

Tool definitions in `services/abc-chat/tools.go` (Anthropic tool-use JSON-schema format).

**Conversation persistence.** Conversations are themselves XTDB entities (`:xt/id "chat:conv-…"`), searchable and shareable like any other entity. Auto-generated titles via the LLM after first turn.

**Provider abstraction.** `services/abc-chat/llm/` exposes one Go interface; Anthropic and local-Llama implementations behind it. Provider chosen by config.

**Files.** `services/abc-chat/` (~600 LOC: orchestrator, tool registry, llm/, conversation store), `cmd/data/chat.go` (~250 LOC), tests (~150 LOC). ~1,000 LOC total. Phase 10.5 (semantic search) adds ~400 LOC if pursued.

**Test gate.**
1. `chat --one-shot "Find all WGS samples for S00123"` → calls `search_files` with correct filters → returns expected files.
2. Multi-turn refinement: ask for "all derived from these", then "leaves only" — second turn produces correct downstream-leaves query without re-stating context.
3. `chat --explain` shows the exact tool calls and arguments for every answer.
4. `chat --resume <id>` reconstructs prior conversation state from XTDB and continues seamlessly.
5. Air-gapped run with local Llama provider → answers parity-checked against Anthropic provider on a fixed eval set of 20 questions.

---

### Phase dependency graph

```
Phase 0.5 (instrument watcher) ──────────────────────────────────────────────────┐
  ships in parallel with Phase 1; needs only Khan                                 │
  provenance writes wired in Phase 3 ──────────────────────────────────────────►─┤
                                                                                  │
Phase 1 ──────────────────────────────────► (independent, ships value alone)      │
                                                                                  │
Phase 2 (infra) ─► Phase 3 ◄─────────────────────────────────────────────────────┘
                       │
                       └─► Phase 4 ─┬─► Phase 5 (infra) ─► Phase 5.5 (infra) ─┐
                                    │                                           │
                                    │                Phase 5.6 (infra) ◄────────┤
                                    │                (private Storj +           │
                                    │                 optional public DCS)      │
                                    │                       │                   │
                                    ├─► Phase 6 ◄───────────┴───────────────────┘
                                    │     (gains backup/integrity/tier filters
                                    │      including :storj-private)
                                    │
                                    └─► Phase 7 ─► Phase 8 ─► Phase 9 (infra)
                                                    │           │
                                                    │           └─► Phase 10 (infra)
                                                    │                  ▲
                                                    └──────────────────┘
                                         (chat tools include backup/lineage)
```

Phase 0.5 (instrument watcher) can ship in parallel with Phase 1 — it only needs Khan for job submission, not the full index. Phase 3 wires its provenance writes to XTDB. Phase 1 ships immediately, no infra work. Phase 2 unlocks Phase 3, which unlocks the rest. Phase 5.5 (restic+Garage) lands before Phase 5.6 (private Storj). Phase 5.6 is **operationally heavy but delivers the strongest durability story**; it can be deferred if Garage proves sufficient. Phases 6 and 7 can be parallelised by separate developers once Phase 5.5 (and optionally 5.6) lands. Phase 10 (chat) is most useful after Phase 8 (provenance) but can launch earlier with a reduced toolset.

**Total estimate across all phases:** ~80 engineer-days, ~15,660 LOC (with full Phase 5.6 including public DCS federation). Without all of 5.6: ~67 days, ~13,750 LOC. With 5.6.a + 5.6.b but skipping public DCS federation: ~77 days, ~15,300 LOC.

---

### Tier-2 enrichments (slot into existing phases when each lands)

These are well-validated patterns from competitor systems that don't warrant a standalone phase but should be designed-into the relevant phase from day one rather than bolted on later.

| # | Enrichment | Borrowed from | Slot into phase |
|---|---|---|---|
| 1 | **Rules engine for lifecycle policies** — formal YAML rules with cron + match + action; replaces the ad-hoc lifecycle hint | iRODS rule engine | Phase 5.5 (~600 LOC, +4d) |
| 2 | **Glossary system** — hierarchical `:bio/*` taxonomies, synonyms, ontology refs (EFO, NCBI Taxonomy) as XTDB entities | OpenMetadata Glossary | Phase 7 (~400 LOC, +3d) |
| 3 | **Domain/Group entities** — `:abc/Domain` for studies, projects, cohorts | DataHub Domains | Phase 7 (~300 LOC, +2d) |
| 4 | **Quality check framework** — scheduled `:abc/QualityCheck` entities (file format, schema, reference checksum) | OpenMetadata Quality | Phase 5.7 (~500 LOC, +3d) |
| 5 | **Bitemporal diff** — `abc data diff --at t1 --at t2 [path]` shows what changed | LakeFS-inspired, XTDB-native | Phase 6 (~250 LOC, +2d) |

If all five are pursued: **+14d, +2,050 LOC**, bringing total to **~80 engineer-days, ~14,175 LOC**.

---

### Deferred (designed for compatibility, not built now)

| # | Capability | Defer because |
|---|---|---|
| 1 | GA4GH Beacon v2 federation | Useful only when peer clusters exist to federate with |
| 2 | Crypt4GH at-rest encryption | Only matters for cross-institution restricted data sharing |
| 3 | Push ingestion HTTP endpoint | Phase 5 event-driven covers most cases |
| 4 | Ownership / access policy model | Tie in to broader cluster auth model when known |
| 5 | Dataset previews (BAM stats, VCF summary) | UI-layer; leave to OpenMetadata + on-demand jobs |

---

### What we are *not* building (and why)

To keep scope honest, here's what the comparative analysis tells us we should *not* re-implement:

- **A web UI / catalog frontend** — adopt OpenMetadata via the OpenLineage bridge (Phase 8.5).
- **A workflow orchestrator** — Nextflow exists; we integrate (Phase 9), not replace.
- **A new file format** — use existing scientific formats; standardise the metadata around them.
- **A new container runtime** — Nomad already handles this.
- **A new identity system** — defer until we know the cluster's auth model.

| Phase | Days | LOC | Type |
|---|---|---|---|
| 0.5 — Instrument watcher (automated ingest) | 4 | 600 | Infra service |
| 1 — Robust transfers | 5 | 1,000 | CLI |
| 2 — Index platform (+ OpenTelemetry baseline) | 3 | 400 | Infra |
| 2.6 — Backend + Host entity refactor | 3 | 600 | Schema + migration |
| 2.7 — Location availability + completeness + at-rest encryption | 2 | 200 | Schema |
| 3 — Check + scan + write-through | 6 | 1,500 | CLI + indexer (fclones/hashdeep + embedded Go scanner) |
| 4 — `abc://` namespace | 3 | 250 | CLI |
| 5 — Event-driven indexing | 4 | 450 | Infra service |
| 5.5 — restic + Garage (+ optional REMS) | 5 | 1,650 | Infra + CLI |
| 5.6.a — Private Storj deployment | 7 | 1,050 | Infra (heavy) |
| 5.6.b — CLI + index integration | 3 | 500 | CLI |
| 5.6.c — Public DCS federation (optional) | 3 | 360 | CLI + policy |
| 5.7 — Integrity + heal + intelligence (+ MultiQC) | 5 | 1,150 | CLI |
| 6 — Search + saved searches | 5 | 1,200 | CLI |
| 7 — Annotations + vocabulary (+ OLS sync) | 5 | 850 | CLI + data model |
| 8 — PROV provenance | 5 | 900 | CLI + run hooks |
| 8.5 — Lineage outlets (+ IGV.js in Khan) | 5 | 1,550 | CLI + infra service |
| 8.7 — Standards & interop (+ htsget + refget + Pixi-fingerprint + BCO) | 9 | 2,800 | Infra + CLI |
| 9 — Nextflow lineage (+ CWL exporter) | 5 | 1,050 | Infra + plugin + CLI |
| 10 — Chat (+ LiteLLM provider abstraction) | 6 | 1,050 | Infra + CLI |

External-component impact accounted for in the rows above: OpenTelemetry (Phase 2), MultiQC (Phase 5.7), OLS sync (Phase 7), IGV.js (Phase 8.5), htsget + refget + Pixi + BCO (Phase 8.7), CWL export (Phase 9), LiteLLM (Phase 10), REMS (conditional in Phase 5.5), Mountpoint for S3 (replaces FUSE placeholder in Phase 11), Marimo (cross-cutting recommendation in §6a). Net delta vs. pre-curated plan: roughly **+8 engineer-days, +1,800 LOC** — concentrated in Phase 8.7. See **Appendix B.11** for the full curated list with rationale and rejected alternatives.

---

## 8. Verification plan

### Smoke matrix

| # | Pair | Tool | Assert |
|---|---|---|---|
| 1 | `abc://raw/x → abc://cold/x` (both MinIO) | s5cmd | count + bytes + ETag equality; XTDB entity created |
| 2 | `abc://raw/x → abc://ext-cold/x` (external S3) | s5cmd | count + bytes |
| 3 | `abc://cold/x → abc://node-gpu03/scratch/x` | s5cmd | files on node; index entry updated |
| 4 | `abc://node-cpu07/data → abc://raw/results` | s5cmd | bucket objects present |
| 5 | `move` bucket↔bucket | s5cmd | source empty; journal `done`; XTDB shows `moved-from`/`moved-to` |
| 6 | relay `--via abc://minio/_relay/$RUN` | both phases | relay prefix empty after cleanup; both phases logged in XTDB |

### Failure injection

- Kill alloc mid-copy → journal has no `done` → rerun emits error with journal URI; `--resume <runid>` skips verified objects.
- Corrupt destination object before verify → exits 2; source intact; XTDB transaction not committed.
- Phase-2 relay failure → relay prefix intact; error includes relay URI + run ID.

### Unit-test seams

- `Resolve`, `LookupByHash`, `ListByPrefix` — fake XTDB transactor.
- `selectTool(src, dst)` — pure, table-driven.
- `journal.Writer`/`journal.Reader` — fake S3 via `internal/floor` test helpers.
- `pickJournalBucket(src, dst, flag)` — pure.
- HCL generator — golden-file for `HostVolumes` block.

---

## 9. System Architecture — Service Integration Map

This section describes how the data platform components fit into the existing abc-cluster topology alongside `abc-khan-svc`, `abc-jurist-svc`, and the CLI. It was designed with full knowledge of the existing service APIs (Khan's admission edge + workspace APIs, Jurist's authorize/patch flow, and their shared XTDB instance).

---

### 9.1 Existing service topology (baseline)

```
┌─────────────────────────────────────────────────────────────────────────────┐
│  Cluster services (Nomad-managed, internal network)                         │
│                                                                             │
│  ┌──────────────────┐    ┌──────────────────┐    ┌──────────────────────┐  │
│  │  abc-khan-svc    │    │  abc-jurist-svc  │    │  Nomad               │  │
│  │  (PHP/Laravel)   │◄──►│  (Clojure)       │◄──►│                      │  │
│  │                  │    │                  │    │  Schedules + runs    │  │
│  │ • Admission edge │    │ • Authorize/patch│    │  all workloads       │  │
│  │ • Workspace APIs │    │ • DTA/consent    │    └──────────────────────┘  │
│  │ • Operator UI    │    │ • Job mutations  │                               │
│  │ • Tus uploads    │    │ • Policy emit    │    ┌──────────────────────┐  │
│  └────────┬─────────┘    └────────┬─────────┘    │  OPA                 │  │
│           │                       │              │  (Rego evaluation)   │  │
│           │                       │              └──────────────────────┘  │
│           └───────────────────────┘                                         │
│                         │                                                   │
│                ┌─────────▼──────────┐                                       │
│                │  XTDB (shared)     │  ← governance namespace               │
│                │  Bitemporal index  │    :decision/* :audit/* :cost/*       │
│                │  PostgreSQL wire   │                                       │
│                └────────────────────┘                                       │
│                                                                             │
│  ┌──────────────────────────────────────────────────────────────────────┐  │
│  │  Storage tier                                                        │  │
│  │  MinIO (hot) • RustFS (warm) • Garage (cold)                        │  │
│  └──────────────────────────────────────────────────────────────────────┘  │
└─────────────────────────────────────────────────────────────────────────────┘
                                  ▲
                     CLI (abc-cluster-cli)
                     local machine / jump host
```

**What the existing flow looks like for a data transfer today:**

```
abc data copy minio-raw:x minio-cold:y
      │
      ├─ 1. GET /api/v1/configs/rclone → Khan → rclone.conf (credentials)
      ├─ 2. Build Nomad HCL (rclone copy job)
      ├─ 3. POST /v1/jobs → Khan admission edge
      │         └─ POST /v1/authorize → Jurist (DTA/consent/clearance check)
      │              └─ PATCH job (datacenters, constraints, node_pool)
      │                   └─ POST /v1/jobs → Nomad
      └─ 4. Nomad alloc runs: rclone copy inside container
```

Three gaps: (a) user must know `minio-raw:x` rclone remote syntax, (b) no verification after copy, (c) no index write after completion.

---

### 9.2 Architectural decisions for the data platform

**Decision A — XTDB: extend the existing instance, don't fork it.**

Khan and Jurist already run a shared XTDB instance (Jurist writes decision records; Khan reads them for the operator console). The data platform adds new entity namespaces to the same XTDB node:

```
Existing namespaces (owned by governance layer):
  :decision/*    — Jurist admission decisions
  :audit/*       — system-managed timestamps
  :cost/*        — per-job cost tracking

New namespaces (owned by data platform):
  :content/*     — one entity per unique SHA-256 (hashes, size, bio annotations)
  :location/*    — one entity per abc:// path (backend, mtime, tier)
  :integrity/*   — integrity check state (observed vs expected, divergence)
  :prov/*        — W3C PROV derivation edges (Activity, Agent, wasGeneratedBy)
  :pipeline/*    — pipeline name/version/params on Activity entities
  :bio/*         — biological annotations on Content entities
  :nf/*          — Nextflow-specific aliases (lid://, task-process, work-dir)
```

The `xtdb-lucene` module is added to the existing XTDB cluster. Lucene indexes `:xt/id` (paths), `:abc/tags`, `:bio/assay`, `:bio/organism`, `:bio/sample-id`. All existing Jurist/Khan queries are unaffected (they use different namespace predicates).

**Decision B — Khan is the single API gateway for the CLI.**

The CLI never talks to XTDB or data platform services directly. It talks to Khan's `/api/v1/data/*` endpoints, which authenticate the request (Bearer token), then proxy to the internal `abc-data-api` service or write to XTDB directly (Khan already has an XTDB connection). This keeps the CLI's trust model simple: one URL, one token, all operations.

**Decision C — `abc data move/copy` still goes through Jurist.**

The Nomad transfer job (s5cmd or rclone) is a Nomad job like any other. It passes through Khan's admission edge and gets authorized by Jurist. This is correct behaviour: a data transfer between two storage backends that span jurisdictions or data classifications must be governed. Jurist gains a new data-transfer rule set (see §9.5).

**Decision D — Write-through happens via Khan, not direct XTDB.**

After a successful transfer, the CLI calls `POST /api/v1/data/index/finalize` on Khan (with source path, dest path, job ID, hashes). Khan validates the request and writes the XTDB content + location transaction. This is audit-complete: the XTDB transaction records the Jurist decision ID and Nomad job ID alongside the transfer provenance.

---

### 9.3 Full service map (data platform added)

```
┌─────────────────────────────────────────────────────────────────────────────────────┐
│  Cluster services (Nomad-managed)                                                   │
│                                                                                     │
│  ┌─────────────────────────────────────────────────────────────────────────────┐   │
│  │  API / Governance layer                                                     │   │
│  │                                                                             │   │
│  │  ┌──────────────────────┐    ┌──────────────────────┐                      │   │
│  │  │  abc-khan-svc        │    │  abc-jurist-svc      │                      │   │
│  │  │  (PHP/Laravel)       │◄──►│  (Clojure)           │                      │   │
│  │  │                      │    │                      │                      │   │
│  │  │  + /api/v1/data/*    │    │  + data-transfer     │                      │   │
│  │  │    resolve           │    │    rule set (new)    │                      │   │
│  │  │    index/scan        │    │                      │                      │   │
│  │  │    index/finalize    │    │  Reads :content/*    │                      │   │
│  │  │    check             │    │  for classification  │                      │   │
│  │  │    search            │    │  checks before       │                      │   │
│  │  │    show              │    │  authorizing move    │                      │   │
│  │  │    chat/*            │    │                      │                      │   │
│  │  └──────────┬───────────┘    └──────────────────────┘                      │   │
│  │             │ proxies to                                                    │   │
│  └─────────────┼───────────────────────────────────────────────────────────────┘   │
│                │                                                                     │
│  ┌─────────────┼───────────────────────────────────────────────────────────────┐   │
│  │  Data platform services (new)                                               │   │
│  │             │                                                                │   │
│  │  ┌──────────▼───────────┐    ┌──────────────────────┐                      │   │
│  │  │  abc-data-api        │    │  abc-chat            │                      │   │
│  │  │  (Go, new)           │    │  (Go, new, Phase 10) │                      │   │
│  │  │                      │    │                      │                      │   │
│  │  │  • Resolve abc:// →  │    │  • LLM tool-calling  │                      │   │
│  │  │    backend + actual  │    │  • Wraps abc-data-api│                      │   │
│  │  │  • Search / ls / show│    │  • Conv entities in  │                      │   │
│  │  │  • Lineage queries   │    │    XTDB              │                      │   │
│  │  │  • Annotation write  │    └──────────────────────┘                      │   │
│  │  │  • Submit scan job   │                                                   │   │
│  │  │  No auth (internal)  │    ┌──────────────────────┐                      │   │
│  │  └──────────┬───────────┘    │  drs-server          │                      │   │
│  │             │                │  (Go, new, Phase 8.7)│                      │   │
│  │             │                │  GA4GH DRS over index│                      │   │
│  │             │                └──────────────────────┘                      │   │
│  │  ┌──────────▼───────────────────────────────────────────────────────────┐  │   │
│  │  │  XTDB (extended — same instance as governance layer)                 │  │   │
│  │  │                                                                      │  │   │
│  │  │  xtdb-lucene module: indexes :xt/id (paths), :bio/assay,             │  │   │
│  │  │    :bio/organism, :bio/sample-id, :location/tags                     │  │   │
│  │  │                                                                      │  │   │
│  │  │  :decision/* :audit/* :cost/*   ← Jurist/Khan (existing)            │  │   │
│  │  │  :content/* :location/*         ← data platform (new)               │  │   │
│  │  │  :prov/* :pipeline/* :bio/*      ← data platform (new)               │  │   │
│  │  │  :nf/* :chat/* :search/*         ← data platform (new)               │  │   │
│  │  └──────────────────────────────────────────────────────────────────────┘  │   │
│  │                                                                             │   │
│  │  ┌───────────────────────┐   ┌────────────────────────┐                   │   │
│  │  │  bucket-event-        │   │  nf-lineage-ingester   │                   │   │
│  │  │  ingester (Go, new)   │   │  (Go, new, Phase 9)    │                   │   │
│  │  │                       │   │                        │                   │   │
│  │  │  MinIO/RustFS S3      │   │  Watches               │                   │   │
│  │  │  events → XTDB        │   │  bucket://_nf-lineage/ │                   │   │
│  │  │  (via NATS JetStream  │   │  → XTDB Activity +    │                   │   │
│  │  │   Phase 5+)           │   │    Agent entities      │                   │   │
│  │  └───────────────────────┘   └────────────────────────┘                   │   │
│  │                                                                             │   │
│  │  ┌───────────────────────┐   ┌────────────────────────┐                   │   │
│  │  │  lineage-bridge       │   │  restic-index-bridge   │                   │   │
│  │  │  (Go, new, Phase 8.5) │   │  (Go, new, Phase 5.5)  │                   │   │
│  │  │                       │   │                        │                   │   │
│  │  │  XTDB tx-log →        │   │  Restic snapshots →   │                   │   │
│  │  │  OpenLineage events   │   │  XTDB Snapshot +      │                   │   │
│  │  │  → Marquez/OMetadata  │   │    backup entities     │                   │   │
│  │  └───────────────────────┘   └────────────────────────┘                   │   │
│  └─────────────────────────────────────────────────────────────────────────────┘   │
│                                                                                     │
│  ┌─────────────────────────────────────────────────────────────────────────────┐   │
│  │  Execution layer                                                            │   │
│  │                                                                             │   │
│  │  Nomad   OPA   NATS JetStream (new, Phase 5)                               │   │
│  └─────────────────────────────────────────────────────────────────────────────┘   │
│                                                                                     │
│  ┌─────────────────────────────────────────────────────────────────────────────┐   │
│  │  Storage tier                                                               │   │
│  │                                                                             │   │
│  │  MinIO (hot) • RustFS (warm) • Garage (cold) • Storj private (Phase 5.6)  │   │
│  │  _abc-journals/  _abc-scan/  _nf-lineage/  _sboms/  (reserved prefixes)   │   │
│  └─────────────────────────────────────────────────────────────────────────────┘   │
└─────────────────────────────────────────────────────────────────────────────────────┘

                              ▲  Bearer token (session or PAT)
                              │  Single endpoint: abc-khan-svc
              ┌───────────────┴────────────────────────────────┐
              │  abc-cluster-cli (local machine / jump host)   │
              │                                                 │
              │  abc data move abc://raw/x abc://cold/y        │
              │  abc data check --from-file sample.bam         │
              │  abc data search "*.bam" --bio.assay WGS       │
              │  abc data chat "find all unverified files"      │
              └─────────────────────────────────────────────────┘
```

---

### 9.4 Request flows — key operations end to end

#### Flow A — `abc data move abc://raw/run42/x abc://cold/run42/x`

```
CLI                    Khan                  Jurist             XTDB        Nomad
 │                       │                     │                  │            │
 │ GET /api/v1/data/      │                     │                  │            │
 │   resolve?path=        │                     │                  │            │
 │   abc://raw/run42/x ──►│                     │                  │            │
 │                        │──── Datalog query ──────────────────►  │            │
 │                        │◄── {backend:minio,                     │            │
 │                        │    actual:minio-raw:raw/run42/x,       │            │
 │                        │    content:sha256:a3f9…}               │            │
 │◄── IndexEntry ─────────│                     │                  │            │
 │                        │                     │                  │            │
 │ (same for dest path)   │                     │                  │            │
 │                        │                     │                  │            │
 │  selectTool() → s5cmd  │                     │                  │            │
 │  (both S3 backends)    │                     │                  │            │
 │                        │                     │                  │            │
 │ POST /v1/jobs          │                     │                  │            │
 │  {job: s5cmd-copy HCL, │                     │                  │            │
 │   output_tags: {       │                     │                  │            │
 │     abc-src: abc://raw/│                     │                  │            │
 │     abc-dst: abc://cold/}}                   │                  │            │
 │ ──────────────────────►│                     │                  │            │
 │                        │ POST /v1/authorize  │                  │            │
 │                        │ {user, job,         │                  │            │
 │                        │  transfers:[        │                  │            │
 │                        │   {src:minio-raw:…, │                  │            │
 │                        │    dst:rustfs-cold:…}]}                │            │
 │                        │────────────────────►│                  │            │
 │                        │                     │ query :content/* │            │
 │                        │                     │ for data class ─►│            │
 │                        │                     │◄─ {:compliance/  │            │
 │                        │                     │   data-class     │            │
 │                        │                     │   :restricted}   │            │
 │                        │                     │                  │            │
 │                        │                     │ (DTA + consent + │            │
 │                        │                     │  clearance check)│            │
 │                        │                     │ → authorized     │            │
 │                        │                     │   + patch job    │            │
 │                        │◄─ {decision:allow,  │                  │            │
 │                        │   decision_id:xyz,  │                  │            │
 │                        │   patched_job}      │                  │            │
 │                        │                     │                  │            │
 │                        │ POST /v1/jobs ──────────────────────────────────────►
 │                        │◄─────────────────────────────────────── {job_id}   │
 │◄── {job_id, decision_id}│                     │                  │            │
 │                        │                     │                  │            │
 │  (CLI polls Nomad for  │                     │                  │            │
 │   alloc completion)    │                     │                  │            │
 │                        │                     │                  │            │
 │ POST /api/v1/data/     │                     │                  │            │
 │   index/finalize       │                     │                  │            │
 │   {src: abc://raw/…,   │                     │                  │            │
 │    dst: abc://cold/…,  │                     │                  │            │
 │    content_sha256:…,   │                     │                  │            │
 │    nomad_job_id:…,     │                     │                  │            │
 │    decision_id:xyz}    │                     │                  │            │
 │ ──────────────────────►│                     │                  │            │
 │                        │ XTDB tx:            │                  │            │
 │                        │  retract abc://raw/…│                  │            │
 │                        │  upsert abc://cold/…│                  │            │
 │                        │  prov/Activity{     │                  │            │
 │                        │   wasAssocWith:user,│                  │            │
 │                        │   decision_id:xyz,  │                  │            │
 │                        │   nomad_job:…}      │                  │────────────►
 │◄── 200 OK ─────────────│                     │                  │            │
```

Key: **Jurist queries XTDB for data classification** before authorizing a transfer involving `abc://` paths. This is the new data-transfer rule in Jurist — it reads `:content/compliance` from the data namespace to check whether the transfer crosses a boundary that the user's DTA covers.

---

#### Flow B — `abc data index refresh --backend minio`

```
CLI                    Khan                              Nomad          XTDB
 │                       │                                 │              │
 │ POST /api/v1/data/     │                                 │              │
 │   index/scan           │                                 │              │
 │   {backend:minio,      │                                 │              │
 │    mode:default}       │                                 │              │
 │ ──────────────────────►│                                 │              │
 │                        │  (build scanner Nomad job HCL)  │              │
 │                        │  POST /v1/jobs → Nomad ────────►│              │
 │                        │  (skip Jurist: scanner is       │              │
 │                        │   a privileged infra job)       │              │
 │◄── {scan_job_id} ──────│                                 │              │
 │                        │                                 │              │
 │                        │         Nomad alloc runs:       │              │
 │                        │         Pass A: s5cmd ls --etag │              │
 │                        │                 (s3-manifest)   │              │
 │                        │         Pass D: orchestrator    │              │
 │                        │          batched tx → XTDB ──────────────────►│
 │                        │                                 │              │
 │  (CLI polls scan job)  │                                 │              │
 │ GET /api/v1/data/       │                                 │              │
 │   index/status/{id}   │                                 │              │
 │ ──────────────────────►│── query XTDB for scan state ────────────────►  │
 │◄── {progress, counts} ─│◄─ {new:4231, updated:89}        │              │
```

The scanner job is submitted with a privileged namespace/node_pool that bypasses Jurist's DTA check (scanners don't move data, they read metadata). This is a new Jurist policy exception added in Phase 3.

---

#### Flow C — Event-driven ingest (bucket notification, Phase 5)

```
MinIO            NATS JetStream          bucket-event-ingester        XTDB
  │                   │                          │                       │
  │ s3:ObjectCreated  │                          │                       │
  │   {bucket, key,   │                          │                       │
  │    etag, size}   ──────────────────────────►  │                       │
  │                   │                          │  resolve etag (MD5)   │
  │                   │                          │  build location entity │
  │                   │                          │  xtdb tx:             │
  │                   │                          │  upsert abc://raw/…   │
  │                   │                          │  (or create content   │
  │                   │                          │   entity if new hash) │
  │                   │                          │ ────────────────────► │
  │                   │                          │ ack to JetStream ─────│
  │                   │                          │                       │
  │ s3:ObjectRemoved  │                          │                       │
  │ ──────────────────────────────────────────►  │                       │
  │                   │                          │  xtdb tx:             │
  │                   │                          │  retract abc://raw/…  │
  │                   │                          │ ────────────────────► │
```

NATS JetStream provides at-least-once delivery with consumer ack. If the ingester crashes mid-batch, it replays from the last ack'd offset — no events lost, no XTDB partial writes.

---

#### Flow D — `abc data chat "find all WGS BAMs not backed up"` (Phase 10)

```
CLI           Khan              abc-chat             abc-data-api        XTDB
  │              │                  │                     │                │
  │ POST          │                  │                     │                │
  │ /api/v1/      │                  │                     │                │
  │ data/chat/    │                  │                     │                │
  │ message       │                  │                     │                │
  │ ────────────► │                  │                     │                │
  │              │ POST /chat/message│                     │                │
  │              │ ────────────────► │                     │                │
  │              │                  │ LLM call (Anthropic) │                │
  │              │                  │ tool_choice:auto     │                │
  │              │                  │                      │                │
  │              │                  │ ← tool call:         │                │
  │              │                  │  search_files({      │                │
  │              │                  │   bio.assay:"WGS",   │                │
  │              │                  │   format_ext:"bam",  │                │
  │              │                  │   not_backed_up:true})               │
  │              │                  │                      │                │
  │              │                  │ POST /search ───────►│                │
  │              │                  │                      │ Datalog query  │
  │              │                  │                      │ ─────────────► │
  │              │                  │                      │◄── [{path,     │
  │              │                  │                      │    backend,    │
  │              │                  │                      │    backup:null}]
  │              │                  │◄── [{paths…}] ───────│                │
  │              │                  │                      │                │
  │              │                  │ → tool result to LLM │                │
  │              │                  │ ← LLM final message  │                │
  │              │◄── {message,     │                      │                │
  │              │   conv_id, tools}│                      │                │
  │◄── response ─│                  │                      │                │
```

The conversation entity (`:xt/id "chat:conv-<id>"`) is persisted to XTDB after each turn. `--resume <conv-id>` loads prior turns from XTDB and feeds them back to the LLM as history.

---

### 9.5 Jurist: new data-transfer rule set

Jurist currently has six rules (jurisdiction, DTA, consent, pipeline, clearance, transfer policy). The data platform adds a seventh: **data classification check**.

When a job submission contains `output_tags.abc-src` (an `abc://` path), Jurist queries XTDB for the content entity at that path and checks `:compliance/data-class`. This gate runs before DTA and consent checks so a `:restricted` dataset is caught early with a clear error.

```
New rule: data-classification
  input: output_tags.abc-src → resolve content entity
  check: :compliance/data-class ∈ {allowed-classes for user's DTA}
  deny if: :compliance/data-class = :restricted AND no DTA covers :restricted
  deny if: :compliance/erasure-tombstone exists
  allow otherwise (pass to downstream rules)
```

This is additive: jobs without `abc-src`/`abc-dst` output_tags (non-data-transfer jobs) skip this rule entirely. Existing behaviour is unchanged.

Jurist's `/vocab` endpoint gains a new vocabulary for data classes (`:compliance/data-class` values) backed by the `:vocab/` entities in XTDB — same controlled term registry as the bio annotations.

---

### 9.6 Khan: new `/api/v1/data/*` endpoints

Khan acts as the auth gateway for all data platform operations. It authenticates Bearer tokens (existing Sanctum middleware), then proxies to `abc-data-api`. No new auth infrastructure required.

| Method | Path | Proxies to | Description |
|---|---|---|---|
| `GET` | `/api/v1/data/resolve` | abc-data-api | Resolve `abc://` or `/abc/` path → `{backend, actual, content_sha256}` |
| `POST` | `/api/v1/data/index/scan` | abc-data-api | Submit a scan Nomad job for one or all backends |
| `GET` | `/api/v1/data/index/status/{id}` | abc-data-api | Poll scan job progress + entity counts |
| `POST` | `/api/v1/data/index/finalize` | abc-data-api | Write-through after a successful CLI transfer |
| `GET` | `/api/v1/data/check` | abc-data-api | `check` by path, hash, or local file hash |
| `GET` | `/api/v1/data/search` | abc-data-api | Full query surface (path, tags, bio, backend, size, time) |
| `GET` | `/api/v1/data/show` | abc-data-api | Full lineage timeline for one path |
| `POST` | `/api/v1/data/annotate` | abc-data-api | Write bio/tag annotations |
| `GET` | `/api/v1/data/lineage` | abc-data-api | Upstream/downstream graph traversal |
| `POST` | `/api/v1/data/chat/message` | abc-chat | LLM tool-calling chat turn |
| `GET` | `/api/v1/data/chat/{conv_id}` | abc-chat | Load conversation history |

Khan adds these routes to its existing `/api/v1/` namespace. The proxy is a thin `Http::get/post` call (Laravel HTTP client) — no business logic in Khan for these routes. The operator console gains a "Data Index" Filament resource page (table of content entities, search, annotation editor).

---

### 9.7 abc-data-api — the new internal Go service

A stateless HTTP service (port 7420 by default) that sits behind Khan. No auth — Khan is the trust boundary. Connects to XTDB via HTTP (same XTDB_URL env var).

**Responsibilities:**
- Resolve `abc://` paths via Datalog queries
- Execute search/check/show/lineage queries
- Write content + location entities (finalize, annotate, scan ingest)
- Submit scan Nomad jobs (calls Nomad API directly with a privileged token stored in its own env)
- Validate vocabulary constraints on annotation writes

**Not responsible for:**
- Auth (Khan handles it)
- Job submission governance (Jurist handles it for transfer jobs)
- Bucket event ingest (bucket-event-ingester handles it)
- LLM chat (abc-chat handles it)

**Deployment:** Nomad service job, 2 replicas, `system` network mode, no public port exposure. Khan reaches it via Consul service discovery (`abc-data-api.service.consul:7420`).

---

### 9.8 NATS JetStream — event bus (Phase 5)

NATS JetStream is deployed as a Nomad service job in Phase 5 (alongside the bucket event ingester). Topics:

| Stream | Publisher | Subscriber | Retention |
|---|---|---|---|
| `abc.bucket.minio` | MinIO webhook sink | bucket-event-ingester | 7 days |
| `abc.bucket.rustfs` | RustFS webhook sink | bucket-event-ingester | 7 days |
| `abc.nf.lineage` | nf-lineage-ingester (bucket watcher) | nf-lineage-ingester (fan-out to XTDB) | 24h |
| `abc.data.events` | abc-data-api (scan completion, finalize) | lineage-bridge (→ OpenLineage) | 30 days |

MinIO can publish directly to a NATS topic (`minioadmin mc event add minio alias/bucket arn:minio:sqs::nats:s3:ObjectCreated:*`). The ingester acknowledges per event; NATS replays from last ack on restart.

NATS is a single lightweight Go binary (≈30 MB). It requires no JVM, no ZooKeeper, no complex partitioning setup. A 3-node NATS cluster for HA is appropriate; each node is a Nomad allocation pinned to a different host.

---

### 9.9 Deployment topology — which services run where

All new services are Nomad jobs. The deployment sizing assumes a cluster of ~12 compute/storage nodes.

| Service | Nomad type | Replicas | Constraints | Notes |
|---|---|---|---|---|
| `abc-data-api` | service | 2 | any | Stateless; both replicas active |
| `bucket-event-ingester` | service | 1 | any | JetStream consumer; 1 active writer |
| `nf-lineage-ingester` | service | 1 | any | Bucket poller; 1 active |
| `lineage-bridge` | service | 1 | any | XTDB tx-log consumer; 1 active |
| `restic-index-bridge` | service | 1 | any | Restic manifest reader; 1 active |
| `restic-check-runner` | periodic | 1 | `meta.class=storage` | Daily; storage-class node |
| `index-refresh` (scan) | batch | 1 per run | node-specific | Submitted on demand via `abc data index refresh` |
| `NATS JetStream` | service | 3 | distinct hosts | HA cluster; pinned to 3 different nodes |
| `abc-chat` | service | 1 | any | Stateless; LLM API key in Vault |
| `drs-server` | service | 2 | any | Read-only DRS; stateless |

XTDB and OPA are existing services — not added by the data platform. XTDB gets the Lucene module added as a configuration change.

---

### 9.10 Reserved bucket prefixes and naming conventions

All data platform internal objects live in dedicated prefixes within the existing MinIO bucket (or a dedicated `_abc-internal` bucket) to prevent collision with user data:

| Prefix | Owned by | Contents |
|---|---|---|
| `_abc-journals/<runid>/` | abc data move/copy CLI | NDJSON safe-move journals |
| `_abc-scan/<runid>/` | scanner Nomad job | Intermediate manifests (size-manifest.tsv, dupe-groups.json, hashes.bsd, s3-manifest.ndjson) |
| `_nf-lineage/` | Nextflow runs | Lineage event JSON files (watched by nf-lineage-ingester) |
| `_sboms/<activity-id>.json` | data platform finalize | CycloneDX SBOMs for pipeline activities |
| `_abc-index/` | abc-data-api | Lucene index snapshots (for cold-start recovery) |

These prefixes are excluded from bucket event notifications to prevent the ingester from self-triggering on its own writes.

---

### 9.11 Source-selection ladder (which copy do we read?)

When content exists at multiple locations (the multi-location use cases in §2), every read path needs a deterministic way to choose. This ladder lives in `abc-data-api/internal/sourceselect/` and is invoked by `abc data restore`, `abc data check --from-file` confirmation, pipeline input fetch, computation cache reuse, and the chat tool's `get_entity` resolution.

**Inputs to the scorer (from each candidate location's Backend entity + the user's read-policy bundle):**

```
Score(location) = f(
  available?           → ∞ if :location/availability ≠ :available
  ACL-permitted?       → ∞ if Jurist read-policy denies this backend's data-class
                            or jurisdiction
  user-has-creds?      → ∞ if :backend/access-profile :requires-auth and the user
                            lacks the auth (Crypt4GH key, DTA, presigned token)
  egress-cost-per-gb   → linear penalty
  typical-latency-ms   → linear penalty
  reliability          → log-scale penalty (0.99 vs 0.9999 matters)
  encryption-overhead  → small penalty if :location/at-rest-encryption requires
                            user-authorised decryption
)
```

**Default ranking pattern (cluster-local first, off-prem last):**

```
1. backend:host:<this-node>      score: 1     local node, sub-1ms (if available)
2. backend:minio-*               score: 1     cluster-hot, sub-10ms, free
3. backend:rustfs-*              score: 3     cluster-warm, ~20ms, free
4. backend:nas:*                 score: 5     LAN, ~50ms, free, requires mount
5. backend:host:<peer-node>      score: 8     LAN, peer compute node
6. backend:disk:*                score: 12    portable disk; transient unless mounted
7. backend:garage-*              score: 15    cluster-cold, ~100ms, free
8. backend:storj-private         score: 20    LAN-class but cold tier, ~200ms
9. backend:restic-*              score: 30    requires extraction step
10. backend:public-url:*         score: 100   internet, no egress to us, ~500ms
11. backend:drs:*                score: 200   cross-institution, requires DTA
12. backend:storj-public         score: 1000  off-prem, paid egress, requires consent
```

**Override mechanisms:**
- `--from <backend-id>` flag — user explicitly picks a backend regardless of score
- `--prefer-local`, `--prefer-cheap`, `--allow-public` — adjust scorer weights
- `--from-public-dr` — required to access `backend:storj-public` (Phase 5.6.c constraint)
- ACL never overridden by score — denied backends are denied

**Where it lives in code:** `abc-data-api/internal/sourceselect/scorer.go` exposes `Rank(content_id, user_context) []ScoredLocation`. Returned list is sorted ascending by score; first available element is the default pick. Pure function over read-policy bundle + Backend access-profiles + Location availability — easy to unit test.

**Edge cases:**

- *No reachable copy.* All candidates have score ∞. Return a structured error to the CLI: "Content `sha256:a3f9…` exists in N locations but none are currently reachable to you. Locations: …". CLI surfaces this as actionable advice (mount this disk, request this DTA, come online from this host).
- *Cache reuse cross-workspace.* If the only candidate is in a workspace the user can't read but `:cache-hit-cross-workspace` policy is `:topology-only`, the scorer reports "exists, request access" rather than failing silently.
- *Encryption-required reads.* If the lowest-score candidate is at-rest-encrypted, the CLI prompts for the key (or unlock confirmation) before proceeding. `--unattended` skips encrypted candidates.
- *Public URL freshness.* Public URLs aren't periodically re-verified; if the public host is down, score-rank returns the next candidate without retry storms.

This logic is used by every read path; building it once in abc-data-api means the CLI, scan-job, and chat tools all rank consistently.

---

## 10. Implementation roadmap (no-Khan baseline)

§9 describes the **target architecture** with Khan as the user-facing trust boundary. This section is the **execution roadmap** for getting there in a world where Khan does not yet exist. The data platform is built service-first; Khan is a deployment-mode change layered on later.

### 10.1 Locked-down decisions

| # | Decision | Choice |
|---|---|---|
| 1 | End-user authentication | **PAT (Personal Access Token) stored in XTDB.** CLI sends `Authorization: Bearer abc_pat_<base32>`. abc-data-api hashes the token and matches against `:agent/access-tokens` on user entities. No external IdP, no Tailscale identity injection, no mTLS cert distribution. |
| 2 | Workspace identity source | **Explicit in `~/.abc/contexts/<ctx>.yaml`** — CLI sends as a header; abc-data-api validates against the resolved user's XTDB `:workspace/memberships` set (rejects if user is not a member of the claimed workspace). |
| 3 | Admin allowlist source | **XTDB-native, ground up** — no Vault, no static config; admin is a `:role/cluster :admin` attribute on user entities (see §10.2). |
| 4 | Service identity (ingesters, watcher, scanner) | **Nomad workload identity (JWT).** Nomad 1.7+ issues per-allocation JWTs with configurable audiences. Service jobs declare `identity { audience = ["abc-data-api"] }` in their job spec; abc-data-api validates incoming JWTs against Nomad's JWKS endpoint and resolves the `nomad_job_id` claim to a service entity in XTDB. No long-lived service tokens to rotate. |
| 5 | Sprint parallelism | Sprints 0/1 run in parallel across two engineers (or scope-divided for one); transfers track and instrument-watcher track are independent. |
| 6 | Discovery commands (`ls`, `find`, `stat`, `du`, `tree`, tab completion) | **Promoted into Sprint 3** — they are the only operator console until Khan ships, and the project's stated direction is to lean on XTDB heavily right now. |

**No reliance on Tailscale for identity.** Tailscale (or any other VPN/private network) may still be the network transport layer to reach abc-data-api, but the *identity* of the caller is established via the Bearer token (end-user) or signed JWT (service) — never via network-injected headers. This makes the auth model portable: the CLI works the same against a Tailscale-private endpoint, a public TLS endpoint, or a future Khan-fronted endpoint.

### 10.2 Bootstrap identity model — XTDB from the ground up

Because Khan and Vault aren't available yet, **XTDB carries the full identity, workspace, role, ACL, and credential state** from day one. This is not a fallback — it's a deliberate choice, and it doesn't change when Khan arrives (Khan reads the same XTDB entities).

**Schema additions for Sprint 0:**

```clojure
;; User entity — one per cluster user
{:xt/id                 "agent:user:abhinav"
 :prov/type             :agent
 :agent/kind            :user
 :agent/name            "Abhinav Sharma"
 :agent/email           "abhinav@biosharp.net"

 ;; PAT credentials — set of token records, one per device/CI/etc.
 ;; Stored as bcrypt hashes; the raw token is never persisted.
 :agent/access-tokens   #{{:hash       "$2a$12$<bcrypt-hash>"
                           :name       "laptop"
                           :created-at #inst "2026-04-30T09:00:00Z"
                           :expires-at #inst "2027-04-30T09:00:00Z"   ; or omit for non-expiring
                           :last-used  #inst "2026-04-30T11:42:00Z"
                           :scope      #{:read :write}}                ; optional per-token scope
                          {:hash       "$2a$12$<bcrypt-hash>"
                           :name       "ci-runner-prod"
                           :created-at #inst "2026-04-30T09:30:00Z"
                           :scope      #{:read}}}

 :workspace/memberships #{"ws-genomics-lab-001"
                          "ws-collab-shared"}
 :workspace/primary     "ws-genomics-lab-001"
 :role/cluster          :user                  ; :user | :admin | :data-steward
                                                ;       | :compliance-officer
 :clearance/tier        :tier-2}

;; Workspace entity
{:xt/id              "ws-genomics-lab-001"
 :workspace/name     "Genomics Lab 001"
 :workspace/created  #inst "2026-04-30T00:00:00Z"
 :workspace/owner    "agent:user:dr-smith"
 :workspace/buckets  #{"minio-raw" "rustfs-cold-001"}
 :workspace/jurisdiction :ZA}

;; Service identity (ingesters, watcher, scanner)
{:xt/id                  "agent:service:instrument-watcher"
 :prov/type              :agent
 :agent/kind             :service
 :agent/nomad-job-id     "abc-instrument-watcher"   ; matches Nomad job spec name;
                                                    ; key for nomad_job_id JWT claim lookup
 :role/scope             :ingest                    ; :ingest | :scan | :index-write
 :workspace/memberships  #{"ws-instruments-001"}}
```

**Token format:** `abc_pat_<32-char-base32>` — the prefix makes leaked tokens trivially recognisable in logs and `git grep`. 32 characters of base32 = 160 bits of entropy. Stored on disk by the CLI in `~/.abc/secrets.yaml` (existing infrastructure under `cmd/secrets/`). Never written to XTDB in plaintext; always bcrypt-hashed at write time.

**Identity resolution flow in abc-data-api:**

```
HTTP request arrives
      │
      ▼
Authorization: Bearer <token>  ──┬──► token starts with "abc_pat_"  ──► PAT path
                                  │      (end-user request)
                                  │
                                  └──► token is a JWT             ──► Service path
                                         (signed by Nomad)

PAT path                                     Service path
────────                                     ────────────
1. bcrypt-compare incoming token             1. Validate JWT signature against
   against :agent/access-tokens hashes          Nomad JWKS (cached, refreshed hourly)
   (Datalog: locate user by token-prefix     2. Verify "aud" claim contains
    fingerprint, then bcrypt-compare; the       "abc-data-api"
    fingerprint avoids full-table scan)      3. Verify "exp" not expired
2. Verify token not expired                  4. Extract "nomad_job_id" claim
3. Update :last-used timestamp (async)       5. [?s :agent/nomad-job-id <id>]
4. Resolve user → role + workspaces             → resolve to agent:service:* entity
                                                 plus scope + workspaces

CLI-supplied X-Abc-Workspace header  ──►   Validate against the resolved user's
                                            :workspace/memberships set
                                            (reject if not a member)
```

In-process cache: 30 s TTL keyed by token-fingerprint (PAT) or JWT `jti` (service). Lookup cost is one Datalog query per cache miss. No extra service, no external IdP.

**Token-prefix fingerprint trick:** PAT lookup needs to be O(1) on the identity-lookup hot path. We store an additional `:agent/access-token-fingerprints` set of 64-bit hashes (`xxhash` of the raw token, fast and non-cryptographic). On request: hash the incoming token's prefix, query XTDB by fingerprint to narrow to one candidate user, then bcrypt-compare for cryptographic verification. This is the same pattern Sanctum uses internally and the same one Khan will use when it arrives.

**Bootstrap data:** Sprint 0 includes a small init job (`infra/nomad-jobs/identity-seed.hcl`) that:

1. Reconciles `infra/identity/{users,workspaces,services}.yaml` into XTDB entities (idempotent — safe to re-run).
2. **On first run only**, if no admin user exists in XTDB, mints a random admin PAT, prints it once to job logs as `INITIAL_ADMIN_TOKEN: abc_pat_...`. The operator captures this from the logs and stores it. From this point, all further user/token management goes through `abc admin user create` / `abc auth tokens create`.

The YAML lives in version control alongside infra; subsequent identity changes go via CLI commands (Sprint 5+) which write XTDB entities directly.

**Token rotation and revocation:**
- `abc auth tokens list` — list user's own tokens (without revealing hashes).
- `abc auth tokens create [--name N] [--expires-in 90d] [--scope read,write]` — mints a new token; raw token printed exactly once.
- `abc auth tokens revoke <name|hash-prefix>` — removes the token record from the user entity.
- `abc admin user revoke-tokens <user>` (admin only) — bulk revoke for compromised accounts.
- Revocation is immediate; identity cache TTL is 30 s, so worst-case window is 30 s.

**When Khan arrives** (later): Khan issues Sanctum tokens which are stored as additional `:agent/access-tokens` entries on the same user entities. abc-data-api's PAT validation path is unchanged — Sanctum tokens are just a different prefix (`abc_san_...` vs `abc_pat_...`) that flows through the same bcrypt-comparison path. No schema reset, no migration; the identity store is unchanged.

### 10.3 Sprint sequence

Calendar weeks assume one engineer; the parallel tracks within a sprint require either two engineers or sequential scheduling.

#### Sprint 0 — Foundation (3 days)

**Goal:** XTDB-Lucene + NATS + OTel online; identity entities seeded; abc-data-api skeleton deployed but mostly empty.

**Deliverables:**
- `infra/nomad-jobs/xtdb.hcl` — XTDB v2 with Lucene module, persistent volume, daily snapshot job.
- `infra/nomad-jobs/nats.hcl` — NATS JetStream 3-node cluster, distinct-host constraint.
- `infra/nomad-jobs/otel-collector.hcl` — OpenTelemetry collector.
- `infra/nomad-jobs/identity-seed.hcl` — periodic job that reconciles `infra/identity/{users,workspaces,services}.yaml` into XTDB entities; on first run mints + prints initial admin PAT.
- `analysis/packages/abc-data-shared/` (new Go module): entity types (Content, Location, Activity, Agent, **User, Workspace, Service, AccessToken**), NATS event schemas, XTDB client wrapper, OTel-instrumented HTTP middleware.
- `analysis/packages/abc-data-api/` (new): HTTP server skeleton, **dual-mode auth middleware** that recognises:
  - `Authorization: Bearer abc_pat_<...>` → PAT path (xxhash-fingerprint lookup → bcrypt-compare → user entity)
  - `Authorization: Bearer eyJ<JWT>` → service path (Nomad JWKS verify → `nomad_job_id` claim → service entity)
  Both paths produce a uniform `RequestIdentity{user_id, workspaces, role, scope}` value passed to handlers. Healthcheck endpoint included; no domain endpoints yet.
- CLI extension: `cmd/auth/login.go` — `abc auth login --token abc_pat_<...>` stores the PAT in `~/.abc/secrets.yaml` (uses existing `cmd/secrets/` infrastructure).

**Test gate:**
1. `curl <xtdb>/_xtdb/status` returns healthy; identity-seed job populates expected user/workspace/service entities; initial admin PAT visible in logs once.
2. NATS publish/subscribe round-trips, durable across node restart.
3. abc-data-api `GET /healthz` with `Authorization: Bearer abc_pat_...` returns ok with caller's resolved identity (e.g., `{user_id: "agent:user:abhinav", workspaces: ["ws-a"], role: "user"}`); same endpoint with a Nomad-issued JWT returns the corresponding service identity; same endpoint without a token returns 401.
4. Token revocation: revoke a token via `abc auth tokens revoke`; within 30 s, requests with that token return 401.

#### Sprint 1 — Robust transfers + instrument watcher (parallel, ~9 days)

**Track A: Robust transfers (5d) — extends `cmd/data/transfer.go`**
- Two-phase safe move (plan → copy → verify → delete-source → finalize), replacing today's `rclone move`.
- NDJSON journal in `bucket://_abc-journals/<runid>/` (`cmd/data/journal.go`, new).
- Tool routing `selectTool()` chooses s5cmd for S3-involved hops; rclone otherwise.
- s5cmd Nomad script variant in `cmd/data/nomad_submit.go`.
- Host-volume HCL support in `internal/hclgen/job/generator.go` (new `HostVolumeMount` field, `volume`/`volume_mount` block emission).
- `--via <rclone-remote>` single-hop relay (`cmd/data/transfer_relay.go`, new).
- `--verify=size|deep` flag.

Talks **directly to Nomad** via existing Nomad ACL token in CLI context. No Khan, no abc-data-api dependency.

**Track B: Instrument watcher (4d) — new `analysis/packages/abc-instrument-watcher/`**
- fsnotify-based directory watcher.
- Sentinel detection: `RTAComplete.txt` (Illumina), `final_summary_*.txt` (Nanopore), `.transferdone` (PacBio).
- Platform parsers: `RunInfo.xml` + `SampleSheet.csv`, `sequencing_summary_*.txt`, `.metadata.xml`.
- Local state journal at `/var/lib/abc-instrument-watcher/state.json` (atomic rename writes).
- Submits transfer jobs **directly to Nomad** via Nomad ACL token (no Khan).
- Provenance writes deferred to Sprint 2 (when abc-data-api accepts them).
- **Authenticates to abc-data-api in Sprint 2 via Nomad workload identity (JWT).** The Nomad job spec includes an `identity { audience = ["abc-data-api"] }` block; Nomad injects a signed JWT at `${NOMAD_TOKEN_FILE}`; the watcher reads it and sends it as a Bearer token. Rotation is automatic — Nomad re-issues the JWT on the configured cadence with no service-side handling needed.
- Nomad system job at `infra/nomad-jobs/abc-instrument-watcher.hcl`.

**Test gates:** §7 Phase 1 + Phase 0.5 test gates from this design.

#### Sprint 2 — Index online + basic discovery (~6 days)

**Goal:** abc-data-api handles real queries; CLI can resolve, check, stat, show. PAT auth (end-users) and Nomad-JWT auth (services) enforce identity.

**Deliverables:**
- abc-data-api endpoints: `/v1/resolve`, `/v1/stat`, `/v1/list`, `/v1/show`, `/v1/index/finalize`, `/v1/check`.
- Typed query layer in `abc-data-api/internal/xtdb/`: `Resolve`, `LookupByHash`, `ListByPrefix`, `Stat` (parallel fetches: location + content + sibling count + snapshot membership).
- CLI extensions:
  - `internal/indexer/api.go` — Go client for abc-data-api.
  - `cmd/data/transfer.go` — gains resolution call before submitting; finalize hook calls `/v1/index/finalize`.
  - `cmd/data/check.go` — `abc data check` (path | hash | local file).
  - `cmd/data/stat.go` — `abc data stat` rich metadata.
  - `cmd/data/show.go` — `abc data show` (lineage timeline via XTDB entity-history).
- abc-instrument-watcher writes sequencing-run Activity to abc-data-api before submitting transfer.
- Tab completion endpoint `/v1/complete?prefix=...&kind=path|vocab` returning ≤50 results in <50 ms.

**Test gate:** §7 Phase 3 test gates 1–7 minus the scan-related tests (covered in Sprint 3).

#### Sprint 3 — Scanner + namespace + read-ACL + discovery commands (~12 days, three parallel tracks)

**Track A: Multi-pass scanner (5d)** — §7 Phase 3 scan deliverables in full: fd + fclones + rhash + b3sum + s5cmd; Pass A→D orchestrator; modes `default | --bootstrap | --audit | --backend <x> | --deep`. `abc data index refresh` CLI command.

**Track B: abc:// dual-form (3d)** — `internal/indexer/parse.go` for both `abc://x` and `/abc/x` forms; canonicalisation; `--style` flag and `ABC_PATH_STYLE` env. §7 Phase 4.

**Track C: Read-ACL via Jurist (4d)**
- `analysis/packages/abc-jurist-svc/src/abc/policy/http/routes.clj` — add `POST /v1/read-policy/compile`.
- `analysis/packages/abc-jurist-svc/src/abc/policy/emit/read_policy.clj` (new) — compiles bundle from existing rule subset (jurisdiction + DTA + consent + clearance).
- `abc-data-api/internal/policy/` — Jurist client, per-user bundle cache (LRU + 15-min TTL).
- `abc-data-api/internal/policy/datalog.go` — bundle → Datalog `:where` clause translator.
- `BuildQuery()` always appends ACL clauses (admin role omits).
- NATS topic `abc.jurist.read-policy.invalidate`.
- Schema additions: `:workspace/owner`, `:workspace/shared-with` on locations; `:location/jurisdiction` on locations.

**Track D: Discovery commands (3d)** — promoted from Phase 11 because there's no Khan operator console:
- `cmd/data/ls.go` — `abc data ls` with `-l`, `-h`, `-a`, `-R`, `-t`, `-S`, `--tier`, `--bio.*`, `--tag` filters.
- `cmd/data/find.go` — BSD/GNU find syntax: `-name`, `-size`, `-mtime`, `-type`, `-bio.assay`, `-orphan`, `-single-copy`.
- `cmd/data/du.go` — disk usage with `--by-tier`, `--by-backend`, `--by-content` (dedup-aware).
- `cmd/data/tree.go` — directory tree visualization with file-counts-per-branch.
- Tab completion: bash + zsh completion scripts via `abc completion bash`.
- Defer to later sprints: `cd`/`pwd`, `grep --content`, `peek`, `head`/`tail`/`wc`, `samples`/`studies`/`pipelines`/`activities`/`agents`/`dups`/`stale`/`orphans` (need Phases 6/7/8 schema).

**Test gates:** §7 Phase 3, Phase 4 gates + new ACL gates:
1. User in ws-a sees only ws-a-owned entities in `abc data ls /abc/`.
2. Admin role bypasses; CLI prints `[admin] showing all workspaces` banner.
3. Jurist DTA addition → invalidation event → next query reflects new policy within 5s.
4. Tab completion respects ACL.

#### Sprint 4 — Event-driven indexing (~4 days)

§7 Phase 5 in full: `abc-bucket-ingester` Nomad service; MinIO/RustFS bucket notifications via webhook → NATS `abc.bucket.{minio,rustfs}` → ingester → XTDB upserts/retracts.

After Sprint 4, the **ingest loop is fully automatic**:

```
Sequencer  →  abc-instrument-watcher  →  Nomad transfer  →  MinIO
   files arrive in MinIO  →  bucket notification  →  NATS
   →  bucket-event-ingester  →  XTDB location entities
   →  scanner (next refresh)  →  XTDB content entities with full hashes
   →  Jurist read-policy bundle gates who can see what
   →  CLI ls/find/stat/check returns the data
```

Zero manual steps from sequencer to indexed file.

#### Sprint 5+ — Subsequent phases per design §7

In order:
- Sprint 5 — restic + Garage (Phase 5.5) + restic-index-bridge.
- Sprint 6 — `abc data search` + saved searches (Phase 6).
- Sprint 7 — annotations + vocabulary + OLS sync (Phase 7).
- Sprint 8 — PROV provenance + activity finalize hooks (Phase 8).
- Sprint 9 — lineage outlets + IGV.js operator UI (Phase 8.5; IGV.js as standalone web app at this stage since no Khan; embeds into Khan when it ships).
- Sprint 10 — standards + interop: DRS + htsget + refget + Pixi-fingerprint + BCO + RO-Crate (Phase 8.7, ~9 days).
- Sprint 11 — Nextflow lineage (Phase 9).
- Sprint 12 — chat + LiteLLM (Phase 10).
- Sprint 13 — Storj private / restic resilience (Phase 5.6, optional).
- Sprint 14 — FUSE (Mountpoint) + computation reuse cache (Phase 11).

Reordering note: Phase 5.7 (integrity + heal) is most useful **after** Phase 5.5 (restic) lands a recovery source — that's its current position. No change.

### 10.4 Concrete first-week deliverables (PR-sized tickets)

If implementation starts tomorrow, these five PRs land in week one:

1. **`infra/nomad-jobs/xtdb.hcl`** — XTDB-Lucene Nomad job spec + persistent-volume config + daily snapshot periodic job.
   *Smoke test:* `curl /_xtdb/status` returns healthy; restart node mid-tx, verify durability.

2. **`infra/nomad-jobs/nats.hcl`** — NATS JetStream 3-node cluster, distinct-host constraint, topic provisioning init job.
   *Smoke test:* pub/sub round-trip survives node restart.

3. **`infra/nomad-jobs/otel-collector.hcl`** + `analysis/packages/abc-data-shared/internal/otel/` initial Go instrumentation library.
   *Smoke test:* hello-world client → trace visible in collector.

4. **`infra/identity/{users,workspaces,services}.yaml`** + **`infra/nomad-jobs/identity-seed.hcl`** — bootstrap identity data + periodic reconciler.
   *Smoke test:* deploying identity-seed populates expected user/workspace/service entities; second run is idempotent.

5. **`analysis/packages/abc-data-shared/`** + **`analysis/packages/abc-data-api/`** — Go module skeletons. Shared module: entity types (incl. User, Workspace, Service, AccessToken), NATS schemas, XTDB client. API module: HTTP server, dual-mode auth middleware (PAT bcrypt-compare via XTDB; Nomad JWT via JWKS), healthcheck.
   *Smoke test:* `GET /healthz` with `Bearer abc_pat_<...>` returns end-user identity; with a Nomad-issued JWT returns service identity; without a token returns 401.

These five PRs are the foundation. Sprint 1's transfer hardening and instrument watcher then start in parallel (Tracks A and B).

### 10.5 Khan migration — when it arrives

Roughly 5 days when Khan ships. Mostly config + auth-middleware changes:

1. Khan adds `/api/v1/data/*` proxy routes (one Laravel controller, ~200 LOC).
2. Khan issues Sanctum tokens (`abc_san_<base32>`) which are stored as additional entries in the same `:agent/access-tokens` set on user entities. abc-data-api's PAT validation path is unchanged — Sanctum tokens flow through the same xxhash-fingerprint + bcrypt-compare lookup, distinguishable only by token prefix.
3. abc-data-api gains an `auth_mode: pat|khan|both` config flag. `both` accepts either prefix during a transition period; `khan` rejects raw `abc_pat_*` tokens and requires Khan-issued `abc_san_*` tokens (or Khan-injected `X-Abc-User-*` headers when Khan is the trusted proxy).
4. CLI context profile gains a `khan_url` field; if set, the CLI's `abc auth login` flow redirects through Khan's session/login UI to obtain a Sanctum token; if empty, `abc auth login --token <pat>` continues to work.
5. Network ACL update: once all end-user clients route through Khan, restrict abc-data-api's PAT path to internal callers only; Khan-issued Sanctum tokens become the only end-user auth.
6. Service identities (ingesters, watcher, scanner) **do not migrate** — they keep using Nomad workload identity (signed JWTs). Khan handles only end-user requests; service-to-service auth is independent of Khan's existence.

The XTDB identity entities are unchanged through this migration — that's the point of putting them in XTDB from day one.

### 10.6 Risks and mitigations

| Risk | Mitigation |
|---|---|
| User PAT leaked (committed to git, captured from terminal scrollback, etc.) | Token prefix `abc_pat_` is recognisable in `git grep`/secret scanners; `abc auth tokens revoke` is immediate (≤30 s cache TTL); `abc admin user revoke-tokens <user>` for bulk compromise; tokens optionally carry `:expires-at`; bcrypt cost factor 12 makes offline cracking impractical |
| Initial-admin token mishandled at bootstrap | identity-seed prints token exactly once and only on first run; subsequent runs warn + skip if any admin user already exists in XTDB; operator runbook documents capture-from-logs procedure |
| Service JWT signing key compromise (Nomad keystore) | Nomad rotates JWT signing keys on a configured schedule; abc-data-api refreshes JWKS hourly; Nomad operations runbook covers key-rotation incident response (industry-standard JWT mitigation, not project-specific) |
| Identity-seed YAML drifts from XTDB reality | identity-seed is a *reconciler*, not a one-time loader: re-runs are idempotent; YAML stays in version control as the source of truth |
| abc-data-api becomes a single point of failure | Sprint 0 deploys 2 replicas; stateless design; XTDB is the durable state |
| XTDB unavailable during deploy → CLI unusable | abc-data-api fails closed with clear error; CLI's `--local --rclone-config` path stays operational for emergency transfers |
| Read-policy bundle compile times grow with DTA count | Cache 15 min; invalidation only on actual policy change; benchmark Jurist `compile` at 100/500/1000 DTAs in Sprint 3 acceptance |
| Discovery commands feel slow without Khan operator UI | Tab completion latency budget enforced (<50 ms); paginate all list commands; CLI defaults to first 50 results with `--all` to override |

### 10.7 What this roadmap does NOT do

- Does not build Khan; assumes someone else delivers it on a separate timeline.
- Does not build Phase 11 FUSE mount (Mountpoint for S3) until Sprint 14.
- Does not build the operator-console UI; CLI commands cover inspection until Khan ships.
- Does not pre-pick external tool versions (htsget reference impl, MultiQC, Pixi); those are Sprint-10 implementation decisions.
- Does not address Storj private deployment until Sprint 13 (optional; depends on whether Garage proves sufficient).

---

## Critical files

- [cmd/data/transfer.go](analysis/packages/abc-cluster-cli/cmd/data/transfer.go)
- [cmd/data/nomad_submit.go](analysis/packages/abc-cluster-cli/cmd/data/nomad_submit.go)
- [internal/hclgen/job/generator.go](analysis/packages/abc-cluster-cli/internal/hclgen/job/generator.go)
- `internal/indexer/xtdb.go` (new — typed XTDB-Lucene client)
- `internal/indexer/scan.go` (new — s5cmd + rhash output → batch transactions)
- `cmd/data/index.go` (new — `abc data index refresh` subcommand)
- `cmd/data/check.go` (new — content-addressed lookup)
- `cmd/data/search.go` (new — full query surface, Phase 1.5)
- `internal/indexer/search.go`, `aggregate.go`, `saved.go`, `querydsl.go` (Phase 1.5)
- `internal/indexer/annotate.go` (new — biological annotation + vocabulary validation, Phase 2)
- `internal/indexer/lineage.go` (new — PROV-aligned graph traversal, Phase 2)
- `cmd/data/annotate.go`, `cmd/data/lineage.go` (new — annotation + lineage subcommands, Phase 2)
- `internal/indexer/nflineage.go`, `services/nf-lineage-ingester/`, `plugins/nf-abc-lineage/` (Phase 2.5)
- `cmd/data/journal.go` (new — NDJSON safe-move journal)
- `cmd/data/transfer_relay.go` (new — relay orchestration)

---

# Appendix A — Decentralized long-term archival (brainstorm, NOT approved scope)

Captured for future reference. Discussed late in the planning conversation; parked as an idea to revisit *after* Phase 5.5 (restic + Garage) has been in production long enough to reveal whether external long-term durability is a real gap.

## Original question

> "Is there a way we can combine the use of Storj and torrent networks to make sure that the data which is compressed, de-anonymized can be stored in the torrent network for long term redundancy?"

## Framing clarifications applied

1. **"Torrent network for long-term storage" is largely a myth.** Torrents only work as long as someone is seeding. There is no economic mechanism that keeps random peers seeding scientific data — unlike Storj, where Storage Node Operators are paid in tokens for proving they hold blocks. A torrent with no seeders dies in days. What the design would actually build is a **federated seedbox network** of always-on nodes operated by us or partner institutions.
2. **"De-anonymized" → de-identified.** For human-derived data (variants, expression, even allele frequencies), de-identification ≠ anonymization. Re-identification attacks on supposedly anonymous genomic data are well-documented (Homer 2008, Gymrek 2013). Anything going onto a public-ish swarm needs encryption or aggregation.
3. **Storj already does most of what hand-built torrents would.** Storj erasure-codes every object into 80 pieces across thousands of independent operators globally; repair is automatic. The reason to *also* use torrents is not decentralization — it is **independence from Storj as a company** (regulatory risk, pricing changes, bankruptcy).

## Recommended three-tier external archive (instead of two)

| Tier | Tool | When to use |
|---|---|---|
| External hot archive | **Storj DCS** (~$4/TB/mo storage, ~$7/TB egress) | Frequently-cited datasets needing minute-scale retrieval. S3-compatible — drops into existing tool routing (s5cmd works against Storj's S3 gateway). |
| Permanent sealed archive | **Arweave** (~$5–10/GB **one-time**) | Sealed published artefacts that should outlive any institution. Content-addressed, designed for centuries-scale durability. |
| Community-mirrored archive | **Federated seedbox network with private trackers** | Datasets a research consortium agrees to mutually mirror. Useful where Storj pricing or data-residency rules don't fit. Operationally heavy. |

## Realistic archival pipeline

```
abc data archive --tier public-archive abc://study-x/

  1. Filter   :  only contents with :bio.share-public true
  2. De-ID    :  bcftools annotate -x INFO,FORMAT/...; strip PHI from headers
  3. Compress :  zstd --long=27 -19  (or genozip for FASTQ/BAM)
  4. Encrypt  :  age -r <recipient-key>  (or Crypt4GH for GA4GH compat)
  5. Chunk    :  mktorrent -p (private flag) -l 22 (4 MiB pieces)
  6. Upload   :  in parallel:
                   - Storj via uplink → bucket://storj-archive/<study>/
                   - Magnet link + payloads → seedbox network
                   - (optional) Arweave for sealed permanent copy
  7. Index    :  XTDB writes a :archive/PublicArchive entity per content
                   :archive/storj-grant         <signed access grant ref>
                   :archive/magnet-uri          <magnet:?xt=urn:btih:...>
                   :archive/torrent-info-hash   <btih>
                   :archive/swarm-seeders       [<seedbox-ids>]
                   :archive/arweave-tx-id       <tx-hash>          ; if used
                   :archive/encryption-recipient <pubkey>
                   :archive/key-vault-ref       <secret manager id>
                   :archive/de-id-policy        "v1.2 / GDPR-recital-26"
                   :archive/sealed-at           #inst "..."
```

A daily **swarm health check** monitors torrent seeder counts; below threshold (e.g. 5) the system re-pushes to standby seedboxes or alerts. This is the operational reality of torrent-based durability.

## Cost / value comparison (100 TB reference)

| Layer | Cost | Durability | Independence |
|---|---|---|---|
| Storj | ~$400/mo storage + ~$700/TB retrieved | 11 nines (claimed) | Single company, distributed substrate |
| Self-hosted seedbox network (10 × 10 TB VPS) | ~$500–1,500/mo or hardware + colo | Depends on operator discipline | Fully self-controlled |
| Arweave | ~$5–10/GB one-time | "Permanent" (200-year endowment model) | Decentralized, blockchain-backed |
| Filecoin + pinning service | ~$3–6/TB/mo | High via storage deals | Decentralized, on-chain proofs |

## What's required to build this

Most additions are small:

- New `:abc/tier` value: `:public-archive`.
- New `:archive/PublicArchive` entity type with the fields above.
- New CLI: `abc data archive --tier public-archive` (extends Phase 5.5's `archive` command).
- Encryption + de-ID pipeline composed from existing tools (`bcftools`, `Crypt4GH`, `age`).
- Storj is just another rclone/s5cmd remote — transfer routing already works.
- **The only genuinely new infrastructure** is the torrent layer: a small Nomad service that creates torrents, registers them with a private tracker, runs swarm health checks.

Estimated **Phase 12 (~6d)** if pursued.

## Pushback considered (and why this is parked)

The minimum-viable answer that delivers ~90% of the value at ~10% of the complexity:

> **Schedule restic backups to Storj.** Storj is restic-compatible out of the box. The Phase 5.5 pipeline already covers external durability without any torrent / Arweave / encryption-pipeline complexity.

The elaborate three-tier design is only worthwhile when there is a specific use case (community sharing, indefinite permanence, jurisdictional independence) that the simpler answer cannot meet.

**Decision:** parked. Revisit after Phase 5.5 has run in production for 6+ months and a concrete archival use case has emerged that restic-to-Storj cannot satisfy.

## Tools and standards referenced

| Need | Tool / standard |
|---|---|
| Decentralized object storage (paid) | Storj DCS, Sia, Filecoin (with pinning service) |
| Permanent storage (paid once) | Arweave |
| Torrent creation / seeding | mktorrent, transmission, rTorrent, libtorrent |
| Private tracker | opentracker, xbtit |
| Compression (general) | zstd (`--long=27 -19`), xz |
| Compression (genomics) | genozip, ORA, CRAM |
| Encryption (modern general) | age |
| Encryption (genomics standard) | Crypt4GH (GA4GH) |
| De-identification | bcftools, samtools, GATK, nf-core/deidentify, pyDeid |
| Re-identification risk literature | Homer 2008 (PLoS Genetics); Gymrek 2013 (Science) |

---

# Appendix B — Tool Landscape Survey

Comprehensive evaluation of tools across every layer of the data platform. "Chosen" rows reflect decisions made during the brainstorm; "alternative" rows document what was considered and why it was not selected.

## B.1 File Enumeration + Dedup Grouping

| Tool | Language | Threads | Hash | JSON | Verdict |
|---|---|---|---|---|---|
| **fclones** | Rust | Yes | BLAKE3 (native) | Yes (--json) | **Chosen for bootstrap mode.** BLAKE3 pre-computed for free; structured JSON output; handles hardlinks and reflinks. |
| fd | Rust | Yes | — | No | **Chosen for directory walk** (Pass A enumeration). ~5× faster than GNU find on large trees. Used alongside fclones. |
| rmlint | C | Yes | BLAKE2 | Yes | Strong alternative. Slightly more complex output schema. Less synergy with Pass C (BLAKE3 vs BLAKE2). |
| fdupes | C | Limited | MD5+byte | No | Not chosen. Single-threaded walk, text output requiring fragile parsing, no BLAKE3. |
| GNU find | C | No | — | No | **Fallback** when fd unavailable (POSIX-only environments). Slower but universally available. |

## B.2 Hashing

| Tool | Algorithms | Multi-file | Parallel | Verdict |
|---|---|---|---|---|
| **rhash** | MD5 + SHA-256 (+ many others) | Yes | Via xargs | **Chosen for Pass B.** Single read pass yields MD5 + SHA-256 simultaneously via `--md5 --sha256`. BSD output is trivially parseable. |
| **b3sum** | BLAKE3 | Yes | Multi-threaded per file | **Chosen for Pass C.** Official BLAKE3 reference CLI. `--num-threads 0` uses all cores. Selective use on files >1 GiB or tagged for archival. |
| sha256sum / md5sum | Single | Yes | Via xargs | Rejected. Two separate passes = 2× I/O. |
| hashdeep | MD5+SHA-256 | Yes | No | Kept as audit-mode parser in `scan.go`. Useful for importing existing audit trails. |
| openssl dgst | Multiple | No | No | Ad-hoc use only. Too slow for corpus-scale work. |

## B.3 Data Transfer

| Tool | Protocol | S3-native | Parallel | Verdict |
|---|---|---|---|---|
| **s5cmd** | S3 (native) | Yes | Yes (--numworkers) | **Chosen for all S3↔S3 and S3↔local hops.** Native S3 API: server-side copy, ETag verification, multipart. Much faster than rclone for S3. |
| **rclone** | 70+ backends | Via plugin | Yes | **Chosen for non-S3 hops** (node↔node, SFTP, GCS, HTTP). The escape hatch for operators who need direct remote:path access. |
| s3cmd | S3 | Yes | Limited | Older; superseded by s5cmd for performance. |
| aws CLI | S3 | Yes | Limited | Heavier runtime (Python). s5cmd is a drop-in superior for scripting. |
| Globus | GridFTP/HTTPS | No | Yes | Evaluated in Appendix C (Custom Integrations). Useful for cross-institution HPC but adds external dependency and auth complexity. |

## B.4 Index + Search Backend

| System | Model | Full-text | Bitemporal | Consistency | Verdict |
|---|---|---|---|---|---|
| **XTDB + Lucene** | Bitemporal Datalog | Yes (xtdb-lucene) | Native | Strong (inline tx) | **Chosen.** Single service. Lucene updates are part of XTDB transactions — no sync gap. Bitemporal model gives full lineage history for free. |
| Meilisearch | Document store | Yes (fuzzy) | No | Eventual | **Rejected.** Second service to sync with XTDB. Eventual consistency window. Fuzzy search not needed for file-index workloads. |
| Elasticsearch | Inverted index | Yes | No | Eventual (with external journal) | Operationally heavy. JVM. Requires separate sync from XTDB. |
| PostgreSQL FTS | Relational + tsvector | Limited | No | Strong | Possible for simpler use cases; Datalog is more expressive for graph lineage queries. |
| Solr | Inverted index | Yes | No | Eventual | Same objections as Elasticsearch. |

## B.5 Compression

| Tool | Best for | Genomics | Decompression speed | Verdict |
|---|---|---|---|---|
| **zstd** (`-19 --long=27`) | General large files | Good | Excellent (parallel) | **Chosen** for general archival compression before Storj/Arweave. `--long=27` uses 128MB dictionary window — critical for repetitive genomic data. |
| **genozip** | FASTQ, BAM, VCF | Native | Good | **Chosen** for genomics-specific compression. 3–6× better than gzip on BAM; lossless; preserves headers. |
| gzip | Universal compatibility | Poor | Good | Use only when receiver requires gzip. Never for new archival. |
| ORA (NVIDIA) | FASTQ only | Very good | GPU-only | Evaluated; GPU dependency makes it impractical for batch archival jobs. |
| CRAM | BAM only | Excellent | OK | Considered; requires reference genome for decompression — adds complexity for cold archive retrieval. |
| xz | Highest ratio | Poor | Very slow | Not appropriate for large files (single-threaded compression). |

## B.6 Encryption

| Tool | Standard | Key mgmt | GA4GH compat | Verdict |
|---|---|---|---|---|
| **age** | Modern (X25519) | Simple (public key) | No | **Chosen** for general-purpose encryption before archival to Storj/Arweave. Simple, auditable. |
| **Crypt4GH** | GA4GH standard | Yes (re-encryption) | Yes | **Chosen** for genomics data requiring GA4GH interoperability (DRS, WES). Supports partial decryption (sub-file ranges). |
| GPG | Old standard | Complex (keyring) | No | Not chosen. Key management is operationally complex at scale. |
| openssl enc | Ad-hoc | Manual | No | Not chosen for archival. No metadata, no standard key format. |

## B.7 De-identification

| Tool | Data type | Approach | Verdict |
|---|---|---|---|
| **bcftools annotate** | VCF | Strip INFO/FORMAT fields, remove sample IDs | **First choice** for VCF de-ID. Native VCF-aware. |
| samtools view | BAM/CRAM | Remove read group tags, strip tags | **First choice** for BAM de-ID (header stripping). |
| GATK SelectVariants | VCF | Subsetting + de-ID | Full pipeline de-ID when bcftools is insufficient. |
| nf-core/deidentify | FASTQ/BAM/VCF | Full pipeline | Entire pipeline approach — run as a Nomad job before archival. |
| pyDeid | Clinical text | NLP-based PHI removal | For clinical notes embedded in metadata; not primary genomic data. |

## B.8 Object Storage

| System | Type | Protocol | Erasure coding | Verdict |
|---|---|---|---|---|
| **MinIO** | Self-hosted S3 | S3 | Erasure (configurable) | **Hot tier.** Already deployed. Sub-10ms local access. |
| **RustFS** | Self-hosted S3 | S3 | Erasure | **Warm tier.** Already deployed. Lower I/O footprint than MinIO. |
| **Garage** | Self-hosted S3 | S3 | Reed-Solomon | **Cold tier.** Already deployed. Designed for multi-DC archival use cases. |
| **Private Storj** | Self-hosted erasure | S3 (gateway-mt) | 4-of-7 for 12 nodes | **Phase 5.6 resilience tier.** Own satellite + storage nodes. 3-node simultaneous loss tolerance. Automatic repair. |
| Ceph (RadosGW) | Self-hosted multi-protocol | S3, Swift, POSIX | CRUSH | Evaluated in Appendix C. Strong candidate if POSIX filesystem semantics needed at scale. |
| Public Storj DCS | Decentralized | S3 / uplink | 29-of-80 | **Phase 5.6.c (optional).** Off-prem disaster recovery only. Requires explicit `--from-public-dr` consent at read time. |

## B.9 Backup / Snapshot

| Tool | Dedup | Encrypted | Targets | Verdict |
|---|---|---|---|---|
| **restic** | Content-addressed | Yes (AES-CTR) | S3, SFTP, REST, Storj | **Chosen.** Already deployed against Garage. Content-addressed chunks → natural dedup. Storj-compatible out of the box. `restic check` used for integrity verification. |
| Borg | Content-addressed | Yes | SSH only | Strong; SSH-only target limits S3 integration. |
| Duplicati | Block-based | Yes | S3, GCS, Dropbox | Web UI focused; less suited for CLI-driven cluster workflows. |
| Kopia | Content-addressed | Yes | S3, GCS, Storj | Strong alternative to restic. Evaluated; restic already integrated in stack. |

## B.10 Provenance / Lineage Standards

| Standard | Scope | Maturity | Verdict |
|---|---|---|---|
| **W3C PROV** | Universal provenance | High | **Chosen** as the data model. Activity/Entity/Agent. Used by Galaxy, GA4GH WES, RO-Crate. |
| **OpenLineage** | Data pipeline lineage | High | **Chosen** for the bridge to catalog UIs (Marquez, OpenMetadata, DataHub). Phase 8.5. |
| **GA4GH WES + DRS** | Genomics workflow + data | High | **Chosen** for genomics interoperability. DRS Phase 8.7; WES-compatible via DRS URIs. |
| **RO-Crate** | Research object packaging | High | **Chosen** for shareable data exports. Phase 8.7. |
| **SBOM (CycloneDX)** | Software bill of materials | High | **Chosen** for pipeline audit trail (container + conda + pip). Phase 8.7. |
| **Nextflow lineage (lid://)** | Nextflow-native | Medium | **Integration target.** SHA-256-based → maps directly to our content entities. Phase 9. |

## B.11 External open-source components (curated additions)

Beyond the per-category picks above, the design adopts a curated set of external open-source projects that either deliver capability we'd otherwise build ourselves, improve operational quality of the distributed system, or align us with mature standards in the genomics community. Each is slotted into a specific phase with a defined contract; none are speculative additions.

### Tier 1 — Capability we'd otherwise build

| Project | What it gives us | Slots into | LOC delta |
|---|---|---|---|
| **htsget** (GA4GH) | Region-streaming protocol for BAM/VCF/CRAM. `GET /reads/x.bam?referenceName=chr17&start=...` returns just the bytes covering the region. | Phase 8.7 — new `abc-htsget-server` Nomad service alongside `abc-drs-server`; resolves via XTDB; ACL via Jurist read-policy bundle | +600 |
| **GA4GH refget** | Content-addressed reference sequences (SHA-512 of normalised content). Solves the "every lab caches GRCh38 differently" problem. | Phase 8.7 — `abc-refget-server` in front of the reference data tier; lineage edges to references carry refget IDs | +400 |
| **Mountpoint for Amazon S3** | AWS Rust FUSE driver; production-grade; designed for read-heavy workloads | Phase 11 (FUSE) — primary FUSE driver behind `/abc/`; rclone mount as backend-specific fallback | (replaces placeholder) |
| **Pixi** (Prefix.dev) | Conda-compatible Rust environment manager with deterministic lockfiles. `pixi.lock` is byte-stable; ideal input to activity fingerprinting. | Phase 8.7 — `pixi.lock` SHA-256 becomes a component of `:prov/activity-fingerprint`; SBOM emitter prefers Pixi lockfile | (no service; spec change) |
| **MultiQC** | Genomics QC aggregator for 100+ tools (samtools, FastQC, Picard, GATK, etc.). | Phase 5.7 — runs in a containerised step inside Pass-C of the scanner; output JSON cached to `:content/format-summary` | +50 |
| **Marimo** | Reactive Python notebooks; deterministic state; diffable `.py` files. | Cross-cutting — recommended notebook environment for new analysis work; `abc data show` outputs link to a Marimo URL when applicable; Jupyter remains supported | (no integration code; recommendation only) |

### Tier 2 — Operational quality

| Project | What it gives us | Slots into | LOC delta |
|---|---|---|---|
| **OpenTelemetry** | Vendor-neutral cross-service tracing, metrics, logs. | Phase 2 — collector deployed as Nomad service; baseline emission contract for every new service from this point forward | +200 instrumentation across phases |
| **VictoriaMetrics + VictoriaLogs** | Lighter Prometheus + Loki replacement at scale. | Opportunistic operational refresh when current Prometheus/Loki struggle with metadata-volume cardinality. Not a feature phase. | (operational, not feature) |
| **LiteLLM** | Unified OpenAI-style API in front of any LLM provider (Anthropic, OpenAI, Bedrock, Ollama, vLLM). | Phase 10 — `abc-chat-svc` talks only to LiteLLM; provider switching is a config change | net +50 |

### Tier 3 — Genomics community alignment

| Project | What it gives us | Slots into | LOC delta |
|---|---|---|---|
| **REMS** (CSC.fi) | Data Access Committee workflow tool. Used by Beacon network, FEGA nodes. | Phase 5.5 region — only when DAC-reviewed datasets enter the cluster; `:compliance/rems-entitlement-id` extends the compliance bundle; Jurist DTA rule reads REMS entitlements | (conditional, +0 if no DAC workflow) |
| **OLS** (EBI Ontology Lookup Service) | Hosted REST API for 250+ biological ontologies (EFO, NCBI Taxonomy, OBO Foundry). | Phase 7 — `abc data vocab sync --from-ols` populates `:vocab/*` entities; periodic refresh as a Nomad periodic job | +150 |
| **IGV.js** (Broad Institute) | Browser-based genome viewer; reads from htsget URLs. | Phase 8.5 — embedded in Khan operator console "Data Index" Filament resource; uses token-authenticated htsget URL | +200 in Khan |
| **CWL exporter** (Nextflow built-in) | Workflow portability across systems (Cromwell, Toil, Arvados). | Phase 9 — `abc data lineage export --as-cwl <activity>` runs `nextflow inspect -format cwl`; thin CLI wrapper | +50 |
| **BioCompute Objects** (IEEE 2791-2020) | FDA-recognised genomic computation provenance standard. | Phase 8.7 — `abc data export-bco <activity-id>` alongside RO-Crate exporter; only relevant in regulated contexts | +150 |

### Tier 4 — Speculative (mention but don't integrate now)

- **DuckDB** — embedded analytics over parquet metadata exports; useful for power users, not core. Mention in docs.
- **JuiceFS** — POSIX filesystem on object storage; revisit when POSIX-tier becomes urgent (alternative to CephFS in Appendix C.1).
- **Spack** — HPC build system; only relevant if cluster runs non-genomics HPC workloads.
- **Apache Avro / JSON Schema Registry** — defer until first painful event-schema evolution on NATS.

### Explicitly rejected (saving future readers the lookup)

| Project | Why no |
|---|---|
| LangChain / LlamaIndex | Tool-calling architecture in Phase 10 doesn't need orchestration framework abstractions |
| Apache Iceberg | Designed for analytics tables; metadata corpus is too small to benefit |
| Argo Workflows | Kubernetes-native; conflicts with Nomad direction |
| Pachyderm | Couples data versioning to its own pipeline runner; would replace Nextflow |
| OpenBIS | Monolithic; same gravity well as iRODS/Egeria |
| Quilt | Less mature than DVC; no clear advantage |
| Dagger | CI/CD focus; not a data-platform fit |
| Wandb | Proprietary; conflicts with self-hosted vision |
| Solid (Berners-Lee) | Research-stage; no production users in genomics |
| Apache Atlas | Already rejected with the broader Egeria pattern (C.9) |
| dbt + lakehouse stacks | SQL-first; metadata model is graph-first via Datalog |

### Standards worth knowing about (no integration yet)

| Standard | What | Trigger to revisit |
|---|---|---|
| GA4GH WES | Workflow Execution Service standard | If we expose Nextflow runners to external GA4GH clients |
| GA4GH Beacon v2 | Genomic variant query federation | Cross-cluster federation requirement |
| MIxS / GSC | Minimum metadata standards for sequencing | Required-fields validation in the vocabulary |
| OpenLineage facets | Standard custom-facet schemas (DataQuality, Schema, etc.) | Already emitting OL events; should adopt facets for richer info |
| BioImage Model Zoo / OME-NGFF | Bioimaging standards | If imaging data ever enters the cluster |
| MIRO / RO-Crate ML profiles | Provenance for ML models | If predictive model training becomes a workflow target |

### Slot-in summary

| Phase | Existing scope | Open-source additions |
|---|---|---|
| 2 | XTDB + Lucene + NATS | + OpenTelemetry collector |
| 5.5 | restic + Garage | + REMS (when DAC workflows are needed) |
| 5.7 | verify, heal, quarantine, reports | + MultiQC for `:content/format-summary` |
| 7 | annotation + vocabulary | + OLS sync for ontology backing |
| 8.5 | OpenLineage bridge | + IGV.js in Khan operator console |
| 8.7 | DRS, RO-Crate, SBOM, .abc pin | + htsget + refget + Pixi env hashing + BioCompute Objects |
| 9 | Nextflow lineage | + CWL exporter |
| 10 | LLM tool-calling chat | + LiteLLM provider abstraction |
| 11 | FUSE / discovery / cache | + Mountpoint for S3 (FUSE driver) |
| cross-cutting | — | + Marimo (recommended notebook environment) |

### Prioritisation order (when implementing)

If the goal is "good UX while honest to the vision," the order of payoff is:

1. **OpenTelemetry now** — debugging the distributed system without it will be misery.
2. **htsget + refget in Phase 8.7** — the largest UX gain in the genomics workflow.
3. **MultiQC in Phase 5.7** — `abc data file <bam>` becomes immediately useful instead of placeholder.
4. **Pixi for env fingerprinting in Phase 8.7** — makes the cache reuse story actually work.
5. **OLS sync in Phase 7** — operators love bootstrapped vocabularies.
6. **LiteLLM in Phase 10** — provider abstraction pays off as soon as you want to support local Llama.
7. **Mountpoint in Phase 11** — when FUSE becomes a real thing, not before.

Everything else is opportunistic.

---

# Appendix C — Custom Integration Evaluations

Evaluations of custom integrations that don't fit neatly into the standard tool categories: POSIX filesystem layers, messaging systems, HPC-native data management, and grid transfer services.

## C.1 CephFS — POSIX Filesystem Tier

**What it is:** Ceph is a self-hosted distributed storage system. CephFS is the POSIX filesystem layer on top of RADOS (the object store). RadosGW provides an S3-compatible API to the same RADOS cluster.

**Why it came up:** Some genomics tools (GATK, certain Nextflow processes) assume POSIX filesystem semantics — `open()`, `seek()`, `mmap()`. MinIO/RustFS expose only S3. Running these tools on cluster data today requires either (a) a node-local scratch disk or (b) mounting via s3fs/goofys (which are known to be unreliable for high-throughput bioinformatics).

**Integration story:**
- RADOS provides a unified pool; RadosGW exposes the same data as S3; CephFS exposes the same data as POSIX.
- A `abc://` path could resolve to a CephFS mount path on compute nodes that have it, or an S3 path via RadosGW — same content entity in XTDB, different access method.
- `selectTool()` gains a new case: if src and dst both resolve to the same Ceph cluster, use a direct filesystem copy (`cp -a`) or rclone with the local backend — no network hop.

**Operational concerns (significant):**
- CephFS requires running the full Ceph stack (monitors, managers, OSDs, MDSes). The metadata server (MDS) is the most fragile component — MDS failure causes the entire POSIX filesystem to stall.
- CephFS performance degrades badly with many small files (metadata server saturation). Fine for BAM/VCF (large files); problematic for Nextflow work directories with millions of tiny files.
- Requires dedicated network tuning (jumbo frames, RDMA for high performance).

**Recommendation:** Slot CephFS as a Phase 5.8 "POSIX tier" — deploy RadosGW as an additional S3 backend (trivial, drops into existing tool routing), then evaluate CephFS only if POSIX tools become a frequent bottleneck. Don't deploy CephFS before RadosGW is proven stable.

**Integration with the abc:// namespace:**
- `abc://posix/run42/sample.bam` resolves to CephFS mount path `/ceph/abc/posix/run42/sample.bam` on nodes that have the CephFS mount.
- Same content entity (same SHA-256) can be simultaneously exposed as S3 via RadosGW and POSIX via CephFS — the index records both locations.

---

## C.2 NATS — Messaging / Event Bus

**What it is:** NATS is a high-performance cloud-native messaging system (pub/sub, queues, JetStream for durable streams). Very lightweight Go binary; single-binary server.

**Why it came up:** The Phase 5 bucket event ingester needs a durable queue between MinIO/RustFS bucket notifications and the XTDB ingester. MinIO can publish to NATS natively.

**Integration story:**
- MinIO → NATS JetStream topic (`abc.bucket.events`) → `bucket-event-ingester` service → XTDB
- JetStream provides at-least-once delivery with acknowledgment, consumer groups (so multiple ingester replicas can process events without duplication), and replay from a specific offset (for ingester restart recovery).
- Same NATS instance could carry Nextflow task events (Phase 9), pipeline status updates (for the LLM chat tool), and integrity check alerts (Phase 5.7).

**Alternative considered:** Kafka. Rejected — Kafka is operationally much heavier (JVM, ZooKeeper or KRaft, complex topic partitioning). NATS JetStream provides equivalent durability at a fraction of the operational footprint. MinIO also has native NATS integration; it doesn't have native Kafka integration (only Kafka as a target via webhook or third-party library).

**Recommendation:** NATS JetStream is the natural event bus for this stack. Deploy as a Nomad service alongside XTDB in Phase 2 (infra platform phase). Replace the simple HTTP webhook sink with a NATS JetStream consumer for better reliability and replay.

---

## C.3 iRODS — Integrated Rule-Oriented Data System

**What it is:** iRODS is a mature data management middleware widely deployed in genomics and biobanking (UK Biobank, EMBL-EBI, most European genome centres). It provides: a virtual filesystem over heterogeneous backends, a rules engine for lifecycle policies, metadata tagging, access control, and federation across institutions.

**Why it came up:** Comparative analysis — iRODS is the closest existing solution to what we're building.

**Gaps iRODS doesn't fill (why we're building instead of adopting):**
- iRODS uses its own database backend (PostgreSQL) and its own agent (iCAT metadata catalog). It doesn't integrate with XTDB's bitemporal model or Lucene-indexed Datalog queries.
- The iRODS rules engine uses its own DSL (iRODS Rule Language) which is opaque and hard to test.
- iRODS doesn't natively integrate with Nomad job scheduling — you'd need custom iRODS microservices to submit Nomad jobs.
- iRODS federation is designed for cross-institution data sharing, not for the cluster-internal lifecycle management that's the primary use case here.
- The iRODS project has historically struggled with slow releases and a complex installation story.

**What we borrow from iRODS:**
- The rules engine concept (§Tier-2 enrichment #1): YAML-based lifecycle policies with match conditions and actions. Our implementation is simpler but inspired by iRODS's approach.
- The vocabulary/controlled-term registry (`:vocab/` namespace) — borrowed from iRODS's metadata templates.
- The concept of a managed namespace (abc://) as opposed to raw backend paths.

**Recommendation:** No iRODS integration. The design borrows its best ideas without its operational complexity.

---

## C.4 Globus — Cross-Institution Data Transfer

**What it is:** Globus is a research data management service (operated by University of Chicago) providing high-speed GridFTP transfers between registered endpoints, access control, and a web portal. Widely used in US national labs and large genomics consortia (NIH, ENCODE, TCGA).

**Why it came up:** For cross-institution data sharing — receiving data from external collaborators or depositing to national repositories.

**Integration story:**
- A Globus endpoint registered on the cluster would allow external collaborators to push/pull data via the Globus web portal, without our cluster needing to expose raw S3 or SSH.
- `abc data import --from-globus <endpoint>:<path>` could submit a Nomad job that runs the Globus CLI (`globus transfer`) and then indexes the result in XTDB.
- Globus doesn't understand `abc://` paths — the integration would be one-directional (Globus → cluster, or cluster → Globus), with the XTDB write-through handling indexing post-transfer.

**Operational concerns:**
- Globus requires registering an endpoint with Globus Auth (external SaaS dependency).
- Globus GridFTP is TCP-optimized for wide-area transfers; it's overkill for within-cluster transfers.
- Air-gapped clusters cannot use Globus without the on-prem Globus Connect Server.

**Recommendation:** Globus is the right tool for cross-institution transfers to/from national repositories. Implement as a thin `abc data import --from-globus` / `abc data export --to-globus` pair that uses the Globus CLI as a subprocess. The indexing is handled by the standard write-through mechanism. Phase 13 (post-Phase 10 chat).

---

## C.5 Patroni — HA PostgreSQL for Storj Satellite

**What it is:** Patroni is a Python-based HA solution for PostgreSQL — it manages leader election (via etcd/ZooKeeper/Consul), automatic failover, and replica promotion.

**Why it matters here:** The private Storj satellite (Phase 5.6.a) stores its metainfo in PostgreSQL. Single-instance Postgres is a single point of failure that defeats the entire durability argument for running Storj. If Postgres goes down, the satellite cannot coordinate storage nodes, cannot verify audit proofs, and cannot repair missing pieces.

**Integration story:**
- Patroni cluster: 3-node PostgreSQL with Patroni + etcd (etcd can be the same cluster used by Nomad/Consul, or a dedicated one).
- Patroni manages automatic failover in ~30 seconds on primary failure.
- Storj satellite configured to connect to the Patroni virtual IP / HAProxy frontend — transparent to the satellite.

**Alternative considered:** CockroachDB (distributed SQL, no single primary). Stronger consistency model; more operationally complex to run than Patroni+Postgres; the Storj satellite is designed for standard PostgreSQL. Recommend CockroachDB only if the team has existing CockroachDB expertise.

**Recommendation:** Patroni + 3-node PostgreSQL is the required HA setup for the Storj satellite in Phase 5.6.a. This is a non-negotiable hard constraint recorded in the Phase 5.6 section of the main plan.

---

## C.6 Tape / HSM — Cold Long-Term Archive

**What it is:** Tape storage (LTO-9: ~18 TB native/45 TB compressed per cartridge, ~$30/cartridge) and Hierarchical Storage Management (HSM) systems (e.g., Amanda, Bacula, IBM Spectrum Archive) that transparently migrate cold data from disk to tape.

**Why it came up:** For datasets that need to be retained for 10+ years but are accessed less than once a year, tape is 10–50× cheaper per TB than any cloud or disk-based cold tier.

**The genomic data case:**
- Raw sequencing data (FASTQ) is read once (at generation), processed, and then stored for regulatory/IRB compliance (often 7–10 years).
- Processed data (BAM, VCF) is used more frequently but the raw FASTQ is effectively write-once-read-never.

**Integration story:**
- Amanda or Bacula as the HSM layer, writing to an LTO tape library.
- `abc://` paths with `:location/tier :tape` would indicate the file is on tape — `abc data restore` would trigger a tape recall job.
- The XTDB index would record tape locations as first-class location entities, with `:location/backend "tape"` and `:location/tape-volume <cartridge-id>`.
- Recall latency: 30–90 seconds to load a cartridge. Not suitable for interactive workflows — fine for compliance/archival queries.

**Recommendation:** Tape is not in scope for the initial build — the Garage + private Storj tiers provide sufficient cold-tier capacity and better recall latency. However, the abc:// location model and the source-selection ladder in `abc data restore` are designed to accommodate tape as a future tier without schema changes. When a tape tier is needed (likely at > 500 TB corpus), slot it as Phase 14.

---

## C.7 Nextflow Lineage Subsystem (native, 24.10+)

**What it is:** Nextflow 24.10 introduced a first-class lineage subsystem. Every task execution creates a `lid://<sha256>` content-addressed lineage event recording: inputs (with hashes), outputs (with hashes), container image, command, parameters, runtime. The `nextflow lineage` CLI can browse and export these events.

**Why this is critically important for our integration:**
- Nextflow lineage IDs (`lid://`) are SHA-256 based — the same hash we store in `:content/sha256`. This means joining our index to Nextflow lineage requires only a hash lookup — no name reconciliation, no path translation.
- Nextflow already computes output hashes; we don't need to re-hash files that Nextflow produced. Pass B of the scanner can skip Nextflow-produced files that already have their hash in XTDB from the lineage ingester.
- Every nf-core pipeline run (which is most pipeline runs in this cluster) automatically generates lineage events without any user action.

**Three integration paths (see Phase 9 for details):**

| Path | Mechanism | Latency | Recommended? |
|---|---|---|---|
| **PROV import bridge** | `nextflow lineage render --format prov` → `abc data lineage import` | Batch (manual) | Yes, for backfill |
| **Sidecar ingester** | Nomad service watches `.nextflow/lineage/` or `bucket://_nf-lineage/` | Near-realtime | **Yes, default** |
| **nf-abc-lineage plugin** | Nextflow operator API plugin, pushes events during execution | Real-time | Yes, for `--watch` |

**Recommendation:** Deploy sidecar ingester (Path B) as the default — it works for every Nextflow run regardless of plugin presence. Add the plugin (Path C) for real-time visibility. Use PROV import (Path A) to backfill historical runs.

---

## C.8 Sequencing Instrument Integration — Event Sources and Ingest Patterns

**The problem:** Sequencing instruments (Illumina, Oxford Nanopore, PacBio) write data to a local filesystem or NAS share — not to an S3 bucket. The cluster's bucket-event-ingester (Phase 5) only fires on MinIO/RustFS events. There is a gap between "instrument produces data" and "data appears in the cluster's object storage."

**Three ingest patterns (ranked by automation level):**

### Pattern 1: abc-instrument-watcher (recommended, Phase 0.5)

A Go service using `fsnotify` (Linux `inotify`) runs as a Nomad system job on nodes with NAS mounts. It watches for platform-specific completion sentinels, parses instrument metadata (run parameters, sample sheet), and automatically submits a transfer job to Khan.

- **Pros:** Fully automatic; captures instrument metadata for provenance; handles all three platforms; integrated with the existing Khan/Jurist/Nomad flow.
- **Cons:** Requires the NAS to be mounted on a Nomad node; requires a service token for Khan.

### Pattern 2: LIMS webhook (complement to Pattern 1)

If the facility has a LIMS (e.g., LabArchives, Benchling, openBIS, custom), the LIMS can POST a webhook to Khan (`/api/v1/data/ingest/notify`) when a run is marked as QC-passed or ready for analysis. The webhook carries biological metadata that the instrument itself doesn't know (IRB number, study ID, consent version, sequencing protocol) — enriching what Pattern 1 captures.

- **Pros:** Richer biological provenance; integrates institutional record-keeping; de-couples instrument hardware from the cluster.
- **Cons:** Requires LIMS integration effort; not all facilities have a LIMS.

**Recommended:** run both — Pattern 1 for immediate automated transfer, Pattern 2 for richer annotation when LIMS confirms the run is study-compliant.

### Pattern 3: Manual push (fallback)

For instruments that write to a desktop workstation or instrument PC (not a NAS), a lab member runs:
```
abc data copy /path/to/run abc://inbox/sequencing/<run-id>/
```
This is today's workflow. The Phase 3 write-through captures the transfer, but instrument metadata (run parameters, sample sheet) is not automatically parsed — it must be annotated separately via `abc data annotate --from-tsv samples.tsv`.

**Instrument platform metadata files:**

| Platform | Files to parse | Key fields |
|---|---|---|
| **Illumina** (NovaSeq/NextSeq/MiSeq) | `RunInfo.xml`, `SampleSheet.csv` | flowcell_id, instrument_id, cycles, read_mode, sample→barcode mapping |
| **Oxford Nanopore** (PromethION/GridION) | `final_summary_*.txt`, `sequencing_summary_*.txt` | device_id, run_id, protocol, basecall_model, reads_passed, N50 |
| **PacBio** (Revio/Sequel IIe) | `.metadata.xml`, `*.transferdone` | instrument_serial, run_name, library_name, movie_length, polymerase_reads |
| **Generic / custom** | configurable sentinel filename | user-defined via `abc-instrument-watcher.yaml` |

**How provenance propagates after ingest:**

```
act:sequencing:RUN42          (instrument watcher writes this)
  ↓ prov/generated
abc://inbox/sequencing/RUN42/  (files land in MinIO)
  ↓ bucket-event-ingester indexes them
  ↓ scanner fills SHA-256 hashes (Pass B)
  ↓ Nextflow nf-core/bcl2fastq consumes the run dir
act:nf:demux-a3f9…            (nf-lineage-ingester writes this)
  ↓ prov/wasAssociatedWith: act:sequencing:RUN42  (linked by run_id matching)
  ↓ prov/generated
abc://raw/RUN42/S00123_R1.fastq.gz
  ↓ Nextflow nf-core/sarek alignment
abc://align/RUN42/S00123.bam
```

The link between the sequencing activity and the demultiplexing activity is made via `:pipeline/params {:run-id "RUN42"}` — both activities reference the same run ID, and the lineage ingester can join them when indexing the demux outputs.

---

## C.9 Apache Egeria — Metadata Federation Platform

**What it is:** Apache Egeria (ODPi/FINOS) is an open metadata and governance platform whose core value proposition is **peer-to-peer metadata federation** across separate institutional repositories. Institutions each run an OMAG (Open Metadata and Governance) server; servers join a *cohort* via Kafka event topics and can query each other's metadata as if it were local. Everything else in Egeria — the type system, connectors, governance action framework — exists in service of that federation goal.

**Seven OMAG server types:**

| Server type | Role |
|---|---|
| Metadata Access Store | Native open metadata repository with local persistence |
| Metadata Access Point | Federated-query-only; no local repository |
| Repository Proxy | Adapter for third-party metadata systems via OMRS connector |
| Integration Daemon | Extracts and synchronises metadata from external systems |
| Engine Host | Executes governance action engines and data management services |
| View Server | Serves UIs for users and external tools |
| Conformance Test Server | Platform capability validation |

**Runtime:** Java 17 (OpenJDK HotSpot), Maven 3.6+, ~1 GB+ heap per process, Kafka for cohort event synchronisation. Moderate-to-high configuration complexity (YAML per server type).

**Type system:** 600+ generic enterprise types across 7 areas (Assets, Collaboration, Data Architecture, Software Development, Lineage, etc.). Lineage area uses Process/DataFlow/ControlFlow types. No native W3C PROV alignment; OpenLineage serves as interchange format. No domain types for genomics (assay, organism, sample, flow cell, etc.).

**Connectors available:** Apache Atlas (bidirectional), Apache Kafka, Apache Atlas, file connectors. **No native connectors for:** MinIO/S3, NATS JetStream, Nextflow lineage, Oxford Nanopore, Illumina instruments, rhash/fclones scan output, or any tool in this project's stack. Custom connectors would require Java development.

---

**Capability-by-capability comparison with this project's design:**

| Egeria capability | This project | Winner |
|---|---|---|
| Metadata repository | XTDB + Lucene | **XTDB** — bitemporal queries, Datalog expressiveness, single service, already deployed. Egeria has no bitemporal model. |
| Lineage types (Process, DataFlow) | W3C PROV (Activity, Entity, Agent) | **W3C PROV** — richer derivation model; Nextflow-native via `lid://` SHA-256 join; set-valued `:prov/wasAssociatedWith` handles multi-agent correctly |
| Catalog integration | Phase 8.5 OpenLineage bridge → Marquez/OpenMetadata/DataHub | **OpenLineage bridge** — connects to modern catalogs directly. Egeria's main catalog connector targets Apache Atlas (legacy). |
| External system sync (Integration Daemon) | bucket-event-ingester, nf-lineage-ingester, abc-instrument-watcher | **Purpose-built Go services** — tuned to MinIO events, Nextflow lineage format, and sequencing instrument sentinels. Egeria has no connectors for any of these. |
| Governance framework | Jurist (Clojure, OPA, DTA/consent/clearance/classification rules) | **Jurist** — already authorizes every Nomad job submission; Egeria's governance action framework would be an unused parallel. |
| Metadata search | XTDB-Lucene | **XTDB-Lucene** — Lucene index updates inline with XTDB transactions (strong consistency); Datalog + text-search in one query. |

**Why the federation model doesn't apply here:**
Egeria's cohort synchronisation (registry + typedef + instance events via Kafka) is designed for the scenario: *"EMBL-EBI, NIH, and Genomics England each operate their own OMAG server and want federated queries across all three."* The abc-cluster is a single unified system with one XTDB instance. Egeria's federation adds 3–5 JVM processes and Kafka cohort configuration complexity with zero benefit when there is only one metadata store.

**Why the type system fights the genomics domain:**
Egeria's 600+ types are generic enterprise types (DataFile, Process, SchemaAttribute, etc.). None natively represent `:content/sha256`, `:bio/assay`, `:location/tier`, `:integrity/status`, or `:compliance/data-class`. Adopting Egeria types would mean either abandoning the domain-specific schema or maintaining two parallel schemas with a mapping layer — both strictly worse than the current design.

---

**Verdict: Not worthwhile for this project.**

Every capability Egeria offers for a single-cluster use case is already covered more cleanly by XTDB + the Phase 8.5 OpenLineage bridge + Jurist. Adding Egeria would increase operational complexity by approximately 4× (multiple JVM processes, Kafka cohort config, Java connector development) for zero new functional capability.

**The one future scenario worth tracking:**
If the cluster eventually needs to join an inter-institutional Open Metadata cohort — e.g., federating metadata with collaborating centres that already run OMAG servers — Egeria's **Repository Proxy** could be placed in front of XTDB without replacing it. The proxy exposes XTDB to Egeria's OMRS protocol so external cohort members can query it. This leaves the internal stack completely unchanged. File under Phase 13 (post-Phase 10 chat), conditional on a concrete federation requirement emerging.

**The analogy to file away:** Egeria is to metadata governance what Kubernetes is to container scheduling — powerful for large, multi-team, multi-cluster environments; severe overkill for a well-designed single-cluster system that already has Nomad and a purpose-built metadata store.

---

*End of Appendix C.*

---

# Appendix D — Document Maintenance

This section captures conventions and structural-integrity checks that future maintainers can run when editing this design doc. The doc has grown to ~3,300 lines with cross-referenced phases, schema sections, and appendices; small edits can silently break references if these checks are skipped.

## Editing conventions

1. **Section structure stays stable.** The numbered sections (§1–§9) and lettered appendices (A–D) are referenced from inside the document and from external places (e.g., the `abc-cluster-cli` codebase comments may reference `§2 schema` or `Appendix B.11`). Renumbering breaks references; insert sub-sections (e.g., new `§9.10`) instead of re-numbering existing ones.

2. **Phase IDs are immutable once shipped.** Once a phase has landed in production (or a partial implementation has been merged), its phase number is permanent. Add new phases as `Phase Nx` (decimal sub-phase, like 5.6.a, 5.7) rather than renumbering.

3. **Schema namespaces (§2 EDN conventions) are normative.** Any new attribute proposed in this doc must respect the namespace inventory and the bundling/cardinality/nullability rules. Don't introduce ad-hoc namespaces in phase sections; add them to §2 first.

4. **Tool choices live in Appendix B; the rationale lives in §How We Got Here.** When picking a new tool, update Appendix B.x with the comparison table, then update the relevant phase to reference the chosen tool. For curated external open-source additions specifically, see **Appendix B.11**.

5. **Rejected alternatives are documented, not deleted.** When a tool/approach is evaluated and rejected, it goes into the "Explicitly rejected" subsection of the relevant appendix (B.11, C.x) so future readers don't redo the analysis.

6. **The revision history (top of document) is appended on every substantial edit.** One row per edit session, ISO date, one-line summary.

## Structural-integrity checks

Run these after any non-trivial edit:

```bash
DOC=analysis/packages/abc-cluster-cli/docs/design/abc-data-platform-design.md

# 1. No accidental "fdupes" reintroduction (we use fclones; the term appears
#    only in Decision-4 comparison, Appendix B.1 rejected row, and this very
#    integrity-check block → expect 5 hits)
grep -c "fdupes" $DOC                        # expect: 5

# 2. All phase sections are referenced from at least one summary table
for phase in 0.5 1 2 3 4 5 5.5 5.6 5.7 6 7 8 8.5 8.7 9 10 11; do
  count=$(grep -c "Phase $phase" $DOC)
  echo "Phase $phase: $count mentions"
done

# 3. Appendix structure intact (expect 4 top-level appendices: A, B, C, D;
#    11 B-subsections; 9 C-subsections)
grep -c "^# Appendix" $DOC                   # expect: 4
grep -c "^## B\." $DOC                       # expect: 11
grep -c "^## C\." $DOC                       # expect: 9

# 4. Phase summary table reconciles with individual phase LOC (manual check;
#    rows should sum to the "Total estimate" line near the end of §7)

# 5. No broken internal references (manual check; search for "§" and confirm
#    each referenced section still exists with that number)

# 6. Markdown is well-formed (linter; this doc is NOT part of the Docusaurus
#    build — the entire docs/design/** tree is excluded in
#    website/docusaurus.config.ts so internal design discussions stay internal)
markdownlint $DOC || true
```

## Visibility

This document is **internal**. The `docs/design/` folder is excluded from the
Docusaurus build via `website/docusaurus.config.ts` (`exclude: ['design/**']`)
so design-discussion docs do not appear on the public CLI documentation site.
The `_category_.json` file in this folder is dormant — it would activate only
if the exclusion were lifted.

If a section of this doc ever needs to become public-facing user
documentation, **extract** the relevant content into a new file under
`docs/reference/` or `docs/tutorials/` (which are included in the build).
Do not lift the design-folder exclusion.

## Files this design references but does NOT modify

This is a design document. Implementation lives elsewhere and may be ahead of, behind, or in disagreement with this doc; reconcile before relying on doc text for behaviour:

- `analysis/packages/abc-cluster-cli/cmd/data/` — current CLI source for data commands
- `analysis/packages/abc-cluster-cli/internal/indexer/` — (planned) typed XTDB query layer
- `analysis/packages/abc-cluster-cli/internal/jurist/` — Jurist client (existing; will extend for read-policy)
- `analysis/packages/abc-khan-svc/app/Http/Controllers/Api/V1/` — (planned) `Data*` controllers for `/api/v1/data/*`
- `analysis/packages/abc-jurist-svc/src/abc/policy/emit/` — (planned) `read_policy.clj` emitter
- `analysis/packages/abc-data-api/` — (planned new package)
- `analysis/packages/abc-instrument-watcher/` — (planned new package)
- `analysis/packages/abc-bucket-ingester/`, `abc-nf-lineage-ingester/`, `abc-restic-bridge/`, `abc-lineage-bridge/`, `abc-drs-server/`, `abc-htsget-server/`, `abc-refget-server/`, `abc-chat-svc/`, `abc-data-shared/` — (planned new packages)

## Cross-doc references

Other design docs in this folder that interact with this one:

- `abc-cli-design-v7.md` — overall CLI architecture; this doc extends the `abc data` subtree
- `data-upload-history-design.md` — upload history (predates the index; will eventually be subsumed by Phase 3 write-through)
- `abc-nodes-services.md` — node-level services topology that the data platform plugs into
- `abc-nodes-observability-and-operations.md` — observability baseline that Phase 2's OpenTelemetry rollout extends

## Open questions that have not yet been resolved in the doc

These were raised in the design conversation and parked for the next planning round:

1. **`cd` persistence** for the discovery layer — shell env var vs. per-context state file vs. process-local. (See §6a / Phase 11 stub.)
2. **Lineage redaction policy** for cross-workspace ancestors — hard hide / topology-only / name-only. (See §9 ACL discussion.)
3. **Workspace inheritance on move** — stay with original owner unless `--transfer-ownership` flag, vs. transfer-on-move.
4. **Cross-workspace cache hit disclosure** — opt-in per workspace vs. on-by-default with topology-only redaction.
5. **Egress consent UX for public Storj DCS** (Phase 5.6.c) — `--from-public-dr` flag mandatory, or per-workspace policy default.

Future planning rounds should resolve each of these before the relevant phase begins implementation.

---

*End of Appendix D. End of document.*
