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
