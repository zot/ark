# Sequence: Spectral Expand (Tag Search)

**Requirements:** R1235-R1247, R1268-R1273, R1378-R1383

Browser sends POST /search/curate (was /search/expand). Server
delegates to Librarian actor, which runs the three-step pipeline:
expand → fuzzy match → curate → fetch results. Curation endpoints
use /search/curate prefix; expansion/matching endpoints remain
under /search/expand/.

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
