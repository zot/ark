# Simple Recall

Language: Go. Environment: built-in subsystem of `ark serve`,
configured via the `[recall]` section in `ark.toml`. No CLI flag
or sidecar process — the watcher and result-doc builder both
live inside the server.

Simple Recall is ambient recall — the corpus quietly suggesting
related material as a Claude Code conversation unfolds. The
pipeline has two layers:

- **Watcher** (deterministic). Watches Claude Code JSONL
  conversations as they grow, runs the chunk-similarity
  substrate against the indexed chunks of each completed turn,
  and writes a **curation doc** to `tmp://ARK-RECALL/`. No
  language model in this layer.
- **Recall agent** (Haiku, one-shot). The assistant subscribes
  to curation events; on event arrival it spawns a one-shot
  Haiku subagent via the Task tool. The agent reads the
  curation doc via `ark fetch`, decides which candidates are
  surface-worthy and which proposed tags are recommendable, and
  writes a **result doc** the assistant reads.

The split keeps an LLM out of the high-frequency watcher path
while letting a cheap model curate before anything reaches the
assistant. Three of the open questions that the original
agent-in-watcher design raised disappear by construction:

- **Process lifecycle.** `ark serve` already runs; the watcher
  is a tracked subsystem inside it. The agent is one-shot per
  fire — no daemon to keep alive.
- **Target-session discovery.** The JSONL filename *is* the
  session UUID. No env-var dance, no handshake.
- **Multi-tenancy.** One server per machine watches every
  session under `~/.claude/projects/`. Per-machine isolation
  falls out for free.

Failure modes reduce to standard server-side error reporting
(watcher errors land where scanner errors do) and standard
Task-tool failure handling (the assistant retries or drops).

The substrate is already there: `ark connections recall
--session SID --propose` does the heavy work. The watcher is
plumbing between the indexer's append callback and a Go-internal
curation-doc writer. The agent talks to the substrate through
a thin set of result-builder CLI verbs.

## Architecture

```
~/.claude/projects/<project>/<session>.jsonl
  │  (append detected by ark's existing scanner)
  ▼
indexer.executeRefresh (isAppend=true)
  │
  │  for each qualifying append:
  ▼
RecallWatcher.OnAppend(path, strategy, newBytes, added)
  │
  ├─ accumulate `added` into pendingChunks[session]
  │
  ├─ scan newBytes line-by-line:
  │     - `"type":"user"`             → cancel pendingTimer[session]
  │     - `"subtype":"turn_duration"` → arm pendingTimer[session]
  │                                     for activation_delay seconds
  ▼ (timer expiry, separate goroutine)
RecallWatcher.fire(session)
  │
  ├─ allocate next global fire number (monotonic per
  │   `ark serve` run, starting at 0)
  │
  ├─ for each chunkID in pendingChunks[session]:
  │     markdown-rechunk; for each paragraph ≥ 30 bytes:
  │       Recall(Text=paragraph, KeepTagless=true,
  │              Propose=true) → top-K candidates
  │
  ├─ if zero sections survive min_similarity gate:
  │     no curation doc written; pendingChunks cleared.
  │
  ├─ else: watcher writes curation doc directly via Go-internal
  │     RecallDocBuilder (no CLI roundtrip):
  │       tmp://ARK-RECALL/curation-<session>-<fire>
  │       @ark-recall-curate: <session>
  │       @ark-recall-fire: <fire>
  │       # Source Chunk: <jsonl-chunkid>
  │       ## Candidate: ...
  │
  └─ mark-on-send: RD records for inline + ext-routed tags on
       every surfaced chunk for <session>
                                       │
                                       │ pubsub publishes the new tmp:// path
                                       ▼
                          assistant — running `ark listen` on a
                          subscription to `@ark-recall-curate`
                                       │
                                       ├─ inspects the event's tag value;
                                       │   drops events whose curate value
                                       │   isn't its own session
                                       │
                                       ├─ reserves a nonce:
                                       │   ark connections recall reserve-nonce → N
                                       │
                                       └─ launches the recall agent via Task,
                                          embedding fire and nonce in description:
                                          "ark-recall fire <F> nonce <N>"
                                                  │
                                                  ▼
                                       recall agent (Haiku subagent, one-shot)
                                                  │
                                                  ├─ reads the curation doc:
                                                  │   ark fetch tmp://ARK-RECALL/...
                                                  │   (Read tool is denied by the
                                                  │    PreToolUse guard; denial
                                                  │    carries the ark fetch
                                                  │    template as the runway —
                                                  │    fumble-onboarding pattern)
                                                  │
                                                  ├─ for each candidate worth surfacing:
                                                  │   ark connections recall surface <F> \
                                                  │     -chunk N -range PATH:RANGE \
                                                  │     -reason "..."
                                                  │
                                                  ├─ for each proposed tag worth recommending:
                                                  │   ark connections recall recommend <F> \
                                                  │     -chunk N -tag @t[:v] \
                                                  │     -reason "..."
                                                  │
                                                  └─ ark connections recall close <F> --nonce <N>
                                                         │
                                                         ├─ writes tmp://ARK-RECALL/result-<S>-<F>
                                                         │   iff any surface/recommend items were added
                                                         ├─ removes the curation doc
                                                         │   (unless -preserve-curation)
                                                         ├─ discovers the agent's own JSONL
                                                         │   via nonce → meta.json lookup,
                                                         │   sums tokens
                                                         └─ appends a record to
                                                            ~/.ark/monitoring/recall.jsonl
                                                  │
                                       pubsub publishes new result tmp:// path
                                                  │
                                                  │ @ark-recall-result=<own-session> matches
                                                  ▼
                                       assistant
                                                  │
                                                  ├─ `ark listen` returned the event
                                                  ├─ `ark fetch` reads the result doc
                                                  └─ decides whether to surface to user
```

