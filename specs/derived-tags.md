# Derived Tags

The recall substrate ([recall.md](recall.md)) already does the
expensive work of finding chunks similar to a context: it loads each
chunk's tags from V records, has the chunk's EC vector in cache, and
the surrounding ED records (tag-definition embeddings) are warm. By
the time it returns a result, the system has assembled exactly what
`ark connections find` operates on. Throwing that away wastes the
cycles.

This spec adds a **statistical derivation pass** that runs as a side
effect of each recall call. Per returned chunk, score the chunk's EC
vector against ED records to surface tag names whose definitions
describe the chunk but aren't yet attached. Persist the surviving
candidates as derived-tag proposals for later human review in the
Tag Forge.

Curation throughput now accrues passively. Every recall call leaves
curation footprints. The vocabulary grows as a side effect of the
system being used.

Owns the storage shape (RC, RJ, RF records), the proposal pass
algorithm, the `--propose` flag on `ark connections recall`, and
the Store-level accept/reject API the Tag Forge will call when it
lands (Forge UI is out of scope here; see ARK-STATE item 6). The
LLM-mediated layers — relevance filtering, new-tag-definition
invention, axis-aware proposals — are item 1 (agent layer) and
deliberately deferred; see "What This Spec Does Not Cover."

Language: Go (Store + Librarian + CLI subcommand flag). Environment:
ark server with the embedding model loaded on demand. The CLI works
in-process via `withDB` when the server is not running, gated by
the same `[embedding] model` checks as the underlying recall substrate.

## Why this exists

Today, curation happens through two channels: the user reviewing
material directly, and explicit `ark connections find` runs. The
conversations the user has *with the assistant* — where most
sense-making actually happens — never reach curation. This pass
closes that gap.

Three things land at once:

- **Conversation as curation input.** Recall is driven by
  conversation turns. The proposals it generates reflect what the
  user is currently thinking about, not what they remembered to ask.
- **AI perspective injected into curation.** D-record similarity
  is an automated proposal stream. The curator gains a collaborator
  in deciding what tags attach where.
- **Automatic.** No `ark connections find` workflow step. Curation
  accrues passively as a byproduct of ordinary recall use.

The third bullet is the keystone. The shift from "user must remember
to ask" to "happens whenever recall runs" is the actual novelty.

## What this slice covers

The proposal pass in this spec is **statistical only** — derived
purely from D-record similarity. The LLM-mediated relevance filter
and new-tag-definition inventor live in the agent layer (ARK-STATE
item 1, gated on the "Still blurry" items in
`.scratch/CONTEXTUAL-RECALL.md`). Per the *Open question: ship
statistically or wait for LLM filter* resolution there, this slice
ships first and accepts noisier forge input; the LLM filter lands
later without changing the storage shape.

## Storage

Three new record classes, all under the `R` (recall) namespace
established by `RD` ([discussed-tags.md](discussed-tags.md)).

### RC — Recall Candidate (derived attach proposal)

- **Key:** `"RC"` + source_tvid varint + target_chunkid varint — a
  tag-derived family key, sibling of the X record. `source_tvid` is the
  tvid of the `@ext-candidate` tag whose TARGET named the chunk.
- **Value:** varint tally, materialized from the `@ext-candidate` line's
  `@count` field.
- **Semantic:** one record per (source `@ext-candidate` tvid, target
  chunk). The proposed `(tagname, value)` is **not** stored — it is
  recovered from the source tvid (`TvidMap.Resolve` → the `@ext-candidate`
  value → `ParseExtTarget`), exactly as X recovers its routed tag. A higher
  tally is a stronger candidate.
- **Lifecycle:** **derived** on (re)index of the `@ext-candidate` file tag,
  not written directly. The propose pass (`--propose`) and `ark ext
  candidate` author that tag; `Store.AcceptDerived` / `RejectDerived`
  rewrite it to `@ext` / `@ext-judgment`, whose reindex drops the RC and
  lands the X+V edge / RJ. Struck via `ExtMap.CleanupSource` when the source
  chunk orphans.
