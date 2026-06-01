# Test Design: Recall Secretary (seam 3a)
**Source:** crc-RecallAgentBuilder.md, crc-RecallWatcher.md

## Test: userProse — genuine vs. injected vs. tool-result
**Purpose:** Verify genuine-user extraction for conversation injection (R2891): prose string content with no harness origin yields the text; a `task-notification` origin and array (tool-result) content are rejected.
**Input:** `userProse("", `"hello there"`)`, `userProse("task-notification", `"x"`)`, `userProse("", `[{"type":"tool_result"}]`)`, `userProse("", ``)`.
**Expected:** `("hello there", true)`, `("", false)`, `("", false)`, `("", false)`.
**Refs:** crc-RecallAgentBuilder.md, R2891

## Test: assistantText — string and content-array shapes
**Purpose:** Verify assistant-text extraction (R2891): a bare string, the concatenated `text` blocks of an array, tool_use blocks skipped.
**Input:** string content; `[{text:"a"},{text:"b"}]`; `[{text:"keep"},{tool_use}]`; `[{tool_use}]`; empty.
**Expected:** `"plain reply"`, `"a b"`, `"keep"`, `""`, `""`.
**Refs:** crc-RecallAgentBuilder.md, R2891

## Test: dropCooledCandidates — floor drops cooled, keeps novel + other sessions
**Purpose:** Verify the surface-cooldown floor (R2893): a candidate whose `(session, chunk)` was surfaced within `surface_cooldown` is dropped; other chunks and other sessions are unaffected.
**Input:** `surface_cooldown="24h"`; `MarkSurfaced("sess-A", 100)`; `dropCooledCandidates("sess-A", [{100},{200}])` and `dropCooledCandidates("sess-B", [{100}])`.
**Expected:** sess-A returns only chunk 200; sess-B returns chunk 100 (not cooled for it).
**Refs:** crc-RecallWatcher.md, R2893

## Test: dropCooledCandidates — zero window disables the floor
**Purpose:** Verify `surface_cooldown="0"` disables the floor (no drops). R2893
**Input:** `surface_cooldown="0"`; `MarkSurfaced("sess-A", 100)`; `dropCooledCandidates("sess-A", [{100}])`.
**Expected:** chunk 100 survives.
**Refs:** crc-RecallWatcher.md, R2893

## Test: SurfaceItem starts the surface cooldown
**Purpose:** Verify the `surface` verb marks the surfaced `(session, chunk)` (R2894) so the cooldown floor will suppress it.
**Input:** Open a curation builder for `("sess-A", fire=5)`; `SurfaceItem(5, 100, "relevant")`; `LastSurfaced("sess-A", 100)`.
**Expected:** `LastSurfaced` returns present with a non-zero timestamp.
**Refs:** crc-RecallAgentBuilder.md, R2894

## Deferred (not yet covered; integration harness needed)
- `RecallNext --session` value-scoped subscription + S-only dispatch
  (R2888/R2889) — needs a builder + tmp-doc + pubsub fixture.
- `recentConversation` last-N-turns ordering against a real session JSONL
  (R2891) — needs a configured chat-jsonl source + a session transcript.
  The pure parsers (`userProse`, `assistantText`) it composes are covered.
