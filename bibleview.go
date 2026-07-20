package ark

// Bible content rendering — see specs/bible-chunker.md §Display.
// CRC: crc-BibleRenderer.md | R3181, R3182, R3183

import (
	"html/template"
	"strconv"
	"strings"

	"github.com/yuin/goldmark/ast"
	"github.com/yuin/goldmark/parser"
	"github.com/yuin/goldmark/renderer"
	"github.com/yuin/goldmark/text"
	"github.com/yuin/goldmark/util"
)

// arkVerseNode is an inline node carrying a prepared `<ark-verse>` element.
// Its whole purpose is to be a node type goldmark does not know: the unsafe
// gate that strips raw HTML guards goldmark's own RawHTML/HTMLBlock nodes, and
// a custom kind with its own renderer never meets it. That is what lets the
// verse element render while raw HTML stays disabled for every indexed file
// (R3183).
// CRC: crc-BibleRenderer.md | R3183
type arkVerseNode struct {
	ast.BaseInline
	html string
}

var kindArkVerse = ast.NewNodeKind("ArkVerse")

func (n *arkVerseNode) Kind() ast.NodeKind { return kindArkVerse }

func (n *arkVerseNode) Dump(src []byte, level int) {
	ast.DumpHelper(n, src, level, nil, nil)
}

// arkVerseRenderer writes an arkVerseNode's prepared HTML straight to the
// output buffer.
// CRC: crc-BibleRenderer.md | R3181, R3183
type arkVerseRenderer struct{}

func (arkVerseRenderer) RegisterFuncs(reg renderer.NodeRendererFuncRegisterer) {
	reg.Register(kindArkVerse, func(w util.BufWriter, _ []byte, n ast.Node, entering bool) (ast.WalkStatus, error) {
		if entering {
			w.WriteString(n.(*arkVerseNode).html)
		}
		return ast.WalkContinue, nil
	})
}

// bibleVerseTransformer replaces each numeric code span — a verse mark — with
// an `<ark-verse>` element wrapping the same `<code>` the mark already
// rendered as, so the page still reads as the markdown it is (R3181).
//
// It works on the parsed document rather than the rendered HTML, which is what
// makes the discrimination correct: a numeric code span inside a fenced block
// is a different node from one in a paragraph, a distinction no pass over
// output HTML could make. Non-numeric spans are left alone, so ordinary inline
// code in a bible file is untouched (R3183).
//
// It carries no routing state. The verse elements come out empty and the ext
// blocks are inserted afterwards by insertVerseExtBlocks — see there for why.
// CRC: crc-BibleRenderer.md | R3181, R3183
type bibleVerseTransformer struct{}

func (bibleVerseTransformer) Transform(doc *ast.Document, reader text.Reader, _ parser.Context) {
	src := reader.Source()

	// Collect first, mutate after: replacing a child during the walk would
	// disturb the traversal.
	var marks []*ast.CodeSpan
	ast.Walk(doc, func(n ast.Node, entering bool) (ast.WalkStatus, error) {
		if !entering {
			return ast.WalkContinue, nil
		}
		if cs, ok := n.(*ast.CodeSpan); ok {
			if _, err := strconv.Atoi(codeSpanText(cs, src)); err == nil {
				marks = append(marks, cs)
			}
		}
		return ast.WalkContinue, nil
	})

	for _, cs := range marks {
		parent := cs.Parent()
		if parent == nil {
			continue
		}
		parent.ReplaceChild(parent, cs, &arkVerseNode{html: verseElement(codeSpanText(cs, src))})
	}
}

// codeSpanText returns a code span's literal text. Assembled from the child
// text segments rather than via Node.Text, which is deprecated — a code span's
// children are plain text nodes, so this is the documented replacement.
// CRC: crc-BibleRenderer.md | R3174
func codeSpanText(cs *ast.CodeSpan, src []byte) string {
	var b strings.Builder
	for c := cs.FirstChild(); c != nil; c = c.NextSibling() {
		t, ok := c.(*ast.Text)
		if !ok {
			return "" // anything but plain text is not a verse mark
		}
		b.Write(t.Segment.Value(src))
	}
	return b.String()
}

// verseElement builds the empty verse wrapper for mark number num.
// CRC: crc-BibleRenderer.md | R3181
func verseElement(num string) string {
	n := template.HTMLEscapeString(num)
	return `<ark-verse n="` + n + `"><code>` + n + `</code></ark-verse>`
}

// insertVerseExtBlocks places each verse's `<ark-ext-tags>` block inside its
// `<ark-verse>` element, keyed by verse number. Verses absent from byVerse are
// left empty (R3182).
//
// This runs **after** wrapTagElements rather than being emitted by the
// transformer, which looks like an extra pass and is deliberate: ext markup
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
