# Test Design: tmp:// Subscription

**Source:** crc-PubSub.md, crc-Server.md, crc-DB.md, specs/tmp-subscription.md

The canonical pattern: **tests are subscribers**. Tests use the
same Go API the Lua bridge uses; no mocks, no test-only seams.
Substrate bugs (e.g. an internal caller that fails to publish)
are caught by tests that exercise the real substrate. (R2312)

## Test: HTTP-driven tmp:// publish still works (regression)

**Purpose:** R2279 — confirm the existing HTTP handler path continues
to deliver events after the centralized DB-layer publish replaces
the manual `PublishAndWatch` calls.
**Input:** subscribe `("test", connections-status, FilterFiles=["tmp://t/x.md"])`.
POST /tmp/add with path `tmp://t/x.md`, content
`"@connections-status: pending\n"`.
**Expected:** `Listen("test", 1s)` returns one event with
`Value="pending"`. Hits counter = 1.
**Refs:** R2279

## Test: AddTmpFile publishes via centralized path

**Purpose:** R2281, R2285 — direct DB.AddTmpFile call publishes
every present tag because the prior tag-set is empty.
**Input:** subscribe `("test", connections-status, FilterFiles=["tmp://t/x.md"])`.
Call `db.AddTmpFile("tmp://t/x.md", "markdown", []byte("@connections-status: pending\n"))`
directly (no HTTP).
**Expected:** Listen returns one event with `Value="pending"`.
**Refs:** R2281, R2285

## Test: UpdateTmpFile publishes only changed tags

**Purpose:** R2281, R2284 — only-on-change diff; unchanged tags
don't re-fire.
**Input:** subscribe with `tag="status"` (any value). AddTmpFile with
`"@status: idle\n@kind: report\n"`. Drain Listen (gets two events).
UpdateTmpFile same path with `"@status: idle\n@kind: report\n"` —
identical content.
**Expected:** Listen times out (no events; nothing changed).
Then UpdateTmpFile with `"@status: running\n@kind: report\n"` —
status changed. Listen returns one event for `status: running` only.
**Refs:** R2281, R2284

## Test: AppendTmpFile diffs against whole resulting content

**Purpose:** R2286 — appending content where the new bytes carry a
tag already present in prior content doesn't re-publish that tag.
**Input:** subscribe with `tag="topic"`. AddTmpFile with
`"@topic: ark\nfirst chunk\n"`. Drain (one event). AppendTmpFile
with `"@topic: ark\nsecond chunk\n"` (same tag value in append).
**Expected:** Listen times out — no event, because the resulting
content's tag-set matches the cached prior set for the path.
**Refs:** R2286

## Test: RemoveTmpFile clears tag-set cache

**Purpose:** R2287 — after RemoveTmpFile, the next AddTmpFile on
the same path publishes every tag (prior set is empty again).
**Input:** Add `"@status: done\n"`, drain. RemoveTmpFile. Add
`"@status: done\n"` again.
**Expected:** second Add fires one event (cache was cleared on
Remove).
**Refs:** R2287

## Test: Internal caller (sweep path) publishes correctly

**Purpose:** R2280, R2281 — the centralization closes the
internal-caller bug surface. The sweep's `tmp://sweep/...` write
must fire subscribers.
**Input:** subscribe with `tag="sweep-status"`,
`FilterFiles=["tmp://sweep/hot-correlations.md"]`. Drive the sweep
progress doc writer (whichever internal path Librarian uses) to
write `"@sweep-status: complete\n"`.
**Expected:** Listen returns one event with `Value="complete"`.
Same code path as HTTP delivery.
**Refs:** R2280, R2281

## Test: matchFileFilters handles tmp:// paths first-class

**Purpose:** R2278 — confirm doublestar glob matching works for
tmp:// paths (literal, single-component glob, and recursive glob).
**Input:** three subscribers:
- `FilterFiles=["tmp://connections/req1.md"]` (literal)
- `FilterFiles=["tmp://connections/*.md"]` (glob)
- `FilterFiles=["tmp://**/*.md"]` (recursive)
Publish on `tmp://connections/req1.md`.
**Expected:** all three subscribers receive the event.
Publish on `tmp://other/req2.md`: only the recursive subscriber
matches.
**Refs:** R2278

