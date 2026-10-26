//go:build windows

package ark

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// Windows only: these need a case-insensitive filesystem to mean anything.
// Run on the VM as a compiled test binary.

// TestDiskSpellingRewritesCaseOnly: an outside path takes the disk's spelling
// component by component; a missing tail and a glob stay as written; a path
// with no volume is untouched.
// CRC: crc-FilenameCase.md | R3626
func TestDiskSpellingRewritesCaseOnly(t *testing.T) {
	root := t.TempDir()
	if err := os.MkdirAll(filepath.Join(root, "Src"), 0o755); err != nil {
		t.Fatal(err)
	}
	file := filepath.Join(root, "Src", "Casey.md")
	if err := os.WriteFile(file, []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	lower := strings.ToLower
	cases := []struct{ name, in, want string }{
		{"existing, mis-cased throughout", lower(file), file},
		{"missing tail kept literal", lower(filepath.Join(root, "Src")) + `\Nope\X.md`, filepath.Join(root, "Src", "Nope", "X.md")},
		{"glob component kept literal", lower(filepath.Join(root, "Src")) + `\*.MD`, filepath.Join(root, "Src", "*.MD")},
		{"drive letter", lower(file[:2]) + file[2:], file},
		{"no volume", "tmp://Casey.md", "tmp://Casey.md"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := DiskSpelling(tc.in); got != tc.want {
				t.Errorf("DiskSpelling(%q) = %q, want %q", tc.in, got, tc.want)
			}
		})
	}
}

