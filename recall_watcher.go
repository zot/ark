package ark

// CRC: crc-RecallWatcher.md | Seq: seq-recall-watcher.md | R2687, R2688, R2689, R2690, R2692, R2693, R2695, R2696, R2698, R2705, R2706, R2708, R2711, R2712, R2713, R2714, R2715, R2728, R2729, R2730, R2731, R2732, R2733, R2734, R2735, R2736, R2739, R2740, R2741, R2746, R2747, R2748, R2898, R2901, R2753, R2933, R2934, R2935, R2936, R2937

import (
	"bytes"
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
// Non-greedy + DOTALL so a multi-line payload is captured whole. Group 1 is the
// opening tag's optional attribute run (parsed by parseBloodhoundAttrs), group
// 2 the payload. The leading `\s` in group 1 is load-bearing: without it
// `<BLOODHOUNDER>` would start matching. R2934, R3110, R3184
// bloodhoundCloseTag is the watermark's closing tag; a cheap pre-check before
// any transcript decode (R3624).
const bloodhoundCloseTag = "</BLOODHOUND>"

var bloodhoundRe = regexp.MustCompile(`(?s)<BLOODHOUND(\s[^>]*)?>(.*?)</BLOODHOUND>`)

// bloodhoundAttrRe pulls one `name="value"` attribute out of the opening tag's
// attribute run. Bare attributes (`notags`) carry no `=` and are matched
// separately by word, so the two forms coexist without a grammar. R3184
var bloodhoundAttrRe = regexp.MustCompile(`([a-zA-Z-]+)\s*=\s*"([^"]*)"`)

// recallTagsToggleRe recognizes the self-closing ambient tag-recommend toggle:
// `<RECALL notags/>` suppresses recommends for the session (R3112),
// `<RECALL tags/>` restores them (R3113). Group 1 is the direction word.
var recallTagsToggleRe = regexp.MustCompile(`<RECALL (notags|tags)/>`)

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

	jobs *ChanSvc

	mu           sync.Mutex
	sessions     map[string]*recallSessionState // R2730
	fireCounters map[string]uint64              // R2901: per-session monotonic, dir-seeded on first use; allocated on timer expiry
	// bloodhoundCounters: per-session monotonic <B> for directed-search
	// tasks. In-memory only, no dir-seeding (task docs are ephemeral
	// tmp://), reset on restart — distinct from fireCounters. R2937
	bloodhoundCounters map[string]uint64

	// Fixer state (S2). luhmann is the S1 orchestrator-seat bridge (owner +
	// nextQueue). Nil-safe: the Fixer no-ops without a hub. R3020, R3023, R3024
	luhmann luhmannHub

	// pools holds one secretary roster per class (R3298): "bloodhound" serves
	// external hunts and stranded watermarks, "recall" serves ambient curation
	// for sessions with no secretary of their own. Classes never share slots,
	// so neither workload can starve the other and each is sized by its own
	// class.<NAME>.pool_max. Read-only after construction — the rosters
	// themselves carry the mutation locks.
	pools map[string]*secretaryPool
	// pool is pools[bloodhoundPoolClass], kept as a direct handle because the
	// CLI-hunt paths are bloodhound-specific by construction (they touch
	// `requests`, which only that class has). Class-generic work — autoscale,
	// pruning, registration — goes through pools.
	pool *secretaryPool
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
	// R3298: one roster per class, built up front so no path has to decide
	// whether a class "exists yet" — an absent roster and an empty one would
	// otherwise mean the same thing to a caller and different things to a
	// stand-up decision.
	pools := map[string]*secretaryPool{
		bloodhoundPoolClass: newSecretaryPool(),
		recallPoolClass:     newSecretaryPool(),
	}
	return &RecallWatcher{
		db:                 db,
		librarian:          lib,
		store:              store,
		builder:            builder,
		jobs:               newChanSvc(chanSvcSlots),
		sessions:           make(map[string]*recallSessionState),
		fireCounters:       make(map[string]uint64),
		bloodhoundCounters: make(map[string]uint64),
		pools:              pools,
		pool:               pools[bloodhoundPoolClass],
	}
}

