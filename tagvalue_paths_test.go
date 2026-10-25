package ark

// CRC: crc-Librarian.md | Test: test-TagValuePaths.md | R3585, R3587

import (
	"os"
	"path/filepath"
	"slices"
	"testing"

	"go.etcd.io/bbolt"
)

// wireChunkFiles gives the store the chunk→file resolver DB.Open wires
// (db.go, SetChunkResolver). The recall harnesses leave it nil, and
// FilesForChunks then answers nothing, so a test of a path lookup would
// pass or fail without the lookup ever running.
func wireChunkFiles(db *DB) {
	db.store.SetChunkResolver(func(txn *bbolt.Tx, chunkID uint64) []uint64 {
		crec, err := db.fts.ReadCRecord(txn, chunkID)
		if err != nil {
			return nil
		}
		ids := make([]uint64, 0, len(crec.FileIDs))
		for _, f := range crec.FileIDs {
			ids = append(ids, f.FileID)
		}
		return ids
	}, nil)
}

// TestTagValuePathsHonorFence: a tag-value lookup never resolves to a fenced
// path, and drops noise paths, keeping the rest. Refs: R3585
func TestTagValuePathsHonorFence(t *testing.T) {
	l, db := setupRecall(t)
	wireChunkFiles(db)
	for _, d := range []string{"fenced", "noise", "open"} {
		if err := os.MkdirAll(filepath.Join(db.dbPath, d), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	db.config.SystemExclude = &[]string{filepath.Join(db.dbPath, "fenced") + "/**"}
	db.config.SearchExclude = &[]string{filepath.Join(db.dbPath, "noise") + "/**"}
	var want string
	for _, d := range []string{"fenced", "noise", "open"} {
		cid, fp := indexLine(t, db, filepath.Join(d, "f.txt"), "body of "+d)
		if err := db.store.UpdateTagValues([]ChunkTagValues{
			{ChunkID: cid, Values: []TagValue{{Tag: "cuisine", Value: "thai"}}},
		}); err != nil {
			t.Fatal(err)
		}
		if d == "open" {
			want = fp
		}
	}
	if got := l.resolveTagValuePaths("cuisine", "thai"); !slices.Equal(got, []string{want}) {
		t.Errorf("resolveTagValuePaths = %v, want only %s (fenced and noise paths dropped)", got, want)
	}
}

// TestTagValuePathsKeepRoutedNoise: a value a mirror routed onto a file the
// noise filter covers keeps that path. Refs: R3587
func TestTagValuePathsKeepRoutedNoise(t *testing.T) {
	l, db := setupFileBackedRecall(t)
	wireChunkFiles(db)
	targetPath := filepath.Join(db.dbPath, "target.md")
	if err := os.WriteFile(targetPath, []byte("alpha line\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := db.indexer.AddFile(targetPath, "line"); err != nil {
		t.Fatalf("index target: %v", err)
	}
	db.config.SearchExclude = &[]string{targetPath} // the target is noise, as a chat log would be
	src := extMirrorSource(t, db, "notes.md")
	if err := os.WriteFile(src, []byte("@ext: "+targetPath+" @note: hello\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := db.indexer.AddFile(src, "line"); err != nil {
		t.Fatalf("index mirror: %v", err)
	}
	if got := l.resolveTagValuePaths("note", "hello"); !slices.Contains(got, targetPath) {
		t.Errorf("resolveTagValuePaths(note, hello) = %v, want it to keep the routed target %s", got, targetPath)
	}
}
