# Simple Recall Watcher

Language: Go. Environment: built-in subsystem of `ark serve`,
configured via the `[recall]` section in `ark.toml`. No CLI flag
or sidecar process — the watcher lives inside the server.

Simple Recall is the **no-AI v1** of ambient recall. It watches
Claude Code JSONL conversations as they grow, runs the existing
chunk-similarity substrate against the indexed chunks of each
completed turn, and DMs the per-chunk results back to the
originating session. No language model in the loop. The
receiving agent (the Claude session at the other end) decides
what, if anything, to surface to the user.

The agent-mediated layer that adds LLM relevance filtering and
definition-class tag proposals is a deferred follow-up; see
ARK-STATE item 10 and the `[recall].agent_cmd` reservation
below.

## Why no-AI first

Three of the open questions that surrounded the agent-mediated
design disappear by construction when no agent runs inside the
watcher:

- **Process lifecycle.** `ark serve` already runs; the watcher
  is a tracked subsystem inside it. No new process to manage,
  no separate daemon to keep alive.
- **Target-session discovery.** The JSONL filename *is* the
  session UUID. No env-var dance, no `ark whoami`, no handshake.
- **Multi-tenancy.** One server per machine watches every
  session under `~/.claude/projects/`. Per-machine isolation
  falls out for free.

Failure mode reduces to standard server-side error reporting
(watcher errors land where scanner errors do today). Compaction
of agent working context becomes moot — there is no agent
working context.

The substrate is already there: `ark connections recall
--session SID --propose` does the work. The watcher is plumbing
between the indexer's append callback and an in-process
`ark message dm` path, driven by a turn-boundary detector.

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
  ├─ for each chunkID in pendingChunks[session]:
  │     Recall(input={ChunkID: cid}, Session, Propose,
  │            KeepTagless: true) → top-K
  │     - KeepTagless: ambient recall surfaces persona files,
  │       design docs, prose — tagless does not mean uninteresting
  │
  ├─ aggregate into one tmp:// DM:
  │     - one section per input chunk
  │     - section header = input excerpt + chunkID
  │     - section body = the substrate stencil for that chunk's
  │       top-K recalled chunks
  │
  ├─ DM via in-process composeDM
  │     → tmp://ARK-RECALL/dm-<session>
  │
  ├─ mark-on-send: RD records for inline + ext tags on every
  │     surfaced chunk across every section
  │
  └─ clear pendingChunks[session], reset state
