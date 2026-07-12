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
describe the chunk but aren't yet attached.

> **State C (compute-for-display, #36 recall-proposals-for-display).** The
> pass **computes** proposals and surfaces them for display; it authors
> nothing and writes no durable records. The sole author of durable
> `@ext-candidate`s is a *discerning approver* — the calling agent (full
> conversation context) via `ark ext candidate`, or the human via the Tag
> Forge. The watcher (blind cosine) and the recall secretary (weak Haiku)
> only propose. This reverses the *autonomous producer* landed by migration
> 018; the RC/RJ record family, the candidate algorithm, and the
> accept/reject verbs all survive. RF freshness is retired (dormant).

Owns the storage shape (RC, RJ, and the dormant RF records), the proposal
pass algorithm, the `--propose` flag on `ark connections recall`, and the
Store-level accept/reject API the Tag Forge calls. The LLM-mediated layers —
relevance filtering, new-tag-definition invention, axis-aware proposals — are
item 1 (agent layer) and deliberately deferred; see "What This Spec Does Not
Cover."

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
- **Discernment-gated.** No `ark connections find` workflow step: the
  proposals surface on every recall call, but nothing durable is written
  until a discerning approver — the calling agent (via `ark ext candidate`)
  or the human (via the Tag Forge) — authors it.

The third bullet is the keystone. The shift is from "user must remember to
ask" to "the system proposes on every recall, and an approver with context
decides what to keep" — the compute is automatic, the authoring is deliberate.

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
  not written directly. `ark ext candidate` — authored by the calling
  agent — writes that tag; `Store.AcceptDerived` / `RejectDerived`
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

### RF — Recall Freshness (per-chunk derivation stamp) — DORMANT

**Retired by #36.** The compute-for-display pass keeps no freshness cache, so
RF has no writer or reader. The record class and its Store methods
(`WriteDerivedFreshness` / `ReadDerivedFreshness`) are retained pending a full
teardown (see the RF-teardown gap). Historical shape:

- **Key:** `"RF"` + chunkid varint.
- **Value:** varint `uint64` — historically `max RecordSerial(ED, *)` at the
  chunk's last derivation.
- **Semantic (historical):** the derivation freshness skip — a chunk was fresh
  iff its RF stamp met the current max ED serial.
- **Lifecycle:** formerly written by the pass on every chunk it processed; no
  writer after #36. Residual records are cleaned lazily by the chunkid-orphan
  callback (alongside EC and F).

These three classes are collision-free with each other and with all
existing prefixes. The `R` namespace's other allocations — `RP`,
`RPE`, `RR` for LLM-driven definition proposals — are reserved for
item 1 (agent layer) and do not appear in this slice.

## The derivation pass

Triggered by `ark connections recall --propose`. Runs alongside the
substrate's chunk-scoring pass and returns per-chunk computed proposals for
this call; `enrichProposedTags` surfaces them on each surfaced chunk's
`ProposedTags`. It writes nothing to the index.

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

### Freshness check — RETIRED (#36)

The RF freshness skip is gone. Compute-for-display keeps no durable RC cache,
so there is nothing to keep fresh: the pass always computes for the chunks it
is asked about (a repeat `--propose` recomputes rather than skipping).
`MaxEDSerial` / `MaxEVSerial` are no longer consulted by the pass.

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

### Returning proposals

The pass returns the surviving candidates per chunk transiently for this recall
call (ordered by similarity descending); `enrichProposedTags` copies them onto
each surfaced chunk's `ProposedTags` / `ProposedTagScores`. **The pass authors
no `@ext-candidate`, writes no RC/RF, and performs no synchronous
materialization.**

Durable authoring is the calling agent's separate `ark ext candidate` verb: the
agent — holding full conversation context — reviews the surfaced proposals and
authors the ones it chooses. Only then does the `@ext-candidate` line's `@count`
tally and the indexer derive the RC record on reindex (the 018 path,
unchanged). The `#36` live-conversation injection (`RecallOpts.ConversationChunks`)
folds the conversation's own chunks into the compute so they earn proposals too,
surfaced tag-only (never surfaced back as content).

### Cost characteristics

Per processed chunk:
- One EC vector load (already in cache from the substrate).
- O(ED-count) cosine comparisons, where ED-count is small
  (~hundreds of records on a typical corpus).
- O(top-N) F-record probes and `rejectByChunk` lookups.
- No writes — the pass is read-only (compute-for-display).

Per recall call:
- No freshness bookmark, no RC/RF writes.
- Compute-for-display recomputes each `--propose` call (no freshness skip),
  bounded by the surfaced set + injected conversation chunks — a few thousand
  cosines at most (see the KeepTagless-recompute gap).

## CLI integration

`ark connections recall` gains one new flag:

| Flag       | Default | Meaning                                                                                       |
|------------|---------|-----------------------------------------------------------------------------------------------|
| `--propose`| false   | Run the compute-for-display derivation pass on the substrate's full scored chunk set, and surface this call's computed proposals per surfaced chunk in the result stencil. Persists nothing. |

Without `--propose`, recall behavior is unchanged. With `--propose`, the result
stencil gains a `@chunk-proposed-tags` line for each surfaced chunk that earned
computed proposals this call (see *Stencil additions* below). Proposals for
tagless chunks (present in the derivation chunk set but absent from the surfaced
output when `-all` is off) are computed but not surfaced — nothing is persisted.

`--propose` does **not** alter which chunks appear in the caller's surfaced
output — `-all` still controls that. The derivation pass internally retains
tagless chunks (see *Chunk set* above), but with nothing authored their
computed proposals are simply discarded unless the chunk is surfaced.

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

**Order:** by similarity descending against the chunk's EC vector. Every
`--propose` call computes the scores (there is no freshness skip), so the scores
are always in hand. Cosine is computed against the max ED similarity for each
tagname (a tag can have multiple def files; the chunk's similarity to the tag is
the max across the tag's ED records).

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

Empty / omitted when `--propose` is false or the chunk earned no computed
proposals.

No CLI verbs for listing, accepting, or rejecting derived proposals
ship in this slice. The Tag Forge wires directly to the Store API
below; CLI verbs land when a non-UI caller needs them.

## Public Go API

`RecallOpts` gains one field:

```go
type RecallOpts struct {
    // ... existing fields ...

    // Propose runs the compute-for-display derivation pass on the
    // scored chunks and surfaces the computed proposals via
    // RecalledChunk.ProposedTags. It authors nothing and writes no
    // RC/RF. (#36)
    Propose bool
    // ConversationChunks folds the live-conversation chunk set into
    // the --propose compute (A66 bypassed) so the conversation earns
    // its own proposals, surfaced tag-only. Watcher-populated. (#36)
    ConversationChunks []uint64
}
```

The `RecallResult` shape is unchanged apart from the `ProposedTags`
enrichment. The pass is read-only.

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
// runDerivationPass runs the compute-for-display proposal pass on the
// scored chunk set: per-chunk candidate generation (cosine vs ED+EV,
// top-N, floor, minus already-attached / ext-routed / net-rejected),
// returning the surviving candidates per chunk transiently. It authors
// nothing and writes no RC/RF. enrichProposedTags copies the result
// onto each surfaced chunk's ProposedTags.
func (l *Librarian) runDerivationPass(scoresMap map[uint64]*chunkScoresAcc) (map[uint64]*chunkWork, error)
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

- `--propose` without `[embedding] model` configured → recall succeeds with the
  trigram-only fallback path; the compute is silently skipped (no ED records to
  score against). The caller's recall result is unaffected.
- `--propose` on a default invocation (without `-all`): the pass computes
  proposals for tagless chunks too, but those chunks do not appear in the
  caller's surfaced result and nothing is written, so their computed proposals
  are simply discarded.
- `--propose` on a chunk with no computed proposals (every candidate filtered
  out by external-tag exclusion, already-attached, or the floor): the
  `@chunk-proposed-tags` line is omitted rather than emitted empty. JSON omits
  the `proposedTags` field via the `omitempty` tag.
- ED record set empty (cold corpus, no tag definitions indexed yet) → the compute
  produces no candidates; nothing is written. The recall result is unaffected.
- Malformed RC value on the **forge** read path (`Store.DerivedProposals`) →
  treat tally as 0; the value re-derives on the next reindex of the source
  `@ext-candidate` line (018 substrate; the recall path does not read RC).
- Concurrency: the compute-for-display pass is read-only, so it cannot race a
  concurrent `ark ext accept`/`reject`. Durable authoring (`ark ext candidate`)
  and accept/reject serialize through the closure actor on the `@count` mirror
  line (R3075), independent of the recall pass.


## Performance

The compute-for-display pass cost on a recall call (read-only; no writes):

- O(ED-count) cosines per processed chunk, ~hundreds of comparisons on a
  typical corpus. The vector math is fast (768-dim float32); under 5 ms per
  chunk in practice.
- There is no freshness skip (RF retired), so every `--propose` call
  recomputes. The compute set is bounded by the surfaced set + injected
  conversation chunks, so cost is `≈ (K + turns) × 5 ms` — well under 100 ms
  for default `K=20`. If it ever bites, align the compute set to the surfaced
  chunks rather than the full scored set (see the KeepTagless-recompute gap).

## Test Strategy

- `recall --propose` on a corpus with ED records surfaces computed proposals
  per surfaced chunk via `ProposedTags` **and writes no RC/RF records** (the
  index is unchanged).
- Proposals surface in the same call (no reindex), similarity-descending.
- External-tag exclusion: a chunk carrying an ext-routed `@food` doesn't get a
  `@food` proposal even when ED-similarity is high.
- Already-attached filter: a chunk with an `@cooking` F record doesn't get a
  `@cooking` proposal.
- Reject filter: a net-rejected `(chunk, tag)` in `ExtMap.rejectByChunk` is not
  proposed.
- The EV leg proposes a tag from an existing value (EV) with no ED.
- Conversation injection: a chunk in `ConversationChunks` earns proposals and is
  appended even when A66 would self-exclude it; absent without the injection.
- `Store.AcceptDerived` / `RejectDerived` (mirror-file rewrites) and the RC/RJ
  derivation on reindex are unchanged (018 substrate) — a regression guard.
- `--propose` without `[embedding] model` → recall result unchanged, no
  proposals, no error.

## Lifecycle: the pass computes; the agent authors; the forge accepts/rejects

```
  ark connections recall --propose <inputs>
        │  compute-for-display pass (no writes)
        ▼
  RecalledChunk.ProposedTags        ← surfaced for review, similarity-desc
        │
        │  the calling agent (full context) reviews and authors what it keeps
        ▼
  ark ext candidate <target> <tag>  → @ext-candidate mirror line
        │                              (reindex derives RC[source_tvid+chunk])
        ▼
  ┌──────────────────┐   reads RC via Store.DerivedProposals (forge, #37)
  │ index store (RC) │ ◀───────────────────────────────────────────────────────┐
  └──────────────────┘                                                         │
        ▲                                                                ┌────────────────┐
        │  accept → external @ext | internal file body, +RJ (drops RC)   │ Tag Forge / CLI│
        │  reject → @ext-judgment @count:−1 (derives RJ −, drops RC)     │  accept/reject │
        └──────────────────────────────────────────────────────────────  └────────────────┘
```

The recall pass writes nothing; it only computes and surfaces proposals.
Durable state is authored by a discerning approver — the calling agent via
`ark ext candidate` (stamping a disposition, default external), or the human via
the Tag Forge. **Accept** resolves each candidate per its disposition: `external`
lands an `@ext` mirror edge, `internal` writes the tag into the source file's own
body (falling back to external when the type can't host it — see
`internal-disposition.md`); and every accept also writes a positive
`@ext-judgment @count:+1` (reinforce, R3103). **Reject** writes `@count:-1`. The
018 substrate (RC/RJ derivation, `Store.DerivedProposals` / `AcceptDerived` /
`RejectDerived`) carries the ledger; the diagram's `accept → @ext` line shows the
external path only.

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
