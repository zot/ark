# Test Design: FileEncoding
**Source:** crc-FileEncoding.md, crc-Indexer.md, crc-Store.md, crc-Server.md, crc-DB.md

Every test opens a real database (`Init` + `Open`), so the chunkers are
registered as production registers them, decoding wrapper included.

## Test: the mark names the encoding, and text round-trips
**Purpose:** R3656, R3657, R3661 — `DecodeText` reads `FF FE` and `FE FF`, leaves UTF-8 (mark included) as is, survives a surrogate pair both ways, and reports an unpaired surrogate as not round-tripping
**Input:** UTF-8; UTF-8 with mark; UTF-16LE and BE of `hello 😀`; `FF FE 00 D8 61 00`
**Expected:** each encoding detected, `EncodeText(DecodeText(x)) == x`; the unpaired surrogate does not round-trip
**Refs:** crc-FileEncoding.md, R3656, R3657, R3661
**Code:** encoding_test.go
**Fire alarm:** DecodeText always reports a round trip
**Inject:** encoding.go:DecodeText
**Pulled:** 2026-10-27 (local) — rang: `an unpaired surrogate round-tripped`

## Test: a UTF-16 file is indexed as text
**Purpose:** R3657, R3660 — a UTF-16LE markdown file indexes: chunk content and ChunkText are decoded, chunk tags and a file-level `@tag:` definition are found, and its N record says utf-16le, writable
**Input:** `bubba.md` as UTF-16LE with `@topic:`, `@tag: quokka …` and a word
**Expected:** decoded chunks with no NUL bytes; `topic` in TagList; the `quokka` definition; N record {utf-16le, true}
**Refs:** crc-FileEncoding.md, crc-Indexer.md, R3657, R3660
**Code:** encoding_test.go
**Fire alarm:** register markdown without the decoding wrapper
**Inject:** db.go:OpenWithTimeout
**Pulled:** 2026-10-27 (local) — rang: every UTF-16 test, `chunk "1-5" contains invalid UTF-8`
**Fire alarm:** the post-store tag scan reads the raw bytes
**Inject:** indexer.go:afterAddStore
**Pulled:** 2026-10-27 (local) — rang: `@tag: quokka definition not indexed`. A first version asserted only a chunk tag, which comes from the decoded chunk callback, and stayed silent
**Fire alarm:** GetChunk passes UTF-16 through undecoded
**Inject:** encoding.go:decodingChunker.GetChunk
**Pulled:** 2026-10-27 (local) — rang: `chunk content is not the decoded text: "\xff\xfe#\x00 …"`

## Test: the N record follows the file
**Purpose:** R3660 — a re-index moves the record to the new id and leaves none on the old; removing the file removes it
**Input:** index, rewrite and Refresh, then Remove
**Expected:** record on the new id, none on the old, none after removal
**Refs:** crc-Indexer.md, crc-Store.md, R3660
**Code:** encoding_test.go
**Fire alarm:** skip the delete in the remove callback
**Inject:** indexer.go:removeCallback
**Pulled:** 2026-10-27 (local) — rang: `N record outlived its file`
**Fire alarm:** skip the old id's delete after a re-index
**Inject:** indexer.go:afterReindexStore
**Pulled:** 2026-10-27 (local) — rang: `N record left behind on the old id 1`

## Test: a grown UTF-16 file re-indexes whole
**Purpose:** R3658 — on both preparers (Refresh's prepareRefresh, the server's RefreshAsync and IndexPathsAsync via prepareScan) a grown UTF-16 file is re-indexed, not appended. The fixture has two sections so the last chunk starts mid-file: an append resumes from that chunk's UTF-8 offset in the raw bytes, a window of ASCII and NULs that is valid UTF-8, so it succeeds with corrupt chunks rather than failing into the re-index fallback
**Input:** a two-section UTF-16LE file, grown by a line; Refresh, and separately RefreshAsync after advancing the mtime
**Expected:** decoded chunks including the new line, no NULs
**Refs:** crc-Indexer.md, R3658
**Code:** encoding_test.go
**Fire alarm:** drop the gate in prepareRefresh
**Inject:** indexer.go:prepareRefresh
**Pulled:** 2026-10-27 (local) — rang: `grown file's chunks = ""`. With a one-section fixture it was silent: the append window then held the `FF FE` mark, failed as invalid UTF-8, and fell back to a re-index
**Fire alarm:** drop the gate in prepareScan
**Inject:** indexer.go:prepareScan
**Pulled:** 2026-10-27 (local) — rang: `grown file's chunks after an async refresh = ""`. Refresh never reaches prepareScan, so the async test exists for this gate

