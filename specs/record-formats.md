# Record Formats

Canonical reference for ark's index record layouts.

This document describes the **current** state of the database. Migration
specs in `specs/migrations/` describe how to *get to* the current state;
this document describes what the current state *is*.

Language: Go. Environment: ark bucket named `ark`. The
microfts2 bucket has its own record formats documented in
microfts2's corpus and is not duplicated here.

## Bucket

A single `*bbolt.DB` hosts two named buckets:

- **`ark`** — primary store: tags, embeddings, file state, config,
  page content. All records described in this document live here.
- **`microfts2`** — full-text search index: chunks, files, trigrams,
  tokens, hashes. Documented in microfts2's repository.

## Prefix Inventory

Every key starts with a one- or two-byte prefix that identifies the
record class. `ark status -db` shows these records so when we add or
change them, we need to update the CLI code so it's up-to-date.

| Prefix | Record class        | Key shape                                         | Value                                   |
|--------|---------------------|---------------------------------------------------|-----------------------------------------|
| `B`    | Bible book index    | `B` + source + `\x00` + book + `\x00` + chapter    | file path (string)                      |
| `D`    | Tag definition      | `D` + tagname + fileid:8                          | description bytes                       |
| `E:`   | Error condition     | `E:` + name                                       | JSON                                    |
| `EC`   | Chunk embedding     | `EC` + chunkID varint                             | float32 vector (3072 bytes)             |
| `ED`   | Tag-def embedding   | `ED` + tagname + fileid:8                         | float32 vector (3072 bytes)             |
| `EF`   | File centroid       | `EF` + fileID varint                              | float32 sum (3072 bytes) + uint32 count |
| `EV`   | Tag-value embedding | `EV` + tvid varint                                | float32 vector (3072 bytes)             |
| `F`    | Chunk-tag           | `F` + chunkid varint + tagname                    | uint32 count + optional tvids           |
| `HC`   | Hot correlations    | `HC` + tagname + chunkid:8                        | float64 score (8 bytes)                 |
| `I`    | Info / settings     | `I` + name                                        | string or JSON or counter (8 bytes)     |
| `M`    | Missing file        | `M` + fileid:8                                    | JSON                                    |
| `PC`   | Page content        | `PC` + fileID varint + page varint                | zlib-compressed blob                    |
| `RC`   | Recall Candidate    | `RC` + source_tvid varint + target_chunkid varint | varint tally (materialized from `@ext-candidate` `@count`) |
| `RD`   | Recall discussed-tag| `RD` + session-bytes + `\x00` + tagname + `\x00` + value | 8-byte big-endian unix nanos     |
| `RF`   | Recall Freshness (dormant, #36) | `RF` + chunkid varint                 | varint uint64 (max S-over-ED at last derivation) — no writer after #36 |
| `RJ`   | Recall Judgment     | `RJ` + source_tvid varint + target_chunkid varint | signed-varint score (from `@ext-judgment` `@count`) + 8-byte big-endian unix nanos |
| `RM`   | Recall surface-cooldown | `RM` + session-bytes + `\x00` + chunkid varint | 8-byte big-endian unix nanos (last surfaced) |
| `S`    | Freshness stamp     | `S` + original-prefix + original-key              | varint uint64 (txn serial)              |
| `T`    | Tag total           | `T` + tagname                                     | uint32 count + optional vector          |
| `U`    | Unresolved file     | `U` + path                                        | JSON                                    |
| `V`    | Tag value           | `V` + tag + `\x00` + value + `\x00` + tvid varint | packed chunkid varints (multi-set)      |
| `X`    | @ext routing        | `X` + tvid_ext varint + target_chunkid varint     | packed routed_tvid varints              |

Encoding conventions used throughout:
- **fileid:8** — 8-byte big-endian `uint64`.
- **varint** — unsigned LEB128 (`encoding/binary.Uvarint`).
- **Vector size** — 768 dimensions × 4 bytes = 3072 bytes. Tied to
  the `nomic-embed-text-v1.5` model dimensionality; future model
  changes update this constant.

## Tag Records

### T — Tag totals (with optional name embedding)

- **Key:** `T` + tagname (raw bytes, no separator — tag names match
  `[\w][\w-]*` and never contain control bytes).
- **Value:** `[count: uint32 big-endian (4 bytes)] [optional float32 vector (3072 bytes)]`.
- **Semantic:** count = number of (chunk, tag) pairs in F records.
  A file with three chunks all tagging `@food: hamburger` contributes
  three (three F records, one per chunkid); a chunk shared across
  two files contributes one. The optional trailing vector is the
  embedding of the tag name (with hyphens converted to spaces, e.g.
  `design-decision` → `design decision`); written by
  `WriteTagNameEmbedding`. A T record with `len(value) == 4` has no
  embedding; `len(value) == 4+3072` has one.
- **Lifecycle:** `adjustTagTotal` increments by 1 for each new
  (chunkid, tag) pair and decrements by 1 when an orphaned chunkid
  drops its F record. When the count would reach zero, the entire
  record is deleted (embedding goes with it).
- **Query-time augmentation:** `Store.TagCounts` returns
  `T[tag] + ExtMap.VirtualTagCount[tag]`. Ext-routed
  contributions don't write F records (target's F is inline-only),
  so the indexed T count doesn't see them; the in-memory virtual count
  fills the gap. See "X — @ext routing" below.

