# ark

Orchestration layer over microfts2 (trigram) and microvec (vector).
Digital zettelkasten with hybrid search. Go CLI and server.

## Language and Environment

- Go
- microfts2 and microvec as library dependencies
- LMDB via microfts2's environment (shared)
- Unix domain socket for server communication

## Shared LMDB Environment

Ark opens microfts2 first (which creates the LMDB environment), then
passes the env to microvec. Ark also opens its own named subdatabase
for metadata (missing files list, etc.). MaxDBs set to 8 to leave
room: microfts2 uses 2, microvec uses 1, ark uses 1+.

## Source Configuration

A config file has three levels:

1. **Directories** — which directories to watch. The top-level
   selection. Ark walks these.
2. **Include patterns** — within those directories, which files to
   index (globs). A file must match at least one include pattern.
3. **Exclude patterns** — default policy for what to skip.

When both an include and exclude pattern match the same file,
include wins. Exclude is the broad policy, include is the
override. No specificity ranking — if anything includes it,
it's in.

Config validation: if an include and exclude pattern are
identical strings, that's an error. Ark reports it on every
operation until the user resolves the contradiction.

Global include/exclude patterns apply to all directories. Per-directory
overrides are optional.

Each directory entry also specifies a chunking strategy name (which
microfts2 chunker to use).

**No rule means no action.** Files that don't match any include or
exclude pattern are not indexed and not ignored — they're held in an
"unresolved" list. The user must provide a rule or explicit decision.
No automagic indexing of unknown files. This prevents the
virus-scanner / git-auto-add problem: nothing enters the index
without an explicit rule or action.

Pattern language — four forms with distinct meanings:
- `name` — matches a file named "name" (not a directory)
- `name/` — matches a directory named "name" (not its contents;
  for exclude this means "don't walk into it")
- `name/*` — matches immediate children of "name"
- `name/**` — matches any descendant of "name" at any depth

Wildcards: `*` matches within a path component (including dotfiles
by default — unlike shell globs), `**` matches any number of path
components. Standard glob wildcards (`?`, `[abc]`) also supported.
A `dotfiles` config preference controls this — default true (match
dotfiles), set to false for standard shell glob behavior.
Backslash escapes literal wildcard characters (`\*`, `\?`, `\[`)
for filenames that contain them.
`ark init` ships with default excludes for `.git/`, `.env`, etc.

Patterns without a leading `/` match at any depth within the
watched directory. Patterns with a leading `/` are anchored to the
watched directory root.

Examples (given `dir: ~/work/myproject`):
- `exclude: node_modules/` — skips node_modules dirs anywhere
- `exclude: /vendor/` — skips vendor/ only at project root
- `include: *.md` — matches markdown files at any depth
- `include: /src/**` — matches everything under src/ at root

### Config file: `ark.toml`

TOML format. Lives in the database directory.

```toml
# Global settings
dotfiles = true  # whether * matches dotfiles (default true)

# Global patterns — apply to all sources
include = ["*.md", "*.txt", "*.org"]
exclude = [".git/", ".env", "node_modules/"]

# Sources — directories to watch
[[source]]
dir = "~/work/myproject"
strategy = "markdown"

[[source]]
dir = "~/notes"
strategy = "markdown"
include = ["*.md"]          # per-source override (adds to global)
exclude = ["drafts/"]       # per-source override (adds to global)

[[source]]
dir = "~/work/reference"
strategy = "plain"
```

Per-source `include` and `exclude` are additive — they combine with
the global patterns, not replace them. A file must match at least one
include pattern (global or per-source) to be indexed. The
include-wins-conflicts rule applies to the combined set.

@manifest files in source directories are a future enhancement. V1
uses the central config.

## Database Directory

Ark stores everything in one directory:
- LMDB environment (data.mdb, lock.mdb)
- `ark.toml` config file
- Unix domain socket (while server is running)

Default: `~/.ark/` or specified via `-db` flag.

## Init

Create a new ark database:
- Initialize microfts2 (character set, case insensitivity, aliases)
- Initialize microvec (embedding command)
- Create ark's own subdatabase
- Write default config

The character set, embedding command, and aliases are immutable after
creation (inherited from microfts2 and microvec constraints).

Newline alias: microfts2 character alias maps `\n` to `\x01` (SOH).
Never appears in real text content. Enables `^` line-start matching
on frontmatter, tags, etc. The agent inserts `\x01` in queries
where line-start anchoring is needed.

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