```

The receiving Claude Code session is expected to be running
`ark listen --session <self>` (or an equivalent subscriber) to
pull DMs. The watcher does not check for active subscribers; if
nobody is listening, the DM remains in tmp:// memory and is
visible to a later `ark search @dm: <self>`. Listener
bootstrap is out of scope for the watcher.

## Configuration — `[recall]` in `ark.toml`

`[recall]` is the only control surface. Recall is a per-corpus
property; ark.toml gates it. Knobs:

| Key                  | Default | Meaning                                                                                                                                                          |
|----------------------|---------|------------------------------------------------------------------------------------------------------------------------------------------------------------------|
| `enabled`            | `false` | Master switch. `false` disables the watcher entirely; `true` enables it for all Claude-JSONL sources (unless `sources` narrows the set).                         |
| `propose`            | `true`  | Pass `--propose` to the recall substrate so RC records accrue on every per-chunk Recall call. Set to `false` for the rare no-curation variant.                   |
| `min_similarity`     | `0.65`  | Per-input hard filter. For each input chunk's Recall result, drop sections whose top recalled chunk's aggregate similarity is below this. Empty sections aren't emitted. |
| `activation_delay`   | `15`    | Seconds the watcher waits after seeing a `turn_duration` record before firing the recall pass. A user record arriving inside this window cancels the firing entirely. |
| `chunks_per_dm`      | `5`     | Per-input top-K cap. Each section in the DM body lists at most this many recalled chunks.                                                                        |
| `sources`            | `[]`    | Optional whitelist of source root directories (matching `Source.Dir` in `ark.toml`). When empty, the watcher autodetects (any source whose strategy is `chat-jsonl`). |
| `discussed_ttl`      | (existing) | Inherited from the existing `[recall]` knob. Time-to-live for RD records that suppress re-surfacing of recently-discussed tags.                              |
| `agent_cmd`          | (reserved) | Reserved for the deferred agent layer (ARK-STATE item 10). When set, an LLM post-processor runs between substrate output and DM emission.                    |

`ark.toml` is the live configuration surface — `ark serve`
reads `[recall]` on startup and on the existing config-reload
path. The watcher pulls config from `db.Config().Recall` on
each pass, so toggling `enabled` or any other knob takes effect
on the next turn boundary without a restart.

## Trigger semantics

The watcher hooks into `indexer.executeRefresh`'s isAppend
branch. The indexer hands the watcher `(path, strategy, newBytes,
added)` for each committed append:

- **Source qualification.** A source qualifies when its
  chunker strategy is `chat-jsonl` and (if `sources` is
  non-empty) the chunk's source root matches an entry in the
  whitelist.
- **Accumulation.** Every `added` chunkID gets appended to
  the per-session `pendingChunks` slice. The watcher does not
  fire per chunk; chunks just pile up.
- **Trigger arming.** The watcher scans `newBytes` line-by-line
  for the assistant's turn-end signal:
  - A line whose top-level type is `"system"` with
    `"subtype":"turn_duration"` is a turn-end marker. Seeing
    one arms the `pendingTimer` for `activation_delay` seconds.
- **Trigger cancellation.** A line whose top-level type is
  `"user"` cancels any currently-armed `pendingTimer`. The
  accumulated `pendingChunks` are *not* cleared — they remain
  pending and roll into the next fire. The reasoning: when the
  user is actively engaged, recall has nothing to add; when the
  user eventually pauses, the recall pass spans the full engaged
  exchange.
- **Fire on expiry.** When the timer expires, the watcher
  processes everything currently in `pendingChunks[session]`.
- **Cold start.** Go-forward only. No backfill of pre-existing
  JSONL chunks on watcher startup.
- **Self-exclusion.** Inherited from the recall substrate
  (R2645).

The per-line scan uses lightweight JSON parsing — each JSONL
line is a self-contained JSON object, so the watcher parses
one line at a time and inspects the top-level `type` and
`subtype` fields. Substring scanning is unreliable because
`"type":"user"` substrings can appear inside other fields
(e.g. `"userType":"external"`).

## DM body shape

The watcher emits each DM through the shared in-process
compose function (see [messaging.md](messaging.md), `dm`
subcommand). The compose function owns the head-of-chunk tag
block; the watcher supplies recipient, subject, sender
identity, and body:

| Compose input    | Watcher value                                       |
|------------------|-----------------------------------------------------|
| recipient        | `<originating-session-uuid>`                        |
| subject          | `recall`                                            |
| sender (service) | `ARK-RECALL`                                        |
| body             | `@ark-recall-fire` line + instructions + per-input sections |

Each emitted DM appears as one chunk under
`tmp://ARK-RECALL/dm-<session>` and looks roughly like:

```
@dm: <originating-session-uuid>: recall
@from-service: ARK-RECALL
@ark-recall-fire: <turn_duration chunkID or timestamp>

## What this is
...
## What to do with it
...

## Recalled for paragraph
@source-chunk: <jsonl-chunkID-A>

> <paragraph excerpt — first ~200 bytes>

### Recalled chunks

- @chunk-id: ...
  ...

## Recalled for paragraph
@source-chunk: <jsonl-chunkID-A>

> <next paragraph from the same chunk>

### Recalled chunks

- @chunk-id: ...
  ...

## Recalled for paragraph
@source-chunk: <jsonl-chunkID-B>

> ...
```

