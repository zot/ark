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

### Default Search Excludes
- **R938:** `search_exclude` is a top-level list of glob patterns in ark.toml
- **R939:** `search_exclude` patterns are applied as `--exclude-files` defaults when the user provides no explicit `--filter-files` or `--exclude-files`
- **R940:** When the user provides explicit `--filter-files` or `--exclude-files`, `search_exclude` is not applied — explicit flags replace the default scope entirely
- **R941:** Subscriptions without explicit file filters inherit `search_exclude` as their exclude-files list
- **R942:** Subscriptions with explicit `--filter-files` or `--exclude-files` use those instead of `search_exclude`
- **R943:** (inferred) `search_exclude` is loaded from config at startup and on config reload

### Naming Normalization
- **R944:** Pubsub `--except-files` CLI flag is renamed to `--exclude-files` for consistency with search
- **R945:** Pubsub `ExceptFiles` struct field is renamed to `ExcludeFiles`
- **R946:** Pubsub JSON wire format `except_files` is renamed to `exclude_files`

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
- **R541:** `opts` table supports: `mode` (contains/about/fuzzy/combined), `k` (max results), `preview` (window size), `filter_files`, `exclude_files`, `filter_file_tags`, `exclude_file_tags`
- **R750:** `mode = "fuzzy"` sets `opts.Fuzzy = true` and dispatches to `SearchFuzzy` via `SearchGrouped`
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

### Tilde expansion
- **R947:** `~` at the start of a path expands to the current user's home directory (`os.UserHomeDir()`)
- **R948:** `~user` at the start of a path expands to the named user's home directory
- **R949:** `~user` first tries the OS user database (`os/user.Lookup`); if that fails, falls back to `filepath.Join(filepath.Dir(homeDir), user)`
- **R950:** Tilde expansion applies to all path-accepting fields: ark.toml (include, exclude, search_exclude, source dir), CLI flags (--filter-files, --exclude-files), glob arguments, and Lua API path parameters (mcp:search_grouped opts, etc.)
- **R951:** (inferred) Expansion happens once at the boundary — config load and CLI flag parsing — before paths reach the matcher or search engine
- **R952:** (inferred) After expansion, all paths are absolute; internal code never sees `~`

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
- **R849:** `--content TEXT` flag provides body text as a command-line argument (alternative to stdin)
- **R850:** If `--content` is set, stdin body reading is skipped
- **R851:** `--content` body is appended after the heading scaffold, same position as stdin body

### new-response
- **R454:** `ark message new-response --from PROJECT --to PROJECT --request ID FILE` creates a new response file
- **R455:** Output file has tag block (`@ark-response`, `@from-project`, `@to-project`, `@status: open`) followed by blank line and `# RESP <id>` heading
- **R456:** Errors if FILE already exists
- **R583:** If stdin is not a terminal, new-response reads body text from stdin until a lone `.` on a line
- **R584:** Stdin body is appended after the `# RESP <id>` heading
- **R852:** `--content TEXT` flag provides body text for new-response (same behavior as R849-R851)

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

## Feature: Sessions
**Source:** specs/sessions.md

### Session Actor

- **R640:** A session is a named, server-side closure actor that carries state across commands
- **R641:** Sessions are identified by name and autocreated on first use
- **R642:** A session runs commands serially in its actor loop
- **R643:** Each session holds a microfts2 ChunkCache as its state
- **R644:** After each command, the session resets a TTL timer
- **R645:** When the TTL fires, a closure is sent into the actor that evicts the cache
- **R646:** The TTL is configured in ark.toml as `session_ttl` (duration string, default "30s")
- **R647:** For search commands, if the new query is not a prefix of the previous query, the cache is evicted before running the search
- **R648:** Sessions require a running server — they are server-side only

### SearchCmd

- **R649:** A SearchCmd struct captures the parameters for a search operation
- **R650:** All three sources (CLI, HTTP, Lua) construct a SearchCmd
- **R651:** A SearchCmd can run directly (no session) or be submitted to a named session
- **R652:** When run within a session, SearchCmd uses the session's ChunkCache
- **R653:** When run without a session, SearchCmd behaves identically to current search — fresh cache per query

### CLI Integration

- **R654:** `ark search` gains a `--session NAME` flag
- **R655:** `--session` implies proxying to the server (server must be running)
- **R656:** Without `--session`, search works as today — direct DB call or server proxy, no session

### HTTP Integration

- **R657:** Search HTTP handler accepts an optional `session` field in the JSON request body
- **R658:** If `session` is present, the server looks up or creates the named session and submits the SearchCmd to it
- **R659:** If `session` is absent, search runs immediately with no session

### Lua Integration

- **R660:** `mcp.search_grouped` accepts an optional `session` field in its opts table
- **R661:** The UI app passes a fixed session name for interactive search so all keystrokes share one cache
- **R662:** (inferred) The Lua function constructs a SearchCmd and submits it to the session

## Feature: Temporary Documents
**Source:** specs/tmp-documents.md

### Core

- **R663:** Temporary documents are ephemeral, in-memory content indexed alongside persistent files
- **R664:** Tmp document paths use the `tmp://` prefix (e.g. `tmp://scoring-notes`)
- **R665:** Tmp documents exist for the lifetime of the running server — server stops, they're gone
- **R666:** Ark delegates tmp storage to microfts2's in-memory overlay (`AddTmpFile`, `UpdateTmpFile`, `RemoveTmpFile`)
- **R667:** Tags are extracted from tmp document content using the same regex as persistent files
- **R668:** (inferred) Tag counts for tmp documents are tracked in memory by the overlay, not in LMDB

### Seamless CLI Integration

- **R669:** `ark add tmp://name` indexes content in memory via `AddTmpFile`
- **R694:** `ark add tmp://name --content "text"` takes inline content from the flag value
- **R695:** `ark add tmp://name --from-file path` reads content from a file on disk
- **R696:** Without `--content` or `--from-file`, `ark add tmp://name` reads content from stdin (default)
- **R909:** `ark add --append tmp://name` appends content to an existing tmp:// document without replacing it; creates the document if it doesn't exist
- **R910:** (inferred) `--append` routes to `/tmp/append` server endpoint instead of `/tmp/add`
- **R670:** `ark remove tmp://name` removes the document from the overlay
- **R671:** `ark files` lists tmp:// files alongside persistent files
- **R672:** `ark search` includes tmp:// results by default
- **R673:** `ark search --no-tmp` excludes tmp:// results
- **R674:** `ark tag files` includes tmp:// files carrying the queried tag
- **R675:** `--filter-files` and `--exclude-files` glob patterns match tmp:// paths
- **R676:** (inferred) `ark status` reports tmp:// document count when any exist

### Search Proxy Optimization

- **R677:** CLI search without `--session` asks the server if tmp files exist via an `onlyIfTmp` flag on the search request
- **R678:** If no tmp files exist, server returns a specific HTTP status (no body) and CLI proceeds with local search
- **R679:** If tmp files exist, server runs the search and returns results
- **R680:** `--no-tmp` on the CLI skips the onlyIfTmp check and always searches locally
- **R681:** `--session` always proxies to the server (unchanged behavior)
- **R682:** `HasTmp()` returns true if any tmp:// documents exist in the overlay

### microfts2 Search Options

- **R683:** `WithNoTmp()` is a microfts2 search option that skips the overlay entirely
- **R684:** `WithNoTmp()` is more efficient than `WithExcept(TmpFileIDs())` — avoids trigram intersection against overlay data

### Server API

- **R685:** Server exposes tmp:// operations through HTTP endpoints: add, update, remove, list
- **R686:** (inferred) Server search handler checks `onlyIfTmp` flag and returns early with a status code if no tmp files exist
- **R687:** (inferred) Server search handler applies `WithNoTmp()` when the request includes a `noTmp` field

### Lua Integration

- **R688:** `mcp.tmp_add(path, content, strategy)` adds a tmp:// document
- **R689:** `mcp.tmp_update(path, content, strategy)` updates an existing tmp:// document
- **R690:** `mcp.tmp_remove(path)` removes a tmp:// document
- **R691:** `mcp.tmp_list()` lists all tmp:// paths

### Content Retrieval

- **R692:** `ark fetch tmp://name` returns full content from the overlay's stored bytes, not disk
- **R693:** `ark chunks tmp://name` works via microfts2's GetChunks which handles tmp:// paths internally

## Feature: Bigram Search (SUPERSEDED)
**Source:** specs/bigram-search.md

Bigrams removed from microfts2 (2026-03-22). Typo tolerance now via SearchFuzzy.

### Strategy API (reverted)

- **R697:** `buildStrategies` returns `map[string]microfts2.ScoreFunc` (reverted from SearchStrategy)
- **R698:** (superseded) StrategyFunc wrappers removed — score functions passed directly
- **R699:** (superseded) BM25 passed directly as ScoreFunc
- **R700:** (superseded) Bigram strategy removed
- **R701:** (superseded) Bigram strategy removed
- **R702:** (superseded) Bigram strategy removed
- **R703:** (superseded) QueryBigramCounts removed from microfts2
- **R704:** (superseded) DB format reverted to v2
- **R705:** (superseded) Bigram rebuild no longer needed
- **R706:** (superseded) No v3 format
- **R707:** (superseded) No bigram index size impact

## Feature: Messaging Support Commands
**Source:** specs/messaging-support.md

