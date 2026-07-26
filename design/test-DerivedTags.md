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

## Test: upsertCountLine first-seen date freeze (R3090, R3091)
**Purpose:** Verify a new candidate/judgment line is stamped with the passed
  first-seen date after the marker, and a later bump preserves that original
  date (a fresh date passed on the bump is ignored); a dateless legacy line
  bumps without gaining a date
**Input:** upsertCountLine(nil, candIdentity, "2026-07-12", 1) then
  upsertCountLine(…, candIdentity, "2026-07-13", 1); dateless bare identity + 1
**Expected:** append → `… 2026-07-12 … @count: 1`; bump → `… 2026-07-12 … @count: 2`
  (frozen); dateless legacy line → `… @count: 1` (stays dateless)
**Refs:** crc-Indexer.md

## Test: CandidateExtTag disposition distinguishes lines (R3090, R3092)
**Purpose:** Verify internal and external proposals of the same (TARGET, tag,
  value) are distinct mirror lines with independent `@count` tallies, each
  carrying a leading first-seen date
**Input:** author external once, internal twice for (target, food, fruit) →
  read the mirror file
**Expected:** two `@ext-candidate:` lines, each with a leading `YYYY-MM-DD`;
  the external line at `@count: 1`, the internal line at `@count: 2`
**Refs:** crc-DB.md

## Test: AcceptExtTag reports the applied disposition (R3248)
**Purpose:** The one-shot fire path is honest about capability degrade: accept
  returns "internal" when the internal write landed, "external" when it
  degraded (or was external all along)
**Input:** the three internal-accept end-to-end tests capture the new return —
  markdown internal (landed), line-chunked internal (degraded), internal
  replace (landed)
**Expected:** applied = internal / external / internal respectively
**Refs:** crc-DB.md

## Test: RemoveInternalTag end-to-end (R3244, R3245)
**Purpose:** The internal-remove cell restores the pre-accept body, and a
  second remove errors — never a silent no-op, never a mirror fallback
**Input:** internal accept writes `@topic: recall` into a markdown file's
  body; RemoveInternalTag(src, topic); then RemoveInternalTag again
**Expected:** body byte-identical to the original after the first remove;
  the second returns a "no inline line" error
**Refs:** crc-DB.md, crc-Indexer.md

## Test: rankCandidateRows orders and truncates (R3253)
**Purpose:** The ranking is the query's contract, and the one place a
  `--limit` could silently drop the row a human most needs. Pure — a slice
  in, a slice out, no DB.
**Input:** hand-built `[]CandidateRow` mixing an unattached row (empty
  `Chunks`) with a low tally against attached rows with higher tallies, plus
  two rows equal on every key but `SourceTvid`, and `limit` values 0, -1, 1,
  and larger than the slice.
**Expected:** unattached first regardless of tally; then tally desc, date
  desc, tagname, tvid; the tvid tiebreak makes the order total, so a repeat
  call on a shuffled input yields the identical sequence. `limit <= 0`
  returns everything; `limit = 1` returns the unattached row (never an
  attached one), proving truncation happens after ranking.
**Refs:** crc-Store.md, R3253

## Test: PendingCandidates row carries the whole line (R3252)
**Purpose:** The console judges `insight` and `@count`, so a row that
  reaches it missing either is useless — and nothing but the tally is
  stored, so every other field is a recovery that can regress silently.
**Input:** file-backed harness; `CandidateExtTag` on an indexed chunk with
  a quoted insight, `internal` disposition, and `replace` set; reindex.
**Expected:** one row whose `Insight`, `Disposition`, `Replace`, `Tagname`,
  `Value`, `Target`, `Date`, and `Tally` all match what was authored, and
  whose `Chunks` holds the target chunkid.
**Refs:** crc-Store.md, R3252

## Test: PendingCandidates corpus-wide vs. per-chunk (R3252)
**Purpose:** Agents author across sessions, so a proposal on a chunk the
  human never pinned must still be visible — the corpus-wide default is the
  whole point of the ledger over a per-card view.
**Input:** candidates authored on two different indexed chunks; call with
  an empty filter, then with each chunk's id.
**Expected:** the empty filter returns both rows; each filtered call returns
  only its own. One row per candidate *line* either way — a TARGET
  resolving to several chunks yields one row listing them, not one row per
  chunk.
**Refs:** crc-Store.md, R3252

## Test: unattached candidate survives as a sentinel row (R3254)
**Purpose:** The completeness invariant. A proposal whose TARGET resolves to
  nothing is the row most likely to need a human, and before the sentinel it
  had no RC record at all — invisible to any prefix scan.
**Input:** file-backed harness; `CandidateExtTag` against a TARGET naming a
  path that is indexed by no source; reindex; then author a *second*
  candidate whose target does resolve.
**Expected:** the unresolvable candidate appears with `Chunks` empty and
  sorts ahead of the resolvable one; `RC[tvid][0]` exists for it and no RC
  record exists at any non-zero chunk; `DerivedProposals` on the resolvable
  chunk does **not** include the sentinel row (it is not a proposal *on* a
  chunk); `candidateSourcesByChunk` has no entry keyed 0.
**Refs:** crc-ExtMap.md, crc-Store.md, R3254

## Test: sentinel clears when a target resolves (R3254)
**Purpose:** The invariant's other half — a stale `RC[tvid][0]` would leave
  a resolved proposal permanently sorted into the alarm band at the top.
**Input:** author a candidate against a path that exists in a source but is
  not yet indexed (sentinel written); index the file so the TARGET resolves;
  reindex the mirror.
**Expected:** `RC[tvid][0]` is gone, `RC[tvid][chunk]` exists, and the row's
  `Chunks` is non-empty.
**Refs:** crc-ExtMap.md, R3254

## Test: amend revises in place and resets the tally (R3255)
**Purpose:** The amend contract — the proposal stays pending rather than
  orphaning, and a changed proposal does not inherit the old one's votes.
**Input:** author a candidate twice (tally 2) with an insight; resolve its
  source tvid; `AmendExtCandidate(tvid, tag, "new value", ExtAmendOpts{})`;
  reindex.
**Expected:** exactly one candidate line for that tag in the mirror, holding
  the new value at `@count: 1`; the old line is gone; the carried-forward
  `Insight` and `Disposition` match the original (nil opts = inherit); the
  ledger shows one row, not two.
**Refs:** crc-DB.md, R3255

## Test: amend merges into a colliding line's tally (R3255)
**Purpose:** Reusing the candidate author is what makes merge-on-collision
  fall out; if amend ever grows its own writer this is the behavior that
  regresses into a duplicate line.
**Input:** two distinct candidates A and B on one target; amend A so it
  becomes byte-identical to B's identity.
**Expected:** one line remains, carrying B's original `@count` incremented
  by one — not two lines, and not a reset to 1.
**Refs:** crc-DB.md, R3255

## Test: amend rejects a tvid that is not a live candidate (R3255)
**Purpose:** Unlike accept/reject, whose no-match is a legitimate no-op,
  amend was handed one specific line — reporting success for a revision that
  never happened is the failure mode worth pinning.
**Input:** an unallocated tvid; and the tvid of an ordinary non-candidate
  tag.
**Expected:** an error both times, and the mirror file unchanged.
**Refs:** crc-DB.md, R3255
