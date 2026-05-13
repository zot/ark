package ark

// CRC: crc-Librarian.md | Test: test-FindConnections.md
// Integration tests for find-connections: real DB → real PubSub →
// real Librarian, test-as-subscriber pattern.
// R2320, R2322, R2324, R2325, R2331, R2333, R2339, R2340, R2341, R2342, R2343

import (
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/zot/microfts2"
)

// readTmpBody returns the body bytes of a tmp:// document via microfts2's
// TmpContent reader. Centralizes the io.ReadAll dance.
func readTmpBody(t *testing.T, db *DB, path string) string {
	t.Helper()
	r, err := db.fts.TmpContent(path)
	if err != nil {
		t.Fatalf("TmpContent %s: %v", path, err)
	}
	b, err := io.ReadAll(r)
	if err != nil {
		t.Fatalf("read tmp body %s: %v", path, err)
	}
	return string(b)
}

// setupConnections wires a Librarian onto a DB with the actor running
// and PubSub installed. The Librarian is constructed by-field because
// NewLibrarian shells out to `claude` lookup; tests don't want that.
func setupConnections(t *testing.T) (*Librarian, *DB, *PubSub) {
	t.Helper()
	dir := t.TempDir()
	ftsDir := filepath.Join(dir, "fts")
	if err := os.MkdirAll(ftsDir, 0o755); err != nil {
		t.Fatal(err)
	}
	fts, err := microfts2.Create(ftsDir, microfts2.Options{})
	if err != nil {
		t.Fatal(err)
	}
	if err := fts.AddStrategyFunc("line", microfts2.LineChunkFunc); err != nil {
		t.Fatal(err)
	}
	if err := fts.AddChunker("markdown", microfts2.MarkdownChunker{}); err != nil {
		t.Fatal(err)
	}
	store := testStore(t)
	tmpStore := NewTmpTagStore(store.TvidMap())
	store.SetTmpTagStore(tmpStore)
	idx := &Indexer{fts: fts, store: store}

	db := &DB{
		fts:      fts,
		store:    store,
		indexer:  idx,
		dbPath:   dir,
		tmpPaths: map[string]uint64{},
	}
	db.svc = make(chan func(), 16)
	go runSvc(db.svc)

	ps := NewPubSub(time.Minute, 32)
	db.SetPubSub(ps)

	l := &Librarian{
		db:                     db,
		available:              true,
		results:                map[string]*ExpandResult{},
		connectionsResults:     map[string]*ConnectionsRecord{},
		connectionsAvailWindow: 60 * time.Second,
		// Mark a recent --wait so ConnectionsAvailable() returns true
		// for the tests that exercise the happy path.
		connectionsLastWait: time.Now(),
	}

	t.Cleanup(func() {
		// The actor goroutine intentionally leaks. Closing db.svc races
		// with in-flight write continuations (startNextWrite schedules
		// a continuation via svc() *after* the write goroutine
		// completes) — and the test process exits clean anyway. Just
		// close the FTS env so disk resources are released.
		_ = fts.Close()
	})
	return l, db, ps
}

