# Sequence: Recall watcher turn-boundary flow (v2)

**Requirements:** R2696, R2705, R2706, R2708, R2711, R2712, R2713,
R2729, R2730, R2731, R2732, R2733, R2734, R2735, R2736, R2739,
R2740, R2741, R2746, R2747, R2748, R2749, R2752, R2753, R2754,
R2806, R2867, R2868, R2869

The watcher hooks into `indexer.executeRefresh`'s isAppend
branch. OnAppend is synchronous on the indexer's goroutine —
fast: a JSON line-scan plus a pending-chunks append. The
heavy work (substrate Recall per paragraph, curation-doc
write, RD writes) runs on the timer-expiry closure-actor
goroutine. v2 replaces the v1 DM emission with a curation-doc
write via the in-process `RecallCurationBuilder`; downstream
flow from the curation doc to the recall agent and result doc
is in `seq-recall-agent.md`.

## Flow 1: per-append signal handling

```
1. Indexer.executeRefresh (isAppend=true, chat-jsonl source)
         │
         ├── 1.1  AppendChunks → microfts2 commits new chunkIDs
         │
         ├── 1.2  RecallWatcher.OnAppend(
         │          path, strategy="chat-jsonl",
         │          newBytes,
         │          added=[c1, c2, ...])
         │
         └── 1.3  indexer write commits; returns to executeRefresh

2. RecallWatcher.OnAppend (synchronous, indexer goroutine)
         │
         ├── 2.1  if !SourceQualifies(path, strategy): return  (R2741)
         │
         ├── 2.2  sessionID = sessionFromJSONLPath(path)
         │
         ├── 2.3  activation gate (R2867):
         │          curate = pubsub.SubscriberCount("ark-recall-curate", sessionID)
         │          result = pubsub.SubscriberCount("ark-recall-result", sessionID)
         │          if curate == 0 || result == 0:
         │            lock; stop any armed pendingTimer;
         │              delete sessions[sessionID]; unlock
         │            return   (unsubscribed → never accumulated, armed,
         │                      or fired; no leaked state; reactivates at
         │                      JSONL end, R2868)
         │
         ├── 2.4  lock sessions[sessionID]
         │
         ├── 2.5  append `added` to pendingChunks                 (R2730)
         │
         ├── 2.6  for each line in newBytes:                      (R2731, R2732)
         │           obj = json.Unmarshal(line)
         │           if obj.type == "user" && genuine(obj):        (R2732)
         │             // genuine = string content && no origin.kind
         │             //   (excludes tool-results + notifications)
         │             cancel pendingTimer; armReady = true        (R2733)
         │           else if obj.type == "system"
         │                && obj.subtype == "turn_duration":
         │             if !armReady: skip (agent-only turn —       (R2734)
         │               no re-arm; stops the ping-pong)
         │             else: cancel any existing pendingTimer
         │               pendingTimer = time.AfterFunc(
         │                 activation_delay seconds,
         │                 func() { post fire(sessionID) to
         │                          jobs channel })
         │               armReady = false  (once per user turn)
         │
         └── 2.7  unlock; return
```

## Flow 2: timer expiry → curation-doc write

