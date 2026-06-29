package ark

// CRC: crc-EventScheduler.md | Seq: seq-pubsub.md

import (
	"bufio"
	"bytes"
	"container/heap"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io"
	"log"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/itlightning/dateparse"
)

const scheduleDateFmt = "2006-01-02 15:04"

// isRecurringSpec returns true if the value looks like a recurring schedule spec.
// IsRecurringSpec returns true if the value looks like a recurring schedule spec.
func IsRecurringSpec(value string) bool {
	return strings.Contains(strings.ToLower(value), "every ")
}

// dateStartKeywords are stripped from the front of a date expression before parsing.
// CRC: crc-EventScheduler.md | R996, R999
var dateStartKeywords = []string{"from", "starting", "beginning", "after", "on"}

// dateEndKeywords are stripped from the front of a date expression before parsing.
// CRC: crc-EventScheduler.md | R997, R999
var dateEndKeywords = []string{"to", "until", "through", "ending", "before", "by"}

// allDateKeywords is the combined list, allocated once.
var allDateKeywords = append(append([]string{}, dateStartKeywords...), dateEndKeywords...)

// stripDateKeyword removes a recognized date keyword from the front of s.
// Returns the stripped string and the keyword found (empty if none).
// Only strips when the remainder parses as a date.
// CRC: crc-EventScheduler.md | R996, R997, R998
func stripDateKeyword(s string, loc *time.Location) (string, string) {
	lower := strings.ToLower(strings.TrimSpace(s))
	for _, kw := range allDateKeywords {
		if rest, ok := strings.CutPrefix(lower, kw+" "); ok {
			rest = strings.TrimSpace(rest)
			// Only strip if remainder starts with something dateparse can handle.
			_, _, err := parseDateTrimmingRaw(rest, loc)
			if err == nil {
				// Return from original string to preserve case of remainder.
				return strings.TrimSpace(s[len(kw)+1:]), kw
			}
		}
	}
	return s, ""
}

// extractBounds extracts start/end bounds from a recurring event value.
// Looks for keyword+date pairs or DATE..DATE adjacent to "every".
// Returns zero times for missing bounds, and the pure recurrence spec as remainder.
// CRC: crc-EventScheduler.md | R1000, R1001, R1002, R1003, R1004
// ExtractBounds extracts start/end bounds from a recurring event value.
func ExtractBounds(value string, loc *time.Location) (notBefore, notAfter time.Time, remainder string) {
	value = strings.TrimSpace(value)

	// Find "every" to split the string.
	lower := strings.ToLower(value)
	everyIdx := strings.Index(lower, "every ")
	if everyIdx < 0 {
		return time.Time{}, time.Time{}, value
	}

	before := strings.TrimSpace(value[:everyIdx])
	after := strings.TrimSpace(value[everyIdx:])

	// Try DATE..DATE form on the non-"every" side first, then trailing.
	notBefore, notAfter, before = tryDotDotBounds(before, loc)
	if notBefore.IsZero() && notAfter.IsZero() {
		// DATE..DATE might trail after the recurrence: "every Mon at 9 2026-03-01..2026-05-30"
		// Find the last space-separated token containing ".." in after.
		if dotIdx := strings.LastIndex(after, ".."); dotIdx > 0 {
			// Find the start of the date range by walking back to a space.
			rangeStart := strings.LastIndex(after[:dotIdx], " ")
			if rangeStart >= 0 {
				recurrence := strings.TrimSpace(after[:rangeStart])
				dateRange := strings.TrimSpace(after[rangeStart:])
				nb, na, rem := tryDotDotBounds(dateRange, loc)
				if !nb.IsZero() || !na.IsZero() {
					notBefore, notAfter = nb, na
					after = strings.TrimSpace(recurrence + " " + rem)
				}
			}
		}
	}

	// Try keyword+date on both sides.
	if notBefore.IsZero() {
		notBefore, before = tryKeywordDate(before, dateStartKeywords, loc)
	}
	if notAfter.IsZero() {
		notAfter, before = tryKeywordDate(before, dateEndKeywords, loc)
	}
	if notBefore.IsZero() {
		notBefore, after = tryKeywordDate(after, dateStartKeywords, loc)
	}
	if notAfter.IsZero() {
		notAfter, after = tryKeywordDate(after, dateEndKeywords, loc)
	}

	remainder = strings.TrimSpace(before + " " + after)
	return
}

// tryDotDotBounds looks for DATE..DATE in s and extracts both dates.
func tryDotDotBounds(s string, loc *time.Location) (start, end time.Time, remainder string) {
	dotIdx := strings.Index(s, "..")
	if dotIdx < 0 {
		return time.Time{}, time.Time{}, s
	}
	leftStr := strings.TrimSpace(s[:dotIdx])
	rightStr := strings.TrimSpace(s[dotIdx+2:])

	startT, _, err1 := parseDateTrimmingRaw(leftStr, loc)
	endT, endDesc, err2 := parseDateTrimmingRaw(rightStr, loc)
	if err1 != nil || err2 != nil {
		return time.Time{}, time.Time{}, s
	}
	return startT, endT, strings.TrimSpace(endDesc)
}

// tryKeywordDate looks for any keyword from the list followed by a date in s.
// Returns the parsed date and s with the keyword+date removed.
func tryKeywordDate(s string, keywords []string, loc *time.Location) (time.Time, string) {
	lower := strings.ToLower(strings.TrimSpace(s))
	for _, kw := range keywords {
		// Try keyword at the start of s.
		if rest, ok := strings.CutPrefix(lower, kw+" "); ok {
			rest = strings.TrimSpace(rest)
			t, desc, err := parseDateTrimmingRaw(rest, loc)
			if err == nil {
				return t, strings.TrimSpace(desc)
			}
		}
		// Try keyword in the middle/end of s.
		needle := " " + kw + " "
		if idx := strings.Index(lower, needle); idx >= 0 {
			before := strings.TrimSpace(s[:idx])
			after := strings.TrimSpace(s[idx+len(needle):])
			t, desc, err := parseDateTrimmingRaw(after, loc)
			if err == nil {
				return t, strings.TrimSpace(before + " " + desc)
			}
		}
	}
	return time.Time{}, s
}

// ScheduleEvent is a computed event occurrence for schedule search results.
// R1027
type ScheduleEvent struct {
	Date    string    `json:"date"` // YYYYMMDD
	Tag     string    `json:"tag"`
	Start   time.Time `json:"start"`
	End     time.Time `json:"end"`
	Summary string    `json:"summary,omitempty"`
	AllDay  bool      `json:"allDay,omitempty"`
	Source  string    `json:"source"` // source file path
	Spec    string    `json:"spec"`   // recurrence spec or one-shot value
}

// QueryRange computes all events between start and end by reading schedule
// logs and cranking forward from specs. Optionally filters by tag name.
// If gaps is true, only returns past events without matching @ack: entries.
// R1027, R1041, R1043
func (es *EventScheduler) QueryRange(start, end time.Time, tag string, gaps bool) []ScheduleEvent {
	var events []ScheduleEvent
	if es.scheduleDir == "" {
		return events
	}
	entries, err := os.ReadDir(es.scheduleDir)
	if err != nil {
		return events
	}
	loc := start.Location()
	for _, entry := range entries {
		if entry.IsDir() {
			continue
		}
		logPath := filepath.Join(es.scheduleDir, entry.Name())
		chunks, err := ReadLogFile(logPath)
		if err != nil {
			continue
		}
		for i := range chunks {
			c := &chunks[i]
			if tag != "" && c.Event != tag {
				continue
			}
			// R1038: load exceptions from source file
			if c.Source != "" && len(c.Removes) == 0 && len(c.Adds) == 0 {
				if content, err := os.ReadFile(c.Source); err == nil {
					c.Removes, c.Adds = ParseExceptions(content, c.Event)
				}
			}
			spec := c.CurrentSpec()
			if IsRecurringSpec(spec) {
				// Crank forward through the range
				startAfter := start.Add(-1 * time.Second)
				if !c.NotBefore.IsZero() && c.NotBefore.After(startAfter) {
					startAfter = c.NotBefore.Add(-1 * time.Second)
				}
				notAfter := end
				if !c.NotAfter.IsZero() && c.NotAfter.Before(notAfter) {
					notAfter = c.NotAfter
				}
				next := ComputeNext(spec, startAfter, notAfter)
				for !next.IsZero() && !next.After(end) {
					// R1039: skip @remove: exceptions
					if removed, _ := isRemoved(next, c.Removes); !removed {
						ev := ScheduleEvent{
							Date:   next.Format("20060102"),
							Tag:    c.Event,
							Start:  next,
							End:    next,
							Source: c.Source,
							Spec:   spec,
						}
						applyDefaultDuration(&ev, es.config.DefaultDuration(c.Event), loc)
						events = append(events, ev)
					}
					next = ComputeNext(spec, next, notAfter)
				}
				// R1036: add @add: exceptions that fall in range
				for _, add := range c.Adds {
					if !add.Date.Before(start) && !add.Date.After(end) {
						ev := ScheduleEvent{
							Date:    add.Date.Format("20060102"),
							Tag:     c.Event,
							Start:   add.Date,
							End:     add.Date,
							Summary: add.Text,
							Source:  c.Source,
							Spec:    spec,
						}
						applyDefaultDuration(&ev, es.config.DefaultDuration(c.Event), loc)
						events = append(events, ev)
					}
				}
			} else {
				// One-shot: parse the spec as a date
				dr, err := ParseDateValue(spec, "", loc)
				if err != nil {
					continue
				}
				if def := es.config.DefaultDuration(c.Event); def != "" && dr.End == dr.Start {
					if def == "all-day" {
						dr.AllDay = true
						dr.End = time.Date(dr.Start.Year(), dr.Start.Month(), dr.Start.Day(), 23, 59, 59, 0, loc)
					} else if d, derr := time.ParseDuration(def); derr == nil {
						dr.End = dr.Start.Add(d)
					}
				}
				if !dr.Start.Before(start) && !dr.Start.After(end) {
					events = append(events, ScheduleEvent{
						Date:    dr.Start.Format("20060102"),
						Tag:     c.Event,
						Start:   dr.Start,
						End:     dr.End,
						Summary: dr.Description,
						AllDay:  dr.AllDay,
						Source:  c.Source,
						Spec:    spec,
					})
				}
			}
		}
	}
	// R1041, R1043: gap detection — filter to unacked past events
	if gaps {
		now := time.Now()
		var gapped []ScheduleEvent
		// Group acks by source file
		ackCache := make(map[string][]AckEntry)
		for _, ev := range events {
			if ev.Start.After(now) {
				continue // future event, skip
			}
			acks, ok := ackCache[ev.Source+":"+ev.Tag]
			if !ok {
				if content, err := os.ReadFile(ev.Source); err == nil {
					acks = ParseAcks(content, ev.Tag)
				}
				ackCache[ev.Source+":"+ev.Tag] = acks
			}
			if acked, _ := AckCoversDate(acks, ev.Start); !acked {
				gapped = append(gapped, ev)
			}
		}
		return gapped
	}
	return events
}

