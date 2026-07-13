package ark

// CRC: crc-RecallWatcher.md | Seq: seq-bloodhound-cli.md | R3020, R3023, R3024, R3025, R3030, R3031, R3032

// The watcher-as-Fixer for external-CLI directed hunts (bloodhound-cli.md, S2).
// Distinct from the OnAppend-driven ambient/in-session paths: this subscribes to
// two pubsub tags and routes the request doc tmp://BLOODHOUND-CLI/<id> across the
// pipeline via the tag baton. All scheduling is deterministic Go — the Fixer is
// the deciding go-between (the Mediator/Fixer pattern), never a language model.

import (
	"log"
	"slices"
	"strconv"
	"strings"
	"sync"
	"time"
)

// bloodhoundPoolClass is the Luhmann-managed class name for the CLI-bloodhound
// secretary pool (config lives under [luhmann].class.bloodhound). R3017, R3018
const bloodhoundPoolClass = "bloodhound"

// bloodhoundCLIPrefix is the tmp:// namespace for CLI request docs, separate
// from ambient recall (ARK-RECALL) and in-session bloodhound (ARK-BLOODHOUND).
const bloodhoundCLIPrefix = "tmp://BLOODHOUND-CLI/"

// The Fixer's pubsub identity and the two tags it batons between. The request
// tag wakes a new hunt; the return tag wakes on a secretary's raw results.
const (
	bloodhoundCLIFixerSession = "bloodhound-cli-fixer"
	bloodhoundCLIRequestTag   = "ark-bloodhound-cli"
	bloodhoundCLIReturnTag    = "ark-bloodhound-cli-return"
	bloodhoundCLIResultTag    = "ark-bloodhound-cli-result" // value-scoped by hunt id; wakes the waiting CLI (R3028)
	bloodhoundCLIListenWindow = 5 * time.Second
	// bloodhoundPruneInterval is how often the Fixer scans for pool secretaries
	// idle past their cooldown to retire (R3019), and the same tick drives the
	// request sweep (reap + re-issue, R3041/R3042). Independent of the per-class
	// cooldown_seconds, which is the warmth window each secretary must exceed.
	bloodhoundPruneInterval = 30 * time.Second
	// bloodhoundReissueThreshold is how long a request may sit pending and
	// unrouted before the sweep re-pushes a stand-up to recover a directive
	// Luhmann dropped (R3042). Shorter than request_ttl_seconds, so a stranded
	// request gets a re-issue attempt well before it is reaped.
	bloodhoundReissueThreshold = 60 * time.Second
)

// poolSec is one pool secretary's roster entry, keyed by its reserved nonce
// (R3032). busy while running a hunt; idleSince stamps the cooldown clock on
// return to idle (R3024).
type poolSec struct {
	nonce     uint64
	busy      bool
	idleSince time.Time
}

// cliRequest is the per-hunt state the Fixer records when a request first
// arrives: its submit time (the reap clock, R3041) and whether the client asked
// to skip curation (`--raw` → curate:false, R3038). Kept in memory because the
// request doc's body is overwritten at the enhance/close hops, so the intent
// cannot survive there.
type cliRequest struct {
	submitted time.Time
	raw       bool
}

// cliPool is the watcher's roster of CLI-bloodhound pool secretaries plus the
// pending-hunt queue, the in-flight path→nonce map, and the per-request record.
// Everything under mu. In-memory; a server bounce drops it and the CLI's --wait
// re-drives. R3023, R3024, R3038, R3041
type cliPool struct {
	mu          sync.Mutex
	secretaries map[uint64]*poolSec    // nonce → state
	inflight    map[string]uint64      // request-doc path → the nonce running it
	pending     []string               // request-doc paths awaiting a free secretary
	requests    map[string]*cliRequest // request-doc path → submit time + raw intent
}

func newCLIPool() *cliPool {
	return &cliPool{
		secretaries: make(map[uint64]*poolSec),
		inflight:    make(map[string]uint64),
		requests:    make(map[string]*cliRequest),
	}
}

// pickFreeLocked returns any not-busy secretary, or nil. Caller holds mu.
func (p *cliPool) pickFreeLocked() *poolSec {
	for _, s := range p.secretaries {
		if !s.busy {
			return s
		}
	}
	return nil
}

