# Test Design: `ark connections` CLI

**Source:** crc-CLI.md, seq-find-connections-substrate.md,
specs/find-connections-substrate.md

CLI tests live in `cmd/ark/main_test.go` (or a sibling
`cmd/ark/connections_test.go` if the surface grows large enough
to warrant its own file). They exercise the positional subcommand
parser against a running test server, validating both the
public verbs (`find`, `wait`, `show`, `list`) and the renamed
sidecar verbs (`sidecar-*`).

The tests assume a server fixture (mirror what existing CLI tests
like `cmdSearch` use). All `ark connections` public subcommands
require a running server, so the fixture is non-optional.

## Test: find with one chunkID returns tmp:// path on stdout

**Purpose:** Validates R2604 — `find` submits and prints the
returned path.

**Input:** Run `ark connections find <chunkID>` against a server
with the chunk indexed.

**Expected:** Stdout is exactly one line matching
`tmp://connections/[A-Za-z0-9-]+\.md`. Exit status 0. Stderr is
empty (or only the standard verbose-level chatter).

**Refs:** crc-CLI.md, seq-find-connections-substrate.md (CLI
find --wait).

## Test: find accepts mixed input types

**Purpose:** Validates R2604 — `find` parses chunkIDs, `PATH:N-M`,
`PATH:N`, and quoted text in one invocation.

**Input:** Run `ark connections find 4711 foo.md:10-20 foo.md:42 "asparagus"`.

**Expected:** Server receives a request with four inputs of the
expected types. Exit status 0. The returned tmp:// doc's
`@connections-pinned-chunks` header reflects the resolved
chunkIDs (c1 + chunks intersecting 10-20 + chunk on line 42);
text input is recorded distinctly in the doc body's evidence.

**Refs:** crc-CLI.md.

## Test: find --wait blocks until completion and prints body

**Purpose:** Validates R2605 — `--wait` returns the completed body.

**Input:** Run `ark connections find <chunkID> --wait` against a
small corpus.

**Expected:** Stdout carries the full doc body (header tags +
`## Proposals` section). Exit status 0. Wall time consistent with
the substrate pipeline target (well under 1 s for a small corpus).

**Refs:** crc-CLI.md.

## Test: find --wait --json emits structured projection

**Purpose:** Validates R2605 — JSON output of the completed body.

**Input:** Run `ark connections find <chunkID> --wait --json`.

**Expected:** Stdout is a single JSON object with fields:
  - `requestID`
  - `status` (= "completed")
  - `mode` (= "normal")
  - `purpose` (= "curate")
  - `warning` (optional, empty unless embedding unavailable)
  - `proposals`: array of `{kind, value, score, evidenceChunks,
    perSubstrate {vectorEd, trigramEd, vectorEc, trigramEc},
    motivatingFiles: [{path, score}]}` objects
Exit status 0.

**Refs:** crc-CLI.md.

## Test: find rejects unknown chunkID with error message

**Purpose:** Validates R2569, R2600 reaching the CLI surface.

**Input:** Run `ark connections find 99999999`.

**Expected:** Exit status non-zero. Stderr contains
`unknown chunk 99999999`. Stdout is empty (no tmp:// path
printed).

**Refs:** crc-CLI.md.

## Test: find --mode turbo respects sidecar availability

**Purpose:** Validates R2603 reaching the CLI.

**Input:** Run `ark connections find <chunkID> --mode turbo`
against a server with no registered sidecar consumer.

**Expected:** Exit status non-zero. Stderr contains
`agent unavailable`. No tmp:// path on stdout.

**Refs:** crc-CLI.md.

## Test: wait <path> blocks until terminal status

**Purpose:** Validates R2606.

**Input:** Start a request asynchronously, capture its path
(via `ark connections find <chunkID>`). Spawn
`ark connections wait <path>` in a second goroutine. The
first call's completion should release the wait.

