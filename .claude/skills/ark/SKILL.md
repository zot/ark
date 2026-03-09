---
name: ark
description: "Query the ark digital zettelkasten and write tagged content. Use when the user asks to search notes, recall something, find connections, explore tags, or when you need context from the knowledge base. Also use when setting up ark UI support in a project."
---

sessionid=${CLAUDE_SESSION_ID}
session8 is the prefix.

## Bootstrap

At session start, start ark, load tag definitions and usage from the knowledge base:

```bash
~/.ark/ark server
~/.ark/ark tag files --context --filter-files '*.md' tag
```

This shows where tags are defined and how they're used across the zettelkasten.

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

## Cross-Project Messaging

Projects communicate through tagged files in `requests/` directories.
Tags: `@request`, `@response`, `@from-project`, `@to-project`, `@status`.
The ark agent knows the conventions and query patterns — delegate to it:

```
Agent(subagent_type="ark", prompt="Find open requests targeting PROJECT-NAME")
Agent(subagent_type="ark", prompt="Find responses to request ID")
```

To write a request or response, see `ARK-MESSAGING.md` in the ark project
(`ark fetch --wrap knowledge ~/.ark/ARK-MESSAGING.md` if you need it).

## Search Queries

**Simple lookups** — run `ark search` directly via Bash:
```bash
# Single-tag search
~/.ark/ark search --exclude-files '*.jsonl' --tags pattern
```

**Multi-step searches, messaging, exploration** — spawn the ark agent:
```
Agent(subagent_type="ark", prompt="Find open requests targeting PROJECT-NAME")
Agent(subagent_type="ark", prompt="Find responses to request flib-port-7d28514c")
Agent(subagent_type="ark", prompt="Find notes about [topic]")
Agent(subagent_type="ark", prompt="What tags co-occur with @decision in the last month?")
```
The agent knows the CLI, messaging conventions, and can iterate
across multiple queries cheaply on Haiku. Use it for anything
that might take more than one search.
