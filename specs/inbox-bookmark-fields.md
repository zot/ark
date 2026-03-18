# Inbox Bookmark Fields

Language: Go. Environment: CLI + embedded UI (part of the `ark` binary).

The `mcp:inbox()` Lua function (used by the Frictionless messaging
dashboard) returns message entries from `DB.Inbox()`. The dashboard
needs two additional fields to compute bookmark lag — whether a
participant has dealt with the counterpart's current status.

See ARK-MESSAGING.md v3 "Cross-status tracking (bookmarks)" for the
full protocol.

## response-handled field

Each inbox entry includes a `responseHandled` field — the value of
the `@response-handled:` tag from the file's tag block. This tag
appears on request files and records the response status the
requester has dealt with. If the tag is absent, the field is empty.

## request-handled field

Each inbox entry includes a `requestHandled` field — the value of
the `@request-handled:` tag from the file's tag block. This tag
appears on response files and records the request status the
responder has dealt with. If the tag is absent, the field is empty.

## Lua pipe

The `mcp:inbox()` function passes `responseHandled` and
`requestHandled` through to Lua as string fields on each entry
table, alongside the existing `status`, `to`, `from`, `summary`,
`path`, `requestId`, and `kind` fields.

## Bookmark lag computation

The Lua UI code (already implemented) compares these fields against
the counterpart's `status` to determine whether a participant is
behind. This is display-only — no status mutation occurs in the
inbox query path.
