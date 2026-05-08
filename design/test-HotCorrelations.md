# Test Design: Hot Correlations
**Source:** crc-Librarian.md, crc-Store.md, specs/hot-correlations.md

## Test: Store HC round-trip
**Purpose:** R2226, R2227, R2229 — write/read/delete HC records,
verify S-substrate stamping happens on the same path as ED/EC.
**Input:** `WriteHotCorrelation("priority", chunkID=1, score=0.85)`.
Read serial via `RecordSerial(prefixHotCorrelation, key)`. Confirm
non-zero. `ReadHotCorrelations("priority")` returns
`[{ChunkID:1, Score:0.85}]`. `DeleteHotCorrelation` drops both the
HC record and its `SHC*` stamp.
**Expected:** stamp present after write; absent after delete; read
returns the value verbatim.
**Refs:** R2226, R2227, R2229

## Test: DropEmbeddings clears HC and SHC alongside T/EV/ED
**Purpose:** R2231 — model swap drops HC together with the other
embedding caches.
**Input:** populate T, EV, ED, EC, and HC records. Call
`DropEmbeddings`.
**Expected:** every `HC*` record gone, every `SHC*` stamp gone,
existing R2187 invariants for `ST*`/`SEV*`/`SED*` still hold.
EC and `SEC*` untouched (consistent with R2187).
**Refs:** R2231

## Test: SweepHotCorrelations from-scratch populates top-K
**Purpose:** R2216, R2232, R2234 — from-scratch (zero bookmark)
runs phase 3 for every tag with ED records.
**Input:** index 25 chunks, write EC vectors crafted so each tag's
top-5 is well-defined. Write ED for two tags. `I:hcsweep` absent.
Call `SweepHotCorrelations()`.
**Expected:** `SweepResult.FromScratch == true`. Each tag has
`min(K_TOP_HC, eligible_chunks)` HC entries. Top-K matches the
hand-computed expected order. Bookmark advances to a non-zero
serial.
**Refs:** R2216, R2232, R2234

## Test: Sweep is idempotent with no changes
**Purpose:** R2233, R2236, R2239 — running with bookmark equal to
current high-water performs no work.
**Input:** populate as above; run sweep once. Run again immediately.
**Expected:** second `SweepResult` has `ChangedEDs == 0`,
`ChangedECs == 0`, `TagsRebuilt == 0`, `TagsTouched == 0`. HC
contents unchanged. Bookmark unchanged.
**Refs:** R2233, R2236, R2239

## Test: Sweep phase 3 picks up new ED record
**Purpose:** R2234 — adding an ED for a previously-unseen tag
triggers a tag-rebuild on next sweep.
**Input:** populate ED for tag "a" only; sweep; verify HC for "a"
exists. Now write ED for tag "b" (S serial advances). Sweep again.
**Expected:** second sweep has `ChangedEDs == 1`, `TagsRebuilt
includes "b"`. HC entries for "b" appear; HC for "a" unchanged.
**Refs:** R2234

## Test: Sweep phase 4 picks up new chunk and displaces
**Purpose:** R2235 — a new EC chunk that scores higher than the
current min of an unaffected tag's top-K displaces the lowest entry.
**Input:** populate chunks and ED for tag "a" so top-5 is full.
Sweep. Then write a new EC chunk crafted to score above the min.
Sweep again (no ED changes, only EC).
**Expected:** second sweep has `ChangedECs == 1`, `TagsRebuilt ==
0`, `TagsTouched >= 1`. The new chunk is in the top-K; the
displaced chunk is gone.
**Refs:** R2235

## Test: Mid-sweep error leaves bookmark unchanged
**Purpose:** R2236 — bookmark only advances on full success.
**Input:** install a fault on `WriteHotCorrelation` for one tag's
phase-3 rebuild. Capture bookmark before, run sweep (errors),
capture bookmark after.
**Expected:** sweep returns error. Bookmark unchanged. Tags processed
before the fault have their HC entries committed (per-tag txn
atomicity, R2238); the faulted tag and unprocessed tags have no
new HC entries beyond what they had before the call.
**Refs:** R2236, R2238

## Test: Top-K read returns from cache
**Purpose:** R2218 — TopKChunksForTag reads HC entries.
**Input:** populate HC entries for tag "a" with three chunks
(scores 0.9, 0.7, 0.5), valid CRecords, valid stamps.
**Expected:** `TopKChunksForTag("a", 5)` returns 3 entries in
descending score order, each with ChunkID/FileID/Path resolved.
**Refs:** R2218, R2220

## Test: Top-K alibi-stamp filter — EC moved
**Purpose:** R2219, R2249(b) — entry whose EC was rewritten after
the HC was stamped is dropped at read time.
**Input:** populate HC for chunkID=1. Then advance EC[1]'s serial
via `WriteChunkEmbedding(1, …)`. Call `TopKChunksForTag`.
**Expected:** entry for chunkID=1 is dropped. Other entries remain.
**Refs:** R2219, R2249

