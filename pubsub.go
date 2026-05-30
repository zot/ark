package ark

// CRC: crc-PubSub.md | Seq: seq-pubsub.md

import (
	"fmt"
	"log"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/bmatcuk/doublestar/v4"
	"github.com/zot/microfts2"
)

// TagSubKind selects whether the subscription matches the tag on
// the chunk that carries it (default) or every chunk belonging to a
// file that has the tag (file-scoped).
// CRC: crc-PubSub.md | R2462
type TagSubKind int

const (
	TagSubChunk TagSubKind = iota // R2457: tag arrives on this chunk
	TagSubFile                    // R2460: any chunk on a matching file
)

// TagSub is a single subscription entry.
// CRC: crc-PubSub.md | R2442, R2457, R2458, R2460, R2462, R2463
type TagSub struct {
	Kind         TagSubKind     // R2462: chunk vs file-scoped
	Predicate    MatchPredicate // R2442: parsed sigil form
	FilterFiles  []string       // only match these path globs (nil = all)
	ExcludeFiles []string       // exclude these path globs
	// FileTagMembers tracks fileIDs currently matching this entry's
	// Predicate. Populated/maintained only when Kind == TagSubFile.
	// In-memory only — re-seeded on server restart via the normal
	// indexing path. R2463, R2469
	FileTagMembers map[uint64]bool
	Hits           atomic.Uint64 // R819: events successfully enqueued
	Drops          atomic.Uint64 // R819: events lost to full queue
}

// Event is a notification produced by Publish.
type Event struct {
	Tag   string
	Value string
	Path  string
	Time  time.Time
}

// PubSub manages tag subscriptions and notification delivery.
// CRC: crc-PubSub.md | R778, R799, R800, R801, R802, R803, R804, R2276, R2279
type PubSub struct {
	subs        map[string][]*TagSub
	queues      map[string]chan Event
	lastListen  map[string]time.Time
	mu          sync.RWMutex
	ttl         time.Duration
	queueDepth  int
	db          *DB                          // for watchdog tmp:// writes (nil = watchdog disabled)
	tagSetCache map[string]map[TagValue]bool // R2283: path → last-published tag-set, used by PublishTmpDiff
	subChanged  chan struct{}                // R2857: close+replace broadcast fired when a subscription is added
}

// NewPubSub creates a PubSub with the given TTL and queue depth.
func NewPubSub(ttl time.Duration, queueDepth int) *PubSub {
	return &PubSub{
		subs:        make(map[string][]*TagSub),
		queues:      make(map[string]chan Event),
		lastListen:  make(map[string]time.Time),
		ttl:         ttl,
		queueDepth:  queueDepth,
		tagSetCache: make(map[string]map[TagValue]bool),
		subChanged:  make(chan struct{}),
	}
}

// SetDB gives pubsub access to the database for watchdog tmp:// writes.
func (ps *PubSub) SetDB(db *DB) {
	ps.db = db
}

// PublishAndWatch calls Publish, then runs Watchdog on unmatched tags and
// persists findings to tmp:// files. Called from the indexer.
func (ps *PubSub) PublishAndWatch(writerID string, path string, tags []TagValue) {
	unmatched := ps.Publish(writerID, path, tags)
	if len(unmatched) == 0 || ps.db == nil {
		return
	}
	results := ps.Watchdog(unmatched, path)
	for _, r := range results {
		var tmpPath string
		var line string
		switch r.Kind {
		case "orphan-schedule":
			tmpPath = "tmp://watchdog/orphan-schedules"
			line = fmt.Sprintf("@watchdog: orphan-schedule @%s: %s in %s\n", r.Tag, r.Value, r.Path)
		case "possible-typo":
			tmpPath = "tmp://watchdog/possible-typos"
			line = fmt.Sprintf("@watchdog: possible-typo @%s: in %s (similar to @%s:, score %.2f)\n", r.Tag, r.Path, r.Similar, r.Score)
		default:
			continue
		}
		ps.db.AppendTmpFile(tmpPath, "markdown", []byte(line))
	}
}

