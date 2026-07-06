# Test Design: Bloodhound CLI output modes

**Source:** crc-CLITree.md

Covers the client-side output formatting of `ark bloodhound search` (R3037,
R3040) — the pure render helper in `cmd/ark/bloodhound_cli.go`, exercised
directly with no server/DB. The `--raw` and default-JSONL paths are verbatim
passthrough of the server's returned body; only `--markdown` transforms it, so
that is where the render logic to test lives.

## Test: markdown render of curated findings
**Purpose:** `renderFindingsMarkdown` turns the curated JSONL into a locator list —
one `- ` + `` `path:range` `` + ` — note` line per finding, the `chunk` excerpt as a
blockquote when present (R3037).
**Input:** two JSONL lines, one with a `chunk`, one without.
**Expected:** two `- ` locator lines; the first followed by a `> ` blockquote of its
chunk; the note text present on each; valid markdown (no raw JSON braces).

## Test: markdown render of an empty result
**Purpose:** an empty body renders a single "no findings" line, not blank output
(R3037).
**Input:** empty `data` (an empty hunt).
**Expected:** the output is the "no findings" line.

## Test: malformed JSONL line is skipped, not fatal
**Purpose:** the render is defensive — a line that does not parse as a finding is
skipped rather than aborting the whole render (R3037).
**Input:** two lines, the first valid, the second not JSON.
**Expected:** the valid finding renders; the render returns without error.
