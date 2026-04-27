# LMDB Record Formats

Canonical reference for ark's LMDB record layouts.

This document describes the **current** state of the database. Migration
specs in `specs/migrations/` describe how to *get to* the current state;
this document describes what the current state *is*.

Language: Go. Environment: ark LMDB subdatabase named `ark`. The
microfts2 subdatabase has its own record formats documented in
microfts2's corpus and is not duplicated here.

## Subdatabase

A single LMDB environment hosts two named subdatabases:

- **`ark`** — primary store: tags, embeddings, file state, config,
  page content. All records described in this document live here.
- **`microfts2`** — full-text search index: chunks, files, trigrams,
  tokens, hashes. Documented in microfts2's repository.

## Prefix Inventory

Every key starts with a one- or two-byte prefix that identifies the
record class.

| Prefix | Record class        | Key shape                                         | Value                                   |
|--------|---------------------|---------------------------------------------------|-----------------------------------------|
| `T`    | Tag total           | `T` + tagname                                     | uint32 count + optional vector          |
| `F`    | File-tag            | `F` + fileid:8 + tagname                          | uint32 count + optional tvids           |
| `V`    | Tag value           | `V` + tag + `\x00` + value + `\x00` + tvid varint | packed fileid varints                   |
| `D`    | Tag definition      | `D` + tagname + fileid:8                          | description bytes                       |
| `EV`   | Tag-value embedding | `EV` + tvid varint                                | float32 vector (3072 bytes)             |
| `EC`   | Chunk embedding     | `EC` + chunkID varint                             | float32 vector (3072 bytes)             |
| `EF`   | File centroid       | `EF` + fileID varint                              | float32 sum (3072 bytes) + uint32 count |
| `I`    | Info / settings     | `I` + name                                        | string or JSON or counter (8 bytes)     |
| `E:`   | Error condition     | `E:` + name                                       | JSON                                    |
| `M`    | Missing file        | `M` + fileid:8                                    | JSON                                    |
| `U`    | Unresolved file     | `U` + path                                        | JSON                                    |
| `PC`   | Page content        | `PC` + fileID varint + page varint                | zlib-compressed blob                    |

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
- **Semantic:** count = number of files in which the tag appears (a
  file with three chunks all tagging `@food: hamburger` contributes
  one). The optional trailing vector is the embedding of the tag
  name (with hyphens converted to spaces, e.g. `design-decision` →
  `design decision`); written by `WriteTagNameEmbedding`. A T record
  with `len(value) == 4` has no embedding; `len(value) == 4+3072`
  has one.
- **Lifecycle:** `adjustTagTotal` increments/decrements the count
  while preserving any trailing vector. When the count would reach
  zero, the entire record is deleted (embedding goes with it).

### F — Per-(file, tag) records (with optional tvid trailer)

- **Key:** `F` + 8-byte big-endian fileid + tagname.
- **Value:** `[count: uint32 big-endian (4 bytes)] [optional packed tvid varints]`.
- **Semantic:** one record per (file, tag) pair. Count = number of
  occurrences of the tag in the file. The tvid trailer lists every
  tag-value pair the file contributed for *this* tag, enabling
  targeted V-record cleanup on file removal: read F records for the
  fileid → collect tvids → remove the fileid from exactly those V
  records.
- **Lifecycle:** `UpdateTags` writes count only (no tvids).
  `AppendTags` preserves any existing tvid trailer. The tvid trailer
  is written by `updateFRecordTvids` after V records are written.
  A writer that doesn't append tvids leaves the record valid (count
  is always present) but stripped of the reverse-lookup hint.

### V — Tag value index

- **Key:** `V` + tagname + `\x00` + value + `\x00` + tvid varint.
- **Value:** packed fileid varints (LEB128).
- **Semantic:** one record per unique (tag, value) pair. The tvid
  is a sequential numeric identifier for the (tag, value) — stable
  across re-index, used as the join key for tag-value embeddings
  (EV records). Count = number of varints in the value.
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
  orphaned chunkIDs that ark deletes inside the same LMDB
  transaction. New chunkIDs (from re-index) are picked up by the
  next `BatchEmbedChunks` pass; EC writes happen out of the actor
  transaction (GPU compute mustn't block the actor).

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

| Name | Encoding | Source field |
|------|----------|--------------|
| `dotfiles` | bool→string ("true"/"false") | `Config.Dotfiles` |
| `case_insensitive` | bool→string | `Config.CaseInsensitive` |
| `embed_cmd` | string | `Config.EmbedCmd` |
| `query_cmd` | string | `Config.QueryCmd` |
| `tag_model` | string (GGUF filename) | `Config.TagModel` |
| `global_include` | JSON array | `Config.GlobalInclude` |
| `global_exclude` | JSON array | `Config.GlobalExclude` |
| `strategies` | JSON map | `Config.Strategies` |
| `sources` | JSON array | `Config.Sources` |
| `chunkers` | JSON array | `Config.Chunkers` |
| `session_ttl` | string | `Config.SessionTTL` |
| `search_exclude` | JSON array | `Config.SearchExclude` |
| `embed_tiers` | JSON array | `Config.EmbedTiers` |
| `schedule` | JSON | `Config.Schedule` |
| `schedule_config` | JSON | `Config.ScheduleConfig` |

### Operational fields

| Name | Encoding | Purpose |
|------|----------|---------|
| `next_tvid` | uint64 counter | Tag-value-id allocation (V record tvid suffix, EV record key). Incremented when a new (tag, value) pair is first indexed. |

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
  `ark status` warnings.

### Known E conditions

| Name | Trigger | Resolution |
|------|---------|-----------|
| `model_mismatch` | `tag_model` changed; stored embeddings are from a different model | Config edit reverts, or `ark rebuild` |
| `index_stale` | `case_insensitive`, alias config, or chunker config changed; FTS index is wrong-shape | `ark rebuild` |
| `config_catastrophe` | All sources removed, or config appears zeroed out | Restore via `ark config recover` from stored I records |

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
- The F record value trailer (for every file occurrence carrying
  this (tag, value)).
- An EV record (lazily, on next batch-embed pass).

Re-indexing the same (tag, value) reuses the existing tvid; tvids
are stable.

### File-level vs chunk-level

| Record | Keyed by | Notes |
|--------|----------|-------|
| F, D, M, EF, U | file (fileid) | File-level; one per file |
| V, T | tag (or tag+value) | Vocabulary-level; cross-file |
| EC | chunkid | Chunk-level (microfts2-dedup'd content) |
| EV | tvid | Tag-value compound (cross-file) |
| PC | (file, page) | File-level page slice |
| I, E | named key | Singletons |
