# Test Design: PtyHost
**Source:** crc-PtyHost.md

The fan-out and lifecycle logic is deterministic and testable with a fake client
(a small double that reports a size and captures broadcast bytes through a
bounded buffer) and a fake child (an in-memory pipe) — no real pty or server.
The launch-confirmation protocol (R3126) touches a real JSONL and the seat
lease, so it is exercised at integration level, not here (see the Gaps phase).

## Test: smallest-wins resize, both directions
**Purpose:** R3120 — the pty size tracks the min across clients and grows on disconnect.
**Input:** attach client A (80×24), then B (100×40). Detach A. Attach C (60×20).
**Expected:** pty size = 80×24 after B, recomputes to 100×40 after A detaches (grew), then 60×20 after C (shrank); a resize (`SIGWINCH`) fires on every change.
**Refs:** crc-PtyHost.md, seq-pty-attach.md#1.2, seq-pty-attach.md#1.6

## Test: forceRepaint toggles the size to force a SIGWINCH redraw
**Purpose:** R3136 — a same-size resize is a kernel no-op, so a repaint shrinks a row then restores, giving the child a real `SIGWINCH` to redraw on.
**Input:** attach a client (80×24); clear the recorded resizes; call `ForceRepaint`.
**Expected:** exactly two resizes — 80×23 then 80×24.
**Refs:** crc-PtyHost.md

## Test: repaint frame round-trips
**Purpose:** R3136 — the `repaint` frame is kind-only on the client→host channel.
**Input:** `WritePtyRepaint` into a buffer, then `ReadPtyFrame`.
**Expected:** kind is `ptyFrameRepaint`, nil data.
**Refs:** crc-PtyHost.md

## Test: child env carries the managed-pty marker
**Purpose:** R3139 — the hosted child is marked so `/luhmann` can tell it was launched by ark.
**Input:** an env slice with a session marker (`CLAUDECODE`) + credentials.
**Expected:** `ARK_MANAGED_PTY=1` present; credentials pass through; the session marker stripped (R3127).
**Refs:** crc-PtyHost.md

## Test: broadcast drops a slow client, never blocks
**Purpose:** R3118 — a client whose buffer overflows is dropped; others keep receiving.
**Input:** attach a normal client and a "stuck" client (bounded buffer that never drains); push more child output than the stuck buffer holds.
**Expected:** the stuck client is dropped from the set; the normal client receives all output; the broadcast never blocks.
**Refs:** crc-PtyHost.md, seq-pty-attach.md#1.3

## Test: input merge is serialized
**Purpose:** R3119 — two clients' input cannot interleave mid-write.
**Input:** two clients each send a distinct multi-byte chunk concurrently.
**Expected:** the child receives each chunk contiguously (no interleave); both chunks arrive.
**Refs:** crc-PtyHost.md, seq-pty-attach.md#1.4

## Test: launch rejects a second session
**Purpose:** R3122 — one hosted session at a time.
**Input:** a PtyHost with a session already hosted; call launch again.
**Expected:** an "already hosted" error; the existing session is undisturbed.
**Refs:** crc-PtyHost.md, seq-pty-launch.md#1.2

## Test: zero clients keeps the session running
**Purpose:** R3121 — detach independence; the session survives zero attached clients.
**Input:** attach one client, then detach it.
**Expected:** the child is still running; a subsequent attach re-registers and re-syncs the size.
**Refs:** crc-PtyHost.md, seq-pty-attach.md#1.7

## Test: child env strips nested-session markers
**Purpose:** R3127 — the hosted child launches as a fresh top-level session, not a detected nested one.
**Input:** an env slice mixing the markers (`CLAUDECODE`, `CLAUDE_CODE_*`, `AI_AGENT`) with credentials/config (`ANTHROPIC_API_KEY`, `CLAUDE_EFFORT`, `HOME`) and a present `TERM`.
**Expected:** every marker is removed; credentials/config and `TERM` pass through untouched.
**Refs:** crc-PtyHost.md

## Test: child env ensures TERM when absent
**Purpose:** R3127 — the TUI renders even if the server's env has no `TERM`.
**Input:** an env slice with no `TERM=` entry.
**Expected:** a `TERM=xterm-256color` entry is appended.
**Refs:** crc-PtyHost.md

## Test: trust dialog accept reads the option number
**Purpose:** R3128 — the accept keystrokes are the option number read from the stream, then Enter.
**Input:** a faithful "trust this folder" render — escapes with digit params (a cursor move right before "trust"), the number ahead of the "Yes … trust" label in stream order, and the question (which also contains "trust").
**Expected:** the scan strips the escapes, ignores the question, and returns `1\r`.
**Refs:** crc-PtyHost.md, seq-pty-launch.md#1.3.1

## Test: reordered trust menu still answered
**Purpose:** R3128 — the number is read, not assumed, so a reordered menu is answered correctly.
**Input:** a dialog where "Yes, I trust" is option 2 and "No, exit" is option 1.
**Expected:** the scan returns `2\r`.
**Refs:** crc-PtyHost.md