// indexLine writes a single-line file and returns its first chunk ID +
// path. Caller supplies the content; the file is one line so AddFile
// produces exactly one chunk.
func indexLine(t *testing.T, db *DB, name, content string) (uint64, string) {
	t.Helper()
	fp := filepath.Join(db.dbPath, name)
	if err := os.WriteFile(fp, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
	fileid, err := db.indexer.AddFile(fp, "line")
	if err != nil {
		t.Fatalf("AddFile: %v", err)
	}
	info, err := db.fts.FileInfoByID(fileid)
	if err != nil || len(info.Chunks) == 0 {
		t.Fatalf("FileInfoByID: %v / chunks=%d", err, len(info.Chunks))
	}
	return info.Chunks[0].ChunkID, fp
}

// TestFindConnections_AgentUnavailable — bridge returns
// (nil, "agent unavailable") when no --wait consumer has been seen.
// R2320, R2324.
func TestFindConnections_AgentUnavailable(t *testing.T) {
	l, _, _ := setupConnections(t)
	l.connectionsLastWait = time.Time{} // wipe the synthetic last-wait

	_, err := l.FindConnections([]uint64{1, 2}, FindConnectionsOpts{})
	if err == nil || err.Error() != "agent unavailable" {
		t.Fatalf("want agent unavailable, got %v", err)
	}
}

// TestFindConnections_EmptyChunkIDs — bridge rejects empty input.
// R2324.
func TestFindConnections_EmptyChunkIDs(t *testing.T) {
	l, _, _ := setupConnections(t)
	_, err := l.FindConnections(nil, FindConnectionsOpts{})
	if err == nil || err.Error() != "chunkIDs empty" {
		t.Fatalf("want chunkIDs empty, got %v", err)
	}
}

// TestFindConnections_EnqueueCreatesPendingDoc — happy enqueue. The
// tmp:// doc exists and carries the expected header tags; the
// subscriber sees the @connections-status: pending event. R2319,
// R2326, R2327, R2333, R2339.
func TestFindConnections_EnqueueCreatesPendingDoc(t *testing.T) {
	l, db, ps := setupConnections(t)
	chunkID, _ := indexLine(t, db, "a.txt", "hello world\n")

	ps.Subscribe("test", []*TagSub{
		{Tag: "connections-status"},
	})

	id, err := l.FindConnections([]uint64{chunkID}, FindConnectionsOpts{})
	if err != nil {
		t.Fatalf("FindConnections: %v", err)
	}
	if id == "" {
		t.Fatal("empty request ID")
	}

	path := connectionsDocPath(id)
	if !db.hasTmpPath(path) {
		t.Fatalf("tmp doc %s not created", path)
	}

	evts := ps.Listen("test", 500*time.Millisecond)
	if len(evts) == 0 {
		t.Fatal("expected at least one connections-status event")
	}
	gotPending := false
	for _, e := range evts {
		if e.Tag == "connections-status" && e.Value == "pending" && e.Path == path {
			gotPending = true
		}
	}
	if !gotPending {
		t.Fatalf("no pending event for %s: %+v", path, evts)
	}
}

// TestSetConnectionsResult_FlipsToCompleted — drives the full happy
// path: enqueue, then post a valid result. The doc reaches
// @connections-status: completed and the body carries the rendered
// Themes / Shared Tag Candidates sections. R2317, R2329, R2330,
// R2333.
func TestSetConnectionsResult_FlipsToCompleted(t *testing.T) {
	l, db, ps := setupConnections(t)
	c1, _ := indexLine(t, db, "a.txt", "alpha\n")
	c2, _ := indexLine(t, db, "b.txt", "beta\n")

	ps.Subscribe("test", []*TagSub{
		{Tag: "connections-status"},
	})

	id, err := l.FindConnections([]uint64{c1, c2}, FindConnectionsOpts{TimeoutSeconds: 5})
	if err != nil {
		t.Fatalf("FindConnections: %v", err)
	}
	_ = ps.Listen("test", 200*time.Millisecond) // drain pending

	res := &ConnectionsResult{
		Themes: []Theme{
			{Text: "Greek letters", Evidence: []uint64{c1, c2}},
		},
		SharedTags: []SharedTagCand{
			{Tag: "topic", Value: "greek", Evidence: []uint64{c1, c2}},
		},
	}
	if err := l.SetConnectionsResult(id, res); err != nil {
		t.Fatalf("SetConnectionsResult: %v", err)
	}

	gotCompleted := false
	deadline := time.Now().Add(time.Second)
	for time.Now().Before(deadline) && !gotCompleted {
		for _, e := range ps.Listen("test", 200*time.Millisecond) {
			if e.Tag == "connections-status" && e.Value == "completed" {
				gotCompleted = true
			}
		}
	}
	if !gotCompleted {
		t.Fatal("did not see completed status event")
	}

	snap := l.ConnectionRecordSnapshot(id)
	if snap == nil || snap.Status != "completed" {
		t.Fatalf("record status not completed: %+v", snap)
	}

	// Verify the body in the tmp doc carries the rendered sections.
	body := readTmpBody(t, db, connectionsDocPath(id))
	if !strings.Contains(body, "## Themes") {
		t.Errorf("body missing Themes section: %s", body)
	}
	if !strings.Contains(body, "@theme-evidence:") {
		t.Errorf("body missing @theme-evidence: %s", body)
	}
	if !strings.Contains(body, "## Shared Tag Candidates") {
		t.Errorf("body missing Shared Tag Candidates section: %s", body)
	}
	if !strings.Contains(body, "@shared-tag: topic") {
		t.Errorf("body missing @shared-tag: topic: %s", body)
	}
}

// TestSetConnectionsResult_RejectsEmptyEvidence — protocol violation
// drives the doc to errored with a protocol message. R2317, R2342.
func TestSetConnectionsResult_RejectsEmptyEvidence(t *testing.T) {
	l, db, _ := setupConnections(t)
	c1, _ := indexLine(t, db, "a.txt", "alpha\n")

	id, err := l.FindConnections([]uint64{c1}, FindConnectionsOpts{})
	if err != nil {
		t.Fatalf("FindConnections: %v", err)
	}
	res := &ConnectionsResult{
		Themes: []Theme{{Text: "x", Evidence: nil}},
	}
	if err := l.SetConnectionsResult(id, res); err != nil {
		t.Fatalf("SetConnectionsResult: %v", err)
	}
	snap := l.ConnectionRecordSnapshot(id)
	if snap == nil || snap.Status != "errored" {
		t.Fatalf("status not errored: %+v", snap)
	}
	if !strings.Contains(snap.Error, "empty evidence") {
		t.Errorf("error should mention empty evidence: %q", snap.Error)
	}
}

// TestConnectionsTimeout_FlipsToErrored — a request whose sidecar
// never posts flips to errored with @connections-error: timeout.
// R2331, R2340.
func TestConnectionsTimeout_FlipsToErrored(t *testing.T) {
	l, db, _ := setupConnections(t)
	c1, _ := indexLine(t, db, "a.txt", "alpha\n")

	id, err := l.FindConnections([]uint64{c1}, FindConnectionsOpts{TimeoutSeconds: 5})
	if err != nil {
		t.Fatalf("FindConnections: %v", err)
	}
	// Override the deadline to fire quickly. Need to atomically replace
	// the timer.
	l.mu.Lock()
	rec := l.connectionsResults[id]
	if rec.Timer != nil {
		rec.Timer.Stop()
	}
	rec.Deadline = time.Now().Add(100 * time.Millisecond)
	rec.TimeoutDur = 100 * time.Millisecond
	rec.Timer = time.AfterFunc(100*time.Millisecond, func() {
		l.connectionsTimeout(id)
	})
	l.mu.Unlock()

	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		if snap := l.ConnectionRecordSnapshot(id); snap != nil && snap.Done {
			if snap.Status != "errored" {
				t.Fatalf("status not errored after timeout: %+v", snap)
			}
			if snap.Error != "timeout" {
				t.Fatalf("error not timeout: %q", snap.Error)
			}
			return
		}
		time.Sleep(50 * time.Millisecond)
	}
	t.Fatalf("timeout did not flip record: tmp doc body=%s", readTmpBody(t, db, connectionsDocPath(id)))
}

