# tmp:// Subscription

The substrate primitive that lets Frictionless apps (and tests,
monitors, scripts, agents) subscribe to tag streams on tmp://
documents through the existing pubsub. The gating piece for every
async ark feature that surfaces in Frictionless: find-connections,
the sweep retrofit, the V4 inspiration prospector, and the Ark
monitor.

Language: Go. Environment: ark server (the index + pubsub + flib Lua
runtime). All work is server-side; no HTTP from the browser.

## What it enables

Six distinct consumers, one substrate:

1. **Find Connections (1G).** Sidecar writes results to
   `tmp://connections/<id>.md`; curation view subscribes to
   `@connections-status: completed`.
2. **Sweep retrofit.** `mcp.sweepHotCorrelations()` becomes
   fire-and-forget; workshop subscribes to `@sweep-status` on
   the existing `tmp://sweep/hot-correlations.md`.
3. **Scheduling (V3).** PLAN.md:315 lists `mcp:subscribe(opts,
   callback)` as a calendar-view deliverable: the UI rebinds on
   schedule-tag changes so the agenda reflects edits in real
   time. Scheduled-event fires reach Frictionless through the
   same subscription path. Error reporting flows through
   `tmp://errors/scheduling` (specs/pubsub.md:341), which a
   "scheduling problems" view or the Ark monitor subscribes to.
   The primitive is the substrate for both the pull
   (`mcp:scheduled(start, end)` range query, separate slice) and
   the push (subscription-driven rebinds and dispatcher fires)
   sides of Scheduling.
4. **V4 inspiration prospector.** Push-side proposals arrive as
   tagged tmp:// docs; the curation inbox subscribes.
5. **Ark monitor.** Debugging subscriber that joins
   `ListSubscriptions`, queue depth, last-listen-time, and an
   S-record cursor for "what changed since I started."
6. **Tests as alternative front-ends.** A Go test subscribes to
   the same tmp:// doc the UI does and asserts against what
   lands. No mocks, no test-only seams.

## Architectural position

**Pub/sub is the integration plane.** UIs, tests, monitors, and
agents are *symmetric subscribers*: same Go API, same Lua API,
no special-cased test paths. The tag-on-tmp-doc is the durable
contract between the producer (sidecar / sweep / indexer) and
the consumer (whoever subscribes).

**Central Go API; the Lua bridge is one of several front-ends.**
The Go API is the canonical contract. The Lua bridge is a thin
wrapper; tests and the monitor hit the Go API directly.

**tmp:// paths are first-class citizens of the existing filter
machinery.** `TagSub.FilterFiles` and `ExcludeFiles` already do
glob matching. tmp:// paths slot in without a new struct, new
field, or special case.

## Publisher path

### What exists today

tmp:// publishing already works **for HTTP-driven writes**. The
three handlers `handleTmpAdd` (server.go:1568), `handleTmpUpdate`
(server.go:1614), and `handleTmpAppend` (server.go:1673) each
call `srv.pubsub.PublishAndWatch("", path, ExtractTagValues(content, strategy))`
after their `SyncVoid` write returns. `matchFileFilters`
(pubsub.go:463) uses doublestar glob matching that already
handles tmp:// paths first-class — the existing test in production
is HTTP tmp:// writes that fire subscribers correctly.

### What's missing (the actual gaps)

1. **Internal callers don't publish.** Several Go-side callers of
   the DB-layer tmp:// write methods skip the publish step:
   - `librarian.go:1374` and `librarian.go:1378` — the **sweep**
     writes `tmp://sweep/hot-correlations.md` directly via
     `db.UpdateTmpFile` / `db.AddTmpFile` and never publishes.
     The sweep's `@sweep-status: complete` transition reaches no
     subscriber today.
   - `pubsub.go:85` — Watchdog appends to `tmp://watchdog/...`
     without publishing.
   - `server.go:292` — missed-events tmp:// write.
   - `server.go:563-565` — indexer-side tmp:// reset path.
   - `server.go:3205` — Lua `tmp_add` bridge.
   Every internal caller is a current bug surface: a tmp:// doc
   they rewrite is silent to subscribers.