// Start begins the closure-actor worker goroutine and the CLI-bloodhound
// Fixer loop (S2). Wire the luhmann hub before calling Start.
func (w *RecallWatcher) Start() {
	if w == nil {
		return
	}
	w.jobs.start()
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
	ReadTranscriptData(newBytes, func(r TranscriptRecord) bool { // R3553
		switch r.Kind {
		case TurnUser:
			// Only a *genuine* human message arms the watcher (R2732).
			// Tool-results and harness-injected lines are also user
			// records — counting them lets the watcher re-fire on its own
			// consumers' wake turns (the recall ping-pong).
			if _, ok := r.Prose(); ok {
				sigs = append(sigs, signalUser)
			}
		case TurnEnd:
			sigs = append(sigs, signalTurnDuration)
		}
		return true
	})
	return sigs
}

// assistantLineTexts yields the decoded prose text of each `type:"assistant"`
// line in newBytes together with that line's `cwd` — the working directory of
// the session that wrote it, which Claude Code stamps on every assistant line.
// Skips blank, unparseable, non-assistant, and text-less lines. Shared by the
// watermark scanners (scanBloodhounds, scanRecallTagsDirective); the cwd is the
// anchoring directory for a bloodhound's file globs (R3193) and is ignored by
// scanners that have no use for it.
func assistantLineTexts(newBytes []byte) func(yield func(string, string) bool) {
	return func(yield func(string, string) bool) {
		ReadTranscriptData(newBytes, func(r TranscriptRecord) bool { // R3553
			if r.Kind != TurnAssistant {
				return true
			}
			text := r.Text()
			return text == "" || yield(text, r.Cwd)
		})
	}
}

// scanBloodhounds walks newBytes for directed-search watermarks in assistant
// output: each `type:"assistant"` line's text (TranscriptRecord.Text, through assistantLineTexts) is
// regex-matched for `<BLOODHOUND …>…</BLOODHOUND>`, and every capture is one
// request. Deterministic and once-only by construction — newBytes is the
// newly-appended slice, so a given line is scanned exactly once (two identical
// watermarks are two requests).
//
// Each line's `cwd` is the anchoring directory for that watermark's file globs
// (R3193): the session's own working directory, taken from the very line the
// watermark rode in on. Anchoring here — once, at the surface that knows the
// project — is what lets the Go-side seed and the secretary's own
// `ark search -files` calls resolve a glob to the same thing.
// R2934, R3184, R3193
func scanBloodhounds(newBytes []byte) []bloodhoundReq {
	var reqs []bloodhoundReq
	for text, cwd := range assistantLineTexts(newBytes) {
		for _, m := range bloodhoundRe.FindAllStringSubmatch(text, -1) {
			// m[1] = the attribute run (or ""), m[2] = payload.
			p := strings.TrimSpace(m[2])
			if p == "" {
				continue
			}
			req := parseBloodhoundAttrs(m[1], cwd)
			req.payload = p
			reqs = append(reqs, req)
		}
	}
	return reqs
}

// parseBloodhoundAttrs reads the opening tag's attribute run into a request:
// the bare `notags` opt-out (R3110) plus the repeatable `filter-files=` /
// `exclude-files=` globs (R3184, R3185), each anchored against dir (R3193).
//
// Attributes are order-independent and may repeat; an unrecognized one is
// ignored rather than failing the match, so a watermark carrying an attribute
// some future assistant emits still dispatches its hunt. Empty values are
// dropped by AnchorGlobsToDir — a stray `filter-files=""` must not become a
// glob that matches nothing and silently empties the corpus (R3185).
// CRC: crc-RecallWatcher.md | R3110, R3184, R3185, R3193
func parseBloodhoundAttrs(attrs, dir string) bloodhoundReq {
	var req bloodhoundReq
	if attrs == "" {
		return req
	}
	var filter, exclude []string
	for _, m := range bloodhoundAttrRe.FindAllStringSubmatch(attrs, -1) {
		switch m[1] {
		case "filter-files":
			filter = append(filter, m[2])
		case "exclude-files":
			exclude = append(exclude, m[2])
		}
	}
	req.filterFiles = AnchorGlobsToDir(filter, dir)
	req.excludeFiles = AnchorGlobsToDir(exclude, dir)
	// `notags` is bare (no `=value`), so look for it as a whole word in
	// what remains after the name="value" pairs are stripped out.
	req.notags = slices.Contains(strings.Fields(bloodhoundAttrRe.ReplaceAllString(attrs, " ")), "notags")
	return req
}

