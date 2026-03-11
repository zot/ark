# Messaging

Language: Go. Environment: CLI (part of the `ark` binary).

Projects communicate through tagged markdown files in `requests/`
directories. Ark indexes these files and connects them through tag
search. The format is simple — tag block at the top, body below — but
models get it wrong reliably enough that the format must be enforced
by a CLI command rather than prompt instructions.

## Tag block format

A tag block is consecutive lines at the top of a file, each matching
`@tag: value`. The first line that doesn't match ends the block.
A blank line separates the tag block from the body.

```
@request: fix-chunker-bug
@from-project: ark
@to-project: microfts2
@status: open
@issue: ChunkFunc doesn't expose retrieval

# fix-chunker-bug

Detailed description here.
```

Rules:
- No blank lines within the tag block
- One tag per line, format `@name: value` (space after colon)
- Blank line between tag block and body
- Tag names use the same character set as ark tags: letters, digits,
  hyphens, dots, underscores (starting with a letter)

## Subcommands

All subcommands are under `ark message`. They operate on plain files —
no server dependency, no new storage.

### new-request

```
ark message new-request --from PROJECT --to PROJECT --issue "..." FILE
```

Creates a new request file with the correct tag block and body scaffold.
Errors if FILE already exists. The request ID is derived from the
filename (basename without extension).

Output file:
```
@request: <id>
@from-project: <from>
@to-project: <to>
@status: open
@issue: <issue text>

# <id>

<issue text>
```

### new-response

```
ark message new-response --from PROJECT --to PROJECT --request ID FILE
```

Creates a new response file. Errors if FILE already exists.

Output file:
```
@response: <id>
@from-project: <from>
@to-project: <to>
@status: done

# RESP <id>

```

### set-tags

```
ark message set-tags FILE TAG VALUE [TAG VALUE ...]
```

Updates or adds tags in the tag block. Arguments are pairs: tag name
then value. If the tag exists, its value is replaced. If not, the tag
is appended to the end of the tag block. Tag order is preserved for
existing tags. Body is untouched.

The tag name should be given without `@` prefix or `:` suffix — the
command adds those.

Errors if FILE doesn't exist. If the file has no tag block (e.g. body
starts on line 1), the tags are inserted at the top with a blank line
before the existing content.

### get-tags

```
ark message get-tags FILE [TAG ...]
```

Reads tags from the tag block. Outputs one `tag\tvalue` per line
(tab-separated, no `@` or `:`). If specific tags are named, outputs
only those (in the order requested). If no tags named, outputs all
tags in file order.

Exits with status 1 if a requested tag is not found (but still outputs
any tags that were found).

### check

```
ark message check FILE
```

Validates the file against the tag block format rules. If the file is
valid, exits 0 with no output.

If invalid, outputs a crank-handle diagnostic: a description of each
problem and the exact `ark message` command to fix it. The output is
designed to be followed by a model without additional context.

Problems detected:
- Tag-like patterns (`@word:` or `## Word:`) in the body that look
  like misplaced tags
- Blank lines within the tag block
- Missing blank line between tag block and body
- Malformed tag lines in the tag block (missing space after colon, etc.)

### ack

```
ark message ack FILE
```

Marks a message as read: sets `@msg` to `read`. Convenience wrapper
around `set-tags` — same file I/O, same TagBlock mechanics.

If `@msg` is already `read`, `acting`, or `closed`, does nothing (no
error). The intent is "I saw it" — idempotent, safe to call repeatedly.

### close

```
ark message close FILE
```

Marks a message as closed: sets `@msg` to `closed`. Same mechanics as
ack. If `@msg` is already `closed`, does nothing.

### inbox

```
ark message inbox [--project PROJECT]
```

Lists messages that are not closed. Unlike the other message
subcommands, inbox is a search operation — it needs the database to
find message files across all indexed sources.

Finds all indexed files that contain `@msg:` tags, reads each file's
tag block, and filters to those where `@msg` is not `closed`.

When `--project` is given, further filters to messages where
`@to-project` matches. Without `--project`, shows all non-closed
messages.

Output is sorted: `@msg:new` messages first, then others. Within
each group, sorted by file path.

Output format: one line per message, tab-separated:
```
msg-value	to-project	from-project	status	issue-or-response	path
```

The `issue-or-response` field is the `@issue` value (for requests)
or `response:<id>` (for responses). If neither tag exists, the field
is empty.

Uses the server proxy when available, falls back to cold-start
(`withDB`). Read-only operation.
