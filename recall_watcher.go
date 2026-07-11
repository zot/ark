package ark

// CRC: crc-RecallWatcher.md | Seq: seq-recall-watcher.md | R2687, R2688, R2689, R2690, R2692, R2693, R2695, R2696, R2698, R2705, R2706, R2708, R2711, R2712, R2713, R2714, R2715, R2728, R2729, R2730, R2731, R2732, R2733, R2734, R2735, R2736, R2739, R2740, R2741, R2746, R2747, R2748, R2898, R2901, R2753, R2933, R2934, R2935, R2936, R2937

import (
	"bytes"
	"encoding/json"
	"fmt"
	"log"
	"path/filepath"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/zot/microfts2"
)

// bloodhoundRe recognizes a directed-search watermark in assistant output.
// Non-greedy + DOTALL so a multi-line payload is captured whole. R2934
var bloodhoundRe = regexp.MustCompile(`(?s)<BLOODHOUND>(.*?)</BLOODHOUND>`)

// recallMinParagraphBytes is the floor on paragraph length before a
// paragraph earns its own Recall call. Drops one-liners like "yes." /
// "ok" / "done." that produce trigram noise without signal. Set to
// 30 bytes — high enough to skip trivial replies, low enough to keep
// genuine short claims like "the rake persona specializes as a poker
// coach" (45 bytes). R2736
const recallMinParagraphBytes = 30

// RecallWatcher is the simple-recall ambient watcher. It detects
// turn boundaries on Claude Code JSONL sources via the
// `turn_duration` system record, accumulates indexed chunks across
// the turn, and DMs a grouped per-input-chunk recall pass back to
// the originating session when the assistant has been idle for
// `activation_delay` seconds.
type RecallWatcher struct {
	db        *DB
	librarian *Librarian
	store     *Store
	builder   *RecallAgentBuilder // R2754: curation-doc builder factory

	jobs chan func()

	mu           sync.Mutex
	sessions     map[string]*recallSessionState // R2730
	fireCounters map[string]uint64              // R2901: per-session monotonic, dir-seeded on first use; allocated on timer expiry
	// bloodhoundCounters: per-session monotonic <B> for directed-search
	// tasks. In-memory only, no dir-seeding (task docs are ephemeral
	// tmp://), reset on restart — distinct from fireCounters. R2937
	bloodhoundCounters map[string]uint64

	// CLI-bloodhound Fixer state (S2). luhmann is the S1 orchestrator-seat
	// bridge (owner + nextQueue); pool is the pool-secretary roster + the
	// pending-hunt queue. Both nil-safe: the Fixer no-ops without a hub.
	// R3020, R3023, R3024
	luhmann luhmannHub
	pool    *cliPool
}

// recallSessionState carries the per-session accumulator and timer.
// R2730
type recallSessionState struct {
	pendingChunks []uint64
	pendingTimer  *time.Timer
	// armReady gates arming to once per user turn (R2734): a user
	// record sets it; a turn_duration arms only when it is set and then
	// clears it. An agent-only turn (e.g. an assistant surfacing recall
	// with no preceding user record) thus does not re-arm — which is
	// what stops the recall ping-pong cascade.
	armReady bool
}

// NewRecallWatcher constructs a watcher. The worker goroutine
// starts when Start is called. R2687
func NewRecallWatcher(db *DB, lib *Librarian, store *Store, builder *RecallAgentBuilder) *RecallWatcher {
	return &RecallWatcher{
		db:                 db,
		librarian:          lib,
		store:              store,
		builder:            builder,
		jobs:               make(chan func(), 16),
		sessions:           make(map[string]*recallSessionState),
		fireCounters:       make(map[string]uint64),
		bloodhoundCounters: make(map[string]uint64),
		pool:               newCLIPool(),
	}
}

// Start begins the closure-actor worker goroutine and the CLI-bloodhound
// Fixer loop (S2). Wire the luhmann hub before calling Start.
func (w *RecallWatcher) Start() {
	if w == nil {
		return
	}
	runSvc(w.jobs)
	w.startFixer() // R3023, R3024: subscribe + drain @ark-bloodhound-cli[-return]
}

