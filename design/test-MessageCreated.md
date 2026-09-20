# Test Design: message @created tag
**Source:** specs/messaging.md

Cheap, pure tests over R3439 — the immutable `@created:` date. Creation stamps it
(a pure tag-block helper), and a set-tags update never rewrites it (a pure update
helper). Both properties are invisible when violated: a missing stamp, or a
re-stamped `@created`, looks fine until someone needs the original authoring date
of a response in a thread.

## Test: creation stamps an immutable @created
**Purpose:** `new-request` and `new-response` stamp `@created` = the creation
date, equal to `@status-date` at that moment. (R3439)
**Input:** `newRequestTags` / `newResponseTags` with a fixed `now`.
**Expected:** `@created` == the date, and `@created` == `@status-date` at creation.
**Refs:** crc-CLITree.md
**Code:** cmd/ark/message_cli_test.go
**Alarm:** 1
**Fire alarm:** drop the `tb.Set("created", date)` line in `newRequestTags` (or
`newResponseTags`) — then `@created` is absent and every successive response
records no authoring date, silently. Red: `@created` = "" (ok=false), want
2026-09-18.
**Inject:** cmd/ark/message_cli.go:newRequestTags, cmd/ark/message_cli.go:newResponseTags
**Pulled:** 2026-09-18 — rang (request: @created = "" when the stamp is dropped)

## Test: a tag update never rewrites @created (write-once)
**Purpose:** `applyTagUpdates` (the set-tags core) restamps `@status-date` on a
status change but leaves `@created` untouched, so a message's creation date
survives every later status/bookmark change. (R3439)
**Input:** a tag block carrying `@created: 2026-09-18`; `applyTagUpdates` with
`{status: completed, response-handled: completed}` and a later `now`.
**Expected:** `@created` still 2026-09-18; `@status-date` moved to the later date.
**Refs:** crc-Server.md
**Code:** tagblock_test.go
**Alarm:** 2
**Fire alarm:** add `tb.Set("created", now.Format("2006-01-02"))` to
`applyTagUpdates` (the "keep created fresh" mistake) — then a status change
silently overwrites the creation date. Red: `@created` = 2026-10-01, want
2026-09-18. This is the invisible-when-violated property `@created` exists for.
**Inject:** server.go:applyTagUpdates
**Pulled:** 2026-09-18 — rang (@created = 2026-10-01 when applyTagUpdates re-stamps it)