// PruneWatchdog removes stale entries from the watchdog tmp docs:
//
//   - `tmp://watchdog/orphan-schedules`: drop lines whose `@TAG:`
//     is now declared as a schedule tag (the user belatedly added
//     a `[schedule.tag.X]` block).
//   - `tmp://watchdog/possible-typos`: drop lines whose `@TAG:` is
//     now subscribed (active session matches the formerly-typo'd
//     name).
//
// Called from the watch loop on config-reload settle so editing
// `ark.toml` to declare a previously-orphan tag clears its stale
// watchdog warnings. Cheap (one read + filter + write per doc) and
// only runs on the rare config-change path.
func (ps *PubSub) PruneWatchdog(cfg *Config) {
	if ps.db == nil || cfg == nil {
		return
	}
	ps.pruneWatchdogDoc("tmp://watchdog/orphan-schedules", func(tag string) bool {
		return cfg.IsScheduleTag(tag)
	})

	ps.mu.RLock()
	subscribed := ps.subscribedTagSet()
	ps.mu.RUnlock()
	ps.pruneWatchdogDoc("tmp://watchdog/possible-typos", func(tag string) bool {
		return subscribed[tag]
	})
}

// pruneWatchdogDoc walks a watchdog tmp doc line-by-line, drops any
// line whose `@watchdog:` payload references a tag for which `now`
// returns true ("this tag is no longer worth warning about"), and
// rewrites the doc via UpdateTmpFile.
func (ps *PubSub) pruneWatchdogDoc(tmpPath string, now func(tag string) bool) {
	content, err := ps.db.TmpContent(tmpPath)
	if err != nil || len(content) == 0 {
		return
	}
	lines := strings.Split(string(content), "\n")
	kept := make([]string, 0, len(lines))
	dropped := 0
	for _, line := range lines {
		if line == "" {
			kept = append(kept, line)
			continue
		}
		tag := extractWatchdogTag(line)
		if tag != "" && now(tag) {
			dropped++
			continue
		}
		kept = append(kept, line)
	}
	if dropped == 0 {
		return
	}
	newContent := strings.Join(kept, "\n")
	if err := ps.db.UpdateTmpFile(tmpPath, "markdown", []byte(newContent)); err != nil {
		log.Printf("watchdog prune: UpdateTmpFile %s: %v", tmpPath, err)
		return
	}
	log.Printf("watchdog prune: dropped %d stale entry/entries from %s", dropped, tmpPath)
}

// extractWatchdogTag parses the tag name out of a watchdog log line.
// Lines look like:
//
//	@watchdog: orphan-schedule @standup: every Monday at 09:00 in /path
//	@watchdog: possible-typo @standpu: in /path (similar to @standup:, score 0.71)
//
// Returns "" if the line doesn't match the expected shape.
func extractWatchdogTag(line string) string {
	const prefix = "@watchdog: "
	if !strings.HasPrefix(line, prefix) {
		return ""
	}
	rest := line[len(prefix):]
	at := strings.Index(rest, "@")
	if at < 0 {
		return ""
	}
	colon := strings.Index(rest[at:], ":")
	if colon < 0 {
		return ""
	}
	return rest[at+1 : at+colon]
}

// Subscribe adds subscriptions for a session. R778, R781, R782, R783, R784, R2457, R2459
func (ps *PubSub) Subscribe(sessionID string, subs []*TagSub) {
	ps.mu.Lock()
	defer ps.mu.Unlock()
	if _, ok := ps.queues[sessionID]; !ok {
		ps.queues[sessionID] = make(chan Event, ps.queueDepth)
	}
	ps.subs[sessionID] = append(ps.subs[sessionID], subs...)
	ps.lastListen[sessionID] = time.Now()
	// Wake anyone gated on subscriber presence — e.g. a recall `next`
	// blocked because a pending curation doc's session had no result
	// subscriber. close+replace broadcasts to all current waiters. R2857
	close(ps.subChanged)
	ps.subChanged = make(chan struct{})
}