- **Reverse lookup** (proposals for one chunk): the in-memory
  `ExtMap.candidateSourcesByChunk` map (target_chunkid → source tvids).

### RJ — Recall Judgment (signed per-edge relevance)

- **Key:** `"RJ"` + source_tvid varint + target_chunkid varint. Mirrors RC —
  `source_tvid` is the `@ext-judgment` tag whose TARGET named the chunk, and
  the tagname is recovered from it (not stored).
- **Value (v3):** `signed-varint(score) + 8-byte BE unix nanos`. `score < 0`
  = net-rejected (magnitude `-score`); `score > 0` = reinforced; `score == 0`
  = neutral, equivalent to record-absent. The score is **materialized from
  the `@ext-judgment` line's signed `@count`**.
- **Semantic:** one signed relevance figure per (source `@ext-judgment`
  tvid, target chunk) edge. The derivation pass suppresses re-proposal when
  the edge is net-rejected, reading the in-memory `ExtMap.rejectByChunk` map
  (not an RJ key lookup — RJ is source_tvid-keyed). `reject_propose_ceiling`
  / `reject_mention_ceiling` in `[recall]` gate on the magnitude.
- **Lifecycle:** **derived** on reindex of the `@ext-judgment` file tag,
  which `Store.RejectDerived` (and `ark ext reject`) authors by creating or
  decrementing its signed `@count`. Struck via `ExtMap.CleanupSource` when the
  source chunk orphans.

### RF — Recall Freshness (per-chunk derivation stamp)

- **Key:** `"RF"` + chunkid varint.
- **Value:** varint-encoded `uint64` — the txn serial that was
  "current" against the ED record set when this chunk was last
  processed by the derivation pass. Specifically, `max
  RecordSerial(ED, *)` at processing time.
- **Semantic:** "this chunk has been processed against the ED
  landscape as of serial N." A chunk is *fresh* (skip-eligible)
  for derivation iff its RF stamp is greater than or equal to the
  current `max RecordSerial(ED, *)` — nothing has changed in tag
  definitions since the last pass touched it.
- **Lifecycle:** written by the derivation pass on every chunk
  it processes (whether or not proposals result). Deleted lazily —
  an RF record for a chunkid orphaned by microfts2 is cleaned up
  by the existing chunkid-orphan callback path (alongside EC and
  F record cleanup); the substrate is tolerant of missing RF
  records (treats them as "stale, process this chunk").

These three classes are collision-free with each other and with all
existing prefixes. The `R` namespace's other allocations — `RP`,
`RPE`, `RR` for LLM-driven definition proposals — are reserved for
item 1 (agent layer) and do not appear in this slice.

## The derivation pass

Triggered by `ark connections recall --propose`. Runs alongside
the substrate's chunk-scoring pass; produces no caller-visible
output (proposals land in the index as a side effect).

### Chunk set

Derivation operates on the substrate's **full scored chunk set**,
independent of the surfacing filter. Concretely, the substrate
runs internally with `KeepTagless=true` whenever `Propose=true`,
so the derivation pass sees tagless chunks. The user-facing recall
result then applies the caller's effective `KeepTagless` value
(default false; `-all` makes it true) as a separate step.

Why: a tagless chunk is the highest-value derivation candidate
(every D-similarity hit is a novel proposal). Skipping tagless
chunks in derivation just because the default surfacing filter
drops them would defeat the pass on cold corpora and on chunks
that have escaped curation. Surfacing and derivation are
orthogonal — `--propose` should never imply changes to the
caller's surfaced output.

### Freshness check

For each chunk in the derivation chunk set:

