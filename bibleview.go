package ark

// Bible content rendering — see specs/bible-chunker.md §Display.
// CRC: crc-BibleRenderer.md | R3181, R3182, R3183

import (
	"bytes"
	"html/template"
	"strconv"
	"strings"

	"golang.org/x/net/html"
	"golang.org/x/net/html/atom"
)

// bibleRenderClasses maps a publisher block class to the ark class its
// rendered paragraph carries. Poetry keeps its per-line structure — a stanza
// is one chunk but several lines on the page — while prose collapses to one
// paragraph class. An unlisted class renders as prose rather than being
// dropped, so an edition ark has not seen still reads.
// CRC: crc-BibleRenderer.md | R3181
var bibleRenderClasses = map[string]string{
	"normal":                   "ark-bible-p",
	"no-indent":                "ark-bible-p",
	"line-group":               "ark-bible-line",
	"line-group-after-heading": "ark-bible-line",
	"line":                     "ark-bible-line",
	"line-indent":              "ark-bible-line ark-bible-indent",
	"line-space":               "ark-bible-line ark-bible-space",
}

// renderBibleXHTML transforms a block of the publisher's XHTML into ark's own
// controlled elements (R3183). It is the whole of "intermediate, don't serve":
// no attribute of the source is ever copied to the output, so the inline
// `onclick` handlers, the `<script>` reference, the external stylesheet links,
// and the footnote/crossref hrefs cannot survive by construction rather than
// by a blocklist. What ark emits is `<p class="ark-bible-*">` and
// `<ark-verse>`, plus escaped text.
//
// Recognition is over the parsed document, where a `verse-num` span is
// structurally distinct from any number in the prose, so only real verse marks
// become `<ark-verse>` and sentence text is left alone (R3181, R3183).
//
// Verse elements come out holding only their number; insertVerseExtBlocks
// fills them after wrapTagElements, for the reason documented there.
// CRC: crc-BibleRenderer.md | R3181, R3183
func renderBibleXHTML(block []byte) string {
	ctx := &html.Node{Type: html.ElementNode, Data: "body", DataAtom: atom.Body}
	nodes, err := html.ParseFragment(bytes.NewReader(block), ctx)
	if err != nil {
		// Unparseable markup renders as escaped text rather than as markup:
		// the one thing that must never happen is the source reaching the
		// page intact (R3183).
		return template.HTMLEscapeString(string(block))
	}

	var buf strings.Builder
	verse, anchored := 0, 0
	var walk func(n *html.Node)
	walk = func(n *html.Node) {
		switch n.Type {
		case html.TextNode:
			buf.WriteString(template.HTMLEscapeString(n.Data))
			return
		case html.ElementNode:
		default:
			return
		}

		class, id := bibleNodeAttr(n, "class"), bibleNodeAttr(n, "id")
		// R3181: identity comes from the publisher's ids, the same source the
		// chunker reads, so a wrapped verse and a chunk's `verses` attribute
		// cannot disagree. Every element carrying one updates the current
		// verse — the enclosing `hBBCCCVVV` span, the `<p>`'s own `vBBCCCVVV`,
		// or the verse-num span's id — so the mark itself needs no id.
		if v, ok := bibleVerseOf(id, class); ok {
			verse = v
		}

		switch {
		case bibleHasApparatusClass(class) && !bibleHasClass(class, "verse-num"):
			// chapter-num, book-name, footnote, crossref — display apparatus
			// and popup anchors into sibling files. Dropped whole, children
			// and all (R3211, R3183).
			return
		case bibleHasClass(class, "verse-num"):
			anchored = verse
			buf.WriteString(`<ark-verse n="`)
			buf.WriteString(strconv.Itoa(verse))
			buf.WriteString(`">`)
			buf.WriteString(template.HTMLEscapeString(bibleTextOf(n)))
			buf.WriteString(`</ark-verse>`)
			return
		case n.DataAtom == atom.P:
			if bibleClassKind(class, true) == blockSkip {
				return // an editorial heading or division label (R3213)
			}
			cls, ok := bibleRenderClasses[class]
			if !ok {
				cls = "ark-bible-p"
			}
			buf.WriteString(`<p class="` + cls + `">`)
			writeNumberlessAnchor(&buf, n, verse, &anchored)
			bibleWalkChildren(n, walk)
			buf.WriteString(`</p>`)
			return
		}
		// Any other element is transparent: its text belongs to the page, its
		// markup does not.
		writeNumberlessAnchor(&buf, n, verse, &anchored)
		bibleWalkChildren(n, walk)
	}
	for _, n := range nodes {
		walk(n)
	}
	return buf.String()
}

// writeNumberlessAnchor emits an empty `<ark-verse n="N">` at the head of an
// element that opens a verse the publisher printed no number for (R3222) —
// the first verse of every chapter, where a `chapter-num` drop cap stands in
// its place. Without it that verse is the one per chapter nothing can address,
// and a routing aimed at it would resolve and then have no element to render
// in.
//
// Two guards keep it from firing wrongly: the subtree lookahead skips any verse
// that *does* carry a number (its own span is the anchor), and `anchored` is a
// high-water mark, so a verse whose identity is repeated by both a `<p>` and
// its inner span is anchored once.
// CRC: crc-BibleRenderer.md | R3222
func writeNumberlessAnchor(buf *strings.Builder, n *html.Node, verse int, anchored *int) {
	if verse == 0 || verse == *anchored || bibleHasVerseNum(n) {
		return
	}
	*anchored = verse
	buf.WriteString(`<ark-verse n="` + strconv.Itoa(verse) + `"></ark-verse>`)
}