// applyDefaultDuration sets the end time based on the tag's default duration.
func applyDefaultDuration(ev *ScheduleEvent, def string, loc *time.Location) {
	if def == "all-day" {
		ev.AllDay = true
		ev.End = time.Date(ev.Start.Year(), ev.Start.Month(), ev.Start.Day(), 23, 59, 59, 0, loc)
	} else if d, err := time.ParseDuration(def); err == nil {
		ev.End = ev.Start.Add(d)
	}
}

// ParseExceptions extracts @remove: and @add: tags from the same chunk as
// a schedule tag in a source file. R1035, R1036, R1037, R1038
func ParseExceptions(content []byte, tag string) (removes, adds []DateException) {
	lines := strings.Split(string(content), "\n")
	inChunk := false
	loc := time.Now().Location()
	for _, line := range lines {
		trimmed := strings.TrimSpace(line)
		if trimmed == "" || trimmed == "---" {
			inChunk = false
			continue
		}
		if strings.Contains(trimmed, "@"+tag+":") {
			inChunk = true
		}
		if !inChunk {
			continue
		}
		if strings.HasPrefix(trimmed, "@remove:") {
			value := strings.TrimSpace(trimmed[len("@remove:"):])
			if de := parseDateException(value, loc); !de.Date.IsZero() {
				removes = append(removes, de)
			}
		} else if strings.HasPrefix(trimmed, "@add:") {
			value := strings.TrimSpace(trimmed[len("@add:"):])
			if de := parseDateException(value, loc); !de.Date.IsZero() {
				adds = append(adds, de)
			}
		}
	}
	return
}

// parseDateException parses "DATE [text]" into a DateException.
func parseDateException(value string, loc *time.Location) DateException {
	t, desc, err := parseDateTrimmingRaw(value, loc)
	if err != nil {
		return DateException{}
	}
	return DateException{Date: t, Text: desc}
}

// dayNames maps day name strings to time.Weekday (package-level, allocated once).
var dayNames = map[string]time.Weekday{
	"sunday": time.Sunday, "sun": time.Sunday,
	"monday": time.Monday, "mon": time.Monday,
	"tuesday": time.Tuesday, "tue": time.Tuesday, "tues": time.Tuesday,
	"wednesday": time.Wednesday, "wed": time.Wednesday,
	"thursday": time.Thursday, "thu": time.Thursday, "thurs": time.Thursday,
	"friday": time.Friday, "fri": time.Friday,
	"saturday": time.Saturday, "sat": time.Saturday,
}

// ScheduledEvent is an entry in the event queue.
type ScheduledEvent struct {
	ID        string // derived from source path + tag + session
	Tag       string
	Value     string
	Path      string
	NextFire  time.Time
	Recurring string // recurrence spec (empty = one-shot)
	SessionID string // which session this fires for (empty = all, e.g. chimes)
	index     int    // heap index
}

// eventHeap implements heap.Interface for ScheduledEvent.
type eventHeap []*ScheduledEvent

func (h eventHeap) Len() int           { return len(h) }
func (h eventHeap) Less(i, j int) bool { return h[i].NextFire.Before(h[j].NextFire) }
func (h eventHeap) Swap(i, j int)      { h[i], h[j] = h[j], h[i]; h[i].index = i; h[j].index = j }
func (h *eventHeap) Push(x any) {
	e := x.(*ScheduledEvent)
	e.index = len(*h)
	*h = append(*h, e)
}
func (h *eventHeap) Pop() any {
	old := *h
	n := len(old)
	e := old[n-1]
	old[n-1] = nil
	e.index = -1
	*h = old[:n-1]
	return e
}

// ErrorReporter appends diagnostic errors to tmp:// files for visibility.
// Subsystems call Report to surface non-fatal errors that would otherwise
// be lost in log output. Each error becomes a tagged, searchable chunk.
type ErrorReporter interface {
	Report(subsystem, message string)
}

// EventScheduler manages time-based event delivery. R805
// Reads schedule log files from ~/.ark/schedule/ at startup.
// CRC: crc-EventScheduler.md | Seq: seq-scheduling.md | R806, R807
type EventScheduler struct {
	queue       eventHeap
	timer       *time.Timer
	pubsub      *PubSub
	pushed      map[string]bool // eventID → delivered this server lifetime
	mu          sync.Mutex
	stopCh      chan struct{}
	reporter    ErrorReporter                           // nil = log only
	scheduleDir string                                  // ~/.ark/schedule/
	config      *Config                                 // schedule tag declarations
	WriteTmpLog func(path string, content []byte) error // set by caller for tmp:// log writes
	ReadTmpLog  func(path string) ([]byte, error)       // set by caller for tmp:// log reads
}

// NewEventScheduler creates a scheduler that delivers through the given PubSub.
// scheduleDir is the path to ~/.ark/schedule/ (created if needed).
// CRC: crc-EventScheduler.md
func NewEventScheduler(pubsub *PubSub, reporter ErrorReporter, scheduleDir string, config *Config) *EventScheduler {
	return &EventScheduler{
		pubsub:      pubsub,
		pushed:      make(map[string]bool),
		stopCh:      make(chan struct{}),
		reporter:    reporter,
		scheduleDir: scheduleDir,
		config:      config,
	}
}

// reportError sends a diagnostic to the error reporter if available.
func (es *EventScheduler) reportError(subsystem, message string) {
	if es.reporter != nil {
		es.reporter.Report(subsystem, message)
	}
}

// --- Schedule log file operations ---

// LogChunk represents one event definition in a schedule log file.
// CRC: crc-EventScheduler.md | R899, R900, R901, R2813, R2814, R2815, R2816, R2817
//
// The chunk is pure audit: spec history (one initial marker + zero or
// more changed markers) plus fire entries. The active spec lives in
// the source file at `@ark-event-source:`, not here. The chunk no
// longer carries a current-state `@ark-event-spec:` (R2814) or any
// `@ark-event-upcoming:` (R2813); the in-memory priority queue is
// the authoritative "what's next" source.
type LogChunk struct {
	Event       string          // tag name (e.g., "standup")
	Source      string          // source file path (disk path or tmp:// URI)
	SpecMarkers []SpecMarker    // ordered spec history: index 0 is the initial marker, the rest are changes (R2815, R2816)
	NotBefore   time.Time       // R1007: @ark-event-start: bound (zero = no bound)
	NotAfter    time.Time       // R1007: @ark-event-end: bound (zero = no bound)
	Fired       []string        // @ark-event-fired: timestamp strings, oldest first; subject to LogCap trim (R2827)
	CheckGaps   []string        // @check-gap: date strings — unresolved fired events (R965, R969)
	Removes     []DateException // R1035: @remove: exceptions from source file
	Adds        []DateException // R1036: @add: exceptions from source file
}

// SpecMarker is one entry in a chunk's spec history. The active spec
// is read from the source file; the markers preserve what the spec
// was at each historical change.
// CRC: crc-EventScheduler.md | R2815, R2816, R2817
type SpecMarker struct {
	Kind string    // "initial" (R2815, exactly one per chunk) or "changed" (R2816, zero or more)
	Time time.Time // when the marker was recorded
	Spec string    // verbatim spec value at the time of the marker
}

// CurrentSpec returns the spec value from the most-recent spec marker
// in the chunk. Empty when the chunk has no markers (e.g. a legacy
// chunk read before the first EnsureUpcoming migration write).
// CRC: crc-EventScheduler.md | R2817
func (c *LogChunk) CurrentSpec() string {
	if len(c.SpecMarkers) == 0 {
		return ""
	}
	return c.SpecMarkers[len(c.SpecMarkers)-1].Spec
}

// specMarkerSep separates the timestamp from the spec value in the
// `@ark-event-spec-initial:` / `@ark-event-spec-changed:` tag value.
// Unicode em-dash with surrounding spaces; the existing recurrence
// parser never sees this separator because the log reader splits on
// it before passing the trailing text anywhere.
// CRC: crc-EventScheduler.md | R2815
const specMarkerSep = " — " // " — "

// DateException is a parsed @remove: or @add: tag from a source file.
// R1035, R1036, R1037
type DateException struct {
	Date time.Time
	Text string // optional descriptive text after the date
}

