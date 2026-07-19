package ark

// CRC: crc-Librarian.md | Seq: seq-find-connections.md
// R2313, R2314, R2322, R2325, R2332, R2335, R2336, R2337, R2338

import (
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"net/http"
	"sort"
	"strconv"
	"strings"
	"time"

	lua "github.com/yuin/gopher-lua"
	"github.com/zot/microfts2"
	"go.etcd.io/bbolt"
)

// ConnectionsRequest is a queued find-connections request handed
// to the ark-connections sidecar via the lotto tube.
// CRC: crc-Librarian.md | R2315, R2319
type ConnectionsRequest struct {
	ID             string   `json:"id"`
	ChunkIDs       []uint64 `json:"chunkIDs"`
	TimeoutSeconds int      `json:"timeoutSeconds"`
}

// ConnectionsResult is the sidecar's posted proposal payload.
// CRC: crc-Librarian.md | R2334
type ConnectionsResult struct {
	Themes     []Theme         `json:"themes"`
	SharedTags []SharedTagCand `json:"sharedTags"`
}

// Theme is a short summary spanning some of the pinned chunks.
// CRC: crc-Librarian.md | R2334
type Theme struct {
	Text     string   `json:"text"`
	Evidence []uint64 `json:"evidence"`
}

// SharedTagCand is a tag value the sidecar proposes for the pinned set.
// CRC: crc-Librarian.md | R2334
type SharedTagCand struct {
	Tag      string   `json:"tag"`
	Value    string   `json:"value"`
	Evidence []uint64 `json:"evidence"`
}

// ConnectionsRecord tracks an in-flight request. The orchestrator
// writes the tmp:// doc through the actor — this record holds the
// metadata needed to drive header-tag updates and to enforce the
// deadline timer.
// CRC: crc-Librarian.md | R2319, R2326, R2331, R2590, R2591
type ConnectionsRecord struct {
	ID            string
	ChunkIDs      []uint64
	Inputs        []substrateInput // R2568, R2574
	Started       time.Time
	Deadline      time.Time
	TimeoutDur    time.Duration
	Status        string // pending | working | completed | errored
	Progress      string // fetching | thinking | posting | done
	Elapsed       int    // seconds (rounded)
	Error         string
	Path          string // tmp://connections/<id>.md
	Timer         *time.Timer
	Done          bool
	Mode          string        // "normal" | "turbo" (R2591)
	Purpose       string        // "curate" | "recall" | ... (R2590)
	K             int           // top-K for normal-mode proposal output (R2585)
	Warning       string        // surfaced as @connections-warning (R2588)
	ProposalCount int           // populated on completed write (R2592)
	stop          chan struct{} // closed when the record reaches terminal state
}

// FindConnectionsOpts bundles the optional parameters to FindConnections.
// CRC: crc-Librarian.md | R2323, R2585, R2590, R2591, R2601
type FindConnectionsOpts struct {
	TimeoutSeconds int
	Mode           string // "normal" (default) | "turbo"; R2591, R2601
	Purpose        string // "curate" (default); R2590, R2601
	K              int    // top-K candidates for normal mode (default 20, clamped [1,200]); R2585
}

// ChunkFetchEntry is one row of the --fetch response: chunkID, its
// primary file's ID + path, and the chunk content as a string.
// CRC: crc-Librarian.md | R2316
type ChunkFetchEntry struct {
	ChunkID uint64 `json:"chunkID"`
	FileID  uint64 `json:"fileID"`
	Path    string `json:"path"`
	Content string `json:"content"`
}

const (
	connectionsDocPrefix       = "tmp://connections/"
	connectionsDefaultTimeout  = 60
	connectionsMinTimeout      = 5
	connectionsMaxTimeout      = 300
	connectionsElapsedThrottle = 5 * time.Second
)

// ConnectionsAvailable reports whether ark-connections sidecar work
// has been observed inside the availability window. The first
// `ark connections --wait` consumer (via DrainPendingConnections or
// WaitForConnectionsRequest) sets `connectionsLastWait`; the bridge
// rejects new requests when nothing has been seen recently.
// CRC: crc-Librarian.md | R2320
func (l *Librarian) ConnectionsAvailable() bool {
	if l == nil {
		return false
	}
	l.mu.Lock()
	defer l.mu.Unlock()
	if l.connectionsLastWait.IsZero() {
		return false
	}
	return time.Since(l.connectionsLastWait) < l.connectionsAvailWindow
}

