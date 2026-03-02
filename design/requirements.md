# Requirements

## Feature: ark
**Source:** specs/main.md

### Language and Environment

- **R1:** Ark is written in Go
- **R2:** Ark uses microfts2 (trigram) and microvec (vector) as library dependencies
- **R3:** LMDB access is via microfts2's shared environment
- **R4:** Server communication uses Unix domain sockets

### Shared LMDB Environment

- **R5:** Ark opens microfts2 first (creates LMDB env), then passes the env to microvec
- **R6:** Ark opens its own named subdatabase for metadata (missing files, unresolved files, settings)
- **R7:** MaxDBs is set to 8 (microfts2: 2, microvec: 1, ark: 1+, room to grow)

### Source Configuration

- **R8:** Config has three levels: directories, include patterns, exclude patterns
- **R9:** A file must match at least one include pattern to be indexed
- **R10:** When both include and exclude match the same file, include wins — no specificity ranking
- **R11:** Identical include and exclude strings are a config error, reported on every operation until resolved
- **R12:** Global include/exclude patterns apply to all directories
- **R13:** Per-source include/exclude are additive — combine with global, not replace
- **R14:** Each source directory specifies a chunking strategy name (microfts2 chunker)
- **R15:** Files matching no include or exclude pattern are held in an "unresolved" list — no automatic indexing
- **R16:** Pattern language has four forms: `name` (file), `name/` (directory), `name/*` (immediate children), `name/**` (any descendant)
- **R17:** `*` matches within a path component, `**` matches any number of path components
- **R18:** `*` matches dotfiles by default (controlled by `dotfiles` config, default true)
- **R19:** Standard glob wildcards (`?`, `[abc]`) are supported
- **R20:** Backslash escapes literal wildcard characters (`\*`, `\?`, `\[`)
- **R21:** Patterns without leading `/` match at any depth; with leading `/` anchored to watched directory root
- **R22:** `ark init` ships default excludes for `.git/`, `.env`, etc.

### Config File

- **R23:** Config file is TOML format, named `ark.toml`
- **R24:** Config file lives in the database directory
- **R25:** Config has global `dotfiles` setting (default true)
- **R26:** Config has global `include` and `exclude` pattern arrays
- **R27:** Config has `[[source]]` entries with `dir`, `strategy`, and optional `include`/`exclude`

### Database Directory

- **R28:** Ark stores everything in one directory: LMDB env, `ark.toml`, Unix socket
- **R29:** Default database directory is `~/.ark/`, overridden via `-db` flag

### Init

- **R30:** `ark init` creates a new database: initializes microfts2, microvec, ark subdatabase, and writes default config
- **R31:** microfts2 is initialized with character set, case insensitivity, and aliases
- **R32:** microvec is initialized with embedding command
- **R33:** Character set, embedding command, and aliases are immutable after creation
- **R34:** Newline alias maps `\n` to `\x01` (SOH) for line-start matching in queries

### Add Files

- **R35:** Add walks source directories per config
- **R36:** Files are checked for staleness via microfts2 and skipped if fresh
- **R37:** Files are added to microfts2 first (returns fileid and chunk offsets)
- **R38:** Chunk text is read from the file using offsets returned by microfts2
- **R39:** Chunks are added to microvec using the fileid from microfts2
- **R40:** microfts2 is the source of truth for file identity — microvec receives fileids from it

### Remove Files

- **R41:** Remove takes a file path (or glob pattern), removes from both engines
- **R42:** microfts2 resolves path to fileid, microvec removes by fileid

### Refresh

- **R43:** Refresh re-indexes stale files using microfts2 staleness detection (modtime + content hash)
- **R44:** For each stale file: re-add to microfts2, remove old vectors from microvec, add new vectors
- **R45:** Missing files are not auto-deleted — added to ark's missing files list for review

### Combined Search

- **R46:** Combined search sends the same query text to both engines
- **R47:** microfts2 returns file/chunk matches with trigram scores
- **R48:** microvec returns file/chunk matches with cosine similarity scores
- **R49:** Results are merged by (fileid, chunknum), combining scores
- **R50:** Results are sorted by combined score descending
- **R51:** Output format: filepath:startline-endline with score

### Split Search

