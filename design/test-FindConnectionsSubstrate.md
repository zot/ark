# Test Design: FindConnections — Substrate (normal mode)

**Source:** crc-Librarian.md, seq-find-connections-substrate.md,
specs/find-connections-substrate.md

Substrate-pipeline tests live in `connections_substrate_test.go`
beside `connections_test.go`. They use the **test-as-subscriber
pattern** from R2312 — subscribing through `PubSub.Subscribe` +
`Listen` against the tmp:// path the substrate worker writes. No
mocks of the substrate; the index is real, the embedding model is real
when available and skipped (with `t.Skip`) when not.

## Test: enqueue + completed terminal write for one chunkID input

**Purpose:** Validates R2567, R2574, R2580, R2581, R2590, R2591,
R2593–R2594, R2598. End-to-end happy path for the normal mode.

**Input:** Index a small corpus with ≥3 tag definitions and ≥10
chunks; ensure ED and EC records are written. Call
`Librarian.FindConnections([]ConnectionsInput{{ChunkID: c1}},
FindConnectionsOpts{Mode: "normal", K: 5})`. Subscribe to the
returned tmp:// path with `tag="connections-status"`.

**Expected:** Returned requestID is non-empty, returns
sub-millisecond. The first listen batch carries
`@connections-status: pending` with `@purpose: curate` and
`@connections-mode: normal`. The next batch carries
`@connections-status: completed` (no intermediate `working`).
Doc body contains a `## Proposals` section with ≤5
`@proposal-kind: tag-name` rows, each with `@proposal-value`,
`@proposal-score`, `@proposal-evidence-chunks`,
`@proposal-evidence-vector-ed`, `@proposal-evidence-trigram-ed`,
`@proposal-evidence-vector-ec`, `@proposal-evidence-trigram-ec`,
and `@proposal-motivating-files`. `@proposal-count` matches the
row count. Proposals are sorted by `@proposal-score` desc.

**Refs:** crc-Librarian.md, seq-find-connections-substrate.md
(Happy Path).

## Test: normalize inputs — chunkID, path:range, bare text mix

**Purpose:** Validates R2568, R2569, R2570, R2572, R2574.
normalizeInputs accepts and canonicalizes mixed input types.

**Input:** Three inputs: `{ChunkID: c1}`, `{Path: "foo.md",
Range: "10-20"}` (covers chunks c2, c3), `{Text: "asparagus risotto"}`.
Call `Librarian.normalizeInputs(...)` directly.

**Expected:** Returns four `ConnectionsInput` entries
(chunkID c1, chunkID c2, chunkID c3, text "asparagus risotto").
Path resolution succeeds; range intersects 2 chunks; bare text
passes through unchanged. No errors.

**Refs:** seq-find-connections-substrate.md (Multi-Input Merge).

## Test: normalize rejects unknown chunkID

**Purpose:** Validates R2569, R2600.

**Input:** Call `Librarian.normalizeInputs([]ConnectionsInput{
{ChunkID: 99999999}})` for a chunkID not in the index.

**Expected:** Returns `(nil, error)` where the error message
contains `unknown chunk 99999999`. No tmp:// doc is created
(the caller `FindConnections` must call normalize+validate
before allocating the request ID).

**Refs:** crc-Librarian.md, seq-find-connections-substrate.md
(Reject at Enqueue).

## Test: normalize rejects path:range parse errors and missing paths

**Purpose:** Validates R2570, R2571.

**Input:** Three sub-cases:
  a) `{Path: "foo.md"}` (no range) → expect `path "foo.md" requires
     a range; use ":1-" for the whole file`.
  b) `{Path: "missing-file.md", Range: "1-10"}` → expect `path
     "missing-file.md" not found`.
  c) `{Path: "foo.md", Range: "abc-xyz"}` → expect `path:range
     parse error`.

**Expected:** Each sub-case returns `(nil, error)` with the
named message. No tmp:// docs created.

