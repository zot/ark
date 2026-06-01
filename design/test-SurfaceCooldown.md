# Test Design: Recall Surface-Cooldown (RM)
**Source:** crc-Store.md

## Test: Store.MarkSurfaced + LastSurfaced round-trip
**Purpose:** Verify MarkSurfaced writes an RM record and LastSurfaced reads its timestamp.
**Input:** Call `MarkSurfaced("sess-A", 42)`. Then `LastSurfaced("sess-A", 42)`.
**Expected:** Returns `(nanos, true, nil)` with nanos within 1 second of NOW. The stored key is `"RM" + "sess-A" + \x00 + varint(42)`; value is 8-byte big-endian.
**Refs:** crc-Store.md, R2882, R2883, R2884

## Test: Store.LastSurfaced absent
**Purpose:** Verify a never-surfaced (session, chunk) reads as absent.
**Input:** `LastSurfaced("sess-A", 999)` against a fresh DB.
**Expected:** Returns `(0, false, nil)`.
**Refs:** crc-Store.md, R2884

## Test: Store.MarkSurfaced overwrites timestamp
**Purpose:** Verify a second MarkSurfaced for the same (session, chunk) updates the timestamp.
**Input:** `MarkSurfaced("sess-A", 42)` at T=10; `MarkSurfaced("sess-A", 42)` at T=20. `LastSurfaced("sess-A", 42)`.
**Expected:** Single RM record; value decodes to the T=20 timestamp.
**Refs:** crc-Store.md, R2883

## Test: Store.LastSurfaced session isolation
**Purpose:** Verify RM is per-session — surfacing to one session does not affect another.
**Input:** `MarkSurfaced("sess-A", 42)`. Then `LastSurfaced("sess-B", 42)`.
**Expected:** `sess-B` returns `(0, false, nil)`; `sess-A` returns present.
**Refs:** crc-Store.md, R2882

## Test: Store.LastSurfaced malformed value treated as absent
**Purpose:** Verify a corrupt RM value (not 8 bytes) reads as absent.
**Input:** Manually write `"RM" + "sess-A" + \x00 + varint(42)` with a 3-byte value. `LastSurfaced("sess-A", 42)`.
**Expected:** Returns `(0, false, nil)`. No error.
**Refs:** crc-Store.md, R2884

## Test: Store.PruneSurfaceCooldown drops expired entries
**Purpose:** Verify PruneSurfaceCooldown deletes RM entries older than the ttl across all sessions and keeps fresh ones.
**Input:** Write RM for `("sess-A", 1)` with a timestamp 48h in the past and `("sess-B", 2)` with NOW (manually set timestamps). Call `PruneSurfaceCooldown(24h)`.
**Expected:** Returns deleted count 1. `("sess-A", 1)` is gone; `("sess-B", 2)` survives.
**Refs:** crc-Store.md, R2885

## Test: ClearSurfaceCooldown / ClearAllSurfaceCooldown scope
**Purpose:** Verify the clean-command mechanism for RM — per-session and all-sessions wipes.
**Input:** MarkSurfaced for `("sess-A", 1)`, `("sess-A", 2)`, `("sess-B", 3)`. Call `ClearSurfaceCooldown("sess-A")`, then `ClearAllSurfaceCooldown()`.
**Expected:** The first returns 2 and leaves `("sess-B", 3)`; the second returns 1. These are the same Store methods `ark connections clean` calls in default scope (per-session list or all), mirroring RD — the `-session` flag restricts the RM wipe exactly as it restricts RD.
**Refs:** crc-Store.md, R2887