// PoolBusy reports whether the pool has no free secretary and is at pool_max — a
// submit-time snapshot for the CLI's fail-fast-without-`--wait` gate (R3022).
func (w *RecallWatcher) PoolBusy() bool {
	if w == nil {
		return false
	}
	w.pool.mu.Lock()
	defer w.pool.mu.Unlock()
	return w.pool.pickFreeLocked() == nil && len(w.pool.secretaries) >= w.poolMax()
}

// poolMax reads the live [luhmann].class.bloodhound.pool_max (R3017).
func (w *RecallWatcher) poolMax() int {
	if w == nil || w.db == nil {
		return 3
	}
	return w.db.Config().Luhmann.EffectivePoolMax(bloodhoundPoolClass)
}

// cooldown reads the live [luhmann].class.bloodhound.cooldown_seconds (R3018) as
// a Duration — the warmth window an idle secretary must exceed before pruning.
func (w *RecallWatcher) cooldown() time.Duration {
	if w == nil || w.db == nil {
		return 600 * time.Second
	}
	return time.Duration(w.db.Config().Luhmann.EffectiveCooldownSeconds(bloodhoundPoolClass)) * time.Second
}

// requestTTL reads the live [luhmann].class.bloodhound.request_ttl_seconds
// (R3041) as a Duration — the reap window past which a stranded request (the
// client hit --timeout and exited) is dropped.
func (w *RecallWatcher) requestTTL() time.Duration {
	if w == nil || w.db == nil {
		return 900 * time.Second
	}
	return time.Duration(w.db.Config().Luhmann.EffectiveRequestTTLSeconds(bloodhoundPoolClass)) * time.Second
}

// startFixer subscribes the Fixer to the request/return tags and launches its
// drain loop. Idempotent subscribe; the loop runs for the server's lifetime,
// like the closure-actor worker. R3023, R3024
func (w *RecallWatcher) startFixer() {
	if w == nil || w.db == nil || w.db.pubsub == nil {
		return
	}
	var subs []*TagSub
	for _, tag := range []string{bloodhoundCLIRequestTag, bloodhoundCLIReturnTag} {
		p, err := ParseMatchSyntax(tag)
		if err != nil {
			log.Printf("bloodhound-cli: bad match %q: %v", tag, err)
			return
		}
		subs = append(subs, &TagSub{Kind: TagSubChunk, Predicate: p})
	}
	w.db.pubsub.Subscribe(bloodhoundCLIFixerSession, subs)
	go w.fixerLoop()
	go w.pruneLoop() // R3019: autoscale down — retire secretaries idle past cooldown
}

// pruneLoop periodically retires idle-past-cooldown pool secretaries. Runs for
// the server lifetime, like fixerLoop. R3019
func (w *RecallWatcher) pruneLoop() {
	ticker := time.NewTicker(bloodhoundPruneInterval)
	defer ticker.Stop()
	for range ticker.C {
		w.prune()
		w.sweepRequests() // R3041/R3042: reap stranded requests, re-issue dropped stand-ups
	}
}

// prune retires pool secretaries idle past the cooldown window (R3019): for each
// not-busy secretary whose idle time exceeds cooldown_seconds, push a stop
// directive naming its nonce onto Luhmann's nextQueue and drop it from the
// roster. Needs a live Luhmann to receive the directive.
func (w *RecallWatcher) prune() {
	if w.luhmann == nil || w.luhmann.LuhmannOwner() == "" {
		return
	}
	cooldown := w.cooldown()
	var stop []uint64
	w.pool.mu.Lock()
	for nonce, s := range w.pool.secretaries {
		if !s.busy && !s.idleSince.IsZero() && time.Since(s.idleSince) > cooldown {
			stop = append(stop, nonce)
			delete(w.pool.secretaries, nonce)
		}
	}
	w.pool.mu.Unlock()
	for _, nonce := range stop {
		// R3019: one stop directive per over-cooldown secretary; Luhmann stops
		// its Task and exit-records it.
		w.luhmann.EnqueueLuhmann(LuhmannWork{Kind: "directive", Directive: "stop", Class: bloodhoundPoolClass, Nonce: nonce})
	}
}

// fixerLoop drains the two tags and dispatches each event to the request or
// return handler. Runs for the server lifetime. R3023, R3024
func (w *RecallWatcher) fixerLoop() {
	for {
		events := w.db.pubsub.Listen(bloodhoundCLIFixerSession, bloodhoundCLIListenWindow)
		for _, ev := range events {
			switch ev.Tag {
			case bloodhoundCLIRequestTag:
				w.onBloodhoundCLIRequest(ev.Path)
			case bloodhoundCLIReturnTag:
				w.onBloodhoundCLIReturn(ev.Path)
			}
		}
	}
}

