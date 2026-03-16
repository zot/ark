# Ark Cross-Project Messaging

## Version 2 2026-0311-1845

How ark projects communicate requests and responses through tagged
files that ark indexes and connects automatically.

## Why

Projects often need things from each other — API changes, new
exports, bug fixes. Ark replaces ad-hoc drop-box files with tagged
notes that are searchable from any project context.

## Cardinal Rule

**Always write to YOUR project's `requests/` directory.** Never
modify files in another project's directory. A request FROM ark
TO microfts2 lives in `ark/requests/`. A response FROM microfts2
TO ark lives in `microfts2/requests/`.

This is load-bearing. It means:
- No cross-project file writes, ever
- Each project owns its own files completely
- Acknowledgment happens by creating a response, not modifying the request

## Convention

### Requests

A requesting project creates a file in its own `requests/` directory:

```
requests/<short-name>.md
```

Use the bare name. If the name already exists, append `-<session8>`
(first 8 chars of Claude session ID) to disambiguate.

Use `ark message new-request` to create (never hand-write tag blocks):

```bash
~/.ark/ark message new-request \
  --from my-project --to target-project \
  --issue "short description" \
  requests/chunker-interface.md
```

The file uses tags for discoverability:

```markdown
@ark-request: chunker-interface
@from-project: ark
@to-project: microfts2
@status: open
@issue: Define Chunker interface for per-chunk attributes

# chunker-interface

What's needed and why. Include enough context for the target
project to act without reading the requesting project's code.
```

### Responses

The target project creates a response file in its own `requests/`:

```
requests/RESP-<request-id>.md
```

Use `ark message new-response` to create:

```bash
~/.ark/ark message new-response \
  --from my-project --to requesting-project \
  --request original-request-id \
  requests/RESP-original-request-id.md
```

```markdown
@ark-response: chunker-interface
@from-project: microfts2
@to-project: ark
@status: accepted

# RESP chunker-interface

What was done. Reference commits, files, or API surfaces.
```

### Response as acknowledgment

**The response file's existence is the ack.** Creating a response
with `@status: accepted` means "I saw your request, I'll act on it."
No need to modify the sender's file. No cross-project writes.

Response status progression:
1. `accepted` — I saw it, I'll do it
2. `in-progress` — working on it
3. `completed` — here's the result

The sender checks whether their request was seen by searching for
responses to their request ID.

### Self-messages

For notes-to-self (same project as sender and receiver), a single
file with `@status` is sufficient. No response file needed —
just update status on the original: `open` → `completed`.

## Status Values

One lifecycle, clear progression:

- **open** — needs attention
- **accepted** — acknowledged, will act
- **in-progress** — being worked on
- **completed** — finished
- **denied** — won't do (response body explains why)
- **future** — not yet actionable, parked for later

### Archiving

`@archived: true` is separate from status. Any message can be archived
regardless of its status. Inbox excludes archived messages by default.
Searches can exclude with `--exclude-file-tags archived`.

```bash
# Archive a completed request
~/.ark/ark tag set requests/old-request.md archived true

# Find archived messages
~/.ark/ark search --filter-file-tags archived --filter-files '**/requests/*'
```

## IDs

Format: `<short-name>` — the bare descriptive name. Only append
`-<session8>` (first 8 chars of Claude session ID) if the name
already exists. The `@ark-request:` / `@ark-response:` tag says what it
is. The requesting project owns the ID.

## Tags

- `@ark-request: <id>` — a cross-project request
- `@ark-response: <id>` — a response to a request
- `@from-project: <name>` — who's asking
- `@to-project: <name>` — who's answering
- `@issue:` — short description
- `@status: <value>` — lifecycle state (open, accepted, in-progress, completed, denied, future)
- `@status-date: <date>` — when status last changed (set automatically by `ark message`)
- `@reopened: <date> -- <reason>` — request was completed but incomplete
- `@resolved: <date> -- <description>` — reopened issue was fixed

## Finding Conversations

```bash
# Inbox: requests to me with no response from me yet
# (implementation TBD — needs join logic in ark message inbox)

# All requests to a project
~/.ark/ark search --exclude-files '*.jsonl' --regex '@to-project:.*\bPROJECT\b'

# Responses to a specific request
~/.ark/ark search --exclude-files '*.jsonl' --regex '@ark-response:.*REQUEST-ID'

# All tags co-occurring with a request
~/.ark/ark search --exclude-files '*.jsonl' --tags REQUEST-ID

# Waiting-for: my requests with no response yet
~/.ark/ark search --exclude-files '*.jsonl' \
  --regex '@from-project:.*\bMY-PROJECT\b' --regex '@ark-request:'
# then check which have matching @ark-response: files
```

Both request and response files surface together — ark connects
them through content, not directory structure.

## Reopening

When a completed request turns out to be incomplete, change
`@status:` back to `open` and add a `@reopened:` tag with the
date and reason:

```markdown
@status: open
@reopened: 2026-03-09 -- field added but Start() ignores it
```

The `@reopened:` tag is searchable — `ark search --tags reopened`
finds all requests that needed a second pass.

When the reopened issue is fixed, add `@resolved:` with the date
and description:

```markdown
@resolved: 2026-03-09 -- fixed in mcp.Server.Start()
```

## Project View

Every request and response has a `@status` value. Query across
all projects to build a project board:

```bash
# All open work
~/.ark/ark search --exclude-files '*.jsonl' --regex '@status:.*\bopen\b'

# Everything in progress
~/.ark/ark search --exclude-files '*.jsonl' --regex '@status:.*\bin-progress\b'

# Completed items
~/.ark/ark search --exclude-files '*.jsonl' --regex '@status:.*\bcompleted\b'
```

A project dashboard built from status tags:

```
 Future       Open          Accepted      In-Progress   Completed     Denied
 ──────       ────          ────────      ───────────   ─────────     ──────
 tag+value    chunker       chunk attrs                 JSONL unwrap
 index        interface                                 ack/close
              CLI help                                  inbox
              ark approve                               local fetch
```

Franklin uses this to build the daily view — what's open, what's
waiting, what got done.

## Response Content

Responses should always contain at least a one-line summary of
what was done. An empty response body with `@status: accepted`
is valid as an ack, but once work is complete the response should
reference what changed — commits, files, API surfaces. Franklin
needs this to distinguish "completed" from "completed but nobody
wrote down what happened."
