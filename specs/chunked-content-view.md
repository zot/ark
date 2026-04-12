# Chunked Content View

The `/content/` route for non-markdown files shows chunk-extracted
text instead of raw file content. Each chunk renders as a separate
`<div>`, giving clean text with correct tag boundaries. `/raw/`
continues to serve the unprocessed file.
Language: Go (server.go). HTML templates unchanged.

## Problem

Non-markdown files (JSONL, code) are currently displayed as raw text
in a `<pre>` block. For JSONL files, this means the JSON envelope
is visible and tags inside quoted strings get `<ark-tag>` decoration
even though they're mentions, not uses. The chunker already solves
this — it extracts clean conversational text from JSONL and
meaningful blocks from code files. But `/content/` doesn't use it.

## What Changes

When serving a non-markdown file via `/content/` (the `else` branch
in `handleContentView`), the server reads the file's chunks from the
index instead of dumping raw content. Each chunk's extracted text is
rendered as a `<div>` with a separator between chunks. The `<pre>`
block is replaced by a sequence of chunk divs.

For files with no chunks in the index (unindexed or newly added),
fall back to the current raw `<pre>` rendering.

### Chunk rendering

Each chunk becomes:

```html
<div class="ark-chunk" data-range="START-END">CHUNK_TEXT</div>
```

For JSONL files (strategy `chat-jsonl`), chunk text is rendered
through goldmark — the extracted content is markdown written by
humans and AI assistants. This gives proper headings, code blocks,
lists, and inline formatting instead of raw markdown syntax.

For other file types, chunk text is HTML-escaped (pre-wrapped text).

In both cases, `wrapTagElements` runs on each chunk's rendered
output independently. Tags in the extracted text are real tags
(the chunker already applied use/mention filtering at index time
for JSONL). A subtle visual separator (border or margin) between
chunks shows the boundaries without being intrusive.

### What stays the same

- Markdown files continue through the goldmark path — no change
- The `range=` query parameter for single-chunk views continues
  to work (iframe previews use this)
- `/raw/` serves the unprocessed file — unchanged
- `wrapTagElements` and `<ark-tag>` widgets work exactly as before,
  just on cleaner input text
- The `autoEdit` / CM6 editor path is unaffected — it fetches via
  `/fetch` which returns raw content

### How to get the chunks

The server creates a `ChunkCache` from the DB's FTS, calls
`GetChunks` with a target range and large window to retrieve all
chunks for the file. The chunk cache handles reading the file and
running the appropriate chunker (determined by the file's strategy).

If the file has no chunks (ChunkCache returns empty), fall back to
the raw `<pre>` rendering.

## CSS

```css
.ark-chunk {
  white-space: pre-wrap;
  word-wrap: break-word;
  padding: 0.5em 1em;
  border-bottom: 1px solid var(--term-border, #2a2a3a);
}
.ark-chunk:last-child {
  border-bottom: none;
}
```

Added to `content-plain.html` alongside the existing styles.
