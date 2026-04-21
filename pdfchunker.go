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
	"sort"
	"strconv"
	"strings"
	"sync"

	"seehuhn.de/go/geom/matrix"
	"seehuhn.de/go/geom/vec"
	"seehuhn.de/go/pdf"
	"seehuhn.de/go/pdf/font/textextract"
	"seehuhn.de/go/pdf/pagetree"
	"seehuhn.de/go/pdf/reader"
	"seehuhn.de/go/postscript/cid"

	"github.com/zot/microfts2"
)

// CRC: crc-PDFChunker.md | Seq: seq-pdf-chunk.md | R1624-R1643

// attrContentOffset and attrContentLen locate a chunk's text inside
// its page's compressed blob. R1719
const (
	attrContentOffset = "content_offset"
	attrContentLen    = "content_len"
)

// pendingPageBlob is one page's compressed text awaiting a fileid. R1720
type pendingPageBlob struct {
	page uint32
	blob []byte
}

// pdfSpan is a positioned text fragment extracted from a PDF page.
type pdfSpan struct {
	X, Y     float64 // device coordinates (points, origin bottom-left)
	FontSize float64
	Text     string
	Width    float64 // estimated width in points
}

// pdfLine is a horizontal line of merged spans.
type pdfLine struct {
	X, Y     float64 // leftmost X, shared Y
	FontSize float64 // dominant font size
	Text     string
	Width    float64
	Height   float64
}

// pdfRect is a bounding box in PDF points (bottom-left origin).
type pdfRect struct {
	X, Y, W, H float64
}

func (r pdfRect) String() string {
	return fmt.Sprintf("%.0f,%.0f,%.0f,%.0f", r.X, r.Y, r.W, r.H)
}

// pdfRule is a detected horizontal or vertical line in the content stream. R1626
type pdfRule struct {
	X1, Y1, X2, Y2 float64
}

func (r pdfRule) isHorizontal(tol float64) bool { return math.Abs(r.Y1-r.Y2) < tol }
func (r pdfRule) isVertical(tol float64) bool   { return math.Abs(r.X1-r.X2) < tol }

// pdfTableRegion is a detected table bounding box. R1626, R1627
type pdfTableRegion struct {
	rect  pdfRect
	lines []pdfLine // lines inside the table
}

// PDFChunker extracts text from PDF files with structure detection. R1641
//
// Holds a reference to ark.DB so FileChunks can stage per-page text blobs
// (flushed once microfts2 has allocated a fileid) and GetChunk can read
// them back at retrieval time. R1720, R1726
type PDFChunker struct {
	db      *DB
	mu      sync.Mutex
	pending map[string][]pendingPageBlob
}

// NewPDFChunker constructs a chunker bound to ark's DB.
func NewPDFChunker(db *DB) *PDFChunker {
	return &PDFChunker{db: db, pending: make(map[string][]pendingPageBlob)}
}

// Chunks implements microfts2.Chunker for tmp documents.
// CRC: crc-PDFChunker.md | R1642, R1652, R1660
func (c *PDFChunker) Chunks(path string, content []byte, yield func(microfts2.Chunk) bool) error {
	r, err := pdf.NewReader(bytes.NewReader(content), int64(len(content)), nil)
	if err != nil {
		log.Printf("pdf: salvage %s: %v", path, err)
		c.salvage(path, content, yield, false)
		return nil
	}
	defer r.Close()
	return c.extractChunks(path, r, yield, false)
}

// FileChunks implements microfts2.FileChunker for indexed files.
// CRC: crc-PDFChunker.md | R1642
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
	r, err := pdf.NewReader(bytes.NewReader(data), int64(len(data)), nil)
	if err != nil {
		// R1652, R1660: fall back to byte-level salvage for malformed PDFs
		log.Printf("pdf: salvage %s: %v", path, err)
		c.salvage(path, data, yield, true)
		return hash, nil
	}
	defer r.Close()
	return hash, c.extractChunks(path, r, yield, true)
}

