package ark

// CRC: crc-Monitor.md | crc-LuhmannCLI.md | Seq: seq-luhmann-supervisor.md

import (
	"bufio"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"sort"
	"strings"
	"time"
)

// MonitorRecallFreshness is the freshness window used by `ark monitor
// status` to classify the `recall` class as `active` vs `idle` based on
// the latest record's timestamp.
// CRC: crc-Monitor.md | R2785
const MonitorRecallFreshness = 90 * time.Minute

// MonitorClasses is the hardcoded list of monitoring classes the
// monitor CLI understands.
// CRC: crc-Monitor.md | R2784
var MonitorClasses = []string{"recall", "luhmann"}

// MonitorDir returns the directory holding monitoring JSONL files.
func MonitorDir(arkDir string) string { return filepath.Join(arkDir, "monitoring") }

// MonitorClassPath returns the JSONL path for a class under arkDir.
func MonitorClassPath(arkDir, class string) string {
	return filepath.Join(MonitorDir(arkDir), class+".jsonl")
}

// LuhmannRecord is one append-only entry in ~/.ark/monitoring/luhmann.jsonl.
// CRC: crc-LuhmannCLI.md | R2791, R2792, R2793, R2861
type LuhmannRecord struct {
	Timestamp string `json:"ts"`
	Kind      string `json:"kind"`
	Class     string `json:"class,omitempty"`
	Nonce     int    `json:"nonce,omitempty"`
	TaskID    string `json:"task_id,omitempty"`
	Reason    string `json:"reason,omitempty"`
	Crashes   int    `json:"crashes"`
	QuitEarly int    `json:"quit_early"`
	Backoff   int    `json:"backoff,omitempty"`
}

// MonitorControlRecord is the tiny record `ark monitor pause/resume`
// appends to <class>.jsonl. Same JSONL file as the class's other
// records; the consumer treats `kind` of `pause`/`resume` as a
// class-level control signal. A storm pause carries a `reason`
// (crash-storm / quit-early-storm) that distinguishes it from a plain
// user pause and lights the emergency flag (R2863).
// CRC: crc-Monitor.md | R2787, R2788, R2863
type MonitorControlRecord struct {
	Timestamp string `json:"ts"`
	Kind      string `json:"kind"`
	Class     string `json:"class"`
	Nonce     int    `json:"nonce"`
	Reason    string `json:"reason,omitempty"`
}

// appendMonitorJSONL appends one JSON-encoded record (followed by a
// newline) to the given path under the write actor. Creates parent
// directories on demand.
func appendMonitorJSONL(db *DB, path string, record any) error {
	return SyncVoid(db, func(_ *DB) error {
		if err := os.MkdirAll(filepath.Dir(path), 0755); err != nil {
			return err
		}
		f, err := os.OpenFile(path, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0644)
		if err != nil {
			return err
		}
		defer f.Close()
		enc := json.NewEncoder(f)
		return enc.Encode(record)
	})
}

// AppendLuhmannRecord appends one supervisor record to luhmann.jsonl
// via the write actor. The caller is responsible for setting Timestamp
// (typically `time.Now().UTC().Format(time.RFC3339)`); a missing
// timestamp is filled with the current UTC time.
// CRC: crc-LuhmannCLI.md | R2794, R2795
func AppendLuhmannRecord(db *DB, arkDir string, rec LuhmannRecord) error {
	if rec.Timestamp == "" {
		rec.Timestamp = time.Now().UTC().Format(time.RFC3339)
	}
	return appendMonitorJSONL(db, MonitorClassPath(arkDir, "luhmann"), rec)
}

// AppendMonitorControl appends one pause/resume control record to
// `<class>.jsonl`. Caller validates the state transition (see
// MonitorClassState) before invoking; this function does not enforce
// the guard.
// CRC: crc-Monitor.md | R2787, R2788, R2863
func AppendMonitorControl(db *DB, arkDir, class, kind, reason string) error {
	rec := MonitorControlRecord{
		Timestamp: time.Now().UTC().Format(time.RFC3339),
		Kind:      kind,
		Class:     class,
		Reason:    reason,
	}
	return appendMonitorJSONL(db, MonitorClassPath(arkDir, class), rec)
}

