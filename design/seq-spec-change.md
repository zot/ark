# Spec-change detection and audit append

How the indexer's `EnsureUpcoming` pass notices a source-file spec
change and appends a `@ark-event-spec-changed:` marker to the
schedule audit log, without disturbing the queue's idempotent
re-arming. The current spec lives in the source file; the log
preserves history.

```
1. Indexer pass on a scheduled source file
1.1. Indexer.RefreshFile / Scan      → parses `~/notes/schedule.md`, finds `@standup: every Tuesday at 09:00`
1.2. Indexer.AccumulateSchedule      → appends `{tag: "standup", value: "every Tuesday at 09:00", source: "~/notes/schedule.md"}` to pendingSchedule
1.3. Indexer.DrainSchedule           → after scan/refresh completes, returns the accumulated items
1.4. Server.processScheduleItems     → for each item, calls scheduler.EnsureUpcoming(tag, value, source)

2. EnsureUpcoming inspects current audit state
2.1. EventScheduler.EnsureUpcoming   → reads Config.IsSuppressed(tag); if true, no-op. (R2835)
2.2. EventScheduler.EnsureUpcoming   → reads Config.Lifecycle(tag) → dest in {"disk","tmp","none"}. (R2822)
2.3. EventScheduler.EnsureUpcoming   → if dest == "none": skip log read/write entirely, go to 4 (queue Add). (R2825)
2.4. EventScheduler.EnsureUpcoming   → reads existing chunk for (tag, source):
     -  if dest == "disk": ReadLogFile(~/.ark/schedule/HASH.md). (R2823)
     -  if dest == "tmp":  ReadTmpFile(tmp://schedule/TAG/SOURCE-ENCODED). (R2824)
     - missing chunk: chunkExists = false.

3. Detect spec change OR new chunk
3.1. EventScheduler.EnsureUpcoming   → if !chunkExists: append `@ark-event:` `@ark-event-source:` `@ark-event-spec-initial: NOW — value` and bounds tags. (R2815)
3.2. EventScheduler.EnsureUpcoming   → else: cur = currentSpec(chunk)  ← walks SpecMarkers, returns latest.Spec. (R2817)
3.3. EventScheduler.EnsureUpcoming   → if cur != value: appendSpecMarker(chunk, "changed", NOW, value); rewrite bounds tags if changed. (R2816)
3.4. EventScheduler.EnsureUpcoming   → if cur == value: no log mutation.
3.5. EventScheduler.EnsureUpcoming   → writeAuditChunk(chunk, dest). (R2823, R2824)

4. Arm the priority queue (always — regardless of dest, regardless of mutation)
4.1. EventScheduler.EnsureUpcoming   → next = ComputeNext(value, max(fired-max, now), bounds, exceptions). (R2820)
4.2. EventScheduler.EnsureUpcoming   → Add(ScheduledEvent{ID: eventID(source, tag, next), Tag: tag, Value: nextStr, Path: source, NextFire: next, Recurring: value}). (R2820, R2826)
4.3. EventScheduler.Add              → idempotent per-ID; replaces any existing queue entry for the same eventID. (R808, R809, R2809)
```

Notes:

- The active spec is `value` — what the indexer just read from the
  source file. The log markers preserve history; queue arming uses
  `value` directly without consulting the log's spec memory.
- Step 3.2 is "what was the spec last time we recorded it?" — used
  to decide if a marker append is needed. It's NOT used to compute
  the next fire (that's `value` from step 1).
- Step 4 runs unconditionally — every `EnsureUpcoming` call re-arms
  the queue. The `Add` is idempotent (R808/R809/R2809) so re-running
  on an unchanged spec just replaces the existing queue entry with
  an equivalent one. Cheap.
- For lifecycle="none" (3.0 branch), we never read or write a log
  chunk — the queue is the only state. Fire delivers and re-enqueues
  per `fire()`'s Recurring logic; no audit trail accumulates.