**Refs:** crc-Librarian.md.

## Test: normalize rejects empty input list

**Purpose:** Validates R2573.

**Input:** Call `Librarian.FindConnections([]ConnectionsInput{},
FindConnectionsOpts{Mode: "normal"})`.

**Expected:** Returns `("", error)` with `chunkIDs/text/range empty`.
No tmp:// doc.

**Refs:** crc-Librarian.md.

## Test: substrate pipeline scores match Suggest-then-merge oracle

**Purpose:** Validates R2575–R2581. The four substrate passes
produce the same per-tag aggregate as running each substrate
in isolation and taking the max.

**Input:** Index a corpus where chunk c1 is closely related to
tag T (matching ED text and overlapping V records). Call
`substrateForInput({ChunkID: c1}, txn)` directly. Compare with:
  - `SuggestTagNames(c1, ∞)` — gives vector_ed scores
  - hand-computed trigram_ed via microfts2 fuzzy on T's definition text
  - `SearchChunks(EC[c1], K')` + V-record vote count for T's chunks
  - hand-computed trigram_ec via microfts2 fuzzy on chunk text

**Expected:** Each substrate's per-tag score in the substrate
result equals the oracle's score (within floating-point tolerance).
The aggregate score equals the max across the four substrates.

**Refs:** seq-find-connections-substrate.md (Multi-Input Merge),
crc-Librarian.md.

## Test: cross-input merge takes max per tag

**Purpose:** Validates R2581, R2582.

**Input:** Submit a request with two inputs (chunkIDs c1 and c2)
where:
  - tag T has high vector_ed score for c1 (0.91) and low for c2 (0.20)
  - tag U has low vector_ed score for c1 (0.10) and high for c2 (0.84)
Compare the output proposals against per-input substrate runs.

**Expected:** Proposal for T has aggregate 0.91 (max across inputs).
Proposal for U has aggregate 0.84. Per-substrate detail fields
retain the max-per-substrate values across inputs. Supporting
chunks include both c1 and c2 when both contributed.

**Refs:** seq-find-connections-substrate.md (Multi-Input Merge).

## Test: top-K caps the output

**Purpose:** Validates R2585.

**Input:** A corpus with ≥50 tag definitions, all moderately
related to chunk c1. Call FindConnections with `opts.K = 7`.

**Expected:** The completed doc carries exactly 7 proposal rows.
`@proposal-count: 7`. Rows are sorted by `@proposal-score` desc;
the 8th would-be candidate's score is ≤ the 7th's.

**Refs:** crc-Librarian.md.

## Test: top-K clamps to [1, 200]

**Purpose:** Validates R2585 clamp.

**Input:** Two sub-cases: `opts.K = 0` and `opts.K = 1000`.

**Expected:** K=0 → effective K=20 (default). K=1000 → effective K=200.
Verified by counting `@proposal-kind: tag-name` rows in the
completed doc.

**Refs:** crc-Librarian.md.

## Test: bare-text input embeds on the fly

**Purpose:** Validates R2572, R2575, R2577.

**Input:** A corpus with ED and EC records present. Call
FindConnections with one input `{Text: "asparagus risotto recipe"}`.
Capture the resulting proposals.

**Expected:** Completed doc exists with proposals. Each proposal's
`@proposal-evidence-chunks` is non-empty (drawn from EC vector +
EC trigram side). `@proposal-motivating-files` is non-empty for
proposals where vector_ed or trigram_ed scored. The embedded
query produced a vector identical to `EmbedQuery("asparagus risotto recipe")`
(side-channel check: stub or instrument EmbedQuery to record calls).

**Refs:** seq-find-connections-substrate.md (Multi-Input Merge,
case I.text != "").

## Test: vector cosine scores are normalized to [0, 1]

**Purpose:** Validates R2586.

**Input:** Index a corpus where tag T's ED vector has cosine
similarity exactly -0.5 with the input chunk's EC. Submit
FindConnections for that chunk.

