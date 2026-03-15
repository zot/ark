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

### Inbox Flags

All flags compose (intersection when combined):
- `--project PROJECT` — filter by to-project
- `--from PROJECT` — filter by from-project
- `--all` — include completed/done/denied (default: excluded)
- `--include-archived` — include archived messages (default: excluded)
- `--counts` — output `status\tcount` lines instead of individual rows

### Inbox Output

Default: one tab-separated line per message:
```
status	to-project	from-project	issue-or-response	path
```

With `--counts`: one tab-separated line per status:
```
status	count
```

## Creating Messages

Always write to YOUR project's `requests/` directory. Never write to
another project's folder.

**Use heredoc to include body content.** The command reads stdin until
a lone `.` on a line or EOF. Write the body naturally — no quoting,
no escaping needed.

```bash
# Send a request with body
~/.ark/ark message new-request \
  --from this-project --to target-project \
  --issue "short description" \
  requests/short-name.md <<'BODY'
Detailed description goes here.

Multiple paragraphs work. Use markdown freely.

## Subsections are fine

Tables, lists, code blocks — whatever the message needs.
.
BODY

# Acknowledge a request with body
~/.ark/ark message new-response \
  --from this-project --to requesting-project \
  --request original-request-id \
  requests/RESP-original-request-id.md <<'BODY'
Response content here.
.
BODY
```

The `.` line ends the body. Everything between the heredoc markers
becomes stdin. Without a heredoc, the command creates the scaffold
with no body (tags + heading only).

Bare filenames: `requests/<short-name>.md`. Only add `-<session8>`
suffix if the name collides.

After creating, validate:
```bash
~/.ark/ark message check requests/the-file.md
```
Follow any fix commands it outputs.

## Managing Messages

```bash
# Update status
~/.ark/ark message set-tags <path> status in-progress
~/.ark/ark message set-tags <path> status completed

# Read tags
~/.ark/ark message get-tags <path> [TAG ...]

# Read message content
~/.ark/ark fetch --wrap knowledge <path>
```

Never hand-edit tag blocks. Use `ark message set-tags` to change tags.
The CLI enforces format that models get wrong.

## Status Values

One lifecycle — `@status`: open, accepted, in-progress, completed, denied, future.

Response progression: accepted → in-progress → completed.

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