```
3. pendingTimer fires (separate goroutine, Go runtime)
         │
         └── 3.1  posts closure to RecallWatcher.jobs channel

4. RecallWatcher worker pops closure → calls fire(sessionID)

5. RecallWatcher.fire(sessionID)
         │
         ├── 5.1  lock sessions[sessionID]
         │        snapshot = pendingChunks
         │        pendingChunks = nil
         │        unlock                                          (R2735)
         │
         ├── 5.2  if len(snapshot) == 0: return                   (no work)
         │
         ├── 5.3  fire = watcher.nextFireNumber()                 (R2752)
         │        cfg = db.Config().Recall                        (R2695)
         │
         ├── 5.4  subscriber backstop (R2806): re-query both
         │          SubscriberCount("ark-recall-curate", sessionID) and
         │          SubscriberCount("ark-recall-result", sessionID).
         │          if either == 0 (consumer dropped during
         │          activation_delay): append recall.jsonl record with
         │          outcome="no-subscriber" (R2808); return.
         │          pendingChunks already cleared at 5.1 → next
         │          OnAppend starts fresh.
         │
         ├── 5.5  candidates per paragraph:                       (R2736, R2746)
         │        sections = []
         │        for each cid in snapshot:
         │          text = db.ChunkTextByID(cid)
         │          for each para in
         │              microfts2.MarkdownChunker{}.Chunks(text)
         │              where len(para) >= 30:
         │            result = librarian.Recall(
         │              [{Text: para}],
         │              RecallOpts{
         │                K: cfg.EffectiveChunksPerDM(),
         │                IncludeContent: true,
         │                Session: sessionID,
         │                Propose: cfg.EffectivePropose(),
         │                KeepTagless: true,
         │              })
         │            if err: log recall-error; continue
         │            if len(result.Chunks) == 0
         │               or result.Chunks[0].Score
         │                  < cfg.EffectiveMinSimilarity():
         │              continue                                  (R2708, R2739)
         │            for r in result.Chunks:
         │              r.Content = truncateUTF8(r.Content, 500)  (R2705)
         │            paraExcerpt = truncateUTF8(para, 200)       (R2749)
         │            sections.append({sourceCID: cid,
         │                             paraExcerpt,
         │                             candidates: result.Chunks})
         │
         ├── 5.6  if len(sections) == 0:                          (R2753)
         │          log fired with sections-emitted=0; return
         │
         ├── 5.7  b = db.RecallCurationOpen(sessionID, fire)      (R2754, R2869)
         │        for each section in sections:
         │          b.Section(section.sourceCID,
         │                    section.paraExcerpt)
         │          for each c in section.candidates:
         │            tagOnly = sessionFromJSONLPath(c.Path)       (R2869)
         │                      == sessionID  // own-session → no surface
         │            b.Candidate(c.ChunkID, c.Path, c.Range,
         │                        c.Score, c.Tags,
         │                        c.ProposedTagsWithScores,
         │                        c.Content, tagOnly)
         │        b.Close()                                       (R2747, R2748)
         │        → writes tmp://ARK-RECALL/curation-
         │            <sessionID>-<fire>
         │          (write actor publishes pubsub event for
         │           subscribers to @ark-recall-curate)
         │
         ├── 5.8  mark-on-send: for each section.candidates[*]:   (R2711, R2712, R2740)
         │          for each (tag, value) in chunk.Tags:
         │            store.AddDiscussed(sessionID, tag, value)
         │
         └── 5.9  log fired with fire=<F>, sections-emitted=N,    (R2713)
                  candidates=K, discussed-records=M
```

## Flow 3: state-machine transitions (informational)

```
                     user-record
            ┌───────────────────────┐
            │                       │
            ▼                       │
       ┌────────┐  turn_duration   ┌┴───────┐
       │  IDLE  │ ────────────────▶│ ARMED  │
       └────────┘                  └────┬───┘
            ▲                           │
            │                           │ timer expires
            │ pendingChunks cleared,    │
            │ curation doc written (or  │
            │ all sections dropped      │
            │ silently)                 │
            │                           ▼
            └─────────────────── ┌──────────┐
                                 │  FIRING  │
                                 └──────────┘
```

- pendingChunks accumulate in IDLE and ARMED equally; only
  FIRING clears them.
- **armReady gates the IDLE→ARMED transition** (R2734, omitted from
  the boxes above for simplicity): a user record sets armReady,
  arming clears it — so IDLE→ARMED fires only on the *first*
  turn_duration after a user record.
- user-record sets armReady (enabling the next arm) and cancels any
  running timer; in IDLE there is no timer to cancel and
  pendingChunks keep accumulating.
- A turn_duration with armReady unset — an agent-only turn (no
  intervening user record, e.g. the assistant surfacing recall) — is
  ignored: no arm, no fire. This is what stops the recall ping-pong.
- FIRING allocates the next fire number; the fire number is
  per `ark serve` run and is consumed whether or not a
  curation doc gets written.
