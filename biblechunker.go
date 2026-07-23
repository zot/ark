package ark

// Bible chunking strategy — see specs/bible-chunker.md.
// CRC: crc-BibleChunker.md | R3172, R3173, R3175, R3176, R3177, R3178, R3209, R3210, R3211, R3212, R3213

import (
	"bytes"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"sync"

	"github.com/zot/microfts2"
	"golang.org/x/net/html"
	"golang.org/x/net/html/atom"
)

// bibleStrategy is the registered name of the bible chunking strategy.
// R3172
const bibleStrategy = "bible"

// bibleVirtualSegment is the one reserved path segment under a bible source:
// `<source>/BIBLE/<Book>` is a virtual address, not a file. One reserved name
// rather than sixty-six root-level book names, which are common words
// (Numbers, Acts, Mark, Job) that would silently shadow real files. R3216
const bibleVirtualSegment = "BIBLE"

// bibleTextSuffix is the only file kind the strategy handles — the sibling
// `.main`/`.crossrefs`/`.footnotes`/`.resources` files are book intros and
// reference apparatus. R3209
const bibleTextSuffix = ".text.xhtml"

// bibleChunker chunks scripture held as a publisher's XHTML (an ESV epub is
// the worked example) into prose blocks, reading each block's chapter and
// verse identity from the publisher's own `vBBCCCVVV`/`hBBCCCVVV` ids rather
// than recognizing marks in the text (R3210). The files are line-oriented —
// one element per line — so blocks carry line/byte ranges for retrieval while
// their indexed content is the prose alone, apparatus stripped. Read-only: a
// reference corpus is annotated beside itself, never edited in place.
//
// Holds a reference to ark.DB so FlushBookIndex can persist the book-index
// records Chunks stages, the same staging split PDFChunker uses: microfts2
// re-runs Chunks at retrieval time, so a chunker that wrote during the walk
// would persist on every read. R3214
// CRC: crc-BibleChunker.md | R3173, R3178, R3210, R3214
type bibleChunker struct {
	db      *DB
	mu      sync.Mutex
	pending map[string][]int // path → the distinct chapters that file holds
}

// newBibleChunker constructs a chunker bound to ark's DB.
// CRC: crc-BibleChunker.md | R3214
func newBibleChunker(db *DB) *bibleChunker {
	return &bibleChunker{db: db, pending: make(map[string][]int)}
}

// bibleLine is one line of the file: its 1-based number, its start offset, and
// the offset just past its terminating newline (or EOF for a final line with
// none). The publisher's XHTML puts one element per line, so a block is a run
// of whole lines and its byte range covers them exactly.
type bibleLine struct {
	num   int
	start int
	end   int
	blank bool
}

// bibleLines splits content into lines with their offsets.
func bibleLines(content []byte) []bibleLine {
	var lines []bibleLine
	num, pos := 0, 0
	for pos < len(content) {
		num++
		nl := bytes.IndexByte(content[pos:], '\n')
		textEnd, end := len(content), len(content)
		if nl >= 0 {
			textEnd, end = pos+nl, pos+nl+1
		}
		lines = append(lines, bibleLine{
			num:   num,
			start: pos,
			end:   end,
			blank: len(bytes.TrimSpace(content[pos:textEnd])) == 0,
		})
		pos = end
	}
	return lines
}

// biblePClassRe extracts the class of the first <p> on a line. The XHTML is
// line-oriented, so this classifies the line's block without a full parse.
var biblePClassRe = regexp.MustCompile(`<p[^>]*\bclass="([^"]*)"`)

// bibleIDRe recognizes a publisher identity token `vBBCCCVVV` / `hBBCCCVVV`:
// book (2 digits), chapter (3), verse (3). R3210.
var bibleIDRe = regexp.MustCompile(`^[vh](\d{2})(\d{3})(\d{3})$`)

// bibleChapterSectionRe recognizes the opening tag of the container that holds
// scripture. `chapter` is matched as a whole token of epub:type, which may
// carry several. R3224.
var bibleChapterSectionRe = regexp.MustCompile(`<section[^>]*\bepub:type="[^"]*\bchapter\b[^"]*"`)

