# RecallWatcher
**Requirements:** R2687, R2688, R2689, R2690, R2692, R2693, R2695, R2696, R2698, R2705, R2706, R2708, R2711, R2712, R2713, R2714, R2715, R2728, R2729, R2730, R2731, R2732, R2733, R2734, R2735, R2736, R2739, R2740, R2741, R2747, R2748, R2753, R2746, R2806, R2808, R2867, R2868, R2869, R2893, R2898, R2901, R2934, R2935, R2936, R2937, R2947, R2948, R2949, R3006, R3007, R3009, R3020, R3023, R3024, R3025, R3030, R3031, R3032, R3033, R3019, R3034, R3038, R3039, R3041, R3042, R3043, R3044, R3045

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
- bloodhoundCounters: map[sessionID]uint64 — per-session
  monotonic **bloodhound id** (`<B>`) counter (R2937). In-memory
  only, no dir-seeding (task docs are ephemeral tmp://), reset on
  restart — distinct from the dir-seeded `fireCounters`.
- cliPool: the CLI-bloodhound pool roster (R3023, R3024) — pool-
  secretary sessions with per-secretary state (free / busy /
  idle-since for the cooldown clock), plus the in-flight request
  docs keyed by `<id>`. The watcher marks a secretary busy on
  dispatch and free on return; Luhmann owns the spawn/stop lifecycle
  (crc-LuhmannCLI.md). In-memory; a bounce drops it and the CLI's
  `--wait` re-drives the hunt. Also holds `requests`: a
  path→`cliRequest{submitted, raw}` map recording each hunt's submit
  time (the reap clock, R3041) and its `--raw` curate-intent (R3038),
  set when the request first arrives and read at the return hop.

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
  return immediately. Then the **per-capability gate** (R2947, R2949,
  superseding the old both-ends R2867): if the secretary is absent
  (`SubscriberCount("ark-secretary-work", sid) == 0`, level ≤2), stop
  any armed `pendingTimer`, delete `sessions[sid]`, and return — no
  worker, nothing to produce (R2868). With the secretary present,
  recognition and ambient gate **independently** on the assistant's
  per-capability result subs: scan bloodhounds iff
  `ark-bloodhound-result` is subscribed (R2947); accumulate
  `pendingChunks` + arm the timer iff `ark-recall-result` is subscribed
  (R2949). The ambient bullets below run under the recall-result gate;
  bloodhound recognition under its own:
  - Append `addedChunkIDs` to `sessions[sid].pendingChunks`
    (R2729, R2730).
  - Scan `newBytes` line-by-line; parse each line as JSON and
    inspect top-level `type`/`subtype`, plus `message.content` and
    `origin.kind` for user records (R2731, R2732):
    - On a *genuine* `type=user` record — string content and the
      positive `origin.kind == "human"` marker (`isGenuineUserMessage`;
      excludes tool-results (array content), injected notifications
      (`origin.kind` like `task-notification`), and local-command
      caveats (no origin) — absence of origin is not genuine, R3009) →
      cancel `pendingTimer`, set `armReady` (R2732, R2733, R3009).
    - On `type=system, subtype=turn_duration` → only if
      `armReady`: cancel any armed timer and arm a fresh one for
      `activation_delay` seconds (clearing `armReady`) whose expiry
      posts the fire closure. If not `armReady`, ignore — an
      agent-only turn does not re-arm, which stops the recall
      ping-pong (R2734).
  - **Bloodhound recognition** (R2934, R2935, R2936, R2947) — gated on
    `ark-bloodhound-result`, independent of the ambient bullets above:
    `scanBloodhounds(newBytes)` parses each
    `type:"assistant"` line's text (reusing `assistantText`) and
    regex-matches `<BLOODHOUND>(.*?)</BLOODHOUND>` (non-greedy,
    DOTALL) — each capture is one payload. Deterministic, once-only
    (newBytes is the newly-appended slice), and orthogonal to the
    turn machinery: it never reads or touches
    `pendingTimer`/`armReady`/`pendingChunks`, so a watermark
    dispatches on its own schedule and neither triggers nor
    suppresses a curation fire (R2935). For each payload, allocate
    `<B>` (`nextBloodhoundLocked`) under the lock and post a closure
    to `jobs` → `dispatchBloodhound`, keeping OnAppend a fast
    line-scan (R2936).
- dispatchBloodhound(sessionID, B, payload): worker-goroutine job.
  Re-checks the **bloodhound gate** as the write-time backstop —
  secretary present (`ark-secretary-work`) AND `ark-bloodhound-result`
  subscribed (R2947). Then **seeds the hunt** via `renderSeed` (R3006, R3007,
  R3043–R3045): `clueOf` extracts the **clue** from the payload — stripping any
  leading `scope`/`depth`/`want`/`curate` metadata lines (R3044); free-form
  in-session prose has none, so it is returned whole — then `splitParagraphs`
  (the same markdown chunker the fire uses, R2736) splits the clue and
  `librarian.Recall` runs with **one `ConnectionsInput` per paragraph**. `Recall`
  unions the per-input hits by chunkID (R3043), so each distinct idea in a complex
  clue contributes its own matches instead of a single centroid query; the seed K
  scales with the paragraph count (`min(bloodhoundSeedK + 5·(paras−1), cap)`,
  R3045), and a single-paragraph clue is one input — unchanged. **Clue-only and
  session-agnostic** (`Session` and `Propose` left off: a directed *pull* hunt sees
  every match, no discussed-exclusion, no derivation side-effects; the conversation
  is never folded into the search input) — the result renders via
  `renderBloodhoundSeed` (the compact `<path>:<range> (<size>) <score> [tags]`
  locator list, no chunkid; empty-seed note when the corpus matches nothing). The
  seed string is passed to
  `builder.RecallBloodhoundOpen(sessionID, B, payload, seed)` — writes
  the task doc in the `ARK-BLOODHOUND` namespace and retains the clue for
  the finding header (R2937, owned by crc-RecallAgentBuilder.md). Reaching
  the value→chunk tag axis (R2905/R2906) is the point: the subagent's own
  `ark search` is content-only, so only `Recall` can seed it hypergraph-aware.
- fire(sessionID): timer-expiry callback. Snapshots
  `pendingChunks`, clears the slice under the per-session
  lock, allocates the next per-session fire value
  (`fireCounters[sessionID]`, seeded from the curation-dir on
  first use, R2901), then runs the recall pipeline outside the
  lock (R2735). Before
  invoking the substrate or opening the curation builder,
  re-queries **both** `pubsub.SubscriberCount("ark-secretary-work",
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
    two head-of-chunk tags `@ark-secretary-work: <session>`
    and `@ark-recall-fire: <fire>` (R2747, R2748).
  - Mark-on-send: for every candidate chunk written in any
    surfaced section, call `store.AddDiscussed(sessionID,
    tag, value)` for each inline + ext-routed tag (R2711,
    R2712, R2740).
  - Log `fired` decision with section/candidate/discussed
    counts and the fire number (R2713).

### CLI-hunt Fixer (R3020, R3023–R3025, R3030, R3031, R3038, R3039, R3041, R3042)
The watcher is the **Fixer** for external-CLI hunts
([bloodhound-cli.md](../specs/bloodhound-cli.md)): distinct from the
OnAppend-driven ambient/in-session paths above, it **subscribes to two
pubsub tags** and routes the request doc `tmp://BLOODHOUND-CLI/<id>`
across the pipeline via the **tag baton** (R3031). All scheduling is
deterministic Go.

- **On `@ark-bloodhound-cli`** (a new request doc; R3023): gate on a live
  Luhmann first — if `luhmannOwner` is empty (no orchestrator), do not
  schedule, and the CLI reports orchestrator-not-running (R3020). **Record
  the request** in `cliPool.requests` with its submit time and its `raw`
  intent — read the doc once and set `raw` iff the payload carries `curate:
  false` (R3038); the body is overwritten at the enhance/close hops, so the
  intent must be captured now, in memory. Then
  **enhance** the request doc into a standard bloodhound task doc, reusing
  the in-session seed machinery — run `librarian.Recall` (R3006), render
  the `## Recall seed`, and add the search crank handle (R2938) — so a
  pool secretary runs it identically to an in-session hunt (R3030). Then
  **schedule + route**: pick a free pool secretary from `cliPool` and
  re-tag the request doc to its `@ark-secretary-work=<pool-sec>` tube
  (R2948), marking it busy; none free with room
  (`class.bloodhound.pool_max`) → push a *stand up another* directive onto
  Luhmann's `nextQueue` and route when the new secretary subscribes; none
  free with the pool full → leave the request pending (the CLI's `--wait`
  blocks); too many idle past `cooldown_seconds` → push a *stop one*
  directive.
- **On `@ark-bloodhound-cli-return`** (a secretary re-tagged the request
  doc with its raw results; R3024): **free the secretary** — mark it
  idle-in-cooldown in `cliPool`, occupancy freed *before* curation; only
  past `cooldown_seconds` is it eligible for a *stop one*. Then **branch on
  the recorded `raw` intent** (R3039): for a **curated** request (default)
  push the request-doc path onto Luhmann's `nextQueue` as a curation task
  (R3025, no doc-tag hop); for a **raw** request skip Luhmann entirely and
  `relayRawResult` — write the result doc `tmp://BLOODHOUND-CLI-RESULT/<id>`
  directly from the request doc's `## Raw findings` (head tag
  `@ark-bloodhound-cli-result: <id>`, reusing `bloodhoundCLIResultPath`),
  then drop the request doc. Either way the request's `cliPool.requests`
  entry is cleared. The branch decides *who curates*, never occupancy — the
  secretary was already freed above.
