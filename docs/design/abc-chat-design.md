# abc chat — UX Brainstorm (Round 3, narrowed)

## Re-anchoring

Round 2 drifted into a maximalist "natural-language frontend to the XTDB index" vision with rich storyboards (lineage trees, cost forecasts, compliance triage). The user pulled it back. The settled framing:

> **Chat is glue. The CLI is ground truth.**

Chat exists to **orient users to the right `abc` command** and answer simple introspection questions. It does **not** absorb the work of `abc data search`, `abc data lineage`, `abc cost forecast`, `abc compliance audit`. Those are first-class CLI surfaces with their own structured renderings; chat points to them.

This document is the brainstorm at that scope. No code — only the conceptual model and the interaction shapes.

---

## Settled Decisions

| Dimension | Choice |
|-----------|--------|
| Role | Glue — command discovery, history Q&A, light bioinformatics orientation |
| Surface | TUI (`abc chat`) + one-shot (`abc chat "question"`) |
| One-shot latency | Two-tier: regex fast-path for `docs.usage` lookups, LLM otherwise |
| Backend | **Cluster-hosted Ollama only** — fails closed if unreachable, no external fallback |
| Model | Mistral 7B default, configurable per namespace by admin |
| Data scope | Metadata only — names, statuses, paths, sizes, timestamps |
| History | Local JSONL **and** cluster-side audit stream from day one |
| Tool catalog | Ship thin: only tools with real backing today |
| Mutation | **Read-only.** Chat never runs commands. No `/run` chord, ever. |
| Output | **Human-readable only.** No `--json`. Scripts use the structured CLI. |
| Trust seam | `↳ /why` chord on every answer reveals the tool-call trace |
| Out-of-scope | Polite refusal + suggested in-scope rephrase |

---

## What Chat Is For (and Isn't)

### In scope

1. **Command discovery** — *"How do I run a job with 8 cores?"* → cite the directive, link to the command
2. **Syntax recall** — *"What's the flag for setting a timeout?"* → `#ABC --time=HH:MM:SS`
3. **History orientation** — *"What did I run last week?"* → quick summary + pointer to `abc job list --since=7d`
4. **Bioinformatics how-to (cluster-flavored)** — *"How do I do variant calling here?"* → which modules exist, which examples to read
5. **Where-do-I-find-X** — *"Where does the cluster put job logs?"* → `abc job logs <id>`, retention notes
6. **Translating intent to commands** — *"I want to add 32GB of RAM to my last script"* → show the diff to the directive line

### Explicitly out of scope (belongs to CLI, not chat)

| User wants… | Goes to | Not chat because |
|-------------|---------|------------------|
| Lineage of a file | `abc data lineage <uri>` (Phase 4) | Tree rendering, drill-down, citation — chat would be a worse surface |
| Cost forecasts / budget detail | `abc cost forecast`, `abc cost show` | Time series + projection deserves a real renderer, not narration |
| Compliance triage | `abc compliance audit`, `abc compliance retention` | Long lists, multi-pane diff/triage UI |
| Data search | `abc data search` (Phase 2.7) | Faceted filters + structured output |
| Investigation rollup | `abc study show` (future) | Multi-pane composition |

The chat **may name these commands** when relevant — that is the glue role — but it does not duplicate their output.

---

## Interaction Shapes

### Shape 1 — Command discovery

```
You  how do I run a job with 8 cores and 32GB of RAM?

abc  Add cores and memory directives to your script preamble:

       #ABC --cores=8 --mem=32GiB

     Then submit it as usual:
       abc job run script.sh

     See `abc job run --help` or the directive reference in USAGE.md.

     [tools used: docs.usage(directives)]
```

Short, citation-grounded, points at the command. No fluff.

---

### Shape 2 — Syntax recall

```
You  what's the timeout directive again?

abc  #ABC --time=HH:MM:SS  (e.g. --time=04:30:00 for 4h30m)

     [tools used: docs.usage(directives)]
```

One-liner. The chat does not lecture.

---

