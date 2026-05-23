# Sequence: Discussed Tags — Write + Read

**Requirements:** R2648–R2663

Two flows for the per-session recall dedup state: the recall agent
*writing* RD records via `ark discussed add`, and the recall
substrate *reading* them via `--session SID` on
`ark connections recall`. Both routes share `Store` as the LMDB
front, the write actor for mutations, and view txns for reads.

## Write — `ark discussed add --session SID @t1 @t2:v ...`

```
Recall agent /          CLI                Server            Store                 LMDB
caller (humans)         (cmdDiscussed)     (HTTP bridge)     (helpers)             (RD records)
  |                          |                   |                  |                    |
  |- ark discussed add ----->|                   |                  |                    |
  |  --session SID @t...     |                   |                  |                    |
  |                          |                   |                  |                    |
1.1                          |- parse session,   |                  |                    |
                             |  tag list (R2654) |                  |                    |
                             |- reject empty     |                  |                    |
                             |  session/tag list |                  |                    |
                             |  with exit !=0    |                  |                    |
                             |  (R2650)          |                  |                    |
1.2                          |- DetectServer:    |                  |                    |
                             |  proxy if up,     |                  |                    |
                             |  else withDB      |                  |                    |
1.3                          |--- proxy path --->|                  |                    |
                             |   POST /discussed/add               |                    |
                             |                   |- enqueueWrite -->|                    |
                             |                   |                  |- AddDiscussed(s,  |
                             |                   |                  |   t, v) for each  |
                             |                   |                  |   tag (R2650) --->|
1.4                          |                   |                  |                    |- Put RD
                             |                   |                  |                    |   key = "RD"+s+\0+t+\0+v
                             |                   |                  |                    |   value = NOW big-endian uint64
                             |                   |                  |                    |   (R2648)
                             |                   |                  |<- ok ------------- |
                             |                   |<- ok ------------|                    |
                             |<- 200 OK ---------|                   |                    |
                             |                                       |                    |
1.5                          |--- cold path ---------------------->  |                    |
                             |   withDB(...): Store.AddDiscussed(s,t,v) for each tag (R2650)
                             |                                       |                    |
  |<- exit 0 (no stdout) ----|                                       |                    |
```

`add` writes one RD record per tag argument. Re-adding an existing
`(session, tag, value)` overwrites the timestamp — "discussed now"
wins (R2650). Bare-tag arguments encode with an empty value
segment (R2648).

## Read — `ark connections recall --session SID --discussed @t...`

```
Caller          CLI               Server (Lib.Recall)        Store                LMDB
(user / agent)  (cmdRecall)       substrate worker           (helpers)            (RD + V + EC)
  |                  |                    |                       |                    |
  |- ark connections recall ...           |                       |                    |
  |  --session SID                        |                       |                    |
  |  --discussed @t:v -->|                |                       |                    |
2.1                      |- parse flags;  |                       |                    |
                         |  build         |                       |                    |
                         |  RecallOpts.   |                       |                    |
                         |  Discussed     |                       |                    |
                         |  from          |                       |                    |
                         |  --discussed   |                       |                    |
                         |  (R2655)       |                       |                    |
2.2                      |- proxy to      |                       |                    |
                         |  POST /recall  |                       |                    |
                         |  with opts --->|                       |                    |
2.3                      |                |- env.View(txn):       |                    |
                         |                |  if opts.Session != "":                   |
                         |                |    ListDiscussed(s,   |                    |
                         |                |    since=0, ttl) ---->|                    |
2.4                      |                |                       |- range scan       |
                         |                |                       |  "RD"+s+\0 ------>|
                         |                |                       |                    |- iter RD keys
                         |                |                       |                    |  for session s
                         |                |                       |- skip entries     |
                         |                |                       |  where            |
                         |                |                       |  ts+ttl<NOW       |
                         |                |                       |  (R2659)          |
                         |                |                       |- skip 8-byte      |
                         |                |                       |  malformed values |
                         |                |                       |  (R2663)          |
                         |                |<- []Discussed --------|                    |
2.5                      |                |- union with           |                    |
                         |                |  opts.Discussed       |                    |
                         |                |  (R2655)              |                    |
2.6                      |                |- run vector+trigram   |                    |
                         |                |  EC passes per input  |                    |
                         |                |  (existing recall)    |                    |
2.7                      |                |- per candidate chunk: |                    |
                         |                |  strip tags whose     |                    |
                         |                |  (name,value)         |                    |
                         |                |  matches the          |                    |
                         |                |  exclusion set        |                    |
                         |                |  (R2656, R2657)       |                    |
2.8                      |                |- drop chunk if its    |                    |
                         |                |  tag list goes empty  |                    |
                         |                |  after stripping      |                    |
                         |                |  (R2656). This runs   |                    |
                         |                |  before the           |                    |
                         |                |  KeepTagless check    |                    |
                         |                |  (R2658).             |                    |
2.9                      |                |- render markdown      |                    |
                         |                |  stencil (existing)   |                    |
                         |                |  return RecallResult  |                    |
                         |<- JSON --------|                       |                    |
  |<- markdown stencil --|                                        |                    |
```

The substrate never writes RD records — `cmdRecall` is read-only
on the recall namespace (R2662). The matching rule is uniform
across both flag sources: an exclusion entry with empty value
matches any value under that name, a non-empty value matches the
exact pair (R2657).

## Lazy expiry — `ark discussed list` and prune

```
Caller        CLI                Store                 LMDB
              (cmdDiscussed)     (helpers)             (RD records)
  |                |                  |                     |
  |- ark discussed list --session SID                       |
3.1                |- ListDiscussed(s,                     |
                   |   since, ttl) -->|                     |
                   |                  |- range scan         |
                   |                  |  "RD"+s+\0 -------->|
                   |                  |                     |- iter
                   |                  |- per record:        |
                   |                  |  drop if            |
                   |                  |  ts+ttl<NOW (R2659) |
                   |                  |  drop if since>0    |
                   |                  |  and ts<NOW-since   |
                   |                  |  drop if value bytes|
                   |                  |  != 8 (R2663)       |
                   |<- []entries -----|                     |
                   |- format @name    |                     |
                   |  or @name: value |                     |
                   |  per line, or    |                     |
                   |  --json (R2651)  |                     |
  |<- stdout ------|                                        |
                                                            |
  |- ark discussed prune [--ttl DUR]                        |
3.2                |- PruneDiscussed(ttl) ---------------->  |
                   |                  |- full RD scan       |
                   |                  |  across all         |
                   |                  |  sessions           |
                   |                  |- delete entries     |
                   |                  |  where ts+ttl<NOW   |
                   |                  |  (R2653, R2659)     |
                   |                  |<- deleted-count --- |
                   |<- count ---------|                     |
  |<- count on stderr                                       |
```

`list` and the substrate's exclusion-set load (step 2.3) share
the same lazy-expiry logic in `Store.ListDiscussed`. `prune`
extends the scan across all sessions and physically deletes
expired entries; lazy expiry on read is the steady-state behavior,
prune is the explicit cleanup verb (R2653).

## Error paths

- `cmdDiscussed add` without `--session` or with no tag arguments
  → exit non-zero before any RD write (R2650).
- `cmdDiscussed prune --ttl <invalid>` → exit non-zero before any
  RD delete (R2653).
- ark.toml `[recall].discussed_ttl` unparseable → server logs a
  warning at startup, falls back to 24h (R2659, R2663).
- RD record value not exactly 8 bytes → reader treats as expired,
  caller never sees it; writer never produces this shape (R2663).
