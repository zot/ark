# Test Design: NanoApprover
**Source:** crc-NanoApprover.md

## Test: y approves once
**Purpose:** R2547 — "y" approves a single command without flipping ApproveAll
**Input:** ApproveAll=false, Stdin "y\n"
**Expected:** approve returns true; Nano.ApproveAll remains false
**Refs:** crc-NanoApprover.md

## Test: a approves and persists
**Purpose:** R2548 — "a" flips ApproveAll to true
**Input:** ApproveAll=false, Stdin "a\n"
**Expected:** approve returns true; Nano.ApproveAll is now true
**Refs:** crc-NanoApprover.md

## Test: EOF denies
**Purpose:** R2549 — EOF on stdin denies the command
**Input:** ApproveAll=false, empty Stdin
**Expected:** approve returns false
**Refs:** crc-NanoApprover.md

## Test: unrecognized input denies
**Purpose:** R2549 — any unknown response denies
**Input:** Stdin variants: "no\n", "x\n", "yes please\n", "\n"
**Expected:** all return false
**Refs:** crc-NanoApprover.md

## Test: ApproveAll short-circuit
**Purpose:** R2546 — ApproveAll bypasses the prompt and Stdin read
**Input:** ApproveAll=true, Stdin closed
**Expected:** approve returns true without reading from Stdin
**Refs:** crc-NanoApprover.md
