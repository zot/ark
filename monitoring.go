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
// CRC: crc-LuhmannCLI.md | R2791, R2792, R2793
type LuhmannRecord struct {
	Timestamp string `json:"ts"`
	Kind      string `json:"kind"`
	Class     string `json:"class,omitempty"`
	Nonce     int    `json:"nonce,omitempty"`
	TaskID    string `json:"task_id,omitempty"`
	Reason    string `json:"reason,omitempty"`
	Crashes   int    `json:"crashes"`
	Backoff   int    `json:"backoff,omitempty"`
}

// MonitorControlRecord is the tiny record `ark monitor pause/resume`
// appends to <class>.jsonl. Same JSONL file as the class's other
// records; the consumer treats `kind` of `pause`/`resume` as a
// class-level control signal.
// CRC: crc-Monitor.md | R2787, R2788
type MonitorControlRecord struct {
	Timestamp string `json:"ts"`
	Kind      string `json:"kind"`
	Class     string `json:"class"`
	Nonce     int    `json:"nonce"`
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
// CRC: crc-Monitor.md | R2787, R2788
func AppendMonitorControl(db *DB, arkDir, class, kind string) error {
	rec := MonitorControlRecord{
		Timestamp: time.Now().UTC().Format(time.RFC3339),
		Kind:      kind,
		Class:     class,
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
// CRC: crc-Monitor.md | R2784, R2785
type MonitorClassSummary struct {
	Class           string         `json:"class"`
	State           string         `json:"state"`
	LatestTimestamp string         `json:"latest_timestamp,omitempty"`
	LatestKind      string         `json:"latest_kind,omitempty"`
	Counters        map[string]any `json:"counters,omitempty"`
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
		// Most recent crashes and nonce values.
		haveCrashes, haveNonce := false, false
		for i := len(recs) - 1; i >= 0 && !(haveCrashes && haveNonce); i-- {
			r := recs[i]
			if !haveCrashes {
				if v, ok := r["crashes"].(float64); ok {
					sum.Counters["crashes"] = int(v)
					haveCrashes = true
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
	return sum
}

func intField(rec map[string]any, name string) int {
	if v, ok := rec[name].(float64); ok {
		return int(v)
	}
	return 0
}

// luhmannState walks records back-to-front; the most recent state-
// defining record (spawn / exit / respawn / crash / pause / resume)
// determines current state. R2785
func luhmannState(recs []map[string]any) string {
	for i := len(recs) - 1; i >= 0; i-- {
		kind, _ := recs[i]["kind"].(string)
		switch kind {
		case "pause":
			return "paused"
		case "resume", "spawn", "respawn", "exit":
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

// PrevCrashes returns the `crashes` counter on the most recent
// luhmann.jsonl record for the named class, or 0 when none. Used by
// `ark luhmann exit-record` to compute the new counter value.
// CRC: crc-LuhmannCLI.md | R2795
func PrevCrashes(arkDir, class string) (int, error) {
	recs, err := readMonitorLines(MonitorClassPath(arkDir, "luhmann"))
	if err != nil {
		return 0, err
	}
	for i := len(recs) - 1; i >= 0; i-- {
		if c, ok := recs[i]["class"].(string); !ok || c != class {
			continue
		}
		return intField(recs[i], "crashes"), nil
	}
	return 0, nil
}

// ClassifyLuhmannReason maps an exit reason to the supervisor record's
// kind ("exit" for healthy recycle, "crash" otherwise) and decides
// whether the crashes counter should reset (true) or increment (false).
// R2795
func ClassifyLuhmannReason(reason string) (kind string, reset bool) {
	if reason == "context-limit" {
		return "exit", true
	}
	return "crash", false
}