// SetLuhmannHub wires the S1 orchestrator-seat bridge (owner lookup + nextQueue
// producer) into the watcher-as-Fixer. Called by Serve before Start. R3020
func (w *RecallWatcher) SetLuhmannHub(h luhmannHub) {
	if w != nil {
		w.luhmann = h
	}
}

func (w *RecallWatcher) config() RecallConfig {
	if w == nil || w.db == nil {
		return RecallConfig{}
	}
	return w.db.Config().Recall
}

// Enabled reports whether the master switch is on. R2688
func (w *RecallWatcher) Enabled() bool {
	if w == nil {
		return false
	}
	return w.config().Enabled
}

// SourceQualifies reports whether a (path, strategy) pair qualifies
// for ambient recall under the current config. R2696, R2741
// ClearPending drops the watcher's in-memory pendingChunks
// accumulator. With sessions empty, clears every session; otherwise
// clears only the listed session UUIDs. Stops any armed timer for
// affected sessions so a pending fire doesn't process chunks that
// have just been cleared. R2745
func (w *RecallWatcher) ClearPending(sessions []string) {
	if w == nil {
		return
	}
	w.mu.Lock()
	defer w.mu.Unlock()
	if len(sessions) == 0 {
		for _, st := range w.sessions {
			if st.pendingTimer != nil {
				st.pendingTimer.Stop()
				st.pendingTimer = nil
			}
			st.pendingChunks = nil
		}
		return
	}
	for _, sid := range sessions {
		st, ok := w.sessions[sid]
		if !ok {
			continue
		}
		if st.pendingTimer != nil {
			st.pendingTimer.Stop()
			st.pendingTimer = nil
		}
		st.pendingChunks = nil
	}
}

func (w *RecallWatcher) SourceQualifies(path, strategy string) bool {
	if !w.Enabled() {
		return false
	}
	if strategy != "chat-jsonl" {
		return false
	}
	cfg := w.config()
	if len(cfg.Sources) == 0 {
		return true
	}
	if w.db == nil {
		return false
	}
	root, ok := w.db.Config().SourceRootForPath(path)
	if !ok {
		return false
	}
	return slices.Contains(cfg.Sources, root)
}

// sessionFromJSONLPath derives the Claude Code session UUID from
// a JSONL path's basename. R2701
func sessionFromJSONLPath(path string) string {
	base := filepath.Base(path)
	return strings.TrimSuffix(base, filepath.Ext(base))
}

// jsonlSignal categorizes the signals the watcher cares about.
type jsonlSignal int

const (
	signalNone jsonlSignal = iota
	signalTurnDuration
	signalUser
)

// scanNewBytes walks newBytes line-by-line, parses each line as a
// single JSON object, and reports the trigger signals it found in
// the order they appeared. R2731, R2732
func scanNewBytes(newBytes []byte) []jsonlSignal {
	var sigs []jsonlSignal
	for line := range bytesIterLines(newBytes) {
		if len(bytes.TrimSpace(line)) == 0 {
			continue
		}
		var rec struct {
			Type    string `json:"type"`
			Subtype string `json:"subtype"`
			Origin  struct {
				Kind string `json:"kind"`
			} `json:"origin"`
			Message struct {
				Content json.RawMessage `json:"content"`
			} `json:"message"`
		}
		if err := json.Unmarshal(line, &rec); err != nil {
			continue // unparseable line — skip silently (could be a partial trailing line)
		}
		switch {
		case rec.Type == "user":
			// Only a *genuine* human message arms the watcher (R2732).
			// Tool-results and harness-injected lines are also
			// `type:"user"` — counting them lets the watcher re-fire on
			// its own consumers' wake turns (the recall ping-pong).
			if isGenuineUserMessage(rec.Origin.Kind, rec.Message.Content) {
				sigs = append(sigs, signalUser)
			}
		case rec.Type == "system" && rec.Subtype == "turn_duration":
			sigs = append(sigs, signalTurnDuration)
		}
	}
	return sigs
}

