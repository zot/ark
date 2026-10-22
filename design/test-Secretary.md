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

## Test: Prose — genuine vs. injected vs. tool-result
**Purpose:** Verify genuine-user extraction for conversation injection, through the transcript reader's `TranscriptRecord.Prose()` (R3553). R3009 keys on the positive human-origin marker (`origin.kind == "human"`), not origin-absence: a `"human"`-stamped prose string yields the text; an unstamped turn, a `task-notification` origin, array (tool-result) content, and empty content are all rejected. (Used by the R2891 injection path.)
**Input:** user lines with origin `human` and content `"hello there"` → text; no origin, origin `task-notification`, origin `human` with tool-result content, and origin `human` with no content → all rejected.
**Expected:** `("hello there", true)`, `("", false)`, `("", false)`, `("", false)`.
**Refs:** crc-RecallAgentBuilder.md, R2891

## Test: Text — string and content-array shapes
**Purpose:** Verify assistant-text extraction (R2891), through `TranscriptRecord.Text()` (R3553): a bare string, the concatenated `text` blocks of an array, tool_use blocks skipped.
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
  The transcript reader's `Prose()` and `Text()` it composes are covered.

## Test: `recall next` echoes only the address the caller typed
**Purpose:** R3322 says a secretary never types its routing address. The server
resolves it, and the prompts' "run next again" line must not hand the resolved
address back as `--tube`. Measured 2026-10-22: every prompt echoed the resolved
composite, the agent template forbids passing one, and a pool secretary
alternated between the two forms and quit.
**Input:** a builder over a test DB with a live PubSub and a bloodhound-result
subscriber for the session; a pending search task written by
`RecallBloodhoundOpen`; `RecallNext` with the resolved tube and a requested tube
of "" and then "override-X".
**Expected:** the returned task prompt ends with `next 3 again` when nothing was
requested (no `--tube` anywhere), and with `next --tube override-X 3 again` when
the caller passed one.
**Refs:** crc-RecallAgentBuilder.md, R3322
**Code:** recall_secretary_test.go
**Fire alarm:** pass the resolved tube to the task prompt instead of the
requested one; the prompt says `--tube sess-A` to a caller that typed none.
**Inject:** recall_next.go:RecallNext
**Pulled:** 2026-10-22 — rang: "prompt tells the secretary to pass --tube although it typed none"

## Test: listen's footer describes what arrived
**Purpose:** R2865's crank handle tells the caller what to do with what came
back. Measured 2026-10-22: a directed finding came back calling itself ambient
recall, with Surface and Recommend guidance for items it did not have.
**Input:** three result docs written to tmp:// and handed to
`recallListenPrompt` as events: a finding on the bloodhound-result tag; a
finding carrying a `## Recommend:` item (a pool hunt's proposal, R3283); an
ambient recollection on the recall-result tag.
**Expected:** a finding's footer names it as the answer to the caller's
watermark and says nothing about ambient recall; the reject-a-tag step appears
for the recollection and for the finding with a proposal, not for the plain
finding; every footer ends with the instruction to listen again.
**Refs:** crc-RecallAgentBuilder.md, R2865, R3283
**Code:** recall_secretary_test.go
**Fire alarm:** restore HEAD's `recallListenPrompt`; both finding cases fail,
with the footer calling them ambient recall.
**Inject:** recall_next.go:recallListenPrompt
**Pulled:** 2026-10-22 — rang: "footer has \"ambient recall\", which this batch does not warrant" on both finding cases

## Test: the subagent token readers
**Purpose:** R2777, R3560. A subagent's context fill is the latest assistant turn
that reports one, and its token totals sum every assistant turn. Neither was
tested before they moved onto the transcript reader; found 2026-10-22, when
injecting "first instead of latest" and "drop the output tokens" turned nothing
red.
**Input:** a transcript of three assistant turns (context fills 1000, 25000 and
none) around a tool-result turn; a missing file.
**Expected:** context fill 25000; totals (6 in, 60 out); the missing file
reports no fill.
**Refs:** crc-RecallAgentBuilder.md, crc-TranscriptReader.md, R2777, R3560
**Code:** recall_secretary_test.go
**Fire alarm:** stop at the first nonzero fill; the context reads 1000.
**Inject:** recall_agent_builder.go:SubagentContextTokens
**Pulled:** 2026-10-22 — rang: SubagentContextTokens = (1000, true), want 25000
**Fire alarm:** drop the output tokens from the sum; the totals read (6, 0).
**Inject:** recall_agent_builder.go:sumSubagentTokens
**Pulled:** 2026-10-22 — rang: sumSubagentTokens = (6, 0), want (6, 60)
