# Test Design: Chunk Retrieval
**Source:** crc-Searcher.md

## Test: FillChunks reads correct content
**Purpose:** Verify chunk text extracted using offsets
**Input:** File with 3 chunks indexed, search returns chunk 1 (middle)
**Expected:** Result.Text contains only the content of chunk 1
**Refs:** crc-Searcher.md, seq-search.md

## Test: FillFiles deduplicates
**Purpose:** Verify multiple chunk hits from one file → one file entry
**Input:** Search returns chunks 0, 2, 4 from same file (scores 0.9, 0.7, 0.5)
**Expected:** One result with full file content, score = 0.9 (best chunk)
**Refs:** crc-Searcher.md

## Test: FillFiles preserves multi-file results
**Purpose:** Verify dedup doesn't collapse across files
**Input:** Search returns chunk 0 from file A (score 0.8), chunk 1 from file B (score 0.6)
**Expected:** Two results, each with their file's full content
**Refs:** crc-Searcher.md

## Test: chunks and files mutually exclusive
**Purpose:** Verify validation rejects both flags
**Input:** SearchOpts with Chunks=true and Files=true
**Expected:** Error "—chunks and —files are mutually exclusive"
**Refs:** crc-Searcher.md

## Test: JSONL output format chunks
**Purpose:** Verify JSONL schema for --chunks
**Input:** Search with --chunks, one result
**Expected:** Single line of valid JSON with path, startLine, endLine, score, text fields
**Refs:** crc-Searcher.md, seq-search.md

## Test: JSONL output format files
**Purpose:** Verify JSONL schema for --files
**Input:** Search with --files, one result
**Expected:** Single line of valid JSON with path, score, text fields (no line range)
**Refs:** crc-Searcher.md, seq-search.md