// scanBloodhounds walks newBytes for directed-search watermarks in assistant
// output: each `type:"assistant"` line's text (decoded via assistantText) is
// regex-matched for `<BLOODHOUND>…</BLOODHOUND>`, and every capture is one
// payload. Deterministic and once-only by construction — newBytes is the
// newly-appended slice, so a given line is scanned exactly once (two identical
// watermarks are two requests). R2934
func scanBloodhounds(newBytes []byte) []string {
	var payloads []string
	for line := range bytesIterLines(newBytes) {
		if len(bytes.TrimSpace(line)) == 0 {
			continue
		}
		var rec struct {
			Type    string `json:"type"`
			Message struct {
				Content json.RawMessage `json:"content"`
			} `json:"message"`
		}
		if json.Unmarshal(line, &rec) != nil || rec.Type != "assistant" {
			continue
		}
		text := assistantText(rec.Message.Content)
		if text == "" {
			continue
		}
		for _, m := range bloodhoundRe.FindAllStringSubmatch(text, -1) {
			if p := strings.TrimSpace(m[1]); p != "" {
				payloads = append(payloads, p)
			}
		}
	}
	return payloads
}

// isGenuineUserMessage reports whether a `type:"user"` record is a real
// human message rather than a tool-result or a harness-injected line.
// Two structural tells, both from the Claude Code JSONL:
//   - genuine prose has STRING content; tool-results are arrays of
//     `{type:"tool_result", ...}` parts.
//   - a genuine typed turn carries the positive marker
//     `origin.kind == "human"`. Absence of an origin is NOT genuine:
//     tool-results and local-command caveats also lack it, and injected
//     user-records (e.g. background-task completions) carry a different
//     kind like "task-notification". Anthropic doesn't publish this
//     format, so we key on the one known human marker (conservative
//     allowlist); if it changes, arming goes quiet and the -vv
//     "turn_duration ignored" log is the tripwire.
//
// CRC: crc-RecallWatcher.md | R2732, R3009
func isGenuineUserMessage(originKind string, content json.RawMessage) bool {
	if originKind != "human" {
		return false // only a positively-marked human turn arms recall
	}
	c := bytes.TrimSpace(content)
	return len(c) > 0 && c[0] == '"' // JSON string ⇒ genuine prose; '[' ⇒ tool-result
}

// bytesIterLines returns a function that yields each newline-terminated
// line (excluding the terminator). The final un-terminated tail, if
// any, is also yielded so the caller sees partial appends as best
// effort.
func bytesIterLines(b []byte) func(yield func([]byte) bool) {
	return func(yield func([]byte) bool) {
		for {
			i := bytes.IndexByte(b, '\n')
			if i < 0 {
				if len(b) > 0 {
					yield(b)
				}
				return
			}
			if !yield(b[:i]) {
				return
			}
			b = b[i+1:]
		}
	}
}

// secretaryPresent reports whether the secretary (its `next` loop) is
// subscribed to the work tube for this session — the prerequisite for any
// watcher output, since an unsubscribed work tube means no worker to drain it.
// With no pubsub (e.g. tests) it reports true so the watcher tracks normally.
// CRC: crc-RecallWatcher.md | R2948
func (w *RecallWatcher) secretaryPresent(sessionID string) bool {
	if w.db == nil || w.db.pubsub == nil {
		return true
	}
	return w.db.pubsub.SubscriberCount("ark-secretary-work", sessionID) > 0
}

// bloodhoundEnabled gates directed-search recognition/dispatch: the secretary
// present AND the assistant subscribed to the bloodhound-result tag (the level-3
// opt-in). CRC: crc-RecallWatcher.md | R2947
func (w *RecallWatcher) bloodhoundEnabled(sessionID string) bool {
	if w.db == nil || w.db.pubsub == nil {
		return true
	}
	return w.secretaryPresent(sessionID) &&
		w.db.pubsub.SubscriberCount("ark-bloodhound-result", sessionID) > 0
}