// SubChanged returns a channel closed when the next subscription is
// added (a close+replace broadcast). A caller gated on subscriber
// presence — recall `next` — selects on it to wake immediately instead
// of polling. R2857
func (ps *PubSub) SubChanged() <-chan struct{} {
	ps.mu.RLock()
	defer ps.mu.RUnlock()
	return ps.subChanged
}

// QueueChan returns a session's event queue (nil when it has none), so a
// caller can select on it directly rather than via the blocking Listen.
// R2857
func (ps *PubSub) QueueChan(sessionID string) <-chan Event {
	ps.mu.RLock()
	defer ps.mu.RUnlock()
	return ps.queues[sessionID]
}

// TouchListen refreshes a session's last-listen time. A caller that
// waits via its own select (rather than Listen) calls this each cycle so
// Reap doesn't drop its subscription. R803, R2857
func (ps *PubSub) TouchListen(sessionID string) {
	ps.mu.Lock()
	defer ps.mu.Unlock()
	if _, ok := ps.lastListen[sessionID]; ok {
		ps.lastListen[sessionID] = time.Now()
	}
}

// Cancel removes subscriptions. R786, R787, R2458
// Empty tag cancels all entries for the session. Non-empty tag
// drops every entry whose Predicate accepts the lowered name and
// (when a value is supplied) the value. The cancel target is
// described in plain strings — callers parse the user's sigil arg
// once and forward the (name, value) fields.
func (ps *PubSub) Cancel(sessionID string, tag string, value string) {
	ps.mu.Lock()
	defer ps.mu.Unlock()
	if tag == "" {
		// Cancel all
		delete(ps.subs, sessionID)
		if ch, ok := ps.queues[sessionID]; ok {
			close(ch)
			delete(ps.queues, sessionID)
		}
		delete(ps.lastListen, sessionID)
		return
	}
	existing := ps.subs[sessionID]
	var kept []*TagSub
	for _, s := range existing {
		if !s.Predicate.MatchName(tag) {
			kept = append(kept, s)
			continue
		}
		if value != "" && !s.Predicate.MatchValue(value) {
			kept = append(kept, s)
			continue
		}
		// Drop this sub
	}
	ps.subs[sessionID] = kept
}

// Publish checks extracted tags against subscriptions and enqueues events.
// writerID is excluded from self-notification (empty = no exclusion). R795, R796, R797, R798
// Returns unmatched tags for watchdog processing.
// If the file contains @mute: true, all events from it are silenced.
//
// Two passes (R2462):
//   - Tag-kind (default): one event per matching (tag, value).
//   - File-tag: one event per indexed chunk when the file's
//     aggregate tags satisfy the predicate (entry/continued/exit).
//
// The file-tag pass runs even when tags is empty so a content-only
// change on a member file still fires (R2466).
func (ps *PubSub) Publish(writerID string, path string, tags []TagValue) []TagValue {
	// Check for @mute: true — silences all events from this file
	for _, tv := range tags {
		if tv.Tag == "mute" && strings.TrimSpace(strings.ToLower(tv.Value)) == "true" {
			return nil
		}
	}
	ps.mu.RLock()
	defer ps.mu.RUnlock()
	now := time.Now()
	var unmatched []TagValue
	for _, tv := range tags {
		matched := false
		// Broadcast across all matching sessions — each session has its
		// own queue and is an independent subscriber. Used to short-
		// circuit after the first session match, which silently
		// prevented "tests as alternative subscribers" alongside the
		// running UI (and admin observers alongside their target
		// session). R2276, R2293.
		for sid, subs := range ps.subs {
			if sid == writerID {
				continue // R798: no self-notification
			}
			ch := ps.queues[sid]
			if ch == nil {
				continue
			}
			for _, sub := range subs {
				if sub.Kind != TagSubChunk {
					continue // R2462: file-tag subs go through the file-tag pass
				}
				if !sub.Predicate.Match(tv) {
					continue
				}
				if !matchFileFilters(path, sub.FilterFiles, sub.ExcludeFiles) {
					continue
				}
				// Non-blocking send — drop if full. R801, R819
				evt := Event{Tag: tv.Tag, Value: tv.Value, Path: path, Time: now}
				select {
				case ch <- evt:
					sub.Hits.Add(1)
				default:
					sub.Drops.Add(1)
				}
				matched = true
				break // one notification per (tag) within a session, not per sub
			}
			// continue to the next session — broadcast to all
			// matching sessions, not just the first (R2276, R2293).
		}
		if !matched {
			unmatched = append(unmatched, tv)
		}
	}

	// Pass 2: file-tag subscriptions (R2460–R2471). For each session
	// with at least one file-tag sub, resolve the path's fileID once
	// (lazy — most publishes have no file-tag subs at all). Then per
	// sub, compare the cached membership against an authoritative
	// read of the file's aggregate tag set and act on the transition.
	if ps.anyFileTagSubLocked() {
		ps.publishFileTagPass(writerID, path, now)
	}
	return unmatched
}

