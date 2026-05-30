package ark

// CRC: crc-RecallWatcher.md | Seq: seq-recall-watcher.md | R2687, R2688, R2689, R2690, R2692, R2693, R2695, R2696, R2698, R2705, R2706, R2708, R2711, R2712, R2713, R2714, R2715, R2728, R2729, R2730, R2731, R2732, R2733, R2734, R2735, R2736, R2739, R2740, R2741, R2746, R2747, R2748, R2749, R2752, R2753

import (
	"bytes"
	"encoding/json"
	"log"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"time"

	"github.com/zot/microfts2"
)

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

	mu          sync.Mutex
	sessions    map[string]*recallSessionState // R2730
	fireCounter uint64                         // R2752: per-server monotonic; allocated on timer expiry
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
		db:        db,
		librarian: lib,
		store:     store,
		builder:   builder,
		jobs:      make(chan func(), 16),
		sessions:  make(map[string]*recallSessionState),
	}
}

// Start begins the closure-actor worker goroutine.
func (w *RecallWatcher) Start() {
	if w == nil {
		return
	}
	runSvc(w.jobs)
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

// isGenuineUserMessage reports whether a `type:"user"` record is a real
// human message rather than a tool-result or a harness-injected line.
// Two structural tells, both from the Claude Code JSONL (R2732):
//   - genuine prose has STRING content; tool-results are arrays of
//     `{type:"tool_result", ...}` parts.
//   - the harness stamps injected user-records (e.g. background-task
//     completions) with an `origin.kind` ("task-notification"); a typed
//     message has no `origin`.
//
// A notification's content can itself be a string, so the origin check
// is what excludes it; tool-results are excluded by the array check.
func isGenuineUserMessage(originKind string, content json.RawMessage) bool {
	if originKind != "" {
		return false // harness-injected (task-notification, etc.)
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

	w.mu.Lock()
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
	w.mu.Unlock()
}

// fire is the timer-expiry callback. Runs on the watcher's
// closure-actor goroutine so concurrent fires across sessions
// serialize.
// CRC: crc-RecallWatcher.md | Seq: seq-subscriber-presence.md | R2735, R2752, R2753, R2806, R2808
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
	w.fireCounter++
	fire := w.fireCounter
	w.mu.Unlock()

	if len(snapshot) == 0 {
		return
	}

	// R2806: subscriber-presence gate. Skip the substrate call entirely
	// when nobody is listening for this session's curation events; the
	// downstream agent cost and disk write would be wasted.
	if w.db != nil && w.db.pubsub != nil {
		if w.db.pubsub.SubscriberCount("ark-recall-curate", sessionID) == 0 {
			Logv(2, "recall-watcher: no curate subscriber session=%s fire=%d pending=%d",
				sessionID, fire, len(snapshot))
			if w.builder != nil {
				// R2808: outcome="no-subscriber" goes into the monitor log so
				// `ark monitor` can surface skip rate.
				w.builder.appendMonitor(fire, sessionID, 0, 0, 0, 0, 0, 0, 0, "no-subscriber")
			}
			return
		}
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
			// Capture each chunk's full byte size before truncating
			// the Content excerpt so the curation doc can stamp it on
			// the Candidate H2 (R2749 size column).
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
	// side via the CLI verbs. R2747, R2748, R2749, R2753, R2754
	cb := w.builder.RecallCurationOpen(sessionID, fire)
	for _, sec := range sections {
		cb.Section(sec.sourceChunkID, sec.inputExcerpt)
		for i, ch := range sec.recalled {
			tagNames := make([]string, 0, len(ch.Tags))
			for _, t := range ch.Tags {
				tagNames = append(tagNames, t.Tag)
			}
			pNames, pScores := ch.ProposedTags, ch.ProposedTagScores
			cb.Candidate(ch.ChunkID, ch.Path, ch.Range, sec.sizes[i],
				ch.Score, tagNames, pNames, pScores, ch.Content)
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

// recallSection captures one paragraph's recall result, ready for
// rendering into the curation doc. R2749
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
