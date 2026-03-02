# Test Design: Store
**Source:** crc-Store.md

## Test: add and list missing
**Purpose:** Missing file records round-trip through LMDB
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
**Purpose:** GetSettings/PutSettings preserve ark settings
**Input:** PutSettings with dotfiles=true, sourceConfig ref
**Expected:** GetSettings returns matching values
**Refs:** crc-Store.md, R107
