# Hermes Messaging Reference

Your tools are `~/.ark/ark` commands. Do not use grep, awk, wc, find,
or shell loops when an ark command exists.

The database is at `~/.ark`. The ark command is at `~/.ark/ark`.

**Never start, stop, or restart the server.** That is not your job — the
guard blocks `ark serve`/`stop`/`restart`. Creating and checking message
files writes and reads files directly and does not need the server (the file
in `requests/` is the whole message; it indexes when the server next runs).
If a command reports the server is down, stop and report that plainly — do
not run `ark serve`.

## Inbox Commands

Pick the right scope. The three project flags compose as
intersection — `--to ark --from microfts2` shows only the
microfts2→ark slice.

```bash
# all messages involving a project (both directions, non-completed)
~/.ark/ark message inbox --project PROJECT

# incoming only — addressed TO this project
~/.ark/ark message inbox --to PROJECT

# outgoing only — sent FROM this project
~/.ark/ark message inbox --from PROJECT

# counts by status (one row per status)
~/.ark/ark message inbox --project PROJECT --counts
~/.ark/ark message inbox --to PROJECT --counts
~/.ark/ark message inbox --from PROJECT --counts

# include completed/denied messages
~/.ark/ark message inbox --project PROJECT --all
~/.ark/ark message inbox --project PROJECT --all --counts

# unanswered requests TO this project (incoming, no response yet)
~/.ark/ark message inbox --to PROJECT --unmatched

# unanswered requests FROM this project (outbound, awaiting response)
~/.ark/ark message inbox --from PROJECT --unmatched

# unanswered requests in either direction
~/.ark/ark message inbox --project PROJECT --unmatched
```

### Inbox Flags

All flags compose (intersection when combined):
- `--project PROJECT` — filter by EITHER `to-project` OR `from-project` (both directions)
- `--to PROJECT` — filter by `to-project` (incoming only)
- `--from PROJECT` — filter by `from-project` (outgoing only)
- `--all` — include completed/denied (default: excluded)
- `--include-archived` — include archived messages (default: excluded)
- `--counts` — output `status\tcount` lines instead of individual rows
- `--unmatched` — show only requests with no matching response (by requestId).
  Pair lookup is global — works correctly with `--to`, `--from`, and
  `--project`. The filter selects which unmatched requests are
  displayed; the matcher always sees the full inbox.

### Inbox Output

First line is a date header: `# inbox YYYY-MM-DD`

Then one tab-separated line per message, sorted by date descending
(most recent first, undated entries last):
```
date	status	to-project	from-project	issue-or-response	path	lag
```

The `date` column (1st) is the most recent `@status-date:` from
either the request or its paired response. Shows `-` if neither
has a date.

The `lag` field (7th column) shows bookmark lag — when a participant
hasn't handled the counterpart's current status. Format: `lag:PROJECT:STATUS`.
Empty when bookmarks are current or no counterpart exists.

With `--counts`: one tab-separated line per status:
```
status	count
```

## Creating Messages

Always write to YOUR project's `requests/` directory. Never write to
another project's folder.

**Transcribe, don't edit.** When you are handed the issue line and the
body, pass them through `--issue` and `--content` exactly as given — byte
for byte. Never shorten, summarize, drop a parenthetical, or reword to
"improve" them. You carry the message without altering what it says; a
messenger who garbles the message wastes the researcher's time.

**Verbatim via a file beats retyping.** When the caller hands you a *file
path* for the issue line or the body (because the exact wording matters),
pass it straight through with `--issue-file PATH` / `--content-file PATH`
instead of copying the text into `--issue`/`--content`. The CLI reads the
file byte-for-byte, so you never retype — and so cannot alter — the payload.
Each `*-file` flag is mutually exclusive with its inline twin.

**`@issue:` is the card name.** The `--issue` flag on `new-request` sets it
at creation time. When you are *composing* a fresh issue from a vague
description, aim for 5-8 words for dashboard display — but a caller-provided
issue line is preserved verbatim even when it runs longer. Fidelity beats
brevity.

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

**Validate every message file you create.** This is not optional —
malformed tag blocks become silent index drift.
```bash
~/.ark/ark message check requests/the-file.md
```
`message check` is the message-aware form (it knows the expected
heading list). If it reports problems, fix them with `ark tag set`
or rewrite the file via `new-request`/`new-response`. Common
failure modes:
- Blank lines inside the tag block — must be a contiguous run of
  `@name: value` lines with a single blank line separating the
  block from the body.
- Tags written without a space after the colon (`@area:lua`).
- Tags inside the body instead of the top block.

Pass `--content` to `new-request`/`new-response` rather than
hand-writing the file; the CLI lays down the canonical shape.

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

**Never invent statuses.** If `ark message inbox` shows messages
with `done`, `resolved`, `shipped`, or any other value outside the
list above, those are legacy artifacts from earlier lifecycles.
Report them by their actual value when summarizing — do not silently
normalize to `completed` and do not change them. Surface the
mismatch so the owning session can decide.

The `tag set` command accepts whatever string you give it; the
guard is here, not in the CLI.

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
