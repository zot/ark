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
- **R29:** Default database directory is `~/.ark/`, overridden via `--dir` flag

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

- **R71:** All CLI commands accept `--dir <path>` (default `~/.ark/`), parsed globally before subcommand dispatch
- **R72:** `ark init` creates a new database with `-embed-cmd`, optional `-query-cmd`, `-case-insensitive`, `-aliases`
- **R73:** `ark add` adds files by path or walks directories per config; accepts `-strategy`
- **R74:** `ark remove` removes files from both engines; accepts paths or glob patterns
- **R75:** `ark scan` walks configured directories, indexes new files, flags unresolved; does not re-check existing
- **R76:** `ark refresh` re-indexes stale files; optional patterns to scope; reports missing
- **R77:** `ark search` performs combined search with query text
- **R78:** `ark search` with `--about`, `--contains`, or `--regex` performs split search
- **R79:** `ark serve` starts the server
- **R80:** `ark status` shows counts: files, stale, missing, unresolved, index built, server running
- **R81:** `ark files [<pattern>...]` lists indexed files; if patterns given, only matching files
- **R82:** `ark stale [<pattern>...]` lists files needing re-indexing; if patterns given, only matching
- **R83:** `ark missing [<pattern>...]` lists missing files; if patterns given, only matching
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

## Feature: Chunk Retrieval
**Source:** specs/search.md

### Chunk Retrieval

- **R108:** `ark search --chunks` emits chunk text as JSONL instead of range output
- **R109:** `ark search --files` emits full file content as JSONL for each matching file
- **R110:** `--chunks` and `--files` are mutually exclusive — error if both provided
- **R111:** (inferred) `--files` deduplicates by file — multiple chunk hits from one file emit the file content once
- **R112:** (inferred) JSONL schema for `--chunks`: `{"path":"...","startLine":N,"endLine":N,"score":F,"text":"..."}`
- **R113:** (inferred) JSONL schema for `--files`: `{"path":"...","score":F,"text":"..."}`  — score is the best chunk score for that file
- **R114:** `--chunks` and `--files` work with combined search, split search, and tag search
- **R115:** Chunk retrieval enables permission end-run: indexed file content emitted without per-file permission
- **R116:** Chunk retrieval works without an embedding model (FTS-only and tag search)

## Feature: Tag Tracking
**Source:** specs/tags.md

- **R117:** @tags are extracted from file content during add and refresh operations
- **R118:** Tag extraction regex: `@[a-zA-Z][\w-]*:` — @ followed by letter, word chars or hyphens, then colon (colon disambiguates from emails/mentions)
- **R119:** `T` prefix + tagname bytes → uint32 count (total occurrences across all files)
- **R120:** `F` prefix + fileid (8 bytes) + tagname bytes → uint32 count (occurrences in that file)
- **R121:** Tag counts are recomputed on refresh — old counts for a file are removed, new counts stored
- **R122:** `ark tag list` shows all known tags with their total counts
- **R123:** `ark tag counts <tag>...` shows the total count for each specified tag
- **R124:** `ark tag files <tag>...` shows files containing the specified tags with file size
- **R125:** `ark tag files --context <tag>...` shows each tag occurrence with the line from tag to end-of-line — includes definitions from tags.md alongside usage
- **R126:** (inferred) When a file is removed, its tag counts are decremented and its F records deleted

### Vocabulary

- **R127:** Tag vocabulary file lives at `~/.ark/tags.md` — indexed like any other file
- **R128:** Tag definition format: `@tag: name -- description`
- **R129:** `ark init` creates a starter tags.md documenting the format and core tags
- **R130:** New tags emerge by use — the vocabulary file documents meanings, not enforces them

### CLI and API

- **R131:** `ark tag` is a new subcommand with sub-subcommands: `list`, `counts`, `files`
- **R132:** (inferred) `GET /tags` — list all tags (server API)
- **R133:** (inferred) `POST /tags/counts` — get counts for specified tags (server API)
- **R134:** (inferred) `POST /tags/files` — get files for specified tags (server API)

## Feature: Indexing Strategies
**Source:** specs/indexing.md

- **R135:** A `jsonl` chunking strategy splits files on newline boundaries — each line is one chunk
- **R136:** `ark init` registers the `jsonl` strategy alongside the existing line-based strategies
- **R137:** (inferred) The JSONL chunker is a microfts2 external command or Go function that outputs byte offsets at newline boundaries

