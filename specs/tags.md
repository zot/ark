# Tag Tracking

Ark extracts `@tag:` patterns from file content during scan/add.
The trailing colon is required — disambiguates from emails and
mentions. Tags are an ark-level concept, not a microfts2 concern.

## Storage (ark subdatabase)

T records hold tag vocabulary with global counts. F records hold
per-file tag occurrences. Tags are updated whenever a file is
indexed or refreshed; on remove, the file's tag entries are deleted
and global counts decremented.

Record key/value layouts: see [record-formats.md](record-formats.md)
(T and F sections).

## Tag vocabulary file: `~/.ark/tags.md`

- Format: `@tag: name -- description`
- Indexed like any other file — ark finds definitions by content
- New tags emerge by use; this file documents what they mean
- `ark init` creates a starter tags.md with the format documented

## Tag definitions

Tag definitions are lines matching `@tag: <name> <description>`.
The first word after `@tag:` is the tag name; the rest is the
description. These appear in `~/.ark/tags.md` and any other indexed
file.

Definitions are extracted at index time and cached as D records.
Files remain the source of truth — D records update whenever files
are indexed, refreshed, or appended. One record per (tag, file)
pair where the file defines the tag; a file defining multiple tags
produces multiple D records. Removed and re-extracted on re-index
(same lifecycle as F records).

Record key/value layout: see [record-formats.md](record-formats.md)
(D section).

## Tag values

Tag values are indexed for fast completion. One V record per unique
(tag, value) pair, updated alongside T, F, and D records during
index/refresh/append/remove. See
[specs/tag-value-index.md](tag-value-index.md) for the design and
query patterns; record key/value layout in
[record-formats.md](record-formats.md) (V section).

## CLI

- `ark tag list` — all known tags with counts
- `ark tag counts <tag>...` — total count per tag
- `ark tag files <tag>...` — filename and size per file
  - `--context` shows each occurrence with tag to end of line
  - includes tag definitions from tags.md alongside usage
- `ark tag defs [TAG...]` — tag definitions from LMDB cache
  - No args: all definitions. With args: only those tags.
  - Default output: `tagname description` per line, deduplicated,
    sorted alphabetically.
  - `--path`: output `path tagname description`, lexically sorted,
    not deduplicated. Spaces in paths are backslash-escaped.

## HTTP API

- `GET /tags` — all tags with counts
- `GET /tags/<tag>/files` — files containing tag