1. Read `RF[chunkid]`. If absent, treat as serial 0.
2. Compute `maxSerial = max(maxED, maxEV)` where `maxED` /
   `maxEV` are the max `RecordSerial` across all ED / EV keys
   (`Store.MaxEDSerial` / `MaxEVSerial`). The `S` substrate makes
   each a single bookmark computation; the recall pass holds the
   result for the batch. EV is folded in because the propose pass
   now scores against tag *values* too (R2911), so a value change
   must also invalidate freshness.
3. If `RF[chunkid] >= maxSerial`, the chunk is fresh — skip
   derivation for this chunk. The chunk still appears in the recall
   result; only the proposal pass is skipped.
4. Otherwise, run derivation on this chunk. After it completes
   (with or without proposals), write `RF[chunkid] = maxSerial`.

The skip keeps re-runs cheap. The common case — recall called many
times across a session without tag-definition or tag-value changes
— runs derivation only once per chunk.

### Candidate generation

For each eligible chunk:

1. Compute cosine similarity between the chunk's EC vector and
   every tag vector — both ED (tag *definitions*) and EV (existing
   tag *values*, R2911) — aggregating per tag by max, so a chunk
   earns a tag for resembling a definition *or* an existing value.
   Take the top-N by similarity (`derivationK`, default 10).
1a. Drop any candidate whose per-tag max cosine is below
    `[recall].min_propose_similarity` (default 0.70). The floor
    keeps the top-K from filling with loosely related neighbors
    when a chunk has few strong matches. Tuning the floor by eye
    relies on the parenthesized scores the recall stencil renders
    (`@chunk-proposed-tags: foo (0.72), bar (0.58)`).
2. Filter out tags the chunk already carries (lookup via F
   records). Reduces noise; the curator doesn't need to see
   suggestions to attach a tag that's already attached.
3. Filter out tags routed onto the chunk via `@ext` (bare-name
   rule — skip any candidate whose tagname matches *any*
   ext-routed tagname on the chunk, value-agnostic).
   External routing already asserts authority over those tag
   names on this chunk; shadowing it with a derived proposal
   would create curator noise. The conservative bare-name default
   may relax to exact-pair matching once we see real proposal
   streams; that's a future change.
4. Filter out candidates the curator has net-rejected, read from the
   in-memory `ExtMap.rejectByChunk` map (target_chunkid → tagname →
   signed score; negative = net-rejected). The curator already said no;
   don't re-surface. The `reject_propose_ceiling` knob can relax this
   for low-magnitude rejections.

### Writing proposals

For each surviving candidate, the pass **authors an `@ext-candidate`
file tag** via `DB.CandidateExtTag` — it does not write RC directly.
The `@ext-candidate` line's `@count` is the repetition tally: a new
`(TARGET, tag)` writes `@count: 1`; an exact-identity repeat increments
it. The indexer derives the RC record (`source_tvid + target_chunkid →
@count`) when that file tag is indexed.

Authoring, the RF freshness stamp, and the reindex all run inside one
closure-actor op per recall call (not per proposal). The pass then
**synchronously materializes**: it reindexes each distinct touched
mirror once (deduped by target file) so the RC records derive and the
proposals surface in the same `--propose` call, rather than only after
the async watcher pass.

### Cost characteristics

Per processed chunk:
- One EC vector load (already in cache from the substrate).
- O(ED-count) cosine comparisons, where ED-count is small
  (~hundreds of records on a typical corpus).
- O(top-N) F-record probes and RJ-record probes.
- O(survivors) RC writes (typically a handful).

Per recall call:
- One `max(S over ED)` bookmark computation.
- Per-chunk freshness check (cheap index get).
- Derivation only on stale chunks. In steady state, most chunks
  are fresh — the marginal cost of `--propose` on a recall call
  is near zero.

## CLI integration

`ark connections recall` gains one new flag:

| Flag       | Default | Meaning                                                                                       |
|------------|---------|-----------------------------------------------------------------------------------------------|
| `--propose`| false   | Run the statistical derivation pass on the substrate's full scored chunk set. Persist surviving candidates as RC records, and surface accumulated proposals per surfaced chunk in the result stencil. |

