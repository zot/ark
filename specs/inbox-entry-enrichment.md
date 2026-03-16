# Inbox Entry Enrichment

Language: Go. Environment: CLI + embedded UI (part of the `ark` binary).

The `mcp:inbox()` Lua function (used by the Frictionless messaging
dashboard) returns message entries from `DB.Inbox()`. The dashboard
needs two additional fields to merge request/response pairs into
conversations and to distinguish message types.

## RequestID field

Each inbox entry includes a `requestId` field — the shared identifier
that links a request to its response. Extracted from the
`@ark-request:` or `@ark-response:` tag value. If neither tag exists,
the field is empty.

This enables the UI to group a request and its response into a single
conversation card rather than showing them as separate items.

## Kind field

Each inbox entry includes a `kind` field indicating the message type:
- `"request"` — file has `@ark-request:` tag and different from/to
- `"response"` — file has `@ark-response:` tag
- `"self"` — file has `@ark-request:` tag with same from and to project

This enables the UI to determine which side of a conversation each
entry represents.

## Multi-target normalization

The `@to-project:` tag may contain comma-separated project names
(e.g., `frictionless, ui-engine`). The inbox normalizes this to the
first project name only. The tag value is split on comma and trimmed.

This ensures each inbox entry has a single, clean project name for
filtering and display.

## Lua pipe

The `mcp:inbox()` function passes `requestId` and `kind` through to
Lua as string fields on each entry table, alongside the existing
`status`, `to`, `from`, `summary`, and `path` fields.
