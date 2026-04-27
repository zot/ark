# Tag Value Index

LMDB index for tag values, enabling fast completion without reading
files from disk. Language: Go. Environment: ark LMDB subdatabase.

## Problem

Tag value completion (e.g. completing `@status: in` to `in-progress`)
currently requires reading every file that has the tag and scanning
for the value. This is O(files) disk I/O per keystroke — unusable
for tags like `status` that appear in hundreds of files.

## V Records

The `V` prefix stores tag values. Each unique (tag, value) pair gets
one LMDB entry whose value bytes list the fileids with that
(tag, value), enabling fast value completion without disk I/O.

Record key/value layout: see [record-formats.md](record-formats.md)
(V section). Note: V keys also carry a trailing tvid varint that
joins to EV records (tag-value embeddings) — see
[tag-embeddings.md](tag-embeddings.md).

## Lifecycle

V records follow the same lifecycle as F and D records:

- **Index/Refresh:** extract tag values from file content (already
  done by `ExtractTagValues`), remove old V entries for the file,
  add new V entries.
- **Append:** extract tag values from appended content, add V entries
  (no removal — appended tags are additive).
- **Remove:** remove the fileid from all V entries. If a V entry's
  fileid list becomes empty, delete the key.

## Querying

V records support three query patterns: all-values-for-a-tag (count
varint fileids per record), prefix-filtered values (LMDB sorted-key
range scan), and (tag, value) → fileids direct lookup. See
[record-formats.md](record-formats.md) (V section) for the exact
prefix scan keys.

## Integration with handleTagValues

The `POST /tags/values` endpoint switches from reading files to
querying V records. The Store provides a method that does the prefix
scan and returns `{value, count}` pairs sorted by count descending.

## Rebuild

`ark rebuild` regenerates V records from scratch alongside T, F,
and D records. No migration needed — V records are derived from
the same content that produces T/F/D.
