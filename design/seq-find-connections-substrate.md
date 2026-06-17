# Sequence: Find Connections — Substrate (normal mode)

**Requirements:** R2567–R2615

End-to-end flow from the curation workshop calling
`sys.findConnections(..., {mode="normal"})` through the in-process
substrate pipeline to the workshop rebinding on the completed
transition. Three pieces in motion:

- **Curation workshop** (Lua, Frictionless): calls
  `sys.findConnections`, subscribes via Subtask 0 primitives.
- **Server (Librarian + bridge)**: orchestrates the request,
  writes the tmp:// doc through the write actor, runs the
  substrate pipeline in-process.
- **Substrate pipeline**: four passes per input (vector+trigram
  against ED, vector+trigram against EC) over one shared
  View txn. Merges per-substrate, per-input, sorts top-K.

## Happy Path (single chunkID input)

```
Curation       Server (Lua bridge       Librarian.FindConnections      Substrate pipeline      Write actor + DB     PubSub +
workshop       + HTTP handlers)         orchestrator                    (in-process worker)     (index)              listener
  |                  |                          |                              |                       |                  |
  |- mcp.onpublish + mcp.subscribe(             |                              |                       |                  |
  |  session, {tag="connections-status"...})--->|                              |                       |                  |
  |                  |  (substrate sub starts)  |                              |                       |                  |
  |<-- OK -----------|                          |                              |                       |                  |
1.1
  |- sys.            |                          |                              |                       |                  |
  | findConnections( |                          |                              |                       |                  |
  | {{chunkID=4711}},|                          |                              |                       |                  |
  | {mode="normal"})>|                          |                              |                       |                  |
1.2                  |- normalizeInputs() ----->|                              |                       |                  |
1.3                  |- validateEnqueue()       |                              |                       |                  |
                     |  (unknown chunk?         |                              |                       |                  |
                     |   path miss? range?)     |                              |                       |                  |
1.4                  |- FindConnections(inputs, |                              |                       |                  |
                     |  {mode=normal})--------->|                              |                       |                  |
1.5                  |                          |- alloc requestID             |                       |                  |
1.6                  |                          |- AddTmpFile(                 |                       |                  |
                     |                          |  "tmp://connections/<id>.md",|                       |                  |
                     |                          |  @purpose: curate,           |                       |                  |
                     |                          |  @connections-mode: normal,  |                       |                  |
                     |                          |  @connections-status: pending|                       |                  |
                     |                          |  ...) ---------------------->|                       |                  |
                     |                          |                              |                       |- actor commit    |
                     |                          |                              |                       |- PublishTmpDiff->|
                     |                          |                              |                       |                  |- queue events
1.7                  |                          |- launch substrate goroutine->|                       |                  |
                     |<- requestID -------------|                              |                       |                  |
1.8                  |<- requestID -----------|                                |                       |                  |
                     |                                                         |                       |                  |
                     |                                                  1.9    |- db.View(txn):        |                  |
                     |                                                         |    for each input I:  |                  |
                     |                                                         |      EC[I] or embed   |                  |
                     |                                                         |      vector(I, ED) -> |                  |
                     |                                                         |        SuggestTagNames|                  |
                     |                                                         |        helper         |                  |
                     |                                                         |      trigram(I, ED) ->|                  |
                     |                                                         |        microfts2      |                  |
                     |                                                         |        fuzzy on       |                  |
                     |                                                         |        ED text        |                  |
                     |                                                         |      vector(I, EC) -> |                  |
                     |                                                         |        SearchChunks(K)|                  |
                     |                                                         |      trigram(I, EC) ->|                  |
                     |                                                         |        microfts2      |                  |
                     |                                                         |        fuzzy on       |                  |
                     |                                                         |        chunk text     |                  |
                     |                                                         |      collect V record |                  |
                     |                                                         |      votes per chunk  |                  |
                     |                                                  1.10   |- mergePerSubstrate    |                  |
                     |                                                         |  (max), mergeAcross   |                  |
                     |                                                         |  Inputs (max),        |                  |
                     |                                                         |  top-K by aggregate   |                  |
                     |                                                  1.11   |- buildProposalBody    |                  |
                     |                                                         |  (## Proposals with   |                  |
                     |                                                         |   @proposal-kind:     |                  |
                     |                                                         |   tag-name rows)      |                  |
                     |                                                         |                       |                  |
                     |                          |- SetConnectionsResult        |                       |                  |
                     |                          |  (substrate, payload)<-------|                       |                  |
1.12                 |                          |- UpdateTmpFile(              |                       |                  |
                     |                          |    body,                     |                       |                  |
                     |                          |    @connections-status:      |                       |                  |
                     |                          |    completed,                |                       |                  |
                     |                          |    @proposal-count: N,       |                       |                  |
                     |                          |    @connections-completed:   |                       |                  |
                     |                          |    <RFC3339>) -------------->|                       |                  |
                     |                          |                              |                       |- actor commit    |
                     |                          |                              |                       |- PublishTmpDiff->|
                     |                          |                              |                       |                  |- listen drains
                     |                          |                              |                       |                  |  batch, compress
                     |                          |                              |                       |                  |  by (path,tag),
                     |                          |                              |                       |                  |  WithLua(cb)
  |- cb({{tag="connections-status", value="completed", path=...}})<-----------------------------------------------------|
  |  read tmp doc body, parse Proposals, populate _connectionResults, rebind                                              |
```

## Multi-Input Merge