## Configuration — `[recall]` in `ark.toml`

`[recall]` is the only control surface. Recall is a per-corpus
property; ark.toml gates it. Knobs:

| Key                       | Default | Meaning                                                                                                                                                          |
|---------------------------|---------|------------------------------------------------------------------------------------------------------------------------------------------------------------------|
| `enabled`                 | `false` | Master switch. `false` disables the watcher entirely; `true` enables it for all Claude-JSONL sources (unless `sources` narrows the set).                         |
| `propose`                 | `true`  | Pass `--propose` to the recall substrate so RC records accrue on every per-chunk Recall call.                                                                    |
| `min_similarity`          | `0.65`  | Per-section similarity gate. Sections whose top recalled chunk's aggregate similarity is below this are dropped before the curation doc is written.              |
| `min_propose_similarity`  | `0.70`  | Chunk-EC ↔ tag-ED cosine floor for derived-tag proposals. Candidates below this are never written as RC records.                                                 |
| `activation_delay`        | `15`    | Seconds the watcher waits after seeing a `turn_duration` record before firing the recall pass. A user record arriving inside this window cancels the firing.     |
| `chunks_per_dm`           | `5`     | Per-paragraph top-K cap. Each candidate set in the curation doc lists at most this many recalled chunks. (Historical name; semantics unchanged.)                 |
| `sources`                 | `[]`    | Optional whitelist of source root directories. When empty, the watcher autodetects (any source whose strategy is `chat-jsonl`).                                  |
| `discussed_ttl`           | `"24h"` | Time-to-live for RD records that suppress re-surfacing of recently-discussed tags.                                                                               |
| `reject_propose_ceiling`  | `0`     | Once a `(chunk, tag)` pair accumulates this many rejections, the propose pass stops surfacing it. `0` (unset) = infinite; safe default.                          |
| `reject_mention_ceiling`  | `0`     | Once a `(chunk, tag)` pair accumulates this many rejections, the assistant stops mentioning the count. `0` = infinite.                                           |

`ark.toml` is the live configuration surface — `ark serve`
reads `[recall]` on startup and on the existing config-reload
path. The watcher pulls config from `db.Config().Recall` on
each pass, so toggling `enabled` or any other knob takes effect
on the next turn boundary without a restart.