**Expected:** The proposal for T (if it makes top-K) carries
`@proposal-evidence-vector-ed: 0.25` (computed as `(-0.5 + 1) / 2`).

**Refs:** crc-Librarian.md.

## Test: embedding unavailable falls back to trigram-only

**Purpose:** Validates R2588.

**Input:** Run with the embedding model disabled (clear the `[embedding]`
model config or stub `EmbeddingAvailable()` to false). Submit a
chunkID request.

**Expected:** Completed doc carries `@connections-warning: embedding
unavailable`. Proposals are present but their
`@proposal-evidence-vector-ed` and `@proposal-evidence-vector-ec`
fields are zero (substrate skipped). Trigram fields populated.
Aggregate scores reflect trigram-only ranking.

**Refs:** seq-find-connections-substrate.md (Embedding Unavailable).

## Test: empty ED prefix returns EC-only proposals (no warning)

**Purpose:** Validates R2589.

**Input:** A fresh corpus with EC records but no D / ED records.
Submit a chunkID request.

**Expected:** Completed doc exists. `@connections-warning` header
is absent. Proposals are limited to tags discovered through
V-record votes on the EC-similar chunks; `@proposal-motivating-files`
is empty on every row. No vector_ed / trigram_ed scores.

**Refs:** crc-Librarian.md.

## Test: turbo mode still queues for sidecar (compat)

**Purpose:** Validates R2603 — turbo mode requires
ConnectionsAvailable, doesn't run the substrate in-process.

**Input:** Register a sidecar consumer (simulate by calling
`Librarian.WaitForConnectionsRequest`). Submit FindConnections
with `opts.Mode = "turbo"`. Drain via `sidecar-wait`.

**Expected:** Request appears in the drained queue. Doc starts in
`pending` with `@connections-mode: turbo`. Pipeline does not run
in-process; no proposals are written until the sidecar posts.

**Refs:** seq-find-connections.md, crc-Librarian.md.

## Test: turbo mode rejects when sidecar unavailable

**Purpose:** Validates R2603, R2600.

**Input:** No sidecar registered. Submit FindConnections with
`opts.Mode = "turbo"`.

**Expected:** Returns `("", "agent unavailable")`. No tmp:// doc.

**Refs:** crc-Librarian.md.

## Test: per-mode body shape — turbo emits both legacy and new sections

**Purpose:** Validates R2597 (migration window dual emission).

**Input:** Drive a turbo request to completion with a stub sidecar
posting `{themes: [...], sharedTags: [...]}`. Read the resulting
doc body.

**Expected:** Body contains BOTH:
  - `## Themes` and `## Shared Tag Candidates` (legacy 1G sections,
    R2330)
  - `## Proposals` with `@proposal-kind: theme` and
    `@proposal-kind: shared-tag` rows derived from the same
    payload (R2595, R2596)
Field values match across both representations.

**Refs:** crc-Librarian.md, seq-find-connections-substrate.md.

## Test: subscriber sees pending → completed transition (no working)

**Purpose:** Validates R2598.

**Input:** Subscribe through `PubSub.Subscribe` with
`tag="connections-status"`, `filterFiles=[doc-path]`. Submit a
normal-mode request. `Listen` for events.

**Expected:** Event batch contains two events: `pending` and
`completed`. No `working` event. Order is preserved.

**Refs:** seq-find-connections-substrate.md.

## Test: ListConnections snapshots in-flight records

**Purpose:** Validates R2609 — server-side support for
`ark connections list`.

**Input:** Submit three FindConnections requests (mix of normal
and turbo, with the sidecar simulated by a slow stub). While the
turbo request is pending, call `Librarian.ListConnections()`.

**Expected:** Returns three records. Each carries `ID`, `ChunkIDs`,
`Mode`, `Purpose`, `Status`, `Started`, `Path`. Records are copies
(mutating the returned slice does not affect the Librarian's map).

**Refs:** crc-Librarian.md.
