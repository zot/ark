# RecallAgentBuilder
**Requirements:** R2747, R2748, R2749, R2750, R2751, R2754, R2755, R2756, R2757, R2758, R2759, R2760, R2761, R2762, R2763, R2772, R2774, R2777

In-server state machine that owns the curation-doc and result-
doc builders for the Simple Recall v2 pipeline. Two callers
share the same builder family:

- The **watcher** (crc-RecallWatcher.md) calls the Go-internal
  `RecallCurationOpen(session, fire)` to write the curation doc
  at fire time (R2754).
- The **recall agent** (a Haiku subagent, see
  seq-recall-agent.md) shells out to four CLI verbs —
  `reserve-nonce`, `surface`, `recommend`, `close` — that route
  through this component (R2755, R2756, R2757, R2758).

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
- curations: map[fireID]*curationDoc — open curation-doc
  builders. Keyed by fire number; populated by
  `RecallCurationOpen` and dropped on `Close()`.
- results: map[fireID]*resultDoc — open result-doc builders.
  Keyed by fire number; populated lazily on the first
  `surface` or `recommend` call for that fire; dropped on
  `Close`.
- nonceCounter: uint32 — in-memory monotonic counter for
  `reserve-nonce`. Resets on `ark serve` restart (R2755).
- monitorLog: *MonitorLogWriter — append-only writer over
  `~/.ark/monitoring/recall.jsonl` (R2763) and the Fumble
  Log at `~/.ark/monitoring/recall-fumbles.jsonl` (R2772).
  Owns its own file handle, mutex-protected.

## Does

### Curation builder (Go-internal)
- RecallCurationOpen(session, fire) → *RecallCurationBuilder
  (R2754). Initializes an empty in-memory doc keyed by `fire`,
  registers it in `curations[fire]`, returns the builder.
- (builder).Section(sourceChunkID, sourceParagraphText)
  appends the `# Source Chunk:` H1 + `> `-blockquoted
  paragraph excerpt (UTF-8-safe ~200-byte cap) to the
  in-memory doc (R2749).
- (builder).Candidate(chunkID, path, rangeLabel, byteSize,
  score, tagNames, proposedTagsWithScores, contentExcerpt)
  appends a `## Candidate: <chunkid> (<size>) <path>:<range>`
  H2 with score, tag list, parenthesized proposed-tags, and a
  fenced ~500-char content excerpt to the most recent section.
  `byteSize` is the chunk's full pre-truncation length; the
  watcher captures it before applying the 500-char excerpt cap
  and passes it through. Size renders via `friendlySize` (same
  helper Surface uses). (R2749)
- (builder).Close() writes
  `tmp://ARK-RECALL/curation-<session>-<fire>` via the write
  actor with the two head tags `@ark-recall-curate: <session>`
  and `@ark-recall-fire: <fire>` followed by the accumulated
  body (R2747, R2748). Removes `curations[fire]`.

### Nonce reservation
- ReserveNonce() uint32 — atomic-add increment of
  `nonceCounter`, returns the new value (R2755). Cheap and
  in-memory; no DB write.

### Result-doc builder (CLI-driven)
- SurfaceItem(fire, chunkID, reason) error (R2756) —
  opens `results[fire]` on first call for that fire; appends
  a `## Surface: <chunkid> (<size>)` H2 with its `reason: ...`
  line. The size label is computed server-side by
  `db.ChunkTextByID` + `friendlySize` (decimal bytes / K /
  M) so the assistant can decide whether to fetch a big
  chunk; on lookup failure the size renders as `?` and the
  surface still emits. Errors on missing required args before
  any state change.
- RecommendItem(fire, chunkID, tagSpec, reason) error
  (R2757) — same open-on-first-call semantics; appends a
  `## Recommend: @<tag>[:<value>] on <chunkid>` H2 with its
  `reason: ...` line.
- Close(fire, nonce, preserveCuration bool) error (R2758) —
  the single cleanup verb:
  - If `results[fire]` exists with at least one item: write
    `tmp://ARK-RECALL/result-<session>-<fire>` via the write
    actor with the head tag `@ark-recall-result: <session>`
    followed by the accumulated body (R2750, R2751). Set
    `outcome := "result-emitted"`.
  - If no items were ever added: skip the result-doc write
    entirely; the assistant's `ark listen` never sees a
    matching event. Set `outcome := "silent-close"`.
  - Either way: remove `tmp://ARK-RECALL/curation-<session>-
    <fire>` via the write actor unless `preserveCuration`.
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
  - Drop `results[fire]` and the originating session map
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