// FindConnections is the unified entry point for normal and turbo
// modes. Normalizes inputs (chunkID, path+range, text), validates at
// enqueue, allocates a request ID, writes the pending tmp:// doc with
// @purpose / @connections-mode headers, then dispatches:
//
//	normal → launches in-process substrate worker, returns ID
//	turbo  → queues for sidecar (existing 1G path), returns ID
//
// CRC: crc-Librarian.md | Seq: seq-find-connections-substrate.md | R2567, R2569, R2570, R2571, R2572, R2573, R2585, R2590, R2591, R2598, R2600, R2601, R2602, R2603
func (l *Librarian) FindConnections(inputs []ConnectionsInput, opts FindConnectionsOpts) (string, error) {
	if l == nil {
		return "", errors.New("agent unavailable")
	}
	mode := opts.Mode
	if mode == "" {
		mode = "normal"
	}
	purpose := opts.Purpose
	if purpose == "" {
		purpose = "curate"
	}
	k := opts.K
	switch {
	case k <= 0:
		k = 20
	case k > 200:
		k = 200
	}
	// Strict chunkID-existence check only for normal mode: the
	// substrate worker needs each chunk's EC vector at enqueue.
	// Turbo preserves R2324's deferred "surface at --fetch" semantics.
	strict := mode == "normal"
	// Normalize through a one-shot copy-bound read view so the enqueue-time
	// FileIDPaths / FileInfoByID reads can't race the write actor's
	// InvalidateCaches (R995, R3163). The substrate worker builds its own
	// copy later in newSubstrateOp; this closes normalizeInputs' own
	// off-actor read, which the #43 substrateOp pilot left on the live db.
	normInputs, chunkIDs, err := normalizeInputs(l.db.withFTS(l.db.fts.Copy()), inputs, strict)
	if err != nil {
		return "", err
	}
	if mode == "turbo" && !l.ConnectionsAvailable() {
		return "", errors.New("agent unavailable")
	}
	timeoutSec := clampTimeout(opts.TimeoutSeconds)
	id := randomID()
	now := time.Now()
	rec := &ConnectionsRecord{
		ID:         id,
		ChunkIDs:   chunkIDs,
		Inputs:     normInputs,
		Started:    now,
		Deadline:   now.Add(time.Duration(timeoutSec) * time.Second),
		TimeoutDur: time.Duration(timeoutSec) * time.Second,
		Status:     "pending",
		Progress:   "fetching",
		Path:       connectionsDocPath(id),
		Mode:       mode,
		Purpose:    purpose,
		K:          k,
		stop:       make(chan struct{}),
	}
	if werr := l.writeConnectionsDoc(rec, true); werr != nil {
		return "", fmt.Errorf("create tmp doc: %w", werr)
	}
	l.mu.Lock()
	l.connectionsResults[id] = rec
	if mode == "turbo" {
		l.pendingConnections = append(l.pendingConnections, ConnectionsRequest{
			ID:             id,
			ChunkIDs:       rec.ChunkIDs,
			TimeoutSeconds: timeoutSec,
		})
		for _, ch := range l.connectionsWaiters {
			select {
			case ch <- struct{}{}:
			default:
			}
		}
		l.connectionsWaiters = nil
		rec.Timer = time.AfterFunc(rec.TimeoutDur, func() {
			l.connectionsTimeout(id)
		})
	}
	stop := rec.stop
	l.mu.Unlock()
	if mode == "turbo" {
		go l.runConnectionsTicker(id, stop)
	} else {
		go l.runSubstrate(rec, normInputs, k)
	}
	return id, nil
}

// FindConnectionsByChunkIDs is the legacy 1G call shape preserved as
// a back-compat sugar layer. Converts each chunkID into a
// ConnectionsInput entry and delegates with mode="turbo". Existing
// callers (mcp.findConnections shim, the 1G sidecar wiring) continue
// to work unchanged. R2568
func (l *Librarian) FindConnectionsByChunkIDs(chunkIDs []uint64, opts FindConnectionsOpts) (string, error) {
	if len(chunkIDs) == 0 {
		return "", errors.New("chunkIDs empty")
	}
	inputs := make([]ConnectionsInput, len(chunkIDs))
	for i, cid := range chunkIDs {
		inputs[i] = ConnectionsInput{ChunkID: cid}
	}
	if opts.Mode == "" {
		opts.Mode = "turbo"
	}
	return l.FindConnections(inputs, opts)
}

