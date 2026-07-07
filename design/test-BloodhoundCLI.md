# Test Design: Bloodhound CLI output modes + clue input

**Source:** crc-CLITree.md

Covers the client-side output formatting of `ark bloodhound search` (R3037,
R3040) and the clue-input / payload-build helpers (R3046) — the pure helpers in
`cmd/ark/bloodhound_cli.go`, exercised directly with no server/DB. The `--raw`
and default-JSONL paths are verbatim passthrough of the server's returned body;
only `--markdown` transforms it, so that is where the render logic to test lives.

## Test: resolveClue — positional, file, mutual exclusion
**Purpose:** the clue comes from positional `CLUE...` (joined) or `--file PATH` (read
byte-for-byte); the two are mutually exclusive (R3046). (`--file -` / stdin is driven
via an injected reader.)
**Input:** positional args only; a temp file only; both together; a `--file` path via an
injected stdin reader for the `-` case.
**Expected:** positional → the joined line; file → the file's bytes; both → an error; `-`
→ the injected reader's content.

## Test: buildSearchPayload — metadata-first, clue last
**Purpose:** the payload places `scope`/`depth`/`want` (and `curate: false` when `--raw`)
as leading `key: value` lines, then the clue body last, so the watcher's `clueOf` strips
metadata and splits only the clue (R3046, R3044).
**Input:** a multi-paragraph clue with scope/depth/want; the same with `--raw`.
**Expected:** scope/depth/want lead; `curate: false` present only for `--raw`; the clue
body (with its blank-line paragraph breaks intact) is last; feeding the result through
`clueOf` returns exactly the clue body.

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