// anyFileTagSubLocked reports whether any session has at least one
// TagSubFile entry. Must hold at least RLock. Cheap early-out so the
// per-path fileID lookup is skipped for the common case where no
// file-tag subscriptions exist.
func (ps *PubSub) anyFileTagSubLocked() bool {
	for _, subs := range ps.subs {
		for _, s := range subs {
			if s.Kind == TagSubFile {
				return true
			}
		}
	}
	return false
}

// publishFileTagPass runs the membership transition logic across all
// TagSubFile entries for the path just indexed. Caller holds RLock;
// FileTagMembers maps are mutated under it because publish is
// single-threaded per PubSub instance (no concurrent Publish calls
// hit the same entry).
// CRC: crc-PubSub.md | Seq: seq-pubsub.md | R2460, R2461, R2462, R2463, R2464, R2469, R2470, R2471, R2472
func (ps *PubSub) publishFileTagPass(writerID, path string, now time.Time) {
	if ps.db == nil {
		return
	}
	fileID, ok := ps.db.PathFileID(path)
	if !ok || fileID == 0 {
		return
	}
	store := ps.db.Store()
	if store == nil {
		return
	}
	for sid, subs := range ps.subs {
		if sid == writerID {
			continue // R2471: no self-notification
		}
		ch := ps.queues[sid]
		if ch == nil {
			continue
		}
		for _, sub := range subs {
			if sub.Kind != TagSubFile {
				continue
			}
			if !matchFileFilters(path, sub.FilterFiles, sub.ExcludeFiles) {
				continue
			}
			wasMember := false
			if sub.FileTagMembers != nil {
				wasMember = sub.FileTagMembers[fileID]
			}
			isMember := store.FileMatchesPredicate(fileID, sub.Predicate)
			// CRC: crc-PubSub.md | R2465, R2466, R2467, R2468
			switch {
			case !wasMember && !isMember:
				continue // R2468 — no-op transition
			case !wasMember && isMember:
				// R2465 — entry: track fileID and deliver chunk.
				if sub.FileTagMembers == nil {
					sub.FileTagMembers = make(map[uint64]bool)
				}
				sub.FileTagMembers[fileID] = true
			case wasMember && !isMember:
				// R2467 — exit: untrack fileID, still deliver this chunk.
				delete(sub.FileTagMembers, fileID)
				// R2466 — was=Y is=Y: continued; no membership change.
			}
			evt := Event{
				Tag:   sub.Predicate.Canonical(),
				Value: "",
				Path:  path,
				Time:  now,
			}
			select {
			case ch <- evt:
				sub.Hits.Add(1)
			default:
				sub.Drops.Add(1)
			}
		}
	}
}