// readMonitorLines reads the contents of path as JSONL, returning each
// line decoded as `map[string]any`. Missing file → empty result, no
// error. Malformed lines are skipped (best-effort tail).
func readMonitorLines(path string) ([]map[string]any, error) {
	f, err := os.Open(path)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return nil, nil
		}
		return nil, err
	}
	defer f.Close()
	var out []map[string]any
	scanner := bufio.NewScanner(f)
	scanner.Buffer(make([]byte, 1024*1024), 1024*1024)
	for scanner.Scan() {
		line := scanner.Bytes()
		if len(line) == 0 {
			continue
		}
		var m map[string]any
		if err := json.Unmarshal(line, &m); err != nil {
			continue
		}
		out = append(out, m)
	}
	return out, scanner.Err()
}

// MonitorTail returns the last n records for a class (oldest-first
// within the returned window). When class is empty, reads every
// shipped class and interleaves records by timestamp.
// CRC: crc-Monitor.md | R2786
func MonitorTail(arkDir, class string, n int) ([]map[string]any, error) {
	if n <= 0 {
		n = 20
	}
	classes := []string{class}
	if class == "" {
		classes = MonitorClasses
	}
	var all []map[string]any
	for _, c := range classes {
		recs, err := readMonitorLines(MonitorClassPath(arkDir, c))
		if err != nil {
			return nil, err
		}
		for _, r := range recs {
			if _, ok := r["_class"]; !ok {
				r["_class"] = c
			}
			all = append(all, r)
		}
	}
	sort.SliceStable(all, func(i, j int) bool {
		return tsString(all[i]) < tsString(all[j])
	})
	if len(all) > n {
		all = all[len(all)-n:]
	}
	return all, nil
}

func tsString(rec map[string]any) string {
	if s, ok := rec["ts"].(string); ok {
		return s
	}
	if s, ok := rec["timestamp"].(string); ok {
		return s
	}
	return ""
}

// MonitorClassSummary is the per-class structured view emitted by
// `ark monitor status`.
// CRC: crc-Monitor.md | R2784, R2785, R2863
type MonitorClassSummary struct {
	Class           string          `json:"class"`
	State           string          `json:"state"`
	LatestTimestamp string          `json:"latest_timestamp,omitempty"`
	LatestKind      string          `json:"latest_kind,omitempty"`
	Counters        map[string]any  `json:"counters,omitempty"`
	Emergency       *EmergencyState `json:"emergency,omitempty"`
}

// EmergencyState describes a class currently in a storm pause — the
// loud, machine-visible signal the orchestrator raises when a crash or
// quit-early streak trips its ceiling. Exposed so Frictionless can
// reflect it (the downstream emergency-light UI) and so the orchestrator
// can escalate. R2863
type EmergencyState struct {
	Active bool   `json:"active"`
	Class  string `json:"class"`
	Reason string `json:"reason"`
	Since  string `json:"since,omitempty"`
}

// MonitorStatus computes the per-class state and counters for every
// shipped class. Cold-start; reads only `~/.ark/monitoring/*.jsonl`.
// CRC: crc-Monitor.md | R2784, R2785
func MonitorStatus(arkDir string) ([]MonitorClassSummary, error) {
	out := make([]MonitorClassSummary, 0, len(MonitorClasses))
	for _, c := range MonitorClasses {
		recs, err := readMonitorLines(MonitorClassPath(arkDir, c))
		if err != nil {
			return nil, err
		}
		out = append(out, deriveClassSummary(c, recs))
	}
	return out, nil
}