// TestCaseRenamedPathIsNotLive: after a case-only rename the old spelling
// still stats, and the liveness rule must still call it gone.
// CRC: crc-FilenameCase.md | R3629
func TestCaseRenamedPathIsNotLive(t *testing.T) {
	dir := t.TempDir()
	oldPath, newPath := filepath.Join(dir, "Casey.md"), filepath.Join(dir, "CASEY.md")
	if err := os.WriteFile(oldPath, []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.Rename(oldPath, newPath); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(oldPath); err != nil {
		t.Fatalf("fixture: the old spelling should still stat on Windows: %v", err)
	}
	if !respelled(oldPath) || respelled(newPath) {
		t.Errorf("respelled(old)=%v respelled(new)=%v, want true, false", respelled(oldPath), respelled(newPath))
	}
	if unresolvedIsLive(UnresolvedRecord{Path: oldPath}) {
		t.Error("an unresolved record under the old spelling is still live; it would never be cleaned")
	}
	if !unresolvedIsLive(UnresolvedRecord{Path: newPath}) {
		t.Error("an unresolved record under the current spelling should be live")
	}
}

// TestResolveViaSpellsAsTheDisk: every outside path resolved through the link
// table comes back in on-disk spelling, even with no links tracked.
// CRC: crc-FilenameCase.md | R3627
func TestResolveViaSpellsAsTheDisk(t *testing.T) {
	dir := t.TempDir()
	file := filepath.Join(dir, "Casey.md")
	if err := os.WriteFile(file, []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	lt := NewLinkTable(&Config{})
	if got, _ := lt.ResolveVia(strings.ToUpper(file)); got != file {
		t.Errorf("ResolveVia(upper) = %q, want %q", got, file)
	}
}

// TestCaseRenameKeepsOneIndexedFile: through the real Init + Open, a
// case-only rename re-added under its new spelling reaches the same file
// record and respells it, rather than indexing a second file.
// CRC: crc-FilenameCase.md | R3630
func TestCaseRenameKeepsOneIndexedFile(t *testing.T) {
	dbDir, srcDir := t.TempDir(), t.TempDir()
	seed := "[[source]]\n  dir = '" + srcDir + "'\n  include = [\"*.md\"]\n"
	if err := Init(dbDir, InitOpts{ConfigSeed: []byte(seed)}); err != nil {
		t.Fatal(err)
	}
	db, err := Open(dbDir)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	oldPath, newPath := filepath.Join(srcDir, "Casey.md"), filepath.Join(srcDir, "CASEY.md")
	if err := os.WriteFile(oldPath, []byte("case token ccc1\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := db.Add([]string{oldPath}, "markdown"); err != nil {
		t.Fatal(err)
	}
	before, err := db.fts.CheckFile(oldPath)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.Rename(oldPath, newPath); err != nil {
		t.Fatal(err)
	}
	_ = db.Add([]string{DiskSpelling(oldPath)}, "markdown") // the old name's event, spelled as the disk spells it
	after, err := db.fts.CheckFile(newPath)
	if err != nil {
		t.Fatalf("the new spelling is not indexed: %v", err)
	}
	if after.FileID != before.FileID {
		t.Errorf("file id %d after the rename, want %d: the rename indexed a second file", after.FileID, before.FileID)
	}
	rec, err := db.fts.FileInfoByID(after.FileID)
	if err != nil {
		t.Fatal(err)
	}
	if len(rec.Names) == 0 || rec.Names[0] != newPath {
		t.Errorf("stored spelling %v, want %q", rec.Names, newPath)
	}
}

// TestSourceDirTakesDiskSpelling: a [[source]].dir written in another case is
// loaded spelled as the disk spells it, so every scanned and watched path,
// which descends from it, is too.
// CRC: crc-FilenameCase.md | R3628
func TestSourceDirTakesDiskSpelling(t *testing.T) {
	root := t.TempDir()
	src := filepath.Join(root, "Src")
	if err := os.MkdirAll(src, 0o755); err != nil {
		t.Fatal(err)
	}
	cfgPath := filepath.Join(root, "ark.toml")
	if err := os.WriteFile(cfgPath, []byte("[[source]]\n  dir = '"+strings.ToUpper(src)+"'\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	cfg, err := LoadConfig(cfgPath)
	if err != nil {
		t.Fatal(err)
	}
	if len(cfg.Sources) != 1 || cfg.Sources[0].Dir != src {
		t.Fatalf("source dir = %+v, want %q", cfg.Sources, src)
	}
}

// TestCaseRenameRespellsOnRefresh: a case-only rename changes neither content
// nor mtime, so the scan the watcher runs must not take its unchanged skip
// while the stored spelling differs. Measured live 2026-10-26: it did, and the
// file stayed under its old name.
// CRC: crc-FilenameCase.md | R3631
func TestCaseRenameRespellsOnRefresh(t *testing.T) {
	dbDir, srcDir := t.TempDir(), t.TempDir()
	seed := "[[source]]\n  dir = '" + srcDir + "'\n  include = [\"*.md\"]\n"
	if err := Init(dbDir, InitOpts{ConfigSeed: []byte(seed)}); err != nil {
		t.Fatal(err)
	}
	db, err := Open(dbDir)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	oldPath, newPath := filepath.Join(srcDir, "Casey.md"), filepath.Join(srcDir, "CASEY.md")
	if err := os.WriteFile(oldPath, []byte("case token ccc1\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := db.Add([]string{oldPath}, "markdown"); err != nil {
		t.Fatal(err)
	}
	before, err := db.fts.CheckFile(oldPath)
	if err != nil {
		t.Fatal(err)
	}
	chunkIDs := func(id uint64) []uint64 {
		t.Helper()
		rec, err := db.fts.FileInfoByID(id)
		if err != nil {
			t.Fatal(err)
		}
		var ids []uint64
		for _, c := range rec.Chunks {
			ids = append(ids, c.ChunkID)
		}
		return ids
	}
	chunksBefore := chunkIDs(before.FileID)
	if err := os.Rename(oldPath, newPath); err != nil {
		t.Fatal(err)
	}
	// The watcher's own path: IndexPathsAsync on the DB actor, scanned off it.
	if err := SyncVoid(db, func(db *DB) error { return db.IndexPathsAsync([]string{DiskSpelling(oldPath)}) }); err != nil {
		t.Fatal(err)
	}
	var names []string
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		if st, err := db.fts.CheckFile(newPath); err == nil {
			if rec, err := db.fts.FileInfoByID(st.FileID); err == nil {
				names = rec.Names
				if len(names) > 0 && names[0] == newPath {
					// A full reindex takes a new file id, as any edit does; what
					// carries embeddings and routings is the chunk ids.
					if got := chunkIDs(st.FileID); fmt.Sprint(got) != fmt.Sprint(chunksBefore) {
						t.Errorf("chunk ids %v after the respell, want %v kept", got, chunksBefore)
					}
					if st.FileID != before.FileID {
						if _, err := db.fts.FileInfoByID(before.FileID); err == nil {
							t.Errorf("file id %d is still indexed beside %d: two records for one file", before.FileID, st.FileID)
						}
					}
					return
				}
			}
		}
		time.Sleep(50 * time.Millisecond)
	}
	t.Errorf("stored spelling after the watcher's scan %v, want %q: the unchanged skip kept the old name", names, newPath)
}

// TestCaseRenameRespellsOnStaleRefresh: a rename that also edits the file makes
// it stale, and the stale path comes from the index in its old spelling. The
// refresh must run under the current spelling and store it.
// (A rename with identical bytes is not stale at all: staleness is judged by
// content, measured 2026-10-26, so that case is the scan's, above.)
// CRC: crc-FilenameCase.md | R3631
func TestCaseRenameRespellsOnStaleRefresh(t *testing.T) {
	dbDir, srcDir := t.TempDir(), t.TempDir()
	seed := "[[source]]\n  dir = '" + srcDir + "'\n  include = [\"*.md\"]\n"
	if err := Init(dbDir, InitOpts{ConfigSeed: []byte(seed)}); err != nil {
		t.Fatal(err)
	}
	db, err := Open(dbDir)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	oldPath, newPath := filepath.Join(srcDir, "Casey.md"), filepath.Join(srcDir, "CASEY.md")
	if err := os.WriteFile(oldPath, []byte("case token ccc1\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := db.Add([]string{oldPath}, "markdown"); err != nil {
		t.Fatal(err)
	}
	if err := os.Rename(oldPath, newPath); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(newPath, []byte("case token ccc2, edited\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	// No pattern: on Windows the Matcher cannot yet match an absolute path
	// (windows-support 8.4), so a pattern would test that instead of this.
	if err := db.Refresh(nil); err != nil {
		t.Fatal(err)
	}
	st, err := db.fts.CheckFile(newPath)
	if err != nil {
		t.Fatal(err)
	}
	rec, err := db.fts.FileInfoByID(st.FileID)
	if err != nil {
		t.Fatal(err)
	}
	if len(rec.Names) == 0 || rec.Names[0] != newPath {
		t.Errorf("stored spelling after a stale refresh %v, want %q", rec.Names, newPath)
	}
	if st.Status != "fresh" {
		t.Errorf("status %q after refresh, want fresh: the refresh did not run", st.Status)
	}
}

// TestCaseRenameRespellsOnReconcile: the reconcile's refresh (RefreshAsync)
// takes stale paths from the index in their old spelling too, and must scan
// them under the current one.
// CRC: crc-FilenameCase.md | R3631
func TestCaseRenameRespellsOnReconcile(t *testing.T) {
	dbDir, srcDir := t.TempDir(), t.TempDir()
	seed := "[[source]]\n  dir = '" + srcDir + "'\n  include = [\"*.md\"]\n"
	if err := Init(dbDir, InitOpts{ConfigSeed: []byte(seed)}); err != nil {
		t.Fatal(err)
	}
	db, err := Open(dbDir)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	oldPath, newPath := filepath.Join(srcDir, "Casey.md"), filepath.Join(srcDir, "CASEY.md")
	if err := os.WriteFile(oldPath, []byte("case token ccc1\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := db.Add([]string{oldPath}, "markdown"); err != nil {
		t.Fatal(err)
	}
	if err := os.Rename(oldPath, newPath); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(newPath, []byte("case token ccc2, edited\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := SyncVoid(db, func(db *DB) error { return db.RefreshAsync() }); err != nil {
		t.Fatal(err)
	}
	var names []string
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		if st, err := db.fts.CheckFile(newPath); err == nil && st.Status == "fresh" {
			if rec, err := db.fts.FileInfoByID(st.FileID); err == nil {
				names = rec.Names
				if len(names) > 0 && names[0] == newPath {
					return
				}
			}
		}
		time.Sleep(50 * time.Millisecond)
	}
	t.Errorf("stored spelling after the reconcile %v, want %q", names, newPath)
}

// TestCaseRenamedLinkIsUnresolvable: a directory link renamed only in case
// still lstats under its old spelling; Observe must treat that spelling as no
// link at all. Needs the symlink privilege (the VM's admin session has it).
// CRC: crc-FilenameCase.md | R3629
func TestCaseRenamedLinkIsUnresolvable(t *testing.T) {
	src := t.TempDir()
	target := filepath.Join(src, "target")
	if err := os.MkdirAll(target, 0o755); err != nil {
		t.Fatal(err)
	}
	oldLink, newLink := filepath.Join(src, "Linky"), filepath.Join(src, "LINKY")
	if err := os.Symlink(target, oldLink); err != nil {
		t.Skipf("no symlink privilege here: %v", err)
	}
	lt := NewLinkTable(&Config{Sources: []Source{{Dir: src}}})
	if _, status := lt.Observe(oldLink); status == LinkUnresolvable {
		t.Fatalf("fixture: the link should resolve before the rename")
	}
	if err := os.Rename(oldLink, newLink); err != nil {
		t.Fatal(err)
	}
	if _, status := lt.Observe(oldLink); status != LinkUnresolvable {
		t.Errorf("Observe(old spelling) = %v, want LinkUnresolvable", status)
	}
	if _, status := lt.Observe(newLink); status == LinkUnresolvable {
		t.Errorf("Observe(new spelling) is unresolvable; the link itself is fine")
	}
}