- `@ark-recall-fire` records the fire-time identifier so the
  receiving agent can correlate this DM with the turn that
  triggered it. The value is the chunkID of the
  `turn_duration` record (when one was indexed) or a
  Unix-nanosecond timestamp (when it wasn't — see
  R2702-equivalent note in the requirements).
- The instruction block is the same crank-handle as v1 (see
  R2703-equivalent): "default to silence" plus the derived-tag
  candidate-handling guidance.
- Each input chunk that produces at least one recalled chunk
  above `min_similarity` gets its own `## Recalled for chunk
  <ID>` section. Sections whose top recalled chunk is below
  the threshold are dropped. If no sections survive, no DM is
  emitted.
- The section's blockquoted excerpt is the *input* chunk's
  text (capped at ~200 chars), giving the agent immediate
  context for what triggered each recall section.
- The `### Recalled chunks` body within each section uses the
  existing `ark connections recall` markdown stencil — same
  per-chunk shape as the CLI surface.

## Quality bar

Three defense layers in order of cost, cheapest first:

### Layer 1 — `@dm` subject line

Pre-body triage. The watcher emits `@dm: <session>: recall`
so the receiving agent sees `## @dm: <session>: recall` as
the message heading via `ark listen`'s output format. An agent
in fast-flow mode can pattern-match the `recall` subject and
skip the body entirely — sub-token cost.

### Layer 2 — per-section similarity gate

`min_similarity` is applied per input chunk's Recall result.
A section is emitted only when its top recalled chunk's score
clears the threshold. If no section survives, no DM is emitted
— silent skip. (Turn-boundary firing naturally rate-limits
DMs, so a separate cooldown knob is unnecessary.)

### Layer 3 — crank-handle bias-to-silence

The DM body's instruction block opens with "default to silence"
and explicitly grants the agent permission to drop. Modeled on
`ark-messenger`'s prompt discipline. The "no reply needed"
line is load-bearing.

### Layer 4 — feedback-driven calibration (instrument now,
calibrate later)

The receiving agent appends an action tag to the DM doc on
the way out:

- `@ark-recall-acted: surfaced` — surfaced to user
- `@ark-recall-acted: dropped` — read and dismissed
- `@ark-recall-acted: skipped` — Layer-1 subject-line skip
- (no tag at all = never read; agent crashed or DM expired)

V1 emits the logging path so per-session drop/skip/surface
rates are observable. Auto-calibration (auto-ratchet of
`min_similarity` when drop+skip stays above some threshold
over a sliding window) is deferred until the logs tell us
where the threshold belongs.

## Discussed-tag marking policy

The watcher writes RD records for the inline and ext-routed
tags on every chunk surfaced in *any* section of the DM,
scoped to the originating session. Two rules:

- **Mark on send, not on action.** RD records are written
  when the watcher composes the DM, not when the receiving
  agent acts on it. Whether the agent surfaced or dropped, the
  tag-value has been "considered" for this session. Avoids
  re-DMing the same chunks until the `discussed_ttl` elapses.
- **Don't mark RC-derived candidates.** Derived tags listed
  in `@chunk-proposed-tags` within any section are proposals
  the agent has not acted on. Re-surfacing them in future
  passes is correct (the RC tally counter is the natural
  priority signal). The agent's accept/reject action writes
  its own state (V or RJ); RD is for "tag-value was visible
  on a real chunk in a DM."

## Logging and observability

The watcher emits structured logs at watcher decision points:

- `armed`     — turn_duration seen, timer armed (session,
  pending-chunk count).
- `disarmed`  — user record seen, timer cancelled.
- `fired`     — timer expired; per-section counts of recalled
  chunks, sections-emitted, sections-dropped-below-threshold,
  total RD records written.
- `recall-error` — substrate or DM-emit failure (unconditional;
  always logged).

These ride on `ark serve`'s existing log pipeline; no new
sink.

## What this watcher does not do

