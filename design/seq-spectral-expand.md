# Sequence: Spectral Expand (Tag Search)

**Requirements:** R1235-R1247, R1268-R1273, R1378-R1383

Browser sends POST /search/curate (was /search/expand). The server
**queues** the request and a **sidecar agent** drives the steps: it
picks up work via GET /search/curate/wait (lotto tube), runs expand,
calls the match endpoints under /search/expand/ (fuzzy, embed,
search), curates, then posts the result via POST /search/curate/result.
The browser polls GET /search/curate/result/{id} for the curated
results. Curation endpoints use the /search/curate prefix;
expansion/matching endpoints remain under /search/expand/.

> **Note — the diagrams below are superseded.** They depict the
> pre-rename topology where the Librarian actor spawned a `claude`
> **co-process** and ran expand → fuzzy → curate → fetch **inline**
> on the server (the old `POST /search/expand` all-in-one returning
> `source:"expansion"` directly — retired R1244/R1245). The current
> model is sidecar-driven and separated (R1378–R1383, summarised
> above); the conceptual three steps are unchanged but the execution
> topology is not. A full redraw to the lotto-tube model is pending.

## Happy Path

```
Browser        Server           Librarian(actor)    Store/V-records   claude(co-process)
  |               |                  |                    |                  |
  |--POST curate->|                  |                    |                  |
  |               |--ExpandTags()--->|                    |                  |
  |               |                  |                    |                  |
  |               |                  |--- Step 1: Expand -|---------------->|
  |               |                  |  "tag=design,      |                  |
  |               |                  |   value=ui"        |                  |
  |               |                  |<--- alternatives --|-----------------|
  |               |                  |  [{design,layout}, |                  |
  |               |                  |   {pattern,ui},    |                  |
  |               |                  |   {architecture,   |                  |
  |               |                  |    interface}]      |                  |
  |               |                  |                    |                  |
  |               |                  |--- Step 2: Fuzzy --|                  |
  |               |                  |  fuzzyMatchTags()  |                  |
  |               |                  |--V record scans--->|                  |
  |               |                  |<--matched tuples---|                  |
  |               |                  |  [(design,layout,  |                  |
  |               |                  |    5, 0.8),        |                  |
  |               |                  |   (pattern,ui,     |                  |
  |               |                  |    12, 0.9), ...]  |                  |
  |               |                  |                    |                  |
  |               |                  |--- Step 3: Curate -|---------------->|
  |               |                  |  "here are matches,|                  |
  |               |                  |   which relevant?" |                  |
  |               |                  |<--- curated subset-|-----------------|
  |               |                  |  [pattern:ui,      |                  |
  |               |                  |   design:layout]   |                  |
  |               |                  |                    |                  |
  |               |                  |--- Step 4: Fetch --|                  |
  |               |                  |  search for curated|                  |
  |               |                  |  tags via Searcher |                  |
  |               |                  |                    |                  |
  |               |<--results--------|                    |                  |
  |<--JSON--------|  (source:        |                    |                  |
  |  (expansion    |   "expansion")  |                    |                  |
  |   results)    |                  |                    |                  |
```

## Cold Start (process not running)

```
Browser        Server           Librarian(actor)      claude
  |               |                  |                    |
  |--POST curate->|                  |                    |
  |               |--ExpandTags()--->|                    |
  |               |                  |--spawn()           |
  |               |                  |  exec.Command(     |
  |               |                  |    "claude",       |
  |               |                  |    "--print",      |
  |               |                  |    "--model",      |
  |               |                  |    "haiku", ...)   |
  |               |                  |  cmd.Start()------>|
  |               |                  |  (pipeline runs    |
  |               |                  |   as happy path)   |
  |               |<--results--------|                    |
  |<--JSON--------|                  |                    |
```

## TTL Expiry

```
                                 Librarian(actor)      claude
                                      |                    |
                                      |--timer fires       |
                                      |--kill()            |
                                      |  stdin.Close()---->|
                                      |  cmd.Wait()        |
                                      |  nil cmd, pipes    |
                                      |                   (exits)
```

## Process Crash (retry on next request)

```
Browser        Server           Librarian(actor)      claude
  |               |                  |                    |
  |--POST curate->|                  |                    |
  |               |--ExpandTags()--->|                    |
  |               |                  |--sendMessage()---->|
  |               |                  |<--stdout EOF/error |
  |               |                  |--kill() (cleanup)  |
  |               |                  |--spawn() (retry)   |
  |               |                  |  (pipeline restarts|
  |               |                  |   from step 1)     |
  |               |<--results--------|                    |
  |<--JSON--------|                  |                    |
```

## Unavailable (claude not on PATH)

```
Browser        Server           Librarian
  |               |                  |
  |--POST curate->|                  |
  |               |--ExpandTags()--->|
  |               |<--err: unavail---|
  |<--503---------|                  |
```

## Shutdown

```
Server           Librarian(actor)      claude
  |                    |                    |
  |--shutdown signal   |                    |
  |--kill()----------->|                    |
  |                    |--stdin.Close()---->|
  |                    |--cmd.Wait()        |
  |                    |                   (exits)
  |--close(ch)         |                    |
  |                   (goroutine exits)     |
```
