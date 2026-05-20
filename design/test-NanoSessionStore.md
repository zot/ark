# Test Design: NanoSessionStore
**Source:** crc-NanoSessionStore.md

## Test: missing file is not an error
**Purpose:** R2504 — LoadNanoSessions returns (nil, nil) when the file does
not exist
**Input:** path to a non-existent temp file
**Expected:** sessions == nil, err == nil
**Refs:** crc-NanoSessionStore.md

## Test: dedup by (label, cwd)
**Purpose:** R2505 — SaveNanoSession replaces a same-label-same-cwd entry
**Input:** save twice with identical label "foo" and cwd "/x", different
messages
**Expected:** file contains exactly one entry; messages match the second
save
**Refs:** crc-NanoSessionStore.md

## Test: 50-session cap
**Purpose:** R2506 — file is capped at most recent 50 sessions
**Input:** save 60 distinct sessions
**Expected:** LoadNanoSessions returns 50 entries; the first 10 are gone
**Refs:** crc-NanoSessionStore.md

## Test: file mode 0600
**Purpose:** R2533 — file is written with mode 0600
**Input:** SaveNanoSession to a fresh temp path
**Expected:** os.Stat(path).Mode().Perm() == 0600
**Refs:** crc-NanoSessionStore.md

## Test: NanoSessionsInCwd filter
**Purpose:** R2507 — only sessions matching cwd are returned, oldest first
**Input:** file containing sessions for cwds A, B, A, B, A
**Expected:** NanoSessionsInCwd(A) returns the three A entries in insertion
order
**Refs:** crc-NanoSessionStore.md