### Shape 3 — Quick history orientation

```
You  what did I run last week?

abc  4 jobs and 1 pipeline between Apr 24–30. To see the full list:
       abc job list --since=2026-04-24
       abc pipeline list --since=2026-04-24

     One failure jumps out: deseq2-S00123 (Apr 26). Inspect with:
       abc job logs deseq2-S00123
       abc job trace deseq2-S00123     # when Phase 4 ships

     [tools used: nomad.jobs(period=7d), pipeline.runs(period=7d)]
```

Note the shape: a **summary sentence**, then **commands to drill in**. The chat does not render a 50-row table — that's `abc job list`'s job. It surfaces the headline and hands off.

---

### Shape 4 — Translating intent to a command

```
You  I want to bump my last script to 16 cores

abc  Edit the preamble of trim-S00125.sh:
       - #ABC --cores=8
       + #ABC --cores=16

     Then re-run:
       abc job run trim-S00125.sh

     Note: this will roughly double the cost of the run
     (linear in CPU-hours). Use `abc cost estimate trim-S00125.sh`
     for a precise figure.

     [tools used: docs.usage(directives), nomad.jobs(last=1)]
```

Chat names the cost concern but **defers the actual estimate to a CLI command** — that's the glue boundary in action.

---

### Shape 5 — Bioinformatics how-to, cluster-scoped

```
You  how do I do variant calling on this cluster?

abc  Two paths supported here:

     1. nf-core/sarek pipeline (recommended for whole-genome/exome).
        Available modules: bwa-mem2, gatk4-haplotypecaller,
        gatk4-genotypegvcfs.
        Start with:
           abc pipeline run nf-core/sarek --help

     2. Custom job script using gatk wave-exec runtime.
        See the example:
           examples/wave-bio/03-variant-calling.sh

     [tools used: docs.example(variant-calling),
                  pipeline.list(public)]
```

Bounded to **what this cluster supports**. The model is not asked to teach variant calling in general — it tells the user which of the cluster's existing on-ramps to use.

---

### Shape 6 — Where to find something

```
You  where do my job logs live after the job finishes?

abc  Logs persist in Nomad for 7 days, then archived to S3.
     Read them with:
       abc job logs <job-id>             # within retention window
       abc data ls abc://logs/<job-id>/  # archived

     [tools used: docs.usage(logs)]
```

---

### Shape 7 — Out-of-scope refusal with redirect

```
You  what's the best aligner for long reads?

abc  I can't give general bioinformatics advice — only what this
     cluster supports. From the modules registered here:
       - minimap2 (long reads, recommended for ONT/PacBio)
       - winnowmap (repetitive regions)

     See examples/wave-bio/02-long-read-align.sh for a starter.

     [tools used: docs.example, pipeline.list(modules)]
```

The refusal is constructive: it transforms "what's best?" into "what's available here?" — a question the cluster *can* answer.

---

### Shape 8 — Hard out-of-scope

```
You  write me a haiku about CRISPR

abc  I can only help with abc cluster usage, your job and pipeline
     history, and bioinformatics workflows supported here.
```

Flat refusal. No redirect when there isn't a sensible in-scope rephrase.

---

## Tool Catalog at v1 (Thin)

Only tools with real backing today. Each returns a small structured payload that the LLM narrates briefly.

| Tool | Backed by | Used in shapes |
|------|-----------|----------------|
| `docs.usage(topic)` | Embedded `USAGE.md` | 1, 2, 4, 6 |
| `docs.example(scenario)` | Embedded `examples/` | 5, 7 |
| `nomad.jobs(filter)` | Existing `abc job list` API | 3, 4 |
| `pipeline.runs(filter)` | Existing `abc pipeline list` API | 3 |
| `pipeline.list(public)` | Existing pipeline registry | 5, 7 |
| `accounting.attribute(job_id)` | Existing accounting API | (referenced for cost hints, deferred to CLI) |

**Not wired in v1** — but the model is told they will exist later, so when a user asks for them, the answer is:

