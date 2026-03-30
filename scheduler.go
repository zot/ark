package ark

// CRC: crc-EventScheduler.md | Seq: seq-pubsub.md

import (
	"bufio"
	"container/heap"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"log"
	"os"
	"path/filepath"
	"slices"
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
// R1025, R1027
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
// R1025, R1027, R1041, R1043
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
			if IsRecurringSpec(c.Spec) {
				// Crank forward through the range
				startAfter := start.Add(-1 * time.Second)
				if !c.NotBefore.IsZero() && c.NotBefore.After(startAfter) {
					startAfter = c.NotBefore.Add(-1 * time.Second)
				}
				notAfter := end
				if !c.NotAfter.IsZero() && c.NotAfter.Before(notAfter) {
					notAfter = c.NotAfter
				}
				next := ComputeNext(c.Spec, startAfter, notAfter)
				for !next.IsZero() && !next.After(end) {
					// R1039: skip @remove: exceptions
					if removed, _ := isRemoved(next, c.Removes); !removed {
						ev := ScheduleEvent{
							Date:   next.Format("20060102"),
							Tag:    c.Event,
							Start:  next,
							End:    next,
							Source: c.Source,
							Spec:   c.Spec,
						}
						if def, ok := es.config.IsScheduleTag(c.Event); ok {
							applyDefaultDuration(&ev, def, loc)
						}
						events = append(events, ev)
					}
					next = ComputeNext(c.Spec, next, notAfter)
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
							Spec:    c.Spec,
						}
						if def, ok := es.config.IsScheduleTag(c.Event); ok {
							applyDefaultDuration(&ev, def, loc)
						}
						events = append(events, ev)
					}
				}
			} else {
				// One-shot: parse the spec as a date
				dr, err := ParseDateValue(c.Spec, "", loc)
				if err != nil {
					continue
				}
				if def, ok := es.config.IsScheduleTag(c.Event); ok && dr.End == dr.Start {
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
						Spec:    c.Spec,
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

// EventScheduler manages time-based event delivery. R805, R806, R807
// Reads schedule log files from ~/.ark/schedule/ at startup.
// CRC: crc-EventScheduler.md | Seq: seq-scheduling.md
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
// CRC: crc-EventScheduler.md | R899, R900, R901
type LogChunk struct {
	Event     string          // tag name (e.g., "standup")
	Source    string          // source file path
	Spec      string          // recurring spec (e.g., "every Monday at 09:00")
	NotBefore time.Time       // R1007: @ark-event-start: bound (zero = no bound)
	NotAfter  time.Time       // R1007: @ark-event-end: bound (zero = no bound)
	Fired     []string        // @ark-event-fired: date strings
	Upcoming  []string        // @ark-event-upcoming: date strings
	CheckGaps []string        // @check-gap: date strings — unresolved fired events (R965, R969)
	Removes   []DateException // R1035: @remove: exceptions from source file
	Adds      []DateException // R1036: @add: exceptions from source file
}

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

// buildDateSet returns a set of date strings already in a chunk (upcoming + fired).
func buildDateSet(c *LogChunk) map[string]bool {
	m := make(map[string]bool, len(c.Upcoming)+len(c.Fired))
	for _, u := range c.Upcoming {
		m[u] = true
	}
	for _, f := range c.Fired {
		if fields := strings.Fields(f); len(fields) > 0 {
			if t, err := time.Parse(scheduleDateFmt, fields[0]); err == nil {
				m[t.Format(scheduleDateFmt)] = true
			}
		}
	}
	return m
}

// crankForward fills a chunk's Upcoming with dates through the forward window.
// Returns the number of new entries added. Optionally enqueues events.
func (es *EventScheduler) crankForward(c *LogChunk, now time.Time, enqueue bool) int {
	if !IsRecurringSpec(c.Spec) {
		return 0
	}
	// R1003: start from notBefore if it's after now
	startAfter := now
	if !c.NotBefore.IsZero() && c.NotBefore.After(now) {
		startAfter = c.NotBefore.Add(-1 * time.Second) // computeNext returns strictly after
	}

	modified := 0

	// Convert past upcoming entries to fired (catch-up after downtime).
	var futureUpcoming []string
	for _, u := range c.Upcoming {
		t, err := ParseDate(u)
		if err != nil {
			futureUpcoming = append(futureUpcoming, u)
			continue
		}
		if t.Before(now) {
			c.Fired = append(c.Fired, u)
			modified++
		} else {
			futureUpcoming = append(futureUpcoming, u)
		}
	}
	c.Upcoming = futureUpcoming

	// Ensure exactly one future upcoming entry exists, skipping removed dates.
	if len(c.Upcoming) == 0 {
		next := ComputeNext(c.Spec, startAfter, c.NotAfter)
		// R1039: skip @remove: exceptions
		for !next.IsZero() {
			if removed, _ := isRemoved(next, c.Removes); !removed {
				break
			}
			next = ComputeNext(c.Spec, next, c.NotAfter)
		}
		if !next.IsZero() {
			dateStr := next.Format(scheduleDateFmt)
			c.Upcoming = append(c.Upcoming, dateStr)
			modified++
			if enqueue {
				es.Add(&ScheduledEvent{
					ID:       eventID(c.Source, c.Event, dateStr),
					Tag:      c.Event,
					Value:    dateStr,
					Path:     c.Source,
					NextFire: next,
				})
			}
		}
	}
	return modified
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

// ReadLogFile reads all chunks from a schedule log file.
func ReadLogFile(path string) ([]LogChunk, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer f.Close()

	var chunks []LogChunk
	var cur *LogChunk
	scanner := bufio.NewScanner(f)
	for scanner.Scan() {
		line := scanner.Text()
		if strings.HasPrefix(line, "@ark-event:") {
			if cur != nil {
				chunks = append(chunks, *cur)
			}
			cur = &LogChunk{Event: strings.TrimSpace(line[len("@ark-event:"):])}
		} else if cur != nil {
			switch {
			case strings.HasPrefix(line, "@ark-event-source:"):
				cur.Source = strings.TrimSpace(line[len("@ark-event-source:"):])
			case strings.HasPrefix(line, "@ark-event-spec:"):
				cur.Spec = strings.TrimSpace(line[len("@ark-event-spec:"):])
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
				cur.Upcoming = append(cur.Upcoming, strings.TrimSpace(line[len("@ark-event-upcoming:"):]))
			case strings.HasPrefix(line, "@check-gap:"):
				cur.CheckGaps = append(cur.CheckGaps, strings.TrimSpace(line[len("@check-gap:"):]))
			}
		}
	}
	if cur != nil {
		chunks = append(chunks, *cur)
	}
	return chunks, scanner.Err()
}

// WriteLogFile writes chunks to a schedule log file.
// Caller must ensure the directory exists (via ensureDir).
// formatLogChunks renders log chunks as markdown bytes.
func formatLogChunks(chunks []LogChunk) []byte {
	var buf strings.Builder
	for i, c := range chunks {
		if i > 0 {
			buf.WriteString("\n---\n\n")
		}
		fmt.Fprintf(&buf, "@ark-event: %s\n", c.Event)
		fmt.Fprintf(&buf, "@ark-event-source: %s\n", c.Source)
		if c.Spec != "" {
			fmt.Fprintf(&buf, "@ark-event-spec: %s\n", c.Spec)
		}
		if !c.NotBefore.IsZero() {
			fmt.Fprintf(&buf, "@ark-event-start: %s\n", c.NotBefore.Format("2006-01-02"))
		}
		if !c.NotAfter.IsZero() {
			fmt.Fprintf(&buf, "@ark-event-end: %s\n", c.NotAfter.Format("2006-01-02"))
		}
		buf.WriteString("\n")
		for _, f := range c.Fired {
			fmt.Fprintf(&buf, "@ark-event-fired: %s\n", f)
		}
		for _, u := range c.Upcoming {
			fmt.Fprintf(&buf, "@ark-event-upcoming: %s\n", u)
		}
		for _, g := range c.CheckGaps {
			fmt.Fprintf(&buf, "@check-gap: %s\n", g)
		}
	}
	return []byte(buf.String())
}

func WriteLogFile(path string, chunks []LogChunk) error {
	return os.WriteFile(path, formatLogChunks(chunks), 0644)
}

// EnsureUpcoming ensures a schedule log chunk exists for an event with
// @ark-event-upcoming: entries through the forward window.
// Called from the indexer when a source file with a schedule tag is indexed.
// CRC: crc-EventScheduler.md | R902, R905
func (es *EventScheduler) EnsureUpcoming(tag, value, sourcePath string) error {
	isTmp := strings.HasPrefix(sourcePath, "tmp://")
	var logPath string
	var chunks []LogChunk
	if isTmp {
		logPath = "tmp://schedule/" + logFileHash(sourcePath) + ".md"
		// tmp:// logs don't persist on disk — read from WriteTmpLog content if available
		// For now, start fresh each time (tmp:// is ephemeral)
	} else {
		if err := es.ensureDir(); err != nil {
			return err
		}
		logPath = es.logFilePath(sourcePath)
		chunks, _ = ReadLogFile(logPath)
	}

	// Find or create chunk
	var chunk *LogChunk
	for i := range chunks {
		if chunks[i].Event == tag && chunks[i].Source == sourcePath {
			chunk = &chunks[i]
			break
		}
	}
	now := time.Now()

	// R1000-R1004: extract bounds from recurring values
	spec := value
	var notBefore, notAfter time.Time
	if IsRecurringSpec(value) {
		notBefore, notAfter, spec = ExtractBounds(value, now.Location())
	}

	// R1035, R1036: parse scheduling exceptions from source file
	var removes, adds []DateException
	if !isTmp {
		if content, err := os.ReadFile(sourcePath); err == nil {
			removes, adds = ParseExceptions(content, tag)
		}
	}

	if chunk == nil {
		chunks = append(chunks, LogChunk{Event: tag, Source: sourcePath, Spec: spec, NotBefore: notBefore, NotAfter: notAfter, Removes: removes, Adds: adds})
		chunk = &chunks[len(chunks)-1]
	} else {
		chunk.Spec = spec
		chunk.NotBefore = notBefore
		chunk.NotAfter = notAfter
		chunk.Removes = removes
		chunk.Adds = adds
	}

	modified := false

	if IsRecurringSpec(spec) {
		modified = es.crankForward(chunk, now, false) > 0
	} else {
		// One-shot: add one upcoming if in the future and not already present
		dr, err := ParseDateValue(value, "", now.Location())
		if err != nil {
			return nil
		}
		if dr.Start.After(now) {
			dateStr := dr.Start.Format(scheduleDateFmt)
			existing := buildDateSet(chunk)
			if !existing[dateStr] {
				chunk.Upcoming = append(chunk.Upcoming, dateStr)
				modified = true
			}
		}
	}

	if !modified {
		return nil // R905: no-op write avoided
	}
	if isTmp && es.WriteTmpLog != nil {
		content := formatLogChunks(chunks)
		return es.WriteTmpLog(logPath, content)
	}
	return WriteLogFile(logPath, chunks)
}

// ScanScheduleLogs reads all log files in ~/.ark/schedule/ and populates
// the scheduler queue. Converts past @ark-event-upcoming: to @ark-event-fired:
// and cranks forward for recurring events.
// CRC: crc-EventScheduler.md | R874, R875, R876
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

	now := time.Now()
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

		modified := false
		for i := range chunks {
			c := &chunks[i]

			// Partition upcoming into past (→fired) and future (→enqueue)
			var futureUpcoming []string
			for _, u := range c.Upcoming {
				t, err := time.Parse(scheduleDateFmt, u)
				if err != nil {
					futureUpcoming = append(futureUpcoming, u)
					continue
				}
				if t.Before(now) {
					c.Fired = append(c.Fired, u)
					modified = true
				} else {
					futureUpcoming = append(futureUpcoming, u)
					es.Add(&ScheduledEvent{
						ID:       eventID(c.Source, c.Event, u),
						Tag:      c.Event,
						Value:    u,
						Path:     c.Source,
						NextFire: t,
					})
				}
			}
			c.Upcoming = futureUpcoming

			if es.crankForward(c, now, true) > 0 {
				modified = true
			}
		}

		if modified {
			if err := WriteLogFile(logPath, chunks); err != nil {
				log.Printf("schedule: error writing %s: %v", logPath, err)
			}
		}
	}
	return nil
}

// Add pushes an event onto the queue. Idempotent — same ID replaces. R808, R809
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

// AddChime adds the quarter-chime recurring event. R810
func (es *EventScheduler) AddChime() {
	now := time.Now()
	// Next quarter hour
	minute := now.Minute()
	nextQ := (minute/15 + 1) * 15
	next := time.Date(now.Year(), now.Month(), now.Day(), now.Hour(), 0, 0, 0, now.Location())
	next = next.Add(time.Duration(nextQ) * time.Minute)
	if next.Before(now) {
		next = next.Add(15 * time.Minute)
	}
	es.Add(&ScheduledEvent{
		ID:        "chime:quarter",
		Tag:       "chime",
		Value:     "", // filled at fire time
		NextFire:  next,
		Recurring: "every 15m",
	})
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

// fire delivers the head event and reschedules if recurring. R806, R807, R877
// CRC: crc-EventScheduler.md | Seq: seq-scheduling.md
func (es *EventScheduler) fire() {
	es.mu.Lock()
	if es.queue.Len() == 0 {
		es.mu.Unlock()
		return
	}
	event := heap.Pop(&es.queue).(*ScheduledEvent)

	if event.Tag == "chime" {
		event.Value = time.Now().Format("2006-01-02 15:04 MST, Monday")
	}

	// Check push record. R811
	if !es.pushed[event.ID] {
		es.pushed[event.ID] = true
		tags := []TagValue{{Tag: event.Tag, Value: event.Value}}
		es.mu.Unlock()
		es.pubsub.Publish("", event.Path, tags)
		es.mu.Lock()
	}

	delete(es.pushed, event.ID)
	isChime := event.Tag == "chime"
	es.mu.Unlock()

	// R877, R964-R968: mutate schedule log for lifecycle tags only
	if !isChime && event.Path != "" && es.config != nil && es.config.IsLifecycleTag(event.Tag) {
		es.fireLogMutate(event)
	}

	es.mu.Lock()
	es.resetTimer()
	es.mu.Unlock()
}

// fireLogMutate converts @ark-event-upcoming: → @ark-event-fired: in the
// log file and adds the next upcoming for recurring events. R877
func (es *EventScheduler) fireLogMutate(event *ScheduledEvent) {
	logPath := es.logFilePath(event.Path)
	chunks, err := ReadLogFile(logPath)
	if err != nil {
		log.Printf("schedule: cannot read log for fire: %v", err)
		return
	}

	for i := range chunks {
		c := &chunks[i]
		if c.Event != event.Tag || c.Source != event.Path {
			continue
		}
		// Move firedValue from upcoming to fired, append check-gap
		var newUpcoming []string
		for _, u := range c.Upcoming {
			if u == event.Value {
				c.Fired = append(c.Fired, event.Value)
				// R965: append @check-gap: in same chunk
				if !slices.Contains(c.CheckGaps, event.Value) {
					c.CheckGaps = append(c.CheckGaps, event.Value)
				}
			} else {
				newUpcoming = append(newUpcoming, u)
			}
		}
		c.Upcoming = newUpcoming

		// R877: single lookahead for the next occurrence (not full window)
		if event.Recurring != "" {
			now := time.Now()
			next := ComputeNext(event.Recurring, now, time.Time{})
			if !next.IsZero() {
				dateStr := next.Format(scheduleDateFmt)
				if !slices.Contains(c.Upcoming, dateStr) { // R905: exception check
					c.Upcoming = append(c.Upcoming, dateStr)
					es.Add(&ScheduledEvent{
						ID:        eventID(event.Path, event.Tag, dateStr),
						Tag:       event.Tag,
						Value:     dateStr,
						Path:      event.Path,
						NextFire:  next,
						Recurring: event.Recurring,
					})
				}
			}
		}
		break
	}

	if err := WriteLogFile(logPath, chunks); err != nil {
		log.Printf("schedule: cannot write log for fire: %v", err)
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

// parseScheduledTime parses a one-shot date value. R821
// Flexible: tries multiple formats, strips trailing description text.
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

		// Parse left side (must be absolute date) R864
		start, _, err := parseDateTrimming(leftStr, loc)
		if err != nil {
			return DateRange{}, err
		}

		// Right side: try relative duration first R862, R863
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
func parseDateTrimmingRaw(s string, loc *time.Location) (time.Time, string, error) {
	s = strings.TrimSpace(s)
	if s == "" {
		return time.Time{}, "", errEmptyValue
	}
	words := strings.Fields(s)
	for end := len(words); end > 0; end-- {
		candidate := strings.Join(words[:end], " ")
		t, err := dateparse.ParseIn(candidate, loc)
		if err == nil {
			desc := strings.TrimSpace(strings.Join(words[end:], " "))
			return t, desc, nil
		}
	}
	return time.Time{}, "", &ParseError{"cannot parse date: " + s}
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
// N UNIT later [description...]. R862, R863
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
