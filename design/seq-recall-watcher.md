# Sequence: Recall watcher turn-boundary flow (v2)

**Requirements:** R2696, R2705, R2706, R2708, R2711, R2712, R2713,
R2729, R2730, R2731, R2732, R2733, R2734, R2735, R2736, R2739,
R2740, R2741, R2746, R2747, R2748, R2753, R2754,
R2806, R2867, R2868, R2869, R2898, R2901, R3009

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
         ├── 2.3  activation gate (R2867, R3279, R3294) — TWO INDEPENDENT
         │          gates, one per level; a session secretary is part of
         │          NEITHER, since both levels can be served by a pool:
         │            bloodhoundEnabled = SubscriberCount("ark-bloodhound-result", sessionID) > 0
         │            ambientEnabled    = SubscriberCount("ark-recall-result", sessionID) > 0
         │          if !bloodhoundEnabled && !ambientEnabled:
         │            return   (nothing at all to produce; reactivates at
         │                      JSONL end, R2868)
         │          then, under the lock:
         │            if !ambientEnabled: stop any armed pendingTimer and
         │              clear pendingChunks — the recall-result sub going
         │              away is what means "stop accumulating"; a missing
         │              secretary tears down nothing (R3294).
         │
         ├── 2.4  lock sessions[sessionID]
         │
         ├── 2.5  append `added` to pendingChunks                 (R2730)
         │
         ├── 2.6  for each line in newBytes:                      (R2731, R2732)
         │           obj = json.Unmarshal(line)
         │           if obj.type == "user" && genuine(obj):        (R2732, R3009)
         │             // genuine = string content && origin.kind=="human"
         │             //   (excludes tool-results, notifications, caveats;
         │             //    origin-absence is NOT genuine, R3009)
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
         ├── 5.3  fire = watcher.nextFire(sessionID)              (R2901)
         │        (per-session counter; seeded max+2 from the
         │         curation-dir on first use, then in-memory)
         │        cfg = db.Config().Recall                        (R2695)
         │
         ├── 5.4  subscriber backstop (R2806): re-check ambientEnabled,
         │          i.e. SubscriberCount("ark-recall-result", sessionID)
         │          ALONE (R3294). A session-local secretary is NOT part
         │          of this gate — who serves is decided later, at 6.x.
         │          if == 0 (consumer dropped during activation_delay):
         │          append recall.jsonl record with
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
         │            paraExcerpt = truncateUTF8(para, 200)       (R2898)
         │            sections.append({sourceCID: cid,
         │                             paraExcerpt,
         │                             candidates: result.Chunks})
         │
         ├── 5.6  if len(sections) == 0:                          (R2753)
         │          log fired with sections-emitted=0; return
         │
         ├── 5.7  b = db.RecallCurationOpen(sessionID, fire)      (R2754, R2869)
         │        for each section in sections:
         │          srcPath, srcRange = ChunkInfo(section.sourceCID)
         │          b.Section(srcPath, srcRange,                   (R2898)
         │                    section.paraExcerpt)
         │          for each c in section.candidates:
         │            tagOnly = sessionFromJSONLPath(c.Path)       (R2869)
         │                      == sessionID  // own-session → no surface
         │            b.Candidate(c.Path, c.Range,                 (R2898)
         │                        c.Score, c.Tags,
         │                        c.ProposedTagsWithScores,
         │                        c.Content, tagOnly)
         │        b.Close()                                       (R2747, R2748)
         │        → writes tmp://ARK-RECALL/curation-
         │            <sessionID>-<fire>
         │          path = the doc's ORIGIN; the @ark-secretary-work
         │          head tag = its ADDRESS, chosen by routing at 6.x
         │          (R3302, R3303) — not necessarily <sessionID>
         │          (write actor publishes pubsub event for
         │           subscribers to @ark-secretary-work)
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
- FIRING allocates the next fire number; the fire counter is
  per-session (R2901), seeded from the curation-dir on first
  use, and is consumed whether or not a curation doc gets written.

## Flow 4: bloodhound recognition (R2934–R2937, R3279–R3282)

