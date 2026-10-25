# Test Design: Refresh
**Source:** crc-Indexer.md

## Test: a forced refresh re-indexes an unchanged file and keeps its chunk ids
**Purpose:** R3568. `ark refresh --force` exists to re-chunk files whose content
has not changed (after a chunker's rules change), so it must reach the full
in-place reindex that a plain refresh skips for an unchanged file (R2864), and
unchanged chunk text must keep its chunk id there, which keeps its embedding and
everything that points at it (microfts2 R673). Bill's use, 2026-10-23: dropping
the harness chunks #158 stopped producing from an existing index without
re-embedding the corpus.
**Input:** a two-turn chat transcript indexed with `chat-jsonl`; a plain refresh;
then a forced refresh of the same unchanged file.
**Expected:** the plain refresh leaves the file id alone; the forced refresh
allocates a new file id (the full reindex ran) and every chunk keeps its id.
**Refs:** crc-Indexer.md, crc-DB.md
**Code:** refresh_force_test.go
**Input (revised):** a real DB (`Init` with a seeded `ark.toml` naming the temp
directory as a `chat-jsonl` source, then `Open`), since a forced scan runs only
for a file inside a source and through both actors.
**Fire alarm:** restore the store-time unchanged skip for forced scans; the file id is unchanged.
**Inject:** indexer.go:storeScan
**Pulled:** 2026-10-23 — rang: "forced refresh did not re-index (file id unchanged at 1)"
**Fire alarm:** dispatch the scan unforced; the file id is unchanged.
**Inject:** db.go:startFileScanWith
**Pulled:** 2026-10-23 — rang: "forced refresh did not re-index (file id unchanged at 1)"

## Test: a forced refresh re-chunks a grown file whole
**Purpose:** R3568. A live transcript has usually grown since it was indexed; the
append path would chunk only the new lines and keep the old chunks, which is what
`--force` exists to replace. A full reindex allocates a new file id; an append
keeps the old one.
**Input:** a one-turn transcript indexed, then a second turn appended, then forced.
**Expected:** a new file id and two chunks.
**Refs:** crc-Indexer.md
**Code:** refresh_force_test.go
**Fire alarm:** let a forced scan take the append branch; the file id stays the same.
**Inject:** indexer.go:prepareScan
**Pulled:** 2026-10-25 — rang: "a grown file was appended to, not re-chunked whole (file id still 1)"

## Test: a forced refresh keeps a missing file
**Purpose:** R45, R3568. The watcher's scan removes a file gone from disk; a
forced refresh must report it and keep it.
**Input:** a transcript indexed, deleted from disk, then forced.
**Expected:** still indexed.
**Refs:** crc-DB.md
**Code:** refresh_force_test.go
**Fire alarm:** let a forced scan remove a missing file; CheckFile reports it gone.
**Inject:** db.go:prepareFileScan
**Pulled:** 2026-10-25 — rang: "a forced refresh removed a missing file from the index"

## Test: a forced refresh needs a pattern
**Purpose:** R3569. Forcing the whole index is rarely meant, so `--force` without
a pattern is refused.
**Input:** `ForceRefresh(nil)`.
**Expected:** the needs-a-pattern refusal.
**Refs:** crc-DB.md, crc-CLI.md
**Code:** refresh_force_test.go
**Fire alarm:** drop the refusal; `ForceRefresh(nil)` returns nil.
**Inject:** db.go:ForceRefresh
**Pulled:** 2026-10-23 — rang: "ForceRefresh(nil) = <nil>, want the needs-a-pattern refusal"