### @status-date: automatic timestamps

- **R708:** `ark message new-request` sets `@status-date: YYYY-MM-DD` in the tag block
- **R709:** `ark message new-response` sets `@status-date: YYYY-MM-DD` in the tag block
- **R710:** `ark tag set FILE status VALUE` also sets `@status-date:` to today's date
- **R711:** The auto-set triggers only when the tag name being set is exactly `status`
- **R712:** Date format is `YYYY-MM-DD` (local date, no time)

### ark message inbox --unmatched

- **R713:** `ark message inbox --unmatched` shows only requests with no matching response
- **R714:** Matching groups inbox entries by `requestId` — a request is unmatched if no response shares its `requestId`
- **R715:** `--unmatched` composes with `--project`, `--from`, `--all`, `--include-archived`
- **R716:** The unmatched check applies after all other filtering
- **R717:** (inferred) `--unmatched` implies request-only output — responses are never "unmatched"

### Bookmark lag in CLI inbox

- **R718:** CLI inbox output includes a lag field after existing tab-separated fields
- **R719:** Lag is computed by pairing entries by `requestId`, then comparing each side's handled tag against the counterpart's status
- **R720:** Empty handled tag with non-empty counterpart status counts as lag
- **R721:** No lag when bookmarks are current or when no counterpart exists
- **R722:** Lag field format: `lag:PROJECT:STATUS` showing who is behind and what they haven't handled; empty when no lag
- **R723:** (inferred) Pairing logic is shared between `--unmatched` and lag computation

## Feature: Verbose Flags
**Source:** specs/verbose-flags.md

### Global Flag Parsing

- **R724:** `-v` through `-vvvv` set a global verbosity level (1–4) parsed before subcommand dispatch
- **R725:** Both stacked (`-vvv`) and repeated (`-v -v -v`) forms work
- **R726:** The expansion converts `-vvv` into three `-v` flags; a counter accumulates the total
- **R727:** Verbosity is stripped from the argument list before subcommand dispatch, like `--dir`

### Logging Helper

- **R728:** A package-level `Logv(level int, format string, args ...any)` function emits log output when `verbosity >= level`
- **R729:** Log format is `[vN] message` matching frictionless convention
- **R730:** (inferred) `Logv` uses `log.Printf` so output goes through the existing MultiWriter when the server is running

### Verbosity Levels

- **R731:** Level 1: server lifecycle, connection events
- **R732:** Level 2: HTTP requests, protocol messages
- **R733:** Level 3: indexing detail, variable operations
- **R734:** Level 4: full values, chunk content

### Server Pass-through

- **R735:** `ServeOpts` gains a `Verbosity int` field
- **R736:** The server stores the verbosity level and uses it for `Logv` calls
- **R737:** (inferred) When ark starts the embedded UI, the verbosity level is propagated to `cfg.Logging.Verbosity`

## Feature: Fuzzy Search
**Source:** specs/fuzzy-search.md

### CLI Flag

- **R738:** `ark search --fuzzy` runs typo-tolerant search via `microfts2.SearchFuzzy`
- **R739:** `--fuzzy` takes a positional query (same as `--multi`)
- **R740:** `--fuzzy` is mutually exclusive with `--multi`, `--score`, `--about`, `--regex`, `--like-file`, and `--contains`

### Composable Flags

- **R741:** `--fuzzy` composes with all filter flags (`--filter-files`, `--exclude-files`, `--filter-file-tags`, `--exclude-file-tags`, `--filter`, `--except`)
- **R742:** `--fuzzy` composes with `--proximity` for reranking
- **R743:** `--fuzzy` composes with `--no-tmp`, `-k`, `--chunks`, `--files`, `--tags`, `--scores`, `--wrap`, `--preview`, `--after`, `--before`

### Go API

- **R744:** `SearchOpts` gains a `Fuzzy bool` field
- **R745:** `Searcher.SearchFuzzy(query, opts)` wraps `microfts2.SearchFuzzy(query, k, ...searchOpts)`
- **R746:** `SearchFuzzy` resolves filters, applies proximity reranking if requested, and runs filterAndResolve
- **R747:** `SearchGrouped` dispatches to `SearchFuzzy` when `opts.Fuzzy` is true

### Server Proxy

- **R748:** The search request JSON gains a `Fuzzy` field for server proxy
- **R749:** (inferred) `handleSearch` dispatches to `SearchFuzzy` when the request has `Fuzzy: true`

## Feature: Tag Extraction Fixes
**Source:** specs/tag-extraction-fixes.md

### Inline Tags
- **R751:** `tagRegex` matches `@tag:` anywhere in content, not just at line start
- **R752:** `tagDefRegex` retains its line-start anchor — definitions are a structured format
- **R753:** `tagPattern` (search.go) and tag-block regexes (tagblock.go) are unchanged

### Append-Detection Tag Boundary
- **R754:** During append detection, the tag extraction window backs up from the split point to the previous newline in the full file data
- **R755:** The widened window applies to both `ExtractTags` and `ExtractTagDefs`
- **R756:** The bytes sent to `AppendChunks` are not affected — only the tag scan window is widened
- **R757:** (inferred) Full refresh path is unaffected — it scans the entire file

## Feature: Table Sort
**Source:** specs/table-sort.md

- **R758:** `mcp:sort(table, property, isDate, descending)` sorts a Lua array of tables in place by a named field
- **R759:** `property` is a string field name; the value at that key is used for comparison
- **R760:** When `isDate` is true, values are compared as date strings (`YYYY-MM-DD` format); lexicographic comparison is sufficient
- **R761:** When `isDate` is false, values are compared as case-insensitive strings
- **R762:** When `descending` is true, sort order is reversed (largest/latest first)
- **R763:** Items where the property is missing or nil sort to the end regardless of direction
- **R764:** The function returns the input table (sorted in place)

### InboxEntry statusDate field
**Source:** specs/table-sort.md

- **R765:** `InboxEntry` includes a `StatusDate` field (`json:"statusDate"`) containing the `@status-date:` tag value
- **R766:** `DB.Inbox()` reads the `@status-date:` tag from the file's tag block and populates `StatusDate`
- **R767:** `mcp:inbox()` passes `statusDate` to Lua as a string field on each entry table

## Feature: Message Tag Operations
**Source:** specs/app-search.md

### Bulk Tag Write — mcp.setTags()

- **R768:** `mcp.setTags(path, tags)` is a Lua function on the mcp table (dot syntax, no self)
- **R769:** `path` is the file path, `tags` is a Lua table of name/value string pairs
- **R770:** The function reads the file, parses via `TagBlock.Parse`, calls `TagBlock.Set` for each pair, then writes via `TagBlock.Render`
- **R771:** If a tag named "status" is set, "status-date" is auto-set to today (YYYY-MM-DD), matching CLI `ark tag set` behavior
- **R772:** Returns true on success, nil + error string on failure

### Read Message — mcp.readMessage()

- **R773:** `mcp.readMessage(path)` is a Lua function on the mcp table (dot syntax, no self)
- **R774:** The function reads the file and parses via `TagBlock.Parse`
- **R775:** Returns a Lua table with `tags` (table of tag name/value pairs from the tag block only) and `html` (body rendered via goldmark)
- **R776:** Tag values come exclusively from the tag block, not from body content
- **R777:** Returns nil + error string on read or parse failure

## Feature: Tag Pubsub
**Source:** specs/pubsub.md

### Subscribe

- **R778:** `ark subscribe --session ID --tag TAG` registers a tag subscription for the session
- **R779:** `--value REGEX` optionally filters on tag content (Go RE2)
- **R780:** No `--value` means match any value for that tag
- **R781:** Multiple `--tag` flags create multiple independent subscriptions (OR semantics)
- **R782:** `--filter-files GLOB` restricts matching to files matching the glob
- **R783:** `--except-files GLOB` excludes files matching the glob from matching
- **R784:** `--filter-files` and `--except-files` compose: filter sets the scope, except carves out exceptions
- **R785:** File filters are checked at publish time before enqueue
- **R786:** `--cancel` with no `--tag` cancels all subscriptions for the session
- **R787:** `--cancel --tag TAG` cancels all subscriptions for that tag
- **R788:** `--cancel --tag TAG --value VAL` cancels only subscriptions whose value regex would match VAL
- **R937:** `--tag` values are normalized: leading `@` and trailing `:` are stripped so `@status:`, `@status`, and `status` all resolve to `status`

### Listen

- **R789:** `ark listen --session ID` long-polls for queued notifications
- **R790:** `--timeout N` sets the long-poll timeout in seconds (default 120)
- **R791:** Output is markdown, not JSON — each event is a crank handle
- **R792:** Events are separated by `---` dividers
- **R793:** Each event contains a descriptive phrase and file references (`ark fetch` paths, `ark chunks` commands)
- **R794:** After output, the queue is drained; the agent loops back to listen

### Publish

- **R795:** Publishing is implicit — happens in the existing tag extraction path during add, refresh, and append
- **R796:** After tags are extracted, each tag is matched against the subscription registry
- **R797:** On match: check value regex, check file filters, then enqueue
- **R798:** A session does not receive notifications about its own writes by default

### Subscription Registry

