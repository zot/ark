# Find Connections — Substrate (no-agent normal mode)

Tag Forge Phase 2A backend. The no-agent counterpart to 1G's
sidecar pipeline (see `find-connections.md`). Same tmp:// doc
schema, lifecycle, and pubsub contract — different mode.

The substrate runs in-process, deterministic, and sub-second.
Vector and trigram passes look up tag-name candidates two ways:
against the corpus's tag definitions (ED records) and against
its chunks (EC + V records). The merged ranking, with supporting
chunk evidence and per-substrate scores, is the foundation for
**both** the curation workshop (this phase) and ambient agent
recall (Phase 2B). The workshop is the first consumer.

Language: Go (Librarian orchestrator + new pipeline package, CLI
subcommands). Environment: ark server with the embedding model
loaded on demand (same TTL-managed `Librarian.modelCtx` already
used by `EmbedQuery`).

## Why a New Mode

1G's `mcp.findConnections` reads chunk text and asks Haiku for
themes and shared-tag candidates. The output is high-quality but
slow (5–60 s, requires a sidecar agent) and emits one specific
shape (themes + shared tags applied to the input set).

2A answers a related but distinct question: **given some chunks
or some text, which tag names from the corpus's existing
vocabulary describe what this is about?** No agent. The corpus
itself is the oracle, queried four ways:

- vector(query, ED)   → tag-defs whose definitions resemble the input
- trigram(query, ED)  → tag-defs whose definition text overlaps the input
- vector(query, EC)   → chunks similar to the input → vote for their tags
- trigram(query, EC)  → chunks lexically near the input → vote for their tags

These four signals merge into one ranked list of tag-name
candidates. Each candidate carries the chunks that motivated it
and the per-substrate scores that produced the ranking.

The two modes share the request lifecycle (`tmp://connections/<id>.md`
with `@connections-status`), so a single CLI surface covers
both. Per-mode body shape is selected via `@connections-mode`.

## Architectural Position

Foundation layer for two consumers:

- **Curation workshop (this phase).** The Tag Forge calls
  `sys.findConnections(inputs, {mode="normal"})` and subscribes
  to the resulting tmp:// doc. Renders tag-name candidates with
  substrate badges; expanding a candidate shows the supporting
  chunks and per-substrate scores. (UI work is item 4 — covered
  by `/ui-thorough`, not this spec.)
- **Recall CLI (Phase 2B, item 2 in ARK-STATE).** An agent calls
  `ark recall <context>` to surface relevant tagged chunks before
  taking action. Recall uses the same substrate paths exposed
  here, behind a different CLI verb.

This is also the substrate the V4 prospective inspiration loop
will use to score JSONL chunks for relevance (item 3 in ARK-STATE,
script-first).

## Inputs

`sys.findConnections` and `ark connections find` accept three
input shapes, all normalized to the same internal form before
the substrate runs:

1. **Chunk IDs.** `find 4711 5023` → look up EC[4711], EC[5023].
   Path is resolved via `fts.ReadCRecord` for evidence reporting.
2. **Path:range locators.** `find ark-tags.md:38-43` → resolve the
   file's chunks via `fts.ReadFRecord`, take the subset whose
   `(chunkStart, chunkEnd)` intersects the requested line range,
   read their EC records. Multiple `path:range` arguments are
   permitted and merge with the chunkID and text inputs.
3. **Bare text.** `find "asparagus risotto recipe"` → embed the
   text in-process with the loaded model (same call path as
   `EmbedQuery`), no EC record written. The text counts as a
   single virtual input with `chunkID=0` and `path="<query>"`
   in evidence reporting.

Mixed inputs are allowed (`find 4711 ark-tags.md:38-43 "text"`).
Each becomes one input in the normalized list; the substrate runs
per-input and merges votes.

### Path:range Resolution

A path:range locator is `<path>:<startLine>-<endLine>` (1-based,
inclusive). `<path>` matches a single F-record using the existing
path resolver (`fts.ResolvePath` or equivalent). `<startLine>`
alone (no `-<endLine>`) selects just that line. `<path>` alone
(no `:range`) is rejected — the user must say "the whole file"
explicitly via `<path>:1-` or pass the file's chunkIDs.

Chunks whose `(startLine, endLine)` interval overlaps the
requested range qualify. A chunk that straddles the range
boundary on either side counts. The chunker's existing line-
range fields on F-record entries drive this; no new data.

