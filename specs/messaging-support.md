# Messaging Support Commands

Language: Go. Environment: CLI (part of the `ark` binary).

Three features that complete the messaging lifecycle for agents
and the dashboard: bookmark crank handles, unmatched detection,
and status timestamps.

## @status-date: automatic timestamps

When `ark message new-request` or `ark message new-response`
creates a message, it sets `@status-date: YYYY-MM-DD` (today's
date) in the tag block.

When `ark tag set` (or its alias `ark message set-tags`) sets
a `status` tag, it also sets `@status-date:` to today's date.
This applies only when the tag name being set is exactly `status`
— other tags don't trigger it.

Format: `@status-date: 2026-03-20` (date only, no time). The
date is always the local date when the command runs.

This enables: sort by age in dashboard, stale message detection,
Franklin daily review.

## ark message inbox --unmatched

```
ark message inbox --unmatched [--project PROJECT]
```

Shows inbound requests that have no corresponding response file
in this project's index. "Inbound" means `@ark-request:` files.
"No response" means no `@ark-response:` file shares the same
request ID in the inbox results.

The matching logic: group all inbox entries by `requestId`. A
request is unmatched if no response entry shares its `requestId`.

`--unmatched` composes with existing filters (`--project`,
`--from`, `--all`, `--include-archived`). The unmatched check
applies after all other filtering.

This is the crank handle for Hermes: one command to answer
"what needs a response?" without diffing two lists.

## Bookmark crank handles in CLI output

The bookmark tags (`@response-handled:`, `@request-handled:`)
are already extracted by `Inbox()` and passed through to the
Lua dashboard. The CLI `ark message inbox` currently doesn't
surface them.

Add bookmark lag to the CLI inbox output: when a message has
stale bookmarks (the handled tag is behind the counterpart's
status), append a lag indicator to the output line.

The lag computation: for each inbox entry, compare
`responseHandled` against the counterpart response's status,
and `requestHandled` against the counterpart request's status.
A mismatch (or empty handled with non-empty counterpart status)
means lag.

This requires the same pairing logic as `--unmatched` — group
by requestId, then compare across the pair. Since both features
need pairing, they share the same grouping pass.

Output format extension: after the existing tab-separated fields,
append a lag field. Empty when no lag, otherwise a compact
indicator like `lag:project:status` showing who is behind and
what they haven't handled.