// PublishTmpDiff is the centralized tmp:// publish path. Called from
// db.AddTmpFile / db.UpdateTmpFile / db.AppendTmpFile after the actor
// write commits. Extracts tags from content, diffs against the cached
// prior tag-set for path, and publishes only the (tag, value) pairs
// that are new since the last publish for this path. Updates the
// cache so the next call sees the current state as prior.
//
// AppendTmpFile callers pass the full resulting content (existing +
// appended) so prior tags don't re-fire on each append (R2286).
// AddTmpFile callers see an empty prior set (path's first write), so
// every present tag fires (R2285).
//
// CRC: crc-PubSub.md | Seq: seq-tmp-subscription.md | R2278, R2281, R2283, R2284, R2285, R2286
func (ps *PubSub) PublishTmpDiff(writerID, path string, content []byte, strategy string) {
	tags := ExtractTagValues(content, strategy)

	// Build the new set; collect (tag, value) pairs that weren't in the
	// prior set. Lock once for the read/swap of tagSetCache; do the
	// publish outside the lock to avoid blocking other publishers while
	// fan-out runs.
	next := make(map[TagValue]bool, len(tags))
	for _, tv := range tags {
		next[tv] = true
	}

	ps.mu.Lock()
	prev := ps.tagSetCache[path]
	ps.tagSetCache[path] = next
	ps.mu.Unlock()

	var changed []TagValue
	for tv := range next {
		if !prev[tv] {
			changed = append(changed, tv)
		}
	}
	if len(changed) == 0 {
		return
	}
	ps.PublishAndWatch(writerID, path, changed)
}

// PublishTmpAppend is the append-variant of PublishTmpDiff. Called
// from db.AppendTmpFile after the actor write commits. Extracts tags
// from the appended content and **unions** them into the cache —
// prior tags remain in the cache (so they don't re-fire on the next
// publish for this path), and only newly-introduced (tag, value)
// pairs are published.
//
// Equivalent semantically to "extract tags from the whole resulting
// content and diff against the cache" (R2286), but the union form
// avoids reading the full doc back from the overlay.
//
// CRC: crc-PubSub.md | Seq: seq-tmp-subscription.md | R2281, R2286
func (ps *PubSub) PublishTmpAppend(writerID, path string, appendContent []byte, strategy string) {
	newTags := ExtractTagValues(appendContent, strategy)

	ps.mu.Lock()
	prev := ps.tagSetCache[path]
	if prev == nil {
		prev = make(map[TagValue]bool, len(newTags))
		ps.tagSetCache[path] = prev
	}
	var changed []TagValue
	for _, tv := range newTags {
		if !prev[tv] {
			prev[tv] = true
			changed = append(changed, tv)
		}
	}
	ps.mu.Unlock()

	if len(changed) == 0 {
		return
	}
	ps.PublishAndWatch(writerID, path, changed)
}

// ClearTagSetCache drops the cached tag-set for path. Called from
// db.RemoveTmpFile so the next AddTmpFile on the same path treats it
// as new (prior set empty → every tag fires).
//
// CRC: crc-PubSub.md | R2287
func (ps *PubSub) ClearTagSetCache(path string) {
	ps.mu.Lock()
	delete(ps.tagSetCache, path)
	ps.mu.Unlock()
}

// CompressBatch returns one Event per (path, tag) keeping the latest.
// Used by per-session listening goroutines before dispatching a batch
// into the Lua VM — rapid progress-style tag changes within one batch
// collapse to a single callback-relevant snapshot. Operates on Go
// structs; no Lua-table allocation here (R2296).
//
// CRC: crc-PubSub.md | Seq: seq-tmp-subscription.md | R2295, R2310
func CompressBatch(events []Event) []Event {
	if len(events) <= 1 {
		return events
	}
	type key struct{ path, tag string }
	idx := make(map[key]int, len(events))
	out := make([]Event, 0, len(events))
	for _, e := range events {
		k := key{e.Path, e.Tag}
		if i, ok := idx[k]; ok {
			out[i] = e // replace with latest
			continue
		}
		idx[k] = len(out)
		out = append(out, e)
	}
	return out
}

