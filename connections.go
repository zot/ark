package ark

// CRC: crc-Librarian.md | Seq: seq-find-connections.md
// R2313, R2314, R2322, R2325, R2332, R2335, R2336, R2337, R2338

import (
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/bmatsuo/lmdb-go/lmdb"
	"github.com/zot/microfts2"
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
// CRC: crc-Librarian.md | R2319, R2326, R2331
type ConnectionsRecord struct {
	ID         string
	ChunkIDs   []uint64
	Started    time.Time
	Deadline   time.Time
	TimeoutDur time.Duration
	Status     string // pending | working | completed | errored
	Progress   string // fetching | thinking | posting | done
	Elapsed    int    // seconds (rounded)
	Error      string
	Path       string // tmp://connections/<id>.md
	Timer      *time.Timer
	Done       bool
	stop       chan struct{} // closed when the record reaches terminal state
}

// FindConnectionsOpts bundles the optional parameters to FindConnections.
// CRC: crc-Librarian.md | R2323
type FindConnectionsOpts struct {
	TimeoutSeconds int
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

// FindConnections allocates a request ID, writes the initial pending
// tmp:// doc through the write actor, enqueues the request for the
// sidecar, and schedules the deadline timer. Returns the request ID
// immediately.
// CRC: crc-Librarian.md | Seq: seq-find-connections.md | R2319, R2321, R2326, R2327, R2331, R2332
func (l *Librarian) FindConnections(chunkIDs []uint64, opts FindConnectionsOpts) (string, error) {
	if l == nil {
		return "", errors.New("agent unavailable")
	}
	if len(chunkIDs) == 0 {
		return "", errors.New("chunkIDs empty")
	}
	if !l.ConnectionsAvailable() {
		return "", errors.New("agent unavailable")
	}
	timeoutSec := clampTimeout(opts.TimeoutSeconds)
	id := randomID()
	now := time.Now()
	rec := &ConnectionsRecord{
		ID:         id,
		ChunkIDs:   append([]uint64(nil), chunkIDs...),
		Started:    now,
		Deadline:   now.Add(time.Duration(timeoutSec) * time.Second),
		TimeoutDur: time.Duration(timeoutSec) * time.Second,
		Status:     "pending",
		Progress:   "fetching",
		Path:       connectionsDocPath(id),
		stop:       make(chan struct{}),
	}
	if err := l.writeConnectionsDoc(rec, true); err != nil {
		return "", fmt.Errorf("create tmp doc: %w", err)
	}
	l.mu.Lock()
	l.connectionsResults[id] = rec
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
	stop := rec.stop
	l.mu.Unlock()
	go l.runConnectionsTicker(id, stop)
	return id, nil
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
// returns true). R2321.
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
// honestly. R2320, R2321.
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
	l.mu.Lock()
	rec := l.connectionsResults[id]
	if rec == nil {
		l.mu.Unlock()
		return errors.New("unknown request id")
	}
	if rec.Done {
		l.mu.Unlock()
		log.Printf("connections: late --result for %s, discarding (status=%s)", id, rec.Status)
		return nil
	}
	rec.Status = "completed"
	rec.Progress = "done"
	rec.Elapsed = int(time.Since(rec.Started).Round(time.Second) / time.Second)
	rec.Done = true
	if rec.Timer != nil {
		rec.Timer.Stop()
		rec.Timer = nil
	}
	closeStop(rec)
	l.mu.Unlock()
	body := renderConnectionsBody(result)
	return l.writeConnectionsDoc(rec, false, body)
}

// SetConnectionsError flips the tmp:// doc to errored. Idempotent
// on terminal state. R2318, R2329, R2333.
func (l *Librarian) SetConnectionsError(id, msg string) error {
	l.mu.Lock()
	rec := l.connectionsResults[id]
	if rec == nil {
		l.mu.Unlock()
		return errors.New("unknown request id")
	}
	if rec.Done {
		l.mu.Unlock()
		log.Printf("connections: late --error for %s, discarding (status=%s)", id, rec.Status)
		return nil
	}
	rec.Status = "errored"
	rec.Error = msg
	rec.Progress = "done"
	rec.Elapsed = int(time.Since(rec.Started).Round(time.Second) / time.Second)
	rec.Done = true
	if rec.Timer != nil {
		rec.Timer.Stop()
		rec.Timer = nil
	}
	closeStop(rec)
	l.mu.Unlock()
	return l.writeConnectionsDoc(rec, false)
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
// CleanResults for spectral expand. R2321.
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

	paths, err := l.db.fts.FileIDPaths()
	if err != nil {
		return nil, fmt.Errorf("file id paths: %w", err)
	}

	out := make([]ChunkFetchEntry, 0, len(rec.ChunkIDs))
	cache := l.db.fts.NewChunkCache()
	err = l.db.fts.Env().View(func(txn *lmdb.Txn) error {
		for _, chunkID := range rec.ChunkIDs {
			crec, rerr := l.db.fts.ReadCRecord(txn, chunkID)
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
// come from the record; the body (R2330) is either rendered from a
// passed-in string (completed state) or omitted.
func renderConnectionsDoc(rec *ConnectionsRecord, body ...string) []byte {
	var b strings.Builder
	fmt.Fprintf(&b, "@connections-status: %s\n", rec.Status)
	fmt.Fprintf(&b, "@connections-request-id: %s\n", rec.ID)
	fmt.Fprintf(&b, "@connections-pinned-chunks: %s\n", joinUint64s(rec.ChunkIDs))
	fmt.Fprintf(&b, "@connections-started: %s\n", rec.Started.UTC().Format(time.RFC3339))
	fmt.Fprintf(&b, "@connections-elapsed: %d\n", rec.Elapsed)
	fmt.Fprintf(&b, "@connections-progress: %s\n", rec.Progress)
	if rec.Done {
		fmt.Fprintf(&b, "@connections-completed: %s\n", time.Now().UTC().Format(time.RFC3339))
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

// renderConnectionsBody renders the Themes / Shared Tag Candidates
// markdown sections per R2330. Tag lines use comma-separated
// chunk-ID lists.
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
