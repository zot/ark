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

## Test: ParseExtTarget peels leading insight (R3050)
**Purpose:** Verify a leading reserved `insight: "..."` (no @) is peeled
  before the TARGET and excluded from routed tags
**Input:** `insight: "resembles the streaming note" %abc @topic: recall`
**Expected:** target `%abc`, tags `[{topic, recall}]` — insight not a routed tag
**Refs:** crc-Indexer.md

## Test: ParseExtTarget insight with embedded @/: (R3050)
**Purpose:** Verify a quoted insight holding `@` or `:` is not mis-split
**Input:** `insight: "see @foo: bar, ratio 3:1" %abc @topic: recall`
**Expected:** tags `[{topic, recall}]` — embedded `@foo:` stays inside the quote
**Refs:** crc-Indexer.md

## Test: ParseExtTarget spacey path (R3050)
**Purpose:** Verify a bare path with spaces is bounded at the first routed
  `@tag:` (the automated producer proposes on such files), bare and with
  leading insight
**Input:** `/home/my notes/file with space.md @topic: recall`; and
  `insight: "why" /home/my notes/x y.md @topic: recall`
**Expected:** target keeps the full spacey path; tags `[{topic, recall}]`
**Refs:** crc-Indexer.md

## Test: stripLeadingDateDisposition peel + guards (R3090, R3092, R3093)
**Purpose:** Verify a leading `YYYY-MM-DD` date and — only after a date — an
  internal/external disposition are peeled; committed values and date-shaped
  filenames pass through
**Input:** `2026-07-12 external insight: "why" notes/f.md @topic: x`;
  `2026-07-12 notes/f.md @topic:` (judgment, date only);
  `notes/f.md @topic: recall` (committed); `2026-07-12.md @topic: recall`
  (no space at position 10); `external-notes.md @topic: recall` (disposition
  word but no date)
**Expected:** date+disposition peeled → `insight: "why" notes/f.md @topic: x`;
  judgment → date only; committed / `2026-07-12.md` / `external-notes.md` unchanged
**Refs:** crc-Indexer.md

## Test: ParseExtTarget with date + disposition (R3090, R3092)
**Purpose:** Verify a dated candidate/judgment value parses to the same
  (TARGET, tags) as its bare committed form
**Input:** `2026-07-12 internal notes/f.md @topic: recall`;
  `2026-07-12 notes/f.md @topic:` (judgment)
**Expected:** target `notes/f.md`, tags `[{topic, recall}]` / `[{topic, ""}]`
**Refs:** crc-Indexer.md

## Test: candidateLine disposition + insight-first (R3051, R3053, R3092)
**Purpose:** Verify the canonical `@ext-candidate` line — disposition right
  after the marker, insight quoted before the TARGET, no @; tag-name-only when
  value empty; quotes escaped; empty disposition omitted
**Input:** candidateLine("%abc","topic","recall","it fits","external");
  candidateLine("%abc","topic","recall",`say "hi"`,"external");
  candidateLine("%abc","topic","recall","","")
**Expected:** `@ext-candidate: external insight: "it fits" %abc @topic: recall`;
  `@ext-candidate: external insight: "say \"hi\"" %abc @topic: recall`;
  `@ext-candidate: %abc @topic: recall` (empty disposition omitted)
**Refs:** crc-DB.md

## Test: accept transition drops insight (R3054)
**Purpose:** Verify accept removes the matching `@ext-candidate` span and
  re-emits `@ext`, dropping the insight
**Input:** candidate line with insight for (%abc, topic, recall) →
  applyExtMirrorEdit(remove, candidate) → upsertExtLine(add, committed)
**Expected:** `@ext: %abc @topic: recall` (candidate gone, insight dropped)
**Refs:** crc-DB.md

## Test: reject transition tag-name-only (R3055)
**Purpose:** Verify reject removes matching candidate span(s) and re-emits a
  single tag-name-only `@ext-judgment`, deduped across values
**Input:** two candidate lines for (%abc, topic) with different values →
  remove(candidate) → per-distinct-tag upsertExtLine(add, judgment, value="")
**Expected:** one `@ext-judgment: %abc @topic:` line
**Refs:** crc-DB.md
