# Find Connections

The pinned-chunks → connection-proposals action inside the curation
view. Phase 1G of the curation plan. Frictionless-native: the
agent's output becomes a corpus artifact (a `tmp://` document), and
the curation view subscribes to its status tag.

Three things flow together:

1. **Sidecar agent (`ark-connections`).** Haiku running in a lotto
   tube loop. Picks up requests via `ark connections --wait`, reads
   chunk content via `ark connections --fetch`, proposes themes and
   shared-tag candidates inline, posts the result via
   `ark connections --result`.
2. **tmp:// document.** The result lives in
   `tmp://connections/<id>.md` with `@connections-status`,
   `@connections-progress`, `@connections-elapsed`, and a body
   carrying the themes and shared-tag candidates with their
   evidence chunk IDs. The document is the durable contract; any
   tool can read or subscribe to it.
3. **Fire-and-forget Lua bridge.** `mcp.findConnections(chunkIDs)`
   enqueues the request and returns a request ID **immediately**.
   The Lua VM never blocks. The Frictionless UI subscribes to the
   tmp:// document's `@connections-status` transitions via the
   substrate primitives added in Subtask 0 (`mcp.onpublish`,
   `mcp.subscribe`, `mcp.cancel`) and rebinds its reactive state
   when the terminal status fires.

Language: Go (server, sidecar CLI, Librarian orchestrator).
Environment: ark server with the `ark-connections` agent launched
as a background subagent in the same Claude Code session that runs
the curation view.

## Architectural Position

Find Connections is the first slice that combines the sidecar
pattern with the **tmp:// + pubsub** shape for async Frictionless
actions:

- Sidecar agents are stateless workers that read corpus + write
  corpus.
- The corpus contract — what gets written, where, with which tags
  — is the durable interface between the agent and the rest of
  the system.
- Frictionless UIs subscribe to tag transitions on the result
  documents; they don't poll, they don't block on RPCs, they
  don't reach across to JS.
- Other tools (search, the V4 prospector's inbox, future audit
  views) read the same documents.

This shape applies to every async ark action that surfaces in
Frictionless: the existing `mcp.sweepHotCorrelations()` should
be retrofitted to it, the V4 inspiration prospector adopts it
for push-side proposals, and Phase 2B's Magic-grade Curate
extends it with richer result content.

## User-visible Flow

1. User pins chunks in `Ark.Curation` (existing 1F.1 behavior).
2. User clicks "Find Connections" on the curation header.
3. View calls `mcp.findConnections(_pinned.chunkIDs)`. The bridge
   returns immediately with a request ID. View sets a
   `_findingConnections` flag, clears prior results, subscribes to
   `tmp://connections/<id>.md` with filter `@connections-status:
   completed`.
4. The viewdef rerenders: a "Thinking…" status row appears at the
   top of the result panel. Elapsed time updates from the
   `@connections-elapsed` ticks (sidecar updates the doc every
   ~5 s while it works).
5. Sidecar wakes from `ark connections --wait`, reads each chunk's
   content via `--fetch`, asks Claude inline for themes +
   shared-tag candidates + evidence, posts the result via
   `--result`.
6. The server writes
   `tmp://connections/<id>.md` to `@connections-status: completed`
   (atomic write through the write actor).
7. Subscription fires. Lua callback reads the tmp:// doc body,
   parses the proposals, populates `_connectionResults`. Reactive
   rebind renders the proposal panel.
8. User clicks Accept on a shared-tag proposal. View invokes the
   existing `Ark:applyTagToChunks` flow.
9. When the user closes / clears the panel, the view unsubscribes;
   the tmp:// doc remains until server restart (standard tmp://
   lifecycle).

## Result Content

Themes and shared-tag candidates are the two primary surfaces.
Evidence chunk-ID lists ride along on both. `@ext:` routings (the
full `ProposeExtRoutings` shape from `.scratch/CURATE-CHUNK.md`)
are **out of scope for 1G** — deferred to Phase 2B.

```go
type ConnectionsResult struct {
    Themes      []Theme           // short summaries spanning the pinned set
    SharedTags  []SharedTagCand   // tag values that could apply across pinned chunks
}

type Theme struct {
    Text     string    // one-line summary, e.g. "Lua coroutine patterns"
    Evidence []uint64  // chunk IDs from the pinned set that motivated this theme
}

type SharedTagCand struct {
    Tag      string    // e.g. "topic"
    Value    string    // e.g. "lua-coroutines"
    Evidence []uint64  // chunk IDs from the pinned set that motivated the proposal
}
```

