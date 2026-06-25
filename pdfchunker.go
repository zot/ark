package ark

import (
	"bytes"
	"compress/zlib"
	"crypto/sha256"
	"fmt"
	"io"
	"log"
	"math"
	"os"
	"regexp"
	"strconv"
	"strings"
	"sync"

	"github.com/zot/microfts2"
	"github.com/zot/pdftext"
)

// CRC: crc-PDFChunker.md | Seq: seq-pdf-chunk.md | R1729-R1738

// attrContentOffset and attrContentLen locate a chunk's text inside
// its page's compressed blob.
// CRC: crc-PDFChunker.md | R1719
const (
	attrContentOffset = "content_offset"
	attrContentLen    = "content_len"
)

// pendingPageBlob is one page's compressed text awaiting a fileid. R1720
type pendingPageBlob struct {
	page uint32
	blob []byte
}

// PDFChunker maps pdftext Blocks to ark chunks and manages the
// per-page text blob cache. Holds a reference to ark.DB so
// FileChunks can stage blobs that FlushBlobs writes once microfts2
// has assigned a fileid. R1720, R1726
type PDFChunker struct {
	db      *DB
	mu      sync.Mutex
	pending map[string][]pendingPageBlob
}

// NewPDFChunker constructs a chunker bound to ark's DB.
func NewPDFChunker(db *DB) *PDFChunker {
	return &PDFChunker{db: db, pending: make(map[string][]pendingPageBlob)}
}

// IsWritable reports whether the chunker handles editable text.
// Always false for PDF — binary format with no text-edit primitive.
// CRC: crc-PDFChunker.md | R2388
func (c *PDFChunker) IsWritable() bool { return false }

// CommentSyntax returns the line-comment delimiter for inline tag
// authoring. Empty for PDFs — no line-comment convention applies.
// CRC: crc-PDFChunker.md | R2388
func (c *PDFChunker) CommentSyntax() string { return "" }

// Chunks implements microfts2.Chunker. microfts2's collectChunks
// dispatch prefers Chunker over FileChunker when both are
// implemented, so this entry point is load-bearing for indexed-
// file persistence — it MUST stage page blobs. The retrieval-
// time streaming fallback uses the persist=false private helper
// so retrieval doesn't masquerade as indexing. R1729, R1734, R2428
// CRC: crc-PDFChunker.md | R1729, R1730, R1734, R2428
func (c *PDFChunker) Chunks(path string, content []byte, yield func(microfts2.Chunk) bool) error {
	return c.chunks(path, content, yield, true)
}

// chunks is the persist-parameterized private helper behind Chunks
// and streamingRetrieve. persist=true seals page blobs; persist=false
// is for retrieval-time invocations that must not stage. R2428, R2429
// CRC: crc-PDFChunker.md | R2428, R2429
func (c *PDFChunker) chunks(path string, content []byte, yield func(microfts2.Chunk) bool, persist bool) error {
	doc, err := pdftext.Open(bytes.NewReader(content), int64(len(content)))
	if err != nil {
		log.Printf("pdf: open %s: %v", path, err)
		return nil
	}
	defer doc.Close()
	c.extractDoc(path, doc, yield, persist)
	return nil
}

// FileChunks implements microfts2.FileChunker for indexed files. R1729, R1734
// CRC: crc-PDFChunker.md | R1729, R1734
func (c *PDFChunker) FileChunks(path string, oldHash [32]byte, yield func(microfts2.Chunk) bool) ([32]byte, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return [32]byte{}, fmt.Errorf("pdf read %s: %w", path, err)
	}
	hash := sha256.Sum256(data)
	if hash == oldHash {
		return hash, nil // unchanged, skip
	}
	c.resetPending(path)
	doc, err := pdftext.Open(bytes.NewReader(data), int64(len(data)))
	if err != nil {
		log.Printf("pdf: open %s: %v", path, err)
		return hash, nil
	}
	defer doc.Close()
	c.extractDoc(path, doc, yield, true)
	return hash, nil
}

