# Test Design: Curation
**Source:** crc-Curation.md

## Test: Load returns silently when curation.toml is missing
**Purpose:** Fresh installs (no on-disk state) start with an empty pinned slice without surfacing an error
**Input:** newCuration(dbPath) where dbPath/curation.toml does not exist; call Load()
**Expected:** pinnedSnapshot() returns []; no panic; no error log for ENOENT
**Refs:** crc-Curation.md, R2383

## Test: Load + save round-trips the pinned slice
**Purpose:** A saved state survives a process restart — the load reproduces the slice the save serialized
**Input:** instance A constructed with dbPath; pinned slice seeded with three entries (varying ChunkID/FileID/Path/PinnedAt); call save(); construct instance B over the same dbPath; call Load()
**Expected:** instance B's pinnedSnapshot() equals instance A's seed slice (order preserved, all fields equal)
**Refs:** crc-Curation.md, R2382, R2384

## Test: Load logs and starts empty on malformed TOML
**Purpose:** A corrupted curation.toml does not stop the server; the next mutation overwrites the bad file
**Input:** dbPath/curation.toml contains non-TOML bytes ("not toml at all"); call Load()
**Expected:** pinnedSnapshot() returns []; log carries the parse error; statePath unchanged so the next save can rewrite it
**Refs:** crc-Curation.md, R2383, R2385

## Test: Load logs and starts empty on unknown version
**Purpose:** Future schema changes can introduce a new version number; older binaries refuse to load it rather than misinterpreting fields
**Input:** dbPath/curation.toml contains `version = 99` with a valid [[pinned]] entry; call Load()
**Expected:** pinnedSnapshot() returns []; log mentions the unexpected version
**Refs:** crc-Curation.md, R2383

## Test: save is atomic — never leaves a partial file
**Purpose:** A crash mid-save must not corrupt an existing curation.toml; readers see either the old file or the new one
**Input:** instance with two entries seeded; save() succeeds; verify no curation.toml.tmp file remains in dbPath
**Expected:** dbPath contains curation.toml with the expected bytes; no leftover .tmp sibling
**Refs:** crc-Curation.md, R2384

## Test: empty dbPath disables persistence
**Purpose:** Tests and in-memory-only callers can construct a Curation without touching the filesystem
**Input:** newCuration("") followed by save()
**Expected:** no file written anywhere; save returns without error or log
**Refs:** crc-Curation.md, R2381

## Test: validatePins drops unresolvable and path-mismatched pins
**Purpose:** R3251 — reindexing can reassign chunk IDs, so a persisted pin can silently resolve to a different real chunk (the 2026-07-23 incident); validation after Load must drop exactly the bad pins and persist the pruned list
**Input:** three pins seeded (A resolves to its recorded path; B's resolver path differs; C's resolver returns !ok); validatePins with a fake resolver func
**Expected:** pinnedSnapshot() holds only A; curation.toml on disk holds only A (the prune was saved back)
**Refs:** crc-Curation.md, R3251

## Test: validatePins is a no-op when every pin validates
**Purpose:** R3251 — validation must not rewrite state when nothing is stale
**Input:** pins seeded in memory only (no prior save); validatePins with an all-ok resolver
**Expected:** slice unchanged; no curation.toml written (save runs only when something was dropped)
**Refs:** crc-Curation.md, R3251
