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