**Expected:** `wait`'s stdout is the completed doc body. Exit
status 0. Total wall time for `wait` is at most the substrate
pipeline duration plus a small subscribe-startup overhead.

**Refs:** crc-CLI.md.

## Test: wait --timeout exits non-zero when status never flips

**Purpose:** Validates R2606 timeout branch.

**Input:** Submit a request via the Lua bridge in a way that
delays completion artificially (or against a tmp:// path that
will never become a real connections doc). Run
`ark connections wait <path> --timeout 1`.

**Expected:** Exit status non-zero within ~1.2 s. Stderr contains
the last-seen `@connections-status` value.

**Refs:** crc-CLI.md.

## Test: show projects all default fields

**Purpose:** Validates R2607 — default markdown projection.

**Input:** Submit a request, wait for completion, then run
`ark connections show <path>`.

**Expected:** Stdout is a structured markdown summary listing
the proposals with their values and scores. Without flags, the
output is more concise than the raw doc body (no per-substrate
score fields, no motivating-files listing — those are reached
via `--tag` or `--json`).

**Refs:** crc-CLI.md.

## Test: show --status prints only the status

**Purpose:** Validates R2607 `--status` flag.

**Input:** Run `ark connections show <path> --status` on a
completed doc.

**Expected:** Stdout is exactly one line: `completed` (or whatever
the terminal status is). Exit status 0. No other output.

**Refs:** crc-CLI.md.

## Test: show --tags lists tag-name proposals one per line

**Purpose:** Validates R2607 `--tags` flag.

**Input:** Run `ark connections show <path> --tags` on a
completed doc with N tag-name proposals.

**Expected:** Stdout is N lines, each one tag name. Order matches
the body's proposal order (sorted by score desc).

**Refs:** crc-CLI.md.

## Test: show --tag NAME filters to matching proposals

**Purpose:** Validates R2607 `--tag NAME` flag.

**Input:** A completed doc with proposals for `area`, `topic`,
`design-decision`. Run
`ark connections show <path> --tag area`.

**Expected:** Stdout includes only the `area` proposal's row(s)
in the projection. Other proposals are filtered out. If the tag
isn't present, stdout is empty and exit status is 0.

**Refs:** crc-CLI.md.

## Test: show --threshold drops low-scoring proposals

**Purpose:** Validates R2607 `--threshold N` flag.

**Input:** A completed doc with proposals scored 0.91, 0.78,
0.45, 0.30. Run `ark connections show <path> --threshold 0.5`.

**Expected:** Projection contains only the 0.91 and 0.78
proposals.

**Refs:** crc-CLI.md.

## Test: show --json emits JSON projection

**Purpose:** Validates R2607 `--json` flag.

**Input:** Run `ark connections show <path> --json` on a
completed doc. Pipe through `jq` to assert structure.

**Expected:** Single JSON object with the same fields as the
find --wait --json shape. The projection flags (`--tag`,
`--threshold`) compose with `--json` (filter applies before
serialization).

**Refs:** crc-CLI.md.

## Test: show <path> on errored doc reports the error

**Purpose:** R2607 / robustness.