### F — Per-(chunk, tag) records (with optional tvid trailer)

- **Key:** `F` + chunkid (varint) + tagname.
- **Value:** `[count: uint32 big-endian (4 bytes)] [optional packed tvid varints]`.
- **Semantic:** one record per (chunkid, tag) pair. Count = number
  of occurrences of the tag in that chunk's content. The tvid
  trailer lists every (tag, value) pair the chunk contributed,
  enabling targeted V-record cleanup when a chunkid is orphaned.
- **Lifecycle:** F records are written together with V records by
  `UpdateTagValues`/`AppendTagValues` (chunkid-keyed). The cleanup
  path runs via microfts2's orphan-chunkid callback, which calls
  `Store.RemoveTagValuesInTxn(chunkID)`: scan `F[chunkid]`, drop the
  chunkid from each tvid's V record, delete F records, decrement T.

### V — Tag value index

- **Key:** `V` + tagname + `\x00` + value + `\x00` + tvid varint.
- **Value:** packed chunkid varints (LEB128). **Multi-set, not set.**
- **Semantic:** one record per unique (tag, value) pair. The tvid
  is a sequential numeric identifier for the (tag, value) — stable
  across re-index, used as the join key for tag-value embeddings
  (EV records). The value lists chunkids that carry this
  (tag, value); count = number of varints in the value. File-level
  callers resolve chunkids → fileids via microfts2 `FilesForChunk`
  (or `ReadCRecord`) and dedupe.
- **Multi-set semantics:** `addChunkIDToVRecord` does not dedup —
  every contribution writes its own varint entry. A chunk that has
  `@food: hamburger` inline AND is ext-routed `@food: hamburger`
  from one source has two entries in
  `V[food][hamburger][tvid_food]`. Search-side result sets coalesce,
  so duplicates are invisible to callers; the multiplicity exists
  so that removal of one contribution doesn't strip valid
  contributions from others. Inline cleanup uses `removeVarint`
  (remove all occurrences for a given chunkid+tvid pair, scoped by
  F-trail walk); ext cleanup uses `removeOneVarint` (remove first
  occurrence) so each X-record's contribution is independently
  reversible.
- **Forward lookup** (find values for a tag): prefix scan
  `V` + tagname + `\x00` returns one record per value, with tvid in
  the trailing bytes of each key.
- **Filtered scan** (values matching prefix): prefix scan
  `V` + tagname + `\x00` + valuePrefix.
- **(tag, value) lookup**: prefix scan `V` + tagname + `\x00` +
  value + `\x00` returns the single record for that pair.
- **Reverse lookup** (chunkid/tvid resolution from key): `parseVKey`
  splits on the two `\x00` separators and decodes the trailing
  varint.
- **Legacy compatibility:** `parseVKey` accepts pre-tvid keys
  (no trailing varint after the second `\x00`) and returns tvid=0;
  writers always emit the tvid suffix.

### X — @ext routing

- **Key:** `X` + tvid_ext varint + target_chunkid varint, where
  tvid_ext is the tvid of the source's `@ext` tag and
  target_chunkid is one of the chunks the @ext routing applied to.
- **Value:** packed routed_tvid varints — the tvids of the routed
  tags (the `@tag1: v1 @tag2: v2 …` chain inside the @ext value)
  that this routing contributed to the target's V records.