- **R799:** The subscription registry is in-memory; it dies with the server
- **R800:** Per-session data: subscription list, notification channel, last-listen timestamp
- **R801:** Notification channel is bounded (default 100 events) and lossy — non-blocking send, drop if full
- **R802:** If a session hasn't called listen within the TTL (default 10 minutes), its subscriptions and queue are dropped
- **R803:** The listen call resets the TTL timer
- **R804:** A reaper runs on a server ticker (once per minute) to drop stale sessions

### Event Scheduler

- **R805:** Ark maintains a priority queue sorted by next-fire time with a single `time.Timer`
- **R806:** When the timer fires: deliver the event through listen, pop the entry, reset the timer to the new head
- **R807:** If the fired event is recurring, compute the next occurrence and re-enqueue before resetting the timer
- **R808:** ~~Scheduling is a subscription property, not a tag property~~ **Superseded by R853-R855:** scheduling is driven by ark.toml config + day-bucket indexing, not subscriptions
- **R809:** Only the next occurrence of a recurring event lives in the queue
- **R821:** One-shot (`--scheduled`) tag values: DATE formats `YYYY-MM-DD HH:MM`, `YYYY-MM-DD` (defaults 09:00), `MM-DD` (annual). Past one-shots ignored.
- **R822:** Recurring (`--recurring`) tag values follow the grammar: `[starting [on|at] DATE] every [ORDINAL] PERIOD [at HH:MM] [ending [on|at] DATE] [DESCRIPTION]`
- **R823:** Annual shorthand: a bare `MM-DD` value is treated as annually recurring
- **R824:** Recurring PERIOD types: duration (Nm, Nh), day name (Monday-Sunday), day class (weekday, weekend, day), scope (of the week/month/year)
- **R825:** `@ended: [REASON]` in the same chunk as a scheduled/recurring tag stops the event — scheduler skips chunks containing both
- **R826:** ~~On subscribe with `--scheduled`/`--recurring`, scan existing tags via TagContext and populate the queue.~~ **Superseded by R868-R870:** scheduler reads day buckets at startup, not subscription-triggered
- **R827:** ~~If no subscription declares a tag as scheduled/recurring, zero scheduling overhead~~ **Superseded by R853:** zero overhead unless tag is in ark.toml `[schedule]`
- **R828:** ~~Scheduled events are per-subscription~~ **Superseded by R868:** scheduler fires to all listening sessions, not per-subscription

### Muting

- **R829:** `@mute: true` in a file silences all pubsub events from that file
- **R830:** The mute check happens before subscription matching — no events fire, no watchdog findings
- **R831:** Muted files are still indexed and searchable; only notifications are suppressed
- **R810:** Quarter chimes: a built-in recurring event every 15 minutes with full date, day of week, time of day
- **R811:** Push records: in-memory set of (event-id, session-id) pairs prevents duplicate delivery
- **R812:** Server restart clears push records; startup re-scan fires anything due that hasn't been delivered
- **R813:** A Lua function in init.lua computes variable-date holidays at startup and writes them to a tmp:// file with `@ark-event:` tags

### List and Stats

- **R814:** `ark subscribe --list` shows all active sessions and their subscriptions
- **R815:** `ark subscribe --list --session ID` shows subscriptions for one session
- **R816:** List output is tab-separated: session, tag, value_regex, filter_files, except_files, hits, drops
- **R817:** `ark subscribe --stats` shows aggregate hit/drop counts across all sessions
- **R818:** `ark subscribe --stats --session ID` shows stats for one session
- **R819:** PubSub tracks per-subscription hit count (events enqueued) and drop count (events lost to full queue)
- **R820:** Stats are in-memory, reset on server restart

## Feature: app-source-tree
**Source:** specs/app-source-tree.md

### In-process directory listing

- **R835:** `mcp:listSource(sourcePath, prototype)` is a Go function registered on the Lua mcp table via RegisterLuaFunctions
- **R836:** sourcePath is an absolute directory path; the function lists one level of that directory
- **R837:** (inferred) sourcePath must be within a configured source directory; returns empty table if not
- **R838:** Each entry includes: name (basename), relPath (relative to source root), fullPath (absolute), isDir (boolean)
- **R839:** Each entry is classified in-process using Config.ShowWhy logic: state (included/excluded/unresolved), whyPatterns, whySources, whyConflict
- **R840:** Classification checks global and per-source include/exclude patterns plus .gitignore/.arkignore patterns
- **R841:** Entries are sorted: directories first, then alphabetically by name
- **R842:** Directory entries include hasIgnoreFile (boolean) — true when .gitignore or .arkignore exists in that directory
- **R843:** Missing files (in index but not on disk) at the listed directory level are included with isMissing = true
- **R844:** (inferred) Missing file detection uses the DB's existing missing list, not a separate scan

### Prototype wiring

- **R845:** When prototype argument is nil, entries are returned as bare Lua tables
- **R846:** When prototype is non-nil, Go checks once at the start whether the prototype itself has a `new` method (rawget — inherited new() captures the wrong prototype)
- **R847:** If type-specific `new` exists, each entry is created via `prototype:new(table)` — custom init
- **R848:** If `new` does not exist, each entry is created via `session:create(prototype, table)` — mutation tracking without init. Falls back to metatable wiring if session:create unavailable.

## Feature: scheduling
**Source:** specs/scheduling.md

### Schedule Tag Configuration

- **R853:** ark.toml `[schedule]` section declares which tags carry date values; only listed tags are date-parsed at index time
- **R854:** `[schedule.defaults]` maps tag names to default durations (e.g., `dentist = "1h"`, `standup = "15m"`, `birthday = "all-day"`)
- **R855:** Tags not listed in `[schedule]` incur zero date-parsing or day-bucket overhead
- **R856:** Adding or removing a tag from `[schedule]` triggers re-materialization of day-bucket entries for all files containing that tag

### Date and Duration Parsing

- **R857:** The `..` range operator expresses durations: `TIME..TIME` (same-day), `TIME..DATE TIME` (multi-day span)
- **R858:** `DATE TIME` with no `..` uses the default duration from `[schedule.defaults]`; `DATE` alone is all-day
- **R859:** No spaces around `..`; timestamps on both sides. Everything after the time portion is description text.
- **R860:** Use itlightning/dateparse for structured date parsing — handles dozens of formats without format specification
- **R861:** Token-trimming loop separates date from description text: parse progressively shorter prefixes until dateparse succeeds
- **R862:** Anchored-only relative dates: `Feb 2..one week later` is allowed (relative to absolute start); bare `next Tuesday` is not (shifts on re-index)
- **R863:** Relative vocabulary: `N days/weeks/months later` — arithmetic on the parsed start date, no NLP library
- **R864:** The left side of `..` must be an absolute date; no relative-to-relative ranges
- **R865:** Description text after the date portion is preserved on round-trip edits (reschedule)

### Date Keyword Stripping

- **R996:** `parseDateTrimming` strips recognized start keywords from the front of a date expression before passing to dateparse: `from`, `starting`, `beginning`, `after`, `on`
- **R997:** `parseDateTrimming` strips recognized end keywords from the front of a date expression before passing to dateparse: `to`, `until`, `through`, `ending`, `before`, `by`
- **R998:** Keywords are only stripped when followed by a parseable date — `"on time"` does not lose its `on`
- **R999:** Keyword stripping benefits all date parsing (one-shot events, durations, bounds), not just bounded recurrences

### Bounded Recurring Events

- **R1000:** Recurring events can have a start bound, an end bound, or both
- **R1001:** Bounds can appear before or after the recurrence spec: `from March 1 to May 30 every Monday at 5pm` and `every Monday at 5pm from March 1 to May 30` are equivalent
- **R1002:** The `..` range form works for bounds in either order: `2026-03-01..2026-05-30 every Monday at 09:00`
- **R1003:** Start-only bound: no end, materialize through forward window. `every Sat at 9:30am starting Mar 2 2026`
- **R1004:** End-only bound: start from first occurrence after now. `every Monday at 5pm until May 30`
- **R1005:** `computeNext` gains `notBefore` and `notAfter` parameters; returns zero time when next occurrence exceeds `notAfter`
- **R1006:** Materialization stops at `min(endDate, now + forwardWindow)`
- **R1007:** Schedule log records parsed bounds as `@ark-event-start:` and `@ark-event-end:` tags so the scheduler reads them directly on startup
- **R1008:** (inferred) Crank-forward on startup respects bounds — does not create `@ark-event-upcoming:` entries beyond `@ark-event-end:`
- **R1009:** (inferred) `writeDateIndex` skips schedule log files (`~/.ark/schedule/*`) to prevent cascade — log writes trigger watcher, watcher re-indexes, re-index calls EnsureUpcoming, which writes again
- **R1010:** Schedule log maintains exactly one `@ark-event-upcoming:` per recurring event. Calendar UI computes future dates from `@ark-event-spec:`.
- **R1011:** After downtime, crank-forward converts all past upcoming to fired, then writes one new upcoming
- **R1012:** Per-tag schedule filtering via `[schedule.tag.NAME]` in ark.toml with `filter_files`/`exclude_files`. Global excludes always apply; per-tag filters narrow further.
- **R1013:** tmp:// source files produce tmp:// schedule logs (`tmp://schedule/HASH.md`), not disk logs
- **R1014:** Schedule processing deferred outside DB actor — items accumulated during indexing, drained after scan/refresh, processed in goroutine
- **R1015:** `ark schedule search DATE` uses same date grammar as schedule tags (single date, `..` range, keyword prefixes)
- **R1016:** `ark schedule parse DATE` diagnostic — shows parsed start, end, description, recurrence spec, bounds, next occurrence
- **R1017:** `ark schedule tags` shows configured tags, defaults, lifecycle status, per-tag filters
- **R1018:** `RemoveFile`/`RemoveByID` clears TD/TF day bucket records via `ClearDayBuckets`
- **R1019:** `WriteDayBucketsForFile` handles schedule log files via `dayBucketsFromLogFile` — parses `@ark-event-upcoming:`/`@ark-event-fired:` entries
- **R1020:** `ParseDate` handles `2006-01-02 15:04` format (space-separated date+time)
- **R1021:** `ReloadConfig` updates `indexer.config` (was stale after ark.toml reload)
- **R1022:** Indexer config set at DB open time, not only when scheduler is wired — enables day bucket writes during rebuild

