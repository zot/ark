# Test Design: Sweep
**Source:** crc-DB.md, seq-reconcile.md

## Test: sweep removes newly-excluded file
**Purpose:** A previously-indexed path that no longer classifies as Included is removed
**Input:** index contains /proj/foo.js; config changed to add exclude `**/*.js`; Sweep runs
**Expected:** /proj/foo.js no longer in fts.StaleFiles; tag values, V records, and any ext routings dropped via Indexer.RemoveFile
**Refs:** crc-DB.md, R2139, R2141

## Test: sweep removes file whose source was deleted
**Purpose:** Files that no longer have a claiming source are removed
**Input:** index contains /old-src/a.md; config no longer lists /old-src; Sweep runs
**Expected:** /old-src/a.md removed via Indexer.RemoveFile
**Refs:** crc-DB.md, R2140

## Test: sweep keeps still-included file
**Purpose:** Files that still classify as Included survive
**Input:** index contains /proj/notes.md; include patterns unchanged; Sweep runs
**Expected:** /proj/notes.md remains; no remove call issued for it
**Refs:** crc-DB.md, R2139

## Test: sweep removes file via the canonical removal path
**Purpose:** Sweep removal hits Indexer.RemoveFile so chunk refcount, tag values, ext routings are all cleaned
**Input:** spy on Indexer.RemoveFile; one file becomes Excluded
**Expected:** RemoveFile called exactly once with that path; not RemoveByPath bypassing tag cleanup
**Refs:** crc-DB.md, R2141

## Test: sweep runs every Reconcile
**Purpose:** Sweep is part of Reconcile, not gated to post-mutation
**Input:** simulate startup Reconcile (no config change); ensure Sweep step runs
**Expected:** Sweep invoked once per Reconcile cycle
**Refs:** crc-DB.md, seq-reconcile.md, R2142
