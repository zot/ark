# Test Design: Date-parse malformed-input guards
**Source:** crc-EventScheduler.md

Covers the malformed-datetime guards in `parseDateTrimmingRaw` —
dash-form normalization (R2846), date-with-timezone-but-no-time
rejection (R2847), and ambiguous mm/dd rejection (R2848). Implemented
in `scheduler.go`; tests in `scheduler_test.go`
(`TestParseDateValueMalformed`).

## Behavioral assumptions about itlightning/dateparse

These were established empirically (probe against the linked fork,
2026-05-29) and the guards depend on them. If a `dateparse` upgrade
changes any of these, the guards — and these tests — must be
revisited. This is the load-bearing reason the test inputs are shaped
the way they are.

- `dateparse` is **permissive**: for some malformed inputs it returns
  a wrong-but-valid time with **no error** rather than failing.
- `ParseIn("2026-05-28-13:45")` → `2026-05-28 00:00`, no error.
  `ParseFormat` reveals why: layout `2006-01-02-07:00` — the `-13:45`
  is read as a **timezone offset**, not a time-of-day. Hence midnight.
- `ParseStrict` rejects **only** ambiguous mm/dd vs dd/mm
  (`3/1/2014` → "ambiguous mm/dd vs dd/mm" error). It does **not**
  reject trailing content or the tz-misparse above — so it is not a
  substitute for R2846/R2847, only the mechanism for R2848.
- `ParseFormat` returns the detected layout string; in Go's reference
  layout the token `07` appears only in numeric zone offsets, and
  `15`/`03`/`04`/`05`/`PM` only in clock components. This disjointness
  is what `layoutHasZone` / `layoutHasClock` rely on.
- Reachability of the R2847 guard is **input-shape-sensitive**:
  - `2026-05-28+0700` (single token) → `ParseIn` succeeds as
    date+offset-no-clock → reaches the layout guard → rejected.
  - `2026-05-28Z` → `ParseIn` **fails outright** ("cannot parse") and
    never reaches the guard. Still errors, but by a different path —
    so it does *not* exercise R2847.
  - `2026-05-28 -07:00` / `2026-05-28 MST` (space-separated) →
    `ParseIn` fails on the whole token; the trimming loop drops the
    zone into the **description** → midnight + zone-as-text. This is
    the existing trailing-text behavior (R860/R861), not the guard,
    and is the broader strictness problem deferred in
    `.scratch/SCHEDULE-SEARCH-POLISH.md` item 3.

## Test: dash-form normalization
**Purpose:** R2846 — the dash-joined date/time form yields the intended
time-of-day, not dateparse's silent midnight.
**Input:** `ParseDateValue("2026-05-28-13:45", "", UTC)`; also the
trailing-description form `"2026-05-28-13:45 standup"` and the range
form `"2026-05-28-13:45..2026-05-28-14:00"`.
**Expected:** start hour 13, minute 45; description `"standup"` in the
second; start hour 13 / end hour 14 in the range.
**Refs:** crc-EventScheduler.md (normalizeDashDateTime, ParseDateValue)

## Test: date-with-timezone-but-no-time is rejected
**Purpose:** R2847 — a value that parses to a date carrying a timezone
offset but no clock time errors rather than becoming midnight.
**Input:** `ParseDateValue("2026-05-28+0700", "", UTC)`. The `+offset`
single-token form is used deliberately because it *reaches* the layout
guard (a bare `Z` fails earlier in `ParseIn` and would test the wrong
path — see assumptions above).
**Expected:** non-nil error whose message contains "timezone but no
time-of-day" (confirms the layout-guard path, not an incidental
parse failure).
**Refs:** crc-EventScheduler.md (guardParsedDate, layoutHasZone,
layoutHasClock)

## Test: ambiguous mm/dd is rejected
**Purpose:** R2848 — an ambiguous month/day value is rejected rather
than silently guessed.
**Input:** `ParseDateValue("3/1/2014", "", UTC)`.
**Expected:** non-nil error (the `ParseStrict` re-check reports
ambiguity).
**Refs:** crc-EventScheduler.md (guardParsedDate)

## Test: well-formed values still parse (regression)
**Purpose:** the guards do not reject legitimate inputs.
**Input:** `2026-05-28T13:45`, `2026-05-28 13:45` (expect hour 13);
`2026-04-15`, `April 15 2026` (date-only, all-day, no error).
**Expected:** no error; hour matches where checked; date-only forms
parse without tripping the tz-no-time guard (no zone token in layout).
**Refs:** crc-EventScheduler.md (ParseDateValue)

## Test: machine-written markers stay on the direct path (documented, not asserted)
**Purpose:** record that `@ark-event-start:`/`@ark-event-end:` reads
(`dateparse.ParseLocal`, scheduler.go) intentionally bypass these
guards — they parse canonical machine-written timestamps. See approved
gap A68. No test asserts guard behavior on that path.
**Refs:** crc-EventScheduler.md