The retired `agent_cmd` reservation from v1 is gone. The recall
agent is invoked by the assistant via the Task tool, not by a
configured command; future orchestrator wiring (Phase 2 /
Luhmann, see `.scratch/SIMPLE-RECALL.md`) owns its own startup.

## Trigger semantics

The watcher hooks into `indexer.executeRefresh`'s isAppend
branch. The indexer hands the watcher `(path, strategy, newBytes,
added)` for each committed append. Trigger semantics are
unchanged from v1:

- **Source qualification.** A source qualifies when its
  chunker strategy is `chat-jsonl` and (if `sources` is
  non-empty) the chunk's source root matches an entry in the
  whitelist.
- **Accumulation.** Every `added` chunkID gets appended to
  the per-session `pendingChunks` slice. The watcher does not
  fire per chunk; chunks pile up.
- **Trigger arming.** The watcher scans `newBytes` line-by-line
  for the assistant's turn-end signal: a line whose top-level
  type is `"system"` with `"subtype":"turn_duration"` arms the
  `pendingTimer` for `activation_delay` seconds.
- **Trigger cancellation.** A line whose top-level type is
  `"user"` cancels any currently-armed `pendingTimer`. The
  accumulated `pendingChunks` are *not* cleared — they remain
  pending and roll into the next fire.
- **Fire on expiry.** When the timer expires, the watcher
  allocates the next global fire number, processes everything
  currently in `pendingChunks[session]`, and clears the slice.
- **Cold start.** Go-forward only. No backfill of pre-existing
  JSONL chunks on watcher startup.
- **Self-exclusion.** Inherited from the recall substrate.

The per-line scan uses lightweight JSON parsing — each JSONL
line is a self-contained JSON object, so the watcher parses
one line at a time and inspects the top-level `type` and
`subtype` fields.

## Fire counter

The fire number is a globally monotonic counter scoped to one
`ark serve` process lifetime, starting at 0 and allocated by
the watcher at the moment `pendingTimer` expires. The same fire
number ties the curation doc and the result doc together for
that turn.

In-flight fires don't survive a server restart — `tmp://` is
per-process, so the curation/result docs disappear and the
counter resets. No persistence is needed.

## Curation doc shape

Path: `tmp://ARK-RECALL/curation-<session>-<fire>`.

The `<session>` segment is technically redundant (fire is
globally unique within an `ark serve` run) but the path is more
diagnostic-friendly when listing the tmp:// directory.

Header tags (one per line, no blank line between them and the
first body section):

```
@ark-recall-curate: <originating-session-uuid>
@ark-recall-fire: <fire>
```

Body — one `# Source Chunk:` H1 per paragraph that cleared
`min_similarity`, each followed by 1..K `## Candidate:` H2s
ordered by score descending:

```
# Source Chunk: <jsonl-chunkid>

> <paragraph excerpt — first ~200 bytes, UTF-8-safe truncation>

## Candidate: <chunkid> (<size>) <path>:<range>

- score: <0.NN>
- tags: <comma-separated bare tagnames>
- proposed-tags: <name> (<0.NN>), <name> (<0.NN>), ...

```
<~500-char excerpt of chunk content>
```

## Candidate: <chunkid> (<size>) <path>:<range>
- ...
```

Notes:

- The paragraph excerpt is `> `-quoted markdown so the agent
  reading the doc as plain text sees what triggered each
  section.
- `tags` lists the candidate chunk's inline and ext-routed
  tag *names* (bare). Values aren't shown in the curation doc;
  the agent reads them via `ark fetch` if it needs detail.
- `proposed-tags` lists derived-tag candidates surviving
  `min_propose_similarity`, each rendered with the
  parenthesized chunk-EC ↔ tag-ED cosine score from the
  propose pass.
- The content excerpt uses a fenced block (not blockquoted) so
  the agent can distinguish it from the source-paragraph
  excerpt without ambiguity.

