# Test Design: Derived Tags
**Source:** crc-Store.md, crc-Librarian.md, crc-CLI.md, crc-Server.md

## Test: Store.WriteDerivedProposal round-trip + tally
**Purpose:** Verify WriteDerivedProposal writes RC with tally=1 on first call and increments to 2 on second.
**Input:** Inside one write txn, call `WriteDerivedProposal(chunkID=42, tagname="priority")` twice. Read key `"RC" + varint(42) + "priority"` from the ark subdatabase.
**Expected:** Record exists. After first call, value decodes to big-endian uint64 = 1. After second call, value decodes to 2.
**Refs:** crc-Store.md, seq-derived-tags.md#1.7, R2664, R2674

## Test: Store.WriteDerivedFreshness round-trip
**Purpose:** Verify WriteDerivedFreshness writes RF with the supplied serial.
**Input:** Call `WriteDerivedFreshness(chunkID=42, serial=12345)`. Read key `"RF" + varint(42)`.
**Expected:** Record exists. Value decodes via varint to uint64 = 12345.
**Refs:** crc-Store.md, R2666, R2669

## Test: Store.ReadDerivedFreshness missing returns (0, false)
**Purpose:** Verify ReadDerivedFreshness on a chunk that never derived returns (0, false, nil).
**Input:** Call `ReadDerivedFreshness(chunkID=999)` against a fresh DB.
**Expected:** Returns (serial=0, found=false, err=nil).
**Refs:** crc-Store.md, R2669, R2682

## Test: Store.ReadDerivedFreshness malformed varint treated as 0
**Purpose:** Verify a corrupt RF value is silently treated as serial 0 (force re-derivation).
**Input:** Manually write `"RF" + varint(42)` with a non-varint value (e.g. a single 0xFF byte). Call `ReadDerivedFreshness(42)`.
**Expected:** Returns (serial=0, found=false, err=nil). No error surfaced to caller.
**Refs:** crc-Store.md, R2681

## Test: Store.HasDerivedRejection — present and absent
**Purpose:** Verify HasDerivedRejection returns true iff the RJ record exists.
**Input:** Call `RejectDerived(chunkID=42, tagname="bogus")`. Then call `HasDerivedRejection(42, "bogus")` and `HasDerivedRejection(42, "different")`.
**Expected:** First returns (true, nil); second returns (false, nil).
**Refs:** crc-Store.md, R2665, R2673

## Test: Store.DerivedProposals returns tally-descending
**Purpose:** Verify DerivedProposals sorts by tally descending.
**Input:** Write RC records for chunk 42: `("priority", tally=3)`, `("status", tally=1)`, `("axis", tally=5)`. Call `DerivedProposals(42)`.
**Expected:** Returns three entries in order: `axis(5), priority(3), status(1)`.
**Refs:** crc-Store.md, R2678

## Test: Store.DerivedProposals filters RJ-shadowed entries
**Purpose:** Verify DerivedProposals defensive-filters proposals that have an RJ record.
**Input:** Write RC records `("priority", 2)` and `("status", 1)` for chunk 42. Write `RJ[42 + "status"]`. Call `DerivedProposals(42)`.
**Expected:** Returns only `priority(2)`. The `status` entry is filtered out.
**Refs:** crc-Store.md, R2678, R2673

## Test: Store.DerivedProposals malformed value tally=0
**Purpose:** Verify a corrupt 4-byte RC value surfaces as tally=0, not as an error.
**Input:** Manually write `"RC" + varint(42) + "priority"` with a 4-byte value. Call `DerivedProposals(42)`.
**Expected:** Returns one entry `{ChunkID:42, Tagname:"priority", Tally:0}`. No error.
**Refs:** crc-Store.md, R2681

## Test: Store.AcceptDerived drops RC and writes F/V
**Purpose:** Verify AcceptDerived is atomic — RC delete + F/V append happen in one txn.
**Input:** Set up chunk 42 in a known file F. Write `RC[42 + "priority"] = tally=2`. Call `AcceptDerived(42, "priority", "high")`. Inspect: (a) RC[42+priority] presence; (b) V record for (priority, high); (c) F record for (42, priority).
**Expected:** (a) RC record gone. (b) V[priority \x00 high \x00 tvid] exists and contains chunk 42's varint. (c) F[42+priority] exists with the resolved tvid in its trailer. Returned tvid is the resolved value.
**Refs:** crc-Store.md, seq-derived-tags.md#2.2, seq-derived-tags.md#2.4, R2679

## Test: Store.AcceptDerived bare-tag value
**Purpose:** Verify AcceptDerived with empty value produces a bare-tag attach (no value segment in V).
**Input:** Write `RC[42 + "todo"] = 1`. Call `AcceptDerived(42, "todo", "")`. Inspect V records.
**Expected:** V record exists with key `V[todo \x00 \x00 tvid]` (empty value segment). RC[42+todo] gone.
**Refs:** crc-Store.md, R2679

