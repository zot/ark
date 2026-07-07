# Sequence: Bloodhound CLI hunt (tag baton)
**Requirements:** R3010, R3011, R3020, R3021, R3022, R3023, R3024, R3025, R3027, R3028, R3030, R3031, R3038, R3039, R3040, R3041, R3042

The external-CLI directed hunt. The request doc's tag is a routing baton
rewritten at each hop (the Fixer pattern): CLI → watcher → pool secretary →
watcher → Luhmann → CLI. Two docs — the request doc (internal working
artifact) and the result doc (clean external JSONL). Participants:
`ark bloodhound search` (CLITree/CLI), RecallWatcher, a pool secretary
(RecallAgentBuilder `next`), Luhmann (`ark luhmann next` + `ark bloodhound
add`, LuhmannCLI).

```
1.1  ark bloodhound search [CLUE...] [--file PATH|-] [--wait] [--timeout S] [--raw] [--markdown] (R3021, R3022)
     1.1.1  subscribe @ark-bloodhound-cli-result:<id>  (before submit)     (R3021)
     1.1.2  clue ← positional CLUE... or --file (--file - = heredoc stdin);
              create request doc tmp://BLOODHOUND-CLI/<id> = @ark-bloodhound-cli
              + payload (metadata-first: scope/depth/want/[curate] then clue
              body); server accumulates, one atomic write          (R3021, R3031, R3046)
     1.1.3  block on the result tag (--wait: stubborn across a bounce)     (R3022)

1.2  watcher wakes on @ark-bloodhound-cli   (Fixer: request trigger)       (R3023)
     1.2.1  gate: luhmannOwner present? no → CLI reports not-running (≠0)  (R3020)
     1.2.2  enhance: renderSeed — clueOf strips metadata, split clue per
              paragraph, Recall unions per-idea hits (K scales) + crank
              handle → a standard bloodhound task doc              (R3006, R3030, R3043-R3045)
     1.2.3  schedule + route:
              free secretary → re-tag @ark-secretary-work=<pool-sec>,
                mark busy  (atomic append + re-tag)                        (R3023, R3031)
              none, room  → push "stand up" on Luhmann nextQueue           (R3023)
              none, full  → leave pending; CLI --wait blocks               (R3022, R3023)

1.3  pool secretary drains @ark-secretary-work via `... recall next`       (R3030)
     1.3.1  run the hunt (seal + crank handle + seed) — as in-session      (R3030)
     1.3.2  close: append raw results to the request doc, re-tag
              @ark-bloodhound-cli-return  (atomic; not a finding- doc)     (R3025, R3031)

1.4  watcher wakes on @ark-bloodhound-cli-return  (Fixer: return trigger)  (R3024)
     1.4.1  free the secretary → idle-in-cooldown (occupancy pre-curate)   (R3024)
     1.4.2  curated (default): push request-doc path onto Luhmann
              nextQueue → 1.5                                             (R3024, R3025)
     1.4.3  raw (curate:false, R3038): relayRawResult — write result doc
              from ## Raw findings, tag @ark-bloodhound-cli-result:<id>,
              drop request doc; skip Luhmann → 1.6                        (R3039, R3040)

1.5  Luhmann drains it: `ark luhmann next --session S`  (curated only)     (R3010, R3011)
     1.5.1  refine the raw results (strong-parent discernment)            (R3025)
     1.5.2  ark bloodhound add --result <id> ...  (one item per call)      (R3027)
     1.5.3  terminal add flips result doc tag @ark-bloodhound-cli-result:<id> (R3027, R3028, R3031)

1.6  CLI wakes on its result tag                                           (R3021)
     1.6.1  read result doc, print per flag: JSONL default (empty → no
              lines, exit 0); --markdown renders the JSONL as a locator
              list; --raw prints the relayed raw findings verbatim         (R3021, R3037, R3040)
```

## 2. Periodic request sweep (reap + re-issue)

Rides the same `pruneLoop` ticker as autoscale-down (R3019); recovers the two
time-based liveness gaps a stranded request leaves (nothing event-driven wakes
the watcher for one). Operates only on **pending** requests.

```
2.1  pruneLoop ticker fires (bloodhoundPruneInterval)
     2.1.1  pump() — route any pending hunt a missed pump left stranded    (R3023)
     2.1.2  reap: for each cliPool.requests entry older than
              request_ttl_seconds → drop from requests/pending, remove
              its request doc (client hit --timeout and exited)            (R3041)
     2.1.3  re-issue: a request pending past the re-issue threshold with
              pool room → push one `stand up another` on Luhmann nextQueue
              (bounded by pool_max), recovering a dropped stand-up         (R3042)
```

Notes:
- The sole non-tag hop is 1.4.2 (watcher → Luhmann), an in-process `nextQueue`
  push; every other hop is a doc-tag flip (R3031). The raw branch 1.4.3 writes
  the result doc itself (no Luhmann), still a tag flip that wakes the CLI.
- Occupancy frees at 1.4.1, *before* the 1.4.2/1.4.3 branch, so neither a slow
  refine nor a raw relay holds a pool slot (R3024, R3026).