```
Substrate pipeline (in db.View txn)
  |
  |- inputs = [{chunkID=4711}, {chunkID=5023}, {text="asparagus"}]
  |
  |- per-input substrate passes
2.1    |- for I in inputs:
       |    if I.chunkID != 0: queryVec = EC[I.chunkID]
       |    elif I.text != "": queryVec = EmbedQuery(I.text)
       |    elif I.path != "": for each chunk overlapping I.range:
       |                         queryVec = EC[chunkID]
       |                         (each overlapping chunk = one virtual input)
2.2    |  vector_ed(I)    = scan ED, max per tag across files
       |  trigram_ed(I)   = microfts2 trigram fuzzy vs tag-def text
       |  vector_ec(I)    = SearchChunks(queryVec, K')
       |                    → for each chunk in result, read V records,
       |                    → vote per tag with chunk score
       |  trigram_ec(I)   = microfts2 fuzzy vs chunk text, same vote
       |
2.3   |- per-input merge across substrates:
       |  for each tag T discovered for input I:
       |    perInput[I][T] = max(vector_ed, trigram_ed, vector_ec, trigram_ec)
       |  retain per-substrate scores as evidence
       |
2.4   |- cross-input merge:
       |  for each tag T:
       |    aggregate[T] = max(perInput[I][T] for I in inputs)
       |    perSubstrate[T] = {sub: max(perInput[I][T][sub] for I)}
       |    supportingChunks[T] = union of contributing chunks, cap 10
       |
2.5   |- sort by aggregate[T] desc, top-K (default 20)
```

## Embedding Unavailable (trigram-only fallback)

```
Substrate pipeline                 Librarian.FindConnections
  |                                       |
  |- db.View(txn):                        |
  |  EmbeddingAvailable()? -> false       |
  |  vector_ed, vector_ec: skip           |
  |  trigram_ed, trigram_ec: run normally |
  |- buildProposalBody                    |
  |                                       |
3.1                                       |- UpdateTmpFile(
                                          |    body,
                                          |    @connections-status: completed,
                                          |    @connections-warning:
                                          |    embedding unavailable,
                                          |    @proposal-count: N,
                                          |    ...) --> write actor
```

## Reject at Enqueue (unknown chunk, path miss)

```
Curation       Server (Lua bridge       Librarian
workshop       + HTTP handlers)         (normalizeInputs / validateEnqueue)
  |                  |                          |
  |- sys.            |                          |
  | findConnections( |                          |
  | {{chunkID=9999}},|                          |
  | {mode="normal"})>|                          |
4.1                  |- normalizeInputs() ----->|
                     |  - ReadCRecord(9999)     |
                     |    → not found           |
4.2                  |<- "unknown chunk 9999" --|
4.3                  |  (no tmp:// doc          |
                     |   written)               |
  |<- (nil, "unknown chunk 9999") --------------|
```

## CLI find --wait

```
ark CLI (cmdConnectionsFind)     Server (HTTP)               Librarian
       |                              |                            |
5.1    |- parse positional inputs --->|                            |
       |  build ConnectionsInput list |                            |
       |  --mode normal --wait        |                            |
5.2    |- POST /connections/find      |                            |
       |  (inputs, opts) ------------>|- Librarian.FindConnections-|
       |                              |<- requestID ---------------|
5.3    |<- requestID                  |                            |
5.4    |- subscribe via               |                            |
       |  POST /pubsub/subscribe      |                            |
       |  (session, tag=connections-  |                            |
       |  status, filterFiles=[doc])->|                            |
5.5    |- GET /pubsub/listen          |                            |
       |  (long-poll) --------------->|                            |
       |                              |  (substrate completes)     |
       |                              |- terminal write -> PubSub  |
5.6    |<- event(tag=connections-     |                            |
       |   status, value=completed) --|                            |
5.7    |- GET /fetch?path=<doc> ----->|                            |
       |<- body --------------------- |                            |
5.8    |  print body to stdout        |                            |
       |  (or JSON projection if --json)                           |
```

## CLI show <path>

```
ark CLI (cmdConnectionsShow)      Server (HTTP)
       |                                  |
6.1    |- parse path + projection flags ->|
6.2    |- GET /fetch?path=<doc> --------->|
       |<- body ---------------------------|
6.3    |- parseConnectionsDoc(body)
       |  → ConnectionsDoc {
       |      Status, Mode, Purpose,
       |      Warning, Proposals[]
       |    }
6.4    |- applyFlags(--status, --tags,
       |    --tag NAME, --threshold N)
6.5    |  print markdown projection
       |  (or JSON with --json)
```

## CLI list

```
ark CLI (cmdConnectionsList)      Server (HTTP)               Librarian
       |                                  |                          |
7.1    |- GET /connections/list --------->|- ListConnections() ----->|
       |                                  |<- []*ConnectionsRecord --|
       |<- JSON array --------------------|
7.2    |  print markdown table
       |  (or JSON with --json)
```

## Sidecar Subcommand Rename (no flow change)

The wire protocol from CLI to server is unchanged. Only the CLI's
argument parsing differs:

```
old: ark connections --wait
new: ark connections sidecar-wait

old: ark connections --fetch ID
new: ark connections sidecar-fetch ID

old: ark connections --result ID  (stdin JSON)
new: ark connections sidecar-result ID  (stdin JSON unchanged)

old: ark connections --error ID=MESSAGE
new: ark connections sidecar-error ID MESSAGE
```

Each new subcommand makes the same HTTP call its old flag made.
The `ark-connections` agent's guard script needs corresponding
edits to allow the new argument shape.