## Substrate Pipeline

For each normalized input `I` (carrying an EC vector or an ad-hoc
embedding):

1. **vector(I, ED)** — cosine scan ED records, max per tag across
   defining files. Top-K' (default K'=50 inside the pipeline,
   shrunk to k at the end). Same algorithm as `SuggestTagNames`,
   reused as a library call.
2. **trigram(I, ED)** — extract trigrams from the input text;
   trigram-match against tag-def text via microfts2's existing
   trigram indexes. Score = normalized overlap count. Aggregate
   per tag across defining files (max). Top-K'.
3. **vector(I, EC)** — cosine scan EC records (the existing
   `SearchChunks(queryVec, K')` call). Returns top-K' similar
   chunks with scores. For each returned chunk, read its V
   records; each (tag, value) on a returned chunk casts a vote
   for that tag. Per-tag score = max chunk-score across the
   chunks carrying that tag. Per-tag evidence = the contributing
   chunks (capped at, say, 10 per tag).
4. **trigram(I, EC)** — trigram-fuzzy against chunk text (the
   existing fuzzy match path — `microfts2.FuzzyChunks` or
   equivalent). Same vote mechanism: returned chunks vote for
   their tags. Per-tag score = max trigram-score across
   contributors.

Each input thus produces four `(tag, score, evidence)` lists.

**Merge across substrates per input.** For input `I` and tag
`T`, the per-substrate scores are kept distinct:

- `vector_ed(I, T)`, `trigram_ed(I, T)`, `vector_ec(I, T)`, `trigram_ec(I, T)`

A tag's aggregate score for input `I` is **max** across the four
substrate scores (after normalization). Max preserves "this tag
is strongly suggested by *one* of these signals" — the right
signal for surfacing candidates the user could otherwise miss.
Averaging dilutes a single sharp signal with three weak ones.

**Merge across inputs.** For tag `T`, the aggregate score across
inputs is `max(per-input aggregate)`. Per-substrate per-input
detail is retained for the evidence display.

**Evidence preservation.** Each candidate carries:

- `Tag` — the tag name.
- `Score` — the cross-substrate, cross-input aggregate (one number).
- `PerSubstrate` — four scores (vector_ed, trigram_ed, vector_ec, trigram_ec), each the cross-input max.
- `SupportingChunks` — up to N (default 10) chunks that motivated this candidate, with each chunk's per-substrate scores.
- `MotivatingFiles` — for the ED-side substrates, the tag-def files whose definitions matched, with per-file scores.

Output is the top-k candidates by aggregate score (default k=20,
configurable).

## Score Normalization

Vector cosines are in `[-1, 1]`; we use `(cos + 1) / 2` to map
to `[0, 1]`. Trigram scores are normalized to `[0, 1]` via
microfts2's existing fuzzy-match scoring (already documented
in `specs/fuzzy-search.md`). The four substrate scores are
directly comparable on a single `[0, 1]` scale.

## Empty / Degenerate Inputs

- No inputs after normalization → reject with
  `chunkIDs/text/range empty`.
- One or more chunkIDs unknown to the index → reject the whole
  request before enqueueing; emit a single error naming the
  first unknown ID. (Mirrors `BuildFetchPayload`'s pattern but
  applied at enqueue time, because the substrate pipeline needs
  EC vectors immediately.)
- Embedding unavailable (no `tag_model` configured, or model
  file missing) → the ED-vector and EC-vector substrates skip;
  trigram substrates still run. The result includes the two
  trigram-side rankings with a note in the doc header
  (`@connections-warning: embedding unavailable`).
- ED prefix empty (no tag defs indexed yet) → ED-side substrates
  return nothing; EC-side substrates still run. No header warning
  — emptiness is honest.

## tmp:// Document — Shared Schema

`tmp://connections/<request-id>.md` is the durable contract for
**both** modes. Existing find-connections.md describes the
shared header tags and lifecycle; 2A adds three new headers and
generalizes the body.

### Header Tags (new in 2A)

```
@purpose: curate
@connections-mode: normal | turbo
@proposal-count: <N>          # set on terminal completed status
```

`@purpose` distinguishes connections docs the curation workshop
should surface from connections docs other consumers create
(e.g. recall in Phase 2B uses `@purpose: recall`). The workshop
subscribes filtered to `@purpose: curate`.

