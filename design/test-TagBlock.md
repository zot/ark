# Test Design: TagBlock
**Source:** crc-TagBlock.md

## Test: Parse well-formed file
**Purpose:** Verify basic tag block parsing
**Input:** File with 3 tags, blank line, body text
**Expected:** 3 tags in order, bodyOffset points after blank line
**Refs:** crc-TagBlock.md, seq-message.md

## Test: Parse empty file
**Purpose:** Handle edge case of empty input
**Input:** Empty byte slice
**Expected:** Zero tags, bodyOffset 0
**Refs:** crc-TagBlock.md

## Test: Parse file with no tags
**Purpose:** Body starts on line 1
**Input:** `# Heading\n\nBody text`
**Expected:** Zero tags, bodyOffset 0 (entire file is body)
**Refs:** crc-TagBlock.md

## Test: Parse file with tags but no blank separator
**Purpose:** Tags run directly into body
**Input:** `@status: open\n# Heading`
**Expected:** 1 tag, bodyOffset points to `# Heading`
**Refs:** crc-TagBlock.md, R446

## Test: Set replaces existing tag
**Purpose:** In-place value replacement
**Input:** Parse `@status: open\n@issue: foo\n\nBody`, Set("status", "done")
**Expected:** Render produces `@status: done\n@issue: foo\n\nBody`
**Refs:** crc-TagBlock.md, R459

## Test: Set appends new tag
**Purpose:** New tag goes at end of block
**Input:** Parse `@status: open\n\nBody`, Set("priority", "high")
**Expected:** Render produces `@status: open\n@priority: high\n\nBody`
**Refs:** crc-TagBlock.md, R460

## Test: Set on tagless file inserts block
**Purpose:** Tags inserted before existing content
**Input:** Parse `# Heading\nBody`, Set("status", "open")
**Expected:** Render produces `@status: open\n\n# Heading\nBody`
**Refs:** crc-TagBlock.md, R463

## Test: Get returns value and found status
**Purpose:** Verify Get for present and absent tags
**Input:** Parse `@status: open\n@issue: foo\n\nBody`
**Expected:** Get("status") → ("open", true), Get("missing") → ("", false)
**Refs:** crc-TagBlock.md

## Test: Render preserves body exactly
**Purpose:** Body bytes are not modified
**Input:** Parse file with tags + body containing special chars, unicode
**Expected:** Render output body portion is byte-identical to input body
**Refs:** crc-TagBlock.md, R461

## Test: Validate detects blank line in tag block
**Purpose:** Catch split-chunk problem
**Input:** `@status: open\n\n@issue: foo\n\nBody`
**Expected:** Validate returns problem at line 2 mentioning blank line
**Refs:** crc-TagBlock.md, R473

## Test: Validate detects missing separator
**Purpose:** No blank line between tags and body
**Input:** `@status: open\n# Heading`
**Expected:** Validate returns problem about missing blank separator
**Refs:** crc-TagBlock.md, R474

## Test: Validate detects malformed tag
**Purpose:** Missing space after colon
**Input:** `@status:open\n\nBody`
**Expected:** Validate returns problem at line 1 about format
**Refs:** crc-TagBlock.md, R475

## Test: ScanBody finds stray tags
**Purpose:** Detect misplaced tag-like patterns in body
**Input:** `@status: open\n\nBody\n## Status: done\nmore`
**Expected:** ScanBody returns finding at the `## Status:` line
**Refs:** crc-TagBlock.md, R472

## Test: ScanBody finds @tag: in body
**Purpose:** Detect actual @tag: pattern in body
**Input:** `@status: open\n\n@priority: high\nBody`
**Expected:** ScanBody returns finding for `@priority:` in body
**Refs:** crc-TagBlock.md, R472

## Test: Multiple Set calls preserve order
**Purpose:** Existing tags keep position, new tags append
**Input:** Parse `@a: 1\n@b: 2\n@c: 3\n\nBody`, Set("b", "X"), Set("d", "4")
**Expected:** Render produces `@a: 1\n@b: X\n@c: 3\n@d: 4\n\nBody`
**Refs:** crc-TagBlock.md, R459, R460
