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
Agent(subagent_type="ark-hermes", prompt="Check inbox for PROJECT_NAME. Write a summary to requests/summary.md — counts, what's new, what's waiting, what's stale. Plain markdown, no tag block.")
```
Then:
```
Agent(subagent_type="ark-franklin", prompt="Morning sweep for PROJECT_NAME. Read requests/summary.md for the inbox state. What needs attention today?")
```

Replace PROJECT_NAME with the current project. Hermes leaves the
summary at the drop point; Franklin reads it and asks the daily question.
Neither agent knows the other exists.

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

**Mail room** — spawn ark-hermes. Inbox checks, sending messages,
acknowledging, searching, research. Hermes gathers and carries.
```
Agent(subagent_type="ark-hermes", prompt="Check inbox for ark. Write summary to requests/summary.md")
Agent(subagent_type="ark-hermes", prompt="Send a request from ark to microfts2 about chunker interface")
Agent(subagent_type="ark-hermes", prompt="Find notes about append detection")
Agent(subagent_type="ark-hermes", prompt="Ack the microfts2 chunk-context notification")
```

## Cross-Project Messaging

Projects communicate through tagged files in `requests/` directories.

Two lifecycle tags:
- `@status` — work state: open, in-progress, done, declined
- `@msg` — delivery state: new, read, acting, closed

**Franklin manages commitments.** Inbox, daily narrowing, what needs doing.
**Hermes carries messages.** Creating requests/responses, finding conversations
across projects, searching the knowledge base.

Messages always live in YOUR project's `requests/` directory — never write
to another project's folder. Use `ark message` commands, never hand-edit tags.