// isRemoved returns true if the given time matches any @remove: exception (same day).
func isRemoved(t time.Time, removes []DateException) (bool, string) {
	for _, r := range removes {
		if t.Year() == r.Date.Year() && t.YearDay() == r.Date.YearDay() {
			return true, r.Text
		}
	}
	return false, ""
}

// eventID builds a deterministic event identifier.
func eventID(source, tag, dateStr string) string {
	return fmt.Sprintf("%s:%s:%s", source, tag, dateStr)
}

// crankForwardAndEnqueue computes the next fire from the chunk's
// current spec and bounds, skips @remove: exceptions, and (when
// enqueue is true) adds the resulting ScheduledEvent to the priority
// queue with Recurring populated per R2812.
//
// The log chunk no longer carries `@ark-event-upcoming:` (R2813) — the
// queue is the sole "what's next" source. So this helper only enqueues;
// no chunk mutation. Returns true when an event was computed and (if
// enqueue) added; false when the spec is non-recurring, out of bounds,
// or every candidate falls on a @remove: date.
//
// CRC: crc-EventScheduler.md | Seq: seq-spec-change.md#4 | R2820
func (es *EventScheduler) crankForwardAndEnqueue(c *LogChunk, now time.Time, enqueue bool) bool {
	spec := c.CurrentSpec()
	if !IsRecurringSpec(spec) {
		return false
	}
	// R1003: start from notBefore if it's after now
	startAfter := now
	if !c.NotBefore.IsZero() && c.NotBefore.After(now) {
		startAfter = c.NotBefore.Add(-1 * time.Second) // ComputeNext returns strictly after
	}

	next := ComputeNext(spec, startAfter, c.NotAfter)
	// R1039: skip @remove: exceptions
	for !next.IsZero() {
		if removed, _ := isRemoved(next, c.Removes); !removed {
			break
		}
		next = ComputeNext(spec, next, c.NotAfter)
	}
	if next.IsZero() {
		return false
	}
	if enqueue {
		dateStr := next.Format(scheduleDateFmt)
		// R2812, R2820: propagate Recurring so fire() can re-enqueue.
		es.Add(&ScheduledEvent{
			ID:        eventID(c.Source, c.Event, dateStr),
			Tag:       c.Event,
			Value:     dateStr,
			Path:      c.Source,
			NextFire:  next,
			Recurring: spec,
		})
	}
	return true
}

// logFileHash returns a stable hash string for a source path.
func logFileHash(sourcePath string) string {
	h := sha256.Sum256([]byte(sourcePath))
	return hex.EncodeToString(h[:8])
}

// logFilePath returns the schedule log path for a source file.
func (es *EventScheduler) logFilePath(sourcePath string) string {
	return filepath.Join(es.scheduleDir, logFileHash(sourcePath)+".md")
}

// ensureDir creates the schedule directory if needed.
func (es *EventScheduler) ensureDir() error {
	return os.MkdirAll(es.scheduleDir, 0755)
}

// ReadLogFile parses chunks from a schedule log file. Recognizes the
// current schema (R2815, R2816: spec-initial / spec-changed markers,
// no @ark-event-spec, no @ark-event-upcoming) plus a legacy-shape
// fallback (R2813/R2814 retire @ark-event-spec and @ark-event-upcoming,
// but pre-migration files still carry them — they get folded into a
// synthetic initial marker so they survive the next write).
// ScheduleTagSummary returns the fully-formatted output lines for
// `ark schedule tags`. The CLI's local arm and POST /schedule/tags both
// call it so their output cannot drift. showValues appends the per-event
// last-fire detail read from the schedule log dir. R3000
func (db *DB) ScheduleTagSummary(showValues bool) []string {
	cfg := db.Config()
	tags := cfg.ScheduleTags()
	if len(tags) == 0 {
		return []string{
			"no schedule tags configured",
			"add [schedule.tag.NAME] blocks to ark.toml",
		}
	}
	var lines []string
	names := make([]string, 0, len(tags))
	for k := range tags {
		names = append(names, k)
	}
	sort.Strings(names)
	for _, t := range names {
		tc := tags[t]
		line := "@" + t + ":"
		if def := tc.DefaultDuration; def != "" {
			line += " (default " + def + ")"
		}
		if tc.Suppress {
			line += " [suppressed]"
		}
		switch cfg.Lifecycle(t) {
		case LifecycleTmp:
			line += " [lifecycle=tmp]"
		case LifecycleNone:
			line += " [lifecycle=none]"
		}
		if len(tc.FilterFiles) > 0 {
			line += " filter=" + strings.Join(tc.FilterFiles, ",")
		}
		if len(tc.ExcludeFiles) > 0 {
			line += " exclude=" + strings.Join(tc.ExcludeFiles, ",")
		}
		lines = append(lines, line)
	}
	if len(cfg.Schedule.ExcludeFiles) > 0 {
		lines = append(lines, "", "exclude: "+strings.Join(cfg.Schedule.ExcludeFiles, ", "))
	}
	if len(cfg.Schedule.FilterFiles) > 0 {
		lines = append(lines, "filter: "+strings.Join(cfg.Schedule.FilterFiles, ", "))
	}
	if showValues {
		entries, err := os.ReadDir(filepath.Join(db.dbPath, "schedule"))
		if err != nil {
			return lines
		}
		lines = append(lines, "")
		for _, entry := range entries {
			if entry.IsDir() {
				continue
			}
			chunks, err := ReadLogFile(filepath.Join(db.dbPath, "schedule", entry.Name()))
			if err != nil {
				continue
			}
			for _, ch := range chunks {
				lastFire := "(no fires)"
				if n := len(ch.Fired); n > 0 {
					lastFire = ch.Fired[n-1]
				}
				lines = append(lines, fmt.Sprintf("@%s: %s\n  source: %s\n  last fire: %s",
					ch.Event, ch.CurrentSpec(), ch.Source, lastFire))
			}
		}
	}
	return lines
}

// CRC: crc-EventScheduler.md | R2813, R2814, R2815, R2816, R2817
func ReadLogFile(path string) ([]LogChunk, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	return readLogChunks(f)
}

// readLogChunks parses chunks from any io.Reader. ReadLogFile and
// parseLogChunks (used for tmp:// in-memory reads) both delegate here
// so the scanner logic is single-sourced.
// CRC: crc-EventScheduler.md | R2813, R2814, R2815, R2816, R2817
func readLogChunks(r io.Reader) ([]LogChunk, error) {
	var chunks []LogChunk
	var cur *LogChunk
	var legacySpec string // legacy @ark-event-spec: value for the current chunk
	flush := func() {
		if cur == nil {
			return
		}
		// Migrate legacy chunk shape: synthesize an initial marker from
		// the legacy @ark-event-spec: value if no markers were read.
		if len(cur.SpecMarkers) == 0 && legacySpec != "" {
			cur.SpecMarkers = []SpecMarker{{Kind: "initial", Time: time.Time{}, Spec: legacySpec}}
		}
		chunks = append(chunks, *cur)
		cur = nil
		legacySpec = ""
	}
	scanner := bufio.NewScanner(r)
	for scanner.Scan() {
		line := scanner.Text()
		if strings.HasPrefix(line, "@ark-event:") {
			flush()
			cur = &LogChunk{Event: strings.TrimSpace(line[len("@ark-event:"):])}
			continue
		}
		if cur == nil {
			continue
		}
		switch {
		case strings.HasPrefix(line, "@ark-event-source:"):
			cur.Source = strings.TrimSpace(line[len("@ark-event-source:"):])
		case strings.HasPrefix(line, "@ark-event-spec-initial:"):
			if m, ok := parseSpecMarker("initial", line[len("@ark-event-spec-initial:"):]); ok {
				cur.SpecMarkers = append(cur.SpecMarkers, m)
			}
		case strings.HasPrefix(line, "@ark-event-spec-changed:"):
			if m, ok := parseSpecMarker("changed", line[len("@ark-event-spec-changed:"):]); ok {
				cur.SpecMarkers = append(cur.SpecMarkers, m)
			}
		case strings.HasPrefix(line, "@ark-event-spec:"):
			// Legacy shape (R2814 retired @ark-event-spec). Stash for
			// migration in flush().
			legacySpec = strings.TrimSpace(line[len("@ark-event-spec:"):])
		case strings.HasPrefix(line, "@ark-event-start:"):
			if t, err := dateparse.ParseLocal(strings.TrimSpace(line[len("@ark-event-start:"):])); err == nil {
				cur.NotBefore = t
			}
		case strings.HasPrefix(line, "@ark-event-end:"):
			if t, err := dateparse.ParseLocal(strings.TrimSpace(line[len("@ark-event-end:"):])); err == nil {
				cur.NotAfter = t
			}
		case strings.HasPrefix(line, "@ark-event-fired:"):
			cur.Fired = append(cur.Fired, strings.TrimSpace(line[len("@ark-event-fired:"):]))
		case strings.HasPrefix(line, "@ark-event-upcoming:"):
			// Legacy shape (R2813 retired @ark-event-upcoming). Silently
			// drop; the queue is recomputed from spec + now.
		case strings.HasPrefix(line, "@check-gap:"):
			cur.CheckGaps = append(cur.CheckGaps, strings.TrimSpace(line[len("@check-gap:"):]))
		}
	}
	flush()
	return chunks, scanner.Err()
}

