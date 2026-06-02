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
| `-fuzzy TERM`    | trigram similarity (generous)                          |
| `-regex PAT`     | Go RE2 (no lookahead/backreferences)                   |
| `-tag TAG`       | tag filter (uses tag index, fast)                      |
| `-file-tag TAG`  | every chunk on a file carrying the tag                 |
| `-about QUERY`   | vector similarity (server required)                    |
| `-files GLOB`    | file path glob (doublestar `/**/` supported)           |

Polarity is sticky until changed: `-with` (must match, default) /
`-without` (subtract). Tag sigils: `name:value` = value-contains,
`name=value` = value-exact, bare `name` = any value.

**Match the matcher to the query.** Use `-contains` for an exact,
distinctive phrase. `-fuzzy` is trigram similarity: it tolerates typos
*and* medium-length phrases, but it is generous, and the largest prose
corpus in the index can dominate results for any common-word query.
Reach for it when you have a specific approximate term and can tolerate
noise. `-about` is semantic but needs the server and is best-effort:
vectors are progressive while trigram stays primary, so never rely on
it as the only pass.

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

### Searching conversation history (recall)

Conversation logs (`~/.claude/projects/**`) and schedule logs are
**excluded from search by default** via `[search].search_exclude` — right
for normal corpus search, but it means a plain `ark search` never returns
chat history. When the *point* of the search is what was *discussed*
(recall, clues, "did we mention X?"), do a **second, chat-scoped pass**:

```bash
# Pass 1 — corpus (chat logs auto-excluded):
~/.ark/ark search -fuzzy "cerro gordo" -scores -k 20

# Pass 2 — chat history (the positive -files both scopes to chat AND
# disables the default exclude, so the logs surface):
~/.ark/ark search -files '~/.claude/projects/**' -fuzzy "cerro gordo" -scores -k 20
```

Why the `-files` is the trick: an explicit positive `-files` narrowing
turns OFF the `search_exclude` defaults — so the one flag that scopes to
chat is also what un-hides it.

- **Two passes, don't merge them.** Fuzzy scores saturate at `1.0000` for
  short queries (too few trigrams to separate a near-match from an exact
  one), so a single combined search can't sort the corpus and chat pools
  into a useful mix — one drowns the other. Keep them separate; merge with
  judgment.
- Single-quote globs so the shell doesn't expand them; ark expands a
  leading `~/` itself (R950), so `'~/.claude/projects/**'` works.

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

- **Conversation logs are excluded by default** — `[search].search_exclude` (`~/.claude/projects/**`, `~/.ark/schedule/**`) is applied automatically, so you don't exclude them by hand. To *include* chat history, see "Searching conversation history" above (a positive `-files` pass)
- **`-fuzzy` is generous (trigram similarity).** A large project can swamp common-word queries; tighten the query, or use `-contains` for an exact phrase. Note short queries (≤~3 trigrams) saturate at score `1.0000` — the score won't discriminate, so judge by content
- **`-files` globs anchor at the full path's start.** A pattern beginning with a literal segment (`'HollowStuff/**'`) silently matches nothing, since real paths start with `/home/...`; prefix interior dirs with `**/` (`'**/HollowStuff/**'`). Extension globs (`'*.jsonl'`) already lead with a wildcard, so they work as-is
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
