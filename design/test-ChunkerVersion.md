# Test Design: ChunkerVersion
**Source:** crc-Indexer.md, crc-FileScanActor.md
**Requirements:** R3597-3600, R3611

A file chunked under one chunker version and then left stale by a version bump
(`versionedLineChunker` at 0, then 1) must be re-chunked by every refresh path.

## Test: RefreshStale re-chunks a byte-identical version-stale file
**Purpose:** the byte-identical skip (R2864) never applies to a file microfts2 judged stale (R3597)
**Input:** file indexed under version 0, chunker bumped to 1, bytes unchanged; `RefreshStale(nil, nil)`
**Expected:** `CheckFile` reports fresh and the F record carries version 1
**Refs:** crc-Indexer.md
**Code:** chunker_version_test.go
**Fire alarm:** stop clearing `prep.unchanged` for a stale file; red is "file is stale after refresh, want fresh" and "file chunker version = 0, want 1"
**Inject:** indexer.go:prepareStaleRefresh
**Pulled:** 2026-10-25 — rang ("file is stale after refresh, want fresh"; version 0, want 1)

## Test: a version-stale append re-chunks the whole file
**Purpose:** an append microfts2 refuses with `ErrChunkerVersionStale` falls through to a full reindex (R3598)
**Input:** file indexed under version 0, chunker bumped to 1, a line appended; `RefreshFile`
**Expected:** no error; the file is fresh at version 1
**Refs:** crc-Indexer.md
**Code:** chunker_version_test.go
**Fire alarm:** make the executeRefresh append branch return its error instead of falling through to `executeFullRefresh`; red is "refresh of a version-stale grown file: ... chunker version"
**Inject:** indexer.go:executeRefresh
**Pulled:** 2026-10-25 — rang (RefreshFile returned microfts2's chunker-version refusal)

## Test: a version-stale store refusal re-emits a forced scan
**Purpose:** the scan path's retry cannot meet the same refusal (R3599)
**Input:** `storeResultAction` with a wrapped `ErrChunkerVersionStale`, and with a plain error
**Expected:** the refusal yields retire, reEmit and force; a plain failure does not force
**Refs:** crc-FileScanActor.md, seq-file-scan-actor.md
**Code:** chunker_version_test.go
**Fire alarm:** stop setting `force` in storeResultAction; red is "version-stale refusal: got {... force:false}"
**Inject:** db.go:storeResultAction
**Pulled:** 2026-10-25 — rang ("got {retire:true reEmit:true force:false}")

## Test: a file scan forces a byte-identical version-stale file
**Purpose:** neither append detection nor the store-time unchanged escape (R3423) passes a version-stale file by (R3600)
**Input:** file indexed under version 0, chunker bumped to 1, bytes unchanged; `prepareScan` with stale=true, then `storeScan`
**Expected:** the file is fresh at version 1
**Refs:** crc-FileScanActor.md
**Code:** chunker_version_test.go
**Fire alarm:** drop the R3600 block that forces the scan; red is "file is stale after refresh, want fresh" — the escape skipped the store
**Inject:** indexer.go:prepareScan
**Pulled:** 2026-10-25 — rang ("file is stale after refresh, want fresh"; version 0, want 1)

## Test: chat sub-chunks keep their voice
**Purpose:** a multi-paragraph thinking block's middle sub-chunks stay marked, and text after it stays unmarked (R3611)
**Input:** `<thinking>` spanning three paragraphs, then a plain paragraph; `chatSubchunks`
**Expected:** three sub-chunks each wrapped flush in `<thinking>…</thinking>`, then `plain answer`
**Refs:** crc-Librarian.md
**Code:** chunker_version_test.go
**Fire alarm:** stop reopening the span at a sub-chunk's start; red is the middle sub-chunk coming back as bare `second part`
**Inject:** recall.go:chatSubchunks
**Pulled:** 2026-10-25 — rang (sub-chunks `["<thinking>first part</thinking>" "second part" "third part</thinking>" ...]`)