// ambientEnabled gates ambient curation (arm/accumulate/fire): the secretary
// present AND the assistant subscribed to the recall-result tag (the level-4
// opt-in). Shared by the OnAppend arming gate and the fire() backstop so the two
// cannot drift. CRC: crc-RecallWatcher.md | R2806, R2949
func (w *RecallWatcher) ambientEnabled(sessionID string) bool {
	if w.db == nil || w.db.pubsub == nil {
		return true
	}
	return w.secretaryPresent(sessionID) &&
		w.db.pubsub.SubscriberCount("ark-recall-result", sessionID) > 0
}

// OnAppend is the indexer-side entry. Called synchronously from
// `executeRefresh`'s isAppend branch. R2729
func (w *RecallWatcher) OnAppend(path, strategy string, newBytes []byte, added []uint64) {
	if w == nil {
		return
	}
	if !w.SourceQualifies(path, strategy) {
		return // R2741
	}
	sessionID := sessionFromJSONLPath(path)
	if sessionID == "" {
		return
	}

	// Per-capability gate (R2947, R2949): drop the session only when the
	// secretary is absent (level ≤2) — no worker, nothing to produce; a later
	// (re)subscription reactivates at the current JSONL end (no backfill). With
	// the secretary present, ambient and bloodhound gate independently on the
	// assistant's per-capability result subs.
	// CRC: crc-RecallWatcher.md | Seq: seq-recall-watcher.md#2.3 | R2868, R2947, R2949
	if !w.secretaryPresent(sessionID) {
		w.mu.Lock()
		if st, ok := w.sessions[sessionID]; ok {
			if st.pendingTimer != nil {
				st.pendingTimer.Stop()
			}
			delete(w.sessions, sessionID)
		}
		w.mu.Unlock()
		return
	}

	w.mu.Lock()
	defer w.mu.Unlock()

	// Ambient (R2949): accumulate + arm only when the recall-result sub is
	// present; otherwise tear down any lingering ambient state so a dropped
	// recall-result doesn't leave an armed timer or stale pendingChunks.
	if !w.ambientEnabled(sessionID) {
		if st, ok := w.sessions[sessionID]; ok {
			if st.pendingTimer != nil {
				st.pendingTimer.Stop()
				st.pendingTimer = nil
			}
			st.pendingChunks = nil
		}
	} else {
		st, ok := w.sessions[sessionID]
		if !ok {
			st = &recallSessionState{}
			w.sessions[sessionID] = st
		}
		if len(added) > 0 {
			st.pendingChunks = append(st.pendingChunks, added...) // R2730
		}

		// Apply each signal in order. R2731, R2732, R2733, R2734
		for _, sig := range scanNewBytes(newBytes) {
			switch sig {
			case signalUser:
				if st.pendingTimer != nil {
					st.pendingTimer.Stop()
					st.pendingTimer = nil
					Logv(2, "recall-watcher: disarmed session=%s pending=%d",
						sessionID, len(st.pendingChunks))
				}
				st.armReady = true // R2733: a user turn (re-)enables arming
			case signalTurnDuration:
				if !st.armReady {
					// R2734: arm only once per user turn. A turn_duration
					// with no intervening user record (an agent-only turn)
					// does not re-arm — this is what stops the ping-pong.
					Logv(2, "recall-watcher: turn_duration ignored, no user turn since last arm session=%s", sessionID)
					break
				}
				if st.pendingTimer != nil {
					st.pendingTimer.Stop()
				}
				delay := time.Duration(w.config().EffectiveActivationDelay()) * time.Second
				sid := sessionID
				st.pendingTimer = time.AfterFunc(delay, func() {
					svc(w.jobs, func() { w.fire(sid) })
				})
				st.armReady = false // R2734: consumed this user turn's single arm
				Logv(2, "recall-watcher: armed session=%s pending=%d delay=%ds",
					sessionID, len(st.pendingChunks),
					w.config().EffectiveActivationDelay())
			}
		}
	}

	// Bloodhound recognition (R2934, R2935, R2936, R2947): scan the same
	// newBytes for directed-search watermarks in assistant output — gated on
	// the bloodhound-result sub, independent of the ambient arm/fire machinery
	// above (it touches no timer/armReady/pendingChunks). For each payload,
	// allocate <B> under the lock and dispatch off the indexer goroutine.
	if w.bloodhoundEnabled(sessionID) {
		for _, payload := range scanBloodhounds(newBytes) {
			bid := w.nextBloodhoundLocked(sessionID)
			p, sid := payload, sessionID
			svc(w.jobs, func() { w.dispatchBloodhound(sid, bid, p) })
		}
	}
}