// parseSpecMarker parses the `TIMESTAMP — SPECVALUE` body of a
// spec-initial / spec-changed tag. Returns ok=false if the format
// doesn't match — caller drops the malformed line silently.
// CRC: crc-EventScheduler.md | R2815, R2816
func parseSpecMarker(kind, body string) (SpecMarker, bool) {
	body = strings.TrimSpace(body)
	idx := strings.Index(body, specMarkerSep)
	if idx < 0 {
		return SpecMarker{}, false
	}
	tsStr := strings.TrimSpace(body[:idx])
	spec := strings.TrimSpace(body[idx+len(specMarkerSep):])
	t, err := time.ParseInLocation(scheduleDateFmt, tsStr, time.Local)
	if err != nil {
		return SpecMarker{}, false
	}
	return SpecMarker{Kind: kind, Time: t, Spec: spec}, true
}

// formatLogChunks renders log chunks as markdown bytes.
// CRC: crc-EventScheduler.md | R2813, R2814, R2815, R2816
func formatLogChunks(chunks []LogChunk) []byte {
	var buf strings.Builder
	for i, c := range chunks {
		if i > 0 {
			buf.WriteString("\n---\n\n")
		}
		fmt.Fprintf(&buf, "@ark-event: %s\n", c.Event)
		fmt.Fprintf(&buf, "@ark-event-source: %s\n", c.Source)
		if !c.NotBefore.IsZero() {
			fmt.Fprintf(&buf, "@ark-event-start: %s\n", c.NotBefore.Format("2006-01-02"))
		}
		if !c.NotAfter.IsZero() {
			fmt.Fprintf(&buf, "@ark-event-end: %s\n", c.NotAfter.Format("2006-01-02"))
		}
		buf.WriteString("\n")
		for _, m := range c.SpecMarkers {
			tag := "@ark-event-spec-initial:"
			if m.Kind == "changed" {
				tag = "@ark-event-spec-changed:"
			}
			tsStr := m.Time.Format(scheduleDateFmt)
			if m.Time.IsZero() {
				tsStr = "0000-00-00 00:00" // migrated legacy chunk, original time unknown
			}
			fmt.Fprintf(&buf, "%s %s%s%s\n", tag, tsStr, specMarkerSep, m.Spec)
		}
		for _, f := range c.Fired {
			fmt.Fprintf(&buf, "@ark-event-fired: %s\n", f)
		}
		for _, g := range c.CheckGaps {
			fmt.Fprintf(&buf, "@check-gap: %s\n", g)
		}
	}
	return []byte(buf.String())
}

// WriteLogFile writes chunks to a schedule log file. Caller must
// ensure the directory exists (via ensureDir).
func WriteLogFile(path string, chunks []LogChunk) error {
	return os.WriteFile(path, formatLogChunks(chunks), 0644)
}

// EnsureUpcoming is the sole queue-population path. The indexer calls
// this when a source file with a schedule tag is indexed. Behavior
// dispatches on the tag's `lifecycle`:
//
//   - "disk" / "tmp": read the audit chunk, append a spec-change marker
//     if the source's value differs from the chunk's latest recorded
//     spec (or write an initial marker for a new chunk), then arm the
//     queue.
//   - "none": skip log read/write entirely; arm the queue only.
//
// Suppressed tags (R2835) become a no-op — neither audit nor queue.
//
// Source-file duplication for the same tag (e.g. literal
// `@chime-15m: every 15m` text in a code file) is prevented at config
// level via `[schedule].exclude_files`, not in this function.
//
// CRC: crc-EventScheduler.md | Seq: seq-spec-change.md | R2778, R2809, R2812, R2815, R2816, R2817, R2818, R2819, R2820, R2825, R2826, R2835
func (es *EventScheduler) EnsureUpcoming(tag, value, sourcePath string) error {
	if es.config == nil {
		return nil
	}
	// R2835: suppressed tags don't arm.
	if es.config.IsSuppressed(tag) {
		return nil
	}

	now := time.Now()

	// R1000-R1004: extract bounds from recurring values; spec is the
	// pure recurrence string with bounds stripped.
	spec := value
	var notBefore, notAfter time.Time
	if IsRecurringSpec(value) {
		notBefore, notAfter, spec = ExtractBounds(value, now.Location())
	}

	lifecycle := es.config.Lifecycle(tag)

	// Build the chunk in memory — used both for queue arming and
	// (for audit-bearing lifecycles) as the persisted shape.
	chunk := &LogChunk{
		Event:       tag,
		Source:      sourcePath,
		NotBefore:   notBefore,
		NotAfter:    notAfter,
		SpecMarkers: []SpecMarker{{Kind: "initial", Time: now, Spec: spec}},
	}

	if lifecycle == LifecycleNone {
		// R2825: no audit anywhere. Arm queue only.
		es.armChunk(chunk, value, now)
		return nil
	}

	// Audit-bearing path: read existing chunk (if any), detect spec
	// changes, write updates.
	logPath, isTmp := es.auditLogPath(tag, sourcePath, lifecycle)
	chunks, _ := es.readAuditLog(logPath, isTmp)

	var existing *LogChunk
	for i := range chunks {
		if chunks[i].Event == tag && chunks[i].Source == sourcePath {
			existing = &chunks[i]
			break
		}
	}

	// Load source-file exceptions (disk sources only — tmp:// sources
	// don't have @remove/@add semantics at the schedule layer).
	var removes, adds []DateException
	if !isTmp {
		if content, err := os.ReadFile(sourcePath); err == nil {
			removes, adds = ParseExceptions(content, tag)
		}
	}

	modified := false
	if existing == nil {
		chunk.Removes = removes
		chunk.Adds = adds
		chunks = append(chunks, *chunk)
		existing = &chunks[len(chunks)-1]
		modified = true
	} else {
		existing.Removes = removes
		existing.Adds = adds
		existing.NotBefore = notBefore
		existing.NotAfter = notAfter
		if existing.CurrentSpec() != spec {
			// R2816: spec change — append a changed marker. Also covers the
			// legacy-migration case where CurrentSpec() is "" because the
			// chunk had no markers; the appended marker becomes initial.
			kind := "changed"
			if len(existing.SpecMarkers) == 0 {
				kind = "initial"
			}
			existing.SpecMarkers = append(existing.SpecMarkers, SpecMarker{
				Kind: kind,
				Time: now,
				Spec: spec,
			})
			modified = true
		} else if len(existing.SpecMarkers) > 0 && existing.SpecMarkers[0].Time.IsZero() {
			// Legacy chunk with a synthetic zero-time marker — give it a real
			// timestamp now so the migration is permanent.
			existing.SpecMarkers[0].Time = now
			modified = true
		}
	}

	// R2820: arm the queue from the current value, regardless of whether
	// the log was modified.
	es.armChunk(existing, value, now)

	if !modified {
		return nil
	}
	return es.writeAuditLog(logPath, chunks, isTmp)
}

// armChunk computes the next occurrence (recurring) or the one-shot
// fire time and enqueues the resulting ScheduledEvent with Recurring
// populated per R2812. Idempotent per-ID via Add (R808, R809, R2809).
// CRC: crc-EventScheduler.md | R2812, R2820, R2826
func (es *EventScheduler) armChunk(c *LogChunk, value string, now time.Time) {
	spec := c.CurrentSpec()
	if IsRecurringSpec(spec) {
		es.crankForwardAndEnqueue(c, now, true)
		return
	}
	// One-shot: parse the value and enqueue if in the future.
	dr, err := ParseDateValue(value, "", now.Location())
	if err != nil || !dr.Start.After(now) {
		return
	}
	dateStr := dr.Start.Format(scheduleDateFmt)
	es.Add(&ScheduledEvent{
		ID:       eventID(c.Source, c.Event, dateStr),
		Tag:      c.Event,
		Value:    dateStr,
		Path:     c.Source,
		NextFire: dr.Start,
		// Recurring left empty — one-shot events do not re-enqueue.
	})
}

// auditLogPath returns the audit log location for a (tag, source)
// chunk. Disk: ~/.ark/schedule/HASH.md (one file per source, all that
// source's tags). tmp: tmp://schedule/TAG/HASH.md (one doc per
// (tag, source) per R2824).
// CRC: crc-EventScheduler.md | R2823, R2824
func (es *EventScheduler) auditLogPath(tag, sourcePath, lifecycle string) (string, bool) {
	if lifecycle == LifecycleTmp {
		return "tmp://schedule/" + tag + "/" + logFileHash(sourcePath) + ".md", true
	}
	return es.logFilePath(sourcePath), false
}

// readAuditLog reads chunks from a disk log file or tmp:: overlay
// document. Returns an empty slice if the log doesn't exist yet
// (treated as a fresh chunk by the caller).
// CRC: crc-EventScheduler.md | R2823, R2824
func (es *EventScheduler) readAuditLog(logPath string, isTmp bool) ([]LogChunk, error) {
	if isTmp {
		if es.ReadTmpLog == nil {
			return nil, nil
		}
		content, err := es.ReadTmpLog(logPath)
		if err != nil || len(content) == 0 {
			return nil, nil
		}
		return parseLogChunks(content)
	}
	if err := es.ensureDir(); err != nil {
		return nil, err
	}
	chunks, err := ReadLogFile(logPath)
	if os.IsNotExist(err) {
		return nil, nil
	}
	return chunks, err
}

// writeAuditLog serializes chunks and writes them to the destination.
// Disk: WriteLogFile. tmp:: WriteTmpLog (R2281 actor route).
// CRC: crc-EventScheduler.md | R2823, R2824
func (es *EventScheduler) writeAuditLog(logPath string, chunks []LogChunk, isTmp bool) error {
	if isTmp {
		if es.WriteTmpLog == nil {
			return nil
		}
		return es.WriteTmpLog(logPath, formatLogChunks(chunks))
	}
	return WriteLogFile(logPath, chunks)
}

