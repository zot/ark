# Tag Value Index

LMDB index for tag values, enabling fast completion without reading
files from disk. Language: Go. Environment: ark LMDB subdatabase.

## Problem

Tag value completion (e.g. completing `@status: in` to `in-progress`)
currently requires reading every file that has the tag and scanning
for the value. This is O(files) disk I/O per keystroke — unusable
for tags like `status` that appear in hundreds of files.

## V Records

A new LMDB key prefix `V` stores tag values:

- Key: `V[tagname]\x00[value]`
- Value: packed varint-encoded fileids

The `\x00` byte separates the tag name from the value. Tag names
are `[\w][\w-]*` so they cannot contain null bytes.

Each unique (tag, value) pair gets one LMDB entry. The value bytes
are the list of fileids that have that tag set to that value,
encoded as varints (unsigned, LEB128). Count = number of varints.

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

- **All values for a tag:** prefix scan `V[tagname]\x00`. Decode
  varints from each value to get count.
- **Values matching prefix:** prefix scan `V[tagname]\x00[prefix]`.
  LMDB's sorted keys make this a range scan.
- **Files for a (tag, value):** direct key lookup, decode varints.

## Integration with handleTagValues

The `POST /tags/values` endpoint switches from reading files to
querying V records. The Store provides a method that does the prefix
scan and returns `{value, count}` pairs sorted by count descending.

## Rebuild

`ark rebuild` regenerates V records from scratch alongside T, F,
and D records. No migration needed — V records are derived from
the same content that produces T/F/D.
