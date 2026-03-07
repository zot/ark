# ark

Your one digital zettelkasten. One database, one server, one index
for everything — notes, code, conversations, decisions. Orchestration
layer over microfts2 (trigram) and microvec (vector). Go CLI and server.

## Principles

- **You own the data.** Your files stay where they are — ark never
  moves, copies, or ingests them into an opaque store. The index is
  derived. Delete it, rebuild from your files. No lock-in, no cloud
  dependency, no single point of corruption.
- **Long-term memory for your AI assistant.** Ark is how your
  assistant remembers across sessions, projects, and time.
- **Know everything about all your stuff.** Index your notes, code,
  conversations, decisions. One place to search all of it.
- **Runs anywhere.** Full functionality on any hardware. Vector
  search enhances but is never required.

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
case_insensitive = true

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

Default: `~/.ark/` or specified via `--dir` flag.

### PATH Injection

On init and open, ark inserts the database directory into `PATH`
just before `/usr/bin` (or `/usr/local/bin`). This ensures that
companion binaries in the ark directory (`microfts`, `microvec`,
chunking commands) are found before system commands with the same
name — notably `ark` itself, which is an archive manager on some
Linux distributions (e.g. Steam Deck). User-installed paths
(`~/.local/bin`, etc.) still take priority since they appear
earlier in PATH.

## Init

Create a new ark database:
- Initialize microfts2 (case insensitivity, byte aliases)
- Initialize microvec (embedding command — optional, can be added later)
- Create ark's own subdatabase
- Write default config

microfts2 uses raw byte trigrams — no character set configuration
needed. All non-whitespace bytes are indexed. Case insensitivity and
aliases are creation-time settings (inherited from microfts2
constraints). The embedding command can be configured after creation
to enable vector search on an existing FTS-only database.

### Init Seeding from ark.toml

If `ark.toml` already exists in the database directory, `ark init`
reads `case_insensitive` and `aliases` from it instead of requiring
flags. This enables a clean "delete DB, re-init, scan" workflow —
the config file preserves the settings.

If `ark.toml` does not exist, `ark init` writes one with
case_insensitive so it is complete for next time.

### Newline Alias

microfts2 byte alias maps `\n` to `\x01` (SOH). Never appears
in real text content. Enables line-start matching on frontmatter,
tags, etc. The agent inserts `\x01` in queries where line-start
anchoring is needed.

## Chunking Strategies

Ark registers chunking strategies with microfts2. Strategies are
either Go functions (registered on every Init and Open) or external
commands (persisted in LMDB settings).

