# Test Design: Derived Tags
**Source:** crc-Store.md, crc-Librarian.md, crc-ExtMap.md, crc-DB.md, crc-Server.md

State B (tag-derived RC/RJ subsystem, #22 Pass B+C). RC/RJ are re-keyed
`source_tvid + target_chunkid` and **derived** from `@ext-candidate` /
`@ext-judgment` file tags, not written by a direct `chunkid + tagname`
primitive. Tests split across `derived_tags_test.go` (Store primitives +
recall `--propose` behavior) and `derived_flip_test.go` (the `@count`
read-modify-write helpers, the `ExtMap` reverse-lookup accessors, the
map-backed read path, and the file-backed accept/reject/derive
end-to-end).

## File-backed harness

`setupFileBackedRecall` isolates `arkHomeDir` to a temp `HOME` (so mirror
authoring lands in the sandbox, never live `~/.ark`) and configures a
source over the test index dir plus `~/.ark/external`, so `resolveExtMirror`
and the propose pass's `syncOnePath` both resolve the target files.
`reindexMirrors` indexes every authored mirror (`.md`) with the `line`
chunker so each `@ext-candidate` / `@ext-judgment` line derives its RC / RJ
record and reverse-lookup maps — exactly as the live watcher would after
authoring. `externalMirrors` concatenates the authored mirror files for
content assertions. These stand in for the async watcher that the
non-`SameCall` tests would otherwise have to wait on.

## Test: extractCountField strips @count from routed tags
**Purpose:** Verify `extractCountField` peels the reserved `@count` field out of the routed-tag list, returns its signed value, and treats a malformed count as absent.
**Input:** (a) `[{topic:recall},{count:-3}]`. (b) `[{topic:recall}]` (no count). (c) `[{count:abc}]` (malformed).
**Expected:** (a) `routed=[{topic:recall}]`, `count=-3`, `has=true`. (b) `routed` unchanged, `count=0`, `has=false`. (c) count dropped from routed (empty), `count=0`, `has=false`.
**Refs:** crc-DB.md, R3074

## Test: upsertCountLine — candidate bump, judgment decrement, zero-removal, bare implicit-0
**Purpose:** Verify the `@count` read-modify-write on a mirror line: a new candidate materializes `@count: 1`, an exact-identity repeat bumps it, a judgment creates/decrements a negative count, a count returning to 0 removes the line, and a bare identity line counts as 0.
**Input:** With `cand = "@ext-candidate: %abc @topic:"` and `jid = judgmentIdentity("%abc","topic")`: `upsertCountLine(nil, cand, +1)`; again `+1`; `upsertCountLine(nil, jid, -1)`; again `-1`; `upsertCountLine(jid+" @count: -1\n", jid, +1)`; `upsertCountLine(cand+"\n", cand, +1)`.
**Expected:** Candidate → `cand + " @count: 1\n"`, then `" @count: 2\n"`. Judgment → `jid + " @count: -1\n"`, then `" @count: -2\n"`. The `-1`→+1 case returns `""` (line removed, absent ≡ neutral). The bare `cand+"\n"` case materializes `" @count: 1\n"` (implicit 0 + 1).
**Refs:** crc-DB.md, R3074, R3075

## Test: judgmentIdentity renders the @ext-judgment identity line
**Purpose:** Verify `judgmentIdentity` builds the tag-name-only `@ext-judgment` identity string and lowercases the tag.
**Input:** `judgmentIdentity("%abc", "Topic")`.
**Expected:** `"@ext-judgment: %abc @topic:"`.
**Refs:** crc-DB.md, R3055, R3075

## Test: ExtMap.CandidateSourcesForChunk / RejectScore
**Purpose:** Verify the two in-memory reverse-lookup accessors: `CandidateSourcesForChunk` returns a defensive copy (empty when absent) and `RejectScore` returns 0 when neutral, the signed score when net-rejected.
**Input:** `NewExtMap()`. `CandidateSourcesForChunk(7)` on an empty map. Set `candidateSourcesByChunk[7]=[11,12]`, read again, mutate the returned slice's element. `RejectScore(7,"food")` before and after setting `rejectByChunk[7]={food:-2}`.
**Expected:** Absent chunk → `nil`. Present → `[11,12]`; mutating the copy leaves `candidateSourcesByChunk[7][0]==11`. `RejectScore` → `0` neutral, `-2` after seeding.
**Refs:** crc-ExtMap.md, R3065, R3066

## Test: Store.DerivedProposals map-backed, tally-descending, reject-filtered
**Purpose:** Verify `DerivedProposals` reads `candidateSourcesByChunk`, recovers each candidate's `(tagname, value)` from its source tvid, reads the tally from the RC record, sorts tally-descending, and filters net-rejected tagnames (defense-in-depth).
**Input:** Seed two candidate sources for chunk 42 via `AllocOverlay(extCandidateTag, "x @<tag>: @count: N")` + `WriteDerivedCandidate(txn, tvid, 42, tally)` — `food`(3), `style`(1) — and set `candidateSourcesByChunk[42]=[tFood,tStyle]`. Call `DerivedProposals(42)`. Then set `rejectByChunk[42]={style:-1}` and call again.
**Expected:** First call → `[food(tally 3), style(tally 1)]` in tally-descending order. After the reject seed, `style` is filtered out.
**Refs:** crc-Store.md, seq-derived-tags.md#1.7, R3058, R3065, R3067

## Test: Store.ReadDerivedJudgment codec — absent, present, malformed
**Purpose:** Verify the v3 judgment codec on the live `ReadDerivedJudgment` helper (the coverage the retired `ReadJudgment` held): absent → neutral, present → the signed score, malformed → conservatively rejected.
**Input:** (a) `ReadDerivedJudgment(src=50, tgt=7)` on a fresh DB. (b) After `WriteDerivedJudgment(50, 7, -3, 999)`. (c) Write a 3-byte value at `derivedRoutedKey(prefixDerivedRejection, 51, 8)`, then `ReadDerivedJudgment(51, 8)`.
**Expected:** (a) `(0, false, nil)`. (b) `(-3, true, nil)`. (c) `(negative score, present=true, nil)` — a value that is not `signed-varint + 8 bytes` reads as rejected, no error.
**Refs:** crc-Store.md, R3059

## Test: Store.WriteDerivedFreshness / ReadDerivedFreshness round-trip
**Purpose:** Verify `WriteDerivedFreshness` writes RF with the supplied serial and `ReadDerivedFreshness` reads it back.
**Input:** `WriteDerivedFreshness(chunkID=42, serial=12345)` in a write txn, then `ReadDerivedFreshness(42)`.
**Expected:** `(serial=12345, found=true, err=nil)`.
**Refs:** crc-Store.md, R2666, R2669

## Test: Store.ReadDerivedFreshness missing returns (0, false)
**Purpose:** Verify a chunk that never derived returns the stale sentinel.
**Input:** `ReadDerivedFreshness(999)` against a fresh DB.
**Expected:** `(serial=0, found=false, err=nil)`.
**Refs:** crc-Store.md, R2669, R2682

## Test: Store.MaxEDSerial tracks the high-water serial
**Purpose:** Verify `MaxEDSerial` reflects the highest stamped ED serial and advances as new ED records land.
**Input:** `WriteTagDefEmbedding("a", 10, ...)`, read `MaxEDSerial()`; then `WriteTagDefEmbedding("b", 11, ...)`, read again.
**Expected:** First max is non-zero; the second call strictly exceeds the first (a new ED write bumps the S serial).
**Refs:** crc-Store.md, R2669

## Test: --propose authors @ext-candidate, reindex derives RC (end-to-end)
**Purpose:** Verify the inverted producer path: `--propose` authors an `@ext-candidate` mirror line and the reindex of that mirror derives the RC record + reverse-lookup map.
**Input:** File-backed harness. Two chunks `cInput` / `cTarget` with aligned EC vectors; ED `food` with cosine 1.0. Run `Recall(--propose, KeepTagless)`. Assert the mirror content, then `reindexMirrors` and read `DerivedProposals(cTarget)`.
**Expected:** `externalMirrors` contains `@food: @count: 1` (the authored candidate). After reindex, `DerivedProposals(cTarget)` includes `food` with tally 1.
**Refs:** crc-Librarian.md, seq-derived-tags.md#1.6, R3064, R3068

## Test: --propose writes RC (via reindex) and stamps RF
**Purpose:** Verify a single `--propose` run scores chunks against ED, produces a surviving RC on reindex, and stamps RF for the derived chunk.
**Input:** File-backed harness, `cInput` query + `cTarget` subject, ED `food` cosine 1.0. Run `Recall(--propose, KeepTagless)`, `reindexMirrors`, read `DerivedProposals(cTarget)` and `ReadDerivedFreshness(cTarget)`.
**Expected:** `food` proposal with tally 1 exists for `cTarget`; RF stamp for `cTarget` is greater than 0.
**Refs:** crc-Librarian.md, seq-derived-tags.md#1.6, R2667, R2669, R2670, R3068

## Test: --propose EV leg proposes a tag from an existing value
**Purpose:** Verify R2911 — a chunk resembling an existing tag *value* (EV), with no definition (ED) for that tag, still earns the tag as a proposal.
**Input:** File-backed harness. Attach `cuisine: italian` to a holder chunk, write its EV embedding aligned to `cTarget`, and write **no** ED for `cuisine`. Run `Recall(--propose, KeepTagless)`, `reindexMirrors`, read `DerivedProposals(cTarget)`.
**Expected:** `cuisine` is proposed for `cTarget` via the EV leg (with no ED, only the value embedding can surface it).
**Refs:** crc-Librarian.md, R2911

## Test: --propose freshness skip leaves the tally unchanged
**Purpose:** Verify a second `--propose` with no ED/EV change takes the RF freshness skip and does not bump `@count`.
**Input:** File-backed harness. Run `Recall(--propose)` twice with unchanged ED. `reindexMirrors`, read `DerivedProposals(cTarget)`.
**Expected:** `food` tally stays 1 — the second pass skipped derivation (RF fresh), so the `@ext-candidate` `@count` was not bumped.
**Refs:** crc-Librarian.md, R2669, R3075

## Test: --propose re-derives and bumps @count after an ED change
**Purpose:** Verify a new ED write advances the S serial, invalidates RF, and the next `--propose` re-derives — bumping the surviving candidate's `@count` (and thus its RC tally).
**Input:** File-backed harness. Run `Recall(--propose)` once; write a new ED (`style`, higher serial); run `--propose` again. `reindexMirrors`, read `DerivedProposals(cTarget)`.
**Expected:** `food` tally advances to 2 (the same candidate re-emitted, `@count` incremented).
**Refs:** crc-Librarian.md, R2669, R3075

## Test: --propose filters tags already on the chunk
**Purpose:** Verify an already-attached tag is not proposed.
**Input:** File-backed harness. Attach `@food: pasta` to `cTarget`, then run `Recall(--propose)`. `reindexMirrors`, read `DerivedProposals(cTarget)`.
**Expected:** No `food` proposal — the F-record / `AllTagsForChunk` probe filters it.
**Refs:** crc-Librarian.md, R2671

## Test: --propose skips net-rejected candidates via rejectByChunk
**Purpose:** Verify the reject filter reads `ExtMap.rejectByChunk` (not an RJ key lookup): a net-rejected `(chunk, tag)` is never re-authored.
**Input:** File-backed harness. Seed `rejectByChunk[cTarget]={food:-1}` (as a derived `@ext-judgment` would). Run `Recall(--propose, KeepTagless)`. Inspect mirrors and (after reindex) proposals.
**Expected:** No `@food:` candidate is authored (mirrors do not contain it), and `DerivedProposals(cTarget)` has no `food`.
**Refs:** crc-Librarian.md, seq-derived-tags.md#1.7, R3070

## Test: --propose surfaces ProposedTags after reindex, similarity-ordered
**Purpose:** Verify the surfaced `RecalledChunk` carries `ProposedTags` (similarity-descending) once `--propose` authored candidates and a reindex derived their RC records.
**Input:** File-backed harness. ED `food` cosine 1.0, `style` cosine ~0.7. First `Recall(--propose)` authors; `reindexMirrors`; a second `Recall(--propose)` surfaces the target.
**Expected:** The target chunk's `ProposedTags` is non-empty and ordered with `food` first (highest cosine).
**Refs:** crc-Librarian.md, seq-derived-tags.md#1.8, R2684, R2686, R3067

## Test: --propose synchronous same-call proposals
**Purpose:** Verify R3076 — a single `--propose` call authors AND reindexes on the actor, so the proposal is visible in the same call, both to `DerivedProposals` and in the surfaced chunk's `ProposedTags`, with no manual reindex.
**Input:** File-backed harness. ED `food` cosine 1.0. One `Recall(--propose, KeepTagless)`, no explicit `reindexMirrors`.
**Expected:** `DerivedProposals(cTarget)` already contains `food`; the surfaced chunk's `ProposedTags[0] == "food"` in the same call.
**Refs:** crc-Librarian.md, R3076

## Test: --propose batched sync-reindex cost
**Purpose:** Measure the batched synchronous materialization cost — one `syncOnePath` reindex per distinct touched mirror. Diagnostic, not a pass/fail gate.
**Input:** File-backed harness. Author `@ext-candidate` on M=12 distinct target mirrors via `CandidateExtTag`, resolve each mirror path, then `SyncVoid` a loop of `syncOnePath` over them, timing the batch.
**Expected:** The reindex completes for all mirrors; the test logs total time and ms/mirror (FTS + tag + derive of tiny mirrors — embedding stays deferred). No error.
**Refs:** crc-Librarian.md, R3076

## Test: Store.RejectDerived file-backed
**Purpose:** Verify the re-homed reject: author a candidate (→ RC), then `RejectDerived` authors an `@ext-judgment` whose reindex derives a negative RJ (`rejectByChunk`) and drops the candidate.
**Input:** File-backed harness. `CandidateExtTag(target,"food","","")`, `reindexMirrors` (proposal present). `RejectDerived(db, cTarget, "food")`, `reindexMirrors`. Read `RejectScore(cTarget,"food")` and `DerivedProposals(cTarget)`.
**Expected:** `RejectScore(cTarget,"food") < 0` after reindex; `food` no longer appears in proposals (the candidate transitioned to a judgment).
**Refs:** crc-Store.md, seq-derived-tags.md#3.2, R3069, R3075

## Test: Store.AcceptDerived file-backed
**Purpose:** Verify the re-homed accept: author a candidate (→ RC), then `AcceptDerived` rewrites it to `@ext` whose reindex lands the live X+V edge (the tag attaches) and drops the candidate.
**Input:** File-backed harness. `CandidateExtTag(target,"priority","high","")`, `reindexMirrors`. `AcceptDerived(db, cTarget, "priority", "high")`, `reindexMirrors`. Read `AllTagsForChunk(cTarget)`.
**Expected:** `priority:high` is attached to `cTarget` via the committed `@ext` (the accept loop closed on reindex).
**Refs:** crc-Store.md, seq-derived-tags.md#2.2, R3071

## Test: ProposedTags omitted without --propose
**Purpose:** Verify `RecalledChunk.ProposedTags` stays empty when `--propose` is not set, even with existing RC records + reverse-lookup map in the database.
**Input:** Pre-seed a derived proposal on `cTarget`: `AllocOverlay(extCandidateTag, "x @leftover:")` + `WriteDerivedCandidate(txn, tvid, cTarget, 1)` + `candidateSourcesByChunk[cTarget]=[tvid]`. Run `Recall` **without** `--propose`.
**Expected:** Every surfaced chunk has empty `ProposedTags`; no derivation activity occurred.
**Refs:** crc-Librarian.md, crc-Server.md, R2686

## Test: --propose min-similarity floor + ProposedTagScores
**Purpose:** Verify the chunk-EC ↔ tag-ED cosine floor (`[recall].min_propose_similarity`) drops sub-threshold candidates before the top-K cut, never authors them, and surfaces scores via `ProposedTagScores` aligned to `ProposedTags`.
**Input:** File-backed harness, floor 0.5. ED `food` cosine 1.0 (above), ED `noise` cosine ≈0.287 (below). First `Recall(--propose)` authors above-floor candidates; `reindexMirrors`; second `Recall(--propose)` surfaces the target. Read the target's `ProposedTags` / `ProposedTagScores` and `DerivedProposals(cTarget)`.
**Expected:** `ProposedTags == [food]`; one aligned score ≈ 1.0. `noise` produced no RC record (write-side floor).
**Refs:** crc-Librarian.md, R2742, R2743

## Test: --propose without [embedding] model is a no-op
**Purpose:** Verify `--propose` with no embedding available authors nothing and stamps no RF — the substrate result is unaffected.
**Input:** A `Librarian` with empty `modelPath` (so `EmbeddingAvailable()` is false). Run `Recall(--propose, KeepTagless)`. Read `DerivedProposals(cTarget)` and `ReadDerivedFreshness(cTarget)`.
**Expected:** No proposals for `cTarget`; RF stamp is 0. No error.
**Refs:** crc-Librarian.md, R2676

## Test: ClearAll{DerivedProposals,Freshness,Rejections,Discussed} wipe across substrates
**Purpose:** Verify the four `ClearAll*` recall-namespace helpers each remove every record under their own prefix without touching the others.
**Input:** Seed RC via `WriteDerivedCandidate` (source_tvid + target_chunkid keys), RF via `WriteDerivedFreshness`, RJ via `WriteDerivedJudgment`, and RD via `AddDiscussed` across two sessions. Call `ClearAllDerivedProposals`, then `ClearAllDerivedFreshness`, then `ClearAllDerivedRejections`, then `ClearAllDiscussed`, checking counts (via `ScanAllDerivedCandidates` / `ScanAllDerivedJudgments`) between calls.
**Expected:** `ClearAllDerivedProposals` deletes 2 RC and leaves RJ intact (count 2). `ClearAllDerivedFreshness` deletes 2 RF (both chunks read absent after). `ClearAllDerivedRejections` deletes 2 RJ (count 0 after). `ClearAllDiscussed` deletes 2 RD (both sessions empty after).
**Refs:** crc-Store.md, R2744