When zero paragraphs across `pendingChunks` clear the
similarity gate, the watcher writes nothing — the fire
completes silently, `pendingChunks` is cleared, no curation doc
is published.

## Result doc shape

Path: `tmp://ARK-RECALL/result-<session>-<fire>`.

Header tag (one line, no `@ark-recall-fire` — the assistant
correlates via the pubsub event path):

```
@ark-recall-result: <originating-session-uuid>
```

Body — siblings of two H2 kinds, in the order the agent emitted
them. **Surface** items recommend showing a chunk to the user;
**Recommend** items propose a tag attach worth re-curating.

```
## Surface: <chunkid> (<size>)

reason: <one-line justification>

## Recommend: @<tag>[:<value>] on <chunkid>

reason: <one-line justification>
```

The Surface H2 carries the chunkID plus the chunk's byte size
in parentheses (e.g. `(33K)`, `(500b)`, `(1.2M)`). The
assistant resolves path / range / context on demand via
`ark chunks <chunkid> [-before N] [-after N]`; the size lets it
glance at the cost of fetching before deciding (some chunks are
in the tens of kB — markdown nested-list explosions, full
function definitions in bracket-go chunks). Keeping the result
doc thin preserves the assistant's "scan reasons, drill on
demand" loop.

The assistant has final say on whether to surface a chunk to
the user or to ask the user about a tag recommendation. The
agent's role is to filter and rank; the assistant's role is to
present.

When the agent has nothing surface-worthy and nothing to
recommend, it issues `close` with no prior `surface` /
`recommend` calls. No result doc is written, the curation doc
is still removed, and the monitoring log entry is still
written. The assistant's `ark listen` never sees a matching
event in that case.

## Builder CLI

The watcher uses an in-process Go builder; the agent uses
thin CLI wrappers backed by the same state machine. The two
paths produce identical doc shapes.

**Curation builder — Go-internal only, called by the watcher:**

```go
b := db.RecallCurationOpen(session, fire)
b.Section(sourceChunkID, sourceParagraphText)   // # Source Chunk: H1
b.Candidate(chunkID, path, rangeLabel, score,
            tagNames, proposedTagsWithScores,
            contentExcerpt)                      // ## Candidate: H2
b.Close()                                        // writes tmp:// doc
```

**Nonce reservation — CLI, called by the assistant:**

```
ark connections recall reserve-nonce
```

Returns a small monotonic integer (`1`, `2`, ...) per `ark
serve` run. In-memory counter; resets on restart. The assistant
calls this once before invoking the recall agent via Task; the
integer is embedded in the Task `description` field as the
literal string `ark-recall fire <F> nonce <N>` (see
*Nonce-in-description format* below) and passed into the
agent's prompt body.

**Result-doc builder — CLI, called by the recall agent. The
fire number is the cookie that ties calls together.**

```
ark connections recall surface <F> -chunk N -range PATH:RANGE \
                                   -reason "..."
ark connections recall recommend <F> -chunk N -tag @t[:v] \
                                     -reason "..."
ark connections recall close <F> --nonce <N> [-preserve-curation]
```

Discipline:

- One item per `surface` / `recommend` call. Repeated `-chunk`
  flags are not accepted. The one-per-call shape keeps the
  agent's flag generation simple and the in-flight state
  machine boring.
- `surface` and `recommend` implicitly open the result-doc
  builder for `<F>` on first call.
- `close` works whether or not the result builder was ever
  opened. It is the single cleanup verb:
  - If `surface` / `recommend` items were added, write
    `tmp://ARK-RECALL/result-<session>-<F>`.
  - If nothing was added, write nothing — the assistant's
    `ark listen` never sees a matching event.
  - Remove `tmp://ARK-RECALL/curation-<session>-<F>` unless
    `-preserve-curation`. Also sweep any orphan curation docs
    for the same session whose fire number is strictly less
    than `<F>` — older fires the assistant missed handling.
    Same-session scope only; other sessions' orphans are not
    touched.
  - Locate the calling subagent's own JSONL via the nonce →
    `.meta.json` lookup described below; sum tokens; append
    one record to `~/.ark/monitoring/recall.jsonl`.