## Test: Top-K alibi-stamp filter — ED moved
**Purpose:** R2219, R2249(c) — entry whose tag's ED was rewritten
after the HC was stamped is dropped.
**Input:** populate HC for tag "a". Then write a new ED for
("a", fileid=99). Call `TopKChunksForTag("a", 5)`.
**Expected:** all entries are dropped (any ED move invalidates
the entire tag's top-K under alibi-stamp semantics).
**Refs:** R2219, R2249

## Test: Top-K alibi-stamp filter — EC missing
**Purpose:** R2249(a) — entry referring to a chunk whose EC has
been deleted is dropped.
**Input:** populate HC for chunkID=1. Call `DeleteChunkEmbedding(1)`.
Call `TopKChunksForTag`.
**Expected:** entry dropped, no error.
**Refs:** R2249

## Test: Top-K returns (nil, nil) when no HC
**Purpose:** R2220.
**Input:** no HC entries. Call `TopKChunksForTag("a", 5)`.
**Expected:** results == nil, err == nil.
**Refs:** R2220

## Test: RelatedTags ranks tags by max-pair cosine
**Purpose:** R2221, R2224 — top-K tags nearest to a focused tag.
**Input:** ED for "a", "b", "c" with vectors crafted so that
cosine(a,b) > cosine(a,c) > cosine(b,c). Call `RelatedTags("a", 5)`.
**Expected:** length 2, order ["b", "c"], with `SrcFileID`/
`DstFileID` set to the def files that scored each pair.
**Refs:** R2221, R2224

## Test: TagPairConflict returns max-pair score
**Purpose:** R2222.
**Input:** tag "a" has defs at fileid=10 and fileid=11; tag "b" has
defs at fileid=20. Vectors crafted so `cosine(a@10, b@20) = 0.5`
and `cosine(a@11, b@20) = 0.9`.
**Expected:** result `TagSimilarity{Tag:"", Score:0.9, SrcFileID:11,
DstFileID:20}`.
**Refs:** R2222, R2224

## Test: TagDrift pairwise within one tag
**Purpose:** R2223, R2225 — within-tag def cosines, descending.
**Input:** tag "a" has defs at fileids 10, 11, 12 with crafted
vectors yielding three distinct cosines.
**Expected:** length 3 (= 3*2/2), sorted descending, each pair's
FileIDA < FileIDB to canonicalize.
**Refs:** R2223, R2225

## Test: Progress doc lifecycle on success
**Purpose:** R2240, R2241, R2244 — tmp:// doc reflects sweep
state through `running` → `complete`.
**Input:** wire a fake clock or capture sequence of UpdateTmpFile
calls. Run a small sweep.
**Expected:** observed sequence:
1. `@sweep-status: running`, `@sweep-started: <ts>`, fields zeroed.
2. Zero or more `@sweep-progress:` ticks.
3. `@sweep-status: complete`, `@sweep-completed: <ts>`,
   `@sweep-orphan-total: <n>`, `@sweep-error:` absent.
**Refs:** R2240, R2241, R2244

## Test: Progress doc on error path
**Purpose:** R2243 — terminal error transition flushes immediately
and sets `@sweep-error:`.
**Input:** fault as in the mid-sweep error test.
**Expected:** doc has `@sweep-status: error`, `@sweep-error: <msg>`,
`@sweep-completed:` set. The error flush bypasses the throttle.
**Refs:** R2243

## Test: Progress throttle bounds update rate
**Purpose:** R2242 — at most one update per ~250 ms.
**Input:** drive the sweep with a fake clock that advances by 50 ms
per simulated tick over 1.0 s of work; capture UpdateTmpFile call
count.
**Expected:** between 4 and 6 update calls during the run (one per
~250 ms window plus the initial `running` flush and the terminal
flush). Not 20 — the throttle suppresses sub-window updates.
**Refs:** R2242

## Test: Read-only — Top-K and tag-tag queries don't mutate
**Purpose:** R2252, R2253, R2256 — none of the read APIs writes
LMDB.
**Input:** populate HC, EC, ED. Snapshot RecordCounts; run
`TopKChunksForTag`, `RelatedTags`, `TagPairConflict`, `TagDrift`;
snapshot again.
**Expected:** all per-prefix counts unchanged.
**Refs:** R2252

## Test: No orphan filter applied to TopK
**Purpose:** R2253 — chunks already carrying the tag are still
returned by TopKChunksForTag.
**Input:** chunk=1 has V record for ("priority", "high"). Populate
HC entry for ("priority", chunk=1).
**Expected:** `TopKChunksForTag("priority", 5)` includes chunk 1.
**Refs:** R2253

## Test: ED-changed sweep refreshes alibi
**Purpose:** R2251 — sweep replaces stale entries; subsequent reads
return the entries (no longer filtered).
**Input:** populate HC for tag "a"; advance ED for "a" so reads
filter all entries (matching the "ED moved" filter test). Run a
sweep. Read again.
**Expected:** sweep performs phase-3 rebuild for "a"; HC entries
are rewritten with stamps that exceed the new ED stamps; reads
return entries unfiltered.
**Refs:** R2251