// extractChunks iterates all pages, seals each page's blob, and yields chunks.
// When persist is true, sealed blobs are staged in c.pending[path] for
// FlushBlobs to write once the fileid is known. R1720, R1721
// CRC: crc-PDFChunker.md | R1624
func (c *PDFChunker) extractChunks(path string, doc *pdf.Reader, yield func(microfts2.Chunk) bool, persist bool) error {
	iter := pagetree.NewIterator(doc)
	pageNum := 0
	for _, pageDict := range iter.All() {
		pageNum++
		spans, rules, err := extractPage(doc, pageDict)
		Logv(1, "pdf: page %d: %d spans, %d rules, err=%v", pageNum, len(spans), len(rules), err)
		if err != nil {
			continue // skip unreadable pages
		}
		lines := mergeSpans(spans)
		if len(lines) == 0 {
			continue
		}
		chunks := buildPageChunks(pageNum, lines, rules)
		if persist {
			if blob := sealPageBlob(chunks); blob != nil {
				c.pushPending(path, uint32(pageNum), blob)
			}
		}
		for _, ch := range chunks {
			if !yield(ch) {
				return nil
			}
		}
	}
	if iter.Err != nil {
		return fmt.Errorf("pdf page iteration: %w", iter.Err)
	}
	return nil
}

// salvage dispatches to salvageText and optionally seals the salvage
// chunks into the per-file page-0 blob. R1723
func (c *PDFChunker) salvage(path string, content []byte, yield func(microfts2.Chunk) bool, persist bool) {
	chunks := salvageChunks(content)
	if len(chunks) == 0 {
		return
	}
	if persist {
		if blob := sealPageBlob(chunks); blob != nil {
			c.pushPending(path, 0, blob)
		}
	}
	for _, ch := range chunks {
		if !yield(ch) {
			return
		}
	}
}

// resetPending clears any half-built staging state for this path so a
// retry doesn't inherit data from a failed prior pass. R1724
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

// sealPageBlob concatenates each chunk's text (null-byte separated),
// records each chunk's offset/length in its Attrs, and returns the
// zlib-compressed blob. Returns nil on compression failure. R1719, R1721, R1722
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

// GetChunk implements microfts2.RandomAccessChunker. Reads the chunk's
// content_offset/content_len from Attrs, loads the page blob from Store
// (decompressing once per page via customData), and slices. Falls back
// to a streaming FileChunks pass when any step reports missing data.
// R1726, R1727, R1728
// CRC: crc-PDFChunker.md | Seq: seq-pdf-chunk-retrieval.md | R1726
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
// the target range and returns its content. Used as fallback when
// fastRetrieve can't. Does not stage any pending blobs — retrieval must
// not masquerade as indexing. R1728
func (c *PDFChunker) streamingRetrieve(path, rangeLabel string) ([]byte, bool) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, false
	}
	var result []byte
	var found bool
	_ = c.Chunks(path, data, func(ch microfts2.Chunk) bool {
		if string(ch.Range) == rangeLabel {
			result = append(result[:0], ch.Content...)
			found = true
			return false
		}
		return true
	})
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
// page-blob cache inside microfts2's per-file customData. R1727
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

