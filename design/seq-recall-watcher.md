# Sequence: Recall watcher turn-boundary flow

**Requirements:** R2696, R2700, R2701, R2702, R2703, R2704, R2705,
R2706, R2707, R2708, R2711, R2712, R2713, R2729, R2730, R2731,
R2732, R2733, R2734, R2735, R2736, R2737, R2738, R2739, R2740,
R2741

The watcher hooks into `indexer.executeRefresh`'s isAppend
branch. OnAppend is synchronous on the indexer's goroutine —
fast: a JSON line-scan plus a pending-chunks append. The
heavy work (substrate Recall, DM compose, RD writes) runs on
the timer-expiry closure-actor goroutine.

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
         ├── 2.3  lock sessions[sessionID]
         │
         ├── 2.4  append `added` to pendingChunks                 (R2730)
         │
         ├── 2.5  for each line in newBytes:                      (R2731, R2732)
         │           obj = json.Unmarshal(line)
         │           if obj.type == "user":
         │             cancel pendingTimer                        (R2733)
         │           else if obj.type == "system"
         │                && obj.subtype == "turn_duration":
         │             cancel any existing pendingTimer
         │             pendingTimer = time.AfterFunc(             (R2734)
         │               activation_delay seconds,
         │               func() { post fire(sessionID) to
         │                        jobs channel })
         │             lastTurnDurationChunkID = (chunkID of
         │               the indexed chunk containing this line,
         │               if any)
         │
         └── 2.6  unlock; return
```

## Flow 2: timer expiry → recall pipeline

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
         ├── 5.3  cfg = db.Config().Recall                        (R2695)
         │
         ├── 5.4  sections = []
         │        for each cid in snapshot:                       (R2736)
         │          result = librarian.Recall(
         │            [{ChunkID: cid}],
         │            RecallOpts{
         │              K: cfg.EffectiveChunksPerDM(),
         │              IncludeContent: true,
         │              Session: sessionID,
         │              Propose: cfg.EffectivePropose(),
         │            })
         │          if err: log recall-error; continue
         │          if len(result.Chunks) == 0
         │             or result.Chunks[0].Score
         │                < cfg.EffectiveMinSimilarity():
         │            continue                                    (R2708, R2739)
         │          for r in result.Chunks:
         │            r.Content = truncateUTF8(r.Content, 500)    (R2705)
         │          inputExcerpt = truncateUTF8(
         │            chunkText(cid), 200)                        (R2738)
         │          sections.append({cid, inputExcerpt,
         │                           recalled: result.Chunks})
         │
         ├── 5.5  if len(sections) == 0:                          (R2739, R2740)
         │          log fired with sections-emitted=0; return
         │
         ├── 5.6  body = composeBody(sections,
         │          ref = lastTurnDurationChunkID or now-nanos)   (R2702, R2737)
         │
         ├── 5.7  composeDM(                                      (R2700, R2701)
         │          sender = {Service: "ARK-RECALL"},
         │          recipients = [sessionID],
         │          subject = "recall",
         │          body)
         │        → @dm: <sessionID>: recall
         │          @from-service: ARK-RECALL
         │        → POST /tmp/append at
         │          tmp://ARK-RECALL/dm-<sessionID>
         │
         ├── 5.8  mark-on-send: for each section.recalled[*]:     (R2711, R2712, R2740)
         │          for each (tag, value) in chunk.Tags:
         │            store.AddDiscussed(sessionID, tag, value)
         │
         └── 5.9  log fired with sections-emitted=N, dropped=M,   (R2713)
                  discussed-records=K
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
            │ DM emitted (or all        │
            │ sections dropped silently)│
            │                           ▼
            └─────────────────── ┌──────────┐
                                 │  FIRING  │
                                 └──────────┘
```

- pendingChunks accumulate in IDLE and ARMED equally; only
  FIRING clears them.
- user-record in IDLE is a no-op (no timer to cancel,
  pendingChunks keep accumulating).
- A new turn_duration while ARMED resets the deadline.

## Flow 4: receiving agent action (out-of-band, informational)

The receiving Claude Code session, listening on its own
`@dm: <self>` subscription, reads the appended chunk and
decides whether to surface. Layer 4 instrumentation expects
the agent to append `@ark-recall-acted: surfaced|dropped|skipped`
to the same tmp:// document; the watcher does not enforce
this.