> *"That's what `abc data lineage` will do once the data platform's lineage phase ships — not available yet."*

This is honest and shapes expectations. As phases land, tools light up. The system prompt is updated; nothing else changes.

---

## Audit Model (From Day One)

Per the settled decision, every chat turn is a compliance event. Two destinations:

### Local: `~/.abc/chat/history.jsonl`

For the user's own recall and `abc chat --history` browsing.

### Cluster: audit stream

Streamed to a cluster-side topic the moment the chat session is bound to a context. Record schema:

```
ts                 ISO8601
user               whoami label
namespace          active context's namespace
scope              user / admin / sudo / cloud
session_id         per-invocation
turn_index         0..n
prompt_text        full text  (or hash, per namespace policy)
tools_called       [{name, args_redacted, result_count, latency_ms}]
response_text      full text  (or hash)
response_class     answer / refusal-out-of-scope / refusal-policy
                  / error-backend-down
model              ollama model id
ollama_node        which node served the inference
tokens_in/out      usage
abc_chat_version   build SHA
```

Two affordances this enables later:

- **`abc compliance audit chat --user X --since 30d`** — full chat trail for a user
- **Per-namespace prompt-text policy** — PHI namespaces store hashes only; sandbox namespaces store full text. Decided by the same OPA layer as everything else.

This means **the chat surface ships pre-wired for the compliance trajectory**. When Phase 6 lands, no retro-import is needed.

---

## Cluster-Hosted Backend — Failure Modes

Hard rule: external backends are rejected at config-load. The implications worth thinking through:

### Ollama up → chat works

Normal path. Discovered via `abc cluster capabilities sync` → `capabilities.ollama_addr`.

### Ollama unreachable → chat fails closed

```
$ abc chat
✗ chat unavailable: cluster Ollama service did not respond at
  http://ollama.service:11434 (last successful: Apr 30 08:14)

  This cluster is configured for cluster-hosted inference only.
  External providers are not enabled.

  Try:
    abc cluster capabilities sync   # re-discover
    abc admin nomad job status ollama   # if admin
```

No silent fallback. The chat does not pretend to work.

### Ollama up but model not loaded

```
$ abc chat
⚠ chat backend reachable, but model 'mistral:7b' is not loaded.

  If you are an admin:
    abc admin nomad job dispatch ollama-warm --model mistral:7b
```

Concrete next-step instruction. Don't strand the user.

### Ollama under heavy queue

Show a queue-position indicator instead of a generic spinner:

```
abc  thinking... (queued #3, ~12s)
```

So the user understands the wait isn't the model being slow — it's the cluster being shared.

---

## Conversation Memory (Bounded)

Within a session, the chat keeps the last ~6 turns. Cross-session memory is **not** exposed to the model by default — the user can opt in per turn:

```
You  /recall last-week-deseq2

abc  Pulled in 3 turns from session ch-3a99 (Apr 26):
     [summary…]
```

This keeps prompts small, audit logs clean, and avoids the failure mode of the model "remembering" something the user has forgotten saying.

---

## Scope-Aware Tool Gating (Lightweight v1)

Persona-based filtering at session start. v1 does not yet have admin-only tools wired, but the gating mechanism is in place so Phase 6 compliance tools slot in cleanly.

| Persona | v1 tools | Future tools (gated when wired) |
|---------|----------|-------------------------------|
| `bioinformatics-user` | docs.*, nomad.jobs (own), pipeline.runs (own) | accounting.attribute (own) |
| `bioinformatics-admin` | + nomad.jobs (namespace), pipeline.runs (namespace) | + accounting.spend, compliance.policy_log |
| `cluster-admin` (`--sudo`) | + cross-namespace queries | + infra topology |
| `cloud` (`--cloud`) | (none in v1) | + accounting.set, policy.edit (always with confirm-chord) |

Write tools never auto-fire — they always require a `↳ /confirm …` chord.

---

## Visualization in the TUI (Restrained)

Given the narrower role, the TUI rendering set is small:

