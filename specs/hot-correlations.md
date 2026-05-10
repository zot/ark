# Hot Correlations

The corpus-wide cosine sweep that produces orphan-candidate caches
and underwrites the curation view's "what's interesting?" surfaces.
Phase 1E of the curation plan. First real client of the vector
freshness substrate (Phase 1C, S records).

Two halves:

1. **Tag → chunk top-K cache (`HC` records).** A persistent cache
   of the top-K best-scoring chunks per tag, refreshed
   incrementally via S serials. Read by the curation view to
   surface orphan candidates.
2. **Tag → tag query functions.** Three live, on-demand lenses
   on the ED↔ED matrix — conflict, drift, related — small enough
   not to need a cache.

Plus a tmp:// progress doc that surfaces sweep activity through
existing pubsub plumbing.

Language: Go. Environment: ark server, in-process embedding
context already loaded for ED writes.

## Public API

### Sweep invocation and progress

```go
// SweepHotCorrelations runs the incremental sweep against the
// HC cache, using S-record serials to skip unchanged work.
// Synchronous; intended to be called from a background goroutine
// (CLI, Lua, future cron-via-tag subscriber).
// Returns when the sweep completes. Progress is published through
// the tmp://sweep/hot-correlations.md document.
func (l *Librarian) SweepHotCorrelations() (*SweepResult, error)

// SweepResult is what the sweep returns to the caller. The same
// numbers also land in the tmp:// progress doc as @sweep-* tags.
type SweepResult struct {
    StartedAt    time.Time
    CompletedAt  time.Time
    DurationMS   int64
    ChangedEDs   int     // ED records advanced past last_sweep_serial
    ChangedECs   int     // EC records advanced past last_sweep_serial
    TagsRebuilt  int     // tags whose full top-K was recomputed (driven by ED changes)
    TagsTouched  int     // tags whose top-K was incrementally adjusted (driven by EC changes)
    OrphanTotal  int     // total HC entries after sweep
    FromScratch  bool    // true if last_sweep_serial was zero
}
```

### Top-K read API

```go
// TopKChunksForTag reads the top-K chunks for a tag from the HC
// cache. Fast (LMDB lookup, no cosine math) but reflects whatever
// state the last sweep left. Same result shape as ChunksForTag —
// callers can treat them interchangeably.
//
// Stale-entry handling: an HC entry whose chunk no longer exists
// (orphaned EC) or whose source serials have advanced beyond the
// stamped values is silently filtered at read time. The next
// sweep cleans up.
//
// Returns (nil, nil) if no HC entries exist for the tag, k <= 0,
// or embedding unavailable.
func (l *Librarian) TopKChunksForTag(tag string, k int) ([]ChunkSuggestion, error)
```

`ChunksForTag` (the live primitive from 1D) and `TopKChunksForTag`
(the cached read) are complementary: live for spot-checks and
warm-up, cached for fast UI loads. The curation view will prefer
the cache; ad-hoc tooling will prefer live.

### Tag → tag queries (entry-point 4)

```go
// RelatedTags returns up to k tags whose ED vectors are nearest
// to any of the named tag's ED vectors. Max-pair aggregation per
// other tag — the score is the best (def_a, def_b) cosine.
// Live; no cache. ~365ms for the full ED↔ED scan at ~270 tags.
func (l *Librarian) RelatedTags(tag string, k int) ([]TagSimilarity, error)

// TagPairConflict returns the max-pair cosine between two tags
// and the (fileID_a, fileID_b) defs that scored it. The "are
// these two the same idea?" probe.
func (l *Librarian) TagPairConflict(tagA, tagB string) (TagSimilarity, error)

// TagDrift returns the pairwise cosine matrix within one tag's
// own ED records, sorted descending. Reveals how unified or
// fractured the tag's definition is across files. For a tag with
// n defs, returns n*(n-1)/2 pairs.
func (l *Librarian) TagDrift(tag string) ([]DriftPair, error)

type TagSimilarity struct {
    Tag         string  // the matching tag (empty for TagPairConflict — both tags are inputs)
    Score       float64 // best cosine across def-pairs
    SrcFileID   uint64  // best-matching def from the queried tag
    DstFileID   uint64  // best-matching def from the matching tag
    SrcPath     string  // resolved at return time
    DstPath     string  // resolved at return time
}

type DriftPair struct {
    FileIDA uint64
    FileIDB uint64
    PathA   string
    PathB   string
    Score   float64
}
```