// TestConnectionsTimeout_LateResultDiscarded — a --result that arrives
// after timeout is logged and dropped; the doc state is unchanged.
// R2331.
func TestConnectionsTimeout_LateResultDiscarded(t *testing.T) {
	l, db, _ := setupConnections(t)
	c1, _ := indexLine(t, db, "a.txt", "alpha\n")

	id, err := l.FindConnections([]uint64{c1}, FindConnectionsOpts{TimeoutSeconds: 5})
	if err != nil {
		t.Fatalf("FindConnections: %v", err)
	}
	// Force timeout immediately.
	l.connectionsTimeout(id)
	snap := l.ConnectionRecordSnapshot(id)
	if snap == nil || snap.Status != "errored" {
		t.Fatalf("expected errored, got %+v", snap)
	}

	res := &ConnectionsResult{
		Themes: []Theme{{Text: "x", Evidence: []uint64{c1}}},
	}
	if err := l.SetConnectionsResult(id, res); err != nil {
		t.Fatalf("late SetConnectionsResult should be no-op: %v", err)
	}
	snap = l.ConnectionRecordSnapshot(id)
	if snap.Status != "errored" || snap.Error != "timeout" {
		t.Fatalf("late result should not modify state: %+v", snap)
	}
	_ = db // shut linter up
}