// fire is the timer-expiry callback. Runs on the watcher's
// closure-actor goroutine so concurrent fires across sessions
// serialize.
// CRC: crc-RecallWatcher.md | Seq: seq-subscriber-presence.md | R2735, R2901, R2753, R2806, R2808
func (w *RecallWatcher) fire(sessionID string) {
	w.mu.Lock()
	st := w.sessions[sessionID]
	if st == nil {
		w.mu.Unlock()
		return
	}
	snapshot := st.pendingChunks
	st.pendingChunks = nil
	st.pendingTimer = nil
	fire := w.nextFireLocked(sessionID)
	w.mu.Unlock()

	if len(snapshot) == 0 {
		return
	}

	// Ambient backstop to the OnAppend arming gate (R2949): re-check the
	// secretary + recall-result subs in case the consumer dropped during
	// activation_delay. Missing → skip the substrate call + disk write;
	// pendingChunks is already cleared (R2735), so the next OnAppend starts
	// fresh.
	// CRC: crc-RecallWatcher.md | Seq: seq-recall-watcher.md#5.4 | R2806, R2949
	if !w.ambientEnabled(sessionID) {
		Logv(2, "recall-watcher: no secretary/recall-result subscriber session=%s fire=%d pending=%d",
			sessionID, fire, len(snapshot))
		if w.builder != nil {
			// R2808: outcome="no-subscriber" goes into the monitor log so
			// `ark monitor` can surface skip rate.
			w.builder.appendMonitor(fire, sessionID, 0, 0, 0, 0, 0, 0, 0, "no-subscriber")
		}
		return
	}

	cfg := w.config()
	sections := make([]recallSection, 0, len(snapshot))
	totalRecalled := 0
	dropped := 0
	skipped := 0
	notFound := 0
	for _, cid := range snapshot {
		text, err := w.db.ChunkTextByID(cid)
		if err != nil {
			// "chunk not found" is an expected transient for live
			// JSONL files mid-rechunk: the file may have been
			// re-indexed between the OnAppend callback and this
			// timer-expiry pass, and the chunkIDs we accumulated
			// were superseded. Skip silently; count for the fired
			// summary so the rate stays visible without flooding
			// the log.
			notFound++
			continue
		}
		// Re-chunk the JSONL chunk's extracted text via the markdown
		// chunker so each paragraph becomes its own Recall input. R2736
		paragraphs := splitParagraphs(text)
		for _, para := range paragraphs {
			if len(para) < recallMinParagraphBytes {
				skipped++
				continue
			}
			result, err := w.librarian.Recall(
				// Text input embeds on the fly via the warm model,
				// bypassing the EC freshness race for just-arrived
				// JSONL chunks. R2736
				//
				// KeepTagless: true so the watcher surfaces persona
				// files, design docs, and other prose-heavy content
				// that lacks @tag: lines. Without it the propose
				// pass still computes RC records for these chunks,
				// but the surfacing filter hides them from the DM —
				// exactly the opposite of what ambient recall
				// wants. R2746
				[]ConnectionsInput{{Text: para}},
				RecallOpts{
					K:              cfg.EffectiveChunksPerDM(),
					IncludeContent: true,
					Session:        sessionID,
					Propose:        cfg.EffectivePropose(),
					KeepTagless:    true,
				},
			)
			if err != nil {
				log.Printf("recall-watcher: Recall failed session=%s source-chunk=%d: %v", sessionID, cid, err)
				continue
			}
			// R2708, R2739
			if len(result.Chunks) == 0 || result.Chunks[0].Score < cfg.EffectiveMinSimilarity() {
				dropped++
				continue
			}
			// R2893: surface-cooldown floor — drop candidates surfaced within
			// surface_cooldown so the secretary judges only novel candidates.
			result.Chunks = w.dropCooledCandidates(sessionID, result.Chunks)
			if len(result.Chunks) == 0 {
				dropped++
				continue
			}
			// Capture each chunk's full byte size before truncating
			// the Content excerpt so the curation doc can stamp it on
			// the Candidate H2 (R2898 size column).
			sizes := make([]int, len(result.Chunks))
			for i := range result.Chunks {
				sizes[i] = len(result.Chunks[i].Content)
				result.Chunks[i].Content = truncateUTF8(result.Chunks[i].Content, 500) // R2705
			}
			sections = append(sections, recallSection{
				sourceChunkID: cid,
				inputExcerpt:  truncateUTF8(para, 200), // R2738
				recalled:      result.Chunks,
				sizes:         sizes,
			})
			totalRecalled += len(result.Chunks)
		}
	}

	if len(sections) == 0 {
		Logv(2, "recall-watcher: fired session=%s fire=%d pending=%d sections=0 dropped=%d skipped-short=%d not-found=%d",
			sessionID, fire, len(snapshot), dropped, skipped, notFound)
		return
	}

	// Build the curation doc via the in-process RecallCurationBuilder.
	// The same state machine emits identical body shape on the agent
	// side via the CLI verbs. R2747, R2748, R2898, R2753, R2754
	cb := w.builder.RecallCurationOpen(sessionID, fire)
	for _, sec := range sections {
		// CRC: crc-RecallWatcher.md | Seq: seq-recall-watcher.md#5.7 | R2898
		// The curation doc references chunks by path:range, not chunkid;
		// the watcher resolves the source chunk's path:range at build time.
		srcPath, srcRange := w.sourceLocator(sec.sourceChunkID)
		cb.Section(srcPath, srcRange, sec.inputExcerpt)
		for i, ch := range sec.recalled {
			tagNames := make([]string, 0, len(ch.Tags))
			for _, t := range ch.Tags {
				tagNames = append(tagNames, t.Tag)
			}
			pNames, pScores := ch.ProposedTags, ch.ProposedTagScores
			// Classify by source: a candidate in the originating session's
			// own JSONL is tag-only — recommend, never surface, since the
			// live conversation is already in the reader's context.
			// CRC: crc-RecallWatcher.md | Seq: seq-recall-watcher.md#5.7 | R2869
			tagOnly := sessionFromJSONLPath(ch.Path) == sessionID
			cb.Candidate(ch.Path, ch.Range, sec.sizes[i],
				ch.Score, ch.Cell, ch.PerSubstrate, tagNames, pNames, pScores, ch.Content, tagOnly)
		}
	}
	if err := cb.Close(); err != nil {
		log.Printf("recall-watcher: curation doc write failed: %v", err)
		return
	}

	// Mark-on-send RD writes. R2711, R2712, R2740
	discussedCount := 0
	for _, sec := range sections {
		for _, ch := range sec.recalled {
			for _, t := range ch.Tags {
				if err := w.store.AddDiscussed(sessionID, t.Tag, t.Value); err != nil {
					log.Printf("recall-watcher: AddDiscussed %s=%s: %v", t.Tag, t.Value, err)
					continue
				}
				discussedCount++
			}
		}
	}

	Logv(2, "recall-watcher: fired session=%s fire=%d pending=%d sections=%d dropped=%d skipped-short=%d not-found=%d recalled=%d discussed=%d",
		sessionID, fire, len(snapshot), len(sections), dropped, skipped, notFound, totalRecalled, discussedCount)
}

