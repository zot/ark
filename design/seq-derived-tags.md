# Sequence: Derived Tags — Derive + Accept + Reject

**Requirements:** R2664–R2686

Three flows cover the derived-tag lifecycle. The recall substrate's
*derivation pass* writes RC and RF records as a side effect of
`ark connections recall --propose`. The Tag Forge (item 6) later
consumes those records and routes user decisions through
`Store.AcceptDerived` (which promotes a candidate to an attached tag
via the existing F/V path) or `Store.RejectDerived` (which writes
RJ to suppress re-proposal).

## Derive — `ark connections recall --propose`

```
Caller        CLI               Server (Lib.Recall)              Store                    LMDB
(user/agent)  (cmdRecall)       substrate + derivation worker    (helpers)                (RC/RJ/RF + EC/ED)
  |                |                    |                              |                          |
  |- ark connections recall ...         |                              |                          |
  |  --propose <inputs> -->|            |                              |                          |
1.1                        |- parse flags;                             |                          |
                           |  RecallOpts.Propose=true                  |                          |
                           |  (R2667)                                  |                          |
1.2                        |- proxy POST  |                              |                          |
                           |  /recall --->|                              |                          |
1.3                        |              |- env.View(txn):                |                          |
                           |              |  force                       |                          |
                           |              |  KeepTagless=true            |                          |
                           |              |  internally for              |                          |
                           |              |  derivation chunk            |                          |
                           |              |  set (R2668)                 |                          |
1.4                        |              |- run vector+trigram         |                          |
                           |              |  EC passes (existing         |                          |
                           |              |  substrate, R2620)           |                          |
1.5                        |              |- partition scored:           |                          |
                           |              |  surfaced[]                  |                          |
                           |              |  (caller's effective         |                          |
                           |              |  KeepTagless) +              |                          |
                           |              |  derivation[]                |                          |
                           |              |  (full set, R2668)           |                          |
1.6                        |              |- derivationPass(             |                          |
                           |              |    txn, derivation):         |                          |
                           |              |- Store.MaxEDSerial() --->     |                          |
                           |              |    (one S-over-ED walk,      |                          |
                           |              |    R2669)                    |                          |
                           |              |<- maxED -------------------- |                          |
1.7                        |              |- per chunk in derivation:    |                          |
                           |              |  - ReadDerivedFreshness ---> |                          |
                           |              |     (R2669)                  |                          |
                           |              |  - if rf >= maxED:           |                          |
                           |              |      skip derivation         |                          |
                           |              |      for this chunk          |                          |
                           |              |      (record this in         |                          |
                           |              |      chunkSimilarities       |                          |
                           |              |      with no scores —        |                          |
                           |              |      stencil step            |                          |
                           |              |      will compute            |                          |
                           |              |      on-demand)              |                          |
                           |              |  - else:                     |                          |
                           |              |      cosine(EC[chunkid],     |                          |
                           |              |         ED[*]) → top-N       |                          |
                           |              |         (R2670)              |                          |
                           |              |      drop candidates         |                          |
                           |              |         in F-record set      |                          |
                           |              |         (R2671)              |                          |
                           |              |      drop candidates         |                          |
                           |              |         matching ext-routed  |                          |
                           |              |         tagnames (R2672)     |                          |
                           |              |      HasDerivedRejection --->|                          |
                           |              |         (R2673)              |                          |
                           |              |         drop rejected        |                          |
                           |              |      for each survivor:      |                          |
                           |              |         WriteDerivedProposal |                          |
                           |              |         (tally++) (R2674) -->|                          |
                           |              |                              |                          |- Put RC[chunk+tag]
                           |              |                              |                          |   8-byte BE uint64
                           |              |                              |                          |   tally (R2664)
                           |              |      WriteDerivedFreshness   |                          |
                           |              |         (chunkid, maxED) --->|                          |
                           |              |                              |                          |- Put RF[chunk]
                           |              |                              |                          |   varint(maxED)
                           |              |                              |                          |   (R2666)
1.8                        |              |- enrichProposedTags(surfaced,|                          |
                           |              |    chunkSimilarities):       |                          |
                           |              |  per surfaced chunk          |                          |
                           |              |  with any RC:                |                          |
                           |              |  - DerivedProposals --------->                          |
                           |              |    (R2678)                   |                          |
                           |              |  - if chunk was              |                          |
                           |              |    derived-this-call:        |                          |
                           |              |      use stored similarities |                          |
                           |              |    else:                     |                          |
                           |              |      cosine(EC[chunkid],     |                          |
                           |              |         ED[tag, *]) max      |                          |
                           |              |         per tag (R2685)      |                          |
                           |              |  - sort by similarity desc   |                          |
                           |              |    (R2685)                   |                          |
                           |              |  - assign                    |                          |
                           |              |    RecalledChunk.            |                          |
                           |              |    ProposedTags (R2686)      |                          |
1.9                        |              |- render markdown stencil:    |                          |
                           |              |  per chunk with non-empty    |                          |
                           |              |  ProposedTags, emit          |                          |
                           |              |  `@chunk-proposed-tags:` line|                          |
                           |              |  after `@chunk-tags`         |                          |
                           |              |  (R2684)                     |                          |
                           |              |<- batched txn commit         |                          |
                           |<- 200 JSON --|                              |                          |
  |<- markdown stencil ----|                                             |                          |
```