// deriveClassSummary turns a slice of class records into the
// summary the `monitor status` command renders.
// CRC: crc-Monitor.md | R2785
func deriveClassSummary(class string, recs []map[string]any) MonitorClassSummary {
	sum := MonitorClassSummary{Class: class, State: "empty"}
	if len(recs) == 0 {
		return sum
	}
	latest := recs[len(recs)-1]
	sum.LatestTimestamp = tsString(latest)
	if kind, ok := latest["kind"].(string); ok {
		sum.LatestKind = kind
	}
	sum.Counters = make(map[string]any)
	switch class {
	case "luhmann":
		sum.State = luhmannState(recs)
		// Most recent crashes, quit_early, and nonce values.
		haveCrashes, haveQuitEarly, haveNonce := false, false, false
		for i := len(recs) - 1; i >= 0 && !(haveCrashes && haveQuitEarly && haveNonce); i-- {
			r := recs[i]
			if !haveCrashes {
				if v, ok := r["crashes"].(float64); ok {
					sum.Counters["crashes"] = int(v)
					haveCrashes = true
				}
			}
			if !haveQuitEarly {
				if v, ok := r["quit_early"].(float64); ok {
					sum.Counters["quit_early"] = int(v)
					haveQuitEarly = true
				}
			}
			if !haveNonce {
				if v, ok := r["nonce"].(float64); ok && int(v) != 0 {
					sum.Counters["nonce"] = int(v)
					haveNonce = true
				}
			}
		}
	case "recall":
		sum.State = recallState(latest)
		// Recent fire count + averages over the tail.
		const window = 20
		start := 0
		if len(recs) > window {
			start = len(recs) - window
		}
		tail := recs[start:]
		sum.Counters["recent_fires"] = len(tail)
		var inTok, outTok int
		var ctxTok int
		for _, r := range tail {
			inTok += intField(r, "in_tokens")
			outTok += intField(r, "out_tokens")
			ctxTok += intField(r, "context_tokens")
		}
		if len(tail) > 0 {
			sum.Counters["avg_in_tokens"] = inTok / len(tail)
			sum.Counters["avg_out_tokens"] = outTok / len(tail)
			sum.Counters["avg_context_tokens"] = ctxTok / len(tail)
		}
	}
	// Emergency: a storm pause on this class's log lights the flag,
	// independent of the activity/lifecycle state above (R2863).
	if reason, since := stormPause(recs); reason != "" {
		sum.Emergency = &EmergencyState{Active: true, Class: class, Reason: reason, Since: since}
	}
	return sum
}

// isStormReason reports whether a pause reason is a supervisor storm
// (a tripped crash or quit-early ceiling) rather than a plain user
// pause. R2863
func isStormReason(reason string) bool {
	return reason == "crash-storm" || reason == "quit-early-storm"
}

// stormPause returns the reason and timestamp of an active storm pause
// for a class, or ("", "") when the class is not currently paused on a
// storm. A class is in a storm pause when its latest control state is
// "paused" (luhmannState) and the most recent pause record carries a
// storm reason. R2863
func stormPause(recs []map[string]any) (reason, since string) {
	if luhmannState(recs) != "paused" {
		return "", ""
	}
	for i := len(recs) - 1; i >= 0; i-- {
		if k, _ := recs[i]["kind"].(string); k == "pause" {
			r, _ := recs[i]["reason"].(string)
			if isStormReason(r) {
				return r, tsString(recs[i])
			}
			return "", ""
		}
	}
	return "", ""
}

// MonitorEmergencies returns one EmergencyState per shipped class
// currently in a storm pause (empty when none). Cold-start, reading the
// same supervisor logs `monitor status` does, so any in-process caller
// (the server, exposing it to a Lua bridge for the Frictionless
// emergency light) sees a flag always consistent with the records.
// CRC: crc-Monitor.md | R2863
func MonitorEmergencies(arkDir string) ([]EmergencyState, error) {
	sums, err := MonitorStatus(arkDir)
	if err != nil {
		return nil, err
	}
	var out []EmergencyState
	for _, s := range sums {
		if s.Emergency != nil && s.Emergency.Active {
			out = append(out, *s.Emergency)
		}
	}
	return out, nil
}

func intField(rec map[string]any, name string) int {
	if v, ok := rec[name].(float64); ok {
		return int(v)
	}
	return 0
}

// luhmannState walks records back-to-front; the most recent state-
// defining record (spawn / exit / respawn / crash / quit-early / pause /
// resume) determines current state. A quit-early is transient — always
// followed by a respawn — so it maps to running, not a terminal state.
// R2785, R2861
func luhmannState(recs []map[string]any) string {
	for i := len(recs) - 1; i >= 0; i-- {
		kind, _ := recs[i]["kind"].(string)
		switch kind {
		case "pause":
			return "paused"
		case "resume", "spawn", "respawn", "exit", "quit-early":
			return "running"
		case "crash":
			return "crashed"
		}
	}
	return "empty"
}

// recallState classifies based on whether the latest record's
// timestamp is within MonitorRecallFreshness of now. R2785
func recallState(latest map[string]any) string {
	ts := tsString(latest)
	if ts == "" {
		return "empty"
	}
	t, err := time.Parse(time.RFC3339, ts)
	if err != nil {
		return "unknown"
	}
	if time.Since(t) < MonitorRecallFreshness {
		return "active"
	}
	return "idle"
}

