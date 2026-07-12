package ark

// Internal-disposition tag insertion. When an `@ext-candidate` is accepted
// with disposition `internal`, ark writes the `@tag: value` into the target
// file's own body — where a human reading the source sees it — instead of
// routing it to the mirror tree. Each writable text chunker owns an
// insertion *stencil* (the rigid per-language format: comment delimiter,
// position, indentation); this file holds the stencils and the capability
// gate that says which file types can host an internal tag.
//
// CRC: crc-Indexer.md

import (
	"strings"

	"github.com/zot/microfts2"
)

// tagScope selects internal-tag placement. Chunk-level lands the tag inside
// the target chunk (so it stays with that chunk after a re-chunk); file-level
// lands it at the top of the file as its own chunk. The scope mirrors the
// `@ext` address granularity: a chunk address (`:range`, `%uuid`, `:"snippet"`)
// is chunk-level; a bare-path target is file-level.
// CRC: crc-Indexer.md | R3097, R3098
type tagScope int

const (
	tagScopeChunk tagScope = iota
	tagScopeFile
)

// tagInserter is the capability gate for internal-disposition tagging: a
// registered chunker type-asserts to it iff its file type can host an inline
// `@tag`. Only the markdown / bracket / indent wrappers implement it;
// `lines` / `chat-jsonl` / `pdf` do not, so an accept with disposition
// `internal` against those degrades to external by construction.
// CRC: crc-Indexer.md | R3096
type tagInserter interface {
	// InsertTag returns fileBytes with an inline `@tag: value` placed so it
	// belongs to targetChunk (tagScopeChunk) or the whole file (tagScopeFile).
	// ok=false signals "cannot write a valid inline tag" — a comment-less code
	// chunker — so the caller falls back to external.
	InsertTag(fileBytes []byte, targetChunk microfts2.Chunk, tag, value string, scope tagScope) (out []byte, ok bool)
}

// stencilKind names the three insertion stencils. They differ in opener
// detection, whether the tag must be comment-wrapped to keep the source
// valid, and whether the column is load-bearing.
// CRC: crc-Indexer.md | R3095
type stencilKind int

const (
	stencilMarkdown stencilKind = iota // bare `@tag:` line; heading opener; headingless edge
	stencilBracket                     // comment-wrapped inside the brace block; column cosmetic
	stencilIndent                      // comment-wrapped; column matches the block body indent
)

// fatChunker is the union of every microfts2 chunker interface the writable
// text chunkers (markdown, bracket, indent) satisfy. A wrapper must embed
// *this*, not the bare `Chunker`, or it silently strips the fast GetChunk /
// AppendChunks / FileChunks paths and the ChunkerMetadata pair that microfts2
// type-asserts for at index and retrieval time.
// CRC: crc-Indexer.md | R3095
type fatChunker interface {
	microfts2.Chunker
	microfts2.AppendAwareChunker
	microfts2.RandomAccessChunker
	microfts2.FileChunker
	microfts2.ChunkerMetadata
}

// internalTagChunker wraps a microfts2 text chunker: every fatChunker method
// promotes to the underlying chunker (so microfts2's assertions still hit the
// real implementation), and InsertTag adds the per-kind insertion stencil.
// CRC: crc-Indexer.md | R3095
type internalTagChunker struct {
	fatChunker
	kind stencilKind
}

// InsertTag implements tagInserter, delegating to the pure insertInternalTag
// with this wrapper's comment delimiter (from the promoted ChunkerMetadata)
// and stencil kind.
// CRC: crc-Indexer.md | R3096, R3097, R3098, R3099
func (w internalTagChunker) InsertTag(fileBytes []byte, targetChunk microfts2.Chunk, tag, value string, scope tagScope) ([]byte, bool) {
	return insertInternalTag(fileBytes, targetChunk, tag, value, scope, w.CommentSyntax(), w.kind)
}

