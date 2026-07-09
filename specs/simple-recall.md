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
- **Secretary** (Haiku, per-session). Each session's own assistant
  spawns a secretary for it via `/recall`. Its whole loop is a single
  verb — `ark connections recall next --session <S> <nonce>` — which
  subscribes value-scoped to that session's curation docs
  (`@ark-secretary-work=<S>`), dispatches the next one (blocking up to a
  ~90-second keepalive otherwise), context-gates, and **prepends the
  session's last-N conversation turns** so the secretary judges with the
  live conversation, not excerpts alone. The agent runs it in the
  foreground and loops in one continuous turn. The secretary decides which
  candidates fit the conversation well enough to surface and which tags
  are worth recommending — filtering *and* sharpening them — and writes a
  **result doc** the assistant reads.

The split keeps an LLM out of the high-frequency watcher path
while letting a cheap model curate before anything reaches the
assistant. Three of the open questions that the original
agent-in-watcher design raised disappear by construction:

- **Process lifecycle.** `ark serve` already runs; the watcher
  is a tracked subsystem inside it, with no LLM to keep alive on
  the high-frequency path. Each secretary's lifecycle (spawn, recycle
  on context-fill, respawn) is owned by the **session's own assistant**
  (via `/recall`), not a central orchestrator and not the watcher.
- **Target-session discovery.** The JSONL filename *is* the
  session UUID. No env-var dance, no handshake.
- **Multi-tenancy.** One server per machine watches every session
  under `~/.claude/projects/`; each active session runs its own
  secretary, subscribed value-scoped to its own curation docs, so
  sessions never see each other's recall. Per-session isolation falls
  out of the value-scoped subscription.

Failure modes reduce to standard server-side error reporting
(watcher errors land where scanner errors do) and standard
Task-tool failure handling (the assistant retries or drops).

The substrate is already there: `ark connections recall
--session SID --propose` does the heavy work. The watcher is
plumbing between the indexer's append callback and a Go-internal
curation-doc writer. The secretary talks to the substrate through
a thin set of result-builder CLI verbs.

> **Topology (seam 3a).** The curating agent is a **per-session
> secretary**, spawned and supervised by each session's own assistant via
> `/recall` — not a single shared daemon spawned by the Luhmann
> orchestrator. Where prose below says "the agent" or "the daemon", read
> "the per-session secretary": it subscribes **value-scoped** to its
> session's curation docs (`@ark-secretary-work=<S>`), its loop verb is
> `ark connections recall next --session <S> <nonce>`, and the server
> prepends the session's recent conversation (`[recall].context_turns`) to
> each doc it hands over. The watcher, the curation/result doc shapes, the
> builder verbs, and the subscriber-presence gates are unchanged — only the
> consuming agent's scope (one session, not all), ownership (the assistant,
> not Luhmann), and injected context changed. Full detail: the completed
> `secretary-pipeline` migration.

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
  │     - `"type":"user"`             → cancel pendingTimer[session],
  │                                     set armReady (re-enable arming)
  │     - `"subtype":"turn_duration"` → if armReady: arm pendingTimer
  │                                     for activation_delay, clear
  │                                     armReady (once per user turn)
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
  │       @ark-secretary-work: <session>
  │       @ark-recall-fire: <fire>
  │       # Source Chunk: <jsonl-chunkid>
  │       ## Candidate: ...
  │
  └─ mark-on-send: RD records for inline + ext-routed tags on
       every surfaced chunk for <session>
                                       │
                                       │ pubsub publishes the new tmp:// path
                                       ▼
                          secretary (Haiku, per-session) — spawned by
                          the session's own assistant via /recall, with
                          session <S> + nonce <N> in its prompt. Its
                          whole loop is one verb:
                                       │
                                       ▼
                          ark connections recall next --session <S> <N>
                                       │  server-side, in one verb:
                                       │   idempotent subscribe (value-scoped
                                       │   @ark-secretary-work=<S>, session
                                       │   recall-curate-<S>) → context-gate
                                       │   → pick lowest-fire pending
                                       │   curation-<S>-<F> (this session
                                       │   only) → block up to a ~90s
                                       │   keepalive if none → prepend the
                                       │   session's last-N turns (context).
                                       │   Returns inline (foreground) the doc
                                       │   content + "judge, close, run next",
                                       │   or a keepalive ("run next again");
                                       │   at the context limit, EXIT
                                       │   (status 2) and stop.
                                       ▼
                                       secretary judges (conversation injected)
                                                  │
                                                  ├─ for each candidate worth surfacing:
                                                  │   ark connections recall surface <F> \
                                                  │     -chunk N -reason "..."
                                                  │   (server resolves path:range + size)
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