// onBloodhoundCLIRequest handles a new hunt (Fixer request trigger, R3023): gate
// on a live Luhmann (R3020), enqueue the request, push a stand-up directive if
// the pool has room, then pump pending hunts to free secretaries.
func (w *RecallWatcher) onBloodhoundCLIRequest(path string) {
	if w.luhmann == nil || w.luhmann.LuhmannOwner() == "" {
		// R3020: no orchestrator = no curator. Leave it pending; the CLI's own
		// pre-check reports orchestrator-not-running. If Luhmann arrives later,
		// a subsequent trigger (or a pool registration) pumps it.
		Logv(1, "bloodhound-cli: no Luhmann owner; %s left pending", path)
		return
	}
	// R3038: read the request doc once and record the curate intent — the body is
	// overwritten at enhance/close, so --raw (curate:false) must be captured now,
	// in memory. A nil db (unit tests) or a read miss defaults to curated.
	raw := false
	if w.db != nil {
		if content, err := w.db.TmpContent(path); err == nil {
			for _, line := range strings.Split(string(content), "\n") {
				if strings.TrimSpace(line) == "curate: false" {
					raw = true // R3038: a dedicated marker line, not a clue substring
					break
				}
			}
		}
	}
	w.pool.mu.Lock()
	w.pool.pending = append(w.pool.pending, path)
	w.pool.requests[path] = &cliRequest{submitted: time.Now(), raw: raw} // R3038, R3041
	needStandUp := w.pool.pickFreeLocked() == nil && len(w.pool.secretaries) < w.poolMax()
	w.pool.mu.Unlock()
	Logv(1, "bloodhound-cli: request %s (raw=%v stand-up=%v)", path, raw, needStandUp)
	if needStandUp {
		// R3023: none free, room in the pool → ask Luhmann to stand one up.
		w.luhmann.EnqueueLuhmann(LuhmannWork{Kind: "directive", Directive: "stand-up", Class: bloodhoundPoolClass})
	}
	w.pump()
}

// onBloodhoundCLIReturn handles a secretary's return (Fixer return trigger,
// R3024): free the secretary into cooldown *before* curation, push the
// request-doc path to Luhmann's next queue for curation (R3025), then pump.
func (w *RecallWatcher) onBloodhoundCLIReturn(path string) {
	w.pool.mu.Lock()
	if nonce, ok := w.pool.inflight[path]; ok {
		delete(w.pool.inflight, path)
		if sec := w.pool.secretaries[nonce]; sec != nil {
			sec.busy = false           // R3024: occupancy freed before curation
			sec.idleSince = time.Now() // cooldown clock starts
		}
	}
	req := w.pool.requests[path]
	delete(w.pool.requests, path) // R3039: the hunt is resolved either way
	w.pool.mu.Unlock()

	// R3039: branch on the recorded curate intent. Raw skips Luhmann entirely —
	// the watcher relays the secretary's uncurated findings straight to the result
	// doc; curated (default) routes to Luhmann for the discernment step.
	if req != nil && req.raw {
		if err := w.relayRawResult(path); err != nil {
			log.Printf("bloodhound-cli: raw relay %s failed: %v", path, err)
		}
	} else if w.luhmann != nil {
		// R3024, R3025: hand the raw finding to Luhmann for curation (in-process,
		// no tag hop). If the queue is full the curation is dropped — the CLI's
		// --timeout bounds the wait; a bounce re-drives the whole hunt.
		w.luhmann.EnqueueLuhmann(LuhmannWork{Kind: "curation", Path: path})
	}
	w.pump()
}