- **No LLM in the watcher.** Deterministic on its inputs.
- **No new tag-name invention.** RC records expose statistical
  tag *value* candidates against existing tag definitions.
  Definition-class proposals (RP/RPE/RR records, reserved
  2026-05-24 in derived-tags.md A65) belong to the agent
  layer.
- **No tag-axis filtering.** Deferred until
  `.scratch/TAG-AXES.md` earns implementation.
- **No backfill on cold start.** Go-forward only.
- **No subscriber liveness check.** The DM persists in tmp://
  memory whether or not a listener is attached.
- **No per-session state TTL.** sessionState lives forever in
  v1; minor leak when Claude Code sessions close. Worth a
  follow-up but not a blocker.

## Test strategy

- Watcher disabled (`enabled = false`) — no DM is emitted
  regardless of JSONL appends.
- Single complete turn (user msg + assistant flurry +
  turn_duration + 15s idle) — one DM appears with a section
  per indexed chunk in the turn.
- User-record cancellation — turn_duration arms the timer; a
  user record arriving within `activation_delay` cancels it;
  no DM is emitted; `pendingChunks` carries forward.
- Multi-turn accumulation — three quick exchanges (turn,
  user, turn, user, turn, idle) emit one DM after the final
  idle, covering chunks from all three turns.
- `min_similarity` threshold — per-section drop when the top
  recalled chunk's score is below the threshold.
- `chunks_per_dm` cap — per-section top-K cap honored.
- Mark-on-send check — after a DM, the inline tags on every
  surfaced chunk (across every section) have RD records for
  the originating session.
- Source filter (whitelist) — non-whitelisted sources don't
  arm the timer.
- Non-`chat-jsonl` source — never arms the timer.
- Cold start — no DMs on watcher startup despite existing
  JSONL chunks.
- Live config reload — flipping `enabled` or `activation_delay`
  takes effect on the next turn boundary, no restart.

## Open at spec phase — decided

- **Derived-candidate rendering in the DM body.** Reuse the
  existing `@chunk-proposed-tags: a, b, c` line from the
  recall substrate stencil. No new tag name introduced.
- **`@to-project` in the DM body.** Dropped.
- **Listener bootstrap.** Out of scope; the receiving
  Claude Code session runs `ark listen --session <self>`.
- **Re-chunking via markdown chunker.** Kept. The
  `chat-jsonl` chunker emits one chunk per content-bearing
  JSONL line, which for a long assistant response is one
  ~3KB chunk covering many topics. Feeding that whole block
  to the substrate as a single vector smears the topical
  signal across paragraphs — recall against fine-grained
  corpus content (paragraph-sized) finds weak average
  matches instead of strong per-topic matches. So the
  watcher takes each pending JSONL chunk's text, runs it
  through `microfts2.MarkdownChunker{}`, and treats each
  paragraph (≥ 50 bytes) as its own Recall input. Each
  paragraph that clears the similarity gate produces a
  `## Recalled for paragraph` section in the DM body, with
  an `@source-chunk:` tag pointing back to the JSONL chunk
  it came from. Text-input (not ChunkID-input) is used so
  the substrate embeds on the fly via the warm model —
  fresh JSONL chunks don't yet have EC records when the
  watcher fires.

## Sequencing

Depends on (all landed except the last):

- `ark connections recall` substrate ([recall.md](recall.md)).
- Discussed-tags storage ([discussed-tags.md](discussed-tags.md)).
- Derived-tag proposals (`--propose`,
  [derived-tags.md](derived-tags.md)).
- `ark message dm` exposed as an in-process Go-callable
  compose function (landed in `ark.ComposeDM` as part of the
  v1 watcher work).
- `@dm` grammar upgrade, `@from-service` tag, and
  `--from-service` flag — all anchored in
  [messaging.md](messaging.md).

Independent of:

- Tag Forge UI (ARK-STATE items 5, 7).
- Find-connections turbo (ARK-STATE item 6).
- Tag axes (`.scratch/TAG-AXES.md`).
