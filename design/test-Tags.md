# Test Design: Tag Tracking
**Source:** crc-Store.md, crc-Indexer.md

## Test: ExtractTags basic
**Purpose:** Verify tag extraction regex finds @word: patterns
**Input:** Content with `@decision: chose LMDB\n@pattern: closure-actor\nnot a @tag without colon`
**Expected:** map{"decision": 1, "pattern": 1} — "tag" without colon not matched
**Refs:** crc-Indexer.md

## Test: ExtractTags multiple occurrences
**Purpose:** Verify counting of repeated tags
**Input:** Content with `@decision: first\nsome text\n@decision: second`
**Expected:** map{"decision": 2}
**Refs:** crc-Indexer.md

## Test: ExtractTags case and hyphens
**Purpose:** Verify tag names with hyphens and mixed case
**Input:** Content with `@my-tag: value\n@CamelTag: value`
**Expected:** map{"my-tag": 1, "cameltag": 1} — names stored lowercase
**Refs:** crc-Indexer.md

## Test: ExtractTags ignores emails and mentions
**Purpose:** Verify colon requirement disambiguates
**Input:** Content with `user@example.com and @mention without colon`
**Expected:** empty map — neither matches @word: pattern
**Refs:** crc-Indexer.md

## Test: Store UpdateTags and ListTags
**Purpose:** Verify T/F records written and totals computed
**Input:** UpdateTags(fileid=1, {"decision": 2, "pattern": 1}),
  UpdateTags(fileid=2, {"decision": 1})
**Expected:** ListTags returns {"decision": 3, "pattern": 1}
**Refs:** crc-Store.md

## Test: Store UpdateTags replaces
**Purpose:** Verify refresh replaces old counts
**Input:** UpdateTags(fileid=1, {"decision": 2}), then
  UpdateTags(fileid=1, {"pattern": 1})
**Expected:** ListTags returns {"pattern": 1} — decision gone
**Refs:** crc-Store.md

## Test: Store RemoveTags
**Purpose:** Verify tag cleanup on file removal
**Input:** UpdateTags(fileid=1, {"decision": 2}),
  UpdateTags(fileid=2, {"decision": 1}), RemoveTags(fileid=1)
**Expected:** ListTags returns {"decision": 1}
**Refs:** crc-Store.md

## Test: Store TagFiles
**Purpose:** Verify per-file tag lookup
**Input:** UpdateTags(fileid=1, {"decision": 2}),
  UpdateTags(fileid=2, {"decision": 1, "pattern": 3})
**Expected:** TagFiles(["decision"]) returns [{fileid=1, count=2}, {fileid=2, count=1}]
**Refs:** crc-Store.md