// relayRawResult is the raw branch of the return hop (R3039, R3040): it writes
// the result doc tmp://BLOODHOUND-CLI-RESULT/<id> directly from the request
// doc's ## Raw findings — the secretary's own uncurated markdown, already the
// Baby Food an agent reads — tags it @ark-bloodhound-cli-result: <id> (WITH
// COLON, so the waiting CLI's subscription wakes), and drops the request doc.
// The mirror of BloodhoundCLIAddDone's curated write, but with no Luhmann in the
// loop. Best-effort: a nil db (unit tests) is a no-op.
// CRC: crc-RecallWatcher.md | Seq: seq-bloodhound-cli.md#1.4.3 | R3039, R3040
func (w *RecallWatcher) relayRawResult(path string) error {
	if w.db == nil {
		return nil
	}
	id := cliRequestID(path)
	data, err := w.db.TmpContent(path)
	if err != nil {
		return err
	}
	// Relay only the ## Raw findings body; an absent section (shouldn't happen —
	// the secretary always writes it) yields a "(no findings)" body.
	findings := "(no findings)\n"
	if i := strings.Index(string(data), "## Raw findings"); i >= 0 {
		if body := strings.TrimSpace(string(data)[i+len("## Raw findings"):]); body != "" {
			findings = body + "\n"
		}
	}
	body := "@" + bloodhoundCLIResultTag + ": " + id + "\n\n" + findings
	if err := SyncVoid(w.db, func(db *DB) error {
		_, e := db.AddTmpFile(bloodhoundCLIResultPath(id), "markdown", []byte(body))
		return e
	}); err != nil {
		return err
	}
	// The request doc has served its purpose; drop it (mirrors BloodhoundCLIAddDone).
	_ = SyncVoid(w.db, func(db *DB) error {
		return db.RemoveTmpFile(path)
	})
	return nil
}

// sweepRequests is the periodic request maintenance the pruneLoop drives
// (R3041/R3042): re-attempt routing, reap requests the client abandoned, and
// re-issue a stand-up for a request stranded by a dropped directive. Operates
// only on pending requests — an in-flight one is bounded by the secretary
// lifecycle, so the sweep never races a live secretary's write.
// CRC: crc-RecallWatcher.md | Seq: seq-bloodhound-cli.md#2.1 | R3041, R3042
func (w *RecallWatcher) sweepRequests() {
	if w == nil {
		return
	}
	// 2.1.1: a free secretary may have been missed by an earlier pump.
	w.pump()

	ttl := w.requestTTL()
	var reap []string
	standUp := false
	w.pool.mu.Lock()
	// 2.1.2 reap: a pending request older than the TTL — the client hit --timeout
	// and exited. Drop it from requests + pending; its doc is removed below.
	kept := w.pool.pending[:0]
	for _, path := range w.pool.pending {
		if req := w.pool.requests[path]; req != nil && time.Since(req.submitted) > ttl {
			reap = append(reap, path)
			delete(w.pool.requests, path)
			continue
		}
		kept = append(kept, path)
	}
	w.pool.pending = kept
	// 2.1.3 re-issue: any surviving request pending past the threshold, with room
	// in the pool, earns one stand-up to recover a directive Luhmann dropped.
	if w.pool.pickFreeLocked() == nil && len(w.pool.secretaries) < w.poolMax() {
		for _, path := range w.pool.pending {
			if req := w.pool.requests[path]; req != nil && time.Since(req.submitted) > bloodhoundReissueThreshold {
				standUp = true
				break
			}
		}
	}
	w.pool.mu.Unlock()

	for _, path := range reap {
		Logv(1, "bloodhound-cli: reaped stranded request %s", path)
		if w.db != nil {
			_ = SyncVoid(w.db, func(db *DB) error { return db.RemoveTmpFile(path) })
		}
	}
	if standUp && w.luhmann != nil && w.luhmann.LuhmannOwner() != "" {
		Logv(1, "bloodhound-cli: re-issuing a dropped stand-up for a stranded request")
		w.luhmann.EnqueueLuhmann(LuhmannWork{Kind: "directive", Directive: "stand-up", Class: bloodhoundPoolClass})
	}
}