### Subscriber-presence gate

The watcher and `close` both query subscriber presence before
writing their respective tmp:// docs. See
[subscriber-presence.md](subscriber-presence.md) for the API
(`db.SubscriberCount`) and the CLI form (`ark subscribers --tag T`).

- **Watcher → curation doc.** Before writing
  `tmp://ARK-RECALL/curation-<session>-<fire>`, the watcher
  queries `SubscriberCount("ark-recall-curate", "<originating-session-uuid>")`.
  If zero, the watcher skips the curation write, clears
  `pendingChunks` as usual, and appends a record to
  `recall.jsonl` with `outcome: "no-subscriber"`.
- **`close` → result doc.** Before writing
  `tmp://ARK-RECALL/result-<session>-<fire>`, `close` queries
  `SubscriberCount("ark-recall-result", "<originating-session-uuid>")`.
  If zero, `close` skips the result write, still performs the
  curation removal + orphan sweep + monitoring-log append, and
  records `outcome: "no-subscriber"`.

The gate avoids paying the disk write + downstream agent cost when
no consumer is present. If the lights aren't on, there's nobody
home.

### Nonce-in-description format

The Task tool exposes the agent's `description` field in its
`.meta.json` sidecar. The assistant constructs the description
as the literal string:

```
ark-recall fire <F> nonce <N>
```

`close` discovers the agent's JSONL by scanning
`<subagents-dir>/*.meta.json` for descriptions whose body
contains `nonce <N>` as a substring. Trivially
`strings.Contains` parseable; no JSON parsing of the description
field is performed.

### Subagent JSONL discovery

At `close` time:

1. `cwd_encoded := replace_slashes_with_dashes(cwd)` — the ark
   CLI assumes cwd is the session's project directory.
2. `parent_session := $CLAUDE_CODE_SESSION_ID` — exposed by
   Claude Code in the subagent's environment.
3. `subagents_dir := ~/.claude/projects/<cwd_encoded>/<parent_session>/subagents/`.
4. Scan `<subagents_dir>/*.meta.json` for entries whose
   `description` field contains `nonce <N>`.
5. The matched basename gives `agent-<id>.jsonl` (paired with
   `agent-<id>.meta.json`).
6. Sum `usage.input_tokens` / `usage.output_tokens` across all
   `"type":"assistant"` records in that JSONL. No `isSidechain`
   filter is needed — the file is the subagent's dedicated
   transcript.

The assistant record that issues the `close` tool call has its
own usage counted, but any wrap-up response the agent emits
after `close` returns is missed — it isn't in the JSONL at the
time `close` reads it. Expected undercount is small (<1k tokens
per fire) and acceptable as a monitor metric. The monitoring
view documents this so the totals aren't surprising.

### Monitoring log

`~/.ark/monitoring/recall.jsonl` is the append-only log written
by every `close` call. Each line is one JSON record:

```json
{
  "fire": 17,
  "session": "<originating-session-uuid>",
  "nonce": 5,
  "in_tokens": 1842,
  "out_tokens": 211,
  "context_tokens": 26139,
  "latency_ms": 1934,
  "surfaced": 2,
  "recommended": 1,
  "outcome": "result-emitted",
  "timestamp": "2026-05-26T20:31:14Z"
}
```

`context_tokens` is the agent's cumulative context fill at close
time (`cache_creation_input_tokens + cache_read_input_tokens` from
the most recent assistant record in the subagent's JSONL — the
same value `ark connections recall context` returns). In v2
one-shot fires the number is roughly per-fire static; in Phase 2's
long-running lotto-tube agent it's the load-bearing telemetry
showing context creep across fires until the agent self-recycles.

