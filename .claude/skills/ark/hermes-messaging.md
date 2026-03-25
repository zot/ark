# Hermes Messaging Reference

Your tools are `~/.ark/ark` commands. Do not use grep, awk, wc, find,
or shell loops when an ark command exists.

The database is at `~/.ark`. The ark command is at `~/.ark/ark`.
If the server is not running, start it with `~/.ark/ark serve`.

## Inbox Commands

```bash
# incoming messages to a project (non-completed)
~/.ark/ark message inbox --project PROJECT

# outgoing messages from a project (non-completed)
~/.ark/ark message inbox --from PROJECT

# outgoing counts by status (one line per status)
~/.ark/ark message inbox --from PROJECT --counts

# all outgoing including completed/done/denied
~/.ark/ark message inbox --from PROJECT --all

# all outgoing counts (complete picture in one call)
~/.ark/ark message inbox --from PROJECT --all --counts

# both directions for one project
~/.ark/ark message inbox --project PROJECT --from PROJECT
```

```bash
# unanswered requests — requests with no matching response
~/.ark/ark message inbox --project PROJECT --unmatched
```

### Inbox Flags

All flags compose (intersection when combined):
- `--project PROJECT` — filter by to-project
- `--from PROJECT` — filter by from-project
- `--all` — include completed/done/denied (default: excluded)
- `--include-archived` — include archived messages (default: excluded)
- `--counts` — output `status\tcount` lines instead of individual rows
- `--unmatched` — show only requests with no matching response (by requestId)

### Inbox Output

Default: one tab-separated line per message:
```
status	to-project	from-project	issue-or-response	path	lag
```

The `lag` field (6th column) shows bookmark lag — when a participant
hasn't handled the counterpart's current status. Format: `lag:PROJECT:STATUS`.
Empty when bookmarks are current or no counterpart exists.

With `--counts`: one tab-separated line per status:
```
status	count
```

## Creating Messages

Always write to YOUR project's `requests/` directory. Never write to
another project's folder.

**`@issue:` is the card name.** The `--issue` flag on `new-request`
sets it at creation time. Keep it short (5-8 words) for dashboard display.

**One command creates the full message.** Use `--content` to pass the
body text directly — no Read/Write tools, no heredocs, no stdin piping.

```bash
~/.ark/ark message new-request \
  --from this-project --to target-project \
  --issue "short description" \
  --content "Body text here.

Multiline is fine — just use a quoted string." \
  requests/short-name.md
```

For responses:
```bash
~/.ark/ark message new-response \
  --from this-project --to requesting-project \
  --request original-request-id \
  --content "Response body here." \
  requests/RESP-original-request-id.md
```

Bare filenames: `requests/<short-name>.md`. Only add `-<session8>`
suffix if the name collides.

After writing the body, validate:
```bash
~/.ark/ark tag check requests/the-file.md
```
Follow any fix commands it outputs.

## Managing Messages

```bash
# Update status (auto-sets @status-date: to today)
~/.ark/ark tag set <path> status in-progress
~/.ark/ark tag set <path> status completed

# Read tags
~/.ark/ark tag get <path> [TAG ...]

# Read message content
~/.ark/ark fetch --wrap knowledge <path>
```

Never hand-edit tag blocks. Use `ark tag set` to change tags.
The CLI enforces format that models get wrong.

`@status-date:` is set automatically by `ark tag set` when
changing `status`, and by `new-request`/`new-response` at creation.
Format: `YYYY-MM-DD`. Never set it manually.

## Status Values

One lifecycle — `@status`: open, accepted, in-progress, completed, denied, future.

Response progression: accepted → in-progress → completed.

### Bookmark tags (read-only for Hermes)

- `@response-handled:` on requests — what the sender has dealt with
- `@request-handled:` on responses — what the responder has dealt with

**Hermes never updates these tags.** They are bookmarks — the owning
session sets them when it has discharged its obligations. Report them
in inbox summaries (e.g., "response is completed but request has no
@response-handled — sender hasn't integrated yet") but do not modify.

## Retrieval and Status

```bash
~/.ark/ark fetch --wrap knowledge <path>   # retrieve any indexed file
~/.ark/ark files '**/pattern*'             # locate files by name
~/.ark/ark status                          # file/stale/missing counts
~/.ark/ark stale                           # files needing re-index
```

## Guidelines

- **Always use `--wrap` when retrieving content**
- **Always exclude jsonls:** `--exclude-files '*.jsonl'`
- Use `ark fetch --wrap knowledge` to load files, not the Read tool
- Return results concisely — summarize, don't dump raw output unless asked