The sidecar must populate `Evidence` on every entry; an empty
evidence list is a protocol violation and the server rejects the
result (status flips to `errored`).

## tmp:// Document Schema

`tmp://connections/<request-id>.md` carries the full lifecycle.
The path is unique per request; the document is created when the
request is enqueued and persists for the server lifetime.

### Header tags

```
@connections-status: pending | working | completed | errored
@connections-request-id: fc-7Yp2K3
@connections-pinned-chunks: 4711,4712,4715
@connections-started: 2026-05-12T09:30:00Z
@connections-elapsed: 18
@connections-progress: fetching | thinking | posting | done
@connections-completed: 2026-05-12T09:30:27Z          # set on terminal transition
@connections-error: <message>                          # only when status = errored
```

`@connections-status` is the discrete state subscribers care
about. `@connections-progress` is finer-grained for status text.
`@connections-elapsed` ticks every 5 s while the sidecar works.

### Body (after completion)

```markdown
## Themes

- @theme-evidence: 4711,4712
  Lua coroutine patterns

- @theme-evidence: 4715
  Error propagation across yields

## Shared Tag Candidates

- @shared-tag: topic
  @shared-tag-value: lua-coroutines
  @shared-tag-evidence: 4711,4712,4715

- @shared-tag: difficulty
  @shared-tag-value: intermediate
  @shared-tag-evidence: 4712,4715
```

The body uses the same tag-line conventions as the rest of the
corpus, so search and content-fetch read the proposals naturally.
The header tags drive the workshop subscription; the body is what
the workshop renders.

### Throttling

The sidecar updates the doc at most every ~5 s while working
(`@connections-elapsed`, `@connections-progress`). The terminal
transition (`completed` or `errored`) flushes immediately, so the
subscribing workshop never sees stale state at the end.

## Lua API

A single fire-and-forget bridge method on the Go side; the
workshop wires it together with the three substrate primitives
that landed in Subtask 0 (`mcp.onpublish` / `mcp.subscribe` /
`mcp.cancel`).

```lua
-- Enqueue a Find Connections request. Returns the request ID
-- immediately; the call never blocks the Lua VM.
local requestID = mcp.findConnections(chunkIDs, opts)
-- opts (optional table):
--   timeoutSeconds = 60     -- clamped to [5, 300]
-- Returns: requestID (string) on success, (nil, errstring) if
-- the agent is unavailable or the chunkIDs list is empty.

-- The workshop subscribes to the tmp:// doc via the substrate
-- primitives. Session names are app-chosen; replace-by-
-- (session, tag) gives free cancellation when the user clicks
-- Find Connections again.
local session = "curation:fc-" .. requestID
mcp.onpublish(session, function(events)
    for _, e in ipairs(events) do
        if e.tag == "connections-status" then
            if e.value == "completed" then
                -- Read the doc body, parse Themes / Shared Tag
                -- Candidates, populate reactive state.
            elseif e.value == "errored" then
                -- Render @connections-error.
            end
        end
    end
end)
mcp.subscribe(session, {
    tag         = "connections-status",
    valueRE     = "^(completed|errored)$",
    filterFiles = { "tmp://connections/" .. requestID .. ".md" },
})

-- Cancellation: drop the subscription. The sidecar work
-- continues until timeout but its result is ignored.
mcp.cancel(session, "")
```

`mcp.findConnections` returns `(nil, errstring)` when:

- No `ark-connections` sidecar is registered — `Librarian.
  ConnectionsAvailable()` returns false when no
  `ark connections --wait` has been observed within the
  availability window (mirrors `Librarian.Available()` for
  spectral expand).
- `chunkIDs` is empty.

Unknown chunk IDs are not checked at enqueue time — the sidecar's
`--fetch` step surfaces them as an `errored` terminal status with
`@connections-error: unknown chunk <id>`. This keeps the bridge
sub-millisecond and avoids a redundant LMDB pass on enqueue.

Standard gopher-lua two-return convention. Field names follow the
project's lowerCamelCase Lua conventions (R2266 et al.).

## Sidecar Protocol

Mirrors `ark search expand` exactly. The sidecar agent runs a
single lotto-tube loop:

