# Test Design: Content View rendering
**Source:** crc-Server.md, seq-content-fetching.md

Characterization tests for `handleContentView`'s render cases. Written to
pin the assembled HTML **before** the case-split refactor (#41 piece 4) so
the extraction is provably behavior-preserving, and kept afterwards as the
regression net the render path never had (design.md O63).

Harness is `setupContentView` (contentview_race_test.go): a live DB, a
source covering the test dir, and `{{.Content}}` stand-in templates, so a
test asserts exactly the HTML the render assembled with no shell noise.

Each test drives the real handler through `httptest` — the same door the
browser uses — rather than calling a renderer directly, so the dispatch
itself is under test.

**Not covered here** (needs fixtures disproportionate to the refactor,
tracked by design.md O63): PDF page aggregation (`renderPdfChunksByPage`)
and PDF single-chunk preview (`renderPdfPreview`) need a real PDF; the
chat-jsonl role-grouping loop needs chunks carrying `role` attrs, which
only the JSONL chunker produces.

## Test: markdown full-file renders one chunk div per chunk
**Purpose:** the AllChunks walk — the common `/content/` case. Each chunk
gets its own `<div class="ark-chunk">` carrying its range, its chunk ID,
and the file's ID, with goldmark-rendered content inside.
**Input:** an indexed multi-line `.md` file; `GET /content/<path>`.
**Expected:** one `ark-chunk` div per indexed chunk; every div carries a
non-empty `data-chunkid` and the same non-empty `data-fileid`; the markdown
is rendered (a heading emits `<h1>`, not literal `#`).
**Refs:** crc-Server.md, R1160-R1164, R2415, R2065

## Test: markdown single chunk honors ?range=
**Purpose:** the `isChunk` branch — an iframe preview of one chunk renders
that chunk alone, not the whole file.
**Input:** the same file; `GET /content/<path>?range=<first chunk range>`.
**Expected:** exactly one `ark-chunk` div, its `data-range` equal to the
requested range, and its `data-chunkid` matching the chunk at that
location; the body of a later chunk does not appear.
**Refs:** crc-Server.md, R1423-R1425, R2415

## Test: non-markdown chunked file escapes rather than renders
**Purpose:** the plain branch's chunk loop — the same div structure as
markdown but HTML-escaped, not passed through goldmark.
**Input:** an indexed `.txt` file whose text contains markdown syntax and
an HTML-special character; `GET /content/<path>`.
**Expected:** `ark-chunk` divs with chunk/file IDs as above; the markdown
syntax survives literally (no `<h1>`); the special character is escaped.
**Refs:** crc-Server.md, R1495-R1496, R1499

## Test: non-markdown single chunk is wrapped for the pin button
**Purpose:** the unchunked fallback with `?range=` — the curate-pin path
(R2417) still needs the wrapping div even when no chunk walk happens.
**Input:** the `.txt` file; `GET /content/<path>?range=<range>`.
**Expected:** one `ark-chunk` div wrapping the escaped chunk text, carrying
the requested range and the resolved chunk ID.
**Refs:** crc-Server.md, R2415, R2417

## Test: unindexed markdown falls back to a whole-file render
**Purpose:** the no-chunks fallback — a file readable in a source but not
in the index still renders, just without per-chunk structure.
**Input:** a `.md` file written into the source dir but never indexed;
`GET /content/<path>`.
**Expected:** the markdown is rendered (heading becomes `<h1>`) and no
`ark-chunk` div is emitted.
**Refs:** crc-Server.md, R1160-R1164
