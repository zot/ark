package ark

// CRC: crc-DB.md | Test: test-TmpSubscription.md
// Integration tests for the centralized tmp:// publish path:
// real DB → real PubSub → real Subscriber, no mocks.

import (
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/zot/microfts2"
)

// setupTmpDB returns a DB wired through microfts2 + Store + TmpTagStore
// + PubSub, ready for AddTmpFile / UpdateTmpFile / AppendTmpFile /
// RemoveTmpFile to drive publish via the centralized path.
func setupTmpDB(t *testing.T) (*DB, *PubSub, func()) {
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
	if err := fts.AddChunker("lines", microfts2.FuncChunker{Fn: microfts2.LineChunkFunc}, makeTagTransform("lines")); err != nil {
		t.Fatal(err)
	}
	store := testStore(t)
	tmpStore := NewTmpTagStore(store.TvidMap())
	store.SetTmpTagStore(tmpStore)

	db := &DB{fts: fts, store: store, tmpPaths: map[string]uint64{}}
	db.svc = make(chan func(), 16)
	go runSvc(db.svc)

	ps := NewPubSub(time.Minute, 32)
	db.SetPubSub(ps)

	cleanup := func() {
		close(db.svc)
		_ = fts.Close()
	}
	return db, ps, cleanup
}

// TestAddTmpFilePublishesAllTags — AddTmpFile fires every present tag
// because the prior cache is empty for a new path. R2281, R2285.
func TestAddTmpFilePublishesAllTags(t *testing.T) {
	db, ps, cleanup := setupTmpDB(t)
	defer cleanup()

	ps.Subscribe("test", []*TagSub{
		{Predicate: mustParseSub(t, "status"), FilterFiles: []string{"tmp://add.md"}},
		{Predicate: mustParseSub(t, "topic"), FilterFiles: []string{"tmp://add.md"}},
	})

	_, err := Sync(db, func(d *DB) (uint64, error) {
		return d.AddTmpFile("tmp://add.md", "lines",
			[]byte("@status: pending\n@topic: ark\nbody\n"))
	})
	if err != nil {
		t.Fatalf("AddTmpFile: %v", err)
	}

	evts := ps.Listen("test", 200*time.Millisecond)
	if len(evts) != 2 {
		t.Fatalf("want 2 events (status+topic), got %d (%+v)", len(evts), evts)
	}
	gotStatus, gotTopic := false, false
	for _, e := range evts {
		if e.Tag == "status" && e.Value == "pending" {
			gotStatus = true
		}
		if e.Tag == "topic" && e.Value == "ark" {
			gotTopic = true
		}
	}
	if !gotStatus || !gotTopic {
		t.Errorf("missing events: status=%v topic=%v (%+v)", gotStatus, gotTopic, evts)
	}
}

// TestUpdateTmpFileOnlyChangesFire — replacing content with identical
// tag values fires nothing; changing a value fires only that tag. R2284.
func TestUpdateTmpFileOnlyChangesFire(t *testing.T) {
	db, ps, cleanup := setupTmpDB(t)
	defer cleanup()

	ps.Subscribe("test", []*TagSub{
		{Predicate: mustParseSub(t, "status"), FilterFiles: []string{"tmp://u.md"}},
		{Predicate: mustParseSub(t, "kind"), FilterFiles: []string{"tmp://u.md"}},
	})

	// Seed via Add; drain initial events.
	_, err := Sync(db, func(d *DB) (uint64, error) {
		return d.AddTmpFile("tmp://u.md", "lines",
			[]byte("@status: idle\n@kind: report\n"))
	})
	if err != nil {
		t.Fatalf("AddTmpFile: %v", err)
	}
	_ = ps.Listen("test", 200*time.Millisecond) // drain initial 2

	// Identical Update — zero events.
	if err := SyncVoid(db, func(d *DB) error {
		return d.UpdateTmpFile("tmp://u.md", "lines",
			[]byte("@status: idle\n@kind: report\n"))
	}); err != nil {
		t.Fatalf("UpdateTmpFile identical: %v", err)
	}
	if evts := ps.Listen("test", 200*time.Millisecond); len(evts) != 0 {
		t.Errorf("identical update: want 0 events, got %+v", evts)
	}

	// Update with status changed only — one event.
	if err := SyncVoid(db, func(d *DB) error {
		return d.UpdateTmpFile("tmp://u.md", "lines",
			[]byte("@status: running\n@kind: report\n"))
	}); err != nil {
		t.Fatalf("UpdateTmpFile changed: %v", err)
	}
	evts := ps.Listen("test", 200*time.Millisecond)
	if len(evts) != 1 {
		t.Fatalf("changed update: want 1 event, got %d (%+v)", len(evts), evts)
	}
	if evts[0].Tag != "status" || evts[0].Value != "running" {
		t.Errorf("want status=running, got %+v", evts[0])
	}
}

