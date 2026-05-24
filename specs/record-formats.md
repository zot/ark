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
record class. `ark status -db` shows these records so when we add or
change them, we need to update the CLI code so it's up-to-date.

| Prefix | Record class        | Key shape                                         | Value                                   |
|--------|---------------------|---------------------------------------------------|-----------------------------------------|
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
| `RC`   | Recall Candidate    | `RC` + chunkid varint + tagname                   | 8-byte big-endian uint64 tally counter  |
| `RD`   | Recall discussed-tag| `RD` + session-bytes + `\x00` + tagname + `\x00` + value | 8-byte big-endian unix nanos     |
| `RF`   | Recall Freshness    | `RF` + chunkid varint                             | varint uint64 (max S-over-ED at last derivation) |
| `RJ`   | Recall reJection    | `RJ` + chunkid varint + tagname                   | 8-byte big-endian unix nanos            |
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
  `LMDB_T[tag] + ExtMap.VirtualTagCount[tag]`. Ext-routed
  contributions don't write F records (target's F is inline-only),
  so the LMDB T count doesn't see them; the in-memory virtual count
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
- **What lives in memory, not LMDB:** the ExtMap's six
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
  orphaned chunkIDs that ark deletes inside the same LMDB
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
  the LMDB transaction that wrote the stamped record. Multiple
  records written in one txn share one serial; later txns have
  strictly greater serials.
- **Source counter:** `I:serial` (varint uint64). Maintained
  explicitly rather than from `txn.ID()` because ark's startup
  compact-copy (`mdb_env_copy(MDB_CP_COMPACT)`) may reset
  `mt_txnid` on the destination, but the I-record counter sits
  in the live B-tree and is preserved across compactions.
- **Semantic:** monotonic freshness stamp. "Records that moved
  together carry the same mark." Used by derived caches (HC,
  future) to identify which records changed since a bookmark —
  `RecordSerial(prefix, key)` and
  `WalkRecordsSinceSerial(prefix, since, fn)`.
- **Lifecycle:** written via `stampWrite` / `stampWriteWith`
  alongside the original record's `txn.Put`, inside the same
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
| `tag_model`        | string (GGUF filename)       | `Config.TagModel`        |
| `global_include`   | JSON array                   | `Config.GlobalInclude`   |
| `global_exclude`   | JSON array                   | `Config.GlobalExclude`   |
| `strategies`       | JSON map                     | `Config.Strategies`      |
| `sources`          | JSON array                   | `Config.Sources`         |
| `chunkers`         | JSON array                   | `Config.Chunkers`        |
| `session_ttl`      | string                       | `Config.SessionTTL`      |
| `search_exclude`   | JSON array                   | `Config.SearchExclude`   |
| `embed_tiers`      | JSON array                   | `Config.EmbedTiers`      |
| `schedule`         | JSON                         | `Config.Schedule`        |
| `schedule_config`  | JSON                         | `Config.ScheduleConfig`  |

### Operational fields

| Name | Encoding | Purpose |
|------|----------|---------|
| `next_tvid` | uint64 counter | Tag-value-id allocation (V record tvid suffix, EV record key). Incremented when a new (tag, value) pair is first indexed. |
| `serial` | varint uint64 | Per-txn monotonic serial counter for the S freshness substrate. Allocated once per write txn that stamps records; preserved across LMDB compact-copy (unlike `txn.ID()`). |
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

## Recall Records (R*)

`R` is reserved as the recall-feature namespace. Future records
under this prefix carry emission logs, per-session configuration,
trigger state, and similar recall-only data. Reserved letters
include `RP`/`RPE`/`RR` for LLM-driven definition proposals (not
yet implemented; see `.scratch/CONTEXTUAL-RECALL.md` for the
agent-layer design). Currently:

### RC — Recall Candidate (derived attach proposal)

- **Key:** `"RC"` + chunkid varint + tagname (raw bytes; `[\w][\w\-.]*`
  grammar, no control bytes).
- **Value:** 8 bytes — big-endian `uint64` tally counter.
- **Semantic:** one record per (chunkid, tagname) statistical
  derivation candidate. The tagname is the proposed attach; bare-tag
  shape in the statistical slice (no value segment). Tally counts
  how many derivation passes have proposed the same (chunkid,
  tagname); higher tally = stronger signal in the Tag Forge.
- **Lifecycle:** written by the derivation pass when
  `ark connections recall --propose` is set. Deleted by
  `Store.AcceptDerived` (after writing F/V for the attach) or
  `Store.RejectDerived` (after writing the corresponding RJ
  record).
- **Reverse lookup** (proposals for one chunk): prefix scan
  `"RC"` + chunkid varint. Detail spec: `derived-tags.md`.

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

### RF — Recall Freshness (per-chunk derivation stamp)

- **Key:** `"RF"` + chunkid varint.
- **Value:** varint-encoded `uint64` — the `max RecordSerial(ED, *)`
  observed when this chunk was last derivation-processed.
- **Semantic:** "this chunk has been processed against the ED
  landscape as of serial N." A chunk is fresh (skip-eligible) for
  derivation iff its RF stamp `>=` the current max ED serial.
  Missing RF is treated as serial 0 (force re-process).
- **Lifecycle:** written by the derivation pass on every chunk
  it processes, with or without resulting proposals. Cleaned up
  lazily — RF records for chunkids orphaned by microfts2 are
  removed alongside EC and F via the existing chunkid-orphan
  callback path. Detail spec: `derived-tags.md`.

### RJ — Recall reJection (sticky no-resurface marker)

- **Key:** `"RJ"` + chunkid varint + tagname. Mirrors RC exactly.
- **Value:** 8 bytes — big-endian `uint64` unix nanoseconds
  (rejection timestamp; presence of the record is what blocks
  re-proposal, not the timestamp value).
- **Semantic:** the curator rejected this (chunkid, tagname). The
  derivation pass checks RJ before writing RC; an RJ hit
  suppresses re-proposal. Sticky in v1 — no TTL, no
  un-reject verb. Detail spec: `derived-tags.md`.

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
