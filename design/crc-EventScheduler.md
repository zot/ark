# EventScheduler
**Requirements:** R805, R806, R807, R809, R810, R811, R812, R821, R822, R823, R824, R825, R857, R858, R859, R860, R861, R862, R863, R864, R865, R869, R874, R875, R876, R877, R878, R902, R903, R905, R907, R899, R900, R901, R904, R906, R908, R890, R891, R892, R964, R965, R966, R967, R968, R969, R970, R971, R972, R973, R974, R978, R979, R996, R997, R998, R999, R1000, R1001, R1002, R1003, R1004, R1005, R1006, R1007, R1008, R1010, R1011, R1012, R1013, R1014, R1015, R1016, R1017, R1023, R1024, R1025, R1026, R1027, R1035, R1036, R1038, R1039, R1040, R1041, R1043, R2780, R2779, R2783, R2778, R2809, R2810, R2812, R2813, R2814, R2815, R2816, R2817, R2818, R2819, R2820, R2821, R2822, R2823, R2824, R2825, R2826, R2827, R2828, R2829

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
- Recurring: string — recurrence spec (empty = one-shot). Every
  enqueue site populates this from the source chunk's `@ark-event-spec:`
  so `fire()` can re-enqueue the next occurrence — without it, recurring
  events fire exactly once after startup and then go silent. (R2812)
- IsScheduledFire: bool — true when firing at scheduled time (vs tag change)

## Does
- Add(event ScheduledEvent): push onto heap, reset timer if new
  head. Idempotent — same ID replaces existing entry. (R805)