// bibleApparatusClasses are the span classes whose text is display apparatus,
// not scripture prose, and is stripped from a chunk's indexed text. R3211.
var bibleApparatusClasses = map[string]bool{
	"verse-num":   true,
	"chapter-num": true,
	"book-name":   true,
	"footnote":    true,
	"crossref":    true,
}

// bibleBlockKind classifies a line's <p> for block assembly.
type bibleBlockKind int

const (
	blockSkip       bibleBlockKind = iota // heading (R3213) or a structural line
	blockProse                            // a prose paragraph — one chunk (R3173)
	blockPoetryOpen                       // a stanza opener — starts a chunk (R3212)
	blockPoetryCont                       // a poetry continuation line — extends it
)

// bibleClassKind maps a <p>'s class to its block kind. A poetry stanza opens
// with line-group / line-group-after-heading and continues through line /
// line-indent / line-space; a heading is dropped; everything else with a <p>
// (normal, no-indent, and the chapter-start variants) is prose. A line with no
// <p> class (a <section>, <body>, or the like) is structural and skipped.
// R3173, R3212, R3213.
func bibleClassKind(class string, hasP bool) bibleBlockKind {
	switch {
	case !hasP:
		return blockSkip
	case class == "heading" || class == "psalm-book":
		// editorial labels — a pericope title or a Psalter division
		// ("Book One"). Not scripture; dropped like a heading (R3213). The
		// division label carries verse 1's id, so keeping it would shadow the
		// real verse-1 chunk in resolution.
		return blockSkip
	case class == "line-space":
		// Prose with a gap above it, despite the name. The edition gives
		// line-space a top margin and nothing else, so it keeps the base
		// paragraph's first-line indent, while every genuine poetry class
		// overrides margin-left and text-indent. Reading it as poetry merged
		// 150 of its 151 occurrences into the stanza above (Genesis 1:28 into
		// the poetry of 1:27). R3212
		return blockProse
	case strings.Contains(class, "line-group"):
		return blockPoetryOpen
	case strings.HasPrefix(class, "line"):
		return blockPoetryCont
	default:
		return blockProse
	}
}

// Chunks emits one chunk per block — a prose paragraph or a poetry stanza —
// with the block's prose as content (apparatus stripped, R3211) and its
// chapter/verses read from the ids (R3175, R3176, R3210). Blocks are runs of
// whole lines, so Range is the line span and Locator the byte span; retrieval
// re-runs this pass and matches by Range, so the transformed content is
// re-derived rather than stored.
// CRC: crc-BibleChunker.md | R3173, R3175, R3176, R3210, R3211, R3212, R3213, R3224
func (c *bibleChunker) Chunks(path string, content []byte, yield func(microfts2.Chunk) bool) error {
	open := false
	var startLine, startByte, endLine, endByte int
	var chapters []int

	flush := func() bool {
		if !open {
			return true
		}
		open = false
		text, chapter, verses := bibleBlock(content[startByte:endByte])
		if len(text) == 0 {
			return true // no prose survived the strip — nothing to index
		}
		// R3214: the file's chapter set is what the book index records. It
		// comes from the same ids the attributes do, so the index and the
		// chunks cannot disagree about which chapters the file holds.
		if n, err := strconv.Atoi(chapter); err == nil && (len(chapters) == 0 || chapters[len(chapters)-1] != n) {
			chapters = append(chapters, n)
		}
		return yield(microfts2.Chunk{
			Range:   []byte(fmt.Sprintf("%d-%d", startLine, endLine)),
			Locator: microfts2.EncodeByteRangeLocator(startByte, endByte),
			Content: text,
			Attrs:   bibleAttrs(chapter, verses),
		})
	}

	// R3224: sectionDepth tracks nesting; chapterDepth records the depth the
	// scripture container opened at, and is 0 whenever the walk is outside one.
	sectionDepth, chapterDepth := 0, 0

	for _, ln := range bibleLines(content) {
		line := content[ln.start:ln.end]
		if bytes.Contains(line, []byte("<section")) {
			sectionDepth++
			if chapterDepth == 0 && bibleChapterSectionRe.Match(line) {
				chapterDepth = sectionDepth
			}
		}
		if bytes.Contains(line, []byte("</section>")) {
			if sectionDepth == chapterDepth {
				chapterDepth = 0
			}
			if sectionDepth > 0 {
				sectionDepth--
			}
		}

		if ln.blank {
			continue
		}
		// Outside a chapter section nothing is scripture — the file's appended
		// footnote, cross-reference, and navigation blocks live here. R3224
		if chapterDepth == 0 {
			if !flush() {
				return nil
			}
			continue
		}
		class, hasP := biblePClass(line)
		switch bibleClassKind(class, hasP) {
		case blockSkip:
			if !flush() {
				return nil
			}
		case blockProse:
			if !flush() {
				return nil
			}
			open, startLine, startByte, endLine, endByte = true, ln.num, ln.start, ln.num, ln.end
			if !flush() {
				return nil
			}
		case blockPoetryOpen:
			if !flush() {
				return nil
			}
			open, startLine, startByte, endLine, endByte = true, ln.num, ln.start, ln.num, ln.end
		case blockPoetryCont:
			if !open {
				open, startLine, startByte = true, ln.num, ln.start
			}
			endLine, endByte = ln.num, ln.end
		}
	}
	flush()
	// Only a walk that ran to the end has seen the file's whole chapter set;
	// the early returns above are a consumer stopping short (retrieval seeking
	// one range), which must not stage a truncated set. R3214
	c.stageBookIndex(path, chapters)
	return nil
}

