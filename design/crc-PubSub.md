# PubSub
**Requirements:** R778, R779, R780, R781, R782, R783, R784, R785, R786, R787, R788, R789, R790, R791, R792, R793, R794, R795, R796, R797, R798, R799, R800, R801, R802, R803, R804, R814, R815, R816, R817, R818, R819, R820, R829, R830, R831, R879, R880

Subscription registry and notification delivery for tag events.
In-memory, dies with server. Agents subscribe to tag patterns and
long-poll for markdown crank-handle notifications.

## Knows
- subs: map[string][]TagSub — sessionID → subscriptions
- queues: map[string]chan Event — sessionID → bounded notification channel
- lastListen: map[string]time.Time — sessionID → last listen timestamp
- mu: sync.RWMutex — protects all maps
- ttl: time.Duration — reap threshold (default 10 minutes)

### TagSub
- Tag: string — exact tag name to match
- ValueRE: *regexp.Regexp — optional, nil = match any value
- FilterFiles: []doublestar.Pattern — only match these paths (nil = all)
- ExceptFiles: []doublestar.Pattern — exclude these paths
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
- Cancel(sessionID string, tag string, value string): remove
  subscriptions. Empty tag = cancel all. Empty value = cancel all
  for that tag. Non-empty value = cancel only subs whose ValueRE
  would match the value.
- Publish(sessionID string, path string, tags map[string]uint32,
  content []byte): called from tag extraction path. sessionID is the
  writer — excluded from self-notification. For each tag, scan subs
  across all sessions: check tag name match, check ValueRE against
  tag's value from content, check file filters against path. On
  match: non-blocking send to session's queue. Drop if full.
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
- Server: owns the PubSub instance, wires HTTP handlers, starts reaper ticker
- Indexer: calls Publish after tag extraction in AppendFile and prepareRefresh
- CLI: subscribe and listen commands proxy to server

## Sequences
- seq-pubsub.md