// runConnectionsTicker advances @connections-elapsed every
// connectionsElapsedThrottle while the record is still in flight.
// Exits as soon as the record's stop channel closes. R2328.
func (l *Librarian) runConnectionsTicker(id string, stop <-chan struct{}) {
	ticker := time.NewTicker(connectionsElapsedThrottle)
	defer ticker.Stop()
	for {
		select {
		case <-stop:
			return
		case <-ticker.C:
			l.TickConnectionsElapsed(id, "")
		}
	}
}

// DrainPendingConnections atomically drains the queue. Side effect:
// records the drain time as evidence of a live --wait consumer
// (the polled `--wait` endpoint calls this after WaitForConnectionsRequest
// returns true).
// CRC: crc-Librarian.md | R2321
func (l *Librarian) DrainPendingConnections() []ConnectionsRequest {
	l.mu.Lock()
	defer l.mu.Unlock()
	reqs := l.pendingConnections
	l.pendingConnections = nil
	l.connectionsLastWait = time.Now()
	return reqs
}

// WaitForConnectionsRequest blocks until at least one request is
// queued. Records the call time so ConnectionsAvailable can report
// honestly. R2320.
// CRC: crc-Librarian.md | R2321
func (l *Librarian) WaitForConnectionsRequest(timeout time.Duration) bool {
	l.mu.Lock()
	l.connectionsLastWait = time.Now()
	if len(l.pendingConnections) > 0 {
		l.mu.Unlock()
		return true
	}
	ch := make(chan struct{}, 1)
	l.connectionsWaiters = append(l.connectionsWaiters, ch)
	l.mu.Unlock()
	select {
	case <-ch:
		return true
	case <-time.After(timeout):
		return false
	}
}

// finalizeConnectionsDoc performs a terminal transition on a connections
// request — the single door for all three (SetConnectionsError,
// SetConnectionsResult, SetSubstrateResult). It stops the timeout timer and
// elapsed-ticker eagerly, then applies the transition's own fields (mutate),
// flips Done, renders, and writes the tmp:// doc — the Done flip, the render,
// and the write all INSIDE the write-actor closure.
//
// Both key properties fall out of the write actor running closures strictly
// one at a time (the serialization contract — specs/db-write-actor.md, R1058,
// R1067):
//   - Dedup is atomic with no extra lock or flag: the first terminal write to
//     run sees Done==false and wins; any later one sees Done==true and discards.
//   - Done becomes observable only once this write closure is in flight, so a
//     caller polling the record and seeing Done is guaranteed WaitWritesIdle
//     will block for the write before fts.Close — closing the observe-before-
//     durable race that panicked on a nil overlay when a poller tore the DB
//     down between the Done flip and the (then still-queued) write.
//
// mutate sets the transition's fields (Status/Error/…) under l.mu inside the
// closure; render sees the flipped Done (it emits @connections-completed).
// Returns the write result, nil if the record was already terminal, or an
// error if the id is unknown.
// CRC: crc-Librarian.md | Seq: seq-find-connections.md | R2333, R3164
func (l *Librarian) finalizeConnectionsDoc(id string, mutate func(rec *ConnectionsRecord), body ...string) error {
	l.mu.Lock()
	rec := l.connectionsResults[id]
	if rec == nil {
		l.mu.Unlock()
		return errors.New("unknown request id")
	}
	// Stop the timeout timer and elapsed-ticker eagerly so neither enqueues
	// further work behind this terminal write. Idempotent — a losing terminal
	// transition (deduped in the closure below) doing it again is harmless.
	if rec.Timer != nil {
		rec.Timer.Stop()
		rec.Timer = nil
	}
	closeStop(rec)
	l.mu.Unlock()

	ch := make(chan error, 1)
	if err := SyncVoid(l.db, func(db *DB) error {
		db.enqueueWrite(func(_ *microfts2.DB) {
			l.mu.Lock()
			if rec.Done {
				l.mu.Unlock()
				log.Printf("connections: late terminal for %s, discarding (status=%s)", id, rec.Status)
				ch <- nil
				return
			}
			mutate(rec)
			rec.Done = true
			content := renderConnectionsDoc(rec, body...)
			l.mu.Unlock()
			ch <- db.UpdateTmpFile(rec.Path, "markdown", content)
		})
		return nil
	}); err != nil {
		return err
	}
	return <-ch
}

