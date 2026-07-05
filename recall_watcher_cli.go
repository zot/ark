package ark

// CRC: crc-RecallWatcher.md | Seq: seq-bloodhound-cli.md | R3020, R3023, R3024, R3025, R3030, R3031, R3032

// The watcher-as-Fixer for external-CLI directed hunts (bloodhound-cli.md, S2).
// Distinct from the OnAppend-driven ambient/in-session paths: this subscribes to
// two pubsub tags and routes the request doc tmp://BLOODHOUND-CLI/<id> across the
// pipeline via the tag baton. All scheduling is deterministic Go — the Fixer is
// the deciding go-between (the Mediator/Fixer pattern), never a language model.

import (
	"log"
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
	// idle past their cooldown to retire (R3019). Independent of the per-class
	// cooldown_seconds, which is the warmth window each secretary must exceed.
	bloodhoundPruneInterval = 30 * time.Second
)

// poolSec is one pool secretary's roster entry, keyed by its reserved nonce
// (R3032). busy while running a hunt; idleSince stamps the cooldown clock on
// return to idle (R3024).
type poolSec struct {
	nonce     uint64
	busy      bool
	idleSince time.Time
}

// cliPool is the watcher's roster of CLI-bloodhound pool secretaries plus the
// pending-hunt queue and the in-flight path→nonce map. Everything under mu.
// In-memory; a server bounce drops it and the CLI's --wait re-drives. R3023, R3024
type cliPool struct {
	mu          sync.Mutex
	secretaries map[uint64]*poolSec // nonce → state
	inflight    map[string]uint64   // request-doc path → the nonce running it
	pending     []string            // request-doc paths awaiting a free secretary
}

func newCLIPool() *cliPool {
	return &cliPool{
		secretaries: make(map[uint64]*poolSec),
		inflight:    make(map[string]uint64),
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
		return 120 * time.Second
	}
	return time.Duration(w.db.Config().Luhmann.EffectiveCooldownSeconds(bloodhoundPoolClass)) * time.Second
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
	w.pool.mu.Lock()
	w.pool.pending = append(w.pool.pending, path)
	needStandUp := w.pool.pickFreeLocked() == nil && len(w.pool.secretaries) < w.poolMax()
	w.pool.mu.Unlock()
	Logv(1, "bloodhound-cli: request %s (stand-up=%v)", path, needStandUp)
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
	w.pool.mu.Unlock()
	if w.luhmann != nil {
		// R3024, R3025: hand the raw finding to Luhmann for curation (in-process,
		// no tag hop). If the queue is full the curation is dropped — the CLI's
		// --timeout bounds the wait; a bounce re-drives the whole hunt.
		w.luhmann.EnqueueLuhmann(LuhmannWork{Kind: "curation", Path: path})
	}
	w.pump()
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
	seed := w.renderSeed(payload)
	body := buildSearchTask(composite, cliRequestID(path), payload, seed)
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

// renderSeed runs the hypergraph-aware combined search on the payload and
// renders the ## Recall seed block. Shared by dispatchBloodhound and the CLI
// Fixer. A failed seed is not fatal — the empty-seed note still dispatches the
// hunt. R3006, R3007
func (w *RecallWatcher) renderSeed(payload string) string {
	result, err := w.librarian.Recall(
		[]ConnectionsInput{{Text: payload}},
		RecallOpts{K: bloodhoundSeedK, IncludeContent: true, KeepTagless: true},
	)
	if err != nil {
		log.Printf("bloodhound-cli: seed Recall failed: %v", err)
		result = nil
	}
	return renderBloodhoundSeed(result)
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
