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
- **R14:** Each source directory may optionally specify a `strategies` map (glob pattern → chunking strategy name) that amends the global strategies table for files in that source
- **R15:** Files matching no include or exclude pattern are held in an "unresolved" list — no automatic indexing
- **R16:** Pattern language uses doublestar glob syntax (github.com/bmatcuk/doublestar/v4). Trailing `/` means directory-only; no trailing `/` means file-only. These are ark-level semantic modifiers on top of doublestar matching.
- **R17:** `*` matches within a path component, `**` matches zero or more path components (must appear between separators or at pattern boundaries). Mid-pattern `**` without separators (e.g. `**.md`) acts as single `*` — use `**/*.md` for recursive matching.
- **R18:** `*` and `**` match dotfiles by default (controlled by `dotfiles` config, default true)
- **R19:** Standard glob wildcards (`?`, `[abc]`, `{alt1,alt2}`) are supported
- **R20:** Backslash escapes literal wildcard characters (`\*`, `\?`, `\[`)
- **R21:** Patterns without leading `/` match at any depth; with leading `/` anchored to watched directory root
- **R22:** `ark init` ships default excludes for `.git/`, `.env`, etc.

### Config File

- **R23:** Config file is TOML format, named `ark.toml`
- **R24:** Config file lives in the database directory
- **R25:** Config has global `dotfiles` setting (default true)
- **R26:** Config has global `include` and `exclude` pattern arrays
- **R27:** Config has `[[source]]` entries with `dir`, optional `strategies` map, and optional `include`/`exclude`

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
- **R55:** `--contains` and `--regex` compose — `--contains` drives FTS query, `--regex` post-filters results
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

### Tag Definitions
- **R502:** Tag definitions are lines matching `@tag: <name> <description>` — first word after `@tag:` is the tag name, rest is description
- **R503:** Definitions are extracted at index time and cached in LMDB as `D` prefix records
- **R504:** Storage: `D` [tagname] [fileid: 8] → description bytes. One record per definition per source file
- **R505:** When a file is re-indexed, its D records are removed and re-extracted (same lifecycle as F records)
- **R506:** `ark tag defs [TAG...]` outputs tag definitions from the LMDB cache
- **R507:** No args: all definitions. With args: only those tags
- **R508:** Default output: `tagname description` per line, deduplicated, sorted alphabetically
- **R509:** `--path` output: `path tagname description` per line, lexically sorted, not deduplicated. Spaces in paths are backslash-escaped
- **R510:** (inferred) Uses server proxy when available, falls back to cold-start withDB. Read-only
- **R511:** (inferred) Append path: scan new bytes for `@tag:` definitions, add D records (no removal — append only adds)

## Feature: Indexing Strategies
**Source:** specs/indexing.md

- **R135:** A `chat-jsonl` chunking strategy splits files on newline boundaries — each line is one chunk
- **R136:** `ark init` registers the `chat-jsonl` strategy alongside the existing line-based strategies
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

- **R143:** `ark config add-source <dir>` adds a new `[[source]]` entry to ark.toml
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
- **R206:** During scan, each file is checked against the merged strategies map (per-source overlaid on global)
- **R207:** Longest matching pattern wins (character count as tiebreaker)
- **R208:** Per-source `strategies` entries take precedence over global entries for the same pattern; if no pattern matches, the default strategy is `lines`
- **R209:** (inferred) Strategy names in the map must be registered in microfts2 — error at scan time if unknown

## Feature: File Logging
**Source:** specs/v25-enhancements.md

- **R210:** Server logs to `~/.ark/logs/ark.log` in addition to stderr
- **R211:** Server creates the logs directory on startup if it doesn't exist
- **R212:** Uses `io.MultiWriter` for both stderr and the log file
- **R213:** On startup, if the log file exceeds 10MB, truncate to last 1MB
- **R214:** CLI commands that cold-start do not log to file — server only

## Feature: Search Filtering
**Source:** specs/search-filtering.md