The retired `agent_cmd` reservation from v1 is gone. The per-session
secretary is spawned by the session's own assistant via the Task tool
(the `/recall` skill), not by a configured command and not by the Luhmann
orchestrator; the assistant owns its secretary's spawn/respawn lifecycle.

## Trigger semantics

The watcher hooks into `indexer.executeRefresh`'s isAppend
branch. The indexer hands the watcher `(path, strategy, newBytes,
added)` for each committed append. Trigger semantics, with one
refinement over v1 — **arm once per user turn** (so the watcher
never re-triggers on its own consumers' output):

- **Source qualification.** A source qualifies when its
  chunker strategy is `chat-jsonl` and (if `sources` is
  non-empty) the chunk's source root matches an entry in the
  whitelist.
- **Activation gate.** The watcher tracks a session only while
  *both* ends of the recall pipe are subscribed: the session's secretary
  on `@ark-secretary-work=<session>` (value-scoped) and a client on
  `@ark-recall-result=<session>`. On each `OnAppend`, if either
  `SubscriberCount("ark-secretary-work", <session>)` or
  `SubscriberCount("ark-recall-result", <session>)` is zero, the
  watcher ignores the append and **drops the session's in-memory
  state** — it stops any armed `pendingTimer` and forgets the
  session (clearing its `pendingChunks`). An unsubscribed session is
  therefore never accumulated, armed, or fired; it costs nothing.
- **Accumulation.** Every `added` chunkID gets appended to
  the per-session `pendingChunks` slice. The watcher does not
  fire per chunk; chunks pile up.
- **Trigger arming.** The watcher scans `newBytes` line-by-line
  for the assistant's turn-end signal: a line whose top-level
  type is `"system"` with `"subtype":"turn_duration"` arms the
  `pendingTimer` for `activation_delay` seconds — **but only once
  per user turn**. Arming requires `armReady` (set by a preceding
  *genuine* user record) and clears it, so a `turn_duration` with no
  intervening genuine user record (an agent-only turn — e.g. the
  assistant surfacing recall, woken by a notification) does **not**
  re-arm. This is what stops the recall ping-pong: the watcher never
  fires on a turn that no human initiated.
- **Genuine user records only.** A `type:"user"` line counts as a
  user turn only when it's a real human message: `message.content` is
  a JSON string (tool-results are arrays) **and** it carries the human
  origin marker `origin.kind == "human"`. Absence of an origin is *not*
  a genuine signal — tool-results and local-command caveats also lack
  one — so detection keys on the positive marker, not its absence.
  Injected lines (e.g. background-task completions) carry a different
  `origin.kind` like `"task-notification"`. Anthropic does not publish
  this format, so the rule is deliberately conservative: a positive
  allowlist of the one known human marker, with the `-vv` `turn_duration
  ignored` log as the drift tripwire if the marker ever changes. Tool-
  results and notification wake-lines are *not* user turns. Without
  this, a consumer surfacing recall — woken by a `type:"user"`
  notification — would re-arm the watcher and cascade.
- **Trigger cancellation.** A genuine user record cancels any
  currently-armed `pendingTimer` **and sets `armReady`, re-enabling
  arming for the next `turn_duration`**. The accumulated
  `pendingChunks` are *not* cleared — they remain pending and roll
  into the next fire.
- **Fire on expiry.** When the timer expires, the watcher
  allocates the next global fire number, processes everything
  currently in `pendingChunks[session]`, and clears the slice.
- **Cold start / (re)activation.** Go-forward only. No backfill of
  pre-existing JSONL chunks — on watcher startup *or* when a
  session's watch (re)activates. Because only live appends drive the
  watcher and the session's state was dropped while unsubscribed,
  watching resumes at the current end of the JSONL: a session that
  runs `/recall` mid-conversation gets recall on its next turn, never
  a replay of the prior transcript.
- **Self-exclusion.** The substrate already drops the exact input
  chunk. On top of that, when building the curation doc the watcher
  classifies each surviving candidate by source: a candidate in the
  **originating session's own JSONL** is marked *tag-only* — kept for
  tag suggestions but never surfaced, since that conversation is
  already in the reader's live context (the model's attention handles
  it better than a flat similarity hit). Candidates in **another
  session's JSONL** ("remember when we talked about…") or in
  **external files** surface normally. Tag suggestions stay available
  for all three — conversation chunks are taggable via external
  (`@ext`) tags, which is how past conversations enter the hypergraph
  (see Result doc shape).

