package ark

// CRC: crc-DB.md, crc-Server.md | Test: test-OffActorReads.md

import (
	"net/http/httptest"
	"os"
	"path/filepath"
	"sync"
	"testing"

	"github.com/zot/microfts2"
)

// setupContentView wires the minimum handleContentView needs: a source
// covering the test dir (contentPath's ReadSourceFile gate), an on-disk
// content template (loadContentTemplate reads dbPath/html), and an indexed
// markdown file so the chunked render path — the one that reads the fts
// caches — actually runs.
func setupContentView(t *testing.T) (*Server, *DB, string) {
	t.Helper()
	l, db := setupRecall(t)
	_ = l
	db.config.Sources = []Source{{Dir: db.dbPath}}

	htmlDir := filepath.Join(db.dbPath, "html")
	if err := os.MkdirAll(htmlDir, 0o755); err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"content-markdown.html", "content-plain.html"} {
		if err := os.WriteFile(filepath.Join(htmlDir, name), []byte(`{{.Content}}`), 0o644); err != nil {
			t.Fatal(err)
		}
	}

	_, path := indexLine(t, db, "viewrace.md", "# Heading\n\nbody text with a @tag: value\n")
	return &Server{db: db}, db, path
}

// TestContentView_OffActorReadsDontRaceInvalidate drives the real
// handleContentView against a live write actor under -race.
//
// The handler runs on the HTTP goroutine, never inside Sync(srv.db, ...),
// and its render subtree reads the fts Go-side caches in five places:
// ChunkIDByLocation and ChunkIDsForPath (CheckFile + FileInfoByID),
// ResolveLink via wrapTagElements, resolveFilePath via
// ExtRoutingsForTargetChunk, and ChunkIDsForPath again inside
// renderPdfChunksByPage. The write actor's completion closure nils those
// same caches via InvalidateCaches (db.go:501). Reading them off the actor
// is the O154 race class.
//
// Before the R3165 read seam this failed with a DATA RACE between
// microfts2.(*DB).InvalidateCaches and lookupFileByPath. The handler now
// binds one private fts.Copy() and threads it through, so the reads land
// on caches no one else invalidates.
func TestContentView_OffActorReadsDontRaceInvalidate(t *testing.T) {
	srv, db, path := setupContentView(t)

	const rounds = 60
	var wg sync.WaitGroup

	// Writer: drive the write actor so its completion closure runs
	// db.fts.InvalidateCaches() on the actor, over and over. The write
	// body is a no-op — the invalidate is what we race against.
	wg.Add(1)
	go func() {
		defer wg.Done()
		for i := 0; i < rounds; i++ {
			SyncVoid(db, func(d *DB) error {
				d.enqueueWrite(func(*microfts2.DB) {})
				return nil
			})
		}
	}()

	// Readers: two concurrent content views, full-file and single-chunk,
	// so both the AllChunks walk and the isChunk branch are exercised.
	for _, query := range []string{"", "?range=1-3"} {
		wg.Add(1)
		go func(q string) {
			defer wg.Done()
			for i := 0; i < rounds; i++ {
				req := httptest.NewRequest("GET", "/content"+path+q, nil)
				srv.handleContentView(httptest.NewRecorder(), req)
			}
		}(query)
	}

	wg.Wait()
	db.WaitWritesIdle()
}

// TestContentView_ReadViewIsPrivate pins the shape of the read seam itself.
// A future refactor that dropped the fts.Copy(), or that left the Searcher
// or the ExtMap unbound on the view, would silently restore the race class
// above — the -race test only catches it when the timing happens to line
// up, so assert the structure directly. Mirrors
// TestRecall_OpReadsThroughCopy. R3165
func TestContentView_ReadViewIsPrivate(t *testing.T) {
	_, db, path := setupContentView(t)

	rdb := db.withFTS(db.fts.Copy())

	if rdb.fts == db.fts {
		t.Fatal("read view shares the original fts; expected a private copy")
	}
	if rdb.search == nil || rdb.search.fts != rdb.fts {
		t.Fatal("read view did not rebind the Searcher to the copy fts")
	}
	if rdb.extmap != db.extmap {
		t.Fatal("read view lost the ExtMap; @ext routings would not resolve")
	}
	if rdb.svc != nil {
		t.Fatal("read view carries an actor; it is a reads-only view")
	}

	// The copy still resolves the reads the handler makes through it.
	if ids := rdb.ChunkIDsForPath(path); len(ids) == 0 {
		t.Fatal("ChunkIDsForPath through the read view returned nothing")
	}
}