// extractPage extracts text spans and drawn rules from one page. R1624, R1626
func extractPage(doc *pdf.Reader, pageDict pdf.Dict) ([]pdfSpan, []pdfRule, error) {
	var spans []pdfSpan
	var rules []pdfRule
	var pathStartX, pathStartY float64
	var pathPoints [][2]float64 // accumulated path points
	inPath := false

	ext := pdf.NewExtractor(doc)
	rdr := reader.New(ext)

	glyphCache := make(map[interface{}]map[cid.CID]string)
	// TextEvent fires TextEventSpace when a TJ positional adjustment
	// is ≥ 0.3× space width — seehuhn's own signal that the PDF
	// intended a word break (via kerning-style positioning rather than
	// an explicit space glyph). Prepend a space to the next Character
	// call so mergeSpans sees the break in the stream, independent of
	// our gap heuristic.
	var pendingSpace bool
	rdr.TextEvent = func(event reader.TextEvent, _ float64) {
		if event == reader.TextEventSpace {
			pendingSpace = true
		}
	}

	rdr.Character = func(c cid.CID, text string) error {
		if text == "" {
			currentFont := rdr.State.GState.TextFont
			cidMap, ok := glyphCache[currentFont]
			if !ok {
				cidMap = textextract.GlyphNameMapping(currentFont)
				glyphCache[currentFont] = cidMap
			}
			text = cidMap[c]
		}
		if text == "" {
			pendingSpace = false
			return nil
		}
		if pendingSpace {
			text = " " + text
			pendingSpace = false
		}
		x, y := rdr.GetTextPositionDevice()
		fontSize := rdr.State.GState.TextFontSize
		// Estimate width: fontSize * 0.6 per character (rough average)
		w := fontSize * 0.6 * float64(len([]rune(text)))
		spans = append(spans, pdfSpan{
			X: x, Y: y, FontSize: fontSize, Text: text, Width: w,
		})
		return nil
	}

	// Track path operations for table rule detection. Path operator args
	// are in pre-CTM user space; we transform to device coords so rules
	// share the coordinate system of text spans (post-CTM). R1626
	applyCTM := func(x, y float64) (float64, float64) {
		v := rdr.State.GState.CTM.Apply(vec.Vec2{X: x, Y: y})
		return v.X, v.Y
	}
	rdr.EveryOp = func(op string, args []pdf.Object) error {
		switch op {
		case "m": // moveto
			if len(args) >= 2 {
				pathStartX, pathStartY = applyCTM(pdfFloat(args[0]), pdfFloat(args[1]))
				pathPoints = [][2]float64{{pathStartX, pathStartY}}
				inPath = true
			}
		case "l": // lineto
			if len(args) >= 2 && inPath {
				lx, ly := applyCTM(pdfFloat(args[0]), pdfFloat(args[1]))
				pathPoints = append(pathPoints, [2]float64{lx, ly})
			}
		case "re": // rectangle
			if len(args) >= 4 {
				rx0, ry0 := applyCTM(pdfFloat(args[0]), pdfFloat(args[1]))
				rx1, ry1 := applyCTM(pdfFloat(args[0])+pdfFloat(args[2]), pdfFloat(args[1])+pdfFloat(args[3]))
				rw := math.Abs(rx1 - rx0)
				rh := math.Abs(ry1 - ry0)
				// Thin rectangles are rules
				if rh < 2 || rw < 2 {
					rules = append(rules, pdfRule{rx0, ry0, rx1, ry1})
				}
			}
		case "h": // closepath
			if inPath && len(pathPoints) > 0 {
				pathPoints = append(pathPoints, [2]float64{pathStartX, pathStartY})
			}
		case "S", "s", "f", "f*", "B", "B*", "b", "b*": // stroke/fill
			if inPath {
				for i := 1; i < len(pathPoints); i++ {
					p0 := pathPoints[i-1]
					p1 := pathPoints[i]
					rules = append(rules, pdfRule{p0[0], p0[1], p1[0], p1[1]})
				}
			}
			inPath = false
			pathPoints = nil
		case "n": // end path without painting
			inPath = false
			pathPoints = nil
		}
		return nil
	}

	err := rdr.ParsePage(pageDict, matrix.Identity)
	return spans, rules, err
}

// filterBlankLines returns lines whose text contains at least one
// non-whitespace rune. Some PDFs encode paragraph separators as
// single-space lines at a fresh Y; these need to be stripped before
// gap-based structure detection so the real gap between paragraphs
// becomes visible. R1661, R1662
func filterBlankLines(lines []pdfLine) []pdfLine {
	out := lines[:0:0]
	for _, l := range lines {
		if strings.TrimSpace(l.Text) != "" {
			out = append(out, l)
		}
	}
	return out
}