The per-line scan uses lightweight JSON parsing — each JSONL
line is a self-contained JSON object, so the watcher parses
one line at a time and inspects the top-level `type` and
`subtype` fields.

## Fire counter

The fire number is a **per-session** monotonic counter held in memory
(`map[sessionID]uint64`), allocated by the watcher at the moment
`pendingTimer` expires. On the first fire for a session after an
`ark serve` start the counter is seeded by scanning
`~/.ark/recall-curation/` for that session's
`curation-<session>-<fire>.md` files and taking `max(fire)+2` (or `1`
if none); thereafter it increments in memory. The `+2` skips a
possibly-allocated-but-unmaterialized in-flight doc (one secretary ⇒
lag ≤ 1); the in-memory hold — not a per-allocation dir recompute —
closes the allocation→materialization race a constant offset cannot.
The surviving materialized files are the high-water record (Alibi
Stamp), so a restart or an `ark rebuild` never re-mints a number a
surviving file occupies; a cleanly-closed fire leaves no file, so
reusing its number is safe.

The composite `<session>-<fire>` token ties the curation doc and the
result doc together for that turn — globally unique even though the
fire integer is only per-session — and is the cookie the agent passes
to `surface` / `recommend` / `close`.

The tmp:// curation/result docs don't survive a server restart
(`tmp://` is per-process); the materialized curation file does, and
the per-session dir-seed above is what keeps a reset counter from
clobbering it.

## Curation doc shape

Path: `tmp://ARK-RECALL/curation-<session>-<fire>`.

The `<session>` segment is load-bearing: the fire counter is now
per-session (R2901), so the fire integer alone is not unique across
sessions — `<session>-<fire>` is what disambiguates the doc and keys
the in-flight builder map.

Header tags (one per line, no blank line between them and the
first body section):

```
@ark-secretary-work: <originating-session-uuid>
@ark-recall-fire: <fire>
```

Body — one `# Source Chunk:` H1 per paragraph that cleared
`min_similarity`, each followed by 1..K `## Candidate:` H2s
ordered by score descending:

```
# Source: <path>:<range>

> <paragraph excerpt — first ~200 bytes, UTF-8-safe truncation>

## Candidate: <path>:<range> (<size>)

- score: <0.NN>
- tags: <comma-separated bare tagnames>
- proposed-tags: <name> (<0.NN>), <name> (<0.NN>), ...
- tag-only: true        (present only for own-session candidates)

```
<~500-char excerpt of chunk content>
```

## Candidate: <path>:<range> (<size>)
- ...
```

Notes:

- The paragraph excerpt is `> `-quoted markdown so the agent
  reading the doc as plain text sees what triggered each
  section.
- `tags` lists the candidate chunk's inline and ext-routed
  tag *names* (bare). Values aren't shown — the curation doc
  carries names only, and the sealed daemon works from what
  `next` hands it inline; it has no path to fetch more.
- `proposed-tags` lists derived-tag candidates surviving
  `min_propose_similarity`, each rendered with the
  parenthesized chunk-EC ↔ tag-ED cosine score from the
  propose pass.
- The content excerpt uses a fenced block (not blockquoted) so
  the agent can distinguish it from the source-paragraph
  excerpt without ambiguity.
- `tag-only: true` appears only on a candidate whose source is the
  originating session's own JSONL. The daemon may `recommend` a tag
  for such a candidate but must **not** `surface` it — it's already
  in the reader's context. Candidates without the line may be
  surfaced. The daemon's crank-handle (the prose `next` appends)
  restates this rule.

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
## Surface: <path>:<range> (<size>)

reason: <one-line justification>

## Recommend: @<tag>[:<value>] on <path>:<range>