`--propose` is purely additive at the caller level (no input
behavior change). The derivation pass writes RC/RF as a side
effect; the caller's surfaced output gains the
`@chunk-proposed-tags` line per surfaced chunk that accumulated
proposals (R2684, R2685). Tagless chunks are derivation candidates
when `--propose` is set (R2668), but their proposals are invisible
in the stencil unless `-all` is also set.

When `--propose` is set but no `tag_model` is configured, the
derivation pass exits silently — there are no ED records to score
against (R2676); the recall result is unaffected.

## Accept — `Store.AcceptDerived(chunkID, tagname, value)`

Called by the Tag Forge UI (item 6) when the user accepts a derived
candidate. No CLI surface in this slice — the forge calls the Store
API directly.

```
Tag Forge UI    Store.AcceptDerived       LMDB
(item 6)        (write actor)             (RC + V/F)
  |                  |                          |
  |- accept(c,t,v)-->|                          |
2.1                  |- enqueueWrite(...):      |
                     |  one txn through         |
                     |  write actor (R2679)     |
2.2                  |- delete RC[chunk+tag]--->|
                     |                          |- Del RC[chunkid+tagname]
2.3                  |- AllocTagValueID or      |
                     |  prefix-scan to resolve  |
                     |  existing tvid for       |
                     |  (tag, value)            |
2.4                  |- AppendTagValues(        |
                     |    fileid,               |
                     |    [{tag, value}]) ----->|
                     |  (existing F/V path)     |- Put V[tag+\0+value+\0+tvid]
                     |                          |   append chunkid varint
                     |                          |- Put F[chunk+tag]
                     |                          |   append tvid
                     |  (R2679)                 |
  |<- tvid ----------|                          |
```

The accept is *atomic* — RC delete + F/V write happen in one txn.
If the accept fails mid-flight, RC stays in place; the user can
retry. The existing F/V attach path (R1099 + family) is reused
verbatim — `AcceptDerived` is a thin RC-aware wrapper over
`AppendTagValues`.

Empty `value` produces a bare-tag attach (no value segment in the
V record); matches the bare-tag shape the statistical pass writes
into RC.

## Reject — `Store.RejectDerived(chunkID, tagname)`

Called by the Tag Forge UI (item 6) when the user rejects a derived
candidate. Persists an RJ record so the derivation pass never
re-proposes the same (chunkid, tagname).

```
Tag Forge UI    Store.RejectDerived       LMDB
(item 6)        (write actor)             (RC + RJ)
  |                  |                          |
  |- reject(c,t) --->|                          |
3.1                  |- enqueueWrite(...):      |
                     |  one txn through         |
                     |  write actor (R2680)     |
3.2                  |- delete RC[chunk+tag]--->|
                     |                          |- Del RC[chunkid+tagname]
3.3                  |- AdjustJudgment(-1):     |
                     |    read score (absent=0),|
                     |    score-=1, stamp NOW    |
                     |    (R2877) ------------->|
                     |                          |- Put RJ[chunkid+tagname]
                     |                          |   signed-varint(score)
                     |                          |   + 8-byte BE nanos
                     |                          |   (R2874)
  |<- magnitude -----|                          |
```

The Recall Judgment edge is signed and bidirectional, but there is
no manual un-reject verb: the next derivation pass that would
propose the same (chunkid, tagname) sees `score < 0` via
`HasDerivedRejection` (step 1.7 above) and skips the candidate
(R2673, R2878, R2881). A reinforcement producer (the secretary, a
later seam) is the only thing that raises the score back toward 0.

## Error / edge paths

- `--propose` without `tag_model` configured: derivation pass
  exits before any cosine work; recall result unaffected (R2676).
- `Store.AcceptDerived` on a (chunkid, tagname) that has no RC
  record: the delete-RC step is a no-op (LMDB delete on missing
  key); F/V write still runs. The user's intent (attach this tag)
  is honored even if the RC vanished between forge-list and
  accept (e.g. another concurrent accept).
- `Store.RejectDerived` on a (chunkid, tagname) that has no RC
  record: delete-RC no-op; RJ write still runs. The user's intent
  (don't ever propose this again) is honored.
- `Store.RejectDerived` called twice for the same (chunkid,
  tagname): each call applies a `-1` delta, so the score walks
  `-1, -2, …` and the timestamp updates to NOW; the magnitude
  returned is the rejection strength. (Not idempotent by design —
  the magnitude feeds `reject_propose_ceiling` / `reject_mention_ceiling`.)
- Concurrent recall calls with `--propose`: each runs its own
  derivation pass through the write actor. Tally increments
  serialize through the actor; no lost updates.
- RF record present but malformed (not a valid varint): reader
  treats as serial 0; the chunk re-derives unconditionally and
  the next write self-corrects (R2681).
- RC record with malformed value (not 8 bytes): `DerivedProposals`
  surfaces tally=0; `WriteDerivedProposal` overwrites with the
  correct shape on next pass (R2681).
- Chunkid orphaned by microfts2 after derivation: the existing
  chunkid-orphan callback cleans RF (and the orphan's RC/RJ
  entries) alongside EC/F via the same callback chain (R2682).
