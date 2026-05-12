# Sequence: tmp:// Subscription

**Requirements:** R2276–R2312

The flow from a sidecar (or any internal writer) updating a
tmp:// document, through centralized publish + only-on-change
diff, into a Lua callback fired by the listening goroutine.

## Subscribe / OnPublish (one-time, at app init)

```
Curation app          Server (Lua bridge)             PubSub
  |                       |                            |
  |- mcp.onpublish(       |                            |
  |    "curation", cb) -->|                            |
  |                       |- onpublishCallbacks        |
  |                       |  ["curation"] = cb         |
  |                       |                            |
  |- mcp.subscribe(       |                            |
  |   "curation", {       |                            |
  |   tag="connections-status",                        |
  |   filterFiles={...},  |                            |
  |   ...}) ------------->|                            |
  |                       |- Cancel("curation",        |
  |                       |   "connections-status","")------>|
  |                       |- Subscribe("curation",     |
  |                       |   [newSub]) -------------->|
  |                       |                            |- create queue
  |                       |                            |- append sub
  |                       |- listenLoops["curation"] = stopCh
  |                       |- go runListenLoop(         |
  |                       |     "curation")            |
  |<--- OK ---------------|                            |
```

## Publish on tmp:// write (centralized via DB layer)

```
Internal writer        DB (actor)                     PubSub
(sweep, sidecar,         |                              |
 watchdog, ...)          |                              |
  |                      |                              |
  |- db.UpdateTmpFile(   |                              |
  |   path, strategy,    |                              |
  |   content) --------->|                              |
  |                      |- microfts2.UpdateTmpFile     |
  |                      |  (indexed-chunk callback)    |
  |                      |- Store.UpdateTagValues       |
  |                      |  (replace overlay entries)   |
  |                      |  (actor write commits)       |
  |                      |                              |
  |                      |- pubsub.PublishTmpDiff(      |
  |                      |   writerID, path, content,   |
  |                      |   strategy) ---------------->|
  |                      |                              |- tags := ExtractTagValues(content, strategy)
  |                      |                              |- prev := tagSetCache[path]
  |                      |                              |- changed := diff(prev, tags)
  |                      |                              |- if len(changed) == 0: return
  |                      |                              |- Publish(writerID, path, changed)
  |                      |                              |- tagSetCache[path] = tags
  |<---------------------|                              |
```

`db.AddTmpFile` follows the same path with `prev = {}` (every
tag is a change on first write). `db.AppendTmpFile` calls
`PublishTmpDiff` with the **whole resulting content** so prior
tags don't re-fire.

## Subscriber receives compressed batch

```
                       PubSub                Server (listen goroutine)         Lua VM
                         |                          |                            |
(Publish enqueues        |- queue["curation"] <- evt|                            |
 to per-session chan)    |                          |                            |
                         |                          |- Listen("curation", t) ----+
                         |- block on chan           |  (blocks)                  |
                         |                          |                            |
(more events arrive,     |                          |                            |
 chan accumulates)       |                          |                            |
                         |                          |                            |
                         |- chan -> []Event         |                            |
                         |- return batch -----------|                            |
                         |                          |                            |
                         |                          |- compressed :=             |
                         |                          |    pubsub.CompressBatch(   |
                         |                          |      events)               |
                         |                          |  // (path, tag) -> latest  |
                         |                          |                            |
                         |                          |- uiRuntime.WithLua(        |
                         |                          |    func(rt) {              |
                         |                          |- L := rt.State             |
                         |                          |  cb := callbacks[          |
                         |                          |        "curation"]         |
                         |                          |  arr := buildEventArray(   |
                         |                          |        L, compressed)      |
                         |                          |  L.CallByParam(            |
                         |                          |    {cb, NRet:0,            |
                         |                          |     Protect:true}, arr)----+
                         |                          |    }) -------------------->|- cb(events)
                         |                          |                            |  for _,e in ipairs(events):
                         |                          |                            |    handle e
                         |                          |                            |  (UI rebinds once)
                         |                          |<-- WithLua returns --------|
                         |                          |                            |
                         |                          |- loop back to Listen ------+
```

**Compression operates on Go structs.** `pubsub.CompressBatch`
returns `[]Event` with one entry per `(path, tag)` (latest
value). The Lua event-array table is built only from the
survivors, inside `WithLua`. Discarded events never allocate
Lua tables.

## Re-subscribe (replace-by-(session, tag))

```
Curation app          Server (Lua bridge)             PubSub
  |                       |                            |
(user clicks Find         |                            |
 Connections again        |                            |
 with new request ID)     |                            |
  |- mcp.subscribe(       |                            |
  |   "curation", {       |                            |
  |   tag="connections-status",                        |
  |   filterFiles={       |                            |
  |     "tmp://connections/req2.md"                    |
  |   }}) -------------->|                            |
  |                       |- Cancel("curation",        |
  |                       |   "connections-status", "")|
  |                       |  ----------------------->  |- drop prior sub on tag
  |                       |- Subscribe("curation",     |
  |                       |   [newSub]) -------------->|- append new sub
  |                       |                            |
  |   (listenLoop running, |                           |
  |    callback unchanged) |                           |
  |<--- OK ---------------|                            |
```

Workshop intent ("drop old, follow new") is the natural
behavior. An event for the old request that landed between
Cancel and Subscribe is silently dropped — that matches the
user's intent.

## Cancel last sub (lifecycle stop)

```
Curation app          Server (Lua bridge)             PubSub
  |                       |                            |
  |- mcp.cancel(          |                            |
  |   "curation", "") --->|                            |
  |                       |- Cancel("curation",        |
  |                       |   "", "") ---------------->|- drop all subs
  |                       |                            |- close queue
  |                       |                            |- delete state
  |                       |- close(listenLoops[        |
  |                       |   "curation"])             |
  |                       |- delete onpublishCallbacks |
  |                       |  ["curation"]              |
  |   (goroutine exits    |                            |
  |    on close detect)   |                            |
  |<--- OK ---------------|                            |
```

## Cross-session admin (Ark monitor)

```
Ark monitor           Server (Lua bridge)             PubSub
  |                       |                            |
  |- mcp.onpublish(       |                            |
  |    "monitor.curation",|                            |
  |    cb) -------------->|                            |
  |                       |- onpublishCallbacks        |
  |                       |  ["monitor.curation"] = cb |
  |- mcp.subscribe(       |                            |
  |  "monitor.curation",  |                            |
  |  {tag="*", ...}) ---->|                            |
  |                       |- Subscribe(...)            |
  |- mcp.subscribe(       |                            |
  |  "monitor.sweep",     |                            |
  |  {...}) ------------->|                            |
  |  (multiple sessions   |                            |
  |   owned by one app)   |                            |
```

The Ark monitor registers per-watched-session onpublishes in
its own VM. Each session's listening goroutine dispatches into
the same monitor LState. Combined with `PubSub.QueueDepth(sid)`
and `PubSub.LastListenAt(sid)`, the monitor renders rich
diagnostics without polling.
