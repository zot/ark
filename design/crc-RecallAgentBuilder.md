# RecallAgentBuilder
**Requirements:** R2747, R2748, R2750, R2754, R2755, R2757, R2758, R2759, R2760, R2761, R2762, R2763, R2772, R2774, R2777, R2807, R2808, R2857, R2858, R2865, R2866, R2869, R2870, R2871, R2872, R2873, R2888, R2889, R2890, R2891, R2894, R2896, R2898, R2899, R2900, R2901, R2902, R2903, R2909, R2937, R2938, R2939, R2940, R2943, R2944, R2945, R2946, R2947, R2948, R2950, R3006

In-server state machine that owns the curation-doc and result-
doc builders for the Simple Recall v2 pipeline. Two callers
share the same builder family:

- The **watcher** (crc-RecallWatcher.md) calls the Go-internal
  `RecallCurationOpen(session, fire)` to write the curation doc
  at fire time (R2754).
- The **recall agent** (a Haiku subagent, see
  seq-recall-agent.md) shells out to four CLI verbs —
  `reserve-nonce`, `surface`, `recommend`, `close` — that route
  through this component (R2755, R2900, R2757, R2758).

Both paths produce identical doc shapes; the file is the
contract, and the same builder code emits it in both cases.

The agent-facing CLI verbs are server-proxied: they POST through
the existing `connections` subcommand wiring and the work runs
in the `ark serve` process so the in-flight `(fire, builder)`
state lives in one place. The CLI never opens the DB directly
for these verbs — `ark serve` must be running.

## Knows
- store: *Store — write actor; used to publish tmp:// docs and
  to read the recall agent's `.meta.json` / JSONL pair when
  computing the monitor log entry.