### Month Buckets (replaces Day Buckets)

- **R1023:** Remove LMDB day bucket records (TD/TF). Replace with in-memory month buckets computed from schedule log specs.
- **R1024:** One month bucket entry per month per recurring event — the first occurrence in that month
- **R1025:** Query: find month bucket at or before range start, crank forward to generate all events in range
- **R1026:** Month buckets computed on startup from schedule log files. Recomputable on restart.
- **R1027:** `ark schedule search` computes events from specs and month buckets — works without a running server
- **R1028:** @obsolete-req: R866 -- day bucket LMDB indexing replaced by month buckets
- **R1029:** @obsolete-req: R871 -- TF reverse index for deletion no longer needed
- **R1030:** @obsolete-req: R911 -- TD JSON array no longer needed
- **R1031:** @obsolete-req: R912 -- ack status embedded in day buckets no longer needed
- **R1032:** @obsolete-req: R1019 -- dayBucketsFromLogFile no longer needed

### Schedule Tags --values

- **R1033:** `ark schedule tags --values` shows tag values from source files and next upcoming date from schedule logs
- **R1034:** Reads schedule log files directly — no server dependency

### Scheduling Exceptions

- **R1035:** `@remove: DATE [text]` in the same chunk as a schedule tag skips that occurrence
- **R1036:** `@add: DATE [text]` in the same chunk as a schedule tag adds an extra occurrence
- **R1037:** Exception tags use short names scoped by the event chunk (not @ark-event- prefix)
- **R1038:** Exceptions parsed at index time and stored in the event struct
- **R1039:** crankForward, month bucket generation, and schedule search all respect exceptions
- **R1040:** Source file is the authority — schedule log upcoming entry reflects the computed result after exceptions

### Gap Detection (revised)

- **R1041:** Gap detection compares recurrence spec against @ack: dates — no fired records needed
- **R1042:** @obsolete-req: R870 -- @ark-event-fired: entries in log no longer needed for gap detection
- **R1043:** `ark schedule search --gaps` computes unacked past occurrences from spec vs @ack: dates

## Feature: Chat Transcript
**Source:** specs/chat-transcript.md

- **R1044:** `ark chats GLOB` reads Claude Code JSONL logs and renders human-readable transcripts
- **R1045:** User turns introduced with `❯`, assistant turns with `●`, continuation lines indented 2 spaces
- **R1046:** Text word-wrapped at `--line-length` (default 100)
- **R1047:** `--with-tools` shows tool calls inline as `⚙ ToolName summary`
- **R1048:** `--wrap NAME` surrounds output with `<NAME>...</NAME>` tags
- **R1049:** Sidechain messages (subagent traffic) filtered out
- **R1050:** GLOB matches against file basenames in `~/.claude/projects/` directories

### Day-Bucket LMDB Indexing

- **R866:** Events are discretized into day-granularity buckets: key `TD|YYYYMMDD|fileid|tag`, value is a JSON array of events for that day/file/tag
- **R867:** Calendar range query: seek `TD|start`, scan to `TD|end` — no post-filtering needed
- **R911:** TD value is a JSON array — multiple events per day per file/tag (e.g., rescheduled occurrences)
- **R912:** Each event in the array carries ack status (acked bool, ackText string), parsed from `@ack:` tags in the same chunk at index time
- **R913:** (inferred) Calendar view gets events + ack status in one range scan, no second pass

### Schedule CLI

- **R914:** `ark schedule search START END` queries day buckets for events overlapping the date range
- **R915:** START and END accept flexible date formats via dateparse
- **R916:** Output is markdown by default (crank-handle style for agents)
- **R917:** `--json` flag outputs JSON array
- **R918:** `--tag TAG` filters to a specific schedule tag
- **R919:** `--gaps` shows only past events with `acked: false` — Franklin's missed-event query
- **R920:** Each event in output includes ack status from the day-bucket record
- **R921:** `ark schedule change PATH TAG NEWSTART [NEWEND]` rewrites the date in a schedule tag value
- **R922:** Description text after the date is preserved on rewrite
- **R923:** File is re-indexed after modification
- **R924:** For recurring events, updates the corresponding `@ark-event-upcoming:` entry in the schedule log
- **R925:** `--dry-run` shows what would change without writing
- **R926:** (inferred) `ark schedule` with no subcommand or `--help` shows usage

### Config Change Detection

- **R927:** Store serialized `[schedule]` section in LMDB settings record (I prefix) on server startup
- **R928:** On config reload (startup, ark.toml fsnotify), compare current `[schedule]` vs stored
- **R929:** Tags added: scan files with the new tag, write day buckets
- **R930:** Tags removed: clear day buckets for files with that tag
- **R931:** Defaults changed: re-materialize affected day buckets with new durations
- **R932:** (inferred) After re-materialization, update the stored `[schedule]` in LMDB

### Acknowledgment Indexing

- **R933:** When indexing a file with schedule tags, parse `@ack:` tags in the same chunk
- **R934:** For each day bucket being written, check if any `@ack:` covers that date
- **R935:** Embed `acked: true` and `ackText` in the DayBucketEvent when covered
- **R936:** `@ack:` parsing uses the same date formats as schedule tag parsing (dateparse)
- **R868:** (inferred) Multi-day events produce one TD entry per day spanned
- **R869:** (inferred) Day buckets for recurring events are derived from `@ark-event-upcoming:` entries in schedule log files, not materialized directly from the recurring spec
- **R870:** Past events are indexed from `@ark-event-fired:` entries in schedule log files as day buckets — the calendar is a historical record

### Reverse Index for Deletion

- **R871:** `TF|fileid` key stores the list of all dates with day-bucket entries for that file
- **R872:** On re-index: read `TF|fileid` (one read), delete each `TD|date|fileid|*`, delete `TF|fileid`, write new TD + TF from current content
- **R873:** File removal (`RemoveFile`, `RemoveByID`) clears TD/TF day bucket records via `Store.ClearDayBuckets`

### Schedule Log

- **R899:** `~/.ark/schedule/` directory holds schedule log files — one per source file containing schedule tags
- **R900:** Each event definition gets a chunk in its log file with `@ark-event:`, `@ark-event-source:`, `@ark-event-spec:` tags identifying it
- **R901:** `@ark-event-upcoming:` tags in the log represent concrete future instances; `@ark-event-fired:` tags represent past instances
- **R902:** On index of a source file with a schedule tag, the scheduler ensures `@ark-event-upcoming:` entries exist through the forward window (default 6 months)
- **R903:** Deleting an `@ark-event-upcoming:` line is a scheduling exception — that occurrence is skipped
- **R904:** Editing an `@ark-event-upcoming:` date moves that occurrence — just a file edit, indexed normally
- **R905:** Crank-forward checks for existing `@ark-event-upcoming:` before adding — no duplicates
- **R906:** Log files are rotatable — old `@ark-event-fired:` entries can be archived; `@ack:` in source files is the durable human record
- **R907:** Log files are regular ark files — tagged, indexed, searchable
- **R908:** (inferred) `~/.ark/schedule/*.md` is included in the `~/.ark` source so log files are indexed automatically

## Feature: Schedule Lifecycle
**Source:** specs/schedule-lifecycle.md

### Schedule Filtering
- **R953:** `filter_files` in `[schedule]` restricts which files are scanned for schedule tags (glob patterns, tilde expanded)
- **R954:** `exclude_files` in `[schedule]` excludes files from schedule scanning (glob patterns, tilde expanded)
- **R955:** `filter_files` and `exclude_files` use the same narrow/carve semantics as search — filter sets scope, exclude carves exceptions
- **R956:** When both are absent, all indexed files are eligible for schedule scanning
- **R957:** `lifecycle_include` controls which schedule tags get the full lifecycle (log entries, check-gap, gap detection). Default `"*"`.
- **R958:** `lifecycle_exclude` carves exceptions from `lifecycle_include`
- **R959:** Tags outside the lifecycle still fire through pubsub — they just don't get logged or monitored
- **R960:** (inferred) Lifecycle include/exclude use glob patterns on tag names

### EnsureArkSource Scoping
- **R961:** The hardcoded `~/.ark` source sets `include = ["ark.toml", "schedule/**", "apps/**", "storage/**"]`
- **R962:** Directories outside the include list (data.mdb, lock files, logs) are not indexed
- **R963:** (inferred) Archived schedule logs in `~/.ark/schedule-archive/` are unindexed — rotated logs leave the index automatically