// extractDoc walks the document, maps each Block to an ark chunk,
// seals each page's blob, and yields. When persist is true, sealed
// blobs are staged in c.pending[path] for FlushBlobs to write once
// the fileid is known. R1720, R1730, R1733
// CRC: crc-PDFChunker.md | R1721
func (c *PDFChunker) extractDoc(path string, doc *pdftext.Doc, yield func(microfts2.Chunk) bool, persist bool) {
	for page := range doc.Pages() {
		pageNum := page.Number()
		blocks := page.Blocks()
		cb := page.CropBox()
		chunks := pageChunks(pageNum, blocks, [2]float64{cb.X + cb.W, cb.Y + cb.H})
		Logv(1, "pdf: page %d: %d blocks, %d chunks", pageNum, len(blocks), len(chunks))
		if len(chunks) == 0 {
			continue // R1733: empty page emits nothing
		}
		if persist {
			if blob := sealPageBlob(chunks); blob != nil {
				c.pushPending(path, uint32(pageNum), blob)
			}
		}
		for _, ch := range chunks {
			if !yield(ch) {
				return
			}
		}
	}
}

// pageChunks maps a page's Blocks to ark chunks, assigning
// per-kind 1-indexed locations (PAGE/para/N, PAGE/heading/N, etc.).
// Returns nil when the page has no indexable blocks. R1730, R1733, R1738
func pageChunks(pageNum int, blocks []pdftext.Block, cropExtent [2]float64) []microfts2.Chunk {
	if len(blocks) == 0 {
		return nil
	}
	pageStr := strconv.Itoa(pageNum)
	pageSize := cropExtent
	counters := make(map[string]int)
	var chunks []microfts2.Chunk
	for i := range blocks {
		b := &blocks[i]
		suffix, ok := locationSuffix(b.Kind)
		if !ok {
			continue // R1730: Image skipped
		}
		counters[suffix]++
		n := counters[suffix]
		chunks = append(chunks, blockToChunk(pageStr, suffix, n, b, pageSize))
	}
	return chunks
}

// locationSuffix maps a pdftext BlockKind to an ark location suffix.
// Returns ok=false for Image blocks (no indexable text). R1730
func locationSuffix(k pdftext.BlockKind) (string, bool) {
	switch k {
	case pdftext.Paragraph, pdftext.Irregular:
		return "para", true
	case pdftext.Heading:
		return "heading", true
	case pdftext.Table:
		return "table", true
	case pdftext.List:
		return "list", true
	case pdftext.Salvage:
		return "salvage", true
	}
	return "", false
}

// blockToChunk builds one microfts2.Chunk from a pdftext.Block.
// Content is Caption + "\n" + Text (Caption empty → Text only).
// R1730, R1731
func blockToChunk(pageStr, suffix string, n int, b *pdftext.Block, pageSize [2]float64) microfts2.Chunk {
	content := joinCaptionText(b.Caption, b.Text)
	rect := clampRectToPage(pdfRect{X: b.BBox.X, Y: b.BBox.Y, W: b.BBox.W, H: b.BBox.H}, pageSize)
	fontSize := 0.0
	if b.Kind == pdftext.Heading {
		fontSize = b.FontSize
	}
	attrs := chunkAttrs(pageStr, rect, fontSize, pageSize, b)
	return microfts2.Chunk{
		Range:   fmt.Appendf(nil, "%s/%s/%d", pageStr, suffix, n),
		Content: []byte(strings.TrimRight(content, "\n")),
		Attrs:   attrs,
	}
}

// joinCaptionText prepends caption to text with a newline separator
// so FTS sees both in one chunk. Caption-only or text-only cases
// degenerate cleanly. R1731
func joinCaptionText(caption, text string) string {
	switch {
	case caption == "":
		return text
	case text == "":
		return caption
	}
	return caption + "\n" + text
}

// FlushBlobs writes staged page blobs for path under fileid, replacing
// any previously stored blobs for the same file. R1720, R1724
// CRC: crc-PDFChunker.md | R1720, R1724
func (c *PDFChunker) FlushBlobs(path string, fileid uint64) error {
	c.mu.Lock()
	blobs := c.pending[path]
	delete(c.pending, path)
	c.mu.Unlock()
	if c.db == nil || c.db.store == nil || len(blobs) == 0 {
		return nil
	}
	if err := c.db.store.RemovePageContents(fileid); err != nil {
		return fmt.Errorf("pdf: clear page contents %d: %w", fileid, err)
	}
	for _, pb := range blobs {
		if err := c.db.store.WritePageContent(fileid, pb.page, pb.blob); err != nil {
			return fmt.Errorf("pdf: write page content %d/%d: %w", fileid, pb.page, err)
		}
	}
	return nil
}

