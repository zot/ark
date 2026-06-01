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

**Search uses a filter stack, not standalone filter flags.** The first
filter is the primary search; the rest are chunk-level post-filters.
Filters are composable and repeatable. Bare terms coalesce into a single
`-contains`. Run `-parse` to see exactly how your args were interpreted.

Filter modes (each consumes the next arg as its query):

| Mode             | Match                                                  |
|------------------|--------------------------------------------------------|
| `-contains TERM` | substring (default for bare terms)                     |
| `-fuzzy TERM`    | typo-tolerant                                          |
| `-regex PAT`     | Go RE2 (no lookahead/backreferences)                   |
| `-tag TAG`       | tag filter (uses tag index, fast)                      |
| `-file-tag TAG`  | every chunk on a file carrying the tag                 |
| `-about QUERY`   | vector similarity (server required)                    |
| `-files GLOB`    | file path glob (doublestar `/**/` supported)           |

Polarity is sticky until changed: `-with` (must match, default) /
`-without` (subtract). Tag sigils: `name:value` = value-contains,
`name=value` = value-exact, bare `name` = any value.

```bash
# Bare terms coalesce to -contains
~/.ark/ark search fred ethel

# Exclude conversation-log noise (the common one — jsonl floods results)
~/.ark/ark search QUERY -without -files '*.jsonl'

# Primary FTS + regex chunk-level post-filter
~/.ark/ark search -contains "phrase" -regex '@tag:.*value'

# Combine: search, drop done items, markdown only
~/.ark/ark search fred -without -tag status:done -with -files '*.md'

# Only chunks on files carrying a tag / exclude files carrying a tag
~/.ark/ark search QUERY -with -file-tag status
~/.ark/ark search QUERY -without -file-tag msg

# Vector similarity (needs server)
~/.ark/ark search -about "machine learning" -without -tag project:archive

# Verify parse without searching
~/.ark/ark search -parse fred -without -files '*.md'
```

Output options (conventional flags, after the filter stack):

```bash
~/.ark/ark search QUERY -wrap knowledge          # XML-wrapped (best for context injection)
~/.ark/ark search QUERY -chunks                  # chunk text as JSONL
~/.ark/ark search QUERY -file-content            # full file text as JSONL
~/.ark/ark search QUERY -tags                    # extracted @tag activity as bullets
~/.ark/ark search QUERY -k 50 -scores            # cap results, show scores
~/.ark/ark search QUERY -chunks -preview 200     # preview window around match

# Expand context around a hit (separate command)
~/.ark/ark chunks /path/to/file 150-175 -before 2 -after 2
```

## Messages

```bash
# Check inbox
~/.ark/ark message inbox --project PROJECT

# Unanswered requests (no matching response file)
~/.ark/ark message inbox --project PROJECT --unmatched

# Read a message
~/.ark/ark fetch --wrap knowledge /path/to/message.md

# Create a request (always write to YOUR project's requests/)
# Use bare name; add -SESSION8 suffix only if name collides
# Auto-sets @status-date: to today
~/.ark/ark message new-request \
  --from THIS-PROJECT --to TARGET-PROJECT \
  --issue "short description" \
  requests/short-name.md

# Create a response (response = ack, auto-sets @status-date:)
~/.ark/ark message new-response \
  --from THIS-PROJECT --to TARGET-PROJECT \
  --request ORIGINAL-ID \
  requests/RESP-original-id.md

# Set tags on any file with a tag block
# Setting "status" auto-sets @status-date: to today
~/.ark/ark tag set FILE status completed

# Read tags
~/.ark/ark tag get FILE

# Validate format (do this after creating/editing)
~/.ark/ark tag check FILE
```

### Inbox output

Default output is tab-separated:
```
date  status  to-project  from-project  summary  path  lag
```

The `lag` field shows bookmark lag (empty when current, otherwise
`lag:PROJECT:STATUS` showing who is behind and what they haven't handled).

## Gotchas

- **Always `-without -files '*.jsonl'`** unless you want conversation logs (they flood results)
- **Always wrap retrieved content** (`-wrap` on search, `--wrap` on fetch) — gives source attribution
- **`ark tag defs`** not grep — to find tag definitions
- **`ark fetch`** not Read — to view indexed files from other projects
- **`ark tag set`/`get`/`check`** not hand-editing — for tag blocks
- **`ark tag check`** after creating any message file
- **Tags are line-start-only** — indented `@tag:` in prose won't index
- **Tag values are single-line** — everything from `@tag:` to newline
- **Message cardinal rule** — always write to YOUR `requests/` directory
- **`@status`** is the only lifecycle: open, accepted, in-progress, completed, denied, future
- **`@status-date:`** is auto-set when status changes — never set it manually
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