// nextFireLocked returns the next per-session fire number. Must be
// called with w.mu held. On the first fire for a session this server
// run it seeds the counter from the curation dir (so a restart/rebuild
// never re-mints a number a surviving materialized file occupies), then
// increments in memory — the in-memory hold (not a per-allocation dir
// recompute) is what closes the allocation→materialization race a
// constant offset cannot. CRC: crc-RecallWatcher.md | R2901
func (w *RecallWatcher) nextFireLocked(sessionID string) uint64 {
	if w.fireCounters == nil {
		w.fireCounters = make(map[string]uint64)
	}
	n, seeded := w.fireCounters[sessionID]
	if !seeded {
		n = w.seedFire(sessionID)
	}
	n++
	w.fireCounters[sessionID] = n
	return n
}

// nextBloodhoundLocked returns the next per-session bloodhound id. Must be
// called with w.mu held. In-memory only (task docs are ephemeral tmp://), so
// no dir-seeding — it simply increments. R2937
func (w *RecallWatcher) nextBloodhoundLocked(sessionID string) uint64 {
	if w.bloodhoundCounters == nil {
		w.bloodhoundCounters = make(map[string]uint64)
	}
	w.bloodhoundCounters[sessionID]++
	return w.bloodhoundCounters[sessionID]
}

