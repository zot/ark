---
name: ark
description: "Query the ark digital zettelkasten and write tagged content. Use when the user asks to search notes, recall something, find connections, explore tags, or when you need context from the knowledge base. Also use when setting up ark UI support in a project."
---

sessionid=${CLAUDE_SESSION_ID}
session8 is the prefix.

## Bootstrap

At session start, start ark, load tags, then run the dead drop:

```bash
~/.ark/ark serve &
~/.ark/ark tag files --context --filter-files '*.md' tag
```

Then the two-step morning sweep — Hermes gathers, Franklin narrows:
```
Agent(subagent_type="ark-messenger", prompt="Check inbox for PROJECT_NAME. Report incoming messages, outgoing counts by status, and what's new or stale.")
```
Then:
```
Agent(subagent_type="ark-franklin", prompt="Morning sweep for PROJECT_NAME. Read requests/summary.md for the inbox state. What needs attention today?")
```

Replace PROJECT_NAME with the current project. The messenger's
SessionStart hook auto-loads its skill reference. Franklin reads
the summary and asks the daily question. Neither agent knows the
other exists.

## Tags

Tags are `@word:` patterns in indexed files. The colon is required.
Everything after the colon to end of line is the tag's content — this
is what gets indexed and searched. The bootstrap command above loads
all tag definitions from the knowledge base. New tags emerge by use. Any tagged line is a reminder candidate —
the reminder system finds tagged content matching the current
conversation via vector + FTS.

### Writing Tags

**Tags match to end of line.** Never break a tag across lines — the
content after the colon through the newline is one atomic unit.
Wrapping a tag line splits the content and loses everything after
the break.

In markdown, bare on its own line (no line breaks):
```
@connection: recall agent context isolation = closure-actor private state
```

In code, inside block comments so the tag starts on its own line:
```go
/*
@pattern: closure-actor
@decision: use LMDB for index — single writer, crash safe
*/
```

## Project Setup

To install ark UI support in a particular project, run:

```bash
~/.ark/ark ui install
```

This creates skill symlinks in the project's `.claude/skills/`, sets up the database if needed, and prints a CLAUDE.md snippet to paste. Run it once per project.

## Search Before You Design

Before planning or building a feature, ask ark what it already knows.
The zettelkasten connects dots across projects, old plans, and design
conversations you may have forgotten.

```bash
~/.ark/ark search --contains "topic keywords" --chunks --wrap recall --exclude-files '*.jsonl'
```

`--wrap recall` marks retrieved content as stored knowledge. `--chunks`
gives you the relevant passages, not just file paths. Read the results
before writing anything — they often surface context that changes the
approach.

## Routing

Three categories — route by what you're doing, not by complexity:

**Operational commands** — direct Bash. Mechanical, no judgment:
```bash
~/.ark/ark serve
~/.ark/ark status
~/.ark/ark tag list
```

**Context loading** — direct Bash. You're loading data into yourself,
not asking a question:
```bash
~/.ark/ark tag files --context --filter-files '*.md' tag
~/.ark/ark fetch --wrap knowledge <path>
```

**Messaging** — spawn ark-messenger. Inbox, sending, ack, status changes.
Hermes curates and reports honest misses.
**Send in background** — sending is fire-and-forget, don't block the
conversation. Inbox checks need the result, so run foreground.
**Be explicit about direction.** Hermes is Haiku — it answers what
you ask, not what you meant. Say exactly what you need:
- "unanswered requests to PROJECT" — `--unmatched` does this in one command now
- "outbound requests FROM project still open" — waiting on others
- "all messages involving project" — full picture
- "messages with bookmark lag" — who needs to catch up
```
Agent(subagent_type="ark-messenger", run_in_background=true, prompt="Send a request from ark to microfts2 about chunker interface.")
Agent(subagent_type="ark-messenger", prompt="Check inbox for ark. Report incoming, outgoing counts, what's new or stale. Use --unmatched for unanswered items.")
```

**Search** — spawn ark-searcher. Finding notes, exploring tags, retrieval.
Hermes expands queries and curates results. Never interpret raw search
results yourself.
```
Agent(subagent_type="ark-searcher", prompt="Find notes about append detection.")
Agent(subagent_type="ark-searcher", prompt="What tags relate to concurrency patterns?")
```

**What needs doing** — spawn ark-franklin. The daily question, narrowing,
"what should I work on." Franklin reads the landscape and helps you cut.
```
Agent(subagent_type="ark-franklin", prompt="Morning sweep for ark. Read requests/summary.md for inbox state.")
Agent(subagent_type="ark-franklin", prompt="I finished the chunker interface. What's next?")
```

**Operational context:** your default scope is the current project.
Include the project name when spawning agents.

Each Hermes agent auto-loads its skill reference via a SessionStart
hook — the caller no longer needs to include a fetch preamble.
Hermes is a persona, not an expert — the hook is the GM explaining
the rules every session, every time.

## Cross-Project Messaging

Projects communicate through tagged files in `requests/` directories.
See ARK-MESSAGING.md for full protocol.

Message identity tags: `@ark-request: <id>` and `@ark-response: <id>`.
The `ark-` prefix avoids collision with generic uses of "request"/"response".
Other tags in the block (`@from-project:`, `@to-project:`, `@status:`,
`@issue:`) are generic — unambiguous once the discriminator is present.

One lifecycle tag — `@status`: open, accepted, in-progress, completed, denied, future.

**`@status-date:`** — set automatically when status changes or message is created. Format: `YYYY-MM-DD`. Never set manually.

**`@issue:`** — short description, also used as card name in the dashboard.

**Response = ack.** Creating a response file with `@status: accepted`
means "I saw it." No cross-project file writes, ever.

### Bookmark tags

Each side tracks what it has **dealt with** from the counterpart:

- `@response-handled:` on requests — the response status the sender has processed
- `@request-handled:` on responses — the request status the responder has processed

The bookmark marks your place. The gap between where the bookmark is
and where the counterpart has moved is unfinished business — Franklin
surfaces these as reminders. **Hermes never updates any status tag.**
Only the owning session updates when obligations are discharged.

**Setting the value:** the bookmark should reflect how far along you
are in processing the counterpart's state — not what you saw, but
the farthest status level you've reached given the work you've done.
If the response is `completed` and you've read it and started
integration, set `@response-handled: in-progress`. When integration
is done, set it to `completed`. The bookmark tracks your progress,
not your awareness.

See ARK-MESSAGING.md for the full lifecycle example.

Filenames: bare `<short-name>.md`. Only add `-<session8>` suffix if the name collides.

**Franklin manages commitments.** Inbox, daily narrowing, what needs doing.
**ark-messenger carries messages.** Creating requests/responses, status changes.
**ark-searcher finds things.** Searching the knowledge base, finding conversations
across projects, exploring tags.

Messages always live in YOUR project's `requests/` directory — never write
to another project's folder. Use `ark message` commands, never hand-edit tags.

**Reference tags** (for citing messages in any file):
- `@ark-request-sent: <path>` — a request was sent from this planning item
- `@ark-request-ref: <path-or-id>` — see this request
- `@ark-response-ref: <path-or-id>` — see this response

**Audit trail:** When sending a cross-project request, tag the planning or
tracking file that motivated it with `@ark-request-sent: requests/foo.md`
near the relevant item. This makes the link searchable — `ark search --regex
'@ark-request-sent:'` finds every planning item with a pending request,
across all projects.
