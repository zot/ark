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

- `chat-jsonl` — `ark chunk-chat-jsonl <file>`: content-aware chunker
  for Claude conversation logs. Extracts text and thinking blocks,
  skips tool use/results and metadata.

### Future strategies (ark subcommands or external)

- `markdown` — split on heading boundaries (## level)
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
  - Queue for microvec embedding (background)

FTS is live the moment a file is added. Embedding happens
asynchronously — a background goroutine works through the queue.
This means scan/add never blocks on embedding, which can be very
slow on resource-constrained hardware.

microfts2 is the source of truth for file identity — microvec
receives fileids from it.

### Background embedding

- Files are queued for embedding after FTS indexing completes
- A single background goroutine processes the queue (one file at a
  time to avoid overwhelming the CPU)
- Status reports progress: "vector: 14/17 files embedded"
- The queue survives server restart (persisted in ark subdatabase)
- If no embed-cmd is configured, the queue is simply not processed
  (FTS-only mode)

## Remove Files

Remove a file from both engines by path. microfts2 resolves the path
to a fileid, microvec removes by fileid.

## Refresh

Re-index stale files. Uses microfts2's staleness detection (modtime +
content hash). For each stale file:
- Re-add to microfts2 (gets new chunk offsets)
- Remove old vectors from microvec
- Add new vectors to microvec

Missing files are not auto-deleted. They're added to ark's missing
files list for review. The user or agent decides what to do.
