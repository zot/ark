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
 |   path, tags, content) |
 |                        |-- for each tag:
 |                        |     for each session != writerID:
 |                        |       for each sub matching tag name:
 |                        |         check ValueRE against tag value
 |                        |         check FilterFiles against path
 |                        |         check ExceptFiles against path
 |                        |         non-blocking send Event to queue
 |                        |         (drop if full)
 |<-----------------------|
```

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