`outcome` is one of `result-emitted`, `silent-close`,
`no-subscriber`, or `error`. `no-subscriber` covers both gate
points: the watcher's pre-curation skip and `close`'s
pre-result skip (see the *Subscriber-presence gate* section
above). `surfaced` and `recommended` are counts of items
added before `close`. The format is intentionally append-only
and forward-compatible; future fields slot in at the end.

## Recall agent

The agent definition lives at
`.claude/agents/ark-recall-agent.md`. Shape mirrors
`ark-messenger` / `ark-searcher`:

- Model: Haiku 4.5.
- `memory: local` so MEMORY.md does not leak into the agent.
- Skill file at `~/.ark/skills/ark-recall.md`, fetched
  via the fumble-onboarding pattern: the PreToolUse guard
  denies the agent's first tool attempt and the denial message
  carries the `ark fetch` template as the runway. A Fumble Log
  (silent ride-along) records parse failures so we can tighten
  the curation-doc format over time.

**Tool allowlist (hermetic seal, enforced by the PreToolUse
guard script):**

- `Bash` permitted only for:
  - `ark fetch tmp://ARK-RECALL/curation-*` (the agent's only
    read path)
  - `ark connections recall surface <F> ...`
  - `ark connections recall recommend <F> ...`
  - `ark connections recall close <F> [-preserve-curation]`
- `Read`, `Edit`, `Write`, and network tools are all denied.
  The `Read` denial is the runway — the guard's stderr tells
  the agent to use `ark fetch` instead.

**Persona briefing.** The agent is a curator, not a
synthesizer. Its job: read the curation doc, decide which
candidates fit the source paragraph well enough to surface, and
which proposed tags fit their chunk well enough to recommend.
Defaults to silence — better to drop a doc (close with no
items) than to spam the assistant.

**Agent does not write RJ records.** Permanent rejection is a
user-relayed decision. When the assistant surfaces a tag
recommendation to the user and the user rejects it, the
*assistant* calls `ark connections recall reject-derived` (the
existing path) to write the RJ. The agent's role is filter and
recommend; the durable rejection state stays under user
control.

**Subscriptions.** The agent does not subscribe to pubsub. It
is invoked one-shot per fire by the assistant via the Task
tool, with the curation doc path and the `(fire, nonce)`
identifiers passed in its prompt.

## Assistant subscriptions

The assistant runs two subscriptions, both scoped under its own
Claude Code session UUID as the subscription session ID:

```
ark subscribe --session <claude-code-session-uuid> \
              --tag ark-recall-curate
ark subscribe --session <claude-code-session-uuid> \
              --tag ark-recall-result=<claude-code-session-uuid>
```

- The curate subscription is **bare** (no value constraint).
  The assistant receives every curate event the watcher emits,
  inspects the `@ark-recall-curate` tag value, and drops events
  whose value isn't its own Claude Code session. Cheap filter,
  no false negatives if multi-session deployments later turn
  on.
- The result subscription is value-scoped to the assistant's
  own session so cross-session result docs never reach the
  wrong listener.

`ark listen --session <claude-code-session-uuid>` is the
blocking wait used to pop events from both subscriptions.

## Discussed-tag marking policy

Unchanged from v1. The watcher writes RD records for the
inline and ext-routed tags on every chunk surfaced in *any*
candidate within the curation doc, scoped to the originating
session:

- **Mark on write, not on action.** RD records are written
  when the watcher writes the curation doc, not when the
  agent or assistant acts on it. Whether the curation is
  surfaced or dropped, the tag-value has been "considered" for
  this session. Avoids re-curating the same chunks until
  `discussed_ttl` elapses.
- **Don't mark RC-derived candidates.** Derived tags listed
  in `proposed-tags` are proposals the agent has not acted on.
  Re-surfacing them in future passes is correct; the RC tally
  counter is the natural priority signal.

## RJ record format (with rejection counter)

Each RJ value carries a rejection count alongside the
most-recent timestamp:

- **Key:** unchanged — `"RJ"` + chunkid varint + tagname.
- **Value:** `varint(counter) + 8-byte BE unix nanos`. Counter
  increments on every rejection write; timestamp updates to
  "most recently rejected."