// parseLogChunks parses chunks from in-memory bytes (used for tmp:// reads).
func parseLogChunks(content []byte) ([]LogChunk, error) {
	return readLogChunks(bytes.NewReader(content))
}

// ScanScheduleLogs reads all log files in ~/.ark/schedule/ and populates
// the scheduler queue. Converts past @ark-event-upcoming: to @ark-event-fired:
// and cranks forward for recurring events. Reconciles each chunk
// against the current [schedule] config — chunks whose tag is no
// longer scheduled or whose source no longer passes the schedule
// filter are dropped; log files with no surviving chunks are deleted.
// R812: the push-record map (es.pushed) is in-memory, so a server restart
// clears it; this startup re-scan re-arms the queue from the logs and fires
// anything currently due.
// CRC: crc-EventScheduler.md | R812, R874, R875, R876, R2810, R2818, R2821
func (es *EventScheduler) ScanScheduleLogs() error {
	if es.scheduleDir == "" {
		return nil
	}
	entries, err := os.ReadDir(es.scheduleDir)
	if os.IsNotExist(err) {
		return nil
	}
	if err != nil {
		return err
	}

	for _, entry := range entries {
		if entry.IsDir() || !strings.HasSuffix(entry.Name(), ".md") {
			continue
		}
		logPath := filepath.Join(es.scheduleDir, entry.Name())
		chunks, err := ReadLogFile(logPath)
		if err != nil {
			log.Printf("schedule: error reading %s: %v", logPath, err)
			continue
		}

		kept := make([]LogChunk, 0, len(chunks))
		modified := false
		for i := range chunks {
			c := &chunks[i]

			// R2810: drop chunks whose tag is no longer scheduled or
			// whose source no longer passes the schedule filter.
			if !es.chunkInCurrentConfig(c) {
				log.Printf("schedule: dropping %s chunk for %s (retired by current config)",
					c.Event, c.Source)
				modified = true
				continue
			}

			// R2821: drop chunks whose @ark-event-source: file is gone.
			if c.Source != "" && !strings.HasPrefix(c.Source, "tmp://") {
				if _, err := os.Stat(c.Source); err != nil {
					log.Printf("schedule: dropping %s chunk — source unreadable: %s",
						c.Event, c.Source)
					modified = true
					continue
				}
			}

			// Detect legacy-format migration: ReadLogFile synthesizes an
			// initial marker with zero time when the chunk had a legacy
			// `@ark-event-spec:` line but no spec-* markers. Marking the
			// file modified here forces a rewrite that emits the new
			// schema and drops any leftover `@ark-event-upcoming:` lines.
			if len(c.SpecMarkers) > 0 && c.SpecMarkers[0].Time.IsZero() {
				c.SpecMarkers[0].Time = time.Now()
				modified = true
			}

			// R2818, R2819: queue arming is the indexer's EnsureUpcoming
			// pass — but the indexer only re-indexes stale or changed
			// files. For schedule tags in files that haven't changed
			// since the last index, the indexer wouldn't call
			// EnsureUpcoming and the queue would be empty until the next
			// source-file edit. Arm here from the chunk's recorded spec
			// to cover that case. crankForwardAndEnqueue is idempotent
			// per-ID with Add's R808/R809 dedup, so a later
			// EnsureUpcoming call from the indexer replaces rather than
			// duplicates.
			es.crankForwardAndEnqueue(c, time.Now(), true)

			kept = append(kept, *c)
		}

		if len(kept) == 0 {
			if err := os.Remove(logPath); err != nil {
				log.Printf("schedule: error removing empty log %s: %v", logPath, err)
			} else {
				log.Printf("schedule: removed empty log %s", logPath)
			}
			continue
		}
		if modified {
			if err := WriteLogFile(logPath, kept); err != nil {
				log.Printf("schedule: error writing %s: %v", logPath, err)
			}
		}
	}
	return nil
}

// chunkInCurrentConfig returns true if a schedule log chunk's tag is
// still in [schedule].tags and its source still passes the schedule
// filter for that tag. Used by ScanScheduleLogs to retire entries
// after the user tightens [schedule].tags or [schedule].exclude_files.
// CRC: crc-EventScheduler.md | R2810
func (es *EventScheduler) chunkInCurrentConfig(c *LogChunk) bool {
	if es.config == nil {
		return true
	}
	if !es.config.IsScheduleTag(c.Event) {
		return false
	}
	return es.config.MatchesScheduleFilterForTag(c.Source, c.Event)
}

// SetConfig swaps the scheduler's config pointer. Called from the
// watch loop's config-reload settle path so subsequent IsScheduleTag
// / Lifecycle / IsSuppressed / DefaultDuration lookups see the
// reloaded ark.toml. Without this, the scheduler keeps a stale
// pointer captured at NewEventScheduler time and reload-driven
// re-arming would use the pre-edit config.
// CRC: crc-EventScheduler.md
func (es *EventScheduler) SetConfig(cfg *Config) {
	es.mu.Lock()
	defer es.mu.Unlock()
	es.config = cfg
}

// DropAll empties the priority queue. Used by the watch-loop
// config-reload settle path to force a full re-arm from sources of
// truth (disk chunks + chimes.md) via subsequent ScanScheduleLogs
// + ArmChimesFromFile. Cheaper and more uniform than diff-tracking
// which tags became (un)declared/(un)suppressed.
// CRC: crc-EventScheduler.md
func (es *EventScheduler) DropAll() int {
	es.mu.Lock()
	defer es.mu.Unlock()
	n := len(es.queue)
	es.queue = es.queue[:0]
	if n > 0 {
		es.resetTimer()
	}
	return n
}

// RemoveByTag drops every queue entry whose Tag matches `tag`. Used
// by `ark schedule suppress` to take effect immediately (without
// waiting for a fire) on the priority queue. (R2836)
// CRC: crc-EventScheduler.md | R2836
func (es *EventScheduler) RemoveByTag(tag string) int {
	es.mu.Lock()
	defer es.mu.Unlock()
	kept := make(eventHeap, 0, len(es.queue))
	dropped := 0
	for _, e := range es.queue {
		if e.Tag == tag {
			dropped++
			continue
		}
		kept = append(kept, e)
	}
	es.queue = kept
	heap.Init(&es.queue)
	if dropped > 0 {
		es.resetTimer()
	}
	return dropped
}

// Upcoming returns a snapshot of armed events, optionally filtered to a
// single tag. Sorted by NextFire (oldest first). Used by
// `ark schedule upcoming` (R2838).
// CRC: crc-EventScheduler.md | R2838
func (es *EventScheduler) Upcoming(tag string) []ScheduledEvent {
	es.mu.Lock()
	defer es.mu.Unlock()
	out := make([]ScheduledEvent, 0, len(es.queue))
	for _, e := range es.queue {
		if tag != "" && e.Tag != tag {
			continue
		}
		out = append(out, *e)
	}
	sort.Slice(out, func(i, j int) bool {
		return out[i].NextFire.Before(out[j].NextFire)
	})
	return out
}

// Add pushes an event onto the queue. Idempotent — same ID replaces. R808
// CRC: crc-EventScheduler.md | R809
func (es *EventScheduler) Add(event *ScheduledEvent) {
	es.mu.Lock()
	defer es.mu.Unlock()
	// Remove existing with same ID
	for i, e := range es.queue {
		if e.ID == event.ID {
			heap.Remove(&es.queue, i)
			break
		}
	}
	heap.Push(&es.queue, event)
	es.resetTimer()
}

// isChimeTag returns true when the tag name is one of the standard
// chime cadences (`chime-1m`, `chime-5m`, …, `chime-60m`). Chime tags
// route through the normal schedule path but bypass log mutation in
// fire() and are re-enqueued directly.
// CRC: crc-EventScheduler.md | R2778, R2783
func isChimeTag(tag string) bool {
	return strings.HasPrefix(tag, "chime-")
}

// chimesFilePath is the canonical hosting file for chime recurrence
// specs. Owned by ark — auto-created if missing.
// CRC: crc-EventScheduler.md | R2779, R2780
func chimesFilePath(arkDir string) string {
	return filepath.Join(arkDir, "chimes.md")
}

const chimesFileContent = `# Chimes — standard scheduling tags

This file is auto-created and maintained by ark. It declares the
recurrence specs for the six standard chime cadences. Subscribers
attach with plain ` + "`ark subscribe --tag chime-Nm`" + `.

@chime-1m: every 1m
@chime-5m: every 5m
@chime-15m: every 15m
@chime-30m: every 30m
@chime-45m: every 45m
@chime-60m: every 60m
`

// EnsureChimesFile writes ~/.ark/chimes.md if missing, with the
// canonical six chime entries. Called from server startup before
// ScanChimes so the file exists when we parse it.
// CRC: crc-EventScheduler.md | R2779, R2780
func EnsureChimesFile(arkDir string) error {
	if arkDir == "" {
		return nil
	}
	path := chimesFilePath(arkDir)
	_, err := os.Stat(path)
	if err == nil {
		return nil
	}
	if !os.IsNotExist(err) {
		return err
	}
	return os.WriteFile(path, []byte(chimesFileContent), 0644)
}

