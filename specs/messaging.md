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

## Direct messages (`@dm`)

Request/response messaging routes through files in `requests/`
directories and uses `@from-project`/`@to-project` for the
addresses. Direct messaging routes through `tmp://` documents
and uses the `@dm` tag for the address. The two surfaces share
no plumbing beyond ark's tag-search machinery; they're separate
protocols for different audiences (projects vs. agent sessions).

### `@dm` grammar

```
@dm: RECIPIENT[ RECIPIENT2 RECIPIENT3 ...][: SUBJECT]
```

- One or more recipients, space-separated. A recipient is
  typically a Claude Code session UUID, but the value is
  opaque to the grammar — any string a subscriber can match
  against is legal.
- An optional subject, separated from the recipient list by
  `: `. The subject is freeform text the receiving agent can
  pre-triage on without reading the message body, since
  `ark listen` renders the tag value as a `## @dm: <value>`
  heading at the top of each delivery.
- The single-recipient, subject-less form (`@dm: <session>`)
  is unchanged from prior usage and remains the most common
  shape. Multi-recipient and subject are additive.

Examples:

```
@dm: 0e2f...
@dm: 0e2f...: recall
@dm: 0e2f... 71a3...: standup-ping
```

Parsing rules:

- Split on `: ` (colon-space) — the substring before is the
  recipient list, the substring after (if present) is the
  subject. The subject may itself contain `: ` (the split is
  on the first occurrence only).
- The recipient list splits on whitespace runs into
  individual recipient tokens.
- A trailing `:` with no subject (`@dm: foo:`) is illegal;
  reject it at write time.

### Service identities (`@from-service`)

For messages emitted by ark's own internal subsystems
(watchers, schedulers, background derivation passes), the
sender identity is `@from-service`, not `@from-project`.

```
@from-service: ARK-<SUBSYSTEM>
```

- Values follow `ARK-<SUBSYSTEM>` shape — one identity per
  service. Examples: `ARK-RECALL` (the simple-recall
  watcher), `ARK-SCHEDULER` (reserved for future use).
- Service identities are not shared umbrellas: each emitting
  subsystem gets its own identity so receivers can subscribe
  to `@from-service: ARK-RECALL` independently of any other
  ark-emitted traffic.
- Mutually exclusive with `@from-project` on the same
  message. A message either comes from a project (a user-
  facing entity) or from an ark service (an internal
  subsystem); never both.

Why split from `@from-project`: ark itself ships as a project
literally named `ark` and already participates in
cross-project messaging via `@from-project: ark`. Reusing
`@from-project` for service traffic would conflate two
distinct origins. Splitting the tag makes the distinction
first-class without teaching receivers a value-casing
convention.

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

### dm

```
ark message dm --from SESSION         --to RECIPIENT [--to R2 ...] [--subject TEXT] [--ref ID] --content TEXT
ark message dm --from-service NAME    --to RECIPIENT [--to R2 ...] [--subject TEXT] [--ref ID] --content TEXT
```

Appends a direct-message chunk to `tmp://<sender>/dm-<to0>`
where `<sender>` is the `--from` session UUID or the
`--from-service` identity (e.g. `ARK-RECALL`) and `<to0>` is
the first recipient. Server-required — DMs live in tmp://
memory.

The subcommand owns the tag block at the head of each
appended chunk. The caller supplies the body content; the
subcommand prepends the chunk boundary, the `@dm`/`@from`
tags, and the optional `@ref`, then the body, then a
trailing newline.

Sender flags (mutually exclusive):

| Flag                  | Effect                                                                                                                                |
|-----------------------|---------------------------------------------------------------------------------------------------------------------------------------|
| `--from SESSION`      | Sender is a Claude Code session UUID. Emits `@from: <session>`. tmp:// path uses `<session>` as the sender segment.                   |
| `--from-service NAME` | Sender is an ark internal subsystem. Emits `@from-service: NAME` instead of `@from`. tmp:// path uses `NAME` as the sender segment.   |

Recipient and subject flags:

| Flag             | Behavior                                                                                                                                  |
|------------------|-------------------------------------------------------------------------------------------------------------------------------------------|
| `--to RECIPIENT` | Required. Repeatable for multi-recipient DMs. Recipients are space-joined into the `@dm` value in the order given.                        |
| `--subject TEXT` | Optional. When set, appended to `@dm` as `: TEXT` after the recipient list (the `@dm` subject form). Empty text is rejected.              |
| `--ref ID`       | Optional. Threading reference. Emits `@ref: ID` on its own line in the tag block.                                                         |

Emitted tag block shapes:

```
@dm: <recipient>
@from: <session>
```

```
@dm: <r1> <r2> <r3>: <subject>
@from-service: ARK-RECALL
@ref: <ref-id>
```

The internal compose function is shared between the CLI and
ark's in-process callers — the simple-recall watcher emits
through the same path so the contract stays uniform. The
CLI is useful for manual testing of a service-emitted DM
without standing up the watcher:

```
ark message dm --from-service ARK-RECALL --to <session> \
  --subject recall --content "$(cat body.md)"
```

Body content rules match `new-request` / `new-response`:
`--content` flag (preferred for agents) or stdin until lone
`.` when stdin is non-TTY and `--content` is unset.