// bloodhoundReq is one recognized `<BLOODHOUND>` watermark: the clue payload,
// whether it opted out of tag proposals via `<BLOODHOUND notags>`, and the
// hunt's file scope (already anchored — see scanBloodhounds). Carried as one
// value so a hunt's attributes thread through dispatch and seeding without a
// growing parameter list. R3110, R3184, R3185
type bloodhoundReq struct {
	payload      string
	notags       bool
	filterFiles  []string
	excludeFiles []string
}

// scoped reports whether the hunt carries any file scope at all.
func (r bloodhoundReq) scoped() bool {
	return len(r.filterFiles) > 0 || len(r.excludeFiles) > 0
}

// scanRecallTagsDirective returns the final ambient tag-recommend toggle in
// newBytes: suppress=true for `<RECALL notags/>` (R3112), suppress=false for
// `<RECALL tags/>` (R3113). found is false when no marker is present. Markers
// are read in assistant-line order and the last one wins, so a batch that flips
// the toggle resolves to its final state. R3112, R3113
func scanRecallTagsDirective(newBytes []byte) (suppress, found bool) {
	for text, _ := range assistantLineTexts(newBytes) {
		for _, m := range recallTagsToggleRe.FindAllStringSubmatch(text, -1) {
			suppress, found = m[1] == "notags", true
		}
	}
	return suppress, found
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

// bloodhoundEnabled gates directed-search *recognition*: the assistant
// subscribed to the bloodhound-result tag (the level-3 opt-in) — and nothing
// else. Deliberately NOT conjoined with secretaryPresent: a session without its
// own secretary can still be served by Luhmann's pool, so requiring one here
// made a staffed pool worth nothing to a watermark and turned an unservable
// hunt into silence. Who serves a recognized watermark is decided later, in
// dispatchBloodhound (R3280).
// CRC: crc-RecallWatcher.md | R3279
func (w *RecallWatcher) bloodhoundEnabled(sessionID string) bool {
	if w.db == nil || w.db.pubsub == nil {
		return true
	}
	return w.db.pubsub.SubscriberCount("ark-bloodhound-result", sessionID) > 0
}

// poolCanServe reports whether Luhmann's pool could take a hunt now — an
// orchestrator is seated AND either a secretary is free or the pool has room to
// grow one. The Fixer makes the same determination when it pumps; this is the
// cheap pre-check dispatchBloodhound uses to choose between the pool route and
// the not-served report (R3280, R3282).
// CRC: crc-RecallWatcher.md | R3280
func (w *RecallWatcher) poolCanServe() bool {
	if w == nil || w.luhmann == nil || w.luhmann.LuhmannOwner() == "" {
		return false
	}
	w.pool.mu.Lock()
	defer w.pool.mu.Unlock()
	return w.pool.pickFreeLocked() != nil || len(w.pool.secretaries) < w.poolMax(bloodhoundPoolClass)
}

// ambientEnabled gates ambient curation (arm/accumulate/fire): the assistant
// subscribed to the recall-result tag (the level-4 opt-in) — and nothing else.
// Shared by the OnAppend arming gate and the fire() backstop so the two cannot
// drift.
//
// Deliberately NOT conjoined with secretaryPresent, for the same reason
// bloodhoundEnabled is not (R3279): a session without its own secretary can
// still be served by Luhmann's pool, so requiring one *to arm* made a staffed
// pool worth nothing to the more expensive of the two levels. Who serves a fired
// curation doc is decided later, at dispatch (R3294).
// CRC: crc-RecallWatcher.md | R2806, R3294
func (w *RecallWatcher) ambientEnabled(sessionID string) bool {
	if w.db == nil || w.db.pubsub == nil {
		return true
	}
	return w.db.pubsub.SubscriberCount("ark-recall-result", sessionID) > 0
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

	// Per-capability gate (R3279, R3294): **neither** capability consults the
	// session secretary. Both can be served by Luhmann's pool — directed per
	// R3280, ambient per R3294 — so a missing secretary tears nothing down and
	// decides nothing here. Return early only when there is nothing at all to
	// produce, i.e. the assistant holds neither result subscription.
	//
	// The ambient teardown that used to live here (on secretary absence) is gone
	// with its premise: the `ambientEnabled` block below already drops lingering
	// state when the recall-result sub goes, which is the condition that actually
	// means "stop accumulating".
	// CRC: crc-RecallWatcher.md | Seq: seq-recall-watcher.md#2.3 | R3279, R3294
	if !w.bloodhoundEnabled(sessionID) {
		logUnheardWatermarks(sessionID, newBytes) // R3624
		if !w.ambientEnabled(sessionID) {
			return
		}
	}

	w.mu.Lock()
	defer w.mu.Unlock()

	// Ambient (R3294): accumulate + arm only when the recall-result sub is
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
		for _, req := range scanBloodhounds(newBytes) {
			bid := w.nextBloodhoundLocked(sessionID)
			r, sid := req, sessionID
			svc(w.jobs, func() { w.dispatchBloodhound(sid, bid, r) })
		}
	}
	// R3112/R3113: a <RECALL notags/> | <RECALL tags/> marker toggles per-session
	// tag-recommend suppression on the builder (last marker in the batch wins).
	if w.builder != nil {
		if suppress, found := scanRecallTagsDirective(newBytes); found {
			if suppress {
				w.builder.SetRecallNotags(sessionID)
			} else {
				w.builder.ClearRecallNotags(sessionID)
			}
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

	// Ambient backstop to the OnAppend arming gate (R3294): re-check the
	// secretary + recall-result subs in case the consumer dropped during
	// activation_delay. Missing → skip the substrate call + disk write;
	// pendingChunks is already cleared (R2735), so the next OnAppend starts
	// fresh.
	// CRC: crc-RecallWatcher.md | Seq: seq-recall-watcher.md#5.4 | R2806, R3294
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
				// pass still computes proposals for these chunks,
				// but the surfacing filter hides them from the DM —
				// exactly the opposite of what ambient recall
				// wants. R2746
				[]ConnectionsInput{{Text: para}},
				// R3190: no FilterFiles/ExcludeFiles here, and none should ever
				// be added. A hunt's scope is opt-in per watermark and lives on
				// the `<BLOODHOUND>` tag, which this ambient path does not read.
				// Ambient recall is push — nobody asked it a question — so
				// narrowing it by current-project scope would suppress exactly
				// the cross-project tangents that make it worth having.
				RecallOpts{
					K:              cfg.EffectiveChunksPerDM(),
					IncludeContent: true,
					Session:        sessionID,
					Propose:        cfg.EffectivePropose(),
					KeepTagless:    true,
					// R3082: inject the source chunk (each snapshot turn is
					// processed as cid across this loop) so the conversation
					// earns compute-for-display proposals, rendered tag-only.
					ConversationChunks: []uint64{cid},
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

	// Choose who serves it. The builder addressed the doc to its own session,
	// which is right only when that session has a secretary; otherwise the
	// watcher re-addresses it to a recall-class pool secretary, or reports it
	// unservable. Routing is deliberately after the write rather than before:
	// a doc that waits for a stand-up must already exist to be routed on
	// registration.
	// CRC: crc-RecallWatcher.md | Seq: seq-recall-watcher.md#6 | R3302, R3303, R3304
	w.routeCuration(sessionID, fire)

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
// then hands the request + seed to the builder, which writes the task doc in
// the ARK-BLOODHOUND namespace and retains the clue for the finding header.
// The request carries the hunt's file scope, which reaches both the seed's
// substrate filter and the secretary's ready-made filter string (R3185, R3189).
// CRC: crc-RecallWatcher.md | Seq: seq-recall-watcher.md | R2937, R2947, R3006, R3007, R3185
func (w *RecallWatcher) dispatchBloodhound(sessionID string, bid uint64, req bloodhoundReq) {
	if w.builder == nil {
		return
	}
	if !w.bloodhoundEnabled(sessionID) {
		logWatermarkRoute(sessionID, bid, req.payload, routeListenerGone) // R3623
		return
	}
	// R3280: server selection. The session's own secretary is preferred — it is
	// dedicated and already warm, and its findings carry the tag-recommend half
	// the raw pool relay cannot. Without one, borrow the pool; with neither, say
	// so rather than dropping the hunt on the floor (R3282).
	if !w.secretaryPresent(sessionID) {
		if !w.poolCanServe() {
			w.emitNotServed(sessionID, bid, req)
			return
		}
		if err := w.enqueueWatermarkHunt(sessionID, bid, req); err != nil {
			log.Printf("recall-watcher: watermark pool dispatch failed session=%s B=%d: %v", sessionID, bid, err)
			w.emitNotServed(sessionID, bid, req)
			return
		}
		logWatermarkRoute(sessionID, bid, req.payload, routePool) // R3623
		return
	}
	// Seed the hunt with the hypergraph-aware combined search (renderSeed):
	// clue-only and session-agnostic, so a directed *pull* hunt sees every match
	// with no derivation side-effects. Only Recall reaches the value→chunk tag
	// axis (R2905/R2906) the subagent's content-only `ark search` cannot. A
	// failed seed is not fatal — the empty-seed note still dispatches. R3006, R3007
	seed := w.renderSeed(req)
	if err := w.builder.RecallBloodhoundOpen(sessionID, bid, req, seed); err != nil {
		log.Printf("recall-watcher: bloodhound dispatch failed session=%s B=%d: %v", sessionID, bid, err)
		return
	}
	logWatermarkRoute(sessionID, bid, req.payload, routeSecretary) // R3623
}

// Watermark routes, as the R3623 log line names them.
const (
	routeSecretary    = "session secretary"
	routePool         = "Luhmann's pool"
	routeListenerGone = "dropped: the listener left before dispatch"
	routeNotServed    = "not served: "
)

// watermarkLogClueMax caps the clue echoed in a watermark log line: enough for
// a reader to tell which watermark it was, short enough to keep one line.
const watermarkLogClueMax = 60

// logWatermarkRoute writes the one unconditional log line a recognized
// watermark gets, naming where its hunt went. A lost hunt and a slow one are the
// same silence to the session blocked on `listen`; this line is what tells them
// apart. Watermarks are rare, so it is not behind -v.
// CRC: crc-RecallWatcher.md | R3623
func logWatermarkRoute(sessionID string, bid uint64, payload, route string) {
	log.Printf("recall-watcher: watermark session=%s B=%d clue=%q route=%s",
		sessionID, bid, logClue(payload), route)
}

// logUnheardWatermarks logs each watermark in newBytes for a session with no
// bloodhound-result subscriber. Not recognition: no hunt id is issued and
// nothing dispatches (R3279). The Contains check keeps an ordinary append from
// paying for a transcript decode.
// CRC: crc-RecallWatcher.md | R3624
func logUnheardWatermarks(sessionID string, newBytes []byte) {
	if !bytes.Contains(newBytes, []byte(bloodhoundCloseTag)) {
		return
	}
	for _, req := range scanBloodhounds(newBytes) {
		log.Printf("recall-watcher: watermark session=%s clue=%q seen with no listener; not recognized (start `ark bloodhound listen`)",
			sessionID, logClue(req.payload))
	}
}

// logClue is a watermark's clue cut to one short log-safe fragment.
func logClue(payload string) string {
	c := strings.Join(strings.Fields(clueOf(payload)), " ")
	if r := []rune(c); len(r) > watermarkLogClueMax {
		c = string(r[:watermarkLogClueMax]) + "…"
	}
	return c
}

// emitNotServed writes a `## Finding:` doc reporting that a recognized watermark
// could not be served — no session secretary, and Luhmann's pool either unseated
// or full with no room to grow (R3282).
//
// The failure mode this exists to kill is silence. An assistant blocked on
// `listen` cannot tell a dropped watermark from a hunt still running or one that
// legitimately found nothing, so it waits forever on a request nobody holds. The
// doc shape is the ordinary finding shape, so the existing consumer needs no
// change; only the body differs, naming the failed condition and the caller-driven
// fallback. Same discipline as the scope-matched-nothing note (R3188): a failure
// must read as a failure, not as an answer.
// CRC: crc-RecallWatcher.md | Seq: seq-recall-watcher.md#4 | R3282
func (w *RecallWatcher) emitNotServed(sessionID string, bid uint64, req bloodhoundReq) {
	reason := "no secretary is running for this session and Luhmann's pool is unavailable (no orchestrator seated)"
	if w.luhmann != nil && w.luhmann.LuhmannOwner() != "" {
		reason = "no secretary is running for this session and Luhmann's pool is full with no room to grow"
	}
	w.emitNotServedReason(sessionID, bid, clueOf(req.payload), reason)
}

// emitNotServedReason writes the not-served finding for a caller-supplied
// reason. Split out of emitNotServed because the two failures that must report
// are discovered at different places and hold different things: dispatch has the
// bloodhoundReq, while the reaper has only a cliRequest — which already carries
// the computed clue, so nothing needs re-deriving (R3282, R3293).
//
// Keeping one writer is the point. Two report paths that formatted their own doc
// would drift, and a consumer that parses one shape would silently stop
// recognizing the other as a failure — which lands it back at silence by a
// longer road.
// CRC: crc-RecallWatcher.md | Seq: seq-recall-watcher.md#4 | R3282, R3293
func (w *RecallWatcher) emitNotServedReason(sessionID string, bid uint64, clue, reason string) {
	if w.db == nil {
		return
	}
	body := bloodhoundFindingHeader(sessionID, clue) + fmt.Sprintf(
		"\n**Not served.** %s.\n\n"+
			"This hunt was recognized but never ran, so the absence of results says nothing about the corpus. "+
			"Run it yourself with `ark bloodhound search --raw`, or spawn a session secretary and re-emit.\n",
		reason)
	if err := SyncVoid(w.db, func(db *DB) error {
		_, e := db.AddTmpFile(bloodhoundFindingPath(sessionID, bid), "markdown", []byte(body))
		return e
	}); err != nil {
		log.Printf("recall-watcher: not-served report failed session=%s B=%d: %v", sessionID, bid, err)
		return
	}
	logWatermarkRoute(sessionID, bid, clue, routeNotServed+reason) // R3623
}

// renderBloodhoundSeed formats a Recall result as the `## Recall seed` block of
// a bloodhound task doc: one compact locator line per candidate —
// `<path>:<range> (<size>) <score> [tags]` with a short excerpt, no chunkid on
// the wire (the crank handle opens each with `ark chunks --wrap recall <path:range>`). A
// nil/empty result renders the empty-seed note so the task still dispatches.
// When notags, the per-line `[tags]` are omitted — a no-tags hunt won't propose,
// so the bridge vocabulary is dead weight (R3110).
//
// A hunt whose positive globs matched no indexed file gets its own note naming
// them (R3188). That case is *not* the ordinary empty seed: it says the scope
// was wrong, where the plain note says the corpus was empty. Collapsing the two
// would hand back a confident "nothing found" for a mistyped glob, which is the
// one failure a reader cannot detect. R3006
// CRC: crc-RecallWatcher.md | R3006, R3110, R3188
func renderBloodhoundSeed(result *RecallResult, req bloodhoundReq) string {
	notags := req.notags
	var sb strings.Builder
	sb.WriteString("## Recall seed\n\n")
	if result != nil && result.ScopeEmpty {
		fmt.Fprintf(&sb, "_(**scope matched no indexed file**: %s — the hunt's include globs match nothing in the corpus, so this seed is empty for that reason and not because the corpus lacks an answer. Search without them, or report the scope as wrong.)_\n",
			strings.Join(req.filterFiles, ", "))
		return sb.String()
	}
	if result == nil || len(result.Chunks) == 0 {
		sb.WriteString("_(no corpus matches — start from your own searches)_\n")
		return sb.String()
	}
	sb.WriteString("Strong candidates from the deluxe combined search (4 substrates: meaning + tags — the tag axis your own `ark search` can't reach). READ these first with `ark chunks --wrap recall <path:range>` (clean text, not JSON); run your own searches only to widen or if this is thin.\n\n")
	for _, c := range result.Chunks {
		tags := ""
		if names := recallTagNames(c.Tags); !notags && len(names) > 0 {
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