// TestAppendTmpFileNoRefireOnExistingTags — appending content that
// carries a tag already published from prior content doesn't re-fire. R2286.
func TestAppendTmpFileNoRefireOnExistingTags(t *testing.T) {
	db, ps, cleanup := setupTmpDB(t)
	defer cleanup()

	ps.Subscribe("test", []*TagSub{
		{Predicate: mustParseSub(t, "topic"), FilterFiles: []string{"tmp://app.md"}},
	})

	// Seed via Add; drain initial.
	_, err := Sync(db, func(d *DB) (uint64, error) {
		return d.AddTmpFile("tmp://app.md", "lines", []byte("@topic: ark\nfirst\n"))
	})
	if err != nil {
		t.Fatalf("AddTmpFile: %v", err)
	}
	_ = ps.Listen("test", 200*time.Millisecond)

	// Append more content carrying the same @topic: ark — no event.
	_, err = Sync(db, func(d *DB) (uint64, error) {
		return d.AppendTmpFile("tmp://app.md", "lines", []byte("@topic: ark\nsecond\n"))
	})
	if err != nil {
		t.Fatalf("AppendTmpFile same: %v", err)
	}
	if evts := ps.Listen("test", 200*time.Millisecond); len(evts) != 0 {
		t.Errorf("append-same: want 0 events, got %+v", evts)
	}

	// Append content with a new @topic value — fires.
	_, err = Sync(db, func(d *DB) (uint64, error) {
		return d.AppendTmpFile("tmp://app.md", "lines", []byte("@topic: leisure\nthird\n"))
	})
	if err != nil {
		t.Fatalf("AppendTmpFile new: %v", err)
	}
	evts := ps.Listen("test", 200*time.Millisecond)
	if len(evts) != 1 || evts[0].Value != "leisure" {
		t.Errorf("append-new: want one topic=leisure, got %+v", evts)
	}
}

// TestRemoveTmpFileClearsCache — after RemoveTmpFile, a subsequent
// AddTmpFile on the same path treats it as new (every tag fires). R2287.
func TestRemoveTmpFileClearsCache(t *testing.T) {
	db, ps, cleanup := setupTmpDB(t)
	defer cleanup()

	ps.Subscribe("test", []*TagSub{
		{Predicate: mustParseSub(t, "status"), FilterFiles: []string{"tmp://rm.md"}},
	})

	// Seed; drain.
	_, err := Sync(db, func(d *DB) (uint64, error) {
		return d.AddTmpFile("tmp://rm.md", "lines", []byte("@status: done\n"))
	})
	if err != nil {
		t.Fatalf("AddTmpFile: %v", err)
	}
	_ = ps.Listen("test", 200*time.Millisecond)

	// Identical AddTmpFile (after removing first) should fire status again.
	if err := SyncVoid(db, func(d *DB) error {
		return d.RemoveTmpFile("tmp://rm.md")
	}); err != nil {
		t.Fatalf("RemoveTmpFile: %v", err)
	}

	_, err = Sync(db, func(d *DB) (uint64, error) {
		return d.AddTmpFile("tmp://rm.md", "lines", []byte("@status: done\n"))
	})
	if err != nil {
		t.Fatalf("AddTmpFile (after remove): %v", err)
	}
	evts := ps.Listen("test", 200*time.Millisecond)
	if len(evts) != 1 {
		t.Errorf("after remove + re-add: want 1 event, got %+v", evts)
	}
}

// TestSubscribeBeforeDocExists — subscribing to a tmp:// path that
// hasn't been created registers the sub; the first AddTmpFile fires. R2311.
func TestSubscribeBeforeDocExists(t *testing.T) {
	db, ps, cleanup := setupTmpDB(t)
	defer cleanup()

	// Subscribe first — no doc exists yet.
	ps.Subscribe("future", []*TagSub{
		{Predicate: mustParseSub(t, "status"), FilterFiles: []string{"tmp://later.md"}},
	})

	// No events before AddTmpFile.
	if evts := ps.Listen("future", 100*time.Millisecond); len(evts) != 0 {
		t.Errorf("before doc exists: want 0, got %+v", evts)
	}

	// Now create the doc — event fires.
	_, err := Sync(db, func(d *DB) (uint64, error) {
		return d.AddTmpFile("tmp://later.md", "lines", []byte("@status: hello\n"))
	})
	if err != nil {
		t.Fatalf("AddTmpFile: %v", err)
	}
	evts := ps.Listen("future", 200*time.Millisecond)
	if len(evts) != 1 || evts[0].Value != "hello" {
		t.Errorf("after AddTmpFile: want status=hello, got %+v", evts)
	}
}

