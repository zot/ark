# EventScheduler
**Requirements:** R805, R806, R807, R809, R810, R811, R812, R821, R822, R823, R824, R825, R857, R858, R859, R860, R861, R862, R863, R864, R865, R869, R874, R875, R876, R877, R878, R902, R903, R905, R907, R899, R900, R901, R904, R906, R908, R890, R891, R892, R964, R965, R966, R967, R968, R969, R970, R971, R972, R973, R974, R978, R979, R996, R997, R998, R999, R1000, R1001, R1002, R1003, R1004, R1005, R1006, R1007, R1008, R1010, R1011, R1012, R1013, R1014, R1015, R1016, R1017, R1023, R1024, R1025, R1026, R1027, R1035, R1036, R1038, R1039, R1040, R1041, R1043, R2780, R2779, R2783, R2778, R2809

Priority queue of time-tagged events with a single timer. Reads
schedule logs at startup. Delivers events as crank handles through
PubSub's listen channel. In-memory month buckets serve range queries.

## Knows
- queue: heap of ScheduledEvent — sorted by NextFire
- timer: *time.Timer — set to the head of the queue
- pushed: map[string]bool — eventID → delivered this server lifetime
- monthBuckets: map[eventKey][]MonthEntry — skip list for range queries (R1023, R1024)
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
  If recurring: crank forward — convert past upcoming to fired,
  write one new upcoming entry, re-enqueue. (R807, R877, R1010, R1011)
  Reset timer to new head.
- ComputeNext(recurring string, after time.Time, notAfter time.Time) time.Time:
  parse recurrence spec, return next occurrence after the given time.
  Returns zero time if next occurrence exceeds notAfter (zero notAfter = no bound).
  Supports: "every Xm", "every Xh", "every WEEKDAY HH:MM",
  "YYYY-MM-DD HH:MM" (one-shot), "MM-DD" (annual). (R822, R823, R824, R1005)
- stripDateKeyword(s string) (stripped string, keyword string): strip a
  recognized date keyword from the front of a string. Start keywords:
  from, starting, beginning, after, on. End keywords: to, until, through,
  ending, before, by. Returns empty keyword if no match. Only strips when
  remainder parses as a date. (R996, R997, R998, R999)
- ExtractBounds(value string) (notBefore, notAfter time.Time, remainder string):
  extract start/end bounds from a schedule tag value. Looks for keyword+date
  pairs (from/to/starting/until/etc) or DATE..DATE adjacent to "every".
  Returns zero times for missing bounds. Remainder is the pure recurrence
  spec. (R1000, R1001, R1002, R1003, R1004)
- ParseDateValue(value string, defaultDur string) (start, end time.Time,
  description string, err error): parse a schedule tag value including
  `..` duration operator. Uses itlightning/dateparse with token-trimming
  loop. Calls stripDateKeyword before dateparse. Returns start, end
  (using defaultDur if no `..`), and remaining description text.
  (R857, R858, R859, R860, R861, R865, R999)
- ParseRelativeDuration(anchor time.Time, expr string) time.Time: parse
  anchored relative expressions like "one week later", "3 days later".
  (R862, R863, R864)
- ScanScheduleLogs(): called at startup. Scan ~/.ark/schedule/ for
  log files. Read @ark-event-upcoming: entries, populate the priority
  queue. Respects @ark-event-start:/@ark-event-end: bounds — does not
  create upcoming entries beyond end bound. (R1007, R1008)
  Any @ark-event-upcoming: in the past gets converted to
  @ark-event-fired: and the next occurrence is computed from
  @ark-event-spec: and appended (checking for duplicates first).
  Re-index the log file after mutations. (R874, R875, R876)
  Also scan for unresolved @check-gap: entries within lookback
  window — append to tmp://watchdog/missed-events. (R972, R973, R979)
- ResolveCheckGap(tag, sourcePath, date): called when an @ack:
  covering the fired date is detected via subscription. Removes
  the @check-gap: line from the log chunk, re-indexes. (R969, R970, R971)
- EnsureUpcoming(logPath, event): called when a source file with a
  schedule tag is indexed. Calls extractBounds on the tag value.
  Writes @ark-event-start:/@ark-event-end: tags in the log chunk
  when bounds are present. Ensures @ark-event-upcoming: entries
  through min(endDate, forward window). Create log file and chunk
  if needed. **Live-enqueues** the next occurrence via
  `crankForward(chunk, now, true)` so recurring tags armed
  mid-session fire without waiting for a restart. Add is idempotent
  per-ID (R808, R809); re-running on an already-armed chunk
  replaces rather than duplicates. (R902, R1006, R1007, R2778, R2809)
- ~~AddChime():~~ removed by R2783 (retiring R810). The hardcoded
  15-minute quarter-chime is subsumed by the `@chime-15m:` tag
  declared in `~/.ark/chimes.md`, which routes through the same
  schedule-log path as user-authored schedule tags. No
  special-case code remains.
- EnsureChimesFile(): called once by the server during startup,
  before `ScanScheduleLogs`. Verifies `~/.ark/chimes.md` exists;
  if missing, writes the canonical six entries (`@chime-1m: every 1m`
  through `@chime-60m: every 60m`, one per line). The file is
  owned by ark — if the user deletes it, the next startup
  re-creates it. (R2779, R2780)
- maybeOverrideChimeValue(eventID, value) string: called inside
  `fire()` before delivery. If the tag name (derived from
  `eventID`) starts with `chime-`, replace the source value (the
  recurrence spec like `every 15m`) with the current time in
  RFC 3339 format. Subscribers consuming chimes receive a usable
  "now" tick rather than the source recurrence string. Non-chime
  events keep their source value. (R2778)
- BuildMonthBuckets(): compute month buckets from all schedule log specs.
  One entry per month per event — first occurrence in that month. Called
  on startup after ScanScheduleLogs. (R1023, R1024, R1026)
- QueryRange(start, end time.Time, exceptions []Exception) []Event:
  find month bucket at or before start, crank forward to generate all
  events in range, apply @remove:/@add: exceptions, merge @ack: status.
  Used by schedule search CLI and calendar UI. (R1025, R1039, R1041)
- ComputeGaps(start, end time.Time, acks []AckEntry) []Event:
  compare spec occurrences against ack dates, return unacked past
  events. (R1041, R1043)

## Collaborators
- PubSub: delivers events through listen channels
- Config: knows which tags are schedule tags and default durations
- Server: owns the scheduler, starts it after reconciliation

## Sequences
- seq-pubsub.md
- seq-scheduling.md
