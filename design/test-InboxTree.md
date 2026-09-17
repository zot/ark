# Test Design: InboxTree
**Source:** crc-CLI.md

Tests for the baby-food inbox renderer (`printInboxNode`, `printInboxEmptyHint`
in `cmd/ark/message_cli.go`). Pure functions over `ark.InboxEntry` values that
write to stdout, so they test with zero collaborators — no DB, no server. Stdout
is captured through an `os.Pipe`.

## Test: response nests under its request even when filtered as a row
**Purpose:** R3406 — a completed response (which the row view hides by default)
is still shown nested beneath its open request, and R3407 states the bookmark in
words.
**Input:** request `{Status: open, From: microfts2, To: mini-spec, Summary: "…"}`
paired with response `{Status: completed, From: mini-spec, To: microfts2,
RequestID: X, ResponseHandled: ""}`.
**Expected:** output contains a `- response: completed  mini-spec → microfts2`
line and `microfts2 has not handled this response yet`.
**Refs:** crc-CLI.md, seq-message.md
**Code:** cmd/ark/message_cli_test.go
**Alarm:** 1
**Fire alarm:** in `printInboxNode`, skip rendering the response (return right
after the request path line, as if the response had been dropped with its row).
The test goes red: the `response: completed` line and the bookmark sentence both
vanish — the exact regression this feature fixes.
**Inject:** cmd/ark/message_cli.go:printInboxNode
**Pulled:** 2026-09-16 — rang (response line and bookmark both vanished)

## Test: a request with no response says so
**Purpose:** R3408 — an unanswered request prints an explicit `no response` line.
**Input:** request entry, `resp == nil`.
**Expected:** output contains `- no response`.
**Refs:** crc-CLI.md
**Code:** cmd/ark/message_cli_test.go
**Alarm:** 2
**Fire alarm:** change the `resp == nil` branch to print nothing. The test goes
red: `no response` is absent, so an unanswered request reads identically to one
whose response failed to render.
**Inject:** cmd/ark/message_cli.go:printInboxNode
**Pulled:** 2026-09-16 — rang (the `- no response` line was absent)

## Test: empty result names the projects present
**Purpose:** R3410 — an over-narrow filter reports the from-/to-projects that
were present, so a `mini-spec` vs `minispec` typo is correctable.
**Input:** entries spanning from-projects {ark, mini-spec} and to-projects
{microfts2}, with `from = "minispec"`.
**Expected:** output contains `no messages match (--from minispec)`,
`from-projects present: ark, mini-spec` (sorted), and `to-projects present:
microfts2`.
**Refs:** crc-CLI.md
**Code:** cmd/ark/message_cli_test.go
**Alarm:** 3
**Fire alarm:** drop `from` from the applied-filters list, or unsort the keys.
The test goes red: the filter echo or the sorted project list no longer matches.
**Inject:** cmd/ark/message_cli.go:printInboxEmptyHint
**Pulled:** 2026-09-16 — rang (filter echo dropped `--from minispec`)