2. **Publish is duplicated, not centralized.** The publish step
   lives in three HTTP handlers as a copy-pasted call, not in
   the DB-layer methods that actually write the content. Any
   new caller of `db.AddTmpFile` (etc.) has to remember to also
   call `PublishAndWatch` — easy to forget, exactly as the
   internal callers above demonstrate.

3. **No only-on-change diff.** Every publish re-publishes every
   tag in the new content. A sweep that rewrites
   `tmp://sweep/hot-correlations.md` every 250 ms with
   `@sweep-progress: 0.30` → `0.31` → `0.32` and unchanged
   `@sweep-status: running` would re-fire `@sweep-status` events
   forever even though nothing changed about that tag.

### The fix

Move the publish step **into** the DB-layer methods:

- `db.AddTmpFile(path, strategy, content)` — after the actor
  commits, extract tags, diff against the previously cached
  tag-set for this path (empty for a new path = everything
  changes), publish only changed tags, update the cache.
- `db.UpdateTmpFile(path, strategy, content)` — same diff
  against the cached prior tag-set.
- `db.AppendTmpFile(path, strategy, content)` — same. Append
  case: extract tags from the **whole resulting content**
  (existing + appended), diff against prior. The
  alternative — diffing against just-appended content — would
  fire on every line carrying a tag that's already settled.
- HTTP handlers stop calling `PublishAndWatch` manually; the
  centralized DB path does it.

This is the **only-on-change** policy plus the centralization
that closes the internal-caller gaps in one move. A sweep that
rewrites progress every 250 ms produces one event per progress
increment, not a flood for every tag in the doc.

### Tag-set cache