// pump routes queued hunts to free secretaries — the single "route when ready"
// engine, called on any state change (new request, secretary returned/freed,
// new secretary registered). Each iteration pops one pending path and one free
// secretary atomically under the lock, so concurrent pumps never double-route.
// R3023, R3030, R3031, R3032
func (w *RecallWatcher) pump() {
	if w.luhmann == nil {
		return
	}
	owner := w.luhmann.LuhmannOwner()
	if owner == "" {
		return
	}
	for {
		w.pool.mu.Lock()
		if len(w.pool.pending) == 0 {
			w.pool.mu.Unlock()
			return
		}
		sec := w.pool.pickFreeLocked()
		if sec == nil {
			w.pool.mu.Unlock()
			return
		}
		path := w.pool.pending[0]
		w.pool.pending = w.pool.pending[1:]
		sec.busy = true
		sec.idleSince = time.Time{}
		nonce := sec.nonce
		w.pool.inflight[path] = nonce
		w.pool.mu.Unlock()

		// R3032: the routing identity is the composite <luhmann-session>-<nonce>,
		// so the secretary owns a unique @ark-secretary-work tube.
		composite := owner + "-" + strconv.FormatUint(nonce, 10)
		if err := w.enhanceRequestDoc(path, composite); err != nil {
			log.Printf("bloodhound-cli: enhance %s failed: %v", path, err)
			w.pool.mu.Lock()
			delete(w.pool.inflight, path)
			sec.busy = false
			sec.idleSince = time.Now()
			w.pool.mu.Unlock()
			continue
		}
		Logv(1, "bloodhound-cli: routed %s → @ark-secretary-work=%s", path, composite)
	}
}

// enhanceRequestDoc turns the request doc into a standard bloodhound task doc
// routed to one pool secretary (R3006, R3030, R3031): read the payload, seed the
// hunt, and in one atomic write-actor flush overwrite the doc with the head tag
// @ark-secretary-work=<composite> (the baton flip) + the ## Search task + the
// ## Recall seed + the search crank handle. The CLI request id is the finding
// cookie. R3031, R3032
func (w *RecallWatcher) enhanceRequestDoc(path, composite string) error {
	payload, err := w.readCLIRequestPayload(path)
	if err != nil {
		return err
	}
	// CLI hunts are findings-only: their recommends have no in-session builder to
	// land in (results route through Luhmann curation), so suppress step 8's tag
	// proposals here — #38's tag-proposing is the in-session bloodhound path (R3110).
	seed := w.renderSeed(payload, true)
	body := buildSearchTask(composite, cliRequestID(path), payload, seed, true)
	// UpdateTmpFile (not Add) — the request doc already exists (the CLI created
	// it); this overwrites it in one write-actor flush, re-indexing the head tag
	// from @ark-bloodhound-cli to @ark-secretary-work=<composite> (the baton flip).
	return SyncVoid(w.db, func(db *DB) error {
		return db.UpdateTmpFile(path, "markdown", []byte(body))
	})
}

// RegisterPoolSecretary records a nonce as a pending pool secretary (R3033) and
// pumps any waiting hunt to it. Called by the reserve-nonce --luhmann handler:
// the reservation doubles as pool registration, so the Fixer learns the nonce
// Luhmann will spawn with. Idempotent. R3033
func (w *RecallWatcher) RegisterPoolSecretary(nonce uint64) {
	if w == nil {
		return
	}
	w.pool.mu.Lock()
	if _, exists := w.pool.secretaries[nonce]; !exists {
		w.pool.secretaries[nonce] = &poolSec{nonce: nonce, idleSince: time.Now()}
	}
	w.pool.mu.Unlock()
	w.pump()
}

// DeregisterPoolSecretary drops a pool secretary from the roster on a terminal
// exit record for the bloodhound class — the symmetric counterpart to
// RegisterPoolSecretary (R3034/R3033). Called from the exit-record handler for
// every luhmann record; it self-gates on the bloodhound class and a terminal
// kind, so a spawn/respawn record or another class is a no-op. Removing the
// nonce means a secretary that exited on its own context limit (not via a stop
// directive) no longer counts toward pool_max and is never routed a hunt; any
// inflight entry pointing at it is dropped in the same step. Idempotent — an
// already-removed nonce is a no-op, so a prune-driven stop and an independent
// self-exit reconcile safely.
// CRC: crc-RecallWatcher.md | R3034
func (w *RecallWatcher) DeregisterPoolSecretary(class, kind string, nonce uint64) {
	if w == nil || class != bloodhoundPoolClass {
		return
	}
	switch kind {
	case "exit", "crash", "quit-early":
	default:
		return // spawn/respawn (or anything non-terminal) never deregisters
	}
	w.pool.mu.Lock()
	delete(w.pool.secretaries, nonce)
	for path, n := range w.pool.inflight {
		if n == nonce {
			delete(w.pool.inflight, path)
		}
	}
	w.pool.mu.Unlock()
}

// bloodhoundSeedKCap bounds the per-idea seed budget so a many-paragraph clue
// can't blow the seed up (R3045).
const bloodhoundSeedKCap = 30