Without `--propose`, recall behavior is unchanged. With
`--propose`, the result stencil gains a `@chunk-proposed-tags`
line for each surfaced chunk that has any RC records (see
*Stencil additions* below). Proposals for tagless chunks (which
are present in the derivation chunk set but absent from the
surfaced output when `-all` is off) are not visible in the
stencil but are persisted to the index for the Tag Forge to pick up.

`--propose` does **not** alter which chunks appear in the
caller's surfaced output — `-all` still controls that. The
derivation pass internally retains tagless chunks (see *Chunk
set* above) to do background curation work, but those chunks
appear in the result stencil only when `-all` is set.

### Stencil additions

When `--propose` is set, each surfaced chunk with at least one
RC record gains a single line in the markdown stencil:

```
@chunk-proposed-tags: priority, status, axis
```

Comma-separated bare tagnames, parallel to `@chunk-tags`. The
list is the *accumulated* RC record set for the chunk (this
call's emissions plus prior calls' proposals not yet accepted
or rejected) — not just survivors of this pass.

**Order:** by similarity descending against the chunk's EC
vector. Similarity is computed at stencil-emission time: for a
chunk whose RF stamp is fresh (derivation skipped), per-RC
cosine computation is bounded by the small RC-count for that
chunk (typically a handful); for a chunk that derived this
call, the scores are in hand. Cosine is computed against the
max ED similarity for each tagname (a tag can have multiple
def files; the chunk's similarity to the tag is the max across
the tag's ED records).

Bare-tag values only in this slice (no `@chunk-proposed-tag-value:`
sub-items). When item 1's LLM lands and proposes specific values,
those gain sub-items in the same shape as `@chunk-tag-value:`.

For the JSON shape (`--json`), each `RecalledChunk` gains:

```go
type RecalledChunk struct {
    // ... existing fields ...
    ProposedTags []string `json:"proposedTags,omitempty"` // similarity-desc order
}
```

Empty / omitted when `--propose` is false or the chunk has no
RC records.

No CLI verbs for listing, accepting, or rejecting derived proposals
ship in this slice. The Tag Forge wires directly to the Store API
below; CLI verbs land when a non-UI caller needs them.

## Public Go API

`RecallOpts` gains one field:

```go
type RecallOpts struct {
    // ... existing fields ...

    // Propose runs the statistical derivation pass on returned
    // chunks. Surviving candidates are written as RC records.
    // Result shape unchanged; proposals are persisted side-effects.
    Propose bool
}
```

The `RecallResult` shape is unchanged. The pass is a side effect.

New Store methods:

```go
// DerivedProposals returns a chunk's derived proposals, sorted by tally
// descending. Reads ExtMap.candidateSourcesByChunk, recovers each
// candidate's (tagname, value) from its source tvid (TvidMap.Resolve +
// ParseExtTarget), and reads the tally from the RC record. Skips
// tagnames net-rejected in ExtMap.rejectByChunk (defense-in-depth).
func (s *Store) DerivedProposals(chunkID uint64) ([]DerivedProposal, error)

type DerivedProposal struct {
    ChunkID uint64
    Tagname string
    Tally   uint64
}

// AcceptDerived commits a proposal by authoring the promotion in the
// target's mirror file: it resolves the chunk's locator and delegates to
// DB.AcceptExtTag (@ext-candidate → @ext). On reindex the RC derivation
// drops and the X+V edge lands — no separate RC-delete-and-attach step.
func (s *Store) AcceptDerived(db *DB, chunkID uint64, tagname, value string) error

// RejectDerived records a durable rejection by authoring an @ext-judgment
// file tag via DB.RejectExtTag (create-or-decrement its signed @count).
// The indexer derives the RJ record; the returned magnitude reads back
// from ExtMap.rejectByChunk (0 until that reindex materializes it).
func (s *Store) RejectDerived(db *DB, chunkID uint64, tagname string) (uint64, error)
```

The pair `AcceptDerived` / `RejectDerived` is front-loaded for the forge
UI work that follows (item 6); both edit the mirror file rather than
writing bbolt records directly. They have no in-tree caller yet; the Tag
Forge wires to them when it lands.

Internal helpers (not part of the public API contract; sketched
for the design phase):

```go
// derivationPass runs the proposal pass on a batch of chunks
// inside the recall path. Holds maxED for the batch; per-chunk
// freshness check; per-chunk candidate generation; then, in one
// actor op, authors an @ext-candidate file tag per survivor,
// stamps RF, and reindexes each touched mirror so the RC records
// derive in this same call.
func (l *Librarian) derivationPass(chunks []RecalledChunk) error
```

## Lua bridge

`sys.recall` gains a `propose` boolean field:

```lua
local result = sys.recall(inputs, {
    k = 20,
    propose = true,
})
```

No new Lua methods for accept/reject in this slice — the forge UI
isn't here yet. When it lands, `sys.derived` exposes `proposals`,
`accept`, `reject` as a sibling family to `sys.discussed`.

## Empty / Error Cases

- `--propose` without `[embedding] model` configured → recall succeeds
  with the trigram-only fallback path; derivation is silently
  skipped (no ED records to score against). The caller's recall
  result is unaffected.
- `--propose` on a default invocation (without `-all`): the
  derivation pass processes tagless chunks too, but those chunks
  do not appear in the caller's surfaced result. Proposals
  written for tagless chunks become visible only when the user
  later runs the Tag Forge.
- `--propose` on a chunk with no RC records (e.g. a chunk where
  every candidate was filtered out by external-tag exclusion or
  already-attached): the `@chunk-proposed-tags` line is omitted
  rather than emitted empty. JSON omits the `proposedTags` field
  via the `omitempty` tag.
- ED record set empty (cold corpus, no tag definitions indexed
  yet) → derivation produces no candidates; RF stamps still get
  written (max=0). Future recall calls remain efficient.
- RC record with malformed value (not 8 bytes) → treat tally as
  0; the next write overwrites with a corrected value. Should
  not happen — the writer always produces 8-byte values.
- RF record with malformed value → treat as serial 0 (force
  re-derivation). Same self-healing pattern.
- Acceptance racing with a re-derivation: `AcceptDerived` and the
  derivation pass both go through the same write actor, so they
  serialize. A pass that produces a proposal which is accepted
  before the pass returns sees the RC delete after its own write —
  the deleted record is fine; future passes won't re-propose
  because the chunk now carries the tag (F-record check filters
  it).

## Performance

The derivation pass cost on a recall call:

- Cold (first time touching a chunk): O(ED-count) cosines per
  chunk, ~hundreds of comparisons on a typical corpus. The vector
  math is fast (768-dim float32); under 5 ms per chunk in
  practice.
- Warm (chunk already processed against current ED set): single
  index get for `RF[chunkid]`, near-zero cost.
- Per-batch fixed cost: `max(S over ED)` bookmark computation,
  cheap with the existing S substrate (`WalkRecordsSinceSerial`).

Recall calls with `--propose` and a warm cache should add under
10 ms total to substrate-only recall latency. Cold-cache cost
(no RF records yet) is bounded by `K * 5 ms` ≈ 100 ms for
default `K=20`; this is amortized across future passes.

## Test Strategy

- `recall --propose` on a corpus with ED records writes RC
  records for each returned chunk's surviving candidates.
- A second `recall --propose` immediately after, without ED
  changes, skips derivation (RF freshness check); RC records and
  their tallies are unchanged.
- A tag-definition change (new ED record) **or a tag-value change
  (new EV record)** invalidates RF on the next recall — the
  affected chunks re-derive and tally increments where the same
  candidate survives.
- External-tag exclusion: a chunk carrying an ext-routed `@food`
  doesn't get `@food` proposals even when ED-similarity is high.
- Already-attached filter: a chunk with an `@cooking` F record
  doesn't get `@cooking` proposals.
- Rejection persistence: write an RJ record for a (chunk, tag);
  subsequent `recall --propose` skips that candidate.
- `Store.AcceptDerived` drops RC and writes F/V for the attach;
  the next derivation pass doesn't re-propose because the F
  filter now matches.
- `Store.RejectDerived` drops RC and writes RJ; subsequent
  derivation skips the candidate.
- RF malformed-value tolerance: corrupt an RF value to 1 byte;
  next derivation re-runs the chunk and overwrites RF correctly.
- `--propose` without `[embedding] model` → recall result unchanged, no
  RC records written, no error.

## Lifecycle: substrate writes RC + RF; forge writes RJ via Store API

```
                  ┌────────────────────────────────┐
                  │ ark connections recall         │
                  │   --propose <inputs>           │
                  └────────────┬───────────────────┘
                               │ derivation pass
                               ├──── writes RC ──────┐
                               └──── writes RF ──────┤
                                                     ▼
                                          ┌──────────────────┐
                                          │ index store      │
                                          └──────────────────┘
                                                     ▲
                  ┌────────────────────────────────┐ │
                  │ Tag Forge UI (item 6)          │ │
                  │   reads RC via                 │ │
                  │   Store.DerivedProposals       │─┘
                  │                                │
                  │   on accept:                   │ writes F/V
                  │     Store.AcceptDerived ───────│─→ drops RC
                  │                                │
                  │   on reject:                   │ writes RJ
                  │     Store.RejectDerived ───────│─→ drops RC
                  └────────────────────────────────┘
```

The substrate has no opinion about *who* consumes RC records. Any
future caller — Tag Forge, an ambient watcher, a CLI verb when one
emerges — uses the same `Store.DerivedProposals` /
`Store.AcceptDerived` / `Store.RejectDerived` surface.

## What This Spec Does Not Cover

These are intentionally out of scope. They belong to item 1 (agent
layer) or item 6 (forge UI) and will be specified separately.

- **LLM relevance filtering.** The agent layer (ARK-STATE item 1)
  inserts a Haiku judgment step that filters statistical proposals
  before they reach the forge. This slice ships the statistical
  proposals raw; the forge absorbs noise via its existing
  accept/reject loop.
- **New-tag-definition proposals.** The LLM can invent tags that
  don't exist yet (e.g. propose `@appt-mgt-sys` with a draft
  definition). Those proposals use a separate record class (RP +
  RPE + RR), reserved in the `R` namespace and specified in item 1.
- **Axis-aware proposals.** Tag-axis classification (About,
  Connection, etc.; see `.scratch/TAG-AXES.md`) requires LLM
  judgment beyond literal `@axis:` declaration matching. Item 1
  territory.
- **Forge UI rendering.** The Tag Forge consumes RC records as a
  new proposal source. UI rendering, sort order, accept/reject
  controls, and provisional-tag framing live in item 6 (`/ui-thorough`).
- **CLI verbs `ark derived list|accept|reject`.** Deferred until
  a non-UI caller needs them. The Store API above is sufficient
  for the forge.
- **Recall agent process lifecycle, target-session discovery,
  compaction, multi-tenancy.** Five "Still blurry" items in
  `.scratch/CONTEXTUAL-RECALL.md`. Gates item 1; orthogonal to
  this slice.
- **RJ TTL / un-reject.** Rejections are sticky in v1. A future
  `ark derived unreject` verb may add explicit removal; TTL-based
  decay is a future call.
- **Cross-corpus derivation.** RC records are scoped to chunks
  the local corpus already carries. Cross-corpus derivation (a
  chunk in corpus A surfacing a candidate from corpus B's tag
  vocabulary) is Phase 2C turbo territory.
