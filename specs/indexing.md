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

Each line is read through the transcript reader
([transcript-reader.md](transcript-reader.md)), the one part of ark that decodes
Claude Code's JSON.

**What is indexed.** One chunk per user or assistant turn that has text. The
chunk's content is the turn's text, thinking and narration blocks, one per line;
a string `content` is one text block. Thinking is the `thinking` field, never its
`signature`.

**What is not.**

- `tool_use` blocks (input contains file contents and code edits whose
  authoritative copies live in the indexed files) and `tool_result` blocks
  (command output and file reads, also indexed in their source form or
  obsolete).
- A turn with no text.
- Every record type the reader does not use: attachments, API request copies,
  progress, file-history snapshots, queue operations, system records and other
  bookkeeping. They are harness context rather than conversation. Until
  2026-10-22 attachments and API request copies were indexed by accident,
  because the old scanner took the first `content` key anywhere in a line; they
  were 24,416 of 146,991 chat chunks, mostly repeated system reminders and skill
  listings competing with real conversation in every search. Dropping them was
  Bill's decision, 2026-10-22.

**A line still being written.** A partial trailing line that has not yet
received its newline is not yet a record. It is indexed as its raw bytes, so the
content is searchable while the turn is in progress, and replaced when the line
completes (see Append handling). This is the only case of raw bytes in a chunk.

Chunk range is `N-N` (1-based line number) for traceability. A chunk's attrs
carry `timestamp`, and `role`: `assistant` for an assistant turn; `skill` for a
skill injection or an agent report; `human` for any other user turn, including a
person's mid-turn message and a task notification.

**Append handling.** When a JSONL file grows (the common case during
an active Claude Code session), the chunker handles incremental
indexing through the `AppendAwareChunker` interface. Only new and
modified content is re-chunked — the rest of the file's chunks are
preserved without re-embedding. A partial trailing line that hasn't
yet received its newline is emitted as a chunk for the bytes
available; when a later append completes the line, that chunk is
replaced with a corrected version through the drop-and-replace
semantics that microfts2's append protocol provides.

**Chunker version.** The chunker declares a version through microfts2's
optional `VersionedChunker` interface, and microfts2 records in each file's
record the version that file was chunked under. Bump the version whenever what
the chunker indexes changes: every chat file chunked under another version then
reads stale and the ordinary refresh re-chunks it (see Refresh). It is at
**version 1** (Bill, 2026-10-25), the first stamp after the 2026-10-22 rules
above; files chunked before the stamp existed read as version 0.

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
- The queue survives server restart (persisted in the ark bucket)
- If no embed-cmd is configured, the queue is simply not processed
  (FTS-only mode)

## Remove Files

Remove a file by path. microfts2 resolves the path to a fileid and
drops its chunks; each chunk's EC embedding record is reclaimed via
microfts2's orphan callbacks (chunkid-keyed, R1914).

## Refresh

Re-index stale files. Uses microfts2's staleness detection: the file's
chunker version (a file chunked under a different version than its chunker
declares is stale whatever its bytes), then modtime + content hash. For each
stale file:
- Re-add to microfts2 (gets new chunk offsets)
- Re-embed the file's chunks via the Librarian/EC pipeline (EC records,
  chunkid-keyed)

**A version-stale file is re-chunked even though its bytes are unchanged.**
The byte-identical shortcut, which skips re-chunking a file whose length and
content hash match its record, exists to absorb redundant change events. It
never applies to a file microfts2 has judged stale: such a file can only be
stale by its chunker version, and skipping it would leave it stale forever.
The file scan has the same rule: a scan of a file microfts2 judged stale whose
bytes still match its record runs as a forced scan, so neither append detection
nor the store-time unchanged escape can pass it by.

**An append onto a version-stale file re-chunks the whole file instead.**
microfts2 refuses an append when the file was chunked under another version,
since the existing chunks follow other rules. ark answers with a full reindex
of that file. Where a store failure normally re-emits one plain change event,
this refusal re-emits a *forced* scan, which never appends, so the retry
cannot meet the same refusal.

Missing files are not auto-deleted. They're added to ark's missing
files list for review. The user or agent decides what to do.
