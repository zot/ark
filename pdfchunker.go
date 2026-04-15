package ark

import (
	"bytes"
	"crypto/sha256"
	"fmt"
	"log"
	"math"
	"os"
	"sort"
	"strconv"
	"strings"

	"seehuhn.de/go/geom/matrix"
	"seehuhn.de/go/pdf"
	"seehuhn.de/go/pdf/font/textextract"
	"seehuhn.de/go/pdf/pagetree"
	"seehuhn.de/go/pdf/reader"
	"seehuhn.de/go/postscript/cid"

	"github.com/zot/microfts2"
)

// CRC: crc-PDFChunker.md | Seq: seq-pdf-chunk.md | R1624-R1643

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
type PDFChunker struct{}

// Chunks implements microfts2.Chunker for tmp documents.
// CRC: crc-PDFChunker.md | R1642
func (c *PDFChunker) Chunks(path string, content []byte, yield func(microfts2.Chunk) bool) error {
	r, err := pdf.NewReader(bytes.NewReader(content), int64(len(content)), nil)
	if err != nil {
		return fmt.Errorf("pdf parse %s: %w", path, err)
	}
	defer r.Close()
	return c.extractChunks(r, yield)
}

// ChunkText implements microfts2.Chunker for single-chunk retrieval.
// CRC: crc-PDFChunker.md | R1642
func (c *PDFChunker) ChunkText(path string, content []byte, rangeLabel string) ([]byte, bool) {
	var result []byte
	var found bool
	c.Chunks(path, content, func(ch microfts2.Chunk) bool {
		if string(ch.Range) == rangeLabel {
			result = make([]byte, len(ch.Content))
			copy(result, ch.Content)
			found = true
			return false
		}
		return true
	})
	return result, found
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
	r, err := pdf.NewReader(bytes.NewReader(data), int64(len(data)), nil)
	if err != nil {
		log.Printf("pdf: skip %s: %v", path, err)
		return hash, nil // unparseable PDF — yield no chunks, don't block indexer
	}
	defer r.Close()
	return hash, c.extractChunks(r, yield)
}

// FileChunkText implements microfts2.FileChunker for single-chunk retrieval.
// CRC: crc-PDFChunker.md | R1642
func (c *PDFChunker) FileChunkText(path string, rangeLabel string) ([]byte, bool) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, false
	}
	return c.ChunkText(path, data, rangeLabel)
}

// extractChunks iterates all pages and yields chunks.
// CRC: crc-PDFChunker.md | R1624
func (c *PDFChunker) extractChunks(doc *pdf.Reader, yield func(microfts2.Chunk) bool) error {
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
			return nil
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

	// Track path operations for table rule detection. R1626
	rdr.EveryOp = func(op string, args []pdf.Object) error {
		switch op {
		case "m": // moveto
			if len(args) >= 2 {
				pathStartX, pathStartY = pdfFloat(args[0]), pdfFloat(args[1])
				pathPoints = [][2]float64{{pathStartX, pathStartY}}
				inPath = true
			}
		case "l": // lineto
			if len(args) >= 2 && inPath {
				pathPoints = append(pathPoints, [2]float64{pdfFloat(args[0]), pdfFloat(args[1])})
			}
		case "re": // rectangle
			if len(args) >= 4 {
				rx, ry := pdfFloat(args[0]), pdfFloat(args[1])
				rw, rh := pdfFloat(args[2]), pdfFloat(args[3])
				// Thin rectangles are rules
				if math.Abs(rh) < 2 || math.Abs(rw) < 2 {
					rules = append(rules, pdfRule{rx, ry, rx + rw, ry + rh})
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

// mergeSpans combines spans on the same line into pdfLines. R1625
func mergeSpans(spans []pdfSpan) []pdfLine {
	if len(spans) == 0 {
		return nil
	}
	// Sort by Y descending (top of page first), then X ascending
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
		// Same line if Y is within tolerance
		if math.Abs(s.Y-cur.Y) < cur.FontSize*0.3 {
			// Gap between spans — insert space if needed
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
			lines = append(lines, cur)
			cur = pdfLine{
				X: s.X, Y: s.Y,
				FontSize: s.FontSize,
				Text:     s.Text,
				Width:    s.Width,
				Height:   s.FontSize,
			}
		}
	}
	lines = append(lines, cur)
	return lines
}

// buildPageChunks detects structure and emits chunks for one page. R1626-R1636
func buildPageChunks(pageNum int, lines []pdfLine, rules []pdfRule) []microfts2.Chunk {
	page := strconv.Itoa(pageNum)

	// Page-level fallback. R1636
	if len(lines) < 2 {
		text := ""
		for _, l := range lines {
			text += l.Text + "\n"
		}
		rect := boundingRect(lines)
		return []microfts2.Chunk{{
			Range:   []byte(page),
			Content: []byte(strings.TrimSpace(text)),
			Attrs:   chunkAttrs(page, rect, 0),
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
			Content: []byte(strings.TrimSpace(text)),
			Attrs:   chunkAttrs(page, rect, 0),
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
		if g.isHeading {
			headingN++
			chunks = append(chunks, microfts2.Chunk{
				Range:   []byte(fmt.Sprintf("%s/heading/%d", page, headingN)),
				Content: []byte(strings.TrimSpace(text)),
				Attrs:   chunkAttrs(page, rect, g.lines[0].FontSize),
			})
		} else {
			paraN++
			chunks = append(chunks, microfts2.Chunk{
				Range:   []byte(fmt.Sprintf("%s/para/%d", page, paraN)),
				Content: []byte(strings.TrimSpace(text)),
				Attrs:   chunkAttrs(page, rect, 0),
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
func boundingRect(lines []pdfLine) pdfRect {
	if len(lines) == 0 {
		return pdfRect{}
	}
	minX, minY := math.Inf(1), math.Inf(1)
	maxX, maxY := math.Inf(-1), math.Inf(-1)
	for _, l := range lines {
		minX = math.Min(minX, l.X)
		minY = math.Min(minY, l.Y-l.Height)
		maxX = math.Max(maxX, l.X+l.Width)
		maxY = math.Max(maxY, l.Y)
	}
	return pdfRect{X: minX, Y: minY, W: maxX - minX, H: maxY - minY}
}

// lineInRect checks if a line's midpoint falls within a rect.
func lineInRect(l pdfLine, r pdfRect) bool {
	mx := l.X + l.Width/2
	my := l.Y - l.Height/2
	return mx >= r.X && mx <= r.X+r.W && my >= r.Y && my <= r.Y+r.H
}

// chunkAttrs builds the Attrs slice for a chunk. R1637, R1638, R1639
func chunkAttrs(page string, rect pdfRect, fontSize float64) []microfts2.Pair {
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
	return attrs
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
