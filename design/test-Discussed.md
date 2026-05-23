# Test Design: Discussed Tags
**Source:** crc-Store.md, crc-CLI.md, crc-Librarian.md, crc-Server.md

## Test: Store.AddDiscussed round-trip
**Purpose:** Verify AddDiscussed writes the RD record with the expected key shape and 8-byte unix-nanos value.
**Input:** Call `AddDiscussed("sess-1", "topic", "messaging")`, then directly read the key `"RD" + "sess-1" + \x00 + "topic" + \x00 + "messaging"` from the ark subdatabase.
**Expected:** Record exists; value is 8 bytes; decoded big-endian uint64 is within 1 second of the test's NOW. Key bytes match exactly.
**Refs:** crc-Store.md, seq-discussed.md#1.4, R2648, R2650

## Test: Bare-tag encoding writes empty value segment
**Purpose:** Verify a bare `@name` argument (no value) encodes with an empty trailing value segment.
**Input:** `AddDiscussed("sess-1", "topic", "")`.
**Expected:** Key is `"RD" + "sess-1" + \x00 + "topic" + \x00` (no trailing value bytes). ListDiscussed returns one entry with Tag="topic", Value="".
**Refs:** crc-Store.md, R2648

## Test: AddDiscussed re-add overwrites timestamp
**Purpose:** Verify re-adding an existing (session, tag, value) bumps the timestamp rather than creating a duplicate.
**Input:** Add "sess-1"/"topic"/"messaging" at T=10. Wait. Add the same triple at T=20.
**Expected:** Only one RD record exists for that key; its value decodes to the T=20 timestamp.
**Refs:** crc-Store.md, R2650

## Test: ListDiscussed scope + ordering
**Purpose:** Verify ListDiscussed returns only the requested session's entries; entries from other sessions are excluded.
**Input:** Populate "sess-A" with two tags and "sess-B" with one tag. Call `ListDiscussed("sess-A", 0, 24h)`.
**Expected:** Returns exactly the two "sess-A" entries; "sess-B" entry is not in the result.
**Refs:** crc-Store.md, R2651

## Test: ListDiscussed since filter
**Purpose:** Verify --since DUR drops entries older than NOW - DUR.
**Input:** Add entries at T=NOW-2h and T=NOW-10m. Call `ListDiscussed("sess-1", 30m, 24h)`.
**Expected:** Only the T=NOW-10m entry survives.
**Refs:** crc-Store.md, R2651

## Test: ListDiscussed lazy TTL drop
**Purpose:** Verify entries past their TTL are skipped on read without being deleted.
**Input:** Add entry at T=NOW-25h. Call `ListDiscussed("sess-1", 0, 24h)`.
**Expected:** Returns empty slice. Raw LMDB scan still finds the record (not yet pruned).
**Refs:** crc-Store.md, R2659

## Test: ListDiscussed skips malformed values
**Purpose:** Verify RD records whose value is not exactly 8 bytes are treated as expired.
**Input:** Manually write a key `"RD" + "sess-1" + \x00 + "topic" + \x00 + "messaging"` with a 4-byte value. Call `ListDiscussed("sess-1", 0, 24h)`.
**Expected:** Returns empty slice (the malformed entry is skipped, not surfaced).
**Refs:** crc-Store.md, R2663

## Test: ClearDiscussed scope
**Purpose:** Verify ClearDiscussed removes only the requested session's RD records.
**Input:** Populate "sess-A" and "sess-B" with multiple entries. Call `ClearDiscussed("sess-A")`.
**Expected:** Returned count matches the number of "sess-A" entries pre-clear. "sess-A" range scan returns empty. "sess-B" range scan unchanged.
**Refs:** crc-Store.md, R2652

## Test: PruneDiscussed cross-session sweep
**Purpose:** Verify prune deletes expired entries across all sessions and returns the count.
**Input:** Populate three sessions, mix old (T=NOW-25h) and recent (T=NOW-1h) entries. Call `PruneDiscussed(24h)`.
**Expected:** Returned count matches the number of old entries across all sessions. Subsequent range scans show only the recent entries.
**Refs:** crc-Store.md, R2653, R2659