// SetConnectionsResult validates the payload, renders the body, and
// flips the tmp:// doc to completed (or errored on protocol violation).
// CRC: crc-Librarian.md | Seq: seq-find-connections.md | R2317, R2329, R2330, R2333
func (l *Librarian) SetConnectionsResult(id string, result *ConnectionsResult) error {
	if result == nil {
		return l.SetConnectionsError(id, "empty result payload")
	}
	if err := validateEvidence(result); err != nil {
		return l.SetConnectionsError(id, "protocol: "+err.Error())
	}
	return l.finalizeConnectionsDoc(id, func(rec *ConnectionsRecord) {
		rec.Status = "completed"
		rec.Progress = "done"
		rec.Elapsed = int(time.Since(rec.Started).Round(time.Second) / time.Second)
	}, renderConnectionsBody(result))
}

// SetConnectionsError flips the tmp:// doc to errored. Idempotent
// on terminal state. R2318, R2329, R2333.
func (l *Librarian) SetConnectionsError(id, msg string) error {
	return l.finalizeConnectionsDoc(id, func(rec *ConnectionsRecord) {
		rec.Status = "errored"
		rec.Error = msg
		rec.Progress = "done"
		rec.Elapsed = int(time.Since(rec.Started).Round(time.Second) / time.Second)
	})
}

// closeStop closes the record's stop channel exactly once. The caller
// holds l.mu.
func closeStop(rec *ConnectionsRecord) {
	if rec.stop == nil {
		return
	}
	select {
	case <-rec.stop:
		// already closed
	default:
		close(rec.stop)
	}
}

// CleanConnectionsResults caps the in-memory record map by age. Mirrors
// CleanResults for spectral expand.
// CRC: crc-Librarian.md | R2321
func (l *Librarian) CleanConnectionsResults() {
	l.mu.Lock()
	defer l.mu.Unlock()
	if len(l.connectionsResults) <= 100 {
		return
	}
	cutoff := time.Now().Add(-30 * time.Minute)
	for id, rec := range l.connectionsResults {
		if rec.Done && rec.Started.Before(cutoff) {
			delete(l.connectionsResults, id)
		}
	}
}

// BuildFetchPayload assembles the chunk content for an in-flight
// request. Resolves each chunk's primary FileID, looks up the path,
// and reads the chunk text. An unknown chunk ID produces an error
// that names the offending ID. R2316, R2324.
func (l *Librarian) BuildFetchPayload(id string) ([]ChunkFetchEntry, error) {
	l.mu.Lock()
	rec := l.connectionsResults[id]
	l.mu.Unlock()
	if rec == nil {
		return nil, errors.New("unknown request id")
	}

	// R995: read through a private fts.Copy() so this off-actor path
	// resolution (FileIDPaths) and chunk-cache access can't race the write
	// actor's InvalidateCaches. The bbolt reads below (View/ReadCRecord)
	// are MVCC-safe either way. See specs/architecture.md.
	db := l.db.withFTS(l.db.fts.Copy())
	paths, err := db.fts.FileIDPaths()
	if err != nil {
		return nil, fmt.Errorf("file id paths: %w", err)
	}

	out := make([]ChunkFetchEntry, 0, len(rec.ChunkIDs))
	cache := db.fts.NewChunkCache()
	err = db.fts.DB().View(func(txn *bbolt.Tx) error {
		for _, chunkID := range rec.ChunkIDs {
			crec, rerr := db.fts.ReadCRecord(txn, chunkID)
			if rerr != nil || len(crec.FileIDs) == 0 {
				return fmt.Errorf("unknown chunk %d", chunkID)
			}
			// ReadCRecord doesn't populate ChunkID from the value (the
			// key carries it). resolveChunkLocation needs it set so the
			// F-record entry lookup matches.
			crec.ChunkID = chunkID
			path, loc, ok := resolveChunkLocation(crec, paths)
			if !ok {
				return fmt.Errorf("unknown chunk %d", chunkID)
			}
			content, ok := cache.ChunkText(path, loc)
			if !ok {
				return fmt.Errorf("chunk %d content unavailable", chunkID)
			}
			out = append(out, ChunkFetchEntry{
				ChunkID: chunkID,
				FileID:  crec.FileIDs[0].FileID,
				Path:    path,
				Content: string(content),
			})
		}
		return nil
	})
	if err != nil {
		return nil, err
	}
	return out, nil
}