No data migration is required. The format change is part of
this round's implementation work, and the v1 RJ records are
trivially regenerated — `ark connections clean -all` wipes RJ
records and the next propose/reject cycle rewrites them in the
v2 shape.

Two readers consume the counter:

- The propose pass reads the counter alongside record existence.
  If `counter >= reject_propose_ceiling`, the candidate is
  suppressed before reaching the curation doc.
- The assistant's "mention rejected proposals" path reads the
  counter to gate whether the user even sees the count. Records
  with `counter >= reject_mention_ceiling` are silent.

Between the two ceilings the assistant may say "you have N
rejected proposals worth revisiting" (count only, no specifics).
Both ceilings default to `0` (infinite); v1 behavior is
preserved when the user leaves them unset.

## Quality bar

Three defense layers, cheapest first:

### Layer 1 — per-section similarity gate

`min_similarity` is applied per input paragraph's Recall result.
A `# Source Chunk:` section is included in the curation doc only
when its top recalled chunk's score clears the threshold. If no
section survives, no curation doc is written — the fire
completes silently. Turn-boundary firing naturally rate-limits
fires; no separate cooldown knob.

### Layer 2 — Haiku curator

The recall agent reads the curation doc and filters again.
Persona briefing biases hard toward silence: `close` with no
items is the preferred outcome when nothing fits. The agent's
cost is bounded (one curation doc per fire, ~2k tokens of input
typical).

### Layer 3 — assistant has final say

The result doc lists `Surface:` and `Recommend:` items, but the
assistant decides whether to actually present any of them to
the user. Conversation context wins over offline curation —
the assistant may drop a surface item because it just
discussed the same topic, or because the user is mid-flow on
something else.

## Logging and observability

The watcher continues to emit structured logs at decision
points (`armed`, `disarmed`, `fired`, `recall-error`). v2 adds
the monitoring log written by `close`:

- `~/.ark/monitoring/recall.jsonl` — one record per fire close,
  populated with the fields above. The CLI surface for reading
  this log lands in a later phase (`ark monitor`); the log
  itself is in scope here.

## What this pipeline does not do

- **No LLM in the watcher.** The watcher is deterministic on its
  inputs. The LLM lives only in the one-shot recall agent.
- **No new tag-name invention.** RC records expose statistical
  tag *value* candidates against existing tag definitions.
  Definition-class proposals (RP/RPE/RR records, reserved
  2026-05-24 in derived-tags.md A65) remain deferred.
- **No tag-axis filtering.** Deferred until
  `.scratch/TAG-AXES.md` earns implementation.
- **No backfill on cold start.** Go-forward only.
- **No backfill on subscriber arrival.** The subscriber-presence
  gate (below) skips the curation / result write entirely when no
  one is listening. A subscriber that arrives after the skip does
  not retroactively receive the dropped fire.
- **No long-running orchestrator.** Phase 1 spawns the agent
  one-shot per fire via Task. Phase 2 (Luhmann, see
  `.scratch/SIMPLE-RECALL.md`) introduces a long-running
  orchestrator; out of scope here.
- **Agent does not write RJ records.** The agent recommends;
  the user-via-assistant rejects.

## Test strategy

- Watcher disabled (`enabled = false`) — no curation doc is
  written regardless of JSONL appends.
- Single complete turn (user msg + assistant flurry +
  turn_duration + 15s idle) — one curation doc appears with
  a `# Source Chunk:` per indexed chunk in the turn that
  cleared the gate.
- User-record cancellation — turn_duration arms the timer; a
  user record arriving within `activation_delay` cancels it;
  no curation doc is written; `pendingChunks` carries forward.
- Multi-turn accumulation — three quick exchanges emit one
  curation doc after the final idle, covering chunks from all
  three turns.
- `min_similarity` threshold — per-section drop when the top
  recalled chunk's score is below the threshold.
- `chunks_per_dm` cap — per-section top-K cap honored.
- Mark-on-write check — after a curation doc is written, the
  inline tags on every candidate chunk have RD records for the
  originating session.
