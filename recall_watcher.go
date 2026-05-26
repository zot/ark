package ark

// CRC: crc-RecallWatcher.md | Seq: seq-recall-watcher.md | R2687, R2688, R2689, R2690, R2692, R2693, R2694, R2695, R2696, R2698, R2700, R2701, R2702, R2703, R2704, R2705, R2706, R2707, R2708, R2709, R2710, R2711, R2712, R2713, R2714, R2715, R2728, R2729, R2730, R2731, R2732, R2733, R2734, R2735, R2736, R2737, R2738, R2739, R2740, R2741

import (
	"bytes"
	"encoding/json"
	"fmt"
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

	jobs chan func()

	mu       sync.Mutex
	sessions map[string]*recallSessionState // R2730
}

// recallSessionState carries the per-session accumulator and timer.
// R2730
type recallSessionState struct {
	pendingChunks           []uint64
	pendingTimer            *time.Timer
	lastTurnDurationChunkID uint64 // 0 when unknown; ts is used in @ark-recall-fire
}

// NewRecallWatcher constructs a watcher. The worker goroutine
// starts when Start is called. R2687
func NewRecallWatcher(db *DB, lib *Librarian, store *Store) *RecallWatcher {
	return &RecallWatcher{
		db:        db,
		librarian: lib,
		store:     store,
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
			st.lastTurnDurationChunkID = 0
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
		st.lastTurnDurationChunkID = 0
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
		}
		if err := json.Unmarshal(line, &rec); err != nil {
			continue // unparseable line — skip silently (could be a partial trailing line)
		}
		switch {
		case rec.Type == "user":
			sigs = append(sigs, signalUser)
		case rec.Type == "system" && rec.Subtype == "turn_duration":
			sigs = append(sigs, signalTurnDuration)
		}
	}
	return sigs
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
		case signalTurnDuration:
			if st.pendingTimer != nil {
				st.pendingTimer.Stop()
			}
			// Record the most recent indexed chunk as the
			// turn_duration anchor (best-effort: when the
			// chunker indexed the turn_duration line itself,
			// it's the last entry in `added`; otherwise we
			// fall back to 0 and the body uses a timestamp).
			if len(added) > 0 {
				st.lastTurnDurationChunkID = added[len(added)-1]
			}
			delay := time.Duration(w.config().EffectiveActivationDelay()) * time.Second
			sid := sessionID
			st.pendingTimer = time.AfterFunc(delay, func() {
				svc(w.jobs, func() { w.fire(sid) })
			})
			Logv(2, "recall-watcher: armed session=%s pending=%d delay=%ds",
				sessionID, len(st.pendingChunks),
				w.config().EffectiveActivationDelay())
		}
	}
	w.mu.Unlock()
}

// fire is the timer-expiry callback. Runs on the watcher's
// closure-actor goroutine so concurrent fires across sessions
// serialize. R2735
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
	fireRef := st.lastTurnDurationChunkID
	w.mu.Unlock()

	if len(snapshot) == 0 {
		return
	}

	cfg := w.config()
	sections := make([]recallSection, 0, len(snapshot))
	totalRecalled := 0
	dropped := 0
	skipped := 0
	for _, cid := range snapshot {
		text, err := w.db.ChunkTextByID(cid)
		if err != nil {
			log.Printf("recall-watcher: ChunkTextByID session=%s cid=%d: %v", sessionID, cid, err)
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
			for i := range result.Chunks {
				result.Chunks[i].Content = truncateUTF8(result.Chunks[i].Content, 500) // R2705
			}
			sections = append(sections, recallSection{
				sourceChunkID: cid,
				inputExcerpt:  truncateUTF8(para, 200), // R2738
				recalled:      result.Chunks,
			})
			totalRecalled += len(result.Chunks)
		}
	}

	if len(sections) == 0 {
		Logv(2, "recall-watcher: fired session=%s pending=%d sections=0 dropped=%d skipped-short=%d",
			sessionID, len(snapshot), dropped, skipped)
		return
	}

	ref := w.fireRef(fireRef)
	body := composeRecallBody(ref, sections)
	dmPath, payload, err := ComposeDM(
		DMSender{Service: "ARK-RECALL"},
		[]string{sessionID},
		"recall",
		"",
		body,
	)
	if err != nil {
		log.Printf("recall-watcher: ComposeDM failed: %v", err)
		return
	}
	if err := SyncVoid(w.db, func(db *DB) error {
		_, e := db.AppendTmpFile(dmPath, "markdown", []byte(payload))
		return e
	}); err != nil {
		log.Printf("recall-watcher: AppendTmpFile failed: %v", err)
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

	Logv(2, "recall-watcher: fired session=%s pending=%d sections=%d dropped=%d skipped-short=%d recalled=%d discussed=%d",
		sessionID, len(snapshot), len(sections), dropped, skipped, totalRecalled, discussedCount)
}