- curations: map[fireToken]*curationDoc — open curation-doc
  builders. Keyed by the composite `<session>-<fire>` token
  (per-session fire numbers aren't globally unique, R2901);
  populated by `RecallCurationOpen` and dropped on `Close()`.
- results: map[fireToken]*resultDoc — open result-doc builders.
  Keyed by the same `<session>-<fire>` token; populated lazily
  on the first `surface` or `recommend` call for that token;
  dropped on `Close` (R2901).
- nonceCounter: uint32 — in-memory monotonic counter for
  `reserve-nonce`. Resets on `ark serve` restart (R2755).
- bloodhounds: map[bhToken]*recallResultDoc — open **finding**-doc
  builders for directed search (R2943), in the `ARK-BLOODHOUND`
  namespace. Keyed by the kind-marked cookie `<session>-b<B>` so it
  never collides with a `<session>-<fire>` recall token in the maps
  above; populated lazily on first `finding`. Reuses the
  `recallResultDoc` accumulator (same one-item-per-call shape as
  results), but its items are `## Finding:` H2s with no own-session
  gate (R2944).
- bloodhoundClues: map[bhToken]string — the originating clue
  retained when the watcher's `RecallBloodhoundOpen` mints `<B>`
  (R2937), stamped into the finding doc's `## Finding:` header at
  close so the assistant correlates verbatim (R2946).
- monitorLog: *MonitorLogWriter — append-only writer over
  `~/.ark/monitoring/recall.jsonl` (R2763) and the Fumble
  Log at `~/.ark/monitoring/recall-fumbles.jsonl` (R2772).
  Owns its own file handle, mutex-protected.

## Does

### Curation builder (Go-internal)
- RecallCurationOpen(session, fire) → *RecallCurationBuilder
  (R2754). Initializes an empty in-memory doc keyed by `fire`,
  registers it in `curations[fire]`, returns the builder.
- (builder).Section(sourcePath, sourceRange, sourceParagraphText)
  appends the `# Source: <path>:<range>` H1 + `> `-blockquoted
  paragraph excerpt (UTF-8-safe ~200-byte cap) to the
  in-memory doc — no chunkid in the heading (R2898). The watcher
  resolves the source chunk's path:range at build time.
- (builder).Candidate(path, rangeLabel, byteSize,
  score, tagNames, proposedTagsWithScores, contentExcerpt,
  tagOnly) appends a `## Candidate: <path>:<range> (<size>)`
  H2 — path:range first, no chunkid (R2898) — with score, tag
  list, parenthesized proposed-tags, and a fenced ~500-char
  content excerpt to the most recent section. `byteSize` is the
  chunk's full pre-truncation length; the watcher captures it
  before applying the 500-char excerpt cap and passes it through.
  Size renders via `friendlySize` (same helper Surface uses).
  When `tagOnly`
  (the candidate is in the originating session's own JSONL,
  R2869) the H2 carries a `- tag-only: true` line — the agent may
  recommend a tag for it but must not surface it. (R2898, R2869)
- (builder).Close() writes
  `tmp://ARK-RECALL/curation-<session>-<fire>` via the write
  actor with the two head tags `@ark-secretary-work: <session>`
  and `@ark-recall-fire: <fire>` followed by the accumulated
  body (R2747, R2748). Removes `curations[fire]`.

### Nonce reservation
- ReserveNonce() uint32 — atomic-add increment of
  `nonceCounter`, returns the new value (R2755). Cheap and
  in-memory; no DB write.

### Loop driver — `next` (CLI-driven)
- RecallNext(nonce, session) — the secretary's single loop verb (R2857,
  R2858, R2888, R2889, R2891). With `session` set (the per-session
  secretary, seam 3a) it subscribes **value-scoped**
  `@ark-secretary-work=<session>` under subscription session
  `<session>` — keyed on the durable session, not `recall-<nonce>`,
  so two secretary generations across a restart share one stable,
  unique key and the `SubCount == 0` re-subscribe guard never
  no-ops a colliding subscriber (R2902, mirroring the consumer's
  session-keyed result subscription). `lowestPendingCuration`
  dispatches only that
  session's `curation-<session>-<fire>` docs, and the returned doc is
  prefixed with the session's last-`[recall].context_turns` conversation
  turns (`injectConversation` / `recentConversation`, best-effort). With
  no `session` it keeps the legacy bare-`@ark-secretary-work`, all-session
  scan (subscription session `recall-<nonce>`). It then
  checks the nonce's context fill (R2777) against
  `[luhmann].context_limit`: at or over the limit it returns an
  **exit** directive (exit status `2`). Otherwise it picks the
  lowest-fire pending `tmp://ARK-RECALL/curation-<session>-<fire>`
  doc whose session has a result subscriber
  (`pubsub.SubscriberCount("ark-recall-result", session) > 0`) —
  numeric fire ordering decided server-side — **materializes it to a
  real file** `~/.ark/recall-curation/curation-<session>-<fire>.md`
  (`writeCurationFile`, R2896) and returns a **short pointer** to that
  file as the body (exit status `0`), NOT the doc content inline — the
  large content would overflow the agent's truncating foreground-Bash
  stdout. The secretary Reads the file (R2897). Docs whose session has no result
  subscriber are skipped (left to pile up): the daemon never
  dispatches work `Close` would discard, so the subscriber-presence
  gate moves from `close` to dispatch. When none is dispatchable it
  **blocks up to a keepalive window (~90s)** via `waitForWork`, a
  select over the curate queue (`QueueChan`), the subscription-changed
  broadcast (`SubChanged` — a late result subscriber dispatches piled
  docs at once, not after the tick), the `recallNextListenWindow`
  re-check timer, and ctx; it `TouchListen`s each cycle so `Reap`
  doesn't drop the subscription. On the keepalive deadline it returns a
  **keepalive** (exit `0`, "no doc yet — run next again"). The window
  is short so the subagent's foreground `next` returns before the
  harness's foreground-Bash auto-background threshold (~120s) — a
  detached `next` would end the subagent's turn and emit a per-cycle
  "completed" the orchestrator can't tell from a real exit; inline
  return keeps it in one continuous turn that completes only on a true
  context-limit exit. Every non-exit return — doc or keepalive —
  carries crank-handle prose ending in "run next again"; only the exit
  directive stops (a uniform crank handle, no ambiguous empty). For a
  doc, the prose instructs the agent to surface and/or recommend per
  candidate but to **recommend-only** — never surface — any candidate
  marked `tag-only`, the reader's own conversation (R2870); it also
  names the surfaceable id as the `## Candidate:` chunkid and warns
  never to pass the `# Source Chunk:` id to surface/recommend (R2873).
  This bounded block supersedes 008's pure no-timeout lotto-tube.
  Lowest-fire-first keeps `Close`'s
  same-session orphan sweep (R2758) safe.