## Test: `ark discussed add` CLI error cases
**Purpose:** Validate the CLI rejects missing/empty `--session` and missing tag list before any write.
**Input:**
- Case A: `ark discussed add @topic` (no `--session`).
- Case B: `ark discussed add --session "" @topic`.
- Case C: `ark discussed add --session sess-1` (no tag args).
**Expected:** All three exit non-zero with a descriptive error. No RD records are written for any case.
**Refs:** crc-CLI.md, seq-discussed.md#1.1, R2650

## Test: `ark discussed list --json` shape
**Purpose:** Verify the JSON output parses as `[{tag, value, timestamp}, ...]` with RFC3339 timestamps.
**Input:** Add three tags including one bare-name entry. Run `ark discussed list --session sess-1 --json`.
**Expected:** Output parses as a JSON array of three objects, each with `tag`, `value`, `timestamp` keys; bare-name entry has `value: ""`; timestamps parse as RFC3339.
**Refs:** crc-CLI.md, R2651

## Test: `ark discussed prune --ttl` invalid value
**Purpose:** Verify an unparseable `--ttl` value exits non-zero before any RD delete.
**Input:** Populate one session with one entry. Run `ark discussed prune --ttl foo`.
**Expected:** Exit non-zero with parse-error message. The entry is still present in LMDB after the call.
**Refs:** crc-CLI.md, R2653

## Test: Tag input grammar parses bare and exact-pair forms
**Purpose:** Verify the CLI parses `@name` and `@name:value` correctly; rejects `\x00` in name or value.
**Input:**
- Case A: `add --session s @topic @ext:tagdefs` — bare and exact-pair.
- Case B: `add --session s "@topic:hello world"` — value with whitespace.
- Case C: `add --session s "@bad\x00name"` — \x00 in name.
**Expected:** Case A writes two RD records (`topic`/`""`, `ext`/`tagdefs`). Case B writes one record with Value="hello world". Case C exits non-zero before any write.
**Refs:** crc-CLI.md, R2654

## Test: `connections recall --session SID` loads session exclusion
**Purpose:** Verify the substrate reads the session's RD records into the exclusion set.
**Input:** Populate "sess-1" with `@topic:messaging`. Run `ark connections recall --session sess-1 "<input that would match a chunk tagged @topic:messaging + @ext:other>"`.
**Expected:** The matching chunk surfaces but with `@topic:messaging` stripped from its tag list; `@ext:other` remains. If `@topic:messaging` was the only tag, the chunk is dropped.
**Refs:** crc-Librarian.md, crc-CLI.md, seq-discussed.md#2.3, seq-discussed.md#2.7, seq-discussed.md#2.8, R2655, R2656

## Test: `connections recall --discussed` parses comma list
**Purpose:** Verify the CLI parses `--discussed @t1,@t2:v` into the exclusion set without needing RD records.
**Input:** Run `ark connections recall --discussed "@topic,@ext:tagdefs" <input>` without any RD records.
**Expected:** The exclusion set contains `{topic, *}` and `{ext, tagdefs}`. Chunks lose those tags per the matching rule (R2657).
**Refs:** crc-CLI.md, R2655

## Test: `--session` + `--discussed` union
**Purpose:** Verify both flags together produce the union exclusion set.
**Input:** Populate "sess-1" with `@topic:messaging`. Run `ark connections recall --session sess-1 --discussed "@ext:tagdefs" <input>`.
**Expected:** Both `@topic:messaging` and `@ext:tagdefs` are in the exclusion set.
**Refs:** crc-Librarian.md, crc-CLI.md, R2655