| Block | Purpose |
|-------|---------|
| Plain text | The default — the chat is mostly prose + commands |
| Code block | For commands and script fragments |
| Inline diff | Shape 4 — proposing edits to a script |
| Suggested-prompt chord | One-line `↳ <prompt>` rows the user can press to follow up |
| `view query` chord | Audit seam — opens the tool-call trace |

No tables, no trees, no gauges. Those are CLI command outputs. Chat stays text-shaped.

---

## What Round 2 Got Wrong (Documented for Posterity)

Round 2 proposed:
- Lineage tree rendering inside chat → **belongs to `abc data lineage`**
- Cost forecast narratives → **belongs to `abc cost forecast`**
- Compliance triage views → **belongs to `abc compliance audit`**
- Investigation rollups → **belongs to `abc study show` (future)**
- Ambient budget gauges in TUI → **belongs to a status bar / `abc cost`**
- Inline tables for jobs/files → **belongs to existing list commands**

Each of these is genuinely valuable, but each deserves a **dedicated CLI surface with structured rendering**. Putting them in chat would (a) make the chat a worse version of those commands, and (b) blur the architectural boundary the user is drawing: chat for navigation, CLI for the thing.

These ideas should migrate to **a separate plan file** for the data platform CLI roadmap (`abc data lineage`, `abc cost forecast`, etc.), not be left orphaned in the chat plan.

---

## Two-Tier One-Shot Path (detail)

The fast path is a small classifier in front of the chat command, not in the model:

```
abc chat "QUERY"
   │
   ▼
classify(QUERY)
   │
   ├── matches "what's the X flag?" / "X directive" / "syntax for X"
   │     → grep USAGE.md for the directive/flag, print one-liner, exit
   │     → ~50–100ms, no Ollama touch
   │     → still audit-logged (response_class: fast-path)
   │
   └── otherwise
         → full LLM path with tool calling
         → ~2–4s, cluster Ollama
```

Two design notes:

- The fast path is **transparent** — its answers cite `docs.usage` the same way the LLM path does, so users don't see two different shapes.
- The fast path only triggers in one-shot mode. The TUI always goes through the model — preserving conversational coherence and `/why`-traceability.

---

## `/why` Chord Detail

Every assistant turn ends with a `[tools used: …  ↳ /why]` footer. Pressing `/why` opens an inline pane with:

```
┌─ reasoning trace ─────────────────────────────────┐
│ system prompt fragment                            │
│   "When asked about job history, prefer naming    │
│    the abc command for drill-in over rendering    │
│    the table inline."                             │
│                                                   │
│ tools called                                      │
│   nomad.jobs(period=7d, scope=user)               │
│     → 4 rows in 38 ms                             │
│                                                   │
│ index freshness                                   │
│   last_sync: Apr 30 22:14 UTC (7h ago)            │
│                                                   │
│ model + node                                      │
│   mistral:7b on llm-01 · 412→248 tokens · 2.1s    │
└───────────────────────────────────────────────────┘
```

This is mostly a re-render of the audit record for the same turn — no new data path needed. The trust dividend is meaningful: a power user who is going to read `view query` once decides whether to trust the assistant; a compliance auditor reviewing a session has the same trace inline that they'd otherwise pull from the audit topic.

Implementation cost is small *because* the audit log already captures everything `/why` shows. `/why` is just a renderer.

---

## Remaining Open Questions — Recommendations

### Per-namespace model gating

**Recommendation: yes, but conservative.** PHI-flagged namespaces force the on-cluster default model; non-PHI namespaces may opt into a larger model when available. The decision is a property of the namespace's policy bundle (Jurist/OPA), not a user setting. Rationale: the data class boundary is the right unit. A user shouldn't be able to upgrade their own model in a namespace whose policy says "small model only," and an admin shouldn't have to remember which namespace is which — it's encoded.

```
namespace su-mbhg (PHI=true)        → mistral:7b  (forced)
namespace sandbox-pilot (PHI=false) → mixtral:8x7b (opt-in)
```

