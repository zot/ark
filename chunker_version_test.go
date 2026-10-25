package ark

import (
	"fmt"
	"os"
	"strings"
	"testing"

	"github.com/zot/microfts2"
)

// CRC: crc-Indexer.md | Test: test-ChunkerVersion.md | R3597-R3600, R3611

// versionedLineChunker is a line chunker that declares a version, so a test can
// chunk a file under one version and then bump the rules under it.
type versionedLineChunker struct{ v uint32 }

func (versionedLineChunker) Chunks(path string, content []byte, yield func(microfts2.Chunk) bool) error {
	return microfts2.LineChunkFunc(path, content, yield)
}

func (c versionedLineChunker) ChunkerVersion() uint32 { return c.v }

// indexUnderVersionZero adds a file chunked under version 0 of "vline", then
// bumps the chunker to version 1, leaving the file stale by version only.
func indexUnderVersionZero(t *testing.T) (*Indexer, string) {
	t.Helper()
	idx, dir := testIndexer(t)
	if err := idx.fts.AddChunker("vline", versionedLineChunker{0}); err != nil {
		t.Fatal(err)
	}
	fp := writeFile(t, dir, "v.txt", "line one\nline two\n")
	if _, err := idx.AddFile(fp, "vline"); err != nil {
		t.Fatal(err)
	}
	if err := idx.fts.AddChunker("vline", versionedLineChunker{1}); err != nil {
		t.Fatal(err)
	}
	return idx, fp
}

// requireRechunked asserts nothing is stale and the file now carries version 1.
func requireRechunked(t *testing.T, idx *Indexer, fp string) {
	t.Helper()
	status, err := idx.fts.CheckFile(fp)
	if err != nil {
		t.Fatal(err)
	}
	if status.Status != "fresh" {
		t.Errorf("file is %s after refresh, want fresh", status.Status)
	}
	info, err := idx.fts.FileInfoByID(status.FileID)
	if err != nil {
		t.Fatal(err)
	}
	if info.ChunkerVersion != 1 {
		t.Errorf("file chunker version = %d, want 1", info.ChunkerVersion)
	}
}

// TestRefreshStaleRechunksVersionStaleFile: a byte-identical file that is stale
// only by chunker version is re-chunked, not skipped as unchanged. R3597
func TestRefreshStaleRechunksVersionStaleFile(t *testing.T) {
	idx, fp := indexUnderVersionZero(t)
	if status, err := idx.fts.CheckFile(fp); err != nil || status.Status != "stale" {
		t.Fatalf("setup: want the file stale after the bump, got %+v, %v", status, err)
	}
	if _, err := idx.RefreshStale(nil, nil); err != nil {
		t.Fatal(err)
	}
	requireRechunked(t, idx, fp)
}

// TestVersionStaleAppendFallsBackToReindex: a file that grew after its chunker
// was bumped is refused the append and re-chunked whole instead. R3598
func TestVersionStaleAppendFallsBackToReindex(t *testing.T) {
	idx, fp := indexUnderVersionZero(t)
	f, err := os.OpenFile(fp, os.O_APPEND|os.O_WRONLY, 0o644)
	if err != nil {
		t.Fatal(err)
	}
	f.WriteString("line three\n")
	f.Close()
	if err := idx.RefreshFile(fp, "vline"); err != nil {
		t.Fatalf("refresh of a version-stale grown file: %v", err)
	}
	requireRechunked(t, idx, fp)
}

// TestStoreResultActionVersionStale: a store refused for chunker version
// re-emits a forced scan; any other failure re-emits a plain one. R3599
func TestStoreResultActionVersionStale(t *testing.T) {
	refused := storeResultAction(fmt.Errorf("append x: %w", microfts2.ErrChunkerVersionStale))
	if !refused.retire || !refused.reEmit || !refused.force {
		t.Fatalf("version-stale refusal: got %+v, want {retire reEmit force}", refused)
	}
	other := storeResultAction(fmt.Errorf("disk error"))
	if other.force {
		t.Fatalf("plain store failure must not force: got %+v", other)
	}
}

// TestScanForcesVersionStaleFile: a file scan of a file judged stale whose bytes
// match its record runs forced, so the store-time unchanged escape cannot skip
// it. R3600
func TestScanForcesVersionStaleFile(t *testing.T) {
	idx, fp := indexUnderVersionZero(t)
	status, err := idx.fts.CheckFile(fp)
	if err != nil || status.Status != "stale" {
		t.Fatalf("setup: want the file stale after the bump, got %+v, %v", status, err)
	}
	prep, err := idx.prepareScan(fp, "vline", status.FileID, true, true, false, newFakeGate())
	if err != nil {
		t.Fatal(err)
	}
	if err := idx.storeScan(prep); err != nil {
		t.Fatal(err)
	}
	requireRechunked(t, idx, fp)
}

// TestChatSubchunksKeepVoice: every sub-chunk of a multi-paragraph thinking
// block carries the tags, and plain text after it carries none. R3611
func TestChatSubchunksKeepVoice(t *testing.T) {
	content := "<thinking>first part\n\nsecond part\n\nthird part</thinking>\n\nplain answer"
	var got []string
	for _, c := range chatSubchunks(content) {
		got = append(got, strings.TrimSpace(string(c.Content)))
	}
	want := []string{
		"<thinking>first part</thinking>",
		"<thinking>second part</thinking>",
		"<thinking>third part</thinking>",
		"plain answer",
	}
	if strings.Join(got, "|") != strings.Join(want, "|") {
		t.Fatalf("sub-chunks:\n%q\nwant\n%q", got, want)
	}
}