## Feature: Recall Agent
**Source:** specs/main.md

### Recall Agent / /ark Skill

- **R138:** An `/ark` Claude skill provides delegation guidelines for subagents using the ark CLI
- **R139:** The skill gives the subagent the CLI reference — it does not implement search logic itself

### CLAUDE.md Bootstrap

- **R140:** CLAUDE.md (or a hook) runs `ark search --chunks` at session start to seed context
- **R141:** Bootstrap uses tags and FTS queries — no model required
- **R142:** (inferred) Bootstrap replaces manual MEMORY.md curation for factual recall where ark has the content

## Feature: Config Mutation CLI
**Source:** specs/main.md

### Config Subcommands

- **R143:** `ark config add-source <dir> --strategy <name>` adds a new `[[source]]` entry to ark.toml
- **R159:** `ark config remove-source <dir>` removes a source directory from ark.toml — does not remove indexed files (they become orphans until dismissed or re-added)
- **R144:** `ark config add-include <pattern> [--source <dir>]` adds an include pattern — global if no --source, per-source otherwise
- **R145:** `ark config add-exclude <pattern> [--source <dir>]` adds an exclude pattern — global if no --source, per-source otherwise
- **R146:** `ark config remove-pattern <pattern> [--source <dir>]` removes a pattern from include or exclude lists — global if no --source, per-source otherwise
- **R147:** `ark config show-why <file-path>` explains why a file is included, excluded, or unresolved — shows the matching pattern(s), their source (global, per-source, .gitignore, .arkignore), and whether include-wins-conflicts applied
- **R148:** (inferred) `add-source` validates the directory exists before writing
- **R149:** (inferred) `add-include`/`add-exclude` validate the pattern syntax before writing
- **R150:** (inferred) `remove-pattern` is a no-op (with message) if the pattern doesn't exist
- **R151:** (inferred) Config mutation commands re-validate the config after writing (catch identical include/exclude)

### Config Mutation HTTP API

- **R160:** (inferred) `POST /config/remove-source` — remove a source directory (server API)
- **R152:** (inferred) `POST /config/add-source` — add a source directory (server API)
- **R153:** (inferred) `POST /config/add-include` — add an include pattern (server API)
- **R154:** (inferred) `POST /config/add-exclude` — add an exclude pattern (server API)
- **R155:** (inferred) `POST /config/remove-pattern` — remove a pattern (server API)
- **R156:** (inferred) `POST /config/show-why` — explain file status (server API)

### Ignore File Support

- **R157:** `show-why` recognizes .gitignore and .arkignore patterns and reports them as the source
- **R158:** (inferred) Ignore files are read at query time by `show-why`, not stored in ark.toml

## Feature: Fetch
**Source:** specs/main.md

- **R161:** `ark fetch <path>` returns full file contents for any indexed file
- **R162:** The file must be known to microfts2 (in the index) — error if not indexed
- **R163:** Adding a file to ark implies read-approval; fetch side-steps other permission gates
- **R164:** `POST /fetch` — server API endpoint, accepts path in request body, returns file content
- **R165:** (inferred) Output is raw file content to stdout (CLI), or JSON with content field (HTTP)

## Feature: Init Seeding
**Source:** specs/main.md

- **R166:** If `ark.toml` exists in the database directory, `ark init` reads case_insensitive and aliases from it
- **R167:** Init seeding from ark.toml replaces CLI flags — enables clean "delete DB, re-init, scan"
- **R168:** If `ark.toml` does not exist, `ark init` writes one with case_insensitive so it is complete for next time
- **R169:** (inferred) CLI flags override values read from ark.toml when both are provided

## Feature: Server Lifecycle
**Source:** specs/main.md

- **R170:** `ark serve` exits 0 if a server is already running — intent is "make sure it's up"
- **R171:** `ark serve` prints a message on stderr when server is already running
- **R172:** `ark stop` reads PID file, verifies process exists and is ark (handles PID rollover), sends SIGTERM
- **R173:** `ark stop` polls until process exits; returns exit code 1 if process doesn't die within timeout
- **R174:** `ark stop -f` sends SIGKILL instead of SIGTERM
- **R175:** Server catches SIGTERM: closes socket, closes DB, exits 0
- **R176:** Server never removes the PID file — stale PID files are safe because `ark stop` verifies before killing
- **R177:** Remove the current `defer os.Remove(pidPath)` in server.go

## Feature: Context Wrapping
**Source:** specs/search.md