The chat surface shows the model in the header so it's never invisible:

```
[aither | su-mbhg-bioinformatics-user · mistral:7b]
```

### Shared sessions for support

**Recommendation: transcript export, not live attach.** A `/share` chord exports the current session as a sanitized transcript (or audit-log slice) the user can hand to an admin. Rationale:

- Live attach has unclear semantics: who is the model talking to? whose scope filters apply? whose audit trail records the turn?
- Transcript export composes with the audit trail already being built, so the admin sees exactly what the user saw.
- If real co-piloting is later wanted, build on the transcript-share primitive — don't start with the harder thing.

```
You  /share

abc  Sanitized transcript exported:
       /tmp/abc-chat-ch-7x9k-asharma-2026-05-01.md

     Send this to your admin. It contains:
       - your prompts (PHI-redacted per namespace policy)
       - the assistant's responses
       - the tool-call trace for each turn
     It does NOT contain your access tokens or Ollama session id.
```

---

## First-Run / Onboarding UX

The first `abc chat` invocation a user ever makes is the moment that decides whether they come back. Three failure modes to design against: (a) the user types nothing because they don't know what to ask, (b) the user asks an out-of-scope question, gets refused, and concludes the chat is useless, (c) the user asks an in-scope question but phrases it in a way the model can't ground (e.g. "show me my files" before any data has been indexed).

### Detection

Onboarding state is keyed off `~/.abc/chat/history.jsonl`. Empty file → first run. Some history but no turn in the active context → first run *for this context* (different posture than first-ever).

### Shape — first ever launch

```
┌─ abc chat ─────────────────────────────────────────────────┐
│ [aither | su-mbhg-bioinformatics-user · mistral:7b]   /help │
├─────────────────────────────────────────────────────────────┤
│                                                             │
│  abc  Hi — I'm a chat assistant scoped to this cluster.    │
│       I can help with four things:                          │
│                                                             │
│         1. abc command syntax & directives                  │
│         2. your job and pipeline history                    │
│         3. where things live in your storage namespaces     │
│         4. bioinformatics workflows supported here          │
│                                                             │
│       I won't answer general questions, run commands for    │
│       you, or guess things I can't look up. If you ask      │
│       something out of scope I'll say so.                   │
│                                                             │
│       Try one of these to get started:                      │
│                                                             │
│         ↳ how do I run a job with 8 cores and 32GB RAM?     │
│         ↳ what did I run this week?                         │
│         ↳ how do I do variant calling on this cluster?      │
│         ↳ /help    for the full slash-command list          │
│                                                             │
│ > _                                                         │
└─────────────────────────────────────────────────────────────┘
```

Three things this is doing:

1. **States the scope upfront, before the user types anything wrong.** Pre-empts the refusal-as-first-experience failure mode.
2. **States the negative contract too** — "won't answer general questions, run commands, or guess." Setting expectation lowers the chance of a bad first impression when the user later tests the boundary.
3. **The three suggestion chords are concrete, scoped to the cluster, and each routes to a different one of the four buckets.** This is the equivalent of a tutorial without making a tutorial. After 1–2 chord uses, the user has a working mental model.

### Shape — first run in a new context

When the user already has chat history but switches to a fresh context (e.g. `abc context use cloud-prod` for the first time), the welcome is shorter and context-specific:

```
abc  New context: cloud-prod (cluster-admin scope).

     Compared to aither, this context unlocks:
       - cross-namespace job and pipeline queries
       - accounting.spend (when wired)
       - cluster topology questions

     Try:
       ↳ what jobs are running across all namespaces right now?
       ↳ which namespaces are nearing their budget cap?
```

This is glue at the persona boundary — orienting the user to *what's different* in this context, not re-teaching the basics.

### `/help` content

```
You  /help

abc  Slash commands:
       /help       this list
       /history    browse past chat sessions
       /clear      end the current session
       /share      export sanitized transcript
       /why        explain the previous answer
       /quit       exit

     Scope reminders:
       I can answer:  cluster usage, your history, your data,
                      bioinformatics workflows on this cluster
       I cannot:      run commands, give general advice,
                      see file contents, mutate state

     Cluster status:
       backend:    http://ollama.service:11434
       model:      mistral:7b
       index sync: 7h ago
       audit:      streaming to compliance topic
```