## HC Record Format

```
HC + <tag-bytes> + <chunkid:8>  →  <score:float64>
```

Same key encoding as ED records (variable-length tag prefix, fixed
8-byte chunkid suffix). Value is just the score: 8 bytes flat. No
version metadata embedded in the value.

Top-K bound is enforced by the sweep, not by storage: at the
current corpus (105 tags × top-K=20), the cache holds ~2100
entries (~17 KB raw values plus ~5 KB key overhead). The bound
`K_TOP_HC` is fixed at **20** for this slice; making it
configurable is a future tuning question.

### Freshness via the S-substrate (Alibi Stamp)

Freshness tracking lives entirely in the existing S side-index
(Phase 1C). HC writes are stamped on the same path as ED, EC,
EV, and T. To verify an HC entry is current at read time:

```
fresh iff
  RecordSerial(HC, key) ≥ RecordSerial(EC, chunkid)
  and RecordSerial(HC, key) ≥ max RecordSerial(ED, tag||fileid_of_def)
                                   over all defs of the tag
```

The HC entry's own stamp is its **alibi** — proof of when it
was written. If every input's current stamp is ≤ the HC's
stamp, no input has moved since the HC was last computed. If
any input's stamp has advanced past the HC's, the score is
computed against state that has since changed, and the entry
is filtered.

This is the [Alibi Stamp pattern](../../../.claude/personal/patterns/alibi-stamp.md):
the cache value holds only the result of the computation; the
freshness signal lives outside the value, in the substrate.
The HC entry doesn't need to record which `def_fileid` won the
max-pair, or which input version it computed against — the
stamps alone tell us whether any input has moved.

Adding a new dependency (e.g. tracking the embedding model's
identity in a future slice) widens the substrate, not the HC
value: a new stamped record gets added to the freshness check,
no schema change to HC.

## Sweep Algorithm

### State

```
I:hcsweep  →  uint64    last successful sweep's high-water serial
```

This is the persistence anchor. Cleared by `ark rebuild` and by
`DropEmbeddings` (model swap), forcing a from-scratch sweep on
next run.

### Phases

1. **Read bookmark.** Load `last_sweep_serial` from `I:hcsweep`.
   Zero means from-scratch.

2. **Survey changed work.**
   - `WalkRecordsSinceSerial(prefixEmbedDef, last_sweep_serial, …)`
     produces `(tag, fileid, serial)` triples for ED changes.
   - `WalkRecordsSinceSerial(prefixEmbedChunk, last_sweep_serial, …)`
     produces `(chunkid, serial)` pairs for EC changes.

3. **Tag rebuild pass (driven by ED changes).** For each tag with
   any changed ED record, recompute its full top-K by walking
   every EC record. Atomically replace the tag's HC entries
   (delete old, write new top-K). Tagged as "tag rebuilt."