## Test: Store.AcceptDerived idempotent on missing RC
**Purpose:** Verify AcceptDerived honors the user's intent even when RC has already been removed (e.g. concurrent accept).
**Input:** Call `AcceptDerived(42, "priority", "high")` for a (chunkid, tagname) with no RC record present. Inspect F/V.
**Expected:** No error. V/F records reflect the attach. The RC delete is a no-op LMDB Del on a missing key.
**Refs:** crc-Store.md, seq-derived-tags.md error paths, R2679

## Test: Store.RejectDerived drops RC and writes RJ
**Purpose:** Verify RejectDerived is atomic and writes the rejection marker.
**Input:** Write `RC[42 + "fluff"] = 1`. Call `RejectDerived(42, "fluff")`. Inspect RC and RJ.
**Expected:** RC[42+fluff] gone. RJ[42+fluff] exists with an 8-byte big-endian value within 1 second of NOW unix nanoseconds.
**Refs:** crc-Store.md, seq-derived-tags.md#3.2, seq-derived-tags.md#3.3, R2665, R2680

## Test: Store.RejectDerived idempotent
**Purpose:** Verify a second RejectDerived for the same (chunkid, tagname) overwrites the timestamp and does not error.
**Input:** Call `RejectDerived(42, "fluff")` at T=10. Call it again at T=20.
**Expected:** Both calls succeed. RJ[42+fluff] value decodes to the T=20 timestamp.
**Refs:** crc-Store.md, R2680