// fireRef returns the @ark-recall-fire value: the chunkID of the
// turn_duration record when known, or a Unix-nanosecond timestamp.
// R2702
func (w *RecallWatcher) fireRef(chunkID uint64) string {
	if chunkID != 0 {
		return fmt.Sprintf("%d", chunkID)
	}
	return fmt.Sprintf("t%d", time.Now().UnixNano())
}

// recallSection captures one paragraph's recall result, ready for
// rendering into the grouped DM body. R2737
type recallSection struct {
	sourceChunkID uint64 // the JSONL chunk the paragraph came from (R2738)
	inputExcerpt  string // the paragraph text itself, capped at ~200 chars
	recalled      []RecalledChunk
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

// composeRecallBody builds the grouped DM body: the @ark-recall-fire
// line, then the instruction block, then one section per paragraph
// that cleared the similarity gate. R2702, R2703, R2704, R2737, R2738
func composeRecallBody(fireRef string, sections []recallSection) string {
	var buf strings.Builder
	fmt.Fprintf(&buf, "@ark-recall-fire: %s\n\n", fireRef)
	buf.WriteString(recallInstructionBlock)
	for _, sec := range sections {
		buf.WriteString("\n## Recalled for paragraph\n")
		fmt.Fprintf(&buf, "@source-chunk: %d\n\n", sec.sourceChunkID)
		if sec.inputExcerpt != "" {
			for _, line := range strings.Split(sec.inputExcerpt, "\n") {
				fmt.Fprintf(&buf, "> %s\n", line)
			}
			buf.WriteString("\n")
		}
		buf.WriteString("### Recalled chunks\n")
		RenderRecallChunks(&buf, sec.recalled)
	}
	return buf.String()
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

// recallInstructionBlock is the default-to-silence crank-handle
// text the watcher prepends to every DM body. R2703, R2707, R2709
const recallInstructionBlock = `## What this is

Ambient recall surfaced by ark. The recent conversation turn
matched material the corpus has touched before. Each
` + "`## Recalled for paragraph`" + ` section below covers one paragraph
of the recent turn: the blockquote is the paragraph that
prompted the recall, ` + "`@source-chunk`" + ` traces back to the
indexed JSONL chunk it came from, and the listed chunks are
what your corpus already has about that topic — with tags,
excerpts, and any statistical tag-candidates the substrate
proposed (RC records — see specs/derived-tags.md).

## What to do with it

**Default to silence.** Most recalls won't fit the
conversation; that's normal. Surface only when the connection
is *clearly* relevant — not just topically adjacent. If you
just surfaced something in the last few turns, weight strongly
against surfacing again. The user doesn't need acknowledgement
that recall happened — drop is fine. No reply needed.

If you do surface, frame the source naturally — "ark recall
surfaces …" or similar — so the user understands this came
from ambient recall, not from your own context.

**Derived-tag candidates.** Each recalled chunk may list
` + "`@chunk-proposed-tags`" + `. These are statistical proposals the
substrate generated against the current tag-definition
landscape; they have no human judgment yet. If a candidate
genuinely fits the chunk's content, accept it via
` + "`ark connections recall accept-derived <chunkID> @<tag>:<value>`" + `
(value picks happen at accept time). Reject obviously bad ones
with ` + "`ark connections recall reject-derived <chunkID> @<tag>`" + `
so they don't resurface. Most should drop silently — only
promote the clear wins.

When you're done with this DM, ` + "`ark remove <path>`" + ` clears it
from your inbox. ` + "`ark search @dm: <self>`" + ` shows pending DMs
you haven't processed.
`