## Test: edits keep the file's encoding
**Purpose:** R3662 — ReplaceRegion, an editor save and a tag-block write leave the file UTF-16LE with its mark; EncodeForFile follows the file on disk
**Input:** a UTF-16LE file; ReplaceRegion at 0; POST /file/save; POST /tags/set
**Expected:** the file decodes as utf-16le, round-trips, and holds each edit
**Refs:** crc-FileEncoding.md, crc-DB.md, crc-Server.md, R3662
**Code:** encoding_test.go
**Fire alarm:** ReplaceRegion writes the text unencoded
**Inject:** db.go:ReplaceRegion
**Pulled:** 2026-10-27 (local) — rang: `after the edit: enc=utf-8`
**Fire alarm:** handleSave skips EncodeForFile
**Inject:** server.go:handleSave
**Pulled:** 2026-10-27 (local) — rang: `after save: enc=utf-8`
**Fire alarm:** handleSetTags writes the rendered block unencoded
**Inject:** server.go:handleSetTags
**Pulled:** 2026-10-27 (local) — rang: `after set-tags: enc=utf-8`

## Test: a file that does not round-trip is never written
**Purpose:** R3661 — a UTF-16 file with an unpaired surrogate indexes with writable=false, ReadEditableText refuses it, ReplaceRegion fails, and its bytes are untouched
**Input:** UTF-16LE text with `00 D8` mid-line
**Expected:** N {utf-16le, false}; errNotWritable; no change on disk
**Refs:** crc-FileEncoding.md, R3661
**Code:** encoding_test.go
**Fire alarm:** ReadEditableText ignores the round trip
**Inject:** encoding.go:ReadEditableText
**Pulled:** 2026-10-27 (local) — rang: `ReadEditableText err = <nil>, want errNotWritable`

## Test: the content routes serve text
**Purpose:** R3663, R3661 — /fetch and /content serve the decoded text, /raw the file's bytes, and a lossy file's view has no editor
**Input:** a UTF-16LE file and a lossy one, through a mux registered as production registers the routes
**Expected:** decoded text on /fetch and /content (hide=false), the mark on /raw, hide=true for the lossy file
**Refs:** crc-Server.md, R3661, R3663
**Code:** encoding_test.go
**Fire alarm:** handleContentFetch skips the decode
**Inject:** server.go:handleContentFetch
**Pulled:** 2026-10-27 (local) — rang: `/fetch = "{\"content\":\"��#\u0000 …`
**Fire alarm:** the view never hides the editor for a lossy file
**Inject:** server.go:newContentRender
**Pulled:** 2026-10-27 (local) — rang: `lossy /content = "hide=false …"`

## Test: an unwrapped chunker's failure names the encoding
**Purpose:** R3659 — explainEncoding adds the encoding and the chunker to a microfts2 invalid-UTF-8 error on a UTF-16 file, and leaves a UTF-8 file's error alone
**Input:** the microfts2 error text; UTF-16LE and UTF-8 data
**Expected:** `utf-16le` and `chat-jsonl chunker reads UTF-8 only`, wrapping the original; the UTF-8 case unchanged
**Refs:** crc-FileEncoding.md, R3659
**Code:** encoding_test.go
**Fire alarm:** return the error unchanged
**Inject:** encoding.go:explainEncoding
**Pulled:** 2026-10-27 (local) — rang: `explained = chunk "1-2" contains invalid UTF-8 in x.md`

## Test: every text chunker reads UTF-16
**Purpose:** R3664 — the `lines` strategy and the chat-jsonl chunker index a UTF-16 file as decoded text, and the bible chunker yields identical chunks from a UTF-16 copy of a book
**Input:** UTF-16LE `notes.txt` (lines) and `log.jsonl` (chat-jsonl) through AddFile; `bibleFixture` in both encodings through bibleChunksOf
**Expected:** decoded chunks with the fixture's word and no NULs; equal Content, Range and Locator chunk for chunk
**Refs:** crc-FileEncoding.md, crc-DB.md, crc-BibleChunker.md, R3664
**Code:** encoding_test.go
**Fire alarm:** register `lines` without decodingFunc
**Inject:** db.go:OpenWithTimeout
**Pulled:** 2026-10-27 (local) — rang: `notes.txt (lines): … chunk "1-1" contains invalid UTF-8`
**Fire alarm:** drop the decode from JSONLChunker.Chunks
**Inject:** db.go:JSONLChunker.Chunks
**Pulled:** 2026-10-27 (local) — rang: `log.jsonl (chat-jsonl) chunks = "\x00"`
**Fire alarm:** drop the decode from bibleChunker.Chunks
**Inject:** biblechunker.go:bibleChunker.Chunks
**Pulled:** 2026-10-27 (local) — rang: `UTF-16 copy gave 0 chunks, UTF-8 4`

Not separately tested: the write sites `writeInternalTag`, `RemoveInternalTag`, `rescheduleTag`, the Lua `setTags`, and the CLI's `ark tag set`, and the readers `readInboxFields`, `cmdTagGet`, `cmdTagCheck` and the Lua `readMessage`. Each makes the same ReadEditableText/ReadText and EncodeText calls the tested sites make.