`@connections-mode` selects the renderer. `normal` (this phase)
emits the tag-name proposal body. `turbo` (2C) overlays the
agent on top of the substrate output and may add per-chunk
values and ext-routings.

`@proposal-count` is convenience metadata for the workshop to
size its rendering before parsing the body.

The 1G doc shape gains `@purpose: curate` and
`@connections-mode: turbo` retroactively — the sidecar already
emits the turbo-style body, so the new headers describe it
correctly. 1G's existing body sections (`## Themes`, `## Shared
Tag Candidates`) stay, marked as `@proposal-kind: theme` and
`@proposal-kind: shared-tag` rows under the unified `##
Proposals` section (see Body section below).

### Body — Unified `## Proposals`

All proposal types live under one `## Proposals` heading,
distinguished by `@proposal-kind`. The renderer reads each row's
kind and dispatches.

For 2A (`mode: normal`), every row is `@proposal-kind: tag-name`:

```markdown
## Proposals

- @proposal-kind: tag-name
  @proposal-value: "design-decision"
  @proposal-score: 0.84
  @proposal-evidence-chunks: 4711, 5023
  @proposal-evidence-vector-ed: 0.91
  @proposal-evidence-trigram-ed: 0.78
  @proposal-evidence-vector-ec: 0.65
  @proposal-evidence-trigram-ec: 0.40
  @proposal-motivating-files: defs/architecture.md:0.91, specs/design.md:0.74

- @proposal-kind: tag-name
  @proposal-value: "area"
  @proposal-score: 0.71
  …
```

For 1G (`mode: turbo`), the rows are `@proposal-kind: theme` and
`@proposal-kind: shared-tag` — the existing fields rename to
the unified `@proposal-*` namespace:

```markdown
- @proposal-kind: theme
  @proposal-text: "Lua coroutine patterns"
  @proposal-evidence-chunks: 4711, 4712

- @proposal-kind: shared-tag
  @proposal-tag: "topic"
  @proposal-value: "lua-coroutines"
  @proposal-evidence-chunks: 4711, 4712, 4715
```

This is a small body-shape migration for 1G (header tags within
each item). The doc-parse path in 1G's Lua subscriber is in
`apps/ark/curation.lua` (callsite identified in the existing
spec); the migration is straightforward but does require a
matching Lua change. Bill should be aware: the change is
described here as the substrate spec's coupling to 1G, not as
a separate item — implementation needs to keep both shapes
working during transition OR migrate them together.

### Status Lifecycle (unchanged)

`@connections-status: pending → working → completed | errored`.
Same transitions, same atomic write rules, same throttling as
1G. For 2A, the pipeline is fast enough (sub-second on a typical
small input set) that intermediate `working` is rarely observed
by subscribers; the doc may transition `pending → completed`
directly without a `working` intermediate. Tickers and
progress-text updates are skipped in normal mode.

### Errored Cases (2A-specific)