// stageBookIndex records the chapters a completed walk of path found, for
// FlushBookIndex to persist once indexing commits. Replaces rather than
// appends, so a re-walk of the same file cannot accumulate.
// CRC: crc-BibleChunker.md | R3214
func (c *bibleChunker) stageBookIndex(path string, chapters []int) {
	if c == nil || len(chapters) == 0 {
		return
	}
	c.mu.Lock()
	if c.pending == nil {
		// A chunker registered without a DB (InitDB) still gets walked if
		// anything chunks in that process; staging into a nil map would panic.
		c.pending = make(map[string][]int)
	}
	c.pending[path] = chapters
	c.mu.Unlock()
}

// FlushBookIndex writes the staged book-index records for path — one per
// chapter, `B<source>\0<book>\0<chapter>` → path (R3214). Called by the
// indexer after the file is committed, mirroring PDFChunker.FlushBlobs.
// The source comes from the file's enclosing source, the book from its
// filename token (R3215); a file outside every source has no key to write
// under and is skipped.
// CRC: crc-BibleChunker.md | R3214, R3215
func (c *bibleChunker) FlushBookIndex(path string) error {
	c.mu.Lock()
	chapters := c.pending[path]
	delete(c.pending, path)
	c.mu.Unlock()
	if c.db == nil || c.db.store == nil || len(chapters) == 0 {
		return nil
	}
	src, _, ok := c.db.findSourceForPath(path)
	if !ok {
		return nil
	}
	book := bibleBookName(bibleFileToken(path))
	if book == "" {
		return nil
	}
	for _, chapter := range chapters {
		if err := c.db.store.WriteBookIndex(src.Dir, book, chapter, path); err != nil {
			return fmt.Errorf("bible: book index %s %d: %w", book, chapter, err)
		}
	}
	return nil
}

// ActivateForSource is the per-source hook (R3217): it runs once per source
// that maps this chunker locally, at config-resolve on every startup.
//
// It registers `<source>/BIBLE/** → bible` so the virtual book addresses
// classify as bible and dispatch to the bible resolver with no bespoke branch
// (R3218). A global-map entry is safe here precisely because it is
// source-prefixed — it can only ever match that source's own paths — and it is
// the filesystem-absolute `/X` form, matched against the absolute path.
// Nothing is persisted; the entry is re-derived every boot.
//
// It also runs the guard (R3219): a real `<source>/BIBLE` path on disk would
// make the reserved namespace ambiguous, so the source fails to load with an
// error naming the collision rather than silently shadowing one or the other.
// CRC: crc-BibleChunker.md | R3217, R3218, R3219
func (c *bibleChunker) ActivateForSource(src *Source, register func(pattern, strategy string) error) error {
	reserved := filepath.Join(src.Dir, bibleVirtualSegment)
	if _, err := os.Lstat(reserved); err == nil {
		return fmt.Errorf("%s exists, but a bible source reserves that path for %s/<Book> addresses — move or rename it", reserved, bibleVirtualSegment)
	}
	return register(reserved+"/**", bibleStrategy)
}

