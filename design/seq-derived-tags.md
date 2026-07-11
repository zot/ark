# Sequence: Derived Tags — Derive + Accept + Reject

**Requirements:** R2670, R2671, R2672, R2684, R2685, R2686, R2911, R3054, R3055, R3058, R3059, R3065, R3066, R3067, R3069, R3070, R3071, R3074, R3075, R3079, R3080, R3081, R3082

> **State C (compute-for-display, #36 recall-proposals-for-display).** The
> derivation pass **computes proposals for display only** — it authors nothing
> and writes no RC/RF. `selectCandidates` scores each chunk's EC against ED+EV,
> filters, and returns the surviving candidates transiently for this call
> (R3079); `enrichProposedTags` surfaces them from that transient set, not from
> RC (R3080). The **calling agent** is the sole author of durable
> `@ext-candidate`s, via `ark ext candidate` (R3081); the pass and the recall
> secretary only propose. The live-conversation chunk set (source seed +
> recent-N turns) is folded into the compute so the conversation earns proposals
> too, while never being surfaced back (R3082). RF freshness is retired
> (R2666/R2669/R3072). The 018 substrate below — RC/RJ derivation from the
> agent-authored mirror lines, Accept, Reject — is unchanged; only the
> *autonomous producer* is reversed.

Three flows cover the derived-tag lifecycle. The recall substrate's
*derivation pass* **computes** derived-tag proposals for the current
`ark connections recall --propose` call and returns them for display — it no
longer authors `@ext-candidate`s or stamps RF (#36, R3079). The **calling
agent** authors the proposals it chooses via `ark ext candidate`; the indexer
then derives RC from those mirror lines (the 018 path). The Tag Forge (item 12)
consumes derived proposals and routes user decisions through
`Store.AcceptDerived` (which delegates to `DB.AcceptExtTag`, rewriting
`@ext-candidate` → `@ext` so the reindex lands the attached tag) or
`Store.RejectDerived` (which delegates to `DB.RejectExtTag`, authoring
an `@ext-judgment` whose reindex derives RJ to suppress re-proposal).

## Derive — `ark connections recall --propose`

```
Caller        CLI          Server (Lib.Recall)                     Store        index
(user/agent)  (cmdRecall)  substrate + compute-for-display pass    (helpers)    (ED/EV/EC)
  |               |              |                                      |            |
  |- ark connections recall      |                                      |            |
  |  --propose <inputs> -->|     |                                      |            |
1.1               |- parse flags; RecallOpts.Propose=true (R2667)       |            |
1.2               |- proxy POST /recall -->|                            |            |
1.3               |              |- db.View(txn): force KeepTagless=true internally   |
                  |              |  for the derivation set (R2668)      |            |
1.4               |              |- run vector+trigram EC passes (existing substrate, |
                  |              |  R2620)                              |            |
1.5               |              |- assemble the compute set: the scored chunks PLUS  |
                  |              |  the injected live-conversation chunk set (source  |
                  |              |  seed + recent-N turns, A66 self-exclusion         |
                  |              |  bypassed, tag-only) (R3082)         |            |
1.6               |              |- runDerivationPass(scoresMap): per chunk,          |
                  |              |  selectCandidates —                  |            |
                  |              |    cosine(EC, ED[*]+EV[*]) -> top-N (R2670, R2911)  |
                  |              |    floor min_propose_similarity (R2742)            |
                  |              |    drop already-attached (F set) (R2671)           |
                  |              |    drop ext-routed tagnames (R2672)  |            |
                  |              |    drop net-rejected via ExtMap.rejectByChunk (R3070)
                  |              |  collect survivors into transient work[chunk]      |
                  |              |  (similarity desc)                   |            |
1.7               |              |- return work — NO @ext-candidate authoring, NO     |
                  |              |  RC/RF write, NO syncOnePath materialize (R3079)   |
1.8               |              |- enrichProposedTags(surfaced, work): per surfaced  |
                  |              |  chunk, set ProposedTags/ProposedTagScores from    |
                  |              |  work[chunk] (similarity desc) — NOT from          |
                  |              |  DerivedProposals/RC (R3080, R2685, R2686)         |
1.9               |              |- render stencil: per chunk with non-empty          |
                  |              |  ProposedTags, emit `@chunk-proposed-tags:` after  |
                  |              |  `@chunk-tags` (R2684)               |            |
                  |              |<- read-only View; no write txn       |            |
                  |<- 200 JSON --|                                      |            |
  |<- markdown stencil ----|                                            |            |
```

`--propose` is purely additive at the caller level (no input behavior change)
and now **read-only** — the compute-for-display pass writes nothing to the
index (R3079). The caller's surfaced output gains the `@chunk-proposed-tags`
line per surfaced chunk that this call computed proposals for (R2684, R2685).
Tagless chunks are compute candidates when `--propose` is set (R2668), but
their proposals are invisible in the stencil unless `-all` is also set. The
live-conversation chunk set is folded into the compute (R3082) so the
conversation earns proposals for the calling agent to author, though those
chunks are never surfaced back (R2872).

The calling agent — holding full conversation context — decides which computed
proposals to author as durable `@ext-candidate`s via `ark ext candidate`
(R3081); that authoring, and the RC derivation it triggers on reindex, is the
018 path (unchanged). The recall pass itself neither authors nor materializes,
so a repeat `--propose` recomputes rather than skipping a freshness stamp.

When `--propose` is set but no `[embedding] model` is configured, the
compute pass exits silently — there are no ED/EV records to score against
(R2676); the recall result is unaffected.


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
  **read-only** compute pass and writes nothing to the index, so there
  is no contention (R3079). Durable authoring is the calling agent's
  separate `ark ext candidate`, whose `@count` read-modify-write
  serializes through the actor (R3075).
- RC record with malformed value (not a valid varint tally):
  `DerivedProposals` surfaces tally=0; the next reindex of the
  `@ext-candidate` mirror re-derives the correct `@count`-materialized
  value (R2681, R3058).
- Source chunk orphaned by microfts2: `ExtMap.CleanupSource` strikes
  the derived RC/RJ records (keyed by source tvid, like X) when the
  source `@ext-candidate` / `@ext-judgment` chunk orphans (R3064). RF is
  dormant (#36); any residual RF records are cleaned incidentally by the
  chunkid-orphan callback alongside EC/F until the class is torn down.