// ArmChimesFromFile reads ~/.ark/chimes.md at startup, finds each
// @chime-Nm: tag, and calls EnsureUpcoming for it. This covers the
// case where chime tags have lifecycle="none" (the default per
// R2834) — there's no on-disk audit chunk for ScanScheduleLogs to
// arm from, and the indexer skips re-processing chimes.md when its
// mtime hasn't changed since the last indexing pass. Without this,
// chimes only fire after the next chimes.md edit triggers an
// indexer notification. The startup arming is idempotent — a later
// indexer pass replaces the queue entry rather than duplicating.
// CRC: crc-EventScheduler.md | R2779, R2780, R2825, R2834
func (es *EventScheduler) ArmChimesFromFile(arkDir string) error {
	path := chimesFilePath(arkDir)
	f, err := os.Open(path)
	if err != nil {
		return err
	}
	defer f.Close()
	scanner := bufio.NewScanner(f)
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		// Match `@chime-Nm: VALUE` lines specifically — the file is
		// chime-only, so any `@TAG: VALUE` here is fair game.
		if !strings.HasPrefix(line, "@") {
			continue
		}
		colon := strings.Index(line, ":")
		if colon < 0 {
			continue
		}
		tag := strings.TrimSpace(line[1:colon])
		value := strings.TrimSpace(line[colon+1:])
		if tag == "" || value == "" {
			continue
		}
		if err := es.EnsureUpcoming(tag, value, path); err != nil {
			log.Printf("schedule: ArmChimesFromFile EnsureUpcoming(@%s) error: %v", tag, err)
		}
	}
	return scanner.Err()
}

// Stop shuts down the scheduler.
func (es *EventScheduler) Stop() {
	close(es.stopCh)
	es.mu.Lock()
	if es.timer != nil {
		es.timer.Stop()
	}
	es.mu.Unlock()
}

// resetTimer sets the timer to the head of the queue. Must hold mu.
func (es *EventScheduler) resetTimer() {
	if es.timer != nil {
		es.timer.Stop()
	}
	if es.queue.Len() == 0 {
		es.timer = nil
		return
	}
	head := es.queue[0]
	delay := max(time.Until(head.NextFire), 0)
	es.timer = time.AfterFunc(delay, es.fire)
}

// fire delivers the head event and reschedules if recurring.
// CRC: crc-EventScheduler.md | Seq: seq-scheduling.md | seq-chimes.md | R2778, R806, R807, R877
func (es *EventScheduler) fire() {
	es.mu.Lock()
	if es.queue.Len() == 0 {
		es.mu.Unlock()
		return
	}
	event := heap.Pop(&es.queue).(*ScheduledEvent)

	// R2778: chime tags carry an RFC 3339 timestamp at fire time, not
	// the source recurrence spec. Subscribers receive a usable "now"
	// tick.
	isChime := isChimeTag(event.Tag)
	if isChime {
		event.Value = time.Now().UTC().Format(time.RFC3339)
	}

	// Check push record.
	// CRC: crc-EventScheduler.md | R811
	if !es.pushed[event.ID] {
		es.pushed[event.ID] = true
		tags := []TagValue{{Tag: event.Tag, Value: event.Value}}
		es.mu.Unlock()
		es.pubsub.Publish("", event.Path, tags)
		es.mu.Lock()
	}

	delete(es.pushed, event.ID)
	es.mu.Unlock()

	// Audit dispatch — pure lifecycle, no chime special-case. Chimes
	// now follow the per-tag lifecycle (defaults to "disk" per D1),
	// with log_cap trim bounding the fired-entry history. The R2778
	// value override above stays — chime subscribers still receive an
	// RFC 3339 "now" tick rather than the source recurrence string.
	switch {
	case event.Path != "" && es.config != nil && es.config.Lifecycle(event.Tag) != LifecycleNone:
		// Audit-bearing lifecycle ("disk"/"tmp"): append fired entry
		// to the audit chunk and re-enqueue. (R2823, R2824, R2826, R2827)
		es.fireLogMutate(event)
	case event.Recurring != "":
		// lifecycle=none: no audit, but recurring events still need to
		// re-enqueue so the next occurrence fires. (R2825)
		next := ComputeNext(event.Recurring, time.Now(), time.Time{})
		if !next.IsZero() {
			es.Add(&ScheduledEvent{
				ID:        event.ID,
				Tag:       event.Tag,
				Value:     next.Format(scheduleDateFmt),
				Path:      event.Path,
				NextFire:  next,
				Recurring: event.Recurring,
			})
		}
	}

	es.mu.Lock()
	es.resetTimer()
	es.mu.Unlock()
}

// fireLogMutate appends a fired entry to the audit chunk for the
// given event, applies log_cap trim, and re-enqueues the next
// occurrence (recurring tags only). Lifecycle dispatch chooses the
// audit destination (disk vs tmp:// — never called for "none").
// CRC: crc-EventScheduler.md | Seq: seq-tmp-audit-trim.md | R2823, R2824, R2827, R2828, R2829
func (es *EventScheduler) fireLogMutate(event *ScheduledEvent) {
	if es.config == nil {
		return
	}
	lifecycle := es.config.Lifecycle(event.Tag)
	if lifecycle == LifecycleNone {
		return
	}
	logPath, isTmp := es.auditLogPath(event.Tag, event.Path, lifecycle)
	chunks, err := es.readAuditLog(logPath, isTmp)
	if err != nil {
		log.Printf("schedule: cannot read log for fire: %v", err)
		return
	}

	now := time.Now()
	stamp := now.Format(scheduleDateFmt)

	var c *LogChunk
	for i := range chunks {
		if chunks[i].Event == event.Tag && chunks[i].Source == event.Path {
			c = &chunks[i]
			break
		}
	}
	if c == nil {
		// Chunk doesn't exist yet — EnsureUpcoming should have created
		// one before fire(), but be defensive and synthesize a marker.
		spec := event.Recurring
		if spec == "" {
			spec = event.Value
		}
		chunks = append(chunks, LogChunk{
			Event:       event.Tag,
			Source:      event.Path,
			SpecMarkers: []SpecMarker{{Kind: "initial", Time: now, Spec: spec}},
		})
		c = &chunks[len(chunks)-1]
	}

	// R2827: log_cap trim — drop older half before append if at cap.
	cap := es.config.LogCap(event.Tag)
	if len(c.Fired)+1 > cap {
		drop := min(len(c.Fired)+1-cap/2, len(c.Fired))
		c.Fired = c.Fired[drop:]
	}
	c.Fired = append(c.Fired, stamp)

	// R965: append @check-gap: only for tags with a default_duration —
	// the duration is the "this is an event, not a tick" signal.
	// Heartbeat tags (chimes — no default_duration) have no human-ack
	// loop, so check-gap entries for them would accumulate unboundedly
	// and pollute tmp://watchdog/missed-events on every restart's
	// ScanCheckGaps pass.
	if es.config.DefaultDuration(event.Tag) != "" && !slices.Contains(c.CheckGaps, stamp) {
		c.CheckGaps = append(c.CheckGaps, stamp)
	}

	if err := es.writeAuditLog(logPath, chunks, isTmp); err != nil {
		log.Printf("schedule: cannot write log for fire: %v", err)
	}

	// Re-enqueue the next occurrence for recurring events. (R2820)
	if event.Recurring != "" {
		es.crankForwardAndEnqueue(c, now, true)
	}
}

// ResolveCheckGap removes a @check-gap: entry for a tag+source when an
// @ack: covering that date is detected. Called from the ack subscription path.
// CRC: crc-EventScheduler.md | R969, R970, R971
func (es *EventScheduler) ResolveCheckGap(tag, sourcePath, date string) {
	logPath := es.logFilePath(sourcePath)
	chunks, err := ReadLogFile(logPath)
	if err != nil {
		return // log file might not exist (non-lifecycle tag)
	}
	modified := false
	for i := range chunks {
		c := &chunks[i]
		if c.Event != tag || c.Source != sourcePath {
			continue
		}
		var kept []string
		for _, g := range c.CheckGaps {
			if g != date {
				kept = append(kept, g)
			} else {
				modified = true
			}
		}
		c.CheckGaps = kept
		break
	}
	if modified {
		if err := WriteLogFile(logPath, chunks); err != nil {
			log.Printf("schedule: cannot write log for check-gap resolve: %v", err)
		}
	}
}

// ResolveCheckGapsFromAcks removes check-gaps covered by any ack entry.
// Iterates all schedule log chunks for the source path and removes check-gaps
// whose date falls within any ack's date range.
// CRC: crc-EventScheduler.md | R970, R971
func (es *EventScheduler) ResolveCheckGapsFromAcks(sourcePath string, acks []AckEntry) {
	logPath := es.logFilePath(sourcePath)
	chunks, err := ReadLogFile(logPath)
	if err != nil {
		return
	}
	modified := false
	for i := range chunks {
		c := &chunks[i]
		if c.Source != sourcePath {
			continue
		}
		var kept []string
		for _, g := range c.CheckGaps {
			t := parseScheduledTime(g, time.Now())
			if t.IsZero() {
				kept = append(kept, g)
				continue
			}
			covered, _ := AckCoversDate(acks, t)
			if covered {
				modified = true
			} else {
				kept = append(kept, g)
			}
		}
		c.CheckGaps = kept
	}
	if modified {
		if err := WriteLogFile(logPath, chunks); err != nil {
			log.Printf("schedule: cannot write log for ack resolve: %v", err)
		}
	}
}

