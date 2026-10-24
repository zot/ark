package ark

// CRC: crc-RecallWatcher.md | Seq: seq-bloodhound-cli.md | R3020, R3023, R3024, R3025, R3030, R3031, R3032

// The watcher-as-Fixer for external-CLI directed hunts (bloodhound-cli.md, S2).
// Distinct from the OnAppend-driven ambient/in-session paths: this subscribes to
// two pubsub tags and routes the request doc tmp://BLOODHOUND-CLI/<id> across the
// pipeline via the tag baton. All scheduling is deterministic Go — the Fixer is
// the deciding go-between (the Mediator/Fixer pattern), never a language model.

import (
	"fmt"
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

// recallPoolClass is the Luhmann-managed class name for the ambient-recall
// secretary pool (config under [luhmann].class.recall). A second class rather
// than a second customer of the bloodhound pool: classes never share slots, so
// a recollection cannot occupy a secretary a directed hunt needs, and the two
// limits are tuned for arrival rates that differ by orders of magnitude — a
// deliberate watermark against every turn boundary of every enabled session.
// R3298
const recallPoolClass = "recall"

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
	// bloodhoundStrandTTL is the reap deadline for a **watermark** request,
	// whose emitting session is still blocked on `listen`. request_ttl_seconds
	// is tuned for a CLI client that hit --timeout and is already gone, where
	// reaping late costs nothing; a blocked assistant pays for every second, so
	// it learns sooner. Comfortably above bloodhoundReissueThreshold, so the
	// re-issued stand-up has room to land and a merely slow orchestrator is not
	// mistaken for an absent one (R3293).
	bloodhoundStrandTTL = 180 * time.Second
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
// arrives: its submit time (the reap clock, R3041), whether the client asked to
// skip curation (`--raw` → curate:false, R3038), and — for a hunt that entered
// from a `<BLOODHOUND>` watermark rather than the CLI — where the raw result
// must be delivered (R3281). Kept in memory because the request doc's body is
// overwritten at the enhance/close hops, so the intent cannot survive there.
//
// origin is the discriminator: empty means the external CLI (result goes to
// @ark-bloodhound-cli-result: <id>, the waiting client), non-empty is the
// session ID that emitted the watermark (result goes to that session's
// @ark-bloodhound-result finding channel, indistinguishable from one its own
// secretary would have produced). bid/clue carry what the finding doc's header
// needs, since the pool path bypasses the builder that would otherwise hold them.
// CRC: crc-RecallWatcher.md | R3038, R3041, R3281
type cliRequest struct {
	submitted time.Time
	raw       bool
	origin    string // R3281: emitting session for a watermark hunt; "" for CLI
	bid       uint64 // R3281: the <B> the finding doc is keyed by
	clue      string // R3281: echoed verbatim into the `## Finding:` header
	notags    bool   // R3110/R3283: the watermark's own `<BLOODHOUND notags>` opt-out
}

// proposesTags reports whether this hunt's secretary should propose connecting
// tags (R3283): session-delegated hunts do, CLI-delegated ones never do, and a
// watermark's own `notags` opt-out (R3110) overrides on either server.
func (r *cliRequest) proposesTags() bool {
	return r != nil && r.origin != "" && !r.notags
}

// secretaryPool is one class's roster of pool secretaries plus its pending-work
// queue and in-flight doc→nonce map. One instance per class (R3298); the
// watcher holds them in `pools`. Everything under mu. In-memory; a server
// bounce drops it and the CLI's --wait re-drives.
//
// `requests` is bloodhound-only: it records a hunt's submit time (the reap
// clock) and its raw/curated intent. The recall class leaves it empty and has
// no analogue — a recollection has no external client to strand and no
// raw/curated fork. R3023, R3024, R3038, R3041
type secretaryPool struct {
	mu          sync.Mutex
	secretaries map[uint64]*poolSec    // nonce → state
	inflight    map[string]uint64      // work-doc path → the nonce running it
	pending     []string               // work-doc paths awaiting a free secretary
	requests    map[string]*cliRequest // bloodhound only: submit time + raw intent
}

func newSecretaryPool() *secretaryPool {
	return &secretaryPool{
		secretaries: make(map[uint64]*poolSec),
		inflight:    make(map[string]uint64),
		requests:    make(map[string]*cliRequest),
	}
}

// pickFreeLocked returns any not-busy secretary, or nil. Caller holds mu.
func (p *secretaryPool) pickFreeLocked() *poolSec {
	for _, s := range p.secretaries {
		if !s.busy {
			return s
		}
	}
	return nil
}

// PoolSize reports the pool-secretary roster count for the managed-PTY status
// (R3124): how many pool secretaries the hosted Luhmann is running, summed
// across every class (R3298) — not the bloodhound roster alone.
func (w *RecallWatcher) PoolSize() int {
	if w == nil {
		return 0
	}
	// R3298: sum across every class. Reading only the bloodhound roster made a
	// live recall secretary report as zero, which is how this was found.
	total := 0
	for _, pool := range w.pools {
		pool.mu.Lock()
		total += len(pool.secretaries)
		pool.mu.Unlock()
	}
	return total
}

// PoolNonces returns a snapshot of every pool secretary, paired with the class
// whose roster holds it, so a graceful stop can record each one's exit and
// deregister it before the pty teardown takes it down (R3125, R3298).
func (w *RecallWatcher) PoolNonces() []PoolMember {
	if w == nil {
		return nil
	}
	// R3298: every class, and each nonce paired with the class that owns it —
	// the caller must exit-record and deregister against the right roster, and a
	// bloodhound-only enumeration silently stranded recall secretaries as ghosts
	// on a graceful stop (the failure R3125 exists to prevent).
	var members []PoolMember
	for class, pool := range w.pools {
		pool.mu.Lock()
		for nonce := range pool.secretaries {
			members = append(members, PoolMember{Class: class, Nonce: nonce})
		}
		pool.mu.Unlock()
	}
	return members
}

// IsPoolNonce reports whether `nonce` sits in any class's roster (R3322). That
// membership is what distinguishes a pool secretary, whose tube is the composite
// `<parent>-<nonce>`, from a session's own secretary, whose tube is the bare
// parent session — so it is the one bit the address derivation needs.
// CRC: crc-RecallAgentBuilder.md | R3322
func (w *RecallWatcher) IsPoolNonce(nonce uint64) bool {
	if w == nil {
		return false
	}
	for _, pool := range w.pools {
		pool.mu.Lock()
		_, ok := pool.secretaries[nonce]
		pool.mu.Unlock()
		if ok {
			return true
		}
	}
	return false
}

// PoolMember identifies one pool secretary by the class whose roster holds it.
// The class is carried rather than assumed because a nonce alone cannot say
// which roster to deregister from. R3298
type PoolMember struct {
	Class string
	Nonce uint64
}

// PoolBusy reports whether the pool has no free secretary and is at pool_max — a
// submit-time snapshot for the CLI's fail-fast-without-`--wait` gate (R3022).
func (w *RecallWatcher) PoolBusy() bool {
	if w == nil {
		return false
	}
	w.pool.mu.Lock()
	defer w.pool.mu.Unlock()
	return w.pool.pickFreeLocked() == nil && len(w.pool.secretaries) >= w.poolMax(bloodhoundPoolClass)
}

// poolMax reads the live [luhmann].class.<class>.pool_max (R3017). Per class,
// so the two rosters bound independently (R3298, R3301).
func (w *RecallWatcher) poolMax(class string) int {
	if w == nil || w.db == nil {
		return 3
	}
	return w.db.Config().Luhmann.EffectivePoolMax(class)
}

// cooldown reads the live [luhmann].class.<class>.cooldown_seconds (R3018) as
// a Duration — the warmth window an idle secretary must exceed before pruning.
// Per class (R3301). R3298
func (w *RecallWatcher) cooldown(class string) time.Duration {
	if w == nil || w.db == nil {
		return 600 * time.Second
	}
	return time.Duration(w.db.Config().Luhmann.EffectiveCooldownSeconds(class)) * time.Second
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
	if !w.subscribeFixer() {
		return
	}
	go w.fixerLoop()
	go w.pruneLoop() // R3019: autoscale down — retire secretaries idle past cooldown
}

// subscribeFixer subscribes the Fixer session to the request and return tags.
// Reports false if a tag fails to parse. R3023, R3024
func (w *RecallWatcher) subscribeFixer() bool {
	var subs []*TagSub
	for _, tag := range []string{bloodhoundCLIRequestTag, bloodhoundCLIReturnTag} {
		p, err := ParseMatchSyntax(tag)
		if err != nil {
			log.Printf("bloodhound-cli: bad match %q: %v", tag, err)
			return false
		}
		subs = append(subs, &TagSub{Kind: TagSubChunk, Predicate: p})
	}
	w.db.pubsub.Subscribe(bloodhoundCLIFixerSession, subs)
	return true
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
// Runs per class (R3298, R3301), each against its own cooldown_seconds, and the
// stop directive names the class it came from — Luhmann must not stop a
// bloodhound secretary because a recall one went cold.
//
// A secretary serving ambient is `busy` for exactly as long as it holds the doc
// (R3300), so this cannot retire one mid-curation. That is the whole of the
// protection: before ambient was routed through the watcher it never set `busy`
// at all, so a steadily-working secretary read as idle here and was stopped.
func (w *RecallWatcher) prune() {
	if w.luhmann == nil || w.luhmann.LuhmannOwner() == "" {
		return
	}
	type stopReq struct {
		class string
		nonce uint64
	}
	var stop []stopReq
	for class, pool := range w.pools {
		cooldown := w.cooldown(class)
		pool.mu.Lock()
		for nonce, s := range pool.secretaries {
			if !s.busy && !s.idleSince.IsZero() && time.Since(s.idleSince) > cooldown {
				stop = append(stop, stopReq{class: class, nonce: nonce})
				delete(pool.secretaries, nonce)
			}
		}
		pool.mu.Unlock()
	}
	for _, r := range stop {
		// R3019: one stop directive per over-cooldown secretary; Luhmann stops
		// its Task and exit-records it.
		w.luhmann.EnqueueLuhmann(LuhmannWork{Kind: "directive", Directive: "stop", Class: r.class, Nonce: r.nonce})
	}
}

// fixerLoop drains the two tags and dispatches each event to the request or
// return handler. Runs for the server lifetime. R3023, R3024
func (w *RecallWatcher) fixerLoop() {
	for {
		events := w.db.pubsub.Listen(bloodhoundCLIFixerSession, bloodhoundCLIListenWindow)
		for _, ev := range events {
			w.dispatchFixerEvent(ev)
		}
	}
}

// dispatchFixerEvent sends one Fixer event to the request or return handler.
// R3023, R3024
func (w *RecallWatcher) dispatchFixerEvent(ev Event) {
	switch ev.Tag {
	case bloodhoundCLIRequestTag:
		w.onBloodhoundCLIRequest(ev.Path)
	case bloodhoundCLIReturnTag:
		w.onBloodhoundCLIReturn(ev.Path)
	}
}

// onBloodhoundCLIRequest handles a new hunt (Fixer request trigger, R3023): gate
// on a live Luhmann (R3020), enqueue the request, push a stand-up directive if
// the pool has room, then pump pending hunts to free secretaries.
func (w *RecallWatcher) onBloodhoundCLIRequest(path string) {
	if strings.HasPrefix(cliRequestID(path), watermarkHuntPrefix) {
		// R3552: a watermark hunt did its own bookkeeping, with its origin, when
		// it wrote this doc. Recording it here as a CLI hunt would drop the origin
		// and queue it twice. Decided by the id, so it holds whichever goroutine
		// runs first.
		return
	}
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
	needStandUp := w.pool.pickFreeLocked() == nil && len(w.pool.secretaries) < w.poolMax(bloodhoundPoolClass)
	w.pool.mu.Unlock()
	Logv(1, "bloodhound-cli: request %s (raw=%v stand-up=%v)", path, raw, needStandUp)
	if needStandUp {
		// R3023: none free, room in the pool → ask Luhmann to stand one up.
		w.luhmann.EnqueueLuhmann(LuhmannWork{Kind: "directive", Directive: "stand-up", Class: bloodhoundPoolClass})
	}
	w.pump()
}

// watermarkHuntID is the request id for a pool-served watermark. The `wm-`
// prefix keeps it out of the CLI's integer-nonce id space, so the two origins
// can never collide on a request-doc path.
// CRC: crc-RecallWatcher.md | R3280
func watermarkHuntID(sessionID string, bid uint64) string {
	return watermarkHuntPrefix + sessionID + "-" + strconv.FormatUint(bid, 10)
}

// watermarkHuntPrefix marks a request id as a pool-served watermark's. R3552
const watermarkHuntPrefix = "wm-"

// enqueueWatermarkHunt routes a recognized `<BLOODHOUND>` watermark to Luhmann's
// pool when the emitting session has no secretary of its own (R3280). It reuses
// the CLI hunt pipeline wholesale — same request-doc namespace, same Fixer
// scheduling and enhance-and-route, same raw return hop — and differs only in
// *origin* and *destination*, both of which ride the in-memory cliRequest record
// rather than the doc body (which enhance overwrites).
//
// The body is written in the Fixer's own TERMS shape so the existing readers
// recover the hunt unchanged: `curate: false` marks it raw (R3281) for the
// return branch, and the watermark's already-anchored globs go out as
// filter-files/exclude-files lines for scopeOf. clueOf then strips all of it
// back off when seeding.
// CRC: crc-RecallWatcher.md | Seq: seq-recall-watcher.md#4 | R3280, R3281
func (w *RecallWatcher) enqueueWatermarkHunt(sessionID string, bid uint64, req bloodhoundReq) error {
	if w.db == nil {
		return nil
	}
	id := watermarkHuntID(sessionID, bid)
	path := bloodhoundCLIPrefix + id
	var b strings.Builder
	fmt.Fprintf(&b, "@%s: %s\n\ncurate: false\n", bloodhoundCLIRequestTag, id)
	for _, g := range req.filterFiles {
		fmt.Fprintf(&b, "filter-files: %s\n", g)
	}
	for _, g := range req.excludeFiles {
		fmt.Fprintf(&b, "exclude-files: %s\n", g)
	}
	fmt.Fprintf(&b, "%s\n", strings.TrimRight(req.payload, "\n"))
	if err := SyncVoid(w.db, func(db *DB) error {
		_, e := db.AddTmpFile(path, "markdown", []byte(b.String()))
		return e
	}); err != nil {
		return err
	}

	w.pool.mu.Lock()
	w.pool.pending = append(w.pool.pending, path)
	w.pool.requests[path] = &cliRequest{
		submitted: time.Now(),
		raw:       true, // R3281: a watermark is always raw — the emitter curates
		origin:    sessionID,
		bid:       bid,
		clue:      clueOf(req.payload),
		notags:    req.notags, // R3110: `<BLOODHOUND notags>` rides to the pool too
	}
	needStandUp := w.pool.pickFreeLocked() == nil && len(w.pool.secretaries) < w.poolMax(bloodhoundPoolClass)
	w.pool.mu.Unlock()

	Logv(1, "bloodhound: watermark session=%s B=%d → pool (stand-up=%v)", sessionID, bid, needStandUp)
	if needStandUp && w.luhmann != nil {
		w.luhmann.EnqueueLuhmann(LuhmannWork{Kind: "directive", Directive: "stand-up", Class: bloodhoundPoolClass})
	}
	w.pump()
	return nil
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
		if err := w.relayRawResult(path, req); err != nil {
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
// rawDestination decides where a raw hunt's findings are delivered and renders
// the doc body for that destination — the one place the two origins diverge.
// An empty path means "deliver nowhere"; the caller still drops the request doc
// so a departed consumer leaks nothing.
// CRC: crc-RecallWatcher.md | R3040, R3281
func (w *RecallWatcher) rawDestination(id string, req *cliRequest, findings string) (path, body string) {
	if req == nil || req.origin == "" {
		// CLI origin: wake the waiting client on its own value-scoped result tag.
		return bloodhoundCLIResultPath(id), "@" + bloodhoundCLIResultTag + ": " + id + "\n\n" + findings
	}
	if w.db.pubsub != nil && w.db.pubsub.SubscriberCount("ark-bloodhound-result", req.origin) == 0 {
		// R2808: parity; the consumer dropped mid-hunt, so there is nothing to wake.
		Logv(1, "bloodhound: watermark relay: no subscriber for session=%s", req.origin)
		return "", ""
	}
	// R3281: watermark origin. Same doc path, tag, and `## Finding:` header
	// closeBloodhound writes, so the consumer cannot tell a pool-served hunt from
	// one the session's own secretary ran — no `listen` change is needed.
	return bloodhoundFindingPath(req.origin, req.bid),
		bloodhoundFindingHeader(req.origin, req.clue) + findings
}

func (w *RecallWatcher) relayRawResult(path string, req *cliRequest) error {
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

	outPath, body := w.rawDestination(id, req, findings)
	if outPath != "" {
		if err := SyncVoid(w.db, func(db *DB) error {
			_, e := db.AddTmpFile(outPath, "markdown", []byte(body))
			return e
		}); err != nil {
			return err
		}
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
// reapDeadline returns how long a pending request may sit before the sweep
// reaps it. The discriminator is **who is still waiting** (R3293): a CLI
// request's client hit `--timeout` and exited long ago, so a generous TTL costs
// nothing and protects a slow-but-live client from being reaped out from under
// itself; a watermark's emitting session is still blocked on `listen`, and every
// second past the point of hope is spent by someone.
// CRC: crc-RecallWatcher.md | R3041, R3293
func reapDeadline(req *cliRequest, cliTTL time.Duration) time.Duration {
	if req.origin == "" {
		return cliTTL
	}
	// A configured CLI TTL shorter than the strand deadline wins: the operator
	// asked for prompt reaping, and a watermark should never outlive a CLI
	// request submitted at the same moment.
	if cliTTL < bloodhoundStrandTTL {
		return cliTTL
	}
	return bloodhoundStrandTTL
}

// CRC: crc-RecallWatcher.md | Seq: seq-bloodhound-cli.md#2.1 | R3041, R3042
func (w *RecallWatcher) sweepRequests() {
	if w == nil {
		return
	}
	// 2.1.1: a free secretary may have been missed by an earlier pump.
	w.pump()

	ttl := w.requestTTL()
	var reap []string
	// R3293: a reaped watermark has a live session still blocked on `listen`,
	// so it must be told. Collected under the lock, reported after it.
	var stranded []cliRequest
	standUp := false
	w.pool.mu.Lock()
	// 2.1.2 reap: a pending request past its deadline. For a CLI request that
	// means the client hit --timeout and exited; for a watermark it means
	// nothing ever stood up to serve it. Drop it from requests + pending; its
	// doc is removed below.
	kept := w.pool.pending[:0]
	for _, path := range w.pool.pending {
		req := w.pool.requests[path]
		if req != nil && time.Since(req.submitted) > reapDeadline(req, ttl) {
			reap = append(reap, path)
			if req.origin != "" {
				stranded = append(stranded, *req)
			}
			delete(w.pool.requests, path)
			continue
		}
		kept = append(kept, path)
	}
	w.pool.pending = kept
	// 2.1.3 re-issue: any surviving request pending past the threshold, with room
	// in the pool, earns one stand-up to recover a directive Luhmann dropped.
	if w.pool.pickFreeLocked() == nil && len(w.pool.secretaries) < w.poolMax(bloodhoundPoolClass) {
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
	// R3293: report the strand. The dispatch-time conditions all held when this
	// hunt was accepted, so the reason names what actually happened instead —
	// otherwise R3282's silence is merely moved from the gate to the reaper,
	// where it is harder to spot because the hunt appeared to be accepted.
	for _, req := range stranded {
		w.emitNotServedReason(req.origin, req.bid, req.clue,
			"this hunt was queued for Luhmann's pool, but no secretary ever became available to run it")
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
	// R3283: proposals are origin-dependent. A session-delegated hunt (watermark
	// origin) proposes — its recommends ride the findings back to an assistant
	// that can winnow them. A CLI-delegated hunt stays findings-only: a script has
	// no winnower, and the curated path's consumer is Luhmann, whose job is
	// refining findings rather than judging tag candidates.
	w.pool.mu.Lock()
	propose := w.pool.requests[path].proposesTags()
	w.pool.mu.Unlock()

	filter, exclude := scopeOf(payload)
	req := bloodhoundReq{payload: payload, notags: !propose, filterFiles: filter, excludeFiles: exclude}
	seed := w.renderSeed(req)
	body := buildSearchTask(composite, cliRequestID(path), seed, req)
	// UpdateTmpFile (not Add) — the request doc already exists (the CLI created
	// it); this overwrites it in one write-actor flush, re-indexing the head tag
	// from @ark-bloodhound-cli to @ark-secretary-work=<composite> (the baton flip).
	return SyncVoid(w.db, func(db *DB) error {
		return db.UpdateTmpFile(path, "markdown", []byte(body))
	})
}

// RegisterPoolSecretary records a nonce as a pending pool secretary of `class`
// (R3033, R3299) and pumps any waiting work to it. Called by the reserve-nonce
// --luhmann handler: the reservation doubles as pool registration, so the Fixer
// learns the nonce Luhmann will spawn with. The class is declared by the
// reserving caller, not inferred — an unknown class is dropped rather than
// silently defaulted, since defaulting would put a recall secretary in the
// bloodhound roster and the symptom (work never served) would appear far from
// the cause. Idempotent.
func (w *RecallWatcher) RegisterPoolSecretary(class string, nonce uint64) {
	if w == nil {
		return
	}
	pool, ok := w.pools[class]
	if !ok {
		log.Printf("recall-watcher: reserve-nonce for unknown pool class %q (nonce %d) ignored", class, nonce)
		return
	}
	pool.mu.Lock()
	if _, exists := pool.secretaries[nonce]; !exists {
		pool.secretaries[nonce] = &poolSec{nonce: nonce, idleSince: time.Now()}
	}
	pool.mu.Unlock()
	switch class {
	case bloodhoundPoolClass:
		w.pump()
	case recallPoolClass:
		w.pumpCuration() // R3301: route a doc that was waiting on this stand-up
	}
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
	if w == nil {
		return
	}
	pool, ok := w.pools[class]
	if !ok {
		return // another class entirely (or none) — not ours to reconcile
	}
	switch kind {
	case "exit", "crash", "quit-early":
	default:
		return // spawn/respawn (or anything non-terminal) never deregisters
	}
	pool.mu.Lock()
	delete(pool.secretaries, nonce)
	var orphaned []string
	for path, n := range pool.inflight {
		if n == nonce {
			delete(pool.inflight, path)
			orphaned = append(orphaned, path)
		}
	}
	if class == recallPoolClass {
		// R3304: a curation doc whose secretary died is not lost — requeue it so
		// the next stand-up takes it. Dropping it here would be a silent loss,
		// and nobody is blocked to notice.
		pool.pending = append(pool.pending, orphaned...)
	}
	pool.mu.Unlock()
	if class == recallPoolClass && len(orphaned) > 0 {
		w.pumpCuration()
	}
}

// bloodhoundSeedKCap bounds the per-idea seed budget so a many-paragraph clue
// can't blow the seed up (R3045).
const bloodhoundSeedKCap = 30

// seedMetaKeys are the leading `key:` metadata fields clueOf strips before the
// clue body — they shape the hunt (crank handle) but are not search ideas
// (R3044). `curate:` is the #30 raw marker; `filter-files:`/`exclude-files:`
// are the hunt's file scope (R3191, repeatable); the others are the CLI's flags.
var seedMetaKeys = []string{"scope:", "depth:", "want:", "curate:", "filter-files:", "exclude-files:"}

// scopeKeys maps a scope metadata key to whether it is the positive side.
var scopeKeys = map[string]bool{"filter-files:": true, "exclude-files:": false}

// scopeOf pulls a CLI hunt's file scope out of the leading metadata lines the
// CLI wrote (R3191). Both keys repeat, one glob per line; the CLI anchored them
// against its own cwd before submitting (R3193), so these arrive absolute and
// are used as-is.
// CRC: crc-RecallWatcher.md | R3191, R3193
func scopeOf(payload string) (filter, exclude []string) {
	for _, line := range strings.Split(payload, "\n") {
		t := strings.TrimSpace(line)
		lower := strings.ToLower(t)
		for key, positive := range scopeKeys {
			if !strings.HasPrefix(lower, key) {
				continue
			}
			glob := strings.TrimSpace(t[len(key):])
			if glob == "" {
				continue
			}
			if positive {
				filter = append(filter, glob)
			} else {
				exclude = append(exclude, glob)
			}
		}
	}
	return filter, exclude
}

// clueOf extracts the searchable clue from a bloodhound payload: every hunt-
// metadata line (seedMetaKeys) is dropped wherever it sits, and the rest, trimmed,
// is the clue. The CLI writes metadata first, but a watermark is prose, so a
// `depth:` or `want:` line can just as well follow the clue; left in, it becomes
// a seed paragraph that matches the documents about hunting (R3576). A paragraph
// that held nothing else leaves only blank lines, which splitParagraphs drops.
// Free-form in-session prose has no metadata, so it is returned whole.
// CRC: crc-RecallWatcher.md | R3044, R3576
func clueOf(payload string) string {
	lines := strings.Split(payload, "\n")
	kept := lines[:0]
	for _, line := range lines {
		t := strings.ToLower(strings.TrimSpace(line))
		if slices.ContainsFunc(seedMetaKeys, func(k string) bool { return strings.HasPrefix(t, k) }) {
			continue
		}
		kept = append(kept, line)
	}
	return strings.TrimSpace(strings.Join(kept, "\n"))
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
// its own matches. The hunt's file scope rides into RecallOpts, where it filters
// at admission so the seed's top-K is computed within the scope rather than
// trimmed to it afterwards (R3185, R3186). A failed seed is not fatal — the
// empty-seed note still dispatches the hunt. R3006, R3007, R3043
func (w *RecallWatcher) renderSeed(req bloodhoundReq) string {
	inputs, k := seedInputs(req.payload)
	result, err := w.librarian.Recall(
		inputs,
		RecallOpts{
			K:              k,
			IncludeContent: true,
			KeepTagless:    true,
			FilterFiles:    req.filterFiles,
			ExcludeFiles:   req.excludeFiles,
		},
	)
	if err != nil {
		log.Printf("bloodhound-cli: seed Recall failed: %v", err)
		result = nil
	}
	return renderBloodhoundSeed(result, req)
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