- **Semantic:** durable provenance for `@ext: TARGET @tag: v …`.
  Source files contribute @ext-routed entries to a target chunk's
  V records via multi-set V append (one entry per routed tag per
  target chunk). The X record records the source-side tvid and the
  routed tvids it added so cleanup is precise: when the source
  chunk is orphaned, walk `X[tvid_ext]`, strike each
  `target_chunkid` from the named routed tag's V record (multi-set
  remove first occurrence), then drop the X record.
- **Chunkid-keyed (not fileid-keyed):** the X key holds a
  target_chunkid so an offline edit across an ark restart still
  resolves. If ark stops, the user edits the target file (deleting
  an `@id` line, say), and ark restarts → reindex → target chunk
  orphans, the startup scan of X records populates
  `chunkToTargets[orphan_chunkid]` so the orphan callback can find
  the routings to clean up. Fileid-keyed X cannot do this — the
  post-edit re-resolution would return empty and the stale V entry
  would have no discovery path.
- **Forward lookup** (all routings for one source @ext): prefix
  scan `X` + tvid_ext varint returns one record per target chunk.
- **Startup rebuild:** `ScanAllExtRecords` iterates every X record
  to repopulate the in-memory ExtMap (six maps + virtualTagCount).
  Bounded by total routing count.
- **Lifecycle:** written by `ExtMap.applyIndexExt` at index time
  and `ExtMap.applyReresolve` on target reindex; deleted by
  `ExtMap.CleanupSource` (source chunk orphaned) and
  `applyReresolve` Removes (target chunkid no longer matches the
  spec). See `specs/at-ext-storage.md` for the canonical
  re-resolution flow and `design/seq-ext-routing.md` for the
  sequence diagram.
- **What lives in memory, not the index:** the ExtMap's six
  in-memory maps (`targetToChunk`, `chunkToTargets`,
  `fileidToTvids`, `extByAnchor`, `unresolvedTargets`,
  `virtualTagCount`) are derived from the X records and rebuilt at
  startup. Only the X records are persistent.

### D — Tag definitions

- **Key:** `D` + tagname + 8-byte big-endian fileid.
- **Value:** description bytes (raw string).
- **Semantic:** one record per (tag, file) pair where the file
  defines the tag. A file defining multiple tags produces multiple
  D records. Tag definitions live in `~/.ark/tags.md` and any
  indexed file matching the `@tag: name -- description` pattern.
- **Lifecycle:** removed and re-extracted on file re-index, same
  as F records.
- **Reverse lookup** (definitions for a tag): prefix scan
  `D` + tagname returns one record per defining file.
- **Reverse lookup** (definitions by file): walk the D prefix
  matching the trailing 8-byte fileid (used by `UpdateTagDefs`).

## Embedding Records

### EV — Tag-value compound embeddings

- **Key:** `EV` + tvid varint.
- **Value:** float32 vector (3072 bytes for nomic-768).
- **Semantic:** one embedding per unique (tag, value) compound.
  Embedded text is `"tagname: value"` with hyphens converted to
  spaces in the tag name. Used for spectral hypergraph search
  (semantic similarity over tag-value space).
- **Join:** EV is keyed by tvid; tvid lives in the V record key
  suffix. To resolve "what tag-value does this EV belong to,"
  scan V records for the matching tvid in the key trailer.

### EC — Chunk content embeddings

- **Key:** `EC` + chunkID varint.
- **Value:** float32 vector (3072 bytes for nomic-768).
- **Semantic:** one embedding per unique chunk content. microfts2
  deduplicates chunk content — same text gets one C record with a
  unique chunkID, shared across files — so EC has one record per
  dedup'd content, not per file occurrence.
- **Resolution:** `chunkID → []fileid` via microfts2's public
  `FilesForChunk` API.