### Log Writing on Fire
- **R964:** When a lifecycle event fires, convert `@ark-event-upcoming: DATE` to `@ark-event-fired: DATE` in the schedule log
- **R965:** Append `@check-gap: DATE` in the same paragraph as `@ark-event-fired:` — same chunk after markdown chunking
- **R966:** Compute next occurrence, append `@ark-event-upcoming: NEXT` if no exception exists for that date
- **R967:** Re-index the log file after modification so day buckets update
- **R968:** For non-lifecycle tags, fire through pubsub but skip log writing (no fired tag, no check-gap)

### Check-Gap and Ack Resolution
- **R969:** `@check-gap: DATE` in a schedule log chunk means the event fired but hasn't been acknowledged
- **R970:** The lifecycle subscribes to `@ack:` tag changes in source files
- **R971:** When an ack arrives covering a fired date, remove the corresponding `@check-gap:` line and re-index the log file
- **R972:** On startup, scan schedule logs for unresolved `@check-gap:` entries within the lookback window (default 7 days)
- **R973:** Unresolved check-gaps within the lookback window are appended to `tmp://watchdog/missed-events`
- **R974:** (inferred) No polling — ack resolution is subscription-driven. Check-gap presence = unresolved, absence = handled.

### Config Change Re-materialization
- **R975:** Schedule filtering config (`filter_files`, `exclude_files`, `lifecycle_include`, `lifecycle_exclude`) is included in the stored `[schedule]` hash
- **R976:** Filter changes trigger re-evaluation: files newly in scope get schedule log entries written; files out of scope get log entries and day buckets removed
- **R977:** (inferred) Lifecycle filter changes re-evaluate which tags get check-gap monitoring — newly excluded tags have their check-gaps removed

### Materialization Strategy
- **R978:** Only the next occurrence of a recurring event is materialized in the schedule log
- **R979:** On startup, compute missed occurrences between last-fired and now, surface as missed events, then materialize just the next one
- **R980:** (inferred) Calendar UI computes virtual recurring items on the fly from recurrence specs — deferred to Lua/UI work

### Scheduler Integration

- **R874:** Scheduler reads schedule log files at startup — not subscriptions, not LMDB registries
- **R875:** On server startup: scan `~/.ark/schedule/` for `@ark-event-upcoming:` entries, populate the priority queue
- **R876:** On startup: `@ark-event-upcoming:` entries in the past are converted to `@ark-event-fired:`, next occurrences computed and appended
- **R877:** On recurring event fire: convert `@ark-event-upcoming:` → `@ark-event-fired:` in log, compute next occurrence, append `@ark-event-upcoming:` if none for that date, re-index log, re-enqueue
- **R878:** Events delivered through the publisher carry their nature (scheduled fire vs tag-change notification) so receivers can distinguish

### Remove Scheduling from Subscriptions

- **R879:** Remove `--scheduled` and `--recurring` flags from `ark subscribe` CLI
- **R880:** Remove `ScheduleMode` type, `ScheduleNone`/`ScheduleOneShot`/`ScheduleRecurring` constants, and `Schedule` field from `TagSub`
- **R881:** Remove `ScanForSub` from EventScheduler — replaced by day-bucket startup scan (R875)
- **R882:** (inferred) Remove `RemoveForSession` session-scoped event cleanup — events are no longer per-subscription

### Acknowledgments

- **R883:** `@ack:` tags in the same chunk as an event record acknowledgment
- **R884:** `@ack: ..DATE [text]` — open start, first ack only ("covered from the beginning through DATE")
- **R885:** `@ack: DATE [text]` — single date acknowledgment
- **R886:** `@ack: DATE..DATE [text]` — closed range, both endpoints required
- **R887:** Open ends (`DATE..`) are never allowed
- **R888:** Multiple `@ack:` tags per chunk, no blank lines between them (same-chunk rule)
- **R889:** Gaps between acknowledged dates = missed/unacknowledged occurrences (the staleness signal)

### Gap Detection

- **R890:** Compare event fire dates against `@ack:` entries in the chunk to find unacknowledged occurrences
- **R891:** Lookback window (default 7 days) limits recent-miss detection on agent connect
- **R892:** (inferred) Gap detection results are surfaceable via Lua API for Franklin's morning briefing

### Lua APIs

- **R893:** `mcp:scheduled(startDate, endDate)` returns items overlapping a date range from day-bucket index; each item has date, endDate, tag, summary, path, recurring, allDay
- **R894:** `mcp:reschedule(path, tag, newDate, newEndDate)` rewrites the date in the tag value, preserves trailing description text, re-indexes
- **R895:** `mcp:tagComplete(prefix)` returns tag name and value completions from the index
- **R896:** `mcp:fileStatus(path)` returns whether the file is indexed, its tags, and schedule info
- **R897:** `mcp:subscribe(opts, callback)` registers a UI-side tag-change subscription; callback fires on matching tag events
- **R898:** `mcp:subscribe` supports tag, value (RE2 regex), filterFiles, exceptFiles — full parity with CLI minus removed scheduled/recurring flags

## Feature: Status DB Records
**Source:** specs/status-db.md

- **R899:** `ark status --db` shows LMDB record counts grouped by subdatabase (microfts2, ark)
- **R900:** Each record type displays prefix letter, purpose label, count, key bytes, and value bytes
- **R901:** Record types are sorted alphabetically within each subdatabase
- **R902:** Counts are right-aligned for readability
- **R903:** Without `--db`, status output is unchanged
- **R904:** microfts2 record types: C (chunks), F (files), H (hashes), I (config), N (paths), T (trigrams), W (tokens)
- **R905:** ark record types: D (tag-defs), F (file-tags), I (settings), M (missing), T (tag-totals), U (unresolved), V (tag-values)
- **R906:** `GET /status?db=true` includes record counts in the JSON StatusInfo response
- **R907:** (inferred) Store needs a RecordCounts method to count ark subdatabase records by prefix
- **R908:** (inferred) microfts2 needs a RecordCounts method returning counts per prefix byte
- **R1130:** A total summary line shows aggregate record count, key bytes, value bytes, and proportion of LMDB map

## Feature: Search Profiling
**Source:** specs/search-profiling.md

- **R981:** `ark search --cpuprofile FILE` writes a Go pprof CPU profile covering the full search operation
- **R982:** `ark search --memprofile FILE` writes a Go pprof heap profile after search completes (post-GC)
- **R983:** All three flags are optional and independent
- **R984:** (inferred) Profiling wraps the entire cmdSearch scope — DB open through result output
- **R985:** `ark search --trace FILE` writes a Go execution trace (runtime/trace) covering the full search operation

## Feature: DB Concurrency
**Source:** specs/db-concurrency.md

- **R986:** All DB operations are serialized through a closure actor (ChanSvc) on `ark.DB`
- **R987:** Watcher file change operations (reindex, remove) use fire-and-forget (Svc)
- **R988:** HTTP handler operations use synchronous calls (SvcSync) to return results and status codes
- **R989:** CLI search operations use synchronous calls (SvcSync) to return results
- **R990:** The existing reconcileLoop merges into the DB actor — no separate reconcile goroutine
- **R991:** Watcher batches specific changed/removed paths during throttle window, sends one closure on expiry
- **R992:** Full reconcile still runs on config change and startup
- **R993:** (inferred) Session → DB call direction is always one-way; no SvcSync from DB actor back to session actor
- **R994:** (inferred) Lua source-add operations use fire-and-forget through the Lua session's closure actor
- **R995:** (inferred) Go-side caches (pathCache, pathToID, frecordCache) are safe by construction — only accessed inside the actor

## Feature: DB Write Actor
**Source:** specs/db-write-actor.md

- **R1051:** Reads execute directly in the main actor and return immediately — LMDB MVCC ensures consistent snapshots during writes
- **R1052:** Config files (ark.toml) are indexed in-place in the main actor, synchronously, before any normal writes that depend on them
- **R1053:** Normal file writes are queued as closures; if the queue was empty, the first closure is dequeued and run in a goroutine
- **R1054:** The write goroutine calls `db.Copy()` to get a shallow copy sharing the LMDB env but with nil caches
- **R1055:** The write goroutine opens a write transaction on the copy and indexes the batch (file I/O off the actor)
- **R1056:** After indexing, the goroutine sends a reconcile closure back to the main actor channel
- **R1057:** The reconcile closure calls `InvalidateCaches()`, commits the write transaction, and dequeues the next write if available
- **R1058:** Each write goroutine runs one batch and dies — the main actor decides whether to start the next (continuation pattern)
- **R1059:** On goroutine panic: defer/recover sends an error closure to the main actor; the batch is dropped
- **R1060:** On reconcile error: log the failure, skip the batch, dequeue next — system self-heals on next write request
- **R1061:** Errors must be logged visibly — silent drops cause confusion about why files aren't indexed
- **R1062:** When a scan produces N files: partition into config files vs content files, process config first (synchronous), then queue content as write batches
- **R1063:** microfts2 needs `Copy() *DB` — shallow copy sharing LMDB env, overlay pointer shared (has its own mutex), caches set to nil, chunker registry shared (read-only)
- **R1064:** microfts2 needs `InvalidateCaches()` — nils pathCache, pathToID, frecordCache, forcing lazy reload on next access
- **R1065:** The write actor is a goroutine, not a separate ChanSvc — no lifetime management, no second channel
- **R1066:** (inferred) The deferred-schedule pattern (pendingSchedule / DrainSchedule / processScheduleItems) can be removed once schedule I/O moves into the write goroutine
- **R1067:** (inferred) No more than one write goroutine runs at a time — serialized by the main actor's dequeue-after-commit pattern
- **R1068:** (inferred) The public API (db.Search, db.AddFile, etc.) is unchanged — the write queue is an internal optimization

