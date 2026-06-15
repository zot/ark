package ark

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/zot/microfts2"
)

func TestTmpTagStoreUpdateAndRead(t *testing.T) {
	store := NewTmpTagStore(NewTvidMap())

	// Two chunks for one tmp:// fileid
	const fid = uint64(0xFFFFFFFFFFFFFFFF)
	store.UpdateTagValues(fid, []ChunkTagValues{
		{ChunkID: 0xFFFFFFFFFFFFFFF0, FileID: fid, Values: []TagValue{
			{Tag: "status", Value: "open"},
			{Tag: "from-project", Value: "ark"},
		}},
		{ChunkID: 0xFFFFFFFFFFFFFFF1, FileID: fid, Values: []TagValue{
			{Tag: "status", Value: "open"},
		}},
	})

	tagFiles := store.TagFiles([]string{"status"})
	if len(tagFiles) != 2 {
		t.Errorf("want 2 status records, got %d (%+v)", len(tagFiles), tagFiles)
	}

	ids := store.TagValueChunks("status", "open")
	if len(ids) != 2 {
		t.Errorf("want 2 chunkids for status:open, got %d", len(ids))
	}

	values := store.FileTagValues(fid, []string{"status", "from-project"})
	if values["status"] != "open" || values["from-project"] != "ark" {
		t.Errorf("FileTagValues mismatch: %+v", values)
	}

	store.RemoveFile(fid)
	if store.HasFile(fid) {
		t.Errorf("HasFile should be false after RemoveFile")
	}
}

func TestStoreUnionAndDispatch(t *testing.T) {
	s := testStore(t)
	tmp := NewTmpTagStore(s.TvidMap())
	s.SetTmpTagStore(tmp)

	const fid = uint64(0xFFFFFFFFFFFFFFFE)
	const cid = uint64(0xFFFFFFFFFFFFFFF0)

	if err := s.UpdateTagValues([]ChunkTagValues{{
		ChunkID: cid, FileID: fid, Values: []TagValue{{Tag: "status", Value: "open"}},
	}}); err != nil {
		t.Fatalf("UpdateTagValues: %v", err)
	}

	if !tmp.HasFile(fid) {
		t.Errorf("overlay should hold fileid after dispatch")
	}

	values, err := s.FileTagValues(fid, []string{"status"})
	if err != nil {
		t.Fatalf("FileTagValues: %v", err)
	}
	if values["status"] != "open" {
		t.Errorf("expected overlay FileTagValues to return open, got %q", values["status"])
	}

	s.RemoveFileTagValues(fid)
	if tmp.HasFile(fid) {
		t.Errorf("overlay should be empty after RemoveFileTagValues")
	}
}

// TestAddTmpFileExtractsTags drives the DB.AddTmpFile path with real
// microfts2 + Store + TmpTagStore wiring and verifies that the
// in-memory tag overlay is populated from the WithIndexedChunkCallback
// that microfts2 fires for tmp:// paths.
func TestAddTmpFileExtractsTags(t *testing.T) {
	dir := t.TempDir()
	ftsDir := filepath.Join(dir, "fts")
	if err := os.MkdirAll(ftsDir, 0o755); err != nil {
		t.Fatal(err)
	}
	fts, err := microfts2.Create(IndexPath(ftsDir), microfts2.Options{})
	if err != nil {
		t.Fatal(err)
	}
	defer fts.Close()
	if err := fts.AddChunker("lines", microfts2.FuncChunker{Fn: microfts2.LineChunkFunc}); err != nil {
		t.Fatal(err)
	}

	store := testStore(t)
	tmp := NewTmpTagStore(store.TvidMap())
	store.SetTmpTagStore(tmp)
	db := &DB{fts: fts, store: store, tmpPaths: map[string]uint64{}}
	db.svc = make(chan func(), 16)
	go runSvc(db.svc)
	defer close(db.svc)

	content := []byte("@status: zzunique\n@from-project: ark\nbody line\n")
	fid, err := Sync(db, func(d *DB) (uint64, error) {
		return d.AddTmpFile("tmp://test-doc", "lines", content)
	})
	if err != nil {
		t.Fatalf("AddTmpFile: %v", err)
	}

	if !tmp.HasFile(fid) {
		t.Fatalf("TmpTagStore should hold fileid %x after AddTmpFile", fid)
	}
	values := tmp.FileTagValues(fid, []string{"status", "from-project"})
	if values["status"] != "zzunique" {
		t.Errorf("status: want zzunique, got %q", values["status"])
	}
	if values["from-project"] != "ark" {
		t.Errorf("from-project: want ark, got %q", values["from-project"])
	}

	if err := SyncVoid(db, func(d *DB) error { return d.RemoveTmpFile("tmp://test-doc") }); err != nil {
		t.Fatalf("RemoveTmpFile: %v", err)
	}
	if tmp.HasFile(fid) {
		t.Error("TmpTagStore should be empty after RemoveTmpFile")
	}
}