`ark init` registers these three by default.

### Future strategies (ark subcommands or external)

- `jsonl` — one JSONL record per chunk (split on newlines)
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
  - Add to microfts2 (gets fileid, chunk offsets)
  - Read chunk text from the file using offsets
  - Add to microvec (fileid + chunk texts)

Both engines indexed in one pass per file. microfts2 is the source of
truth for file identity — microvec receives fileids from it.

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

## Search

### Combined search

Both engines query the same text. Results merged and re-ranked.

- microfts2 returns file/chunk matches with trigram scores
- microvec returns file/chunk matches with cosine similarity scores
- Ark merges by (fileid, chunknum), combining scores
- Results sorted by combined score descending
- Output: filepath:startline-endline with score

### Split search

Targeted queries for one or both engines:

- `--about <text>` goes to microvec (semantic)
- `--contains <text>` goes to microfts2 (exact)
- `--regex <pattern>` goes to microfts2 (regex)
- `--contains` and `--regex` are mutually exclusive — error if both
- Either flag works alone (single-engine search, no intersection)
- Both `--about` + one of `--contains`/`--regex` — results intersected
  by (fileid, chunknum)
- Output: same format as combined

### Common search options

- `-k <num>` — max results (default 20)
- `--scores` — show scores in output
- `--after <date>` — only results newer than date (time filtering)

## Server

`ark serve` starts a long-running server:
- Binds a Unix domain socket (path in database directory)
- Writes a PID file outside the database (for emergency kill only)
- Opens the database with exclusive lock
- Loads the embedding model (keeps warm for fast queries)
- Accepts HTTP requests over the socket
- Same operations as CLI: search, add, remove, refresh, scan, status

Startup reconciliation sequence (closes the gap between last
shutdown and now):
1. Start fsnotify watches (future — so nothing changes unseen)
2. Scan — walk configured directories, find new files to index,
   flag new unresolved files
3. Refresh — check indexed files for staleness and missing

Watch first, then reconcile. V1 without fsnotify runs steps 2-3
on startup by default. `--no-scan` to skip.

Highlander rule: there can be only one server per database. The
socket bind is the real lock — if another server holds it, bind
fails and the new server exits with an error. On crash/kill -9, the
stale socket remains but bind will fail for new connections. The CLI
detects this (connect fails), unlinks the stale socket, and proceeds
with cold-start.

The PID file is an ops convenience for manual shutdown, not a
correctness mechanism. Location TBD (e.g. `~/.ark/ark.pid`).

The CLI tries to connect to the socket. If it connects — proxy to
server. If not — clean up any stale socket and run the operation
directly (cold start for embedding).

## CLI

All commands take `-db <path>` (default `~/.ark/`).

- `ark init -db <path> -embed-cmd <command> [-query-cmd <command>] [-charset <chars>] [-case-insensitive] [-aliases <from=to,...>]`
  Create a new database.
- `ark add -db <path> [-strategy <name>] <file-or-dir>...`
  Add files. If directory, walk per config. If file, add directly.
- `ark remove -db <path> <file-or-pattern>...`
  Remove files from both engines. Accepts paths or glob patterns.
- `ark scan -db <path>`
  Walk configured directories. Index new files matching include
  rules, flag new unresolved files. Does not re-check existing files.
- `ark refresh -db <path> [<pattern>...]`
  Re-index stale files. If patterns given, only refresh matching
  files. No patterns = all stale files. Report missing files.
- `ark search -db <path> [-k <num>] [--scores] [--after <date>] <query>...`
  Combined search.
- `ark search -db <path> --about <text> --contains <text> [--regex] [-k <num>] [--scores]`
  Split search.
- `ark serve -db <path>`
  Start the server.
- `ark status -db <path>`
  Show counts: files, stale, missing, unresolved. Index built.
  Server running or not.
- `ark files -db <path>`
  List all indexed files.
- `ark stale -db <path>`
  List files that need re-indexing.
- `ark missing -db <path>`
  List files that are no longer at their indexed path.
- `ark dismiss -db <path> <file-or-pattern>...`
  Remove missing files from the missing list (and from both engines).
  Accepts paths or glob patterns.
- `ark config -db <path>`
  Show current source configuration.
- `ark unresolved -db <path>`
  List files that don't match any include or exclude pattern.
- `ark resolve -db <path> <pattern>...`
  Dismiss unresolved files matching the given patterns (remove from
  unresolved list without adding a permanent rule). Patterns use the
  same glob syntax as include/exclude. Backslash-escape literal
  wildcard characters in filenames (`file\*name`).