- **R178:** `--wrap <name>` flag on `ark search` and `ark fetch` wraps output in XML tags named by the argument
- **R179:** Format: `<name source="path" lines="start-end">content</name>` for chunks, `<name source="path">content</name>` for files/fetch
- **R180:** Occurrences of the closing tag (`</name>`) in content are escaped to prevent XML breakage
- **R181:** `--wrap` works with `--chunks` and `--files` output modes
- **R182:** Convention: `memory` for conversation/experience content, `knowledge` for distilled facts/notes/code

## Feature: File Similarity Search
**Source:** specs/search.md

- **R183:** `--like-file <path>` reads the file and uses its content as the FTS query with density scoring
- **R184:** Density scoring measures how much a chunk is about the query terms, suitable for long queries
- **R185:** `--like-file` is mutually exclusive with `--contains` and `--regex` (all are FTS queries)
- **R186:** `--like-file` participates in split search — can combine with `--about` (intersect FTS + vector)
- **R187:** `--about-file <path>` deferred to V4 (vector file similarity, requires context window chunking)
- **R188:** (inferred) `--like-file` reads the file at query time — the file does not need to be indexed

## Feature: Tag-only Search
**Source:** specs/search.md

- **R189:** `--tags` flag changes search output to return only @tags found in matching chunks
- **R190:** Search runs normally (FTS, vector, or combined); output is tag vocabulary from results
- **R191:** Output: one tag per line with count (how many result chunks contained it)
- **R192:** With `--scores`, includes the best chunk score where the tag appeared
- **R193:** (inferred) Tag extraction uses the same regex as indexing: `@[a-zA-Z][\w-]*:`

## Feature: Glob Sources
**Source:** specs/v25-enhancements.md

- **R194:** A source `dir` in ark.toml can be a glob pattern (contains `*`, `?`, or `[`)
- **R195:** Glob patterns are stored as-is in ark.toml — they are directives, not concrete directories
- **R196:** `ark sources check` expands globs from config, diffs against existing concrete sources
- **R197:** New directories from glob expansion are added as sources automatically
- **R198:** Directories that no longer exist are flagged as MIA but not removed
- **R199:** Sources previously resolved from a glob that is no longer in config are reported as orphans
- **R200:** Removing a concrete source managed by a glob is an error — change the glob instead
- **R201:** `ark config add-source` accepts glob patterns; if glob chars present, skips os.Stat validation
- **R202:** `POST /config/sources-check` runs glob reconciliation via server API
- **R203:** (inferred) Glob expansion uses filepath.Glob after ~ expansion on each pattern
- **R204:** (inferred) `ark sources check` is cheap enough to run on every `ark serve` startup

## Feature: Global Strategy Mapping
**Source:** specs/v25-enhancements.md

- **R205:** A `[strategies]` table in ark.toml maps file glob patterns to chunking strategy names
- **R206:** During scan, each file is checked against the strategies map before the source's default strategy
- **R207:** Longest matching pattern wins (character count as tiebreaker)
- **R208:** The source `strategy` field is the fallback when no global pattern matches
- **R209:** (inferred) Strategy names in the map must be registered in microfts2 — error at scan time if unknown

## Feature: File Logging
**Source:** specs/v25-enhancements.md

- **R210:** Server logs to `~/.ark/logs/ark.log` in addition to stderr
- **R211:** Server creates the logs directory on startup if it doesn't exist
- **R212:** Uses `io.MultiWriter` for both stderr and the log file
- **R213:** On startup, if the log file exceeds 10MB, truncate to last 1MB
- **R214:** CLI commands that cold-start do not log to file — server only

## Feature: Source Filtering
**Source:** specs/search.md

- **R215:** `--source <pattern>` restricts search to files from source directories matching the pattern (substring match)
- **R216:** `--not-source <pattern>` excludes files from source directories matching the pattern (substring match)
- **R217:** Multiple `--source` or `--not-source` flags allowed (OR logic within each)
- **R218:** `--source` and `--not-source` are mutually exclusive — error if both provided
- **R219:** Source filtering is pushed to microfts2 as a file ID set — excluded files never enter scoring or consume result slots
- **R220:** microfts2 provides WithOnly(ids) and WithExcept(ids) search options that filter during result resolution
- **R221:** Source filtering works with both SearchCombined and SearchSplit paths
- **R222:** (inferred) Source filtering fields pass through the server proxy via searchRequest JSON