// QueueDepth returns the current event-queue length for sessionID.
// Monitor read API — non-blocking inspection of how many events are
// queued behind a (possibly slow) subscriber.
//
// CRC: crc-PubSub.md | R2303
func (ps *PubSub) QueueDepth(sessionID string) int {
	ps.mu.RLock()
	defer ps.mu.RUnlock()
	return len(ps.queues[sessionID])
}

// LastListenAt returns the timestamp of the session's most recent
// Listen drain. Zero time if the session has no record. Monitor read
// API — lets a watcher flag sessions whose drain has stalled.
//
// CRC: crc-PubSub.md | R2304
func (ps *PubSub) LastListenAt(sessionID string) time.Time {
	ps.mu.RLock()
	defer ps.mu.RUnlock()
	return ps.lastListen[sessionID]
}

// SubCount returns the number of active subscriptions for sessionID.
// Used by the Lua-bridge lifecycle code to decide when to stop the
// per-session listening goroutine.
//
// CRC: crc-PubSub.md | R2300
func (ps *PubSub) SubCount(sessionID string) int {
	ps.mu.RLock()
	defer ps.mu.RUnlock()
	return len(ps.subs[sessionID])
}

// SubscriberCount returns the number of currently-registered subscriptions
// whose predicate would accept the (tagName, tagValue) pair if it were
// published right now. Per-subscription file filters are intentionally
// ignored — the query answers "could anyone receive this?", not "would
// this specific file pass each subscriber's filter?". Only Kind ==
// TagSubChunk subs are counted (FileTag subs depend on per-file state
// the query has no path for).
//
// CRC: crc-PubSub.md | R2802, R2803, R2804
func (ps *PubSub) SubscriberCount(tagName, tagValue string) int {
	tv := TagValue{Tag: tagName, Value: tagValue}
	ps.mu.RLock()
	defer ps.mu.RUnlock()
	count := 0
	for _, subs := range ps.subs {
		for _, sub := range subs {
			if sub.Kind != TagSubChunk {
				continue
			}
			if sub.Predicate.Match(tv) {
				count++
			}
		}
	}
	return count
}

// TagValue is a tag name + value pair for Publish.
type TagValue struct {
	Tag   string
	Value string
}

// Listen blocks until events are available or timeout. R789, R790, R794
func (ps *PubSub) Listen(sessionID string, timeout time.Duration) []Event {
	ps.mu.RLock()
	ch := ps.queues[sessionID]
	ps.mu.RUnlock()
	if ch == nil {
		return nil // no subscriptions — return immediately
	}

	timer := time.NewTimer(timeout)
	defer timer.Stop()

	var events []Event
	// Block until first event or timeout
	select {
	case evt, ok := <-ch:
		if !ok {
			return nil
		}
		events = append(events, evt)
	case <-timer.C:
		ps.mu.Lock()
		ps.lastListen[sessionID] = time.Now()
		ps.mu.Unlock()
		return nil
	}

	// Drain remaining without blocking
	for {
		select {
		case evt, ok := <-ch:
			if !ok {
				goto done
			}
			events = append(events, evt)
		default:
			goto done
		}
	}
done:
	ps.mu.Lock()
	ps.lastListen[sessionID] = time.Now() // R803
	ps.mu.Unlock()
	return events
}

