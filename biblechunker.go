package ark

// Bible chunking strategy — see specs/bible-chunker.md.
// CRC: crc-BibleChunker.md | R3172, R3173, R3174, R3175, R3176, R3177, R3178

import (
	"bytes"
	"fmt"
	"regexp"
	"strconv"
	"strings"

	"github.com/zot/microfts2"
)

// bibleStrategy is the registered name of the bible chunking strategy.
// R3172
const bibleStrategy = "bible"

// bibleVerseMarkRe matches a verse mark: a backtick-wrapped integer. The
// backticks are what make it unambiguous — nothing else in scripture prose
// can be mistaken for a mark, and ordinary markdown viewers render it as a
// small code span. R3174
var bibleVerseMarkRe = regexp.MustCompile("`(\\d+)`")

// bibleChapterHeadingRe matches a chapter heading — an ATX heading of any
// level whose text ends `Chapter N`. Any level, because the book title may
// occupy `#` and a chapter `##`, or a file may nest a level deeper. R3175
var bibleChapterHeadingRe = regexp.MustCompile(`(?i)^#{1,6}\s+.*\bchapter\s+(\d+)\s*$`)

// bibleChunker chunks scripture markdown one paragraph at a time, stamping
// each chunk with the chapter it falls in and the verse marks it contains,
// so a CHAPTER.VERSE reference can find its paragraph without storage
// carrying a verse dimension. Read-only: a reference corpus is annotated
// beside itself, never edited in place.
// CRC: crc-BibleChunker.md | R3173, R3178
type bibleChunker struct{}

// bibleLine is one line of the file: its 1-based number, its start offset,
// and the offset just past its terminating newline (or EOF for a final line
// with none). The past-the-newline end is what makes a block's byte range
// cover its content exactly, trailing newline included, matching what
// microfts2's own line-oriented chunkers encode.
type bibleLine struct {
	num   int
	start int
	end   int
	blank bool
}

// bibleLines splits content into lines with their offsets. A line is blank
// when it holds nothing but whitespace — blank lines are the paragraph
// separators R3173 chunks on.
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

// Chunks emits one chunk per blank-line-separated block (R3173), carrying
// the chapter in force (R3175) and the block's verse span (R3176).
// CRC: crc-BibleChunker.md | R3173, R3175, R3176
func (bibleChunker) Chunks(path string, content []byte, yield func(microfts2.Chunk) bool) error {
	chapter := "" // carried forward; empty before the first chapter heading
	blockStart := -1
	blockStartLine, blockEndLine, blockEndByte := 0, 0, 0
	blockChapter := ""

	emit := func() bool {
		if blockStart < 0 {
			return true
		}
		body := content[blockStart:blockEndByte]
		ok := yield(microfts2.Chunk{
			Range:   []byte(fmt.Sprintf("%d-%d", blockStartLine, blockEndLine)),
			Locator: microfts2.EncodeByteRangeLocator(blockStart, blockEndByte),
			Content: body,
			Attrs:   bibleAttrs(blockChapter, body),
		})
		blockStart = -1
		return ok
	}

	for _, ln := range bibleLines(content) {
		if ln.blank {
			if !emit() {
				return nil
			}
			continue
		}
		if blockStart < 0 {
			blockStart, blockStartLine, blockChapter = ln.start, ln.num, chapter
		}
		// A chapter heading inside the block sets the chapter for this block
		// and every later one, until the next heading. R3175
		if m := bibleChapterHeadingRe.FindSubmatch(content[ln.start:ln.end]); m != nil {
			chapter = string(m[1])
			blockChapter = chapter
		}
		blockEndLine, blockEndByte = ln.num, ln.end
	}
	emit()
	return nil
}

// bibleAttrs builds a block's per-chunk metadata: the chapter in force
// (omitted before the file's first chapter heading) and the span of verse
// marks in the block — `first-last`, or the bare number for a single mark,
// omitted when the block holds none. Nothing is fabricated: a block with no
// marks is ordinary markdown and says so by omission.
// CRC: crc-BibleChunker.md | R3175, R3176
func bibleAttrs(chapter string, body []byte) []microfts2.Pair {
	var attrs []microfts2.Pair
	if chapter != "" {
		attrs = append(attrs, microfts2.Pair{Key: []byte("chapter"), Value: []byte(chapter)})
	}
	marks := bibleVerseMarkRe.FindAllSubmatch(body, -1)
	if len(marks) == 0 {
		return attrs
	}
	first, last := string(marks[0][1]), string(marks[len(marks)-1][1])
	span := first
	if first != last {
		span = first + "-" + last
	}
	return append(attrs, microfts2.Pair{Key: []byte("verses"), Value: []byte(span)})
}

// IsWritable reports false: bible files are a reference corpus whose text is
// fixed and whose verse numbering every annotation depends on. Existing
// machinery does the rest — inline `@tag:` insertion is refused, so
// annotation degrades to the external disposition and lands in a mirror
// file, and the content view drops its edit affordance. R3178
// CRC: crc-BibleChunker.md | R3178
func (bibleChunker) IsWritable() bool { return false }

// CommentSyntax reports "" — scripture prose has no comment form. Travels
// with IsWritable as microfts2's ChunkerMetadata pair. R3178
// CRC: crc-BibleChunker.md | R3178
func (bibleChunker) CommentSyntax() string { return "" }

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