## Test: no false positive on ordinary output
**Purpose:** R3128 — normal output is not mistaken for the dialog, even when it contains the word "trust".
**Input:** a ready-prompt render, a sentence using "trust", and empty input.
**Expected:** the scan reports no dialog (no keys) for each.
**Refs:** crc-PtyHost.md

## Test: new-project directory encoding
**Purpose:** R3128 — the new-project check maps cwd to Claude Code's per-project log dir.
**Input:** cwd `/home/deck/.ark/luhmann`.
**Expected:** the computed path ends with `/.claude/projects/-home-deck--ark-luhmann` (`/` and `.` → `-`).
**Refs:** crc-PtyHost.md

## Test: accepter fires, accumulates, and waits
**Purpose:** R3128, R3327 — the armed accepter handles the dialog exactly once, across chunk boundaries, and stays quiet otherwise. Driven against a standalone per-launch accepter, which is the point of R3327: it needs no host at all, because it is owned by one reader rather than shared on the host.
**Input:** an armed accepter fed (a) the full dialog, (b) the dialog split across two reads, (c) ordinary output with no menu.
**Expected:** (a) disarms, closes `trustDone`, and sends `1\r` to the input funnel; (b) still detects across the split; (c) stays armed, `trustDone` open, no keystrokes sent.
**Refs:** crc-PtyHost.md, seq-pty-launch.md#1.3.1

## Test: a child self-exit records its pool secretaries' exits
**Purpose:** R3323 — the teardown obligations run on the path down that no verb drives; before this, a child that died on its own left its secretaries in the roster as ghosts.
**Input:** a host with a fake master (pipe read end) and a hosted session id, its read chain running (R3333); close the write end so the master reaches EOF, as a child going away on its own does.
**Expected:** exactly one exit recording, for that session id and carrying the self-exit reason (R3324); the seat is released and the host reports nothing hosted.
**Refs:** crc-PtyHost.md, seq-pty-attach.md#1.3

## Test: stop records once, with the stop reason, despite the reader's racing finalize
**Purpose:** R3323, R3324 — the chokepoint's idempotence is what lets both callers ask unconditionally, and the reason distinguishes ark's teardown from the child's.
**Input:** the same host, with the read chain running so the master's close raises a second, racing finalize; call `Stop`.
**Expected:** exactly one exit recording, carrying the stop reason — never two, and never relabelled by the reader's finalize that follows.
**Refs:** crc-PtyHost.md

## Test: a teardown with no confirmed session records nothing
**Purpose:** R3323 — the recording is guarded on a hosted session id, so an abandoned launch (no seat claim, hence no secretaries) writes no records, and an unmanaged Luhmann's roster is left alone.
**Input:** a host with a master installed but no session id, finalized with the launch-failed reason.
**Expected:** no exit recordings at all.
**Refs:** crc-PtyHost.md

## Test: Reserve rejects a second launch
**Purpose:** R3122, R3325 — one launch at a time, and the rejection must be a *return value* so the asynchronous binding can report it instead of losing it in a goroutine.
**Input:** a fresh host reserved twice; and a second host with a master already installed, reserved once.
**Expected:** the second reserve errors (launch mid-flight), and the reserve against a hosted session errors too.
**Refs:** crc-PtyHost.md

## Test: a launch rejected at the reservation publishes nothing
**Purpose:** R3326 — the announcement speaks only for launches that started; publishing a failure for one that never began would be a false record, and would overwrite the hosted tag while that session is still up.
**Input:** a host with a session already hosted; call `Launch`.
**Expected:** an error, and zero publications.
**Refs:** crc-PtyHost.md

## Test: the launch-outcome body is always one line
**Purpose:** R3326 — a tag's value runs to end of line, so a multi-line error would be cut at the first newline and its tail indexed as loose prose.
**Input:** a confirmed session; a single-line reason; a reason containing newlines and runs of spaces; an empty reason; a whitespace-only reason.
**Expected:** the hosted tag for the first, the failed tag for the next two with all whitespace folded to single spaces, and *no content at all* for the last two — an empty-valued tag is never published.
**Refs:** crc-Server.md

## Test: ReleaseReservation frees a launch that never ran
**Purpose:** R3325 — the asynchronous caller's panic path is the one exit `LaunchReserved` cannot clean up after, and a stuck reservation would refuse every later launch as "already hosted".
**Input:** reserve, release, reserve again.
**Expected:** the second reserve succeeds.
**Refs:** crc-PtyHost.md

