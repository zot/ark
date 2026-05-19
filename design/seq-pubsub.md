# Sequence: Tag Pubsub

## Subscribe Flow

```
CLI                     Server                  PubSub
 |                        |                       |
 |-- POST /subscribe ---->|                       |
 |   {session, subs}      |-- Subscribe(id,subs)->|
 |                        |                       |-- create queue chan if needed
 |                        |                       |-- append subs to session's list
 |                        |                       |-- reset lastListen
 |<-- 200 OK ------------|<-- ok -----------------|
```

## Cancel Flow

```
CLI                     Server                  PubSub
 |                        |                       |
 |-- POST /subscribe ---->|                       |
 |   {session, cancel,    |-- Cancel(id,tag,val)->|
 |    tag?, value?}       |                       |-- match and remove subs
 |                        |                       |-- if no subs left: close queue
 |<-- 200 OK ------------|<-- ok -----------------|
```

## Publish Flow (on tag extraction)

```
Indexer                 PubSub
 |                        |
 |-- AppendFile --------->|  (tag extraction completes)
 |   store.AppendTags()   |
 |                        |
 |-- Publish(writerID, -->|
 |   path, fileID,        |
 |   tags, fileTags)      |
 |                        |-- @mute: true on file? → return
 |                        |
 |                        |-- Pass 1: Tag-kind subs (R2457, R2459)
 |                        |   for each tv in tags:
 |                        |     for each session != writerID:
 |                        |       for each sub.Kind==Tag where
 |                        |         sub.Predicate.Match(tv):
 |                        |         check FilterFiles/ExcludeFiles
 |                        |         non-blocking send Event (drop if full)
 |                        |
 |                        |-- Pass 2: FileTag-kind subs (R2460-R2471)
 |                        |   for each session != writerID:
 |                        |     for each sub.Kind==FileTag:
 |                        |       wasMember = sub.FileTagMembers[fileID]
 |                        |       isMember  = anyMatch(sub.Predicate, fileTags)
 |                        |       (see Membership Transition Table below)
 |<-----------------------|
```

## File-Tag Membership Transition Table

`fileTags` is the authoritative per-file tag aggregate, computed
by the indexer after `store.AppendTags` settles — never the local
delta from the just-indexed chunk (R2464). Removing a tag from one
chunk does not imply the file lost the tag if another chunk in the
file still carries it.

```
   wasMember  isMember   action
  ----------  ---------  --------------------------------------------
       N          N      no-op (R2468)
       N          Y      add fileID to FileTagMembers
                         deliver chunk as entry event (R2465)
                         (no backfill of prior chunks)
       Y          Y      deliver chunk (R2466)
       Y          N      remove fileID from FileTagMembers
                         deliver chunk as exit event (R2467)
```

Self-notification rule (R2471) and `@mute: true` short-circuit
(R2470) apply just as they do to Tag-kind subs.

## Subscribe-FileTag Initial Bootstrap

```
CLI                     Server                  PubSub
 |                        |                       |
 |-- POST /subscribe ---->|                       |
 |   subs[i].Kind=FileTag |-- Subscribe(id,subs)->|
 |                        |                       |-- create queue chan if needed
 |                        |                       |-- for each FileTag sub:
 |                        |                       |     FileTagMembers = {}
 |                        |                       |     (no backfill — empty start)
 |                        |                       |-- append subs to session
 |                        |                       |-- reset lastListen
 |<-- 200 OK ------------|<-- ok -----------------|
```

The set starts empty (R2469). The first chunk indexed on each
matching file populates the set via the `was=N, is=Y` transition.
On server restart, every membership set re-initializes empty —
the next stream of indexing events repopulates them.

## Listen Flow

```
CLI                     Server                  PubSub
 |                        |                       |
 |-- GET /listen -------->|                       |
 |   ?session=X           |-- Listen(id, 120s) -->|
 |   &timeout=120         |                       |-- select on queue chan or timeout
 |                        |                       |   ... blocks ...
 |                        |                       |-- event arrives on chan
 |                        |                       |-- drain remaining (non-blocking)
 |                        |                       |-- update lastListen
 |                        |<-- []Event -----------|
 |                        |                       |
 |                        |-- FormatMarkdown() -->|
 |                        |<-- markdown ----------|
 |                        |                       |
 |<-- markdown -----------|                       |
 |                        |                       |
 |   (agent processes,    |                       |
 |    loops back)         |                       |
```

## Event Scheduler Fire Flow

```
EventScheduler          PubSub
 |                        |
 |-- timer fires -------->|
 |   pop head event       |
 |                        |
 |-- for each listening session:
 |     check push record  |
 |     if not pushed:     |
 |       enqueue Event -->|-- non-blocking send to queue
 |       mark pushed      |
 |                        |
 |-- if recurring:        |
 |     computeNext()      |
 |     re-enqueue         |
 |                        |
 |-- reset timer to       |
 |   new head             |
```

## Startup Flow

```
Server                  EventScheduler          PubSub
 |                        |                       |
 |-- (after reconcile) -->|                       |
 |   ScanTimeTags(store)  |                       |
 |                        |-- query @event:,      |
 |                        |   @birthday:,         |
 |                        |   @recurring: tags    |
 |                        |                       |
 |                        |-- compute next fire   |
 |                        |   for each, build     |
 |                        |   priority queue      |
 |                        |                       |
 |                        |-- AddChime() (15m)    |
 |                        |                       |
 |                        |-- fire any overdue    |
 |                        |   events (check push  |
 |                        |   records)            |
 |                        |                       |
 |                        |-- set timer to head   |
 |                        |                       |
 |-- start reaper ------->|                       |
 |   ticker (1m)          |               Reap()->|
```

## TTL Reap Flow

```
Server ticker           PubSub
 |                        |
 |-- (every 1 minute) -->|
 |   Reap()              |
 |                        |-- scan lastListen map
 |                        |-- for each session where
 |                        |   now - lastListen > ttl:
 |                        |     close queue channel
 |                        |     delete subs
 |                        |     delete lastListen
 |                        |     delete queue
```