// ScanCheckGaps scans all schedule logs for unresolved @check-gap: entries
// within the lookback window and appends them to tmp://watchdog/missed-events.
// Called at startup. CRC: crc-EventScheduler.md | R972, R973
func (es *EventScheduler) ScanCheckGaps(lookbackDays int) []string {
	if es.scheduleDir == "" {
		return nil
	}
	cutoff := time.Now().AddDate(0, 0, -lookbackDays)
	entries, err := os.ReadDir(es.scheduleDir)
	if err != nil {
		return nil
	}
	var missed []string
	for _, entry := range entries {
		if entry.IsDir() || !strings.HasSuffix(entry.Name(), ".md") {
			continue
		}
		chunks, err := ReadLogFile(filepath.Join(es.scheduleDir, entry.Name()))
		if err != nil {
			continue
		}
		for _, c := range chunks {
			for _, g := range c.CheckGaps {
				t := parseScheduledTime(g, time.Now())
				if t.IsZero() {
					continue
				}
				if t.After(cutoff) {
					missed = append(missed, fmt.Sprintf("@watchdog: missed @%s: %s in %s\n", c.Event, g, c.Source))
				}
			}
		}
	}
	return missed
}

// parseScheduledTime parses a one-shot date value.
// Flexible: tries multiple formats, strips trailing description text.
// CRC: crc-EventScheduler.md | R821
func parseScheduledTime(value string, now time.Time) time.Time {
	value = strings.TrimSpace(value)
	// Try each format, longest first
	dateLayouts := []string{
		"2006-01-02 15:04",
		"2006/01/02 15:04",
		"2006-01-02",
		"2006/01/02",
		"Jan 2, 2006 15:04",
		"Jan 2, 2006",
		"January 2, 2006 15:04",
		"January 2, 2006",
		"2 Jan 2006 15:04",
		"2 Jan 2006",
	}
	for _, layout := range dateLayouts {
		// Try parsing with progressively shorter prefixes
		for end := len(value); end >= len(layout)-2; end-- {
			if t, err := time.ParseInLocation(layout, strings.TrimSpace(value[:end]), now.Location()); err == nil {
				if t.After(now) {
					return t
				}
				return time.Time{} // Past one-shot
			}
		}
	}
	// "MM-DD" or "MM/DD" — annual, next occurrence. R823
	for _, sep := range []string{"-", "/"} {
		if len(value) >= 5 && value[2:3] == sep {
			if t, err := time.Parse("01"+sep+"02", value[:5]); err == nil {
				next := time.Date(now.Year(), t.Month(), t.Day(), 9, 0, 0, 0, now.Location())
				if next.Before(now) {
					next = next.AddDate(1, 0, 0)
				}
				return next
			}
		}
	}
	return time.Time{}
}

// computeNext computes the next occurrence of a recurring event after the given time.
// If notAfter is non-zero, returns zero time when the next occurrence exceeds it.
// All matching is case-insensitive.
// CRC: crc-EventScheduler.md | R822, R823, R824, R1005
func ComputeNext(spec string, after time.Time, notAfter time.Time) time.Time {
	spec = strings.TrimSpace(spec)
	// Strip description after " -- "
	if idx := strings.Index(spec, " -- "); idx >= 0 {
		spec = strings.TrimSpace(spec[:idx])
	}
	lower := strings.ToLower(spec)

	// "every ..."
	if rest, ok := strings.CutPrefix(lower, "every "); ok {
		rest = strings.TrimSpace(rest)

		// Strip optional "at HH:MM" from the end
		hour, minute := 9, 0
		if atIdx := strings.LastIndex(rest, " at "); atIdx >= 0 {
			timeStr := strings.TrimSpace(rest[atIdx+4:])
			if parts := strings.SplitN(timeStr, ":", 2); len(parts) == 2 {
				hour, _ = strconv.Atoi(parts[0])
				minute, _ = strconv.Atoi(parts[1])
			}
			rest = strings.TrimSpace(rest[:atIdx])
		}

		// "every WEEKDAY" — case-insensitive
		if day, ok := parseDayName(rest); ok {
			next := time.Date(after.Year(), after.Month(), after.Day(), hour, minute, 0, 0, after.Location())
			for next.Weekday() != day || !next.After(after) {
				next = next.AddDate(0, 0, 1)
			}
			return boundCheck(next, notAfter)
		}

		// "every weekday" / "every weekend"
		if rest == "weekday" || rest == "weekend" {
			isWeekday := rest == "weekday"
			next := time.Date(after.Year(), after.Month(), after.Day(), hour, minute, 0, 0, after.Location())
			if !next.After(after) {
				next = next.AddDate(0, 0, 1)
			}
			for {
				wd := next.Weekday()
				match := isWeekday && wd >= time.Monday && wd <= time.Friday ||
					!isWeekday && (wd == time.Saturday || wd == time.Sunday)
				if match {
					return boundCheck(next, notAfter)
				}
				next = next.AddDate(0, 0, 1)
			}
		}

		// "every Nth of the month"
		if n, ok := parseOrdinalPrefix(rest); ok {
			if strings.Contains(rest, "of the month") {
				next := time.Date(after.Year(), after.Month(), n, hour, minute, 0, 0, after.Location())
				if !next.After(after) {
					next = next.AddDate(0, 1, 0)
				}
				return boundCheck(next, notAfter)
			}
			// "every Nth WEEKDAY" — e.g. "every 3rd monday"
			remaining := strings.TrimSpace(rest[strings.IndexByte(rest, ' ')+1:])
			if day, ok := parseDayName(remaining); ok {
				return boundCheck(nthWeekdayInMonth(after, n, day, hour, minute), notAfter)
			}
		}

		// "every Nm" / "every Nh" — duration intervals
		if suffix, ok := strings.CutSuffix(rest, "m"); ok {
			if n, err := strconv.Atoi(strings.TrimSpace(suffix)); err == nil {
				d := time.Duration(n) * time.Minute
				startOfHour := time.Date(after.Year(), after.Month(), after.Day(), after.Hour(), 0, 0, 0, after.Location())
				elapsed := after.Sub(startOfHour)
				intervals := int(elapsed/d) + 1
				return boundCheck(startOfHour.Add(time.Duration(intervals)*d), notAfter)
			}
		}
		if suffix, ok := strings.CutSuffix(rest, "h"); ok {
			if n, err := strconv.Atoi(strings.TrimSpace(suffix)); err == nil {
				d := time.Duration(n) * time.Hour
				startOfDay := time.Date(after.Year(), after.Month(), after.Day(), 0, 0, 0, 0, after.Location())
				elapsed := after.Sub(startOfDay)
				intervals := int(elapsed/d) + 1
				return boundCheck(startOfDay.Add(time.Duration(intervals)*d), notAfter)
			}
		}
	}

	// "MM-DD" or "MM/DD" — annual shorthand (same as parseScheduledTime)
	if t := parseScheduledTime(spec, after); !t.IsZero() {
		return boundCheck(t, notAfter)
	}

	// "annual" — next year same date
	if lower == "annual" {
		return boundCheck(after.AddDate(1, 0, 0), notAfter)
	}

	return time.Time{}
}

// boundCheck returns zero time if t exceeds notAfter (when notAfter is non-zero).
func boundCheck(t time.Time, notAfter time.Time) time.Time {
	if t.IsZero() || notAfter.IsZero() {
		return t
	}
	if t.After(notAfter) {
		return time.Time{}
	}
	return t
}

// parseDayName matches a day name case-insensitively.
func parseDayName(s string) (time.Weekday, bool) {
	day, ok := dayNames[strings.TrimSpace(strings.ToLower(s))]
	return day, ok
}

// parseOrdinalPrefix extracts an ordinal number from the start of a string.
// Handles "1st", "2nd", "3rd", "4th"..."365th" and words "second" through "tenth".
func parseOrdinalPrefix(s string) (int, bool) {
	s = strings.TrimSpace(s)
	words := map[string]int{
		"second": 2, "third": 3, "fourth": 4, "fifth": 5,
		"sixth": 6, "seventh": 7, "eighth": 8, "ninth": 9, "tenth": 10,
	}
	first := s
	if idx := strings.IndexByte(s, ' '); idx >= 0 {
		first = s[:idx]
	}
	if n, ok := words[first]; ok {
		return n, true
	}
	// "1st", "2nd", "3rd", "4th"...
	numStr := strings.TrimRight(first, "stndrh")
	if n, err := strconv.Atoi(numStr); err == nil && n > 0 {
		return n, true
	}
	return 0, false
}

// nthWeekdayInMonth returns the nth occurrence of a weekday in the next month
// where it falls after the given time.
func nthWeekdayInMonth(after time.Time, n int, day time.Weekday, hour, minute int) time.Time {
	// Try this month first, then next
	for monthOffset := 0; monthOffset <= 1; monthOffset++ {
		y, m, _ := after.AddDate(0, monthOffset, 0).Date()
		first := time.Date(y, m, 1, hour, minute, 0, 0, after.Location())
		// Find first matching weekday
		for first.Weekday() != day {
			first = first.AddDate(0, 0, 1)
		}
		// Advance to nth occurrence
		target := first.AddDate(0, 0, (n-1)*7)
		if target.Month() == m && target.After(after) {
			return target
		}
	}
	return time.Time{}
}

// DateRange represents a parsed date/time range from a schedule tag value.
// CRC: crc-EventScheduler.md | R857, R858, R859, R865
type DateRange struct {
	Start       time.Time
	End         time.Time
	Description string // text after the date portion
	AllDay      bool
}

