# Sequence: Derived Tags — Derive + Accept + Reject

**Requirements:** R2666, R2669, R2670, R2671, R2672, R2684, R2685, R2686, R2911, R3054, R3055, R3058, R3059, R3065, R3066, R3067, R3068, R3069, R3070, R3071, R3072, R3074, R3075, R3076

> **State B (tag-derived RC/RJ subsystem, #22 Pass B+C).** The diagram and
> prose below reflect the **inverted** producer path. The derivation pass no
> longer writes bbolt RC directly; it authors an `@ext-candidate` file tag via
> `DB.CandidateExtTag` (an exact-identity duplicate bumps the line's `@count`,
> R3075), and the indexer derives RC keyed `source_tvid + target_chunkid`
> (sibling of the X record, seq-ext-routing.md), with the tally materialized
> from `@count` (R3058, R3074). Accept delegates to `DB.AcceptExtTag`
> (`@ext-candidate` → `@ext`; on reindex the RC drops and the X+V edge lands,
> R3071); reject delegates to `DB.RejectExtTag` (authors an `@ext-judgment`
> whose signed `@count` derives the RJ, R3069). Reads go through the in-memory
> `ExtMap.candidateSourcesByChunk` / `rejectByChunk` maps, not RC/RJ prefix
> scans (R3065, R3066, R3067). The propose pass **synchronously materializes**
> its own proposals — it reindexes each touched mirror once, on the actor, so
> the RC records derive and proposals surface in the same `--propose` call
> (R3076). RF freshness (R3072) is unchanged.

Three flows cover the derived-tag lifecycle. The recall substrate's
*derivation pass* authors `@ext-candidate` file tags (from which the
indexer derives RC) and stamps RF as a side effect of
`ark connections recall --propose`. The Tag Forge (item 12) later
consumes the derived proposals and routes user decisions through
`Store.AcceptDerived` (which delegates to `DB.AcceptExtTag`, rewriting
`@ext-candidate` → `@ext` so the reindex lands the attached tag) or
`Store.RejectDerived` (which delegates to `DB.RejectExtTag`, authoring
an `@ext-judgment` whose reindex derives RJ to suppress re-proposal).

## Derive — `ark connections recall --propose`

```
Caller        CLI               Server (Lib.Recall)              Store                    index
(user/agent)  (cmdRecall)       substrate + derivation worker    (helpers)                (RC/RJ/RF + EC/ED)
  |                        |              |                              |                          |
  |- ark connections recall ...           |                              |                          |
  |  --propose <inputs> -->|              |                              |                          |
1.1                        |- parse flags;                               |                          |
                           |  RecallOpts.Propose=true                    |                          |
                           |  (R2667)                                    |                          |
1.2                        |- proxy POST  |                              |                          |
                           |  /recall --->|                              |                          |
1.3                        |              |- db.View(txn):               |                          |
                           |              |  force                       |                          |
                           |              |  KeepTagless=true            |                          |
                           |              |  internally for              |                          |
                           |              |  derivation chunk            |                          |
                           |              |  set (R2668)                 |                          |
1.4                        |              |- run vector+trigram          |                          |
                           |              |  EC passes (existing         |                          |
                           |              |  substrate, R2620)           |                          |
1.5                        |              |- partition scored:           |                          |
                           |              |  surfaced[]                  |                          |
                           |              |  (caller's effective         |                          |
                           |              |  KeepTagless) +              |                          |
                           |              |  derivation[]                |                          |
                           |              |  (full set, R2668)           |                          |
1.6                        |              |- derivationPass(             |                          |
                           |              |    scored):                  |                          |
                           |              |- MaxEDSerial() +             |                          |
                           |              |    MaxEVSerial() --->        |                          |
                           |              |    maxSerial =               |                          |- max(ED,EV)
                           |              |    max(ED,EV) walk           |                          |   S bookmarks
                           |              |    (R2669, R2911)            |                          |
                           |              |<- maxSerial ----------       |                          |
1.7                        |              |- per chunk in derivation:    |                          |
                           |              |  - ReadDerivedFreshness ---> |                          |
                           |              |     (R2669)                  |                          |
                           |              |  - if rf >= maxSerial:       |                          |
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
                           |              |         ED[*]+EV[*]) → top-N |                          |
                           |              |         (R2670, R2911)       |                          |
                           |              |      drop candidates         |                          |
                           |              |         in F-record set      |                          |
                           |              |         (R2671)              |                          |
                           |              |      drop candidates         |                          |
                           |              |         matching ext-routed  |                          |
                           |              |         tagnames (R2672)     |                          |
                           |              |      drop net-rejected via   |                          |
                           |              |         ExtMap.rejectByChunk |                          |
                           |              |         (R3070)              |                          |
                           |              |      for each survivor:      |                          |
                           |              |         author @ext-candidate|                          |
                           |              |         file tag via         |                          |
                           |              |         DB.CandidateExtTag   |                          |
                           |              |         (@count 1 / bump,    |                          |
                           |              |         R3068, R3075)        |                          |
                           |              |      WriteDerivedFreshness   |                          |
                           |              |         (chunkid, maxSerial) |                          |- Put RF[chunk]
                           |              |                              |                          |   varint(maxSerial)
                           |              |                              |                          |   (R2666)
                           |              |- then, one actor op:         |                          |
                           |              |    reindex each distinct     |                          |
                           |              |    touched mirror once       |                          |- derive RC[src_tvid
                           |              |    via DB.syncOnePath        |                          |   + target_chunk] =
                           |              |    (R3076) --->              |                          |   varint tally
                           |              |                              |                          |   (from @count,
                           |              |                              |                          |   R3058)
1.8                        |              |- enrichProposedTags(surfaced,|                          |
                           |              |    chunkSimilarities):       |                          |
                           |              |  per surfaced chunk          |                          |
                           |              |  with any candidate:         |                          |
                           |              |  - DerivedProposals: read    |                          |
                           |              |     candidateSourcesByChunk  |                          |
                           |              |     → TvidMap.Resolve +      |                          |
                           |              |     ParseExtTarget, tally    |                          |
                           |              |     from RC (R3067)          |                          |
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
behavior change). The derivation pass authors `@ext-candidate` file
tags and stamps RF as a side effect, then synchronously reindexes the
touched mirrors so the RC records derive in the same call (R3076); the
caller's surfaced output gains the `@chunk-proposed-tags` line per
surfaced chunk that accumulated proposals (R2684, R2685). Tagless
chunks are derivation candidates when `--propose` is set (R2668), but
their proposals are invisible in the stencil unless `-all` is also set.

The write+materialize phase is one closure-actor op: authoring every
survivor's `@ext-candidate` (the `@count` read-modify-write inside
`DB.CandidateExtTag` can't lose a concurrent bump, R3075), stamping RF,
and reindexing each **distinct** touched mirror once via
`DB.syncOnePath` all run on the actor the caller already holds.
Embedding stays deferred to `BatchEmbedChunks`, so the sync cost is
only FTS + tag-extraction + derivation of the tiny mirror files.

When `--propose` is set but no `[embedding] model` is configured, the
derivation pass exits silently — there are no ED/EV records to score
against (R2676); the recall result is unaffected.

## Accept — `Store.AcceptDerived(db *DB, chunkID uint64, tagname, value string) error`

Called by the Tag Forge UI (item 12) when the user accepts a derived
candidate. No CLI surface in this slice — the forge calls the Store
API directly. `AcceptDerived` is **re-homed to the mirror path**: it
resolves the chunk's locator and delegates to `DB.AcceptExtTag`; the
accept loop closes by construction on the reindex (R3071).

```
Tag Forge UI         Store.AcceptDerived DB.AcceptExtTag      index
(item 12)            (resolve+delegate)  (write actor)        (mirror file + X/V)
  |                  |                    |                       |
  |- accept(c,t,v)-->|
2.1                  |- ChunkInfo(c):     |                       |
                     |  resolve path:range|                       |
                     |- AcceptExtTag(     |                       |
                     |    target,t,v) --->|                       |
2.2                  |                    |- rewrite matching     |
                     |                    |  @ext-candidate line  |
                     |                    |  → @ext (temp+rename) |
                     |                    |  (R3054)              |
2.3                  |                    |- reindex mirror --->  |
                     |                    |  (watcher / sync)     |
2.4                  |                    |                       |- derive: RC drops
                     |                    |                       |   (no @ext-candidate)
                     |                    |                       |- Put X[src_tvid+chunk]
                     |                    |                       |- append V edge; the
                     |                    |                       |   tag attaches (R3071)
  |<- err            |
```

The accept is a **mirror-file rewrite**, not a bbolt edit: `AcceptExtTag`
rewrites the matching `@ext-candidate` line(s) to `@ext` in one atomic
temp+rename (R3054). On the subsequent reindex the indexer derives the
X record and appends the routed V edge (the tag attaches), and the RC
derivation drops because the `@ext-candidate` line is gone — the accept
loop closes by construction, with no separate RC-delete-and-attach step
(R3071). `AcceptDerived` takes `*DB` to reach the mirror-authoring path.

Empty `value` promotes every value for that tag; a bare-tag candidate
(the shape the statistical pass authors) promotes to a bare `@ext`.

## Reject — `Store.RejectDerived(db *DB, chunkID uint64, tagname string) (uint64, error)`

Called by the Tag Forge UI (item 12) when the user rejects a derived
candidate. **Inverts** to the mirror path: it authors an `@ext-judgment`
file tag via `DB.RejectExtTag`, whose reindex derives the RJ record so
the derivation pass never re-proposes the same `(chunk, tagname)`.

```
Tag Forge UI         Store.RejectDerived DB.RejectExtTag      index
(item 12)            (resolve+delegate)  (write actor)        (mirror file + RJ)
  |                  |                    |                       |
  |- reject(c,t)---->|
3.1                  |- ChunkInfo(c):     |                       |
                     |  resolve path:range|                       |
                     |- RejectExtTag(     |                       |
                     |    target,t,"") -->|                       |
3.2                  |                    |- create/decrement     |
                     |                    |  signed @count on the |
                     |                    |  @ext-judgment line   |
                     |                    |  (R3055, R3075);      |
                     |                    |  @count==0 removes it |
3.3                  |                    |- reindex mirror --->  |
                     |                    |                       |- derive RJ[src_tvid
                     |                    |                       |   + chunk] = signed
                     |                    |                       |   @count + 8B nanos
                     |                    |                       |   (R3059)
                     |                    |                       |- update rejectByChunk
  |<- magnitude      |
```

`RejectExtTag` creates or decrements a signed `@count` on the
`@ext-judgment` line as one closure-actor read-modify-write (R3075):
the first reject writes `@count: -1`, a repeat walks `-2, -3, …`, and a
`@count` that reaches 0 removes the line (absent ≡ neutral). On reindex
the indexer materializes `RJ[source_tvid + target_chunkid]` from that
signed `@count` (R3059) and refreshes the in-memory `rejectByChunk`
map; the returned magnitude reads back from that map (0 until the
reindex lands). The judgment edge is signed and bidirectional, but
there is no manual un-reject verb — the next derivation pass consults
`rejectByChunk` (step 1.7 above) and skips a net-rejected `(chunk,
tag)` (R3070, R2881). `RejectDerived` takes `*DB` to reach the
mirror-authoring path.

## Error / edge paths

- `--propose` without `[embedding] model` configured: derivation pass
  exits before any cosine work; recall result unaffected (R2676).
- `Store.AcceptDerived` on a chunk with no locator (e.g. the chunk
  orphaned between forge-list and accept): `ChunkInfo` fails and the
  method is a silent no-op. When a locator resolves, `AcceptExtTag`
  is a no-op if the `@ext-candidate` line is already gone (R3054);
  either way the user's intent is honored without error.
- `Store.RejectDerived` on a chunk with no locator: same no-op via
  the failed `ChunkInfo`. When a locator resolves, `RejectExtTag`
  authors the `@ext-judgment` regardless of whether a candidate RC
  still exists — the intent (don't propose this again) is honored.
- `Store.RejectDerived` called twice for the same `(chunk, tagname)`:
  each call decrements the `@ext-judgment` line's signed `@count`
  (`-1, -2, …`); the reindex re-materializes RJ with the new score
  and refreshes `rejectByChunk`. The magnitude feeds
  `reject_propose_ceiling` / `reject_mention_ceiling`. Because the
  count is file-backed, the read-modify-write is one closure-actor op
  and cannot lose a concurrent decrement (R3075).
- Concurrent recall calls with `--propose`: each runs its own
  derivation pass, authoring `@ext-candidate` file tags through the
  closure actor. The `@count` bumps serialize through the actor; no
  lost updates (R3075).
- RF record present but malformed (not a valid varint): reader
  treats as serial 0; the chunk re-derives unconditionally and
  the next write self-corrects (R2681).
- RC record with malformed value (not a valid varint tally):
  `DerivedProposals` surfaces tally=0; the next reindex of the
  `@ext-candidate` mirror re-derives the correct `@count`-materialized
  value (R2681, R3058).
- Source chunk orphaned by microfts2: `ExtMap.CleanupSource` strikes
  the derived RC/RJ records (keyed by source tvid, like X) when the
  source `@ext-candidate` / `@ext-judgment` chunk orphans (R3064). RF,
  which is chunkid-keyed, is still cleaned by the chunkid-orphan
  callback alongside EC/F (R2682).