- Remove(id string): remove from heap by ID, reset timer.
- fire(): called by timer. Pop head, deliver to all listening
  sessions via PubSub (check push records, skip duplicates).
  Event carries IsScheduledFire=true so receivers distinguish
  scheduled fires from tag-change notifications. (R806, R878)
  After publishing, branch on `Config.Lifecycle(event.Tag)`:
  - `"disk"` → append `@ark-event-fired: NOW` to the disk log
    chunk, append `@check-gap:` if the tag is a lifecycle event,
    trim per `log_cap` if needed (R2823, R2827).
  - `"tmp"` → same as disk but the audit doc is at
    `tmp://schedule/TAG/SOURCE-ENCODED`; write routes through
    `db.UpdateTmpFile`/`AppendTmpFile` per R2281 (R2824, R2827).
  - `"none"` → no audit; fire-and-forget (R2825).
  Chime tag value override (R2778) still applies before publish.
  If recurring (`event.Recurring != ""`): re-enqueue the next
  occurrence via `ComputeNext(event.Recurring, NOW, …)` regardless
  of audit destination. (R807, R877, R1010, R1011, R2812, R2820)
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
  log files. **Audit-log hygiene + startup queue arming.**
  Per chunk:
  - Drop if `Config.IsScheduleTag(chunk.Event)` says no (the
    `[schedule.tag.X]` block was removed). (R2810, R2818)
  - Drop if `Config.MatchesScheduleFilterForTag(chunk.Source,
    chunk.Event)` says no. (R2810, R2818)
  - Drop if `chunk.Source` cannot be opened (file removed,
    unreadable). (R2821)
  - Otherwise keep and arm: call `crankForwardAndEnqueue(c, now,
    true)` so the priority queue holds the next occurrence for
    schedules whose source file is unchanged since the previous
    startup (the indexer's reconcile only re-indexes stale files).
    `Add` is idempotent per-ID so a subsequent `EnsureUpcoming` call
    from an indexer notification replaces rather than duplicates.
    (R2818, R808, R809, R2809)
  If all chunks in a file are dropped, delete the log file. (R2810)
  Also scan for unresolved @check-gap: entries within lookback
  window — append to tmp://watchdog/missed-events. (R972, R973, R979)
- ResolveCheckGap(tag, sourcePath, date): called when an @ack:
  covering the fired date is detected via subscription. Removes
  the @check-gap: line from the log chunk, re-indexes. (R969, R970, R971)
- EnsureUpcoming(tag, value, sourcePath): called when the indexer
  notices a schedule tag in a source file. Sole queue-population
  path for the priority queue. (R2819)
  Flow:
  1. If `Config.IsSuppressed(tag)`: no-op. (R2835)
  2. Determine audit destination from `Config.Lifecycle(tag)`:
     `"disk"` → `~/.ark/schedule/HASH.md`; `"tmp"` →
     `tmp://schedule/TAG/SOURCE-ENCODED`; `"none"` → skip log
     read/write entirely. (R2822, R2823, R2824, R2825)
  3. For audit-bearing categories, read the chunk if it exists.
     Compare `value` to the most-recent spec marker's value. On
     difference, append `@ark-event-spec-changed: NOW — value` to
     the chunk; update bounds if changed. (R2819, R2816)
     For a new chunk, write `@ark-event:`, `@ark-event-source:`,
     `@ark-event-spec-initial: NOW — value`, and bounds. (R2815)
  4. Compute `next = ComputeNext(value, max(fired-max, now),
     bounds)` and enqueue a `ScheduledEvent` with `Recurring =
     value`. Add is idempotent per-ID (R808, R809, R2809), so
     re-arming on each indexer notification replaces rather than
     duplicates. (R2820, R2826)
  Pre-R2809 behavior of materializing a forward window of
  `@ark-event-upcoming:` entries is retired (T117); the chunk no
  longer carries an upcoming tag (R2813).
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
- appendFiredEntry(chunk, ts): append `@ark-event-fired: TIMESTAMP`
  to the chunk's fired list. If `len(chunk.Fired) >= log_cap` for
  the tag, drop the older half of fired entries (spec markers
  preserved) before appending. Per-chunk trim. (R2827, R2828, R2829)
- appendSpecMarker(chunk, kind, ts, spec): append
  `@ark-event-spec-initial: TIMESTAMP — SPEC` (kind=initial, exactly
  once per chunk) or `@ark-event-spec-changed: TIMESTAMP — SPEC`
  (kind=changed, zero or more). Trim never touches spec markers.
  (R2815, R2816, R2827)
- currentSpec(chunk) string: derived helper returning the most-
  recent spec-marker's spec value. There is no stored current-spec
  field on the chunk; this walks the marker list. (R2814, R2817)
- writeAuditChunk(chunk, dest): write the chunk to its lifecycle
  destination — disk file at `~/.ark/schedule/HASH.md`, or tmp::
  document at `tmp://schedule/TAG/SOURCE-ENCODED`. tmp:// writes
  route through `db.UpdateTmpFile` so the centralized write-actor
  + publish invariant holds. (R2823, R2824)

### LogChunk shape

- Source: string — `@ark-event-source:`, the source file path
  (disk path or `tmp://` URI).
- Event: string — `@ark-event:`, the schedule tag name.
- SpecMarkers: []SpecMarker — ordered list of spec-initial /
  spec-changed entries. Index 0 is always the initial marker;
  later entries are changes. Each SpecMarker carries
  `{Kind: "initial"|"changed", Time time.Time, Spec string}`.
  (R2815, R2816, R2817)
- Fired: []string — `@ark-event-fired:` timestamps, oldest first.
  Subject to `log_cap` trim (R2827).
- CheckGaps: []string — `@check-gap:` entries; existing.
- NotBefore / NotAfter: time.Time — bounds, mirrored from source
  spec at write time; rewritten on spec change. Existing fields.
- Removes / Adds: exception sets loaded from the source file at
  query time. Existing fields. Not stored in the log chunk itself.

The chunk no longer carries a `Spec` field or an `Upcoming` slice
(R2813, R2814). Code paths that read the spec call `currentSpec(c)`.

## Collaborators
- PubSub: delivers events through listen channels
- Config: knows which tags are schedule tags and default durations
- Server: owns the scheduler, starts it after reconciliation

## Sequences
- seq-pubsub.md
- seq-scheduling.md