// TickConnectionsElapsed advances @connections-elapsed (and optionally
// @connections-progress) on a working request. Honors the 5-second
// throttle on non-terminal updates.
// CRC: crc-Librarian.md | Seq: seq-find-connections.md | R2328
func (l *Librarian) TickConnectionsElapsed(id, progress string) {
	l.mu.Lock()
	rec := l.connectionsResults[id]
	if rec == nil || rec.Done {
		l.mu.Unlock()
		return
	}
	newElapsed := int(time.Since(rec.Started).Round(time.Second) / time.Second)
	if newElapsed == rec.Elapsed && (progress == "" || progress == rec.Progress) {
		l.mu.Unlock()
		return
	}
	rec.Elapsed = newElapsed
	if progress != "" {
		rec.Progress = progress
	}
	if rec.Status == "pending" {
		rec.Status = "working"
	}
	l.mu.Unlock()
	if err := l.writeConnectionsDoc(rec, false); err != nil {
		log.Printf("connections: tick update for %s: %v", id, err)
	}
}

// connectionsTimeout is the deadline-timer callback. R2331.
func (l *Librarian) connectionsTimeout(id string) {
	if err := l.SetConnectionsError(id, "timeout"); err != nil {
		log.Printf("connections: timeout handler for %s: %v", id, err)
	}
}

// writeConnectionsDoc serializes the record into the tmp:// doc.
// Routes through the write actor (R2333). On initial create, passes
// `create=true` to use AddTmpFile; subsequent calls use UpdateTmpFile.
// An optional `body` overrides the rendered body — used for the
// completed transition to embed the proposal markdown. R2330.
func (l *Librarian) writeConnectionsDoc(rec *ConnectionsRecord, create bool, body ...string) error {
	content := renderConnectionsDoc(rec, body...)
	ch := make(chan error, 1)
	if err := SyncVoid(l.db, func(db *DB) error {
		db.enqueueWrite(func(_ *microfts2.DB) {
			if create {
				_, werr := db.AddTmpFile(rec.Path, "markdown", content)
				ch <- werr
				return
			}
			ch <- db.UpdateTmpFile(rec.Path, "markdown", content)
		})
		return nil
	}); err != nil {
		return err
	}
	return <-ch
}

func clampTimeout(s int) int {
	if s <= 0 {
		return connectionsDefaultTimeout
	}
	if s < connectionsMinTimeout {
		return connectionsMinTimeout
	}
	if s > connectionsMaxTimeout {
		return connectionsMaxTimeout
	}
	return s
}

func connectionsDocPath(id string) string {
	return connectionsDocPrefix + id + ".md"
}

func validateEvidence(r *ConnectionsResult) error {
	for i, t := range r.Themes {
		if len(t.Evidence) == 0 {
			return fmt.Errorf("empty evidence in theme[%d]", i)
		}
	}
	for i, s := range r.SharedTags {
		if len(s.Evidence) == 0 {
			return fmt.Errorf("empty evidence in sharedTag[%d]", i)
		}
	}
	return nil
}