// TestFileTagSubscribe — a TagSubFile subscription fires for every
// chunk on a file once any chunk in the file carries a tag matching
// the predicate. Membership transition follows the table in seq-pubsub.md.
// R2460-R2469.
func TestFileTagSubscribe(t *testing.T) {
	db, ps, cleanup := setupTmpDB(t)
	defer cleanup()

	ps.Subscribe("ft", []*TagSub{{
		Kind:      TagSubFile,
		Predicate: mustParseSub(t, "to-project=ark"),
	}})

	// AddTmpFile with the matching tag — entry transition (N → Y).
	// Should deliver one file-tag event for this indexing.
	_, err := Sync(db, func(d *DB) (uint64, error) {
		return d.AddTmpFile("tmp://ft/req.md", "lines",
			[]byte("@to-project: ark\n@from-project: microfts2\n"))
	})
	if err != nil {
		t.Fatalf("AddTmpFile: %v", err)
	}
	evts := ps.Listen("ft", 200*time.Millisecond)
	if len(evts) != 1 {
		t.Fatalf("entry: want 1 event, got %d (%+v)", len(evts), evts)
	}
	if evts[0].Path != "tmp://ft/req.md" {
		t.Errorf("entry event path: got %q", evts[0].Path)
	}

	// AppendTmpFile on the same file (now a member) — continued
	// transition (Y → Y). Should deliver one event.
	_, err = Sync(db, func(d *DB) (uint64, error) {
		return d.AppendTmpFile("tmp://ft/req.md", "lines",
			[]byte("@status: open\nbody chunk\n"))
	})
	if err != nil {
		t.Fatalf("AppendTmpFile: %v", err)
	}
	evts = ps.Listen("ft", 200*time.Millisecond)
	if len(evts) != 1 {
		t.Fatalf("continued: want 1 event, got %d (%+v)", len(evts), evts)
	}

	// UpdateTmpFile with content that drops the @to-project tag —
	// exit transition (Y → N). Should deliver one event (the exit).
	if err := SyncVoid(db, func(d *DB) error {
		return d.UpdateTmpFile("tmp://ft/req.md", "lines",
			[]byte("@status: closed\n"))
	}); err != nil {
		t.Fatalf("UpdateTmpFile: %v", err)
	}
	evts = ps.Listen("ft", 200*time.Millisecond)
	if len(evts) != 1 {
		t.Fatalf("exit: want 1 event, got %d (%+v)", len(evts), evts)
	}

	// Further appends on the now-non-member file — no events (N → N).
	_, err = Sync(db, func(d *DB) (uint64, error) {
		return d.AppendTmpFile("tmp://ft/req.md", "lines",
			[]byte("more content\n"))
	})
	if err != nil {
		t.Fatalf("AppendTmpFile post-exit: %v", err)
	}
	if evts := ps.Listen("ft", 100*time.Millisecond); len(evts) != 0 {
		t.Errorf("post-exit: want 0 events, got %+v", evts)
	}
}

// TestMultiSubscriberBroadcast — multiple sessions subscribed to the
// same tag and path each receive the event. R2293 (cross-session).
func TestMultiSubscriberBroadcast(t *testing.T) {
	db, ps, cleanup := setupTmpDB(t)
	defer cleanup()

	ps.Subscribe("alpha", []*TagSub{
		{Predicate: mustParseSub(t, "status"), FilterFiles: []string{"tmp://b.md"}},
	})
	ps.Subscribe("beta", []*TagSub{
		{Predicate: mustParseSub(t, "status"), FilterFiles: []string{"tmp://b.md"}},
	})

	_, err := Sync(db, func(d *DB) (uint64, error) {
		return d.AddTmpFile("tmp://b.md", "lines", []byte("@status: ready\n"))
	})
	if err != nil {
		t.Fatalf("AddTmpFile: %v", err)
	}

	a := ps.Listen("alpha", 200*time.Millisecond)
	b := ps.Listen("beta", 200*time.Millisecond)
	if len(a) != 1 || a[0].Value != "ready" {
		t.Errorf("alpha: want status=ready, got %+v", a)
	}
	if len(b) != 1 || b[0].Value != "ready" {
		t.Errorf("beta: want status=ready, got %+v", b)
	}
}
