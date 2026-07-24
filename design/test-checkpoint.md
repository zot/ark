# Test Design: Checkpoint
**Source:** crc-Checkpoint.md

Tests run the real fossil binary in `t.TempDir()` sandboxes (skip if
`~/.claude/bin/fossil` is absent). The 2026-07-23 incidents were all
interaction bugs between our command sequences and fossil's actual
semantics — a fake fossil would re-encode the assumptions that were
wrong. Sleeping-sentry style: the drills march the incident inputs
past the machinery and assert it challenges them.

## Test: branch save preserves working tree
**Purpose:** R3237 — the local/update dance never modifies disk (the
second-incident clobber).
**Input:** repo with trunk baseline `v1`; disk edited to `v2` plus a
new file; run the local-save sequence.
**Expected:** after the save, disk still holds `v2` and the new file;
`fossil cat -r local` returns `v2`; checkout is back on trunk.
**Refs:** crc-Checkpoint.md, seq-checkpoint.md#1

## Test: closed-leaf branch save reopens and commits
**Purpose:** R3238 — the first-incident swallowed refusal.
**Input:** repo whose `local` branch leaf is closed (skeleton); disk
edited; run the local-save sequence.
**Expected:** save succeeds; `local` leaf reopened; snapshot content
on `local`; no error output.
**Refs:** crc-Checkpoint.md, seq-checkpoint.md#1.4.1

## Test: missing branch is created
**Purpose:** R3238 — first save to a branch that doesn't exist.
**Input:** repo with only trunk; run the local-save sequence.
**Expected:** `local` branch created carrying the disk snapshot; tree
untouched.
**Refs:** seq-checkpoint.md#1.2.1

## Test: baseline commits current disk and preserves branches
**Purpose:** R3240 — baseline's snapshot source is the disk, and both
side branches survive (per-branch bundles).
**Input:** repo with stale trunk `v1`, branches `updates` and `local`
with distinct content; disk at `v2`; run baseline.
**Expected:** new trunk baseline content is `v2`; disk still `v2`;
`updates` and `local` both present with their content.
**Refs:** crc-Checkpoint.md, seq-checkpoint.md#2

## Test: nested enclosing checkout — no walk-up
**Purpose:** R3234/R3236 — the first-incident walk-up.
**Input:** app dir inside an open enclosing checkout; run fossilInit
(via first save), then verify.
**Expected:** open succeeds (`--nested`) and verifyCheckout reports
the app repo; the enclosing checkout's state is untouched (no close,
no commits on its trunk).
**Refs:** seq-checkpoint.md#3

## Test: verify rejects a foreign binding
**Purpose:** R3234 — verifyCheckout is the abort gate.
**Input:** app dir whose only reachable checkout is an enclosing one
(no nested checkout open).
**Expected:** verifyCheckout returns an error naming the expected
repo and the reported one; no fossil verb runs after it.
**Refs:** crc-Checkpoint.md

## Test: fossil failure surfaces
**Purpose:** R3239 — no swallowed errors, no unconditional success.
**Input:** a verb pointed at a corrupted/absent repo state that makes
a mid-dance fossil command fail.
**Expected:** the returned error names the fossil command and carries
fossil's output; the verb's success message is not printed.
**Refs:** crc-Checkpoint.md
