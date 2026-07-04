# Sequence: Bloodhound CLI hunt (tag baton)
**Requirements:** R3010, R3011, R3020, R3021, R3022, R3023, R3024, R3025, R3027, R3028, R3030, R3031

The external-CLI directed hunt. The request doc's tag is a routing baton
rewritten at each hop (the Fixer pattern): CLI → watcher → pool secretary →
watcher → Luhmann → CLI. Two docs — the request doc (internal working
artifact) and the result doc (clean external JSONL). Participants:
`ark bloodhound search` (CLITree/CLI), RecallWatcher, a pool secretary
(RecallAgentBuilder `next`), Luhmann (`ark luhmann next` + `ark bloodhound
add`, LuhmannCLI).

```
1.1  ark bloodhound search TERMS [--wait] [--timeout S]                    (R3021, R3022)
     1.1.1  subscribe @ark-bloodhound-cli-result:<id>  (before submit)     (R3021)
     1.1.2  create request doc tmp://BLOODHOUND-CLI/<id> = @ark-bloodhound-cli
              + TERMS; server accumulates, one atomic write                (R3021, R3031)
     1.1.3  block on the result tag (--wait: stubborn across a bounce)     (R3022)

1.2  watcher wakes on @ark-bloodhound-cli   (Fixer: request trigger)       (R3023)
     1.2.1  gate: luhmannOwner present? no → CLI reports not-running (≠0)  (R3020)
     1.2.2  enhance: Librarian.Recall seed + search crank handle →
              a standard bloodhound task doc                              (R3006, R3030)
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
     1.4.2  push the request-doc path onto Luhmann nextQueue (curation)    (R3024, R3025)

1.5  Luhmann drains it: `ark luhmann next --session S`                     (R3010, R3011)
     1.5.1  refine the raw results (strong-parent discernment)            (R3025)
     1.5.2  ark bloodhound add --result <id> ...  (one item per call)      (R3027)
     1.5.3  terminal add flips result doc tag @ark-bloodhound-cli-result:<id> (R3027, R3028, R3031)

1.6  CLI wakes on its result tag                                           (R3021)
     1.6.1  read result doc, print JSONL (empty hunt → no lines, exit 0)   (R3021)
```

Notes:
- The sole non-tag hop is 1.4.2 (watcher → Luhmann), an in-process `nextQueue`
  push; every other hop is a doc-tag flip (R3031).
- Occupancy frees at 1.4.1, *before* curation (1.5), so a slow refine never
  holds a pool slot (R3024, R3026).