- **Lifecycle:** orphan EC = chunkID with no C record in microfts2.
  `RemoveFileWithCallback` and `ReindexWithCallback` deliver
  orphaned chunkIDs that ark deletes inside the same
  transaction. New chunkIDs (from re-index) are picked up by the
  next `BatchEmbedChunks` pass; EC writes happen out of the actor
  transaction (GPU compute mustn't block the actor).

### ED — Tag-definition embeddings

- **Key:** `ED` + tagname + 8-byte big-endian fileid. Mirrors D
  exactly — same one-record-per-(tag, file) shape.
- **Value:** float32 vector (3072 bytes for nomic-768).
- **Semantic:** one embedding per tag-definition (tag, file) pair.
  Embedded text is the description alone (no tag name) — the
  query direction is chunk → tag, so the name's lexical surface
  would bias the vector against meaning. Used for chunk →
  tag-name retrieval: score a chunk's EC vector against ED
  records to surface tag names whose definitions describe the
  chunk.
- **Lifecycle:** ED is dropped alongside D in
  `Store.UpdateTagDefs` (replace path) and
  `Store.RemoveTagDefs` (file removal). New ED records are written
  by the next batch-embed pass via `Store.WriteTagDefEmbedding`,
  driven by `Store.MissingTagDefEmbeddings`. Same drop path as T
  and EV on `tag_model` change (`Store.DropEmbeddings`) and on
  `ark rebuild`.
- **Reverse lookup** (embeddings for a tag): prefix scan
  `ED` + tagname returns one record per defining file.

### EF — File centroids

- **Key:** `EF` + fileID varint.
- **Value:** float32 running sum (3072 bytes) + uint32 count.
- **Semantic:** centroid = sum / count. Used for about-mode search
  filtering (brute-force cosine scan against file centroids).
- **Computation:** read the file's microfts2 F-record chunk list →
  look up `EC[chunkID]` for each → average the vectors. Stored as
  sum + count for O(1) incremental updates (`add: sum += vec; n++`,
  `remove: sum -= vec; n--`). Recomputed from scratch on full
  re-index.
- **Lifecycle:** deleted with its file via callback. Recomputed
  after `BatchEmbedChunks` processes new embeddings for the file.

## Freshness Substrate (S)

### S — Per-record txn serial stamp

- **Key:** `S` + original-prefix-bytes + original-key. Examples:
  `ST<tagname>`, `SEV<tvid-varint>`, `SEC<chunkID-varint>`,
  `SED<tagname><fileid:8>`, `SHC<tagname><chunkid:8>` (any
  stamped prefix gets a parallel `S<prefix>` entry).
- **Value:** varint-encoded uint64 — the txn serial allocated to
  the transaction that wrote the stamped record. Multiple
  records written in one txn share one serial; later txns have
  strictly greater serials.
- **Source counter:** `I:serial` (varint uint64). Maintained
  explicitly so the counter sits in the live B-tree and is
  preserved across compactions.
- **Semantic:** monotonic freshness stamp. "Records that moved
  together carry the same mark." Used by derived caches (HC,
  future) to identify which records changed since a bookmark —
  `RecordSerial(prefix, key)` and
  `WalkRecordsSinceSerial(prefix, since, fn)`.
- **Lifecycle:** written via `stampWrite` / `stampWriteWith`
  alongside the original record's `b.Put`, inside the same
  txn (atomic). Deleted alongside the original record
  (`DeleteChunkEmbedding`, `UpdateTagDefs` def-replacement,
  `DropEmbeddings` model swap, `DropChunkEmbeddings` rebuild).
  No tombstones — `RecordSerial` returns `found=false` after
  delete.
- **Currently stamped prefixes:** T, EV, EC, ED, HC. Future
  stamped prefixes (F, X, schedule, etc.) adopt the substrate
  generically — no per-prefix code beyond the `stampWrite` call.
- **Prefix-byte note:** `S` is a single byte and disjoint from
  every other allocated prefix's first byte. Variable-length
  tagname suffixes could otherwise collide (e.g. a `T` record
  with a tagname starting with `S` would collide with `TS*`);
  `S` was chosen specifically to avoid that.

Full design: `specs/vector-freshness.md`.

## Derived Caches (HC)

### HC — Hot correlations top-K cache

- **Key:** `HC` + tagname (raw bytes) + chunkid:8 (big-endian).
  Same variable-length tagname prefix as T / D / ED records.
- **Value:** float64 score (8 bytes, IEEE 754). No version
  metadata embedded — freshness lives in the S substrate.
- **Semantic:** per-tag top-K best-scoring chunks. One record
  per (tag, chunkid) in the bucket. Top-K bound is enforced by
  the sweep, not by storage. At current corpus
  (~105 tags × top-K=20), the cache holds ~2100 entries.
- **Freshness:** HC writes are stamped via the S substrate
  (`SHC<tagname><chunkid:8>`). A read considers an HC entry
  fresh iff:
  - `RecordSerial(HC, key) ≥ RecordSerial(EC, chunkid)`, and
  - `RecordSerial(HC, key) ≥ max RecordSerial(ED, tagname || fileid)`
    across all of the tag's def files.

  The HC's own stamp is its alibi — proof of when it was
  written. Stale entries are filtered at read time
  (`TopKChunksForTag`); the next sweep refreshes them.
