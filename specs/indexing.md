# Indexing

## Chunking Strategies

microfts2 uses external commands for chunking: the command receives
a file path and outputs byte offsets to stdout. Strategies are
registered by name in the database.

### Built-in strategies (from microfts2 CLI)

- `lines` — `microfts chunk-lines <file>`: split by line count
- `lines-overlap` — `microfts chunk-lines-overlap -lines 50 <file>`:
  overlapping line-based chunks
- `words-overlap` — `microfts chunk-words-overlap <file>`:
  overlapping word-based chunks

`ark init` registers these by default, plus:

- `chat-jsonl` — content-aware chunker for Claude conversation logs.
  See "chat-jsonl chunker" below.

### chat-jsonl chunker

Indexes Claude Code conversation logs (JSONL, one JSON record per line).

**Emission invariant.** Every non-empty line that isn't explicitly
filtered produces exactly one chunk. The chunker's job is to group
content for indexing and embedding — it must not silently drop
user-searchable content. If a line can't be cleanly extracted (partial
JSON at the tail of a growing file, malformed JSON, recognized JSON
that doesn't match the text extractor), the chunk's content is the
raw line bytes. Searchable beats invisible.

**Explicit filters** — these records are skipped because their content
is already represented elsewhere in the index or is operational
metadata:

- `tool_use` blocks (input contains file contents and code edits
  whose authoritative copies live in the indexed files)
- `tool_result` blocks (command output and file reads, also already
  indexed in their source form or obsolete)
- top-level `planContent` (duplicate of message content)
- record types: `progress`, `file-history-snapshot`,
  `queue-operation`, `system` (operational metadata, not user-meaningful)

**Extraction**, when the line isn't filtered:

- `type:text` blocks: the `text` field; multiple text blocks in one
  message join with newlines.
- `type:thinking` blocks: the `thinking` field (not the `signature`).
- `message.content` when it's a string: the entire string.
- Anything else (parseable JSON that doesn't match the above, partial
  or malformed JSON): the raw line as chunk content.

Chunk range is `N-N` (1-based line number) for traceability. The
chunk's attrs carry `timestamp` and `role` when extractable from the
JSON.

**Append handling.** When a JSONL file grows (the common case during
an active Claude Code session), the chunker handles incremental
indexing through the `AppendAwareChunker` interface. Only new and
modified content is re-chunked — the rest of the file's chunks are
preserved without re-embedding. A partial trailing line that hasn't
yet received its newline is emitted as a chunk for the bytes
available; when a later append completes the line, that chunk is
replaced with a corrected version through the drop-and-replace
semantics that microfts2's append protocol provides.

### Paragraph-based markdown chunker

`markdown` — a func strategy in microfts2 that splits markdown files
on paragraph boundaries. A paragraph boundary is a blank line or a
heading transition (a line starting with one or more `#` characters).

Each chunk is a coherent unit of thought: a paragraph, a heading with
its immediately following paragraph, or a contiguous block of
non-blank lines. Consecutive blank lines collapse to a single boundary.

Chunks use the same line-range format as other strategies (`"5-12"`),
1-based, for consistency with `extractByRange` and `WithBaseLine`.

A heading line starts a new chunk. The heading and the paragraph
immediately following it (up to the next blank line or next heading)
form one chunk together.

Blank lines are boundaries only — not included in any chunk's content.
Gaps between chunks are expected.

Chunk boundaries are not always clean for append detection: the last
paragraph may continue when content is appended. The consumer derives
this by comparing the last chunk's end position to the file length —
the chunker doesn't report it. Until back-seek (O12) is implemented,
append detection falls back to full reindex for markdown-strategy files.

Registered as a func strategy via `AddStrategyFunc` in microfts2,
alongside `LineChunkFunc`. Ark registers it in both `InitDB` and
`Open`, and adds it to the global strategy map in ark.toml
(`"*.md" = "markdown"` replaces `"*.md" = "lines"`).

### Future strategies (ark subcommands or external)

- `code` — keep functions/methods with their doc comments intact
  (tree-sitter or per-language heuristics)
- `manual` — read a sidecar offset file written by a human or agent
  for complex cases where automated chunking fails

Custom strategies can be registered via microfts2's AddStrategy API
pointing at any command that follows the offsets-to-stdout protocol.

## Add Files

Add files to the index:
- Walk source directories per config
- For each file matching include/exclude patterns:
  - Check staleness via microfts2 (skip if fresh)
  - Add to microfts2 synchronously (gets fileid, chunk offsets)
  - Embed the new chunks via the Librarian/EC pipeline (background;
    EC records keyed by chunkid)

FTS is live the moment a file is added. Embedding happens
asynchronously — a background goroutine works through the queue.
This means scan/add never blocks on embedding, which can be very
slow on resource-constrained hardware.

microfts2 is the source of truth for file identity; chunk embeddings
(EC records) are keyed by chunkid (R1914).

### Background embedding

- Files are queued for embedding after FTS indexing completes
- A single background goroutine processes the queue (one file at a
  time to avoid overwhelming the CPU)
- Status reports progress: "vector: 14/17 files embedded"
- The queue survives server restart (persisted in ark subdatabase)
- If no embed-cmd is configured, the queue is simply not processed
  (FTS-only mode)

## Remove Files

Remove a file by path. microfts2 resolves the path to a fileid and
drops its chunks; each chunk's EC embedding record is reclaimed via
microfts2's orphan callbacks (chunkid-keyed, R1914).

## Refresh

Re-index stale files. Uses microfts2's staleness detection (modtime +
content hash). For each stale file:
- Re-add to microfts2 (gets new chunk offsets)
- Re-embed the file's chunks via the Librarian/EC pipeline (EC records,
  chunkid-keyed)

Missing files are not auto-deleted. They're added to ark's missing
files list for review. The user or agent decides what to do.
