# Test Design: Stale chunks
**Source:** crc-DB.md, crc-Server.md, crc-Searcher.md, crc-Librarian.md, crc-CLI.md

Covers a chunk the index holds but the file's current chunker no longer
produces (design.md "Stale Chunks"). One fixture serves every test: three
one-line chunks, indexed by a line chunker that also stores chapter and
verse attrs, then the file is shortened on disk without re-indexing, so
chunks 2 and 3 come back Missing *with* stored attrs. The fixture checks
both facts itself. Without the attrs, a test that attrs do not leak passes
by construction, which is how the first draft of these tests passed.

## Test: read as absent
**Purpose:** a missing chunk's content and stored attrs are not used (R3571, R3574)
**Input:** the fixture
**Expected:** `ChunkMissing` true for chunk 3, false for chunk 1 and for an
unknown range; `ChunkAttrs` nil; `chunkForVerse` places no chapter-1 reference;
`chunkContent` not-found; the ext regex `^$` matches no stale chunk (a
positive control proves the target reaches the regex branch); `ChunkStats`
counts 1; `SuggestExtLocator` errors naming staleness; `FetchChunkContent`
(behind `ark chunks` and `POST /chunks`) returns the stale entries without
attrs, while the live one keeps them (the fixture check)
**Refs:** crc-DB.md
**Code:** stale_chunk_test.go
**Fire alarm:** each guard in turn: make `ChunkMissing` answer false; drop
the Missing check from `ChunkAttrs`, `chunkContent`, the ext anchor walk,
`ChunkStats`, `chunkForVerse`, `SuggestExtLocator`; keep the attrs in
`FetchChunkContent`. Each reds its own line.
**Inject:** db.go, locator.go
**Pulled:** 2026-10-24 — rang, all eight, each on its own assertion

## Test: placeholder
**Purpose:** one text, and the HTML form says the same thing (R3572)
**Input:** a plain path and one with a space
**Expected:** the repair command appears, quoted when the path needs it; the
HTML sets it in `<code>`
**Refs:** crc-DB.md, crc-Server.md
**Code:** stale_chunk_test.go

## Test: search fill drops
**Purpose:** a search hit whose text is unavailable is dropped (R3573)
**Input:** one live and one stale result
**Expected:** `FillChunks` returns only the live one
**Refs:** crc-Searcher.md
**Code:** stale_chunk_test.go
**Fire alarm:** return the unfiltered slice
**Inject:** search.go:FillChunksUsing
**Pulled:** 2026-10-24 — rang (FillChunks kept 2 results)

## Test: recall drops unavailable
**Purpose:** a recall hit whose content was asked for and cannot be read is dropped (R3573)
**Input:** a query vector and the stale chunk's vector beside it
**Expected:** recall without content surfaces the stale chunk (the fixture
check); with content it does not
**Refs:** crc-Librarian.md
**Code:** stale_chunk_test.go
**Fire alarm:** keep the candidate when `ChunkText` fails
**Inject:** recall.go:Recall
**Pulled:** 2026-10-24 — rang (recall with content returned the stale chunk)

## Test: content view
**Purpose:** a stale chunk keeps its div, holding the placeholder; a PDF page
draws no overlay for it (R3572, R3574)
**Input:** the fixture, rendered by `chunkedBody`, `markdownBody` (walk and
single-chunk) and `unchunkedBody` (single-chunk); one Missing PDF chunk with
a page size and rect
**Expected:** three chunk divs and two placeholders in each walk; the
single-chunk views show the placeholder; the PDF output omits the stale rect
**Refs:** crc-Server.md
**Code:** stale_chunk_test.go
**Fire alarm:** drop the Missing check from each of the five sites in turn
**Inject:** server.go (chunkedBody, markdownBody twice, unchunkedBody), search.go:renderPdfChunksByPage
**Pulled:** 2026-10-24 — rang, all five. The PDF alarm was silent on the
first pull: the test chunk had no page size, so no chunk reached the overlay
code at all. It rang once the page size was added.

## Test: embed action
**Purpose:** the embedding pass leaves a stale chunk queued, with no sentinel (R3575)
**Input:** a missing, an all-tag and a plain chunk
**Expected:** stale, tag-only and embed, in that order
**Refs:** crc-Librarian.md
**Code:** stale_chunk_test.go
**Fire alarm:** drop the Missing check from `embedChunkAction`
**Inject:** librarian.go:embedChunkAction
**Pulled:** 2026-10-24 — rang (a missing chunk got the tag-only action)

## Test: chunks --wrap
**Purpose:** under `--wrap` a stale chunk holds the placeholder; JSONL keeps
`"missing": true` (R3572)
**Input:** one live and one missing `ChunkResult`
**Expected:** one placeholder under `--wrap`; the flag and no placeholder in JSONL
**Refs:** crc-CLI.md
**Code:** cmd/ark/chunks_stale_test.go
**Fire alarm:** drop the Missing check in `emitChunkResult`
**Inject:** cmd/ark/main.go:emitChunkResult
**Pulled:** 2026-10-24 — rang (the stale element came out empty)

## Not covered
The `?range=` resolution in `handleContentView` (setting `stale` from
`ChunkMissing`) runs only in the HTTP handler; its two halves are covered
separately (ChunkMissing above, the stale render in "content view").
`handleContentFetch` (the JSON fetch) is deliberately unchanged: a stale
range still falls back to the whole file there, because placeholder text in
a payload an editor might save back is worse than the fallback.