// ReconcileBookIndex drops book-index records belonging to any source outside
// declared — the sources that still map `bible` in the config just resolved
// (R3221). It runs once after the whole activation pass, and it runs even when
// declared is empty, which is precisely the case a per-source hook can never
// reach: a source removed from the config gets no hook call, so nothing else
// would ever notice its records.
//
// The criterion is *declares the strategy*, not *activated successfully*. A
// source whose activation failed the R3219 collision guard is still a bible
// source, and a misconfiguration is no reason to delete indexed data.
// CRC: crc-BibleChunker.md | Seq: seq-bible-resolve.md#1.11 | R3221
func (c *bibleChunker) ReconcileBookIndex(declared []*Source) error {
	if c == nil || c.db == nil || c.db.store == nil {
		return nil
	}
	keep := make(map[string]bool, len(declared))
	for _, src := range declared {
		keep[src.Dir] = true
	}
	return c.db.store.PruneBookIndex(func(source string) bool { return keep[source] })
}

// bibleFileToken extracts the book token from an epub text filename:
// `b43.02.John.text.xhtml` → `John`, `b09.01.1-Samuel.text.xhtml` →
// `1-Samuel`. The leading `bNN.MM.` is the publisher's book and part numbers —
// a book spans several files, which is why the book index maps chapters to
// files rather than assuming one file per book. Returns "" for a name that is
// not a text file.
// CRC: crc-BibleChunker.md | R3209, R3215
func bibleFileToken(path string) string {
	base := filepath.Base(path)
	if !strings.HasSuffix(base, bibleTextSuffix) {
		return ""
	}
	stem := strings.TrimSuffix(base, bibleTextSuffix)
	if dot := strings.LastIndexByte(stem, '.'); dot >= 0 {
		stem = stem[dot+1:]
	}
	return stem
}

// biblePClass returns the class of the line's first <p>, and whether the line
// carries a <p> at all.
func biblePClass(line []byte) (string, bool) {
	m := biblePClassRe.FindSubmatch(line)
	if m == nil {
		return "", false
	}
	return string(m[1]), true
}

// bibleBlock parses one block's XHTML once, returning its prose text (apparatus
// stripped, whitespace normalized — R3211) and its chapter and verse span read
// from the ids (R3175, R3176, R3210). chapter is empty and verses empty when
// the block carries no verse-bearing id (a preamble). verses is `first-last`,
// or the bare number for a single verse.
// CRC: crc-BibleChunker.md | R3175, R3176, R3210, R3211
func bibleBlock(body []byte) (text []byte, chapter, verses string) {
	ctx := &html.Node{Type: html.ElementNode, Data: "body", DataAtom: atom.Body}
	nodes, err := html.ParseFragment(bytes.NewReader(body), ctx)
	if err != nil {
		return nil, "", ""
	}
	var buf bytes.Buffer
	loV, hiV := -1, -1
	var walk func(n *html.Node, inApparatus bool)
	walk = func(n *html.Node, inApparatus bool) {
		if n.Type == html.ElementNode {
			for _, a := range n.Attr {
				if a.Key == "id" || a.Key == "class" {
					for _, tok := range strings.Fields(a.Val) {
						if m := bibleIDRe.FindStringSubmatch(tok); m != nil {
							if chapter == "" {
								c, _ := strconv.Atoi(m[2])
								chapter = strconv.Itoa(c)
							}
							if v, _ := strconv.Atoi(m[3]); v > 0 {
								if loV < 0 || v < loV {
									loV = v
								}
								if v > hiV {
									hiV = v
								}
							}
						}
					}
				}
				if a.Key == "class" {
					for _, c := range strings.Fields(a.Val) {
						if bibleApparatusClasses[c] {
							inApparatus = true
						}
					}
				}
			}
		}
		if n.Type == html.TextNode && !inApparatus {
			buf.WriteString(n.Data)
		}
		for c := n.FirstChild; c != nil; c = c.NextSibling {
			walk(c, inApparatus)
		}
	}
	for _, n := range nodes {
		walk(n, false)
	}
	if loV >= 0 {
		if loV == hiV {
			verses = strconv.Itoa(loV)
		} else {
			verses = strconv.Itoa(loV) + "-" + strconv.Itoa(hiV)
		}
	}
	if fields := strings.Fields(buf.String()); len(fields) > 0 {
		text = []byte(strings.Join(fields, " "))
	}
	return text, chapter, verses
}