Built-in func strategies:
- `lines` — one chunk per line (microfts2's LineChunkFunc)
- `jsonl` — content-aware JSONL chunker for Claude conversation logs

The `jsonl` strategy parses each line as JSON and extracts only
human-readable text content. Claude conversation logs contain
metadata envelopes, tool inputs (file contents, code edits),
tool results, cryptographic signatures, duplicate plan content,
and file snapshots — typically 97%+ of the file is not useful
for search. The chunker extracts:
- `type:text` blocks from `message.content` (user and assistant text)
- `type:thinking` blocks (the `thinking` field, not the `signature`)

It skips everything else:
- `tool_use` blocks (input contains file contents, code edits — machine payload)
- `tool_result` blocks (command output, file reads)
- `planContent` top-level field (duplicate of content already in message)
- `progress`, `file-history-snapshot`, `queue-operation`, `system` record types
- `signature` fields on thinking blocks (cryptographic, not searchable)
- All envelope metadata (`uuid`, `sessionId`, `cwd`, `usage`, etc.)

Each JSONL line produces at most one chunk. The range is `N-N`
(1-based line number) for traceability. Lines with no extractable
text produce no chunk. The chunk content is the concatenation of
all extracted text blocks from that line, separated by newlines.

This is a Go func strategy, not an external command — it avoids
the scanner buffer limit that external chunkers hit on large
JSONL lines, and eliminates the exec-per-file overhead.

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
on startup by default. `--no-scan` to skip. Reconciliation runs
in a background goroutine so the server accepts requests
immediately — status queries work during scan.

Highlander rule: there can be only one server per database. The
socket bind is the real lock — if another server holds it, bind
fails and the new server exits with an error. On crash/kill -9, the
stale socket remains but bind will fail for new connections. The CLI
detects this (connect fails), unlinks the stale socket, and proceeds
with cold-start.

The PID file is an ops convenience for manual shutdown, not a
correctness mechanism. Location TBD (e.g. `~/.ark/ark.pid`).

### Server Lifecycle

`ark serve` exits 0 if a server is already running (intent: "make
sure it's up" — already up means success). Message on stderr for
humans.

`ark stop` reads the PID file, verifies the process exists and is
ark (handles PID rollover), sends SIGTERM, and polls until the
process exits. Returns 1 if the process doesn't die within timeout.
`-f` flag sends SIGKILL instead.

Server signal handling: catch SIGTERM, close socket, close DB, exit
0. The server process never removes the PID file — stale PID files
are safe because `ark stop` verifies before killing.

The current `defer os.Remove(pidPath)` in server.go should be
removed.

The CLI tries to connect to the socket. If it connects — proxy to
server. If not — clean up any stale socket and run the operation
directly (cold start for embedding).

## CLI

All commands accept `[--dir <path>]` (default `~/.ark/`). This is a global
flag parsed before the subcommand — it can appear anywhere in the
argument list.

- `ark init [--dir <path>] [-embed-cmd <command>] [-query-cmd <command>] [-case-insensitive] [-aliases <from=to,...>]`
  Create a new database. Without `-embed-cmd`, creates an FTS-only
  database (vector search disabled). Embed command can be added later
  via `ark config set-embed-cmd`.
- `ark add [--dir <path>] [-strategy <name>] <file-or-dir>...`
  Add files. If directory, walk per config. If file, add directly.
- `ark remove [--dir <path>] <file-or-pattern>...`
  Remove files from both engines. Accepts paths or glob patterns.
- `ark scan [--dir <path>]`
  Walk configured directories. Index new files matching include
  rules, flag new unresolved files. Does not re-check existing files.
- `ark refresh [--dir <path>] [<pattern>...]`
  Re-index stale files. If patterns given, only refresh matching
  files. No patterns = all stale files. Report missing files.
- `ark search [--dir <path>] [-k <num>] [--scores] [--after <date>] [--chunks] [--files] [--filter <query>...] [--except <query>...] [--filter-files <pat>...] [--exclude-files <pat>...] <query>...`
  Combined search. `--chunks` emits chunk text as JSONL. `--files`
  emits full file content as JSONL. Filter flags scope the search
  (see specs/search-filtering.md).
- `ark search [--dir <path>] --about <text> --contains <text> [--regex] [-k <num>] [--scores] [--chunks] [--files] [--filter <query>...] [--except <query>...] [--filter-files <pat>...] [--exclude-files <pat>...]`
  Split search.
- `ark serve [--dir <path>]`
  Start the server.
- `ark status [--dir <path>]`
  Show counts: files, stale, missing, unresolved. Index built.
  Server running or not.
- `ark files [--dir <path>] [--status] [<pattern>...]`
  List indexed files. If patterns given, only list matching files.
  `--status` prefixes each line with a status letter:
  `G` (good — not missing, not stale), `S` (stale), `M` (missing).
- `ark stale [--dir <path>] [<pattern>...]`
  List files that need re-indexing. If patterns given, only list matching.
- `ark missing [--dir <path>] [<pattern>...]`
  List files no longer at their indexed path. If patterns given, only list matching.
- `ark dismiss [--dir <path>] <file-or-pattern>...`
  Remove missing files from the missing list (and from both engines).
  Accepts paths or glob patterns.
- `ark config [--dir <path>]`
  Show current source configuration.
- `ark config add-source [--dir <path>] <dir> --strategy <name>`
  Add a source directory to ark.toml.
- `ark config remove-source [--dir <path>] <dir>`
  Remove a source directory from ark.toml. Does not remove indexed
  files — they become orphans until dismissed or re-added.
- `ark config add-include [--dir <path>] <pattern> [--source <dir>]`
  Add an include pattern. If --source given, adds per-source; otherwise global.
- `ark config add-exclude [--dir <path>] <pattern> [--source <dir>]`
  Add an exclude pattern. If --source given, adds per-source; otherwise global.
- `ark config remove-pattern [--dir <path>] <pattern> [--source <dir>]`
  Remove a pattern from include or exclude lists. If --source given,
  removes from per-source; otherwise global.
- `ark config set-embed-cmd [--dir <path>] <command>`
  Set or change the embedding command. Enables vector search on an
  FTS-only database. Existing files will need `ark refresh` to
  generate embeddings.
- `ark config show-why [--dir <path>] <file-path>`
  Explain why a file is included, excluded, or unresolved. Shows the
  matching pattern(s), their source (global, per-source, .gitignore,
  .arkignore), and whether include-wins-conflicts applied.
- `ark fetch [--dir <path>] <file-path>`
  Return the full contents of any indexed file. The file must be in
  the index (known to microfts2). Adding a file to ark implies
  read-approval — using fetch side-steps other permission gates.
  Agents with access to the ark binary can view any indexed file
  without needing separate file-read permissions.
- `ark unresolved [--dir <path>]`
  List files that don't match any include or exclude pattern.
- `ark resolve [--dir <path>] <pattern>...`
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
- `POST /fetch` — return full file content for an indexed file path

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
- `T` [tagname] -> count — tag vocabulary with global counts
- `F` [fileid: 8] [tagname] -> count — per-file tag occurrences
- `I` -> JSON — ark-level settings
  - sourceConfig: embedded or path reference to config
  - dotfiles: boolean — whether * matches dotfiles (default true)

## Install

`ark install` bootstraps ark into a project directory. It is designed
to be run by Claude following the ark README instructions.

### Prerequisites

The ark README instructs Claude to first install Frictionless
(github/zot/frictionless) using its own README. Frictionless creates
the `.ui/` directory structure. Ark's install checks for this.

### Install flow

`ark install` runs in the current project directory:

1. **Check for `.ui/` directory.** If missing, output a crank-handle
   prompt: "Install Frictionless first using the github/zot/frictionless
   README, then re-run `~/.ark/ark install`." Exit.

2. **Install skill.** Copy the ark skill to `.claude/skills/ark/SKILL.md`.
   The skill contains the bootstrap sequence (start server, load tags)
   and the `/ark` invocation that spawns the ark agent.

3. **Install agent.** Copy the ark agent doc to `.claude/agents/ark.md`.
   The agent carries the CLI reference, output formats, and guidelines
   (prefer `--wrap`, use knowledge vs memory).

4. **Install app.** Copy the Frictionless app to `.ui/apps/ark/`.
   This gives the user the interactive search UI.

5. **Crank-handle output.** Print a prompt instructing Claude to add
   `load /ark` at the top of the project's CLAUDE.md. This is the
   final step — Claude edits CLAUDE.md, and every future session in
   this project starts with ark context.

### Crank-handle pattern

`ark install` is a Go binary — it can copy files but cannot edit
CLAUDE.md intelligently. The crank-handle output is a self-contained
prompt designed for any model tier (including Haiku) to follow:

- If prerequisite missing: tells Claude exactly what to install and
  how, then asks to re-run `ark install`
- If install succeeds: tells Claude exactly what line to add to
  CLAUDE.md and where

Each output is a complete instruction. Claude reads it, follows it,
done. No context about ark internals needed.

### README-driven installation

The user's only action is telling Claude: "use the ark README to
install it." The README provides:

1. Check if `~/.ark/ark` exists
2. If not, download the binary and place it at `~/.ark/ark`
3. Run `~/.ark/ark install` in the project directory

The README also instructs Claude to install Frictionless first if
not already present. The entire chain is self-resolving — each step
either succeeds or tells Claude what to do next.
