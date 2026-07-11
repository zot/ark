# Test Design: Derived Tags
**Source:** crc-Store.md, crc-Librarian.md, crc-ExtMap.md, crc-DB.md, crc-Server.md

**State C (compute-for-display, #36 recall-proposals-for-display).** The recall
`--propose` pass **computes** tag proposals and surfaces them per surfaced chunk
via `RecalledChunk.ProposedTags` **in the same call**; it authors no
`@ext-candidate` and writes no RC/RF. Durable authoring is the calling agent's
separate `ark ext candidate` verb (018 substrate, tested via the flip tests
below). RF is retired (dormant methods retained pending teardown). Tests split
across `derived_tags_test.go` (recall `--propose` compute-for-display behavior +
dormant-RF/ClearAll store methods) and `derived_flip_test.go` (the `@count`
read-modify-write helpers, the `ExtMap` reverse-lookup accessors, the map-backed
`DerivedProposals` read, and the file-backed accept/reject end-to-end — all
unchanged by #36).

## File-backed harness

`setupFileBackedRecall` isolates `arkHomeDir` to a temp `HOME` (so any mirror
authoring by the accept/reject tests lands in the sandbox, never live `~/.ark`)
and configures a source over the test index dir plus `~/.ark/external`.
`reindexMirrors` indexes authored mirrors so their `@ext-candidate` /
`@ext-judgment` lines derive RC / RJ — used by the flip tests (accept/reject),
**not** by the compute-for-display tests (the pass authors nothing).
`externalMirrors` concatenates authored mirror files; `assertNoDerivedWrites`
asserts the pass wrote no RC/RF and authored no `@ext-candidate`.

## Compute-for-display recall tests (state C)

## Test: --propose computes, surfaces, writes nothing
**Purpose:** A single `--propose` pass surfaces the computed proposal in the SAME
call via `ProposedTags` and writes nothing (no RC, no RF, no `@ext-candidate`).
**Input:** cInput query + cTarget subject, aligned EC vectors, ED `food` cosine 1.0.
One `Recall(--propose, KeepTagless)`.
**Expected:** the surfaced target's `ProposedTags[0] == "food"`; `assertNoDerivedWrites`
passes (no RC/RF, mirrors carry no `@food`).
**Refs:** crc-Librarian.md, seq-derived-tags.md#1.6, R3079, R3080

## Test: --propose surfaces similarity-ordered
**Purpose:** proposals surface descending by similarity in the same call.
**Input:** ED `food` cosine 1.0, `style` cosine ≈0.7. One `Recall(--propose)`.
**Expected:** `ProposedTags[0] == "food"`; `ProposedTagScores` aligns with `ProposedTags`.
**Refs:** crc-Librarian.md, seq-derived-tags.md#1.8, R3080, R2684, R2686

## Test: --propose EV leg proposes from an existing value
**Purpose:** R2911 — a chunk resembling an existing tag *value* (EV) with no ED
for that tag still earns the tag as a computed proposal, surfaced same-call.
**Input:** attach `cuisine: italian` to a holder chunk, write its EV aligned to
cTarget, write no ED for `cuisine`. One `Recall(--propose)`.
**Expected:** `cuisine` appears in the target's `ProposedTags`.
**Refs:** crc-Librarian.md, R2911

## Test: --propose filters tags already on the chunk
**Purpose:** the already-attached filter drops a tag the chunk already carries.
**Input:** attach `@food: pasta` to cTarget, ED `food` cosine 1.0. One `Recall(--propose)`.
**Expected:** `food` not in the target's `ProposedTags`.
**Refs:** crc-Librarian.md, R2671

## Test: --propose skips net-rejected candidates
**Purpose:** the reject filter reads `ExtMap.rejectByChunk`; a net-rejected
`(chunk, tag)` is not proposed.
**Input:** seed `rejectByChunk[cTarget] = {food:-1}`, ED `food` cosine 1.0. One `Recall(--propose)`.
**Expected:** `food` not in the target's `ProposedTags`.
**Refs:** crc-Librarian.md, R3070

## Test: --propose min-similarity floor + aligned scores
**Purpose:** the cosine floor drops sub-threshold candidates from the computed
proposals; scores surface via `ProposedTagScores` aligned to `ProposedTags`.
**Input:** floor 0.5; ED `food` cosine 1.0 (above), `noise` cosine ≈0.287 (below).
One `Recall(--propose)`.
**Expected:** `ProposedTags == [food]`, one aligned score ≈1.0; `noise` absent.
**Refs:** crc-Librarian.md, R2742, R2743

## Test: --propose injects the live-conversation chunk set (R3082)
**Purpose:** a chunk in `RecallOpts.ConversationChunks` earns computed proposals
and is appended to the result even when A66 self-exclusion would drop it (it is
the query input); without the injection it does not appear. Authors nothing.
**Input:** cConv is BOTH the query input and the injected conversation chunk, its
EC aligned to ED `food`. Run `Recall(--propose, ConversationChunks:[cConv])`,
then `Recall(--propose)` without the injection.
**Expected:** with injection, cConv is surfaced with `ProposedTags[0] == "food"`
and `assertNoDerivedWrites` passes; without injection, cConv is self-excluded
(absent from the result).
**Refs:** crc-Librarian.md, seq-derived-tags.md#1.6, R3082

## Test: ProposedTags omitted without --propose (RC not read)
**Purpose:** no `--propose` ⇒ empty `ProposedTags`; and with a derived RC record +
reverse-lookup map pre-seeded, that the recall path does NOT read RC (R3080:
enrich reads the transient compute, not `DerivedProposals`).
**Input:** pre-seed `WriteDerivedCandidate` + `candidateSourcesByChunk[cTarget]`.
Run `Recall` without `--propose`.
**Expected:** every surfaced chunk has empty `ProposedTags`.
**Refs:** crc-Librarian.md, crc-Server.md, R2686, R3080

## Test: --propose without [embedding] model is a no-op
**Purpose:** `--propose` with no `EmbeddingAvailable` is silent: no computed
proposals, result unaffected.
**Input:** a `Librarian` with empty `modelPath`. One `Recall(--propose)`.
**Expected:** every surfaced chunk has empty `ProposedTags`; no error.
**Refs:** crc-Librarian.md, R2676

## Dormant RF store methods (retained pending teardown)

RF is retired by #36; the compute pass no longer reads or writes it. These guard
the retained Store methods until the banked full-teardown O-gap.

## Test: WriteDerivedFreshness / ReadDerivedFreshness round-trip
**Purpose:** the dormant RF methods still round-trip a serial.
**Input:** `WriteDerivedFreshness(42, 12345)`, then `ReadDerivedFreshness(42)`.
**Expected:** `(12345, true, nil)`.
**Refs:** crc-Store.md, R2666 (dormant), R2669 (dormant)

## Test: ReadDerivedFreshness missing returns (0, false)
**Purpose:** a never-derived chunk returns the stale sentinel.
**Input:** `ReadDerivedFreshness(999)` on a fresh DB.
**Expected:** `(0, false, nil)`.
**Refs:** crc-Store.md, R2669 (dormant), R2682 (dormant)

## Test: MaxEDSerial tracks the high-water serial
**Purpose:** `MaxEDSerial` reflects the highest stamped ED serial (method retained,
no caller after #36 dropped the freshness comparator).
**Input:** `WriteTagDefEmbedding` twice with rising serials; read `MaxEDSerial` between.
**Expected:** the second read strictly exceeds the first.
**Refs:** crc-Store.md

## Test: ClearAll{DerivedProposals,Freshness,Rejections,Discussed} wipe across substrates
**Purpose:** each `ClearAll*` helper removes every record under its own prefix
without touching the others (RF clear operates on residual dormant records).
**Input:** seed RC/RF/RJ (source_tvid+target_chunkid keys) + RD across two
sessions; call the four helpers, checking counts between.
**Expected:** each clear deletes exactly its 2 records, leaving the others intact.
**Refs:** crc-Store.md, R2744

## 018-substrate tests (unchanged by #36 — derived_flip_test.go)

The `@count` RMW helpers (`extractCountField`, `upsertCountLine`,
`judgmentIdentity`), the `ExtMap` accessors (`CandidateSourcesForChunk`,
`RejectScore`), the map-backed `Store.DerivedProposals` (forge-facing reader,
tally-desc, reject-filtered), the v3 judgment codec (`ReadDerivedJudgment`), and
the file-backed `Store.RejectDerived` / `Store.AcceptDerived` end-to-end all
remain as landed by migration 018 — #36 reverses only the *autonomous producer*,
not the ledger or its verbs.
**Refs:** crc-DB.md, crc-ExtMap.md, crc-Store.md, R3051, R3054, R3055, R3058,
R3059, R3065, R3066, R3067, R3069, R3071, R3074, R3075
