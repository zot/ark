---
name: ark-usage
description: "Quick reference for using ark CLI directly. Load when you need to run ark commands yourself rather than delegating to Hermes. Common patterns, right tool for each job, gotchas."
---

If you don't know your sessionid, load the /ark skill

# Ark CLI — Quick Reference for Agents

The binary is `~/.ark/ark`. Never bare `ark` (Linux has an archive manager).

## Finding Things

```bash
# Tag definitions — where a tag is defined and what it means
~/.ark/ark tag defs --path TAG

# Tag usage — which files use a tag, with context
~/.ark/ark tag files --context TAG

# Tag counts
~/.ark/ark tag list

# File by name pattern (across all projects)
~/.ark/ark files '**/pattern*'

# File contents (any indexed file, any project)
~/.ark/ark fetch --wrap knowledge /path/to/file
```

## Searching

**All search flags compose.** `--contains`, `--regex`, `--filter-files`,
`--exclude-files`, `--filter-file-tags`, `--exclude-file-tags` can all be
used together. `--contains` drives FTS, `--regex` post-filters, file globs
restrict scope, tag filters use the index (fast). File globs support
doublestar (`/**/`) for paths. Regex is Go RE2 (no lookahead, no backreferences).

```bash
# Combined FTS + optional filters
~/.ark/ark search --exclude-files '*.jsonl' QUERY

# Exact text match (FTS-driven, fast)
~/.ark/ark search --contains "exact phrase"

# Regex post-filter (composable with --contains)
~/.ark/ark search --contains "phrase" --regex '@tag:.*value'

# Multiple regex (AND logic), exclude patterns (subtract)
~/.ark/ark search --regex 'pat1' --regex 'pat2' --except-regex 'noise'

# Restrict to specific files
~/.ark/ark search --filter-files 'requests/*.md' QUERY

# Exclude specific files
~/.ark/ark search --exclude-files '*.jsonl' QUERY

# Only files with a specific tag (uses tag index, very fast)
~/.ark/ark search --filter-file-tags status QUERY

# Exclude files with a tag
~/.ark/ark search --exclude-file-tags msg QUERY

# Get chunk text (JSONL output)
~/.ark/ark search --chunks QUERY

# Get full file text (JSONL output)
~/.ark/ark search --files QUERY

# XML-wrapped output (preferred for context injection)
~/.ark/ark search --wrap knowledge QUERY

# Expand context around a hit
~/.ark/ark chunks /path/to/file 150-175 -before 2 -after 2
```

## Messages

```bash
# Check inbox
~/.ark/ark message inbox --project PROJECT

# Read a message
~/.ark/ark fetch --wrap knowledge /path/to/message.md

# Acknowledge: create a response (response = ack)
~/.ark/ark message new-response \
  --from THIS-PROJECT --to SENDER-PROJECT \
  --request REQUEST-ID \
  requests/RESP-request-id.md
# then set @status: accepted on it

# Create a request (always write to YOUR project's requests/)
# Use bare name; add -SESSION8 suffix only if name collides
~/.ark/ark message new-request \
  --from THIS-PROJECT --to TARGET-PROJECT \
  --issue "short description" \
  requests/short-name.md

# Create a response
~/.ark/ark message new-response \
  --from THIS-PROJECT --to TARGET-PROJECT \
  --request ORIGINAL-ID \
  requests/RESP-original-id.md

# Set tags on any file with a tag block
~/.ark/ark tag set FILE status completed

# Read tags
~/.ark/ark tag get FILE

# Validate format (do this after creating/editing)
~/.ark/ark tag check FILE
```

## Gotchas

- **Always `--exclude-files '*.jsonl'`** unless you want conversation logs
- **Always `--wrap`** when retrieving content — gives source attribution
- **`ark tag defs`** not grep — to find tag definitions
- **`ark fetch`** not Read — to view indexed files from other projects
- **`ark tag set`/`get`/`check`** not hand-editing — for tag blocks
- **`ark tag check`** after creating any message file
- **Tags are line-start-only** — indented `@tag:` in prose won't index
- **Tag values are single-line** — everything from `@tag:` to newline
- **Message cardinal rule** — always write to YOUR `requests/` directory
- **`@status`** is the only lifecycle: open, accepted, in-progress, completed, denied, future
- **Response = ack** — create a response file, don't modify the request

## When to Use This vs Hermes

**Use ark directly** when you know exactly what you want:
- Looking up a specific tag definition
- Fetching a known file
- Running operational commands (serve, status, tag list)
- Updating status on your own message files

**Use Hermes** when you're asking a question:
- "Find notes about X" — Hermes expands queries, curates results
- "Check inbox" — Hermes summarizes, prioritizes
- "What do we know about Y" — Hermes checks adjacent shelves