### Content Filtering
- **R215:** `--filter <query>` runs a preliminary FTS search; matching file IDs become the scope for the main search
- **R216:** Multiple `--filter` flags intersect — all must match (file must appear in every filter's results)
- **R217:** `--except <query>` runs a preliminary FTS search and subtracts those file IDs from the scope
- **R218:** Multiple `--except` flags union — any match is excluded
- **R219:** Content filters are pushed to microfts2 as file ID sets via WithOnly/WithExcept

### Path Filtering
- **R220:** `--filter-files <pattern>` restricts search to files whose paths match the glob pattern
- **R221:** `--exclude-files <pattern>` excludes files whose paths match the glob pattern
- **R222:** Multiple patterns supported for both (OR logic — match any pattern)
- **R223:** Path filtering matches against the full indexed file path using glob syntax
- **R224:** (inferred) Path filters are resolved to file ID sets from microfts2's file list — no FTS query needed

### Tag Filtering
- **R512:** `--filter-file-tags <tag>` restricts search to files that contain the given tag, using the tag index to resolve file IDs
- **R513:** `--exclude-file-tags <tag>` excludes files that contain the given tag
- **R514:** Multiple tag patterns supported (same composition rules as other filters: positive intersect, negative subtract)
- **R515:** Tag filters use the LMDB tag index (T records via Store.TagFiles) — no FTS query or chunk scanning needed
- **R516:** (inferred) Tag filters evaluate after path filters and before content filters in the resolveFilters pipeline

### Composition
- **R225:** All filters produce file ID sets: positive filters intersect, negative filters subtract
- **R226:** Evaluation order: path filters first (cheap), then content filters
- **R227:** The combined file ID set is passed to microfts2 as WithOnly (if any positives) or WithExcept (if only negatives)
- **R228:** Search filtering works with SearchCombined, SearchSplit, and tag search
- **R229:** (inferred) Filter fields pass through the server proxy via searchRequest JSON

### Replaces Source Filtering
- **R230:** `--source` and `--not-source` flags are removed — replaced by `--filter-files` and `--exclude-files`
- **R231:** (inferred) No backward compatibility shim needed — flags are not in use outside testing

## Feature: Config Flag Parsing Bug
**Source:** specs/config-flag-bug.md

- **R232:** Config mutation subcommands must parse flags correctly when positional args precede optional flags
- **R233:** Fix: reorder args so flags come first before calling `fs.Parse`, or document flags-first convention
- **R234:** Affected subcommands: `add-include`, `add-exclude`, `remove-pattern` — any with positional arg + optional `--source` flag
- **R235:** (inferred) Add a test that verifies per-source add-include round-trips correctly through the CLI arg parsing path

## Feature: Content-Aware JSONL Chunker
**Source:** specs/main.md

- **R236:** The `chat-jsonl` strategy is a Go func chunker registered with microfts2 on both Init and Open
- **R237:** Each JSONL line is parsed as JSON; lines with no extractable text produce no chunk
- **R238:** The chunker extracts `type:text` blocks (the `text` field) from `message.content`
- **R239:** The chunker extracts `type:thinking` blocks (the `thinking` field, not the `signature`)
- **R240:** The chunker skips `tool_use` blocks entirely (input contains file contents, code edits)
- **R241:** The chunker skips `tool_result` blocks entirely (command output, file reads)
- **R242:** The chunker skips `planContent` top-level field (duplicate of message content)
- **R243:** The chunker skips record types: `progress`, `file-history-snapshot`, `queue-operation`, `system`
- **R244:** Chunk range is `N-N` (1-based line number) for traceability back to source
- **R245:** Chunk content is the concatenation of extracted text blocks, separated by newlines
- **R246:** As a Go func strategy, the chunker avoids scanner buffer limits on large JSONL lines
- **R247:** (inferred) When `message.content` is a string (not array), the entire string is the chunk text
- **R248:** (inferred) The `chat-jsonl` strategy replaces the external `ark chunk-chat-jsonl` command — no external process needed

## Feature: Enhanced Status
**Source:** specs/status-enhanced.md

- **R249:** `ark status` reports LMDB map usage: used bytes, total map size, and percentage
- **R250:** Map usage is displayed in human-readable units (MB/GB)
- **R251:** Map usage is computed from LMDB env info: (LastPNO + 1) * PageSize = used bytes
- **R252:** `ark status` reports total chunk count across all indexed files
- **R253:** `ark status` reports file count per chunking strategy (e.g., lines=1200 chat-jsonl=73)
- **R254:** `ark status` reports number of configured source directories
- **R255:** New status fields appear after existing fields (files, stale, missing, unresolved)
- **R256:** Output order: files, stale, missing, unresolved, chunks, sources, strategies, map, server
- **R257:** `GET /status` returns the same enhanced data in the JSON StatusInfo response
- **R258:** (inferred) Chunk count is computed by summing ChunkRanges across all indexed files via microfts2 FileInfoByID
- **R605:** `ark status` reports total size of all indexed files, summed from FRecord.FileLength
- **R606:** Total size is displayed parenthesized after the file count in human-readable units (e.g., "files: 1273 (156 MB)")

## Feature: Embedded UI Engine
**Source:** specs/embedded-ui.md

### Dependency
- **R259:** Ark imports `github.com/zot/ui-engine/cli` as a Go library dependency
- **R260:** No separate frictionless binary is required — one ark binary serves everything

### Unified Home Directory
- **R261:** `~/.ark/` contains both ark data (data.mdb, ark.toml, ark.sock, logs/) and UI assets (html/, lua/, viewdefs/, apps/)
- **R262:** The ui-engine's `Server.Dir` is set to `~/.ark/`
- **R263:** UI directories (html/, lua/, viewdefs/, apps/) coexist with ark files without namespace collision

### Three Listeners
- **R264:** `ark serve` starts the ark API server on a Unix socket (`ark.sock`)
- **R265:** `ark serve` starts the ui-engine HTTP server (port written to `ui-port`)
- **R266:** `ark serve` starts the ui-engine MCP protocol server (port written to `mcp-port`)
- **R267:** All three listeners run in one process

### Server Lifecycle
- **R268:** The ark API server (socket, DB) starts before the ui-engine server
- **R269:** If the ui-engine fails to start (port conflict, missing assets), the ark API server continues running — UI is optional
- **R270:** The failure is logged but does not cause `ark serve` to exit
- **R271:** On SIGTERM/SIGINT, the ui-engine server shuts down before the ark DB closes
- **R272:** (inferred) The ui-engine server is started in a goroutine so it doesn't block the ark API server

### ark ui Command
- **R273:** `ark ui` (no subcommand) opens the default browser to the ui-engine's HTTP URL
- **R274:** `ark ui` reads `~/.ark/ui-port` to determine the port
- **R275:** If the server is not running (no ui-port file or port not listening), `ark ui` reports an error
- **R284:** `ark ui run '<lua>'` executes Lua code in the UI session via mcp-port
- **R285:** `ark ui display <app>` displays an app in the browser via mcp-port
- **R286:** `ark ui event` waits for the next UI event (long-poll, 120s timeout)
- **R287:** `ark ui checkpoint <cmd> <app> [msg]` manages app checkpoints (save/list/rollback/diff/clear/baseline/count/update/local)
- **R288:** `ark ui audit <app>` runs a code quality audit on an app
- **R289:** `ark ui status` returns the ui-engine server status
- **R290:** `ark ui open` opens the browser to the current UI session
- **R291:** All `ark ui` subcommands read mcp-port from `~/.ark/mcp-port` and communicate via HTTP
- **R292:** (inferred) `ark ui` subcommands replace the `.ui/mcp` shell script — no separate script needed

### ark setup — Global Bootstrap
- **R276:** UI assets are embedded in the binary via zip-graft (ui-engine's bundle system), not `//go:embed`
- **R277:** The bundle contains the full UI stack: ui-engine static site (html/), frictionless assets, and ark's own app (apps/ark/)
- **R278:** `ark setup` extracts bundled UI assets to `~/.ark/` (html/, lua/, viewdefs/, apps/) using `bundle.ExtractBundle`
- **R279:** `ark setup` runs linkapp to create lua/ and viewdefs/ symlinks for the ark app
- **R280:** `ark setup` is idempotent — safe to run after every binary update, overwrites assets from the binary
- **R281:** `ark setup` creates `~/.ark/` if it doesn't exist
- **R323:** `ark setup` installs the ark skill to `~/.claude/skills/ark/SKILL.md` from bundled assets
- **R324:** `ark setup` installs the ui skill to `~/.claude/skills/ui/SKILL.md` from bundled assets
- **R325:** `ark setup` installs the ark agent to `~/.claude/agents/ark.md` from bundled assets
- **R326:** `ark setup` reports what was installed/updated — no crank-handle output

### ark init — Setup Integration
- **R327:** `ark init` runs `ark setup` first if `~/.ark/` has not been bootstrapped (no `html/` directory present)
- **R328:** `ark init --no-setup` skips the automatic setup, only creates the database
- **R329:** `ark init --if-needed` skips database creation when a database already exists (exits silently)
- **R330:** (inferred) `--if-needed` checks for `data.mdb` in the database directory

### ark ui install — Per-project Setup
- **R331:** `ark ui install` is the single entry point for connecting a project to ark
- **R332:** `ark ui install` internally runs `ark init --if-needed` to ensure setup and database exist
- **R333:** `ark ui install` creates symlinks in the project's `.claude/skills/` pointing to `~/.ark/skills/ark/` and `~/.ark/skills/ui/`
- **R334:** Symlinks, not copies — `ark setup` keeps the originals current
- **R335:** `ark ui install` prints a crank-handle prompt instructing Claude to add the ark bootstrap line to the project's CLAUDE.md
- **R336:** Per-project setup does NOT install UI building skills (ui-thorough, ui-fast) — those are for specialist agents only
- **R337:** (inferred) `ark ui install` creates `.claude/skills/` in the project directory if it doesn't exist

### No MCP Server for Ark
- **R282:** Ark does not register as an MCP server — its interface is the CLI
- **R283:** Agents drive the UI via `ark ui` subcommands (e.g. `~/.ark/ark ui run '...'`) — no separate shell script or MCP registration needed

## Feature: Bundle and Asset Commands
**Source:** specs/bundle-assets.md

### Bundle Mechanism
- **R293:** Assets are grafted onto the ark binary as a zip appendix using ui-engine's bundle system
- **R294:** The zip-graft approach allows the Makefile to layer assets from multiple sources without recompilation
- **R295:** Ark imports ui-engine's exported bundle functions directly (`cli.IsBundled`, `cli.BundleListFilesWithInfo`, `cli.BundleReadFile`)
- **R296:** `bundle.CreateBundle` and `bundle.ExtractBundle` must be re-exported from ui-engine's `cli/exports.go`

### ark bundle
- **R297:** `ark bundle -o <output> [-src <binary>] <dir>` grafts a directory onto a binary as a zip appendix
- **R298:** `-o` (output path) is required
- **R299:** `-src` specifies the source binary; default is the current executable
- **R300:** The positional argument is the directory to bundle; required
- **R301:** Both source binary and directory must exist — error if not
- **R302:** On success, prints "Created bundled binary: <output>"
- **R303:** (inferred) This is a build-time command used by the Makefile, not by end users

### ark ls
- **R304:** `ark ls` lists embedded assets in the running binary
- **R305:** If the binary is not bundled, print an error and exit 1
- **R306:** One file per line; symlinks show as `path -> target`

### ark cat
- **R307:** `ark cat <file>` prints an embedded file's contents to stdout
- **R308:** If the binary is not bundled, print an error and exit 1
- **R309:** Output is raw bytes — no trailing newline added

### ark cp
- **R310:** `ark cp <pattern> <dest-dir>` extracts embedded files matching a glob pattern
- **R311:** If the binary is not bundled, print an error and exit 1
- **R312:** Pattern matches against both basename and full path
- **R313:** Creates destination directories as needed
- **R314:** Preserves file permissions from the bundle
- **R315:** Recreates symlinks as symlinks (not copies)
- **R316:** Removes existing files/symlinks before writing (allows overwrite)
- **R317:** Reports each copied file to stdout
- **R318:** If no files match the pattern, print an error and exit 1

### Makefile Asset Pipeline
- **R319:** The build pipeline extracts frictionless assets (which include ui-engine assets) into a cache directory
- **R320:** Ark's own assets (apps/ark/) are layered on top of the cache
- **R321:** The ark Go binary is built, then `ark bundle` grafts the cache onto it
- **R322:** The result is one binary containing the full UI stack

## Feature: Source Monitoring
**Source:** specs/source-monitoring.md

### ~/.ark Hardcoded Source
- **R338:** ~/.ark is always a source — hardcoded, not configured via ark.toml
- **R339:** The server ensures ~/.ark is a source on every startup, before reading ark.toml
- **R340:** `ark config remove-source` on ~/.ark returns an error
- **R341:** ~/.ark does not appear in ark.toml's source list

### Phase A: Config-Triggered Reconcile
- **R342:** A Reconcile method encapsulates the startup reconciliation cycle: sources-check → scan → refresh
- **R343:** Startup calls Reconcile (existing behavior, extracted into method)
- **R344:** Every config mutation handler (add-source, remove-source, add-include, add-exclude, remove-pattern) triggers Reconcile after completing
- **R345:** Reconcile runs in a background goroutine — HTTP handlers return immediately
- **R346:** If a Reconcile is already running when another is requested, the new request waits for the current one to finish, then runs
- **R347:** Reconcile is idempotent — safe to call repeatedly

### Phase B: Filesystem Watching
- **R348:** The server watches ark.toml with fsnotify; any write triggers config reload + Reconcile
- **R349:** The server watches each resolved source directory with fsnotify
- **R350:** Watches are recursive — subdirectories within sources are watched
- **R351:** When Reconcile adds new sources, new watches start; when sources are removed, watches stop
- **R352:** File events use throttled on-notify: first event triggers immediate index update, then a throttle window starts
- **R353:** Events during the throttle window are ignored — the filesystem is the source of truth
- **R354:** When the throttle window expires, one re-index of current state runs
- **R355:** If events arrive during the re-index, another throttle window starts after it completes
- **R356:** When a window expires with no events, the next notification triggers immediately again
- **R357:** A maximum wait ceiling forces re-index regardless of incoming events, preventing event storms from starving the index
- **R358:** Startup watches source directories before running reconciliation — so nothing changes unseen during the scan
- **R359:** (inferred) fsnotify only sees changes while watching; startup scan catches changes between shutdown and boot

### Watcher Pattern Filtering
- **R387:** Before triggering reconcile on a file event, the watcher checks whether the file is indexable — same Classify check the Scanner uses during Scan()
- **R388:** The watcher finds which source directory the file belongs to, gets effective include/exclude patterns, and calls Classify
- **R389:** If the file would not be included by any source's patterns, the event is ignored (no reconcile)
- **R390:** Directory creation events bypass the indexability check — new directories need watches regardless of pattern match
- **R391:** ark.toml changes have their own code path and bypass the indexability check
- **R392:** DB exposes an IsIndexable(path) method that encapsulates the source lookup and pattern check
- **R393:** Non-indexable paths are cached in a set (negative cache) — subsequent events for the same path skip Classify in favor of a set lookup
- **R394:** The negative cache is cleared whenever ark.toml is reloaded, since pattern changes can alter indexability
- **R395:** (inferred) The negative cache is safe because ark.toml reload is the only event that changes pattern rules — between reloads, indexability cannot change

### Phase C: Append Detection
- **R360:** When a file's modtime changes, check whether the change was append-only before full reindex
- **R361:** Hash the file's content up to the stored length; if hash matches, the change is append-only
- **R362:** If hash differs, fall back to full reindex
- **R363:** For append-only changes, compare the last stored chunk against the same byte range to check for a clean chunk boundary
- **R364:** Ark reads the last chunk's position from microfts2 FileInfo.ChunkRanges for boundary checking
- **R365:** If last chunk matches (clean boundary), append new chunks from the end of the file
- **R366:** If last chunk doesn't match (unclean boundary), re-chunk from the last chunk's start offset only
- **R367:** Append-only chunks only extract tags from new chunks, adding to existing T/F counts
- **R368:** Append detection is universal — every file gets it, not strategy-specific
- **R369:** (inferred) Strategies can report whether they produce clean chunk boundaries (line-based and JSONL always do)

### chat-jsonl Rename
- **R370:** The current `jsonl` chunking strategy is renamed to `chat-jsonl`
- **R371:** A generic JSONL strategy should also exist for non-chat JSONL files

### Markdown Chunker

- **R376:** A `markdown` chunking strategy splits files on paragraph boundaries (blank lines and heading transitions)
- **R377:** A heading line (starting with `#`) always starts a new chunk
- **R378:** A heading and its immediately following paragraph (up to the next blank line or heading) form one chunk
- **R379:** Consecutive blank lines collapse to a single boundary
- **R380:** Chunks use 1-based line ranges (`"5-12"`) consistent with `LineChunkFunc`
- **R381:** The chunker is a `ChunkFunc` in microfts2, registered via `AddStrategyFunc`
- **R382:** Ark registers the markdown strategy in both `InitDB` and `Open`
- **R383:** The default strategy mapping for `*.md` changes from `lines` to `markdown`
- **R384:** Blank lines are boundaries only — not included in any chunk's content
- **R385:** (inferred) Append detection derives boundary cleanliness from last chunk end vs file length — no chunker reporting needed
- **R386:** (inferred) Until O12 back-seek is implemented, append detection falls back to full reindex for markdown-strategy files

## Feature: Cluster 1 — Config/CLI Fixes
**Source:** specs/main.md

### ark rebuild
- **R396:** `ark rebuild` deletes `data.mdb` and `lock.mdb`, then re-runs init (reading settings from ark.toml) and scan
- **R397:** `ark rebuild` preserves ark.toml — only the index is destroyed and recreated
- **R398:** (inferred) `ark rebuild` refuses to run if the server is running — the server holds the DB open

### ark init --no-setup db nuke
- **R399:** `ark init` removes the existing database files (`data.mdb`, `lock.mdb`) before creating a fresh database, regardless of `--no-setup`
- **R400:** (inferred) `ark init --if-needed` does NOT remove existing database — its purpose is the opposite (skip if exists)

### ark ui open rename
- **R401:** `ark ui browser` is renamed to `ark ui open`
- **R402:** (inferred) No alias for `browser` — clean break

## Feature: App Search Support
**Source:** specs/app-search.md

### Grouped Search — mcp:search_grouped()
- **R403:** `mcp:search_grouped(query, opts)` returns results grouped by file as a Lua table of tables
- **R404:** Files sorted by best chunk score (descending), chunks sorted by score within each file
- **R405:** Each chunk table includes `range`, `score`, and `preview` (pre-rendered HTML)
- **R406:** Preview rendering uses goldmark for markdown, JSON pretty-print for JSON (under a length threshold), plain text with HTML escaping otherwise
- **R407:** Query tokens are highlighted with `<mark>` tags in all preview formats
- **R408:** The file's chunking strategy determines which renderer to use
- **R541:** `opts` table supports: `mode` (contains/about/combined), `k` (max results), `preview` (window size), `filter_files`, `exclude_files`, `filter_file_tags`, `exclude_file_tags`
- **R542:** (inferred) Default mode is "combined", default k is 20, default preview is 0

### Click to Open — mcp:open()
- **R410:** `mcp:open(path)` opens a file with the system viewer (`xdg-open` on Linux, `open` on macOS)
- **R411:** The function returns immediately — the viewer opens asynchronously
- **R412:** (inferred) The file path must be an indexed file — error if not found

### Indexing State — mcp:indexing()
- **R414:** Returns an empty table when no indexing is in progress
- **R415:** `mcp:indexing()` is a Go function registered on the Lua mcp table, returns a Lua array of strings
- **R416:** (inferred) All mcp Lua functions are registered after Frictionless setup completes

### HTTP Endpoint Removal
- **R543:** `POST /search/grouped` endpoint is removed
- **R544:** `POST /open` endpoint is removed
- **R545:** `GET /indexing` endpoint is removed
- **R546:** (inferred) All three operations are available only as Lua functions on the mcp table

### Search Consistency
- **R372:** Searches check results for staleness (via microfts2 CheckFile)
- **R373:** If stale hits exist, re-index those files and re-search
- **R374:** After 2 retries with still-stale results, prune stale results and return what's valid
- **R375:** Search never blocks on achieving a perfectly consistent index

## Feature: infrastructure
**Source:** specs/infrastructure.md

### ark ui reload — port persistence
- **R417:** `ark ui reload` restarts the ui-engine without changing the HTTP port
- **R418:** The browser page reconnects automatically via existing WebSocket reconnect logic
- **R419:** If the previous port is unavailable on restart, fall back to a new port and log a warning
- **R420:** (inferred) Reload requires passing a preferred port to flib/ui-engine on restart
- **R421:** If a second WebSocket connection arrives while one is active, the UI shows a "use the other tab" message
- **R422:** (inferred) Second-tab detection is a ui-engine or Frictionless concern, not ark Go code

### MCP event pulse indicator
- **R423:** The 9-dot app grid button pulses while the MCP shell is waiting for Claude to respond
- **R424:** Tooltip on the grid button shows the pending event count
- **R425:** No permanent screen real estate is consumed by the pulse indicator
- **R426:** Pulse stops when the event resolves (Claude responds or timeout)
- **R427:** (inferred) Pulse is driven by CSS class toggle on the grid button element
- **R428:** (inferred) Event pending state already exists in the MCP shell — no new Go code needed

### ark install — project bootstrap
- **R429:** `ark install` runs `ark init --if-needed` internally to bootstrap `~/.ark/`
- **R430:** `ark install` starts the server if not already running
- **R431:** `ark install` creates symlinks in `.claude/skills/` pointing to `~/.ark/skills/`
- **R432:** `ark install` creates a symlink for `.claude/agents/ark.md` pointing to `~/.ark/agents/ark.md`
- **R433:** `ark install` prints a crank-handle prompt instructing Claude to add `load /ark first` to CLAUDE.md
- **R434:** (inferred) Symlinks are idempotent — re-running `ark install` updates existing symlinks without error
- **R435:** (inferred) `ark install` creates `.claude/skills/` and `.claude/agents/` directories if they don't exist
- **R436:** (inferred) `ark install` is an alias for `ark ui install`

### UI status endpoint
- **R437:** `ark ui status` reports whether the UI engine is running and its port
- **R438:** `ark ui status` reports the number of connected browsers (WebSocket connections)
- **R439:** (inferred) Browser count replaces session count in status output (session count is always 1)
- **R440:** `ark ui status` reports indexing state (true/false)
- **R441:** (inferred) Status information is available both as CLI output and via `GET /status` JSON
- **R442:** (inferred) When the UI is not running, `ark ui status` outputs "ui: not available"

## Feature: Messaging
**Source:** specs/messaging.md

### Tag Block Format
- **R443:** A tag block is consecutive lines at the top of a file, each matching `@tag: value`
- **R444:** The first line that doesn't match `@tag: value` ends the tag block
- **R445:** No blank lines within the tag block
- **R446:** A blank line separates the tag block from the body
- **R447:** Tag format is `@name: value` — space after colon is required
- **R448:** One tag per line
- **R449:** Tag names use the same character set as ark tags: letters, digits, hyphens, dots, underscores (starting with a letter)

### new-request
- **R450:** `ark message new-request --from PROJECT --to PROJECT --issue "..." FILE` creates a new request file
- **R451:** The request ID is derived from the filename (basename without extension)
- **R452:** Output file has tag block (`@ark-request`, `@from-project`, `@to-project`, `@status: open`, `@issue`) followed by blank line, heading, and issue text as body
- **R453:** Errors if FILE already exists
- **R580:** If stdin is not a terminal, new-request reads body text from stdin until a lone `.` on a line
- **R581:** Stdin body is appended after the heading scaffold (after the issue text line)
- **R582:** If stdin is a terminal or empty, the command produces the same output as before (no behavior change)

### new-response
- **R454:** `ark message new-response --from PROJECT --to PROJECT --request ID FILE` creates a new response file
- **R455:** Output file has tag block (`@ark-response`, `@from-project`, `@to-project`, `@status: done`) followed by blank line and `# RESP <id>` heading
- **R456:** Errors if FILE already exists
- **R583:** If stdin is not a terminal, new-response reads body text from stdin until a lone `.` on a line
- **R584:** Stdin body is appended after the `# RESP <id>` heading

## Feature: Multisearch
**Source:** specs/multisearch.md

### Multi-strategy search
- **R585:** `--multi` flag on `ark search` runs the query through all four scoring strategies in a single pass
- **R586:** The four strategies are: coverage, density, overlap, bm25
- **R587:** `--multi` calls microfts2 `SearchMulti` which collects candidates once (single LMDB transaction) and scores with each strategy independently
- **R588:** Results from all strategies are deduplicated by (fileid, chunknum), keeping the best score per chunk
- **R589:** `-k` applies to the final merged set, not per-strategy
- **R590:** `--multi` is mutually exclusive with `--score` — using both is an error
- **R591:** `--multi` works with combined search (query arg) and `--contains`
- **R592:** `--multi` does not apply to `--regex`, `--about`, or `--like-file` — using `--multi` with these is an error
- **R593:** All filter flags (`--filter-files`, `--exclude-files`, `--filter-file-tags`, `--exclude-file-tags`, `--filter`, `--except`) apply to all strategies equally

### Proximity reranking
- **R594:** `--proximity` flag enables post-search proximity reranking
- **R595:** Proximity reranking reads chunk text for top candidates and adjusts scores based on minimum term span
- **R596:** The number of candidates to rerank defaults to 2x the `-k` value
- **R597:** `--proximity` composes with any search mode including `--multi`
- **R598:** When used with `--multi`, proximity reranking happens after the multi-strategy merge

### Strategy tagging
- **R599:** When `--scores` and `--multi` are both active, each result includes which strategy produced it
- **R600:** If multiple strategies found the same chunk, the strategy is reported as "multi"

### Go API
- **R601:** `Searcher.SearchMulti(query, opts)` wraps microfts2 SearchMulti for internal callers
- **R602:** SearchMulti handles filter resolution, strategy setup (including BM25 initialization from index counters), deduplication, proximity reranking if requested, and the standard resolve/filter pipeline
- **R603:** SearchGrouped supports multi-strategy search for the UI
- **R604:** (inferred) BM25 initialization reads I record counters (totalTokens, totalChunks) from the microfts2 database — these counters must exist (require reindex on older databases)

### set-tags (alias for `ark tag set`)
- **R457:** `ark tag set FILE TAG VALUE [TAG VALUE ...]` updates or adds tags in the tag block
- **R458:** Arguments are pairs: tag name (without `@` or `:`) then value
- **R459:** If the tag exists, its value is replaced in place (preserving position)
- **R460:** If the tag doesn't exist, it is appended to the end of the tag block
- **R461:** Body content is untouched
- **R462:** Errors if FILE doesn't exist
- **R463:** If the file has no tag block (body starts on line 1), tags are inserted at the top with a blank line before existing content

### get-tags (alias for `ark tag get`)
- **R464:** `ark tag get FILE [TAG ...]` reads tags from the tag block
- **R465:** Output is one `tag\tvalue` per line (tab-separated, no `@` or `:`)
- **R466:** If specific tags named, output only those in the order requested
- **R467:** If no tags named, output all tags in file order
- **R468:** Exits with status 1 if a requested tag is not found (still outputs any found tags)

### check (alias for `ark tag check`)
- **R469:** `ark tag check FILE` validates the file against tag block format rules
- **R470:** If valid, exits 0 with no output
- **R471:** If invalid, outputs a crank-handle diagnostic: problem descriptions and exact `ark tag set` commands to fix them
- **R472:** Detects tag-like patterns (`@word:` or `## Word:`) in the body that look like misplaced tags
- **R473:** Detects blank lines within the tag block
- **R474:** Detects missing blank line between tag block and body
- **R475:** Detects malformed tag lines in the tag block (missing space after colon, etc.)
- **R476:** (inferred) The diagnostic output is designed as a crank-handle prompt — self-contained instructions a model can follow without additional context

### ack
- **R489:** `ark message ack FILE` sets `@msg` to `read` in the file's tag block
- **R490:** If `@msg` is already `read`, `acting`, or `closed`, does nothing (no error)
- **R491:** (inferred) Uses same file read/parse/render/write pattern as set-tags

### close
- **R492:** `ark message close FILE` sets `@msg` to `closed` in the file's tag block
- **R493:** If `@msg` is already `closed`, does nothing (no error)
- **R494:** (inferred) Uses same file read/parse/render/write pattern as set-tags

### inbox
- **R495:** `ark message inbox [--project PROJECT]` lists non-closed messages across all indexed sources
- **R496:** Finds files containing `@msg:` tags via the database, reads each file's tag block
- **R497:** Filters to messages where `@msg` is not `closed`
- **R498:** When `--project` is given, further filters to `@to-project` matching PROJECT
- **R499:** Output sorted: `@msg:new` first, then others; within each group, sorted by file path
- **R500:** Output format: one tab-separated line per message: `msg-value\tto-project\tfrom-project\tstatus\tissue-or-response\tpath`
- **R501:** (inferred) Uses server proxy when available, falls back to cold-start withDB. Read-only.

### inbox enhancements
- **R530:** `--from PROJECT` flag filters messages where `@from-project` matches PROJECT
- **R531:** `--from` is composable with `--project` — when both given, a message must match both (intersection)
- **R532:** When only `--from` is given, `--project` is unconstrained
- **R533:** `--all` flag includes messages with any `@status` value (completed, done, denied)
- **R534:** Without `--all`, current behavior preserved (completed/done/denied filtered out)
- **R535:** `--include-archived` flag includes messages with `@archived: true`
- **R536:** Without `--include-archived`, archived messages excluded (current behavior)
- **R537:** `--counts` flag outputs one line per status value with count instead of individual rows
- **R538:** `--counts` output is tab-separated (`status\tcount`), sorted alphabetically by status
- **R539:** All four flags are composable — counts reflect whatever the other filters select
- **R540:** (inferred) No changes to output format for existing usage — new flags are additive

### Message tag vocabulary
- **R525:** Message identity uses `@ark-request:` and `@ark-response:` tags (ark-prefixed to avoid collision with generic uses)
- **R526:** `@ark-request-sent: <path>` — reference tag for planning files linking to a sent request
- **R527:** `@ark-request-ref: <path-or-id>` — reference tag for citing a request in any file
- **R528:** `@ark-response-ref: <path-or-id>` — reference tag for citing a response in any file
- **R529:** (inferred) Reference tags are passive — ark indexes them like any other tag but assigns no special behavior

### General
- **R477:** Most `ark message` subcommands operate on plain files — no server dependency, no new storage. Exception: `inbox` requires the database.
- **R478:** (inferred) The tag block parser is shared across all subcommands

## Feature: Chunk Context Expansion
**Source:** specs/chunk-context.md

### ark chunks command
- **R479:** `ark chunks <path> <range> [-before N] [-after N]` returns the target chunk plus N neighboring chunks
- **R480:** Default for `-before` and `-after` is 0 (target chunk only)
- **R481:** Output is JSONL — one JSON object per chunk, same format as `ark search --chunks`
- **R482:** Each output object includes `path`, `range`, `content`, and `index` (0-based position in file's chunk list)
- **R483:** Chunks are returned in positional order (ascending index)
- **R484:** Calls `microfts2.DB.GetChunks()` directly — no re-implementation of chunk retrieval
- **R485:** Works via cold-start (`withDB`) — no server proxy needed (read-only, fast)
- **R486:** The file must be indexed — error if not found in the database
- **R487:** `--wrap <name>` wraps output in XML tags, consistent with `ark search` and `ark fetch`
- **R488:** (inferred) Range labels are opaque — the exact string from search results is passed through to `GetChunks`

## Feature: Parallel Indexing
**Source:** specs/parallel-indexing.md

- **R517:** Rebuild and refresh prepare files in parallel — read, chunk, extract tags/trigrams are independent per file
- **R518:** LMDB writes are serialized through a ChanSvc actor — workers send closures that capture prepared data
- **R519:** Worker count defaults to `runtime.NumCPU()`
- **R520:** Worker errors (file read, chunk failure) skip the file and log a warning — do not abort the batch
- **R521:** Missing files are collected and returned as before (no behavior change)
- **R522:** (inferred) Applies to RefreshStale (used by rebuild, refresh, and server reconcile) — single-file paths unchanged
- **R523:** (inferred) No changes to microfts2 API — all writes go through existing methods
- **R524:** (inferred) No changes to fsnotify coordination — reconcileLoop already serializes via channel

## Feature: Vector Benchmark
**Source:** specs/vec-bench.md

### ark vec bench
- **R547:** `ark vec bench --model PATH` loads a GGUF model in-process via gollama and benchmarks embedding against real LMDB chunks
- **R548:** `--n N` controls how many chunks to embed (default 10)
- **R549:** `--random` selects chunks randomly; without it, chunks are sequential from start of index
- **R550:** `--ctx N` sets context window size in tokens (default 2048)
- **R551:** `--prefix TEXT` prepends text before each chunk (e.g. "search_document: " for nomic models)
- **R552:** Model is loaded once at command start; only embedding computation is timed per chunk
- **R553:** Model load time is reported separately from embedding timings
- **R554:** Per-chunk output includes chunk byte length and embedding duration
- **R555:** Summary stats: min, max, mean, median, total time, chunks/sec
- **R556:** Read-only — does not write to the database
- **R557:** (inferred) Uses cold-start (withDB) — benchmark is a one-off diagnostic, not a server operation

### ark vec bench-search
- **R558:** `ark vec bench-search --model PATH --query TEXT` benchmarks the full search path: embed query, brute-force cosine against stored vectors
- **R559:** `--k N` controls number of results (default 10)
- **R560:** `--prefix TEXT` sets query embedding prefix
- **R561:** Reports how many vectors exist in the index and total search time
- **R562:** (inferred) Only useful if vectors have been previously stored — reports zero vectors gracefully

## Feature: Messaging Dashboard
**Source:** .scratch/MSG-DASHBOARD.md

### Go Data Pipe
- **R563:** `mcp:inbox(show_all)` Lua function returns a table of message entries from the LMDB tag index
- **R564:** Each entry contains: status, to (project), from (project), summary, path
- **R565:** Messages are filtered to `requests/` paths only
- **R566:** By default excludes completed/done/denied; `show_all=true` includes them
- **R567:** Excludes archived messages unless explicitly included
- **R568:** Results sorted: open first, then alphabetically by path

### JSON Utilities
- **R569:** `mcp:parseJson(str)` parses a JSON string and returns a Lua table
- **R570:** `mcp:readJsonFile(path)` reads a file and parses its JSON content into a Lua table
- **R571:** Both return nil + error string on parse failure

## Feature: Scoring Strategy
**Source:** specs/search.md

- **R572:** `--score <mode>` flag on `ark search` controls FTS scoring strategy
- **R573:** Three modes: `auto` (default when omitted), `coverage`, `density`
- **R574:** `coverage` mode uses microfts2 coverage scoring (fraction of query trigrams present). No escalation
- **R575:** `density` mode uses microfts2 density scoring (token-density, OR semantics). No escalation
- **R576:** `auto` mode uses coverage first; if zero FTS results, retries with density scoring (fuzzy escalation)
- **R577:** Fuzzy escalation only fires in auto mode — explicit `--score coverage` or `--score density` disables it
- **R578:** `--like-file` always uses density scoring regardless of `--score`
- **R579:** (inferred) Unknown `--score` values produce an error message and exit

## Feature: Tag Block Commands
**Source:** specs/tag-block-commands.md

- **R607:** `ark tag set FILE TAG VALUE [TAG VALUE ...]` updates or adds tags in a file's tag block
- **R608:** `ark tag get FILE [TAG ...]` reads tags from a file's tag block, outputs `tag\tvalue` per line
- **R609:** `ark tag get` exits 1 if a requested tag is not found
- **R610:** `ark tag check FILE [HEADING ...]` validates tag block structure, exits 0 if valid, 1 with diagnostics if not
- **R611:** `ark tag check` with heading arguments flags body headings not in the allowed list
- **R612:** `ark tag check` without heading arguments runs structural validation only
- **R613:** `ark message check FILE` becomes a wrapper that calls `ark tag check` with the standard message heading list
- **R614:** `ark message set-tags` and `ark message get-tags` become aliases for `ark tag set` and `ark tag get`
- **R615:** (inferred) Help text for `ark tag` lists the new subcommands (set, get, check)
- **R616:** (inferred) Help text for aliased commands points users to `ark tag`

## Feature: Inbox Entry Enrichment
**Source:** specs/inbox-entry-enrichment.md

- **R617:** InboxEntry includes a `RequestID` field extracted from `@ark-request:` or `@ark-response:` tag value
- **R618:** InboxEntry includes a `Kind` field: "request" (has `@ark-request:`, different from/to), "response" (has `@ark-response:`), or "self" (has `@ark-request:`, same from/to)
- **R619:** When `@to-project:` contains a comma, Inbox takes the first project name (trimmed) and discards the rest
- **R620:** `mcp:inbox()` passes `requestId` and `kind` fields through to Lua entry tables

## Feature: Inbox Bookmark Fields
**Source:** specs/inbox-bookmark-fields.md

- **R621:** InboxEntry includes a `ResponseHandled` field extracted from the `@response-handled:` tag value (empty if absent)
- **R622:** InboxEntry includes a `RequestHandled` field extracted from the `@request-handled:` tag value (empty if absent)
- **R623:** `mcp:inbox()` passes `responseHandled` and `requestHandled` fields through to Lua entry tables

## Feature: Chunker Strategy Registration
**Source:** specs/chunker-strategies.md

- **R624:** Chunker language configs are defined in `ark.toml` as `[[chunker]]` entries, not hardcoded in Go
- **R625:** Each `[[chunker]]` entry has a `name` (strategy name), `type` ("bracket" or "indent"), and language fields (line_comments, block_comments, strings, brackets, leading_comments)
- **R626:** `type = "bracket"` entries register via `microfts2.AddChunker` with `microfts2.BracketChunker(lang)`
- **R627:** `type = "indent"` entries register via `microfts2.AddChunker` with `microfts2.IndentChunker(lang, tabWidth)`
- **R628:** `tab_width` field is required for indent type; defaults to 4 if omitted
- **R629:** Unknown `type` values produce a warning at init, not a fatal error
- **R630:** Invalid configs (missing required fields) produce a warning at init and are skipped
- **R631:** Default chunker configs ship in `install/ark.toml`, bundled via `BundleReadFile` with fallback
- **R632:** `ark init` seeds `ark.toml` from `install/ark.toml` when no ark.toml exists; preserves existing ark.toml
- **R633:** Custom distributions replace `install/ark.toml` before bundling to customize defaults
- **R634:** Default skeleton includes bracket configs for Go, C/C++, Java, JS, Lisp, nginx, Pascal, Shell/Bash
- **R635:** Default skeleton includes indent configs for Python (tab width 4) and YAML (tab width 2)
- **R636:** On `DB.Init`, ark reads `[[chunker]]` entries from ark.toml, constructs `BracketLang` values, and calls `AddChunker` for each
- **R637:** If a `[[chunker]]` name conflicts with a hardcoded strategy, the TOML config wins
- **R638:** Existing hardcoded strategies (lines, markdown, chat-jsonl, lines-overlap, words-overlap) remain unchanged
- **R639:** (inferred) Chunker strategies appear in `ark strategy list` alongside existing strategies