reason: <one-line justification>
```

Both H2 kinds name the chunk by its `<path>:<range>` locator
(mirroring the curation-doc `## Candidate:` line, R2898) — never a
chunkid, which is volatile across a reindex; Surface additionally
carries the chunk's byte size in parentheses (e.g. `(33K)`, `(500b)`,
`(1.2M)`). The locator is inline so the consuming assistant can prune
by file — dropping surfaces for code it already has in context,
keeping the ones that pull in out-of-view code — without resolving
every chunk first. A **Recommend** can reference a chunk that was
never surfaced, so it carries its own locator for the same judgment.
The assistant still resolves full content on demand via
`ark chunks <path>:<range> [-before N] [-after N]` (which accepts a
locator or a chunkid); the size lets it glance at fetch cost before
drilling. The result doc stays a "scan-and-prune, drill on demand"
surface.

The assistant has final say on whether to surface a chunk to
the user or to ask the user about a tag recommendation. The
agent's role is to filter and rank; the assistant's role is to
present.

**Source chunks are never surfaced.** A `# Source:` locator is the
conversation paragraph that *triggered* a section — it lives in the
reader's own session and is already in context. The agent surfaces
the `## Candidate:` locators beneath it, never the source one.
`surface` enforces this deterministically: `SurfaceItem` reads the
path from the `-loc` it is given and rejects any locator whose path
resolves to the fire's own session, and the rejection names the fix
(pass a `## Candidate:` locator, not the source one) — doubling as
fumble-onboarding. `recommend` is **not** gated: tagging an
own-session chunk is the intended hypergraph path. The recall-agent
skill and the curation crank-handle reinforce this by naming the
surfaceable locator `<CANDIDATE-PATH:RANGE>` (matching the
`surface`/`recommend` call verbatim) and marking the `# Source:`
locator as never-surface — two locators in view, one named "do this,"
the other "never this."

**External tags for conversation chunks.** A `## Surface:` or
`## Recommend:` line can point at a chunk in a chat-JSONL file — a
past conversation, now within recall's reach. Those files are
append-only source of truth: a tag on them must be applied as an
**external (`@ext`) tag**, never an inline edit. When a result doc
references any chat-JSONL chunk, the consumer crank-handle (the prose
`recall listen` appends to its reply) carries this reminder. The
internal-vs-`@ext` choice is the assistant's, made per chunk from its
`<path>` — and tagging conversation chunks this way is exactly how
they enter the hypergraph.

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
b.Section(sourcePath, sourceRange, paragraph)    // # Source: H1
b.Candidate(path, rangeLabel, byteSize, score,
            tagNames, proposedTagsWithScores,
            contentExcerpt, tagOnly)             // ## Candidate: H2
b.Close()                                        // writes tmp:// doc
```

The watcher resolves the source chunk's path:range at build time; no
chunkid appears anywhere in the curation doc (R2898).

**Nonce reservation — CLI, called by the orchestrator:**

```
ark connections recall reserve-nonce
```

Returns a small monotonic integer (`1`, `2`, ...) per `ark
serve` run. In-memory counter; resets on restart. The Luhmann
orchestrator calls this once per generation, before spawning the
daemon via Task; the integer is embedded in the Task
`description` field as the literal string `ark-recall lotto-tube
loop nonce <N>` (see *Nonce-in-description format* below) and
passed into the agent's prompt body.

**Result-doc builder — CLI, called by the recall agent. The
`<F>` = `<session>-<fire>` token is the cookie that ties calls
together (R2901); the crank-handle emits it and the agent pastes it
opaque.**

```
ark connections recall surface <F> -loc <path>:<range> -reason "..."
ark connections recall recommend <F> -loc <path>:<range> -tag @t[:v] \
                                     -reason "..."
