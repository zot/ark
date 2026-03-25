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

## Index-time: Day-Bucket Materialization

```
Indexer              Store
 |                     |
 |-- (log file re-indexed after mutations)
 |                     |
 |-- parse @ark-event-upcoming: and
 |   @ark-event-fired: entries
 |                     |
 |-- discretize into days
 |                     |
 |-- WriteDayBuckets ->|
 |                     |-- ClearDayBuckets(fileid)
 |                     |   read TF|fileid → dates
 |                     |   delete TD|date|fileid|*
 |                     |   delete TF|fileid
 |                     |
 |                     |-- write new TD entries
 |                     |-- write new TF entry
 |<--------------------|
```

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
 |   (day buckets updated)                   |
 |                     |                     |
 |-- re-enqueue        |                     |
 |-- reset timer       |                     |
```

## Re-index: Day-Bucket Cleanup

```
Indexer              Store
 |                     |
 |-- RefreshFile       |
 |   (log file changed)|
 |                     |
 |-- ClearDayBuckets ->|
 |   (fileid)          |-- read TF|fileid → [dates]
 |                     |-- delete TD|date|fileid|*
 |                     |-- delete TF|fileid
 |<--------------------|
 |                     |
 |-- WriteDayBuckets ->|
 |   (from @ark-event-upcoming:              |
 |    and @ark-event-fired: entries)         |
 |                     |-- write new TD+TF   |
 |<--------------------|
```

## Calendar Query (Lua)

```
Lua                 Server              Store
 |                     |                  |
 |-- mcp:scheduled  ->|                  |
 |   ("2026-03-01",   |                  |
 |    "2026-03-31")   |                  |
 |                     |-- QueryDayBuckets|
 |                     |   (start, end)->|
 |                     |            <----|-- seek TD|20260301
 |                     |                 |   scan to TD|20260331
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