Pubsub maintains an in-memory `map[path] -> map[tag]value`
cache of the last-published tag-set per tmp:// path. Updated
inside the publish helper after each successful publish. Goes
away when the tmp:// path is removed (`RemoveTmpFile`) or on
server restart (matches the tmp:// lifecycle).

### Why only-on-change

The doc body carries the current state at all times. Pubsub
events are *notifications* — "go look at the doc now" — not
the state-of-record. Redundant intermediate values don't add
information; they only inflate event volume. Subscribers who
need the full state at any moment read the doc body.

For an `AddTmpFile` (new path), every present tag is a "change"
relative to the prior empty set, so all current tags publish.
For a `RemoveTmpFile` (path deletion), the cached prior tag-set
is cleared; no events fire for the removal itself (subscribers
that need to know about deletion subscribe to a tag the
publisher sets — e.g., a final `@status: gone`).

## Filter machinery (unchanged)

`TagSub` (pubsub.go:19) already carries the fields needed:

```go
type TagSub struct {
    Tag          string
    ValueRE      *regexp.Regexp
    FilterFiles  []string  // glob patterns
    ExcludeFiles []string  // glob patterns
    Hits, Drops  atomic.Uint64
}
```

A subscriber wanting `connections-status: completed` for a
specific request:

```go
&TagSub{
    Tag:         "connections-status",
    ValueRE:     regexp.MustCompile("^completed$"),
    FilterFiles: []string{"tmp://connections/fc-7Yp2K3.md"},
}
```

`FilterFiles` glob-matches against the publish path. tmp:// paths
match the same way persistent paths do. A glob like
`tmp://prospector/*.md` covers a path family.

## Lua bridge

Three new methods on the `mcp` table. **All three take an
explicit sessionID** as the first argument, mirroring the Go
signatures `Subscribe(sessionID, ...)` /
`Listen(sessionID, ...)` / `Cancel(sessionID, ...)`.

```lua
-- sessionID is a logical bookkeeping namespace. Default
-- convention: app + view (here, the ark app's curation view).
local me = "curation"

-- 1. Register the per-session onpublish callback. One per
-- session; re-registering replaces. Receives a Lua array of
-- compressed event tables in a single call (see "Batched
-- dispatch" below).
mcp.onpublish(me, function(events)
    for _, e in ipairs(events) do
        -- e is a Lua table mirroring the Go Event struct:
        -- e.path, e.tag, e.value, e.time, e.writerID, ...
    end
end)

-- 2. Register (or replace) a subscription. One sub per
-- (session, tag) pair. Re-subscribing for the same (session,
-- tag) drops the prior sub and adds the new one. Filter table
-- fields mirror TagSub one-to-one; optional fields can be
-- omitted or nil.
mcp.subscribe(me, {
    tag          = "connections-status",
    valueRE      = "^(completed|errored)$",
    filterFiles  = { "tmp://connections/fc-7Yp2K3.md" },
    excludeFiles = nil,
})

-- 3. Cancel. mcp.cancel(session, tag) drops the sub for that
-- tag; mcp.cancel(session, "") drops all subs for the session
-- (and stops its listening goroutine).
mcp.cancel(me, "connections-status")
```

### Replace semantics

- **`mcp.subscribe(session, filter)`** is *replace by (session, tag)*.
  Implemented in the bridge as `PubSub.Cancel(session, tag, "")`
  followed by `PubSub.Subscribe(session, [newSub])`. Two lock
  acquires; if a `Publish` lands between them, an event for the
  not-yet-subscribed filter is missed — which matches the user's
  intent ("drop old, start new"). The constraint is a
  Lua-bridge convention; the Go `Subscribe` API keeps its
  append semantics for HTTP and direct Go callers.
- **`mcp.onpublish(session, fn)`** is *replace by session*. One
  callback per session; re-registering overwrites.

Parallel subs on the same tag are not exposed through Lua. Use
glob `filterFiles` patterns for multi-path coverage
(`tmp://prospector/*.md` covers all of a tag's paths).

### Sessions as bookkeeping namespaces

`sessionID` is a string the app picks. The default convention
is "app name" or "view name" (e.g. `"curation"`), but apps are
free to use finer granularity — per-panel, per-request, etc.
Each session has its own queue, listening goroutine, and
onpublish callback. Sessions are cheap.

A common pattern for request-scoped lifecycles:

```lua
local reqSession = "curation:fc-" .. requestID
mcp.onpublish(reqSession, function(events) ... end)
mcp.subscribe(reqSession, {
    tag = "connections-status",
    filterFiles = { "tmp://connections/" .. requestID .. ".md" },
})
-- on completion or cancel:
mcp.cancel(reqSession, "")  -- wipes everything for this request
```

### Admin observation

Because sessionID is an explicit argument, one app can register
`mcp.onpublish("otheraapp", fn)` and `mcp.subscribe("otherapp", ...)`
to observe another session's events through its own VM. This is
how the Ark monitor watches multiple sessions simultaneously —
registers per-watched-session onpublish, runs the callback in
its own LState.

## Session-listening scaffolding

Per session with at least one subscription, the server runs one
listening goroutine:

```go
for {
    events, ok := pubsub.Listen(sessionID, listenTimeout)
    if !ok || len(events) == 0 {
        // session cancelled / timed out — check for shutdown
        continue
    }
    compressed := compressBatch(events)  // (path, tag) → latest
    srv.uiRuntime.WithLua(func(rt *cli.LuaRuntime) error {
        L := rt.State
        cb := lookupCallback(sessionID)
        if cb == nil { return nil }
        arr := buildEventArrayTable(L, compressed)
        return L.CallByParam(
            lua.P{Fn: cb, NRet: 0, Protect: true}, arr)
    })
}
```

`srv.uiRuntime.WithLua` is the existing flib closure-actor
primitive (server.go:2834 et al.) — used throughout
`registerLuaFunctions` to serialize Go calls into the Lua VM.
The listening goroutine inherits this pattern.

### Batched dispatch

The goroutine drains one batch from `Listen` and makes **one**
`WithLua` call per batch, regardless of event count. The
callback receives the compressed array; iteration happens
inside Lua. This keeps the Lua-VM hop count bounded by batch
frequency, not event rate.

### Compression policy

`compressBatch` operates on `[]Event` (Go structs). For each
`(path, tag)` pair seen in the batch, only the latest event
survives. Earlier events for the same pair are dropped.

**Order matters:** compression runs *before* `WithLua`. Lua
tables are constructed only for events that survive compression
(via `buildEventArrayTable`). Avoids allocating Lua tables for
events the bridge would immediately discard.

A 60-second sweep with `@sweep-progress` ticking every 250 ms
produces ~240 raw events; after compression each batch typically
holds 1–2 events per tag (the latest snapshot at the batch
boundary). The UI re-renders once per batch, not per tick.

### Goroutine lifecycle

- **Start:** first `mcp.subscribe(session, ...)` call for a
  session. The bridge creates the per-session queue (already
  done by `PubSub.Subscribe`), launches the listening goroutine,
  and registers the goroutine handle alongside the callback.
- **Stop:** when the session's subscription count reaches zero
  via `mcp.cancel(session, "")` or `mcp.cancel(session, tag)`
  for the last remaining tag. The bridge stops the goroutine,
  unregisters the callback, and calls `PubSub.Cancel(session, "", "")`
  to drop the queue.
- **Server shutdown:** all listening goroutines stop cleanly via
  the existing flib shutdown path.

### Backpressure

If the Lua VM is busy and `WithLua` queues, events accumulate
in the per-session pubsub channel (existing behavior;
`ps.queues[sessionID]` has a fixed capacity). Overflow
increments `sub.Drops` per existing logic. No new flow control
needed.

## Callback signature

The Lua callback receives one argument — a Lua array (1-indexed
table) of event tables. Each event table mirrors the Go `Event`
struct field-for-field, with lowerCamelCase field names per the
project convention (R2266 et al.).

```lua
function(events)
    for _, e in ipairs(events) do
        -- e.path, e.tag, e.value, e.time, e.writerID, ...
        -- (all current Event fields, automatically reflected)
    end
end
```

Future additions to the Go `Event` struct surface in Lua
automatically; the bridge constructs the table dynamically from
the struct's field set.

## Monitor support

The Ark monitor needs three queries plus the S-substrate cursor.
`ListSubscriptions` already exists; this slice adds the other two.

```go
// QueueDepth returns the current event-queue length for a session.
// Used by the monitor to surface "events queued behind a slow
// subscriber" without polling.
func (ps *PubSub) QueueDepth(sessionID string) int

// LastListenAt returns the timestamp of the session's most
// recent Listen drain. Lets the monitor flag stalled subscribers.
func (ps *PubSub) LastListenAt(sessionID string) time.Time
```

Both read from existing `ps.queues` / `ps.lastListen` state
under the existing lock — no new state.

For "what changed since I connected," the monitor uses
`Store.RecordSerial` + `Store.WalkRecordsSinceSerial`
(R2174–R2193, vector freshness substrate). The monitor pulls
the current serial at startup as its baseline and filters out
older changes by stamp comparison.

## What this does not do

- **Does not buffer event history.** Subscribers see live
  events only. Current state lives in the tmp:// doc's tags at
  subscribe time; a subscriber connecting after a terminal
  transition reads the doc body to learn the current state. No
  replay primitive in this slice.
- **Does not introduce HTTP-from-browser endpoints.** The
  primitive is Lua-and-Go only. (The existing HTTP
  `POST /subscribe` from `pubsub.md` is unchanged; it does not
  yet route through the new tmp:// publisher path either —
  whether to wire that in is a separate decision.)
- **Does not expose handle-based cancellation from Lua.**
  Re-subscribing is the cancel via the replace-by-tag rule.
- **Does not change the Go `Subscribe` API.** Go callers keep
  append semantics and value-pattern Cancel. The Lua bridge
  layers tighter conventions on top.
- **Does not buffer or coalesce across batches.** Compression
  is intra-batch only. A `@sweep-progress` increment that lands
  in a batch right after a prior batch's drain produces a fresh
  event, not a continuation of the prior batch.
- **Does not auto-subscribe.** Subscriptions are explicit per
  session.
- **Does not unify with the existing pubsub HTTP/CLI surface
  beyond reusing primitives.** `ark subscribe` and `ark listen`
  (cli) still target sessions that long-poll via HTTP. The Lua
  bridge is its own consumer of the same underlying PubSub.

## Sweep retrofit (a same-day follow-up)

Once the primitive lands, `mcp.sweepHotCorrelations()` gets
split into fire-and-forget + subscribe:

```lua
local me = "curation"
mcp.onpublish(me, onAnyEvent)
mcp.subscribe(me, {
    tag         = "sweep-status",
    valueRE     = "^complete$",
    filterFiles = { "tmp://sweep/hot-correlations.md" },
})
mcp.startSweepHotCorrelations()  -- returns immediately
-- onAnyEvent fires with the terminal status when the sweep completes
```

The existing `tmp://sweep/hot-correlations.md` doc already
carries `@sweep-status`. The retrofit is a small additive
change: rename the current `mcp.sweepHotCorrelations` to
`mcp.startSweepHotCorrelations` and remove the
`Librarian.SweepHotCorrelations` synchronous return path from
the Lua side. Go callers keep the synchronous path.

## Performance

- **Publisher overhead per tmp:// write:** one
  `ExtractTagValues` pass over the new content (linear in
  content size, already O(n)); one map diff against the cached
  prior tag-set (linear in tag count, typically small); one
  `PubSub.Publish` call per changed tag.
- **Tag-set cache footprint:** one entry per actively-watched
  tmp:// path. Each entry stores the prior tag-set
  (map[tag]value or equivalent). Tens of paths × tens of tags ≈
  bytes per entry × few hundred entries ≈ negligible.
- **Listening goroutine:** one per session with subscriptions.
  Blocks in `Listen` until events arrive or timeout. No CPU
  cost when idle.
- **Compression:** O(events in batch). Hash map by `(path, tag)`,
  pointer-keep-latest. Sub-microsecond per event.
- **Lua-VM hop:** one `WithLua` per batch. Cost dominated by
  Lua table construction (O(events × fields)) plus user
  callback execution.

## Test strategy

The test-as-subscriber pattern is foundational. Tests use the
**same Go API** the Lua bridge uses. No mocks; tests are simply
additional subscribers.

This pattern catches substrate-correctness bugs that mock-based
testing structurally cannot: e.g. the sweep silently not
publishing was invisible until something tried to subscribe.
A mock test that stubs `PublishAndWatch` would assert against
the stub and pass. A real-pubsub test asserts events arrive in
the real queue — which the sweep's missing publish would have
failed immediately. Substrate bugs that hurt the real system
get caught by tests that exercise the real substrate.

```go
func TestPublishOnAddTmpFile(t *testing.T) {
    db, srv := newTestServer(t)
    defer srv.Close()

    db.PubSub().Subscribe("test", []*pubsub.TagSub{{
        Tag:         "connections-status",
        FilterFiles: []string{"tmp://connections/test.md"},
    }})

    db.AddTmpFile("tmp://connections/test.md", "markdown",
        []byte("@connections-status: completed\n"))

    events := db.PubSub().Listen("test", 1*time.Second)
    require.Len(t, events, 1)
    require.Equal(t, "completed", events[0].Value)
}
```

Coverage targets:

- Publish fires on `AddTmpFile` with each new tag in the
  content.
- Publish fires on `UpdateTmpFile` only for tags whose value
  changed (only-on-change).
- Publish does NOT fire on `UpdateTmpFile` when the new
  content has the same tag-set as the prior.
- `FilterFiles` glob matching applies to tmp:// paths correctly.
- Two subscribers on the same `(tag, path)` both receive events
  (broadcast).
- `Cancel(session, tag, "")` drops all subs for that tag.
- Batch compression: a batch with multiple events for the same
  `(path, tag)` reduces to one event (latest value).
- Monitor APIs: `QueueDepth`, `LastListenAt` return correct
  values.

Lua-bridge tests live alongside the Go tests but exercise
`mcp.subscribe` / `mcp.onpublish` / `mcp.cancel` from a test
harness that drives a real Lua VM through `WithLua`. Same
substrate, different front-end — exactly the pattern apps will
use.

## Convergence

Once this lands, every async-Frictionless feature reuses the
substrate without bespoke plumbing:

- find-connections (1G) wires up.
- Sweep retrofit lands the same day.
- Scheduling (V3) gets its UI-side `mcp:subscribe(opts, callback)`
  deliverable directly. Calendar view rebinds on schedule-tag
  changes; agent dispatcher receives scheduled fires through
  the same path; scheduling errors flow through
  `tmp://errors/scheduling` to whoever watches them.
- V4 inspiration prospector publishes proposals as tagged tmp://
  docs; the curation inbox subscribes.
- Ark monitor is mostly viewdef work — it subscribes to
  whatever it wants to observe, queries `QueueDepth` /
  `LastListenAt` / `ListSubscriptions` for the overview row.
- The pattern documentation ("substrate as integration plane;
  tests, UIs, monitors, agents are symmetric subscribers")
  can be drafted with concrete behavior to quote.

One substrate. One Go API. One Lua wrapper. Many subscribers.
