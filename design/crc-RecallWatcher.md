# RecallWatcher
**Requirements:** R2687, R2688, R2689, R2690, R2692, R2693, R2695, R2696, R2698, R2705, R2706, R2708, R2711, R2712, R2713, R2714, R2715, R2728, R2729, R2730, R2731, R2732, R2733, R2734, R2735, R2736, R2739, R2740, R2741, R2747, R2748, R2749, R2752, R2753, R2746

Built-in subsystem of `ark serve` that watches Claude Code JSONL
sources, detects turn boundaries via the `turn_duration` system
record, accumulates indexed chunks across the turn, and writes a
**curation doc** to `tmp://ARK-RECALL/curation-<session>-<fire>`
holding per-paragraph recall candidates. No language model in
the watcher itself — the curation doc is read by a one-shot
Haiku recall agent (see crc-RecallAgentBuilder.md and
seq-recall-agent.md) that produces the result doc the assistant
reads. The watcher is deterministic on its inputs.

The substrate (`Librarian.Recall`) is the actor that does the
chunk-similarity work; the watcher is plumbing — config + a
per-session state machine + a fire counter + a worker that
composes and writes each curation doc via the in-process
`RecallCurationBuilder`.

## Knows
- db: *DB — back-pointer; the watcher reads `[recall]` via
  `db.Config().Recall` lazily on each pass so a live reload
  via `db.ReloadConfig()` propagates without an explicit
  reload call (R2695)
- librarian: *Librarian — substrate caller (`Recall(...)`)
- store: *Store — write actor for RD records
- curationBuilder: func(session, fire) → *RecallCurationBuilder
  — opens a Go-internal builder for the curation doc; same
  state machine the agent-facing CLI uses for the result doc
  (R2754). Owned by crc-RecallAgentBuilder.md.
- sessions: map[sessionID]*sessionState — per-session state;
  mutex-protected (R2730). Each `sessionState` carries:
  - pendingChunks: []uint64 — indexed chunkIDs accumulated
    since the last fire
  - pendingTimer: *time.Timer — armed when turn_duration is
    seen; nil otherwise
- fireCounter: uint64 — globally monotonic counter scoped to
  one `ark serve` run, starting at 0; allocated on each timer
  expiry; written into the curation doc header and used as
  the cookie that ties curation ↔ result (R2752). Lives at
  the watcher level (one counter per server), not per
  session.
- jobs: chan func() — closure-actor channel; processed by the
  single worker goroutine so all fire-time work serializes
  cleanly (the per-session timer expiry posts a closure
  here)

## Does
- Enabled() bool: true when `db.Config().Recall.Enabled` is
  true. Re-read on every OnAppend so the master switch
  reflects the live config (R2688, R2695).
- SourceQualifies(path, strategy) bool: `strategy ==
  "chat-jsonl"` AND (`Recall.Sources` empty OR the path's
  source root, per `Config.SourceRootForPath`, matches an
  entry in `Recall.Sources`) (R2696, R2741).
- OnAppend(path, strategy, newBytes, addedChunkIDs):
  indexer-side entry called from `executeRefresh`'s isAppend
  branch. Source-qualifies first (R2741); if not qualified,
  return immediately. Otherwise:
  - Append `addedChunkIDs` to `sessions[sid].pendingChunks`
    (R2729, R2730).
  - Scan `newBytes` line-by-line; parse each line as JSON and
    inspect top-level `type`/`subtype` (R2731, R2732):
    - On `type=user` → cancel `pendingTimer` (R2733).
    - On `type=system, subtype=turn_duration` → cancel any
      armed timer, arm a fresh one for `activation_delay`
      seconds whose expiry posts the fire closure (R2734).
- fire(sessionID): timer-expiry callback. Snapshots
  `pendingChunks`, clears the slice under the per-session
  lock, allocates the next `fireCounter` value (R2752), then
  runs the recall pipeline outside the lock (R2735):
  - For each chunkID in the snapshot, fetch the chunk text
    via `db.ChunkTextByID`, run `microfts2.MarkdownChunker{}`
    to split into paragraphs ≥ 30 bytes, and call
    `librarian.Recall([]ConnectionsInput{{Text: paragraph}},
    RecallOpts{K: cfg.EffectiveChunksPerDM(),
    IncludeContent: true, Session: sessionID,
    Propose: cfg.EffectivePropose(),
    KeepTagless: true})` per paragraph (R2736, R2746).
  - Open a `RecallCurationBuilder(sessionID, fire)`
    (R2753, R2754).
  - For each paragraph whose Recall result's top chunk
    clears `min_similarity` (R2708, R2739): call
    `b.Section(sourceChunkID, paragraphText)` to emit the
    `# Source Chunk:` H1 + blockquoted excerpt (R2749); for
    each top-K candidate, call `b.Candidate(chunkID, path,
    rangeLabel, score, tagNames, proposedTagsWithScores,
    contentExcerpt)` (R2749).
  - If no sections survived: drop the builder without
    calling `b.Close()`; the fire completes silently and
    no curation doc is written (R2753).
  - Otherwise call `b.Close()` to write
    `tmp://ARK-RECALL/curation-<session>-<fire>` with the
    two head-of-chunk tags `@ark-recall-curate: <session>`
    and `@ark-recall-fire: <fire>` (R2747, R2748).
  - Mark-on-send: for every candidate chunk written in any
    surfaced section, call `store.AddDiscussed(sessionID,
    tag, value)` for each inline + ext-routed tag (R2711,
    R2712, R2740).
  - Log `fired` decision with section/candidate/discussed
    counts and the fire number (R2713).

## Out of scope
- No subscriber liveness check before emit (R2714)
- No backfill on cold start; goes-forward only (R2698)
- No self-exclusion logic — inherited from substrate (A66)
- No LLM call, no new-definition tag proposals (RP/RPE/RR
  not written here), no tag-axis filtering (R2715)
- No per-session state TTL — sessions leak in v1; small leak
  per closed Claude Code session.

## Collaborators
- Indexer: `executeRefresh` isAppend path calls `OnAppend`
  with `(path, strategy, newBytes, added)` (R2729).
- Librarian: substrate `Recall` with `Session`, `Propose`,
  and `KeepTagless` options (R2736, R2746).
- Store: `AddDiscussed(session, tag, value)` writes one RD
  record per surfaced tag (per R2650, R2659).
- RecallAgentBuilder (crc-RecallAgentBuilder.md): owns the
  curation-doc builder state machine. The watcher calls
  `RecallCurationOpen(session, fire)` to get a builder
  (R2754). The same state machine backs the agent-facing
  result-doc CLI verbs.
- Server: registers the watcher subsystem during `Serve()`;
  wires the in-process curation-builder constructor; reads
  `[recall]` from ark.toml on startup and reload; owns the
  `fireCounter` increment under the watcher's lock.

## Sequences
- seq-recall-watcher.md
- seq-recall-agent.md
