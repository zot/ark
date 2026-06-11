# ark

Your one digital zettelkasten. One database, one server, one index
for everything — notes, code, conversations, decisions. Orchestration
layer over microfts2 (trigram) and the Librarian/EC embedding pipeline
(vector). Go CLI and server.

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
- microfts2 as a library dependency; vector search via the internal Librarian/EC embedding pipeline (no separate vector library)
- LMDB via microfts2's environment (shared)
- Unix domain socket for server communication

## Shared LMDB Environment

Ark opens microfts2 first (which creates the LMDB environment), then
opens its own named subdatabase for metadata (missing files list,
etc.), tags, and chunk embeddings (EC records). The Store and the
Librarian share that env. MaxDBs leaves room: microfts2 uses 2, the
ark subdatabase uses 1+ — no separate vector subDB (R1911).

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

**Top-level patterns are defaults; per-source patterns replace or
extend them.** The top-level `default_include` and `default_exclude`
arrays apply to any source that does not specify its own `include` or
`exclude`. A source can override the default in two ways:

- **Replace** form — `include = ["*.md"]`. Per-source patterns
  *replace* `default_include` entirely for that source.
- **Extend** form — `include.add = ["*.md"]`. Per-source patterns
  are *added* to `default_include` for that source.

The two forms are mutually exclusive within a source — a TOML key
is either an array or a table, not both. A source uses replace
when narrowing the default set (e.g. only `*.md` here), extend when
broadening it (e.g. defaults plus this directory's special files).

The same forms apply to `exclude` / `exclude.add`.

A source's `include` and `exclude` are independent: setting only
`include` (any form) keeps `default_exclude` in effect; setting only
`exclude` (any form) keeps `default_include` in effect.

Each directory entry may optionally specify a `strategies` map (glob
pattern → chunking strategy name) that amends the global strategies
table for files in that source. This lets a source override or
extend strategy assignments without affecting other sources.

**Explicit `add` resolves the strategy from the source.** When
`ark add PATH` names a single file and no `--strategy` is given, the
strategy is resolved the same way the directory walk resolves it: the
file's enclosing source is located and the source's strategy map
(over the global map) picks the match, defaulting to `lines` when no
glob matches. A file that lies **outside every configured source** has
no source to resolve against; added with no explicit `--strategy` this
is a client error — the caller must say how to chunk it — reported as
HTTP 400, never a server-side 500. An explicit `--strategy` is always
honored, so any file (in or out of a source) can still be added by
naming its strategy.

**No rule means no action.** Files that don't match any include or
exclude pattern are not indexed and not ignored — they're held in an
"unresolved" list. The user must provide a rule or explicit decision.
No automagic indexing of unknown files. This prevents the
virus-scanner / git-auto-add problem: nothing enters the index
without an explicit rule or action.

Pattern language uses doublestar glob syntax
(`github.com/bmatcuk/doublestar/v4`). Two semantic modifiers
control file-vs-directory matching:

- Trailing `/` — pattern matches directories only (not files).
  For exclude this means "don't walk into it."
- No trailing `/` — pattern matches files only (not directories).

Wildcards:
- `*` matches any sequence of non-separator characters within a
  single path component (including dotfiles by default — unlike
  shell globs)
- `**` matches zero or more path components (directories). Must
  appear between separators or at pattern boundaries: `**/`, `/**/`,
  `/**`. Mid-pattern `**` without separators (e.g. `**.md`) acts
  as a single `*` — use `**/*.md` for recursive matching.
- `?` matches any single non-separator character
- `[abc]`, `[a-z]` — character classes
- `{alt1,alt2}` — comma-separated alternatives (can nest)

A `dotfiles` config preference controls whether `*` and `**`
match dot-prefixed names — default true (match dotfiles), set to
false for standard shell glob behavior.
Backslash escapes literal wildcard characters (`\*`, `\?`, `\[`)
for filenames that contain them.
`ark init` ships with default excludes for `.git/`, `.env`, etc.

Three anchoring forms by leading slashes:

- **No leading slash** — matches at any depth within the source
  directory (equivalent to prepending `**/`).
- **Single leading slash** (`/path`) — **filesystem-absolute**;
  matches against the file's absolute path on disk, regardless of
  which source it belongs to. Enables blanket excludes (e.g.
  `/tmp/**`).
- **Leading `./`** (`./path`) — **source-root-anchored**; matches
  only at the root of the source directory. After stripping `./`,
  the remainder is matched against the source-relative path.

Examples:
- `exclude: node_modules/` — skips node_modules dirs anywhere
- `exclude: ./vendor/` — skips vendor/ only at the source root
- `exclude: /tmp/**` — blanket exclude of anything under /tmp
- `exclude: /home/me/project/vendor/**` — excludes that exact
  filesystem subtree (works whether or not /home/me/project is
  itself a source)
- `include: *.md` — matches markdown files at any depth
- `include: ./src/**` — matches everything under src/ at the
  source root
- `include: **/*.md` — same as `*.md` (explicit recursive form)
- `include: docs/**/*.txt` — text files anywhere under docs/
- `include: *.{md,txt,org}` — multiple extensions in one pattern


### Config file: `ark.toml`

TOML format. Lives in the database directory.

```toml
# Global settings
dotfiles = true  # whether * matches dotfiles (default true)
case_insensitive = true

# Default patterns — apply to any source that doesn't override
default_include = ["*.md", "*.txt", "*.org"]
default_exclude = [".git/", ".env", "node_modules/"]

# Sources — directories to watch
[[source]]
dir = "~/work/myproject"

[[source]]
dir = "~/notes"
include = ["*.md"]          # replaces default_include for this source
exclude = ["drafts/"]       # replaces default_exclude for this source

[[source]]
dir = "~/scripts"
include.add = ["*.lua"]     # extends default_include with *.lua

[[source]]
dir = "~/work/reference"
strategies = {"*.txt" = "plain"}  # amends global strategies for this source
```

Per-source `include`/`exclude` either replace or extend the
corresponding default. Replace form (`include = [...]`) substitutes
the default entirely; extend form (`include.add = [...]`) appends to
the default. A source that omits both inherits both defaults. A
source that sets only one (in either form) inherits the other. A file
must match at least one include pattern (effective for that source)
to be indexed. The include-wins-conflicts rule applies to the
resulting set.

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
companion binaries in the ark directory (`microfts`, chunking
commands) are found before system commands with the same
name — notably `ark` itself, which is an archive manager on some
Linux distributions (e.g. Steam Deck). User-installed paths
(`~/.local/bin`, etc.) still take priority since they appear
earlier in PATH.

## Init

Create a new ark database:
- Initialize microfts2 (case insensitivity, byte aliases)
- Embeddings are config-gated (the Librarian/EC pipeline runs when
  `tag_model` is set) — no separate vector-store init step
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
- `chat-jsonl` — content-aware JSONL chunker for Claude conversation logs

The `chat-jsonl` strategy parses each line as JSON and extracts only
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

### Extracted fields

From each JSONL record's `message.content` array:
- `"text"` blocks → user messages, assistant responses
- `"thinking"` blocks → assistant reasoning (not the `signature`)

From records with `"content":"string"` (simple text content):
- The string value directly

### Chunk metadata (attrs)

Each chunk carries metadata as key-value attrs:
- `role` — derived from the record's top-level `type` and `isMeta`
  fields: `human` (type=user, no isMeta), `assistant` (type=assistant),
  or `skill` (type=user, isMeta=true). Used by the content view to
  render conversation structure with role indicators.
- `skill` — for skill chunks only, the last path component from the
  `Base directory for this skill: PATH` line (e.g. `ark`, `mini-spec`).
- `timestamp` — the record's `timestamp` field, if present.

### Skipped record types

Entire records are skipped for these `type` values:
- `progress`, `file-history-snapshot`, `queue-operation`, `system`, `last-prompt`

### Skipped fields within message records

- `tool_use` blocks (file contents, code edits — machine payload)
- `tool_result` blocks (command output, file reads)
- `planContent` (duplicate of content already in message)
- `signature` fields (cryptographic, not searchable)
- All envelope metadata (`uuid`, `sessionId`, `cwd`, `usage`, `parentUuid`, etc.)

### Chunk output consistency

`FillChunks` applies the same extraction when emitting chunk text
via `--chunks`, `--wrap`, or `ark chunks`. The raw JSONL line is
never exposed — agents see human-readable text, not JSON envelopes.
This means search results, chunk expansion, and wrapped output all
contain the same extracted content that was indexed.

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

The release version is the single line `**Version: X.Y.Z**` in `README.md`.
The Makefile extracts it and injects it into the binary via
`-ldflags "-X github.com/zot/ark.Version=..."` (the `-X` path is the full
module path, not `ark`); a plain `go build` leaves it as `dev`.

- `ark version`
  Print the build version (`ark <version>`).
- `ark init [--dir <path>] [-embed-cmd <command>] [-query-cmd <command>] [-case-insensitive] [-aliases <from=to,...>]`
  Create a new database. Without `-embed-cmd`, creates an FTS-only
  database (vector search disabled). Embed command can be added later
  via `ark config set-embed-cmd`.
- `ark rebuild [--dir <path>]`
  Delete and recreate the database, then re-scan all sources. Reads
  init settings (case_insensitive, aliases, embed_cmd) from ark.toml
  so the new database matches the old one. Sources and strategies
  are preserved — only the index is rebuilt.
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
- `ark config add-source [--dir <path>] <dir>`
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

LMDB subdatabase `ark` stores tag records (T/F/V/D), embedding records
(EV/EC/EF), config (I), error conditions (E:), file state (M/U), and
page content (PC). Record key/value layouts and the schema-version
protocol live in [record-formats.md](record-formats.md).

Notable behaviors documented elsewhere:
- M records: files that were indexed but have since disappeared from
  disk — flagged for the user/agent to decide what to do.
- U records: files seen during a scan that don't match any
  include/exclude rule. Persisted so the list survives scans;
  cleared when a covering rule is added, the file is dismissed, or
  the file no longer exists on disk.

## Setup and Install

Ark embeds Frictionless — there is no separate UI install. One binary
carries the full stack: search engine, HTTP server, and UI assets.
Three commands handle the journey from binary to working system.

### `ark setup` — global bootstrap

`ark setup` prepares `~/.ark/` for use. It is idempotent — safe to
run after every binary update.

1. **Extract bundled assets.** UI assets (html/, lua/, viewdefs/,
   apps/) are extracted from the binary's zip appendix to `~/.ark/`.
   Existing files are overwritten (they come from the binary, not the
   user).

2. **Install global skills.** Copy the ark skill to
   `~/.claude/skills/ark/SKILL.md` and the ui skill to
   `~/.claude/skills/ui/SKILL.md`. These are the lightweight
   interaction skills — every session gets them.

3. **Install agent.** Copy the ark agent doc to
   `~/.claude/agents/ark.md`.

4. **Link the ark app.** Run `linkapp add ark` so the Frictionless
   UI can serve the ark search interface.

5. **Report.** Print what was installed/updated. No crank-handle
   output — setup is complete in itself.

If `~/.ark/` doesn't exist, it is created. If skills or agents
already exist, they are overwritten with the version from the
current binary. The bundled assets are the source of truth.

### `ark init` — database creation

`ark init` creates a new ark database. Existing behavior is
unchanged: initializes microfts2 and the ark subdatabase, writes
default config (no microvec init step; R1912).

By default, `ark init` runs `ark setup` first if `~/.ark/` has not
been bootstrapped (no `html/` directory present). This means the
common case — first install — is a single command. `--no-setup`
skips the bootstrap for callers who only want the database.

When the database already exists, `ark init` removes it first (deletes
`data.mdb` and `lock.mdb`) before creating a fresh one. This applies
regardless of `--no-setup`. Use `ark rebuild` for the common
"delete and re-scan" workflow — it handles the full cycle.

`--if-needed` skips database creation when a database already exists.
This is for callers (like `ark ui install`) that want to ensure a
working system without risking an existing database.

### `ark ui install` — per-project setup

`ark ui install` is the single entry point for connecting a project
to ark. It runs in the current working directory and ensures the
entire system is ready — the user never needs to run `ark setup` or
`ark init` separately.

1. **Bootstrap.** Internally runs `ark init --if-needed`, which
   triggers setup if needed and creates the database if it doesn't
   exist. If everything is already in place, this is a no-op.

2. **Symlink skills.** Create symlinks in the project's
   `.claude/skills/` pointing to `~/.ark/skills/ark/` and
   `~/.ark/skills/ui/`. Symlinks, not copies — `ark setup` keeps the
   originals current.

3. **Crank-handle output.** Print a prompt instructing Claude to add
   the ark bootstrap line to the project's CLAUDE.md. The binary
   cannot edit CLAUDE.md intelligently; the crank-handle prompt is
   designed for any model tier (including Haiku) to follow.

Per-project setup does NOT install UI building skills (ui-thorough,
ui-fast). Those are heavyweight design systems loaded by specialist
agents, never global.

### README-driven installation

The user's only action is telling Claude: "use the ark README to
install it." The README provides:

1. Check if `~/.ark/ark` exists
2. If not, download the binary and place it at `~/.ark/ark`
3. Run `~/.ark/ark ui install` in the project directory

One command. The entire chain is self-resolving — setup, database,
project connection. No separate Frictionless install. No `.ui/`
prerequisite.