```
ark connections --wait
  → blocks until requests are queued; returns JSON array of
    {id, chunkIDs, timeoutSeconds} entries

for each request:
  ark connections --fetch ID
    → returns JSON array of {chunkID, fileID, path, content}
      for the chunks in the request

  (sidecar thinks: themes + shared-tag candidates + evidence)

  ark connections --result ID
    → reads JSON from stdin, validates evidence, writes the
      tmp:// doc body and flips @connections-status to completed
      payload: {themes: [...], sharedTags: [...]}

  on failure:
    ark connections --error ID="message"
      → flips @connections-status to errored, sets
        @connections-error
```

`--fetch` is the only chunk-content read path the sidecar uses
— it does not call `ark search` or anything else. The guard
script (`connections-guard.sh`) allows only `~/.ark/ark`
commands; everything else is rejected.

## Timeout and Lifecycle

- Default timeout: 60 s, clamped to [5, 300] via the `opts.timeoutSeconds`
  bridge argument.
- Server-side timeout: the orchestrator schedules a cancellation
  after the timeout; on fire, it flips `@connections-status` to
  `errored` with `@connections-error: timeout`. A late `--result`
  from the sidecar after this point is logged and discarded.
- tmp:// doc lifecycle: created on enqueue, persists until server
  restart. The workshop subscribes per-request; an old doc that
  the workshop never reopens stays around but is harmless.

## What This Does Not Do

- **Does not include any HTTP endpoint called from the browser.**
  Find Connections is initiated from Lua and observed via
  pubsub. No JS island, no fetch from the browser.
- **Does not include any custom web component.** The curation
  view renders proposals via Frictionless sub-prototypes
  (`Ark.ConnectionProposal`).
- **Does not write the proposed tags directly.** Accept flows
  through the existing `Ark:applyTagToChunks` path.
- **Does not propose `@ext:` routings or per-chunk tag triples.**
  Deferred to Phase 2B with the richer prompt.
- **Does not embed or score anything.** Pure agent inference;
  vector retrieval (trigram-zip, reworded-fuzzy) belongs to 2B.
- **Does not auto-trigger.** Pinning chunks does not start a
  request. The button is the only entry point in 1G.
- **Does not retry across server restarts.** Active requests
  vanish on restart; the workshop's subscription is invalidated
  and it falls back to a fresh button click.

## Performance

- Lua-side: zero blocking. The bridge call is sub-millisecond.
- Sidecar wall time: dominated by Claude's response. Haiku
  typically returns in 5–15 s for a handful of chunks; 60 s
  default timeout gives headroom for harder cases.
- `--fetch` cost: one LMDB read per chunk + a file system read
  per unique file containing those chunks. Single-digit ms for
  the common case (pinned set ≤ ~20 chunks).
- tmp:// rewrite throttling: 5 s elapsed-tick cadence; terminal
  transitions flush immediately.

## Test Strategy

- Enqueue a request, simulate sidecar posting `--result`, verify
  the tmp:// doc carries the expected header tags and body shape.
- Server timeout: enqueue a request, never post, wait past
  timeout, verify the doc flips to `errored` with the timeout
  message.
- Sidecar `--fetch` returns the right chunk content for known
  chunks; errors cleanly on unknown chunk IDs.
- Sidecar `--result` rejects results with empty evidence lists.
- Subscription firing: subscribe through Go's `PubSub.Subscribe`
  + `Listen` API (the test-as-subscriber pattern from
  Subtask 0's R2312) against the tmp:// doc, verify events fire
  on the terminal transition with the expected tag values.
- Lua bridge: `mcp.findConnections` returns a request ID, never
  blocks; returns `(nil, errstring)` cleanly when the agent is
  unavailable.

## Convergence With Future Phases

The shared rule across find-connections, the V4 prospector, and
Phase 2B's Magic-grade Curate: **one sidecar surface, one result
shape, multiple modes on top.**

- The V4 prospector writes the same `tmp://connections/<id>.md`
  shape, but unprompted — it pushes proposals into the inbox.
  The workshop's renderer is the same.
- Phase 2B richens the result schema with `@ext:` routings; the
  workshop's renderer gains a third proposal type.
- Sweep button retrofit: `mcp.sweepHotCorrelations()` becomes
  fire-and-forget, the existing `tmp://sweep/hot-correlations.md`
  becomes a subscription target via the same primitive, and the
  VM-blocking call is gone.

The shared substrate is the tmp:// + pubsub + sidecar pattern;
1G is the first slice that gets the full pipe end-to-end.