## Feature: Editor HTTP Endpoints
**Source:** specs/editor-endpoints.md

### Grouped Search Endpoint
- **R1069:** `POST /search/grouped` accepts JSON body with query, mode, k, session, filter_files, exclude_files, filter_file_tags, exclude_file_tags, filter, except
- **R1070:** Response is a JSON array of `{path, strategy, chunks}` groups, sorted by best chunk score descending
- **R1071:** Each chunk includes `range`, `score`, `content` (raw text), `contentType`, and `preview` (rendered HTML)
- **R1072:** `contentType` is derived from the indexing strategy: "markdown" for markdown, "json" for chat-jsonl, "code" for bracket/indent, "text" for everything else
- **R1073:** `mode` field selects search mode: "combined" (default), "contains", "about", "fuzzy"
- **R1074:** `session` field enables session-scoped search with chunk caching (same as existing handleSearch)
- **R1075:** Uses existing `SearchGrouped` — no new search logic, only HTTP exposure + enhanced chunk fields

### Tag Completion Endpoint
- **R1076:** `POST /tags/complete` accepts JSON body with `prefix` string
- **R1077:** Returns JSON array of `{name, description}` objects matching the prefix
- **R1078:** Matches D (definition) records by tag name prefix, case-insensitive
- **R1079:** When multiple files define the same tag, use the first description found (deduplicate by name)
- **R1080:** Empty prefix returns all known tag names (from T records) with descriptions from D records where available

### Tag Value Completion Endpoint
- **R1081:** `POST /tags/values` accepts JSON body with `tag` and `prefix` strings
- **R1082:** Returns JSON array of `{value, count}` objects for known values of the tag
- **R1083:** Values are extracted by scanning files that have the tag (via F records for file IDs)
- **R1084:** Results are filtered by prefix (case-insensitive) and sorted by count descending
- **R1085:** (inferred) Count reflects how many files have that tag+value combination

### File Save Endpoint
- **R1086:** `POST /save` accepts JSON body with `path` and `content` strings
- **R1087:** Path must be within an indexed source directory — reject with 403 if not
- **R1088:** Writes file content, then triggers single-file refresh for immediate re-indexing
- **R1089:** (inferred) The watcher will also notice the change, but explicit refresh avoids debounce delay

### Set Tags Endpoint
- **R1090:** `POST /set-tags` accepts JSON body with `path` and `tags` (object of name→value pairs)
- **R1091:** Reads file, parses tag block, sets each tag via TagBlock.Set, writes file back
- **R1092:** When setting `status`, auto-sets `status-date` to today (YYYY-MM-DD) — same as Lua mcp.setTags and CLI `ark tag set`
- **R1093:** (inferred) The watcher picks up the file change for re-indexing

### GroupedChunk Enhancement
- **R1094:** Add `Content` (raw chunk text) and `ContentType` (strategy-derived type string) fields to `GroupedChunk` struct
- **R1095:** `SearchGrouped` populates `Content` from the already-available `SearchResultEntry.Text`
- **R1096:** `SearchGrouped` populates `ContentType` by mapping strategy name to type string (R1072 mapping)
- **R1097:** Existing Lua `search_grouped` gains `content` and `contentType` fields in chunk tables

### CORS
- **R1098:** (inferred) Editor endpoints share the same origin as the HTTP port UI — no explicit CORS headers needed unless serving from file:// origin

## Feature: Tag Value Index
**Source:** specs/tag-value-index.md

### V Record Structure
- **R1099:** V record key format: `V[tagname]\x00[value]` — null byte separates tag from value
- **R1100:** V record value: packed varint-encoded fileids (unsigned LEB128)
- **R1101:** One LMDB entry per unique (tag, value) pair — fileids accumulate in the value
- **R1102:** Count of files with a given (tag, value) = number of varints decoded from the value

### V Record Lifecycle
- **R1103:** On index/refresh: remove all V entries for the file's old fileids, then add V entries from freshly extracted tag values
- **R1104:** On append: add V entries for newly extracted tag values (no removal — appended tags are additive)
- **R1105:** On remove: remove the fileid from all V entries; delete the key if fileid list becomes empty
- **R1106:** `ExtractTagValues` (already called during index/refresh/append) provides the source data — no new extraction logic needed
- **R1107:** (inferred) V records are rebuilt from scratch by `ark rebuild`, same as T/F/D records

### V Record Queries
- **R1108:** Prefix scan `V[tagname]\x00` returns all values for a tag with counts
- **R1109:** Prefix scan `V[tagname]\x00[prefix]` filters values by prefix — LMDB sorted keys make this a range scan
- **R1110:** Direct key lookup `V[tagname]\x00[value]` returns fileids for a specific (tag, value) pair

### Endpoint Integration
- **R1111:** `POST /tags/values` switches from file-reading to V record queries — O(1) LMDB lookup instead of O(files) disk reads
- **R1112:** (inferred) Lua `mcp:tagComplete` should also use V records for value completion when wired

## Feature: Chunk Callback Tag Extraction
**Source:** specs/chunk-callback.md

### Callback Wiring
- **R1113:** Indexer passes `WithChunkCallback` to `AddFileWithContent` to receive clean chunk text during indexing
- **R1114:** Indexer passes `WithChunkCallback` to `ReindexWithContent` during full refresh
- **R1115:** Indexer passes `WithAppendChunkCallback` to `AppendChunks` during append refresh
- **R1116:** The callback accumulates chunk text slices for microvec embedding
- **R1117:** The callback extracts tag values from each chunk's clean text via `ExtractTagValues`
- **R1118:** The callback extracts tag defs from each chunk's clean text via `ExtractTagDefs`
- **R1119:** (inferred) The callback extracts tag counts via `TagCountsFromValues` on accumulated tag values

### Tag Merging
- **R1120:** Tag counts from multiple chunks are summed for the same tag name
- **R1121:** Tag values from multiple chunks are collected; Store deduplicates by fileid
- **R1122:** Tag defs from multiple chunks use last-writer-wins per tag name

### splitChunks Elimination
- **R1123:** `splitChunks` is removed from `AddFile` — callback provides chunk text
- **R1124:** `splitChunks` is removed from `executeFullRefresh` — callback provides chunk text
- **R1125:** `splitChunks` is retained in the append microvec path (needs all chunks for re-embedding)

### Prep/Execute Restructure
- **R1126:** `prepareRefresh` no longer extracts tags for full refresh — tags come from callback in `executeRefresh`
- **R1127:** `prepareRefresh` still extracts tags for append path using `tagWindowForAppend` (unchanged)
- **R1128:** (inferred) `refreshPrep.tags`, `.defs`, `.tagValues` fields are nil for full refresh, populated for append

### Tag Value Sort
- **R1129:** `ark tag values` output sorts by count descending (high-count values first)

## Feature: Tag Value File Filtering
**Source:** specs/tag-value-filtering.md

### Flags
- **R1131:** `ark tag values` accepts `--filter-files GLOB` (repeatable) to include only matching files
- **R1132:** `ark tag values` accepts `--exclude-files GLOB` (repeatable) to exclude matching files
- **R1133:** Both flags are composable: filter narrows first, exclude removes from the result
- **R1134:** Without either flag, behavior is unchanged

### Filtering Behavior
- **R1135:** When filtering is active, fileids are resolved to paths and matched against the globs
- **R1136:** Counts are recomputed from matching files only
- **R1137:** Values with zero matching files after filtering are omitted from output
- **R1138:** The `-files` flag composes with filtering — only files that passed the filter are shown

## Feature: Chunk Cache Threading
**Source:** specs/chunk-cache-threading.md

### Cache in Search Options
- **R1139:** When `SearchOpts.Cache` is non-nil, `defaultSearchOpts` appends `microfts2.WithChunkCache(opts.Cache)` to the search options slice
- **R1140:** When `SearchOpts.Cache` is nil, no `WithChunkCache` option is appended — microfts2 auto-creates a per-search cache internally (backwards compatible)
- **R1141:** (inferred) All search paths that call `defaultSearchOpts` — SearchCombined, SearchSplit, SearchMulti, SearchFuzzy — gain cache threading without signature changes

## Feature: Inbox from V Records
**Source:** specs/inbox-v-records.md

### New Store Method
- **R1142:** `Store.FileTagValues(fileid uint64, tags []string) (map[string]string, error)` returns the first value found per tag by scanning V records for the fileid
- **R1143:** For each requested tag, scans V record prefix `V[tag]\x00` entries checking if fileid is in the varint list
- **R1144:** (inferred) Returns empty string for tags with no value for the fileid — callers treat missing values as absent, not errors

### Inbox Rewrite
- **R1145:** `DB.Inbox` uses `TagFiles(["status"])` for candidate fileids and path resolution (unchanged)
- **R1146:** `DB.Inbox` filters to `/requests/` paths before per-file tag lookup (unchanged)
- **R1147:** `DB.Inbox` calls `Store.FileTagValues` instead of `os.ReadFile` + `ParseTagBlock` for each candidate
- **R1148:** When `showAll` is false, `DB.Inbox` uses `TagValueFiles("status", "completed")` and `TagValueFiles("status", "denied")` to build an exclusion set before per-file tag lookup
- **R1149:** (inferred) InboxEntry fields are populated from the map returned by `FileTagValues` — same field mapping as current code
- **R1150:** (inferred) Existing Inbox output, sort order, and filtering behavior are preserved — this is a performance change, not a behavior change