- **Atomic baton** (R3031): the request → `@ark-secretary-work` re-tag the
  watcher performs appends its content (seed + crank handle) and rewrites
  the tag in one write-actor flush, so the secretary never wakes on a
  half-enhanced doc.
- **Pool roster lifecycle** (R3033, R3034): the roster is a set of
  `cliPool` entries whose membership brackets a secretary's life.
  `RegisterPoolSecretary(nonce)` adds one on `reserve-nonce --luhmann`
  (R3033); `DeregisterPoolSecretary(class, kind, nonce)` removes it on a
  terminal `exit-record` for the bloodhound class (R3034) — self-gated on
  class + terminal kind, and also dropping any `inflight` entry for that
  nonce. Registration and deregistration are the only membership changes
  besides `prune` (R3019); a self-exit and a `prune`-driven stop reconcile
  idempotently. Without the deregister, a secretary that exits on its own
  context limit would linger in the roster — miscounting `pool_max` and
  drawing hunts to a dead tube — until the cooldown prune swept it.
- **Reap + re-issue sweep** (R3041, R3042): the existing `pruneLoop` ticker
  (`bloodhoundPruneInterval`) also runs a request sweep, since both recovery
  points are time-based and nothing event-driven wakes the watcher for a
  stranded request. Each pass: (1) `pump()` — re-attempt routing in case a
  free secretary was missed; (2) **reap** — for each `cliPool.requests` entry
  older than `request_ttl_seconds` (R3041), drop it from `requests` /
  `pending` and remove its request doc (the CLI hit `--timeout` and exited —
  no clean abandonment signal, so TTL is the trigger; the TTL is generous so
  a live client is never reaped); (3) **re-issue** — if a request has sat
  pending, unrouted, past a re-issue threshold while the pool has room
  (`len(secretaries) < pool_max`), push one *stand up another* directive
  (R3042), recovering a stand-up Luhmann dropped (drift / garbled tool call —
  the R3036 re-launch-first failure class). Bounded by `pool_max` and
  at-most-one stand-up per pass, so it never over-spawns. Both operate only on
  **pending** requests — an in-flight request is bounded by the secretary
  lifecycle (return, or crash→deregister), so the sweep never races a live
  secretary's write.

## Out of scope
- ~~No subscriber liveness check before emit (R2714)~~ — gates added
  by R2867 (activation, at OnAppend) and R2806 (backstop, at fire);
  the watcher checks both `@ark-secretary-work` and
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
  `ark-secretary-work` and `ark-recall-result=<session>` gates
  session activation at OnAppend (R2867) and the curation-doc
  write at fire (R2806); also carries the watcher's
  `@ark-bloodhound-cli` / `@ark-bloodhound-cli-return` subscriptions
  that drive the CLI-hunt Fixer (R3023, R3024).
- LuhmannCLI (crc-LuhmannCLI.md): the watcher pushes *stand up* /
  *stop* directives and curation tasks onto Luhmann's `nextQueue`
  (R3024, R3025), and gates CLI scheduling on a live `luhmannOwner`
  (R3020); Luhmann owns the pool-secretary spawn/stop lifecycle.

## Sequences
- seq-recall-watcher.md
- seq-recall-agent.md
- seq-bloodhound-cli.md