4. **Chunk displace pass (driven by EC changes).** For each
   changed EC chunk that wasn't already covered by phase 3:
   - Compute its cosine against each ED vector (per tag,
     max-aggregate across that tag's defs).
   - For each tag, read current HC top-K (≤20 entries) and
     decide if the chunk displaces the lowest-scoring entry.
   - On displace: delete the displaced HC entry, write the new
     one. Tagged as "tag touched."

5. **Update bookmark.** Write `I:hcsweep = max(seen_serial)` only
   on success. A mid-sweep error leaves the bookmark unchanged
   so the next run picks up where this one stopped.

6. **Surface result.** Update tmp:// progress doc to "complete"
   with the final counts.

### Cost shape (at current corpus, 49,123 EC × 105 ED, Steam Deck @ 3 µs/cosine)

- From-scratch (every tag rebuilt): **~16 s** (105 × 49 K
  cosines ≈ 5.2 M cosines).
- Steady state:
  - 100 new chunks → ~30–50 ms (chunk-displace pass only).
  - 5 ED edits → ~1.2 s (per-tag rebuilds, each a full EC scan).
  - Both → ~1.3 s.

### Atomicity

Phase 3 and 4 each run in their own write transaction per
affected tag (not one big txn for the whole sweep). Reasons:
- A long-running write txn blocks all other writers (closure
  actor serializes writes).
- Per-tag txns mean a sweep crash leaves the cache in a
  partially-updated but internally-consistent state — the
  bookmark stays at the last successful pre-crash run, so the
  next sweep redoes only the work that wasn't committed.

The sweep is **idempotent** per (tag, chunkid): rerunning the
same work produces the same HC contents.

## Progress Doc

### Path and shape

`tmp://sweep/hot-correlations.md`. Single chunk; rewrites
in-place on each progress tick.

```markdown
@sweep: hot-correlations
@sweep-status: idle | running | complete | error
@sweep-started: 2026-05-08T09:00:00Z
@sweep-progress: 0.30
@sweep-phase: tag-rebuild | chunk-displace | done
@sweep-changed-eds: 5
@sweep-changed-ecs: 100
@sweep-tags-rebuilt: 2
@sweep-tags-touched: 18
@sweep-orphan-total: 142
@sweep-eta-seconds: 45
@sweep-duration-ms: 1487
@sweep-completed: 2026-05-08T09:01:30Z
@sweep-error: <message>            # only present when status = error
```

`@sweep-status:` and `@sweep-phase:` are the discrete state tags
UI subscribers care about. `@sweep-progress:`, `@sweep-eta-*`,
and the count tags update over the course of the run.

### Throttling

The sweep updates the doc **at most every 250 ms**. Within a
250 ms window, progress counters are accumulated in memory and
flushed at the end of the window. The `running → complete` /
`running → error` transitions always flush immediately, even
mid-window, so the terminal state is never delayed.

A 250 ms throttle gives 4 UI updates per second — fast enough
to feel live, slow enough that pubsub fan-out and tmp:// re-
indexing stay cheap. At current-corpus speeds: a from-scratch
run produces ~60–100 ticks; a 100-chunk steady-state run may
fall under one throttle window and surface only the terminal
state, which is the desired behavior.

### Doc lifecycle

- **Server startup.** The doc is created with
  `@sweep-status: idle` and no progress fields. Persistent
  across server runtime; cleared on server restart (tmp://
  semantics).
- **Sweep start.** Status flips to `running`, started timestamp
  set, all progress fields zeroed.
- **Sweep tick.** Progress fields rewritten according to the
  throttle.
- **Sweep complete / error.** Status flips to terminal,
  completed timestamp set, error message set if applicable.
  The doc stays in this state until the next sweep starts.

A subscriber that misses the live updates can read the doc
later and see the most recent terminal state.

## Pubsub Subscription

Subscribers reach the doc through existing tag-subscription
plumbing — no new event channel:

- `mcp:subscribe({tag="sweep-status"}, cb)` — fires whenever
  the sweep transitions phase.
- `mcp:subscribe({tag="sweep-status", value="complete"}, cb)` —
  fires only on completion.
- A polling client can fetch the doc directly via the
  search/content API.

The status-bar UI (Phase 1F) will subscribe to `sweep-status`
plus `sweep-progress` to drive the progress indicator.

## Invocation

For 1E the invocation surfaces are direct:

- `Librarian.SweepHotCorrelations()` from Go.
- A CLI subcommand `ark sweep correlations` (thin wrapper that
  proxies to a running server, like other long-running ops).
- A Lua API `mcp.sweepHotCorrelations()` (thin wrapper). See the
  Lua API section below for the full curation-view bridge surface
  including the read methods.

**Cron-via-tag triggering is deferred to a follow-up slice.**
The plan's "Cron-via-tags as Phase 1H or separate" item is the
right home for it. When that lands, the agentic-executor
subscriber will fire `SweepHotCorrelations()` on its scheduled
tag — the engine doesn't need to know about cron.

## Lua API

Five thin Lua wrappers, one per Librarian read method plus the
sweep trigger. Surfaced for the Phase 1F curation view: the
cached chunk-for-tag panel, the three tag→tag lenses, and an
on-demand sweep button.

```lua
-- Cached top-K chunks for a tag (HC entries with alibi-stamp filter).
local chunks = mcp.topKChunksForTag("design-decision", 10)
-- chunks[i] = {
--   chunkID = 4711, fileID = 123, path = "/abs/path/to/chunk.md",
--   score = 0.78,
--   motivatingDefs = {
--     { fileID = 88, path = "...", score = 0.78 }, ...
--   }
-- }
-- Same ChunkSuggestion shape as mcp.chunksForTag — swappable.

-- Tags whose ED vectors are nearest a focused tag's ED records.
local related = mcp.relatedTags("design-decision", 10)
-- related[i] = {
--   tag = "decision-record", score = 0.82,
--   srcFileID = 88, srcPath = "/abs/path/to/source-def.md",
--   dstFileID = 91, dstPath = "/abs/path/to/related-def.md"
-- }

-- Conflict between two tags: the max-pair cosine across their ED records.
local conflict = mcp.tagPairConflict("design-decision", "decision-record")
-- conflict = {
--   tag = "", score = 0.91,
--   srcFileID = 88, srcPath = "/abs/path/to/A-def.md",
--   dstFileID = 91, dstPath = "/abs/path/to/B-def.md"
-- }
-- (tag is empty because both tags are inputs; src/dst identify the
-- best-matching definition file from each side.)

-- Drift within a single tag: pairwise cosine across the tag's ED records.
local drift = mcp.tagDrift("design-decision")
-- drift[i] = {
--   fileIDA = 88, pathA = "/abs/path/to/def-a.md",
--   fileIDB = 91, pathB = "/abs/path/to/def-b.md",
--   score = 0.62
-- }
-- (fileIDA < fileIDB by convention so pairs are canonical.)

-- Trigger the corpus-wide sweep. Routes through enqueueWrite.
-- Subscribe to @sweep-status / @sweep-progress beforehand to follow
-- progress; the call returns when the sweep completes.
local result = mcp.sweepHotCorrelations()
-- result = {
--   startedAt    = "2026-05-09T11:42:00Z",   -- RFC3339
--   completedAt  = "2026-05-09T11:42:16Z",
--   durationMs   = 16000,
--   changedEDs   = 0, changedECs = 12,
--   tagsRebuilt  = 0, tagsTouched = 8,
--   orphanTotal  = 14,
--   fromScratch  = false
-- }
-- Equivalent to POST /sweep/correlations. When the embedding model
-- is unavailable the call returns {status = "embedding-unavailable"}
-- (matching the HTTP path's degraded reply).
```

Field naming, ID encoding, empty-result, and error conventions
match `mcp.suggestTagNames` (see suggest-tag-names.md):
lowerCamelCase fields, IDs as Lua numbers, empty result → empty
table `{}`, errors → `(nil, errstring)`. The four read methods
are read-only. `mcp.sweepHotCorrelations()` is the one writer in
the set; it enqueues through the write goroutine, identical to
the HTTP `POST /sweep/correlations` path.

## Stale Entry Handling

An HC entry can become stale for three reasons:

1. The EC record at `chunkid` was deleted (chunk removed from
   the corpus). `RecordSerial(EC, chunkid)` returns `not found`.
2. The EC record was rewritten and its current serial advanced
   past the HC's stamp.
3. Any of the tag's ED records was rewritten and its current
   serial advanced past the HC's stamp.

Two policies apply:

- **Read-time filter.** `TopKChunksForTag` performs the alibi-
  stamp comparison on each entry — looks up the HC's serial,
  the EC's current serial, and each of the tag's ED current
  serials, and drops the entry if any input's stamp has
  advanced past the HC's. Stale data never reaches the UI;
  the apparent top-K may be shorter than `k` until the next
  sweep refreshes.
- **Sweep-time refresh.** The next sweep visits every tag whose
  ED records advanced (phase 3 rebuild) or whose chunks
  advanced (phase 4 displace), naturally replacing stale entries
  with current ones — the rewrites are stamped at the new sweep
  serial, restoring freshness.

No active deletion sweep runs at chunk-remove time; lazy
filtering is enough at this corpus scale. Per-tag ED stamp
lookups during the read-time filter are O(defs) per tag —
typically 1–5 — and dominate by EC freshness checks at top-K
size.

## What This Does Not Do

- Does not invoke a search agent. Pure cosine math against
  vectors already in LMDB.
- Does not call the embedding model. ED and EC vectors are
  read; not computed.
- Does not filter chunks already carrying the tag at the
  storage layer. Same decision as 1D: orphan-detection policy
  is caller-side. The curation view applies the filter when
  rendering, not the cache.
- Does not invalidate HC on its own when ED/EC change — the
  S serials in the entry plus the read-time filter handle
  that. Active invalidation would tie the EC/ED write paths to
  HC, increasing coupling.
- Does not persist completion history. `@sweep-completed:`
  and `@sweep-orphan-total:` exist on the live tmp:// doc;
  they vanish at server restart. A persistent completion log
  in `~/.ark/sweep/correlations-history.md` is a follow-up
  slice.
- Does not auto-trigger on indexer activity, file changes, or
  any other corpus event. Sweep runs on explicit invocation.
- Does not maintain in-memory mirrors of HC. All reads go
  through LMDB. The in-memory tail of recent S records (1C
  deferred question) becomes actionable once 1E is profiled
  against real workloads, not in this slice.

## Performance

- HC read by tag (top-K of ~20): one prefix scan at the tag's
  bucket, sub-millisecond.
- Sweep from-scratch: **~16 s** on the current corpus (105 ED ×
  49 K EC × 3 µs).
- Sweep steady-state: ~30 ms – 1.3 s depending on ED/EC churn.
- Tag-tag queries: live, all sub-second at current scale.
  - `RelatedTags`: ~30–55 ms (full ED↔ED scan, 105 × 105
    cosines).
  - `TagPairConflict`: trivial (handful of cosines per call).
  - `TagDrift`: trivial (within-tag pairwise).

## Storage Scale

- HC: ~2100 entries × ~24 bytes ≈ 50 KB at top-K=20 across
  105 tags. Headroom to ~10× without storage concern.
- I:hcsweep: 8–10 bytes (one varint).
- tmp:// progress doc: in-memory only, ~500 bytes.

## Test Strategy

- HC read/write/delete on Store, including atomic per-tag
  replace.
- Sweep with hand-crafted ED/EC values across the bookmark
  boundary: verify phase-3 rebuild picks up tags with new ED
  records, phase-4 displace picks up new chunks against unchanged
  tags, the bookmark advances correctly, and a re-run is a
  no-op.
- Bookmark unchanged on simulated mid-sweep error.
- TopKChunksForTag stale-filtering: write HC entries with stale
  serials (manually advance an ED or EC), verify they're dropped
  on read.
- Tag-tag queries: small fixture with crafted ED vectors, verify
  ranking and aggregation per lens.
- Progress doc: throttle behavior (no more than one update per
  ~250 ms), terminal-state immediate flush, error path sets
  `@sweep-error:`.