Cluster status at the bottom of `/help` is doubly useful — it's the single place a user goes to figure out *why is the chat slow / wrong / refusing*.

### Onboarding-related design choices

- **No multi-screen tour.** A wall of welcome text fails. The three suggestion chords are the tour.
- **First-run message is a regular assistant turn**, audit-logged like any other. Predictable.
- **Suggestion chords are personalized by what's plausibly there.** If the user's `nomad.jobs(period=30d)` returns zero rows, drop the "what did I run this week?" chord and substitute "how do I submit my first job?".

---

## Failure & Recovery UX

The fail-closed Ollama policy is the headline, but real-world chat sessions have many partial failures. Each deserves a designed shape rather than a generic "something went wrong." The principle: **always tell the user what failed, what the consequences are for the answer they're looking at, and what to do next.**

### Failure taxonomy

| Failure | Where | Severity | UX shape |
|---------|-------|----------|----------|
| Ollama unreachable | session start | fatal | abort with concrete admin steps (already covered) |
| Ollama dies mid-session | between turns | fatal-for-session | clear banner, history kept, exit cleanly |
| Model loaded but slow | inference | warning | queue indicator, no scary error |
| Tool call timeout | mid-answer | recoverable | model warned, narrates partial answer with caveat |
| Tool returned 0 rows | mid-answer | normal | model says "nothing matches"; not an error |
| Tool returned auth error | mid-answer | recoverable | explain scope mismatch, suggest `--sudo` if applicable |
| Index stale / sync failed | session start | warning | banner with last-sync time, narrated by model on relevant answers |
| Audit stream unreachable | per-turn | depends | namespace policy decides: warn-and-continue vs fail-closed |
| Namespace policy fetch failed | session start | fatal | refuse to start chat — can't determine model/scope |
| Malformed tool call by model | mid-answer | recoverable | retry once with a stricter system prompt; surface if still bad |
| Model returns JSON pretending to be tool output | mid-answer | sneaky | reject + refuse to narrate; log as suspicious in audit |

### Shape — mid-session backend death

```
┌─ abc chat ─────────────────────────────────────────────────┐
│ [aither | su-mbhg-bioinformatics-user · mistral:7b]         │
├─────────────────────────────────────────────────────────────┤
│  You  what did I run last week?                            │
│                                                             │
│  abc  ⚠ chat backend stopped responding mid-answer.        │
│       Last contact: 14:22:09 UTC                           │
│                                                             │
│       Your session and history are saved. The CLI commands │
│       below give you the same information without chat:    │
│                                                             │
│         abc job list --since=7d                            │
│         abc pipeline list --since=7d                       │
│                                                             │
│       To investigate: abc admin nomad job status ollama    │
│                                                             │
│  [session ended]                                            │
└─────────────────────────────────────────────────────────────┘
```

Note the failure shape always names the **CLI fallback** for the question the user just asked. The chat being down is never a reason the user can't get the information — they just go through the ground-truth surface.

### Shape — tool timeout, partial answer

```
You  what did I run last week?

abc  Partial answer — one tool didn't respond:

     Nomad jobs (last 7d):
       trim-fastq        Apr 24  complete
       align-bwa-S00123  Apr 25  complete
       deseq2-S00123     Apr 26  failed

     ⚠ pipeline.runs timed out (5s) — pipeline history not shown.
       For pipelines, run: abc pipeline list --since=7d

     [tools used: nomad.jobs (ok), pipeline.runs (timeout) · ↳ /why]
```

Three discipline points here:
- The model is told never to fabricate data when a tool fails — narrate the success and own the gap.
- The CLI command for the missing slice is named explicitly.
- The tool footer marks `(timeout)` so `/why` shows the actual error.

### Shape — auth / scope mismatch