ark connections recall close <F> --nonce <N> [-preserve-curation]
```

`surface` and `recommend` take the candidate's `<path>:<range>`
locator (R2900); the server resolves content and (for `surface`) byte
size via `ChunkText(path, range)` and writes the locator into the
result doc (R2899). No chunkid crosses the wire.

Discipline:

- One item per `surface` / `recommend` call. Repeated `-loc`
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

- **Watcher → activation gate.** The watcher processes a session
  only while *both* the session's secretary
  (`@ark-secretary-work=<session>`, value-scoped) and a client
  (`@ark-recall-result=<session>`) are subscribed — both ends of the
  pipe. The gate is applied primarily at the
  watch-activation point (`OnAppend`, see Trigger semantics): an
  append for a session missing either subscription is ignored and
  the session's in-memory state is dropped, so no curation doc is
  ever produced for it. `fire()` re-checks both counts as a
  backstop for the consumer dropping during `activation_delay`; on
  a zero count it clears `pendingChunks` and appends a
  `recall.jsonl` record with `outcome: "no-subscriber"`.
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
`.meta.json` sidecar. The orchestrator constructs the description
as a string containing the substring `nonce <N>`:

```
ark-recall lotto-tube loop nonce <N>
```

(No fire in the description — one daemon generation spans many
fires.) `close` / `context` discover the agent's JSONL by scanning
`<subagents-dir>/*.meta.json` for descriptions whose body contains
`nonce <N>` as a substring. Trivially `strings.Contains`
parseable; no JSON parsing of the description field is performed.

The same nonce is also passed in the agent's **prompt** (`Nonce:
<N>`), because a sealed subagent cannot read its own description —
the prompt copy is what the agent types into its `subscribe`,
`close`, and `context` calls.

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
same value `ark connections recall context` returns). For the
long-running daemon it's the load-bearing telemetry: context
creep across fires until `next`'s gate recycles the generation at
`[luhmann].context_limit`.

`outcome` is one of `result-emitted`, `silent-close`,
`no-subscriber`, or `error`. `no-subscriber` covers both gate
points: the watcher's pre-curation skip and `close`'s
pre-result skip (see the *Subscriber-presence gate* section
above). `surfaced` and `recommended` are counts of items
added before `close`. The format is intentionally append-only
and forward-compatible; future fields slot in at the end.

## Recall secretary

The agent definition lives at
`.claude/agents/ark-recall-agent.md`. It is a **per-session secretary**,
spawned by the session's own assistant via `/recall` — one secretary per
active session, recycled across context generations, not one shared daemon
and not per-fire. Shape mirrors `ark-messenger` / `ark-searcher`:

- Model: Haiku 4.5.
- `memory: local` so MEMORY.md does not leak into the agent.
- No bootstrap skill. The loop is small enough to live in the
  agent persona: the body says "run `ark connections recall next
  --session <S> <your nonce>` and do what its output tells you,"
  looping until `next` returns the exit directive. The surfacing bar —
  when a candidate genuinely fits the live conversation vs. merely
  resembling the source paragraph — lives in the persona too, since that
  judgment is the secretary's core identity. `recall-loop.md` is retired as the
  loop driver; `~/.ark/skills/ark-recall.md` remains the
  standalone one-shot work skill (the preserved capability),
  untouched. The fumble-onboarding pattern still applies: every
  denied tool call carries `ark connections recall next <N>` as
  the runway, and a Fumble Log (silent ride-along) records parse
  failures so we can tighten the curation-doc format over time.

**Spawn contract.** The session's assistant reserves a nonce `N`, then
spawns the secretary with the nonce in two places: the Task
`description` (`ark-recall secretary loop nonce <N>`, for JSONL
discovery) and the prompt (`Start the recall secretary loop now.
Session: <S>. Nonce: <N>.`). The agent cannot read its own description,
so the prompt copy is the one it passes to its `next --session <S> <N>`
and `close` calls. The server-side context-gate
(`[luhmann].context_limit`) recycles the secretary; on its clean exit the
assistant respawns it with a fresh nonce.

**Tool allowlist (hermetic seal, enforced by the PreToolUse
guard script):**

- `Bash` permitted for the four loop verbs:
  - `ark connections recall next <N>` (the loop driver — subscribe,
    block, fire-order, fetch, and context-gate all live inside it)
  - `ark connections recall surface <F> ...`
  - `ark connections recall recommend <F> ...`
  - `ark connections recall close <F> --nonce <N> [-preserve-curation]`
  — plus `cat <file>` (single arg, no chaining/redirection) so the
  agent can read the backgrounded `next`'s output file.
- `Read`, `Edit`, `Write`, network tools, and every other `ark`
  verb — `subscribe`, `listen`, `files`, `fetch`, `context` — are
  denied as a class, because `next` absorbs them. Each denial's
  stderr is the runway: it points back at `ark connections recall
  next <N>`.

**Persona briefing.** The secretary is a curator, not a synthesizer.
Its job per fire: read the injected recent conversation, then decide
which candidates fit *what's being discussed* well enough to surface,
and which tags are worth recommending — filtering *and* sharpening them
(the bar is discrimination, not mere accuracy; a generic tag that fits
everything sharpens nothing). Defaults to silence — better to close with
no items than to spam an assistant.

**Agent does not write RJ records.** Permanent rejection is a
user-relayed decision. When an assistant surfaces a tag
recommendation to its user and the user rejects it, the
*assistant* calls `ark connections recall reject-derived` (the
existing path) to write the RJ. The agent's role is filter and
recommend; the durable rejection state stays under user
control.

**The loop.** The secretary's entire loop is `ark connections recall
next --session <S> <N>`. On its first call `next` idempotently
establishes the **value-scoped** `@ark-secretary-work=<S>` subscription
under session `recall-curate-<S>` — keyed on the durable session, not
the volatile nonce, so a restart can't recycle the key and two
generations share it (R2902); thereafter it returns the lowest-fire pending
curation doc **for session S** (whose result subscriber is the assistant
that spawned the secretary), blocking up to a **~90-second keepalive**
when none is dispatchable (a doc with no result subscriber is left to
pile up, never dispatched — defer, never discard). On the keepalive
timeout it returns a "run `next` again" directive; at the context limit
it returns an exit directive. The secretary runs `next` in the
**foreground** and loops in one continuous turn — the ~90s window keeps
`next` returning inline before the harness's foreground auto-background
threshold (~120s), so the subagent never ends its turn mid-loop and only
"completes" on a true context-limit exit (no per-cycle beats for the
spawning assistant to misread as an exit). The secretary never runs
`subscribe`, `listen`, `fetch`, or `context`; `next` carries them all. It
derives each fire from the doc `next` hands it; it never allocates a fire.

**Riding out a server bounce.** The `next` CLI treats an `ark serve`
restart as a wait, not an error (R2903): on a cold dial it redials with
bounded backoff; on a mid-block drop or budget exhaustion it returns a
keepalive (exit 0) — never a fatal error and never the context-limit
exit — so the loop re-invokes `next` and rides out the bounce across
iterations, never hanging and always within the foreground window. The
durable `recall-curate-<S>` subscription key (R2902) and the per-session
dir-seeded fire counter (R2901) are what make that reconnection land
safely: the resumed subscription can't collide with a recycled nonce,
and a reset counter can't clobber a surviving curation file. This is
Stubborn Plumbing applied to the recall loop.

**Doc delivery — by file, not inline (R2896/R2897).** When `next`
dispatches a doc it does **not** print the doc to stdout — a large doc
(tens of KB) overflows the subagent's foreground-Bash output, which the
harness truncates to a preview + spill file, and the `cat` fallback just
re-overflows (the failure mode `/tmp/log.txt` captured). Instead `next`
writes the (conversation-injected) doc to a real file
`~/.ark/recall-curation/curation-<S>-<F>.md` and returns a **short
pointer** + the crank-handle. The secretary's `tools:` include `Read`, and
the guard permits the Read tool **only** on that path
(`.../recall-curation/curation-*.md`) — one keyhole, everything else still
denied. The Read tool paginates, so it opens any doc size; `close` deletes
the materialized file alongside the tmp:// doc. The `tmp://ARK-RECALL/
curation-*` doc stays the canonical store; the file is the Readable
materialization.

## Assistant subscriptions — `recall listen` and `/recall`

Recall is **opt-in per session**. A user-facing assistant consumes
results by running one batteries-included verb — the consumer-side mirror
of the secretary's `next`:

```
ark connections recall listen --session <claude-code-session-uuid> [--ambient]
```

`listen` carries the whole consumer loop:

- **Subscribe (idempotent), per capability.** On first call `listen`
  establishes the value-scoped **bloodhound** subscription
  `@ark-bloodhound-result=<session>` — findings, the base (see
  [bloodhound.md](bloodhound.md)). With **`--ambient`** it *also* establishes
  `@ark-recall-result=<session>` (ambient surfaces) — and **that** recall-result
  subscription is the **ambient opt-in** the watcher's curation gate keys on.
  Later calls are no-ops; the assistant never runs `ark subscribe` itself.
- **Block until a result.** It blocks until at least one result doc is published
  for the session — a `tmp://ARK-BLOODHOUND/finding-<session>-<B>` (a
  `## Finding:`) or, with `--ambient`, a `tmp://ARK-RECALL/result-<session>-<fire>`
  (`## Surface:` / `## Recommend:` items) — and returns its content,
  path-stamped. Unlike the daemon's
  `next` there is **no keepalive and no context-gate**: the assistant
  runs `listen` backgrounded and should wake only on a real result, not
  on idle ticks (a keepalive would bloat the assistant's context the way
  per-cycle beats bloat the orchestrator's). A long quiet stretch just
  blocks; cancellation (request/session end) is the only non-result
  return.
- **Crank-handle.** The body ends with prose: "this is ambient recall —
  decide what genuinely helps the user (you have final say; skip stale /
  off-topic), then run `recall listen --session <session>` again."

The assistant's downstream judgment is unchanged: it consults the RJ
counter for each `## Recommend:` (R2765 / R2766) and, on a user
rejection, calls `ark connections recall reject-derived`. `listen` does
**not** filter recommends itself — the RJ decision stays the assistant's.

**Opt-in via the subscriber-gate.** Until the assistant runs `listen --ambient`
(no subscriber for `@ark-recall-result=<session>`), the **watcher's ambient
gate** doesn't fire for the session — no curation doc is ever produced, so the
substrate cost is never paid. (Plain `listen` opts into the bloodhound, not
ambient.) (The daemon's
`next` dispatch gate is the downstream backstop: if a consumer drops
between a doc being written and dispatched, that doc is left pending —
deferred, not discarded.) So a session gets ambient recall exactly when
(and only when) its assistant opts in, and recall begins at the current
end of its JSONL — never a replay of the prior transcript.

**The `/recall` skill** turns ambient on (level 4) atop the bloodhound. It
**requires `/bloodhound`** — which spawns the session's secretary (a background
Task, `subagent_type: ark-recall-agent`, session + nonce in the prompt,
respawned on context-limit exit) and runs the base `listen` — then runs `ark
connections recall listen --ambient` **backgrounded** so the
`@ark-recall-result` subscription is established and the watcher fires ambient,
surfacing what helps the user on each completion and relaunching the `listen`.
Idempotent: if `/bloodhound` already spawned the secretary, `/recall` reuses it
and only adds the ambient subscription. If the user opts into neither, both stay
dormant for that session.
The notification-woken surfacing turn does not arm the watcher — its
`origin.kind` is `task-notification`, not a genuine user message (R2732)
— so the consumer loop does not feed itself.

The assistant owns **both** ends for its session: via `/bloodhound` it spawns
the secretary (which establishes `@ark-secretary-work=<S>` via `next`) and runs
the base `listen` (`@ark-bloodhound-result=<S>`); via `/recall` it adds
`--ambient` (`@ark-recall-result=<S>`). These are two separate subagent/loop
roles in the one session — the secretary curates and hunts, the assistant
consumes. (`listen` absorbs subscribe + block + fetch into one verb, as `next`
does for the secretary.)

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

## Recall Judgment record (signed per-edge relevance)

The RJ record is the **Recall Judgment** edge: one signed relevance
figure per (source `@ext-judgment` tvid, target chunk). Rejection is the
negative tail of a single bidirectional axis.

- **Key:** `"RJ"` + source_tvid varint + target_chunkid varint (re-keyed
  from the old chunkid + tagname shape). The tagname is recovered from the
  source tvid, not stored.
- **Value (v3):** `signed-varint(score) + 8-byte BE unix nanos`.
  `score < 0` is net-rejected (magnitude `-score` is the rejection
  strength); `score > 0` is reinforced; `score == 0` ≡ record-absent.
  The timestamp is the most-recent adjustment.

The score is **materialized from the `@ext-judgment` line's signed
`@count`** and RJ is derived on reindex, not written directly.
`Store.RejectDerived` authors that tag via `DB.RejectExtTag`, which
creates the judgment (`@count: -1`) or decrements it. With no
reinforcement producer yet (the Recall Secretary is a later seam), a
rejection-only sequence yields scores `-1, -2, -3, …` — the magnitude
accumulates in `@count`. The reject filter reads the in-memory
`ExtMap.rejectByChunk` map, not an RJ key lookup.

**Migration:** the v2 (`varint counter + nanos`) and v3 values are
structurally indistinguishable, so there is no automatic drop —
`ark connections clean -all -checkpoint` wipes RJ and the next
reject/reinforce cycle rewrites in v3 shape.

Two readers consume the rejection magnitude (`-score`):

- The propose pass suppresses a candidate whose score is negative.
  If `reject_propose_ceiling == 0`, any net rejection suppresses;
  otherwise the candidate is suppressed once
  `magnitude >= reject_propose_ceiling`.
- The assistant's "mention rejected proposals" path stays silent for
  a pair once `magnitude >= reject_mention_ceiling`.

Between the two ceilings the assistant may say "you have N rejected
proposals worth revisiting" (count only, no specifics). Both ceilings
default to `0` (infinite); the prior behavior is preserved when the
user leaves them unset.

## Recall surface-cooldown record (RM)

The `RM` record is the per-(session, chunk) surface-cooldown signal —
an RD-family sibling keyed by chunk instead of tag-value. It records
*when a chunk was last surfaced to a session* so a deterministic floor
can suppress re-surfacing the same chunk for a window (the secretary
then spends judgment only on novel candidates).

- **Key:** `"RM"` + session-bytes + `\x00` + chunkid varint. session-bytes
  is the Claude Code session UUID (variable-length, no `\x00`); the
  `\x00` separates it from the trailing chunkid varint, mirroring RD's
  layout.
- **Value:** 8-byte big-endian unix nanoseconds — the most recent time
  this chunk was surfaced to this session. Presence means "surfaced
  before"; the timestamp drives the cooldown window.

Store API:

- `Store.MarkSurfaced(session, chunkID)` writes/overwrites the RM
  record with NOW.
- `Store.LastSurfaced(session, chunkID)` reads the timestamp; absent →
  `(0, false)`.
- `Store.PruneSurfaceCooldown(ttl)` sweeps RM entries older than `ttl`
  across all sessions; the read path also treats an entry older than
  the cooldown window as expired (lazy expiry, mirroring RD).

The cooldown window is `[recall].surface_cooldown` (Go duration, default
`"24h"`); it doubles as the RM record's lazy-expiry TTL — an entry past
the window means the chunk is off cooldown and the record is prunable.
`ark connections clean` wipes RM alongside RD (per-session recall
state).

The deterministic floor that *consumes* this record (drop a candidate
whose `(session, chunk)` is within `surface_cooldown`) is wired in the
secretary pipeline seam, not here — this seam delivers the record and
its Store API.

**Deferred — match-frequency.** The design pairs surface-cooldown with a
*match-frequency* signal (how often a chunk matches a session's
conversations → the "paint here" tag-recommend priority, the inverse of
cooldown). That is a distinct consumer; the RM value can later grow a
leading `varint(match_count)` trailer (as V records grew a tvid trailer)
when the tag-priority consumer is built. Not in this seam.

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
  inputs. The LLM lives only in the curating secretary.
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
- **Secretary lifecycle is the assistant's.** Each session's
  assistant spawns and respawns its secretary (one generation per
  spawn-to-context-fill cycle) via `/recall`; recall is no longer a
  Luhmann-orchestrator class. This doc owns the watcher, the builder
  verbs, and the secretary's per-doc work.
- **Secretary does not write RJ records.** The secretary recommends;
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
- Judgment reject parity — three `reject-derived` calls on the
  same `(chunk, tag)` walk the score to `-3` (magnitude 3).
- `reject_propose_ceiling = 2`, score = -2 — propose pass
  suppresses the candidate; score = -1 — propose pass surfaces.
- `reject_mention_ceiling = 5`, score = -5 — assistant's
  "mention rejected" path is silent for this record.
- Agent allowlist — Haiku attempting `Read`, or any non-loop
  command (`ark fetch`, `ark listen`, `ark subscribe`), triggers
  the guard's denial; the denial stderr names `ark connections
  recall next <N>` as the loop driver; the agent's next call uses
  `next`.
- Fumble Log — agent emits a malformed `surface` flag set;
  parse failure is appended to the Fumble Log; the fire still
  completes (the malformed call is rejected by the CLI; the
  agent continues).
- `ark connections clean -all -checkpoint` — wipes RJ records
  (v3 signed format) in addition to RC, RD, RF, and tmp:// recall
  docs.

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
- RJ value: signed Recall Judgment, v3 (see "Recall Judgment
  record" above; the v2 `varint counter + nanos` shape it
  superseded landed here).
- `reject_propose_ceiling` and `reject_mention_ceiling`
  `ark.toml` knobs + readers in propose pass and assistant
  flow.
- `.claude/agents/ark-recall-agent.md` definition (Haiku,
  `memory: local`).
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
