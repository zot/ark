---
name: ark
description: "Query the ark digital zettelkasten and write tagged content. Use when the user asks to search notes, recall something, find connections, explore tags, or when you need context from the knowledge base. Also use when setting up ark UI support in a project."
---

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

In markdown, bare on its own line:
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

## Search Queries

Spawn the `ark` agent for search, tag exploration, and index management.
It has the full CLI reference and runs ark commands via Bash.

```
Agent(subagent_type="ark", prompt="Find notes about [topic]")
```
