# Sequence: Find Connections

**Requirements:** R2313–R2343

End-to-end flow from the curation view clicking "Find Connections"
through the `ark-connections` sidecar to the workshop rebinding on
the terminal status transition. Three pieces in motion:

- **Curation view** (Lua, Frictionless): calls
  `mcp.findConnections`, subscribes via the Subtask 0 primitives.
- **Server (Librarian + bridge)**: orchestrates the request,
  writes the tmp:// doc through the write actor, enforces timeout.
- **ark-connections sidecar**: Haiku in a lotto-tube loop using
  `ark connections --wait` / `--fetch` / `--result` / `--error`.

## Happy Path

```
Curation       Server (Lua bridge       Server (Librarian            Server (write actor       Server (PubSub +     ark-connections
view (Lua)     + HTTP handlers)         orchestrator)                + DB)                     listen goroutine)    sidecar (Haiku)
  |                  |                          |                            |                          |                  |
  |- mcp.subscribe(  |                          |                            |                          |                  |
  |  session, {tag=  |                          |                            |                          |                  |
  |  "connections-   |                          |                            |                          |                  |
  |  status"...})--->|                          |                            |                          |                  |
  |                  |- substrate (R2288–R2295) |                            |                          |                  |
  |                  |  registers sub + starts  |                            |                          |                  |
  |                  |  listen goroutine        |                            |                          |                  |
  |<-- OK -----------|                          |                            |                          |                  |
  |                  |                          |                            |                          |                  |
  |- mcp.            |                          |                            |                          |                  |
  | findConnections( |                          |                            |                          |                  |
  | chunkIDs, opts)->|                          |                            |                          |                  |
  |                  |- ConnectionsAvailable?-->|                            |                          |                  |
  |                  |<-- yes ------------------|                            |                          |                  |
  |                  |- FindConnections(...)--->|                            |                          |                  |
  |                  |                          |- alloc requestID           |                          |                  |
  |                  |                          |- AddTmpFile(               |                          |                  |
  |                  |                          |    "tmp://connections/     |                          |                  |
  |                  |                          |     <id>.md",              |                          |                  |
  |                  |                          |    header tags +           |                          |                  |
  |                  |                          |    @connections-status:    |                          |                  |
  |                  |                          |    pending) -------------->|                          |                  |
  |                  |                          |                            |- actor write commits     |                  |
  |                  |                          |                            |- PublishTmpDiff -------->|                  |
  |                  |                          |                            |                          |- queue events    |
  |                  |                          |- QueueConnectionsRequest   |                          |                  |
  |                  |                          |  (signals waiters)         |                          |                  |
  |                  |                          |- schedule timeout timer    |                          |                  |
  |                  |<- requestID --------------|                           |                          |                  |
  |<- requestID -----|                          |                            |                          |                  |
  |                  |                          |                            |                          |- (deferred — no  |
  |                  |                          |                            |                          |   sub for        |
  |                  |                          |                            |                          |   "pending")     |
  |                  |                          |                            |                          |                  |
  |                  |                          |                            |                          |    (sidecar already running --wait)
  |                  |- GET /connections/wait--→|                            |                          |                  |
  |                  |                          |- DrainPendingConnections-->|                          |                  |
  |                  |                          |<- [{id, chunkIDs, ...}] ---|                          |                  |
  |                  |<-- JSON array -----------|                            |                          |                  |
  |                  |                                                                                                     |<-- ark connections --wait returns
  |                                                                                                                        |
  |                  |- POST /connections/fetch?id=...<------------------------------------------------------ ark connections --fetch ID
  |                  |- BuildFetchPayload (Librarian helper, View txn) ---->|                          |                  |
  |                  |   read each chunk's EC FileID + path via fts;        |                          |                  |
  |                  |   read raw chunk content via DB.AllChunks            |                          |                  |
  |                  |                                                                                                     |
  |                  |- UpdateTmpFile(                                                                                     |
  |                  |    @connections-status: working,                                                                    |
  |                  |    @connections-progress: thinking,                                                                 |
  |                  |    @connections-elapsed: ~5s ticks)  --> write actor --> PublishTmpDiff ------------>|              |
  |                  |  (orchestrator runs an elapsed-tick goroutine; only @connections-elapsed and @connections-progress)|
  |                  |                                                                                                     |
  |                  |                                                                                                     |- thinks: themes,
  |                  |                                                                                                     |  shared tags,
  |                  |                                                                                                     |  evidence chunk IDs
  |                  |- POST /connections/result (stdin JSON)<----------------------------------------------- ark connections --result ID
  |                  |- SetConnectionsResult: validate Evidence non-empty -->|                          |                  |
  |                  |   on protocol violation: SetConnectionsError("empty evidence: <theme/tag>")      |                  |
  |                  |   on success:                                                                                       |
  |                  |     UpdateTmpFile(                                                                                  |
  |                  |       body = renderBody(themes, sharedTags),                                                        |
  |                  |       @connections-status: completed,                                                               |
  |                  |       @connections-completed: <RFC3339>,                                                            |
  |                  |       @connections-progress: done) --> write actor --> PublishTmpDiff --------->|                   |
  |                  |   cancel timeout timer                                                          |                   |
  |                  |                                                                                  |- listen drains   |
  |                  |                                                                                  |  batch, compress |
  |                  |                                                                                  |  by (path,tag),  |
  |                  |                                                                                  |  WithLua(cb)     |
  |- cb({{tag="connections-status", value="completed", path=..., ...}, ...})<--------------------------|                   |
  |  read tmp doc body, parse Themes / SharedTagCandidates,                                                                |
  |  update _connectionResults, rebind                                                                                     |
```