// renderConnectionsDoc emits the full markdown body. Header tags
// come from the record; the body (R2330, R2594) is either rendered
// from a passed-in string (completed state) or omitted.
// CRC: crc-Librarian.md | R2588, R2590, R2591, R2592, R2594
func renderConnectionsDoc(rec *ConnectionsRecord, body ...string) []byte {
	var b strings.Builder
	mode := rec.Mode
	if mode == "" {
		mode = "turbo" // legacy callers default to turbo
	}
	purpose := rec.Purpose
	if purpose == "" {
		purpose = "curate"
	}
	fmt.Fprintf(&b, "@connections-status: %s\n", rec.Status)
	fmt.Fprintf(&b, "@purpose: %s\n", purpose)
	fmt.Fprintf(&b, "@connections-mode: %s\n", mode)
	fmt.Fprintf(&b, "@connections-request-id: %s\n", rec.ID)
	fmt.Fprintf(&b, "@connections-pinned-chunks: %s\n", joinUint64s(rec.ChunkIDs))
	fmt.Fprintf(&b, "@connections-started: %s\n", rec.Started.UTC().Format(time.RFC3339))
	fmt.Fprintf(&b, "@connections-elapsed: %d\n", rec.Elapsed)
	fmt.Fprintf(&b, "@connections-progress: %s\n", rec.Progress)
	if rec.Done {
		fmt.Fprintf(&b, "@connections-completed: %s\n", time.Now().UTC().Format(time.RFC3339))
	}
	if rec.Warning != "" {
		fmt.Fprintf(&b, "@connections-warning: %s\n", rec.Warning)
	}
	if rec.ProposalCount > 0 {
		fmt.Fprintf(&b, "@proposal-count: %d\n", rec.ProposalCount)
	}
	if rec.Error != "" {
		fmt.Fprintf(&b, "@connections-error: %s\n", rec.Error)
	}
	if len(body) > 0 && body[0] != "" {
		b.WriteString("\n")
		b.WriteString(body[0])
	}
	return []byte(b.String())
}

// SetSubstrateResult writes the normal-mode pipeline's output into
// the tmp:// doc and flips status to completed. Mirrors
// SetConnectionsResult but consumes a SubstrateResult instead of the
// sidecar's ConnectionsResult payload.
// CRC: crc-Librarian.md | R2585, R2592, R2593, R2594, R2598, R2599
func (l *Librarian) SetSubstrateResult(id string, result *SubstrateResult) error {
	if result == nil {
		return l.SetConnectionsError(id, "empty substrate result")
	}
	return l.finalizeConnectionsDoc(id, func(rec *ConnectionsRecord) {
		rec.Status = "completed"
		rec.Progress = "done"
		rec.Elapsed = int(time.Since(rec.Started).Round(time.Second) / time.Second)
		rec.Warning = result.Warning
		rec.ProposalCount = len(result.Candidates)
	}, renderSubstrateBody(result))
}

// ListConnections returns snapshot copies of every in-flight
// connections record. Used by `ark connections list`. R2609
func (l *Librarian) ListConnections() []*ConnectionsRecord {
	l.mu.Lock()
	defer l.mu.Unlock()
	out := make([]*ConnectionsRecord, 0, len(l.connectionsResults))
	for _, rec := range l.connectionsResults {
		cp := *rec
		cp.Timer = nil
		cp.stop = nil
		out = append(out, &cp)
	}
	sort.Slice(out, func(i, j int) bool {
		return out[i].Started.After(out[j].Started)
	})
	return out
}

// renderConnectionsBody renders the turbo-mode body. Emits BOTH the
// legacy `## Themes` / `## Shared Tag Candidates` sections (R2330)
// and the new unified `## Proposals` section with @proposal-kind
// rows during the 1G migration window. The duplicate emission is
// removed once apps/ark/curation.lua switches to the unified parser.
// CRC: crc-Librarian.md | R2593, R2595, R2596, R2597
func renderConnectionsBody(r *ConnectionsResult) string {
	var b strings.Builder
	if len(r.Themes) > 0 {
		b.WriteString("## Themes\n\n")
		for _, t := range r.Themes {
			fmt.Fprintf(&b, "- @theme-evidence: %s\n  %s\n\n", joinUint64s(t.Evidence), t.Text)
		}
	}
	if len(r.SharedTags) > 0 {
		b.WriteString("## Shared Tag Candidates\n\n")
		for _, s := range r.SharedTags {
			fmt.Fprintf(&b, "- @shared-tag: %s\n  @shared-tag-value: %s\n  @shared-tag-evidence: %s\n\n",
				s.Tag, s.Value, joinUint64s(s.Evidence))
		}
	}
	if len(r.Themes) > 0 || len(r.SharedTags) > 0 {
		b.WriteString("## Proposals\n\n")
		for _, t := range r.Themes {
			fmt.Fprintf(&b, "- @proposal-kind: theme\n  @proposal-text: %q\n  @proposal-evidence-chunks: %s\n\n",
				t.Text, joinUint64s(t.Evidence))
		}
		for _, s := range r.SharedTags {
			fmt.Fprintf(&b, "- @proposal-kind: shared-tag\n  @proposal-tag: %s\n  @proposal-value: %s\n  @proposal-evidence-chunks: %s\n\n",
				s.Tag, s.Value, joinUint64s(s.Evidence))
		}
	}
	return b.String()
}

