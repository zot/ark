# Chimes

How the six standard chime tags get scheduled, fire, and reach
subscribers. The flow rides the existing schedule infrastructure end
to end — the only chime-specific code is the value-override branch in
`fire()`.

## Startup: ensure hosting file exists

```
1. Server.Serve()                       → existing startup sequence (BindSocket, WritePID, ...)
1.1. Server                             → EnsureChimesFile(dbPath)
1.1.1. EnsureChimesFile                 → stat `~/.ark/chimes.md`
1.1.2. EnsureChimesFile                 → exists → return; missing → write canonical 6 lines (`@chime-1m: every 1m` ... `@chime-60m: every 60m`)
1.2. Server                             → existing scheduler.ScanScheduleLogs (reads any prior schedule logs; chimes.md is indexed as a regular ark file in step 1.3 below)
1.3. Server                             → reconcile → indexer scans → ~/.ark/chimes.md indexed → pendingSchedule accumulates one item per chime tag
1.4. Server.processScheduleItems        → for each pending item, scheduler.EnsureUpcoming(tag, value, sourcePath)
1.5. EventScheduler.EnsureUpcoming      → crankForward(chunk, now, true) — writes log file with @ark-event-upcoming AND enqueues the next chime occurrence in the priority queue (R2778, R2809)
```

## Fire: chime tick → subscribers

```
2. Timer fires (existing path, see seq-pubsub.md / seq-scheduling.md)
2.1. EventScheduler.fire()              → pop head of queue
2.2. EventScheduler.fire                → isChimeTag(event.Tag)? → override event.Value with time.Now().UTC().Format(time.RFC3339) (R2778)
2.3. EventScheduler.fire                → PubSub.Publish (existing) with the (maybe-overridden) value
2.4. PubSub.Publish                     → match against subscribers, enqueue per-session events
2.5. EventScheduler.fire                → for chime tags: re-enqueue via ComputeNext directly (bypass fireLogMutate so log files don't accumulate @ark-event-fired entries). For non-chime lifecycle tags: fireLogMutate as before.
2.6. EventScheduler.fire                → resetTimer
```

## Consume: subscriber receives chime

```
3. Subscriber session
3.1. Consumer (skill / Lua / agent)     → `ark subscribe --session <sid> --tag chime-45m` (existing path, plain — no `--scheduled` / `--recurring`)
3.2. Consumer                           → `ark listen --session <sid>` (existing)
3.3. PubSub.Listen                      → block; on chime tick, return one Event{Tag:"chime-45m", Value:"<RFC 3339 now>", Path:"~/.ark/chimes.md", Time:<now>}
3.4. Consumer                           → handle the tick (e.g. Luhmann appends a keepalive record to its supervisor log to refresh the prompt cache)
```

The Luhmann use case (R2797–R2801): the orchestrator session
subscribes to `chime-45m` so its prompt cache stays warm under the
1-hour TTL; the handler is trivial (a turn that does almost
nothing) but the act of generating the turn refreshes the cache.

## Duplicate-source defense

If some indexed file contains a literal `@chime-Nm: every Nm` line
(notably scheduler.go's `chimesFileContent` constant), EnsureUpcoming
would arm a second event from that source path. The defense is at
config level via `[schedule].exclude_files` — the indexer's
`MatchesScheduleFilterForTag` consults the exclude before queuing
the schedule item. No in-scheduler special-case is needed.

The retirement of `AddChime()` (R810 → R2783) leaves no
special-case code path for chime arming; only the fire-time
value override remains chime-specific.
