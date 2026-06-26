# Sequence: Scheduling

## Index-time: Schedule Log Maintenance

```
Indexer              Config            EventScheduler
 |                     |                    |
 |-- AddFile/Refresh   |                    |
 |   (tags extracted)  |                    |
 |                     |                    |
 |-- for each tag:     |                    |
 |   IsScheduleTag? -->|                    |
 |              <--ok--|                    |
 |                     |                    |
 |-- EnsureUpcoming ---------------------->|
 |   (tag, value, path)|                    |
 |                     |                    |-- find/create log file
 |                     |                    |   in ~/.ark/schedule/
 |                     |                    |-- find/create chunk with
 |                     |                    |   @ark-event: tag
 |                     |                    |   @ark-event-source: path
 |                     |                    |   @ark-event-spec: value
 |                     |                    |-- compute occurrences
 |                     |                    |   through forward window
 |                     |                    |-- append @ark-event-upcoming:
 |                     |                    |   (skip if already exists)
 |<-------------------------------------------------|
```

## Index-time: Priority-queue arming

```
Indexer              EventScheduler
 |                     |
 |-- (log file re-indexed after mutations)
 |                     |
 |-- parse @ark-event-upcoming: entries
 |   (WriteDateIndex, gated by schedule scope)
 |                     |
 |-- EnsureUpcoming(tag, value, path) ->|
 |                     |-- crank forward from the recurrence spec,
 |                     |   enqueue the next ScheduledEvent in the
 |                     |   in-memory priority queue (no DB records)
 |<--------------------|
```

(Earlier the indexer materialized TD/TF day-bucket records in the
Store; event management is no longer in the DB — see
schedule-record-only.md.)

## Startup: Scheduler Population

```
Server             EventScheduler       (log files)
 |                     |                     |
 |-- StartScheduler -->|                     |
 |                     |                     |
 |                     |-- scan ~/.ark/schedule/
 |                     |   for each log file:
 |                     |   read @ark-event-upcoming:
 |                     |                 --->|
 |                     |              <------|
 |                     |                     |
 |                     |-- for each upcoming |
 |                     |   in the past:      |
 |                     |   convert to        |
 |                     |   @ark-event-fired: |
 |                     |   compute next from |
 |                     |   @ark-event-spec:  |
 |                     |   append upcoming   |
 |                     |   (if not exists)   |
 |                     |   re-index log file |
 |                     |                     |
 |                     |-- enqueue upcoming  |
 |                     |   events            |
 |                     |                     |
 |                     |-- AddChime()        |
 |                     |                     |
 |                     |-- fire overdue      |
 |                     |-- set timer         |
 |<--------------------|                     |
```

## Fire: Event Delivery + Crank-Forward

```
EventScheduler       PubSub             (log file)
 |                     |                     |
 |-- timer fires       |                     |
 |   pop head event    |                     |
 |                     |                     |
 |-- Publish(event) -->|                     |
 |   (IsScheduledFire) |-- deliver to        |
 |                     |   listening sessions|
 |<--------------------|                     |
 |                     |                     |
 |-- convert @ark-event-upcoming:            |
 |   → @ark-event-fired: ----------->|
 |                     |                     |
 |-- if recurring:     |                     |
 |   compute next      |                     |
 |   append @ark-event-upcoming:     |
 |   (if not exists) ----------->|
 |                     |                     |
 |-- re-index log file |                     |
 |   (priority queue re-armed)               |
 |                     |                     |
 |-- re-enqueue        |                     |
 |-- reset timer       |                     |
```

## Re-index: Priority-queue re-arm

```
Indexer              EventScheduler
 |                     |
 |-- RefreshFile       |
 |   (log file changed)|
 |                     |
 |-- EnsureUpcoming -->|
 |   (tag, value, path)|-- crank forward from the recurrence spec,
 |                     |   replace the file's queued ScheduledEvent
 |                     |   (no DB records to clear/write)
 |<--------------------|
```

## Calendar Query (Lua)

```
Lua                 Server              EventScheduler
 |                     |                  |
 |-- mcp:scheduled  ->|                  |
 |   ("2026-03-01",   |                  |
 |    "2026-03-31")   |                  |
 |                     |-- QueryRange    -|
 |                     |   (start, end)->|
 |                     |            <----|-- read schedule logs,
 |                     |                 |   crank forward from specs
 |                     |                 |
 |<-- [{date, endDate,|                 |
 |     tag, summary,  |                 |
 |     path, ...}]    |                 |
```

## Gap Detection

```
Franklin            Store              (source file)
 |                     |                     |
 |-- query             |                     |
 |   @ark-event-fired: |                     |
 |   from log -------->|                     |
 |              <------|                     |
 |                     |                     |
 |-- query @ack:       |                     |
 |   from source ----->|                     |
 |              <------|                     |
 |                     |                     |
 |-- compare fired     |                     |
 |   dates vs ack      |                     |
 |   dates             |                     |
 |   gaps = missed     |                     |
```