// resetPending clears staging state for this path so a retry doesn't
// inherit data from a failed prior pass. R1724
func (c *PDFChunker) resetPending(path string) {
	c.mu.Lock()
	delete(c.pending, path)
	c.mu.Unlock()
}

// pushPending appends one sealed page blob to the staging area.
func (c *PDFChunker) pushPending(path string, page uint32, blob []byte) {
	c.mu.Lock()
	c.pending[path] = append(c.pending[path], pendingPageBlob{page: page, blob: blob})
	c.mu.Unlock()
}

// sealPageBlob concatenates each chunk's text (null-byte separated),
// records each chunk's offset/length in its Attrs, and returns the
// zlib-compressed blob. Returns nil on compression failure.
// CRC: crc-PDFChunker.md | R1719, R1721, R1722
func sealPageBlob(chunks []microfts2.Chunk) []byte {
	if len(chunks) == 0 {
		return nil
	}
	var raw bytes.Buffer
	for i := range chunks {
		if i > 0 {
			raw.WriteByte(0)
		}
		offset := raw.Len()
		raw.Write(chunks[i].Content)
		chunks[i].Attrs = append(chunks[i].Attrs,
			microfts2.Pair{Key: []byte(attrContentOffset), Value: []byte(strconv.Itoa(offset))},
			microfts2.Pair{Key: []byte(attrContentLen), Value: []byte(strconv.Itoa(len(chunks[i].Content)))},
		)
	}
	var out bytes.Buffer
	w := zlib.NewWriter(&out)
	if _, err := w.Write(raw.Bytes()); err != nil {
		return nil
	}
	if err := w.Close(); err != nil {
		return nil
	}
	return out.Bytes()
}

// GetChunk implements microfts2.RandomAccessChunker. R1726, R1728
// CRC: crc-PDFChunker.md | Seq: seq-pdf-chunk-retrieval.md | R1726, R1727
func (c *PDFChunker) GetChunk(path string, _ []byte, customData *any, chunk *microfts2.Chunk) error {
	if text, ok := c.fastRetrieve(path, customData, chunk); ok {
		chunk.Content = text
		return nil
	}
	text, ok := c.streamingRetrieve(path, string(chunk.Range))
	if !ok {
		return fmt.Errorf("pdf: chunk %s/%s not found", path, chunk.Range)
	}
	chunk.Content = text
	return nil
}

// fastRetrieve reads Attrs, fetches/decompresses the page blob, and
// slices the requested chunk text. Returns false on any missing step.
func (c *PDFChunker) fastRetrieve(path string, customData *any, chunk *microfts2.Chunk) ([]byte, bool) {
	if c.db == nil || c.db.store == nil {
		return nil, false
	}
	page, offset, length, ok := parseContentAttrs(chunk.Attrs)
	if !ok {
		return nil, false
	}
	info, err := c.db.fts.CheckFile(path)
	if err != nil || info.FileID == 0 {
		return nil, false
	}
	cache := customDataPageCache(customData)
	decompressed, ok := cache[page]
	if !ok {
		blob, err := c.db.store.ReadPageContent(info.FileID, page)
		if err != nil || blob == nil {
			return nil, false
		}
		text, err := zlibDecompress(blob)
		if err != nil {
			return nil, false
		}
		cache[page] = text
		decompressed = text
	}
	if offset+length > len(decompressed) {
		return nil, false
	}
	out := make([]byte, length)
	copy(out, decompressed[offset:offset+length])
	return out, true
}

// streamingRetrieve runs the non-persisting chunker path until it finds
// the target range and returns its content. Fallback when fastRetrieve
// can't. Does not stage pending blobs — retrieval must not masquerade
// as indexing. R1728, R2429
// CRC: crc-PDFChunker.md | R1728, R2429
func (c *PDFChunker) streamingRetrieve(path, rangeLabel string) ([]byte, bool) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, false
	}
	var result []byte
	var found bool
	_ = c.chunks(path, data, func(ch microfts2.Chunk) bool {
		if string(ch.Range) == rangeLabel {
			result = append(result[:0], ch.Content...)
			found = true
			return false
		}
		return true
	}, false)
	return result, found
}

