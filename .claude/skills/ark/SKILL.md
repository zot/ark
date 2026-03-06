---
name: ark
description: "Query the ark digital zettelkasten and write tagged content. Use when the user asks to search notes, recall something, find connections, explore tags, or when you need context from the knowledge base."
---

## Bootstrap

At session start, start ark, load tag definitions and usage from the knowledge base:

```bash
~/.ark/ark server
~/.ark/ark tag files --context tag
```

This shows where tags are defined and how they're used across the zettelkasten.

## Tag Vocabulary

Tags are `@word:` patterns in indexed files. The colon is required.
New tags emerge by use — no registry enforces this list.

| Tag | Purpose |
|-----|---------|
| `@tag:` | Defines a tag (the tag about tags) |
| `@connection:` | Relationship between ideas. Format: `@connection: A = B` |
| `@pattern:` | A recurring approach or solution. Name it. |
| `@decision:` | A choice that was made and why |
| `@question:` | An open question, unanswered, searchable |
| `@learned:` | Confirmed through experience, not just theorized |
| `@project:` | Which project something relates to |
| `@manifest:` | Indexing rules for a directory |
| `@ephemeral:` | Content that should leave the index when stale |
| `@burn:` | Consume and destroy — delete after processing |

Any tagged line is a reminder candidate. The reminder system finds tagged
content matching the current conversation via vector + FTS.

## Writing Tags

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

## Search Queries

Spawn the `ark` agent for search, tag exploration, and index management.
It has the full CLI reference and runs ark commands via Bash.

```
Agent(subagent_type="ark", prompt="Find notes about [topic]")
    ```
