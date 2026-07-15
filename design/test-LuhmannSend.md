# Test Design: LuhmannSend
**Source:** crc-LuhmannSend.md

Deterministic units of the command bridge, reachable with a fake `luhmannHub`
and synthetic JSONL bytes — no server, no live orchestrator, no DB.

## Test: request builder embeds an inert backquoted nonce
**Purpose:** R3131 — the command request carries the instruction verbatim and
the nonce as a backquoted `` `LSEND:<n>` `` marker (a code literal, inert to
the agent).
**Input:** `buildCommandRequest("summarize this listing", 7)`.
**Expected:** output contains the instruction text unchanged; contains the
substring `` `LSEND:7` `` (backticks present); the marker is the searchable
token `commandNonceMarker(7) == "LSEND:7"`, and that token appears in the built
request.

## Test: window scanner brackets open→first-turn-completion
**Purpose:** R3132 — the window runs from the marker line to the first
`turn_duration` after it.
**Input:** synthetic JSONL bytes — a `user` tool_result line carrying
`LSEND:7`, then two `assistant` lines, then a `system`/`turn_duration` line,
then a later `assistant` line. `scanSendWindow(data, "LSEND:7")`.
**Expected:** `closed == true`; `window` includes the marker line through the
`turn_duration` line and **excludes** the trailing assistant line after it.

## Test: window scanner waits when open seen but no close yet
**Purpose:** R3132 — a turn still in progress does not close the bracket.
**Input:** the marker line + one `assistant` line, no `turn_duration` yet.
**Expected:** `closed == false` (the tail keeps polling).

## Test: window scanner ignores turn_duration before the marker
**Purpose:** R3132 — only a `turn_duration` **after** the open counts; a prior
turn boundary must not close a window that has not opened.
**Input:** a `system`/`turn_duration` line, then the marker line, no later
`turn_duration`.
**Expected:** `closed == false` (the pre-marker boundary is not the close).

## Test: JSONL lookup is index-independent (filesystem glob)
**Purpose:** R3132 — `locateSessionJSONL` finds the orchestrator's log by UUID
on disk, since its `~/.ark/luhmann` cwd is never a corpus source. (The
regression the live end-to-end caught: the old `db.SessionJSONLs` index query
missed a just-launched, unindexed session.)
**Input:** a temp `$HOME` with `~/.claude/projects/<proj>/<uuid>.jsonl`.
**Expected:** returns that path for the UUID; returns `errLuhmannNoJSONL` for an
unknown UUID.

## Test: tail loop returns the closed window from a file
**Purpose:** R3132 — `tailSendWindow` reads from the anchor offset, finds the
marker→turn_duration window, and returns it. Deterministic: the temp file
already holds a closed window, so the first poll reads to EOF and returns.
**Input:** a temp JSONL with pre-anchor content, then marker line, assistant,
`turn_duration`, and a trailing assistant line; `tailSendWindow` anchored past
the pre-content.
**Expected:** returns the window spanning marker→`turn_duration`, excluding both
the pre-anchor line and the trailing line.

## Test: tail loop times out with no closing window
**Purpose:** R3133 — no window closes within the deadline ⇒
`errLuhmannSendTimeout`.
**Input:** a temp JSONL with one assistant line (no marker); a 60ms timeout.
**Expected:** returns `errLuhmannSendTimeout`.

## Test: orchestrator gate short-circuits with no owner
**Purpose:** R3134 — no live orchestrator ⇒ orchestrator-not-running, and the
gate returns before any enqueue or JSONL lookup.
**Input:** `LuhmannSend` on a zero-value `*Server` (no seat claimed, so
`LuhmannOwner()` is `""`).
**Expected:** returns `errLuhmannNoOrchestrator`. The gate precedes the
`db`/`EnqueueLuhmann` calls, so a zero-value server (nil `db`, nil `nextQueue`)
never reaches them — the clean error, not a panic, is itself the proof.