// mergeSpans combines spans on the same line into pdfLines. R1625.
//
// Word-break detection uses the gap between the previous span's
// estimated right edge and the next span's left edge. The estimate
// (fontSize * 0.6 * charCount) is imperfect, especially for wide
// glyphs like `@`, so we post-process the merged text to collapse
// the common false-positive pattern `@ name:` → `@name:` (tag
// grammar) — matching spans that are really `@tag:` rendered with
// a wider-than-estimated `@` glyph.
func mergeSpans(spans []pdfSpan) []pdfLine {
	if len(spans) == 0 {
		return nil
	}
	sort.Slice(spans, func(i, j int) bool {
		if math.Abs(spans[i].Y-spans[j].Y) > spans[i].FontSize*0.3 {
			return spans[i].Y > spans[j].Y
		}
		return spans[i].X < spans[j].X
	})

	var lines []pdfLine
	cur := pdfLine{
		X: spans[0].X, Y: spans[0].Y,
		FontSize: spans[0].FontSize,
		Text:     spans[0].Text,
		Width:    spans[0].Width,
		Height:   spans[0].FontSize,
	}

	for i := 1; i < len(spans); i++ {
		s := spans[i]
		if math.Abs(s.Y-cur.Y) < cur.FontSize*0.3 {
			gap := s.X - (cur.X + cur.Width)
			if gap > cur.FontSize*0.3 {
				cur.Text += " "
			}
			cur.Text += s.Text
			cur.Width = (s.X + s.Width) - cur.X
			if s.FontSize > cur.FontSize {
				cur.FontSize = s.FontSize
				cur.Height = s.FontSize
			}
		} else {
			cur.Text = tightenTagPrefixes(cur.Text)
			lines = append(lines, cur)
			cur = pdfLine{
				X: s.X, Y: s.Y,
				FontSize: s.FontSize,
				Text:     s.Text,
				Width:    s.FontSize,
				Height:   s.FontSize,
			}
		}
	}
	cur.Text = tightenTagPrefixes(cur.Text)
	lines = append(lines, cur)
	return lines
}

// tightenTagPrefixes collapses `@ name:` back to `@name:` when the
// span-gap heuristic inserted a spurious space between the `@` glyph
// and the tag name. The `@` glyph is wider than the chunker's width
// estimate, so any tag in a Chrome/Skia-generated PDF shows up split
// unless we glue it back here.
var tightenTagPrefixRe = regexp.MustCompile(`@\s+([a-zA-Z][\w.-]*\s*:)`)

func tightenTagPrefixes(s string) string {
	return tightenTagPrefixRe.ReplaceAllString(s, "@$1")
}

