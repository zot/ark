# Ark Cross-Project Messaging

How ark projects communicate requests and responses through tagged
files that ark indexes and connects automatically.

## Why

Projects often need things from each other — API changes, new
exports, bug fixes. Previously this lived in UPDATES.md drop-box
files per project. Ark replaces that with tagged notes that are
searchable from any project context.

## Convention

### Requests

A requesting project creates a file in its own `requests/` directory:

```
requests/<short-name>-<session>.md
```

`<session>` is the first 8 characters of the Claude session ID that
creates the request — appended as a suffix so the meaningful name
comes first. This namespaces IDs so concurrent sessions don't collide.

The file uses tags for discoverability:

```markdown
@request: flib-withlua-fafaf11c
@from-project: requesting-project
@to-project: target-project
@status: open
@msg: new
@issue: short description

# flib-withlua-fafaf11c

What's needed and why. Include enough context for the target
project to act without reading the requesting project's code.
```

### Responses

The target project creates a response file in its own `requests/`:

```
requests/RESP-<short-name>-<session>.md
```

```markdown
@response: flib-withlua-fafaf11c
@from-project: target-project
@to-project: requesting-project
@status: done
@msg: new

# RESP flib-withlua-fafaf11c

What was done. Reference commits, files, or API surfaces.
```

### Finding conversations

```bash
# All tags co-occurring with a request
ark search --exclude-files '*.jsonl' --tags flib-withlua-fafaf11c

# Full text search for a request
ark search '@request: flib-withlua-fafaf11c'
```

Both files surface together — ark connects them through content,
not directory structure.

## IDs

Format: `<short-name>-<session8>` where session8 is the first
8 chars of the Claude session ID, appended as suffix. The
meaningful name leads for readability. The `@request:` /
`@response:` tag says what it is. The requesting project owns
the ID.

Find existing requests:

```bash
ark search --exclude-files '*.jsonl' --tags request
```

## Tags

- `@request: <id>` — a cross-project request
- `@response: <id>` — a response to a request
- `@from-project: <name>` — who's asking
- `@to-project: <name>` — who's answering
- `@issue:` — short description (also used standalone)
- `@status: <value>` — work/task lifecycle (open, in-progress, done, declined)
- `@msg: <value>` — message delivery lifecycle (new, read, acting, closed)
- `@reopened: <date> -- <reason>` — request was completed but incomplete
- `@resolved: <date> -- <description>` — reopened issue was fixed

## Two lifecycles

Messages track two independent things: the **work** being discussed
and the **message** itself. These are separate tags because they
move independently.

### @status — work lifecycle

What's happening with the task, feature, or bug:

- **open** — not yet addressed
- **in-progress** — target project is working on it
- **done** — implemented
- **declined** — won't do, response explains why

### @msg — message delivery lifecycle

Whether the recipient has consumed and acted on the message:

- **new** — unread, needs attention
- **read** — recipient has seen it
- **acting** — recipient is working on a response or follow-up
- **closed** — resolved, no further action needed

A notification reporting completed work is `@status:done @msg:new` —
the feature is done, but the recipient hasn't read the message yet.
A request that's been filed and acknowledged is `@status:open @msg:read`.

The librarian skips `@msg:closed` by default. Everything else is
potentially relevant, with `@msg:new` items getting priority.

### Reopening

When a completed request turns out to be incomplete, change
`@status:` back to `open`, set `@msg:new`, and add a `@reopened:`
tag with the date and reason:

```markdown
@status: open
@msg: new
@reopened: 2026-03-09 -- field added but Start() ignores it
```

The `@reopened:` tag is searchable — `ark search --tags reopened`
finds all requests that needed a second pass. The date and
description after `--` explain what was missed.

When the reopened issue is fixed, add `@resolved:` with the date
and description:

```markdown
@resolved: 2026-03-09 -- fixed in mcp.Server.Start()
```

Both request and response files should also append a
`## Reopened DATE` section in the body documenting what was
missed and what was done to fix it. This gives human-readable
context alongside the searchable tags.

## Skill Text

Cross-project requests via ark-indexed tagged files. Each project
keeps a `requests/` directory. Requests use `@request: <id>`,
responses use `@response: <id>`. ID format: `<short-name>-<session8>`
(session = first 8 chars of Claude session ID, suffix for
disambiguation). Response filenames prefixed with `RESP-`.

Tags (`@request`, `@response`, `@from-project`, `@to-project`) make everything
searchable with `ark search --tags`. Ark connects request and
response files across projects through content — no registry,
no coordination, just files and tags.

Work status (`@status`): open, in-progress, done, declined.
Message delivery (`@msg`): new, read, acting, closed.
Librarian skips `@msg:closed` by default.