**Input:** Submit a request whose normalization fails (no — that
doesn't create a doc). Instead: submit a turbo request, let it
time out, then run `ark connections show <path>`.

**Expected:** Stdout indicates `status: errored` and surfaces
`@connections-error: timeout`.

**Refs:** crc-CLI.md.

## Test: fetch vs show distinction

**Purpose:** Validates R2608 — `show` parses; `ark fetch` dumps.

**Input:** Submit a request, wait for completion. Run
`ark fetch <path>`. Run `ark connections show <path>`.

**Expected:** `fetch` output is the raw file body verbatim,
including the `## Proposals` markdown headers and all
@proposal-* lines. `show`'s default output is shorter and
projected (the parsed structure). Both succeed; they differ
in shape.

**Refs:** crc-CLI.md.

## Test: list prints in-flight requests as markdown table

**Purpose:** Validates R2609.

**Input:** Submit 2 requests (one normal, one turbo with no
sidecar to keep it pending). Run `ark connections list`.

**Expected:** Stdout is a markdown table with columns for
request ID, mode, status, purpose, path, age. Two rows. Exit
status 0. Order is stable (typically newest first).

**Refs:** crc-CLI.md.

## Test: list --json emits well-formed JSON array

**Purpose:** Validates R2609 `--json` flag.

**Input:** Run `ark connections list --json` with 2 in-flight
requests.

**Expected:** Stdout parses as a JSON array of 2 objects with
the same fields the markdown table shows. Empty list returns
`[]`, not null.

**Refs:** crc-CLI.md.

## Test: public subcommands refuse when server is down

**Purpose:** Validates R2610.

**Input:** Stop the server. Run each of
`ark connections {find <chunkID>, wait <path>, show <path>, list}`.

**Expected:** Each exits non-zero with `server not running` on
stderr.

**Refs:** crc-CLI.md.

## Test: sidecar-wait drains the queue (replaces --wait flag)

**Purpose:** Validates R2611.

**Input:** Submit a turbo request via the bridge. Run
`ark connections sidecar-wait`.

**Expected:** Stdout is a JSON array `[{id, chunkIDs,
timeoutSeconds}]` for the submitted request. Behavior identical
to the old `ark connections --wait` (same HTTP call). Exit 0.

**Refs:** crc-CLI.md.

## Test: sidecar-fetch returns chunk content (replaces --fetch ID)

**Purpose:** Validates R2612.

**Input:** Submit a turbo request, drain via sidecar-wait, then
run `ark connections sidecar-fetch <id>`.

**Expected:** Stdout is a JSON array of `{chunkID, fileID, path,
content}` objects. Exit 0.

**Refs:** crc-CLI.md.

## Test: sidecar-fetch errors on unknown chunkID

**Purpose:** Validates R2612 (error path).

**Input:** Submit a turbo request with one unknown chunkID,
drain, run sidecar-fetch.

**Expected:** Exit non-zero. Stderr contains
`unknown chunk <id>` naming the offending ID.

**Refs:** crc-CLI.md.

## Test: sidecar-result posts payload from stdin (replaces --result ID)

**Purpose:** Validates R2613.

**Input:** Submit a turbo request, drain, fetch. Pipe a valid
result JSON to `ark connections sidecar-result <id>`.

**Expected:** Exit 0. Doc transitions to completed with the
expected body sections. Server validates evidence per the
existing R2317/R2342 rules.

**Refs:** crc-CLI.md.

## Test: sidecar-error posts message with positional argument

**Purpose:** Validates R2614 — message is positional, not
`ID=MESSAGE`.

**Input:** Run `ark connections sidecar-error <id> "boom"`.

**Expected:** Exit 0. Doc transitions to errored with
`@connections-error: boom`.

**Refs:** crc-CLI.md.

## Test: removed --wait flag prints migration hint and exits 2

**Purpose:** Validates R2615 — clean failure for the renamed
flags.

**Input:** Run `ark connections --wait` (the old form).

**Expected:** Exit status 2. Stderr contains a one-line hint
naming `sidecar-wait` as the replacement. Stdout is empty.
Same for `--fetch`, `--result`, `--error`.

**Refs:** crc-CLI.md.

## Test: connections --help lists all public + sidecar subcommands

**Purpose:** Validates the help surface covers the new dispatch
table (general CLI hygiene; ties into R76 `--help` exits 0).

**Input:** Run `ark connections --help`.

**Expected:** Exit 0. Stdout names every public subcommand
(`find`, `wait`, `show`, `list`) with one-line synopsis, plus
the `sidecar-*` subcommands flagged as "agent internal." Help
mentions `tmp://connections/<id>.md` as the durable contract.

**Refs:** crc-CLI.md, specs/cli-commands.md.