// buildPageChunks detects structure and emits chunks for one page. R1626-R1636, R1661-R1664
func buildPageChunks(pageNum int, lines []pdfLine, rules []pdfRule) []microfts2.Chunk {
	page := strconv.Itoa(pageNum)
	pageSize := observedPageSize(lines)

	// R1661, R1662, R1663, R1664: drop whitespace-only lines so structure
	// detection sees only content-bearing lines. ONLYOFFICE-style PDFs
	// encode paragraph separators as single-space lines at a fresh Y;
	// without this filter they fill the Y-gap that gap-based paragraph
	// detection relies on, and the page collapses to one paragraph.
	lines = filterBlankLines(lines)

	// Page-level fallback. R1636
	if len(lines) < 2 {
		text := ""
		for _, l := range lines {
			text += l.Text + "\n"
		}
		rect := boundingRect(lines)
		return []microfts2.Chunk{{
			Range:   []byte(page),
			Content: []byte(strings.TrimRight(text, "\n")),
			Attrs:   chunkAttrs(page, clampRectToPage(rect, pageSize), 0, lines, pageSize),
		}}
	}

	var chunks []microfts2.Chunk
	used := make([]bool, len(lines))

	// 1. Table detection. R1626, R1627, R1628
	tables := detectTables(lines, rules)
	for _, tbl := range tables {
		for i, l := range lines {
			if !used[i] && lineInRect(l, tbl.rect) {
				used[i] = true
			}
		}
	}

	// 2. Heading detection. R1631, R1632
	dominantSize := dominantFontSize(lines)
	headingThreshold := dominantSize * 1.2

	// 3. Group remaining lines into paragraphs and headings
	dominantSpacing := dominantLineSpacing(lines)
	type group struct {
		lines     []pdfLine
		isHeading bool
	}
	var groups []group
	var curGroup *group

	for i, l := range lines {
		if used[i] {
			continue
		}
		isHeading := l.FontSize >= headingThreshold
		if curGroup == nil {
			curGroup = &group{lines: []pdfLine{l}, isHeading: isHeading}
			continue
		}
		// New group on: heading change, or paragraph gap. R1634
		prevLine := curGroup.lines[len(curGroup.lines)-1]
		gap := math.Abs(prevLine.Y - l.Y)
		isParaBreak := gap > dominantSpacing*1.5 && dominantSpacing > 0
		headingChange := isHeading != curGroup.isHeading

		if headingChange || isParaBreak {
			groups = append(groups, *curGroup)
			curGroup = &group{lines: []pdfLine{l}, isHeading: isHeading}
		} else {
			curGroup.lines = append(curGroup.lines, l)
		}
	}
	if curGroup != nil && len(curGroup.lines) > 0 {
		groups = append(groups, *curGroup)
	}

	// Emit table chunks. R1629, R1630
	for i, tbl := range tables {
		text := tableText(tbl)
		rect := tbl.rect
		chunks = append(chunks, microfts2.Chunk{
			Range:   []byte(fmt.Sprintf("%s/table/%d", page, i+1)),
			Content: []byte(strings.TrimRight(text, "\n")),
			Attrs:   chunkAttrs(page, clampRectToPage(rect, pageSize), 0, tbl.lines, pageSize),
		})
	}

	// Emit heading and paragraph chunks. R1633, R1635
	headingN := 0
	paraN := 0
	for _, g := range groups {
		text := ""
		for _, l := range g.lines {
			text += l.Text + "\n"
		}
		rect := boundingRect(g.lines)
		clampedRect := clampRectToPage(rect, pageSize)
		if g.isHeading {
			headingN++
			chunks = append(chunks, microfts2.Chunk{
				Range:   []byte(fmt.Sprintf("%s/heading/%d", page, headingN)),
				Content: []byte(strings.TrimRight(text, "\n")),
				Attrs:   chunkAttrs(page, clampedRect, g.lines[0].FontSize, g.lines, pageSize),
			})
		} else {
			paraN++
			chunks = append(chunks, microfts2.Chunk{
				Range:   []byte(fmt.Sprintf("%s/para/%d", page, paraN)),
				Content: []byte(strings.TrimRight(text, "\n")),
				Attrs:   chunkAttrs(page, clampedRect, 0, g.lines, pageSize),
			})
		}
	}

	// Sort by Y descending (top of page first) for reading order
	sort.Slice(chunks, func(i, j int) bool {
		ri := parseRectY(chunks[i].Attrs)
		rj := parseRectY(chunks[j].Attrs)
		return ri > rj
	})

	return chunks
}

// detectTables finds table regions via drawn rules then column alignment. R1626, R1627, R1628
func detectTables(lines []pdfLine, rules []pdfRule) []pdfTableRegion {
	tol := 2.0 // point tolerance for rule alignment

	// Try drawn rules first. R1626, R1628
	if tables := detectTablesFromRules(rules, tol); len(tables) > 0 {
		// Associate lines with table regions
		for i := range tables {
			for _, l := range lines {
				if lineInRect(l, tables[i].rect) {
					tables[i].lines = append(tables[i].lines, l)
				}
			}
		}
		return tables
	}

	// Fall back to column alignment. R1627
	return detectTablesFromAlignment(lines)
}