// renderSubstrateBody renders the normal-mode body: one ## Proposals
// section with @proposal-kind: tag-name rows, each carrying the
// per-substrate evidence scores. R2593, R2594
func renderSubstrateBody(r *SubstrateResult) string {
	if len(r.Candidates) == 0 {
		return "## Proposals\n\n_no candidates returned_\n"
	}
	var b strings.Builder
	b.WriteString("## Proposals\n\n")
	for _, c := range r.Candidates {
		fmt.Fprintf(&b, "- @proposal-kind: tag-name\n")
		fmt.Fprintf(&b, "  @proposal-value: %q\n", c.Tag)
		fmt.Fprintf(&b, "  @proposal-score: %.4f\n", c.Score)
		fmt.Fprintf(&b, "  @proposal-evidence-chunks: %s\n", joinUint64s(c.SupportingChunks))
		fmt.Fprintf(&b, "  @proposal-evidence-vector-ed: %.4f\n", c.PerSubstrate.VectorED)
		fmt.Fprintf(&b, "  @proposal-evidence-trigram-ed: %.4f\n", c.PerSubstrate.TrigramED)
		fmt.Fprintf(&b, "  @proposal-evidence-vector-ec: %.4f\n", c.PerSubstrate.VectorEC)
		fmt.Fprintf(&b, "  @proposal-evidence-trigram-ec: %.4f\n", c.PerSubstrate.TrigramEC)
		if len(c.MotivatingFiles) > 0 {
			files := make([]string, 0, len(c.MotivatingFiles))
			for _, mf := range c.MotivatingFiles {
				path := mf.Path
				if path == "" {
					path = "<file " + strconv.FormatUint(mf.FileID, 10) + ">"
				}
				files = append(files, fmt.Sprintf("%s:%.4f", path, mf.Score))
			}
			fmt.Fprintf(&b, "  @proposal-motivating-files: %s\n", strings.Join(files, ", "))
		}
		b.WriteString("\n")
	}
	return b.String()
}

func joinUint64s(ids []uint64) string {
	if len(ids) == 0 {
		return ""
	}
	parts := make([]string, len(ids))
	for i, x := range ids {
		parts[i] = strconv.FormatUint(x, 10)
	}
	return strings.Join(parts, ",")
}

// --- HTTP Handlers ---

// HandleConnectionsWait is the lotto tube for ark-connections.
// GET /connections/wait. Blocks until requests are pending, then
// returns the drained queue as JSON. R2315.
// CRC: crc-Librarian.md | Seq: seq-find-connections.md | R2315
func (l *Librarian) HandleConnectionsWait(w http.ResponseWriter, r *http.Request) {
	timeout := 120 * time.Second
	if l.WaitForConnectionsRequest(timeout) {
		writeJSON(w, l.DrainPendingConnections())
		return
	}
	writeJSON(w, []ConnectionsRequest{})
}

// HandleConnectionsFetch returns chunk content for an in-flight
// request. GET /connections/fetch?id=ID. R2316.
// CRC: crc-Librarian.md | Seq: seq-find-connections.md | R2316
func (l *Librarian) HandleConnectionsFetch(w http.ResponseWriter, r *http.Request) {
	id := r.URL.Query().Get("id")
	if id == "" {
		http.Error(w, "missing id", http.StatusBadRequest)
		return
	}
	entries, err := l.BuildFetchPayload(id)
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	writeJSON(w, entries)
}

