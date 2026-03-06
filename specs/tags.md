# Tag Tracking

Ark extracts `@tag:` patterns from file content during scan/add.
The trailing colon is required — disambiguates from emails and
mentions. Tags are an ark-level concept, not a microfts2 concern.

## Storage (ark subdatabase)

- `T` [tagname] -> count — tag vocabulary with global counts
- `F` [fileid: 8] [tagname] -> count — per-file tag occurrences

Tags are updated whenever a file is indexed or refreshed. On
remove, the file's tag entries are deleted and global counts
decremented.

## Tag vocabulary file: `~/.ark/tags.md`

- Format: `@tag: name -- description`
- Indexed like any other file — ark finds definitions by content
- New tags emerge by use; this file documents what they mean
- `ark init` creates a starter tags.md with the format documented

## CLI

- `ark tag list` — all known tags with counts
- `ark tag counts <tag>...` — total count per tag
- `ark tag files <tag>...` — filename and size per file
  - `--context` shows each occurrence with tag to end of line
  - includes tag definitions from tags.md alongside usage

## HTTP API

- `GET /tags` — all tags with counts
- `GET /tags/<tag>/files` — files containing tag
