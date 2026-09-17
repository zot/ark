# Test Design: TagOutput
**Source:** crc-CLI.md

Tests for the agent-readable output of `cmdTagGet` and `cmdTagCheck`
(`cmd/ark/main.go`), shared by `ark tag get`/`check` and the `ark message
get-tags`/`check` aliases. File-backed but collaborator-free: a temp tag-block
file, stdout captured through `os.Pipe`.

## Test: get default is bullets, --compact is the TSV
**Purpose:** R3411 — the default is a `- tag: value` bullet list with values
verbatim; `--compact` restores the legacy `tag\tvalue` TSV.
**Input:** a file whose `@issue` value itself contains `: `, read with
`compact=false` then `compact=true`.
**Expected:** bullet run contains `- from-project: ark` and the colon-bearing
value verbatim; compact run contains `from-project\tark` and no bullet.
**Refs:** crc-CLI.md
**Code:** cmd/ark/tag_output_test.go
**Alarm:** 1
**Fire alarm:** in `cmdTagGet`, drop the `if compact` branch so `format` stays
`tagGetCompact` always. The test goes red: the default run emits TSV, so the
`- from-project: ark` bullet is absent.
**Inject:** cmd/ark/main.go:cmdTagGet
**Pulled:** 2026-09-17 — rang (default run emitted TSV, bullet absent)

## Test: check states success, does not stay silent
**Purpose:** R3412 — a passing check prints a line naming the file and that its
tag block is well-formed, rather than exiting silently (which reads to an agent
like a no-op).
**Input:** a well-formed tag-block file, no bad headings.
**Expected:** output contains `tag block is well-formed`.
**Refs:** crc-CLI.md
**Code:** cmd/ark/tag_output_test.go
**Alarm:** 2
**Fire alarm:** in `cmdTagCheck`, delete the success-line `fmt.Printf` before the
`return`. The test goes red: success is silent again, indistinguishable from a
command that did nothing.
**Inject:** cmd/ark/main.go:cmdTagCheck
**Pulled:** 2026-09-17 — rang (success line absent, output empty)
