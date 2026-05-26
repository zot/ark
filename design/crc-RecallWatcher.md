# RecallWatcher
**Requirements:** R2687, R2688, R2689, R2690, R2692, R2693, R2694, R2695, R2696, R2698, R2700, R2701, R2702, R2703, R2704, R2705, R2706, R2707, R2708, R2709, R2710, R2711, R2712, R2713, R2714, R2715, R2728, R2729, R2730, R2731, R2732, R2733, R2734, R2735, R2736, R2737, R2738, R2739, R2740, R2741

Built-in subsystem of `ark serve` that watches Claude Code JSONL
sources, detects turn boundaries via the `turn_duration` system
record, accumulates indexed chunks across the turn, and DMs a
grouped per-input-chunk recall pass back to the originating
session. No language model in the loop — the watcher is
deterministic on its inputs.

The substrate (`Librarian.Recall`) is the actor that does the
chunk-similarity work; the watcher is plumbing — config + a
per-session state machine + a worker that composes and emits
each DM.

## Knows
- db: *DB — back-pointer; the watcher reads `[recall]` via
  `db.Config().Recall` lazily on each pass so a live reload
  via `db.ReloadConfig()` propagates without an explicit
  reload call (R2695)
- librarian: *Librarian — substrate caller (`Recall(...)`)
- store: *Store — write actor for RD records
- composeDM: func — shared internal compose path used by
  `cmdMessageDM` (see crc-CLI.md) and the watcher (R2700)
- sessions: map[sessionID]*sessionState — per-session state;
  mutex-protected (R2730). Each `sessionState` carries:
  - pendingChunks: []uint64 — indexed chunkIDs accumulated
    since the last fire
  - pendingTimer: *time.Timer — armed when turn_duration is
    seen; nil otherwise
  - lastTurnDurationChunkID: uint64 — chunkID of the most
    recent turn_duration record (used for `@ark-recall-fire`
    when the chunker indexed that line; falls back to a
    timestamp when not)
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
  lock, then runs the recall pipeline outside the lock
  (R2735):
  - For each chunkID in the snapshot, call
    `librarian.Recall([]ConnectionsInput{{ChunkID: cid}},
    RecallOpts{K: cfg.EffectiveChunksPerDM(),
    IncludeContent: true, Session: sessionID,
    Propose: cfg.EffectivePropose()})` (R2736).
  - For each Recall result whose top chunk clears
    `min_similarity` (R2708, R2739), build one
    `## Recalled for chunk <cid>` section:
    - Section header includes a blockquoted ~200-char
      excerpt of the input chunk's text (R2738).
    - Section body is `### Recalled chunks` followed by the
      `RenderRecallChunks` stencil (R2704, R2737).
  - If no sections survived, return without emitting a DM
    (silent success — pendingChunks already cleared).
  - Otherwise build the body: `@ark-recall-fire: <ref>` line
    + instruction block + the per-input sections (R2702,
    R2703, R2737).
  - Emit via `composeDM(DMSender{Service: "ARK-RECALL"},
    [sessionID], "recall", "", body)` and append to tmp://
    via the write actor (R2700, R2701).
  - Mark-on-send: for every recalled chunk listed in any
    surfaced section, call `store.AddDiscussed(sessionID,
    tag, value)` for each inline + ext-routed tag (R2711,
    R2712, R2740).
  - Log `fired` decision with section/recalled/discussed
    counts (R2713).

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
- Librarian: substrate `Recall` with `Session` and `Propose`
  options (already exposed).
- Store: `AddDiscussed(session, tag, value)` writes one RD
  record per surfaced tag (per R2650, R2659).
- Server: registers the watcher subsystem during `Serve()`;
  wires the shared `composeDM` function for in-process use;
  reads `[recall]` from ark.toml on startup and reload.
- CLI (`cmdMessageDM` in crc-CLI.md): shares the
  `composeDM` function — the watcher invokes the in-process
  Go path with the same arguments the CLI flag surface
  produces.

## Sequences
- seq-recall-watcher.md