## Test: output reaches a client in order, and the reader's buffer may be reused behind it
**Purpose:** R3333, R3339 — the read buffer is handed around rather than shared, so a connection must end up with its own copy of the bytes, and the copies must arrive in read order. This is the successor to the retired R3328 test: what it checked (the client's bytes survive the reader overwriting its buffer) is still required, by ownership rather than by an exact-size copy.
**Input:** a host with a master and one attached client; append several distinct chunks through the actor, overwriting the source buffer between appends as the next read would.
**Expected:** the client receives every chunk, concatenated in append order, none of them corrupted by the source buffer being rewritten.
**Refs:** crc-PtyHost.md, seq-pty-attach.md#1.3

## Test: at most one write goroutine per connection
**Purpose:** R3338 — a second concurrent writer would break both the ordering guarantee and gorilla's one-writer rule, and the flag that prevents it is only correct if every transition happens inside the actor.
**Input:** a client whose Write blocks on a release channel and records its concurrent-entry count; append many chunks while the first write is in flight, then release.
**Expected:** the recorded maximum concurrency is 1, and every appended byte is eventually delivered.
**Refs:** crc-PtyHost.md, seq-pty-attach.md#1.3.4

## Test: bytes appended during an in-flight write are not lost
**Purpose:** R3337 — the no-lost-wakeup property. Output arriving while a writer is in flight is picked up by the continuation, not dropped and not left sitting in pending with nothing scheduled to write it.
**Input:** a client whose Write blocks; append chunk A (starts the writer), append B and C while it is blocked, then release the writer.
**Expected:** the client's captured bytes are A+B+C in that order, with no further append needed to flush B and C.
**Refs:** crc-PtyHost.md, seq-pty-attach.md#1.3.5

## Test: a connection past the pending cap is dropped, and the others are not
**Purpose:** R3340, R3118 — the byte cap is where the bounded-buffer contract is now enforced, and the whole point of dropping is that one stalled client does not stall the fan-out.
**Input:** two clients, one blocking in Write and one draining freely; append more than the cap's worth of output.
**Expected:** the blocked client is dropped and closed and leaves the client set; the draining client keeps receiving; the pty size is recomputed, since a drop is a detach.
**Refs:** crc-PtyHost.md, seq-pty-attach.md#1.3.2

## Test: a failed write deregisters its connection
**Purpose:** R3341, R3118 — a transport that errors is the ordinary way a client goes away, and the continuation has to deregister rather than continue writing into a dead socket.
**Input:** a client whose Write returns an error; append one chunk.
**Expected:** the client is dropped from the set and closed, and no further Write is attempted on it.
**Refs:** crc-PtyHost.md, seq-pty-attach.md#1.3.5

## Test: a continuation for a detached connection spawns nothing
**Purpose:** R3342 — a write completing after its connection is gone must not respawn, or every teardown racing an in-flight write leaks a goroutine.
**Input:** a client whose Write blocks; append a chunk to start the writer, Detach the connection, then release the write.
**Expected:** the host settles with an empty client set, no panic, and no further Write on the detached client.
**Refs:** crc-PtyHost.md, seq-pty-attach.md#1.3.5

## Test: the read cycle hands one buffer around across many reads
**Purpose:** R3333, R3334 — the read runs on a goroutine and the actor spawns the successor, so the cycle must survive many reads on one buffer without dropping or reordering anything.
**Input:** a pipe standing in for the master with the read cycle running and one attached client; write eight chunks, waiting for each to land before writing the next so the assertion is about order rather than framing.
**Expected:** the client receives every chunk, concatenated in write order.
**Refs:** crc-PtyHost.md, seq-pty-attach.md#1.3.1

*The EOF half of the cycle — the chain ending and finalizing exactly once — is
covered by the self-exit and stop-race tests above, which drive the same
`startRead` chain over a pipe and then close the write end.*

## Test: a read chain outliving its session distributes nothing
**Purpose:** R3343 — after a teardown the old master stays open through the stop grace, and its remaining output must not reach the clients of a launch that started in that window.
**Input:** a pipe master with the read cycle running; install a *different* master (standing in for a later launch) and attach a client, then write into the original pipe twice.
**Expected:** the new client receives nothing from either write.
**Note — what this does not prove.** It asserts the "publishes nothing" half only. The "keeps draining" half is inferred from the second write being accepted rather than measured: proving it properly means filling the pipe buffer past what the kernel will hold, which needs a real pty and belongs to the live smoke test (O152, PENDING #40), not to a fake-and-zero-value unit test.
**Refs:** crc-PtyHost.md, seq-pty-attach.md#1.3.3

## Test: the project slug is Claude Code's own
**Purpose:** R3644 — ark looks for a session's project folder under the name Claude Code gives it, on every platform.
**Input:** `/home/deck/.ark/luhmann`; the Windows cwd Claude Desktop recorded on windows-gpu (2026-10-27); a path holding `_`, a space and `ü`; one holding a surrogate pair; a 237-character path.
**Expected:** the folder Claude Desktop made for the Windows cwd, and for the rest what node prints running Claude Code 2.1.311's functions verbatim: one `-` per UTF-16 unit outside `A-Za-z0-9`, and the long path cut to 200 plus `-szbxz9`.
**Refs:** crc-PtyHost.md
**Code:** ptyhost_test.go
**Fire alarm:** map only `/` and `.`, as before
**Inject:** ptyhost.go:claudeProjectSlug
**Pulled:** 2026-10-27 (local) — rang: `got "-home-u-my_proj dir-über"`, `want "-home-u-my-proj-dir--ber"`; the Windows cwd came back unchanged