// TestBuildFetchPayload_ReturnsContent — valid chunkIDs resolve to
// chunk content. R2316, R2341.
func TestBuildFetchPayload_ReturnsContent(t *testing.T) {
	l, db, _ := setupConnections(t)
	c1, p1 := indexLine(t, db, "a.txt", "alpha\n")
	c2, p2 := indexLine(t, db, "b.txt", "beta\n")

	id, err := l.FindConnections([]uint64{c1, c2}, FindConnectionsOpts{})
	if err != nil {
		t.Fatalf("FindConnections: %v", err)
	}

	entries, err := l.BuildFetchPayload(id)
	if err != nil {
		t.Fatalf("BuildFetchPayload: %v", err)
	}
	if len(entries) != 2 {
		t.Fatalf("want 2 entries, got %d (%+v)", len(entries), entries)
	}
	if entries[0].ChunkID != c1 || entries[0].Path != p1 {
		t.Errorf("entry 0: want %d %s, got %d %s", c1, p1, entries[0].ChunkID, entries[0].Path)
	}
	if entries[1].ChunkID != c2 || entries[1].Path != p2 {
		t.Errorf("entry 1: want %d %s, got %d %s", c2, p2, entries[1].ChunkID, entries[1].Path)
	}
	if !strings.Contains(entries[0].Content, "alpha") {
		t.Errorf("entry 0 content: %q", entries[0].Content)
	}
	if !strings.Contains(entries[1].Content, "beta") {
		t.Errorf("entry 1 content: %q", entries[1].Content)
	}
}

// TestBuildFetchPayload_UnknownChunkID — an unknown chunkID returns
// an error that names the offending ID. R2316, R2324.
func TestBuildFetchPayload_UnknownChunkID(t *testing.T) {
	l, db, _ := setupConnections(t)
	c1, _ := indexLine(t, db, "a.txt", "alpha\n")

	id, err := l.FindConnections([]uint64{c1, 999999}, FindConnectionsOpts{})
	if err != nil {
		t.Fatalf("FindConnections: %v", err)
	}
	_, err = l.BuildFetchPayload(id)
	if err == nil {
		t.Fatal("expected error for unknown chunk")
	}
	if !strings.Contains(err.Error(), "unknown chunk") || !strings.Contains(err.Error(), "999999") {
		t.Errorf("error should name unknown chunk 999999: %v", err)
	}
}

// TestConnectionsAvailable_AdvancesOnWait — calling
// WaitForConnectionsRequest updates the last-wait timestamp so
// ConnectionsAvailable returns true afterwards. R2320.
func TestConnectionsAvailable_AdvancesOnWait(t *testing.T) {
	l, _, _ := setupConnections(t)
	l.connectionsLastWait = time.Time{} // wipe
	if l.ConnectionsAvailable() {
		t.Fatal("expected unavailable before any --wait")
	}

	done := make(chan struct{})
	go func() {
		l.WaitForConnectionsRequest(50 * time.Millisecond) // returns false
		close(done)
	}()
	<-done

	if !l.ConnectionsAvailable() {
		t.Fatal("expected available after WaitForConnectionsRequest")
	}
}