- **R52:** `--about <text>` sends query to microvec only (semantic search)
- **R53:** `--contains <text>` sends query to microfts2 only (exact match)
- **R54:** `--regex <pattern>` sends query to microfts2 only (regex match)
- **R55:** `--contains` and `--regex` are mutually exclusive — error if both provided
- **R56:** Either `--about` or `--contains`/`--regex` works alone (single-engine, no intersection)
- **R57:** When both `--about` and one of `--contains`/`--regex` are used, results are intersected by (fileid, chunknum)

### Common Search Options

- **R58:** `-k <num>` limits max results (default 20)
- **R59:** `--scores` includes scores in output
- **R60:** `--after <date>` filters results to only those newer than date

### Server

- **R61:** `ark serve` binds a Unix domain socket in the database directory
- **R62:** Server writes a PID file outside the database directory (for emergency kill only)
- **R63:** Server opens the database with exclusive lock
- **R64:** Server loads the embedding model and keeps it warm
- **R65:** Server accepts HTTP requests over the Unix socket
- **R66:** Startup reconciliation: scan (walk directories, index new files, flag unresolved) then refresh (check staleness, flag missing) — runs by default
- **R67:** `--no-scan` flag skips startup reconciliation
- **R68:** Highlander rule: socket bind is the lock — only one server per database
- **R69:** If another server holds the socket, new server exits with error
- **R70:** On crash: stale socket remains, CLI detects failed connect, unlinks stale socket, proceeds with cold-start

### CLI

- **R71:** All CLI commands accept `-db <path>` (default `~/.ark/`)
- **R72:** `ark init` creates a new database with `-embed-cmd`, optional `-query-cmd`, `-charset`, `-case-insensitive`, `-aliases`
- **R73:** `ark add` adds files by path or walks directories per config; accepts `-strategy`
- **R74:** `ark remove` removes files from both engines; accepts paths or glob patterns
- **R75:** `ark scan` walks configured directories, indexes new files, flags unresolved; does not re-check existing
- **R76:** `ark refresh` re-indexes stale files; optional patterns to scope; reports missing
- **R77:** `ark search` performs combined search with query text
- **R78:** `ark search` with `--about`, `--contains`, or `--regex` performs split search
- **R79:** `ark serve` starts the server
- **R80:** `ark status` shows counts: files, stale, missing, unresolved, index built, server running
- **R81:** `ark files` lists all indexed files
- **R82:** `ark stale` lists files that need re-indexing
- **R83:** `ark missing` lists files no longer at their indexed path
- **R84:** `ark dismiss` removes missing files from list and both engines; accepts paths or glob patterns
- **R85:** `ark config` shows current source configuration
- **R86:** `ark unresolved` lists files not matching any include or exclude pattern
- **R87:** `ark resolve` dismisses unresolved files by glob pattern (removes from list, no permanent rule)
- **R88:** CLI detects running server (socket connect); proxies if connected, cold-starts if not

### HTTP API

- **R89:** HTTP API runs over Unix domain socket, mirrors CLI, uses JSON request/response
- **R90:** `POST /search` — combined or split search
- **R91:** `POST /add` — add files
- **R92:** `POST /remove` — remove files
- **R93:** `POST /scan` — walk directories, index new files
- **R94:** `POST /refresh` — refresh stale files
- **R95:** `GET /status` — database status (counts)
- **R96:** `GET /files` — list indexed files
- **R97:** `GET /stale` — list stale files
- **R98:** `GET /missing` — missing files list
- **R99:** `POST /dismiss` — dismiss missing files
- **R100:** `GET /config` — current source configuration
- **R101:** `GET /unresolved` — unresolved files list
- **R102:** `POST /resolve` — dismiss unresolved files by pattern

### Ark Subdatabase

- **R103:** LMDB subdatabase named `ark`
- **R104:** `M` prefix + fileid (8 bytes) → JSON missing file record (path, lastSeen timestamp)
- **R105:** `U` prefix + path bytes → JSON unresolved file record (path, firstSeen timestamp, dir)
- **R106:** Unresolved files that no longer exist on disk are removed silently during scans
- **R107:** `I` key → JSON ark-level settings (sourceConfig, dotfiles boolean)