## Test: Bare-name vs exact-pair matching
**Purpose:** Verify the matching rule (R2657) applied per chunk.
**Input:** Chunk C has tags `@topic:messaging`, `@topic:auth`, `@ext:tagdefs`.
- Case A: exclusion = `{topic, ""}` (bare). 
- Case B: exclusion = `{topic, "messaging"}`.
**Expected:**
- Case A strips both `@topic:*` pairs; chunk retains only `@ext:tagdefs`.
- Case B strips only `@topic:messaging`; chunk retains `@topic:auth` and `@ext:tagdefs`.
**Refs:** crc-Librarian.md, R2657

## Test: Discussed filter precedes `-all`
**Purpose:** Verify a chunk emptied by the discussed filter is dropped even with `-all` set.
**Input:** Chunk C has exactly one tag, `@topic:messaging`. Add `@topic:messaging` to "sess-1". Run `ark connections recall --session sess-1 -all <input>`.
**Expected:** Chunk C does not surface. `-all` does not save it.
**Refs:** crc-Librarian.md, R2658

## Test: Empty discussed set behaves like no filter
**Purpose:** Verify that with no `--session` and no `--discussed`, substrate behavior is unchanged.
**Input:** Same corpus, two runs of `ark connections recall <input>` — one before and one after adding the new flags' parser code.
**Expected:** Identical output. `RecallOpts.Discussed` with empty slice disables the filter.
**Refs:** crc-Librarian.md, R2655, R2660

## Test: TTL default and override
**Purpose:** Verify TTL falls back to 24h when `[recall].discussed_ttl` is absent, honors the configured value when present, and treats "0" as never-expire.
**Input:**
- Case A: ark.toml without `[recall]` section. Add entry at T=NOW-25h, list.
- Case B: ark.toml with `[recall] discussed_ttl = "168h"`. Add entry at T=NOW-100h, list.
- Case C: ark.toml with `[recall] discussed_ttl = "0"`. Add entry at T=NOW-1000h, list.
**Expected:** Case A returns empty (24h TTL expired). Case B returns the entry (within 168h). Case C returns the entry (no expiry).
**Refs:** crc-Store.md, crc-CLI.md, R2659

## Test: Invalid TTL config logs warning and falls back
**Purpose:** Verify an unparseable `[recall].discussed_ttl` falls back to 24h and the server logs a warning at startup.
**Input:** ark.toml with `[recall] discussed_ttl = "foo"`. Start server.
**Expected:** Server starts successfully; log contains a warning naming `discussed_ttl`. Effective TTL is 24h (verify via an entry at T=NOW-25h not surfacing).
**Refs:** crc-Store.md, crc-CLI.md, R2659, R2663

## Test: Lua `sys.recall` accepts `discussed` array
**Purpose:** Verify the Lua bridge maps `opts.discussed = {{tag="topic", value="messaging"}, ...}` to `RecallOpts.Discussed`.
**Input:** Lua call `sys.recall(inputs, {discussed = {{tag="topic", value="messaging"}}})`.
**Expected:** Returned results show `@topic:messaging` stripped from any matching chunk's tag list.
**Refs:** crc-Server.md, R2660

## Test: Lua `sys.discussed` exposes four verbs
**Purpose:** Verify add/list/clear/prune are all callable from Lua.
**Input:** Lua calls `sys.discussed.add(s, {{tag="topic", value="messaging"}})`; `sys.discussed.list(s)`; `sys.discussed.clear(s)`; `sys.discussed.prune({ttl="0"})`.
**Expected:** Each returns the expected shape (booleans, arrays, counts) and underlying RD records reflect the calls in order.
**Refs:** crc-Server.md, R2661

## Test: Substrate is read-only on RD
**Purpose:** Verify `ark connections recall` does not write any RD record under any circumstance.
**Input:** Snapshot LMDB RD range. Run multiple `ark connections recall` invocations with various `--session` and `--discussed` combinations. Snapshot RD range again.
**Expected:** Snapshots match exactly. The substrate is read-only on the recall namespace; only `ark discussed add` writes (R2662).
**Refs:** crc-Librarian.md, R2662
