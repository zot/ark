# Chunkers (summary spec)

Language: Go. Environment: CLI + server (part of the `ark` binary).

**This is a summary spec.** It indexes the chunkers ark registers and the
microfts2 chunker interfaces they implement, along one cross-cutting axis —
*which chunker implements which interface, and where its content comes
from*. It introduces no behavior of its own. The per-feature specs named
below own the contracts; when they disagree with this table, they win and
this table is updated to match.

## What a chunker is

A chunker turns a file (or supplied content) into a sequence of
`microfts2.Chunk{Range, Locator, Content, Attrs}`. microfts2 trigram-indexes
each chunk's `Content`; ark additionally embeds it (EC records) and extracts
ark tags from it. A chunker may implement several optional microfts2
interfaces; microfts2 type-asserts for them at index and retrieval time.

### microfts2 chunker interfaces

- **`Chunker`** — `Chunks(path, content, yield)`. The base: chunk supplied content.
- **`AppendAwareChunker`** — `AppendChunks(path, lastLocator, newBytes, yield)`.
  Incremental append for growing files (chat logs).
- **`RandomAccessChunker`** — `GetChunk(path, data, customData, chunk)`. Fast
  single-chunk retrieval; **re-derives `Content` from the file bytes**.
  `ChunkCache.ChunkText` prefers this path — see chunk-cache-threading.md.
- **`FileChunker`** — `FileChunks(path, oldHash, yield)`. Chunker reads the
  file itself (computes hash, may skip if unchanged).
- **`ChunkerMetadata`** — `IsWritable()`, `CommentSyntax()`. Editability +
  line-comment delimiter; absent ⇒ treated as writable, no comment syntax.

## Interface matrix

| Chunker          | reg. name      | Chunks | AppendChunks | GetChunk | FileChunks | IsWritable | owner spec |
|------------------|----------------|:------:|:------------:|:--------:|:----------:|:----------:|------------|
| MarkdownChunker  | `markdown`     |   ✓    |      ✓       |    ✓     |     ✓      | true       | chunker-strategies.md, indexing.md |
| LineChunker      | (full line)    |   ✓    |      ✓       |    ✓     |     ✓      | true       | chunker-strategies.md |
| bracketChunker   | `bracket-*`    |   ✓    |      ✓       |    ✓     |     ✓      | true       | chunker-strategies.md |
| indentChunker    | `indent-*`     |   ✓    |      ✓       |    ✓     |     ✓      | true       | chunker-strategies.md |
| FuncChunker      | `lines`        |   ✓    |      ✗       |    ✗     |     ✗      | (def true) | chunker-strategies.md |
| JSONLChunker     | `chat-jsonl`   |   ✓    |      ✓       |    ✗     |     ✗      | (def true) | chat-transcript.md |
| PDFChunker       | `pdf`          |   ✓    |      ✗       |    ✓     |     ✓      | **false**  | pdf-chunker.md |

Notes:
- The four microfts2 *text* chunkers (Markdown, Line, bracket, indent) are
  **uniform** — identical full interface set.
- `FileChunks` does **not** distinguish text from binary — text chunkers
  implement it too. The only reliable PDF discriminator is
  `IsWritable() == false`.
- `lines` is registered via `AddStrategyFunc` ⇒ a `FuncChunker` (Chunks
  only), not the full `LineChunker`.

## Registration paths

- **`Init` → `fts.AddChunker`** (db.go) — used by CLI create and test code;
  creates the DB, closes it, returns. **No runtime indexing happens here.**
- **`db.Open` → `db.addChunker`** (db.go) — the live path for both CLI and
  `ark serve`. `db.addChunker` is a thin wrapper over `fts.AddChunker` that
  also mirrors the chunker into `chunkerByName` for metadata lookup. New
  per-chunker behavior (e.g. the tag-strip transform below) hooks in here.

## Tag-strip transform (pending — approach B)

Ark annotates content with inline `@tag: value` lines. Those tags are
indexed and embedded separately (V/EV/ED records), so ark wants them
**stripped from chunk content** before trigram-indexing and EC embedding —
consistently across *every* content-producing path (Chunks, AppendChunks,
FileChunks, GetChunk), so content-as-indexed equals content-as-retrieved.

Chosen approach: a **per-chunker content-transform hook on
`fts.AddChunker`** in microfts2 (signature `func(c *Chunk)` — strip
`Content`, append derived tag `Attrs`). Requested from microfts2 in
`requests/microfts2-content-transform-hook.md`. Ark's strip itself
(`stripArkTags`, indexer.go) is implemented and unit-tested, awaiting the
hook to register against.

- **Owner of the contract:** recall-substrate-v3 (migration, R2904).
- PDF gets no transform (its extracted text carries no ark tags).
- Rationale and the full interface analysis: `.scratch/CHUNKER.md`.

## Related specs

- chunker-strategies.md — config-driven bracket/indent registration.
- pdf-chunker.md — PDF → Block → Chunk mapping.
- chunk-callback.md — the WithChunkCallback / WithIndexedChunkCallback path.
- chunk-cache-threading.md — `ChunkCache` retrieval (the GetChunk fast path).
- indexing.md — how chunks flow into the index.