- Source filter (whitelist) — non-whitelisted sources don't
  arm the timer.
- Non-`chat-jsonl` source — never arms the timer.
- Cold start — no curation docs on watcher startup despite
  existing JSONL chunks.
- Live config reload — flipping `enabled` or `activation_delay`
  takes effect on the next turn boundary, no restart.
- Result builder open-on-first-call — `surface` then `close`
  emits a result doc; `close` alone (no prior items) emits
  no result doc but still removes the curation doc and
  appends a `silent-close` monitor log line.
- Nonce roundtrip — assistant reserves nonce, embeds in Task
  description, agent passes it to `close`, `close` locates
  the matching `.meta.json` and reads tokens from the paired
  JSONL.
- RJ counter increment — three `reject-derived` calls on the
  same `(chunk, tag)` produce a single RJ record with counter
  `3`.
- `reject_propose_ceiling = 2`, counter = 2 — propose pass
  suppresses the candidate; counter = 1 — propose pass surfaces.
- `reject_mention_ceiling = 5`, counter = 5 — assistant's
  "mention rejected" path is silent for this record.
- Agent allowlist — Haiku attempting `Read tmp://...` triggers
  the guard's denial; the denial stderr names `ark fetch` as
  the correct path; the agent's next call uses `ark fetch`.
- Fumble Log — agent emits a malformed `surface` flag set;
  parse failure is appended to the Fumble Log; the fire still
  completes (the malformed call is rejected by the CLI; the
  agent continues).
- `ark connections clean -all` — wipes RJ records (v2 format)
  in addition to RC, RD, RF, and tmp:// recall docs.

## Sequencing

Depends on (all landed):

- `ark connections recall` substrate ([recall.md](recall.md)).
- Discussed-tags storage ([discussed-tags.md](discussed-tags.md)).
- Derived-tag proposals (`--propose`,
  [derived-tags.md](derived-tags.md)).
- Recall watcher v1 (R2687–R2741).
- v1.5 substrate tweaks: `min_propose_similarity` floor,
  parenthesized cosine scores, `KeepTagless: true`, 30-byte
  paragraph floor, `ark connections clean` (R2742–R2746).

Lands in this pass:

- Global fire counter (monotonic per `ark serve` run).
- Watcher writes curation doc via Go-internal builder
  (replaces v1's `tmp://ARK-RECALL/dm-*` write).
- `ark connections recall reserve-nonce` (in-memory monotonic
  counter).
- `ark connections recall surface | recommend | close` CLI
  thin-wrappers. `surface` and `recommend` implicitly open the
  result-doc builder; `close --nonce <N>` handles "write
  result doc / remove curation / discover subagent JSONL /
  append monitor log."
- `~/.ark/monitoring/recall.jsonl` log file format + writer.
- Subagent JSONL discovery routine (cwd → encoded project dir;
  `$CLAUDE_CODE_SESSION_ID` → parent session UUID; scan
  `*.meta.json` for nonce; sum tokens from paired
  `agent-*.jsonl`).
- RJ value extension (varint counter + 8-byte nanos).
- `reject_propose_ceiling` and `reject_mention_ceiling`
  `ark.toml` knobs + readers in propose pass and assistant
  flow.
- `.claude/agents/ark-recall-agent.md` definition (Haiku, ark
  fetch only, no Read).
- PreToolUse guard script for fumble-onboarding the recall
  agent.
- `~/.ark/skills/ark-recall.md` skill file.
- Fumble Log for parse failures (silent ride-along).

Independent of:

- Tag Forge UI (ARK-STATE items 5, 7).
- Find-connections turbo (ARK-STATE item 6).
- Phase 2 / Luhmann orchestrator (ARK-STATE item 2; see
  `.scratch/SIMPLE-RECALL.md` for the Phase 2 design).
- Monitor CLI surface (`ark monitor status | recent | pause |
  resume`); the log writer lands here, the reader is a later
  pass.
- Tag axes (`.scratch/TAG-AXES.md`).
