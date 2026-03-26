# EventScheduler
**Requirements:** R805, R806, R807, R809, R810, R811, R812, R821, R822, R823, R824, R825, R857, R858, R859, R860, R861, R862, R863, R864, R865, R869, R874, R875, R876, R877, R878, R902, R903, R905, R907, R899, R900, R901, R904, R906, R908, R890, R891, R892, R964, R965, R966, R967, R968, R969, R970, R971, R972, R973, R974, R978, R979

Priority queue of time-tagged events with a single timer. Reads day
buckets from LMDB at startup and on crank-forward. Delivers events
as crank handles through PubSub's listen channel. No dependency on
subscriptions for scheduling — ark.toml declares schedule tags, the
indexer writes day buckets, the scheduler reads them.

## Knows
- queue: heap of ScheduledEvent — sorted by NextFire
- timer: *time.Timer — set to the head of the queue
- pushed: map[string]bool — eventID → delivered this server lifetime
- store: *Store — reads day buckets from LMDB
- config: *Config — knows which tags are schedule tags
- scheduleDir: string — ~/.ark/schedule/
- mu: sync.Mutex — protects queue and timer

### ScheduledEvent
- ID: string — unique event identifier (derived from source path + tag)
- Tag: string — the time tag name
- Value: string — the tag's content (time + description)
- Path: string — source file containing the tag
- NextFire: time.Time — when to deliver
- Recurring: string — recurrence spec (empty = one-shot)
- IsScheduledFire: bool — true when firing at scheduled time (vs tag change)

## Does
- Add(event ScheduledEvent): push onto heap, reset timer if new
  head. Idempotent — same ID replaces existing entry. (R805)
- Remove(id string): remove from heap by ID, reset timer.
- fire(): called by timer. Pop head, deliver to all listening
  sessions via PubSub (check push records, skip duplicates).
  Event carries IsScheduledFire=true so receivers distinguish
  scheduled fires from tag-change notifications. (R806, R878)
  If lifecycle tag (Config.IsLifecycleTag): convert upcoming→fired,
  append @check-gap: DATE in same paragraph, compute next upcoming,
  re-index log file. (R964, R965, R966, R967) If non-lifecycle:
  fire through pubsub only, skip log writing. (R968)
  If recurring: crank forward — compute next occurrence,
  materialize new day-bucket entry via Store.WriteDayBuckets,
  re-enqueue. (R807, R877) Reset timer to new head.
- computeNext(recurring string, after time.Time) time.Time: parse
  recurrence spec, return next occurrence after the given time.
  Supports: "every Xm", "every Xh", "every WEEKDAY HH:MM",
  "YYYY-MM-DD HH:MM" (one-shot), "MM-DD" (annual). (R822, R823, R824)
- ParseDateValue(value string, defaultDur string) (start, end time.Time,
  description string, err error): parse a schedule tag value including
  `..` duration operator. Uses itlightning/dateparse with token-trimming
  loop. Returns start, end (using defaultDur if no `..`), and remaining
  description text. (R857, R858, R859, R860, R861, R865)
- ParseRelativeDuration(anchor time.Time, expr string) time.Time: parse
  anchored relative expressions like "one week later", "3 days later".
  (R862, R863, R864)
- ScanScheduleLogs(): called at startup. Scan ~/.ark/schedule/ for
  log files. Read @ark-event-upcoming: entries, populate the priority
  queue. Any @ark-event-upcoming: in the past gets converted to
  @ark-event-fired: and the next occurrence is computed from
  @ark-event-spec: and appended (checking for duplicates first).
  Re-index the log file after mutations. (R874, R875, R876)
  Also scan for unresolved @check-gap: entries within lookback
  window — append to tmp://watchdog/missed-events. (R972, R973, R979)
- ResolveCheckGap(tag, sourcePath, date): called when an @ack:
  covering the fired date is detected via subscription. Removes
  the @check-gap: line from the log chunk, re-indexes. (R969, R970, R971)
- EnsureUpcoming(logPath, event): called when a source file with a
  schedule tag is indexed. Ensure the log chunk exists with
  @ark-event-upcoming: entries through the forward window. Create
  log file and chunk if needed. (R902)
- AddChime(): add the quarter-chime recurring event (every 15m). (R810)

## Collaborators
- PubSub: delivers events through listen channels
- Store: reads day buckets, writes new buckets on crank-forward
- Config: knows which tags are schedule tags and default durations
- Server: owns the scheduler, starts it after reconciliation

## Sequences
- seq-pubsub.md
- seq-scheduling.md
