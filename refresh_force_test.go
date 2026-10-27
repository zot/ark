package ark

// CRC: crc-Indexer.md | Test: test-Refresh.md | R3568, R3569

import (
	"os"
	"path/filepath"
	"testing"
)

// R3568: a forced refresh of an unchanged file runs the full in-place reindex
// (a plain refresh skips it, R2864), and unchanged chunk text keeps its chunk id,
// which is what keeps its embedding and everything that points at it. A full
// reindex allocates a new file id, so the file id shows which path ran.
func TestForceRefreshReindexesUnchangedKeepingChunkIDs(t *testing.T) {
	// The production path: a forced scan runs only for a file inside a configured
	// source, through the DB actor and the write actor, so the DB is a real one.
	dbDir, srcDir := t.TempDir(), t.TempDir()
	seed := "[strategies]\n  \"*.jsonl\" = \"chat-jsonl\"\n[[source]]\n  dir = '" + srcDir + "'\n  include = [\"*.jsonl\"]\n"
	if err := Init(dbDir, InitOpts{ConfigSeed: []byte(seed)}); err != nil {
		t.Fatal(err)
	}
	db, err := Open(dbDir)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	fp := filepath.Join(srcDir, "session.jsonl")
	body := `{"type":"user","origin":{"kind":"human"},"message":{"role":"user","content":"first question about orchards"}}` + "\n" +
		`{"type":"assistant","message":{"role":"assistant","content":[{"type":"text","text":"an answer about pears"}]}}` + "\n"
	if err := os.WriteFile(fp, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := db.Add([]string{fp}, "chat-jsonl"); err != nil {
		t.Fatal(err)
	}
	snapshot := func() (uint64, []uint64) {
		t.Helper()
		st, err := db.fts.CheckFile(fp)
		if err != nil {
			t.Fatal(err)
		}
		info, err := db.fts.FileInfoByID(st.FileID)
		if err != nil {
			t.Fatal(err)
		}
		var ids []uint64
		for _, c := range info.Chunks {
			ids = append(ids, c.ChunkID)
		}
		return st.FileID, ids
	}
	fileBefore, chunksBefore := snapshot()
	if len(chunksBefore) != 2 {
		t.Fatalf("indexed %d chunks, want 2", len(chunksBefore))
	}

	if err := db.Refresh([]string{fp}); err != nil {
		t.Fatal(err)
	}
	if f, _ := snapshot(); f != fileBefore {
		t.Fatalf("a plain refresh re-indexed an unchanged file (file id %d → %d)", fileBefore, f)
	}

	queued, err := db.ForceRefresh([]string{fp})
	if err != nil || queued != 1 {
		t.Fatalf("ForceRefresh = (%d, %v), want 1 file queued", queued, err)
	}
	db.WaitWritesIdle() // the forced scan chunks off the actor; wait for its store
	fileAfter, chunksAfter := snapshot()
	if fileAfter == fileBefore {
		t.Errorf("forced refresh did not re-index (file id unchanged at %d)", fileAfter)
	}
	if len(chunksAfter) != len(chunksBefore) {
		t.Fatalf("chunks %v after, want %v", chunksAfter, chunksBefore)
	}
	for i := range chunksBefore {
		if chunksAfter[i] != chunksBefore[i] {
			t.Errorf("chunk %d id %d → %d: unchanged text must keep its chunk id", i, chunksBefore[i], chunksAfter[i])
		}
	}
}

// R3569: forcing needs a pattern.
func TestForceRefreshNeedsAPattern(t *testing.T) {
	_, db := setupRecall(t)
	if n, err := db.ForceRefresh(nil); err != errForceRefreshNeedsPattern || n != 0 {
		t.Errorf("ForceRefresh(nil) = (%d, %v), want the needs-a-pattern refusal", n, err)
	}
}

// R45, R3568: a forced refresh of a file that vanished from disk reports it and
// never removes it from the index (the watcher's scan path removes such a file;
// the forced path must not).
func TestForceRefreshKeepsAMissingFile(t *testing.T) {
	dbDir, srcDir := t.TempDir(), t.TempDir()
	seed := "[strategies]\n  \"*.jsonl\" = \"chat-jsonl\"\n[[source]]\n  dir = '" + srcDir + "'\n  include = [\"*.jsonl\"]\n"
	if err := Init(dbDir, InitOpts{ConfigSeed: []byte(seed)}); err != nil {
		t.Fatal(err)
	}
	db, err := Open(dbDir)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	fp := filepath.Join(srcDir, "gone.jsonl")
	if err := os.WriteFile(fp, []byte(`{"type":"user","origin":{"kind":"human"},"message":{"role":"user","content":"soon gone"}}`+"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := db.Add([]string{fp}, "chat-jsonl"); err != nil {
		t.Fatal(err)
	}
	if err := os.Remove(fp); err != nil {
		t.Fatal(err)
	}
	if _, err := db.ForceRefresh([]string{fp}); err != nil {
		t.Fatal(err)
	}
	db.WaitWritesIdle()
	if _, err := db.fts.CheckFile(fp); err != nil {
		t.Errorf("a forced refresh removed a missing file from the index: %v", err)
	}
}

// R3568: a file that grew since it was indexed (a live transcript, the common
// case) is still re-chunked whole when forced. The append path would chunk only
// the new lines and keep the old chunks, which is what --force exists to replace.
// A full reindex allocates a new file id; an append keeps the old one.
func TestForceRefreshReindexesAGrownFileInFull(t *testing.T) {
	dbDir, srcDir := t.TempDir(), t.TempDir()
	seed := "[strategies]\n  \"*.jsonl\" = \"chat-jsonl\"\n[[source]]\n  dir = '" + srcDir + "'\n  include = [\"*.jsonl\"]\n"
	if err := Init(dbDir, InitOpts{ConfigSeed: []byte(seed)}); err != nil {
		t.Fatal(err)
	}
	db, err := Open(dbDir)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	fp := filepath.Join(srcDir, "live.jsonl")
	line := func(s string) string {
		return `{"type":"user","origin":{"kind":"human"},"message":{"role":"user","content":"` + s + `"}}` + "\n"
	}
	if err := os.WriteFile(fp, []byte(line("first turn")), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := db.Add([]string{fp}, "chat-jsonl"); err != nil {
		t.Fatal(err)
	}
	before, err := db.fts.CheckFile(fp)
	if err != nil {
		t.Fatal(err)
	}
	f, err := os.OpenFile(fp, os.O_APPEND|os.O_WRONLY, 0)
	if err != nil {
		t.Fatal(err)
	}
	f.WriteString(line("second turn"))
	f.Close()
	if _, err := db.ForceRefresh([]string{fp}); err != nil {
		t.Fatal(err)
	}
	db.WaitWritesIdle()
	after, err := db.fts.CheckFile(fp)
	if err != nil {
		t.Fatal(err)
	}
	if after.FileID == before.FileID {
		t.Errorf("a grown file was appended to, not re-chunked whole (file id still %d)", after.FileID)
	}
	info, _ := db.fts.FileInfoByID(after.FileID)
	if len(info.Chunks) != 2 {
		t.Errorf("chunks after = %d, want 2", len(info.Chunks))
	}
}