## Test: Store.MaxEDSerial reflects current ED landscape
**Purpose:** Verify MaxEDSerial returns the highest serial across all ED records.
**Input:** Write three ED records via WriteTagDefEmbedding at three distinct txn serials (a, b, c with c > b > a). Call `MaxEDSerial()`.
**Expected:** Returns c (or its txn's serial). Adding a fourth ED with a higher serial d raises subsequent calls' return to d.
**Refs:** crc-Store.md, R2669

## Test: Recall --propose runs derivation and stamps RF
**Purpose:** Verify a single --propose run scores chunks against ED, writes surviving RC entries, and stamps RF with maxED.
**Input:** Corpus with two ED records (`@food`, `@cooking`) and one chunk C whose EC vector is close to the `@food` ED. Chunk C is currently tagless. Run `ark connections recall --propose <text matching C>`.
**Expected:** RC[C+food] exists with tally=1. RF[C] exists with the current maxED. (Whether `@cooking` also appears depends on the similarity threshold; the test fixture uses a contrived score so only `@food` survives.)
**Refs:** crc-Librarian.md, seq-derived-tags.md#1.7, R2670, R2674

## Test: Recall --propose tally increments on re-run
**Purpose:** Verify re-running --propose against the same chunk + ED landscape skips derivation (RF fresh) and does NOT increment the tally.
**Input:** Run --propose once (writes RC[C+food]=1, RF[C]=maxED). Without changing ED records, run --propose again.
**Expected:** RC[C+food] still tally=1; RF[C] unchanged. Second pass took the freshness skip path (R2669).
**Refs:** crc-Librarian.md, R2669, R2674

## Test: Recall --propose tally increments after ED change
**Purpose:** Verify a tag-definition write invalidates RF for affected chunks and the next --propose re-derives, bumping tally where the same candidate survives.
**Input:** Same setup as previous. Now write a new ED record (or update an existing one) such that maxED advances. Run --propose again.
**Expected:** RC[C+food] tally bumps to 2 (same candidate re-emitted). RF[C] advances to the new maxED.
**Refs:** crc-Librarian.md, R2669, R2674

## Test: Recall --propose filters tags already on chunk
**Purpose:** Verify already-attached tags don't appear as proposals.
**Input:** Chunk C currently carries `@food: pasta` (so F[C+food] exists). Corpus has ED for `@food`. Run --propose targeted at C.
**Expected:** RC[C+food] is NOT written — the F-record probe filters it out (R2671). Other surviving candidates (if any) appear normally.
**Refs:** crc-Librarian.md, R2671

## Test: Recall --propose excludes ext-routed tagnames
**Purpose:** Verify external-routed tagnames on a chunk are excluded by name (bare-name rule).
**Input:** Chunk C is the target of `@ext: C @food: pasta` from another file. Corpus has ED for `@food`. Run --propose against C.
**Expected:** RC[C+food] is NOT written — the bare-name ext-exclusion rule skips it (R2672). Even though no F record exists at the target, @ext authority shadows the proposal.
**Refs:** crc-Librarian.md, R2672

## Test: Recall --propose skips RJ-rejected candidates
**Purpose:** Verify a previously rejected (chunk, tag) is never re-proposed.
**Input:** Write `RJ[C+food]`. ED for `@food` exists and would otherwise score high against C. Run --propose against C.
**Expected:** RC[C+food] is NOT written. The derivation pass's HasDerivedRejection check filters the candidate (R2673).
**Refs:** crc-Librarian.md, seq-derived-tags.md#1.7, R2673

## Test: Recall --propose without tag_model is a no-op
**Purpose:** Verify --propose with no tag_model configured exits silently — no RC/RF writes — and the substrate result is unaffected.
**Input:** ark.toml with no `tag_model` line. Run `ark connections recall --propose <input>`.
**Expected:** Recall returns the normal substrate result (trigram-only fallback). No RC, RJ, or RF records are written. No error.
**Refs:** crc-Librarian.md, R2676

## Test: Recall --propose derivation chunk set includes tagless
**Purpose:** Verify --propose processes tagless chunks (full scored set), independent of the caller's `-all` flag.
**Input:** Corpus has two chunks similar to the input: C1 (tagless) and C2 (tagged). Run `ark connections recall --propose <input>` *without* `-all`.
**Expected:** RC entries exist for C1 (the tagless one) — derivation processed it. The surfaced stencil shows only C2 (the caller's filter dropped C1). C1's RC records become visible only when later running `Store.DerivedProposals(C1)` or running `recall --propose -all` to surface C1.
**Refs:** crc-Librarian.md, R2668

## Test: --propose stencil emits @chunk-proposed-tags
**Purpose:** Verify the markdown stencil adds a `@chunk-proposed-tags` line for surfaced chunks with RC records, ordered by similarity desc.
**Input:** Surfaced chunk C with RC entries for `@priority`, `@status`, `@axis` such that EC similarity rank is priority > axis > status. Run `ark connections recall --propose <input>`.
**Expected:** The stencil includes `@chunk-proposed-tags: priority, axis, status` on C's block, after `@chunk-tags`.
**Refs:** crc-Librarian.md, seq-derived-tags.md#1.8, seq-derived-tags.md#1.9, R2684, R2685

## Test: --propose stencil omits the line for chunks with no RC
**Purpose:** Verify chunks with no RC records do not gain an empty `@chunk-proposed-tags:` line.
**Input:** Surfaced chunk C with zero RC records (e.g. all candidates filtered by ext-exclusion). Run --propose.
**Expected:** The stencil omits the `@chunk-proposed-tags` line entirely on C's block.
**Refs:** crc-Librarian.md, R2684

## Test: --propose stencil order — fresh chunk on-demand similarity
**Purpose:** Verify a fresh-skip chunk (derivation skipped via RF) still gets similarity-desc ordering by on-demand cosine computation.
**Input:** Chunk C has RC entries for two tags written by a *prior* derivation pass. Re-run --propose with RF fresh so derivation skips C. The on-demand cosine should still order the two proposed tags by similarity desc.
**Expected:** The `@chunk-proposed-tags` line orders tags by current cosine similarity, not by RC iteration order or tally.
**Refs:** crc-Librarian.md, R2685

## Test: JSON shape — ProposedTags populated with --propose
**Purpose:** Verify `RecalledChunk.ProposedTags` appears in JSON only when `--propose` is set AND the chunk has RC records.
**Input:** Run `ark connections recall --propose --json <input>` against a corpus where one surfaced chunk has RC and another does not.
**Expected:** The first chunk's JSON includes `"proposedTags": [...]`. The second chunk's JSON omits the field entirely (omitempty).
**Refs:** crc-Librarian.md, crc-Server.md, R2686

## Test: JSON shape — ProposedTags omitted without --propose
**Purpose:** Verify ProposedTags is omitted from JSON whenever --propose is not set, regardless of RC presence.
**Input:** Corpus has RC records for chunk C (from a prior --propose run). Run `ark connections recall --json <input>` (no --propose).
**Expected:** C's JSON omits `proposedTags`. No derivation activity occurred.
**Refs:** crc-Librarian.md, R2686

## Test: --propose CLI flag is purely additive
**Purpose:** Verify the substrate's surfaced result is identical with and without --propose (modulo the proposed-tags line addition).
**Input:** Run `ark connections recall --json <input>` twice — once with --propose and once without — against the same corpus state. Compare the JSON.
**Expected:** All chunk-level fields (chunkID, path, range, score, perSubstrate, tags, content) are identical. The only difference is the optional `proposedTags` field on chunks with RC records.
**Refs:** crc-Librarian.md, R2667

## Test: Lua sys.recall accepts propose option
**Purpose:** Verify the Lua bridge maps `opts.propose = true` to RecallOpts.Propose.
**Input:** Lua call `sys.recall(inputs, {propose = true})` against a corpus with derivable candidates.
**Expected:** Returned result has `proposedTags` populated for at least one chunk (matching the same chunk's RC entries). RC records are written. Without `propose = true` in the same Lua call, no ProposedTags appear and no RC records are written.
**Refs:** crc-Server.md, R2677

## Test: Concurrent --propose calls serialize via write actor
**Purpose:** Verify two concurrent --propose calls writing to the same chunk do not lose tally updates.
**Input:** Run two `ark connections recall --propose <input>` invocations in parallel against the same input, with the corpus state having one matching candidate chunk + tag. (Use the in-process Go test harness, not separate processes, to avoid server-mode complications.)
**Expected:** RC[chunk+tag] final tally is the sum of both invocations' increments (tally=2, not 1). No partial writes; no panics; both pass complete with non-error returns.
**Refs:** crc-Librarian.md, R2674, R2675