## Feature: Content Fetching
**Source:** specs/content-fetching.md

### Route Registration
- **R1151:** Routes are registered on the UI server (HTTP port) via `Runtime.UIHandleFunc()` after the UI engine starts
- **R1152:** Handlers need access to the DB actor for `IsIndexed` checks and file content reads
- **R1153:** (inferred) Routes are only available when the UI engine is running — no fallback on the unix socket API mux

### Path Validation
- **R1154:** All three routes validate that the requested path is within an indexed source directory (not that the file itself is indexed — non-indexed assets like images are allowed)
- **R1155:** Paths are cleaned via `filepath.Clean` and must be absolute
- **R1156:** Paths outside all configured source directories return 403, missing files return 404

### JSON Content Retrieval — `/fetch/PATH`
- **R1157:** `GET /fetch/PATH` returns file content as JSON with `path`, `content`, and `contentType` fields
- **R1158:** `contentType` is derived from the file's indexing strategy using the same mapping as editor endpoints (markdown, text, json, code)
- **R1159:** (inferred) This is the programmatic access point — JavaScript/HostAPI code fetches content without POST body encoding

### Rich Presentation — `/content/PATH`
- **R1160:** `GET /content/PATH` returns an HTML page that presents the file based on its content type
- **R1161:** Markdown files return an HTML shell that loads the CM6 editor bundle (`ark-markdown-editor.js`)
- **R1162:** The shell fetches content from `/fetch/PATH` and creates an ArkEditor with a HostAPI wired to the editor HTTP endpoints
- **R1163:** Non-markdown files return a minimal HTML page with raw content in a `<pre>` block
- **R1164:** (inferred) Response Content-Type is `text/html` for all `/content/` responses

### Raw Content — `/raw/PATH`
- **R1165:** `GET /raw/PATH` returns file content verbatim with an appropriate Content-Type header
- **R1166:** Content-Type is mapped from file extension (text/markdown, text/plain, application/json, etc.)
- **R1167:** (inferred) No wrapping, no JSON encoding — raw bytes suitable for download, curl, or iframe embedding

## Feature: content-view-edit
**Source:** specs/content-view-edit.md

### Read View (default)
- **R1168:** `/content/PATH` for markdown files renders HTML via goldmark on the server (supersedes R1161-R1162 for `/content/` route)
- **R1169:** Rendered HTML appears in a scrollable content area within the page
- **R1170:** Relative image `src` attributes are rewritten to `/raw/BASEDIR/src`
- **R1171:** Relative link `href` attributes ending in `.md` are rewritten to `/content/BASEDIR/href`
- **R1172:** Absolute paths and external URLs in links/images are left unchanged
- **R1173:** BASEDIR is the directory portion of the requested file's absolute path
- **R1174:** A pencil icon button is positioned at the upper right of the page
- **R1175:** Clicking the pencil button switches to Edit View

### Edit View
- **R1176:** On pencil click, raw markdown is fetched from `/fetch/PATH`
- **R1177:** An ink-mde editor instance is created with ark extensions (tag parser, tag widgets, tag completion, search blocks)
- **R1178:** The editor replaces the rendered content area
- **R1179:** The pencil button becomes an eye icon
- **R1180:** The editor wires to the same HostAPI endpoints as the existing CM6 shell (`/search/grouped`, `/tags/complete`, `/tags/values`, `/file/save`, `/tags/set`)
- **R1181:** Ctrl+S saves via the HostAPI

### Returning to Read View
- **R1182:** Clicking the eye button checks whether the document has been modified since last save
- **R1183:** If dirty, a prompt offers Save / Discard options
- **R1184:** Save: saves via HostAPI, then reloads the page for fresh goldmark rendering
- **R1185:** Discard: reloads the page without saving
- **R1186:** If not dirty: reloads the page

### Bundle Changes
- **R1187:** The `ark-markdown-editor.js` bundle exports `createInkArkEditor` alongside the existing `createArkEditor`
- **R1188:** The `/content/` HTML shell loads the bundle and calls `createInkArkEditor` on pencil click
- **R1189:** (inferred) Non-markdown `/content/` behavior is unchanged (R1163 still applies)

### Tag Line Rendering
- **R1190:** `TagBlock.Render()` emits two trailing spaces before newline on each tag line for markdown line-break rendering
- **R1191:** `ParseTagBlock` trims trailing spaces from tag values to prevent accumulation on round-trip
- **R1192:** `NormalizeTagLines` rewrites any `@tag: value` line in content to end with exactly two trailing spaces
- **R1193:** `handleSave` normalizes tag lines before writing to disk
- **R1194:** `renderMarkdownForContent` normalizes tag lines before goldmark rendering (safety net for hand-edited files)
- **R1195:** The editor JS normalizes tag lines before loading into ink-mde and sets dirty state if content changed

### Content Template Externalization
- **R1196:** Content HTML shells are loaded from `~/.ark/html/` at request time, not compiled into the binary
- **R1197:** `content-markdown.html` and `content-plain.html` are Go `html/template` files with `{{.Title}}` and `{{.Content}}` placeholders
- **R1198:** (inferred) CSS edits to content templates take effect on browser reload without rebuilding the binary
- **R1199:** Content templates include the current theme CSS (base + all theme files) and set the active theme class from localStorage

## Feature: tag-search-panel
**Source:** specs/tag-search-panel.md

### Query Bar
- **R1200:** Clicking ▶ on a tag widget opens a search panel inline below the tag line
- **R1201:** Clicking ▶ on a tag with an already-open panel closes it (toggle)
- **R1202:** The query bar contains three parts: tag name field, regex toggle, value field
- **R1203:** The tag name field is pre-filled with the clicked tag name and is editable
- **R1204:** The tag name field supports autocompletion from the tag index
- **R1205:** The regex toggle button shows `.*` when regex mode is active, plain text icon otherwise
- **R1206:** The value field filters tag content — typing narrows results (spectral narrowing)
- **R1207:** Search fires on Enter or after a short debounce as the user types
- **R1208:** The search query is constructed as `@tag: value` for literal mode
- **R1209:** (inferred) In regex mode, the search uses regex matching on tag values

### Results Area
- **R1210:** Results appear below the query bar in a scrollable area
- **R1211:** Results are grouped by file, styled like search engine results
- **R1212:** Each file group shows the file path as a clickable link navigating to `/content/PATH`
- **R1213:** Each file group has a "show location" button (folder icon) that opens the native file manager
- **R1214:** Chunk previews are rendered as HTML — markdown chunks via goldmark, code as `<pre>`
- **R1215:** The panel is resizable by dragging its bottom edge

### Show in Folder
- **R1216:** A new HostAPI method `showInFolder(path)` calls `POST /file/show`
- **R1217:** `POST /file/show` opens the native file manager with the file selected
- **R1218:** Linux: uses `gdbus call` with `org.freedesktop.FileManager1.ShowItems`
- **R1219:** macOS: uses `open -R <path>`
- **R1220:** Windows: uses `explorer.exe /select,"<path>"`
- **R1221:** The endpoint validates the path is within an indexed source

### Integration
- **R1222:** The search panel component is a standalone TypeScript module usable in both CM6 and ink-mde
- **R1223:** `TagSearchWidget` in `tag-widget.ts` creates/toggles the search panel instead of fire-and-forget search
- **R1224:** (inferred) The search panel reuses the existing `renderSearchResults` for result display

### Search Precision
- **R1225:** Tag search always uses regex mode — constructs `@tag:\s*value` pattern for precise tag-value matching
- **R1226:** Literal mode escapes the value with regexp.QuoteMeta equivalent before constructing the regex
- **R1227:** Invalid tag names in literal mode show a red border and tooltip error
- **R1228:** `handleSearchGrouped` supports `mode: "regex"` routing to `opts.Regex` / `SearchRegex`
- **R1229:** Multi-strategy search guard excludes regex queries (`len(opts.Regex) == 0`)
- **R1230:** Regex search highlights use the raw pattern directly instead of tokenize-and-escape

### Performance and Infrastructure
- **R1231:** `loadContentTemplate` calls `srv.db.Path()` directly, not through the DB actor (immutable value)
- **R1232:** Content templates are patched on disk at startup by `flib.InjectAllThemeBlocks` — no per-request theme injection
- **R1233:** JS bundle imports use cache-busting `?v=mtime` query parameter via `{{.BundleHash}}` template field
- **R1234:** `install/html/` contains canonical content templates with `<!-- #frictionless -->` markers, copied to cache by Makefile

## Feature: Spectral Search
**Source:** specs/spectral-search.md

