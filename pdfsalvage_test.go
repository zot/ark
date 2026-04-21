package ark

// Test: crc-PDFChunker.md | Seq: seq-pdf-salvage.md | R1652-R1660

import (
	"bytes"
	"compress/zlib"
	"strings"
	"testing"

	"github.com/zot/microfts2"
)

// collectSalvage runs salvageChunks and copies the results so tests
// can assert independently of any shared backing storage.
func collectSalvage(content []byte) []microfts2.Chunk {
	src := salvageChunks(content)
	out := make([]microfts2.Chunk, len(src))
	for i, c := range src {
		out[i] = microfts2.Chunk{
			Range:   append([]byte{}, c.Range...),
			Content: append([]byte{}, c.Content...),
			Attrs:   append([]microfts2.Pair{}, c.Attrs...),
		}
	}
	return out
}

// The canopy cover-letter fixture: single content stream, uncompressed,
// one Tj string. Reproduces the exact shape ark's demo PDFs use.
const canopyPDF = `%PDF-1.4
1 0 obj << /Type /Catalog /Pages 2 0 R >> endobj
2 0 obj << /Type /Pages /Kids [4 0 R] /Count 1 >> endobj
3 0 obj
<< /Length 59 >>
stream
BT /F1 12 Tf 72 720 Td (Cover Letter - Canopy Health) Tj ET
endstream
endobj
4 0 obj << /Type /Page /Parent 2 0 R /MediaBox [0 0 612 792] /Contents 3 0 R /Resources << /Font << /F1 5 0 R >> >> >> endobj
5 0 obj << /Type /Font /Subtype /Type1 /BaseFont /Helvetica >> endobj
xref
0 6
0000000000 65535 f
trailer << /Size 6 /Root 1 0 R >>
startxref
420
%%EOF`

// R1655, R1657: extract the single Tj string and emit one salvage/1 chunk.
func TestSalvageCanopyFixture(t *testing.T) {
	chunks := collectSalvage([]byte(canopyPDF))
	if len(chunks) != 1 {
		t.Fatalf("expected 1 chunk, got %d", len(chunks))
	}
	c := chunks[0]
	if string(c.Range) != "salvage/1" {
		t.Errorf("expected Range=salvage/1, got %q", c.Range)
	}
	if string(c.Content) != "Cover Letter - Canopy Health" {
		t.Errorf("expected 'Cover Letter - Canopy Health', got %q", c.Content)
	}
	// R1658: page attribute set, rect absent.
	gotPage := ""
	gotRect := false
	for _, a := range c.Attrs {
		if string(a.Key) == "page" {
			gotPage = string(a.Value)
		}
		if string(a.Key) == "rect" {
			gotRect = true
		}
	}
	if gotPage != "0" {
		t.Errorf("expected page=0 (salvage bucket), got %q", gotPage)
	}
	if gotRect {
		t.Error("salvage chunk must not carry rect attribute")
	}
}

// R1655: TJ array form should concatenate its string elements and skip numbers.
func TestSalvageTJArray(t *testing.T) {
	doc := []byte(`%PDF-1.4
1 0 obj << /Length 99 >>
stream
BT /F1 12 Tf 72 720 Td [(Hello )-50( World) 30 (!)] TJ ET
endstream
endobj
%%EOF`)
	chunks := collectSalvage(doc)
	if len(chunks) != 1 {
		t.Fatalf("expected 1 chunk, got %d", len(chunks))
	}
	got := strings.TrimSpace(string(chunks[0].Content))
	if got != "Hello  World!" {
		t.Errorf("expected 'Hello  World!', got %q", got)
	}
}

// R1656: PDF string escape sequences decode correctly inside Tj literals.
func TestSalvageEscapes(t *testing.T) {
	doc := []byte(`%PDF-1.4
1 0 obj << /Length 99 >>
stream
BT (A\(B\)C\\D\nE\tF\101) Tj ET
endstream
endobj
%%EOF`)
	chunks := collectSalvage(doc)
	if len(chunks) != 1 {
		t.Fatalf("expected 1 chunk, got %d", len(chunks))
	}
	got := strings.TrimSpace(string(chunks[0].Content))
	want := "A(B)C\\D\nE\tFA" // \101 octal = 65 = 'A'
	if got != want {
		t.Errorf("expected %q, got %q", want, got)
	}
}

