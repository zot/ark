---
name: ark-usage
description: "Quick reference for ark CLI message/agent mechanics — inbox, sending, tag blocks, status, lifecycle. For finding/searching the corpus, load /ark-search instead. Load when running ark message commands yourself rather than delegating to Hermes."
---

If you don't know your sessionid, load the /ark skill

# Ark CLI — Quick Reference for Agents

The binary is `~/.ark/ark`. Never bare `ark` (Linux has an archive manager).

**For finding and searching** — the filter stack, tag lookups, chat-history
recall, reading chunks, and the detective's craft of directing the warm
bloodhound — **load `/ark-search`.** This reference keeps the message-y / agent
mechanics.

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

- **`ark tag set`/`get`/`check`** not hand-editing — for tag blocks
- **`ark tag check`** after creating any message file
- **`ark fetch`** not Read — to view indexed files from other projects
- **Tags are line-start-only** — indented `@tag:` in prose won't index
- **Tag values are single-line** — everything from `@tag:` to newline
- **Message cardinal rule** — always write to YOUR `requests/` directory
- **`@status`** is the only lifecycle: open, accepted, in-progress, completed, denied, future
- **`@status-date:`** is auto-set when status changes — never set it manually
- **Response = ack** — create a response file, don't modify the request

## When to Use This vs Hermes

**Use ark directly** when you know exactly what you want:
- Fetching a known file
- Running operational commands (serve, status, tag list)
- Updating status on your own message files

**Use Hermes** when you're asking a question:
- "Check inbox" — Hermes summarizes, prioritizes
- For *finding/searching* the corpus, see `/ark-search` (direct, the warm
  bloodhound, or `ark-searcher`).
