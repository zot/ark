# Test Design: Recall Secretary (seam 3a)
**Source:** crc-RecallAgentBuilder.md, crc-RecallWatcher.md

## Test: tube derivation — the address is never typed
**Purpose:** R3322. `deriveTube` resolves a secretary's routing address from the
identity and nonce the call already carries, so no agent types one. A nonce in a
class roster is a pool secretary and gets the composite; any other nonce is a
session's own secretary and gets the bare identity.
**Input:** `deriveTube(session, nonce, pooled)` across the four combinations,
plus the handler-level override and empty-identity cases.
**Expected:**
- `("S", 7, true)` → `"S-7"` — the pool composite (R3032), matching what the
  watcher composes from `luhmannOwner` + nonce, with nothing reported back.
- `("S", 7, false)` → `"S"` — a session's own secretary.
- an explicit `--tube ADDR` is passed through untouched, derivation not
  consulted: the flag is an override, not a hint.
- `("", 7, …)` → `""`. With no identity there is nothing to derive from, and
  the handler must not invent an address. Unreachable from the CLI, which
  declares the identity need and fails before its Action, but asserted so the
  server never fabricates a tube if some other caller omits it.

The pooled/non-pooled split is the load-bearing pair: collapse it and every
session's own secretary would subscribe to `<S>-<nonce>`, a tube the watcher
never publishes to, which is silent (R3322).
**Refs:** crc-RecallAgentBuilder.md, R3322, R3032

## Test: userProse — genuine vs. injected vs. tool-result
**Purpose:** Verify genuine-user extraction for conversation injection. R3009 keys on the positive human-origin marker (`origin.kind == "human"`), not origin-absence: a `"human"`-stamped prose string yields the text; an unstamped turn, a `task-notification` origin, array (tool-result) content, and empty content are all rejected. (Used by the R2891 injection path.)
**Input:** `userProse("human", `"hello there"`)` → text; `userProse("", `"hello there"`)`, `userProse("task-notification", `"x"`)`, `userProse("human", `[{"type":"tool_result"}]`)`, `userProse("human", ``)` → all rejected.
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
