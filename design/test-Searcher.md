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
**Purpose:** --about alone returns microvec results without intersection
**Input:** about="concept", no contains/regex
**Expected:** microvec results passed through directly
**Refs:** crc-Searcher.md, R56

## Test: contains and regex mutually exclusive
**Purpose:** Error when both --contains and --regex provided
**Input:** contains="foo", regex="bar"
**Expected:** error returned
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