// parseContentAttrs extracts (page, offset, length) from chunk attrs.
func parseContentAttrs(attrs []microfts2.Pair) (page uint32, offset, length int, ok bool) {
	var pageStr, offStr, lenStr string
	for _, a := range attrs {
		switch string(a.Key) {
		case "page":
			pageStr = string(a.Value)
		case attrContentOffset:
			offStr = string(a.Value)
		case attrContentLen:
			lenStr = string(a.Value)
		}
	}
	if offStr == "" || lenStr == "" {
		return 0, 0, 0, false
	}
	p64, err := strconv.ParseUint(pageStr, 10, 32)
	if err != nil && pageStr != "" {
		return 0, 0, 0, false
	}
	off, err := strconv.Atoi(offStr)
	if err != nil || off < 0 {
		return 0, 0, 0, false
	}
	ln, err := strconv.Atoi(lenStr)
	if err != nil || ln < 0 {
		return 0, 0, 0, false
	}
	return uint32(p64), off, ln, true
}

// customDataPageCache returns (and lazily initializes) the decompressed-
// page-blob cache inside microfts2's per-file customData.
// CRC: crc-PDFChunker.md | R1727
func customDataPageCache(customData *any) map[uint32][]byte {
	if *customData == nil {
		cache := make(map[uint32][]byte)
		*customData = cache
		return cache
	}
	if cache, ok := (*customData).(map[uint32][]byte); ok {
		return cache
	}
	cache := make(map[uint32][]byte)
	*customData = cache
	return cache
}

// zlibDecompress reads a zlib-compressed blob and returns the raw bytes.
func zlibDecompress(blob []byte) ([]byte, error) {
	zr, err := zlib.NewReader(bytes.NewReader(blob))
	if err != nil {
		return nil, err
	}
	defer zr.Close()
	return io.ReadAll(zr)
}

// pdfRect is a bounding box in PDF points (bottom-left origin).
type pdfRect struct {
	X, Y, W, H float64
}

func (r pdfRect) String() string {
	return fmt.Sprintf("%.0f,%.0f,%.0f,%.0f", r.X, r.Y, r.W, r.H)
}

// clampRectToPage keeps a chunk rect within the observed page extent.
// Degenerate rects (collapsed after clamp) fall back to the original.
func clampRectToPage(r pdfRect, pageSize [2]float64) pdfRect {
	pageW, pageH := pageSize[0], pageSize[1]
	if pageW <= 0 || pageH <= 0 {
		return r
	}
	x0, y0 := r.X, r.Y
	x1, y1 := r.X+r.W, r.Y+r.H
	if x0 < 0 {
		x0 = 0
	}
	if y0 < 0 {
		y0 = 0
	}
	if x1 > pageW {
		x1 = pageW
	}
	if y1 > pageH {
		y1 = pageH
	}
	if x1 <= x0 || y1 <= y0 {
		return r
	}
	return pdfRect{X: x0, Y: y0, W: x1 - x0, H: y1 - y0}
}

// chunkAttrs builds the Attrs slice for a chunk.
// CRC: crc-PDFChunker.md | R1637, R1638, R1639, R1665
func chunkAttrs(page string, rect pdfRect, fontSize float64, pageSize [2]float64, b *pdftext.Block) []microfts2.Pair {
	attrs := []microfts2.Pair{
		{Key: []byte("page"), Value: []byte(page)},
		{Key: []byte("rect"), Value: []byte(rect.String())},
	}
	if fontSize > 0 {
		attrs = append(attrs, microfts2.Pair{
			Key:   []byte("font_size"),
			Value: fmt.Appendf(nil, "%.1f", fontSize),
		})
	}
	if pageSize[0] > 0 && pageSize[1] > 0 {
		attrs = append(attrs, microfts2.Pair{
			Key:   []byte("page_size"),
			Value: fmt.Appendf(nil, "%s,%s", formatPdfFloat(pageSize[0]), formatPdfFloat(pageSize[1])),
		})
	}
	if tr := extractTagRects(b); tr != "" {
		attrs = append(attrs, microfts2.Pair{
			Key:   []byte("tag_rects"),
			Value: []byte(tr),
		})
		if ts := extractTagSegments(b); ts != "" {
			attrs = append(attrs, microfts2.Pair{
				Key:   []byte("tag_segments"),
				Value: []byte(ts),
			})
		}
	}
	return attrs
}

// tagRectPattern mirrors the generic regex in tagblock.go so rects
// only record tags that ark's generic extraction will index.
// CRC: crc-PDFChunker.md | R1676
var tagRectPattern = regexp.MustCompile(`@([a-zA-Z][\w.-]*):\s*([^\n]*)`)