// bloodhoundSeedK is the candidate count for a directed hunt's Recall seed — a
// focused starting set the weak agent actually reads, not the full ambient
// per-DM count; the agent widens the trail with its own `-k 20` searches. R3006
const bloodhoundSeedK = 10

// dispatchBloodhound runs on the watcher's worker goroutine. It re-checks the
// bloodhound gate (write-time backstop — secretary + bloodhound-result sub,
// R2947), seeds the hunt with the deluxe combined Recall search (R3006, R3007),
// then hands the payload + seed to the builder, which writes the task doc in
// the ARK-BLOODHOUND namespace and retains the clue for the finding header.
// CRC: crc-RecallWatcher.md | Seq: seq-recall-watcher.md | R2937, R2947, R3006, R3007
func (w *RecallWatcher) dispatchBloodhound(sessionID string, bid uint64, payload string) {
	if w.builder == nil || !w.bloodhoundEnabled(sessionID) {
		return
	}
	// Seed the hunt with the hypergraph-aware combined search (renderSeed):
	// clue-only and session-agnostic, so a directed *pull* hunt sees every match
	// with no derivation side-effects. Only Recall reaches the value→chunk tag
	// axis (R2905/R2906) the subagent's content-only `ark search` cannot. A
	// failed seed is not fatal — the empty-seed note still dispatches. R3006, R3007
	seed := w.renderSeed(payload)
	if err := w.builder.RecallBloodhoundOpen(sessionID, bid, payload, seed); err != nil {
		log.Printf("recall-watcher: bloodhound dispatch failed session=%s B=%d: %v", sessionID, bid, err)
		return
	}
	Logv(2, "recall-watcher: bloodhound dispatched session=%s B=%d", sessionID, bid)
}

// renderBloodhoundSeed formats a Recall result as the `## Recall seed` block of
// a bloodhound task doc: one compact locator line per candidate —
// `<path>:<range> (<size>) <score> [tags]` with a short excerpt, no chunkid on
// the wire (the crank handle opens each with `ark chunks --wrap recall <path:range>`). A
// nil/empty result renders the empty-seed note so the task still dispatches. R3006
// CRC: crc-RecallWatcher.md | R3006
func renderBloodhoundSeed(result *RecallResult) string {
	var sb strings.Builder
	sb.WriteString("## Recall seed\n\n")
	if result == nil || len(result.Chunks) == 0 {
		sb.WriteString("_(no corpus matches — start from your own searches)_\n")
		return sb.String()
	}
	sb.WriteString("Strong candidates from the deluxe combined search (4 substrates: meaning + tags — the tag axis your own `ark search` can't reach). READ these first with `ark chunks --wrap recall <path:range>` (clean text, not JSON); run your own searches only to widen or if this is thin.\n\n")
	for _, c := range result.Chunks {
		tags := ""
		if names := recallTagNames(c.Tags); len(names) > 0 {
			tags = " [" + strings.Join(names, ", ") + "]"
		}
		fmt.Fprintf(&sb, "- %s:%s (%s) %.2f%s\n", c.Path, c.Range, friendlySize(len(c.Content)), c.Score, tags)
		if excerpt := seedExcerpt(c.Content); excerpt != "" {
			fmt.Fprintf(&sb, "  > %s\n", excerpt)
		}
	}
	return sb.String()
}

// recallTagNames pulls the tag names (values dropped) from a chunk's tags.
func recallTagNames(tags []RecallTag) []string {
	names := make([]string, 0, len(tags))
	for _, t := range tags {
		names = append(names, t.Tag)
	}
	return names
}