// detectTablesFromRules finds table grids from horizontal + vertical rules. R1626
func detectTablesFromRules(rules []pdfRule, tol float64) []pdfTableRegion {
	var hRules, vRules []pdfRule
	for _, r := range rules {
		if r.isHorizontal(tol) {
			hRules = append(hRules, r)
		} else if r.isVertical(tol) {
			vRules = append(vRules, r)
		}
	}
	// Need at least 2 horizontal and 2 vertical rules for a grid
	if len(hRules) < 2 || len(vRules) < 2 {
		return nil
	}

	// Cluster overlapping rules into grid regions
	// Simple approach: bounding box of all rules
	minX, minY := math.Inf(1), math.Inf(1)
	maxX, maxY := math.Inf(-1), math.Inf(-1)
	for _, r := range append(hRules, vRules...) {
		minX = math.Min(minX, math.Min(r.X1, r.X2))
		minY = math.Min(minY, math.Min(r.Y1, r.Y2))
		maxX = math.Max(maxX, math.Max(r.X1, r.X2))
		maxY = math.Max(maxY, math.Max(r.Y1, r.Y2))
	}

	return []pdfTableRegion{{
		rect: pdfRect{X: minX, Y: minY, W: maxX - minX, H: maxY - minY},
	}}
}

// detectTablesFromAlignment finds table regions by column alignment. R1627
func detectTablesFromAlignment(lines []pdfLine) []pdfTableRegion {
	if len(lines) < 3 {
		return nil
	}

	// Cluster lines by Y coordinate (rows)
	type row struct {
		y     float64
		lines []pdfLine
	}
	var rows []row
	tol := dominantFontSize(lines) * 0.3

	for _, l := range lines {
		found := false
		for i := range rows {
			if math.Abs(rows[i].y-l.Y) < tol {
				rows[i].lines = append(rows[i].lines, l)
				found = true
				break
			}
		}
		if !found {
			rows = append(rows, row{y: l.Y, lines: []pdfLine{l}})
		}
	}

	// Count X positions across rows — if ≥2 X positions appear in ≥3 rows, it's a table
	xCounts := make(map[int]int) // quantized X → row count
	quantize := func(x float64) int { return int(x / tol) }
	for _, r := range rows {
		seen := make(map[int]bool)
		for _, l := range r.lines {
			qx := quantize(l.X)
			if !seen[qx] {
				xCounts[qx]++
				seen[qx] = true
			}
		}
	}

	// Collect aligned column X positions (quantized)
	var colXs []int
	for qx, count := range xCounts {
		if count >= 3 {
			colXs = append(colXs, qx)
		}
	}
	if len(colXs) < 2 {
		return nil
	}
	sort.Ints(colXs)

	// Multi-span row check: a real table has multiple spans per row
	// (cells in different columns). A single-column document with
	// consistent formatting has 1 span per row.
	multiSpanRows := 0
	for _, r := range rows {
		if len(r.lines) >= 2 {
			multiSpanRows++
		}
	}
	if len(rows) > 0 && float64(multiSpanRows)/float64(len(rows)) < 0.3 {
		return nil
	}

	// Overlap check: in a real table, columns don't overlap horizontally.
	// For each row, check that spans starting at one column X don't extend
	// into the next column's X. Bullet lists fail this — the text after
	// the bullet spans the full width.
	overlapCount := 0
	totalChecked := 0
	for _, r := range rows {
		if len(r.lines) < 2 {
			continue
		}
		// Sort row's lines by X
		rowLines := make([]pdfLine, len(r.lines))
		copy(rowLines, r.lines)
		sort.Slice(rowLines, func(i, j int) bool { return rowLines[i].X < rowLines[j].X })
		for i := 0; i < len(rowLines)-1; i++ {
			rightEdge := rowLines[i].X + rowLines[i].Width
			nextX := rowLines[i+1].X
			totalChecked++
			if rightEdge > nextX+tol {
				overlapCount++
			}
		}
	}
	// If most column pairs overlap, this is not a table (e.g., bullet list)
	if totalChecked > 0 && float64(overlapCount)/float64(totalChecked) > 0.3 {
		return nil
	}

	// Table region is the bounding box of all lines
	return []pdfTableRegion{{rect: boundingRect(lines), lines: lines}}
}