// bibleAttrs builds a block's per-chunk metadata from the chapter and verse
// span already read from the ids. Nothing is fabricated: a block with no verse
// identity carries neither attribute.
// CRC: crc-BibleChunker.md | R3175, R3176
func bibleAttrs(chapter, verses string) []microfts2.Pair {
	var attrs []microfts2.Pair
	if chapter != "" {
		attrs = append(attrs, microfts2.Pair{Key: []byte("chapter"), Value: []byte(chapter)})
	}
	if verses != "" {
		attrs = append(attrs, microfts2.Pair{Key: []byte("verses"), Value: []byte(verses)})
	}
	return attrs
}

// IsWritable reports false: bible files are a reference corpus whose text is
// fixed and whose verse numbering every annotation depends on. Existing
// machinery does the rest — inline `@tag:` insertion is refused, so annotation
// degrades to the external disposition and lands in a mirror file, and the
// content view drops its edit affordance. R3178
// CRC: crc-BibleChunker.md | R3178
func (*bibleChunker) IsWritable() bool { return false }

// CommentSyntax reports "" — scripture prose has no comment form. Travels with
// IsWritable as microfts2's ChunkerMetadata pair. R3178
// CRC: crc-BibleChunker.md | R3178
func (*bibleChunker) CommentSyntax() string { return "" }

// bibleBookName turns an epub filename token into the canonical book name — the
// hyphens the epub uses for spaces become spaces (`1-Samuel` → `1 Samuel`,
// `Song-of-Solomon` → `Song of Solomon`), spelled as the edition spells it
// (`Psalm`, singular). R3215.
// CRC: crc-BibleChunker.md | R3215
func bibleBookName(fileToken string) string {
	return strings.ReplaceAll(fileToken, "-", " ")
}

// bibleVirtualTarget recognizes the friendly address `<source>/BIBLE/<Book>`
// and splits it into its source directory and book name (R3216). The book is
// the whole remainder, so a spaced name (`1 Samuel`) works and a nested path
// does not — `BIBLE/` names exactly one book, not a subtree.
//
// Nothing on disk answers to this path; recognizing it is what lets the
// resolver reach the book index instead of the filesystem.
// CRC: crc-BibleChunker.md | Seq: seq-bible-resolve.md#3.3 | R3216
func bibleVirtualTarget(path string) (source, book string, ok bool) {
	const sep = "/" + bibleVirtualSegment + "/"
	i := strings.Index(path, sep)
	if i <= 0 {
		return "", "", false
	}
	source, book = path[:i], path[i+len(sep):]
	if book == "" || strings.Contains(book, "/") {
		return "", "", false
	}
	return source, book, true
}

// parseChapterVerse splits a CHAPTER.VERSE reference (`12.1`) into its two
// numbers. Exactly one dot, both sides positive integers — anything else is
// not a verse reference and the caller falls back to its ordinary anchor
// handling, so a line range like `3-6` passes through untouched.
// CRC: crc-BibleChunker.md | R3179
func parseChapterVerse(s string) (chapter, verse int, ok bool) {
	dot := strings.IndexByte(s, '.')
	if dot <= 0 || dot == len(s)-1 {
		return 0, 0, false
	}
	c, err := strconv.Atoi(s[:dot])
	if err != nil || c <= 0 {
		return 0, 0, false
	}
	v, err := strconv.Atoi(s[dot+1:])
	if err != nil || v <= 0 {
		return 0, 0, false
	}
	return c, v, true
}

// verseSpanContains reports whether a chunk's `verses` attribute value covers
// verse — `"1-2"` covers 1 and 2, the bare `"3"` covers only 3. A malformed or
// empty span covers nothing rather than everything, so a bad attribute drops
// the chunk out of consideration instead of matching every reference.
// CRC: crc-BibleChunker.md | R3176, R3179
func verseSpanContains(span string, verse int) bool {
	if span == "" {
		return false
	}
	lo, hi := span, span
	if dash := strings.IndexByte(span, '-'); dash > 0 {
		lo, hi = span[:dash], span[dash+1:]
	}
	first, err := strconv.Atoi(lo)
	if err != nil {
		return false
	}
	last, err := strconv.Atoi(hi)
	if err != nil {
		return false
	}
	return verse >= first && verse <= last
}