- `chunkIDs/text/range empty` → reject at enqueue, no doc
  written. (Caller error; no tmp:// path to subscribe to.)
- `unknown chunk <id>` → reject at enqueue with named ID.
- `path "<p>" not found` → reject at enqueue.
- `path:range parse error` → reject at enqueue.
- Internal pipeline failure (LMDB read error, embedding model
  error mid-flight) → write the doc with
  `@connections-status: errored`, `@connections-error: <message>`.

## Lua API

Single entry point on `sys`, mirroring the eventual
single-entry shape from `.scratch/CONNECTIONS.md`. The old
`mcp.findConnections` (1G's bridge) gets a one-release shim
that delegates with `mode = "turbo"`.

```lua
-- New canonical surface.
local requestID, err = sys.findConnections(inputs, opts)
-- inputs is a Lua array of entries; each entry is one of:
--   { chunkID = 4711 }
--   { path = "ark-tags.md", range = "38-43" }
--   { text = "asparagus risotto recipe" }
-- (Convenience: a bare array of integers is accepted as
-- {chunkID = N} entries, matching 1G's call shape.)
--
-- opts (optional table):
--   mode             = "normal" | "turbo"   (default: "normal")
--   k                = 20                   (top-K candidates; 1..200)
--   purpose          = "curate" | "recall"  (default: "curate")
--   timeoutSeconds   = 30                   (clamped [5, 300])
--
-- Returns: (requestID, nil) on success;
--          (nil, errstring) when the agent is unavailable for
--          turbo mode, when inputs are empty, or when an
--          enqueue-time check fails (unknown chunk, path miss).

-- Subscription wiring: unchanged from 1G. The workshop subscribes
-- to tmp://connections/<requestID>.md and reads the @proposal-*
-- rows under ## Proposals.
```

`sys.findConnections({…}, {mode="turbo"})` requires the
ark-connections sidecar to be available (`ConnectionsAvailable()`
true). `sys.findConnections({…}, {mode="normal"})` does not — it
runs in-process.

## Sidecar Protocol — unchanged but renamed

1G's sidecar agent (`ark-connections`) keeps its lotto-tube
shape. The CLI invocations migrate from flags to positional
subcommands (see CLI section below) but the wire protocol and
server endpoints don't change:

- `GET /connections/wait` — drain queue (turbo requests only)
- `GET /connections/fetch?id=ID` — chunk content
- `POST /connections/result` — sidecar posts result
- `POST /connections/error` — sidecar posts error

Normal-mode requests are not visible to the sidecar — they run
entirely in-process before the request ID is returned.

## CLI — `ark connections` subcommands

All-positional subcommand surface. The 1G sidecar's flag-based
CLI migrates verbatim (verbs preserved with `sidecar-` prefix).

### Public subcommands (humans and downstream agents)

```
ark connections find [options] <input>...
  Submit a find-connections request. Returns the resulting
  tmp:// path on stdout (e.g. tmp://connections/fc-7Yp2K3.md).

  Inputs (one or more, may mix):
    NNNNNN          chunk ID (decimal)
    PATH:START-END  file path with line range (1-based inclusive)
    PATH:N          file path with single line
    anything else   bare text, embedded in-process (no quoting required)

  Options:
    --mode normal|turbo            (default: normal)
    --k N                          top-K candidates (default 20)
    --purpose curate|recall        (default: curate)
    --timeout SEC                  (default 30, clamped [5,300])
    --type chunk|text              force every positional input to that
                                   category, bypassing auto-detect.
                                   `chunk` accepts decimal chunkIDs and
                                   `PATH:N` / `PATH:N-M` (shape still
                                   selects). `text` treats every token
                                   literally — useful when text happens
                                   to look like a chunkID or path:locator.
    --wait                         block until @connections-status reaches
                                   terminal (completed|errored). On completion,
                                   print the doc body on stdout. With --json,
                                   print the structured form.
    --json                         combined with --wait: emit JSON instead of markdown.

ark connections wait <path> [--timeout SEC] [--json]
  Block until the given tmp:// connections doc reaches terminal status.
  On completion, prints the doc body on stdout (markdown), or
  structured JSON with --json.

ark connections show <path> [options]
  Project structured summary or specific fields from the doc.
  Reads the persisted tmp:// content; does not block on status.

  Options:
    --status              print only @connections-status
    --tags                list all tag-name proposals (one per line)
    --tag NAME            filter to proposals whose @proposal-value == NAME
    --threshold N         drop proposals below score N (0.0-1.0)
    --json                emit JSON projection instead of markdown

  Distinct from `ark fetch <path>` which dumps the raw file body
  unparsed. `show` parses and projects.

ark connections list [--json]
  List in-flight connections requests (those whose record is still
  in the Librarian's connectionsResults map). Default output is a
  markdown table; --json emits an array of records.
```

### Sidecar subcommands (agent-internal protocol)

```
ark connections sidecar-wait
  Lotto tube. Block until turbo requests arrive, then print
  drained queue as JSON. Unchanged from `--wait` flag behavior.

ark connections sidecar-fetch <id>
  Print JSON array of {chunkID, fileID, path, content} for the
  request. Unchanged from `--fetch ID`.

ark connections sidecar-result <id>
  Read result JSON from stdin, post to server. Unchanged from
  `--result ID`.

ark connections sidecar-error <id> <message>
  Post an error message for the request. Unchanged from `--error ID=MESSAGE`.
```

The sidecar protocol moves from `--flag VALUE` to
`subcommand VALUE`, a one-time rename. The `ark-connections`
guard script (`~/.claude/agents/.../connections-guard.sh` or
similar) needs corresponding edits — see Implementation Notes
below.

### Old flags — removal

`ark connections --wait`, `--fetch`, `--result`, `--error` are
removed. The CLI prints a one-line hint pointing at the new
subcommand name when an unknown flag is encountered, then exits
with status 2.

## What This Does Not Do

- **No agent.** No Claude call. Pure cosine + trigram + V-record
  vote counting. The substrate is the floor; agents (1G turbo,
  2C agent expand) layer on top.
- **No tag-value proposals.** Only tag *names* are surfaced. The
  user fills in a value when accepting a proposal (the workshop's
  existing pending-tag flow). Per-chunk values are 2C's job.
- **No cross-corpus reach.** Substrate queries are constrained
  to the running ark index. `RewordedFuzzySearch` and JSONL
  reach belong to 2C.
- **No theme detection.** Themes are 1G/turbo's output. Normal
  mode emits per-tag candidates, not topic groupings.
- **No new R records.** The pair-sweep design in
  `.scratch/CONNECTIONS.md` is future work; 2A reads ED, EC, V,
  C, F directly per request.
- **No background sweep.** Each `find` call is on-demand. The
  substrate is fast enough that caching the result wouldn't
  help; results would go stale as the corpus updates.

## Performance

Target: under 200 ms wall time for a small input set (≤ 5
chunks, ≤ 1000 chunks in EC, ≤ 1000 tag defs in ED) on the
Steam Deck. Breakdown:

- Per-input ED cosine pass: ~3 µs/record × 1000 = 3 ms.
- Per-input EC cosine pass: ~3 µs/record × 50000 = 150 ms.
  (Already optimized in `SearchChunksMulti`; single txn shared
  across substrates.)
- Per-input ED trigram pass: ~5 ms (microfts2 fuzzy on small text).
- Per-input EC trigram pass: ~10 ms (microfts2 fuzzy on chunks).
- Vote aggregation, sort, render: ~5 ms.

For larger input sets the EC pass dominates; the four substrate
passes share a single LMDB View txn to avoid lock churn (the
`SearchChunksMulti` pattern). Per-input passes are sequential
within the txn but the multi-request batched form
(`SearchChunksMulti`) processes all inputs in one cursor walk.

Ad-hoc embedding (bare text input) adds ~10–25 ms (one model
dispatch on the default tier; see
`project_gollama-fork.md`/Embedding Benchmarks).

## Test Strategy

- Submit `find <chunkID>` for a chunk with EC + ED present;
  verify the doc completes with proposals ordered by aggregate
  score and each proposal carries `@proposal-evidence-*` fields.
- Submit `find <text>` for bare text; verify the on-the-fly
  embedding flows through the ED/EC substrates.
- Submit `find <path>:<start>-<end>` covering 2 chunks; verify
  per-input merge produces a single ranked list.
- Submit a mixed-input request (chunkID + text + range); verify
  each input contributes evidence under the same proposal.
- Submit with `--mode turbo`; verify the request enqueues for
  the sidecar and the doc starts in `pending`.
- Submit with `mode=normal` when embedding is unavailable;
  verify trigram-only result with `@connections-warning:
  embedding unavailable`.
- Submit with unknown chunkID; verify enqueue rejection (no
  tmp:// doc created).
- `ark connections show <path> --tag area` filters to
  matching `@proposal-value` rows.
- `ark connections show <path> --threshold 0.5` drops low-scoring
  proposals.
- `ark connections list --json` emits well-formed JSON for
  in-flight records.
- `ark connections wait <path>` blocks until `@connections-status`
  flips to a terminal value; returns within the configured
  timeout when it doesn't.

## Implementation Notes (non-spec, for the design pass)

- The substrate pipeline is its own type — `ConnectionsSubstrate`
  or `NormalModePipeline` on Librarian — to keep `connections.go`
  focused on lifecycle and tmp:// I/O. The substrate type is
  the reusable library call Phase 2B's `ark recall` will reach
  for.
- 1G's body migration from `## Themes` / `## Shared Tag Candidates`
  to unified `## Proposals` lands in the same pass. The Lua
  workshop's parser at `apps/ark/curation.lua` follows in a
  Frictionless pass after this Go work (not in this mini-spec).
  Until that lands, the Go side may emit *both* shapes in turbo
  mode (the old sections AND the new rows) so 1G keeps working
  during transition. The duplication is removed when the Lua
  side switches.
- The `ark-connections` sidecar's guard script needs updating
  to allow `sidecar-wait/sidecar-fetch/sidecar-result/sidecar-error`
  instead of the old flags. That's a `.claude/agents/.../`
  edit, not a Go change — track separately.
