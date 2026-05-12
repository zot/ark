# PubSub
**Requirements:** R778, R779, R780, R781, R782, R783, R784, R785, R786, R787, R788, R789, R790, R791, R792, R793, R794, R795, R796, R797, R798, R799, R800, R801, R802, R803, R804, R814, R815, R816, R817, R818, R819, R820, R829, R830, R831, R879, R880, R941, R942, R944, R945, R946, R2276, R2278, R2279, R2283, R2284, R2287, R2295, R2302, R2303, R2304, R2309, R2312

Subscription registry and notification delivery for tag events.
In-memory, dies with server. Agents subscribe to tag patterns and
long-poll for markdown crank-handle notifications.

Pub/sub is the primary integration plane between ark and any
consumer that observes corpus state (R2276). tmp:// paths are
first-class citizens of `TagSub.FilterFiles` glob matching —
no new struct, no new field (R2278).

## Knows
- subs: map[string][]TagSub — sessionID → subscriptions
- queues: map[string]chan Event — sessionID → bounded notification channel
- lastListen: map[string]time.Time — sessionID → last listen timestamp
- mu: sync.RWMutex — protects all maps
- ttl: time.Duration — reap threshold (default 10 minutes)
- tagSetCache: map[string]map[string]string — path → tag → last-published-value;
  used to diff each tmp:// publish so only changed (tag, value) pairs fire (R2283, R2284)

### TagSub
- Tag: string — exact tag name to match
- ValueRE: *regexp.Regexp — optional, nil = match any value
- FilterFiles: []doublestar.Pattern — only match these paths (nil = all)
- ExcludeFiles: []doublestar.Pattern — exclude these paths (R945, renamed from ExceptFiles)
- Hits: uint64 — events successfully enqueued (atomic)
- Drops: uint64 — events lost to full queue (atomic)
- (R879, R880) Schedule field removed — scheduling driven by ark.toml + day buckets, not subscriptions

### Event
- Tag: string — the matched tag name
- Value: string — the tag's content
- Path: string — file that produced the tag
- Time: time.Time — when the event occurred

## Does
- Subscribe(sessionID string, subs []TagSub): add subscriptions for
  a session. Creates queue channel if needed. Resets lastListen.
  Append semantics preserved for HTTP and direct Go callers
  (R2309); the Lua bridge layers replace-by-(session, tag)
  on top.
- Cancel(sessionID string, tag string, value string): remove
  subscriptions. Empty tag = cancel all. Empty value = cancel all
  for that tag. Non-empty value = cancel only subs whose ValueRE
  would match the value.
- Publish(sessionID string, path string, tags []TagValue):
  called for both persistent-file indexing and tmp:// document
  writes. sessionID is the writer — excluded from self-notification.
  For each tag, scan subs across all sessions: check tag name,
  check ValueRE against value, check FilterFiles/ExcludeFiles via
  `matchFileFilters` (which handles tmp:// paths identically to
  persistent paths, R2278, R2279). On match: non-blocking send to
  session's queue (R2302 — drops on overflow).
- PublishTmpDiff(writerID, path string, content []byte, strategy string):
  centralized tmp:// publish helper called from `db.AddTmpFile`,
  `db.UpdateTmpFile`, `db.AppendTmpFile`. Extracts tags via
  `ExtractTagValues`; for AppendTmpFile callers, the helper is
  invoked with the full resulting content (existing + appended)
  so prior tags don't re-fire (R2286). Diffs the new tag-set
  against `tagSetCache[path]`; calls Publish with only changed
  `(tag, value)` pairs (R2284). Updates the cache after the
  publish returns. On a path's first publish, the prior set is
  empty so every present tag fires (R2285).
- ClearTagSetCache(path string): drop the cached tag-set for a
  path. Called from `db.RemoveTmpFile`. On server restart the
  cache reinitializes empty (R2287).
- CompressBatch(events []Event) []Event: given a batch from
  `Listen`, return one event per `(path, tag)` keeping the latest.
  Operates on `[]Event` (Go structs); no Lua table construction
  here (R2295, R2296).
- QueueDepth(sessionID string) int: return current queue length
  via `len(ps.queues[sessionID])` under the existing read lock.
  Monitor read API (R2303).
- LastListenAt(sessionID string) time.Time: return
  `ps.lastListen[sessionID]` under the existing read lock.
  Monitor read API (R2304).
- Listen(sessionID string, timeout time.Duration) []Event: block on
  session's queue channel or timeout. Drain all available events
  after first arrives (non-blocking reads until empty). Update
  lastListen timestamp. Return events.
- FormatMarkdown(events []Event) string: render events as markdown
  crank handles. Each event gets a descriptive heading and file
  references (ark fetch path, ark chunks commands). Separated by
  `---` dividers.
- List(sessionID string) []SubInfo: return subscription details.
  Empty sessionID returns all sessions. Includes hit/drop counts.
- Stats(sessionID string) []SubStats: return aggregate hit/drop
  counts. Empty sessionID aggregates across all sessions.
- Reap(): called by server ticker. Scan lastListen map, drop any
  session whose lastListen is older than ttl. Close and delete
  queue channel, delete subs.

## Collaborators
- Server: owns the PubSub instance, wires HTTP handlers, starts reaper ticker, runs per-session listening goroutines that drain Listen and dispatch into the Lua VM
- Indexer: calls Publish after tag extraction in AppendFile and prepareRefresh
- DB: calls PublishTmpDiff from AddTmpFile / UpdateTmpFile / AppendTmpFile after the actor write commits
- CLI: subscribe and listen commands proxy to server

## Sequences
- seq-pubsub.md
- seq-tmp-subscription.md
