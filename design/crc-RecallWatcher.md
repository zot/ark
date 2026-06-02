# RecallWatcher
**Requirements:** R2687, R2688, R2689, R2690, R2692, R2693, R2695, R2696, R2698, R2705, R2706, R2708, R2711, R2712, R2713, R2714, R2715, R2728, R2729, R2730, R2731, R2732, R2733, R2734, R2735, R2736, R2739, R2740, R2741, R2747, R2748, R2753, R2746, R2806, R2808, R2867, R2868, R2869, R2893, R2898, R2901

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
    seen (and armReady); nil otherwise
  - armReady: bool — gates arming to once per user turn (R2734);
    set by a user record, cleared on arm
- fireCounters: map[sessionID]uint64 — per-session monotonic
  fire counters, mutex-protected (R2901). Seeded on the first
  fire for a session after an `ark serve` start by scanning
  `~/.ark/recall-curation/` for that session's
  `curation-<session>-<fire>.md` files and taking `max(fire)+2`
  (or `1` if none); thereafter incremented in memory. The `+2`
  skips a possibly-unmaterialized in-flight doc (one secretary ⇒
  lag ≤ 1); the in-memory hold — not a per-allocation dir
  recompute — closes the allocation→materialization race. The
  composite `<session>-<fire>` is the cookie tying curation ↔
  result, globally unique even though the fire integer is only
  per-session. Replaces the global `fireCounter` (R2752, R2901).
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
  return immediately. Then applies the **activation gate**
  (R2867): queries `pubsub.SubscriberCount("ark-recall-curate",
  sid)` and `pubsub.SubscriberCount("ark-recall-result", sid)`;
  if either is zero, stops any armed `pendingTimer`, deletes
  `sessions[sid]`, and returns — an unsubscribed session is never
  accumulated, armed, or fired, and leaks no state (R2867, R2868).
  Otherwise:
  - Append `addedChunkIDs` to `sessions[sid].pendingChunks`
    (R2729, R2730).
  - Scan `newBytes` line-by-line; parse each line as JSON and
    inspect top-level `type`/`subtype`, plus `message.content` and
    `origin.kind` for user records (R2731, R2732):
    - On a *genuine* `type=user` record — string content and no
      harness `origin` (`isGenuineUserMessage`; excludes tool-results
      (array content) and injected notifications (`origin.kind` like
      `task-notification`)) → cancel `pendingTimer`, set `armReady`
      (R2732, R2733).
    - On `type=system, subtype=turn_duration` → only if
      `armReady`: cancel any armed timer and arm a fresh one for
      `activation_delay` seconds (clearing `armReady`) whose expiry
      posts the fire closure. If not `armReady`, ignore — an
      agent-only turn does not re-arm, which stops the recall
      ping-pong (R2734).
- fire(sessionID): timer-expiry callback. Snapshots
  `pendingChunks`, clears the slice under the per-session
  lock, allocates the next per-session fire value
  (`fireCounters[sessionID]`, seeded from the curation-dir on
  first use, R2901), then runs the recall pipeline outside the
  lock (R2735). Before
  invoking the substrate or opening the curation builder,
  re-queries **both** `pubsub.SubscriberCount("ark-recall-curate",
  sessionID)` and `pubsub.SubscriberCount("ark-recall-result",
  sessionID)` as a backstop to the OnAppend activation gate (R2867)
  — covering a consumer that dropped during `activation_delay`. If
  either is zero, skips the substrate call entirely, appends one
  record to `~/.ark/monitoring/recall.jsonl` with
  `outcome: "no-subscriber"` (R2806, R2808), and returns. The
  `pendingChunks` slice is already cleared, so the next OnAppend
  starts fresh.
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
    clears `min_similarity` (R2708, R2739): apply the
    **surface-cooldown floor** via `dropCooledCandidates(sessionID,
    chunks)` (R2893) — drop any candidate whose `(sessionID, chunk)`
    was surfaced within `[recall].surface_cooldown` (seam 2
    `Store.LastSurfaced`), so the secretary judges only novel
    candidates; if none survive, the paragraph is dropped. Then call
    `b.Section(sourcePath, sourceRange, paragraphText)` to emit
    the `# Source: <path>:<range>` H1 + blockquoted excerpt — the
    watcher resolves the source chunk's path:range at build time;
    no chunkid in the heading (R2898). For each top-K candidate,
    classify it by source path (R2869) — `tagOnly =
    sessionFromJSONLPath(path) == sessionID` (the originating
    session's own JSONL) — and call `b.Candidate(path, rangeLabel,
    byteSize, score, tagNames, proposedTagsWithScores,
    contentExcerpt, tagOnly)`; the H2 leads with `<path>:<range>`,
    not a chunkid (R2898). A tag-only candidate renders
    `- tag-only: true` and must not be surfaced — only
    tag-recommended (R2869).
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
- ~~No subscriber liveness check before emit (R2714)~~ — gates added
  by R2867 (activation, at OnAppend) and R2806 (backstop, at fire);
  the watcher checks both `@ark-recall-curate` and
  `@ark-recall-result=<session>` subscriber presence before tracking
  a session and before writing a curation doc.
- No backfill on subscriber arrival: a subscriber that arrives
  after `outcome: "no-subscriber"` was recorded does not
  retroactively receive the dropped fire.
- No backfill on cold start; goes-forward only (R2698)
- No self-exclusion logic — inherited from substrate (A66)
- No LLM call, no new-definition tag proposals (RP/RPE/RR
  not written here), no tag-axis filtering (R2715)
- Per-session state is bounded to actively-watched sessions: the
  activation gate (R2867) deletes a session's entry whenever either
  subscription is absent, so a closed/unsubscribed Claude Code
  session no longer leaks state. (No TTL for a session that stays
  subscribed but goes idle.)

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
- PubSub (crc-PubSub.md): `SubscriberCount` on both
  `ark-recall-curate` and `ark-recall-result=<session>` gates
  session activation at OnAppend (R2867) and the curation-doc
  write at fire (R2806).

## Sequences
- seq-recall-watcher.md
- seq-recall-agent.md
