# Test Design: Searcher
**Source:** crc-Searcher.md

## Test: merge combines scores
**Purpose:** Results from both engines merged by (fileid, chunknum)
**Input:** fts=[{id:1,chunk:0,score:0.8}], vec=[{id:1,chunk:0,score:0.6}]
**Expected:** merged result {id:1,chunk:0,score:combined(0.8,0.6)}
**Refs:** crc-Searcher.md, R49

## Test: merge handles disjoint results
**Purpose:** Files appearing in only one engine still appear in combined
**Input:** fts=[{id:1,chunk:0}], vec=[{id:2,chunk:0}]
**Expected:** both results appear (with zero score from missing engine)
**Refs:** crc-Searcher.md, R49

## Test: results sorted by combined score
**Purpose:** Output is sorted descending by combined score
**Input:** multiple results with various scores
**Expected:** highest combined score first
**Refs:** crc-Searcher.md, R50

## Test: intersect keeps only common results
**Purpose:** Split search with both flags intersects results
**Input:** fts=[{id:1},{id:2}], vec=[{id:2},{id:3}]
**Expected:** only {id:2} in output
**Refs:** crc-Searcher.md, R57

## Test: single flag returns single engine results
**Purpose:** --about alone returns EC (Librarian.SearchChunks) results without intersection
**Input:** about="concept", no contains/regex
**Expected:** EC vector results passed through directly
**Refs:** crc-Searcher.md, R56

## Test: contains and regex compose
**Purpose:** --contains drives FTS, --regex post-filters results
**Input:** contains="foo", regex="bar"
**Expected:** FTS results filtered by regex pattern
**Refs:** crc-Searcher.md, R55

## Test: k limit applied
**Purpose:** -k flag limits result count
**Input:** 50 results, k=10
**Expected:** only top 10 returned
**Refs:** crc-Searcher.md, R58

## Test: after filter
**Purpose:** --after filters by file timestamp
**Input:** results with various timestamps, after=yesterday
**Expected:** only results newer than yesterday
**Refs:** crc-Searcher.md, R60

## Test: post-filter funnel applies to a tag primary (Sleeping Sentry)
**Purpose:** R2951 — an index-lookup primary (-tag/-file-tag) routes its
candidate set through the same post-filter stack and default search_exclude
scope as a content-scan primary. Guards against the F2 bypass where
SearchTagChunks returned the tag set directly.
**Input:** three @guard chunks (keep.md/other.md/excluded.md); config
search_exclude=`**/excluded.md`; SearchTagChunks invoked with (a) `-files
'**/keep.md'`, (b) `-contains needle`, (c) no explicit filter.
**Expected:** (a) {keep.md}; (b) {keep.md}; (c) {keep.md, other.md}.
**Refs:** crc-Searcher.md, seq-search.md, R2951
**Code:** search_tag_funnel_test.go
