# PubSub
**Requirements:** R778, R779, R780, R781, R782, R783, R784, R785, R786, R787, R788, R789, R790, R791, R792, R793, R794, R795, R796, R797, R798, R799, R800, R801, R802, R803, R804, R814, R815, R816, R817, R818, R819, R820, R829, R830, R831, R879, R880, R941, R942, R944, R945, R946, R2276, R2278, R2279, R2283, R2284, R2287, R2295, R2302, R2303, R2304, R2309, R2312, R2457, R2458, R2459, R2460, R2461, R2462, R2463, R2464, R2465, R2466, R2467, R2468, R2469, R2470, R2471, R2802, R2803, R2804

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
- Kind: enum {Tag, FileTag} — selects matching behavior (R2457, R2460)
- Predicate: TagMatcher.MatchPredicate — parsed `[~]NAME [(=|:|~) VALUE]` from `-tag` / `-file-tag`; supersedes the prior `Tag string + ValueRE *regexp` pair (R2442, R2457, R2458, R2460)
- FilterFiles: []doublestar.Pattern — only match these paths (nil = all)
- ExcludeFiles: []doublestar.Pattern — exclude these paths (R945, renamed from ExceptFiles)
- FileTagMembers: map[uint64]bool — set of fileIDs currently matching this entry's predicate; populated/maintained only when Kind == FileTag (R2463, R2469)
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
  (R2309); the Lua bridge layers replace-by-(session, predicate)
  on top. `-tag` and `-file-tag` arguments arrive already parsed
  into MatchPredicates and tagged with Kind (R2442, R2462).
- Cancel(sessionID string, predicate MatchPredicate): remove
  subscriptions. A zero predicate cancels all entries for the
  session. A predicate with name-only mode cancels every entry
  whose name matches. A predicate with name+value cancels every
  entry whose predicate would match the same (name, value) pair
  (R2458 replaces the old `tag/value` parameter pair).
- Publish(sessionID string, path string, fileID uint64, tags []TagValue, fileTags []TagValue):
  called for both persistent-file indexing and tmp:// document
  writes. sessionID is the writer — excluded from self-notification.
  Two-pass match across subs in all sessions:
  1. **Tag-kind subs** (R2457, R2459): for each `tv` in `tags`, find
     subs where Predicate.Match(tv) and path passes file filters.
     Non-blocking send to session queue (R2302 — drops on overflow).
  2. **FileTag-kind subs** (R2460–R2471): for each such sub,
     re-evaluate `wasMember = sub.FileTagMembers[fileID]` against
     `isMember = anyMatch(sub.Predicate, fileTags)`. Transition
     table:
     - was=N, is=N → no-op (R2468)
     - was=N, is=Y → set member, deliver chunk as entry event
       (R2465)
     - was=Y, is=Y → deliver chunk (R2466)
     - was=Y, is=N → unset member, deliver chunk as exit event
       (R2467)
     The `fileTags` argument is the authoritative per-file tag
     aggregate (R2464), passed in by Indexer / DB so PubSub does
     not need a store handle. `@mute: true` short-circuits the
     entire fileTag pass (R2470).
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
- SubscriberCount(tagName, tagValue string) int: walk the
  subscription registry under the read lock; return the number of
  entries whose `Predicate` would accept the synthesized
  `TagValue{Name: tagName, Value: tagValue}` if it were published
  right now. Name normalization (`@` stripping, trailing-`:`
  handling) and value-mode handling are identical to the publish
  path. Per-subscription file filters are intentionally ignored —
  the query answers "could anyone receive this?", not "would a
  specific path pass each subscriber's filter?". TTL-expired
  sessions are not counted (the reaper drops them on its cadence;
  the query treats whatever remains in the registry as live).
  Returns zero when no entry matches. (R2802, R2803, R2804)

## Collaborators
- Server: owns the PubSub instance, wires HTTP handlers, starts reaper ticker, runs per-session listening goroutines that drain Listen and dispatch into the Lua VM
- Indexer: calls Publish after tag extraction in AppendFile and prepareRefresh
- DB: calls PublishTmpDiff from AddTmpFile / UpdateTmpFile / AppendTmpFile after the actor write commits
- CLI: subscribe and listen commands proxy to server. The new
  `subscribers` command consumes `SubscriberCount` via an HTTP
  handler. (R2805)
- RecallWatcher (crc-RecallWatcher.md): consumes `SubscriberCount`
  to gate the curation-doc write (R2806).
- RecallAgentBuilder (crc-RecallAgentBuilder.md): consumes
  `SubscriberCount` to gate the result-doc write inside `Close`
  (R2807).

## Sequences
- seq-pubsub.md
- seq-tmp-subscription.md