- **Lifecycle:** written by `Librarian.SweepHotCorrelations`.
  Phase 3 of the sweep rebuilds a tag's full top-K
  (`Store.ReplaceHotCorrelations`); phase 4 displaces individual
  chunks against unchanged tags. Per-tag write transactions for
  crash safety. Dropped wholesale by `ark rebuild` and by
  `DropEmbeddings` (sweep bookmark `I:hcsweep` reset alongside).
- **Sweep bookmark:** `I:hcsweep` (varint uint64) — the last
  successful sweep's high-water serial. Zero means from-scratch
  on next run.

Full design: `specs/hot-correlations.md`. Alibi-stamp pattern:
`~/.claude/personal/patterns/alibi-stamp.md`.

## Bible Book Index (B)

### B — book/chapter → file

- **Key:** `B` + source + `\x00` + book + `\x00` + chapter. The source is
  the source's directory; the book is the canonical name (the epub filename
  token with hyphens turned to spaces — `1 Samuel`, `Psalm` singular); the
  chapter is the decimal chapter number. **NUL bytes** delimit the
  variable-length fields — a literal `0` would be ambiguous against the digits
  in a chapter or a path.
- **Value:** the file path holding that book's chapter (string).
- **Semantic:** resolves a friendly virtual address
  `<source>/BIBLE/<Book>:chapter.verse` to the real `*.text.xhtml` file. The
  source is in the key so two scripture sources cannot collide on the same book
  and chapter. **Exact-key lookup** — chapters do not span files, so no range
  scan is needed. One record per (source, book, chapter); ~1,189 for a full
  bible.
- **Lifecycle:** written by the BibleChunker at index time through the write
  actor, one record per chapter of each indexed `*.text.xhtml`. Read by
  `DB.lookupBookFile` during `@ext` target resolution. Dropped wholesale by
  `ark rebuild`.

Full design: `specs/bible-chunker.md` §Addressing; `design/crc-BibleChunker.md`,
`design/crc-DB.md`.

## Configuration Records (I)

- **Key:** `I` + name (raw bytes).
- **Value:** string, JSON, or 8-byte big-endian uint64 counter,
  depending on the named field.
- **Pattern:** one record per named field. Multiple distinct keys,
  not one big JSON blob.
- **Access:** `Store.IGet(name)`, `Store.IPut(name, value)` for
  string/JSON; `Store.IGetCounter(name)`, `Store.IPutCounter(name, n)`
  for varint counters.

### Config fields

These mirror `Config` struct fields, written by `WriteConfig`
during `ark init` and by config-mutating commands.

| Name               | Encoding                     | Source field             |
|--------------------|------------------------------|--------------------------|
| `dotfiles`         | bool→string ("true"/"false") | `Config.Dotfiles`        |
| `case_insensitive` | bool→string                  | `Config.CaseInsensitive` |
| `embed_cmd`        | string                       | `Config.EmbedCmd`        |
| `query_cmd`        | string                       | `Config.QueryCmd`        |
| `tag_model`        | string (GGUF filename)       | `Config.Embedding.Model` |
| `global_include`   | JSON array                   | `Config.GlobalInclude`   |
| `global_exclude`   | JSON array                   | `Config.GlobalExclude`   |
| `strategies`       | JSON map                     | `Config.Strategies`      |
| `sources`          | JSON array                   | `Config.Sources`         |
| `chunkers`         | JSON array                   | `Config.Chunkers`        |
| `session_ttl`      | string                       | `Config.SessionTTL`      |
| `search_exclude`   | JSON array                   | `Config.SearchExclude`   |
| `embed_tiers`      | JSON array                   | `Config.Embedding.Tiers` |
| `schedule`         | JSON                         | `Config.Schedule`        |
| `schedule_config`  | JSON                         | `Config.ScheduleConfig`  |

### Operational fields

| Name | Encoding | Purpose |
|------|----------|---------|
| `next_tvid` | uint64 counter | Tag-value-id allocation (V record tvid suffix, EV record key). Incremented when a new (tag, value) pair is first indexed. |
| `serial` | varint uint64 | Per-txn monotonic serial counter for the S freshness substrate. Allocated once per write txn that stamps records; preserved across compaction. |
| `hcsweep` | varint uint64 | High-water serial of the last successful `SweepHotCorrelations` run. Zero means from-scratch on next sweep; reset by `ark rebuild` and by `DropEmbeddings` (model swap). |