// FormatMarkdown renders events as crank-handle markdown. R791, R792, R793
func FormatMarkdown(events []Event) string {
	var b strings.Builder
	for i, evt := range events {
		if i > 0 {
			b.WriteString("\n---\n\n")
		}
		b.WriteString(fmt.Sprintf("## @%s: %s\n\n", evt.Tag, evt.Value))
		b.WriteString(fmt.Sprintf("File: %s\n", evt.Path))
		b.WriteString(fmt.Sprintf("Time: %s\n\n", evt.Time.Format(time.RFC3339)))
		b.WriteString(fmt.Sprintf("To read the full file:\n  ~/.ark/ark fetch --wrap knowledge %s\n", evt.Path))
	}
	return b.String()
}

// SubInfo describes a subscription for listing. R814, R815, R816, R2458
// Tag carries the canonical sigil form of the entry's predicate so
// the user can copy it back into a cancel command. Kind is "tag" or
// "file-tag" (R2462).
type SubInfo struct {
	SessionID    string
	Tag          string
	Kind         string
	FilterFiles  []string
	ExcludeFiles []string
	Hits         uint64
	Drops        uint64
}

func tagSubKindLabel(k TagSubKind) string {
	if k == TagSubFile {
		return "file-tag"
	}
	return "tag"
}

// List returns subscription details. Empty sessionID returns all. R814, R815, R816
func (ps *PubSub) List(sessionID string) []SubInfo {
	ps.mu.RLock()
	defer ps.mu.RUnlock()
	var result []SubInfo
	for sid, subs := range ps.subs {
		if sessionID != "" && sid != sessionID {
			continue
		}
		for _, s := range subs {
			info := SubInfo{
				SessionID:    sid,
				Tag:          s.Predicate.Canonical(),
				Kind:         tagSubKindLabel(s.Kind),
				FilterFiles:  s.FilterFiles,
				ExcludeFiles: s.ExcludeFiles,
				Hits:         s.Hits.Load(),
				Drops:        s.Drops.Load(),
			}
			result = append(result, info)
		}
	}
	return result
}

// SubStats is aggregate stats for a session. R817, R818, R820
type SubStats struct {
	SessionID string
	SubCount  int
	Hits      uint64
	Drops     uint64
}

// Stats returns aggregate hit/drop counts. Empty sessionID returns all. R817, R818
func (ps *PubSub) Stats(sessionID string) []SubStats {
	ps.mu.RLock()
	defer ps.mu.RUnlock()
	var result []SubStats
	for sid, subs := range ps.subs {
		if sessionID != "" && sid != sessionID {
			continue
		}
		st := SubStats{SessionID: sid, SubCount: len(subs)}
		for _, s := range subs {
			st.Hits += s.Hits.Load()
			st.Drops += s.Drops.Load()
		}
		result = append(result, st)
	}
	return result
}

// Reap drops sessions that haven't listened within the TTL. R802, R804
func (ps *PubSub) Reap() {
	ps.mu.Lock()
	defer ps.mu.Unlock()
	now := time.Now()
	for sid, last := range ps.lastListen {
		if now.Sub(last) > ps.ttl {
			if ch, ok := ps.queues[sid]; ok {
				close(ch)
				delete(ps.queues, sid)
			}
			delete(ps.subs, sid)
			delete(ps.lastListen, sid)
		}
	}
}

// WatchdogResult is a finding from the unsubscribed tag watchdog.
type WatchdogResult struct {
	Kind    string // "orphan-schedule" or "possible-typo"
	Tag     string
	Value   string
	Path    string
	Similar string  // for typos: the subscribed tag it's close to
	Score   float64 // trigram similarity score
}