// extractTagRects scans Block.Text (and Block.Caption) for the ark
// tag pattern and returns the compact `tag_rects` chunk attribute —
// one "name=value@x,y,w,h" entry per tag, semicolon-separated. Each
// rect is the union of the Block.Chars/CaptionChars BBoxes covering
// the match's byte range. Empty when the block has no tags. R1735, R1736
func extractTagRects(b *pdftext.Block) string {
	var entries []string
	entries = appendTagRectEntries(entries, b.Text, b.Chars)
	entries = appendTagRectEntries(entries, b.Caption, b.CaptionChars)
	return strings.Join(entries, ";")
}

// appendTagRectEntries scans one text+chars slice for tag matches and
// appends one encoded entry per match. Used for both Text/Chars and
// Caption/CaptionChars. R1735
func appendTagRectEntries(entries []string, text string, chars []pdftext.Char) []string {
	if text == "" || len(chars) == 0 {
		return entries
	}
	for _, m := range tagRectPattern.FindAllStringSubmatchIndex(text, -1) {
		if len(m) < 6 {
			continue
		}
		start, end := m[0], m[1]
		name := text[m[2]:m[3]]
		value := strings.TrimSpace(text[m[4]:m[5]])
		rect, ok := charRangeRect(chars, start, end)
		if !ok {
			continue
		}
		entries = append(entries, fmt.Sprintf(
			"%s=%s@%s,%s,%s,%s",
			encodeTagRectField(name),
			encodeTagRectField(value),
			formatPdfFloat(rect.X),
			formatPdfFloat(rect.Y),
			formatPdfFloat(rect.W),
			formatPdfFloat(rect.H),
		))
	}
	return entries
}

// extractTagSegments emits the `tag_segments` attribute: per-tag
// bounds split into @ / name / : / value segments, index-aligned
// with tag_rects. The value segment is a list of rects — one per
// physical line — so wrapped values carry precise per-line bounds.
// Format: `atRect|nameRect|colonRect|valRect1|valRect2|...;nextTag…`
// (each rect = `x,y,w,h`). Tags whose sub-segments fail produce an
// empty entry so alignment with tag_rects is preserved.
// CRC: crc-PdfChunkElement.md | R1758, R1761
func extractTagSegments(b *pdftext.Block) string {
	var entries []string
	entries = appendTagSegmentEntries(entries, b.Text, b.Chars)
	entries = appendTagSegmentEntries(entries, b.Caption, b.CaptionChars)
	for _, e := range entries {
		if e != "" {
			return strings.Join(entries, ";")
		}
	}
	return ""
}

func appendTagSegmentEntries(entries []string, text string, chars []pdftext.Char) []string {
	if text == "" || len(chars) == 0 {
		return entries
	}
	for _, m := range tagRectPattern.FindAllStringSubmatchIndex(text, -1) {
		if len(m) < 6 {
			continue
		}
		// Gate on the same union check that extractTagRects uses so
		// skipping here stays aligned with skipping there.
		if _, ok := charRangeRect(chars, m[0], m[1]); !ok {
			continue
		}
		atRect, okAt := charRangeRect(chars, m[0], m[0]+1)
		nameRect, okN := charRangeRect(chars, m[2], m[3])
		colonRect, okC := charRangeRect(chars, m[3], m[3]+1)
		valueEnd := m[5]
		// Trim trailing ASCII whitespace from the value byte range so
		// the line-split rects don't include padding beyond the last
		// glyph.
		for valueEnd > m[4] {
			c := text[valueEnd-1]
			if c == ' ' || c == '\t' || c == '\r' || c == '\n' {
				valueEnd--
				continue
			}
			break
		}
		valueRects := charRangeRectsByLine(chars, m[4], valueEnd)
		if !okAt || !okN || !okC || len(valueRects) == 0 {
			entries = append(entries, "")
			continue
		}
		parts := make([]string, 0, 3+len(valueRects))
		parts = append(parts, encodeTagSegment(atRect))
		parts = append(parts, encodeTagSegment(nameRect))
		parts = append(parts, encodeTagSegment(colonRect))
		for _, r := range valueRects {
			parts = append(parts, encodeTagSegment(r))
		}
		entries = append(entries, strings.Join(parts, "|"))
	}
	return entries
}

func encodeTagSegment(r pdfRect) string {
	return fmt.Sprintf(
		"%s,%s,%s,%s",
		formatPdfFloat(r.X),
		formatPdfFloat(r.Y),
		formatPdfFloat(r.W),
		formatPdfFloat(r.H),
	)
}