### Schema markers

| Name | Current value | Semantic |
|------|---------------|----------|
| `ec_version` | `"2"` | EC record schema version. On `DB.Open`, mismatch triggers drop of all EC + EF records (`db.go:286`). New ones regenerate during the next batch-embed pass; tag search degrades gracefully in the interim. |

## E: — Persistent error conditions

- **Key:** `E:` + name. The `:` is part of the prefix, not a
  separator — it disambiguates error records from the `EV`, `EC`,
  and `EF` embedding records that all start with byte `E`. A scan
  for `E:` reaches only error records; a scan for `E` would
  collide with the embeddings.
- **Value:** JSON describing the condition.
- **Semantic:** persistent across restarts; surfaced in
  `ark status` warnings. A condition may only be recorded if it is
  re-derivable from durable state that witnesses it, so records are
  reconciled on every config load and never dismissed — the rule and its
  consequences live in [config-tracking.md](config-tracking.md)
  ("Every condition is re-derived, never dismissed").

### Known E conditions

| Name | Trigger | Resolution |
|------|---------|-----------|
| `model_mismatch` | `tag_model` changed; stored embeddings are from a different model | Config edit reverts, or `ark rebuild` |
| `index_stale` | `case_insensitive`, alias config, or chunker config changed; FTS index is wrong-shape | `ark rebuild` |
| `config_catastrophe` | All sources removed, or config appears zeroed out | Restore via `ark config recover` from stored I records |
| `source_activation` | A source's per-source chunker hook failed at config-resolve — today, a bible source with a real `<source>/BIBLE` path colliding with the reserved address namespace (R3219). Payload lists every failing source. | Fix the source (move or rename the colliding path) and reload; the condition is re-derived per config load, so it clears itself |

## File State Records

### M — Missing files

- **Key:** `M` + 8-byte big-endian fileid.
- **Value:** JSON `{path: string, lastSeen: number}`.
- **Semantic:** files that were indexed but have since disappeared
  from disk. Not auto-deleted; flagged for the user/agent to decide
  what to do.

### U — Unresolved files

- **Key:** `U` + path bytes.
- **Value:** JSON `{path: string, firstSeen: number, dir: string}`.
- **Semantic:** files seen during a scan that don't match any
  include/exclude pattern. Persisted so the list survives across
  scans. Cleared when the user adds a covering rule, dismisses
  them, or the file no longer exists on disk.

## Page Content Records (PC)

- **Key:** `PC` + fileID varint + page varint.
- **Value:** zlib-compressed chunk-text blob.
- **Semantic:** per-page rendered text for paginated documents
  (currently PDFs). One record per (file, page). Authored by the
  PDF chunker; consumed by the PDF chunk-element viewer. Detail
  spec: `pdf-chunk-element.md`.

## Recall Records (R*)

`R` is reserved as the recall-feature namespace. Future records
under this prefix carry emission logs, per-session configuration,
trigger state, and similar recall-only data. Reserved letters
include `RP`/`RPE`/`RR` for LLM-driven definition proposals (not
yet implemented; see `.scratch/CONTEXTUAL-RECALL.md` for the
agent-layer design). Currently:

### RC — Recall Candidate (derived attach proposal)

- **Key:** `"RC"` + source_tvid varint + target_chunkid varint —
  a tag-derived family key, sibling of the X record. `source_tvid`
  is the tvid of the `@ext-candidate` tag whose TARGET named the chunk.
- **Value:** varint tally, materialized from the `@ext-candidate` line's
  `@count` field.
- **Semantic:** one record per (source `@ext-candidate` tvid, target
  chunk). The proposed `(tagname, value)` is **not** stored — it is
  recovered from the source tvid via `TvidMap.Resolve` → the
  `@ext-candidate` value → `ParseExtTarget`, exactly as X recovers its
  routed tag. Higher tally = stronger signal in the Tag Forge.
- **Lifecycle:** **derived**, not directly written — the indexer derives
  it when the `@ext-candidate` file tag is (re)indexed. The propose pass
  (`ark connections recall --propose`) and `ark ext candidate` author that
  tag; `Store.AcceptDerived` / `RejectDerived` rewrite it to `@ext` /
  `@ext-judgment`, whose reindex drops the RC and lands the X+V edge / RJ.
  Struck via `ExtMap.CleanupSource` when the source chunk orphans.