// seedExcerpt returns the first non-blank line of content, truncated for the
// seed's one-line-per-candidate preview.
func seedExcerpt(content string) string {
	for _, line := range strings.Split(content, "\n") {
		if line = strings.TrimSpace(line); line != "" {
			return truncateUTF8(line, 120)
		}
	}
	return ""
}

// seedFire computes the initial per-session fire counter from the
// surviving materialized curation files in ~/.ark/recall-curation/.
// Returns max(fire)+1 when any survive — so the first allocation is
// max+2, skipping a possibly-unmaterialized in-flight doc (one
// secretary ⇒ lag ≤ 1) — else 0, so the first allocation is 1. The
// surviving files are the high-water record (Alibi Stamp); a
// cleanly-closed fire leaves no file, so reusing its number is safe.
// CRC: crc-RecallWatcher.md | R2901
func (w *RecallWatcher) seedFire(sessionID string) uint64 {
	if w.builder == nil {
		return 0
	}
	prefix := "curation-" + sessionID + "-"
	matches, _ := filepath.Glob(filepath.Join(w.builder.curationDir, prefix+"*.md"))
	var max uint64
	for _, m := range matches {
		s := strings.TrimSuffix(strings.TrimPrefix(filepath.Base(m), prefix), ".md")
		if f, err := strconv.ParseUint(s, 10, 64); err == nil && f > max {
			max = f
		}
	}
	if max == 0 {
		return 0
	}
	return max + 1
}

// sourceLocator resolves a source chunk's path:range for the
// `# Source:` heading (R2898). Best-effort: ("", "") when the chunk
// can't be resolved.
func (w *RecallWatcher) sourceLocator(chunkID uint64) (path, rangeLabel string) {
	info, err := w.db.ChunkInfo(chunkID)
	if err != nil {
		return "", ""
	}
	return info.Path, info.Range
}

// dropCooledCandidates filters out recalled chunks whose (session,
// chunk) was surfaced within [recall].surface_cooldown — the
// deterministic surface-cooldown floor that lets the secretary spend
// judgment only on novel candidates. A zero/negative window disables it.
// CRC: crc-RecallWatcher.md | R2893
func (w *RecallWatcher) dropCooledCandidates(sessionID string, chunks []RecalledChunk) []RecalledChunk {
	window, _ := w.config().SurfaceCooldownDuration()
	if window <= 0 || w.store == nil {
		return chunks
	}
	now := time.Now()
	out := chunks[:0]
	for _, ch := range chunks {
		nanos, present, err := w.store.LastSurfaced(sessionID, ch.ChunkID)
		if err == nil && present && now.Sub(time.Unix(0, nanos)) < window {
			continue
		}
		out = append(out, ch)
	}
	return out
}

// recallSection captures one paragraph's recall result, ready for
// rendering into the curation doc. R2898
type recallSection struct {
	sourceChunkID uint64 // the JSONL chunk the paragraph came from
	inputExcerpt  string // the paragraph text itself, capped at ~200 chars
	recalled      []RecalledChunk
	sizes         []int // parallel to recalled; pre-truncation byte sizes
}

// splitParagraphs runs the supplied text through the markdown chunker
// and returns the paragraph contents in order. Empty paragraphs are
// dropped; per-paragraph length filtering happens at the call site
// against `recallMinParagraphBytes`. R2736
func splitParagraphs(text []byte) []string {
	var paras []string
	_ = microfts2.MarkdownChunker{}.Chunks("jsonl-chunk", text, func(c microfts2.Chunk) bool {
		para := strings.TrimSpace(string(c.Content))
		if para != "" {
			paras = append(paras, para)
		}
		return true
	})
	return paras
}

// truncateUTF8 returns s truncated to at most maxBytes, never
// splitting inside a multi-byte rune. R2705, R2738
func truncateUTF8(s string, maxBytes int) string {
	if len(s) <= maxBytes {
		return s
	}
	cut := maxBytes
	for cut > 0 && (s[cut]&0xC0) == 0x80 {
		cut--
	}
	return s[:cut]
}