// charRangeRectsByLine splits a byte range into one union rect per
// physical line. Chars are grouped by baseline Y with a tolerance of
// half the running average glyph height — when Y drops outside that
// window the previous group closes and a new one starts. Handles
// wrapped tag values.
// CRC: crc-PdfChunkElement.md | R1761
func charRangeRectsByLine(chars []pdftext.Char, start, end int) []pdfRect {
	if start < 0 || end > len(chars) || start >= end {
		return nil
	}
	var out []pdfRect
	var cur pdfRect
	var have bool
	var avgH float64
	var count int
	extend := func(c pdftext.Char) {
		if c.BBox.X < cur.X {
			cur.W = cur.X + cur.W - c.BBox.X
			cur.X = c.BBox.X
		}
		if rx := c.BBox.X + c.BBox.W; rx > cur.X+cur.W {
			cur.W = rx - cur.X
		}
		if c.BBox.Y < cur.Y {
			cur.H = cur.Y + cur.H - c.BBox.Y
			cur.Y = c.BBox.Y
		}
		if ry := c.BBox.Y + c.BBox.H; ry > cur.Y+cur.H {
			cur.H = ry - cur.Y
		}
	}
	for i := start; i < end; i++ {
		c := chars[i]
		if c.Rune == pdftext.NoRune {
			continue
		}
		if !have {
			cur = pdfRect{X: c.BBox.X, Y: c.BBox.Y, W: c.BBox.W, H: c.BBox.H}
			avgH = c.BBox.H
			count = 1
			have = true
			continue
		}
		tol := avgH * 0.5
		if math.Abs(c.BBox.Y-cur.Y) > tol {
			out = append(out, cur)
			cur = pdfRect{X: c.BBox.X, Y: c.BBox.Y, W: c.BBox.W, H: c.BBox.H}
			avgH = c.BBox.H
			count = 1
			continue
		}
		extend(c)
		avgH = (avgH*float64(count) + c.BBox.H) / float64(count+1)
		count++
	}
	if have {
		out = append(out, cur)
	}
	return out
}

// charRangeRect returns the union of Char.BBoxes covering the byte
// range [start, end) in a Block.Text or Block.Caption. Slots with
// Rune=NoRune are skipped. Returns ok=false when no chars fall in
// the range or the resulting rect has zero extent. R1735, R1736
func charRangeRect(chars []pdftext.Char, start, end int) (pdfRect, bool) {
	if start < 0 || end > len(chars) || start >= end {
		return pdfRect{}, false
	}
	var minX, minY, maxX, maxY float64
	var have bool
	for i := start; i < end; i++ {
		c := chars[i]
		if c.Rune == pdftext.NoRune {
			continue
		}
		if !have {
			minX = c.BBox.X
			minY = c.BBox.Y
			maxX = c.BBox.X + c.BBox.W
			maxY = c.BBox.Y + c.BBox.H
			have = true
			continue
		}
		if c.BBox.X < minX {
			minX = c.BBox.X
		}
		if c.BBox.Y < minY {
			minY = c.BBox.Y
		}
		if rx := c.BBox.X + c.BBox.W; rx > maxX {
			maxX = rx
		}
		if ry := c.BBox.Y + c.BBox.H; ry > maxY {
			maxY = ry
		}
	}
	if !have || maxX <= minX || maxY <= minY {
		return pdfRect{}, false
	}
	return pdfRect{X: minX, Y: minY, W: maxX - minX, H: maxY - minY}, true
}

// encodeTagRectField percent-encodes the structural delimiters so
// names and values survive round-trip through the tag_rects format.
// R1672
func encodeTagRectField(s string) string {
	needsEncode := false
	for _, r := range s {
		if r == '=' || r == '@' || r == ';' || r == ',' || r == '%' {
			needsEncode = true
			break
		}
	}
	if !needsEncode {
		return s
	}
	var b strings.Builder
	b.Grow(len(s) + 8)
	for _, r := range s {
		switch r {
		case '=', '@', ';', ',', '%':
			fmt.Fprintf(&b, "%%%02X", r)
		default:
			b.WriteRune(r)
		}
	}
	return b.String()
}

func formatPdfFloat(v float64) string {
	return strconv.FormatFloat(v, 'f', 2, 64)
}
