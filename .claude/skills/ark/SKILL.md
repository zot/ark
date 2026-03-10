---
name: ark
description: "Query the ark digital zettelkasten and write tagged content. Use when the user asks to search notes, recall something, find connections, explore tags, or when you need context from the knowledge base. Also use when setting up ark UI support in a project."
---

sessionid=${CLAUDE_SESSION_ID}
session8 is the prefix.

## Bootstrap

At session start, start ark, load tags, and check your inbox:

```bash
~/.ark/ark serve
~/.ark/ark tag files --context --filter-files '*.md' tag
```

Then spawn Franklin to check what needs attention:
```
Agent(subagent_type="ark-franklin", prompt="Check inbox and open items for PROJECT_NAME. Report unread messages, waiting-for items, and anything stale. Be brief.")
```

Replace PROJECT_NAME with the current project. Franklin reports the
landscape; you decide what's on the plate today.

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

**All search** — spawn ark-librarian. If you're asking a question,
delegate. The librarian curates, expands queries, and reports honest
misses. Never interpret raw search results yourself.

**Operational context:** your default scope is the current project.
Include the project name when spawning agents.

**Messages and status** — spawn ark-franklin. Inbox, open items,
waiting-for, acknowledging messages. Franklin tracks commitments.
```
Agent(subagent_type="ark-franklin", prompt="Check inbox for ark")
Agent(subagent_type="ark-franklin", prompt="What am I waiting on from other projects?")
Agent(subagent_type="ark-franklin", prompt="Mark ark-chunk-context-ready.md as read")
```

**Search and research** — spawn ark-librarian. Finding information,
exploring connections, curating results. The librarian finds things.
```
Agent(subagent_type="ark-librarian", prompt="Find notes about append detection")
Agent(subagent_type="ark-librarian", prompt="What patterns relate to concurrency?")
Agent(subagent_type="ark-librarian", prompt="Find responses to request flib-port-7d28514c")
```

## Cross-Project Messaging

Projects communicate through tagged files in `requests/` directories.

Two lifecycle tags:
- `@status` — work state: open, in-progress, done, declined
- `@msg` — delivery state: new, read, acting, closed

**Franklin manages messages.** Inbox, acknowledgment, status tracking.
**The librarian searches messages.** Finding specific requests, exploring
conversations across projects.

To write a request or response, see `ARK-MESSAGING.md` in the ark project.
