---
name: ark
description: "Query the ark digital zettelkasten and write tagged content. Use when the user asks to search notes, recall something, find connections, explore tags, or when you need context from the knowledge base. Also use when setting up ark UI support in a project."
---

sessionid=${CLAUDE_SESSION_ID}
session8 is the prefix.

## Bootstrap

At session start, start ark, load tags, then run the dead drop:

```bash
~/.ark/ark serve
~/.ark/ark tag files --context --filter-files '*.md' tag
```

Then the two-step morning sweep — Hermes gathers, Franklin narrows:
```
Agent(subagent_type="ark-hermes", prompt="First, run this command exactly:\n~/.ark/ark fetch --wrap knowledge ~/.ark/skills/hermes-messaging.md\n\nThen, using only the commands from that reference:\nCheck inbox for PROJECT_NAME. Report incoming messages, outgoing counts by status, and what's new or stale.")
```
Then:
```
Agent(subagent_type="ark-franklin", prompt="Morning sweep for PROJECT_NAME. Read requests/summary.md for the inbox state. What needs attention today?")
```

Replace PROJECT_NAME with the current project. Hermes fetches its
own skill reference first — the caller must always include the fetch
instruction because Haiku won't do it on its own. Franklin reads
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

**All search** — spawn ark-hermes. If you're asking a question,
delegate. Hermes curates, expands queries, and reports honest
misses. Never interpret raw search results yourself.

**Operational context:** your default scope is the current project.
Include the project name when spawning agents.

**What needs doing** — spawn ark-franklin. The daily question, narrowing,
"what should I work on." Franklin reads the landscape and helps you cut.
```
Agent(subagent_type="ark-franklin", prompt="Morning sweep for ark. Read requests/summary.md for inbox state.")
Agent(subagent_type="ark-franklin", prompt="I finished the chunker interface. What's next?")
```

**Calling Hermes** — every Hermes prompt MUST start with a skill fetch.
Hermes is a persona, not an expert — it only knows what you hand it.
Like a GM explaining the rules: every session, every time.

For messaging (inbox, sending, ack, status):
```
Agent(subagent_type="ark-hermes", prompt="First, run this command exactly:\n~/.ark/ark fetch --wrap knowledge ~/.ark/skills/hermes-messaging.md\n\nThen, using only the commands from that reference:\nCheck inbox for ark. Report incoming, outgoing counts, what's new or stale.")
```

For search (finding notes, exploring tags, retrieval):
```
Agent(subagent_type="ark-hermes", prompt="First, run this command exactly:\n~/.ark/ark fetch --wrap knowledge ~/.ark/skills/hermes-search.md\n\nThen, using only the commands from that reference:\nFind notes about append detection.")
```

For both (search + messaging in one task):
```
Agent(subagent_type="ark-hermes", prompt="First, run these commands exactly:\n~/.ark/ark fetch --wrap knowledge ~/.ark/skills/hermes-messaging.md\n~/.ark/ark fetch --wrap knowledge ~/.ark/skills/hermes-search.md\n\nThen, using only the commands from those references:\nSend a request from ark to microfts2 about chunker interface.")
```

## Cross-Project Messaging

Projects communicate through tagged files in `requests/` directories.
See ARK-MESSAGING.md for full protocol.

Message identity tags: `@ark-request: <id>` and `@ark-response: <id>`.
The `ark-` prefix avoids collision with generic uses of "request"/"response".
Other tags in the block (`@from-project:`, `@to-project:`, `@status:`,
`@issue:`) are generic — unambiguous once the discriminator is present.

One lifecycle tag — `@status`: open, accepted, in-progress, completed, denied, future.

**Response = ack.** Creating a response file with `@status: accepted`
means "I saw it." No cross-project file writes, ever.

Filenames: bare `<short-name>.md`. Only add `-<session8>` suffix if the name collides.

**Franklin manages commitments.** Inbox, daily narrowing, what needs doing.
**Hermes carries messages.** Creating requests/responses, finding conversations
across projects, searching the knowledge base.

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