// R1654: FlateDecode-compressed streams are decompressed before extraction.
func TestSalvageFlateDecode(t *testing.T) {
	// Build a zlib-compressed content stream with one Tj.
	inner := []byte("BT /F1 12 Tf 72 720 Td (Compressed greeting) Tj ET\n")
	var buf bytes.Buffer
	w := zlib.NewWriter(&buf)
	w.Write(inner)
	w.Close()
	compressed := buf.Bytes()

	// Assemble a minimal PDF-ish wrapper around the compressed stream.
	var doc bytes.Buffer
	doc.WriteString("%PDF-1.4\n1 0 obj << /Length ")
	doc.WriteString(intStr(len(compressed)))
	doc.WriteString(" /Filter /FlateDecode >>\nstream\n")
	doc.Write(compressed)
	doc.WriteString("\nendstream\nendobj\n%%EOF")

	chunks := collectSalvage(doc.Bytes())
	if len(chunks) != 1 {
		t.Fatalf("expected 1 chunk, got %d", len(chunks))
	}
	got := strings.TrimSpace(string(chunks[0].Content))
	if got != "Compressed greeting" {
		t.Errorf("expected 'Compressed greeting', got %q", got)
	}
}

// R1659: a stream with no text operators yields nothing.
func TestSalvageNoText(t *testing.T) {
	doc := []byte(`%PDF-1.4
1 0 obj << /Length 20 >>
stream
q 100 0 0 100 0 0 cm Q
endstream
endobj
%%EOF`)
	chunks := collectSalvage(doc)
	if len(chunks) != 0 {
		t.Errorf("expected no chunks for text-free stream, got %d", len(chunks))
	}
}

// R1659: a stream with an unknown filter is skipped, not yielded.
func TestSalvageUnknownFilter(t *testing.T) {
	doc := []byte(`%PDF-1.4
1 0 obj << /Length 30 /Filter /LZWDecode >>
stream
BT (unreachable text) Tj ET
endstream
endobj
%%EOF`)
	chunks := collectSalvage(doc)
	if len(chunks) != 0 {
		t.Errorf("expected 0 chunks for unsupported filter, got %d: %q", len(chunks), chunks)
	}
}

// Two content streams produce two salvage chunks numbered 1 and 2.
func TestSalvageMultipleStreams(t *testing.T) {
	doc := []byte(`%PDF-1.4
1 0 obj << /Length 50 >>
stream
BT (First page text) Tj ET
endstream
endobj
2 0 obj << /Length 50 >>
stream
BT (Second page text) Tj ET
endstream
endobj
%%EOF`)
	chunks := collectSalvage(doc)
	if len(chunks) != 2 {
		t.Fatalf("expected 2 chunks, got %d", len(chunks))
	}
	if string(chunks[0].Range) != "salvage/1" {
		t.Errorf("first Range: got %q", chunks[0].Range)
	}
	if string(chunks[1].Range) != "salvage/2" {
		t.Errorf("second Range: got %q", chunks[1].Range)
	}
	if !strings.Contains(string(chunks[0].Content), "First") {
		t.Errorf("first content: %q", chunks[0].Content)
	}
	if !strings.Contains(string(chunks[1].Content), "Second") {
		t.Errorf("second content: %q", chunks[1].Content)
	}
}

// End-to-end: PDFChunker.Chunks on a malformed PDF routes to salvage.
func TestPDFChunkerFallsBackToSalvage(t *testing.T) {
	c := &PDFChunker{}
	var chunks []microfts2.Chunk
	err := c.Chunks("test.pdf", []byte(canopyPDF), func(ch microfts2.Chunk) bool {
		chunks = append(chunks, microfts2.Chunk{
			Range:   append([]byte{}, ch.Range...),
			Content: append([]byte{}, ch.Content...),
		})
		return true
	})
	if err != nil {
		t.Fatalf("Chunks returned error: %v", err)
	}
	if len(chunks) != 1 {
		t.Fatalf("expected 1 salvage chunk, got %d", len(chunks))
	}
	if !strings.Contains(string(chunks[0].Content), "Canopy Health") {
		t.Errorf("expected 'Canopy Health' in content, got %q", chunks[0].Content)
	}
}

// intStr is a tiny helper — avoids importing strconv just for this one use.
func intStr(n int) string {
	if n == 0 {
		return "0"
	}
	var buf [20]byte
	i := len(buf)
	for n > 0 {
		i--
		buf[i] = byte('0' + n%10)
		n /= 10
	}
	return string(buf[i:])
}