- **Reverse lookup** (proposals for one chunk): the in-memory
  `ExtMap.candidateSourcesByChunk` map (target_chunkid → source tvids),
  not a prefix scan. Detail spec: `derived-tags.md`.

### RD — Recall discussed-tag

- **Key:** `"RD"` + session-bytes + `\x00` + tagname + `\x00` + value.
- **Value:** 8 bytes — unix nanoseconds (big-endian `uint64`)
  recording when the entry was written.
- **Semantic:** per-session dedup state for the recall pipeline.
  An entry marks `(session, tag, value)` as "already covered in
  this conversation" so the substrate can strip it from candidate
  chunks on subsequent recall calls. A bare-tag entry (no value)
  is encoded with an empty value segment; matched by the
  substrate as "any value under this name." TTL is applied lazily
  on read; default 24 hours, configured via `[recall].discussed_ttl`
  in `ark.toml`.
- **Lifecycle:** the recall agent writes after emitting a batch of
  tag suggestions. `ark discussed add/list/clear/prune` operate
  directly. `clear` drops all entries for one session; `prune`
  sweeps across all sessions.
- **Cross-references:** `session-bytes` is variable-length
  (Claude Code session UUID, hex), `\x00`-separated from tagname.
  tagname and value follow the same no-`\x00` constraint as V
  records. Detail spec: `discussed-tags.md`.

### RF — Recall Freshness (per-chunk derivation stamp) — DORMANT

**Retired by #36 (recall-proposals-for-display).** The derivation pass is now
compute-for-display and keeps no freshness cache, so RF has no writer or
reader. The record class and its Store methods (`WriteDerivedFreshness` /
`ReadDerivedFreshness`) are retained pending a full teardown (banked O-gap);
`ark connections clean -all` still wipes any residual RF records.

- **Key:** `"RF"` + chunkid varint.
- **Value:** varint-encoded `uint64` — historically the `max RecordSerial(ED, *)`
  observed when this chunk was last derivation-processed.
- **Semantic (historical):** "this chunk has been processed against the ED
  landscape as of serial N." Formerly the derivation freshness skip.
- **Lifecycle:** formerly written by the derivation pass on every chunk it
  processed; **no writer after #36.** Residual records are cleaned lazily via
  the chunkid-orphan callback (alongside EC and F). Detail spec: `derived-tags.md`.

### RJ — Recall Judgment (signed per-edge relevance)

- **Key:** `"RJ"` + source_tvid varint + target_chunkid varint. Mirrors
  RC exactly — a tag-derived family key; `source_tvid` is the
  `@ext-judgment` tag whose TARGET named the chunk, and the tagname is
  recovered from it (not stored).
- **Value (v3):** `signed-varint(score) + 8-byte BE unix nanos`
  (the score is zigzag-encoded via `binary.PutVarint`). `score < 0`
  is net-rejected — magnitude `-score` is the rejection strength;
  `score > 0` is reinforced; `score == 0` is neutral, equivalent to
  record-absent (a write that lands at 0 may delete the record). The
  timestamp is the most-recent adjustment (enables decay-on-read as a
  future knob).
- **Semantic:** one signed relevance figure per (source `@ext-judgment`
  tvid, target chunk) edge. The score is **materialized from the
  `@ext-judgment` line's signed `@count`** (negative = net-rejected
  magnitude, positive = reinforced); RJ is derived on reindex, not
  written directly. Rejection is the negative tail of a single
  bidirectional axis; reinforcement raises the score, decay/rejection
  lowers it. The derivation pass suppresses re-proposal when the score
  is negative, reading the in-memory `ExtMap.rejectByChunk` map (not an
  RJ key lookup — RJ is source_tvid-keyed); two ceiling knobs in
  `[recall]` —
  `reject_propose_ceiling` and `reject_mention_ceiling` — consult the
  magnitude (`-score`) to decide whether the substrate keeps proposing
  and whether the assistant keeps mentioning the pair. `0` (unset) on
  either knob = infinite, the safe default. No manual un-reject verb;
  a reinforcement producer is the only thing that raises a negative
  score back toward 0. **Migration:** the v2 (`varint counter + nanos`)
  and v3 values are structurally indistinguishable, so there is no
  automatic drop — `ark connections clean -all -checkpoint` wipes RJ
  and the next reject/reinforce cycle rewrites in v3 shape. Detail
  spec: `simple-recall.md`.