## Test: CompressBatch dedupes (path, tag) by latest

**Purpose:** R2295, R2310 — batched events with multiple entries
for the same `(path, tag)` collapse to the latest one.
**Input:** `events := []Event{ {path:"p", tag:"x", value:"1"},
{path:"p", tag:"x", value:"2"}, {path:"p", tag:"y", value:"a"},
{path:"p", tag:"x", value:"3"} }`. Call `CompressBatch(events)`.
**Expected:** length 2; one entry `{p, x, "3"}` (latest); one entry
`{p, y, "a"}` (only one).
**Refs:** R2295, R2310

## Test: Listening goroutine batched dispatch

**Purpose:** R2294, R2295 — listening goroutine drains a batch and
makes a single WithLua call per batch, regardless of event count.
**Input:** subscribe + onpublish via the Lua bridge. Publish 5
events for the same subscriber in rapid succession before any
Listen drain. (Simulated by holding the Lua VM until they
accumulate.)
**Expected:** WithLua is called once; the Lua callback receives an
array of 5 events (or fewer if compression applies).
**Refs:** R2294, R2295

## Test: Re-subscribe replaces prior (session, tag)

**Purpose:** R2290 — calling `mcp.subscribe` for an existing
(session, tag) drops the prior sub and adds the new one.
**Input:** via Lua bridge: subscribe `("curation", tag="x",
filterFiles=["tmp://a.md"])`. Subscribe again
`("curation", tag="x", filterFiles=["tmp://b.md"])`.
Publish on `tmp://a.md`.
**Expected:** no event fires (prior sub on `tmp://a.md` was
replaced). Publish on `tmp://b.md` fires.
**Refs:** R2290

## Test: One callback per session (onpublish replace)

**Purpose:** R2291 — re-registering onpublish replaces the prior
callback.
**Input:** via Lua bridge: register cb1. Subscribe + publish — cb1
fires. Register cb2 (replace). Publish again.
**Expected:** cb2 fires; cb1 does not.
**Refs:** R2291

## Test: Cancel all drops session state and stops goroutine

**Purpose:** R2292, R2300 — `mcp.cancel(session, "")` drops all
subs, cleans up `ps.queues`/`ps.subs`/`ps.lastListen`, stops the
listening goroutine.
**Input:** subscribe, then `mcp.cancel("curation", "")`.
**Expected:** `ListSubscriptions` shows no entries for `"curation"`.
A subsequent publish that would have matched does nothing
(no goroutine to dispatch). Listening goroutine exits cleanly.
**Refs:** R2292, R2300

## Test: QueueDepth and LastListenAt report correctly

**Purpose:** R2303, R2304 — monitor read APIs return current state.
**Input:** subscribe `("mon", ...)`. Publish 3 events without
draining. Read QueueDepth("mon"). Then Listen drains. Read
LastListenAt("mon").
**Expected:** QueueDepth returns 3 (assuming queue capacity ≥ 3).
After drain, QueueDepth returns 0. LastListenAt returns a time
within the test's recent window.
**Refs:** R2303, R2304

## Test: Cross-session admin observation

**Purpose:** R2293 — one Lua VM can register onpublish for a
different session string and receive its events.
**Input:** in app A, register `mcp.onpublish("appB", cb)` and
`mcp.subscribe("appB", {...})`. From app B's code, write a
matching tmp:// doc.
**Expected:** app A's cb fires with the event.
**Refs:** R2293

## Test: Subscribe-before-doc-exists is valid

**Purpose:** R2311 — subscribing to a path that doesn't exist yet
registers the sub; events fire when the path is first created.
**Input:** subscribe with `FilterFiles=["tmp://later/x.md"]`. Wait
1s (no events). AddTmpFile to `tmp://later/x.md`.
**Expected:** Listen returns the event from the AddTmpFile after
the path was created.
**Refs:** R2311

## Test: Backpressure increments drops, not unbounded growth

**Purpose:** R2302 — queue overflow drops, doesn't block the
publisher.
**Input:** subscribe with a small queue capacity (test-only by
constructing a PubSub with queueDepth=4). Publish 20 events without
draining.
**Expected:** sub.Drops counter ≈ 16 after the burst. sub.Hits ≈ 4.
Publisher never blocks.
**Refs:** R2302
