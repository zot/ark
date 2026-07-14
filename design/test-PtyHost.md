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
**Purpose:** R3128 — the armed accepter handles the dialog exactly once, across chunk boundaries, and stays quiet otherwise.
**Input:** an armed host fed (a) the full dialog, (b) the dialog split across two reads, (c) ordinary output with no menu.
**Expected:** (a) disarms, closes `trustDone`, and sends `1\r` to the input funnel; (b) still detects across the split; (c) stays armed, `trustDone` open, no keystrokes sent.
**Refs:** crc-PtyHost.md, seq-pty-launch.md#1.3.1