// ParseDateValue parses a schedule tag value including the .. duration operator.
// Uses itlightning/dateparse with token-trimming for flexible date recognition.
// Returns the parsed range and remaining description text.
// CRC: crc-EventScheduler.md | R857, R858, R859, R860, R861, R865
func ParseDateValue(value string, defaultDur string, loc *time.Location) (DateRange, error) {
	value = strings.TrimSpace(value)
	if value == "" {
		return DateRange{}, errEmptyValue
	}

	// Split on .. first (range/duration detection) R857
	if idx := strings.Index(value, ".."); idx >= 0 {
		leftStr := strings.TrimSpace(value[:idx])
		rightStr := strings.TrimSpace(value[idx+2:])

		// Parse left side (must be absolute date)
		// CRC: crc-EventScheduler.md | R864
		start, _, err := parseDateTrimming(leftStr, loc)
		if err != nil {
			return DateRange{}, err
		}

		// Right side: try relative duration first
		// CRC: crc-EventScheduler.md | R862, R863
		if end, ok := parseRelativeDuration(start, rightStr); ok {
			desc := trimRelativePrefix(rightStr)
			return DateRange{Start: start, End: end, Description: strings.TrimSpace(desc)}, nil
		}

		// Right side: try as time-only (same day) R857
		if t, err := parseTimeOnly(rightStr); err == nil {
			end := time.Date(start.Year(), start.Month(), start.Day(), t.Hour(), t.Minute(), 0, 0, loc)
			desc := ""
			if len(rightStr) > 5 {
				desc = strings.TrimSpace(rightStr[5:])
			}
			return DateRange{Start: start, End: end, Description: desc}, nil
		}

		// Right side: try as full date R857
		end, rightDesc, err := parseDateTrimming(rightStr, loc)
		if err != nil {
			return DateRange{}, err
		}
		return DateRange{Start: start, End: end, Description: strings.TrimSpace(rightDesc)}, nil
	}

	// No .. — single date/time R858
	start, desc, err := parseDateTrimming(value, loc)
	if err != nil {
		return DateRange{}, err
	}

	allDay := isDateOnly(value)

	end := start
	if !allDay && defaultDur != "" {
		if defaultDur == "all-day" {
			allDay = true
			end = time.Date(start.Year(), start.Month(), start.Day(), 23, 59, 59, 0, loc)
		} else if d, derr := time.ParseDuration(defaultDur); derr == nil {
			end = start.Add(d)
		}
	}

	return DateRange{Start: start, End: end, Description: strings.TrimSpace(desc), AllDay: allDay}, nil
}

var errEmptyValue = &ParseError{"empty date value"}

// ParseError is a date parsing error.
type ParseError struct {
	Msg string
}

func (e *ParseError) Error() string { return e.Msg }

// parseDateTrimming uses dateparse with a token-trimming loop to separate
// the date from trailing description text. Strips date keywords first.
// CRC: crc-EventScheduler.md | R860, R861, R996, R997, R998, R999
func parseDateTrimming(s string, loc *time.Location) (time.Time, string, error) {
	s = strings.TrimSpace(s)
	if s == "" {
		return time.Time{}, "", errEmptyValue
	}
	// Strip date keyword if present (R996, R997, R998).
	stripped, _ := stripDateKeyword(s, loc)
	return parseDateTrimmingRaw(stripped, loc)
}

// parseDateTrimmingRaw is the core trimming loop without keyword stripping.
// The first candidate that parses is the date; its trailing words become
// the description. Malformed-datetime guards (R2846, R2847, R2848) run on
// that successful candidate.
func parseDateTrimmingRaw(s string, loc *time.Location) (time.Time, string, error) {
	s = strings.TrimSpace(s)
	if s == "" {
		return time.Time{}, "", errEmptyValue
	}
	words := strings.Fields(s)
	for end := len(words); end > 0; end-- {
		candidate := normalizeDashDateTime(strings.Join(words[:end], " ")) // R2846
		t, err := dateparse.ParseIn(candidate, loc)
		if err != nil {
			continue
		}
		// The longest parseable prefix is the date. Reject it outright if
		// it's a malformed shape dateparse accepted silently — don't fall
		// back to a shorter prefix that would parse differently.
		if gerr := guardParsedDate(candidate); gerr != nil { // R2847, R2848
			return time.Time{}, "", gerr
		}
		desc := strings.TrimSpace(strings.Join(words[end:], " "))
		return t, desc, nil
	}
	return time.Time{}, "", &ParseError{"cannot parse date: " + s}
}

// dashDateTimeRe matches an ISO date whose time-of-day is joined with a
// hyphen instead of a `T` or space — e.g. 2026-05-28-13:45 or 2026-05-28-13.
// dateparse reads the trailing -HH:MM as a timezone offset and returns
// midnight, so the date and time-of-day are separated and rejoined with `T`.
var dashDateTimeRe = regexp.MustCompile(`^(\d{4}-\d{2}-\d{2})-(\d{1,2}(?::\d{2})?)($|\s)`)

// normalizeDashDateTime rewrites the dash-joined date/time form to its
// `T`-separated equivalent so dateparse reads a time-of-day, not a timezone
// offset. Tokens that don't match are returned unchanged.
// CRC: crc-EventScheduler.md | R2846
func normalizeDashDateTime(token string) string {
	return dashDateTimeRe.ReplaceAllString(token, "${1}T${2}${3}")
}

// guardParsedDate rejects two shapes dateparse accepts silently:
//   - a date carrying a timezone offset but no time-of-day (e.g. 2026-05-28Z),
//     which would otherwise be read as midnight (R2847);
//   - an ambiguous mm/dd vs dd/mm value (e.g. 3/1/2014), re-checked with
//     dateparse.ParseStrict (R2848).
//
// CRC: crc-EventScheduler.md | R2847, R2848
func guardParsedDate(candidate string) error {
	if layout, err := dateparse.ParseFormat(candidate); err == nil {
		if layoutHasZone(layout) && !layoutHasClock(layout) {
			return &ParseError{"date with a timezone but no time-of-day: " + candidate +
				" (use T or a space before the time, e.g. 2026-05-28T13:45)"}
		}
	}
	if _, err := dateparse.ParseStrict(candidate); err != nil && strings.Contains(err.Error(), "ambiguous") {
		return &ParseError{"ambiguous date (use an unambiguous form like 2014-03-01): " + candidate}
	}
	return nil
}

// layoutHasZone reports whether a dateparse layout carries a timezone token.
// In Go's reference layout, "07" appears only in numeric offsets (-0700,
// -07:00, Z07:00); "Z" and "MST" cover the literal-Z and named-zone forms.
func layoutHasZone(layout string) bool {
	return strings.Contains(layout, "07") || strings.Contains(layout, "Z") || strings.Contains(layout, "MST")
}

// layoutHasClock reports whether a dateparse layout carries a time-of-day.
// The hour/minute/second reference tokens (15, 03, 04, 05) and AM/PM are
// distinct from every date and zone token.
func layoutHasClock(layout string) bool {
	for _, tok := range []string{"15", "03", "04", "05", "PM", "pm"} {
		if strings.Contains(layout, tok) {
			return true
		}
	}
	return false
}

// parseTimeOnly parses HH:MM format.
func parseTimeOnly(s string) (time.Time, error) {
	s = strings.TrimSpace(s)
	if len(s) < 5 {
		return time.Time{}, &ParseError{"too short for time"}
	}
	parts := strings.SplitN(s[:5], ":", 2)
	if len(parts) != 2 {
		return time.Time{}, &ParseError{"not HH:MM"}
	}
	h, err1 := strconv.Atoi(parts[0])
	m, err2 := strconv.Atoi(parts[1])
	if err1 != nil || err2 != nil || h < 0 || h > 23 || m < 0 || m > 59 {
		return time.Time{}, &ParseError{"invalid time"}
	}
	return time.Date(0, 1, 1, h, m, 0, 0, time.UTC), nil
}

// parseRelativeDuration parses anchored relative expressions like
// "one week later", "3 days later". Matches the first 3 words:
// N UNIT later [description...].
// CRC: crc-EventScheduler.md | R862, R863
func parseRelativeDuration(anchor time.Time, expr string) (time.Time, bool) {
	words := strings.Fields(strings.TrimSpace(strings.ToLower(expr)))
	if len(words) < 3 || words[2] != "later" {
		return time.Time{}, false
	}
	n := parseWordNumber(words[0])
	if n <= 0 {
		return time.Time{}, false
	}
	unit := words[1]
	switch {
	case strings.HasPrefix(unit, "day"):
		return anchor.AddDate(0, 0, n), true
	case strings.HasPrefix(unit, "week"):
		return anchor.AddDate(0, 0, n*7), true
	case strings.HasPrefix(unit, "month"):
		return anchor.AddDate(0, n, 0), true
	case strings.HasPrefix(unit, "year"):
		return anchor.AddDate(n, 0, 0), true
	}
	return time.Time{}, false
}

// trimRelativePrefix removes the "N unit later" prefix, returning remaining text.
func trimRelativePrefix(s string) string {
	words := strings.Fields(strings.TrimSpace(s))
	if len(words) >= 3 && strings.ToLower(words[2]) == "later" {
		if _, ok := parseRelativeDuration(time.Time{}, strings.Join(words[:3], " ")); ok {
			if len(words) > 3 {
				return strings.Join(words[3:], " ")
			}
			return ""
		}
	}
	return s
}

// parseWordNumber converts word or digit numbers to int.
func parseWordNumber(s string) int {
	wordNums := map[string]int{
		"one": 1, "two": 2, "three": 3, "four": 4, "five": 5,
		"six": 6, "seven": 7, "eight": 8, "nine": 9, "ten": 10,
		"eleven": 11, "twelve": 12,
	}
	if n, ok := wordNums[s]; ok {
		return n
	}
	if n, err := strconv.Atoi(s); err == nil && n > 0 {
		return n
	}
	return 0
}

// isDateOnly checks if a value has no time component (no colon in first tokens).
func isDateOnly(s string) bool {
	words := strings.Fields(strings.TrimSpace(s))
	if len(words) == 0 {
		return false
	}
	check := words[0]
	if len(words) > 1 {
		check += " " + words[1]
	}
	return !strings.Contains(check, ":")
}