// tableText concatenates table lines row by row. R1629
func tableText(tbl pdfTableRegion) string {
	// Sort by Y descending (top first), then X ascending
	lines := make([]pdfLine, len(tbl.lines))
	copy(lines, tbl.lines)
	sort.Slice(lines, func(i, j int) bool {
		tol := lines[i].FontSize * 0.3
		if math.Abs(lines[i].Y-lines[j].Y) > tol {
			return lines[i].Y > lines[j].Y
		}
		return lines[i].X < lines[j].X
	})
	var b strings.Builder
	for i, l := range lines {
		if i > 0 {
			prev := lines[i-1]
			if math.Abs(prev.Y-l.Y) > l.FontSize*0.3 {
				b.WriteByte('\n')
			} else {
				b.WriteByte('\t')
			}
		}
		b.WriteString(l.Text)
	}
	return b.String()
}

// dominantFontSize returns the most common font size. R1631
func dominantFontSize(lines []pdfLine) float64 {
	counts := make(map[int]int)
	for _, l := range lines {
		q := int(l.FontSize * 10) // quantize to 0.1pt
		counts[q]++
	}
	maxCount := 0
	dominant := 0
	for q, c := range counts {
		if c > maxCount {
			maxCount = c
			dominant = q
		}
	}
	return float64(dominant) / 10.0
}

// dominantLineSpacing returns the most common vertical distance between lines. R1634
func dominantLineSpacing(lines []pdfLine) float64 {
	if len(lines) < 2 {
		return 0
	}
	counts := make(map[int]int)
	for i := 1; i < len(lines); i++ {
		gap := math.Abs(lines[i-1].Y - lines[i].Y)
		if gap > 0.5 { // ignore zero gaps
			q := int(gap * 10)
			counts[q]++
		}
	}
	maxCount := 0
	dominant := 0
	for q, c := range counts {
		if c > maxCount {
			maxCount = c
			dominant = q
		}
	}
	return float64(dominant) / 10.0
}

// boundingRect computes the bounding box of a set of lines.
// boundingRect unions the bounding boxes of a group of lines.
// pdfLine.Y is the glyph baseline and pdfLine.Height is the font size
// (ascender height). The bottom of a line (bottom of descenders) is
// ≈0.25×height below the baseline; the top of a line (top of ascenders)
// is ≈1.0×height above the baseline.
func boundingRect(lines []pdfLine) pdfRect {
	if len(lines) == 0 {
		return pdfRect{}
	}
	minX, minY := math.Inf(1), math.Inf(1)
	maxX, maxY := math.Inf(-1), math.Inf(-1)
	for _, l := range lines {
		minX = math.Min(minX, l.X)
		minY = math.Min(minY, l.Y-l.Height*0.25)
		maxX = math.Max(maxX, l.X+l.Width)
		maxY = math.Max(maxY, l.Y+l.Height)
	}
	return pdfRect{X: minX, Y: minY, W: maxX - minX, H: maxY - minY}
}

// lineInRect checks if a line's midpoint falls within a rect.
func lineInRect(l pdfLine, r pdfRect) bool {
	mx := l.X + l.Width/2
	my := l.Y - l.Height/2
	return mx >= r.X && mx <= r.X+r.W && my >= r.Y && my <= r.Y+r.H
}

// chunkAttrs builds the Attrs slice for a chunk. R1637, R1638, R1639, R1665
func chunkAttrs(page string, rect pdfRect, fontSize float64, lines []pdfLine, pageSize [2]float64) []microfts2.Pair {
	attrs := []microfts2.Pair{
		{Key: []byte("page"), Value: []byte(page)},
		{Key: []byte("rect"), Value: []byte(rect.String())},
	}
	if fontSize > 0 {
		attrs = append(attrs, microfts2.Pair{
			Key:   []byte("font_size"),
			Value: []byte(fmt.Sprintf("%.1f", fontSize)),
		})
	}
	if pageSize[0] > 0 && pageSize[1] > 0 {
		attrs = append(attrs, microfts2.Pair{
			Key:   []byte("page_size"),
			Value: []byte(fmt.Sprintf("%s,%s", formatPdfFloat(pageSize[0]), formatPdfFloat(pageSize[1]))),
		})
	}
	if tr := extractTagRects(lines); tr != "" {
		attrs = append(attrs, microfts2.Pair{
			Key:   []byte("tag_rects"),
			Value: []byte(tr),
		})
	}
	return attrs
}

