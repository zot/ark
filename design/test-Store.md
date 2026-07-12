# Test Design: Store
**Source:** crc-Store.md

## Test: add and list missing
**Purpose:** Missing file records round-trip through the index
**Input:** AddMissing(fileid=42, path="/foo/bar.md", lastSeen=now)
**Expected:** ListMissing returns record with matching fields
**Refs:** crc-Store.md, R104

## Test: remove missing
**Purpose:** RemoveMissing deletes the record
**Input:** AddMissing then RemoveMissing(fileid=42)
**Expected:** ListMissing returns empty
**Refs:** crc-Store.md, R104

## Test: add and list unresolved
**Purpose:** Unresolved file records round-trip
**Input:** AddUnresolved(path="/foo/mystery.dat", dir="/foo")
**Expected:** ListUnresolved returns record with path, firstSeen, dir
**Refs:** crc-Store.md, R105

## Test: clean unresolved removes gone files
**Purpose:** CleanUnresolved removes entries for files no longer on disk
**Input:** AddUnresolved for a file, delete the file, CleanUnresolved
**Expected:** ListUnresolved returns empty
**Refs:** crc-Store.md, R106

## Test: clean unresolved keeps existing files
**Purpose:** CleanUnresolved preserves entries for files still on disk
**Input:** AddUnresolved for an existing file, CleanUnresolved
**Expected:** ListUnresolved still contains the entry
**Refs:** crc-Store.md, R106

## Test: dismiss by pattern
**Purpose:** DismissByPattern removes matching missing records
**Input:** missing records for a.md, b.md, c.txt; dismiss pattern "*.md"
**Expected:** a.md and b.md removed, c.txt remains
**Refs:** crc-Store.md, R84

## Test: resolve by pattern
**Purpose:** ResolveByPattern removes matching unresolved records
**Input:** unresolved records for x.dat, y.dat, z.md; resolve "*.dat"
**Expected:** x.dat and y.dat removed, z.md remains
**Refs:** crc-Store.md, R87

## Test: settings round-trip
**Purpose:** per-field IGet/IPut preserve ark settings (replaces the
monolithic GetSettings/PutSettings blob)
**Input:** IPut dotfiles=true and a default_exclude I record
**Expected:** IGet returns matching values per field
**Refs:** crc-Store.md, R1571

## Test: AllTagsForFile unions and dedups across chunks
**Purpose:** AllTagsForFile collapses the per-chunk multiset to one
file-wide set — a (tag,value) in two chunks appears once, distinct
per-chunk pairs all appear, and ext-routed pairs surface
**Input:** fileID 1 owns chunks 100 and 101; inline `shared=s` on both,
`c100-only=a` on 100, `c101-only=b` on 101; an ext-routed `ext-tag=e`
onto 101
**Expected:** four pairs, each exactly once (shared deduped)
**Refs:** crc-Store.md, R3083

## Test: AllTagsForFile with no chunk resolver
**Purpose:** the Store-only path (SetChunkResolver never called) returns
nil rather than panicking
**Input:** a bare testStore, AllTagsForFile(1)
**Expected:** nil result, no error
**Refs:** crc-Store.md, R3083