```
You  what's running across all namespaces?

abc  Your context (su-mbhg-bioinformatics-user) only sees jobs in
     namespace su-mbhg. To query cluster-wide:

       abc context use aither --sudo
       abc chat

     I can't escalate scope from inside the chat — that's by design.

     [tools used: nomad.jobs(scope=cross-namespace, denied)]
```

The chat surfaces the scope reality and tells the user how to elevate, but does not elevate itself. Consistent with read-only doctrine.

### Shape — stale index banner

When `last_sync` is older than a threshold (24h default, configurable), the TUI header shows it:

```
[aither | su-mbhg-bioinformatics-user · mistral:7b · index 26h stale]
```

And the model is given the staleness in its system prompt, so when an answer depends on the index it says:

```
abc  Based on the index as of Apr 30 22:14 UTC (26h ago), you have
     6 BAMs in study mbhg-001. Anything uploaded since then won't
     be reflected. Refresh with:
       abc cluster capabilities sync
```

The staleness disclosure is automatic, never silent. This matters because the index is often the user's mental model of "what's there" — and a stale answer that *looks* fresh is much worse than an honest stale answer.

### Shape — audit stream unreachable

Per-namespace policy decides how to handle this:

```
namespace su-mbhg (PHI=true)        → fail closed
                                       "audit unreachable, chat refusing
                                        to start until logging is restored"

namespace sandbox-pilot (PHI=false) → warn and continue
                                       "⚠ chat audit stream unreachable;
                                        local history still recorded"
```

This is the right place to encode it: PHI namespaces have the regulatory exposure that justifies refusing to operate without audit. Sandbox namespaces don't.

### Shape — model returns malformed tool call

```
[internal — not shown to user on first attempt]

retry with stricter prompt: "Tool call must match schema X.
Previous attempt: <bad json>"

[on second failure]

abc  I tried to look that up but couldn't form a valid query.
     Try rephrasing, or run the command directly:
       abc job list --status=failed --since=7d

     [tools used: nomad.jobs(malformed × 2) · ↳ /why]
```

One silent retry, then surface honestly. The audit log records both attempts so the model's reliability over time is measurable — a property the platform's compliance posture probably wants anyway.

### Shape — model attempts to narrate without a tool call (hallucination guard)

If the user asks a question whose answer requires a tool (e.g. "how many jobs did I run last week?") and the model returns text without calling a tool, a post-response classifier flags it and replaces the answer:

```
abc  I started to answer that without checking the cluster — that's
     not how I should work. Retrying with a query…

     [retried answer follows]
```

This is the most important guard rail of the bunch. The model "remembering" job counts from training data instead of querying is the failure mode that destroys trust fastest.

### Recovery summary

The unifying design rule across these shapes: **the chat is honest about its limitations and always names the CLI escape hatch.** The user is never stranded. The chat being unable to answer is never a dead end — it's a signpost back to the ground-truth surface.

---

## What Round 3 Has Settled (Summary)

- Chat is glue, CLI is ground truth. No lineage trees / cost forecasts / compliance triage in chat — those go in dedicated CLI surfaces.
- Eight tight interaction shapes (command discovery, syntax recall, history orientation, intent translation, bioinformatics how-to, where-to-find, soft refusal with redirect, hard refusal).
- v1 tools: `docs.usage`, `docs.example`, `nomad.jobs`, `pipeline.runs`, `pipeline.list`. Future tools advertised as "not yet" rather than stubbed out.
- Cluster-hosted Ollama only; fails closed.
- Audit-from-day-one, with the schema above; `abc compliance audit chat` lands free when Phase 6 ships.
- One-shot mode has a regex fast path for syntax lookups; full LLM path for everything else; TUI always goes through the model.
- Read-only by doctrine — no `/run` chord.
- No `--json` — scripts use the structured CLI commands.
- `/why` chord on every answer; cheap because it re-renders the audit record.
- Per-namespace model gating tied to PHI flag.
- Shared support sessions via transcript export, not live attach.