// CheckMonitorControlGuard returns nil when a pause/resume operation
// can proceed against the class's current state, or an error
// describing the conflict. R2789
func CheckMonitorControlGuard(arkDir, class, kind string) error {
	recs, err := readMonitorLines(MonitorClassPath(arkDir, class))
	if err != nil {
		return err
	}
	current := luhmannState(recs) // same state machine for any class log
	switch kind {
	case "pause":
		if current == "paused" {
			return fmt.Errorf("class %q is already paused", class)
		}
	case "resume":
		if current != "paused" {
			return fmt.Errorf("class %q is not paused (state=%q)", class, current)
		}
	}
	return nil
}

// IsKnownMonitorClass reports whether the named class is one ark
// monitors.
func IsKnownMonitorClass(class string) bool {
	return slices.Contains(MonitorClasses, class)
}

// FormatMonitorBullet renders one record as a short markdown bullet
// for `ark monitor recent` (non-JSON output). The shape depends on
// the class.
func FormatMonitorBullet(rec map[string]any) string {
	class, _ := rec["_class"].(string)
	ts := tsString(rec)
	kind, _ := rec["kind"].(string)
	var sb strings.Builder
	sb.WriteString("- ")
	sb.WriteString(ts)
	sb.WriteString(" [")
	sb.WriteString(class)
	sb.WriteString("] ")
	if kind != "" {
		sb.WriteString(kind)
	}
	switch class {
	case "luhmann":
		if v, ok := rec["class"].(string); ok && v != "" {
			fmt.Fprintf(&sb, " class=%s", v)
		}
		if v := intField(rec, "nonce"); v != 0 {
			fmt.Fprintf(&sb, " nonce=%d", v)
		}
		if v, ok := rec["reason"].(string); ok && v != "" {
			fmt.Fprintf(&sb, " reason=%s", v)
		}
		if v := intField(rec, "crashes"); v > 0 {
			fmt.Fprintf(&sb, " crashes=%d", v)
		}
		if v := intField(rec, "quit_early"); v > 0 {
			fmt.Fprintf(&sb, " quit_early=%d", v)
		}
	case "recall":
		if v := intField(rec, "fire"); v != 0 {
			fmt.Fprintf(&sb, " fire=%d", v)
		}
		if v := intField(rec, "nonce"); v != 0 {
			fmt.Fprintf(&sb, " nonce=%d", v)
		}
		if v, ok := rec["outcome"].(string); ok && v != "" {
			fmt.Fprintf(&sb, " outcome=%s", v)
		}
		if v := intField(rec, "surfaced"); v != 0 {
			fmt.Fprintf(&sb, " surfaced=%d", v)
		}
		if v := intField(rec, "recommended"); v != 0 {
			fmt.Fprintf(&sb, " recommended=%d", v)
		}
	}
	return sb.String()
}

// PrevCounters returns the `crashes` and `quit_early` counters on the
// most recent luhmann.jsonl record for the named class, or (0, 0) when
// none. One backward scan serves both counters; `exit-record` uses it to
// compute the new values per R2861.
// CRC: crc-LuhmannCLI.md | R2795, R2861
func PrevCounters(arkDir, class string) (crashes, quitEarly int, err error) {
	recs, err := readMonitorLines(MonitorClassPath(arkDir, "luhmann"))
	if err != nil {
		return 0, 0, err
	}
	for i := len(recs) - 1; i >= 0; i-- {
		if c, ok := recs[i]["class"].(string); !ok || c != class {
			continue
		}
		return intField(recs[i], "crashes"), intField(recs[i], "quit_early"), nil
	}
	return 0, 0, nil
}

// ClassifyLuhmannReason maps an exit reason to the supervisor record's
// kind: `context-limit` → "exit" (healthy recycle), `quit-early` →
// "quit-early" (R2861), anything else → "crash". The boolean return is
// the legacy reset hint (true only for a healthy exit); the actual
// counter math is authoritative server-side, keyed on kind (R2861), so
// the server holds either counter the current kind does not implicate.
// R2795, R2861
func ClassifyLuhmannReason(reason string) (kind string, reset bool) {
	switch reason {
	case "context-limit":
		return "exit", true
	case "quit-early":
		return "quit-early", false
	default:
		return "crash", false
	}
}