// observedPageSize returns the chunker's view of the page extent —
// max (x+width) and max (y+height) across all lines on the page.
// Used as the page's dimensions in the chunker's coord system so the
// <pdf-chunk> element can map between chunker coords and PDF.js's
// viewport regardless of any CTM/TextMatrix quirks in the source PDF.
func observedPageSize(lines []pdfLine) [2]float64 {
	var maxX, maxY float64
	for _, l := range lines {
		if rx := l.X + l.Width; rx > maxX {
			maxX = rx
		}
		if ry := l.Y + l.Height; ry > maxY {
			maxY = ry
		}
	}
	return [2]float64{maxX, maxY}
}

// clampRectToPage keeps a chunk rect within the observed page extent.
// Chrome/Skia PDFs occasionally emit path operators at coordinates far
// outside the page (negative, or tall beyond the page bottom) — the
// visual region of interest is still the intersection with the page.
// Returns the original rect when pageSize is empty.
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
		return r // degenerate after clamp — keep original so downstream can still inspect
	}
	return pdfRect{X: x0, Y: y0, W: x1 - x0, H: y1 - y0}
}

// tagRectPattern matches ark tag grammar — must mirror the generic
// regex in tagblock.go so rects only record tags that ark's generic
// extraction will also index (R1676).
var tagRectPattern = regexp.MustCompile(`@([a-zA-Z][\w.-]*):\s*([^\n]*)`)

// extractTagRects scans a chunk's lines for @name: value tag patterns
// and returns the compact `tag_rects` chunk attribute — one
// "name=value@x,y,w,h" entry per tag, semicolon-separated. Empty
// when the chunk has no tags. R1669-R1674.
//
// Coordinates are in PDF points, origin bottom-left (same convention
// as the chunk-level rect). The tag's x extent is interpolated from
// the pdfLine's Width using byte offsets of the match — the chunker
// already estimates line Width by summing span widths, so the same
// proportionality applies to tags inside them. First-line-only for
// wrapped tag values falls out naturally because each pdfLine carries
// one visual line's text.
func extractTagRects(lines []pdfLine) string {
	var entries []string
	for _, l := range lines {
		if l.Width <= 0 || l.Text == "" {
			continue
		}
		total := len(l.Text)
		for _, m := range tagRectPattern.FindAllStringSubmatchIndex(l.Text, -1) {
			if len(m) < 6 {
				continue
			}
			start, end := m[0], m[1]
			name := l.Text[m[2]:m[3]]
			value := strings.TrimSpace(l.Text[m[4]:m[5]])
			xStart := l.X + l.Width*float64(start)/float64(total)
			xEnd := l.X + l.Width*float64(end)/float64(total)
			entries = append(entries, fmt.Sprintf(
				"%s=%s@%s,%s,%s,%s",
				encodeTagRectField(name),
				encodeTagRectField(value),
				formatPdfFloat(xStart),
				formatPdfFloat(l.Y),
				formatPdfFloat(xEnd-xStart),
				formatPdfFloat(l.Height),
			))
		}
	}
	return strings.Join(entries, ";")
}

// encodeTagRectField percent-encodes the four structural delimiters
// (`=`, `@`, `;`, `,`) and the percent sign itself so names and values
// survive round-trip through the compact tag_rects format. R1672.
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

// parseRectY extracts the Y coordinate from a chunk's rect attribute for sorting.
func parseRectY(attrs []microfts2.Pair) float64 {
	for _, a := range attrs {
		if string(a.Key) == "rect" {
			parts := strings.SplitN(string(a.Value), ",", 3)
			if len(parts) >= 2 {
				y, _ := strconv.ParseFloat(parts[1], 64)
				return y
			}
		}
	}
	return 0
}

// pdfFloat extracts a float64 from a pdf.Object.
func pdfFloat(obj pdf.Object) float64 {
	switch v := obj.(type) {
	case pdf.Number:
		return float64(v)
	case pdf.Integer:
		return float64(v)
	default:
		return 0
	}
}