// bibleHasVerseNum reports whether an element's subtree holds a `verse-num`
// span — the lookahead that tells a numbered verse from a numberless one.
func bibleHasVerseNum(n *html.Node) bool {
	if n.Type == html.ElementNode && bibleHasClass(bibleNodeAttr(n, "class"), "verse-num") {
		return true
	}
	for c := n.FirstChild; c != nil; c = c.NextSibling {
		if bibleHasVerseNum(c) {
			return true
		}
	}
	return false
}

func bibleWalkChildren(n *html.Node, walk func(*html.Node)) {
	for c := n.FirstChild; c != nil; c = c.NextSibling {
		walk(c)
	}
}

// bibleNodeAttr returns an element's attribute value, or "".
func bibleNodeAttr(n *html.Node, key string) string {
	for _, a := range n.Attr {
		if a.Key == key {
			return a.Val
		}
	}
	return ""
}

// bibleHasClass reports whether a class attribute carries a given token.
func bibleHasClass(class, want string) bool {
	for _, c := range strings.Fields(class) {
		if c == want {
			return true
		}
	}
	return false
}

// bibleHasApparatusClass reports whether any of the class tokens names display
// apparatus rather than scripture — the same set the chunker strips (R3211).
func bibleHasApparatusClass(class string) bool {
	for _, c := range strings.Fields(class) {
		if bibleApparatusClasses[c] {
			return true
		}
	}
	return false
}

// bibleVerseOf reads the verse number out of an element's `vBBCCCVVV` id or
// `hBBCCCVVV` class — the identity the chunker reads (R3181). Reports ok=false
// when the element carries no such token, or names a chapter opening rather
// than a verse (verse field 000).
// CRC: crc-BibleRenderer.md | R3181
func bibleVerseOf(id, class string) (int, bool) {
	for _, tok := range append(strings.Fields(id), strings.Fields(class)...) {
		m := bibleIDRe.FindStringSubmatch(tok)
		if m == nil {
			continue
		}
		if v, err := strconv.Atoi(m[3]); err == nil && v > 0 {
			return v, true
		}
	}
	return 0, false
}

// bibleTextOf collects an element's text, dropping its markup — used for a
// verse number, whose digit sits inside a popup anchor ark does not emit.
func bibleTextOf(n *html.Node) string {
	var b strings.Builder
	var walk func(*html.Node)
	walk = func(n *html.Node) {
		if n.Type == html.TextNode {
			b.WriteString(n.Data)
		}
		for c := n.FirstChild; c != nil; c = c.NextSibling {
			walk(c)
		}
	}
	walk(n)
	return strings.TrimSpace(b.String())
}

// bibleLineSlice returns the publisher's XHTML for the block a chunk's
// line-range label names. The chunker's own line splitter does the work, so
// the render and the index can never disagree about where a block begins.
//
// This is why the render side reads the file rather than the chunk: a chunk's
// stored content is stripped prose (R3211), which carries no verse marks at
// all. Returns nil when the label is not a line range or falls outside the
// file, and the caller falls back.
// CRC: crc-BibleRenderer.md | R3181
func bibleLineSlice(content []byte, rangeLabel string) []byte {
	dash := strings.IndexByte(rangeLabel, '-')
	if dash <= 0 {
		return nil
	}
	first, err := strconv.Atoi(rangeLabel[:dash])
	if err != nil {
		return nil
	}
	last, err := strconv.Atoi(rangeLabel[dash+1:])
	if err != nil || first < 1 || last < first {
		return nil
	}
	lines := bibleLines(content)
	if last > len(lines) {
		return nil
	}
	return content[lines[first-1].start:lines[last-1].end]
}

// insertVerseExtBlocks places each verse's `<ark-ext-tags>` block inside its
// `<ark-verse>` element, keyed by verse number. Verses absent from byVerse are
// left empty (R3182).
//
// This runs **after** wrapTagElements rather than being emitted by the
// renderer, which looks like an extra pass and is deliberate: ext markup
// must never pass through wrapTagElements. That function re-wraps any `@word:`
// pattern it sees and does not skip `<ark-tag>` interiors (design gap C2), so a
// routed tag *value* containing an `@word:` — ordinary for ark's compound tags
// — would be re-wrapped into a nested tag inside a `<value>`. Every other
// content kind preserves this invariant by writing the ext block outside
// wrapTagElements in chunkDiv; the bible path preserves it by inserting here.
//
// The scan is structural, not textual: it locates an element this renderer
// emitted, identified by an attribute it controls. Verses do not nest, so the
// first `</ark-verse>` after an opening tag is that element's close.
// CRC: crc-BibleRenderer.md | R3182
func insertVerseExtBlocks(html string, byVerse map[int]string) string {
	const open = `<ark-verse n="`
	const closeTag = `</ark-verse>`
	if len(byVerse) == 0 || !strings.Contains(html, open) {
		return html
	}

	var buf strings.Builder
	buf.Grow(len(html))
	rest := html
	for {
		i := strings.Index(rest, open)
		if i < 0 {
			break
		}
		numStart := i + len(open)
		quote := strings.IndexByte(rest[numStart:], '"')
		if quote < 0 {
			break
		}
		end := strings.Index(rest[i:], closeTag)
		if end < 0 {
			break
		}
		end += i

		buf.WriteString(rest[:end])
		if v, err := strconv.Atoi(rest[numStart : numStart+quote]); err == nil {
			buf.WriteString(byVerse[v]) // "" when this verse carries nothing
		}
		buf.WriteString(closeTag)
		rest = rest[end+len(closeTag):]
	}
	buf.WriteString(rest)
	return buf.String()
}