// HandleConnectionsResult receives the sidecar's proposed result.
// POST /connections/result. Validates evidence and writes the
// completed body, or flips to errored. R2317.
// CRC: crc-Librarian.md | Seq: seq-find-connections.md | R2317
func (l *Librarian) HandleConnectionsResult(w http.ResponseWriter, r *http.Request) {
	var body struct {
		ID     string             `json:"id"`
		Result *ConnectionsResult `json:"result"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	if body.ID == "" {
		http.Error(w, "missing id", http.StatusBadRequest)
		return
	}
	if err := l.SetConnectionsResult(body.ID, body.Result); err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	w.WriteHeader(http.StatusOK)
}

// HandleConnectionsError flips an in-flight request's tmp:// doc to
// errored. POST /connections/error. R2318.
// CRC: crc-Librarian.md | Seq: seq-find-connections.md | R2318
func (l *Librarian) HandleConnectionsError(w http.ResponseWriter, r *http.Request) {
	var body struct {
		ID      string `json:"id"`
		Message string `json:"message"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	if body.ID == "" {
		http.Error(w, "missing id", http.StatusBadRequest)
		return
	}
	if body.Message == "" {
		body.Message = "unspecified error"
	}
	if err := l.SetConnectionsError(body.ID, body.Message); err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	w.WriteHeader(http.StatusOK)
}

// ConnectionRecordSnapshot returns a copy of the in-flight record for
// the given request ID, or nil if absent. Tests use this to inspect
// state without holding the librarian mutex.
func (l *Librarian) ConnectionRecordSnapshot(id string) *ConnectionsRecord {
	l.mu.Lock()
	defer l.mu.Unlock()
	rec := l.connectionsResults[id]
	if rec == nil {
		return nil
	}
	cp := *rec
	cp.Timer = nil
	return &cp
}

// HandleConnectionsFind is the HTTP entry point for `ark connections find`.
// POST /connections/find. Body: {inputs, opts}. Returns {requestID, path}.
// R2567, R2604
func (l *Librarian) HandleConnectionsFind(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Inputs []ConnectionsInput  `json:"inputs"`
		Opts   FindConnectionsOpts `json:"opts"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	id, err := l.FindConnections(body.Inputs, body.Opts)
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	writeJSON(w, map[string]string{
		"requestID": id,
		"path":      connectionsDocPath(id),
	})
}

// luaTableToConnectionsInputs converts a Lua argument table to
// []ConnectionsInput. Accepts both the sugar form (bare integer
// array of chunkIDs, the 1G call shape) and the typed form (array
// of {chunkID|path+range|text} tables). R2568
func luaTableToConnectionsInputs(arr *lua.LTable) []ConnectionsInput {
	out := make([]ConnectionsInput, 0, arr.Len())
	arr.ForEach(func(_, v lua.LValue) {
		switch x := v.(type) {
		case lua.LNumber:
			if x >= 0 {
				out = append(out, ConnectionsInput{ChunkID: uint64(x)})
			}
		case *lua.LTable:
			in := ConnectionsInput{}
			if cid, ok := x.RawGetString("chunkID").(lua.LNumber); ok && cid > 0 {
				in.ChunkID = uint64(cid)
			}
			if p, ok := x.RawGetString("path").(lua.LString); ok {
				in.Path = string(p)
			}
			if r, ok := x.RawGetString("range").(lua.LString); ok {
				in.Range = string(r)
			}
			if t, ok := x.RawGetString("text").(lua.LString); ok {
				in.Text = string(t)
			}
			if in.ChunkID != 0 || in.Path != "" || in.Text != "" {
				out = append(out, in)
			}
		}
	})
	return out
}

// HandleConnectionsList is the HTTP entry point for `ark connections list`.
// GET /connections/list. Returns a JSON array of public-shape records.
// R2609
func (l *Librarian) HandleConnectionsList(w http.ResponseWriter, r *http.Request) {
	recs := l.ListConnections()
	type pubRec struct {
		ID            string    `json:"id"`
		Mode          string    `json:"mode"`
		Purpose       string    `json:"purpose"`
		Status        string    `json:"status"`
		Started       time.Time `json:"started"`
		Elapsed       int       `json:"elapsed"`
		Path          string    `json:"path"`
		ProposalCount int       `json:"proposalCount,omitempty"`
		Error         string    `json:"error,omitempty"`
		Warning       string    `json:"warning,omitempty"`
	}
	out := make([]pubRec, 0, len(recs))
	for _, rec := range recs {
		out = append(out, pubRec{
			ID:            rec.ID,
			Mode:          rec.Mode,
			Purpose:       rec.Purpose,
			Status:        rec.Status,
			Started:       rec.Started,
			Elapsed:       rec.Elapsed,
			Path:          rec.Path,
			ProposalCount: rec.ProposalCount,
			Error:         rec.Error,
			Warning:       rec.Warning,
		})
	}
	writeJSON(w, out)
}