Independent of the arm/fire state machine above, in the same `OnAppend`
pass. A watermark is recognized while `@ark-bloodhound-result=<S>` is
subscribed — **the secretary is not part of the recognition gate** (R3279),
because a session without one can borrow Luhmann's pool (R3280):

- `scanBloodhounds(newBytes)` walks each `type:"assistant"` line, pulls
  its text via `assistantText`, and regex-matches
  `<BLOODHOUND>(.*?)</BLOODHOUND>` (non-greedy, DOTALL). Each capture is
  one payload — deterministic, once-only (newBytes is the newly-appended
  slice).
- For each payload: allocate `<B>` from `bloodhoundCounters` under the
  lock and post `dispatchBloodhound(session, B, payload)` to `jobs`.
  Nothing touches `pendingTimer`/`armReady`/`pendingChunks` (R2935), so a
  watermark neither arms nor suppresses a curation fire.
- `dispatchBloodhound` (worker goroutine): re-check the **bloodhound gate**
  (`ark-bloodhound-result` subscribed, R3279) as the write-time backstop,
  then **seed the hunt** — run `librarian.Recall({Text: payload})`
  (clue-only, `Session`/`Propose` off, R3006/R3007) and
  `renderBloodhoundSeed` the result into a compact locator list — then
  **select the server** (R3280):
  - **Session secretary present** — `RecallBloodhoundOpen(session, B,
    payload, seed)` writes `tmp://ARK-BLOODHOUND/task-<S>-<B>` (tag
    `@ark-secretary-work=<S>`, body = `## Search task` payload + `## Recall
    seed` + search crank handle) and retains the clue. The write actor
    publishes the secretary-work event → wakes the secretary's `next`
    (seq-recall-agent.md, Flow 6).
  - **No session secretary, pool available** — route through the Fixer's
    enhance-and-route path (seq-bloodhound-cli.md#1 / R3023): re-tag to a free pool
    secretary's `@ark-secretary-work=<pool-sec>`, or push a *stand up
    another secretary* directive onto Luhmann's `next` when the pool has
    room. The return is **raw** — relayed to `@ark-bloodhound-result=<S>`
    with no Luhmann curation push (R3281).
  - **Neither available** — emit the not-served `## Finding:` doc on
    `@ark-bloodhound-result=<S>` naming the failed condition (R3282). Never
    a silent drop.

## Flow 5: ambient routing — choosing the doc's address (R3300–R3304)

Runs inside `fire`, at the point the curation doc is written (5.7). The doc's
**origin** is fixed by its path; this flow decides its **address**. Distinct
from Flow 4, which routes a *watermark*: that path has an assistant blocked on
`listen`, this one has nobody, so its failure modes differ.

```
6. RecallWatcher.routeCuration(origin, fire)
         │
         ├── 6.1  if SubscriberCount("ark-secretary-work", origin) > 0:  (R3303 rule 1)
         │          address = origin  → the session's own secretary
         │          no pool slot is consumed; return
         │
         ├── 6.2  else if no Luhmann seated (luhmannOwner == ""):        (R3304)
         │          emit the unservable result on
         │            @ark-recall-result=<origin> naming the failed
         │            condition; drop the curation doc; return.
         │          NOT silence — the remedy belongs to the session.
         │
         ├── 6.3  else consult pools["recall"]:                          (R3303 rule 2)
         │         │
         │         ├── 6.3.1  free secretary found:
         │         │            address = <luhmann-session>-<nonce>
         │         │            mark busy + clear idleSince              (R3300)
         │         │            re-tag doc's @ark-secretary-work
         │         │
         │         ├── 6.3.2  none free, len(roster) < class.recall.pool_max:
         │         │            push "stand up another" (class=recall)
         │         │            onto Luhmann's next; leave doc pending;
         │         │            route on registration                    (R3301)
         │         │
         │         └── 6.3.3  none free, roster at pool_max:
         │                      leave doc pending. DEFERRAL, not
         │                      unservability — no report (R3304).
         │
         └── 6.4  on `recall close` for the <origin>-<fire> token:       (R3300)
                    free the holding secretary into cooldown, so prune
                    never reads a curating secretary as idle.
```