## HTTP API (over Unix domain socket)

Mirrors the CLI. JSON request/response.

- `POST /search` — combined or split search
- `POST /add` — add files
- `POST /remove` — remove files
- `POST /scan` — walk directories, index new files
- `POST /refresh` — refresh stale files
- `GET /status` — database status (counts)
- `GET /files` — list indexed files
- `GET /stale` — list stale files
- `GET /missing` — missing files list
- `POST /dismiss` — dismiss missing files
- `GET /config` — current source configuration
- `GET /unresolved` — unresolved files list
- `POST /resolve` — dismiss unresolved files by pattern

## Ark Subdatabase

LMDB subdatabase `ark` stores:
- `M` [fileid: 8] -> JSON — missing file record
  - path: string — last known path
  - lastSeen: number — timestamp when last indexed
- `U` [path bytes] -> JSON — unresolved file record
  - path: string — full path of the file
  - firstSeen: number — timestamp when first noticed
  - dir: string — which watched directory it was found in
  Files that don't match any include or exclude pattern. Persisted
  so the list survives across scans. Cleared when the user adds a
  rule that covers them or explicitly dismisses them. During scans,
  unresolved files that no longer exist on disk are removed silently
  (no need to track missing files that were never indexed).
- `I` -> JSON — ark-level settings
  - sourceConfig: embedded or path reference to config
  - dotfiles: boolean — whether * matches dotfiles (default true)

## V2 — Model-Free

No embedding model required. Tags + FTS give fully functional
recall on any hardware.

- Chunk retrieval — CLI option to return chunk text instead of ranges
  - `ark search --chunks` — emit chunk text (JSONL)
  - `ark search --files` — emit full file content for matches
  - Enables permission end-run: if it's indexed, ark can emit it
  - Works with FTS and tag search, no model needed
- Tag tracking — track @tags as files are ingested
  - 'T' [tagname] [count] in ark subdatabase
  - 'F' [filename] [tag] [count]
  - helps keep ~/work/daneel/tags.md up-to-date
  - maybe move some files from ~/work/daneel into ~/.ark to generalize
  - subcommands:
    - ark tag counts <tag>...
    - ark tag files <tag>...
      - outputs filename size
      - option to output with tag to end of the line for each occurrence
- Recall agent / /ark skill
- CLAUDE.md bootstrap — run ark at session start to seed context
  - use ark instead of local files when possible
  - tags + FTS queries, no model needed
- Export microfts2 chunker functions (eliminate exec-per-file)
  - AddStrategyFunc alongside AddStrategy
  - Built-in chunkers call Go functions directly

## V3 — With-Model

Add vector search for users with the hardware. Batteries included.

- In-process embedding via gollama
  - Load nomic-embed-text at server startup, hold in memory
  - EmbedFunc in microvec alongside EmbedCmd (no exec)
  - Cold-start CLI loads model on demand (slower, acceptable)
- Model distribution — fetch from HuggingFace on `ark init`
  - nomic-embed-text-v1.5.Q8_0.gguf (~140MB, one-time download)
  - Store in ~/.ark/models/
  - Model-free operation remains fully supported without download
- Orchestrator architecture
  - One Daneel session, launches subagents for heavy work
  - Subagents write back to ark (notes, decisions, tags)
  - Orchestrator queries ark for recall, stays lean
  - Does not need to compact nearly as often
- @manifest files for per-directory indexing rules
  - whenever ingesting a file, check it for @manifest entries
  - track files included specifically from @manifest entries
    because the entries "manage" them
  - maybe a record: 'M' [manifest] [dependent]
  - need format for @manifest — a markdown list might work well here

## V4 — Proactive

Ark surfaces things to you without being asked. Requires model
warm (server mode).

- Time-decay scoring (we store timestamps, scoring comes later)
- Conversation JSONL chunker
- Reminder / proactive memory system
  - ark watches ~/.claude/projects for changes
  - claude sessions subscribe for changes (HTTP, cookie for session
    id, grace period TTL)
  - send accumulated lines in response (one JSON per line, added
    dynamically during chat)
  - track file position for next connect
  - claude uses CLI to connect, which connects via HTTP
- Inspiration mode (random tag from vector results)
  - reminder system (above) chooses one from top N matches that
    contain tags

## Future
- LLM-driven tag extraction
- Secondary "arkive" database for cold storage
- Frictionless management app