- **CLI-side redial (R2903).** The `recall next` CLI command
  (`cmd/ark/main.go`) — not the server handler above — treats an
  `ark serve` bounce as a wait condition: it reclassifies
  dial-refused (cold dial against a restarting server) and
  mid-block EOF from error to not-yet, redials with bounded
  backoff, and re-issues the request (whose subscribe is
  idempotent, R2902), so the secretary's loop never sees a failed
  call. On a cold dial it redials with bounded geometric backoff up
  to a budget (~20s); on a mid-block drop, or once the budget is
  exhausted, it returns a **keepalive** (exit 0) — never fatal,
  never the context-limit exit — so the loop re-invokes `next` and
  rides out the bounce across iterations. It never hangs and never
  reissues a fresh blocking request in the failure path, so each
  call stays under the foreground threshold. An in-flight fire
  abandoned across a bounce is not recovered; the loop re-syncs to a
  fresh doc.

### Consumer loop — `listen` (CLI-driven)
- RecallListen(session, ambient) — the consumer-side loop verb (R2865,
  R2950), the mirror of `RecallNext` run by a user-facing assistant. On
  first call it idempotently subscribes session `<session>` **per
  capability**: always to `@ark-bloodhound-result=<session>` (findings —
  the level-3 base); and, with `ambient` set (`listen --ambient`), also to
  `@ark-recall-result=<session>` (surfaces) — that recall-result sub is the
  **ambient opt-in** the watcher keys on (R2949). Thereafter it blocks (a
  `pubsub.Listen` loop) until a result event arrives, fetches the published
  doc(s) — a `finding-<session>-<B>` (`## Finding:`) or, with ambient, a
  `result-<session>-<fire>` (`## Surface:` / `## Recommend:`) — and returns
  their content plus crank-handle prose ("surface what genuinely helps the
  user, then run `recall listen` again"). When any returned doc
  references a chat-JSONL chunk (a `## Surface:`/`## Recommend:` whose
  `<path>` ends in `.jsonl` — cheap path-shape proxy), the prose
  additionally instructs applying tags on those chunks as external
  (`@ext`) tags —
  append-only source of truth — and notes this is how conversations
  enter the hypergraph; the prose is omitted when no chat-JSONL chunk is
  referenced (R2871). The internal-vs-`@ext` choice stays the
  assistant's. **No keepalive, no context-gate, no subscriber-gate** —
  those are daemon concerns; the only non-result return is ctx
  cancellation. It does **not** filter
  `## Recommend:` by RJ ceiling — the assistant owns that (R2765/R2766).
  The `/recall` skill (`.claude/skills/recall/SKILL.md`, R2866) drives
  the loop: it supplies the session UUID via the
  `sessionid=${CLAUDE_SESSION_ID}` macro, runs `listen --ambient`
  backgrounded (requiring `/bloodhound`, which spawns the secretary + runs
  the base `listen`), and surfaces + relaunches on each completion. Opt-in:
  no `/recall` → no `ark-recall-result` subscriber → ambient stays off
  (`/bloodhound`'s base `listen` still opts into findings).

### Result-doc builder (CLI-driven)
- SurfaceItem(fireToken, loc, reason) error (R2900, R2899,
  R2872) — opens `results[fireToken]` on first call for that
  token; `loc` is the candidate's `<path>:<range>` (R2900).
  **own-session gate (R2872):** if `loc`'s path resolves to the
  fire's own session (`sessionFromJSONLPath(path) == doc.session`)
  it returns an error instead of emitting — the loc is a
  `# Source:` / conversation paragraph already in the reader's
  context, and the error names the fix (surface a `## Candidate:`
  locator, not the source one), doubling as fumble-onboarding.
  Otherwise appends a `## Surface: <path>:<range> (<size>)` H2
  with its `reason: ...` line (R2899). Size is read server-side
  via `ChunkText(path, range)` + `friendlySize` (decimal bytes /
  K / M) so the consuming assistant can gauge fetch cost; on
  lookup failure the size renders as `?` and the surface still
  emits. Errors on missing required args before any state change.
  **Starts the surface cooldown (R2894):** after appending,
  resolves `loc` → chunkID just-in-time and calls
  `db.MarkSurfaced(doc.session, chunkID)` so the watcher's
  cooldown floor (R2893) won't re-offer this chunk within
  `[recall].surface_cooldown`.
- RecommendItem(fireToken, loc, tagSpec, reason) error
  (R2757, R2899) — same open-on-first-call semantics; appends
  a `## Recommend: @<tag>[:<value>] on <path>:<range>` H2 with
  its `reason: ...` line (R2899). `loc` is the candidate's
  path:range — no server-side resolution and no size read needed,
  since the agent already supplies it.
- Close(fireToken, nonce, preserveCuration bool) error (R2758,
  R2901) — the single cleanup verb. `fireToken` is the composite
  `<session>-<fire>`; it decomposes to the session and fire for
  the tmp:// paths below.
  - If `results[fireToken]` exists with at least one item: query
    `pubsub.SubscriberCount("ark-recall-result", session)`
    first. If zero, skip the result-doc write and set
    `outcome := "no-subscriber"` (R2807, R2808). Otherwise
    write `tmp://ARK-RECALL/result-<session>-<fire>` via the
    write actor with the head tag `@ark-recall-result: <session>`
    followed by the accumulated body (R2750, R2899) and set
    `outcome := "result-emitted"`.
  - If no items were ever added: skip the result-doc write
    entirely; the assistant's `ark listen` never sees a
    matching event. Set `outcome := "silent-close"`. (No
    subscriber check is needed here — there is nothing to
    deliver regardless.)
  - Either way: remove `tmp://ARK-RECALL/curation-<session>-
    <fire>` via the write actor unless `preserveCuration`, and
    delete the materialized file `~/.ark/recall-curation/
    curation-<session>-<fire>.md` (`curationFilePath`, R2896).
    Also sweep any orphan curation docs for the same session
    whose fire number is strictly less than the current
    `<fire>` — older fires the assistant missed handling.
    Same-session scope only (R2758).
  - Either way: discover the calling subagent's JSONL via
    `discoverSubagentJSONL(nonce)` (R2759, R2760), sum
    tokens (R2761), and append one record to
    `~/.ark/monitoring/recall.jsonl` (R2763). If discovery
    fails, log zeros and continue — `close` never fails on
    discovery alone.
  - Drop `results[fireToken]` and the originating session map
    entry.

### Subagent JSONL discovery
- discoverSubagentJSONL(nonce) → (jsonlPath string, found bool)
  (R2760):
  1. `cwd_encoded := strings.ReplaceAll(cwd, "/", "-")`
  2. `parent_session := os.Getenv("CLAUDE_CODE_SESSION_ID")`
  3. `dir := ~/.claude/projects/<cwd_encoded>/<parent_session>/subagents`
  4. Scan `*.meta.json` for the first entry whose
     `description` body contains the substring `nonce <N>`.
  5. Return the paired `agent-<id>.jsonl` path.
- sumSubagentTokens(jsonlPath) → (inTokens, outTokens int)
  (R2761): line-by-line scan; for each `"type":"assistant"`
  record, add `usage.input_tokens` and `usage.output_tokens`.
  No `isSidechain` filter — the file is dedicated to one
  subagent.

### Context introspection
- ContextTokens(nonce) → (tokens int, found bool) (R2777) —
  uses `findSubagentJSONL(nonce)` (shared with token-sum
  lookup), reads the JSONL backwards, returns the
  `cache_creation_input_tokens + cache_read_input_tokens` sum
  from the most recent `"type":"assistant"` record carrying a
  usage object. The lotto-tube recall agent (Phase 2) calls
  this to self-recycle when context grows past a configurable
  limit. `(0, false)` on discovery failure so callers
  distinguish "couldn't measure" from a real 0.

### Fumble Log
- LogFumble(fire, nonce uint32, command, args, errMsg string)
  (R2772) — append one JSON line `{timestamp, fire, nonce,
  command, args, error}` to
  `~/.ark/monitoring/recall-fumbles.jsonl`. Called by the CLI
  flag-parser on every malformed `surface` / `recommend` /
  `close` invocation. The fire still completes — the
  malformed call is rejected by the CLI but the surrounding
  pipeline continues.

### Bloodhound — directed search (R2937–R2946)

The directed-search half of the warm secretary. Lives in the
`ARK-BLOODHOUND` tmp:// namespace, separate from recall's
`ARK-RECALL`, with its own in-flight maps — so a bloodhound `<B>`
and a recall `<F>` can never collide on a path or a map key. Input rides
the shared `@ark-secretary-work` tube; the bloodhound's output is its
**own** `@ark-bloodhound-result` subscription (distinct from recall's
`@ark-recall-result`) — the secretary's one `next` and the assistant's one
`listen` carry all of it.

- RecallBloodhoundOpen(session, B, payload, seed) (R2937, R2938, R3006)
  — Go-internal, called by the watcher's `dispatchBloodhound`. Writes
  `tmp://ARK-BLOODHOUND/task-<session>-<B>` (tag
  `@ark-secretary-work: <session>`) whose body `buildSearchTask`
  assembles in order: the `## Search task <cookie>` first line with the
  raw payload; the **`## Recall seed`** block (the `seed` string the
  watcher pre-rendered from `Librarian.Recall`, R3006); then the
  **search crank handle**. Stores `bloodhoundClues[cookie] = payload`
  for the finding header. The crank handle is a Go const (the CLI craft
  travels in the doc, Stencil-style); its first step reads the seed.
- `next` dispatch priority (R2939, R2940): `RecallNext`'s loop scans
  `db.Files()` once and prioritizes a pending
  `ARK-BLOODHOUND/task-` doc over any `ARK-RECALL/curation-` doc for
  the session (`lowestPendingBloodhound` before
  `lowestPendingCuration`); within a kind, lowest id first.
  `lowestPendingBloodhound` dispatches a task only while the session has an
  `ark-bloodhound-result` subscriber (R2947), mirroring how
  `lowestPendingCuration` gates on `ark-recall-result`. A
  bloodhound doc is small (crank handle + payload), so `next`
  returns its body **inline** (no `writeCurationFile`, no Read
  keyhole) with the close directive framed by the `<session>-b<B>`
  cookie. Keepalive / context-gate / redial / foreground window
  unchanged.
- FindingItem(cookie, loc, answer, note, reason) error (R2943,
  R2944) — opens `bloodhounds[cookie]` lazily on first call;
  appends one `- <path>:<range> (<size>) — <note>` line for a
  `-loc` finding (size via `ChunkText`, no chunkid on the wire) or
  the synthesized `-answer` text for an answer/verdict. **No
  own-session gate** — unlike `SurfaceItem`, a directed search may
  point at the requester's own session (R2944). One item per call,
  mirroring surface.
- Close routing (R2945): `Close` detects a bloodhound by the
  cookie's `b<B>` kind-marker (`parseBloodhoundToken`). For a
  bloodhound it writes `tmp://ARK-BLOODHOUND/finding-<session>-<B>`
  (tag `@ark-bloodhound-result: <session>`, R2945) iff `bloodhounds[cookie]`
  has items — stamping the `## Finding: <clue>` header from
  `bloodhoundClues[cookie]` (R2946) — else silent-close; removes
  `task-<session>-<B>`; drops both bloodhound maps; and appends the
  same monitor record (no orphan sweep — bloodhound tasks don't
  pile the way curations do). A plain `<session>-<fire>` cookie
  routes to the existing recall close unchanged.

## Out of scope
- Does **not** spawn or supervise the recall agent process.
  The assistant invokes the agent via the Task tool; this
  component sees only the CLI calls the agent makes back into
  `ark serve`.
- Does **not** call `Store.RejectDerived`. The agent is
  recommend-only (R2774); rejection writes route through the
  assistant's `ark connections recall reject-derived`.
- Does **not** subscribe to or publish pubsub events directly.
  Tmp:// writes go through the write actor; pubsub
  publication is the write actor's responsibility, not the
  builder's.

## Collaborators
- RecallWatcher (crc-RecallWatcher.md): calls
  `RecallCurationOpen(session, fire)` at fire time and drives
  the curation builder.
- Store (crc-Store.md): write-actor calls for the tmp:// doc
  writes and removals; pubsub publication piggybacks on the
  write actor.
- CLI (crc-CLI.md): hosts the four subcommand handlers
  (`reserve-nonce`, `surface`, `recommend`, `close`); each
  proxies to the server-side handler over the existing
  `connections` subcommand wiring.
- Server (crc-Server.md): owns the builder instance, exposes
  HTTP handlers for the four CLI verbs, and hosts the
  `nonceCounter` and the `curations` / `results` maps.

## Sequences
- seq-recall-agent.md