// seedMetaKeys are the leading `key:` metadata fields clueOf strips before the
// clue body — they shape the hunt (crank handle) but are not search ideas
// (R3044). `curate:` is the #30 raw marker; the others are the CLI's flags.
var seedMetaKeys = []string{"scope:", "depth:", "want:", "curate:"}

// clueOf extracts the searchable clue from a bloodhound payload: leading
// scope/depth/want/curate metadata lines (and blank lines) are stripped, and the
// rest is the clue. Free-form in-session prose has no leading metadata, so it is
// returned whole; an old clue-first payload (first line not a stripped key) also
// degrades to the whole string — no regression.
// CRC: crc-RecallWatcher.md | R3044
func clueOf(payload string) string {
	lines := strings.Split(payload, "\n")
	i := 0
	for i < len(lines) {
		t := strings.ToLower(strings.TrimSpace(lines[i]))
		isMeta := slices.ContainsFunc(seedMetaKeys, func(k string) bool {
			return strings.HasPrefix(t, k)
		})
		// Skip blank and metadata lines; stop at the first content line.
		if t != "" && !isMeta {
			break
		}
		i++
	}
	return strings.TrimSpace(strings.Join(lines[i:], "\n"))
}

// seedK scales the seed budget with the clue's idea (paragraph) count so a
// multi-idea union isn't starved by a fixed pool; a single idea keeps the base
// bloodhoundSeedK.
// CRC: crc-RecallWatcher.md | R3045
func seedK(paras int) int {
	if paras <= 1 {
		return bloodhoundSeedK
	}
	return min(bloodhoundSeedK+5*(paras-1), bloodhoundSeedKCap)
}

// seedInputs splits the clue into paragraphs (the same markdown chunker the fire
// path uses) and returns one Recall input per idea plus the scaled seed K. Recall
// unions the per-input hits by chunkID, so each idea contributes its own matches
// instead of a single centroid query; a single-paragraph clue is one input,
// identical to the pre-split seed.
// CRC: crc-RecallWatcher.md | R3043, R3044, R3045
func seedInputs(payload string) ([]ConnectionsInput, int) {
	paras := splitParagraphs([]byte(clueOf(payload)))
	if len(paras) == 0 {
		// Empty clue: one empty input preserves the empty-seed-note behavior.
		return []ConnectionsInput{{Text: ""}}, bloodhoundSeedK
	}
	inputs := make([]ConnectionsInput, len(paras))
	for i, p := range paras {
		inputs[i] = ConnectionsInput{Text: p}
	}
	return inputs, seedK(len(paras))
}

// renderSeed runs the hypergraph-aware combined search on the clue and renders
// the ## Recall seed block. Shared by dispatchBloodhound and the CLI Fixer. The
// clue is split per paragraph (seedInputs) so each idea in a complex clue seeds
// its own matches. A failed seed is not fatal — the empty-seed note still
// dispatches the hunt. R3006, R3007, R3043
func (w *RecallWatcher) renderSeed(payload string, notags bool) string {
	inputs, k := seedInputs(payload)
	result, err := w.librarian.Recall(
		inputs,
		RecallOpts{K: k, IncludeContent: true, KeepTagless: true},
	)
	if err != nil {
		log.Printf("bloodhound-cli: seed Recall failed: %v", err)
		result = nil
	}
	return renderBloodhoundSeed(result, notags)
}

// readCLIRequestPayload reads the request doc and strips its @ark-bloodhound-cli
// head-tag line, returning the raw TERMS payload (clue · scope · depth · want).
func (w *RecallWatcher) readCLIRequestPayload(path string) (string, error) {
	data, err := w.db.TmpContent(path)
	if err != nil {
		return "", err
	}
	return stripCLIRequestTag(string(data)), nil
}

// stripCLIRequestTag drops a leading @ark-bloodhound-cli head-tag line (and the
// blank after it) so the payload reads cleanly into the task doc.
func stripCLIRequestTag(s string) string {
	first, rest, found := strings.Cut(s, "\n")
	if found && strings.HasPrefix(first, "@"+bloodhoundCLIRequestTag) {
		return strings.TrimLeft(rest, "\n")
	}
	return s
}

// cliRequestID extracts the <id> from a tmp://BLOODHOUND-CLI/<id> path — the
// finding cookie and the @ark-bloodhound-cli-result value scope.
func cliRequestID(path string) string {
	return strings.TrimPrefix(path, bloodhoundCLIPrefix)
}
