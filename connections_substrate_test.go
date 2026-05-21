package ark

// CRC: crc-Librarian.md | Test: test-FindConnectionsSubstrate.md
// Tests for the normal-mode substrate pipeline. Embedding-dependent
// passes are skipped (vector substrates return zero); trigram passes
// run against real chunks and tag defs so the pipeline exercises the
// V-record vote path end-to-end.
// R2567, R2569, R2570, R2572, R2573, R2581, R2584, R2588, R2598

import (
	"strings"
	"testing"
	"time"
)

func TestSubstrate_NormalizeRejectsUnknownChunk(t *testing.T) {
	l, _, _ := setupConnections(t)
	_, _, err := l.normalizeInputs([]ConnectionsInput{{ChunkID: 99999999}}, true)
	if err == nil || !strings.Contains(err.Error(), "unknown chunk 99999999") {
		t.Fatalf("want unknown chunk 99999999, got %v", err)
	}
}

func TestSubstrate_NormalizeRejectsEmptyInputs(t *testing.T) {
	l, _, _ := setupConnections(t)
	_, _, err := l.normalizeInputs(nil, true)
	if err == nil || err.Error() != "chunkIDs/text/range empty" {
		t.Fatalf("want chunkIDs/text/range empty, got %v", err)
	}
}

func TestSubstrate_NormalizeRejectsPathWithoutRange(t *testing.T) {
	l, _, _ := setupConnections(t)
	_, _, err := l.normalizeInputs([]ConnectionsInput{{Path: "foo.md"}}, true)
	if err == nil || !strings.Contains(err.Error(), "requires a range") {
		t.Fatalf("want requires a range, got %v", err)
	}
}

func TestSubstrate_NormalizeRejectsMissingPath(t *testing.T) {
	l, _, _ := setupConnections(t)
	_, _, err := l.normalizeInputs([]ConnectionsInput{{Path: "missing.md", Range: "1-10"}}, true)
	if err == nil || !strings.Contains(err.Error(), "not found") {
		t.Fatalf("want path not found, got %v", err)
	}
}

func TestSubstrate_NormalizeRejectsBadRange(t *testing.T) {
	l, db, _ := setupConnections(t)
	_, p := indexLine(t, db, "a.txt", "hello\n")
	_, _, err := l.normalizeInputs([]ConnectionsInput{{Path: p, Range: "abc-xyz"}}, true)
	if err == nil || !strings.Contains(err.Error(), "parse error") {
		t.Fatalf("want path:range parse error, got %v", err)
	}
}

func TestSubstrate_NormalizeAcceptsTextInput(t *testing.T) {
	l, db, _ := setupConnections(t)
	indexLine(t, db, "a.txt", "hello\n")
	inputs, chunkIDs, err := l.normalizeInputs([]ConnectionsInput{{Text: "what is this about"}}, true)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(inputs) != 1 || inputs[0].text != "what is this about" {
		t.Fatalf("want one text input, got %+v", inputs)
	}
	if len(chunkIDs) != 0 {
		t.Fatalf("text input should not produce chunkIDs: %v", chunkIDs)
	}
}

func TestSubstrate_NormalizeExpandsPathRangeToChunks(t *testing.T) {
	l, db, _ := setupConnections(t)
	// Single-line indexer produces one chunk per file; the range covers
	// that chunk. Verifies path:range → chunkID expansion succeeds.
	c1, p := indexLine(t, db, "a.txt", "alpha\n")
	inputs, ids, err := l.normalizeInputs([]ConnectionsInput{{Path: p, Range: "1-1"}}, true)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(inputs) != 1 || inputs[0].chunkID != c1 {
		t.Fatalf("want one chunk %d, got %+v", c1, inputs)
	}
	if len(ids) != 1 || ids[0] != c1 {
		t.Fatalf("want chunkIDs [%d], got %v", c1, ids)
	}
}

func TestSubstrate_FindConnectionsNormalRejectsUnknownChunkAtEnqueue(t *testing.T) {
	l, _, _ := setupConnections(t)
	_, err := l.FindConnections([]ConnectionsInput{{ChunkID: 9999999}}, FindConnectionsOpts{Mode: "normal"})
	if err == nil || !strings.Contains(err.Error(), "unknown chunk") {
		t.Fatalf("want unknown chunk, got %v", err)
	}
}