## Sidecar Unavailable

```
Curation       Server (Lua bridge       Librarian
view             + Librarian)
  |                  |                       |
  |- mcp.            |                       |
  | findConnections->|                       |
  |                  |- ConnectionsAvailable?>|
  |                  |<-- false --------------|
  |<- (nil, "agent unavailable") ------------|
```

## Timeout

```
Curation       Server (orchestrator         Server (timer goroutine)      Server (write actor)
view             + Librarian)
  |                  |                              |                              |
  |  (sidecar never posts result for ID)            |                              |
  |                  |- timer fires at deadline --->|                              |
  |                  |                              |- SetConnectionsError(        |
  |                  |                              |    id, "timeout")            |
  |                  |                              |- UpdateTmpFile(              |
  |                  |                              |    @connections-status:      |
  |                  |                              |    errored,                  |
  |                  |                              |    @connections-error:       |
  |                  |                              |    timeout,                  |
  |                  |                              |    @connections-completed:   |
  |                  |                              |    <RFC3339>) -------------->|
  |                  |                              |                              |- actor commit
  |                  |                              |                              |  publishes errored
  |<-- callback for connections-status=errored, e.value="errored" ---------------- listen goroutine
  |  read @connections-error, render error message
```

A late `--result` for an already-timed-out request: orchestrator
looks up the request in `results` map, sees terminal state, logs
"late result for <id>, discarding," returns 200 OK without touching
the doc.

## Bad Sidecar Output (empty evidence)

```
Sidecar          Server (Librarian
                 + write actor)
  |                  |
  |- POST /connections/result {themes:[{Text:"...", Evidence:[]}], ...} ->|
  |                  |- validateEvidence finds empty list                  |
  |                  |- SetConnectionsError(id, "protocol: empty evidence  |
  |                  |   in theme[0]")                                     |
  |                  |- UpdateTmpFile(@connections-status: errored,        |
  |                  |   @connections-error: "protocol: empty evidence",  |
  |                  |   @connections-progress: done) ----> publish        |
  |<- 400 + error message                                                  |
```

## Subscription Replace (user clicks again before previous completes)

```
Curation        Server (Lua bridge)         PubSub
view
  |                  |                       |
  |- mcp.cancel(session, "")                 |
  |  (orchestrator request keeps running     |
  |   to completion; its result is ignored)  |
  |                  |- Cancel ------------->|- drop subs, close queue, stop listen goroutine
  |- mcp.subscribe(session, {... new requestID ...})
  |- mcp.findConnections(...) → new requestID
```

The first request's eventual completion still writes the doc; with
no subscription, no callback fires. The doc persists until server
restart.

## Server Shutdown

The orchestrator's tracking maps and timers are cleared via the
existing flib/Librarian shutdown path. In-flight tmp:// docs vanish
at restart (standard tmp:// lifecycle). Subscribers fall back to a
fresh button click on the workshop side; the workshop has no
durable session.