### RM — Recall surface-cooldown (per-(session, chunk))

- **Key:** `"RM"` + session-bytes + `\x00` + chunkid varint. RD-family
  sibling keyed by chunk instead of tag-value; session-bytes is the
  Claude Code session UUID (variable-length, no `\x00`), `\x00`-
  separated from the trailing chunkid varint.
- **Value:** 8 bytes — big-endian `uint64` unix nanoseconds, the most
  recent time this chunk was surfaced to this session.
- **Semantic:** the secretary's surface-cooldown substrate. Presence
  means "surfaced before"; the timestamp drives a deterministic floor
  that suppresses re-surfacing the same `(session, chunk)` within
  `[recall].surface_cooldown` (default `"24h"`), which doubles as the
  RM record's lazy-expiry TTL. The floor wiring lives in the secretary
  pipeline; this record + its Store API (`MarkSurfaced`, `LastSurfaced`,
  `PruneSurfaceCooldown`) is the substrate. A value not exactly 8 bytes
  is treated as absent. `ark connections clean` wipes RM alongside RD.
  A `varint(match_count)` trailer (the "paint-priority" match-frequency
  signal) is a deferred extension. Detail spec: `simple-recall.md`.

## Schema Version Protocol

On `DB.Open`, the database checks schema-version markers and reacts
based on the cost of the mismatch:

1. **`ec_version`** — if missing or != `"2"`, drop all EC and EF
   records. The next `BatchEmbedChunks` pass regenerates them.
   Search degrades gracefully (FTS still works) until the batch
   completes. This is automatic — the user doesn't see it as an
   error.

On `DB.Init` (fresh database), all current schema-version markers
are written unconditionally so a brand-new DB starts at the latest
schema.

Old binaries opening a newer DB don't read markers they don't know
about, so they may mis-read newer-shape records as older-shape
garbage. Out of contract — old binaries must not be used on
new-format data.

## Cross-References

### chunkid resolution

EC records key by chunkid. To find which files contain a chunkid,
call microfts2's public `FilesForChunk(chunkID)` — the C record
maps chunkid to a refcounted list of fileids.

### tvid as join key

The tvid varint at the end of a V record key identifies the
(tag, value) pair. The same tvid keys the EV record. To resolve
"what tag-value does this EV vector represent," scan V records and
read the tvid from each key suffix.

### tvid lifecycle

`next_tvid` (an I record) is the allocator. When a new (tag, value)
is first indexed, the counter is incremented and the new tvid is
written into:
- The V record key suffix.
- The F record value trailer (for every chunk occurrence carrying
  this (tag, value)).
- An EV record (lazily, on next batch-embed pass).

Re-indexing the same (tag, value) reuses the existing tvid; tvids
are stable.

Routed tags inside `@ext: TARGET @tag1: v1 …` allocate from the
same `next_tvid` counter — no separate ID space. The routed tvids
are written into the V record (multi-set append at the target
chunkid) and into the X record value (provenance trailer for the
source's @ext tvid). The source's @ext tag itself is also a
regular tag — its (tag="ext", value=full text) gets its own tvid
in V/F as usual.

### File-level vs chunk-level

| Record      | Keyed by                   | Notes                                                                          |
|-------------|----------------------------|--------------------------------------------------------------------------------|
| D, M, EF, U | file (fileid)              | File-level; one per file                                                       |
| F           | chunk (chunkid)            | Chunk-level (chunkid-keyed since tag store v1)                                 |
| V, T        | tag (or tag+value)         | Vocabulary-level; cross-file                                                   |
| X           | (tvid_ext, target chunkid) | @ext provenance; one per (source @ext tvid, target chunk) pair                 |
| EC          | chunkid                    | Chunk-level (microfts2-dedup'd content)                                        |
| EV          | tvid                       | Tag-value compound (cross-file)                                                |
| ED          | (tag, fileid)              | Tag-definition embedding; one per (tag, defining file) pair                    |
| PC          | (file, page)               | File-level page slice                                                          |
| HC          | (tag, chunkid)             | Derived top-K cache; one record per (tag, chunkid) inside the bucket           |
| S           | (stamped prefix + key)     | Side-index; inherits the stamped record's axis (SEC chunk-level, SED per-(tag,file), SHC per-(tag,chunk)) |
| I, E        | named key                  | Singletons                                                                     |