func TestSubstrate_FindConnectionsCompletesPendingToDone(t *testing.T) {
	l, db, _ := setupConnections(t)
	chunkID, _ := indexLine(t, db, "a.txt", "hello world\n")
	id, err := l.FindConnections([]ConnectionsInput{{ChunkID: chunkID}}, FindConnectionsOpts{Mode: "normal"})
	if err != nil {
		t.Fatalf("FindConnections: %v", err)
	}
	// Allow the substrate goroutine to complete.
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		snap := l.ConnectionRecordSnapshot(id)
		if snap != nil && snap.Done {
			if snap.Status != "completed" {
				t.Fatalf("want status completed, got %q (error=%q)", snap.Status, snap.Error)
			}
			if snap.Mode != "normal" {
				t.Fatalf("want mode normal, got %q", snap.Mode)
			}
			if snap.Purpose != "curate" {
				t.Fatalf("want purpose curate, got %q", snap.Purpose)
			}
			body := readTmpBody(t, db, snap.Path)
			if !strings.Contains(body, "@connections-mode: normal") {
				t.Errorf("body missing @connections-mode: normal: %s", body)
			}
			if !strings.Contains(body, "@purpose: curate") {
				t.Errorf("body missing @purpose: curate: %s", body)
			}
			if !strings.Contains(body, "## Proposals") {
				t.Errorf("body missing ## Proposals section: %s", body)
			}
			return
		}
		time.Sleep(20 * time.Millisecond)
	}
	t.Fatalf("substrate did not complete: %+v", l.ConnectionRecordSnapshot(id))
}

func TestSubstrate_ParseConnectionsDocRoundTrip(t *testing.T) {
	doc := ConnectionsDoc{
		Status:    "completed",
		Purpose:   "curate",
		Mode:      "normal",
		RequestID: "abc-123",
		Proposals: []ConnectionsPropo{
			{
				Kind:           "tag-name",
				Value:          "design-decision",
				Score:          0.84,
				EvidenceChunks: []uint64{4711, 5023},
				PerSubstrate: SubstrateScores{
					VectorED:  0.91,
					TrigramED: 0.78,
					VectorEC:  0.65,
					TrigramEC: 0.40,
				},
			},
		},
	}
	body := buildSampleConnectionsBody(doc)
	parsed := ParseConnectionsDoc([]byte(body))
	if parsed.Status != "completed" || parsed.Mode != "normal" || parsed.Purpose != "curate" {
		t.Fatalf("headers parsed wrong: %+v", parsed)
	}
	if len(parsed.Proposals) != 1 || parsed.Proposals[0].Value != "design-decision" {
		t.Fatalf("proposals parsed wrong: %+v", parsed.Proposals)
	}
	if parsed.Proposals[0].PerSubstrate.VectorED != 0.91 {
		t.Fatalf("vector-ed score parsed wrong: %+v", parsed.Proposals[0].PerSubstrate)
	}
}

// buildSampleConnectionsBody assembles a hand-shaped doc body to feed
// into ParseConnectionsDoc. Keeps the parser test independent of the
// renderer.
func buildSampleConnectionsBody(doc ConnectionsDoc) string {
	var b strings.Builder
	b.WriteString("@connections-status: " + doc.Status + "\n")
	b.WriteString("@purpose: " + doc.Purpose + "\n")
	b.WriteString("@connections-mode: " + doc.Mode + "\n")
	b.WriteString("@connections-request-id: " + doc.RequestID + "\n")
	b.WriteString("\n## Proposals\n\n")
	for _, p := range doc.Proposals {
		b.WriteString("- @proposal-kind: " + p.Kind + "\n")
		b.WriteString("  @proposal-value: \"" + p.Value + "\"\n")
		b.WriteString("  @proposal-score: 0.8400\n")
		b.WriteString("  @proposal-evidence-chunks: 4711,5023\n")
		b.WriteString("  @proposal-evidence-vector-ed: 0.9100\n")
		b.WriteString("  @proposal-evidence-trigram-ed: 0.7800\n")
		b.WriteString("  @proposal-evidence-vector-ec: 0.6500\n")
		b.WriteString("  @proposal-evidence-trigram-ec: 0.4000\n\n")
	}
	return b.String()
}