### Haiku Session
- **R1235:** The server manages Haiku interactions via `claude --print --model haiku --output-format json` invocations
- **R1236:** Each invocation uses `--system-prompt-file ~/.ark/searching/CLAUDE.md --tools ""`
- **R1268:** `--system-prompt-file` replaces all default Claude Code instructions — the Librarian is a specialized oracle, not a general assistant
- **R1269:** `--tools ""` disables all tool access — the Librarian only generates text responses
- **R1237:** Conversation context persists via `--resume SESSION_ID` — the session ID from the first invocation is stored and reused
- **R1238:** Two spawns per expansion: one for expand (step 1), one for curate (step 3). Claude's prompt caching pays system prompt tokens once per session.
- **R1239:** The session ID expires after a TTL with no requests — next expansion starts a fresh conversation
- **R1240:** (inferred) A fresh session creates a new conversation context, paying cache creation tokens again
- **R1241:** (inferred) If a claude invocation fails, the session ID is cleared and the next request starts fresh
- **R1242:** (inferred) The Librarian is managed by a closure actor to serialize access from concurrent HTTP handlers

### Expansion Pipeline
- **R1243:** `POST /search/expand` accepts JSON body with `mode`, `tag`, `value` fields
- **R1244:** Returns JSON `{results: [{path, strategy, chunks, source: "expansion"}]}` — curated search results marked as expansion-sourced
- **R1245:** The pipeline runs server-side in three steps: Haiku expands → search → Haiku curates
- **R1246:** For tag mode (Phase A): step 2 is trigram fuzzy matching against V records (tag-value index in LMDB)
- **R1270:** Haiku expand step: given user's tag name and value, suggests alternative tag names and values
- **R1271:** Fuzzy match step: each alternative is fuzzy-matched against V records, producing (tag, value, count, score) tuples
- **R1272:** Haiku curate step: sees matched tag/value pairs with scores, prunes false positives, returns curated subset
- **R1273:** Server fetches actual search results for the curated tags before returning to the client
- **R1247:** (inferred) If the co-process is unavailable (not on PATH, spawn failure), the endpoint returns 503

### Availability
- **R1248:** Server checks for `claude` on PATH at startup
- **R1249:** `GET /status` includes `spectral: true/false` capability flag
- **R1250:** (inferred) The check is a one-time `exec.LookPath("claude")` at startup, not per-request

### Searching Directory
- **R1251:** `~/.ark/searching/CLAUDE.md` contains the system prompt for the Haiku expansion session
- **R1252:** `ark init` creates `~/.ark/searching/` and writes a default `CLAUDE.md` if the directory doesn't exist
- **R1253:** The CLAUDE.md file is read at co-process spawn time via the `--system-prompt-file` flag
- **R1254:** (inferred) Changes to CLAUDE.md take effect on next co-process spawn (after TTL expiry or crash)

### Two-Phase Results (UI)
- **R1255:** Phase 1: literal search fires immediately on user input (existing behavior, ~300ms debounce)
- **R1256:** Phase 2: when spectral mode is on, an expansion request fires after a longer debounce (~1-2 seconds)
- **R1257:** Phase 2 results are interspersed among Phase 1 results, not shown in a separate section
- **R1258:** Phase 2 results are visually highlighted (accent color border or background tint) to distinguish from literal matches
- **R1259:** Phase 2 results height-transition in to avoid jarring layout shifts
- **R1260:** If expansion returns no new results beyond what Phase 1 found, no visual change occurs
- **R1261:** A new keystroke cancels any in-flight expansion request

### Toggle
- **R1262:** A button in the search bar toggles spectral expansion on/off
- **R1263:** Default state is off
- **R1264:** Toggle state persists in localStorage
- **R1265:** If `spectral: false` in server status, the toggle button is hidden

### Content Template Scrolling
- **R1266:** `content-markdown.html` sets `overflow: auto !important` on `html, body` to override theme `overflow: hidden`
- **R1267:** (inferred) Theme CSS sets `overflow: hidden` on `html, body` for the Frictionless single-page app; standalone pages like `/content/` need to opt out

## Feature: Tag Value Embeddings
**Source:** specs/tag-embeddings.md

### Model Configuration
- **R1274:** `tag_model` field in ark.toml specifies the GGUF embedding model filename
- **R1275:** The path is relative to the database directory (`~/.ark/`)
- **R1276:** If `tag_model` is empty or the file doesn't exist, embedding is disabled — trigram fuzzy is the fallback
- **R1277:** The model is loaded by the Librarian on first embedding query
- **R1278:** The model stays warm in memory; unloaded on TTL expiry with no queries
- **R1279:** (inferred) Next query after TTL expiry reloads the model

### Tag Value IDs
- **R1280:** Each unique (tag, value) pair gets a sequential tag-value-id (varint)
- **R1281:** The tag-value-id is part of the V record key: `V[tag]\x00[value]\x00[tvid: varint]` → packed fileids
- **R1282:** The ID counter (`next_tvid`) is stored as an ark LMDB setting (`I` prefix)
- **R1283:** The tag-value-id is stable: assigned on first index, reused if the same (tag, value) pair persists
- **R1284:** (inferred) On rebuild, tag-value-ids are reassigned from 1
- **R1309:** Forward lookup: prefix scan `V[tag]\x00[value]\x00` returns one record with tvid in key suffix
- **R1310:** Reverse lookup: scan V prefix, parse tvid from trailing bytes of each key

### F Record TVIDs
- **R1311:** F record value is extended: `count:4bytes + packed tvid varints` for all tag-value pairs of that tag in that file
- **R1312:** On file removal or re-index, read F records for the fileid to get all tvids
- **R1313:** Remove fileid from exactly those V records identified by F-record tvids (targeted cleanup)
- **R1314:** (inferred) Targeted V cleanup replaces the current full-scan approach in `removeFileidFromAllV`

### What Gets Embedded
- **R1285:** Tag names are embedded with hyphens converted to spaces (`design-decision` → "design decision")
- **R1286:** Tag-value compounds are embedded as `"tagname: value"` with colon preserved and hyphens in tag name converted to spaces
- **R1287:** (revised) Tag names are keyed directly by name in ET records — no tag-name-id needed
- **R1288:** (revised) Hyphens→spaces conversion applies to both ET and EV embedding text for word-level semantics

### Embedding Storage
- **R1289:** (revised) Tag name embeddings are stored inline in T records: `T[tag_name]` → `count:4bytes + optional float32 vector (3072 bytes)`. No separate ET prefix.
- **R1290:** EV records store tag-value compound embeddings: key `EV[tvid: varint]`, value raw float32 vector (3072 bytes)
- **R1291:** (revised) Only EV uses a two-byte prefix. Tag name embeddings are inline in the existing T prefix.

### Embedding Lifecycle
- **R1292:** Batch embed after reconcile: scan V and T records that lack corresponding E records, embed in the write goroutine
- **R1293:** Incremental: new V records created during indexing are queued for embedding; the next reconcile batch picks them up
- **R1294:** `ark rebuild` drops and regenerates all ET and EV records alongside V records
- **R1295:** (inferred) The embedding batch runs in the write goroutine to avoid blocking the main actor

### Query Path
- **R1296:** Embed the query string with the warm model (~50ms)
- **R1297:** (revised) Two-step query: cosine scan T record embeddings (~270) to find top-K tags, then cosine scan EV records only for those tags to find top-K (tag, value, score) tuples
- **R1315:** Tag-level narrowing reduces EV scan from ~3857 to ~50-100 records
- **R1316:** (inferred) Tag embedding score can weight the final tag-value result
- **R1298:** Results have the same shape as FuzzyMatchTags output — drops into the existing Librarian pipeline
- **R1299:** The Librarian offers both trigram fuzzy (no model) and embedding similarity (with model)
- **R1300:** The `--fuzzy` CLI flag gains an `--embed` counterpart; the HTTP fuzzy endpoint accepts a `mode` parameter
- **R1301:** (inferred) When both are available, embedding is the default with trigram as fallback

### CLI
- **R1302:** `ark embed TEXT` embeds a text string and prints the vector as JSON
- **R1303:** `ark embed --bench tags` embeds all tag values, reports per-value and total timing
- **R1304:** `ark embed --bench chunks` reads chunks from random indexed files, embeds them, reports timing
n- **R1305:** (inferred) `ark embed` requires a running server (model lives in the Librarian)

### Build
- **R1306:** The Vulkan build of gollama avoids SIGILL on Zen 2 (Steam Deck)
- **R1307:** The go workspace includes a local gollama with Vulkan-compiled llama.cpp
- **R1308:** (inferred) For non-Zen 2 platforms, the standard CPU gollama build should work without Vulkan

### Use vs Mention Filtering
- **R1317:** Mentioned tags are skipped entirely during extraction — no V, T, F, or EV records
- **R1318:** Only annotation (non-mentioned) tags produce V, T, F, and EV records
- **R1319:** The check runs during tag extraction in ExtractTags and ExtractTagValues
- **R1320:** Heuristic 1 (all strategies): a `@` not at line start and not preceded by whitespace is not a tag (embedded in a larger token, e.g. email address)
- **R1321:** Heuristic 2 (all strategies): count backtick and double-quote characters before the `@` on the same line; odd count = mention (inside quotes), even/zero = annotation
- **R1322:** Heuristic 3 (markdown strategy only): tags inside fenced code blocks (``` or ~~~) are mentions. Track fence state across lines within the chunk.
- **R1323:** Heuristic 4 (markdown strategy only): lines starting with 4+ spaces or a tab are indented code blocks; tags on these lines are mentions
- **R1324:** (inferred) Heuristics are applied in order; if any matches, the tag is skipped
- **R1325:** (inferred) Heuristics 1 and 2 apply to all indexing strategies; heuristics 3 and 4 apply only to the markdown strategy
