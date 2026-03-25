# Messaging

Language: Go. Environment: CLI (part of the `ark` binary).

Projects communicate through tagged markdown files in `requests/`
directories. Ark indexes these files and connects them through tag
search. The format is simple — tag block at the top, body below — but
models get it wrong reliably enough that the format must be enforced
by a CLI command rather than prompt instructions.

## Protocol (v2)

See ARK-MESSAGING.md for the full protocol. Key principles:

- **Cardinal rule:** always write to YOUR project's `requests/` directory.
  No cross-project file writes, ever.
- **Response = ack:** creating a response file acknowledges the request.
  No need to modify the sender's file.
- **One lifecycle:** `@status` tracks everything. Values: open, accepted,
  in-progress, completed, denied, future.
- **Bare filenames:** `requests/<short-name>.md`. Only append `-<session8>`
  if the name already exists.

Response status progression:
1. `accepted` — I saw it, I'll do it (response file created)
2. `in-progress` — working on it
3. `completed` — here's the result

Self-messages (same project as sender and receiver) need only a single
file. Update `@status` on the original: `open` → `completed`.

## Tag block format

A tag block is consecutive lines at the top of a file, each matching
`@tag: value`. The first line that doesn't match ends the block.
A blank line separates the tag block from the body.

```
@ark-request: fix-chunker-bug
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

## Message tags

The `@ark-request:` or `@ark-response:` tag identifies the file as a
message. The `ark-` prefix distinguishes message identity tags from
generic uses of "request" or "response" in other contexts. The
remaining tags (`@from-project:`, `@to-project:`, `@status:`, `@issue:`)
are generic — they're unambiguous once the discriminator tag is present.

Identity tags (in message tag blocks):
- `@ark-request: <id>` — this file is a request
- `@ark-response: <id>` — this file is a response

Reference tags (in any file, for citing messages):
- `@ark-request-sent: <path>` — a request was sent from this planning item
- `@ark-request-ref: <path-or-id>` — see this request
- `@ark-response-ref: <path-or-id>` — see this response

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

Body content can be provided two ways:

1. **`--content "body text"`** — pass the body as a flag value. This is
   the primary path for agents: the body is a command-line argument,
   so no file writes or stdin pipes are needed. Multiline strings work
   naturally in tool calls.

2. **Stdin** — if `--content` is not set and stdin is not a terminal,
   the command reads body text from stdin until a lone `.` on a line
   (like UNIX `mail` and `ed`).

If `--content` is set, stdin is ignored. If neither is provided, the
command produces heading + issue text only (no body).

Output file:
```
@ark-request: <id>
@from-project: <from>
@to-project: <to>
@status: open
@issue: <issue text>

# <id>

<issue text>

<stdin body, if provided>
```

### new-response

```
ark message new-response --from PROJECT --to PROJECT --request ID FILE
```

Creates a new response file. Errors if FILE already exists. The
response file's existence is the acknowledgment — creating it means
"I saw the request."

Body content works the same as new-request: `--content` flag
(preferred for agents) or stdin if `--content` is not set.

Output file:
```
@ark-response: <id>
@from-project: <from>
@to-project: <to>
@status: accepted

# RESP <id>

<stdin body, if provided>
```

### set-tags / get-tags / check

These are aliases for `ark tag set`, `ark tag get`, and `ark tag check`.
See specs/tag-block-commands.md for full documentation.

`ark message check` calls `ark tag check` with no heading arguments
(generic structural validation). Message-specific heading validation
can be added by passing heading names to `ark tag check` directly.

### inbox

```
ark message inbox [--project PROJECT] [--from PROJECT] [--all] [--include-archived] [--counts]
```

Lists messages. Unlike the other message subcommands, inbox is a
search operation — it needs the database to find message files across
all indexed sources.

Finds all indexed files that contain `@status:` tags in `requests/`
directories, reads each file's tag block, and applies filters.

Filters (all composable):
- `--project PROJECT`: only messages where `@to-project` matches
- `--from PROJECT`: only messages where `@from-project` matches
- `--all`: include completed/done/denied (default: excluded)
- `--include-archived`: include `@archived: true` (default: excluded)
- `--counts`: output status counts instead of individual rows

When `--project` and `--from` are both given, a message must match
both (intersection).

Output is sorted: `@status:open` messages first, then others. Within
each group, sorted by file path.

Output format: one line per message, tab-separated:
```
status	to-project	from-project	issue-or-response	path
```

With `--counts`, output is one line per status, tab-separated:
```
status	count
```

The `issue-or-response` field is the `@issue` value (for requests)
or `ark-response:<id>` (for responses). If neither tag exists, the
field is empty.

Uses the server proxy when available, falls back to cold-start
(`withDB`). Read-only operation.
