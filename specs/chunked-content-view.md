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

## Chat-JSONL Role Rendering

JSONL conversation files have a natural structure: alternating
human and assistant turns, each spanning one or more chunks. The
content view should make this structure visible.

### Role in chunk attrs

`extractJSONLTextFast` also extracts the `type` and `isMeta`
fields from the JSONL line and stores a `role` chunk attr:

- `type: "user"`, `isMeta` absent or false → `role=human`
- `type: "user"`, `isMeta: true` → `role=skill`
- `type: "assistant"` → `role=assistant`

The attr rides the existing microfts2 chunk `Attrs` mechanism.

### Skill label

Skill chunks begin with `Base directory for this skill: PATH`.
The extractor parses this line to get the skill path and stores
it as a `skill` attr (the last path component, e.g. `ark` or
`mini-spec`). This becomes the label on the collapsed header.

### Role groups

The server groups consecutive same-role chunks into a wrapper:

```html
<div class="ark-role-group ark-role-human">
  <div class="ark-role-header">👤</div>
  <div class="ark-chunk" data-range="...">...</div>
  <div class="ark-chunk" data-range="...">...</div>
</div>
<div class="ark-role-group ark-role-assistant">
  <div class="ark-role-header">🤖</div>
  <div class="ark-chunk" data-range="...">...</div>
</div>
<div class="ark-role-group ark-role-skill" data-skill="mini-spec">
  <details>
    <summary class="ark-role-header">📋 mini-spec</summary>
    <div class="ark-chunk" data-range="...">...</div>
  </details>
</div>
```

A new group starts when the role changes. Chunks without a role
attr (non-JSONL files, or JSONL lines without a role field) are
not grouped — they render as standalone `ark-chunk` divs.

### Skill collapse

Skill groups use `<details>/<summary>` so they are collapsed by
default. The summary shows the skill icon and name. Click to
expand and read the injected content. This keeps the conversation
flow readable — skills are often long and not part of the dialogue.

### Sticky header

The role header has `position: sticky; top: 0` so the icon stays
pinned at the top of the viewport while scrolling through that
group's chunks. `background: inherit` keeps the header opaque.
The icon sits at the upper right (`text-align: right` or
`float: right`).

### Visual differentiation

Each role group has a left border in a distinct theme color:
- Human: `--term-text-dim` (muted, observer) — 👤
- Assistant: `--term-accent-bright` (active, speaker) — 🤖
- Skill: `--term-border` (background, infrastructure) — 📋

The border runs the full height of the group. Even mid-scroll
with the header off-screen, the border color tells you who's
speaking.

### Single-chunk views (iframe previews)

When serving a single chunk via `range=`, the role attr is still
available. The chunk renders with the role's left border color
and a small icon — but no sticky header and no grouping (the
chunks are non-contiguous snippets from different files, not a
conversation flow).

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
.ark-role-group {
  position: relative;
  border-left: 3px solid transparent;
}
.ark-role-human {
  border-left-color: var(--term-text-dim, #8888a0);
}
.ark-role-assistant {
  border-left-color: var(--term-accent-bright, #ff9966);
}
.ark-role-skill {
  border-left-color: var(--term-border, #2a2a3a);
}
.ark-role-header {
  position: sticky;
  top: 0;
  text-align: right;
  padding: 0.3em 0.75em;
  font-size: 1.2em;
  z-index: 5;
  background: inherit;
}
.ark-role-skill summary {
  cursor: pointer;
  color: var(--term-text-muted, #5a5a70);
  font-size: 0.85em;
  padding: 0.3em 0.75em;
}
```

Added to `content-plain.html` alongside the existing styles.