// wrapInternalTagChunker wraps a microfts2 text chunker in an internalTagChunker
// so `ark ext accept` (internal disposition) can write into the file body. c
// must satisfy the fat interface — the markdown/bracket/indent chunkers do; if
// one somehow does not, the bare chunker is returned unchanged so registration
// and indexing still work (internal tagging is simply unavailable for it).
// CRC: crc-Indexer.md | R3095
func wrapInternalTagChunker(c any, kind stencilKind) any {
	if fc, ok := c.(fatChunker); ok {
		return internalTagChunker{fatChunker: fc, kind: kind}
	}
	return c
}

// insertInternalTag places an inline `@tag: value` into fileBytes so it
// belongs to targetChunk (tagScopeChunk) or the whole file (tagScopeFile),
// returning the edited bytes. A code chunker (bracket/indent) comment-wraps
// the tag with commentSyntax to keep the source valid in its own language; a
// comment-less code chunker cannot, so ok=false and the caller falls back to
// external. Markdown needs no comment (a bare `@tag:` line is valid markdown),
// so it always succeeds. Pure — no file I/O — so it is testable in isolation.
// CRC: crc-Indexer.md | R3097, R3098, R3099
func insertInternalTag(fileBytes []byte, targetChunk microfts2.Chunk, tag, value string, scope tagScope, commentSyntax string, kind stencilKind) ([]byte, bool) {
	code := kind != stencilMarkdown
	if code && commentSyntax == "" {
		return nil, false // comment-less code chunk → no valid inline tag → external
	}
	tagText := "@" + strings.ToLower(strings.TrimSpace(tag)) + ":"
	if v := strings.TrimSpace(value); v != "" {
		tagText += " " + v
	}
	if code {
		tagText = commentSyntax + " " + tagText
	}

	if scope == tagScopeFile {
		// Top of file, above the first heading → the @-run stands as its own chunk.
		return spliceLine(fileBytes, 0, "", tagText), true
	}

	start, end, ok := microfts2.DecodeByteRangeLocator(targetChunk.Locator)
	if !ok || start < 0 || end > len(fileBytes) || start >= end {
		return nil, false
	}
	pos, indent := chunkInsertPoint(fileBytes, start, end, kind)
	return spliceLine(fileBytes, pos, indent, tagText), true
}

// chunkInsertPoint returns the byte offset for a chunk-level tag line and the
// indent to prefix it with. A chunk with a structural opener (markdown
// heading, bracket opening line, indent block header) takes the tag on the
// line right after the opener, so the chunker merges it into the chunk. A
// headingless markdown prose chunk has no opener, so the tag goes at the top
// of the chunk's range. An indent chunk matches the body's indentation so the
// tag stays inside the block's scope rather than re-chunking out of it.
// CRC: crc-Indexer.md | R3097, R3099
func chunkInsertPoint(data []byte, start, end int, kind stencilKind) (pos int, indent string) {
	if kind == stencilMarkdown && !(start < end && start < len(data) && data[start] == '#') {
		return start, "" // headingless prose chunk: no opener → top of the chunk
	}
	nl := indexByteRange(data, start, end, '\n')
	if nl < 0 {
		return end, "" // single-line chunk → after it (end of the range)
	}
	if kind == stencilIndent {
		indent = leadingWhitespace(data, nl+1, end)
	}
	return nl + 1, indent
}

// spliceLine inserts `indent+text+"\n"` at byte offset pos in data, returning
// a fresh slice (data is not mutated).
func spliceLine(data []byte, pos int, indent, text string) []byte {
	line := indent + text + "\n"
	out := make([]byte, 0, len(data)+len(line))
	out = append(out, data[:pos]...)
	out = append(out, line...)
	out = append(out, data[pos:]...)
	return out
}

// indexByteRange returns the offset of the first b in data[start:end], or -1.
func indexByteRange(data []byte, start, end int, b byte) int {
	if end > len(data) {
		end = len(data)
	}
	for i := start; i < end; i++ {
		if data[i] == b {
			return i
		}
	}
	return -1
}

// leadingWhitespace returns the run of spaces/tabs at the start of
// data[from:end] (the block body's indentation for the indent stencil).
func leadingWhitespace(data []byte, from, end int) string {
	if end > len(data) {
		end = len(data)
	}
	i := from
	for i < end && (data[i] == ' ' || data[i] == '\t') {
		i++
	}
	return string(data[from:i])
}