// Watchdog checks recently published tags that matched no subscription.
// Finds schedulable orphans and near-miss typos. Called periodically.
func (ps *PubSub) Watchdog(recentTags []TagValue, path string) []WatchdogResult {
	ps.mu.RLock()
	subscribedTags := ps.subscribedTagSet()
	ps.mu.RUnlock()

	var results []WatchdogResult
	for _, tv := range recentTags {
		if subscribedTags[tv.Tag] {
			continue // matched a subscription, not interesting
		}
		// Check for schedulable orphan: does the value look like a date/recurrence?
		if looksSchedulable(tv.Value) {
			results = append(results, WatchdogResult{
				Kind:  "orphan-schedule",
				Tag:   tv.Tag,
				Value: tv.Value,
				Path:  path,
			})
		}
		// Check for typo: is the tag name close to any subscribed tag?
		for subTag := range subscribedTags {
			score := trigramSimilarity(tv.Tag, subTag)
			if score >= 0.4 && tv.Tag != subTag { // 40% overlap threshold
				results = append(results, WatchdogResult{
					Kind:    "possible-typo",
					Tag:     tv.Tag,
					Value:   tv.Value,
					Path:    path,
					Similar: subTag,
					Score:   score,
				})
			}
		}
	}
	return results
}

// subscribedTagSet returns the set of all tag names with active
// subscriptions. Used by the watchdog to short-circuit unmatched
// reports that actually correspond to a regex/contains subscription
// whose name string differs from the literal extracted tag.
// Must hold at least RLock.
func (ps *PubSub) subscribedTagSet() map[string]bool {
	tags := make(map[string]bool)
	for _, subs := range ps.subs {
		for _, s := range subs {
			tags[s.Predicate.NameStr] = true
		}
	}
	return tags
}

// trigramSimilarity computes the Jaccard similarity of two strings' trigram sets.
// Uses microfts2's trigram engine for correct UTF-8 handling (CJK, emoji, etc).
// Returns 0.0 (no overlap) to 1.0 (identical trigrams).
var trigramEngine = microfts2.NewTrigrams(true, nil)

func trigramSimilarity(a, b string) float64 {
	ta := trigramSet(trigramEngine.ExtractTrigrams([]byte(a)))
	tb := trigramSet(trigramEngine.ExtractTrigrams([]byte(b)))
	if len(ta) == 0 || len(tb) == 0 {
		return 0
	}
	intersection := 0
	for tri := range ta {
		if tb[tri] {
			intersection++
		}
	}
	union := len(ta) + len(tb) - intersection
	if union == 0 {
		return 0
	}
	return float64(intersection) / float64(union)
}

func trigramSet(trigrams []uint32) map[uint32]bool {
	if len(trigrams) == 0 {
		return nil
	}
	s := make(map[uint32]bool, len(trigrams))
	for _, t := range trigrams {
		s[t] = true
	}
	return s
}

// looksSchedulable returns true if a tag value appears to contain a date or recurrence spec.
func looksSchedulable(value string) bool {
	v := strings.ToLower(strings.TrimSpace(value))
	if v == "" {
		return false
	}
	// Date patterns
	if len(v) >= 10 && (v[4] == '-' || v[4] == '/') && (v[7] == '-' || v[7] == '/') {
		return true // YYYY-MM-DD or YYYY/MM/DD
	}
	if len(v) >= 5 && (v[2] == '-' || v[2] == '/') {
		return true // MM-DD or MM/DD
	}
	// Recurrence keywords
	if strings.HasPrefix(v, "every ") {
		return true
	}
	return false
}

// matchFileFilters checks path against filter and except globs. R782, R783, R784, R785
func matchFileFilters(path string, filterFiles, exceptFiles []string) bool {
	if len(filterFiles) > 0 {
		matched := false
		for _, pattern := range filterFiles {
			if ok, _ := doublestar.Match(anchorGlob(pattern), path); ok {
				matched = true
				break
			}
		}
		if !matched {
			return false
		}
	}
	for _, pattern := range exceptFiles {
		if ok, _ := doublestar.Match(anchorGlob(pattern), path); ok {
			return false
		}
	}
	return true
}

// anchorGlob prepends **/ to unanchored patterns so *.md matches foo/bar/notes.md.
// Same convention as Matcher in match.go.
func anchorGlob(pattern string) string {
	if !strings.Contains(pattern, "/") {
		return "**/" + pattern
	}
	return pattern
}
