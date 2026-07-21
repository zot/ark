# Requirements

## Feature: ark
**Source:** specs/main.md

### Language and Environment

- **R1:** Ark is written in Go
- **~~R2:~~** (Retired T18 — no replacement) Ark uses microfts2 (trigram) and microvec (vector) as library dependencies
- **R3:** Index access is via microfts2's shared `*bbolt.DB`
- **R4:** Server communication uses Unix domain sockets

### Shared `*bbolt.DB`

- **~~R5:~~** (Retired T19 — no replacement) Ark opens microfts2 first (creates LMDB env), then passes the env to microvec
- **R6:** Ark opens its own named bucket for metadata (missing files, unresolved files, settings)
- **~~R7:~~** (Retired T27 — see R1911) MaxDBs is set to 8 (microfts2: 2, microvec: 1, ark: 1+, room to grow)

### Source Configuration

- **R8:** Config has three levels: directories, include patterns, exclude patterns
- **R9:** A file must match at least one include pattern to be indexed
- **R10:** When both include and exclude match the same file, include wins — no specificity ranking
- **R11:** Identical include and exclude strings are a config error, reported on every operation until resolved
- **~~R12:~~** (Retired T54 — see R2143) Global include/exclude patterns apply to all directories
- **~~R13:~~** (Retired T55 — see R2144) Per-source include/exclude are additive — combine with global, not replace
- **R14:** Each source directory may optionally specify a `strategies` map (glob pattern → chunking strategy name) that amends the global strategies table for files in that source
- **R15:** Files matching no include or exclude pattern are held in an "unresolved" list — no automatic indexing
- **R16:** Pattern language uses doublestar glob syntax (github.com/bmatcuk/doublestar/v4). Trailing `/` means directory-only; no trailing `/` means file-only. These are ark-level semantic modifiers on top of doublestar matching.
- **R17:** `*` matches within a path component, `**` matches zero or more path components (must appear between separators or at pattern boundaries). Mid-pattern `**` without separators (e.g. `**.md`) acts as single `*` — use `**/*.md` for recursive matching.
- **R18:** `*` and `**` match dotfiles by default (controlled by `dotfiles` config, default true)
- **R19:** Standard glob wildcards (`?`, `[abc]`, `{alt1,alt2}`) are supported
- **R20:** Backslash escapes literal wildcard characters (`\*`, `\?`, `\[`)
- **~~R21:~~** (Retired T48 — see R2133) Patterns without leading `/` match at any depth; with leading `/` anchored to watched directory root
- **R22:** `ark init` ships default excludes for `.git/`, `.env`, etc.
- **R2954:** Explicit `ark add` of a single file with no `--strategy` resolves the chunking strategy from the file's enclosing source via `StrategyForFile` (per-source map over global, default `lines`), matching the directory-walk resolution — not the empty strategy passed straight to microfts2.
- **R2955:** Adding a file outside every configured source with no `--strategy` is a client error (no source to resolve against) reported as HTTP 400, not a server 500; an explicit `--strategy` is always honored regardless of source membership.

### Config File

- **R23:** Config file is TOML format, named `ark.toml`
- **R24:** Config file lives in the database directory
- **R25:** Config has global `dotfiles` setting (default true)
- **~~R26:~~** (Retired T56 — see R2145) Config has global `include` and `exclude` pattern arrays
- **R27:** Config has `[[source]]` entries with `dir`, optional `strategies` map, and optional `include`/`exclude`

### Database Directory

- **R28:** Ark stores everything in one directory: the index, `ark.toml`, Unix socket
- **R29:** Default database directory is `~/.ark/`, overridden via `--dir` flag

### Version

- **R2960:** `ark version` prints the build version as `ark <version>`. The version is the `**Version: X.Y.Z**` line in `README.md`, injected at build time by the Makefile via `-ldflags "-X github.com/zot/ark.Version=..."` (full module path); a plain `go build` leaves the `ark.Version` var as `dev`.

### Init

- **~~R30:~~** (Retired T28 — see R1912) `ark init` creates a new database: initializes microfts2, microvec, ark subdatabase, and writes default config
- **R31:** microfts2 is initialized with character set, case insensitivity, and aliases
- **~~R32:~~** (Retired T20 — no replacement) microvec is initialized with embedding command
- **R33:** Character set, embedding command, and aliases are immutable after creation
- **R34:** Newline alias maps `\n` to `\x01` (SOH) for line-start matching in queries

### Add Files

- **R35:** Add walks source directories per config
- **R36:** Files are checked for staleness via microfts2 and skipped if fresh
- **R37:** Files are added to microfts2 first (returns fileid and chunk offsets)
- **R38:** Chunk text is read from the file using offsets returned by microfts2
- **~~R39:~~** (Retired T21 — no replacement) Chunks are added to microvec using the fileid from microfts2
- **~~R40:~~** (Retired T22 — no replacement) microfts2 is the source of truth for file identity — microvec receives fileids from it

### Remove Files

- **R41:** Remove takes a file path (or glob pattern), removes from both engines
- **~~R42:~~** (Retired T23 — no replacement) microfts2 resolves path to fileid, microvec removes by fileid

### Refresh

- **R43:** Refresh re-indexes stale files using microfts2 staleness detection (modtime + content hash)
- **~~R44:~~** (Retired T24 — no replacement) For each stale file: re-add to microfts2, remove old vectors from microvec, add new vectors
- **R45:** Missing files are not auto-deleted — added to ark's missing files list for review

### Combined Search

- **R46:** Combined search sends the same query text to both engines
- **R47:** microfts2 returns file/chunk matches with trigram scores
- **~~R48:~~** (Retired T29 — see R1915) microvec returns file/chunk matches with cosine similarity scores
- **R49:** Results are merged by (fileid, chunknum), combining scores
- **R50:** Results are sorted by combined score descending
- **R51:** Output format: filepath:startline-endline with score

### Split Search

- **~~R52:~~** (Retired T30 — see R1916) `--about <text>` sends query to microvec only (semantic search)
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

### Ark Bucket

- **~~R103:~~** (Retired T154 — see R2975) LMDB subdatabase named `ark`
- **R104:** `M` prefix + fileid (8 bytes) → JSON missing file record (path, lastSeen timestamp)
- **R105:** `U` prefix + path bytes → JSON unresolved file record (path, firstSeen timestamp, dir)
- **R106:** Unresolved files that no longer exist on disk are removed silently during scans
- **~~R107:~~** (Retired T221 — see R1571) `I` key → JSON ark-level settings (sourceConfig, dotfiles boolean)

## Feature: Chunk Retrieval
**Source:** specs/search.md

### Chunk Retrieval

- **R108:** `ark search --chunks` emits chunk text as JSONL instead of range output
- **R109:** `ark search --file-content` emits full file content as JSONL for each matching file
- **R110:** `--chunks` and `--file-content` are mutually exclusive — error if both provided
- **R111:** (inferred) `--file-content` deduplicates by file — multiple chunk hits from one file emit the file content once
- **R112:** (inferred) JSONL schema for `--chunks`: `{"path":"...","range":"...","score":F,"text":"..."}` (with `--preview N`, `text` is omitted and a `preview` field carries the window)
- **R113:** (inferred) JSONL schema for `--file-content`: `{"path":"...","score":F,"text":"..."}`  — score is the best chunk score for that file
- **R114:** `--chunks` and `--file-content` work with combined search, split search, and tag search
- **R115:** Chunk retrieval enables permission end-run: indexed file content emitted without per-file permission
- **R116:** Chunk retrieval works without an embedding model (FTS-only and tag search)

## Feature: Tag Tracking
**Source:** specs/tags.md

- **R117:** @tags are extracted from file content during add and refresh operations
- **R118:** Tag extraction regex: `@[a-zA-Z][\w-]*:` — @ followed by letter, word chars or hyphens, then colon (colon disambiguates from emails/mentions)
- **R119:** `T` prefix + tagname bytes → uint32 count (total occurrences across all files)
- **~~R120:~~** (Retired T218 — see R1874) `F` prefix + fileid (8 bytes) + tagname bytes → uint32 count (occurrences in that file)
- **R121:** Tag counts are recomputed on refresh — old counts for a file are removed, new counts stored
- **R122:** `ark tag list` shows all known tags with their total counts
- **R123:** `ark tag counts <tag>...` shows the total count for each specified tag
- **R124:** `ark tag files <tag>...` shows files containing the specified tags with file size
- **R125:** `ark tag files --context <tag>...` shows each tag occurrence with the line from tag to end-of-line — includes definitions from tags.md alongside usage
- **~~R126:~~** (Retired T11 — see R1899) (inferred) When a file is removed, its tag counts are decremented and its F records deleted

### Tag source parity

- **R2344:** Tag source parity — read APIs that enumerate tag names, tag values, tag counts, or per-target tag sets union all three tag sources: inline (T/F/V records), ext-routed virtual (ExtMap, including overlay routings from tmp:// source files), and tmp:// overlay (TmpTagStore). Tag definitions (D records) are exempt because virtual and overlay tags have no defining text. Read APIs documented as opting out of the union (e.g., `Store.TagsForChunk` strictly inline) must have a parallel "all-sources" variant.
- **R2345:** `Store.ListTags` returns tags from all three sources, with counts summed across sources.
- **R2346:** `Store.TagCounts(tags)` includes tmp:// overlay counts alongside inline + ext-routed counts.
- **R2347:** `Store.QueryTagValues(tag, prefix)` includes ext-routed values and tmp:// overlay values, not only inline V records.
- **R2348:** `Store.FileTagValues(fileid, tags)` includes ext-routed virtual values targeting the file's chunks and tmp:// overlay values; symmetric for persistent and overlay fileids.
- **R2349:** `Store.MatchTagNames(tokens)` matches against tag names from all three sources.
- **R2350:** `Store.MatchTagValues(tag, tokens)` matches against tag values from all three sources.
- **R2351:** `Store.AllTagsForChunk(chunkID)` unions inline + ext-routed + tmp:// overlay tag pairs at the chunk. `Store.TagsForChunk` retains its inline-only contract by name; documentation points to AllTagsForChunk for the canonical union.
- **R2352:** `ExtMap.VirtualTagNames()` and `ExtMap.VirtualTagValues(tag)` enumerate ext-routed tag names and (name, value) entries respectively, covering routings from inline X records and overlay (tmp://) sources.
- **R2353:** `TmpTagStore.TagNames()`, `TagValuesForTag(tag)`, and `TagCounts(tags)` enumerate names, values, and counts for overlay tags.
- **R2354:** `TmpTagStore.FileTagValues` and `TmpTagStore.TagsForChunk` (when reached via overlay-ID dispatch) union ExtMap-routed virtual tags targeting the overlay chunks, with parity to the persistent-ID path.

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
- **R503:** Definitions are extracted at index time and cached in the index as `D` prefix records
- **R504:** Storage: `D` [tagname] [fileid: 8] → description bytes. One record per definition per source file
- **R505:** When a file is re-indexed, its D records are removed and re-extracted (same lifecycle as F records)
- **R506:** `ark tag defs [TAG...]` outputs tag definitions from the index
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
- **R2993:** For ordinary (non-`tmp://`) paths, `ark fetch` proxies to a running server via `POST /fetch` when one is reachable, and opens the index locally (`withDB`/`db.Fetch`) only when no server is running. The index is single-process (bbolt file lock), so a local open while the server holds the DB would block indefinitely; proxying when the server is up is what keeps `ark fetch` from hanging. Mirrors R510 (`tag defs`).

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
- **~~R191:~~** (Retired T226 — see R2433) Output: one tag per line with count (how many result chunks contained it)
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
- **R219:** Content filters are pushed to microfts2 as file ID sets via WithOnly (positives intersected and negatives subtracted into the set before it is passed)

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
- **R515:** Tag filters use the tag index (T records via Store.TagFiles) — no FTS query or chunk scanning needed
- **R516:** (inferred) Tag filters evaluate after path filters and before content filters in the resolveFilters pipeline

### Composition
- **R225:** All filters produce file ID sets: positive filters intersect, negative filters subtract
- **R226:** Evaluation order: path filters first (cheap), then content filters
- **R227:** The combined file ID set is passed to microfts2 as WithOnly; negative filters are subtracted from the set before it is passed, so the negatives-only case still uses WithOnly rather than a separate WithExcept
- **R228:** Search filtering works with SearchCombined, SearchSplit, and tag search
- **R229:** (inferred) Filter fields pass through the server proxy via searchRequest JSON

### Default Search Excludes
- **R938:** `search_exclude` is a top-level list of glob patterns in ark.toml
- **R939:** `search_exclude` patterns apply as the default exclude scope when the search carries no explicit file filter — no positive `-files GLOB` row and no structural `filter_files`/`exclude_files` (the `SearchOpts` fields the Lua UI sets). (Flag rename: the user-facing `--filter-files`/`--exclude-files` were subsumed by the filter stack's `-files` / `-without -files` — see search-cli-filters.md; the `filter_files`/`exclude_files` struct fields and JSON remain for the Lua UI.)
- **R940:** An explicit file filter replaces the default scope entirely: a **positive** `-files GLOB` row (or a structural `filter_files`/`exclude_files`) disables `search_exclude`, so a positive `-files` pointed at a default-excluded path includes it. A `-without -files GLOB` subtracts the glob *without* disabling the default scope. Implemented by `Searcher.effectiveExcludeFiles` (R2951's funnel injects the same default on the index-lookup paths).
- **R941:** Subscriptions without explicit file filters inherit `search_exclude` as their exclude-files list
- **R942:** Subscriptions with explicit `--filter-files` or `--exclude-files` use those instead of `search_exclude`
- **R943:** (inferred) `search_exclude` is loaded from config at startup and on config reload

### Naming Normalization
- **R944:** Pubsub `--except-files` CLI flag is renamed to `--exclude-files` for consistency with search
- **R945:** Pubsub `ExceptFiles` struct field is renamed to `ExcludeFiles`
- **R946:** Pubsub JSON wire format `except_files` is renamed to `exclude_files`

### Replaces Source Filtering
- **~~R230:~~** (Retired T177 — no replacement) `--source` and `--not-source` flags are removed — replaced by `--filter-files` and `--exclude-files`
- **R231:** (inferred) No backward compatibility shim needed — flags are not in use outside testing

## Feature: Config Flag Parsing Bug
**Source:** specs/config-flag-bug.md

- **R232:** Config mutation subcommands must parse flags correctly when positional args precede optional flags
- **R233:** Fix: reorder args so flags come first before calling `fs.Parse`, or document flags-first convention
- **R234:** Affected subcommands: `add-include`, `add-exclude`, `remove-pattern` — any with positional arg + optional `--source` flag
- **R235:** (inferred) Add a test that verifies per-source add-include round-trips correctly through the CLI arg parsing path

## Feature: Content-Aware JSONL Chunker
**Source:** specs/indexing.md

- **R236:** The `chat-jsonl` strategy is a Go func chunker registered with microfts2 on both Init and Open
- **~~R237:~~** (Retired T57 — see R2271) Each JSONL line is parsed as JSON; lines with no extractable text produce no chunk
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
- **R2271:** Every non-empty JSONL line that isn't covered by an explicit filter (R240, R241, R242, R243) produces exactly one chunk. The chunker must not silently drop user-searchable content; "searchable beats invisible" is the governing principle for indexing.
- **R2272:** When text extraction yields no content (parseable JSON whose shape doesn't match the text/thinking/string extractor, malformed JSON, partial JSON at the tail of a growing file), the chunk's content is the raw line bytes. The line is searchable as JSON text even when the extractor can't lift human-readable content out of it.
- **R2273:** The `chat-jsonl` strategy is registered as a struct implementing `microfts2.Chunker` and `microfts2.AppendAwareChunker`. The struct is empty (no per-instance state); per-file resume state lives in the chunk locator stored in microfts2's F record.
- **R2274:** `chat-jsonl.AppendChunks(path, lastLocator, newBytes, yield)` re-chunks from the byte offset encoded in `lastLocator` through end-of-file. The first emitted chunk's byte range determines `replacedLast`: same range as the previous last chunk → clean boundary (drop the emitted chunk, don't yield it, return `replacedLast=false`); different range (typically extension because a partial-line chunk now has more content) → yield as replacement, return `replacedLast=true`.
- **R2275:** Each chunk's `Locator` carries the chunk's byte range in the file (encoded via `microfts2.EncodeByteRangeLocator`). The `Range` field continues to carry the line-number range for display and traceability (R244); the locator is internal resume state for AppendAware.

## Feature: Enhanced Status
**Source:** specs/status-enhanced.md

- **~~R249:~~** (Retired T155 — see R2982) `ark status` reports LMDB map usage: used bytes, total map size, and percentage
- **R250:** Map usage is displayed in human-readable units (MB/GB)
- **~~R251:~~** (Retired T156 — see R2982) Map usage is computed from LMDB env info: (LastPNO + 1) * PageSize = used bytes
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
- **R261:** `~/.ark/` contains both ark data (index.db, ark.toml, ark.sock, logs/) and UI assets (html/, lua/, viewdefs/, apps/)
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
- **R330:** (inferred) `--if-needed` checks for `index.db` in the database directory

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
- **~~R342:~~** (Retired T49 — see R2138) A Reconcile method encapsulates the startup reconciliation cycle: sources-check → scan → refresh
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
- **R2952:** Watch coverage equals scan coverage. The recursive watch descends into exactly the directory set the Scanner walks: a subdirectory is watched iff `Classify` (with `isDir=true`, respecting `dotfiles` and the source's effective include/exclude) does not mark it Excluded — the same rule the Scanner uses to descend. The watcher and Scanner share one predicate (`DB.IsWatchableDir`), so neither descends a directory the other skips. With `dotfiles=true`, a non-excluded dot-directory (e.g. `.scratch/`) is watched (the Scanner indexes files inside it), while a directory excluded as a directory (e.g. `.git/`) is skipped by both. Refines R350; the prior recursive watch unconditionally skipped every dot-prefixed subdirectory, so files under `.scratch/` never auto-indexed.
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
- **R390:** Directory creation events bypass the *file*-indexability check — a new directory is watched without any file matching an include pattern yet (it is Unresolved, and may later hold indexable files). It is still subject to the directory rule (R2952): a new directory excluded *as a directory* (e.g. `node_modules/`) is not watched.
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
- **R3008:** The watermark stored after an append-only change (stored file length + content hash) is derived from one content snapshot: the stored length equals the length of the bytes hashed and chunked, not a separately-stat'd file size — so a concurrent append during indexing cannot desynchronize the (length, hash) pair (which would force a needless full reindex) or leave the interleaved bytes unchunked
- **~~R369:~~** (Retired T219 — see R2273) (inferred) Strategies can report whether they produce clean chunk boundaries (line-based and JSONL always do)

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
- **~~R385:~~** (Retired T220 — see R2273) (inferred) Append detection derives boundary cleanliness from last chunk end vs file length — no chunker reporting needed
- **R386:** (inferred) Until O12 back-seek is implemented, append detection falls back to full reindex for markdown-strategy files

### Empty Files
- **R1644:** The Scanner maintains an in-memory empty-file set, keyed by path with mtime as the value
- **R1645:** A file is "empty" when its size on disk is zero; any chunker would yield no chunks for it
- **R1646:** During Scan(), if a file's size is zero and the path is already in the set with the current mtime, skip — do not flag as new, do not invoke the indexer
- **R1647:** During Scan(), if a file's size is zero and the path is not in the set (or its mtime has changed), record the path with current mtime in the set and report the path in a separate EmptyFiles result list
- **R1648:** The caller of Scan() removes the path from the index by calling `microfts2.RemoveFile(path)`; microfts2 handles chunk refcounting (multiple paths may share a fileid, so the chunks must not be forcibly deleted at the ark level)
- **R1649:** Non-zero-size files go through the normal CheckFile flow unchanged
- **R1650:** The empty-file set is process-lifetime only — not persisted across restarts
- **R1651:** Access to the empty-file set is serialized through the DB actor (Scanner.Scan runs on the actor goroutine); index evictions from ScanAsync are routed through the write queue (`enqueueWrite`), so no mutex is needed

## Feature: Cluster 1 — Config/CLI Fixes
**Source:** specs/main.md

### ark rebuild
- **R396:** `ark rebuild` deletes `index.db`, then re-runs init (reading settings from ark.toml) and scan
- **R397:** `ark rebuild` preserves ark.toml — only the index is destroyed and recreated
- **R398:** (inferred) `ark rebuild` refuses to run if the server is running — the server holds the DB open

### ark init --no-setup db nuke
- **R399:** `ark init` removes the existing database file (`index.db`) before creating a fresh database, regardless of `--no-setup`
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
- **~~R409:~~** (Retired T70 — no replacement) orphaned numbering gap
- **R541:** `opts` table supports: `mode` (contains/about/fuzzy/combined), `k` (max results), `preview` (window size), `filter_files`, `exclude_files`, `filter_file_tags`, `exclude_file_tags`
- **R750:** `mode = "fuzzy"` sets `opts.Fuzzy = true` and dispatches to `SearchFuzzy` via `SearchGrouped`
- **R542:** (inferred) Default mode is "combined", default k is 20, default preview is 0

### Click to Open — mcp:open()
- **R410:** `mcp:open(path)` opens a file with the system viewer (`xdg-open` on Linux, `open` on macOS)
- **R411:** The function returns immediately — the viewer opens asynchronously
- **R412:** (inferred) The file path must be an indexed file — error if not found
- **~~R413:~~** (Retired T71 — no replacement) orphaned numbering gap

### Indexing State — mcp:indexing()
- **R414:** Returns an empty table when no indexing is in progress
- **R415:** `mcp:indexing()` is a Go function registered on the Lua mcp table, returns a Lua array of strings
- **R416:** (inferred) All mcp Lua functions are registered after Frictionless setup completes

### HTTP Endpoint Removal
- **~~R543:~~** (Retired T178 — no replacement) `POST /search/grouped` endpoint is removed
- **~~R544:~~** (Retired T179 — no replacement) `POST /open` endpoint is removed
- **~~R545:~~** (Retired T180 — no replacement) `GET /indexing` endpoint is removed
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
- **R2956:** `--issue-file PATH` on `new-request` reads the `@issue` value verbatim from a file (trailing newlines trimmed); mutually exclusive with `--issue`, exactly one of the two required; the value must be a single line (a multi-line `--issue-file` is an error). Lets a caller hand the messenger a path so the issue line is never retyped.
- **R2957:** `--content-file PATH` on `new-request` and `new-response` reads the body verbatim from a file (trailing newlines trimmed); mutually exclusive with `--content`; when set, stdin body reading is skipped. The structural verbatim-body path so a relaying agent never retypes the payload.

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
- **R587:** `--multi` calls microfts2 `SearchMulti` which collects candidates once (single transaction) and scores with each strategy independently
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
- **~~R489:~~** (Retired T165 — see R525) `ark message ack FILE` sets `@msg` to `read` in the file's tag block
- **~~R490:~~** (Retired T166 — see R525) If `@msg` is already `read`, `acting`, or `closed`, does nothing (no error)
- **~~R491:~~** (Retired T167 — see R525) (inferred) Uses same file read/parse/render/write pattern as set-tags

### close
- **~~R492:~~** (Retired T168 — see R525) `ark message close FILE` sets `@msg` to `closed` in the file's tag block
- **~~R493:~~** (Retired T169 — see R525) If `@msg` is already `closed`, does nothing (no error)
- **~~R494:~~** (Retired T170 — see R525) (inferred) Uses same file read/parse/render/write pattern as set-tags

### inbox
- **R495:** `ark message inbox [--project PROJECT]` lists non-closed messages across all indexed sources
- **R496:** Finds files containing `@msg:` tags via the database, reads each file's tag block
- **R497:** Filters to messages where `@msg` is not `closed`
- **~~R498:~~** (Retired T60 — see R2431) When `--project` is given, further filters to `@to-project` matching PROJECT
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
- **R485:** Dispatches via `proxyOrLocal` — proxies to `POST /chunks` when a server holds the single-process index, otherwise cold-starts locally; read-only and fast either way (R2998, R3003).
- **R486:** The file must be indexed — error if not found in the database
- **R487:** `--wrap <name>` wraps output in XML tags, consistent with `ark search` and `ark fetch`
- **R488:** (inferred) Range labels are opaque — the exact string from search results is passed through to `GetChunks`

## Feature: Parallel Indexing
**Source:** specs/parallel-indexing.md

- **R517:** Rebuild and refresh prepare files in parallel — read, chunk, extract tags/trigrams are independent per file
- **R518:** Index writes are serialized through a ChanSvc actor — workers send closures that capture prepared data
- **R519:** Worker count defaults to `runtime.NumCPU()`
- **R520:** Worker errors (file read, chunk failure) skip the file and log a warning — do not abort the batch
- **R521:** Missing files are collected and returned as before (no behavior change)
- **R522:** (inferred) Applies to RefreshStale (used by rebuild, refresh, and server reconcile) — single-file paths unchanged
- **R523:** (inferred) No changes to microfts2 API — all writes go through existing methods
- **R524:** (inferred) No changes to fsnotify coordination — reconcileLoop already serializes via channel

## Feature: Vector Benchmark
**Source:** specs/vec-bench.md

### ark vec bench
- **R547:** `ark vec bench --model PATH` loads a GGUF model in-process via yzma and benchmarks embedding against real index chunks
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
- **R563:** `mcp:inbox(show_all)` Lua function returns a table of message entries from the tag index
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
- **R3083:** (inferred) `Store.AllTagsForFile(fileID)` returns the deduplicated union of every (tag, value) pair across all of the file's chunks — each chunk's inline, ext-routed, and overlay tags — collapsing the per-chunk multiset to one file-wide set
- **R3084:** `ark tag chunk ADDRESS` lists the tags at a file or chunk address, reusing the `ark chunks` / `@ext` address grammar; the address granularity picks the tag scope, and output is `tag\tvalue` per line (flat union)
- **R3085:** `ark tag chunk FILE` (bare file) reads the file's own tag block — identical to `ark tag get FILE` (the file-block reader), needing no index
- **R3086:** `ark tag chunk FILE -all` lists every tag anywhere in the file — the deduplicated union across all chunks (R3083)
- **R3087:** `ark tag chunk FILE:TARGET` (a chunk address — `RANGE`, `:"SNIPPET"`, or a decimal chunkID) resolves to a single chunk and lists that chunk's tag union via `Store.AllTagsForChunk`; the only per-chunk tag view
- **R3088:** `ark tag get FILE -all [TAG ...]` lists every tag in the file (the R3086 union); the optional `[TAG ...]` filter composes, narrowing the union to the named tags
- **R3089:** (inferred) The index-backed tag-read forms (`-all`, `FILE:TARGET`) proxy to the server when one holds the single-process index, else resolve against a cold-start DB — the standard proxy-or-local dispatch

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
- **R625:** Each `[[chunker]]` entry has a `name` (strategy name), a `type` (`bracket`, `bracket-full`, `indent`, or `indent-full`), `line_comments`, `block_comments`, and either easy-form pairs (`strings`, `brackets`) or full-form structs (`string_defs`, `bracket_defs`)
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
- **R2147:** Each `strings` / `string_defs` entry is translated to a `microfts2.BracketGroup` with non-nil `AllowedInner` (scan-restricted mode), Escape from the entry (default `\` for easy form), and no inner openers
- **R2148:** Each `brackets` / `bracket_defs` entry is translated to a `microfts2.BracketGroup` with `AllowedInner` left nil (code mode) unless the full-form entry sets `allowed_inner` explicitly
- **R2149:** `bracket_defs` entries accept optional `escape`, `allowed_inner`, and `allowed_parent` fields that pass through to the corresponding `BracketGroup` fields
- **R2150:** A `bracket_defs` entry with `allowed_inner` set (even to `[]`) is interpreted as scan-restricted, matching microfts2's nil-vs-empty semantics; an entry that omits `allowed_inner` stays in code mode

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
- **R668:** (inferred) Tag counts for tmp documents are tracked in memory by the overlay, not in the index

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
- **~~R701:~~** (Retired T35 — no replacement) (superseded) Bigram strategy removed
- **~~R702:~~** (Retired T36 — no replacement) (superseded) Bigram strategy removed
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
- **~~R716:~~** (Retired T87 — see R2484) The unmatched check applies after all other filtering
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

- **R731:** Verbosity is a graded dial — higher levels are strictly more verbose (`Logv` emits when `verbosity >= level`), with no fixed per-level category; call sites pick a level by how much detail they carry. Level 1 (`-v`) is the coarsest tier: high-level indexing/refresh milestones (full refresh, orphan cleanup, PDF page progress).
- **R732:** Level 2 (`-vv`) is the fine tier: per-file refresh/append decisions and recall-watcher activity.
- **R733:** Level 3 (`-vvv`) is a deeper tier surfaced through the same `verbosity >= level` gate — no separate mechanism; call sites opt in for finer operational detail (e.g. variable operations).
- **R734:** Level 4 (`-vvvv`) is the deepest tier through the same gate, for the fullest detail (values, chunk content).

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
- **~~R754:~~** (Retired T223 — see R1895) During append detection, the tag extraction window backs up from the split point to the previous newline in the full file data
- **~~R755:~~** (Retired T224 — see R1895) The widened window applies to both `ExtractTags` and `ExtractTagDefs`
- **~~R756:~~** (Retired T225 — see R1895) The bytes sent to `AppendChunks` are not affected — only the tag scan window is widened
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
- **~~R779:~~** (Retired T61 — see R2458) `--value REGEX` optionally filters on tag content (Go RE2)
- **~~R780:~~** (Retired T62 — see R2458) No `--value` means match any value for that tag
- **R781:** Multiple `--tag` flags create multiple independent subscriptions (OR semantics)
- **R782:** `--filter-files GLOB` restricts matching to files matching the glob
- **R783:** `--except-files GLOB` excludes files matching the glob from matching
- **R784:** `--filter-files` and `--except-files` compose: filter sets the scope, except carves out exceptions
- **R785:** File filters are checked at publish time before enqueue
- **R786:** `--cancel` with no `--tag` cancels all subscriptions for the session
- **R787:** `--cancel --tag TAG` cancels all subscriptions for that tag
- **~~R788:~~** (Retired T63 — see R2458) `--cancel --tag TAG --value VAL` cancels only subscriptions whose value regex would match VAL
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
- **~~R808:~~** (Retired T37 — see R853) ~~Scheduling is a subscription property, not a tag property~~ **Superseded by R853-R855:** scheduling is driven by ark.toml config + day-bucket indexing, not subscriptions
- **R809:** Only the next occurrence of a recurring event lives in the queue
- **R821:** One-shot (`--scheduled`) tag values: DATE formats `YYYY-MM-DD HH:MM`, `YYYY-MM-DD` (defaults 09:00), `MM-DD` (annual). Past one-shots ignored.
- **R822:** Recurring (`--recurring`) tag values follow the grammar: `[starting [on|at] DATE] every [ORDINAL] PERIOD [at HH:MM] [ending [on|at] DATE] [DESCRIPTION]`
- **R823:** Annual shorthand: a bare `MM-DD` value is treated as annually recurring
- **R824:** Recurring PERIOD types: duration (Nm, Nh), day name (Monday-Sunday), day class (weekday, weekend, day), scope (of the week/month/year)
- **R825:** `@ended: [REASON]` in the same chunk as a scheduled/recurring tag stops the event — scheduler skips chunks containing both
- **~~R826:~~** (Retired T38 — see R868) ~~On subscribe with `--scheduled`/`--recurring`, scan existing tags via TagContext and populate the queue.~~ **Superseded by R868-R870:** scheduler reads day buckets at startup, not subscription-triggered
- **~~R827:~~** (Retired T39 — see R853) ~~If no subscription declares a tag as scheduled/recurring, zero scheduling overhead~~ **Superseded by R853:** zero overhead unless tag is in ark.toml `[schedule]`
- **~~R828:~~** (Retired T40 — see R868) ~~Scheduled events are per-subscription~~ **Superseded by R868:** scheduler fires to all listening sessions, not per-subscription

### Muting

- **R829:** `@mute: true` in a file silences all pubsub events from that file
- **R830:** The mute check happens before subscription matching — no events fire, no watchdog findings
- **R831:** Muted files are still indexed and searchable; only notifications are suppressed
- **~~R832:~~** (Retired T72 — no replacement) orphaned numbering gap
- **~~R833:~~** (Retired T73 — no replacement) orphaned numbering gap
- **~~R834:~~** (Retired T74 — no replacement) orphaned numbering gap
- **~~R810:~~** (Retired T107 — see R2783) Quarter chimes: a built-in recurring event every 15 minutes with full date, day of week, time of day
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

- **~~R853:~~** (Retired T108 — see R2830) ark.toml `[schedule]` section declares which tags carry date values; only listed tags are date-parsed at index time
- **~~R854:~~** (Retired T109 — see R2831) `[schedule.defaults]` maps tag names to default durations (e.g., `dentist = "1h"`, `standup = "15m"`, `birthday = "all-day"`)
- **~~R855:~~** (Retired T110 — see R2833) Tags not listed in `[schedule]` incur zero date-parsing or day-bucket overhead
- **~~R856:~~** (Retired T111 — see R2830) Adding or removing a tag from `[schedule]` triggers re-materialization of day-bucket entries for all files containing that tag

### Date and Duration Parsing

- **R857:** The `..` range operator expresses durations: `TIME..TIME` (same-day), `TIME..DATE TIME` (multi-day span)
- **~~R858:~~** (Retired T112 — see R2831) `DATE TIME` with no `..` uses the default duration from `[schedule.defaults]`; `DATE` alone is all-day
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

### Malformed Datetime Handling

- **R2846:** A date token of the shape `YYYY-MM-DD-HH` or `YYYY-MM-DD-HH:MM` is normalized before parsing — the separator hyphen is rewritten to `T` and the token re-parsed — because `dateparse` otherwise reads the `-HH:MM` as a timezone offset and returns midnight. Forgiving by design: an existing dash-form schedule entry begins firing at the intended time instead of midnight.
- **R2847:** A value that parses to a date carrying a timezone offset but no time-of-day (e.g. `2026-05-28Z`, or a dash form normalization did not rescue) is rejected with an error rather than interpreted as midnight. A bare date with no timezone remains a normal all-day event; only the date-plus-timezone shape errors.
- **R2848:** After a permissive parse succeeds, the same date token is re-checked with `dateparse.ParseStrict`; if it reports an ambiguous mm/dd vs dd/mm format (e.g. `3/1/2014`), the value is rejected rather than guessed. Unambiguous forms (ISO `YYYY-MM-DD`, spelled-out months) are unaffected.
- **R2849:** `ark schedule search` markdown render collapses the time range when start equals end (`- 13:45:` not `- 13:45–13:45:`) and drops the trailing `: ` when the summary is empty (`- 09:00–10:30`, `- 13:45`). The all-day form (`- all day: SUMMARY`) drops its trailing colon the same way when the summary is empty.

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
- **~~R1010:~~** (Retired T213 — see R2813) Schedule log maintains exactly one `@ark-event-upcoming:` per recurring event. Calendar UI computes future dates from `@ark-event-spec:`.
- **~~R1011:~~** (Retired T214 — see R2818) After downtime, crank-forward converts all past upcoming to fired, then writes one new upcoming
- **R1012:** Per-tag schedule filtering via `[schedule.tag.NAME]` in ark.toml with `filter_files`/`exclude_files`. Global excludes always apply; per-tag filters narrow further.
- **R1013:** tmp:// source files produce tmp:// schedule logs (`tmp://schedule/HASH.md`), not disk logs
- **R1014:** Schedule processing deferred outside DB actor — items accumulated during indexing, drained after scan/refresh, processed in goroutine
- **R1015:** `ark schedule search DATE` uses same date grammar as schedule tags (single date, `..` range, keyword prefixes)
- **R1016:** `ark schedule parse DATE` diagnostic — shows parsed start, end, description, recurrence spec, bounds, next occurrence
- **R1017:** `ark schedule tags` shows configured tags, defaults, lifecycle status, per-tag filters
- **~~R1018:~~** (Retired T185 — see R2819) `RemoveFile`/`RemoveByID` clears TD/TF day bucket records via `ClearDayBuckets`
- **~~R1019:~~** (Retired T186 — see R2819) `WriteDayBucketsForFile` handles schedule log files via `dayBucketsFromLogFile` — parses `@ark-event-upcoming:`/`@ark-event-fired:` entries
- **R1020:** `ParseDate` handles `2006-01-02 15:04` format (space-separated date+time)
- **R1021:** `ReloadConfig` updates `indexer.config` (was stale after ark.toml reload)
- **~~R1022:~~** (Retired T187 — see R2819) Indexer config set at DB open time, not only when scheduler is wired — enables day bucket writes during rebuild

### Month Buckets (replaces Day Buckets)

- **~~R1023:~~** (Retired T188 — no replacement) Remove the day-bucket records (TD/TF). Replace with in-memory month buckets computed from schedule log specs.
- **~~R1024:~~** (Retired T189 — no replacement) One month bucket entry per month per recurring event — the first occurrence in that month
- **~~R1025:~~** (Retired T190 — no replacement) Query: find month bucket at or before range start, crank forward to generate all events in range
- **~~R1026:~~** (Retired T191 — no replacement) Month buckets computed on startup from schedule log files. Recomputable on restart.
- **R1027:** `ark schedule search` computes events from specs and schedule log files — works without a running server
- **~~R1028:~~** (Retired T41 — no replacement) @obsolete-req: R866 -- day bucket LMDB indexing replaced by month buckets
- **~~R1029:~~** (Retired T42 — no replacement) @obsolete-req: R871 -- TF reverse index for deletion no longer needed
- **~~R1030:~~** (Retired T43 — no replacement) @obsolete-req: R911 -- TD JSON array no longer needed
- **~~R1031:~~** (Retired T44 — no replacement) @obsolete-req: R912 -- ack status embedded in day buckets no longer needed
- **~~R1032:~~** (Retired T45 — no replacement) @obsolete-req: R1019 -- dayBucketsFromLogFile no longer needed

### Schedule Tags --values

- **R1033:** `ark schedule tags --values` shows tag values from source files and next upcoming date from schedule logs
- **R1034:** Reads schedule log files directly — no server dependency

### Scheduling Exceptions

- **R1035:** `@remove: DATE [text]` in the same chunk as a schedule tag skips that occurrence
- **R1036:** `@add: DATE [text]` in the same chunk as a schedule tag adds an extra occurrence
- **R1037:** Exception tags use short names scoped by the event chunk (not @ark-event- prefix)
- **R1038:** Exceptions parsed at index time and stored in the event struct
- **R1039:** crankForward, occurrence generation, and schedule search all respect exceptions
- **R1040:** Source file is the authority — schedule log upcoming entry reflects the computed result after exceptions

### Gap Detection (revised)

- **R1041:** Gap detection compares recurrence spec against @ack: dates — no fired records needed
- **~~R1042:~~** (Retired T46 — no replacement) @obsolete-req: R870 -- @ark-event-fired: entries in log no longer needed for gap detection
- **R1043:** `ark schedule search --gaps` computes unacked past occurrences from spec vs @ack: dates

### Day-Bucket Indexing

- **~~R866:~~** (Retired T192 — see R2810) Events are discretized into day-granularity buckets: key `TD|YYYYMMDD|fileid|tag`, value is a JSON array of events for that day/file/tag
- **~~R867:~~** (Retired T193 — see R2810) Calendar range query: seek `TD|start`, scan to `TD|end` — no post-filtering needed
- **~~R911:~~** (Retired T199 — see R2810) TD value is a JSON array — multiple events per day per file/tag (e.g., rescheduled occurrences)
- **R912:** Each event in the array carries ack status (acked bool, ackText string), parsed from `@ack:` tags in the same chunk at index time
- **R913:** (inferred) Calendar view gets events + ack status in one range scan, no second pass

### Schedule CLI

- **~~R914:~~** (Retired T200 — see R1027) `ark schedule search START END` queries day buckets for events overlapping the date range
- **R915:** START and END accept flexible date formats via dateparse
- **R916:** Output is markdown by default (crank-handle style for agents)
- **R917:** `--json` flag outputs JSON array
- **R918:** `--tag TAG` filters to a specific schedule tag
- **R919:** `--gaps` shows only past events with `acked: false` — Franklin's missed-event query
- **~~R920:~~** (Retired T201 — see R1027) Each event in output includes ack status from the day-bucket record
- **R921:** `ark schedule change PATH TAG NEWSTART [NEWEND]` rewrites the date in a schedule tag value
- **R922:** Description text after the date is preserved on rewrite
- **R923:** File is re-indexed after modification
- **~~R924:~~** (Retired T121 — no replacement) For recurring events, updates the corresponding `@ark-event-upcoming:` entry in the schedule log
- **R925:** `--dry-run` shows what would change without writing
- **R926:** (inferred) `ark schedule` with no subcommand or `--help` shows usage

### Config Change Detection

- **R927:** Store serialized `[schedule]` section in the index settings record (I prefix) on server startup
- **R928:** On config reload (startup, ark.toml fsnotify), compare current `[schedule]` vs stored
- **~~R929:~~** (Retired T202 — see R2836) Tags added: scan files with the new tag, write day buckets
- **~~R930:~~** (Retired T203 — see R2836) Tags removed: clear day buckets for files with that tag
- **~~R931:~~** (Retired T204 — see R2836) Defaults changed: re-materialize affected day buckets with new durations
- **~~R932:~~** (Retired T205 — see R2836) (inferred) After re-materialization, update the stored `[schedule]` in the index

### Acknowledgment Indexing

- **R933:** When indexing a file with schedule tags, parse `@ack:` tags in the same chunk
- **~~R934:~~** (Retired T206 — see R1027) For each day bucket being written, check if any `@ack:` covers that date
- **~~R935:~~** (Retired T207 — see R1027) Embed `acked: true` and `ackText` in the DayBucketEvent when covered
- **R936:** `@ack:` parsing uses the same date formats as schedule tag parsing (dateparse)
- **~~R868:~~** (Retired T194 — see R2810) (inferred) Multi-day events produce one TD entry per day spanned
- **~~R869:~~** (Retired T113 — no replacement) (inferred) Day buckets for recurring events are derived from `@ark-event-upcoming:` entries in schedule log files, not materialized directly from the recurring spec
- **~~R870:~~** (Retired T195 — see R2810) Past events are indexed from `@ark-event-fired:` entries in schedule log files as day buckets — the calendar is a historical record

### Reverse Index for Deletion

- **~~R871:~~** (Retired T196 — see R2810) `TF|fileid` key stores the list of all dates with day-bucket entries for that file
- **~~R872:~~** (Retired T197 — see R2810) On re-index: read `TF|fileid` (one read), delete each `TD|date|fileid|*`, delete `TF|fileid`, write new TD + TF from current content
- **~~R873:~~** (Retired T198 — see R2810) File removal (`RemoveFile`, `RemoveByID`) clears TD/TF day bucket records via `Store.ClearDayBuckets`

### Schedule Log

- **R899:** `~/.ark/schedule/` directory holds schedule log files — one per source file containing schedule tags
- **R900:** Each event definition gets a chunk in its log file with `@ark-event:`, `@ark-event-source:`, `@ark-event-spec:` tags identifying it
- **R901:** `@ark-event-upcoming:` tags in the log represent concrete future instances; `@ark-event-fired:` tags represent past instances
- **~~R902:~~** (Retired T117 — see R2819) On index of a source file with a schedule tag, the scheduler ensures `@ark-event-upcoming:` entries exist through the forward window (default 6 months)
- **~~R903:~~** (Retired T118 — no replacement) Deleting an `@ark-event-upcoming:` line is a scheduling exception — that occurrence is skipped
- **~~R904:~~** (Retired T119 — no replacement) Editing an `@ark-event-upcoming:` date moves that occurrence — just a file edit, indexed normally
- **~~R905:~~** (Retired T120 — no replacement) Crank-forward checks for existing `@ark-event-upcoming:` before adding — no duplicates
- **R906:** Log files are rotatable — old `@ark-event-fired:` entries can be archived; `@ack:` in source files is the durable human record
- **R907:** Log files are regular ark files — tagged, indexed, searchable
- **R908:** (inferred) `~/.ark/schedule/*.md` is included in the `~/.ark` source so log files are indexed automatically

## Feature: Chat Transcript
**Source:** specs/chat-transcript.md

- **R1044:** `ark chats GLOB` reads Claude Code JSONL logs and renders human-readable transcripts
- **R1045:** User turns introduced with `❯`, assistant turns with `●`, continuation lines indented 2 spaces
- **R1046:** Text word-wrapped at `--line-length` (default 100)
- **R1047:** `--with-tools` shows tool calls inline as `⚙ ToolName summary`
- **R1048:** `--wrap NAME` surrounds output with `<NAME>...</NAME>` tags
- **R1049:** Sidechain messages (subagent traffic) filtered out
- **R1050:** GLOB matches against file basenames in `~/.claude/projects/` directories
- **R3035:** `--thinking` renders assistant thinking (chain-of-thought) blocks inline as `✻ ...`, off by default; `--all` enables tools + thinking + sidechain together for a complete transcript. The chunker (`extractJSONLTextFast`) extracts the same `thinking` field, so display and index agree — but Claude Code stopped persisting thinking text ~2026-04 (signature-only since; see `.scratch/CHAT-THINKING-REDACTION-20260705.md`), so both carry reasoning only for pre-May transcripts. `--thinking` is thus effectively an archive reader for that window.

## Feature: Schedule Lifecycle
**Source:** specs/schedule-lifecycle.md

### Schedule Filtering
- **R953:** `filter_files` in `[schedule]` restricts which files are scanned for schedule tags (glob patterns, tilde expanded)
- **R954:** `exclude_files` in `[schedule]` excludes files from schedule scanning (glob patterns, tilde expanded)
- **R955:** `filter_files` and `exclude_files` use the same narrow/carve semantics as search — filter sets scope, exclude carves exceptions
- **R956:** When both are absent, all indexed files are eligible for schedule scanning
- **~~R957:~~** (Retired T122 — see R2822) `lifecycle_include` controls which schedule tags get the full lifecycle (log entries, check-gap, gap detection). Default `"*"`.
- **~~R958:~~** (Retired T123 — see R2822) `lifecycle_exclude` carves exceptions from `lifecycle_include`
- **~~R959:~~** (Retired T124 — see R2825) Tags outside the lifecycle still fire through pubsub — they just don't get logged or monitored
- **~~R960:~~** (Retired T125 — see R2822) (inferred) Lifecycle include/exclude use glob patterns on tag names

### EnsureArkSource Scoping
- **R961:** The hardcoded `~/.ark` source sets `include = ["ark.toml", "schedule/**", "apps/**", "storage/**"]`
- **R962:** Directories outside the include list (index.db, logs) are not indexed
- **R963:** (inferred) Archived schedule logs in `~/.ark/schedule-archive/` are unindexed — rotated logs leave the index automatically

### Log Writing on Fire
- **R964:** When a lifecycle event fires, convert `@ark-event-upcoming: DATE` to `@ark-event-fired: DATE` in the schedule log
- **R965:** Append `@check-gap: DATE` in the same paragraph as `@ark-event-fired:` (same chunk after markdown chunking) **only when the tag has a non-empty `default_duration`**. Tags without a duration are heartbeats (chimes, ticks) and lack a meaningful human-ack loop; appending `@check-gap:` for them would let `ScanCheckGaps` flood `tmp://watchdog/missed-events` on every restart with bogus misses.
- **R966:** Compute next occurrence, append `@ark-event-upcoming: NEXT` if no exception exists for that date
- **R967:** Re-index the log file after modification so the priority queue updates (EnsureUpcoming)
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
- **~~R976:~~** (Retired T216 — see R2818) Filter changes trigger re-evaluation: files newly in scope get schedule log entries written; files out of scope get log entries removed
- **~~R977:~~** (Retired T217 — see R2818) (inferred) Lifecycle filter changes re-evaluate which tags get check-gap monitoring — newly excluded tags have their check-gaps removed

### Materialization Strategy
- **R978:** Only the next occurrence of a recurring event is materialized in the schedule log
- **~~R979:~~** (Retired T215 — see R2818) On startup, compute missed occurrences between last-fired and now, surface as missed events, then materialize just the next one
- **R980:** (inferred) Calendar UI computes virtual recurring items on the fly from recurrence specs — deferred to Lua/UI work

### Scheduler Integration

- **~~R874:~~** (Retired T114 — see R2818) Scheduler reads schedule log files at startup — not subscriptions, not LMDB registries
- **~~R875:~~** (Retired T115 — see R2819) On server startup: scan `~/.ark/schedule/` for `@ark-event-upcoming:` entries, populate the priority queue
- **~~R876:~~** (Retired T116 — see R2818) On startup: `@ark-event-upcoming:` entries in the past are converted to `@ark-event-fired:`, next occurrences computed and appended
- **R877:** On recurring event fire: convert `@ark-event-upcoming:` → `@ark-event-fired:` in log, compute next occurrence, append `@ark-event-upcoming:` if none for that date, re-index log, re-enqueue
- **R878:** Events delivered through the publisher carry their nature (scheduled fire vs tag-change notification) so receivers can distinguish

### Remove Scheduling from Subscriptions

- **~~R879:~~** (Retired T181 — no replacement) Remove `--scheduled` and `--recurring` flags from `ark subscribe` CLI
- **~~R880:~~** (Retired T182 — no replacement) Remove `ScheduleMode` type, `ScheduleNone`/`ScheduleOneShot`/`ScheduleRecurring` constants, and `Schedule` field from `TagSub`
- **~~R881:~~** (Retired T183 — no replacement) Remove `ScanForSub` from EventScheduler — replaced by day-bucket startup scan (R875)
- **~~R882:~~** (Retired T184 — no replacement) (inferred) Remove `RemoveForSession` session-scoped event cleanup — events are no longer per-subscription

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

- **R893:** `mcp.scheduled(startDate, endDate)` returns items overlapping a date range, computed from the schedule logs via EventScheduler.QueryRange (endDate inclusive, whole day); each item has date, endDate, tag, summary, path, recurring, allDay
- **R894:** `mcp:reschedule(path, tag, newDate, newEndDate)` rewrites the date in the tag value, preserves trailing description text, re-indexes
- **R895:** `mcp:tagComplete(prefix)` returns tag name and value completions from the index
- **R896:** `mcp:fileStatus(path)` returns whether the file is indexed, its tags, and schedule info
- **R897:** `mcp:subscribe(opts, callback)` registers a UI-side tag-change subscription; callback fires on matching tag events
- **R898:** `mcp:subscribe` supports tag, value (RE2 regex), filterFiles, exceptFiles — full parity with CLI minus removed scheduled/recurring flags

## Feature: Status DB Records
**Source:** specs/status-db.md

- **R2473:** `ark status --db` shows index record counts grouped by bucket (microfts2, ark)
- **R2474:** Each record type displays prefix letter, purpose label, count, key bytes, and value bytes
- **R2475:** Record types are sorted alphabetically within each bucket
- **R2476:** Counts are right-aligned for readability
- **R2477:** Without `--db`, status output is unchanged
- **R2478:** microfts2 record types: C (chunks), F (files), H (hashes), I (config), N (paths), T (trigrams), W (tokens)
- **R2479:** `ark status -db`'s ark bucket shows one row per record class in the `record-formats.md` ark table, except any class a spec marks "no status display" (currently only `S`). The row set tracks that inventory rather than a hand-kept enumeration; multi-byte prefixes (`E:`, `EV`, `EC`, `EF`, `ED`, `PC`, and the two-byte `HC`/recall family) are not collapsed into a single-byte bucket.
- **R2480:** `GET /status?db=true` includes record counts in the JSON StatusInfo response
- **R2481:** (inferred) Store needs a RecordCounts method that returns counts keyed by full prefix string. Known multi-byte prefixes (`E:`, `EV`, `EC`, `EF`, `PC`) are matched before falling back to a single-byte prefix.
- **R2482:** (inferred) microfts2 needs a RecordCounts method returning counts per prefix byte
- **R1130:** A total summary line shows aggregate record count, key bytes, value bytes, and proportion of the database file size
- **R3078:** The CLI's package-level `arkLabels` map is the status-db allowlist realizing R2479: one label per shown ark record class. `buildRecordCounts` iterates `arkLabels`, so a record class absent from it is silently omitted from `ark status -db`. The map covers `HC` (hot-correlations), `RC` (recall-candidate), `RD` (recall-discussed), `RF` (recall-freshness), and `RJ` (recall-judgment) alongside the pre-existing classes, extending R2162's per-class precedent (ED) to the full inventory; `S` is deliberately excluded (no status display).

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
- **R995:** The microfts2 Go-side caches (pathCache, pathToID, frecordCache) are protected by the DB actor: Go maps are unsafe for concurrent read/write, so a non-actor read operation must read through a private `fts.Copy()` (which carries its own caches) rather than the shared original, whose caches the reconcile step nils via `InvalidateCaches`. Writes go through the write actor
- **R3005:** `IndexPathsAsync` coalesces refresh work via a pending-refresh set — paths with a refresh already queued or in flight are skipped, the rest marked before enqueueing; each path is cleared as its refresh begins, so a change arriving during the refresh re-queues rather than being dropped
- **R3163:** A find-connections substrate computation runs off the actor as a `substrateOp` (Monadic Wrapper) that reads through a private `fts.Copy()`: `DB.withFTS` / `Searcher.withFTS` rebind a read view to the copy, so the pass's cache reads — `FileIDPaths`, `FileInfoByID`, and `SearchFuzzy`'s internal `FileIDPaths` — touch the copy's caches and never race the write actor's `InvalidateCaches` (R995). Bbolt-only reads (`SearchChunks`, via `ViewChunkEmbeddings`/`ReadCRecord`) and embedding stay on the live Librarian; the shared overlay resolves tmp:// documents unchanged. The same copy-read discipline now covers `Recall` (as a `recallOp`, spanning `chatFunnel` and the shared `normalizeInputs` helper, `FindConnections`' enqueue-time normalization included), `BuildFetchPayload`, and the off-actor HTTP read handlers (R3165). The Searcher's own cache readers need no separate migration: they are reached from server handlers that already run inside `Sync(srv.db, …)`, and so are serialized against `InvalidateCaches` by the actor (R3165). The handler-wrapper front door for operations is built and in use (R3166–R3170); what remains under #46/O156 is its opportunistic spread to the rest of the handlers, whose purpose is grep-auditability rather than a live race.
- **R3165:** Whether a Go-side cache read races is determined by the goroutine it runs on, not by which function performs it: `InvalidateCaches` runs only on the actor, so any read reached from inside a `Sync`/`SyncVoid` closure is already serialized, and only off-actor **entry points** — with the whole subtree each can reach — need migrating. An HTTP handler runs on its own goroutine and is therefore such an entry point: a handler that reads cache-backed state outside the actor binds one private `fts.Copy()` at its top (`srv.db.withFTS(srv.db.fts.Copy())`) and threads that view through every off-actor call it makes, so helpers taking a `*DB` inherit it and one binding covers all readers beneath. `handleContentView` (whose subtree reads via `ChunkIDByLocation`, `ChunkIDsForPath`, `ResolveLink` under `wrapTagElements`, `resolveFilePath` under `ExtRoutingsForTargetChunk`, and `renderPdfChunksByPage`) and the Librarian's `HandleExpandSearch` (`SearchGrouped`) read through such a view; their `Sync(srv.db, …)` calls keep the live DB, since a read view carries no actor. The view also carries the `ExtMap` pointer — mutex-guarded and untouched by `InvalidateCaches`, like the overlay — so @ext routings resolve without reaching back to the live DB.

## Feature: DB Write Actor
**Source:** specs/db-write-actor.md

- **R1051:** Reads execute directly in the main actor and return immediately — bbolt MVCC ensures consistent snapshots during writes
- **R1052:** Config files (ark.toml) are indexed in-place in the main actor, synchronously, before any normal writes that depend on them
- **R1053:** Normal file writes are queued as closures; if the queue was empty, the first closure is dequeued and run in a goroutine
- **R1054:** The write goroutine calls `db.Copy()` to get a shallow copy sharing the index but with nil caches
- **R1055:** The write goroutine opens a write transaction on the copy and indexes the batch (file I/O off the actor)
- **R1056:** After indexing, the goroutine sends a reconcile closure back to the main actor channel
- **R1057:** The reconcile closure calls `InvalidateCaches()`, commits the write transaction, and dequeues the next write if available
- **R1058:** Each write goroutine runs one batch and dies — the main actor decides whether to start the next (continuation pattern)
- **R1059:** On goroutine panic: defer/recover sends an error closure to the main actor; the batch is dropped
- **R1060:** On reconcile error: log the failure, skip the batch, dequeue next — system self-heals on next write request
- **R1061:** Errors must be logged visibly — silent drops cause confusion about why files aren't indexed
- **R1062:** When a scan produces N files: partition into config files vs content files, process config first (synchronous), then queue content as write batches
- **R1063:** microfts2 needs `Copy() *DB` — shallow copy sharing the index, overlay pointer shared (has its own mutex), caches set to nil, chunker registry shared (read-only)
- **R1064:** microfts2 needs `InvalidateCaches()` — nils pathCache, pathToID, frecordCache, forcing lazy reload on next access
- **R1065:** The write actor is a goroutine, not a separate ChanSvc — no lifetime management, no second channel
- **R1066:** (inferred) The deferred-schedule pattern (pendingSchedule / DrainSchedule / processScheduleItems) can be removed once schedule I/O moves into the write goroutine
- **R1067:** (inferred) No more than one write goroutine runs at a time — serialized by the main actor's dequeue-after-commit pattern. This one-at-a-time execution is a **contract other code may rely on**: a write closure's body runs atomically with respect to every other write closure, so a check-and-set performed inside a write closure needs no additional lock or atomic (e.g. the terminal-connections-doc dedup, R3164). Parallelizing writes for I/O throughput would break such consumers and must not be done without revisiting them.
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
- **~~R1083:~~** (Retired T10 — see R1876) Values are extracted by scanning files that have the tag (via F records for file IDs)
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
- **~~R1099:~~** (Retired T8 — see R1281) V record key format: `V[tagname]\x00[value]` — null byte separates tag from value
- **~~R1100:~~** (Retired T12 — see R1873) V record value: packed varint-encoded fileids (unsigned LEB128)
- **R1101:** One index entry per unique (tag, value) pair — chunkids accumulate in the value
- **R1102:** Prevalence of a (tag, value) = number of varints decoded from the value — a multi-set count of the chunk-contributions carrying it across the corpus, not distinct files

### V Record Lifecycle
- **R1103:** On index/refresh: remove all V entries for the file's old chunkids, then add V entries from freshly extracted tag values
- **~~R1104:~~** (Retired T208 — see R1884) On append: add V entries for newly extracted tag values (no removal — appended tags are additive)
- **~~R1105:~~** (Retired T209 — see R1899) On remove: remove the chunkid from all V entries; delete the key if chunkid list becomes empty
- **R1106:** `ExtractTagValues` (already called during index/refresh/append) provides the source data — no new extraction logic needed
- **R1107:** (inferred) V records are rebuilt from scratch by `ark rebuild`, same as T/F/D records

### V Record Queries
- **R1108:** Prefix scan `V[tagname]\x00` returns all values for a tag with counts
- **R1109:** Prefix scan `V[tagname]\x00[prefix]` filters values by prefix — sorted keys make this a range scan
- **~~R1110:~~** (Retired T9 — see R1309) Direct key lookup `V[tagname]\x00[value]` returns fileids for a specific (tag, value) pair

### Endpoint Integration
- **R1111:** `POST /tags/values` switches from file-reading to V record queries — O(1) index lookup instead of O(files) disk reads
- **R1112:** (inferred) Lua `mcp:tagComplete` should also use V records for value completion when wired

## Feature: Chunk Callback Tag Extraction
**Source:** specs/chunk-callback.md

### Callback Wiring
- **R1113:** Indexer passes `WithChunkCallback` to `AddFileWithContent` to receive clean chunk text during indexing
- **R1114:** Indexer passes `WithChunkCallback` to `ReindexWithContent` during full refresh
- **R1115:** Indexer passes `WithAppendChunkCallback` to `AppendChunks` during append refresh
- **~~R1116:~~** (Retired T31 — see R1913) The callback accumulates chunk text slices for microvec embedding
- **~~R1117:~~** (Retired T210 — see R1904) The callback extracts tag values from each chunk's clean text via `ExtractTagValues`
- **~~R1118:~~** (Retired T211 — see R2913) The callback extracts tag defs from each chunk's clean text via `ExtractTagDefs`
- **R1119:** (inferred) The callback extracts tag counts via `TagCountsFromValues` on accumulated tag values

### Tag Merging
- **R1120:** Tag counts from multiple chunks are summed for the same tag name
- **R1121:** Tag values from multiple chunks are collected; Store deduplicates by fileid
- **R1122:** Tag defs from multiple chunks use last-writer-wins per tag name

### splitChunks Elimination
- **R1123:** `splitChunks` is removed from `AddFile` — callback provides chunk text
- **R1124:** `splitChunks` is removed from `executeFullRefresh` — callback provides chunk text
- **~~R1125:~~** (Retired T25 — no replacement) `splitChunks` is retained in the append microvec path (needs all chunks for re-embedding)

### Prep/Execute Restructure
- **R1126:** `prepareRefresh` no longer extracts tags for full refresh — tags come from callback in `executeRefresh`
- **~~R1127:~~** (Retired T212 — see R1895) `prepareRefresh` still extracts tags for append path using `tagWindowForAppend` (unchanged)
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
- **R1143:** For each requested tag, scans V record prefix `V[tag]\x00` entries checking if any of the file's chunkids is in the varint list
- **R1144:** (inferred) Returns empty string for tags with no value for the fileid — callers treat missing values as absent, not errors

### Inbox Rewrite
- **R1145:** `DB.Inbox` uses `TagFiles(["status"])` for candidate fileids and path resolution (unchanged)
- **R1146:** `DB.Inbox` filters to `/requests/` paths before per-file tag lookup (unchanged)
- **R1147:** `DB.Inbox` calls `Store.FileTagValues` instead of `os.ReadFile` + `ParseTagBlock` for each candidate
- **R1148:** When `showAll` is false, `DB.Inbox` uses `TagValueChunks("status", "completed")` and `TagValueChunks("status", "denied")` to build an exclusion set before per-file tag lookup
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
- **~~R1235:~~** (Retired T227 — see R1380) The server manages Haiku interactions via `claude --print --model haiku --output-format json` invocations
- **~~R1236:~~** (Retired T228 — no replacement) Each invocation uses `--system-prompt-file ~/.ark/searching/CLAUDE.md --tools ""`
- **~~R1268:~~** (Retired T238 — no replacement) `--system-prompt-file` replaces all default Claude Code instructions — the Librarian is a specialized oracle, not a general assistant
- **~~R1269:~~** (Retired T239 — no replacement) `--tools ""` disables all tool access — the Librarian only generates text responses
- **~~R1237:~~** (Retired T229 — no replacement) Conversation context persists via `--resume SESSION_ID` — the session ID from the first invocation is stored and reused
- **~~R1238:~~** (Retired T230 — no replacement) Two spawns per expansion: one for expand (step 1), one for curate (step 3). Claude's prompt caching pays system prompt tokens once per session.
- **~~R1239:~~** (Retired T231 — no replacement) The session ID expires after a TTL with no requests — next expansion starts a fresh conversation
- **~~R1240:~~** (Retired T232 — no replacement) (inferred) A fresh session creates a new conversation context, paying cache creation tokens again
- **~~R1241:~~** (Retired T233 — no replacement) (inferred) If a claude invocation fails, the session ID is cleared and the next request starts fresh
- **~~R1242:~~** (Retired T234 — no replacement) (inferred) The Librarian is managed by a closure actor to serialize access from concurrent HTTP handlers

### Expansion Pipeline
- **~~R1243:~~** (Retired T162 — see R1379) `POST /search/expand` accepts JSON body with `mode`, `tag`, `value` fields
- **~~R1244:~~** (Retired T163 — see R1382) Returns JSON `{results: [{path, strategy, chunks, source: "expansion"}]}` — curated search results marked as expansion-sourced
- **~~R1245:~~** (Retired T164 — see R1378) The pipeline runs server-side in three steps: Haiku expands → search → Haiku curates
- **R1246:** For tag mode (Phase A): step 2 is trigram fuzzy matching against V records (the tag-value index)
- **R1270:** Haiku expand step: given user's tag name and value, suggests alternative tag names and values
- **R1271:** Fuzzy match step: each alternative is fuzzy-matched against V records, producing (tag, value, count, score) tuples
- **R1272:** Haiku curate step: sees matched tag/value pairs with scores, prunes false positives, returns curated subset
- **R1273:** Server fetches actual search results for the curated tags before returning to the client
- **~~R1247:~~** (Retired T235 — no replacement) (inferred) If the co-process is unavailable (not on PATH, spawn failure), the endpoint returns 503

### Curation Endpoint Rename
- **R1378:** Curation endpoints are renamed from `/search/expand` to `/search/curate` — curation is now a separate step from expansion
- **R1379:** `POST /search/curate` queues a curation request (replaces `POST /search/expand`)
- **R1380:** `GET /search/curate/wait` is the lotto tube for the sidecar (replaces `GET /search/expand/wait`)
- **R1381:** `POST /search/curate/result` receives sidecar results (replaces `POST /search/expand/result`)
- **R1382:** `GET /search/curate/result/{id}` polls for a curation result (replaces `GET /search/expand/result/{id}`)
- **R1383:** Expansion and matching endpoints remain under `/search/expand/`: fuzzy, embed, search

### Availability
- **R1248:** Server checks for `claude` on PATH at startup
- **R1249:** `GET /status` includes `spectral: true/false` capability flag
- **R1250:** (inferred) The check is a one-time `exec.LookPath("claude")` at startup, not per-request

### Searching Directory
- **R1251:** `~/.ark/searching/CLAUDE.md` contains the system prompt for the Haiku expansion session
- **R1252:** `ark init` creates `~/.ark/searching/` and writes a default `CLAUDE.md` if the directory doesn't exist
- **~~R1253:~~** (Retired T236 — no replacement) The CLAUDE.md file is read at co-process spawn time via the `--system-prompt-file` flag
- **~~R1254:~~** (Retired T237 — no replacement) (inferred) Changes to CLAUDE.md take effect on next co-process spawn (after TTL expiry or crash)

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
- **~~R1274:~~** (Retired T151 — see R2964) `tag_model` field in ark.toml specifies the GGUF embedding model filename
- **R1275:** The path is relative to the database directory (`~/.ark/`)
- **R1276:** If `[embedding] model` is empty or the file doesn't exist, embedding is disabled — trigram fuzzy is the fallback
- **R1277:** The model is loaded by the Librarian on first embedding query
- **R1278:** The model stays warm in memory; unloaded on TTL expiry with no queries
- **R1279:** (inferred) Next query after TTL expiry reloads the model

### Tag Value IDs
- **R1280:** Each unique (tag, value) pair gets a sequential tag-value-id (varint)
- **~~R1281:~~** (Retired T13 — see R1873) The tag-value-id is part of the V record key: `V[tag]\x00[value]\x00[tvid: varint]` → packed fileids
- **R1282:** The ID counter (`next_tvid`) is stored as an ark index setting (`I` prefix)
- **R1283:** The tag-value-id is stable: assigned on first index, reused if the same (tag, value) pair persists
- **R1284:** (inferred) On rebuild, tag-value-ids are reassigned from 1
- **R1309:** Forward lookup: prefix scan `V[tag]\x00[value]\x00` returns one record with tvid in key suffix
- **R1310:** Reverse lookup: scan V prefix, parse tvid from trailing bytes of each key

### F Record TVIDs
- **~~R1311:~~** (Retired T14 — see R1875) F record value is extended: `count:4bytes + packed tvid varints` for all tag-value pairs of that tag in that file
- **~~R1312:~~** (Retired T15 — see R1899) On file removal or re-index, read F records for the fileid to get all tvids
- **~~R1313:~~** (Retired T16 — see R1900) Remove fileid from exactly those V records identified by F-record tvids (targeted cleanup)
- **~~R1314:~~** (Retired T17 — see R1900) (inferred) Targeted V cleanup replaces the current full-scan approach in `removeFileidFromAllV`

### What Gets Embedded
- **R1285:** Tag names are embedded with hyphens converted to spaces (`design-decision` → "design decision")
- **R1286:** Tag-value compounds are embedded as `"tagname: value"` with colon preserved and hyphens in tag name converted to spaces
- **R1287:** (revised) Tag name embeddings are inline in T records — no separate ET prefix or tag-name-id needed
- **R1288:** (revised) Hyphens→spaces conversion applies to both T (tag name) and EV (tag value) embedding text for word-level semantics

### Embedding Storage
- **R1289:** (revised) Tag name embeddings are stored inline in T records: `T[tag_name]` → `count:4bytes + optional float32 vector (3072 bytes)`. No separate ET prefix.
- **R1290:** EV records store tag-value compound embeddings: key `EV[tvid: varint]`, value raw float32 vector (3072 bytes)
- **R1291:** (revised) Only EV uses a two-byte prefix. Tag name embeddings are inline in the existing T prefix.

### Embedding Lifecycle
- **R1292:** Batch embed after reconcile: scan T records without inline vectors and V records without EV records, embed in the write goroutine
- **R1293:** Incremental: new V records created during indexing are queued for embedding; the next reconcile batch picks them up
- **R1294:** `ark rebuild` drops T record inline vectors and EV records, regenerates alongside V records
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
- **~~R1302:~~** (Retired T142 — see R1791) `ark embed TEXT` embeds a text string and prints the vector as JSON
- **~~R1303:~~** (Retired T143 — see R1792) `ark embed --bench tags` embeds all tag values, reports per-value and total timing
- **~~R1304:~~** (Retired T144 — see R1792) `ark embed --bench chunks` reads real chunks from random indexed files via AllChunks (real chunker boundaries, not fixed-size slices), embeds them, reports timing
- **~~R1587:~~** (Retired T146 — see R1793) `ark embed --bench` accepts `--ctx N` to set the embedding context window size (default 2048). Passed through to Librarian model loading for benchmarking different context sizes.
- **~~R1305:~~** (Retired T145 — no replacement) (inferred) `ark embed` requires a running server (model lives in the Librarian)

### Build
- **~~R1306:~~** (Retired T159 — see R2969) The Vulkan build of gollama avoids SIGILL on Zen 2 (Steam Deck)
- **~~R1307:~~** (Retired T160 — see R2961) The go workspace includes a local gollama with Vulkan-compiled llama.cpp
- **~~R1308:~~** (Retired T161 — see R2967) (inferred) For non-Zen 2 platforms, the standard CPU gollama build should work without Vulkan

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

## Feature: Markdown Viewer/Editor Component
**Source:** specs/viewer.md

### Host Integration
- **R1326:** The viewer is a standalone CM6 component with no dependency on Frictionless or host view framework
- **R1327:** The host passes an API object at construction with: search, tagComplete, tagValueComplete, save, navigate, setTags
- **R1328:** The viewer never calls ark directly — the host adapts its own transport (HTTP or in-process Lua) to the API interface
- **R1329:** Built assets (JS bundle, CSS) are placed in ~/.ark/html/ — no npm runtime dependency

### Tag Parsing
- **R1330:** Tags (`@word: value`) in the document are detected by a Lezer markdown parser extension and produce typed AST nodes
- **R1331:** (inferred) The tag parser must not conflict with email addresses or other `@` usage — the `@word:` pattern (word chars + colon) is the disambiguator

### Tag Widgets
- **R1332:** Any tag: click opens a search panel below the line, search field shows the full tag text pre-selected, user can read results or type to refine
- **R1333:** Schedule tags: date picker widget for the value
- **R1334:** Status tags: dropdown with known values (open, accepted, in-progress, completed, denied, future)
- **R1335:** Ack tags: gap-detection helpers
- **R1336:** Widgets render inline or as line decorations using CM6 WidgetType

### Tag Completion
- **R1337:** `@` at the start of a word triggers tag name completion from the index (D records via tagComplete)
- **R1338:** After the colon in `@tagname:`, triggers value completion from the tag index (via tagValueComplete)

### ark-search Code Blocks
- **R1339:** Fenced code blocks with `ark-search` language tag render as live search result panels
- **R1340:** Three view modes cycle on click: both (source + results), results only, src only
- **R1341:** Default mode order is both,results,src — initial display is the first in the list. ark-search blocks inside search results default to src,both,results (source first, no search fires until user clicks through)
- **R1342:** Code fence accepts optional `mode=` attribute to restrict and order available modes (e.g. `mode=results` for read-only search)
- **R1343:** Edit mode always enables all three modes regardless of the mode attribute
- **R1344:** Markdown results render in read-only CM6 instances with tag widgets active; non-markdown results use pre-rendered HTML
- **R1345:** Search results include complete raw chunk content (full indexed chunk, not hit context), content type, and pre-rendered HTML
- **R1346:** Click a result to navigate (via host navigate call)
- **R1347:** Edit the query in both/src mode, results update live

### Read/Edit Mode
- **R1348:** Default mode is read-only with markdown rendered and widgets active
- **R1349:** Toggle to edit mode for full text editing
- **R1350:** On save: call save(path, content), host re-indexes
- **R1351:** Tag edits can use setTags for atomic tag block updates

## Feature: Ark Search Component
**Source:** specs/ark-search.md

### SearchAPI Interface
- **R1352:** SearchAPI is the search-relevant subset of HostAPI: search, tagComplete, tagValueComplete, navigate, showInFolder
- **R1353:** HostAPI extends SearchAPI with CM6-specific methods (save, setTags)
- **R1354:** The search component depends only on SearchAPI, not HostAPI
- **R1355:** SearchAPI and shared types (SearchResultGroup, SearchChunk, TagCompletionItem, TagValueCompletionItem) live in ark-search, not markdown-editor

### Custom Element
- **R1356:** `<ark-search>` is a standard custom element (HTMLElement, no shadow DOM)
- **R1357:** The element accepts configuration via properties: api (SearchAPI), tag (string), value (string)
- **R1358:** The element initializes on connectedCallback if api is set, or defers until api is assigned
- **R1359:** The element renders a query bar with tag field, regex toggle, value field, and close button
- **R1360:** The element renders a scrollable results area below the query bar
- **R1361:** The element renders a drag-to-resize handle below the results area
- **R1362:** Value input and tag input changes trigger debounced search (300ms)
- **R1363:** Enter key in either input triggers immediate search

### Result Rendering
- **R1364:** Results are rendered as plain HTML — file path link, optional show-in-folder button, chunk previews
- **R1365:** Chunk previews use the pre-rendered HTML from SearchChunk.preview (no CM6 dependency)
- **R1366:** Click on a result path calls api.navigate(path)
- **R1367:** Show-in-folder button appears when api.showInFolder is defined

### Package Structure
- **R1368:** ark-search/ is a sibling directory to markdown-editor/ with its own package.json and tsconfig.json
- **R1369:** ark-search has no runtime dependencies (pure DOM)
- **R1370:** markdown-editor imports SearchAPI types and the element from ark-search via relative path
- **R1371:** The final bundle is still one esbuild output from markdown-editor — no separate ark-search bundle

### Extraction Scope
- **R1372:** TagSearchPanelWidget rendering logic moves from tag-widget.ts to the custom element
- **R1373:** renderTagSearchResults moves from tag-widget.ts to the custom element
- **R1374:** Tag decoration code (TagSearchWidget, StatusWidget, createOpenSearchPanels, buildTagDecorations, needsRedecoration, toggleSearchPanel) stays in markdown-editor/tag-widget.ts
- **R1375:** search-result-view.ts stays in markdown-editor (CM6-specific rendering)
- **R1376:** ark-search-block.ts stays in markdown-editor (CM6 ViewPlugin)
- **R1377:** tag-widget.ts creates an `<ark-search>` element when a tag panel opens, instead of rendering the panel inline

### Three-Phase Progressive Search
- **R1384:** SearchAPI gains optional methods: embedMatch, expandSearch, curateRequest, curateResult
- **R1385:** If embedMatch and expandSearch are absent, the element uses trigram-only search (phase 1)
- **R1386:** Phase 1 (trigram): fires search() immediately, results shown with normal styling
- **R1387:** Phase 2 (embedding): fires embedMatch() in parallel with phase 1, then expandSearch() for file results, shown muted/bordered
- **R1388:** Phase 3 (curation): fires curateRequest() after phase 2 completes, polls curateResult(), promotes chosen results to full color, strikes through rejected
- **R1389:** Phases 1 and 2 fire in parallel; phase 3 fires after phase 2 completes
- **R1390:** Client-side merge: each phase is a separate response, the element merges progressively
- **R1391:** Phase 2 results that duplicate phase 1 paths are deduplicated — phase 1 takes precedence
- **R1392:** Each result group tracks its source phase for visual treatment
- **R1393:** Phase 2 candidates shown with muted color and a border/icon indicating candidate status
- **R1394:** Phase 3 promoted results change to full color; rejected results get strike-through but remain visible

## Feature: Chunk-Level Filter Closures
**Source:** specs/chunk-filters.md

### resolveChunkLocation
- **R1395:** `resolveChunkLocation` resolves a CRecord to (path, range) using a pre-computed fileIDPaths map
- **R1396:** The fileIDPaths map is computed once per search from FileIDPaths(), not per-chunk

### Filter Constructors
- **R1397:** `ContainsChunkFilter(term, cache, paths)` returns a ChunkFilter that substring-matches chunk text (case-insensitive)
- **R1398:** `FuzzyChunkFilter(term, cache, paths)` returns a ChunkFilter that fuzzy-matches chunk text (typo-tolerant)
- **R1399:** `TagChunkFilter(tag, value, mode, store)` returns a chunk-precise ChunkFilter built from T/V records — F-record ChunkID for name-only, V-record chunkIDs (`TagValueChunks`) for name+value. `chunkIDChunkFilter(set)` provides the chunkID membership predicate over `crec.ChunkID`. No chunk text reads.
- **R1400:** `without` polarity negates the filter: `func(c) { return !filter(c) }`
- **R1401:** If chunk text cannot be read (cache miss), the filter returns true (keep — can't verify, don't reject)
- **R2959:** `resolveChunkLocation` returns unresolved for a `CRecord` with no attached DB (`CRecord.DB() == nil`) instead of calling `FileRecord` on it. Overlay (tmp://) records are unattached, so a `-fuzzy`/`-contains` chunk filter over the overlay would otherwise dereference a nil `*microfts2.DB` and panic the search actor (crash the server); the guard folds into the R1401 can't-verify-keep degradation.

### Endpoint Integration
- **R1402:** `handleSearchGrouped` gains a `chunk_filters` request field: array of `{polarity, mode, query}` objects
- **R1403:** Each chunk filter row becomes a `WithChunkFilter` search option; multiple filters AND together
- **R1404:** Regex-mode chunk filters use `WithRegexFilter`/`WithExceptRegex` instead of ChunkFilter (more efficient)
- **R1405:** Files-mode filters continue to use the existing `resolveFilters` path (ID-level, not chunk-level)

## Feature: Stacked Filter Row UI
**Source:** specs/ark-search.md

### Base Query Bar
- **R1406:** The base query bar has a mode dropdown (`tag`, `contains`, `fuzzy`, `regex`) and a swappable inputs area
- **R1407:** The base query drives scoring and ranking; filter rows only narrow/exclude
- **R1408:** `tag` is the default mode for the interactive bar. In `tag` mode the inputs area shows structured fields (`@ name : [match] value`) mirroring the filter-row tag UI; the element translates them to a regex query before calling the server (no `tag` mode exists on `/search/grouped`). When tag/value properties are set programmatically (tag click), the bar enters `tag` mode with those fields pre-filled. Bar-hidden code-block usage defaults to `contains` so programmatic `query` strings work as free text.

### Filter Row Model
- **R1409:** Filter rows are stackable: a `[+ add filter]` button adds new rows
- **R1410:** Each row has: polarity dropdown (with/without), mode dropdown, query input, remove button
- **R1411:** Filter modes: contains, fuzzy, regex, tag, files
- **R1412:** Contains/fuzzy/regex rows have a free text input
- **R1413:** Tag rows have structured `@[name]: [value]` fields with a match mode toggle (exact / `.*` / `~`)
- **R1414:** Tag rows with empty value match any value (tag must be present)
- **R1415:** Files rows have a comma-separated glob pattern input

### Server Integration
- **R1416:** Contains/fuzzy/tag filter rows are sent as `chunk_filters` JSON array to handleSearchGrouped
- **R1417:** Regex filter rows are sent as `chunk_filters` with mode "regex" (uses WithRegexFilter/WithExceptRegex)
- **R1418:** Files filter rows are sent as `filter_files`/`exclude_files` (path-level, existing resolveFilters)

### Source-Type Bar
- **R1419:** The source-type bar is a permanent row of icon toggles (data/project/memory/chats)
- **R1420:** Each source type maps to path patterns fed to `filter_files`/`exclude_files`
- **R1421:** If the user adds any `[files]` filter rows, the source-type bar grays out — user file filters replace source-type filters entirely
- **R1422:** Removing all `[files]` filter rows restores the source-type bar

## Feature: Content Iframe Previews
**Source:** specs/content-iframe.md

### Content Endpoint Query Params
- **R1423:** `/content/PATH` gains `range` query param — serves only the chunk identified by the range label
- **R1424:** Range resolution uses microfts2's chunk cache (same opaque range strings as SearchChunk.range)
- **R1425:** If range is invalid or unresolvable, falls back to serving the full file
- **R1426:** `toggle=false` hides the pencil/eye toggle button in the HTML template
- **R1427:** `edit=true` auto-loads the CM6 editor in read mode on page load (skips static goldmark view)

### Template Changes
- **R1428:** contentShellData gains HideToggle, AutoEdit, IsChunk boolean fields
- **R1429:** Template conditionally hides #toggle-btn when HideToggle is true
- **R1430:** Template auto-loads CM6 editor when AutoEdit is true (hide #content, show #editor, load on DOMContentLoaded)

### Auto-Height for Iframes
- **R1431:** When loaded in an iframe, the content page posts body height via postMessage({type: 'ark-content-height', height: N})
- **R1432:** Height is posted on load and on resize

## Feature: Query Expansion and OR Groups
**Source:** specs/ark-search.md

### Expand Button
- **R1433:** Filter rows with tag or fuzzy mode show an expand button when api.embedMatch is available
- **R1434:** Clicking expand calls embedMatch with the filter term, producing TagMatch[] alternatives
- **R1435:** The original row is replaced by an OR group of exact-match rows, one per alternative
- **R1436:** Regex and files mode rows do not show an expand button
- **R1437:** Contains mode expansion is deferred (needs Librarian endpoint)

### OR Group Model
- **R1438:** An OR group is a visual grouping of filter rows with OR semantics — any row matching includes the result
- **R1439:** The group inherits the original row's polarity (with/without)
- **R1440:** Individual rows within an OR group can be removed
- **R1441:** Removing all rows in an OR group collapses it (removes the group)
- **R1442:** OR groups are visually distinguished with a border and "OR" label

### Serialization
- **R1443:** OR groups serialize as a single regex chunk_filter that ORs the alternatives
- **R1444:** Tag OR groups serialize as `@(name1|name2):\s*value` regex
- **R1445:** Contains OR groups serialize as `(term1|term2|term3)` regex
- **R1446:** The polarity maps to the existing with/without regex filter path

## Feature: Filter Persistence
**Source:** specs/ark-search.md

- **R1447:** Filters persist across searches within a session (element state) — already implemented
- **R1448:** A chip bar below the filter rows shows saved filter presets
- **R1449:** `[+ save]` button prompts for a name and saves current filter groups to localStorage
- **R1450:** Clicking a chip loads the saved filter configuration into the element
- **R1451:** `x` button on a chip removes it from localStorage
- **R1452:** Saved presets stored in localStorage under key `ark-search-filters` as JSON
- **R1453:** Chips serialize FilterGroup arrays — restore recreates the group/row state

### Tag Name Matching (Base Query + Filter Rows)
- **R1454:** Tag mode has a name match toggle cycling between `contains` (`~`) and `exact` (`=`). The toggle renders between `@` and the name input on both the base query bar and tag filter rows.
- **R1455:** `contains` (default for user-typed queries) builds the name regex as `(^|\s)@[\w.-]*NAME[\w.-]*:` so `project` matches `project`, `to-project`, `from-project`. `exact` builds `(^|\s)@NAME:`. The `(^|\s)` boundary heuristic avoids matching tags deep inside quoted strings — good enough for practical use, per the ark tag definition that tag names are preceded by start of line or whitespace.
- **R1456:** The play-button path (`set tag()` from a tag click) forces the name match mode to `exact` — the user is exploring "that one tag". Concrete rows produced by `expandRow` (embedMatch results) also default to `exact`.
- **R1457:** Tag value input is tokenized on whitespace; all tokens must appear on the tag value line (AND semantics, order-independent). `@ project: ark component` matches tag lines whose value contains both `ark` AND `component`. Built as lookaheads in the regex so word order doesn't matter.
- **R1458:** Contains-name tag filter rows serialize as `regex` chunk filters (the server's tag chunk filter matches names literally). Exact-name rows continue to use the fast `tag` chunk filter path. Follow-up: enhance the Go tag filter to accept regex for the name so contains-name can use the tag index.

### Match Highlighting (Iframe Chunk Previews)
- **R1459:** `<ark-search>` appends `highlight=<regex>` query params to each iframe preview URL, one per regex. For tag mode: one regex for the name prefix plus one per value token so each token highlights independently. For contains/regex modes: one regex (escaped literal or raw). Fuzzy mode emits no highlight (no clean regex translation).
- **R1460:** `content-markdown.html` parses `highlight` params via `URLSearchParams.getAll('highlight')` and passes them to `createInkArkEditor({ highlights: ... })`.
- **R1461:** The `highlight-extension` CM6 ViewPlugin compiles the regex strings, walks `view.visibleRanges`, and applies `Decoration.mark({class: "ark-search-highlight"})` for every hit. Updates on `docChanged` or `viewportChanged`.
- **R1462:** On first render with non-empty highlights, the extension dispatches `EditorView.scrollIntoView(firstMatch, {y: "center"})` in a microtask so the iframe opens scrolled to the first match.
- **R1463:** `.ark-search-highlight` CSS lives in `content-markdown.html` alongside the other `ark-search-*` theme rules, using `--term-accent-dim` fill, `--term-accent-bright` text, and `--term-accent` ring so highlights read as part of the panel palette.

### Results Flicker Elimination (path-keyed diffing, ready signal, live highlights)
- **R1464:** `<ark-search>` caches result group elements in a path-keyed map (`resultEls`) with a chunk signature, a highlight signature, and the current phase. `renderResults` reuses matching cache entries in place: same path + same chunk signature = same DOM subtree, iframes included. Orphan paths are removed from both the cache and the DOM. Reordering uses `insertBefore` on already-attached nodes so iframes keep their `contentWindow` and never reload. Phase 1 → Phase 2 → Phase 3 transitions for the same search are visually silent.
- **R1465:** The `/content/` page posts `{type: 'ark-content-ready', src}` to its parent once the CM6 editor has finished loading, highlights have been applied, and `postHeight()` has fired. New iframes built by `<ark-search>` start at `opacity: 0` with a CSS `transition: opacity 0.2s ease-in`; the element's `message` listener flips the matching iframe to `opacity: 1` on the ready signal. This hides the gray "iframe loading" state behind an invisible element and swaps the finished preview in cleanly.
- **R1466:** When the user edits the query in a way that changes only the highlight patterns (same result path, same chunks), `<ark-search>` calls `updateGroupHighlights` instead of rebuilding. Loaded iframes receive a `{type: 'ark-set-highlights', patterns}` postMessage; lazy-unloaded iframes get their `dataset.src` URL rewritten. Inside the iframe, the `highlightExtension` ViewPlugin has a message listener that dispatches a `setHighlightPatternsEffect` on its own `EditorView`; a `StateField` recompiles the regex list and the plugin rebuilds only the decoration marks — no iframe reload, no DOM churn, no flicker.
- **R2132:** Each highlight regex in the URL param list claims a distinct range. The highlighter tracks taken ranges across regexes; when a match's claimed range overlaps an earlier-taken range (typically a duplicate regex string in the list), it falls back to a literal search for the captured group's text past the taken range, bounded to the same line as the original match. This makes `N` copies of the same line-anchored regex highlight the first `N` occurrences of the token in the matched line, instead of piling onto the first occurrence.

### Tag Name Contains-Tokens (server-side T/V record resolution)
**Source:** specs/tag-name-contains-tokens.md

- **R1467:** `Store.MatchTagNames(tokens []string)` scans T records and returns tag names where every token appears as a case-insensitive substring of the name. Linear scan — the T record set is small (hundreds to low thousands). Single-token input degenerates to simple substring match.
- **R1468:** `Store.MatchTagValues(tag string, tokens []string)` scans V records for a given tag name and returns values where every token appears as a case-insensitive substring. Each result carries the chunkIDs decoded from the V-record value blob; callers that need fileIDs resolve through `filesForChunk`.
- **~~R1469:~~** (Retired T64 — see R2442) `handleSearchGrouped` accepts an optional structured tag query: name tokens, value tokens, and match modes (`name_tokens`, `value_tokens`, `name_match`, `value_match`). The server resolves the chunkID set through `resolveTagChunks` (matched names via R1467, then F-record chunkIDs for name-only or V-record chunkIDs via R1468 for name+value). When no other text primary is set, FTS is bypassed entirely — `Searcher.GroupTagChunks` builds GroupedResult directly from the chunkIDs (path/range via C+F record reads, stale chunkIDs skipped). When combined with a text primary, the chunkID set overlays as a `WithChunkFilter` (`chunkIDChunkFilter`) on top of the chosen FTS pipeline.
- **R2129:** "No text primary" in R1469 means an empty query string, regardless of `mode`. A request with `mode: "regex"` (or any text-primary mode) and `query: ""` routes through the tagOnly fast path — the mode field is leftover UI state, not an instruction to run an empty regex against every chunk in the chunkID filter.
- **~~R1470:~~** (Retired T65 — see R2442) `ChunkFilterRow` gains a `"tag-contains"` mode. Query format: `token1 token2:value1 value2` (space-separated name tokens before `:`, value tokens after). `BuildChunkFilters` constructs a chunk-precise filter via `TagContainsChunkFilter`: matched names from R1467, chunkIDs from R1468 (`TagValueMatch.ChunkIDs`) for the value branch and from F-record ChunkID for the name-only branch. Membership tested via `chunkIDChunkFilter` against `crec.ChunkID`.
- **R1471:** `BuildChunkFilters` accepts a `*Store` parameter so it can resolve T and V records for `"tag-contains"` mode. Existing modes (`contains`, `fuzzy`, `tag`) are unchanged.
- **~~R2128:~~** (Retired T69 — see R2445) `TokenizeTagValue(s string) []string` (in search.go) splits a tag value into tokens with shell-style quoting: whitespace separates tokens; double quotes group whitespace-containing runs into a single token; backslash escapes the next rune (inside or outside quotes), allowing literal `"`, `\`, or space within a token. Unmatched trailing quote or backslash is tolerated; empty tokens are dropped. Used by `TagChunkFilter` (CLI `-tag name:value` and chunk-filter rows) so quoted multi-word values like `meal:"french toast"` produce a single substring token. Browser clients that send `value_tokens` as a JSON array bypass this — they tokenize client-side.
- **~~R1472:~~** (Retired T66 — see R2442) On the client, `buildTagQuery()` for contains-name sends structured fields (`name_tokens`, `value_tokens`, `name_match`, `value_match`) in the search request instead of building a client-side regex. The server resolves and searches. Exact-name continues to send a regex query string as before.
- **~~R1473:~~** (Retired T67 — see R2442) On the client, `collectChunkFilters()` sends `mode: "tag-contains"` with `query: "token1 token2:value1 value2"` for contains-name filter rows, replacing the `mode: "regex"` fallback. Exact-name filter rows continue to use `mode: "tag"`.
- **~~R1474:~~** (Retired T68 — see R2442) Supersedes R1455 (client-side regex for contains-name) and R1458 (regex chunk filter fallback). The contains-name path now goes through the server's T/V record index.
- **R1475:** Highlight regexes (`buildHighlightRegexes`, `tagRowRegex`) continue to build client-side regexes from the name and value tokens — these are for iframe rendering, not search.

## Feature: ark-tag-element
**Source:** specs/ark-tag-element.md

### Component

- **R1476:** `<ark-tag>` is a custom element (no shadow DOM) that renders an interactive tag widget in read-only content. It inherits the host page's theme CSS.
- **R1477:** Markup structure: `<ark-tag><name>TAG</name> <value>VALUE</value></ark-tag>`. The `<name>` and `<value>` child elements carry only the semantic parts — no punctuation in the markup.
- **R1478:** CSS `content` generates the `@` prefix on `name::before` and `:` suffix on `name::after`. Punctuation color uses `--term-text`. Tag name color uses `--term-accent-bright`. Tag value color uses `--term-success`.
- **R1479:** Without JavaScript loaded, the element degrades to readable plain text: `TAG VALUE`.
- **R1480:** Hover cursor indicates clickability.

### Click and Inline Search

- **R1481:** Clicking an `<ark-tag>` element toggles an inline `<ark-search>` panel in the document flow, inserted after the tag's parent block element. The panel is pre-filled with the tag's name and value.
- **R1482:** Only one inline `<ark-search>` panel may be open at a time per content page. Opening a new one closes the previous.
- **R1483:** The `<ark-tag>` element locates a `SearchAPI` instance via `document.arkSearchAPI` to pass to the created `<ark-search>` element.
- **R1484:** Clicking the element also dispatches a bubbling `ark-tag-click` custom event with `detail: { name, value }` for external listeners.

### Server-Side Post-Processing

- **R1485:** A Go function wraps `@tag: value` patterns in `<ark-tag>` elements. Input is rendered HTML; output is HTML with tag patterns replaced. Tag names follow ark's definition: `[a-zA-Z][\w.-]*`.
- **R1486:** The post-processing matches tag values to end of line (or `<br>` / `<br />` in goldmark output). Values are trimmed of leading whitespace.
- **R1487:** Tags already inside an `<ark-tag>` element are not re-wrapped (idempotency).
- **R1488:** The post-processing must not match tag patterns inside HTML attributes or element tag names.
- **R1489:** `handleContentView` applies post-processing to the goldmark-rendered HTML for markdown files (after `renderMarkdownForContent`) and to the HTML-escaped text for plain-text files (after `HTMLEscapeString`).

### Content Page Integration

- **R1490:** `content-markdown.html` sets `document.arkSearchAPI` to the page's `api` object. The `<ark-search>` element and its CSS are already loaded on this page.
- **R1491:** `content-plain.html` loads the `<ark-search>` element script (as a module import) and sets `document.arkSearchAPI` so inline search panels work for plain-text files.
- **R1492:** The `<ark-tag>` component definition is inlined in both HTML templates — it is small enough that a separate bundle is unnecessary.

### Scope Boundary

- **R1493:** `<ark-tag>` must never appear inside the CM6 editor. The server applies post-processing only to the read-view content (`#content` div for markdown, `<pre>` for plain text). The CM6 editor manages its own tag decorations via `tag-widget.ts`.
- **R1494:** The CM6 tag system (`TagSearchWidget`, `StatusWidget`, completion) and the `<ark-tag>` element are independent — they share no code or state.

## Feature: chunked-content-view
**Source:** specs/chunked-content-view.md

### Chunk Rendering

- **R1495:** `handleContentView` for non-markdown files renders chunk-extracted text instead of raw file content. Each chunk from the index becomes a `<div class="ark-chunk" data-range="RANGE">` containing the chunk's extracted text.
- **R1496:** Chunk text is HTML-escaped, then `wrapTagElements` runs on each chunk independently. Tags in the extracted text are real tags — the chunker's use/mention filtering is already applied.
- **R1497:** Chunks are separated by a subtle visual border (`border-bottom` on `.ark-chunk`, none on the last child).
- **R1498:** `.ark-chunk` uses `white-space: pre-wrap` and `word-wrap: break-word` for text formatting — no nested `<pre>` element.
- **R1499:** If the file has no chunks in the index (unindexed or newly added), fall back to the current raw `<pre>` rendering.

### Unchanged Paths

- **R1500:** Markdown files continue through the goldmark rendering path — no change.
- **R1501:** The `range=` query parameter for single-chunk views continues to work (iframe previews use this). Chunk resolution runs before the full-file chunk rendering.
- **R1502:** `/raw/` serves the unprocessed file — unchanged.
- **R1503:** The `/fetch` endpoint returns raw content — unchanged. The CM6 `autoEdit` path is unaffected.

### Chunk Access

- **R1504:** The server uses the DB's `ChunkCache` to read all chunks for the file. The cache handles reading the file and running the appropriate chunker (determined by the file's strategy).

### Markdown Rendering for JSONL Chunks

- **R1505:** For files with strategy `chat-jsonl`, each chunk's extracted text is rendered through goldmark (same as the markdown content path). The extracted content is markdown written by humans and AI assistants — goldmark gives proper headings, code blocks, lists, and inline formatting.
- **R1506:** For other non-markdown strategies (bracket, indent, lines), chunk text is HTML-escaped as pre-wrapped text.
- **R1739:** For files with strategy `pdf`, chunks are grouped by their `page` attribute and each page emits one `<pdf-chunk>` element covering the full page (rect `0,0,PAGE_W,PAGE_H`, taken from the chunks' shared `page_size` attribute). All `tag_rects` from every chunk on that page are concatenated (semicolon-separated) and attached to the page-level `<pdf-chunk>` so every tag on the page overlays the rendered page. Per-Block `<pdf-chunk>` elements are not used in this view because Block rects leave visible gaps between text regions. Search result previews (R1703–R1707) remain per-Block — the narrower scope suits a single hit.
- **R1740:** Pages with no chunks carrying a `page_size` attribute fall through to the HTML-escaped pre-wrapped path. Salvage chunks (no `rect`) contribute their `tag_rects` to the page overlay when they share a page with structured chunks; they do not force the page to fall back on their own.

### Chat-JSONL Role Rendering

- **R1507:** `extractJSONLTextFast` extracts the `type` and `isMeta` fields from the JSONL line and stores a `role` chunk attr: `human` (type=user, no isMeta), `skill` (type=user, isMeta=true), or `assistant` (type=assistant). Uses the existing microfts2 chunk `Attrs` mechanism.
- **R1508:** For skill chunks, `extractJSONLTextFast` parses the `Base directory for this skill: PATH` first line and stores the last path component as a `skill` attr (e.g. `ark`, `mini-spec`).
- **R1509:** In the full-file content view, the server groups consecutive same-role chunks into a wrapper `<div class="ark-role-group ark-role-ROLE">`. A new group starts when the role changes. Chunks without a role attr render as ungrouped standalone divs.
- **R1510:** Each human/assistant role group contains a header `<div class="ark-role-header">` with a role icon: 👤 for human, 🤖 for assistant. The header has `position: sticky; top: 0` so the icon stays pinned at the viewport top while scrolling. `background: inherit` keeps the header opaque.
- **R1511:** Skill groups use `<details>/<summary>` and are collapsed by default. The summary shows a 📋 icon and the skill name from the `skill` attr. Click to expand.
- **R1512:** Each role group has a left border in a role-specific theme color: `--term-text-dim` for human, `--term-accent-bright` for assistant, `--term-border` for skill. The border runs the full height of the group.
- **R1513:** In single-chunk views (`range=` parameter), the chunk renders with the role's left border color and a small icon but no sticky header and no grouping.

## Feature: chunk-stats
**Source:** specs/chunk-stats.md

### Activation

- **R1514:** `ark status --chunks` activates chunk size statistics. Without `--chunks`, behavior is unchanged.
- **R1515:** `--filter-files GLOB` and `--exclude-files GLOB` (repeatable) scope the file set for chunk stats. Same semantics as search filtering.
- **R1516:** When neither filter flag is specified, all indexed files are included.

### Data Collection

- **R1517:** For each file in scope, use `DB.AllChunks(path)` to read chunk content from disk.
- **R1518:** Byte size is `len(chunk.Content)`. Token count (when `--tokenize`) is `len(ctx.Tokenize(chunk.Content))`.
- **R1519:** Files that fail to read are skipped silently (they appear in the normal missing count).
- **R1520:** Strategy for each file comes from the FTS FileStatus record.

### Statistics

- **R1521:** Compute: count, min, max, mean, median, P90, P95, P99 for the chunk sizes.
- **R1522:** Compute overall stats across all chunks ("all" row) and per-strategy stats.

### Output Format

- **R1523:** Output is a right-aligned table with columns: strategy, count, min, max, mean, median, p90, p95, p99.
- **R1524:** First row is "all" (aggregate). Subsequent rows are per-strategy, sorted alphabetically.
- **R1525:** Header line above the table labels the unit: "chunk sizes (bytes):" or "chunk sizes (tokens, MODEL):" where MODEL is the model filename stem.
- **R1526:** Columns are right-aligned, padded to the widest value in each column. Strategy column is left-aligned.
- **R1527:** Zero chunks after filtering: print "no chunks found" instead of the table.

### Tokenization

- **R1528:** `--tokenize` loads the configured embedding model and counts tokens per chunk instead of bytes.
- **R1529:** Create a minimal llama context (small `n_ctx`, no `WithEmbeddings()`) for tokenization only — no KV cache or embedding overhead.
- **R1530:** Use the `[embedding] model` path from ark.toml as the model path.
- **R1531:** `--tokenize` without a configured `[embedding] model`: print error and exit.

## Feature: config-tracking
**Source:** specs/config-tracking.md

### I Records: Config Storage

- **R1532:** Each Config struct field is stored as a separate I record with key `I` + field name.
- **R1533:** Known I record names are Go string constants (pseudo-enum).
- **R1534:** Scalar config fields (dotfiles, case_insensitive, etc.) store their string representation as the I record value.
- **R1535:** Compound config fields (sources, chunkers, global_include, etc.) store JSON as the I record value.
- **R1536:** Operational fields (next_tvid counter, etc.) also use I records, same key format.
- **R1537:** `makeIKey(name)` builds the index key: `I` prefix byte + name bytes. Same pattern as microfts2.
- **R1538:** `iGet`/`iPut`/`iDel` helpers for string values. `iGetCounter`/`iSetCounter` for uint64 counters.

### I Record Lifecycle

- **R1539:** Init writes all config fields from ark.toml to I records.
- **R1540:** Open reads I records and diffs against loaded ark.toml. Classifies changes by field.
- **R1541:** Config mutations (`ark config add-source`, etc.) write ark.toml. The next Open or watcher reload diffs and updates I records.
- **R1542:** Rebuild clears all I and E records, writes fresh config from ark.toml.

### E Records: Error Conditions

- **R1543:** E records use key `E` + condition name → JSON payload.
- **R1544:** E records persist across restarts and are surfaced in `ark status`.
- **R1545:** E records are auto-cleared when the condition resolves (config changed back, rebuild, or manual fix).
- **R1546:** Known E conditions: `model_mismatch`, `index_stale`, `config_catastrophe`.
- **R1547:** `model_mismatch` payload: `{"stored":"old_model","current":"new_model"}`.
- **R1548:** `index_stale` payload: names the changed field (case_insensitive, chunkers).
- **R1549:** `config_catastrophe` payload: stored config summary for recovery.

### Change Classification

- **R1550:** `case_insensitive` change is classified as deferred (option 1).
- **R1551:** `chunkers` change is classified as deferred (option 1).
- **R1552:** All sources removed is classified as deferred (option 1) — likely accidental config wipe.
- **R1553:** `[embedding] model` change is classified as fix-minimal (option 2): delete all T vector and EV embedding records, update I record to new model.
- **R1554:** `sources` add/remove, `global_include`/`global_exclude`, `dotfiles`, `search_exclude`, `session_ttl`, `schedule`, `strategies`, `embed_cmd`/`query_cmd` are classified as benign.
- **R1555:** Benign changes update I records immediately and proceed normally.

### Startup Behavior

- **R1556:** On `ark serve` startup, load ark.toml and diff against stored I records.
- **R1557:** If deferred changes or unresolved E records are detected, error out with diagnostic showing stored vs current values. Suggest `ark rebuild` or `--force`.
- **R1558:** `--force` on `ark serve` clears E records, accepts current config, updates all I records, applies fix-minimal where applicable.
- **R1559:** Fix-minimal changes at startup: apply fix, update I records, log warning, proceed.
- **R1560:** Benign changes at startup: update I records silently, proceed.

### Runtime Behavior (Watcher Reload)

- **R1561:** On watcher config reload, diff new config against I records.
- **R1562:** Deferred changes at runtime: write E record, log error, keep running with stored config for those fields.
- **R1563:** Fix-minimal changes at runtime: apply fix, update I records, log warning.
- **R1564:** Benign changes at runtime: update I records, proceed.

### Status Display

- **R1565:** `ark status` prints E record conditions after normal output under a "warnings:" header.
- **R1566:** Each E record condition prints its name, a human-readable description, and remediation advice.
- **R1567:** When no E records exist, nothing extra is printed.

### Recovery

- **R1568:** `ark rebuild` clears all E records (database is recreated with fresh I records from ark.toml).
- **R1569:** `ark config recover` (new command) reads stored config from I records and writes ark.toml. Disaster recovery for corrupted/missing config.

### ArkSettings Removal

- **~~R1570:~~** (Retired T171 — see R1571) The old `ArkSettings` struct and single-blob I record format are removed.
- **R1571:** `GetSettings`/`PutSettings` are replaced by per-field `iGet`/`iPut` calls.
- **R1572:** The `Extra` map entries (schedule config, ID counters) become their own I record keys.

## Feature: files-status
**Source:** specs/files-status.md

### Filtering

- **R1573:** `--filter-files GLOB` and `--exclude-files GLOB` (repeatable) set the base file set on `ark files`. Same semantics as search filtering.
- **R1574:** Positional glob arguments further narrow the result within the base set.
- **R1575:** When neither `--filter-files` nor positional patterns are given, all indexed files are included.

### Status Output

- **R1576:** `--status` shows bytes, chunk count, and path per file in addition to G/S/M status.
- **R1577:** Columns: status, bytes, chunks, path. Numeric columns are right-aligned.
- **R1578:** Missing files show 0 for bytes and chunks.
- **R1579:** File bytes come from `os.Stat` (actual file size on disk).
- **R1580:** Chunk count and chunk content come from `DB.AllChunks(path)`.

### Verbose Mode

- **R1581:** `-v` shows per-file chunk size statistics as an indented detail line after the summary line.
- **R1582:** Verbose stats: min, max, mean, median, p90, p95. Compact single-line format, indented two spaces.
- **R1583:** Missing and zero-chunk files skip the verbose line.
- **R1584:** Chunk sizes are `len(chunk.Content)` in bytes (or tokens with `--tokenize`).

### Tokenize

- **R1585:** `--tokenize` loads the embedding model tokenizer and counts tokens instead of bytes for chunk size stats.
- **R1586:** `--tokenize` without a configured `[embedding] model`: print error and exit.

## Feature: Chunk Embeddings
**Source:** specs/chunk-embeddings.md

### Configuration

- **~~R1588:~~** (Retired T152 — see R2965) `ark.toml` accepts an `[[embed_tiers]]` array. Each entry has `ctx` (context window tokens) and `parallel` (sequences per batch).
- **R1589:** Tokens-per-sequence is derived: `ctx / parallel`. Byte limit is derived: `tokens_per_seq * 3`.
- **R1590:** Tiers are sorted by byte limit ascending at load time. Chunks route to the smallest tier that fits.
- **R1591:** When `[embedding] tiers` is absent but `[embedding] model` is set, default tiers are used (1024/32, 2048/16, 2048/8, 16384/12, 16384/8).
- **R1592:** (inferred) Invalid tier configs (ctx <= 0, parallel <= 0, parallel > ctx) are rejected at config load.

### Model and Context Lifecycle

- **R1593:** One embedding model is loaded from `[embedding] model`, shared across all tier contexts.
- **R1594:** All tier contexts are pre-allocated from the loaded model on first embedding use (lazy).
- **~~R1595:~~** (Retired T153 — see R2962) Each context is created with `WithEmbeddings()`, `WithContext(ctx)`, `WithBatch(ctx)`, and `WithParallel(parallel)`.
- **R1596:** The model TTL timer unloads the model and all contexts when the embedding queue is idle.
- **R1597:** Tag and query embedding use the tier with 256 tokens/seq (2048/8 default).

### Index Records

- **~~R1598:~~** (Retired T1 — see R1833) EC records store chunk vectors. Key: `EC` + varint(fileID) + varint(chunkIdx). Value: float32 vector (768 dims).
- **R1599:** EF records store file centroids. Key: `EF` + varint(fileID). Value: float32 running sum (768 dims) + uint32 chunk count.
- **~~R1600:~~** (Retired T2 — see R1836) `WriteChunkEmbedding(fileID, chunkIdx, vec)` writes one EC record.
- **~~R1601:~~** (Retired T3 — see R1838) `ReadChunkEmbedding(fileID, chunkIdx)` reads one EC record.
- **R1602:** `WriteFileCentroid(fileID, sum, count)` writes one EF record (running sum + count).
- **R1603:** `ReadFileCentroid(fileID)` reads one EF record, returns sum and count.
- **R1604:** `MissingChunkEmbeddings()` returns chunks with C records in microfts2 but no EC record in ark store.
- **R1605:** `ScanFileCentroids()` returns all EF records as a map.
- **R1606:** `DropChunkEmbeddings()` deletes all EC and EF records (for rebuild or model mismatch).
- **~~R1607:~~** (Retired T4 — see R1849) EC records for a file are deleted when the file is re-indexed.
- **R1608:** (inferred) EF centroid is recomputed from scratch when a file is fully re-indexed.

### Batch Embedding Pipeline

- **R1609:** `BatchEmbedChunks()` runs post-reconcile after `BatchEmbed()` (tag embeddings).
- **R1610:** Scans for missing EC records, reads chunk content via `AllChunks(path)`.
- **R1611:** Priority sort: tag-bearing files first, then non-JSONL authored content, then JSONL. Files matching `search_exclude` are skipped.
- **R1612:** Each chunk routes to the smallest tier whose byte limit fits `len(content)`.
- **R1613:** Chunks exceeding all tiers' byte limits are skipped (logged at verbose level).
- **R1614:** When a tier's bucket reaches its `parallel` count, the batch is dispatched through that tier's context via `EmbedBatch`.
- **R1615:** EC records are written to the index through the DB actor (GPU compute happens off-actor).
- **R1616:** After all chunks for a file are embedded, the EF centroid is updated (running sum approach).
- **R1617:** When all files are processed, all buckets are flushed — no embedded content is left in a partial bucket.

### Input Sanitization

- **R2991:** Ark replaces NUL bytes (`\x00`) with spaces at the single point where any embedding path tokenizes, so no NUL byte reaches the yzma/llama.cpp tokenizer (a NUL ends the C string early and aborts the process); a space rather than deletion keeps adjacent tokens from fusing. Query embedding, batch chunk embedding, and the standalone token counter are all covered by the one guard.
- **R2992:** When the batch-embed pipeline finds a NUL byte in a chunk it logs the offending chunk's path and id.

### Incremental Centroid Updates

- **R1618:** File centroids are stored as a running sum + count — the EF record holds the element-wise sum of the file's embedded chunk vecs plus the count of embedded chunks. The centroid is accumulated by summing chunk vecs (`sum += vec; count++`) and is the stored sum divided by count (R1619).
- **R1619:** Centroid at query time is `sum / count`.
- **~~R1623:~~** (Retired T222 — see R3004) EF centroid count includes permanently-skipped (oversized) chunks so the fast-skip sentinel (`efCount == len(chunkLens)`) terminates correctly for files with chunks exceeding all tier byte limits. Oversized count is only added for fresh centroids; seeded centroids from prior runs already include it.

### Model Mismatch

- **R1620:** If `[embedding] model` changes, all EC and EF records are stale and dropped on next reconcile (extends existing E condition mismatch detection).

### Benchmark

- **~~R1621:~~** (Retired T147 — see R1792) `ark embed --bench chunks` accepts `--parallel N` to set sequences per batch (default 8).
- **~~R1622:~~** (Retired T148 — see R1792) Bench output reports context size, parallel count, tokens/seq, batch vs single throughput, skip rate, and chunk size distribution (min/max/avg).

## Feature: PDF Chunker
**Source:** specs/pdf-chunker.md

### Text Extraction

- @obsolete-req: R1624 -- superseded by R1729 (pdftext replaces seehuhn)
- **R1624:** PDF chunker opens a PDF file, iterates pages, and extracts text spans with position (X, Y in PDF points), font size, and text content using seehuhn.de/go/pdf.
- @obsolete-req: R1625 -- superseded by R1729 (pdftext merges glyphs into Block.Text internally)
- **R1625:** Text spans on the same line (similar Y coordinate, within font-height tolerance) are merged left-to-right into positioned lines with bounding boxes.

### Table Detection

- @obsolete-req: R1626 -- superseded by R1730 (table detection is pdftext's responsibility)
- **R1626:** Detect tables via drawn rules: horizontal and vertical line-drawing operations in the PDF content stream (path operators `re`, `m`, `l`). A grid of ≥2 rows and ≥2 columns is a table.
- @obsolete-req: R1627 -- superseded by R1730
- **R1627:** Detect tables via column alignment: cluster text spans by Y (rows); if multiple rows share ≥2 aligned X positions (within tolerance proportional to dominant font size), the region is a table.
- @obsolete-req: R1628 -- superseded by R1730
- **R1628:** Drawn-rule detection takes priority over column-alignment detection.
- @obsolete-req: R1629 -- superseded by R1730 (Block.Text already carries pdftext's row-structured table text)
- **R1629:** Table chunk content is text spans inside the table region, concatenated row by row.
- **R1630:** Table chunks use location `PAGE/table/N` (1-indexed per page).

### Heading Detection

- @obsolete-req: R1631 -- superseded by R1730 (heading classification is pdftext's responsibility)
- **R1631:** Text spans whose font size exceeds the page's dominant (most common) font size by ≥20% are headings.
- @obsolete-req: R1632 -- superseded by R1730 (pdftext Heading Block stands alone; body follows as its own Block)
- **R1632:** A heading and the body text following it (up to the next heading or structural boundary) form a heading chunk.
- **R1633:** Heading chunks use location `PAGE/heading/N`.

### Paragraph Detection

- @obsolete-req: R1634 -- superseded by R1730 (paragraph grouping is pdftext's responsibility)
- **R1634:** Remaining text (not in tables or headings) is grouped into paragraphs by vertical gap detection: a gap >1.5× the dominant line spacing signals a paragraph boundary.
- **R1635:** Paragraph chunks use location `PAGE/para/N`.

### Page-Level Fallback

- @obsolete-req: R1636 -- superseded by R1733 (pages with no blocks emit no chunks)
- **R1636:** If a page has no detected structure (fewer than 2 text spans, or all text in a single undifferentiated block), the entire page is one chunk with location `PAGE`.

### Chunk Attributes

- **R1637:** Every chunk carries a `page` attribute (page number as string).
- **R1638:** Every chunk carries a `rect` attribute: bounding box as `x,y,w,h` in PDF points (origin = bottom-left per PDF spec).
- **R1639:** Heading chunks carry a `font_size` attribute (dominant font size in the chunk).
- @obsolete-req: R1665 -- partially superseded by R1735 (tag rect source moved from line spans to Block.Chars; tag_rects is also emitted on Salvage blocks now that they carry position info)
- **R1665:** Chunks carry an optional `tag_rects` attribute: per-tag bounding boxes for `@name: value` patterns found in the chunk's positioned text spans. Absent when the chunk has no tags; absent on salvage chunks. Format spec: PDF Chunk Element feature (R1669–R1674).
- **R1719:** Every chunk carries `content_offset` and `content_len` attributes locating its text within the page's cached text blob (byte offset and byte length, decimal strings).

### Chunk Text Cache

- **R1720:** At index time, the PDF chunker writes each page's extracted chunk text into a compressed blob stored in ark's bucket, keyed by `(fileid, page)`.
- **R1721:** Each page's blob contains the concatenated text of every chunk on that page, in emission order, separated by a single null byte.
- **R1722:** Blobs are compressed with zstd.
- @obsolete-req: R1723 -- superseded by R1737 (salvage blocks keyed at their actual page alongside structured blocks)
- **R1723:** Salvage chunks share a single per-file blob indexed as page 0 (salvage chunks have no real page number and arrive in small counts per file).
- **R1724:** Before writing new blobs for a file, the chunker removes all existing blobs for that fileid so stale pages cannot outlive a re-indexed document with fewer pages.
- **R1725:** On file removal, the file's page-content blobs are removed alongside other per-file Store records.
- **R1726:** PDFChunker implements `microfts2.RandomAccessChunker`. `GetChunk` reads `content_offset`/`content_len` from the chunk's Attrs, loads the corresponding page blob from the Store, decompresses, and slices to fill `chunk.Content`.
- **R1727:** `GetChunk` caches decompressed page blobs in `customData` (a `map[page][]byte`) keyed by page number. Because customData lifetime is bounded by the ChunkCache's TTL (minutes), no eviction policy is required.
- **R1728:** If a chunk's Attrs lack `content_offset`/`content_len`, or the page blob is missing from the Store, `GetChunk` falls back to the streaming parse path (run `FileChunks` until the target range is found).

### Chunk Location Format

- **R1640:** Chunk locations use path-style hierarchy: `PAGE/TYPE/N`. Page and chunk numbers are 1-indexed.

### Registration

- **R1641:** PDF chunker registers as strategy `"pdf"` via `microfts2.AddChunker`.
- **R1642:** PDF chunker implements `FileChunker` (indexed files — owns file read, hash-based skip) and `Chunker` (tmp documents — receives raw bytes).
- **R1643:** Strategy assignment via ark.toml `[strategies]` section: `"*.pdf" = "pdf"`. No `[[chunker]]` block needed.

### Blank-Line Filtering

- @obsolete-req: R1661 -- superseded by R1729 (layout-aware line handling is pdftext's responsibility)
- **R1661:** Before any per-page structure detection (tables, headings, paragraphs), lines whose text is entirely whitespace are removed from the line set
- @obsolete-req: R1662 -- superseded by R1729
- **R1662:** Rationale: some PDF generators (notably ONLYOFFICE) emit blank visual lines as real text lines containing only a space glyph; without filtering, gap-based paragraph detection sees consistent line spacing and produces a single paragraph chunk for the entire page
- @obsolete-req: R1663 -- superseded by R1729
- **R1663:** Dropping blank lines causes paragraph-separator gaps to double (two normal gaps collapse into one doubled gap once the blank between them is removed), so the existing 1.5× dominant-spacing threshold fires naturally
- @obsolete-req: R1664 -- superseded by R1729
- **R1664:** The filter also benefits table detection: blank "rows" with no aligned X positions previously diluted the column-alignment signal

### Fallback Text Salvage

- @obsolete-req: R1652 -- superseded by R1734 (graceful degradation is pdftext's responsibility, inline via Salvage BlockKind)
- **R1652:** When `pdf.NewReader` returns any error, the chunker invokes a best-effort salvage pass over the raw bytes instead of returning an error
- @obsolete-req: R1653 -- superseded by R1734
- **R1653:** Salvage scans the raw bytes for `stream\n ... \nendstream` pairs, treating each as a candidate content stream
- @obsolete-req: R1654 -- superseded by R1734
- **R1654:** Salvage inspects the object dictionary immediately preceding the stream for a `/Filter` entry; if `/FlateDecode`, the stream is decompressed with `compress/zlib`; if no filter, the stream bytes are used as-is; other filters cause the stream to be skipped
- @obsolete-req: R1655 -- superseded by R1734
- **R1655:** Within a decoded stream, salvage extracts the text-string argument from the text-showing operators `Tj`, `'`, `"`, and the array form `TJ` (numbers inside `TJ` arrays are kerning and are ignored)
- @obsolete-req: R1656 -- superseded by R1734
- **R1656:** PDF string literals inside the extracted text respect the standard escape sequences: `\(`, `\)`, `\\`, `\n`, `\r`, `\t`, `\b`, `\f`, and three-digit octal `\ddd`
- @obsolete-req: R1657 -- superseded by R1737 (salvage now keyed at actual page, location PAGE/salvage/N)
- **R1657:** Salvage emits one chunk per content stream with location `salvage/N` (1-indexed). Salvage chunks omit the `rect` attribute because coordinates were not consulted
- @obsolete-req: R1658 -- superseded by R1737 (salvage blocks carry their true page and Block.BBox)
- **R1658:** Salvage chunks carry a `page` attribute set to `"1"` and (inferred) no `font_size`; structure detection (tables, headings, paragraphs) is not attempted
- **R1659:** If salvage extracts no text from any stream, the chunker yields nothing — the file takes the standard FileChunker "log once, empty result" path and is skipped on subsequent scans with matching hash
- @obsolete-req: R1660 -- superseded by R1734 (no separate in-ark salvage path to share)
- **R1660:** (inferred) Salvage is invoked from both the byte-input `Chunks` (tmp documents) and the file-input `FileChunks` paths, so tmp PDFs and indexed PDFs both benefit

### pdftext Migration

- **R1729:** PDF chunker uses `github.com/zot/pdftext` for document opening, page iteration, and structure detection. pdftext is pure-Go, MIT-licensed, purpose-built for ark.
- **R1730:** Each pdftext `Block` returned by `page.Blocks()` maps to one ark chunk. `BlockKind` determines the location suffix: `Paragraph` and `Irregular` → `para`, `Heading` → `heading`, `Table` → `table`, `List` → `list`, `Salvage` → `salvage`. `Image` blocks are skipped (no indexable text).
- **R1731:** `Block.Caption` (present on List and Table blocks) is prepended to `Block.Text` with a separating newline in the chunk's content so search matches the caption together with the body. Empty captions are a no-op.
- **R1732:** `Block.Text` and `Block.Caption` arrive NFKC-normalized (ligatures decomposed, fullwidth Latin normalized); ark indexes the normalized form directly and performs no additional normalization.
- **R1733:** Pages with no blocks (image-only, scanner output, etc.) emit no chunks. No whole-page fallback chunk is produced.
- **R1734:** Graceful degradation for malformed pages is delegated to pdftext via `BlockKind=Salvage` inline with structured blocks. Ark does not implement a separate byte-stream salvage codepath. If `pdftext.Open` itself returns a hard error, the chunker yields nothing and the file takes microfts2's standard log-once path.
- **R1735:** Tag rect extraction scans `Block.Text` (and `Block.Caption` when present) for the ark tag pattern `@name: value`. Each match's bounding box is the union of the `Block.Chars` (or `Block.CaptionChars`) BBoxes whose byte ranges overlap the match, giving per-glyph precision. When one source glyph expanded to multiple Unicode runes (ligature decomposition), every expansion byte carries the same originating-glyph BBox.
- **R1736:** A tag value that wraps across multiple lines within a block contributes all covered glyph BBoxes to the union, producing one rect that spans every wrapped line. (supersedes R1674's first-line-only rule — pdftext consolidates multi-line prose into a single `Block.Text`.)
- **R1737:** Salvage chunks are keyed at their source page number, with location `PAGE/salvage/N` (1-indexed per page). Salvage text is written to that page's blob alongside any structured blocks from the same page. (supersedes R1723's page-0 consolidation.)
- **R1738:** Location `N` per kind is 1-indexed per page. Each block kind (`para`, `heading`, `table`, `list`, `salvage`) counts independently: a page with two paragraphs and one table emits `PAGE/para/1`, `PAGE/para/2`, `PAGE/table/1`.

## Feature: PDF Chunk Element
**Source:** specs/pdf-chunk-element.md

### The Primitive

- **R1666:** `<pdf-chunk>` is a custom HTMLElement (no shadow DOM; inherits host theme CSS) that renders one PDF chunk's page region as pixels — no viewer UI, no page navigation.
- **R1667:** Attributes: `src` (URL returning raw PDF bytes), `page` (1-indexed page number), `rect` (chunk bounding box as `x,y,w,h` in PDF points, origin bottom-left).
- **R1668:** Children are `<ark-tag>` elements (standard element used in markdown and plain-text pages), each with an additional `rect="x,y,w,h"` attribute giving the tag's bounding box in the same coordinate system. Without JavaScript or before the canvas renders, these children appear as normal clickable tag widgets.

### Tag Rects From The Chunker

- @obsolete-req: R1669 -- superseded by R1735 (tag pattern scan now runs on Block.Text with per-glyph BBoxes from Block.Chars)
- **R1669:** The PDF chunker scans each chunk's positioned text spans for the tag pattern `@([a-zA-Z][\w.-]*):\s*([^\n]*)` — identical to ark's generic tag grammar — and records a bounding box for each match.
- **R1670:** Recorded tag rects are emitted as the chunk attribute `tag_rects` (see PDF Chunker R1665).
- **R1671:** `tag_rects` encoding is a semicolon-separated list: `name=value@x,y,w,h;name=value@x,y,w,h;…`.
- **R1672:** Tag `name` and `value` URL-encode `=`, `@`, `;`, `,` when those characters appear literally inside them.
- **R1673:** Coordinates are floats in PDF points, origin bottom-left — same convention as chunk-level `rect` (R1638).
- @obsolete-req: R1674 -- superseded by R1736 (pdftext consolidates wrapped lines into one Block.Text; the rect now unions all covered glyph BBoxes)
- **R1674:** When a tag's value wraps across multiple lines in the PDF layout, only the first line's rect is recorded; wrapped tails are not emitted.
- **R1675:** Salvage chunks (R1657) produce no `tag_rects` — no coordinates exist to record.
- **R1676:** Generic tag extraction — T/F/V/D index records — continues unchanged for all PDF chunks including salvage. `tag_rects` is a presentation enrichment, not a replacement for tag indexing.

### Source URL

- **R1677:** `src` resolves to `/raw/PATH` (raw file bytes), not `/content/PATH` (template-wrapped shell). PDF.js requires unadorned bytes.

### PDF Rendering

- **R1678:** Rendering uses PDF.js `getDocument`, `getPage`, and render APIs. The viewer UI is not used.
- **R1679:** The element is an overflow-hidden container; its single visible child during rendering is an `<img>` element sized to the full rendered page and absolutely positioned so the chunk's rect sits at local origin `(0, 0)`.
- **R1680:** Coordinate transform from PDF points to CSS pixels uses the standard origin flip: `y_css = (pageHeight_pdf - y_pdf - h_pdf) * scale`.

### Host-Owned Caches

- **R1681:** `<pdf-chunk>` does not own caches. It resolves documents and page images through its nearest ancestor host element. For v1 the host is `<ark-search>`.
- **R1682:** The host element carries three properties: `docCache: Map<src, Promise<PDFDocumentProxy>>`, `pageCache: Map<srcPageScaleBandKey, Promise<{url, w, h}>>`, and `blobUrls: string[]`. These are element properties, not closure-captured variables.
- **R1683:** The host exposes `getDocument(src)`: returns cached `PDFDocumentProxy` on hit, otherwise calls `pdfjs.getDocument(src)` and caches the promise.
- **R1684:** The host exposes `getPageImage(src, page, scaleBand)`: returns cached `{url, w, h}` on hit, otherwise renders the page to canvas at the band's scale, converts to a blob URL via `canvas.toBlob()` + `URL.createObjectURL()`, pushes the URL to `blobUrls`, and caches the result.
- **R1685:** `scaleBand` is the render scale bucketed to ±10%. Resize within a band is a CSS-only update (no new image). Crossing a band rebuilds the blob URL once; every sibling `<img>` src updates together.

### Blob URL Lifecycle

- **R1686:** `URL.createObjectURL()` allocates memory that the browser does not reclaim while any URL handle exists. Each created URL must be explicitly `URL.revokeObjectURL()`'d.
- **R1687:** Cleanup is host-scoped. The host's `disconnectedCallback` walks `blobUrls` revoking each URL, calls `doc.destroy()` on each cached document, and clears all three maps.
- **R1688:** No refcounting and no grace windows. Slice-and-insert does not churn because both slice halves read from the host's still-live `pageCache`.
- **R1689:** A page-level `beforeunload` handler walks every `<ark-search>` in the document and runs the host cleanup as a safety net for tab close and navigation.
- **R1690:** Cross-panel page-image deduplication is not implemented in v1. Same-tag drill-down is the anticipated motivator for a future ID-keyed registry on a higher shared owner.

### Error States

- **R1691:** `src` fetch failure: element shows fallback children (the `<ark-tag>` widgets) plus a small error indicator.
- **R1692:** `page` out of range for the document: fallback children only.
- **R1693:** `rect` missing or invalid: fallback children only.

### Tag Overlay Rendering

- **R1694:** For each `<ark-tag rect="…">` child, the element creates or reparents that child as an absolutely-positioned overlay over the rendered canvas at the transformed CSS coordinates.
- **R1695:** Overlay styling inherits the standard `<ark-tag>` rules (colors, `@` and `:` pseudo-elements, cursor). An opaque background (default page color, overridable via CSS variable) covers the PDF's rendering of the tag text beneath.
- **R1696:** Each overlay's `font-size` tracks the tag rect's CSS height: `font-size: calc(var(--pdf-tag-h) * 1px)`, with `line-height: 1`, zero vertical padding, `width: calc(var(--pdf-tag-w) * 1px)`, and `overflow: hidden` to clip rather than spill.
- **R1697:** A scoped rule `pdf-chunk > ark-tag { … }` in the page stylesheet carries these overrides so standalone `<ark-tag>` behavior is not affected.
- **R1698:** A clipped or obscured tag value remains fully recoverable — the click handler opens the `<ark-search>` panel with the complete name and value from the element's DOM, regardless of what fits on screen.

### Slice-And-Insert On Tag Click

- **R1699:** When an `<ark-tag>` overlay dispatches `ark-tag-click`, the enclosing `<pdf-chunk>` intercepts the event (bubble phase) and reshapes its own DOM position.
- **R1700:** The element replaces itself in the DOM with three siblings: a top `<pdf-chunk>` (same src/page/x/width, rect height trimmed to just above the clicked tag's top edge, tag-rect children restricted to those above the slice), an `<ark-search>` panel (tag and value pre-filled from the clicked tag), and a bottom `<pdf-chunk>` (same src/page/x/width, rect starting just below the sliced tag's line, tag-rect children restricted to those below the slice and remapped to the new local coord space).
- **R1701:** Closing the `<ark-search>` panel re-merges the three siblings back into a single `<pdf-chunk>` with the original rect and full tag-rect child list.
- **R1702:** Clicking a tag in a slice recurses — that slice splits again. Only one `<ark-search>` panel per container is open at a time; opening a new one closes the previous (matches existing `<ark-tag>` / `<ark-search>` convention).

### Server-Side Emission

- **R1703:** The server generates `<pdf-chunk>` elements in search result previews for chunks with strategy `pdf` that carry a `rect` attribute.
- **R1704:** Emission uses direct structured output from chunk metadata — not a `wrapTagElements`-style post-processing pass on rendered text.
- **R1705:** The preview renderer receives the chunk's file path (for `src="/raw/PATH"`, URL-encoded), `page`, `rect`, and `tag_rects`. The file path is already on `SearchResultEntry.Path`; `page`, `rect`, and `tag_rects` are chunk attributes that must flow through `SearchResultEntry` and `GroupedChunk` — a structural change since today neither carries chunk attrs.
- **R1706:** A PDF chunk with `tag_rects` emits one `<ark-tag rect="…"><name>…</name> <value>…</value></ark-tag>` child per recorded tag rect.
- **R1707:** A PDF chunk with no `tag_rects` emits a childless `<pdf-chunk>`.
- **R1708:** PDF chunks without a `rect` attribute (salvage chunks, R1657) fall through to the existing text-preview path (`<pre>`-escaped text with `wrapTagElements` applied); no `<pdf-chunk>` wrapper is emitted for these.

### Script Loading

- **R1709:** `<pdf-chunk>` ships as a single bundled JS file with PDF.js embedded. The bundle registers the custom element on load.
- **R1710:** PDF.js is bundled locally, not loaded from a CDN (consistent with ark's offline-first stance; matches markdown-editor and ark-search packaging).

### Package Structure

- **R1711:** New `pdf-chunk/` directory, sibling to `markdown-editor/` and `ark-search/`, containing `src/pdf-chunk-element.ts`, `src/index.ts`, `package.json` (pdfjs-dist as bundled dep), and `tsconfig.json`.
- **R1712:** Build output installed to `~/.ark/html/pdf-chunk-element.js` via the same pattern used for `ark-search-element.js` and `ark-markdown-editor.js`.
- **R1713:** (inferred) The Makefile asset pipeline handles the build-and-copy; build output is checked into the install/release pipeline the same way as the other bundles.

### Text-Layer Tag Rendering

- **R1741:** At page-render time `<pdf-chunk>`'s host calls PDF.js `getTextContent()` on the page and scans the returned text items for the tag pattern `@([a-zA-Z][\w.-]*):\s*([^\n]*)`. Each match produces a tag descriptor consisting of `name`, `value`, per-item rects, and a bounding-box union. Chunker-supplied `tag_rects` on `<ark-tag>` children become the fallback source when text-content is unavailable.
- **R1742:** The scanner builds a flat string of text-item `str` values with an offset table pointing back into the items array. Tag detection runs against this flat string; contributing items for each match are identified by overlapping the match byte range with each item's offset range.
- **R1743:** Before the scan, items are narrowed to those whose center point falls inside a search region defined per consuming chunk — the chunk `rect` expanded by a slack margin of approximately one estimated line height on top and bottom plus a small horizontal pad. Line height is estimated from the items' own `height` field or the chunk's `font_size` attribute when present.
- **R1744:** For each match, the per-item rects and the bounding-box union are computed from each item's transform, width, and height — all in the same coordinate system the canvas was rendered in.
- **R1745:** When `getTextContent()` fails (encrypted page, malformed stream, OCR-less scan), the post-processing bake is skipped; the canvas keeps the raw PDF rendering and `<ark-tag>` children fall back to their chunker-supplied `rect` with the pre-R1741 opaque-background overlay treatment. Salvage chunks (no rect) remain unaffected.
- **R1746:** After PDF.js rasterizes the page to the offscreen canvas and before `canvas.toBlob()` runs, a post-processing pass paints each detected tag in theme colors over the raster text (see R1751–R1754). The resulting blob URL is cached in `pageCache` as before (R1684); all chunks sharing the `(src, page, scaleBand)` key receive the same baked image.
- **R1747:** Each `<ark-tag>` child receives a `textRuns` element property — an array of `{x, y, w, h, start, end}` entries, one per contributing text item, in PDF points with `start`/`end` as byte offsets into the canonical `@name: value` string. Consumed by per-run colored painting (R1754) and available for future hover/focus work.
- **R1748:** The `getTextContent()` result is cached on the host element, keyed by `(src, page)`, alongside `docCache` and `pageCache`. Chunks sharing a page share the scan.
- **R1749:** The highlight-rect computation (`applyHighlights`) consumes the same flat-string-plus-offsets structure produced for tag detection — a single combined scan per `(src, page)` produces both the tag descriptor list (cached with the page) and the highlight rects (recomputed per-chunk for its `highlight` attribute).

### Canvas-Baked Tag Painting

- **R1750:** The host exposes a color-sample helper that mounts a hidden `<ark-tag>` probe element in the document, reads computed styles from its `<name>`, `<value>`, and `::before`/`::after` pseudo-elements, and caches `{name, value, punctuation, fontFamily, bg}` as theme descriptors. The probe is removed after sampling. `bg` is read from `document.documentElement`'s `--term-bg` custom property, falling back to white.
- **R1751:** After PDF.js renders the page to an offscreen canvas at the band's scale and before `canvas.toBlob()` runs, the host samples the page background color from a corner pixel of the rendered canvas (falls back to theme bg if sampling returns transparent) and runs a **recolor** pass using a five-step solid-background pipeline: (1) extract a glyph silhouette tile from text pixels, (2) blur it to expand the shape, (3) threshold all non-transparent pixels to fully opaque theme bg, (4) small edge blur so the boundary isn't a hard cutout, (5) draw theme-colored text on top. PDF.js's native font rendering, metrics, and antialiasing are preserved; only the ink color changes. The tag text sits on a fully opaque background surface with known, constant contrast.
- **R1752:** Text pixels are identified by luminance distance from the sampled page background: `textness = clamp(1 - pLum / bgLum, 0, 1)`. Pixels with `textness < 0.05` are treated as background and skipped. In the silhouette tile (step 1), alpha is `round(textness * 255)` — antialiased edge pixels contribute proportional alpha, shaping the expansion blur naturally. In the text tile (step 5), alpha is also `round(textness * 255)`, preserving the PDF's native glyph antialiasing in the final colored text. After threshold (step 3), the background surface is fully opaque regardless of the original per-pixel alpha.
- **R1753:** Target color classification per pixel: the pixel's canvas coordinates are tested against this tag's per-segment runBoxes (from R1758+). On a match, a charIdx is computed from the pixel's x-position inside the runBox; `charIdx === 0` → punctuation (the `@`), `1..nameLen` → name, `nameLen+1` → punctuation (the `:`), otherwise value. Target RGB comes from R1750's cached theme descriptor.
- **R1754:** Recolor runs in two phases to avoid self-contamination. Phase 1 computes geometry and snapshots pristine `ImageData` for every tag on the page (because Phase 2 writes may extend past one tag's own rect and contaminate another's unread region). Phase 2 composites each tag in turn.
- **R1755:** Scope — the transparent-hit-region behavior applies only to `<ark-tag>` elements that are direct children of `<pdf-chunk>` (CSS selector `pdf-chunk > ark-tag`). Standalone `<ark-tag>` elsewhere (markdown and plain-text pages, CM6 read views) is unaffected by R1746–R1770; it continues to render visible, styled, clickable per R1476–R1494. In pdf-chunk context, an `<ark-tag>` child renders as a transparent hit region: `pointer-events: none`, no visible content, no background, positioned at the text-content-derived union rect in CSS pixels. The `<pdf-chunk>` capture-phase click handler rect-tests click coordinates against tag rects to decide between slice-and-insert and letting the click fall through to the text-selection layer. (supersedes the visible-overlay styling portion of R1694–R1697 for the pdf-chunk case when segment-based placement succeeds; R1745's fallback path still renders visible overlays the old way)
- **R1756:** A PDF.js text layer (`renderTextLayer` or `TextLayer`) is mounted over each `<pdf-chunk>`'s clipped region, consuming the same cached `getTextContent()` result. Text spans are transparent and selectable; browser `::selection` styling handles highlight visuals.
- **R1757:** Theme-change invalidation of the baked `pageCache` is deferred — not v1.5. On theme switch, cached pages may show stale colors until naturally re-rendered.

### Tag Segments (Chunker → Element)

- **R1758:** The PDF chunker emits a `tag_segments` chunk attribute index-aligned with `tag_rects`. Per tag: four or more rects separated by `|`, each rect `x,y,w,h` in PDF points. The first three rects are the `@`, the tag name, and the `:` (always single-line). Rects 4+ are the value segment — one rect per physical line of value text, so wrapped values carry precise per-line bounds. Tags separated by `;`. Empty entry (between two `;`) means the tag's segment computation failed; tag_rects' entry at the same index is still valid.
- **R1759:** Each segment rect is computed as the union of `Block.Chars[].BBox` over the segment's byte range in `Block.Text`. `@` covers `[m[0], m[0]+1)`, name covers `[m[2], m[3])`, colon covers `[m[3], m[3]+1)`, value covers `[m[4], valueEnd)` where `valueEnd` is `m[5]` with trailing ASCII whitespace trimmed.
- **R1760:** Wrapped value rects are detected by grouping char BBoxes whose baseline Y differs from the running-average glyph height within the group by more than half an average height; each group becomes one rect. Salvage chunks (no rect, per R1675) produce no `tag_segments`.
- **R1761:** The server passes `tag_segments` through alongside `tag_rects` and emits a `segments="…"` attribute on each `<ark-tag>` child, index-aligned with the existing `rect` attribute. When a tag's `tag_segments` entry is empty (R1758), the `segments` attribute is omitted for that child.
- **R1762:** The `<pdf-chunk>` element parses each child's `segments` attribute into a TagDescriptor: name and value from the child's `<name>`/`<value>` DOM text, runs array with one entry per segment rect (start/end offsets set to the char-range in the canonical `@name: value` string that the segment corresponds to), and a union bbox across all runs. Descriptors are collected per-page across all `<pdf-chunk>` elements that share a (src, page) so a single recolor pass can bake every tag on a page.
- **R1763:** Chunker-supplied segments take precedence over PDF.js-derived detection (R1741). Detection becomes the fallback when no `<ark-tag segments>` children are present for the page (e.g., PDF served without chunker metadata, or chunker emitted tag_rects but not tag_segments for some reason).

### Recolor Geometry

- **R1764:** Per-tag region on the canvas = runBoxes-union padded by (a) ascender pad above, (b) descender pad below, (c) blur pad (~3× blur radius) on all sides so the expansion blur and edge blur have room to extend past the glyph edges.
- **R1765:** Ascender pad ≈ 30% of glyph height; descender pad ≈ 40%. Both are clamped by the vertical gap to the neighboring tag on that side (pad ≤ gap - 0.5pt buffer) so the run classification doesn't extend into a neighbor line's glyph area and misclassify its pixels.
- **R1766:** When compositing the combined (solid background + sharp text) onto the main canvas, the drawImage call is clipped to a generous rect around the runBox union (runBox union ± blur radius horizontally, runBox union vertically). The clip gives the edge blur room to soften the boundary while preventing adjacent tags' background surfaces from overlapping.
- **R1767:** Tags are composited bottom-up on the canvas (sorted by PDF y ascending → iterated in reverse of top-down order). Upper lines write last, so any runBox-overlap pixels at boundaries carry the upper line's classification.
- **R1768:** The background surface color is `theme.bg` — ark's UI background — not the sampled page background. The threshold step (R1752 step 3) makes this surface fully opaque, ensuring the tag text sits on a surface designed to contrast with the name and value colors. The PDF page color is completely hidden beneath the tag, so it reads as an ark element regardless of what page it sits on.
- **R1769:** The snapshot/composite two-phase approach (R1754) is the lock-in: Phase 1 reads main without any Phase 2 writes interfering. Within Phase 2, writes go to disjoint runBox regions (via R1766's clip), so the iteration order matters only at the runBox-boundary overlap pixels handled by R1767.

### Out Of Scope (v1)

- **R1714:** (negative) No PDF.js text layer for selection/copy — deferred to v2. (v1.5 uses `getTextContent()` for tag and highlight positioning per R1741–R1749 but does not surface a selectable text layer for prose.)
- **R1715:** (negative) No sub-hit token-level highlighting — deferred to v2.
- **R1716:** (negative) No server-side rendering of PDF pages (no `/pdf/FID/page/N.png`) — browser-only for v1.
- **R1717:** (negative) No form fields, annotations, or encrypted-PDF handling beyond what `getDocument` handles natively.
- **R1718:** (negative) No `<pdf-chunk>`-based pagination viewer — deferred; will later compose from the primitive.

## Feature: search-cli-filters
**Source:** specs/search-cli-filters.md

### Filter Syntax

- **R1770:** `ark search` accepts mode flags: `-contains TERM`, `-fuzzy TERM`, `-regex PATTERN`, `-tag TAG`, `-about QUERY`, `-files GLOB`. Each produces a filter entry with a mode and query.
- **R1771:** `-with` and `-without` are state toggles that set polarity for subsequent filter entries. Default polarity is `with`.
- **R1772:** `with` polarity means intersect (chunk must match). `without` polarity means subtract (chunk must not match).
- **R1773:** Bare terms (no leading `-`) are shorthand for `-contains`. Consecutive bare terms coalesce into a single `-contains` argument.
- **R1774:** A mode flag or polarity toggle closes the current bare-term group and starts a new filter entry.
- **R1775:** Bare terms following an explicit `-contains` coalesce into that contains group.
- **R2958:** `-files`/`-exclude-files` globs are resolved **cwd-relative in the CLI** before dispatch: a pattern not starting with `/`, `~`, or `tmp://` is joined with the current working directory (`filepath.Join(cwd, glob)`), making it absolute. `/`, `~/`, and `tmp://` patterns are left as-is. Resolution is CLI-side so both cold-start and server-proxied searches receive absolute globs (the server cannot know the client cwd). Consequence: bare `*.jsonl` matches cwd top-level only; `**/*.jsonl` for any depth.

### Primary Search and Filter Stack

- **R1776:** The first filter entry becomes the primary search — it maps to the existing request fields (`query`, `contains`, `about`, `regex`, `fuzzy`).
- **R1777:** All subsequent filter entries become `ChunkFilterRow` entries in the `chunk_filters` field.
- **R1778:** The primary search drives the initial trigram index lookup. Filter rows narrow the result set post-search.
- **R2951:** Post-filter application is independent of the primary mode. The primary mode (`-contains`/`-fuzzy`/`-regex`/`-tag`/`-file-tag`/`-about`) selects only the initial candidate set; every subsequent filter row AND the default `search_exclude` scope (R939/R940) apply identically to that candidate set. No primary mode skips the post-filter stack or the default exclude — including the index-lookup modes `-tag`/`-file-tag` (tag-index resolution via `SearchTagChunks`) and `-about` (vector-only resolution), which historically returned their hits directly.

### Tag Syntax

- **R1779:** `-tag` accepts `name:value` or `@name:value` (leading `@` is stripped).
- **R1780:** `-tag` with name only (no `:value`) matches files having that tag with any value.

### Parse Flag

- **R1781:** `-parse` prints the fully disambiguated command and exits without searching.
- **R1782:** `-parse` output shows each entry with its explicit mode flag and quoted value. Polarity toggles are shown at each boundary.

### Server Endpoint

- **R1783:** `searchRequest` gains a `ChunkFilters []ChunkFilterRow` field (`chunk_filters` in JSON).
- **R1784:** `handleSearch` wires `ChunkFilters` through `BuildChunkFilters` as chunk-level post-filters, same mechanism as `handleSearchGrouped`.
- **R1785:** The flat `[]SearchResultEntry` response format is unchanged.

### Removed Flags

- **R1786:** The old file-level filter flags (`--filter`, `--except`, `--filter-files`, `--exclude-files`, `--filter-file-tags`, `--exclude-file-tags`, `--except-regex`) are removed from `ark search`. The filter stack subsumes them. `SearchOpts` and the server JSON API retain these fields for Lua UI sidebar source filtering.
- **R1787:** `-about` is allowed as both a primary search mode and a filter mode. As a filter, it adds or subtracts chunks based on vector similarity.

### Help Text

- **R1788:** `ark search --help` groups options by purpose: output format, scoring/analysis, and profiling. Filter stack syntax and examples appear above the grouped options.
- **R1789:** Help text includes concrete examples showing bare terms, polarity toggles, mixed modes, and `-parse` usage.

## Feature: Embed Subcommands
**Source:** specs/embed-subcommands.md

### Subcommand Structure

- **R1790:** `ark embed` dispatches to subcommands: `text`, `bench`, `validate`. Running `ark embed` with no subcommand prints usage and exits 0.
- **R1791:** `ark embed text TEXT...` embeds text using the configured tag model, prints the vector as JSON. Supersedes R1302 (`ark embed TEXT`).
- **R1792:** `ark embed bench MODE` benchmarks embedding performance. MODE is `tags` or `chunks`. Supersedes R1303, R1304, R1621, R1622 (the `--bench` flag).
- **R1793:** `ark embed bench` accepts `--ctx N` (default 2048) and `--parallel N` (default 8). Supersedes R1587.
- **R1794:** `ark embed validate` cross-references embedding records (EC/EF) against FTS chunk data to find orphans, mismatches, and gaps.

### embed text

- **R1795:** Requires `[embedding] model` configured in ark.toml.
- **R1796:** Joins all positional args with spaces.
- **R1797:** Output is a JSON array of float32 to stdout.

### embed bench tags

- **R1798:** Collects all tag values from the index, embeds via batch and single paths, reports timing comparison and speedup ratio.

### embed bench chunks

- **R1799:** Samples 200 chunks from indexed files using file-first random sampling (prevents JSONL domination).
- **R1800:** Embeds via batch and single paths, reports timing comparison and speedup ratio.
- **R1801:** Reports context size, parallel count, tokens/seq, chunk size distribution (min/max/avg), and skip rate.

### embed validate — Checks

- **~~R1802:~~** (Retired T5 — no replacement) Orphan EC records: EC records whose fileID does not exist in the FTS index, or whose chunkIdx exceeds the file's actual chunk count.
- **~~R1803:~~** (Retired T6 — no replacement) EF/EC count mismatch: EF centroid's stored count does not match the actual number of EC records for that file.
- **~~R1804:~~** (Retired T7 — no replacement) Missing EC records: files with chunks in the FTS index but no EC records (or fewer EC records than chunks).
- **R1805:** Orphan EF records: EF records whose fileID has no corresponding EC records or no FTS entry.
- **R1806:** Dimension consistency: all EC vectors should have the same dimension. Reports the distribution of dimensions found (count per dimension). Flags any that differ from the majority.

### embed validate — Options

- **R1807:** `--fix` deletes orphan EC records, orphan EF records, and EC records with wrong dimensions.
- **R1808:** `--fix` does not re-embed missing chunks — that requires a running server with a warm model.
- **R1809:** `--verbose` / `-v` shows per-file detail instead of summary counts only.

### embed validate — Output

- **R1810:** Summary line per check category with count of problems found.
- **R1811:** Exit 0 if clean, exit 1 if any problems detected.
- **R1812:** With `--verbose`, lists each problem file/record.
- **R1813:** With `--fix`, reports what was deleted (count per category).

### Remove ark vec

- **~~R1814:~~** (Retired T172 — see R1791) Remove the `vec` case from the CLI command dispatcher.
- **~~R1815:~~** (Retired T173 — see R1791) Delete `cmd/ark/vecbench.go`.
- **~~R1816:~~** (Retired T174 — see R1791) R547-R562 (ark vec bench, ark vec bench-search) are superseded by R1791-R1801.

## Feature: Embed Deduplication
**Source:** specs/embed-dedup.md

### Superseded (high-water tracking, replaced by ec-rekey chunkID dedup)

- **R1817:** ~~superseded by R1847~~ The Librarian maintained an in-memory high-water map. Removed — chunkID EC lookup is the cross-pass dedup.
- **~~R1818:~~** (Retired T75 — see R1848) superseded by R1846-R1848 — high-water tracking state and skip logic
- **~~R1819:~~** (Retired T76 — see R1848) superseded by R1846-R1848 — high-water tracking state and skip logic
- **~~R1820:~~** (Retired T77 — see R1848) superseded by R1846-R1848 — high-water tracking state and skip logic
- **~~R1821:~~** (Retired T78 — see R1848) superseded by R1846-R1848 — high-water tracking state and skip logic
- **~~R1822:~~** (Retired T79 — see R1848) superseded by R1846-R1848 — high-water tracking state and skip logic
- **~~R1823:~~** (Retired T80 — see R1848) superseded by R1846-R1848 — high-water tracking state and skip logic
- **~~R1824:~~** (Retired T81 — see R1848) superseded by R1846-R1848 — high-water tracking state and skip logic
- **~~R1825:~~** (Retired T82 — see R1848) superseded by R1846-R1848 — high-water tracking state and skip logic
- **~~R1826:~~** (Retired T83 — see R1848) superseded by R1846-R1848 — high-water tracking state and skip logic
- **~~R1827:~~** (Retired T84 — see R1848) superseded by R1846-R1848 — high-water tracking state and skip logic
- **~~R1828:~~** (Retired T85 — see R1848) superseded by R1848 — incremental centroid seeding/subtraction
- **~~R1829:~~** (Retired T86 — see R1848) superseded by R1848 — incremental centroid seeding/subtraction

### Centroid Invariants (still load-bearing)

- **R1830:** (invariant) EF centroids are written only after every tier bucket for that file has been flushed. Tier processing is sequential — a fast-bucket chunk must not trigger an early EF write while slow-bucket chunks for the same file are pending.
- **R1831:** The write queue serializes embed passes across reconcile cycles. No two `BatchEmbedChunks` calls run concurrently.
- **R1832:** `BatchEmbedChunks` needs chunk ChunkIDs from the FTS F-record. Implemented via `FileInfoByID`.

### In-Batch Dedup

- **R1862:** `BatchEmbedChunks` Pass 1 maintains a local `seen` set of chunkIDs already queued for embedding in this invocation.
- **R1863:** When a chunkID is encountered that is already in the seen set, skip it without adding it to a tier bucket. This prevents the same content from being embedded multiple times within a single pass when multiple files reference the same deduplicated chunk.
- **R1864:** Log the embed stats: embedded, skipped (too large), deduped (same chunkID from another file reference), and tag-only (stripped to empty on the meaning axis — R3004).
- **R3004:** A chunk that cannot produce a meaning vector gets a nil sentinel EC record (`WriteChunkEmbedding(chunkID, nil)`) so the Pass 1 queue check (R1846) finds `EC[chunkID]` present and does not re-queue it on the next reconcile. Two cases: a chunk too large for every tier bucket (placed in no bucket, Pass 1) and a chunk that strips to empty on the meaning axis (all `@tag:` lines, R2913, Pass 2). The sentinel reads back as a non-nil empty slice, so `ReadChunkEmbedding != nil` skips it; it contributes no vector to the EF centroid. Without it, such chunks re-queue every reconcile (the perpetual "N new" embed-log noise).

## Feature: EC Rekey (chunkID-based Embedding)
**Source:** specs/migrations/complete/002-ec-rekey.md

### Key Format

- **R1833:** EC records are keyed by `EC` + varint(chunkID). One record per unique chunk content.
- **R1834:** The old key format `EC` + varint(fileID) + varint(chunkIdx) is superseded. R1598 (old format) is replaced by R1833.
- **R1835:** EF records remain keyed by fileID. EF centroid is computed from EC records resolved through the file's F-record chunk list.

### Store API

- **R1836:** `WriteChunkEmbedding(chunkID, vec)` writes one EC record keyed by chunkID.
- **R1837:** `WriteChunkEmbeddingBatch(chunks []ChunkVec)` where ChunkVec is `{ChunkID uint64, Vec []float32}`.
- **R1838:** `ReadChunkEmbedding(chunkID)` reads one EC record by chunkID.
- **R1839:** `DeleteChunkEmbedding(chunkID)` deletes one EC record by chunkID.
- **R1840:** `DeleteChunkEmbeddingInTxn(txn *bbolt.Tx, chunkID)` deletes one EC record using an existing transaction. Used inside microfts2 callbacks.
- **R1841:** `DeleteFileCentroidInTxn(txn *bbolt.Tx, fileID)` deletes one EF record using an existing transaction.
- **R1842:** `ReadChunkEmbeddings(chunkIDs []uint64) [][]float32` batch reads EC records for centroid computation.
- **~~R1843:~~** (Retired T175 — see R2115) `RemoveFileChunkEmbeddings(fileID)` is removed. Replaced by per-chunkID deletion in callbacks.
- **R1844:** `DropChunkEmbeddings()` unchanged — drops all EC and EF records.
- **R1845:** `ScanChunkEmbeddingKeys()` returns map[chunkID]*ChunkEmbedInfo (dimension only, no fileID grouping).

### Embedding Pipeline

- **R1846:** `BatchEmbedChunks` reads each file's F-record chunk list, checks EC[chunkID] for each, queues missing chunkIDs for embedding.
- **R1847:** The lastEmbedded high-water map (R1817) is removed. ChunkID existence check is the dedup — if any file already caused a chunk to be embedded, EC[chunkID] exists.
- **R1848:** After embedding a file's chunks, recompute its EF centroid from the file's chunk list: read EC[chunkID] for each entry, average the vectors, write EF.

### Chunk Cleanup

- **R1849:** The indexer's `executeFullRefresh` uses `ReindexWithCallback` instead of `ReindexWithContent`. The callback deletes EC records for orphaned chunkIDs via `DeleteChunkEmbeddingInTxn`.
- **R1850:** The indexer's `RemoveFile` uses `RemoveFileWithCallback`. The callback deletes EC records for orphaned chunkIDs via `DeleteChunkEmbeddingInTxn`.
- **R1851:** The indexer's `RemoveByID` uses `RemoveFileWithCallback`. Same callback as R1850.
- **R1852:** Orphaned chunk cleanup and C record deletion happen in the same transaction (atomic).
- **R1853:** The EF centroid for a removed file is deleted in the same callback.
- **R1854:** New chunkIDs from `ReindexCallback` are embedded in the next `BatchEmbedChunks` pass, not in the callback. GPU compute does not belong inside a transaction.

### Validate

- **R1855:** `ark embed validate` orphan EC check: EC records whose chunkID has no C record in microfts2.
- **R1856:** `ark embed validate` missing EC check: chunkIDs with C records but no EC record. Count unique chunkIDs, not per-file references.
- **R1865:** The missing EC check partitions chunkIDs by `search_exclude`: chunks referenced only by excluded files are reported as "excluded" (expected), not "missing" (actionable). A chunkID shared by both excluded and non-excluded files counts as embeddable.
- **R1866:** Report the excluded chunk count as a separate summary line so the total is transparent: missing + excluded + embedded = total unique chunks.
- **R1857:** `ark embed validate` EF consistency: EF centroid count matches the number of EC records resolvable from the file's chunk list.
- **R1858:** `--fix` deletes orphan EC records (chunkID without C record).

### Migration

- **R1859:** Store an `ec_version` I record. Value "2" means chunkID-keyed EC records.
- **R1860:** On startup, if `ec_version` is absent or "1", drop all EC and EF records and set `ec_version` to "2". The next `BatchEmbedChunks` re-embeds with the new key format.
- **R1861:** R1598 (old EC key format) and R1607 (RemoveFileChunkEmbeddings on re-index) are superseded by R1833 and R1849.

## Feature: microfts2 ABI Catch-Up
**Source:** specs/migrations/microfts2-abi-catchup.md

- **R1867:** ark consumers of `microfts2.CRecord.FileIDs` access the `FileID` field on each element rather than treating the slice as `[]uint64`. Reflects microfts2's refcounted C-record fileids list shape `[]FileIDCount` (struct with `FileID` and `Count`).
- **R1868:** F-record chunk entries' `Location` field continues to be the source of line-range strings used by ark; the additive `Locator []byte` field is not consumed by ark in this catch-up.
- **R1869:** ark's chunkers do not implement the optional `microfts2.AppendAwareChunker` interface in this catch-up. `AppendChunks` calls continue with non-AppendAware chunkers, falling through to full refresh on dirty append boundaries (consistent with R386).
- **R1870:** `FileIDCount.Count` (per-file occurrence count of a chunk) is not consumed in this catch-up. ark continues to treat C-record fileids as a set; refcount-aware deduping is owned by the chunkid-tag-store migration.
- **R1871:** This migration requires a full reindex on first run after deployment because microfts2's on-disk record formats are incompatible with the previous version. `ark rebuild` is the supported path; old indices won't parse against the new code.
- **R1872:** This migration must complete before the chunkid-tag-store migration (which uses the new `Locator` and AppendAwareChunker infrastructure) and before the parked status-db code changes can be verified by build.

## Feature: Chunkid Tag Store
**Source:** specs/migrations/chunkid-tag-store.md

### Record format

- **R1873:** V record key shape preserved: `V[tag]\x00[value]\x00[tvid varint]`. Only the value semantic changes from packed fileid varints to packed chunkid varints. EV records continue to join via tvid; no rekey of EV.
- **R1874:** F record key changes from `F[fileid:8][tagname]` to `F[chunkid varint][tagname]`. Multi-record per chunkid, one record per (chunkid, tagname) pair.
- **R1875:** F record value layout preserved: `[count: uint32 big-endian][optional packed tvid varints]`. Only the key prefix changes; value encoding is unchanged from the pre-migration shape.
- **R1876:** T record key and value layout preserved (`T[tagname]` → `[count: uint32 big-endian][optional float32 vector (3072 bytes)]`). The count *semantic* shifts from "number of files containing the tag" to "number of (chunk, tag) pairs in F records." A file with 3 chunks all carrying the same tag contributes 3; a chunk shared by 2 files contributes 1.
- **R1877:** D records unchanged: key remains `D[tagname][fileid:8]`, value remains description bytes. Definitions are a file-level property.
- **R1878:** EV, EC, EF, M, U, I, E:, PC records unchanged by this migration.

### Schema marker

- **R1879:** Add an I record `tag_store_version`. Value `"1"` marks the post-migration state.
- **R1880:** On `DB.Open`, after the existing `ec_version` check, read `tag_store_version`. If empty or != `"1"`, refuse to start with the error: "tag store schema upgrade required — run `ark rebuild`". Do not auto-drop V/F/T records.
- **R1881:** `cmdRebuild` already removes the index (via `cmdInit --no-setup`); no V/F/T-specific drop code is needed. After re-creating the DB, write `tag_store_version = "1"` unconditionally on first Open of a new DB.
- **R1882:** New DBs from `ark init` are tagged `tag_store_version = "1"` at creation time.

### Store API

- **R1883:** `Store.UpdateTagValues(chunkTags []ChunkTagValues) error` replaces the fileid-keyed UpdateTagValues + UpdateTags pair. ChunkTagValues = `{ChunkID uint64; Values []TagValue}`. T-record increments are computed per-chunk during the merged write.
- **R1884:** `Store.AppendTagValues(chunkTags []ChunkTagValues) error` mirrors UpdateTagValues for the append path.
- **R1885:** `Store.UpdateTags(fileid, tags)` and `Store.AppendTags(fileid, tags)` are removed. Their content is now expressed via ChunkTagValues.
- **R1886:** `Store.UpdateTagDefs(fileid, defs)` and `Store.AppendTagDefs(fileid, defs)` keep their fileid signature; D records remain `D[tagname][fileid:8]`.

### Reverse lookups

- **R1887:** `TagValueChunks(tag, value) []uint64` returns chunkids (post-migration). File-level callers resolve via microfts2 `FilesForChunk` and dedupe.
- **R1888:** `TagFiles(tags) []TagFileInfo` returns chunk-attributed entries (`{ChunkID, FileID, …}`). File-level callers dedupe by FileID.
- **R1889:** `FileTagValues(fileid, tags) (map[string]string, error)` is implemented chunkid-internally with a fileid-input wrapper; wired into Inbox per R1142, R1147, R1149. Inbox no longer reads files from disk for tag lookups.

### Indexer pipeline

- **R1890:** `chunkAccumulator.tagValues` becomes `[][]TagValue` indexed by chunk position; the callback appends one slice per chunk. `chunks` and `tagValues` stay parallel — same length, same order.
- **R1891:** Chunkid-keyed `[]ChunkTagValues` is built directly inside `microfts2.WithIndexedChunkCallback` — each callback fire delivers `IndexedChunk{Chunk, CRecord}`, so `acc.indexedCallback` extracts tags from `ic.Chunk.Content` and emits `ChunkTagValues{ChunkID: ic.CRecord.ChunkID, Values: values}`. After indexing, `Store.UpdateTagValues(acc.chunkTags)` writes them. No `FileInfoByID` lookup, no chunk-list zip — chunkid arrives in-line.
- **R1892:** The `tags` accumulator field (file-level tag→count map) is removed. Per-chunk content lives in `tagValues`.
- **R1893:** `flattenChunkTags(chunkTags [][]TagValue) []TagValue` collapses per-chunk slices to file-level for `writeDateIndex` and `pubsub.PublishAndWatch` call sites. R795/R796 (pubsub) and R866/R869/R870/R872 (schedule) remain file-level.

### Append path

- **R1894:** Both append entry points (`Indexer.AppendFile` and `executeRefresh` isAppend branch) drive their tag pipeline through two microfts2 callbacks: `WithAppendChunkCallback` (text-only, every emitted chunk — feeds `acc.tagValues` for file-level pubsub/schedule) and `WithIndexedChunkCallback` (chunkid-aware, newly-inserted chunks only — feeds `acc.chunkTags` for chunkid-keyed F/V/T writes). Both replace the prior `tagWindowForAppend` pre-extraction.
- **R1895:** `tagWindowForAppend` is removed (definition + both call sites). Boundary handling is microfts2's responsibility via the chunker's append protocol; tags split across the seam are re-emitted by the callback as part of the merged chunk.
- **R1896:** `refreshPrep` no longer carries `tagValues`, `tags`, or `defs`. Tag extraction moves on-actor during execute. Pre-extraction was a marginal optimization the migration makes incompatible.
- **~~R1897:~~** (Retired T26 — no replacement) Vector path unchanged: `splitChunks(prep.data, ...)` and `vec.AddFile(fileid, allChunks)` stay file-level. Vectors are file-scoped.
- **R1898:** If `AppendChunks` returns an error, `executeRefresh` falls through to `executeFullRefresh`, which uses its own fresh accumulator. The append accumulator is discarded.
- **R2864:** `prepareRefresh` detects an *unchanged* file: when the change is not an append and `len(data) == FileInfo.FileLength` and `sha256(data) == FileInfo.ContentHash` (byte-identical to the last index), it sets `prep.unchanged`. `executeRefresh` returns immediately for an unchanged prep, skipping the full re-chunk — which would re-index identical content (a no-op) at the cost of re-chunking the whole file. This catches redundant re-process events (throttle batches, spurious fsnotify with no growth) on large append-only sources like chat JSONLs. The hash is computed from the already-read `data`, so the skip costs one hash rather than a re-chunk; an in-place edit (same length, different bytes) fails the hash check and still triggers a full refresh.

### Cleanup mechanism (orphan chunkids)

- **R1899:** File removal does not directly modify V records. Instead, `RemoveFileWithCallback` and `ReindexWithCallback` deliver any chunkids whose microfts2 C-record refcount reached zero.
- **R1900:** For each orphaned chunkid: scan `F[chunkid]` prefix to enumerate (tagname, count, tvids) entries; for each tvid, decrement the corresponding V record by removing the chunkid; if a V record becomes empty, delete it; decrement T totals by `count` for each tagname; drop F records for the orphaned chunkid.
- **R1901:** Chunks shared across files retain their F/V/T entries until the last file referencing them is removed. Refcount management is microfts2's responsibility via the FileIDCount C-record shape.

### Sequencing

- **R1902:** This migration depends on the completed `microfts2-abi-catchup` migration for refcount-aware `FileIDCount` and the `AppendAwareChunker` infrastructure.

## Feature: microfts2 Callback Adoption (post-chunkid-tag-store)
**Source:** specs/migrations/complete/003-chunkid-tag-store.md

These extend the chunkid-tag-store work with the chunkid-aware indexed
callback API delivered by microfts2 on 2026-04-27 (see
`requests/microfts2-indexed-chunk-callback.md` and the response file
in microfts2). Anchored in-place rather than as a new migration —
the changes are corrections/extensions to the chunkid-tag-store
implementation, not a separate format break.

- **R1903:** Markdown is registered via `microfts2.AddChunker("markdown", microfts2.MarkdownChunker{})` (struct form) rather than via `AddStrategyFunc` + `MarkdownChunkFunc` (function form). The struct form preserves microfts2's `AppendAwareChunker` interface, enabling clean paragraph-extension merges on append.
- **R1904:** `chunkAccumulator` carries an `indexedCallback(microfts2.IndexedChunk)` method and a `chunkTags []ChunkTagValues` field. The method extracts tags from `ic.Chunk.Content` and appends `ChunkTagValues{ic.CRecord.ChunkID, values}`. Fires only for newly-inserted chunkids per microfts2's `WithIndexedChunkCallback` contract.
- **R1905:** Content-dedup'd chunks (microfts2 refcount-bumped C records) do not fire `WithIndexedChunkCallback` and therefore contribute zero F/V/T writes from ark — the records already capture the tags from the first file that brought the content in. This is a deliberate efficiency property of the chunkid-aware path.
- **~~R1906:~~** (Retired T32 — see R1926) All four indexer paths pass both `WithChunkCallback` (or `WithAppendChunkCallback` on append) and `WithIndexedChunkCallback`. The text-only callback feeds `acc.chunks`, `acc.tagValues`, and `acc.defs` for vector indexing, file-level pubsub/schedule, and D-record writes; the indexed callback feeds `acc.chunkTags` for F/V/T writes. Tag extraction runs twice on newly-inserted chunks (once per callback) — sub-millisecond, accepted as not worth optimizing.
- **R1907:** R386's interim ("fall through to full refresh on dirty markdown append boundaries") is partially superseded for markdown — `MarkdownChunker` now implements `AppendAwareChunker` so paragraph-extension appends merge cleanly. R386 still applies to chunkers that haven't implemented `AppendAwareChunker` yet (microfts2's deferred gap O16 covers the remaining built-in chunkers).
- **R1908:** Orphan-chunkid cleanup continues to use microfts2's `RemoveCallback` (delivers `[]uint64` post-deletion). Migration to the richer pre-deletion `WithRemovedChunkCallback` (delivers full `CRecord`) is deferred — the current path correctly drops F/V/T per chunkid via `Store.RemoveTagValuesInTxn`, and the richer callback is only useful when ark needs to read tvids from the CRecord directly instead of scanning F[chunkid] itself.

## Feature: microvec → EC search migration
**Source:** specs/migrations/microvec-to-ec-search.md

### Dependencies and lifetime

- **R1909:** Ark uses microfts2 (trigram) and an internal embedding pipeline (Librarian + EC chunk-embedding records) as its search engines. (replaces R2)
- **R1910:** Ark opens microfts2 first (opens the `*bbolt.DB`). The Store and Librarian share that DB. (replaces R5)
- **~~R1911:~~** (Retired T158 — see R2978) MaxDBs is set to the count microfts2 + the ark subdatabase require — no microvec subDB is allocated. (replaces R7)
- **R1912:** `ark init` creates a new database: initializes microfts2, the ark bucket, and writes default config. No microvec initialization step. (replaces R30)

### Vector search via Librarian + EC

- **R1913:** Chunk embeddings are written to EC records by `Librarian.BatchEmbedChunks`; chunkid is the key. The text-only chunk callback no longer carries chunk text for embedding. (replaces R39, R1116)
- **R1914:** chunkid (not fileid) is the source of truth for embedding identity. `Librarian.BatchEmbedChunks` reads chunks by chunkid via the indexed callback path; orphan cleanup uses microfts2 callbacks (R1899–R1901). (replaces R40)
- **R1915:** `Librarian.SearchChunks(queryVec []float32, k int) ([]ChunkScore, error)` returns the top-k EC records by cosine similarity, found via a single `db.View` with a cursor walk over the EC prefix and a min-heap of size k. (replaces R48)
- **R1916:** `--about <text>` routes through `Librarian.EmbedQuery` for the query vector and `Librarian.SearchChunks` for ranking. When `[embedding] model` is unconfigured, `--about` returns an actionable error (existing `Librarian.EmbeddingAvailable()` gate). (replaces R52)
- **R1917:** `ChunkScore` is `{ChunkID uint64, FileID uint64, Score float64}`. `FileID` is recovered from `CRecord.FileIDs[0]` inside the search txn. Merge/intersect by (FileID, ChunkID) tuple — the same key shape microvec used.
- **R1918:** `Searcher.merge`, `Searcher.intersect`, and `Searcher.vecOnly` retype from `[]microvec.SearchResult` to `[]ChunkScore`. The merge/intersect math is unchanged.

### Centroid filtering — config-gated

- **R1919:** `AboutCentroidFilter` (config bool, `toml:"about_centroid_filter,omitempty"`, default `false`) controls whether centroid-based pre-filtering runs for `about` queries. Default `false` so small/medium corpora bypass the centroid blind spot.
- **R1920:** `AboutCentroidThreshold` (config float64, `toml:"about_centroid_threshold,omitempty"`, default `0.3`) is the cosine similarity gate the centroid filter applies. Consulted only when `AboutCentroidFilter` is true.
- **R1921:** `ResolveAboutFilters` consults `AboutCentroidFilter`. When false, returns no early `WithOnly` / late `WithExcept` options for "about" rows. When true, gates file centroids at `cosineSimilarity > AboutCentroidThreshold` (replacing the previously hard-coded 0.3).
- **R1922:** When `AboutCentroidFilter` is false, `Librarian.SearchChunks` walks every EC record (no centroid pre-filter). When true, the walk narrows to chunks whose owning file passed the centroid gate at `AboutCentroidThreshold`.

### microvec removal

- **R1923:** `Searcher.vec`, `DB.vec`, `DB.Vec()`, and `Indexer.vec` are removed. `microvec.Open` / `microvec.Create` calls in `db.go` are deleted, as is the `microvec` import from every Go file that loses its last reference.
- **R1924:** `go.mod` no longer depends on `github.com/zot/microvec`; `go mod tidy` drops it.
- **R1925:** Pre-existing microvec records inside the index are orphaned blobs reclaimed on the next `ark init` / rebuild. No schema marker bump is required (the record formats ark *writes* don't change).

### Indexer callback wording

- **R1926:** The text-only chunk callback (`WithChunkCallback` / `WithAppendChunkCallback`) feeds `acc.tagValues` and `acc.defs` for file-level pubsub/schedule and D-record writes. It does not carry chunk text for embedding — embeddings go through the chunkid-aware indexed callback path and `Librarian.BatchEmbedChunks`. (replaces R1906; the "vector indexing" clause referred to microvec.)

## Feature: about multi-search and chunk-level about filter
**Source:** specs/migrations/about-multi-search.md

### Multi-query EC walk

- **~~R1927:~~** (Retired T33 — no replacement) `AboutKind` enum has values `AboutTopK` (produce top-k by score) and `AboutSet` (produce a chunkID set whose score ≥ threshold). One value per request in a multi-query walk.
- **R1928:** `AboutRequest` is `{QueryVec []float32, K int}`. There is one request shape — top-K — because empirically a cosine-threshold reducer is no-op against the nomic embedding distribution (basically every chunk passes 0.3; almost none passes a usefully strict gate).
- **R1929:** `AboutResult` is `{TopK []ChunkScore}`. The result slice is index-parallel to the request slice.
- **R1930:** `Librarian.SearchChunksMulti(reqs []AboutRequest) ([]AboutResult, error)` walks EC records once. For each chunk it computes cosine similarity against every `req.QueryVec` and pushes onto that request's min-heap of size `K`. After the walk, every surviving chunk's `FileID` is resolved via `fts.ReadCRecord` inside one shared txn.
- **R1931:** `Librarian.SearchChunks(qvec, k)` is reduced to a thin wrapper that calls `SearchChunksMulti` with one `AboutRequest` and returns the resulting `TopK`.

### Chunk-level "about" filter

- **R1932:** Each `Mode == "about"` filter row goes through `SearchChunksMulti` as a top-K request with `K = row.K` if non-zero, else `cfg.AboutFilterTopK`. The resulting `TopK` is converted to a chunkID set, and a `microfts2.WithChunkFilter` closure checks `crec.ChunkID` against that set. Polarity (`with`/`without`) negates the closure. Top-K (not threshold) because the nomic embedding distribution makes a cosine threshold unworkable for chunk-level filtering.
- **~~R1933:~~** (Retired T34 — no replacement) A single threshold knob is reused for both centroid pre-filter and chunk-level about filter: `cfg.AboutCentroidThreshold` (default 0.3). Per-row override is future work.
- **R1934:** When the embedding pipeline is unavailable (`Librarian.EmbeddingAvailable()` returns false), about-mode chunk filter rows are dropped with a logged warning rather than failing the surrounding search.

### Searcher integration

- **R1935:** Combined about coordination — `ResolveAboutFilters` collects the primary `--about` query (if any) and every `Mode == "about"` chunk filter row into one `SearchChunksMulti` call. Primary `--about` results route to merge/intersect/vecOnly via `opts.aboutResults`. Each filter row's `TopK` becomes a chunkID set: published as a `microfts2.WithChunkFilter` closure (consumed by FTS) **and** exposed as an `AboutFilterSet` so vec-only paths can apply the same membership filter.

### CLI cold path

- **R1936:** The CLI cold path (`cmd/ark/main.go` search dispatcher) errors out with an actionable "start `ark serve`" message when `opts.About != ""` or any chunk filter row has `Mode == "about"`. Embedding-model warm-up per CLI invocation is too costly; the server is the only supported host for about queries.

### UI

- **R1937:** The `ark-search` web component (`ark-search/src/ark-search-element.ts`) adds `"about"` to the `FilterMode` union and to the `FILTER_MODES` array. The existing free-text input branch handles about's input — no per-row threshold control.

### Filter top-K knob

- **R1938:** `AboutFilterTopK` (config int, `toml:"about_filter_top_k,omitempty"`, default 200) is the default chunk count retained per about-mode filter row. A small default narrows aggressively; users tune up if they want more recall.
- **R1939:** `ChunkFilterRow` gains an optional `K int` field (`json:"k,omitempty"`). When non-zero it overrides `cfg.AboutFilterTopK` for that row. Lets a single search mix tight and loose about filters.
- **R1940:** CLI filter stack: `--filter-k N` (or `-filter-k N`) after an `-about` filter entry sets `ChunkFilterRow.K` for that row. Only meaningful for about-mode filters. If placed after a non-about entry, `parseFilterStack` logs a warning that `--filter-k` is ignored on this mode. If placed after `-with`/`-without` (no prior filter entry), logs a warning that `--filter-k` has no entry to apply to.

## Feature: tmp tag overlay
**Source:** specs/tmp-tag-overlay.md

- **R1941:** `DB` owns a `TmpTagStore` collaborator that mirrors the persistent V/F/T runtime API for tmp:// content. The overlay lives in process memory; no index writes, no schema marker, no `ark rebuild` interaction.
- **R1942:** `TmpTagStore.UpdateTagValues(chunkTags []ChunkTagValues)` replaces a tmp:// fileid's per-chunk tag entries. Same `ChunkTagValues` shape as the persistent store.
- **R1943:** `TmpTagStore.AppendTagValues(chunkTags []ChunkTagValues)` adds per-chunk tag entries for newly emitted chunks during `AppendTmpFile`, leaving existing chunk-tag entries untouched.
- **R1944:** `TmpTagStore.RemoveFile(fileid)` drops all V/F entries for the fileid and decrements T counters. Called from `DB.RemoveTmpFile` before microfts2's overlay removal so the tag overlay is consistent with the trigram overlay.
- **R1945:** `TmpTagStore.TagFiles(tags) []TagFileInfo`, `TmpTagStore.TagValueChunks(tag, value) []uint64`, and `TmpTagStore.FileTagValues(fileid, tags) []TagValue` provide the overlay's read contributions, matching the persistent store's signatures.
- **R1946:** `Store`'s read methods (`TagFiles`, `TagValueChunks`, `FileTagValues`) union the persistent index results with `TmpTagStore` results before returning. Callers do not branch on tmp://.
- **R1947:** `Store.UpdateTagValues`, `Store.AppendTagValues`, and `Store.RemoveTagValues` (chunkid-keyed) dispatch by the high bit of the chunkid. `Store.RemoveFileTagValues(fileid)` (file-level cleanup, called from `DB.RemoveTmpFile`) dispatches by the high bit of the fileid. Overlay-issued ids (chunkids and fileids alike) count down from `MaxUint64`, so the high bit set when interpreted as int64 marks them as overlay-routed; everything else goes to the index.
- **R1948:** `DB.AddTmpFile`, `DB.UpdateTmpFile`, and `DB.AppendTmpFile` instantiate a `chunkAccumulator` and pass `microfts2.WithIndexedChunkCallback(acc.callback)` to the overlay call. The callback fires once per genuinely-new chunk (hash-dedup miss), in chunk order. After the call returns, the accumulator's chunk-tag pairs are written to `Store.UpdateTagValues` (add/update) or `Store.AppendTagValues` (append).
- **R1949:** Overlay-fired `IndexedChunk.CRecord` has no transaction context — `CRecord.Tx()` and `CRecord.DB()` return nil. The chunkAccumulator reads only `ChunkID`, `Hash`, `ContentLen`, `Attrs`, `FileIDs`, and `Trigrams`, never traversing the CRecord into the index.
- **R1950:** Overlay chunkids count down from `MaxUint64`. The high bit (set when read as int64) is the per-record origin discriminator alongside the fileid high bit.
- **R1951:** Tvid resolution shares a single map with the persistent tvid resolver (subpoint 3). Each entry is annotated with its origin (persistent vs overlay) so `RemoveFile` cleans up only tvids introduced solely by tmp:// content.
- **R1952:** Inbox queries (`ark message inbox`) resolve their tag lookups via `Store.FileTagValues`, exercising the unified read path. `FileTagValues` is no longer orphaned: R1142, R1147, and R1149 are wired through inbox as part of this feature.

## Feature: tvid map and transaction overlay
**Source:** specs/tvid-map-overlay.md

### Live map

- **R1953:** `Store` owns a `TvidMap` collaborator: an in-memory map `tvid → (tag, value, origin)` covering every tvid the index has seen, persistent and `tmp://` alike. The map lives in process memory; V records are the source of truth.
- **R1954:** `TvidMap.Resolve(tvid uint64) (tag, value string, ok bool)` returns the `(tag, value)` for a tvid in O(1) under read lock, replacing V-prefix scans for tvid resolution.
- **R1955:** `TvidMap.Lookup(tag, value string) (tvid uint64, ok bool)` provides the reverse lookup so callers with a `(tag, value)` pair can find an existing tvid without an index scan.
- **R1956:** `TvidMap.Snapshot() map[uint64]TagAlt` returns a copy for diagnostics; `Store.ScanVRecordTvids` becomes a thin wrapper over `Snapshot`.
- **R1957:** Each entry carries a `TvidOrigin` of `OriginPersistent` or `OriginOverlay`, fixed at the tvid's first registration. A persistent tvid that later acquires an overlay producer keeps `OriginPersistent`; origin marks where the tvid was born, not who currently uses it.

### Startup load

- **R1958:** `Store.LoadTvidMap()` runs once during `DB.Open` (after the Store is wired but before the server accepts traffic). It scans V records and registers every tvid with `OriginPersistent`. This is the only V-prefix scan needed for tvid resolution per process lifetime.

### Transaction overlay

- **R1959:** `TvidTxn` is an overlay struct scoped to one write transaction. `Store.tvids.Begin()` returns a fresh `TvidTxn`; the write actor's `db.Update` block calls `Begin`, then `Commit` on success or `Abort` on error/panic.
- **R1960:** `TvidTxn.Add(tvid, tag, value, origin)` records a tvid registration in the overlay. `TvidTxn.Remove(tvid)` records a removal. Neither touches the live map directly.
- **R1961:** `TvidTxn.Resolve(tvid)` consults the overlay first (added entries visible, removed entries hidden) and falls through to the live map. Used by code running inside the txn that needs to resolve tvids it has just added or is about to remove.
- **R1962:** `TvidTxn.Commit` merges added/removed entries into the live map under write lock. `TvidTxn.Abort` discards the overlay. Reads outside the txn never observe overlay state. The write-actor invariant (one `db.Update` at a time) guarantees only one `TvidTxn` is ever live, so commit-merge never contends with another writer.
- **R1963:** `Store.addChunkIDToVRecord` calls `tt.Add(tvid, tag, value, OriginPersistent)` whenever it allocates a new tvid via `allocIDInTxn`. `Store.removeChunkIDInTxn` calls `tt.Remove(tvid)` whenever it deletes a V record entirely (orphan cleanup).

### Tmp:// integration

- **R1964:** `TmpTagStore` per-chunk entries store tvids instead of `(tag, value)` strings. Read methods (`TagValueChunks`, `FileTagValues`, etc.) resolve tvids via the shared `TvidMap` before returning results.
- **R1965:** `TvidMap.AllocOverlay(tag, value)` allocates a fresh overlay tvid when `Lookup(tag, value)` finds none. Overlay tvids count down from `MaxUint64` using a separate in-memory counter, mirroring the chunkid/fileid overlay convention; the high bit (set when read as int64) marks a tvid as overlay-issued.
- **R1966:** `TmpTagStore.UpdateTagValues` and `AppendTagValues` resolve each `(tag, value)` to a tvid (existing via `Lookup`, new via `AllocOverlay`) before writing per-chunk entries.
- **R1967:** `TmpTagStore.RemoveFile` drops the file's per-chunk tvid contributions. If a tvid loses its last `tmp://` producer AND its origin is `OriginOverlay`, it is removed from the live `TvidMap`. `OriginPersistent` tvids are never dropped on `tmp://` removal — the index record still owns them.

### Lifetime and recovery

- **R1968:** No persistence beyond V records. Server restart triggers `LoadTvidMap` again. No schema marker, no version check, no `ark rebuild` interaction.
- **R1969:** Crash safety: a process death mid-write rolls back the transaction. The next startup reloads from V records. Overlay entries from an aborted `TvidTxn` never enter the live map because `Commit` was never called.

## Feature: @id indexing
**Source:** specs/at-id.md

- **R1970:** `@id: UUID` extracts and indexes as a regular tag through the existing `ExtractTagValues` pipeline. No special record type — V/F/T records use the same shape as any other tag.
- **R1971:** The chunk that contains the `@id:` declaration *is* the resolved target. No separate section-anchor concept; the chunker's granularity (markdown heading, lines window, JSONL message, PDF block, etc.) determines the resolved scope.
- **R1972:** Markdown preamble (content before the first heading) resolves to the file's first chunk. An `@id:` in the preamble identifies the whole leading section. An `@id:` under a heading identifies that heading's chunk.
- **R1973:** Resolution chain: `TvidMap.Lookup("id", UUID)` → tvid; `Store.TagValueChunks("id", UUID)` → chunkids; microfts2 `CRecord.FileIDs` → fileid; `FileInfoByID` → path + chunk Location. Each leg already exists; no new code beyond consumers.
- **R1974:** Multiple chunks with the same UUID resolve to all matching chunks. The index returns the full list; callers choose by policy (all, first, error). The index does not enforce UUID uniqueness — duplicates are an authoring concern.
- **R1975:** `tmp://` content participates in `@id` indexing via the unified read path. `Store.TagValueChunks` unions persistent and overlay results, so a UUID declared in `tmp://` content resolves alongside disk content for the server's lifetime.

## Feature: @link rendering
**Source:** specs/at-link.md

- **R1976:** `DB.ResolveLink(value string) (path, location string, ok bool)` resolves an `@link:` value to a `/content/` URL target. UUID branch first (in-memory `TvidMap.Lookup("id", value)` → tvid → V record → chunkid → fileid → path + Location); path branch second (`microfts2.CheckFile(value)` returns the indexed path with empty Location). Returns `ok=false` when neither resolves.
- **R1977:** UUID resolution uses the live `TvidMap` and a single `bucket.Get` against the exact V key — no prefix scan. Chunkid → fileid uses the existing `chunkID→fileIDs` resolver wired in `DB.Open`.
- **R1978:** Path resolution accepts the value as a literal path. No anchor parsing (`path:line`, `path:/regex/`, `path[N]:`) and no content-hash fallback in v1; both are deferred to a follow-up.
- **R1979:** `wrapTagElements(html string, db *DB) string` consumes a `*DB` so it can call `ResolveLink`. A nil `db` short-circuits the link branch to the broken renderer; tests that bypass the server pass nil.
- **R1980:** When `name == "link"` and `ResolveLink` returns ok, the rendered output is `<a class="ark-link" href="/content/{path}?range={loc}">@link: VALUE</a>` — replacing the would-be `<ark-tag>` wrapper. The `?range=` query param is omitted when location is empty (path-only resolution).
- **R1981:** When `name == "link"` and resolution fails, render `<ark-tag class="ark-link-broken"><name>link</name> <value>VALUE</value></ark-tag>` so the tag widget still picks it up but the frontend can style it as broken.
- **R1982:** All seven `wrapTagElements` call sites (`server.go` ×6, `search.go` ×1) thread `srv.db` (or the equivalent DB reference held by their caller) into the function.

## Feature: @ext parsing and target resolution
**Source:** specs/at-ext-parsing.md

- **R1983:** `ParseExtTarget(value string) (target string, tags []TagValue, ok bool)` splits an `@ext:` value into a TARGET substring (everything up to the first embedded `@tag:`) and the chain of routed `TagValue` entries that follow. Each routed tag's value is clipped at the next embedded `@tag:` boundary or end of string. Tag names are lowercased; values are TrimSpace'd.
- **R1984:** `ParseExtTarget` returns `ok=false` when the TARGET is empty or no embedded tag follows it. A TARGET-only `@ext:` declares no annotation and is treated as a no-op rather than an error.
- **R1985:** `DB.ResolveExtTarget(target string) []uint64` returns the chunkids identified by the TARGET. Empty slice signals "broken or unknown." UUID branch is tried first via `TvidMap.Lookup("id", target)` and the V record's full chunkid blob — every chunk carrying that id is returned. Path branch is tried second via `microfts2.CheckFile(target)` and `FileInfoByID`, returning only the file's first chunk (preamble convention).
- **R1986:** UUID resolution wins when a target string matches both an `@id` value and a path; UUIDs are the more specific identifier.
- **~~R1987:~~** (Retired T58 — see R2366) Anchored target forms (`path:line`, `path:string`, `path:/regex/`, `path[N]:anchor`, `path^:anchor`) are documented in `specs/at-ext-parsing.md` as deferred. v1 ships UUID + path; anchors land as separate branches inside `ResolveExtTarget`.
- **R2365:** `ParseExtTarget`'s TARGET/tag boundary scanner is anchor-aware. When scanning for the first `@tag:` boundary, the scanner skips over `"..."` and `/.../` spans so that an embedded `@tag:` pattern inside a target anchor is not mistaken for the start of the tag list. Anchor openers are only consumed when they appear immediately after a base's `:` (the only legal anchor start position); openers elsewhere in the TARGET span are treated literally.
- **R2366:** Target syntax follows the grammar `TARGET := PATH MODIFIER? PATH_NARROWER? | UUID MODIFIER? UUID_NARROWER?` with `MODIFIER := "[" N "]" | "^"`, `UUID_NARROWER := ":" UUID_ANCHOR`, `PATH_NARROWER := ":" PATH_ANCHOR`, `PATH_ANCHOR := UUID_ANCHOR | RANGE_STRING`, `UUID_ANCHOR := QUOTED_STRING | REGEX`. Each TARGET form parses to exactly one production.
- **R2367:** UUID bases are prefixed with `%` to disambiguate from PATH bases (`%UUID_VALUE`). The sigil is required because `@id` values are free-form strings (see `specs/at-id.md`) and can collide with relative path names; without it the resolver cannot tell whether `notes` is a path or a UUID.
- **R2368:** `\%` is the only escape sequence and resolves to the literal character `%`. `\` before any character other than `%` is literal. `\\%` therefore parses as a literal `\` followed by the `\%` escape, producing literal `\%`. The V record stores the authored form verbatim including the leading `\`; resolution strips `\%` → `%` at lookup time.
- **R2369:** A `PATH` base is **absolute** when it starts with `/` or `~/` (after tilde expansion). Otherwise it is **relative** to the source file's directory — the directory containing the file whose `@ext:` declaration produced the V record. UUID bases are unaffected by this distinction.
- **R2370:** Anchor type is selected by the first non-whitespace character after the `:`: `"` → `QUOTED_STRING` (consume until next unescaped `"`), `/` → `REGEX` (consume until next unescaped `/`), anything else → `RANGE_STRING` (consume until end of TARGET span; PATH base only).
- **R2371:** `MODIFIER` post-filters the anchor result set: no modifier → all matches (hyperedge default); `[N]` → Nth match only, 1-based, out-of-range → empty; `^` → equivalent to `[1]`. `MODIFIER` is meaningful only when a `NARROWER` is present; a bare base resolves on its own (preamble for path, all `@id` chunks for UUID).
- **R2372:** UUID bases accept only `QUOTED_STRING` and `REGEX` anchors. A UUID with a `RANGE_STRING` anchor is invalid and resolves to the empty set. PATH bases accept all three anchor types.
- **R2373:** `DB.ResolveExtTarget(target, sourceDir string) []uint64` accepts the source file's absolute directory as a second argument. `sourceDir` is used to resolve relative `PATH` bases.
- **R2374:** Resolution of a relative `PATH` base uses `filepath.Join(sourceDir, target)`. Normalization is minimal: leading `~/` is substituted with `$HOME/`; no `Clean`, no `EvalSymlinks`, no case folding (matching microfts2's path-storage rule). This gives directories of cross-`@ext`-ing files the "portable bundle" property — copy/rename/delete the original and the copy still works independently.
- **R2375:** V records store the TARGET as authored. Relative paths stay relative; `~/` stays `~/`; the escape `\%` remains in the record. Absolutization happens only at resolve time, every time. Display layers and tooling see the user's intent verbatim.
- **R2376:** Resolution matrix — path bases: bare → first chunk of the file (preamble); `path:"string"` → all chunks in the file containing the literal substring; `path:/regex/` → all chunks in the file matching the regex; `path:range` → the chunk at that range. UUID bases: bare → every chunk carrying that `@id` across all files; `UUID:"string"` → UUID-matching chunks containing the literal substring; `UUID:/regex/` → UUID-matching chunks matching the regex.
- **R2377:** `RANGE_STRING` resolution defers to the chunker registered for the file: the chunker matches the range against its stored chunks. The chunker is the authoritative interpreter of range strings.
- **R2378:** The chunker contract is **soft**: a chunker's `Chunk.Range` value **should not** start with `"` or `/`. If it does, the chunk still indexes normally and remains searchable; only the `:RANGE_STRING` form of `@ext` is unavailable for that chunk (the parser correctly reads the leading `"` or `/` as a different anchor type). All other locator forms — bare base, `:"string"`, `:/regex/`, UUID — still work for routing to such chunks.
- **R2379:** The workshop UI surfaces non-conforming range strings loudly: when a chunk's range string violates the soft contract, the UI flags the chunk and suggests an alternate locator. This visibility requirement is the loud-failure half of the soft-contract bargain — user data access is preserved while the unavailable feature is made explicit.
- **R3050:** `insight` is a reserved metadata field, not a routed tag, and carries no `@` sigil. On an `@ext-candidate` value it appears **first, before the TARGET**, always quoted (`insight: "..."`). `ParseExtTarget` peels a leading `insight: "..."` (via `stripLeadingInsight`) before it parses the TARGET, skipping the quoted span so the rationale may contain `@` or `:` without being misread. Leading-and-quoted is required because a bare TARGET (path / `%uuid` / RANGE_STRING) is undelimited and may contain spaces, so a field placed between the TARGET and the routed tag could not be distinguished from the TARGET. The field is recognized only when `insight:` is followed by whitespace then a quote (so a relative-path base named `insight` with a `:"anchor"` narrower is not mistaken for it); it is excluded from the routed-tag list and preserved per-line, so distinct insights make distinct proposals.
- **R3090:** `@ext-candidate` and `@ext-judgment` lines carry a leading `YYYY-MM-DD` **first-seen date** immediately after the marker (before the disposition, insight, and TARGET). It is recorded **once** — when the line is first authored (`DB.CandidateExtTag` / the reject-created `@ext-judgment` stamp `time.Now()`) — and **frozen**: a later `@count` bump preserves the line's original date rather than restamping it (`bumpCountLine` peels and reinserts each line's own date). `ParseExtTarget` peels the leading date (via `stripLeadingDateDisposition`, before the insight peel) so it never reaches the TARGET; a committed `@ext` carries no date and passes through unchanged.
- **R3091:** The first-seen date is **excluded from line identity**: the `@count` dedup match (`bumpCountLine`) compares the date-stripped line, so a repeat of the same identity bumps the existing line's `@count` and keeps its frozen date instead of appending a duplicate. Date recognition is **shape-only** (`isExtDate`) — exactly ten characters `YYYY-MM-DD` (ASCII digits with `-` at positions 4 and 7) followed by a space; month/day ranges are not validated. The one accepted ambiguity is a bare TARGET that is literally a ten-character date followed by a space; a dated filename such as `2026-07-12.md` has no space at position 10 and is safe.
- **R3092:** An `@ext-candidate` line carries a `disposition` bare token — `internal` or `external`, default `external` — placed right after the marker (after the date on disk). `stripLeadingDateDisposition` peels it before the TARGET, and `candidateLine` emits it as part of the line. The disposition **is part of the line identity**: an internal and an external proposal of the same `(TARGET, tag, value, insight)` are **distinct lines** with independent `@count` tallies, exactly as distinct insights are. It names where an eventual accept writes the tag — `external` = the mirror routing (today's behavior), `internal` = the source-file body — with the internal write path itself defined and built by the internal-disposition feature. `@ext-judgment` lines carry no disposition.
- **R3093:** `stripLeadingDateDisposition` peels the disposition token **only after a leading date was peeled**, which bounds the ambiguity of a TARGET literally named `internal`/`external` (an undated committed value never mis-peels a disposition). An `@ext-judgment` line carries the leading date but no disposition, so its peel yields only the date; a non-disposition word after the date is left in place (only recognized tokens peel).
- **R3104:** An `@ext-candidate` line may carry a bare `replace` token immediately after the disposition, naming an eventual accept as a **replace** (collapse the target's values of the tag to the accepted value) rather than the default **add** (append). `stripLeadingDateDisposition` peels `replace` **only after a disposition was peeled** (itself only after a date), which bounds the ambiguity of a TARGET literally named `replace`. `replace` **is part of the line identity**: a replace-proposal and an add-proposal of the same `(TARGET, tag, value, insight, disposition)` are **distinct lines** with independent `@count` tallies, exactly as a differing disposition is. `candidateLine` emits it; `@ext-judgment` lines carry no `replace`.
- **R3105:** `ark ext candidate` accepts `--replace`, which stamps the `replace` token onto the authored `@ext-candidate` line (part of the line identity per R3104, so an exact-identity repeat with the same replace/disposition bumps `@count`, and a differing add-vs-replace is a new proposal).

## Feature: Internal-disposition tagging
**Source:** specs/internal-disposition.md

- **R3095:** Each writable text chunker (markdown/bracket/indent) is registered wrapped in an `internalTagChunker`, which embeds the union interface of every microfts2 chunker interface it satisfies (Chunker + AppendAwareChunker + RandomAccessChunker + FileChunker + ChunkerMetadata) so all fast paths promote to the underlying chunker, and adds one method, `InsertTag`. The wrapper is transparent for indexing and retrieval; only `InsertTag` is new. Registration happens on `DB.Open` (markdown) and in `registerConfigChunkers` (bracket/indent).
- **R3096:** The capability "can this file type host an inline tag?" is a `chunkerByName[strategy].(tagInserter)` type-assertion. Only the three text wrappers implement `tagInserter`; `lines` / `chat-jsonl` / `pdf` do not, so an internal disposition against them degrades to external by construction.
- **R3097:** A **chunk-level** internal tag (an anchored target) is inserted inside the target chunk, on the line right after its structural opener (markdown heading, bracket opening line, indent block header), so the chunker merges it into the chunk and it stays with that chunk after a re-chunk. A headingless markdown prose chunk has no opener, so the tag goes at the top of the chunk's line range. `InsertTag` is a pure function `(fileBytes, targetChunk, tag, value, scope) → (newBytes, ok)`.
- **R3098:** A **file-level** internal tag (a bare-path target) is inserted at the top of the file, above the first heading, where the `@`-run stands as its own chunk — annotating the whole file without re-indexing any section.
- **R3099:** A code chunker (bracket/indent) comment-wraps the tag line with its `CommentSyntax()` to keep the source valid; a comment-less code chunker (`CommentSyntax() == ""`, e.g. `bracket-json`) cannot, so `InsertTag` returns `ok=false` and the caller falls back to external. Markdown needs no comment. An indent chunker prefixes the tag line with the block body's indentation so it stays in scope; a bracket chunker's column is cosmetic.
- **R3100:** `AcceptExtTag` reads the disposition of each matching `@ext-candidate` line (`peelExtCandidateDisposition` after the leading date) and resolves each candidate per its own disposition. `collectAcceptedCandidates` returns one entry per matching routed tag (the reserved `@count` skipped), tagged with the line's disposition, matching the same spans `applyExtMirrorEdit(extOpRemove, candidate)` consumes.
- **R3101:** For an internal-disposition candidate, `writeInternalTag` resolves the target to its source file (bare path → file-level; single-chunk address → chunk-level), reads the file bytes, applies the chunker's `InsertTag`, and writes the result with a temp+rename that preserves the file's mode. The write runs on the DB actor (serialized against the mirror write and other sessions, which all proxy to the one server); the tag materializes on the normal reindex the file change triggers — no new DB mutation path.
- **R3102:** Internal disposition falls back to the external mirror route (returns `wrote=false`) when the target does not resolve to a single writable, tag-insertable location: a multi-chunk (or unresolved) match, an incapable chunker type, a comment-less code chunk, a read-only zone (`ChunkInfo.Writable == false`), or a file unwritable on disk. Degrade, never refuse.
- **R3103:** Every accept — internal or external — writes a positive `@ext-judgment @count:+1` (deduped per tag), the accept-time producer of the signed-RJ axis. The R3070 net-rejected filter fires on negative counts only, so a positive judgment reinforces without suppressing. Symmetric with reject's `@count:-1`.
- **R3106:** `AcceptExtTag` dispatches on `(disposition, replace)` (R3104) → four cells: **external-add** appends the `@ext` mirror edge (`extOpAdd`, today's path), **external-replace** collapses the mirror's values of the tag to the accepted one (`extOpSet`), **internal-add** inserts a new inline `@tag:` line (R3101), **internal-replace** rewrites the existing inline `@tag:` line in the target. `replace` collapses **all** of the target's values of that tag to the accepted value (exact for a single-value tag; lossy for a multi-value tag, which is out of scope).
- **R3107:** The internal-replace cell locates the existing inline `@tag:` line in the target — inside the target chunk for a chunk-level address, at the file's top-of-file `@`-run for a file-level one — and rewrites its value in place, using the same per-chunker stencil and the same temp+rename on the DB actor as `writeInternalTag`'s insert. It **degrades to internal-add** (a fresh insert) when no such line is present, and to **external** per the capability gate (R3102) when the file type cannot host an internal tag at all.

## Feature: @ext storage layer
**Source:** specs/at-ext-storage.md

- **R1988:** V records become multi-sets — `addChunkIDToVRecord` no longer dedups; every contribution (inline or ext-routed) writes its own varint entry into `V[tag][value][tvid]`. `removeVarint` removes one occurrence so other contributors survive when one is cleaned up.
- **R1989:** X records carry ext provenance: `X[tvid_ext][target_chunkid] → packed routed_tvid varints`. One X record per (tvid_ext, target_chunkid) pair; multiple targets for one tvid_ext produce multiple records, prefix-scannable by tvid_ext.
- **R1990:** X records are chunkid-keyed (not fileid-keyed) so that startup scan can populate `chunkToTargets[chunkid] → []tvid_ext` and the orphan callback can find routings to clean up after offline edits across an ark restart.
- **R1991:** F records stay inline-only. `F[source_chunkid][ext]` carries the @ext tag's tvid; routed-tag tvids are NOT added to any target chunk's F record.
- **R1992:** The in-memory ExtMap maintains six structures alongside DB writes: `targetToChunk[tvid_ext] → []chunkid`, `chunkToTargets[chunkid] → []tvid_ext`, `fileidToTvids[fileid] → []tvid_ext`, `extByAnchor[spec_text] → []tvid_ext`, `unresolvedTargets[tvid_ext] → bool`, `virtualTagCount[tag] → int`.
- **R1993:** ExtMap is rebuilt at startup by scanning X records — deterministic and bounded by total routing count.
- **R1994:** Spec recovery for any tvid_ext is `TvidMap.Resolve(tvid_ext)`; the @ext value text returned contains the original target spec, so no separate anchor cache field is stored.
- **~~R1995:~~** (Retired T59 — see R2380) `extByAnchor` keys on the literal target spec text from the @ext value (UUID string, or path string). The same map covers both forms — UUIDs and paths don't collide. The map handles UUID mobility (a UUID gains an additional location) and the "appearing" case (target spec resolves once a file is added or `@id` is added).
- **R1996:** Indexing flow for each `TagValue{Tag: "ext", Value: V}`: ParseExtTarget → ResolveExtTarget → self-reference check → for each accepted target chunkid, write X record, multi-set append target chunkid to each routed tag's V record, allocate routed tag tvids via `allocIDInTxn(IFieldNextTvid)`, update ExtMap entries, and increment `virtualTagCount[routed_tag]` once per added entry.
- **R1997:** When `ResolveExtTarget` returns empty, mark `unresolvedTargets[tvid_ext] = true` and populate `extByAnchor[target_spec]`. No X records or routed V entries are written. The @ext tag's own V/F/T records still land.
- **R1998:** Self-reference is rejected: if any resolved target's fileid equals the source's fileid, log an error and skip ext routing entirely. The @ext tag's V/F/T records still land so the broken @ext is visible to the user, but no chunks are routed and no X records are written.
- **R1999:** Routed-tag tvids are allocated from the same persistent counter as inline tags via `allocIDInTxn(IFieldNextTvid)`. No new TvidMap API is added; `AllocOverlay` stays exclusive to tmp:// content.
- **R2000:** Canonical re-resolution flow runs when file F is reindexed. Step 1 collects candidate tvid_exts from three sources: `fileidToTvids[F.fileid]`, `extByAnchor[F.path]`, and `extByAnchor[UUID]` for each `@id: UUID` value added or removed in F's chunks.
- **R2001:** Canonical re-resolution flow step 2: for each candidate tvid_ext, recover the spec via `TvidMap.Resolve` → `ParseExtTarget` → `db.ResolveExtTarget` to produce a new chunkid set.
- **R2002:** Canonical re-resolution flow step 3: diff the new chunkid set against the old set (`targetToChunk[tvid_ext]`, scoped to F's chunks for the file-level part of the trigger) to compute Adds (new ∖ old), Removes (old ∖ new), and Updates (unchanged chunkids whose V record blobs change as a side effect).
- **R2003:** Canonical re-resolution flow step 4 (Adds): write `X[tvid_ext][added_chunkid]`; multi-set-append `added_chunkid` to each routed tag's V record; bump `virtualTagCount[routed_tag]`.
- **R2004:** Canonical re-resolution flow step 5 (Updates): rewrite changed V record blobs whose contents shifted because of other entries added/removed in the same record.
- **R2005:** Canonical re-resolution flow step 6 (Removes): strike `removed_chunkid` from each routed tag's V record (one occurrence — multi-set remove); decrement `virtualTagCount[routed_tag]`. If a V record empties, delete it and decrement T as needed; delete `X[tvid_ext][removed_chunkid]`.
- **R2006:** Canonical re-resolution flow step 7 (Empty new set): drop all X records for tvid_ext, mark `unresolvedTargets[tvid_ext] = true`, and update extByAnchor accordingly.
- **R2007:** Append-only file changes use the same canonical re-resolution flow. The diff is empty for unchanged chunks; Adds fire only when newly-resolvable anchors land in the appended content; Removes fire only when the chunker drops and replaces the previous last chunk. No "is this an append?" branch in the ext code.
- **R2008:** Source-side cleanup runs when a source chunk is orphaned. The existing F→V cleanup gives `tvid_ext` from `F[source][ext]`. For each (target_chunkid, routed_tvids) pair under `X[tvid_ext]`, strike target_chunkid from each routed tag's V record (one occurrence), decrement `virtualTagCount`, and drop the X record. Then drop tvid_ext from all ExtMap structures.
- **R2009:** During source-side cleanup and re-resolution, `TvidMap.Resolve(tvid_ext)` MUST be called BEFORE `tt.Commit` drops the tvid; otherwise spec recovery fails when the V record empties.
- **R2010:** T-totals under multi-set V are computed at query time as `index_T[tag] + virtualTagCount[tag]`. The existing `adjustTagTotal` path stays unchanged for inline contributions; `virtualTagCount[tag]` is incremented on each routed-tag entry written and decremented on each removed.
- **R2011:** Ext routing rides inside microfts2's per-file `db.Update` transaction. X record mutations and V record updates from the ext flow use the supplied txn and the same `TvidTxn` that the @ext tag's own tvid lifecycle uses. Multi-file batch convergence is acceptable; some redundant resolution work is tolerated.
- **R2380:** `extByAnchor` is keyed by the BASE of the TARGET only — the absolutized path or the `%UUID_VALUE` — not by the full TARGET text. The narrower (anchor + modifier) is recovered from the tvid_ext's stored TARGET text via `TvidMap.Resolve(tvid_ext)` and re-evaluated at resolve time. This guarantees that target-side reindexing fires re-resolution for narrower-bearing source declarations whose narrowers may now be satisfiable, including the "initially unresolved → satisfiable on later target change" path that `fileidToTvids` cannot cover.

## Feature: @ext overlay target routing
**Source:** specs/at-ext-storage.md

- **R2012:** Each `@ext` routing falls into one of four scope cases by `(sourcePersistent, targetPersistent)` where `IsOverlayID(id)` (high bit set) marks an id as overlay-issued. Index X and V records are written iff `bothPersistent := !IsOverlayID(sourceChunkID) && !IsOverlayID(targetChunkID)`. Any overlay involvement on either end keeps the routing entirely in ExtMap's in-memory state.
- **R2013:** ExtMap gains `overlayRoutings map[uint64]map[uint64][]uint64` (`tvid_ext → target_chunkid → routed_tvids`) — in-memory parallel to X records, populated only when `!bothPersistent`. Session-scoped; never persisted; empty on every startup.
- **R2014:** ExtMap gains `overlayValues map[string]map[string][]uint64` (`tag → value → target_chunkids`) — in-memory parallel to V records, populated only when `!bothPersistent`. Multi-set semantics: each contribution adds an entry; removal strikes one occurrence. Session-scoped; never persisted; empty on every startup.
- **R2015:** `ExtMap.Rebuild` is unchanged — it scans X records and populates only the six original maps. `overlayRoutings` and `overlayValues` start empty on each session and fill as overlay sources index.
- **R2016:** `applyIndexExt` decides per-target. For each accepted target chunkid, compute `bothPersistent`; if true, write X record plus multi-set-append target chunkid to each routed tag's V record (existing path); otherwise write `overlayRoutings[tvid_ext][target_chunkid] = routed_tvids` and append target chunkid to `overlayValues[tag][value]` for each routed tag. Either branch updates the six original maps (`targetToChunk`, `chunkToTargets`, `fileidToTvids`, `extByAnchor`, `virtualTagCount`).
- **R2017:** Routed-tag tvid allocation stays unified. Persistent sources allocate via `allocIDInTxn(IFieldNextTvid)` through the supplied `TvidTxn`; overlay sources allocate via `TmpTagStore.resolveOrAlloc` / `TvidMap.AllocOverlay`. Both paths reuse existing tvids when `(tag, value)` already resolves.
- **R2018:** (inferred) Self-reference rejection fires on every routing regardless of overlay-ness. A `tmp://` source whose @ext resolves to a chunk in the same `tmp://` fileid is rejected; the @ext tag's V/F/T records still land but no chunks are routed. Extends R1998 to overlay sources.
- **R2915:** Overlay `@ext` indexing (`Indexer.runOverlayExtRouting`) opens a read-only `db.View` transaction and threads it into `applyIndexExt`, so resolving a persistent target's fileid (`chunkFileID` → `fts.ReadCRecord` — needed for the R2018 self-reference check and the `fileidToTvids` update) has a valid txn. Overlay sources write no index records (`bothPersistent` always false), so a read-only txn suffices and the `TvidTxn` stays nil. Distinct from R2023's cleanup path, which keeps its nil txn because `CleanupSource` branches on `bothPersistent` before any index access.
- **R2019:** `Store.TagValueChunks(tag, value)` and `Store.TagFiles(tags)` gain a third union leg by consulting `ExtMap.OverlayTagValueFiles(tag, value)` and `ExtMap.OverlayTagFiles(tags)` alongside persistent index results and `TmpTagStore` overlay-direct results. Chunkids do not collide across the three sources.
- **R2020:** `ExtMap.OverlayTagValueFiles(tag, value) []uint64` returns a copy of `overlayValues[tag][value]` under RLock. `ExtMap.OverlayTagFiles(tags []string)` walks `overlayValues` for the requested tag names and returns chunkid + tag entries for each match.
- **R2021:** `virtualTagCount[tag]` counts every routed contribution regardless of overlay-ness; the existing `T_total = index_T[tag] + virtualTagCount[tag]` formula stays correct without modification.
- **R2022:** Persistent source orphan callback uses the existing F→V cleanup to obtain the source's tvid_ext list, then invokes `ExtMap.CleanupSource(sourceChunkID, tvidExt, txn, tt)` for each tvid_ext.
- **R2023:** Overlay source removal hooks into `TmpTagStore.RemoveFile` and `TmpTagStore.RemoveChunk`. Before TmpTagStore drops the chunk, it enumerates the chunk's `tvids["ext"]` and invokes `ExtMap.CleanupSource(sourceChunkID, tvidExt, nil, nil)` for each — txn and TvidTxn are unused because every routing for an overlay source has `bothPersistent=false`.
- **R2024:** `CleanupSource(sourceChunkID, tvidExt, txn, tt)` walks `targetToChunk[tvidExt]` (in-memory). For each target_chunkid: compute `bothPersistent`; if true, read routed_tvids from the X record, strike target_chunkid from each routed tag's V record (one occurrence), and delete the X record; otherwise read routed_tvids from `overlayRoutings[tvidExt][target_chunkid]`, strike target_chunkid from `overlayValues[tag][value]` (one occurrence), and delete the `overlayRoutings` entry. Decrement `virtualTagCount[tag]` per routed tag.
- **R2025:** After the per-target loop, `CleanupSource` drops tvidExt from all relevant maps: `targetToChunk`, `chunkToTargets`, `fileidToTvids`, `extByAnchor`, `unresolvedTargets`, and `overlayRoutings`.
- **R2026:** `applyReresolve` gains the same per-target `bothPersistent` branch on Adds and Removes. Persistent targets touch X / V; overlay targets touch `overlayRoutings` / `overlayValues`. The Updates step (V record blob shifts) only applies to persistent targets — overlay representations don't pack varints, so there is nothing to shift.
- **R2027:** The candidate set for re-resolution is unchanged. Overlay routings appear in `fileidToTvids[F.fileid]` / `extByAnchor[F.path]` / `extByAnchor[UUID]` under the same keys as persistent ones, so they re-resolve alongside persistent routings on file-change events.
- **R2028:** `DB.chunkFileID(txn, chunkID)` branches on `IsOverlayID(chunkID)`. Overlay chunkids resolve via `Store.filesForChunk(chunkID)` (which routes to `TmpTagStore.FilesForChunk` through the `SetChunkResolver` wiring); persistent chunkids continue reading `fts.ReadCRecord(txn, chunkID)`.
- **R2029:** ExtMap holds an in-memory `overlayErrors []OverlayError` log. Each entry: `{Time, SourceChunkID, SourceFileID, Severity, Message}` where `Severity` is `info` or `warn`. The log is in-memory only and resets on each session.
- **R2030:** ExtMap exposes `RecordOverlayError(severity, sourceChunkID, sourceFileID, message)` for internal callers (invoked by `applyIndexExt` and `applyReresolve` when they take overlay-affecting branches), `OverlayErrors() []OverlayError` for read snapshots, `ClearOverlayErrors()` to reset, and `AddOverlayError(severity, message)` for externally-supplied entries.
- **R2031:** The overlay error log is the data source for the `ark errors` CLI command (PLAN.md V2.5) `dump`, `clear`, and `add` operations against the overlay log. Persistent error records are a separate concept and out of scope for this feature.

## Feature: tag overview
**Source:** specs/tag-overview.md

- **R2032:** Sidebar entries are scoped to the current scroll position through the end of the presented content; when `/content/` serves a slice of a file, the cutoff is the bottom of the slice rather than end-of-file.
- **R2033:** The sidebar has three modes: collapsed (badge only), abbreviated (entry names only), and full (names + values for tag entries).
- **R2034:** Clicking the badge cycles modes in order 1 → 2 → 3 → 1 (collapsed → abbreviated → full → collapsed).
- **R2035:** The badge displays a mode glyph that indicates the current mode (collapsed `▢`, abbreviated `▤`, full `▦`).
- **R2036:** Each file opens in abbreviated mode. Mode does not persist across files.
- **R2037:** A file with no headings and no tags shows no badge.
- **R2038:** In collapsed mode the badge text shows the first entry of the current section followed by a total count (e.g., `# Munsters + 5`).
- **R2039:** The badge has two visibly separate hit zones: `[mode-glyph N tags]` cycles modes; `[▼]` opens the category-filter dropdown.
- **R2040:** Heading entries render as the heading text indented per heading level (h1, h2, h3...). Heading rows have no icons; their row click is the only affordance.
- **R2041:** Tag entries (full mode) display tag name + value, grouped per section under the nearest heading or chunk location. Each tag row carries a search icon (🔍) on the right.
- **R2042:** Tag entries (abbreviated mode) display tag name only; hover (desktop) or tap (touch) reveals a single entry's value inline without leaving abbreviated mode.
- **R2043:** Ext entries display a virtual-tag icon (⊕) preceding the tag name and an external-link icon (↗) at the right of the row, after the search icon. Both ext-specific icons appear in abbreviated and full modes.
- **R2044:** At most one entry's peek is open at a time. Any tap closes any open peek; tapping a closed entry then opens it.
- **R2045:** A sidebar section is anchored by either a heading or a tagged chunk, whichever appears first. Auto-track highlights the section currently at the top of the viewport; the highlight remains on that entry until the next heading or tagged chunk reaches the top.
- **R2046:** Heading row click scrolls the document to that heading's chunk.
- **R2047:** Tag row text/row click (inline or ext) scrolls the document to that tag's chunk.
- **R2048:** Inline tag row 🔍 click dispatches a click on the corresponding body `<ark-tag>` element, which scrolls to the tag if needed and opens an `<ark-search>` panel below it seeded with `tag: value`.
- **R2049:** Ext tag row 🔍 click dispatches the indicator's panel-open action with the specific tag from the clicked row, bypassing the pick-list dropdown.
- **R2050:** Ext tag row ↗ click navigates to the source document — the file containing the `@ext:` declaration — and scrolls to the relevant chunk.
- **R2051:** Hovering or long-pressing the ↗ icon shows a three-line tooltip: DEFINITION-PATH (= `externalFile`), divider, THIS-PATH (the file currently being viewed), and an `anchor: ANCHOR-SPEC` line. The `anchor:` line is omitted entirely when `externalTarget` is empty.
- **R2052:** The sidebar header has a substring filter input. Filter tokens are whitespace-separated and order-independent: every token must match (substring, case-insensitive) against the entry's currently visible text.
- **R2053:** Filter visible-text scope is mode-dependent: in abbreviated mode, name + heading text only; in full mode, name + value + heading text. Hover-revealed values do not count as visible.
- **R2054:** Filter matched substrings are highlighted in entries using the existing ark search highlight style.
- **R2055:** The category filter dropdown opens from the badge's `[▼]` hit zone as a custom popover with three checkboxes — headings, inline tags, ext tags. Empty selection means all categories are shown.
- **R2056:** When a filter is active, the badge shows the filtered count and a small indicator on the `[▼]` glyph; when no filter is active, the badge shows the total count.
- **R2057:** Filter state resets per file. Filter state persists across mode toggles within the same file.
- **R2058:** When the auto-track current entry is filtered out, the highlight falls to the nearest visible entry above. If no visible entry exists above, no highlight is shown.
- **R2059:** When the filter input does not fit next to the badge on its row, it wraps to the next line.
- **R2060:** The sidebar's left edge is a draggable resize handle, touch-draggable on touch devices.
- **R2061:** Sidebar minimum width keeps the badge readable; sidebar maximum width is `viewport - 3rem`.
- **R2062:** Default sidebar width on first open (no I record present) is 25% of the viewport width.
- **R2063:** Two sidebar widths persist across sessions in DB I records: `sidebar-width-tag-name` (abbreviated mode) and `sidebar-width-tag-name-value` (full mode). Switching modes resizes the sidebar to that mode's stored width.
- **R2064:** The resize handle and per-mode persisted widths apply only when the sidebar is open (modes 2 and 3); collapsed mode shows only the badge in its corner.
- **R2065:** The server emits an `<ark-ext-tags>` custom element at the top of any chunk that has ext routings; the element contains one `<ark-tag>` child per ext-routed tag at that location.
- **R2066:** `<ark-ext-tags>` renders a Bootstrap-style tag icon — a single-tag glyph when its location has one ext tag and a stacked multi-tag glyph when several — as SVG, scaling cleanly and themed via CSS.
- **R2067:** `<ark-ext-tags>` does not consume vertical space; it overlays the chunk's first line area without affecting document flow.
- **R2068:** Mousedown on `<ark-ext-tags>` opens a dropdown listing each ext tag at the location with its value. The element exposes ARIA role `button` with `aria-haspopup="menu"`.
- **R2069:** Pick-list click-and-drag gesture: mousedown on `<ark-ext-tags>` → drag down onto a tag → mouseup on that tag opens an `<ark-search>` panel at the indicator's seam, seeded with `tag: value`.
- **R2070:** Pick-list click-and-release gesture: mousedown on `<ark-ext-tags>` → mouseup without moving onto a tag leaves the dropdown open; subsequent click on a tag opens the search panel; click outside or Escape dismisses.
- **R2071:** The pick-list dropdown appears even when the location has only one ext tag — there is no single-tag shortcut that skips the dropdown.
- **R2072:** `<ark-ext-tags>` keyboard support: Enter or Space opens the dropdown, arrow keys navigate, Enter selects.
- **R2073:** When `<ark-tag>` is a child of `<ark-ext-tags>`, it carries `externalFile` (source file path) and `externalTarget` (anchor part of the target spec, omitted/empty when the target was a bare path or bare UUID) attributes. The target file path is implicit (= the file currently being rendered) and is not duplicated on the element.
- **R2074:** All `<ark-tag>` elements — inline and ext-routed — carry an `id` attribute for sidebar DOM anchoring.
- **R2075:** HTML and markdown content emit standard `<h1>`–`<h6>` heading elements with `id` attributes for sidebar anchoring.
- **R2076:** PDF content emits `<ark-heading rect="...">` elements positioned absolutely over the `<pdf-chunk>` canvas at rect-derived coordinates. v1 PDF headings carry no level information and render as a flat list in the sidebar.
- **~~R2077:~~** (Retired T47 — no replacement) pdftext gains a heading-rect output (currently absent) so the server has the data needed to emit `<ark-heading>` elements.
- **R2078:** The `/content/` endpoint emits overview data inline (push, not pull). No separate `/overview/` endpoint is created; the existing `/content/` response carries every element the sidebar needs.
- **R2079:** Server-side `/content/` rendering consults the inline tag store, `chunkToTargets` for ext routings, and the chunker / pdftext output for headings to produce the unified element tree.
- **R2080:** A `tagsForChunk` Go method is added to expose per-chunk inline tags for `/content/` rendering. (Today no such method exists.)
- **R2081:** In HTML chunks, `<ark-ext-tags>` and `<ark-heading>` (when applicable) are positioned absolutely within their chunk container (`position: absolute` over a `position: relative` chunk wrapper) so they overlay without disrupting flow.
- **R2082:** In PDF chunks, `<ark-ext-tags>`, `<ark-heading>`, and `<ark-tag>` ride above the `<pdf-chunk>` canvas via absolute positioning at coordinates derived from their rect attributes — the same approach inline `<ark-tag>` already uses for PDF tags.
- **R2083:** Sidebar, badge, indicators, and filter render in `<ark-search>` results' `/content/` iframes identically to standalone `/content/` views — search-result rendering inherits the overview, with no separate code path.
- **R2084:** (inferred — scope boundary) The CodeMirror-based markdown editing view is out of scope for v1. The overview is supported only in rendered content views (HTML, markdown read views, PDF). Editing-view support is a v2 follow-up.
- **R2130:** The sidebar publishes its current outer width as the CSS custom property `--ark-tag-overview-width` on the document element, updated whenever the sidebar resizes (drag, mode change, mount, collapse). Other content positions itself relative to the sidebar by reading this var.
- **R2131:** When `<body>` carries the `data-pdf-host` attribute, embedded `<ark-search>` panels apply `margin-left: 3em` and `margin-right: calc(max(3em, var(--ark-tag-overview-width, 0px)) + 1em)` so the panel's kill boxes remain reachable beneath the sidebar. The fallback gutter applies when the sidebar is absent.

## Feature: ark serve -compact
**Source:** specs/serve-compact.md

- **R2085:** `ark serve` accepts a `-compact` flag. When absent, startup is unchanged.
- **~~R2086:~~** (Retired T157 — see R2981) When `-compact` is set, startup runs `mdb_env_copy2(MDB_CP_COMPACT)` against each LMDB environment under `~/.ark/` (microfts2 and ark) before the server begins handling requests.
- **R2087:** Compaction copies into a sibling path (`<dbpath>.compact`); on success the original is replaced via atomic rename. On failure, the original is left in place, the partial copy is removed, and the error is logged.
- **R2088:** Compaction failure must not block service. Startup continues with the uncompacted database.
- **R2089:** Compaction occurs while the file lock on `~/.ark/` is held and the server is not yet listening; no read-only or read-write transactions are in flight from clients.
- **R2090:** When the post-compaction size of an environment is within 5% of the original size, the rename is skipped and the message "already compact" is logged. (inferred — avoids unnecessary I/O on a fresh DB or one compacted recently.)
- **R2091:** Compaction emits a stdout line per environment of the form `compacting <env>: <oldSize> → <newSize>` before the normal `serving on …` message.

## Feature: ark tag verify
**Source:** specs/tag-verify.md

- **R2092:** `ark tag verify` is a subcommand that cross-checks ark's tag-system state — F, V, T, X records and the in-memory ExtMap — and reports drift.
- **R2093:** `ark tag verify` accepts `--repair` (write corrections; default is read-only) and `--scope SCOPE` where SCOPE is one of `ext`, `tag-totals`, or `all` (default).
- **R2094:** Under `--scope ext`, for every F record carrying the `ext` tag, `verify` parses the value via `ParseExtTarget`, re-resolves the target via `ResolveExtTarget`, and reports missing X records, stale X records (target chunk no longer matches), and routed-tvid drift between the X record's stored tvids and the current `@ext:` value's routed tags.
- **R2095:** Under `--scope ext`, `verify` reports orphan X records — X records whose `tvid_ext` no longer corresponds to any F record source.
- **R2096:** Under `--scope ext`, `verify` cross-checks the in-memory ExtMap maps (`extByAnchor`, `targetToChunk`, `chunkToTargets`, `fileidToTvids`, `unresolvedTargets`, `extSource`) against the on-disk X records and reports any divergence.
- **R2097:** Under `--scope tag-totals`, for each T record, `verify` recomputes the total from V multi-set sizes plus `ExtMap.virtualTagCount`, and reports drift from the stored T value.
- **R2098:** `--scope all` runs both ext and tag-totals checks.
- **R2099:** Output is plain text, one issue per line, ending with a summary `verify: N issues found, M repaired`. Exit code 0 = no issues, 1 = issues (read-only) or partial repair, 2 = verify itself failed.
- **R2100:** With `--repair`: missing X records are written via `WriteExtRecord` plus matching `addChunkIDToVRecord` per routed_tvid; stale or orphan X records are removed via `DeleteExtRecord` plus matching `removeOneChunkIDFromVRecord`; routed-tvid drift is corrected by deleting and rewriting; tag-total drift rewrites the T value; ExtMap drift triggers `ExtMap.Rebuild`.
- **R2101:** Repair operations execute inside a single write transaction. Partial repair (some issues fixable, others not) is reported per-issue and surfaces via exit code 1.
- **R2102:** `verify` is linear in the number of F records carrying `ext`, X records, and T records; not on any hot path.

## Feature: CLI commands central reference
**Source:** specs/cli-commands.md

- **R2103:** `specs/cli-commands.md` is the canonical reference for ark's CLI surface. Every top-level command, every subcommand, and every flag is documented there; per-feature specs supply rationale and design context.
- **R2104:** When the central CLI spec disagrees with the implementation, the spec is the verification target — it gets updated to match the code, not the other way around.
- **R2105:** The central CLI spec contains: a Command Inventory table (one row per top-level command with synopsis, server requirement, and notes); a Global Flags section; a Conventions section (server detection, cold-start, exit codes, output formats, `tmp://` handling, `reorderArgs`, filter stack, path filters, aliases); and a per-command section with flag tables and behavior.
- **R2106:** Each per-command section lists every flag the implementation accepts, including default value and one-line meaning. Flags omitted from the spec are drift to be reconciled.
- **R2107:** (inferred) Internal/build-time commands (e.g. `bundle`, `chunk-chat-jsonl`, `search expand`) are included in the canonical inventory with their internal status noted.

## Feature: @ext storage layer (extSource)
**Source:** specs/at-ext-storage.md

- **R2108:** ExtMap maintains a seventh structure alongside the six in R1992: `extSource[tvid_ext] → source_chunkid` — a single source chunkid per tvid_ext, used by render and cleanup paths to identify which chunk authored the @ext declaration. When multiple chunks share the same compound @ext text (same tvid_ext), any of them is an acceptable source; the map holds one.
- **R2109:** `ExtMap.Rebuild` populates `extSource` while scanning X records: for each tvid_ext encountered, resolve it via `TvidMap.Resolve` to recover the (tag, value) pair, then read `V[ext][value][tvid_ext]` once and store its first source chunkid as `extSource[tvid_ext]`. The first-entry choice is stable across rebuilds because V record source-chunk lists are append-multi-set.

## Feature: compound-tag extraction (per-outer-tag dispatch)
**Source:** specs/tag-extraction-fixes.md, specs/at-ext-parsing.md

- **R2110:** `ExtractTagValues` returns one `(tag, value)` pair per `@x:` line — the *outer* tag, with `value` capturing from after `@x:` to end of line. It does NOT peel embedded `@y: z` segments out of the value as additional sibling entries. Compound interpretation is delegated to the outer tag's owner.
- **R2111:** Each outer tag owns its own embedded-tag semantics. `@ext` is owned by `ParseExtTarget` (specs/at-ext-parsing.md). Future outer tags (e.g. `@priority`) define their own parser and call site. The default for an outer tag with no registered handler is no embedded handling — the value is opaque text.
- **R2112:** Helpers that split a compound value name the *owner-tag-specific* semantics in their identifier — never a generic name like `splitCompoundTags`. This rule keeps future readers from inferring a single shared mechanism that doesn't exist.

## Feature: ark tag inspect (observability)
**Source:** specs/tag-inspect.md

- **R2113:** `ark tag inspect [--scope SCOPE] [--target PATH] [--json]` is a read-only observability subcommand. It never mutates the index or in-memory state. It is a sibling of `ark tag verify`; verify validates and repairs, inspect reveals.
- **R2114:** `--scope ext` (the default and only v1 scope) dumps three sections: on-disk @ext state (X records, V[ext] records, F[chunkid][ext] records); in-memory ExtMap state (every map ExtMap holds); bridges (per-tvid_ext consolidated view linking on-disk and in-memory entries with decoded tag/value/path).
- **R2115:** `--target PATH` filters output to one file: X records whose target_chunkid is in PATH's chunkid set, V[ext] entries whose source chunk is in PATH's chunkid set, and ExtMap entries that reference PATH's fileid. Absence of `--target` means dump everything.
- **R2116:** `--json` emits a machine-readable shape with the same three sections. Default output is plain text grouped by section, suitable for terminal reading.
- **R2117:** `inspect` is server-aware. When `ark serve` is running, the CLI proxies via `POST /tags/inspect` so the in-memory ExtMap section reflects the live server's reconstructed state. When the server is stopped, the CLI opens the index read-only and emits only the on-disk sections plus a note that in-memory state is unavailable.
- **R2118:** Inspect is linear in X records, V[ext] records, and F[chunkid][ext] records. Not on any hot path.
- **R2119:** Dropping the temporary `cmd/extdiag` diagnostic is part of inspect landing — its functionality is fully subsumed by `ark tag inspect --scope ext`.

## Feature: ext-routed targets visible to tag queries
**Source:** specs/at-ext-storage.md

- **R2120:** `Store.TagFiles(tags)` and `Store.TagValueChunks(tag, value)` union four legs: F records (inline source-chunk), `TmpTagStore` (overlay-direct), `ExtMap.ExtTagFiles` / `ExtMap.ExtTagValueChunks` (persistent ext-routed targets), and the same ExtMap accessor for overlay-routed targets. The accessor walks one set of in-memory maps and emits chunkids for both persistence kinds in a single pass. The four legs union without coordination — chunkids do not collide across sources.
- **R2121:** ExtMap maintains `routedTagsByTvidExt[tvid_ext] → []TagValue` — the routed (tag, value) pairs each tvid_ext contributes. The cache eliminates the need for tag-query callers to read X records or re-resolve routed_tvids on the hot path.
- **R2122:** `Rebuild` populates `routedTagsByTvidExt` while scanning X records: for each X record's routed_tvids list, decode each tvid via `TvidMap.Resolve` to (tag, value) and accumulate into the map under the tvid_ext key. Multiple X records sharing the same tvid_ext write the same routed list (routed-tags are a property of the tvid_ext, not the target_chunkid), so later writes are idempotent.
- **R2123:** `applyIndexExt` and `applyReresolve` keep `routedTagsByTvidExt` current alongside the other maps — Adds populate the entry, the Empty-new-set branch drops it. `CleanupSource` drops the entry when the tvid_ext is evicted from the other ExtMap maps.
- **R2124:** `ExtMap.ExtTagFiles(tags []string)` returns `[]TagFileRecord` for every (tvid_ext, target_chunkid) pair where the cached routed-tag set intersects the requested tags. `ExtMap.ExtTagValueChunks(tag, value string)` returns `[]uint64` of target chunkids when `routedTagsByTvidExt[tvid_ext]` contains a matching (tag, value) pair. Both accessors walk persistent and overlay routings in a single pass under one RLock — they replace the historical `OverlayTagFiles` / `OverlayTagValueFiles` pair which only saw overlay routings.

## Feature: auto_compact in ark.toml
**Source:** specs/serve-compact.md, specs/cli-commands.md

- **R2125:** `ark.toml` accepts a top-level `auto_compact = true|false` boolean. When set to `true`, `ark serve` runs the compaction step (R2981) on startup as if `-compact` had been passed.
- **R2126:** When the user supplies `-compact` (or `-compact=false`) on the `ark serve` command line, the flag value wins regardless of `auto_compact` in ark.toml. The CLI distinguishes "flag supplied" from "flag absent at default" via `flag.FlagSet.Visit` after `Parse`.
- **R2127:** When `-compact` is not supplied and `auto_compact` is absent from ark.toml, the default is `false` — preserving the historical opt-in compaction behaviour.

## Feature: pattern anchoring forms and source-shadow validation
**Source:** specs/main.md, specs/source-monitoring.md

- **R2133:** Patterns have three anchoring forms by leading slashes. **No leading slash** matches at any depth within the source directory (equivalent to prepending `**/`). **Single leading slash** (`/path`) is filesystem-absolute — matched against the file's absolute path on disk. **Leading `./`** is source-root-anchored — after stripping `./`, the remainder is matched against the source-relative path. Replaces R21 (Retired T48). The `./` form replaces the prior interpretation where `/path` meant source-anchored.
- **~~R2134:~~** (Retired T50 — no replacement) A filesystem-absolute exclude *shadows* a source when the pattern's literal prefix (the substring up to the first glob character `*`, `?`, `[`, `{`, or `\`) is equal to or an ancestor path of the source's directory. A shadowed source can never have any indexable file.
- **~~R2135:~~** (Retired T51 — no replacement) Shadow detection runs at config-load. On detection, ark refuses to start (or refuses the config-mutation that produced the situation), names the offending pattern and source, and instructs the user to remove one or the other. The user must resolve the contradiction before ark proceeds.
- **~~R2136:~~** (Retired T52 — no replacement) Shadow detection also runs on `ark config add-source`. If adding the new source would make it shadowed by an existing absolute exclude, the add fails before the source is persisted.
- **~~R2137:~~** (Retired T53 — no replacement) Source-root-anchored patterns (`./path` form) and bare patterns cannot shadow a source — they are scoped to a single source's relative paths and never have an "ancestor of source.Dir" relation. Only filesystem-absolute exclude patterns are checked for shadowing.

## Feature: Reconcile sweep step
**Source:** specs/source-monitoring.md

- **R2138:** Reconcile runs `sources-check → sweep → scan → refresh`. The sweep step runs before scan so newly-excluded files are dropped before their dependents are re-evaluated. Replaces R342 (Retired T49).
- **R2139:** Sweep walks every file currently in the index. For each path, sweep determines which configured source claims it (path is under source.Dir) and re-classifies it against the current effective include/exclude patterns of that source. Any file that no longer classifies as `Included` is removed.
- **R2140:** A file whose claiming source has been removed from configuration is also swept — no source claims it, so it cannot be classified `Included`.
- **R2141:** Sweep removal goes through the same removal path as `ark remove`, so all derived state — chunks (via microfts2 refcounting), tag values, and ext routings — is dropped consistently. Sweep does not bypass that path.
- **R2142:** Sweep runs every Reconcile cycle, not only the post-mutation ones. Startup, hand-edits to ark.toml caught by fsnotify, and any other entry into Reconcile produce the same result.

## Feature: default vs per-source patterns (replace semantics)
**Source:** specs/main.md

- **R2143:** The top-level `default_include` and `default_exclude` arrays apply to any source that does not specify its own `include` (resp. `exclude`). Replaces R12 (Retired T54).
- **R2144:** When a source sets `include`, those patterns *replace* `default_include` for that source — no merging. Same for `exclude`. The two patterns are independent: a source may set only `include` (inheriting `default_exclude`) or only `exclude` (inheriting `default_include`). Replaces R13 (Retired T55).
- **R2145:** ark.toml uses TOML keys `default_include` and `default_exclude` at the top level. Per-source `[[source]]` blocks continue to use `include` and `exclude`. Replaces R26 (Retired T56).
- **R2146:** A per-source `include`/`exclude` may be written either as an array (`include = [...]`) for replace form or as a table with key `add` (`include.add = [...]`) for extend form. Replace substitutes the default entirely; extend appends per-source patterns to the default. The two forms are mutually exclusive within one source (TOML enforces this — a key cannot be both array and table). A source that uses neither form inherits the default unchanged.

## Feature: tag-definition embeddings (ED records)
**Source:** specs/tag-def-embeddings.md

- **R2151:** Each tag-definition (D record) has a parallel ED record holding a float32 vector embedding of the definition's description text. One ED per (tag, fileid), keyed `ED` + tagname + fileid:8.
- **R2152:** ED embeds the description text alone — not the tag name. The chunk → tag-name query direction makes the name's lexical surface bias the vector against the description's meaning, so the name is excluded.
- **R2153:** ED uses the same float32 vector format and dimensionality as EV and EC (3072 bytes for nomic-768).
- **R2154:** When a fileid's D records are replaced (`Store.UpdateTagDefs`), the fileid's ED records are dropped in the same transaction. Mirrors D's R505 lifecycle.
- **R2155:** When a fileid's D records are removed (`Store.RemoveTagDefs`), the fileid's ED records are dropped in the same transaction.
- **R2156:** When D records are appended (`Store.AppendTagDefs`), no ED writes occur synchronously — newly added (tag, fileid) pairs are picked up by the next batch-embed pass via `Store.MissingTagDefEmbeddings`. Mirrors D's R511 append path.
- **R2157:** `Store.MissingTagDefEmbeddings` returns `(tag, fileid)` pairs that have a D record but no ED record. Used by the post-reconcile batch-embed pass.
- **R2158:** The Librarian's batch-embed pass (the same pass that writes T-name and EV vectors) embeds each missing description via `EmbedQuery` and writes ED via `Store.WriteTagDefEmbedding(tag, fileid, vec)`.
- **R2159:** `Store.WriteTagDefEmbedding(tag, fileid, vec)` writes one ED record. `Store.ReadTagDefEmbedding(tag, fileid)` reads one back; `(nil, nil)` if absent.
- **R2160:** `Store.DropEmbeddings` deletes all ED records alongside dropping T-name vectors and EV records. ED is gated by `[embedding] model` — a model swap drops T-name, EV, and ED together. No separate ED schema marker.
- **R2161:** (inferred) ED records are rebuilt from scratch by `ark rebuild`, same as T/F/V/D records.
- **R2162:** `ark status -db` prefix listing includes ED alongside T/F/V/D/EV/EC/EF.

## Feature: tag-name suggestion (chunk → tag-name candidates)
**Source:** specs/suggest-tag-names.md

- **R2163:** `Librarian.SuggestTagNames(chunkID, k)` returns up to `k` tag names whose ED vectors are nearest to the chunk's EC vector, ranked by cosine similarity. Lives on Librarian (not DB) — vector queries belong to the embedding layer; HTTP callers reach it via `srv.librarian` like `SearchChunks` and `EmbedSimilarTagValues`.
- **R2164:** Implementation reads `EC[chunkID]`, walks the ED prefix once, computes cosine per record, and resolves fileid → path via a single `db.fts.FileIDPaths()` call. No re-entrant index writes.
- **R2165:** Per-tag aggregation uses **max** across that tag's ED records — the tag's score is the score of the best-matching definition file. Averaging is rejected: it dilutes a sharp single-file match with weaker definitions in other files.
- **R2166:** `TagSuggestion` carries `Tag`, aggregate `Score`, and `MotivatingFiles []TagSuggestionRef` ranked by per-file score descending. `TagSuggestionRef` carries `FileID`, `Path`, and that file's `Score`.
- **R2167:** `MotivatingFiles[].Path` is resolved via `db.FTS().FileIDPaths()` — one map lookup per call, not N point reads. A fileid with no path entry leaves Path empty rather than failing the call.
- **R2168:** `k <= 0` returns `(nil, nil)`. Not an error.
- **R2169:** Chunk has no EC record → return `(nil, nil)`. Not an error: chunks embed lazily, the UI may call before the chunk has been processed.
- **R2170:** Embedding unavailable (no `[embedding] model` configured, model file missing) → return `(nil, nil)`. The UI degrades gracefully to manual tag entry.
- **R2171:** ED prefix empty (no tag defs indexed yet) → return `(nil, nil)`.
- **R2172:** A single ED record with vector dimension mismatched against the chunk's EC vector is skipped, not surfaced as an error. Mirrors `SearchChunksMulti`'s same-dim guard, covering mid-flight model swaps.
- **R2173:** SuggestTagNames is read-only. No writes to the index, no model invocation, no agent call, no spectral expansion.

## Feature: vector freshness substrate (S records)
**Source:** specs/vector-freshness.md

- **R2174:** The vector freshness side index lives under single-byte prefix `S` (`prefixSerial = 'S'`), disjoint from every existing prefix's first byte. Keys: `S + <original-prefix-bytes> + <original-key-tail>`.
- **R2175:** S-record values are varint-encoded uint64 (`binary.PutUvarint` / `binary.Uvarint`).
- **R2176:** Serial values come from a maintained counter stored in I record `I:serial`. `allocSerial(txn)` reads the counter, advances it by 1, writes the new value back, and returns the value used for stamps in that txn. Sourced from the I-record (not from `bbolt.Tx.ID()`) because compaction may reset the transaction id on the destination.
- **R2177:** The serial counter is never reset over the database's lifetime. Preserved across `ark rebuild`, `Store.DropEmbeddings`, model swaps, and compaction because the I-record lives in the active B-tree.
- **R2178:** Original record values (T, EV, ED, EC) are unchanged by stamping. Existing readers (`ReadTagNameEmbedding`, `ReadTagValueEmbedding`, `ReadTagDefEmbedding`, `ReadChunkEmbedding`, scan/walk variants) continue to work without modification.
- **R2179:** `Store.WriteTagNameEmbedding(tag, vec)` stamps `ST<tag>` in the same transaction as the T-record put.
- **R2180:** `Store.WriteTagValueEmbedding(tvid, vec)` stamps `SEV<tvid-varint>` in the same transaction as the EV put.
- **R2181:** `Store.WriteTagDefEmbedding(tag, fileid, vec)` stamps `SED<tag><fileid:8>` in the same transaction as the ED put.
- **R2182:** `Store.WriteChunkEmbedding(chunkID, vec)` stamps `SEC<chunkID-varint>` in the same transaction as the EC put.
- **R2183:** `Store.WriteChunkEmbeddingBatch(chunks)` allocates one serial via `allocSerial` at the top of its callback and stamps every batch record's `SEC<...>` entry with that single allocated serial in the same txn.
- **R2184:** All records stamped within one write transaction share a single serial — "records that moved together carry the same mark." Across transactions, serials are strictly monotonic.
- **R2185:** `Store.DeleteChunkEmbedding(chunkID)` and `Store.DeleteChunkEmbeddingInTxn(txn, chunkID)` drop the matching `SEC<chunkID>` entry in the same txn as the EC delete.
- **R2186:** `Store.UpdateTagDefs`'s existing `delByFileid` loop is extended to drop `SED<tag><fileid>` entries alongside the fileid's old D and ED records.
- **R2187:** `Store.DropEmbeddings` drops every `ST*`, `SEV*`, and `SED*` entry alongside its existing T-name vector strip and EV/ED record drops. `SEC*` entries are not touched, consistent with `DropEmbeddings` not touching EC.
- **R2188:** `Store.RecordSerial(prefix, key) (serial uint64, found bool, err error)` returns the stamped serial of the record at `(prefix + key)`. `found=false` iff no S-entry exists for that (prefix, key).
- **R2189:** `Store.WalkRecordsSinceSerial(prefix []byte, since uint64, fn func(originalKey []byte, serial uint64) error) error` walks the `S<prefix>` side index in key order and calls fn for each entry whose stamped serial is strictly greater than `since`. fn receives the original record's full key (including the original prefix bytes) and the stamped serial.
- **R2190:** A non-nil error returned from `WalkRecordsSinceSerial`'s fn stops iteration and is propagated by the call as its return value.
- **R2191:** (inferred) The substrate does not introduce tombstone serials. Deleted records' S-entries are removed alongside the value; clients reconcile cache rows by lookup-then-delete.
- **R2192:** (inferred) The substrate does not backfill S-entries for records written before it lands. Pre-substrate records gain an S-entry on their next write; clients doing a from-scratch sweep walk the data prefix directly the first time, then switch to `WalkRecordsSinceSerial` for incremental refresh.
- **R2193:** `Store.DropChunkEmbeddings` (rebuild's EC+EF drop) drops every `SEC*` entry alongside the EC delete. EF is not stamped, so no side-index entries are touched on its side.

## Feature: chunks-for-tag (tag → chunk candidates)
**Source:** specs/chunks-for-tag.md

- **R2194:** `Librarian.ChunksForTag(tag, k)` returns up to `k` chunks whose EC vectors are nearest to any of the named tag's ED vectors, ranked by cosine similarity. Lives on Librarian (not DB) — vector queries belong to the embedding layer; HTTP callers reach it via `srv.librarian` like `SuggestTagNames` and `SearchChunks`.
- **R2195:** `Librarian.ChunksForTagDef(tag, fileid, k)` restricts scoring to a single `(tag, fileid)` ED record, useful when reconciling divergent definitions of the same tag across files.
- **R2196:** `ChunksForTag` walks the ED prefix once, collecting every record whose tag matches the requested tag, then performs a single EC walk scoring each chunk against every collected ED vector.
- **R2197:** Per-chunk aggregation in `ChunksForTag` uses **max** across the tag's ED records — the chunk's score is the score of the best-matching definition file. Same rationale as `SuggestTagNames` per-tag aggregation.
- **R2198:** EC records whose dimension does not match an ED query vector's dimension are skipped, mirroring `SearchChunksMulti`'s same-dim guard. Mid-flight model swaps do not surface as errors.
- **R2199:** Top-k selection uses a min-heap of size `k` over chunk aggregate scores. Per-chunk `MotivatingDefs` are retained only for chunks alive in the heap, bounding memory at O(k × |defs|).
- **R2200:** After the EC walk, each surviving chunk's primary `FileID` is resolved via `db.fts.ReadCRecord` inside one shared txn (matches `SearchChunksMulti`). Chunks with no CRecord or empty `FileIDs` are dropped from results, not surfaced as errors.
- **R2201:** Path resolution for both chunk primary fileids and definition-file fileids goes through one `db.FTS().FileIDPaths()` call. A fileid with no path entry leaves `Path` empty rather than failing the call.
- **R2202:** Each result chunk's `MotivatingDefs` is sorted by per-def score descending; chunks are sorted by aggregate score descending.
- **R2203:** `ChunksForTagDef` reads the single `ED[tag, fileid]` record via `Store.ReadTagDefEmbedding`. Missing → return `(nil, nil)`. Not an error.
- **R2204:** `ChunksForTagDef` produces a single-entry `MotivatingDefs` slice per result chunk, holding the requested `(fileid, path, score)`. Same `ChunkSuggestion` shape as `ChunksForTag`.
- **R2205:** `ChunkSuggestion` carries `ChunkID`, `FileID` (chunk's primary), `Path` (for `FileID`), aggregate `Score`, and `MotivatingDefs []DefMatch`.
- **R2206:** `DefMatch` carries `FileID` (the definition file), `Path` (for `FileID`), and `Score` (this def's cosine against the chunk).
- **R2207:** `k <= 0` returns `(nil, nil)`. Not an error.
- **R2208:** Embedding unavailable (no `[embedding] model` configured, model file missing) → return `(nil, nil)`. The UI degrades gracefully.
- **R2209:** `ChunksForTag` — tag has no ED records → return `(nil, nil)`. Not an error.
- **R2210:** `ChunksForTagDef` — `ED[tag, fileid]` absent → return `(nil, nil)`. Not an error.
- **R2211:** EC prefix empty (no chunks embedded yet) → return `(nil, nil)`.
- **R2212:** `ChunksForTag` and `ChunksForTagDef` are read-only. No writes to the index, no model invocation, no agent call, no spectral expansion.
- **R2213:** Neither call gates results by absolute score threshold; `k` is the only cardinality bound. The UI may apply a minimum display threshold.
- **R2214:** Neither call filters chunks already carrying the tag. Orphan-detection policy belongs to the caller (UI) or to the Phase 1E hot-correlations cache.
- **R2215:** (inferred) Neither call maintains a hot-correlations cache. Both are live, on-demand queries each time they are called.

## Feature: hot correlations (HC sweep + tag-tag queries)
**Source:** specs/hot-correlations.md

- **R2216:** `Librarian.SweepHotCorrelations() (*SweepResult, error)` runs the incremental corpus-wide sweep, using S-record serials (Phase 1C) to skip unchanged work. Synchronous; intended to be invoked from a background goroutine. Progress is published through the `tmp://sweep/hot-correlations.md` document.
- **R2217:** `SweepResult` carries `StartedAt`, `CompletedAt`, `DurationMS`, `ChangedEDs`, `ChangedECs`, `TagsRebuilt`, `TagsTouched`, `OrphanTotal`, `FromScratch`. Same numbers also land in the tmp:// progress doc as `@sweep-*` tags.
- **R2218:** `Librarian.TopKChunksForTag(tag, k) ([]ChunkSuggestion, error)` reads the cached top-K chunks for a tag from the HC cache. Sub-millisecond index lookup. Same `ChunkSuggestion` shape as `ChunksForTag` (R2205) — callers can treat the two interchangeably.
- **R2219:** `TopKChunksForTag` filters stale entries at read time using the alibi-stamp pattern: an HC entry is dropped if `RecordSerial(HC, key)` is less than `RecordSerial(EC, chunkid)` *or* less than the maximum `RecordSerial(ED, tag||fileid)` across the tag's defs. Result may be shorter than `k` until the next sweep refreshes it.
- **R2220:** `TopKChunksForTag` returns `(nil, nil)` for: no HC entries for the tag, `k <= 0`, embedding unavailable.
- **R2221:** `Librarian.RelatedTags(tag, k) ([]TagSimilarity, error)` returns up to `k` tags whose ED vectors are nearest to any of the named tag's ED vectors. Per-other-tag aggregate is the max cosine across all (def_a, def_b) pairs. Live; no cache.
- **R2222:** `Librarian.TagPairConflict(tagA, tagB) (TagSimilarity, error)` returns the max-pair cosine between two tags and the `(SrcFileID, DstFileID)` defs that scored it.
- **R2223:** `Librarian.TagDrift(tag) ([]DriftPair, error)` returns pairwise within-tag def cosines, sorted descending. For a tag with `n` defs the result has `n*(n-1)/2` pairs.
- **R2224:** `TagSimilarity` carries `Tag` (empty for `TagPairConflict` since both tags are inputs), `Score`, `SrcFileID`, `DstFileID`, `SrcPath`, `DstPath`.
- **R2225:** `DriftPair` carries `FileIDA`, `FileIDB`, `PathA`, `PathB`, `Score`.
- **R2226:** HC keys use the same variable-tag + fixed-suffix scheme as ED records: `HC<tag-bytes><chunkid:8-bytes-big-endian>`.
- **R2227:** HC values are flat `score:float64` (8 bytes). No version metadata embedded in the value — freshness lives in the S substrate.
- **R2228:** Top-K bound `K_TOP_HC` is fixed at **20** for this slice. Configuration is a future tuning question, not part of 1E.
- **R2229:** HC writes are stamped through the existing S-substrate machinery (Phase 1C). The HC record's own stamp serves as its alibi at freshness check time — `Store.WriteHotCorrelation` (or its equivalent) writes the value and stamps `S<HC<key>>` in the same transaction, on the same path as `WriteChunkEmbedding`, `WriteTagDefEmbedding`, etc. (R2179–R2183).
- **R2230:** `I:hcsweep` is the persistence anchor for the sweep — a uint64 holding the last successful sweep's high-water serial.
- **R2231:** `I:hcsweep` is cleared by `ark rebuild` (full corpus rebuild) and by `Store.DropEmbeddings` (model swap), forcing a from-scratch sweep on the next invocation. `Store.DropEmbeddings` also drops every `HC*` record and its matching `SHC*` stamp, alongside the existing `T*`/`EV*`/`ED*` and `ST*`/`SEV*`/`SED*` drops (R2187).
- **R2232:** Sweep phase 1 — read `last_sweep_serial` from `I:hcsweep`. A zero value indicates from-scratch.
- **R2233:** Sweep phase 2 — survey changed work via `WalkRecordsSinceSerial(prefixEmbedDef, last_sweep_serial, …)` for ED changes and `WalkRecordsSinceSerial(prefixEmbedChunk, last_sweep_serial, …)` for EC changes.
- **R2234:** Sweep phase 3 (tag rebuild) — for each tag with any ED record advancing past the bookmark, recompute the tag's full top-K by walking every EC record. Atomically replace the tag's HC entries (delete previous, write new top-K).
- **R2235:** Sweep phase 4 (chunk displace) — for each EC chunk advancing past the bookmark that wasn't already covered by phase 3, compute cosines vs each ED record, max-aggregate per tag, and displace the lowest-scoring HC entry if exceeded.
- **R2236:** Sweep phase 5 — write `I:hcsweep = max(seen_serial)` only on full success. A mid-sweep error leaves the bookmark unchanged so the next run picks up where this one stopped.
- **R2237:** Sweep phase 6 — update the tmp:// progress doc to `@sweep-status: complete` with final counts (or `@sweep-status: error` with `@sweep-error:`).
- **R2238:** Phases 3 and 4 use per-tag write transactions, not a single transaction across the entire sweep. Reasons: long write transactions block the closure actor; per-tag txns leave the cache in a consistent partially-updated state on crash.
- **R2239:** The sweep is idempotent per `(tag, chunkid)`: rerunning the same work produces the same HC contents.
- **R2240:** Progress is surfaced through `tmp://sweep/hot-correlations.md` — a single-chunk tmp:// document rewritten in place on each progress tick.
- **R2241:** Progress doc tags: `@sweep`, `@sweep-status` (`idle` | `running` | `complete` | `error`), `@sweep-started`, `@sweep-progress` (fraction in `[0, 1]`), `@sweep-phase` (`tag-rebuild` | `chunk-displace` | `done`), `@sweep-changed-eds`, `@sweep-changed-ecs`, `@sweep-tags-rebuilt`, `@sweep-tags-touched`, `@sweep-orphan-total`, `@sweep-eta-seconds`, `@sweep-duration-ms`, `@sweep-completed`, `@sweep-error` (present only when status = error).
- **R2242:** Progress-doc throttling — the sweep updates the doc at most every **250 ms**. Within a 250 ms window, progress counters accumulate in memory; the next flush at the window boundary writes them.
- **R2243:** Terminal-state transitions (`running → complete`, `running → error`) flush the progress doc immediately, even mid-throttle-window. The terminal state is never delayed.
- **R2244:** Progress-doc lifecycle: created at server startup with `@sweep-status: idle` and no progress fields. On sweep start, status flips to `running`, `@sweep-started` is set, all progress fields are zeroed. On terminal transition, status flips, `@sweep-completed` is set, `@sweep-error` is set if applicable. The doc stays in its terminal state until the next sweep starts.
- **R2245:** UI subscribers reach the progress doc through existing tag-subscription plumbing (`mcp:subscribe`). No new event channel is introduced.
- **R2246:** A Lua API `mcp.sweepHotCorrelations()` is a thin wrapper that invokes the Librarian method on the server.
- **R2247:** A CLI subcommand `ark sweep correlations` is a thin wrapper that proxies to the running server (matches the pattern of other long-running ops).
- **R2248:** (inferred — scope boundary) Cron-via-tag triggering is **deferred** to a follow-up slice. The 1E engine knows nothing about the scheduler; an external subscriber will invoke `SweepHotCorrelations()` when cron-via-tag wiring lands.
- **R2249:** An HC entry is considered stale if any of: the EC record at `chunkid` is missing; `RecordSerial(EC, chunkid)` is greater than `RecordSerial(HC, key)`; or any `RecordSerial(ED, tag||fileid)` across the tag's defs is greater than `RecordSerial(HC, key)`. The HC record's own stamp is the comparison reference — there is no per-entry version metadata to track. The check is a single index lookup per axis (HC, EC, and one per def of the tag).
- **R2250:** The read-time staleness filter (R2219, R2249) is the only invalidation path during normal operation. No active sweep on EC delete is performed.
- **R2251:** The next sweep's tag-rebuild (phase 3) and chunk-displace (phase 4) passes naturally replace stale entries with current ones; no separate cleanup pass is needed.
- **R2252:** `SweepHotCorrelations`, `TopKChunksForTag`, `RelatedTags`, `TagPairConflict`, and `TagDrift` do not invoke a search agent and do not call the embedding model. They operate purely on vectors and serials already in the index.
- **R2253:** Hot correlations does not filter chunks already carrying the tag at the storage layer. Same decision as `ChunksForTag` (R2214): orphan-detection policy is caller-side. The curation view applies the filter when rendering.
- **R2254:** EC and ED writes do not actively invalidate HC entries. The substrate's monotonic stamping plus the read-time alibi-stamp filter handle staleness without coupling the write path to HC.
- **R2255:** (deferred — scope boundary) Persistent completion history (e.g. `~/.ark/sweep/correlations-history.md`) is **not** written by 1E. The tmp:// progress doc holds in-memory state only and vanishes on server restart.
- **R2256:** Sweep does not auto-trigger on indexer activity, file changes, model loads, or any other corpus event. It runs on explicit invocation only.
- **R2257:** No in-memory mirror of HC is maintained. All reads go through the index. The "in-memory tail of recent S records" question (1C deferred decision) becomes actionable through profiling 1E against real workloads, not in this slice.

## Feature: curation Lua bridge (mcp:* methods for 1B/1D/1E reads + sweep)
**Source:** specs/suggest-tag-names.md, specs/chunks-for-tag.md, specs/hot-correlations.md

- **R2258:** `mcp.suggestTagNames(chunkID, k)` is registered on the Lua MCP table as a thin wrapper over `Librarian.SuggestTagNames`. Surfaced for the Phase 1F curation view so Lua app code can drive the chunk → tag-candidates entry point without going through HTTP.
- **R2259:** `mcp.chunksForTag(tag, k)` is registered as a thin wrapper over `Librarian.ChunksForTag`. Same `ChunkSuggestion` shape as `mcp.topKChunksForTag` so Lua callers can swap cached and live forms.
- **R2260:** `mcp.chunksForTagDef(tag, fileID, k)` is registered as a thin wrapper over `Librarian.ChunksForTagDef`. Returns a `ChunkSuggestion` array whose per-entry `motivatingDefs` has length 1 (the requested definition file).
- **R2261:** `mcp.topKChunksForTag(tag, k)` is registered as a thin wrapper over `Librarian.TopKChunksForTag`. The HC-cached read path with the alibi-stamp staleness filter (R2218, R2219) applied inside the Go layer.
- **R2262:** `mcp.relatedTags(tag, k)` is registered as a thin wrapper over `Librarian.RelatedTags`. Returns up to `k` `TagSimilarity` records ranked by cosine across the tag's ED↔ED neighbourhood.
- **R2263:** `mcp.tagPairConflict(tagA, tagB)` is registered as a thin wrapper over `Librarian.TagPairConflict`. Returns the single max-pair `TagSimilarity` between two tags' ED records.
- **R2264:** `mcp.tagDrift(tag)` is registered as a thin wrapper over `Librarian.TagDrift`. Returns the within-tag pairwise `DriftPair` array sorted by score descending.
- **R2265:** `mcp.sweepHotCorrelations()` is registered as a thin wrapper that triggers the corpus-wide sweep through the same write-goroutine path used by HTTP `POST /sweep/correlations`. Returns the `SweepResult` summary on completion.
- **R2266:** Result-table field names are lowerCamelCase mirrors of the Go struct field names (`Tag` → `tag`, `MotivatingFiles` → `motivatingFiles`, `FileID` → `fileID`, `MotivatingDefs` → `motivatingDefs`, `DurationMS` → `durationMs`, etc.). Matches the convention established by `mcp:inbox()`.
- **R2267:** `chunkID` and `fileID` arguments and result fields cross the Lua boundary as Lua numbers. Current corpus IDs (~48K chunks, ~1k file IDs) fit within IEEE-754 double precision (2^53). String-encoded fallback is deferred until corpus growth warrants it.
- **R2268:** When the underlying Go call returns `(nil, nil)` (empty result, missing inputs, embedding unavailable, k ≤ 0, etc.), the Lua wrapper pushes an empty Lua table (`{}`) instead of Lua nil. Matches `mcp:inbox()`; lets Lua callers iterate without nil-guarding.
- **R2269:** When the underlying Go call returns a non-nil error, the Lua wrapper returns `(nil, errstring)` — the standard gopher-lua two-return convention used by `mcp:inbox()`, `mcp:open()`, and other existing bridge methods.
- **R2270:** The seven read wrappers (R2258–R2264) acquire only a read txn through `Sync(srv.db, ...)`. They do not enqueue work in the write goroutine and do not block other writers. `mcp.sweepHotCorrelations()` (R2265) is the one writer in the set; it routes through the same `enqueueWrite` path that `POST /sweep/correlations` uses (R2240 etc.), so the actor invariant holds.

## Feature: tmp:// subscription primitive
**Source:** specs/tmp-subscription.md

- **R2276:** Pub/sub is the primary integration plane between ark and any consumer that observes corpus state — UIs, tests, monitors, and agents are symmetric subscribers. They use the same Go API and the same Lua API; no test-only seams are introduced.
- **R2277:** The Go API on `*PubSub` is the canonical contract; the Lua bridge is a thin wrapper that delegates to it. Tests and the Ark monitor exercise the Go API directly.
- **R2278:** tmp:// paths are first-class citizens of the existing `TagSub.FilterFiles` / `TagSub.ExcludeFiles` doublestar glob matcher. No new struct, no new field, no special-case branch — `matchFileFilters` (pubsub.go:463) handles `tmp://...` paths through the same code path as persistent paths.
- **R2279:** tmp:// publish for HTTP-driven writes is already wired: `handleTmpAdd` (server.go:1568), `handleTmpUpdate` (server.go:1614), and `handleTmpAppend` (server.go:1673) each call `srv.pubsub.PublishAndWatch("", path, ExtractTagValues(content, strategy))` after their `SyncVoid` write commits. This requirement records the existing baseline that the slice extends rather than introduces.
- **R2280:** Several internal callers of the DB-layer tmp:// write methods skip publishing today and therefore write tmp:// docs that no subscriber sees: `librarian.go:1374` and `librarian.go:1378` (sweep progress doc), `pubsub.go:85` (Watchdog tmp:// findings), `server.go:292` (missed-events), `server.go:563-565` (indexer-side reset), `server.go:3205` (Lua `tmp_add` bridge). These are bugs to close, not missing features.
- **R2281:** Publishing is centralized inside the DB-layer methods. `db.AddTmpFile`, `db.UpdateTmpFile`, and `db.AppendTmpFile` each, after their actor write commits, extract tags from the resulting content via `ExtractTagValues` and publish through pubsub. Every internal caller becomes correct-by-construction.
- **R2282:** The three HTTP handlers (`handleTmpAdd`, `handleTmpUpdate`, `handleTmpAppend`) stop calling `PublishAndWatch` manually; the centralized DB-layer path handles publishing for them.
- **R2283:** PubSub maintains an in-memory `map[path] → map[tag]value` cache of the last-published tag-set per tmp:// path. The cache is updated after each successful publish.
- **R2284:** Each centralized publish diffs the new tag-set against the cached prior set and publishes only `(tag, value)` pairs whose value differs from the cached value (or whose tag is new). This is the only-on-change policy: a sweep that rewrites `tmp://sweep/hot-correlations.md` every 250 ms with unchanged `@sweep-status` and incrementing `@sweep-progress` produces one event per progress increment, not a flood for every tag in the doc.
- **R2285:** `db.AddTmpFile` treats the prior tag-set as empty — every present tag publishes on the first write to a new path.
- **R2286:** `db.AppendTmpFile` extracts tags from the **whole resulting content** (existing + appended) before diffing, so tags already published from prior content don't re-fire on each append.
- **R2287:** The tag-set cache entry for a path is cleared when `RemoveTmpFile` is called for that path, and on server restart (matches the tmp:// lifecycle — no persistence across restart).
- **R2288:** A new Lua method `mcp.subscribe(sessionID, filter)` registers a subscription for the named session. `sessionID` is a string the app picks (default convention: app or view name like `"curation"`); apps may use finer granularity (per-request, per-panel) as bookkeeping suits.
- **R2289:** The `filter` argument is a Lua table whose fields mirror `TagSub` one-to-one with lowerCamelCase naming: `tag` (required string), `valueRE` (optional regex string), `filterFiles` (optional array of glob strings), `excludeFiles` (optional array of glob strings). Missing/nil optional fields map to Go zero-values that mean "match any."
- **R2290:** `mcp.subscribe` is **replace-by-(session, tag)**: calling it for an existing `(session, tag)` pair drops any prior sub on that tag for that session, then adds the new sub. The Lua bridge implements this as `PubSub.Cancel(session, tag, "")` followed by `PubSub.Subscribe(session, [newSub])`. The Go `PubSub.Subscribe` API keeps its append semantics; the constraint is a Lua-bridge convention.
- **R2291:** A new Lua method `mcp.onpublish(sessionID, callback)` registers the per-session callback. **One callback per session**; re-registering replaces the prior callback.
- **R2292:** A new Lua method `mcp.cancel(sessionID, tag)` drops the subscription on that tag for that session. `mcp.cancel(sessionID, "")` drops all subscriptions for the session and stops the session's listening goroutine.
- **R2293:** All three Lua bridge methods take the session as the first argument explicitly, mirroring Go's `PubSub.Subscribe(sessionID, ...)`, `Listen(sessionID, ...)`, and `Cancel(sessionID, ...)`. Cross-session admin use (e.g. the Ark monitor watching `"otherapp"`) is supported by passing a different session string.
- **R2294:** For each session that has at least one subscription, the server runs one listening goroutine that drains `PubSub.Listen(sessionID, timeout)` in a loop.
- **R2295:** For each batch returned by `Listen`, the listening goroutine compresses the batch by `(path, tag)` — for each unique pair, only the latest event survives — then makes **one** `srv.uiRuntime.WithLua(...)` call passing the compressed array of event tables. One Lua-VM hop per batch, not per event.
- **R2296:** Compression operates on `[]Event` (Go structs); Lua event tables are constructed only for events that survive compression, inside the `WithLua` closure. Discarded events never allocate Lua tables.
- **R2297:** The Lua callback registered via `mcp.onpublish` receives one argument: a 1-indexed Lua array of event tables. Each event table mirrors the Go `Event` struct field-for-field with lowerCamelCase naming (R2266 convention).
- **R2298:** Future fields added to the Go `Event` struct surface in Lua automatically; the bridge builds the event table from the struct's field set, not from a hard-coded list.
- **R2299:** The listening goroutine for a session starts on the first `mcp.subscribe(session, ...)` call for that session.
- **R2300:** When the session's subscription count reaches zero — via `mcp.cancel(session, tag)` removing the last sub, or `mcp.cancel(session, "")` — the listening goroutine stops and the session's pubsub state (`ps.queues[sessionID]`, `ps.subs[sessionID]`, `ps.lastListen[sessionID]`) is cleaned up via the existing `Cancel` empty-tag path.
- **R2301:** Server shutdown stops all listening goroutines cleanly through the existing flib shutdown path.
- **R2302:** If the Lua VM is busy and a `WithLua` call queues, events accumulate in the per-session pubsub channel; overflow increments `sub.Drops` per existing logic. No new flow control is introduced.
- **R2303:** `PubSub.QueueDepth(sessionID) int` returns the current event-queue length for a session, computed as `len(ps.queues[sessionID])` under the existing lock. Used by the Ark monitor to surface "events queued behind a slow subscriber" without polling.
- **R2304:** `PubSub.LastListenAt(sessionID) time.Time` returns the timestamp of the session's most recent `Listen` drain, read from `ps.lastListen[sessionID]` under the existing lock. Used by the monitor to flag stalled subscribers.
- **R2305:** For "what changed since I connected," the monitor uses `Store.RecordSerial` and `Store.WalkRecordsSinceSerial` (R2174–R2193). The monitor pulls the current serial at startup as its baseline and filters older changes by stamp comparison. No new state is introduced by this slice for "since" semantics.
- **R2306:** (scope boundary) No event-history buffer is introduced. Subscribers see live events only; current state lives in the tmp:// doc's tags at subscribe time. A subscriber connecting after a terminal transition reads the doc body to learn the current state.
- **R2307:** (scope boundary) The HTTP `POST /subscribe` and `ark subscribe` CLI surfaces are unchanged. Whether they route through the new centralized DB-layer publish path is decided implicitly by R2281 (they do, because they call the centralized DB methods); the surface contract is unchanged.
- **R2308:** (scope boundary) No handle-based cancellation is exposed from Lua. Re-subscribing is the cancel via the replace-by-(session, tag) rule.
- **R2309:** (scope boundary) The Go `PubSub.Subscribe` and `PubSub.Cancel` APIs are unchanged. Go callers retain append-semantics subscribe and value-pattern cancel. The tighter Lua conventions are layered on top.
- **R2310:** (scope boundary) Compression is intra-batch only. Events for the same `(path, tag)` that arrive in successive `Listen` batches are not coalesced.
- **R2311:** (scope boundary) Subscribe-before-doc-exists is valid. Subscribing to a tmp:// path that hasn't been created yet registers the subscription; events fire when the first `AddTmpFile` publishes.
- **R2312:** The test-as-subscriber pattern is the canonical testing approach. Tests subscribe through the same Go API (`PubSub.Subscribe` + `Listen`) that the Lua bridge uses; no mocks or test-only seams are introduced. Substrate-correctness bugs (e.g. an internal caller that fails to publish) are caught by tests that exercise the real substrate.

## Feature: find connections (1G — pinned-chunks → proposals)
**Source:** specs/find-connections.md

- **R2313:** Find Connections is the pinned-chunks → connection-proposals action inside the curation view. The sidecar agent's output is written as a tmp:// document; the curation UI subscribes to the document's tag transitions via the Subtask 0 substrate. No HTTP endpoint is called from the browser, no custom web component is added, and no JS island is introduced.
- **R2314:** A new sidecar agent `ark-connections` (`.claude/agents/ark-connections.md` + `connections-guard.sh`) runs Haiku in a lotto-tube loop, mirroring the `ark-expansion` hermetic-seal pattern. The guard script permits only `~/.ark/ark` commands.
- **R2315:** `ark connections --wait` blocks until requests are queued and returns a JSON array of `{id, chunkIDs, timeoutSeconds}` entries. The lotto-tube cadence mirrors `ark search expand --wait`.
- **R2316:** `ark connections --fetch ID` returns a JSON array of `{chunkID, fileID, path, content}` for every chunk in the request. `--fetch` is the only chunk-content read path the sidecar uses; the guard script rejects everything else.
- **R2317:** `ark connections --result ID` reads a JSON payload `{themes:[...], sharedTags:[...]}` from stdin, validates that every entry carries a non-empty `Evidence` array, and writes the corresponding body to the tmp:// doc while flipping `@connections-status` to `completed`. An entry with empty evidence is a protocol violation: the server rejects the payload and flips status to `errored` instead.
- **R2318:** `ark connections --error ID="message"` flips `@connections-status` to `errored` and sets `@connections-error` to the message.
- **R2319:** `Librarian.FindConnections(chunkIDs []uint64, opts FindConnectionsOpts) (string, error)` is the orchestrator entry point. It allocates a request ID, creates the tmp:// doc through the write actor, enqueues the request on a new lotto-tube queue, and returns the ID immediately. The call is non-blocking on the sidecar's side.
- **R2320:** `Librarian.ConnectionsAvailable() bool` reports whether `ark connections --wait` has been observed within the availability window. The bridge returns `(nil, "agent unavailable")` when false. The window mirrors `Librarian.Available()` (spectral expand).
- **R2321:** `Librarian.QueueConnectionsRequest`, `DrainPendingConnections`, `WaitForConnectionsRequest`, `SetConnectionsResult`, `SetConnectionsError`, and `CleanConnectionsResults` mirror the spectral-expand queue shape (`QueueExpand`/`DrainPending`/`WaitForRequest`/`SetResult`/`CleanResults`). The two queues are independent but share the lotto-tube pattern.
- **R2322:** A new Lua bridge method `mcp.findConnections(chunkIDs, opts)` enqueues a Find Connections request and returns the request ID string immediately. The bridge call is sub-millisecond and never blocks the Lua VM.
- **R2323:** `mcp.findConnections` accepts an optional `opts` table with `timeoutSeconds` (number). The orchestrator clamps `timeoutSeconds` to the range `[5, 300]`; missing/nil/zero defaults to 60.
- **R2324:** `mcp.findConnections` returns `(nil, errstring)` when `Librarian.ConnectionsAvailable()` is false (`"agent unavailable"`) or `chunkIDs` is empty (`"chunkIDs empty"`). Unknown chunk IDs are not checked at enqueue time; the sidecar's `--fetch` step surfaces them as a terminal `errored` status with `@connections-error: unknown chunk <id>`.
- **R2325:** Lua bridge field names follow the project's lowerCamelCase convention (R2266 et al.); chunk IDs cross the boundary as Lua numbers and Go `uint64`.
- **R2326:** The result document path is `tmp://connections/<request-id>.md`. The path is unique per request and is created when the request is enqueued.
- **R2327:** On enqueue, the tmp:// doc carries `@connections-status: pending`, `@connections-request-id: <id>`, `@connections-pinned-chunks: <comma-separated chunk IDs>`, `@connections-started: <RFC3339>`, and `@connections-progress: fetching`. `@connections-elapsed: 0` is also set.
- **R2328:** While the sidecar is running, the orchestrator (or the sidecar via `--result`/`--error`) advances `@connections-status` through `working` → `completed` or `errored` and `@connections-progress` through `fetching` → `thinking` → `posting` → `done`. `@connections-elapsed` updates every ~5 seconds; the throttle is 5 s wall-clock between non-terminal updates.
- **R2329:** Terminal transitions (`completed` or `errored`) flush the tmp:// doc immediately, bypassing the elapsed-tick throttle. `@connections-completed` (RFC3339) is set on either terminal transition; `@connections-error` is set only on `errored`.
- **R2330:** On successful completion, the tmp:// doc body carries `## Themes` and `## Shared Tag Candidates` sections. Each theme is `- @theme-evidence: <chunk-ids>\n  <text>`. Each shared-tag candidate is `- @shared-tag: <tag>\n  @shared-tag-value: <value>\n  @shared-tag-evidence: <chunk-ids>`. Tag lines use comma-separated chunk-ID lists.
- **R2331:** The orchestrator schedules a server-side timeout for each request equal to the clamped `timeoutSeconds`. On fire, the orchestrator flips `@connections-status` to `errored` with `@connections-error: timeout`. A late `--result` from the sidecar after timeout is logged and discarded; the doc state is not modified.
- **R2332:** The tmp:// doc is created on enqueue and persists until server restart (standard tmp:// lifecycle). Active requests are not retried across server restarts; subscribers observing an in-flight request that vanishes treat the subscription as invalidated and fall back to a fresh button click.
- **R2333:** Every mutation of the tmp:// connections doc — initial create, intermediate update, terminal write — routes through the write actor via `db.AddTmpFile` / `db.UpdateTmpFile`. The centralized publish path (R2281) fans out tag changes to subscribers automatically; no orchestrator code calls `PublishAndWatch` directly.
- **R3164:** The three terminal transitions of a connections request — error (`SetConnectionsError`), sidecar result (`SetConnectionsResult`), and substrate result (`SetSubstrateResult`) — flip the record's `Done` flag and write the terminal tmp:// doc **inside a single write-actor closure** (via the shared `finalizeConnectionsDoc` helper), after stopping the request's timeout timer and elapsed-ticker. Because the write actor serializes closures (R1067), the check-and-set of `Done` is atomic without an extra lock: the first terminal write to run wins and later ones are discarded (this is the mechanism realizing R2331's late-`--result` discard). And because `Done` flips only inside the in-flight write closure, a caller polling the record and observing `Done` is guaranteed the terminal write is durable before `WaitWritesIdle` can return — closing the observe-before-durable race where a poller could see `Done` and tear the fts down while the terminal write was still queued (a nil-overlay panic).
- **R2334:** The result content shape on the Go side is `ConnectionsResult{ Themes []Theme; SharedTags []SharedTagCand }`. `Theme` carries `Text string` and `Evidence []uint64`. `SharedTagCand` carries `Tag string`, `Value string`, and `Evidence []uint64`. Field names mirror the JSON keys the sidecar posts via `--result`.
- **R2335:** (scope boundary) `@ext:` routings and per-chunk tag triples are out of scope for 1G; the richer `ProposeExtRoutings` shape is deferred to Phase 2B.
- **R2336:** (scope boundary) No embedding or retrieval is performed by 1G — themes and shared-tag candidates come from agent inference over the pinned chunks' content. Trigram-zip and reworded-fuzzy retrieval are deferred to Phase 2B.
- **R2337:** (scope boundary) Accept on a proposal does not apply tags directly from the orchestrator; UI uses the existing `Ark:applyTagToChunks` path. The orchestrator's responsibility ends at writing the proposal doc.
- **R2338:** (scope boundary) Pinning chunks does not auto-trigger a Find Connections request. The bridge call is the only entry point in 1G.
- **R2339:** Test: enqueueing a request creates `tmp://connections/<id>.md` with the expected header tags; simulating a `--result` post drives the doc to `completed` with the expected body. The tests subscribe through `PubSub.Subscribe` + `Listen` (the test-as-subscriber pattern from R2312), no mocks.
- **R2340:** Test: a request whose sidecar never posts a result flips to `errored` with `@connections-error: timeout` after the configured timeout.
- **R2341:** Test: `ark connections --fetch` returns the correct chunk content for valid chunk IDs and a non-zero exit with a clear error for unknown chunk IDs.
- **R2342:** Test: `ark connections --result` rejects payloads where any theme or shared-tag entry has empty evidence; the rejection drives `@connections-status` to `errored` with a protocol-violation message.
- **R2343:** Test: `mcp.findConnections` is sub-millisecond, returns the request ID string for valid input, and returns `(nil, "agent unavailable")` when no `--wait` consumer has been observed inside the availability window.

## Feature: Curation Workshop (Go-owned state)
**Source:** specs/curation.md

- **R2355:** The curation workshop's pinned-chunks state lives on the Server as a Go `Curation` struct (`Pinned []PinnedChunk`). Mutations serialize through the Lua executor goroutine via `flib.Runtime.WithLua`, so concurrent callers don't need separate locking beyond the Curation's own mutex.
- **R2356:** Server registers a global Lua table `sys` during `registerLuaFunctions`. `sys.curation` is the Lua-side view of the Go `Curation` struct.
- **R2357:** `sys.curation.pinned` is a Lua-side mirror of the Go-side `Curation.pinned` slice. The mirror is rebuilt inside the same Lua-executor closure that mutates the Go slice, so Frictionless's variable-change detection observes a single atomic transition per mutation.
- **R2358:** `sys.curation.pin(chunkID, fileID, path)` adds or moves-to-top a pinned chunk. Always-add never-flip — a re-pin moves the existing entry to the top of the list, preserving FileID/Path when the new call passes them as zero/empty.
- **R2359:** `PinnedChunk` fields: `ChunkID` (uint64), `FileID` (uint64), `Path` (string), `PinnedAt` (Unix seconds, int64). Lua mirror uses lowerCamelCase field names: `chunkID`, `fileID`, `path`, `pinnedAt`.
- **R2360:** `sys.curation.dismiss(chunkID)` removes the entry whose `ChunkID` matches, if any; a silent no-op when the chunkID is not pinned. Mutation runs in the Lua executor and refreshes the Lua mirror in the same tick.
- **R2361:** `sys.curation.sweepOlder()` drops every pinned entry except the topmost; a silent no-op when zero or one entry is pinned. Mutation runs in the Lua executor and refreshes the Lua mirror in the same tick.
- **R2362:** `refreshLuaTable` preserves Lua identity of entry sub-tables across refreshes: the Curation caches the entry `*lua.LTable` per `ChunkID`, mutates each cached table's fields in place on refresh, allocates a new table only for newly pinned ChunkIDs, and drops cached entries for ChunkIDs that left the slice. This keeps Frictionless's `view.baseItem == item` reuse rule satisfied so per-pin presenters survive mutation events.
- **R2363:** `POST /curation/pin` accepts JSON `{chunkID, fileID, path}` and runs the equivalent of `sys.curation.pin` inside `srv.uiRuntime.WithLua`. `chunkID` is required; `fileID == 0` / `path == ""` preserve existing values on a re-pin. Response: 200 OK on success, 400 Bad Request for malformed JSON or missing `chunkID`, 503 Service Unavailable when the Lua runtime is not wired.
- **R2364:** `mcp.definedTags()` returns an array of `{tag, description}` Lua tables drawn from the same store as the HTTP `POST /tags/defs` handler. Sorted ascending by tag name; duplicate tag entries (multiple D records) are deduplicated keeping the first non-empty description. Empty result is an empty Lua table. Errors follow the `(nil, errstring)` two-return convention.
- **R2381:** Pinned-chunks state persists to `curation.toml` at `filepath.Join(dbPath, "curation.toml")`. The file is co-located with `ark.toml` but is not in `arkSourceIncludePatterns` — the scanner and watcher skip it.
- **R2382:** `curation.toml` format is TOML. Top-level `version = 1` gates future schema changes. Pinned entries are `[[pinned]]` tables with fields `chunkID`, `fileID`, `path`, `pinnedAt` (Unix seconds). Entries are listed in canonical newest-first order, matching the in-memory slice.
- **R2383:** `Curation.Load(path)` runs during `Server.New` after `newCuration()` and before `registerLuaFunctions`. Missing file is a silent no-op (pinned list starts empty). Malformed TOML, unknown version, or unparseable entries log the error, leave the slice empty, and the server continues running; the next mutation's save overwrites the broken file.
- **R2384:** Save runs after every `pin`, `dismiss`, and `sweepOlder` call, inside the same `WithLua` closure that mutates the Go slice and refreshes the Lua mirror. The save is atomic: write to `curation.toml.tmp` then rename over `curation.toml`.
- **R2385:** Save failures (disk full, permission denied) log the error and retain the in-memory state. The next mutation's save retries. If the server crashes between a successful mutation and the next save, that single mutation's worth of state is lost; in-memory state is the source of truth during a session and the file is a checkpoint.

## Feature: Curation Workshop Primitives
**Source:** specs/curation-workshop-primitives.md

- **R2386:** `mcp.chunkInfo(chunkID)` returns a Lua table `{chunkID, fileID, path, range, byteStart, byteEnd, writable, commentSyntax}`. The function runs `Sync` (no DB mutation). Errors follow the gopher-lua `(nil, errstring)` convention; an unknown or overlay chunkid with no file linkage returns `(nil, "chunk not found")`.
- **R2387:** `mcp.chunkInfo`'s `path` is the canonical absolute path microfts2 stores (`info.Names[0]`). `byteStart` and `byteEnd` are the half-open byte range of the chunk in the file. `range` is the chunker-specific range identifier (`Chunk.Range`) verbatim — the same string an `@ext` `:RANGE_STRING` locator would target.
- **R2388:** Chunkers expose writable + comment-syntax metadata via an optional `ChunkerMetadata` interface: `IsWritable() bool` and `CommentSyntax() string`. Chunkers that don't implement it default to `writable=true, commentSyntax=""`. PDFChunker implements the interface returning `false, ""`. Bracket and indent chunkers return `true` and the first `LineComments[]` entry (or `""` when no line-comment is configured). Markdown / line chunkers return `true, ""`.
- **R2389:** `mcp.chunkInfo`'s `writable` field is `false` when the chunker reports `IsWritable() == false` OR the chunk's file path falls under a hardcoded read-only zone. v1's hardcoded zone is `~/.claude/projects/**` (chat-log transcripts treated as conceptually read-only). The workshop UI uses this flag to lock the chunk-text editor and force the ext toggle on.
- **R2390:** `mcp.replaceRegion(path, byteStart, byteEnd, newText)` atomically replaces the byte range `[byteStart, byteEnd)` in `path` with `newText`. The implementation does direct file I/O (matching `mcp.setTags`'s precedent for Lua-driven file mutation); the watcher picks up the change and triggers reindex. Returns `(true, nil)` on success or `(false, errstring)` on failure.
- **R2391:** `mcp.replaceRegion` requires `path` to be an indexed file under one of ark's source roots — tmp:// paths are rejected (they have their own update path via `tmp_update`). `byteEnd >= byteStart` and both must be within file bounds; out-of-range returns an error. Partial writes do not occur — the implementation uses write-to-temp-then-rename atomicity.
- **R2392:** `@ext` mirror files live under `~/.ark/external/<source-slug>/<target-path-within-source>.md`. The source-slug is path-as-slug: every `/` in the absolute source-root path replaced with `-` (`/home/deck/work/ark` → `home-deck-work-ark`). The slug is derived from the source root containing the target chunk's file.
- **R2393:** The hardcoded `~/.ark` source's include list `arkSourceIncludePatterns` adds `external/**`. Mirror files are created on first authoring call; subdirectories are created as needed by `mcp.setExtTag`.
- **R3171:** A `[[source]]` may set `ext_mirror = "<dir>"` (a path relative to the source root) to redirect that source's `@ext` mirror tree from the global `~/.ark/external/<slug>/<target-path>.md` (R2392) into `<source-root>/<ext_mirror>/<target-path>.md`, so a source's routings live under it and travel with it. Only the mirror base directory changes: the trailing `.md`, the file format (R2394), and the authoring/read/cleanup code are all unchanged, and the mirror files are indexed as ordinary in-tree source files (no `external/**`-style registration, R2393, is needed since they already fall under the source's own include/exclude). A target whose file already lies within its source's `ext_mirror` directory has no mirror of its own — computing one would nest `mirrors/mirrors/…` — and is rejected the same way a self-referential `@ext` is. A source with no `ext_mirror` key is unaffected: the global tree remains the default. A **glob** source's `ext_mirror` propagates to each concrete source materialized from it by glob expansion (R197), alongside `strategies`/`include`/`exclude`, so every expanded directory keeps its own in-tree mirror.
- **R2394:** Mirror file content format is a flat list of `@ext:` lines, one per `(TARGET, tag, value)` tuple in v1. Mirror files use absolute paths in their `@ext:` TARGETs (relative paths inside mirror files would resolve against the mirror file's own directory, which is not the target's source location). Multi-tag lines (`@ext: TARGET @t1: v1 @t2: v2`) are syntactically valid but the authoring path emits one tag per line.
- **R2395:** `DB.SetExtTag(targetSpec, tag, value)` (Lua `mcp.setExtTag`) resolves `targetSpec` to a target file via the path or UUID branch, computes the mirror file path under `~/.ark/external/<slug>/<target-path>.md`, reads it (creating empty if absent), and **collapses** every `(TARGET, tag)` span in the mirror file (TARGET matching byte-for-byte, same tag name) to the single new value — rewriting the first match's value in place and dropping every later match (dropping a line left with no tags) — otherwise appends a new line `@ext: TARGET @tag: value`. In the common single-value case this is a plain in-place replace. The write is atomic via temp+rename (matching `mcp.setTags`); the watcher / indexer pick up the change and reindex the mirror file.
- **R2396:** `DB.RemoveExtTag(targetSpec, tag, value)` (Lua `mcp.removeExtTag(targetSpec, tag)` passes an empty value) locates the mirror file (missing file → silent no-op), finds **every** line matching `@ext: TARGET @tag:` (missing line → silent no-op), and removes the matching routing(s): an empty `value` removes all `(TARGET, tag)` spans, a non-empty `value` removes only spans whose value matches. Single-tag line: delete the line including trailing newline. Multi-tag line (rare in v1): remove only the matching `@tag: value` span, preserving the rest. The write is atomic via temp+rename.
- **R2397:** `mcp.suggestExtLocator(chunkID)` returns a table `{base, baseValue, locator, locatorKind, locatorText, withinFileDupCount, crossFileScope}`. `base` is `"uuid"` or `"path"`. `baseValue` is the TARGET base text (`%`-prefixed UUID, or absolute file path). `locatorKind` is `"string" | "regex" | "absolute" | "bare"`. `locatorText` is the locator's text payload (empty for `"bare"`). `withinFileDupCount` is the count of other chunks in the target's file sharing the same `@id`. `crossFileScope.chunks` and `crossFileScope.files` give the count of chunks and files the resolved `(base, locator)` would route to.
- **R2398:** The locator algorithm runs in three layers, falling through if a higher-priority layer cannot produce a unique result. **Layer 1** — Line-prefix token minimum: for each line in the target chunk, find the shortest token-aligned prefix unique among length-N line-prefixes across other chunks in the same file. Pick the smallest token count; tiebreak by earliest line. Tokens split on whitespace and punctuation. Case-insensitive uniqueness comparison; emitted locator preserves case. Empty lines are skipped. A prefix containing a literal `"` is emitted as a regex instead. **Layer 2** — Rare-trigram-anchored substring: when no line has a unique short prefix, search for a mid-line substring anchored at a trigram unique to the target chunk, expanded to word boundaries, clamped 12–60 characters. **Layer 3** — `absolute`: fall back to the chunk's range string.
- **R2399:** Layer 3 (`absolute`) is unavailable when the chunk's `Chunk.Range` value starts with `"` or `/` (non-conforming per the soft chunker contract R2378). In that case `mcp.suggestExtLocator` returns the best non-`absolute` result found by layers 1–2, even when not unique, or `locatorKind = "bare"` if no layer produced anything. The workshop UI surfaces a warning that the chunk could not be uniquely addressed via `@ext` locators.
- **R2400:** Default base selection: `"uuid"` when the chunk has an `@id` tag value (preferred — `@id` is stable across content edits and ranges); `"path"` otherwise. Default locator selection per chunk type: PDF or read-only chunker → `absolute` (text-search unreliable on binary content); `~/.claude/projects/**` paths → `absolute` (chat-log content unstable); markdown / text with `@id` and no within-file dups → `bare` (UUID alone is chunk-precise); markdown / text with `@id` and N > 1 within-file dups → `string` (auto-picked to narrow from N dups); markdown / text without `@id` → `string` (auto-picked), falling back to `absolute` when no unique span exists.
- **R2401:** `crossFileScope` is computed by running the same resolution path the resolver would. For UUID bases, this scans every chunk with the matching `@id` across all files. For path bases, the scope is constrained to the file the path resolves to.
- **R2402:** `mcp.chunkText(chunkID)` returns the chunk's text bytes as a Lua string on success, or `(nil, errstring)` on failure. Failure modes: unknown chunkID, chunk whose `(path, range)` no longer resolves to text (file removed or re-chunked between lookups), Sync error. The bridge runs `Sync` (no DB mutation); UTF-8 is preserved verbatim with no encoding transformation.
- **R2403:** `DB.ChunkTextByID(chunkID uint64) ([]byte, error)` backs `mcp.chunkText`. It resolves the chunkID to `(path, range)` via `ChunkInfo`, then reads bytes via the existing `ChunkText(path, range)` primitive. A `nil` return from `ChunkText` (range unresolvable) surfaces as the error `"chunk text unavailable"`.
- **R2404:** `mcp.parseTagBlock(text)` parses the leading `@name: value` block of `text` and returns a Lua table `{tags, body}`. `tags` is an ordered Lua array of `{name, value}` tables in the order the `@` lines appear; `body` is a Lua string of the bytes after the tag block (and after the blank separator line, if present). Wraps the existing `ParseTagBlock` Go helper; pure function with no DB lookup or Sync.
- **R2405:** `mcp.parseTagBlock` on a chunk with no leading tag block returns `{tags = {}, body = text}` — the entire input is the body. The Lua type check is the only error path (non-string argument errors via the standard gopher-lua convention).
- **R3047:** `DB.AddExtTag(targetSpec, tag, value)` appends a `(TARGET, tag, value)` routing to the target's mirror file, leaving any existing values in place so a `(TARGET, tag)` can carry multiple values (`@topic: recall` **and** `@topic: bloodhound`); an exact `(TARGET, tag, value)` already present is a silent no-op. Resolution, mirror-path computation, and the atomic temp+rename write match `SetExtTag`.
- **R3048:** The `ark ext {set,add,remove} <target> <tag> [value]` CLI group authors `@ext` routings from a plain session (the CLI counterpart to the workshop's Lua bindings), wrapping `DB.SetExtTag` / `DB.AddExtTag` / `DB.RemoveExtTag` respectively; `value` is required for `set`/`add` and optional for `remove`. Being mutating, each verb proxies to the running server when one is up (POST `/ext/{set,add,remove}`) and otherwise opens the index exclusively — mirroring the `config` add/remove dispatch. All three verbs act only on the target's **mirror file** and never scan or edit hand-authored `@ext` lines elsewhere in the corpus; a chunk's effective tag set is the union of its mirror-file routings and any hand-authored `@ext`, of which the CLI owns only the mirror's contribution.
- **R3049:** The server exposes `POST /ext/set`, `/ext/add`, and `/ext/remove`, each decoding `{target, tag, value}` and running the corresponding `DB.SetExtTag` / `DB.AddExtTag` / `DB.RemoveExtTag` inside the DB actor (`SyncVoid`), returning HTTP 200 on success or 400 with the error text. The mirror-file write happens on the actor goroutine via temp+rename (not `enqueueWrite`, which is reserved for bbolt mutations); reindex follows asynchronously via the watcher.
- **R3051:** The mirror file carries three tag classes sharing the `TARGET @tag: value` grammar: `@ext` (committed routing), `@ext-candidate` (proposed routing), and `@ext-judgment` (a judgment on the `(tag-name, chunk)` edge — tag-name only, no value). All three index as ordinary tags (a normal F and V record for the outer tag name), because the mirror file's literal content is the truth of the source. They differ only in what the indexer additionally derives: `@ext` routes a live edge, `@ext-candidate` / `@ext-judgment` derive a proposal / judgment record instead of the live edge. This authoring pass ships the file forms and verbs only; the RC / RJ derivation is a later pass and out of scope here.
- **R3052:** `mutateExtLine` / `applyExtMirrorEdit` are class-aware: the line-mutation machinery operates on any `@ext-*` class, selected by a named `extClass` marker set (`@ext`, `@ext-candidate`, `@ext-judgment`) rather than a hardcoded `@ext:` literal. `SetExtTag` / `AddExtTag` / `RemoveExtTag` operate on the committed (`@ext`) class; the staging verbs supply the candidate and judgment classes.
- **~~R3053:~~** (Retired T248 — see R3075) `DB.CandidateExtTag(targetSpec, tag, value, insight)` (CLI `ark ext candidate`) authors an `@ext-candidate` line in the target's mirror file, carrying the optional quoted `insight` first, before the TARGET (no `@` sigil). An exact duplicate (same TARGET, tag, value, and insight) is a silent no-op; a differing insight is a distinct proposal (a new line), so multiple insights on the same `(TARGET, tag)` are preserved rather than collapsed. Resolution, mirror-path computation, and the atomic temp+rename write match `SetExtTag`.
- **R3054:** `DB.AcceptExtTag(targetSpec, tag, value)` (CLI `ark ext accept`) rewrites every matching `@ext-candidate` line — TARGET byte-for-byte, same tag name, value filter when `value` is non-empty — to `@ext`, committing the routing and consuming the candidate in one file edit. The candidate's `insight` is dropped; the resulting `@ext` line carries the routed tag only. Missing file / line is a silent no-op.
- **R3055:** `DB.RejectExtTag(targetSpec, tag, value)` (CLI `ark ext reject`) rewrites matching `@ext-candidate` line(s) to a single tag-name-only `@ext-judgment: TARGET @tag:`, recording a durable rejection and consuming the candidate(s) for that `(TARGET, tag)`. Missing file / line is a silent no-op.
- **R3056:** The `ark ext {candidate,accept,reject}` CLI group wraps `DB.CandidateExtTag` / `DB.AcceptExtTag` / `DB.RejectExtTag`; `value` is optional for all three, and `candidate` additionally takes `--insight "why"`. Being mutating, each proxies to the running server when one is up and otherwise opens the index exclusively, mirroring `ark ext {set,add,remove}`. All three act only on the target's mirror file and never scan or edit hand-authored `@ext-*` lines elsewhere in the corpus.
- **R3094:** `ark ext candidate` additionally accepts `--disposition internal|external` (default `external`) and the `--internal` shorthand; an unrecognized `--disposition` value is a fatal usage error. The resolved disposition threads through the server `/ext/candidate` body (`extRequest.Disposition`) and `DB.CandidateExtTag` into the authored `@ext-candidate` line (R3092).
- **R3057:** The server exposes `POST /ext/candidate`, `/ext/accept`, and `/ext/reject`, each decoding the request (`{target, tag, value, insight}` for candidate; `{target, tag, value}` for accept / reject) and running the corresponding `DB` method inside the DB actor (`SyncVoid`), returning HTTP 200 on success or 400 with the error text. The mirror-file write happens on the actor goroutine via temp+rename; reindex follows asynchronously via the watcher.
- **R3077:** `LocatorSuggestion.Target()` assembles a suggestion into a single `@ext` TARGET string — the inverse of `ParseExtTargetParts`: bare → `%uuid`/path, absolute → `:range`, string → `:"…"`, regex → `:/…/` — escaping the narrower delimiter (and backslash) so `indexUnescapedByte` still finds the true closing delimiter. `DB.SuggestAnchor(path, range)` resolves a chunk location — a bare chunkID passed as `range` with empty `path`, or an explicit path+range looked up among the file's chunk entries — to a chunkID, then returns `SuggestExtLocator(chunkID).Target()`. It is exposed as `ark chunks -anchor <chunkID | path:range>`, the CLI counterpart to `mcp.suggestExtLocator`, proxying to `POST /chunks/anchor` when the server holds the single-process index (else resolving locally via `withDB`). This is **generic chunk addressing** — the opinionated, durable address a chunk should be routed or cited by — reusing `SuggestExtLocator`'s `@id`→unique-string→range heuristic rather than an `@ext`-only mechanism.

## Feature: tmp:// Lua-side read
**Source:** specs/tmp-documents.md

- **R2406:** `mcp.tmp_get(path)` returns the stored content of an existing `tmp://` document as a Lua string on success, or `(nil, errstring)` on failure. Failure modes: missing `tmp://` prefix, document not present in the overlay, Sync error. Complements the existing `mcp.tmp_add` / `mcp.tmp_update` / `mcp.tmp_remove` / `mcp.tmp_list` surface; same overlay, opposite direction.
- **R2407:** `DB.TmpContent(path string) ([]byte, error)` backs `mcp.tmp_get`. It validates the `tmp://` prefix, calls through to `db.fts.TmpContent(path)`, and reads the bytes. Sync read; no overlay mutation. Non-`tmp://` paths return the error `"not a tmp:// path"`; paths absent from the overlay surface microfts2's underlying error verbatim.

## Feature: hot-correlations async sweep entry
**Source:** specs/hot-correlations.md

- **R2408:** `mcp.sweepHotCorrelationsAsync()` enqueues the corpus-wide HC sweep through the existing write goroutine and returns nothing — the Lua VM is not held for the duration of the sweep. Progress and terminal status are observed via the existing `tmp://sweep/hot-correlations.md` document (subscribers reach it through `mcp.subscribe` on `@sweep-status`).
- **R2409:** `Librarian.SweepHotCorrelationsAsync()` backs the async bridge. It enqueues the same closure the blocking variant runs (`l.SweepHotCorrelations()`) but does not wait for the result; the result reaches callers through the progress document. The `*HCSweepResult` from the inner call is logged-on-error and otherwise discarded. The synchronous `Librarian.SweepHotCorrelations()` and the blocking `mcp.sweepHotCorrelations()` bridge remain unchanged for callers that need the result inline.
- **R2410:** Multiple async calls queue serially through the existing write actor — a second call issued while a sweep is in flight runs once the first completes. The bridge does not de-duplicate enqueues; steady-state sweeps after the first are cheap (chunk-displace only), so back-to-back calls are not pathological. UI callers can debounce the trigger if rapid clicks become a problem.

## Feature: Curate buttons (web-component consumers)
**Source:** specs/curation.md

- **R2411:** `POST /curation/dismiss` accepts a JSON body `{chunkID}` and removes the matching pinned entry by routing through the Lua executor (mirrors `POST /curation/pin` shape and concurrency model). Required: `chunkID` (uint64, non-zero). Response: `200 OK` with empty body on success; `400 Bad Request` for malformed JSON or zero/missing `chunkID`; `503 Service Unavailable` when the Lua runtime is not wired.
- **R2412:** `POST /curation/dismiss` is a silent no-op when the `chunkID` is not currently pinned — the response is `200 OK` even when nothing was removed. Matches the `sys.curation.dismiss` Lua semantic so the button's "toggle off" path is idempotent across stale client state.
- **R2413:** `GET /curation/pinned` returns a JSON response `{chunkIDs: [uint64, ...]}` listing every currently pinned entry's chunk ID in the same newest-first order the in-memory `Curation.Pinned` slice keeps. Empty pinned list returns `{chunkIDs: []}`. Response is `200 OK`; `503 Service Unavailable` when the curation store is not wired.
- **R2414:** `GET /curation/pinned` is read-only — it reads `Curation.Pinned` through a Go-side snapshot helper (no Lua-executor entry, no mutation). Safe to call concurrently with `pin` / `dismiss`; the snapshot may race with an in-flight mutation but is internally consistent.
- **R2415:** The content view (`/content/PATH`) renders each chunk in a `<div class="ark-chunk" data-range="..." data-chunkid="..." data-fileid="...">` wrapper. The new `data-chunkid` attribute carries the chunk's uint64 ID (sourced from `srv.db.ChunkIDsForPath(path)` already called in the rendering path); `data-fileid` carries the file's uint64 ID (sourced once per file from `db.fts.CheckFile(path).FileID`). Both attributes appear on every chunk div across all four rendering branches: markdown single-chunk, markdown full-file, non-markdown chunked, non-markdown PDF.
- **R2416:** `content-markdown.html` and `content-plain.html` include an inline `<script>` that runs on `DOMContentLoaded`: fetches `GET /curation/pinned`, builds a Set of pinned chunk IDs, then iterates every `div.ark-chunk` and prepends a `<button class="ark-curate-pin">` with `aria-pressed="true"` when the chunk's `data-chunkid` is in the set, otherwise `aria-pressed="false"`. A failed fetch leaves every button at `aria-pressed="false"` (display-only feature — must not break the page).
- **R2417:** The pin button's click handler reads `data-chunkid` / `data-fileid` from its parent `div.ark-chunk` and the page's path from `location.pathname` (stripping the `/content/` prefix, URL-decoded). When `aria-pressed="false"`: POSTs `/curation/pin` with `{chunkID, fileID, path}`; on 2xx, flips to `aria-pressed="true"`. When `aria-pressed="true"`: POSTs `/curation/dismiss` with `{chunkID}`; on 2xx, flips back. Non-2xx responses leave the visual state unchanged and log the error to the browser console.
- **R2418:** The pin button is absolutely positioned at the chunk div's upper-left corner with a small inset. The visual distinguishes pinned vs unpinned via CSS class driven by `aria-pressed` — same SVG glyph, filled when pressed and outlined when not. Width/height ~16 px, color uses the existing `--term-accent` and `--term-text-dim` theme variables. The button is inert in print stylesheets.
- **R2419:** PDF chunks (rendered via `<pdf-chunk>` rather than `<div class="ark-chunk">`) are out of scope for v1 — they have their own component wrapper and would need a parallel hook. Tracked as follow-up; the inline script in `content-plain.html` skips them gracefully (they don't match the `div.ark-chunk` selector).
- **R2420:** Cross-page synchrony (workshop pins a chunk → an already-open content iframe reflects it) is not provided in v1. Each iframe seeds state once on load via `GET /curation/pinned`; subsequent mutations made elsewhere are invisible until the iframe is reloaded. A future enhancement can subscribe via the existing pubsub primitives.
- **R2421:** For every PDF chunk with a `rect` attribute, `renderPdfChunksByPage` emits an `<ark-curate-region chunkid="..." fileid="..." rect="x,y,w,h">` overlay child inside the page's `<pdf-chunk>` element. `chunkid` / `fileid` are plain HTML attributes (matching the `<pdf-chunk>` convention rather than `data-`). `rect` is in PDF page coordinates, same encoding as `<ark-tag rect>`. Salvage chunks (no rect — already wrapped in `<div class="ark-chunk">` by the salvage fallback) get the standard pin via the existing div selector.
- **R2422:** `pdf-chunk-element.ts` gains a `positionRegions` pass invoked from `render()` alongside `positionHitRegions`. It iterates `:scope > ark-curate-region[rect]` children and converts page-coord rect to CSS pixels using the same formula `positionHitRegions` already applies: `leftPx = (regionRect.x - chunkRect.x) * cssScale`; `topPx = (chunkRect.y + chunkRect.h - regionRect.y - regionRect.h) * cssScale`; `widthPx = regionRect.w * cssScale`; `heightPx = regionRect.h * cssScale`. Sets `style.position = 'absolute'` plus the computed left/top/width/height.
- **R2423:** `<ark-curate-region>` has a transparent border by default and reveals a faint dashed outline on `:hover` so the user can see chunk boundaries on the page. `pointer-events: auto` so the region receives hover and click events through the PDF canvas. The hover affordance also applies to `<div class="ark-chunk">` for consistency across PDF and non-PDF chunked views.
- **R2424:** The pin button (`<button class="ark-curate-pin">`) is installed as the first child of every chunk container — both `<div class="ark-chunk">` and `<ark-curate-region>`. Existing CSS positions it at the container's upper-left (`top: 0.25em; left: 0.25em` for div; `top: 0; left: 0` for region since the region rect is already small). Same `aria-pressed` state, same click handler, same `/curation/pin` and `/curation/dismiss` endpoints.
- **R2425:** The inline pin-injection script's selector matches both `div.ark-chunk[data-chunkid]` and `ark-curate-region[chunkid]`. Identifier extraction reads `el.dataset.chunkid` for the div case and `el.getAttribute('chunkid')` for the region case; the install loop, hover state, and toggle handler are otherwise shared between the two element types.

## Feature: Curation Workshop Primitives — extractTagValues
**Source:** specs/curation-workshop-primitives.md

- **R2426:** `mcp.extractTagValues(text, strategy)` returns every `@name: value` pair found anywhere in `text`, using the same scanner the indexer uses (`ExtractTagValues`). The result is an ordered Lua array of `{name, value}` tables in the order the `@` lines appear; `name` is lowercased and `value` is whitespace-trimmed. `strategy` selects the chunker (e.g. `"markdown"`) so markdown-specific mention heuristics (fenced/indented code) can skip false positives; it defaults to `"markdown"`. Compound lines like `@ext: TARGET @t1: v1` yield only the outer tag, with the full remainder as its value — embedded-tag splitting is the outer tag's own job. Pure function: no DB lookup, no Sync.

## Feature: tag value regex post-colon gap
**Source:** specs/tag-extraction-fixes.md

- **R2427:** `tagValueRegex` matches `@name:` followed by the value to end of line. The gap between the colon and the value is intra-line whitespace only (`[ \t]*`), never the broader `\s*`. The broader class would include newlines, so an empty-value tag followed by another `@` line — `@e:\n@c: d` — would parse as a single tag `e` whose value is `@c: d`, swallowing the next line. The `[ \t]*` form honors the documented "to end of line" semantics and keeps empty-value tags atomic.

## Feature: PDFChunker indexing-path persistence is dispatch-agnostic
**Source:** specs/pdf-chunker.md

- **R2428:** PDFChunker's page-blob cache (`content_offset` / `content_len` chunk attrs + per-page blobs in the Store) MUST be populated whenever the chunker is invoked at index time, regardless of which interface microfts2's dispatch happens to pick. microfts2's `collectChunks` prefers `Chunker` over `FileChunker` when both are implemented, so `Chunks` is just as load-bearing for indexed-file persistence as `FileChunks`. Both index-time entry points seal page blobs.
- **R2429:** Retrieval-time invocations of the chunker — the fallback inside `GetChunk` when `fastRetrieve` cannot satisfy the request — MUST NOT stage blobs. Retrieval is not indexing; staging without a matching `FlushBlobs` would leak `pending` entries and risk overwriting fresh blobs with old text on the next indexing pass. The streaming-retrieve helper uses a non-persisting code path internal to PDFChunker.

## Feature: inbox project filter (either-side)
**Source:** specs/inbox-enhancements.md

- **R2430:** `ark message inbox --to PROJECT` filters messages where `@to-project` matches PROJECT. This is the strict "incoming for PROJECT" view (requests targeted at PROJECT plus responses to PROJECT's outgoing requests). `--to` carries the semantic the old `--project` flag had before R498 was retired.
- **R2431:** `ark message inbox --project PROJECT` filters messages where EITHER `@to-project` OR `@from-project` matches PROJECT — the "everything involving PROJECT" view. Composable with `--from` and `--to`: every filter must match (intersection). Replaces the retired R498 to-only semantic; the strict to-only behavior is now `--to`.

## Feature: tags-output baby food
**Source:** specs/tags-baby-food.md

- **R2432:** `ark search -tags` proxied through `POST /search` decodes the server's `[]TagResult` response directly. Previously the CLI decoded into `[]SearchResultEntry` and re-extracted on empty data, silently returning nothing for every server-path invocation.
- **R2433:** `ark search -tags` emits a markdown bullet tree with four layers — `@tag-name` → value → file → `:range` — instead of TSV `tag\tcount` lines. The header for a tag carries `(N chunks across M files)` when the tag spans more than one value OR more than one file; single-value single-file tags drop the redundant header.
- **R2434:** Tag extraction captures the value when present on the same line (`@name: value`) in addition to the tag name. The chunk-text scan still recognizes bare `@name:` patterns; the value layer is empty (no children) when no value is present.
- **R2435:** Each `-with -tag NAME` filter on the command line suppresses `@NAME` from level 1 of the `-tags` output (the agent already knows it filtered for that tag). The value layer becomes the top of that subtree. `-without -tag …` never triggers suppression.
- **R2436:** Each `-with -tag NAME:VALUE` filter suppresses both `@NAME` and the matching VALUE; the location layer becomes the top of that subtree. When every `-with -tag` filter is fully specified, the `-tags` output collapses to a flat list of file/chunk locations.
- **R2437:** `-no-values` (orthogonal to the location axis) collapses the value layer; chunks from different values merge into one list under the tag name. Composes with `-no-chunks` and `-no-files`.
- **R2438:** `-no-chunks` strips the `:range` suffix from location lines, leaving file paths only; duplicate file paths under one value are deduplicated. Composes with `-no-values`.
- **R2439:** `-no-files` drops file/chunk locations entirely; only tag/value counts remain. Subsumes `-no-chunks`. Composes with `-no-values`; combined with `-no-values` it produces tag-names only.
- **R2440:** Tags NOT named in any `-with -tag` filter appear in full hierarchy alongside any tags that did trigger suppression. Each filter independently determines what's hidden for its own tag.
- **R2441:** `ark search -tags -json` emits `TagResult` JSONL — one object per line, full structured shape (tag, count, bestScore, fileCount, values[]) — instead of the markdown bullet tree. Suppression flags (`-no-values`/`-no-chunks`/`-no-files`) and `-with -tag` adaptive defaults do NOT affect JSON output; programs filter the structured data themselves.

## Feature: tag match syntax (shared by -tag and -file-tag)
**Source:** specs/file-tag-filter.md

- **R2442:** `-tag` and `-file-tag` accept a single argument of the form `[~|:]NAME [(=|:|~) VALUE]`. A single shared parser converts the argument into a `MatchPredicate` (name mode + name material; value mode + value material) used by every consumer (search CLI, subscribe CLI, server JSON, ark-search element). The leading sigil selects the name mode; the internal separator selects the value mode.
- **R2443:** Name match modes. **Exact:** bare `NAME` is a case-insensitive literal match against the tag name. **Contains:** `:NAME` lowercases NAME and the tag name, splits NAME on whitespace into tokens, matches iff every token is a substring of the lowercased tag name (substring-AND, order-independent). Mirrors `Store.MatchTagNames`. **Regex:** `~NAME` treats NAME as a case-insensitive RE2 pattern matched against the tag name.
- **R2444:** Value match mode — exact: `=VALUE` compares the chunk tag value as a literal string. No normalization beyond the tag extractor's own.
- **R2445:** Value match mode — contains: `:VALUE` lowercases VALUE and the chunk tag value, splits VALUE on whitespace into tokens, matches iff every token is a substring of the lowercased chunk value (substring-AND, order-independent). Mirrors `Store.MatchTagValues` semantics.
- **R2446:** Value match mode — regex: `~VALUE` treats VALUE as an RE2 pattern matched against the chunk tag value with no anchoring (partial matches succeed).
- **R2447:** A bare NAME (no separator) matches any value. The match succeeds if the name-mode accepts the chunk's tag name regardless of value content.
- **R2448:** Empty value after a separator: `T=` matches only tags whose value is the empty string; `T:` and `T~` are degenerate (both match every value) and are accepted as equivalent to bare `T`.
- **R2449:** `@` normalization: a single decorative `@` is stripped from the argument when it appears at the very start, immediately after `~` (regex name sigil), or immediately after `:` (contains name sigil) — so `@T`, `@~T`, `~@T`, `@:T`, and `:@T` all normalize to the same predicate as their `@`-less form. The name-mode sigil is preserved.
- **R2450:** A trailing `:` on the name (the form `@status:` users see in files) is stripped during normalization, preserving existing behavior. Applied after `@` stripping and before sigil parsing.
- **R2451:** `ark search -parse` annotates every `-tag` and `-file-tag` row with the decoded name-mode and value-mode, e.g. `-tag exact:status regex:^(open|in-progress)$`, so users can verify what the parser understood.

## Feature: tag-match wire consolidation
**Source:** specs/file-tag-filter.md

- **R2452:** The server retires `ChunkFilterRow.Mode == "tag-contains"`. The single `tag` mode covers all name and value match shapes via the sigil-form Query. Callers that previously emitted `tag-contains` with query `"ntok:vtok"` must emit `tag` with query `:ntok:vtok` (leading `:` selects the contains-name mode). The semantic of `:V` on the value side is preserved (substring-AND tokens, what `MatchTagValues` already did); the sigil makes the prior implicit contains behavior explicit and adds `=` (exact) and `~` (regex) as new value modes.

## Feature: ark search -file-tag
**Source:** specs/file-tag-filter.md

- **R2453:** `ark search -file-tag TAG[SEP VALUE]` is a chunk-level filter that accepts a chunk iff its primary file F has a tag matching the parsed predicate. The "file has tag" relation is "at least one chunk in F carries that (name, value) in its extracted tag set."
- **R2454:** `-file-tag` honors the active `-with` / `-without` polarity at parse position. `-without -file-tag X` rejects chunks whose file has X.
- **R2455:** `-file-tag` is repeatable in a single search. Multiple `-file-tag` entries AND together at their active polarity — a chunk's file must satisfy every entry.
- **R2456:** The `-file-tag` predicate is evaluated against the per-file tag aggregate index (e.g. `FileTagValues`), never against a single chunk's local tag list. The result is cached once per file per search and reused across all chunks of that file.

## Feature: ark subscribe match-syntax unification
**Source:** specs/file-tag-filter.md

- **R2457:** `ark subscribe -tag` accepts the new sigil match syntax via the shared parser (R2442). `-tag` becomes the only way to express name+value matching for tag-name subscriptions.
- **R2458:** The `-value` flag is removed from `ark subscribe`. The matching pieces are absorbed into `-tag`: `T=V` for exact, `T:V` for contains, `T~RE` for regex-on-value. Callers that previously used `--cancel --tag T --value V` rewrite to `--cancel --tag T=V` (or the appropriate sigil form).
- **R2459:** `-tag` is repeatable in a single subscribe call. Multiple `-tag` entries register multiple subscriptions for the session; entries OR together at delivery time — a notification fires if any entry matches the indexed tag.

## Feature: ark subscribe -file-tag
**Source:** specs/file-tag-filter.md

- **R2460:** `ark subscribe -file-tag TAG[SEP VALUE]` registers interest in **any chunk indexed on a file that has the tag** matching the parsed predicate. The chunk's own text is not consulted; file-tag membership is what gates delivery.
- **R2461:** `-file-tag` is repeatable. Multiple `-file-tag` entries OR together — a chunk's file matching any one of them triggers delivery (consistent with `-tag` repeat semantics).
- **R2462:** A subscription entry may combine `-tag` and `-file-tag` independently. They are independent axes; neither is required when the other is present.
- **R2463:** A subscription with one or more `-file-tag` filters maintains an in-memory set of fileIDs that currently match **at least one** of the subscription's `-file-tag` predicates (OR semantics, consistent with R2461). The set is consulted at delivery time and updated on every relevant indexing event.
- **R2464:** On every chunk indexed for file F (regardless of F's current membership), each `-file-tag` predicate is re-evaluated against F's authoritative per-file tag aggregate (not the chunk's local tag delta). Removing a tag from one chunk does not imply F lost the tag — another chunk in F may still carry it.
- **R2465:** Membership transition `was=N, is=Y`: F is added to the set; the indexed chunk is delivered as the entry event. Prior chunks on F are not backfilled.
- **R2466:** Membership transition `was=Y, is=Y`: F remains a member; the indexed chunk is delivered.
- **R2467:** Membership transition `was=Y, is=N`: F is removed from the set; the indexed chunk is delivered as the exit event. Symmetric with the entry rule — the moment T disappears on F is itself activity the subscriber asked for.
- **R2468:** Membership transition `was=N, is=N`: no `-file-tag` delivery for this chunk on this subscription. Other subscriptions on the session (e.g., a plain `-tag`) may still deliver it independently.
- **R2469:** Membership sets are in-memory only; they live with their parent subscription. On subscription cancel or session TTL reap, the set is discarded. On server restart, sets start empty and re-populate through the normal `was=N, is=Y` path as fresh chunks arrive.
- **R2470:** `@mute: true` in a file silences all file-tag deliveries from that file. The mute check runs before membership evaluation.
- **R2471:** A session does not receive `-file-tag` deliveries for chunks it itself indexed (same self-notification rule the existing `-tag` path already enforces).

## Feature: ark-search element -file-tag row
**Source:** specs/file-tag-filter.md

- **R2472:** The `ark-search` web component exposes a `-file-tag` filter row alongside the existing `-tag` row. Both accept the new sigil match syntax. Polarity (`with` / `without`) and repeat semantics match the `-tag` row. The component serializes file-tag filters into the same `chunk_filters` request shape the server already handles for `-tag`.

## Feature: tag-name CLI normalization
**Source:** specs/cli-commands.md

- **R2483:** Bare tag-name arguments to `ark tag set/get/counts/files/values/defs` (and the `message set-tags`/`message get-tags` aliases) strip a single leading `@` and a single trailing `:` per argument before use. A caller pasting the rendered form `@status:` is equivalent to passing `status`. Mirrors R2449/R2450 for `-tag` sigil parsing; prevents the `@@area::` malformed-tag wonk when a copy-pasted name flows into `TagBlock.Set`.

## Feature: --unmatched global pair lookup
**Source:** specs/cli-commands.md

- **R2484:** Pair matching for `ark message inbox --unmatched` and for bookmark-lag computation uses the **full inbox**, not the post-filter slice. A request is "unmatched" iff no response with matching `requestId` exists anywhere in the index. The CLI's `--project`, `--to`, `--from`, `--all`, `--include-archived` filters select which unmatched requests are *displayed*; they do not constrain which pairings are *visible* to the matcher. (Replaces R716.) The lag field is computed against the global pair for the same reason: a `lag:PROJECT:STATUS` value should not disappear because a directional filter hid the counterpart message.

## Feature: atomic message create
**Source:** specs/cli-commands.md

- **R2485:** `ark message new-request` and `ark message new-response` create the target file atomically (write to a sibling temp file in the same directory, then rename into place). A partial write, killed process, or other mid-flight failure never leaves a 0-byte husk at the target path. Either the file appears with full content, or it does not appear at all.

## Feature: Nano — overview
**Source:** specs/nano-overview.md

- **R2486:** Nano is a Go port of nano.py that preserves the same shell-agent loop and single-tool philosophy. Embedded in ark and exposed via `ark nano`.
- **R2487:** Nano talks to a local Ollama server via `/api/chat`, not OpenAI's Responses API.
- **R2488:** The embedded code lives in module `github.com/zot/ark` (package `ark`); the standalone provenance was `github.com/zot/nano-go`.
- **R2489:** The library lives at `nano.go` as a sibling top-level file in package `ark`.
- **R2490:** The CLI is the `nano` subcommand of `ark`, wired in `cmd/ark/main.go`.
- **R2491:** Nano's library code has no non-stdlib runtime dependencies; only the CLI path may depend on `github.com/chzyer/readline`.

## Feature: Nano — library
**Source:** specs/nano-library.md

- **R2492:** All configuration hangs off a single exported `Nano` struct.
- **R2493:** `Nano.Model` is required; the zero value causes `Run`/`REPL` to return an error.
- **R2494:** `Nano.BaseURL` defaults to `http://localhost:11434`.
- **R2495:** `Nano.MaxSteps` defaults to 200.
- **R2496:** `Nano.SessionsPath` defaults to `~/.ark/nano-sessions.json`.
- **R2497:** `Nano.Stdin`, `Stdout`, and `Stderr` default to `os.Stdin`, `os.Stdout`, and `os.Stderr` respectively.
- **R2498:** `Nano.Cwd` defaults to the result of `os.Getwd`.
- **R2499:** `Nano.HTTPClient` defaults to a fresh `*http.Client{}`.
- **R2500:** `Run(prompt, history)` returns the final assistant text and the updated message history.
- **R2501:** When `history` is nil, `Run` seeds it with the system prompt.
- **R2502:** `REPL` accepts a `ReadLineFunc` callback so the library does not depend on `chzyer/readline`.
- **R2503:** Passing a nil `ReadLineFunc` to `REPL` falls back to a plain `bufio` reader on `Nano.Stdin`.
- **R2504:** `LoadNanoSessions(path)` returns `(nil, nil)` when the file does not exist.
- **R2505:** `SaveNanoSession(path, s)` replaces any existing session with the same label and cwd rather than duplicating it.
- **R2506:** `SaveNanoSession` caps the on-disk file at the most recent 50 sessions.
- **R2507:** `NanoSessionsInCwd(path, cwd)` returns sessions whose cwd matches, oldest first.

## Feature: Nano — CLI
**Source:** specs/nano-cli.md

- **R2508:** The CLI synopsis is `ark nano [-m model] [-c | -s] [prompt...]`.
- **R2509:** A non-empty prompt runs one-shot: the prompt is executed and the program exits.
- **R2510:** An empty prompt drops the user into an interactive REPL.
- **R2511:** `-m <model>` sets the model name. Model must be provided via this flag — no environment-variable fallback.
- **R2512:** `-c` resumes the most recent session whose cwd matches the current working directory.
- **R2513:** `-s` lists up to ten recent sessions in the cwd, reads a digit from stdin, and resumes the chosen session.
- **R2514:** When `-c` or `-s` finds no matching sessions, the CLI exits with `no sessions in this directory`.
- **~~R2515:~~** (Retired T88 — no replacement) `OLLAMA_MODEL` seeds `Nano.Model` when `-m` is not given.
- **~~R2516:~~** (Retired T89 — see R2561) `OLLAMA_BASE_URL` seeds `Nano.BaseURL`.
- **~~R2517:~~** (Retired T90 — see R2562) `NANO_MAX_STEPS` seeds `Nano.MaxSteps`.
- **~~R2518:~~** (Retired T91 — see R2563) `NANO_APPROVE=all` seeds `Nano.ApproveAll`.
- **R2519:** When no model is set, the CLI exits with `model not set: pass -m <model>`.
- **R2520:** The REPL uses `chzyer/readline` for line editing (arrow keys, history, ctrl-L).
- **R2521:** `:q`, `quit`, and `exit` end the REPL.
- **R2522:** `:reset` and `reset` clear the in-memory history and start over.
- **R2523:** Ctrl-D / EOF on the prompt exits the REPL cleanly.
- **R2524:** The CLI always runs with `KeepHistory=true`.
- **R2525:** The CLI enables TTY (color and spinner) only when stderr is a character device.
- **R2526:** Clean exit returns 0; fatal errors return 1.

## Feature: Nano — sessions
**Source:** specs/nano-sessions.md

- **R2527:** Sessions are stored as a JSON array of `NanoSession` objects in a single file.
- **R2528:** Each `NanoSession` has fields `label`, `cwd`, `ts`, and `messages`.
- **R2529:** `label` is truncated to 80 characters when saved.
- **R2530:** `cwd` is the absolute path of the working directory at save time.
- **R2531:** `ts` is Unix epoch seconds at last save.
- **R2532:** `messages` is the full chat history including system, user, assistant, and tool messages.
- **R2533:** The sessions file is written with mode 0600.
- **R2534:** `KeepHistory` defaults to false in the library so embedding does not silently write to disk.
- **R2535:** The default sessions path is `~/.ark/nano-sessions.json`, distinct from both nano.py's `~/.nano_sessions.json` and standalone nano-go's `~/.nano-go_sessions.json`. The schemas are not all compatible; the three files remain independent.

## Feature: Nano — tool loop
**Source:** specs/nano-tool-loop.md

- **R2536:** The model is given exactly one tool, `execute_shell`.
- **R2537:** `execute_shell` accepts `command`, `description`, `cwd`, `timeout`, and `env`.
- **R2538:** `command` and `description` are required arguments.
- **R2539:** The library rejects `execute_shell` calls whose `description` is outside 5–10 words with the literal string `bad arguments: description must be 5-10 words`.
- **R2540:** `timeout` defaults to 60 seconds when absent or zero.
- **R2541:** `cwd` defaults to `Nano.Cwd` when absent.
- **R2542:** Approved commands run with `env = os.Environ() ++ args.env`.
- **R2543:** The Run loop iterates up to `MaxSteps` times.
- **R2544:** When the loop budget is exhausted, `Run` returns the literal string `stopped: too many tool calls`.
- **R2545:** Before each command runs, the approval prompt prints the description, the command (in green when TTY), and any non-default cwd/timeout/env to `Nano.Stderr`.
- **R2546:** When `ApproveAll` is set, commands run without prompting.
- **R2547:** `y` or `yes` approves the current command.
- **R2548:** `a` or `all` flips `ApproveAll` to true and approves the current command.
- **R2549:** Any other input — including EOF — denies the command.
- **R2550:** A denied command returns the literal string `denied by user` as the tool result.
- **R2551:** Approved commands run via `sh -c <command>`.
- **R2552:** The tool result string is shaped as `$ <command>\nexit <code>\n<output>` or `$ <command>\ntimeout after <N>s\n<output>` on timeout.
- **R2553:** Tool result strings are clipped to the last `Nano.MaxOutputBytes` bytes before being returned to the model.
- **R2554:** A timer kills the running process when `timeout` elapses.
- **R2555:** The system prompt includes the cwd, the platform (`runtime.GOOS`/`runtime.GOARCH`), and the user's `$SHELL`.
- **R2556:** The system prompt includes a list of project documentation files (`CLAUDE.md`, `AGENT.md`, `AGENTS.md`, `README.md`, case-insensitive) found under the cwd.
- **R2557:** The system prompt includes a list of skill files (`SKILL.md`, `SKILLS.md`, case-insensitive) found under `.claude/skills`, `~/.claude/skills`, `~/.codex/skills`, and `~/.codex/plugins`.
- **R2558:** Directory walks for docs and skills skip `.git`, `.venv`, `__pycache__`, `node_modules`, and `venv`.
- **R2559:** `Nano.MaxOutputBytes` defaults to 12000.

## Feature: Nano — help flag
**Source:** specs/nano-cli.md

- **R2560:** `ark nano -h` and `ark nano --help` print usage and exit 0. The flag is accepted at any position in the argument list, including after `-m`. Output names the synopsis, every recognized flag, every environment variable read, the sessions-file path, and the REPL command set.

## Feature: Nano — flags-only configuration
**Source:** specs/nano-cli.md

- **R2561:** `--base-url <url>` sets `Nano.BaseURL`. Replaces R2516 (OLLAMA_BASE_URL env-var seed); no environment fallback.
- **R2562:** `--max-steps <N>` sets `Nano.MaxSteps`. Replaces R2517 (NANO_MAX_STEPS env-var seed); no environment fallback. Non-integer argument exits with `--max-steps requires an integer`.
- **R2563:** `--approve-all` (boolean, no argument) sets `Nano.ApproveAll = true`. Replaces R2518 (NANO_APPROVE env-var seed); no environment fallback.

## Feature: Nano — streaming output
**Source:** specs/nano-cli.md, specs/nano-library.md

- **R2564:** `Nano.Stream bool` enables token-by-token streaming. When true, `chat()` dispatches to `chatStream()`, which sends `stream: true` to Ollama's `/api/chat`, parses the NDJSON response, and writes each non-empty `message.content` delta to `Nano.Stdout` as it arrives. Tool calls accumulate from frames where they appear (typically the final `done:true` frame). The thinking spinner (if `Spinner` is true) runs from request-sent until the first frame carrying visible content (`message.content` non-empty) or a tool call arrives, then yields to the stream. Empty / role-marker / reasoning-only frames do not stop the spinner — the live tokens or the approval prompt are the progress signal from that point on. After the stream terminates, the accumulated `Message` is returned for the agent loop. The library writes a trailing newline to Stdout after non-empty streamed content; downstream callers (REPL, one-shot CLI) skip the redundant final answer print when `Stream` is set.
- **R2565:** `ark nano --stream` (boolean, no argument) sets `Nano.Stream = true`. Default off, matching the upstream nano-go behavior of `stream: false`. Mirrors `ollama run`'s token-by-token printing convention.

## Feature: Nano — spinner interactivity gate
**Source:** specs/nano-cli.md

- **R2566:** `Nano.Spinner bool` controls the thinking spinner separately from `Nano.TTY`. `TTY` continues to gate ANSI color on `Stderr`; `Spinner` gates the animated spinner. The CLI sets `Spinner = stderrIsTerminal && stdoutIsTerminal` so the spinner appears only in fully-interactive mode — piping stdout (e.g. `ark nano … | cat`) or redirecting either descriptor suppresses it. `Stream` mode interacts with `Spinner` per R2564 (spin until first frame, then yield).

## Feature: Find Connections — substrate (Tag Forge Phase 2A backend)
**Source:** specs/find-connections-substrate.md

- **R2567:** `sys.findConnections(inputs, opts)` is the canonical Lua entry point for both normal and turbo modes; accepts a mixed input list and an opts table. The old `mcp.findConnections` becomes a backwards-compat shim that delegates with `mode = "turbo"` for one release.
- **R2568:** Each input list entry is one of `{chunkID = N}`, `{path = P, range = R}`, or `{text = T}`. A bare integer array is accepted as a sugar form for `{chunkID = N}` entries (preserves 1G call shape).
- **R2569:** `chunkID` inputs resolve via `EC[chunkID]` for vector substrates and via the chunk's V records for tag votes. Unknown chunkID is rejected at enqueue with `unknown chunk <id>` and no tmp:// doc is created.
- **R2570:** `path:range` inputs select chunks whose `(startLine, endLine)` interval overlaps the requested `[startLine, endLine]` (1-based inclusive). `path:N` alone selects line N; `path` alone (without a `:range` suffix) is rejected.
- **R2571:** `path` is resolved via the existing path resolver. A path miss is rejected at enqueue with `path "<p>" not found`. A malformed range is rejected with `path:range parse error`.
- **R2572:** `text` inputs are embedded in-process via the existing `EmbedQuery` model path; no EC record is written. The text counts as one virtual input with `chunkID = 0` and `path = "<query>"` in evidence reporting.
- **R2573:** Empty input list after normalization is rejected at enqueue with `chunkIDs/text/range empty`. No tmp:// doc is created.
- **R2574:** For each normalized input, the substrate pipeline runs four passes: `vector(input, ED)`, `trigram(input, ED)`, `vector(input, EC)`, `trigram(input, EC)`.
- **R2575:** `vector(input, ED)` is a cosine scan over ED records; per-tag aggregate is **max** across defining files, matching `SuggestTagNames`'s aggregation. Reuses the `SuggestTagNames` walk.
- **R2576:** `trigram(input, ED)` is a trigram match against tag-definition text via microfts2; per-tag aggregate is max across defining files. Normalized overlap drives the score.
- **R2577:** `vector(input, EC)` is a cosine scan over EC records via `SearchChunks(queryVec, K')` (K' = 50 by default). For each returned chunk, its V records cast tag votes; per-tag aggregate is the max chunk-similarity score across chunks carrying that tag.
- **R2578:** `trigram(input, EC)` is a trigram-fuzzy match against chunk text; for each returned chunk, V records cast tag votes; per-tag aggregate is max trigram-score across contributing chunks.
- **R2579:** All four substrate passes for a single request share one `db.View` txn to avoid lock churn. Multi-input requests use the existing `SearchChunksMulti` batched cursor walk where applicable.
- **R2580:** Per-input merge across substrates: a tag's per-input aggregate score is **max** across the four substrate scores after normalization.
- **R2581:** Cross-input merge across inputs: a tag's overall score is **max** across its per-input aggregate scores.
- **R2582:** Per-substrate per-input scores are retained in the result for the evidence display: `vector_ed`, `trigram_ed`, `vector_ec`, `trigram_ec`. The pipeline does not collapse them into the single aggregate.
- **R2583:** Each candidate's supporting-chunk list is the union of contributing chunks across inputs and substrates, capped at 10 entries by default.
- **R2584:** Each candidate's motivating-files list carries the tag-def files whose ED matched, with per-file scores, mirroring `SuggestTagNames.MotivatingFiles`.
- **R2585:** Output is the top-k candidates by overall score. `k` defaults to 20 and is clamped to `[1, 200]` via `opts.k`.
- **R2586:** Vector cosine scores are normalized to `[0, 1]` via `(cos + 1) / 2`.
- **~~R2587:~~** (Retired T92 — see R2643) Trigram scores are normalized to `[0, 1]` using microfts2's existing fuzzy-match scoring. Vector and trigram scores are directly comparable on the same scale.
- **R2588:** When embedding is unavailable (no `[embedding] model` configured, model file missing, or model load fails), vector substrates skip; trigram substrates still run. The doc header carries `@connections-warning: embedding unavailable`.
- **R2589:** When the ED prefix is empty (no tag defs indexed), ED substrates contribute nothing; EC substrates still run; no warning header is emitted.
- **R2590:** Connections docs carry a `@purpose` header: `curate` (default for `sys.findConnections`), `recall` (Phase 2B), or another consumer-chosen value. The curation workshop subscribes filtered to `@purpose: curate`.
- **R2591:** Connections docs carry a `@connections-mode` header: `normal` (this phase) or `turbo` (2C / 1G). `normal` is the default for `sys.findConnections`.
- **R2592:** Connections docs carry a `@proposal-count` header set on terminal `completed` transition with the number of proposal rows in the body.
- **R2593:** Proposal rows live under a single `## Proposals` heading; each row begins with `@proposal-kind: <kind>` selecting its variant (`tag-name`, `theme`, `shared-tag`).
- **R2594:** `@proposal-kind: tag-name` rows include `@proposal-value`, `@proposal-score`, `@proposal-evidence-chunks`, `@proposal-evidence-vector-ed`, `@proposal-evidence-trigram-ed`, `@proposal-evidence-vector-ec`, `@proposal-evidence-trigram-ec`, and `@proposal-motivating-files` (comma-separated `path:score`).
- **R2595:** `@proposal-kind: theme` rows include `@proposal-text` and `@proposal-evidence-chunks`, replacing the legacy `## Themes` section in turbo mode.
- **R2596:** `@proposal-kind: shared-tag` rows include `@proposal-tag`, `@proposal-value`, and `@proposal-evidence-chunks`, replacing the legacy `## Shared Tag Candidates` section in turbo mode.
- **R2597:** During the 1G migration window, the server emits **both** the new `## Proposals` rows and the legacy `## Themes` / `## Shared Tag Candidates` sections in turbo mode so either parser can read the doc; the duplication is removed once the Lua workshop migrates to the unified section.
- **R2598:** Normal-mode requests may transition `pending → completed` directly without an intermediate `working` state; the elapsed-ticker and progress-text updates from R2328 are skipped for normal mode.
- **R2599:** Internal pipeline failure (index read error, embedding mid-flight failure) flips the doc to `errored` with `@connections-error: <message>` via the existing write-actor path.
- **R2600:** `sys.findConnections` returns `(requestID, nil)` on success or `(nil, errstring)` on caller error (empty inputs, unknown chunk, path miss, range parse error) or agent unavailability for turbo mode.
- **R2601:** `opts.mode` defaults to `"normal"`. `opts.purpose` defaults to `"curate"`. `opts.timeoutSeconds` defaults to 30 and is clamped to `[5, 300]`.
- **R2602:** Normal-mode `sys.findConnections` calls never block the Lua VM; the call returns sub-millisecond after enqueueing.
- **R2603:** Turbo-mode `sys.findConnections` requires `ConnectionsAvailable()` to return true; otherwise returns `(nil, "agent unavailable")`.
- **R2604:** `ark connections find [options] <input>...` accepts mixed input types (decimal chunkID, `PATH:N-M` / `PATH:N`, quoted text) and submits a find request. Returns `tmp://connections/<id>.md` on stdout for shell composition.
- **R2605:** `ark connections find --wait` blocks until terminal status; on completion prints the doc body on stdout (markdown by default, JSON with `--json`).
- **R2606:** `ark connections wait <path> [--timeout SEC] [--json]` blocks until the given tmp:// connections doc reaches terminal status and prints the doc body. With `--timeout SEC`, exits non-zero and prints the last-seen status to stderr when the timeout elapses without a terminal transition.
- **R2607:** `ark connections show <path>` parses the persisted doc and projects structured fields without blocking. Supports `--status` (print only `@connections-status`), `--tags` (list tag-name proposals one per line), `--tag NAME` (filter to rows whose `@proposal-value` equals NAME), `--threshold N` (drop proposals below score N), and `--json` (JSON projection instead of markdown).
- **R2608:** `ark connections show <path>` is distinct from `ark fetch <path>` which dumps the raw doc body unparsed. `show` parses and projects.
- **R2609:** `ark connections list [--json]` lists in-flight requests (records still in the Librarian's `connectionsResults` map). Default output is a markdown table; `--json` emits a JSON array.
- **R2610:** All `ark connections` public subcommands (`find`, `wait`, `show`, `list`) require a running server and exit with `server not running` when none is detected.
- **R2611:** `ark connections sidecar-wait` is the lotto-tube drain for the turbo sidecar, replacing the previous `--wait` flag with identical semantics.
- **R2612:** `ark connections sidecar-fetch <id>` returns the chunk-content JSON array for the request, replacing `--fetch ID`.
- **R2613:** `ark connections sidecar-result <id>` reads result JSON from stdin and posts it to the server, replacing `--result ID`.
- **R2614:** `ark connections sidecar-error <id> <message>` posts an error message for the request, replacing `--error ID=MESSAGE`. The message is a positional argument, not a name-value pair.
- **R2615:** The removed `--wait`, `--fetch`, `--result`, `--error` flags produce a one-line hint pointing at the new subcommand name on stderr and exit with status 2.
- **R2616:** `ark connections find --type chunk|text` forces every positional input to a single category. `chunk` treats each token as a chunk reference, accepting decimal chunkIDs and `PATH:N` / `PATH:N-M` locators (shape still selects which); non-chunk-shaped tokens exit non-zero. `text` treats every token literally, including tokens that look like chunkIDs or `path:locator`. With `--type` omitted, each input is auto-detected: decimal → chunkID, `path:locator` → range, otherwise → text. No quoting is required for bare text. An unknown `--type` value exits non-zero.

## Feature: Recall (Tag Forge Phase 2B)
**Source:** specs/recall.md

- **R2617:** The `ark recall` CLI command and the `Librarian.Recall` Go API retrieve the top-K chunks from the corpus relevant to a given set of inputs.
- **R2618:** Inputs may be decimal chunkIDs, `PATH:N-M` / `PATH:N` locators, or bare text. Positional arguments are normalized into `ConnectionsInput` structures via the existing `normalizeInputs` function.
- **R2619:** The `--type chunk|text` CLI flag overrides auto-detection of input tokens, using the same categorization semantics as `ark connections find`.
- **R2620:** For each normalized input, the recall pipeline executes two content-EC substrate passes: Vector-EC (using `SearchChunks`) and Trigram-EC (using `SearchFuzzy`). The old tag-*definition* ED substrates (Vector-ED, Trigram-ED) remain omitted — but the tag axis is **not** absent: it contributes through the value→chunk TagVector/TagTrigram passes (R2905, R2906) added alongside these, so tags are a first-class part of the combined score.
- **~~R2621:~~** (Retired T93 — see R2643) Scores are normalized to `[0,1]`: Vector cosine scores are normalized via `(cos+1)/2`, and Trigram scores are normalized using microfts2's standard fuzzy match scoring.
- **R2622:** Similarity scores are merged per chunk: the overall chunk score is the `max` score across all input parameters and both active substrates.
- **R2623:** Self-chunk exclusion: any input that normalizes to a chunkID — whether passed directly as `{ChunkID: c}` or resolved from a `{Path, Range}` locator — must be excluded from its own recall results. Bare-text inputs do not trigger self-exclusion.
- **R2624:** For each retrieved chunk, the system resolves its metadata: chunkID, path, range, and tags (using `AllTagsForChunk` V-records).
- **R2625:** If `IncludeContent` (CLI flag `--no-content` is false) is true, the system reads and embeds the full content of the chunk from the chunk cache. If false, content is omitted.
- **R2626:** Results are sorted descending by the merged overall score, returning the top-K chunks.
- **R2627:** CLI supports options: `--k` (top-K chunks, default 20, clamped to `[1, 200]`), `--no-content` (sets `IncludeContent` to false), `--json` (emits JSON result), `--type` (auto, chunk, text).
- **R2628:** The Lua API exposes `sys.recall(inputs, opts)` which delegates to `Librarian.Recall` with the same input parsing and options.
- **R2629:** The HTTP server exposes `POST /recall` (mapped to `Librarian.HandleRecall`), parsing inputs and opts, invoking `Recall`, and returning the JSON result.
- **R2630:** If the server is running, the CLI proxies the `recall` command via HTTP/Unix socket to the server (`POST /recall`), using the warm model if configured.
- **R2631:** If the server is **not** running, the CLI checks `ark.toml` for a configured `[embedding] model`.
- **R2632:** If the server is not running and a model is configured in `ark.toml` (and the model file exists), the CLI exits non-zero with `error: server not running; model configured. Please start the server with: ark serve`.
- **R2633:** If the server is not running and **no** model is configured, the CLI opens the index database locally in-process via `withDB` in read-only mode and executes a local trigram-only recall query.
- **R2634:** If an embedding model is unavailable during execution (either locally during in-process execution, or on the server due to configuration or missing files), the Vector-EC pass is skipped, the Trigram-EC pass is run, and the result carries the warning `"embedding unavailable"`.
- **R2635:** By default, the CLI output is a markdown stencil starting with the `## Chunks` header. Each chunk is formatted with `@chunk-` markers (`@chunk-id`, `@chunk-path`, `@chunk-range`, `@chunk-score`, `@chunk-evidence-vector-ec`, `@chunk-evidence-trigram-ec`, `@chunk-tags`) followed by the content lines blockquoted using `> `.
- **R2636:** If no chunks match, the CLI output is `## Chunks\n\n_no results_`.
- **R2637:** If a warning is returned (e.g. embedding unavailable), the CLI prepends `@recall-warning: <warning>` above the `## Chunks` header.
- **R2638:** If the `--json` flag is provided, the CLI prints the raw JSON serialization of `RecallResult`.
- **R2639:** If the input list is empty after normalization, the recall query is rejected with `chunkIDs/text/range empty`.
- **R2640:** If a chunkID is passed but is unknown in the corpus, the query is rejected with `unknown chunk <id>`.
- **R2641:** The `K` option (default 20) is clamped to `[1, 200]`.
- **R2642:** `NewLibrarian` succeeds whether or not `claude` is on PATH; the constructor records availability and the `Available()` method reports whether claude-dependent operations (spectral expansion) are usable. Operations that don't require claude (recall, embed, substrate passes, tag embedding) work regardless of claude availability.
- **R2643:** Trigram-EC scoring uses Jaccard similarity over trigram sets, `score = |Tq ∩ Tc| / |Tq ∪ Tc|`, where `Tq` is the trigram set of the input text and `Tc` is the trigram set of the candidate chunk content (extracted via microfts2's UTF-8-aware trigram engine). This replaces the prior per-input `score / maxScore` normalization. Vector and trigram substrate scores are then directly comparable on a single `[0, 1]` scale. Applies to both Tag Forge Phase 2A's trigram-EC substrate pass and Phase 2B's recall.
- **R2644:** Before computing the full Jaccard score, the trigram-EC pass checks query-coverage `coverage = |Tq ∩ Tc| / |Tq|` and short-circuits to score `0` when `coverage < trigramCoverageFloor` (default `0.1`). The intersection used for the coverage check is reused for the Jaccard computation on survivors.
- **R2645:** When a chunk has tags that carry non-empty values, the markdown stencil emits one sub-list item per such tag under the chunk: `- @chunk-tag-value: <name>: <value>`. `@chunk-tags` carries names only (comma-separated). `@chunk-tag-value` is a legal ark tag whose value is the literal `<name>: <value>` text; an agent splits on the first `": "` to recover the original tag's name and value. Sub-items appear in the same order as the names in `@chunk-tags`.
- **R2646:** If the server is not running and `[embedding] model` is configured in `ark.toml` but the model file at `<arkDir>/<model>` is missing, the CLI exits non-zero with `error: configured embedding model not found at <PATH>`. Distinct from the "server not running; model configured" case (R2632), which assumes the file exists.
- **R2647:** The recall substrate's chunk-similarity pipeline drops candidate chunks with no V records (tagless chunks) during scoring, since they cannot contribute tag information to downstream tag-shaped recall. The `RecallOpts.KeepTagless` field defaults to false (drop tagless); setting it to true retains tagless chunks. The CLI exposes this as `ark connections recall -all`; the Lua bridge exposes `keepTagless`. The filter happens during admission to the scoring map so the substrate's top-K contract is honored against tagged candidates only (no padding fog). The tag lookup performed for the filter is cached on the per-chunk accumulator and reused during result enrichment.

## Feature: Discussed Tags (Recall dedup)
**Source:** specs/discussed-tags.md

- **R2648:** Per-session recall dedup state lives in the `RD` record class. Key: `"RD" + session-bytes + \x00 + tagname + \x00 + value`. Value: 8 bytes, unix nanoseconds as big-endian `uint64`, recording when the entry was written. session-bytes is variable-length (e.g. Claude Code session UUID) with no `\x00`; tagname and value follow the same no-`\x00` constraint as V records. A bare `@name` entry (no value) is encoded with an empty value segment, so the key ends `... + \x00 + tagname + \x00`.
- **R2649:** The single-letter prefix `R` is reserved as the recall-feature namespace; future recall records (emission log, per-session config, trigger state, etc.) take two-letter `R*` prefixes. The first occupant is `RD`.
- **R2650:** `ark discussed add --session SID @tag[:value] [@tag[:value] ...]` writes one RD record per tag argument, stamped with `NOW`. Re-adding an existing `(session, tag, value)` overwrites the timestamp. Exits non-zero if `--session` is absent, the session value is empty, or no tag arguments were given.
- **R2651:** `ark discussed list --session SID` range-scans `"RD" + session-bytes + \x00`, drops entries whose `timestamp + TTL < NOW` (lazy expiry), and prints one tag per line as `@name` (bare value) or `@name: value` (non-empty value). `--since DUR` keeps only entries newer than `NOW - DUR`. `--json` emits `[{"tag":..., "value":..., "timestamp": RFC3339}, ...]`.
- **R2652:** `ark discussed clear --session SID` deletes every RD record under one session and leaves other sessions intact.
- **R2653:** `ark discussed prune [--ttl DUR]` sweeps RD records across all sessions and drops entries older than the cutoff. Without `--ttl`, the cutoff is the configured TTL (R2657). An invalid `--ttl` value exits non-zero before any deletion happens.
- **R2654:** Tag arguments in the `ark discussed` family use ark tag syntax: bare `@name` (no value), `@name:value` (exact pair). Names and values cannot contain `\x00`.
- **R2655:** `ark connections recall --session SID` reads the session's RD records, drops expired entries, and adds the surviving `(tag, value)` pairs to the recall pass's exclusion set. `ark connections recall --discussed @t1[:v1],@t2[:v2],...` parses the comma-separated tag expressions and adds them to the same exclusion set. When both flags are present, the exclusion set is the union.
- **R2656:** The discussed filter is permissive per chunk: for each candidate chunk, strip any of its tags whose `(name, value)` is in the exclusion set, and drop the chunk only if its tag list becomes empty after stripping. A chunk that retains at least one non-discussed tag survives.
- **R2657:** Exclusion-set membership treats a bare `@name` entry as matching any value under that name (the chunk loses every `@name:*` pair it carries). An `@name:value` entry matches only the exact pair.
- **R2658:** The discussed filter runs before the `requireTags` / `-all` decision in the recall substrate. `-all` (R2647) does not override the discussed exclusion — a chunk emptied by `--session`/`--discussed` is dropped regardless of `-all`.
- **R2659:** Default discussed-tag TTL is 24 hours. The `[recall].discussed_ttl` field in `ark.toml` overrides the default and accepts any Go duration string. `"0"` means never expire. Records are not deleted on TTL boundaries; expiry is computed lazily on read (R2651, R2655).
- **R2660:** The Go API exposes `RecallOpts.Discussed []Discussed`, where `Discussed{Tag, Value}` carries one exclusion-set entry; `Value == ""` means bare-name match (R2657). An empty slice disables the filter. The Lua bridge exposes the same shape via `sys.recall(inputs, {discussed = {{tag=..., value=...}}, ...})`.
- **R2661:** The Lua `sys.discussed` table exposes four methods that mirror the CLI verbs: `add(session, tags...)`, `list(session, opts)`, `clear(session)`, `prune(opts)`.
- **R2662:** The recall agent writes RD records via `ark discussed add` after emitting a batch of tag suggestions for a target session — mark-all-N policy (every emitted tag is marked discussed, whether the user engages with it or not). The substrate itself does not write RD records.
- **R2663:** An unparseable `[recall].discussed_ttl` value in `ark.toml` falls back to the 24h default and the server logs a warning at startup. An RD record whose value is not exactly 8 bytes is treated as expired and lazy-skipped on read; the writer never produces malformed values, so this path exists only to keep readers robust.


## Feature: Derived Tags (statistical attach-proposal pass)
**Source:** specs/derived-tags.md

- **~~R2664:~~** (Retired T240 — see R3058) The `RC` record class (Recall Candidate) stores derived attach proposals. Key: `"RC" + chunkid varint + tagname`. Value: 8 bytes, big-endian `uint64` tally counter. One record per (chunkid, tagname) candidate; tagname follows the standard `[\w][\w\-.]*` grammar and contains no control bytes.
- **~~R2665:~~** (Retired T241 — see R3059) The `RJ` record class (Recall reJection) marks (chunkid, tagname) pairs the curator has rejected. Key: `"RJ" + chunkid varint + tagname` — mirrors RC. Value: 8 bytes, big-endian `uint64` unix nanoseconds (rejection timestamp; the presence of the record is what blocks re-proposal, not the timestamp value).
- **~~R2666:~~** (Retired T256 — no replacement) The `RF` record class (Recall Freshness) stamps each chunk with the max `RecordSerial(ED, *)` observed at its last derivation pass. Key: `"RF" + chunkid varint`. Value: varint `uint64` — the txn serial that was "current" against the ED record set when this chunk was last processed.
- **R2667:** `ark connections recall --propose` (default false) runs the compute-for-display derivation pass (R3079) alongside the substrate's chunk-scoring pass and surfaces **this call's** computed proposals per surfaced chunk in the result stencil (R2684, R2685, R2686); it persists nothing. Chunks that aren't surfaced (tagless chunks without `-all`) are still processed by the pass (R2668) but their computed proposals are neither surfaced nor written. `--propose` does not change which chunks appear in the surfaced output — `-all` still controls that.
- **R2668:** When `RecallOpts.Propose` is true, the substrate internally runs with `KeepTagless=true` so the derivation pass sees the full scored chunk set including tagless chunks. The caller's surfacing filter (default drop, `-all` keep) is applied as a separate step to the result stencil; the derivation pass's chunk set is orthogonal to the caller's `KeepTagless` value.
- **~~R2669:~~** (Retired T257 — no replacement) For each chunk in the derivation chunk set, the pass reads `RF[chunkid]` (treating absent as serial 0) and computes `maxED = max RecordSerial(ED, *)` for the batch. If `RF[chunkid] >= maxED`, the chunk is skipped for derivation. Otherwise the pass derives candidates and writes `RF[chunkid] = maxED` after processing (with or without resulting proposals).
- **R2670:** Candidate generation per chunk: cosine-compare the chunk's EC vector against every ED record's vector and take the top-N by similarity, where N is `derivationK` (default 10).
- **R2671:** Already-attached filter: each candidate is dropped if the chunk already carries an F record for the candidate's tagname.
- **R2672:** External-tag exclusion (bare-name rule): each candidate is dropped if its tagname matches any ext-routed tagname on the chunk (value-agnostic). Authority routed via `@ext` is not shadowed by derived proposals.
- **~~R2673:~~** (Retired T242 — see R3070) Rejection filter: each candidate is dropped if `RJ[chunkid + tagname]` exists. The substrate never re-proposes a previously rejected (chunkid, tagname).
- **~~R2674:~~** (Retired T243 — see R3075) RC tally: for each surviving candidate, if `RC[chunkid + tagname]` exists the tally is incremented by 1; otherwise the record is written with tally `1`.
- **~~R2675:~~** (Retired T258 — no replacement) All RC and RF writes produced by one recall call are committed in a single batched write transaction through the write actor.
- **R2676:** `--propose` without `[embedding] model` configured: derivation is silently skipped (no ED records to score against); the caller's recall result is unaffected.
- **R2677:** The Go API exposes `RecallOpts.Propose bool`. The Lua bridge exposes the same shape via `sys.recall(inputs, {propose = true, ...})`.
- **~~R2678:~~** (Retired T244 — see R3067) `Store.DerivedProposals(chunkID uint64) ([]DerivedProposal, error)` returns all RC records for a chunk sorted by tally descending. `DerivedProposal{ChunkID, Tagname, Tally}` carries each entry. The reader excludes any (chunkid, tagname) shadowed by an RJ record as defense-in-depth against pre-rejection RC records.
- **~~R2679:~~** (Retired T245 — see R3071) `Store.AcceptDerived(chunkID uint64, tagname, value string) (uint64, error)` atomically deletes `RC[chunkid + tagname]` and attaches the (tag, value) to the chunk via the existing F/V tag-attach path. Returns the resolved tvid for the (tag, value) pair. An empty `value` produces a bare-tag attach.
- **~~R2680:~~** (Retired T246 — see R3069) `Store.RejectDerived(chunkID uint64, tagname string) error` atomically deletes `RC[chunkid + tagname]` and writes `RJ[chunkid + tagname]` with `NOW` unix nanoseconds.
- **R2681:** Robustness: an RC record whose value is not a valid varint tally (R3058) is treated as tally `0` on read (the next derivation self-corrects). The writer never produces malformed values; this path exists only to keep the forge-facing reader (R3067) robust. (The RF-read robustness clause is retired with RF — R2666/R2669.)
- **~~R2682:~~** (Retired T259 — no replacement) RF cleanup is lazy — missing RF records are treated as "stale, process this chunk." RF records for chunkids orphaned by microfts2 are cleaned up alongside EC and F records via the existing chunkid-orphan callback path; the derivation pass tolerates missing RF unconditionally.
- **~~R2683:~~** (Retired T134 — see R2881) RJ records are sticky in v1 — no TTL, no `ark derived unreject` verb. The substrate never deletes an RJ record.
- **R2684:** When `--propose` is set, the markdown stencil emits a `@chunk-proposed-tags: name1, name2, ...` line after `@chunk-tags` for each surfaced chunk that has at least one RC record. Comma-separated bare tagnames, parallel to `@chunk-tags`. The line is omitted (not emitted empty) for surfaced chunks with no RC records. The list draws from the accumulated RC record set for the chunk (this call's emissions plus prior calls' proposals not yet accepted or rejected) — not just survivors of this pass.
- **R2685:** `@chunk-proposed-tags` order is by chunk-EC-to-tag-ED cosine similarity descending. Similarity is computed at stencil-emission time: for fresh chunks (derivation skipped), per-RC cosine is bounded by the chunk's RC count; for derived chunks, the scores from the pass are reused. A tag's similarity is the maximum cosine across its ED records (one per def file).
- **R2686:** The Go `RecalledChunk` type gains `ProposedTags []string` with JSON tag `proposedTags,omitempty` — populated only when `Propose=true` and the chunk has RC records, ordered the same as the stencil (similarity desc). The Lua `sys.recall` result mirrors the same field.


## Feature: Simple Recall Watcher
**Source:** specs/simple-recall.md

- **R2687:** Simple Recall is a built-in subsystem of `ark serve`, controlled entirely through the `[recall]` section in `ark.toml`. There is no CLI flag, environment variable, or sidecar process to enable or disable it.
- **R2688:** `[recall].enabled` (bool, default `false`) is the master switch. When `false`, the watcher does not register any chunk-append callbacks and does not emit DMs regardless of source activity.
- **R2689:** `[recall].propose` (bool, default `true`) controls whether the watcher passes `propose = true` to the recall substrate. When `false`, RC records are not accumulated and `@chunk-proposed-tags` lines are not emitted.
- **R2690:** `[recall].min_similarity` (float, default `0.65`) is the Layer-2 hard filter. The watcher drops the trigger without emitting a DM when the top recalled chunk's aggregate similarity score is strictly below this threshold.
- **~~R2691:~~** (Retired T94 — no replacement) `[recall].cooldown_seconds` (int, default `60`) is the Layer-2 cooldown. The watcher drops the trigger without emitting a DM when fewer than `cooldown_seconds` have elapsed since the last DM emitted to the same recipient session.
- **R2692:** `[recall].chunks_per_dm` (int, default `5`) caps the number of recalled chunks included in each DM body. The watcher truncates the substrate's top-K result to this size.
- **R2693:** `[recall].sources` (string array, default `[]`) is an optional whitelist of source root directories (matching `Source.Dir` in `ark.toml`). When non-empty, only sources whose root directory is in the list and whose chunker strategy is `chat-jsonl` qualify. When empty, every source whose strategy is `chat-jsonl` qualifies. Source-root matching uses the registered `Source.Dir` exactly; `SourceRootForPath` resolves a chunk's path to its source root before comparison.
- **~~R2694:~~** (Retired T106 — no replacement) `[recall].agent_cmd` is reserved for the deferred agent-layer follow-up (ARK-STATE item 10). v1 does not consume the field; setting it has no effect.
- **R2695:** `ark serve` reads `[recall]` on startup and on the existing live config-reload path. Toggling `enabled` or adjusting any other `[recall]` knob does not require a restart.
- **R2696:** A source qualifies for the watcher when its chunker strategy is `chat-jsonl` and (when `[recall].sources` is non-empty) the chunk's source root (per `Config.SourceRootForPath`) appears in that list.
- **~~R2697:~~** (Retired T95 — see R2734) The watcher triggers once per committed chunk on a qualifying source, regardless of chunk role (user, assistant, tool). The substrate's per-chunk cost is the only built-in throttle; volume-based throttling is `cooldown_seconds`.
- **R2698:** Cold start is go-forward only. When the watcher starts or is enabled mid-session, it does not backfill any prior chunks; on-demand recall via `ark connections recall` remains the path for catch-up.
- **R2699:** Self-exclusion is inherited from the recall substrate (R2645 / substrate input-chunk skip). The watcher does not implement an additional self-exclusion check.
- **~~R2700:~~** (Retired T102 — see R2747) The watcher emits each DM through the same in-process compose function the `ark message dm` CLI subcommand uses. The compose function owns the head-of-chunk tag block; the watcher supplies recipient, subject, sender identity, and body.
- **~~R2701:~~** (Retired T103 — see R2748) Each watcher-emitted DM has recipient = the originating Claude Code session UUID (derived from the JSONL filename), subject = `recall`, and sender identity = `ARK-RECALL` via `--from-service`.
- **~~R2702:~~** (Retired T96 — see R2748) The watcher's DM body begins with an `@ark-recall-fire: <ref>` line, where `<ref>` is the chunkID of the `turn_duration` record that triggered the fire when the chunker indexed that line, or a Unix-nanosecond timestamp when it didn't. Because the dm chunk format has no blank-line separator between the compose-emitted tag block and the body, the line is contiguous with `@dm` and `@from-service` and the chunker reads all three as the chunk's head tag block. Receivers and tools can correlate DMs with their triggering turn via `ark search @ark-recall-fire:<ref>`.
- **~~R2703:~~** (Retired T97 — no replacement) The DM body includes an instruction block headed by `## What this is` and `## What to do with it`, containing bias-to-silence guidance and instructions for handling derived-tag candidates via `ark connections recall accept-derived` / `reject-derived`. The block's exact text is the v1 default and lives in the watcher's emit code; updates land with watcher code changes.
- **~~R2704:~~** (Retired T98 — see R2749) The DM body emits one `## Recalled for chunk <chunkID>` section per input chunk that produced at least one recalled chunk above the similarity gate. Each section opens with a blockquoted excerpt of the input chunk's text (capped at ~200 chars) so the receiving agent sees what triggered that section, followed by an `### Recalled chunks` body using the existing `ark connections recall` markdown stencil shape (per chunk: `@chunk-id`, `@chunk-path`, `@chunk-range`, `@chunk-score`, per-substrate evidence, `@chunk-tags`, optionally `@chunk-proposed-tags`, tag-value sub-list items, and a blockquoted excerpt).
- **R2705:** Each recalled chunk's excerpt is bounded at approximately 500 characters before composition. The substrate's `Content` field is truncated by the watcher; the substrate itself is unchanged.
- **R2706:** `@chunk-proposed-tags` is emitted on chunks with at least one RC record (per R2684) when `[recall].propose = true`.
- **~~R2707:~~** (Retired T99 — no replacement) Layer 1 quality bar: the DM uses `@dm: <session>: recall` so the receiving agent can pre-triage on the `## @dm: <session>: recall` heading `ark listen` renders without reading the body.
- **R2708:** Layer 2 quality bar: when an input chunk's top recalled chunk is below `[recall].min_similarity`, that input's section is dropped from the DM body silently (no per-section emission, no RD records for that section's would-be chunks). When every input's section drops, no DM is emitted at all — the fire silently completes with `pendingChunks` cleared. Turn-boundary firing (R2734) is the per-session rate limit; no separate cooldown knob is necessary.
- **~~R2709:~~** (Retired T100 — no replacement) Layer 3 quality bar: the DM body's instruction block opens with explicit "default to silence" guidance and grants the receiving agent permission to drop the DM without acknowledgement.
- **~~R2710:~~** (Retired T101 — see R2763) Layer 4 quality bar: the receiving agent appends `@ark-recall-acted: surfaced|dropped|skipped` to the DM doc. The watcher logs the per-session rolling rate via `ark serve`'s existing log pipeline. Auto-calibration (auto-ratchet of `min_similarity`) is deferred until logs accumulate; v1 emits only the instrumentation.
- **R2711:** When the watcher composes a DM, it writes RD records (per R2648, R2659) for the inline and ext-routed tags of every surfaced chunk, scoped to the originating session UUID. Mark-on-send: RD records are written at composition time regardless of whether the receiving agent later acts on the DM.
- **R2712:** RC-derived candidates listed in `@chunk-proposed-tags` do not receive RD records — only tags actually attached to the chunk (inline + ext) do. The agent's accept/reject action writes V or RJ records (per R2679 / R2680); the watcher's mark-on-send does not pre-empt that.
- **R2713:** The watcher emits structured log entries at each decision point via `ark serve`'s existing log pipeline: `armed` (turn_duration seen, pendingChunks count, activation_delay), `disarmed` (user record seen, pendingChunks count carried forward), `fired` (timer expired; pendingChunks count, sections-emitted, sections-dropped-below-threshold, total RD records written), and `recall-error` (substrate or DM-emit failure; logged unconditionally regardless of verbosity).
- **R2714:** The watcher does not check for active subscribers before emitting. A DM persists in tmp:// memory whether or not a listener is attached; the receiving session is responsible for its own `ark listen --session <self>` (or equivalent) setup.
- **R2715:** v1 watcher non-features (deliberately deferred to ARK-STATE item 10 or later): no LLM inside the watcher (deterministic on inputs), no new-tag-definition proposals (RP/RPE/RR records are not written by the watcher), no tag-axis filtering, no cold-start backfill, and no subscriber liveness check.


## Feature: Direct Messages — `@dm` grammar, `@from-service`, and `dm` subcommand
**Source:** specs/messaging.md

- **R2716:** The `@dm` tag value grammar is `RECIPIENT[ RECIPIENT2 ...][: SUBJECT]`: one or more whitespace-separated recipient tokens, optionally followed by `: SUBJECT` for a freeform subject. The single-recipient form (`@dm: <token>`) is unchanged from prior usage.
- **R2717:** Parsers split a `@dm` value on the first `: ` (colon-space) substring: characters before are the recipient list, characters after (when present) are the subject. The subject itself may contain `: ` — the split is non-greedy.
- **R2718:** Within a `@dm` value's recipient segment, whitespace runs delimit individual recipient tokens. Each token is opaque to the grammar (typically a Claude Code session UUID, but any non-whitespace string a subscriber can match against is legal).
- **R2719:** A `@dm` value that ends with `:` and no subject text after it (e.g. `@dm: foo:`) is rejected by the `ark message dm` subcommand at write time.
- **R2720:** The `@from-service` tag identifies a message emitted by an ark internal subsystem. Values follow the `ARK-<SUBSYSTEM>` shape (e.g. `ARK-RECALL`). Each emitting subsystem owns its own identity; service identities are not shared umbrellas.
- **R2721:** `@from-service` and `@from-project` are mutually exclusive on the same message. A message either originates from a user-facing project (`@from-project`) or from an ark internal subsystem (`@from-service`); never both.
- **R2722:** `ark message dm` gains the `--from-service NAME` flag, mutually exclusive with the existing `--from SESSION`. Exactly one of the two must be present; supplying both or neither exits non-zero before any write.
- **R2723:** With `--from-service NAME`, the emitted head-of-chunk tag block contains `@from-service: NAME` in place of `@from: <session>`. The remainder of the tag block (`@dm`, optional `@ref`) is unchanged.
- **R2724:** With `--from-service NAME`, the tmp:// destination is `tmp://NAME/dm-<to0>`, where `<to0>` is the first `--to` recipient. With `--from SESSION`, the destination is `tmp://SESSION/dm-<to0>` (the existing behavior, generalized for multi-recipient).
- **R2725:** `ark message dm --to RECIPIENT` is repeatable. The compose function joins recipients with a single space in the order supplied: `--to A --to B --to C` produces `@dm: A B C`.
- **R2726:** `ark message dm --subject TEXT` is optional. When present, the compose function appends `: TEXT` after the recipient list. Empty `TEXT` (whitespace only or zero-length) exits non-zero before any write.
- **R2727:** The internal compose function used by `ark message dm` is exposed as a Go-callable path that in-process callers (the simple-recall watcher in R2700) invoke directly, without shelling out. The CLI and the in-process path produce identical tag blocks and tmp:// writes for the same inputs.


## Feature: Simple Recall Watcher — turn-boundary pipeline
**Source:** specs/simple-recall.md

- **R2728:** `[recall].activation_delay` (int, default `15`) is the number of seconds the watcher waits after observing a `turn_duration` record before firing the recall pass. A user record arriving inside this window cancels the timer entirely (per R2733).
- **R2729:** The indexer hook the watcher exposes is `OnAppend(path, strategy, newBytes, addedChunkIDs)`, called from `executeRefresh`'s isAppend branch. `newBytes` is the raw appended content the indexer already has in hand; `addedChunkIDs` is the set of chunkIDs the chunker emitted for that append. The watcher does its trigger-detection scan against `newBytes` (independent of whether the chunker indexed the triggering line) and tracks `addedChunkIDs` for later turn extraction.
- **R2730:** The watcher maintains per-session state covering at minimum (a) `pendingChunks`, an ordered slice of chunkIDs accumulated since the last fire, (b) `pendingTimer`, the `time.Timer` handle armed when a `turn_duration` record is seen, and (c) `armReady`, a flag that gates arming to once per user turn (R2734). The map is mutex-protected.
- **R2731:** The watcher detects `turn_duration` records by line-by-line JSON parsing of `newBytes`. A line whose top-level JSON object has `"type":"system"` and `"subtype":"turn_duration"` is a turn-end marker. Substring scanning is insufficient (e.g. `userType` would match `"type":"user"`); the parser inspects top-level fields only.
- **R2732:** The watcher detects *genuine* user records by the same line-by-line JSON parse. A line whose top-level `"type"` is `"user"` is a user record, but it counts as a genuine human message — the arming signal for R2733/R2734 — only when (a) its `message.content` is a JSON string (tool-results are arrays of `tool_result` parts) **and** (b) it carries the human origin marker `origin.kind == "human"` (Claude Code stamps a genuine typed turn with `origin.kind: "human"`; tool-results, injected user-records such as background-task completions [`origin.kind: "task-notification"`], and local-command caveats [no origin at all] all lack that marker — so absence of an origin is *not* a genuine signal, and detection keys on the positive marker per R3009). Tool-results and notification-driven wake turns are therefore *not* user signals. This is what stops the recall ping-pong: a consumer's own surfacing turn, woken by a `type:"user"` notification, no longer re-arms the watcher.
- **R3009:** Genuine-user detection (R2732) keys on the *positive* `origin.kind == "human"` marker, never on the *absence* of an origin — local-command caveats and tool-results also lack an origin, so absence is not a genuine signal. Because Anthropic does not publish the transcript format, this is deliberately conservative: a positive allowlist of the single known human marker. If that marker ever changes, arming goes quiet and the `-vv` `turn_duration ignored, no user turn since last arm` log is the drift tripwire. The mirror helper `userProse` (conversation injection, R2891) applies the same rule so retired-origin turns are not dropped from the injected context.
- **R2733:** A genuine user record (R2732) cancels any currently-armed `pendingTimer` for the session **and sets `armReady`, (re-)enabling arming for the next `turn_duration`** (R2734). (Tool-results and harness notifications are not genuine user records, so they neither cancel nor re-enable.) `pendingChunks` is *not* cleared by a user record — accumulated chunks roll forward into the next fire so multi-turn engagement is processed as one recall pass when the user eventually pauses.
- **R2734:** A `turn_duration` record arms `pendingTimer` for the session at `activation_delay` seconds **only when `armReady` is set** — i.e. once per user turn; arming clears `armReady`. A subsequent `turn_duration` with no intervening user record (an agent-only turn, e.g. an assistant surfacing recall) does **not** re-arm, which stops the watcher re-triggering on its own consumers' output (the recall ping-pong). If `armReady` is set and a timer is already armed, the new arming replaces the previous deadline (the most recent turn_duration within the same user turn wins).
- **R2735:** When `pendingTimer` expires, the watcher takes a snapshot of `pendingChunks`, clears the slice, releases the per-session lock, and runs the recall pipeline on the snapshot. Concurrent OnAppend calls during the pipeline run accumulate into fresh `pendingChunks` for the next fire.
- **R2736:** For each chunkID in the snapshot, the watcher fetches the chunk's extracted text via `db.ChunkTextByID`, runs it through `microfts2.MarkdownChunker{}.Chunks` to split it into paragraphs, and runs one `librarian.Recall` call per paragraph whose UTF-8 byte length is ≥ 30. Each call uses `[]ConnectionsInput{{Text: paragraph}}` and `RecallOpts{K: chunks_per_dm, IncludeContent: true, Session: sessionID, Propose: cfg.EffectivePropose()}`. Text-input is required (not ChunkID) so the substrate embeds on the fly via the warm model — fresh JSONL chunks don't yet have EC records when the watcher fires, and ChunkID-input would degrade silently to trigram-only. Paragraphs shorter than 30 bytes are skipped (signal-to-noise floor; tuned down from an initial 50 after the 50-byte floor was observed dropping genuine short user claims like "the rake persona specializes as a poker coach"); the count is logged in the `fired` log line as `skipped-short`.
- **~~R2737:~~** (Retired T104 — see R2749) The DM body is grouped: one `## Recalled for paragraph` section per paragraph whose Recall result clears the per-section similarity gate (R2708). Each section carries an `@source-chunk: <cid>` tag identifying the originating JSONL chunk for traceability. Sections appear in the order they were produced (outer iteration over `pendingChunks`, inner iteration over the markdown chunker's paragraphs). Across all sections, the substrate stencil shape from R2704 is used.
- **~~R2738:~~** (Retired T105 — see R2749) Each section opens with a blockquoted excerpt of the paragraph that triggered it, capped at approximately 200 bytes (UTF-8-safe truncation), so the receiving agent can read what triggered each section without scrolling into the recalled chunks below. The `@source-chunk` tag (R2737) provides the path back to the JSONL chunk the paragraph came from.
- **R2739:** A section is included in the DM body only when its top recalled chunk's aggregate similarity score is ≥ `[recall].min_similarity`. Sections below the gate are dropped silently. When every input's section drops, no DM is emitted; `pendingChunks` is cleared regardless (the fire completed successfully — there was just nothing to surface).
- **R2740:** The mark-on-send RD writes from R2711 apply across all surfaced sections: for every recalled chunk listed in any emitted section, the watcher writes RD records for the chunk's inline + ext-routed tags scoped to the originating session. R2712 still holds — derived-tag candidates listed in `@chunk-proposed-tags` do not receive RD records.
- **R2741:** The watcher's source-qualification gate (R2696) is checked at `OnAppend` entry, before any accumulation or timer arming. Appends from non-qualifying sources are ignored entirely; their chunkIDs do not accumulate and their `newBytes` is not scanned.
- **R2742:** The propose pass applies a chunk-EC ↔ tag-ED cosine floor before the top-K cut in `selectCandidates`. Candidates whose per-tag max cosine is below `[recall].min_propose_similarity` (default 0.55) are dropped and never written as RC records. The floor sits ahead of already-attached and rejected filters but is otherwise independent of them. Configured per-server; live config reload picks up changes on the next pass.
- **R2743:** The recall stencil renders `@chunk-proposed-tags` with parenthesized cosine scores: `tagname (0.NN)`, comma-separated, ordering preserved from `enrichProposedTags`. `RecalledChunk.ProposedTagScores` parallels `ProposedTags` (same length, same index alignment) and is surfaced through both the CLI stencil (`RenderRecallChunks`) and the Lua bridge (`sys.recall` result `chunk.proposedTagScores`).
- **R2744:** `ark connections clean [-all] [-session ID|project]` wipes recall-substrate state for testing/reset. Default scope wipes RC (every derived-tag proposal across the corpus) and RD (every discussed-tag dedup record). `-all` also wipes RF (derivation freshness stamps), RJ (curator rejections — normally durable, exposed under `-all` to support full reset), and removes every `tmp://connections/*` and `tmp://ARK-RECALL/*` document. `-session ID` restricts RD scope to one session UUID; `-session project` enumerates `<session>.jsonl` under `~/.claude/projects/<encoded-cwd>/` and restricts RD scope to those session UUIDs (encoded-cwd replaces every `/` with `-`). RC, RF, RJ, and tmp:// scope are always corpus-wide. All deletions route through the write actor via `SyncVoid` regardless of mode. With a running server, the CLI proxies `POST /connections/clean` (JSON body `{"sessions": [...], "all": bool, "checkpoint": bool}`); without one, the CLI opens the DB directly via `withDB` and runs the same deletions in-process. Since `tmp://` documents are server-process-local, the offline path reports zero for `tmp-connections` / `tmp-recall` — there's nothing to remove when the server isn't running.
- **R2746:** The recall watcher passes `RecallOpts.KeepTagless: true` so the surfacing layer does not drop tagless chunks from the DM. Without this, persona files, design docs, and other prose-heavy content (chunks whose `@chunk-tags` line would be empty) are admitted to the propose pass for derivation but filtered from the surfaced result — the watcher would compute tag candidates for `rake-v1.md` but never tell the agent that `rake-v1.md` is relevant. For ambient recall the body of the corpus is the signal; tagless does not mean uninteresting. The propose pass still runs on these chunks, so the curating agent sees both the chunk content and any derived-tag candidates that cleared `min_propose_similarity`.
- **R2745:** The `-checkpoint` flag on `ark connections clean` advances the indexer's stored FileLength to current on-disk size for every chat-jsonl file indexed under `~/.claude/projects/` (filtered by the same `Sessions` list as RD scoping — empty = every session, otherwise only files whose basename matches `<UUID>.jsonl` for one of the listed UUIDs). Implemented as `microfts2.DB.SetFileLength(fileid, length)`, which rewrites only the F-record's FileLength while preserving Chunks/Tokens/ContentHash/ModTime. Routes through the write actor via `db.CheckpointFile`. After a successful checkpoint the next indexer refresh sees `len(data) == info.FileLength`, so `prep.isAppend` is false and the recall watcher's `OnAppend` hook does not fire for the historical content — the cap. The server-side handler also calls `recallWatcher.ClearPending(sessions)` to drop any in-memory `pendingChunks` for affected sessions; the offline path skips this since no watcher exists.
- **R2869:** When building the curation doc, the watcher classifies each candidate chunk by its source path: a candidate whose path resolves to the originating session's own JSONL (`sessionFromJSONLPath(candidate.path) == <session>`) is emitted with `tag-only: true` in its `## Candidate:` block; all other candidates omit the marker. A `tag-only` candidate is kept for tag recommendation but excluded from surfacing — the originating conversation is already in the reader's live context, so surfacing it is redundant with the model's attention. Candidates in another session's JSONL or in external files are surfaceable ("remember when we talked about…"). This file-scoped rule is in addition to the substrate's exact-input-chunk self-exclusion (A66).


## Feature: Simple Recall — agent layer (v2)
**Source:** specs/simple-recall.md

- **R2747:** Recall v2 introduces two new tmp:// document paths. The **curation doc** path is `tmp://ARK-RECALL/curation-<originating-session-uuid>-<fire>`; the **result doc** path is `tmp://ARK-RECALL/result-<originating-session-uuid>-<fire>`. The `<session>` segment is load-bearing: the fire counter is per-session (R2901), so the `<fire>` integer alone is not unique across sessions — `<session>-<fire>` is what disambiguates the doc and keys the in-flight builder map.
- **R2748:** The curation doc carries two head-of-chunk tags on consecutive lines with no blank line before the first body section: `@ark-recall-curate: <originating-session-uuid>` followed by `@ark-recall-fire: <fire>`. These tags replace the v1 watcher's `@dm:` / `@from-service:` / `@ark-recall-fire:` head block.
- **~~R2749:~~** (Retired T137 — see R2898) The curation doc body emits one `# Source Chunk: <jsonl-chunkid>` H1 per paragraph whose Recall result cleared `[recall].min_similarity`. Each H1 is followed by a `> `-quoted excerpt of the source paragraph (UTF-8-safe ~200-byte cap), then 1..K `## Candidate: <chunkid> (<size>) <path>:<range>` H2s ordered by aggregate similarity score descending. The `<size>` is the chunk's full byte length (pre-truncation) in the same friendly format the result-doc Surface H2 uses (R2751) so the agent can factor fetch cost into its surface judgment. Each candidate H2 carries three bullet lines — `- score: <0.NN>`, `- tags: <comma-separated bare tagnames>`, `- proposed-tags: <name> (<0.NN>), ...` (omitted when no surviving proposals) — and a fenced ~500-character excerpt of the chunk content.
- **R2750:** The result doc header is a single tag line `@ark-recall-result: <originating-session-uuid>`. The result doc omits `@ark-recall-fire`; the assistant correlates a result event with its triggering fire via the pubsub event path rather than the result-doc body.
- **~~R2751:~~** (Retired T138 — see R2899) The result doc body emits zero or more sibling H2s in the order the recall agent emitted them. `## Surface: <chunkid> (<size>) <path>:<range>` recommends showing a chunk to the user; it carries the chunkID, a friendly byte-size label (so the assistant can decide whether to fetch — some chunks are tens of kB), and the chunk's `<path>:<range>` resolved server-side via `db.ChunkInfo` (mirroring the curation-doc `## Candidate:` line, R2749) so the consuming assistant can prune by file path — dropping surfaces for code already in its context — without resolving every chunk first. Size format: `<N>b` for under 1000 bytes, `<N>K` for 1–999 KB (decimal), `<N.N>M` for ≥ 1 MB. `## Recommend: @<tag>[:<value>] on <chunkid> <path>:<range>` proposes a tag attach worth re-curating; it carries the same `<path>:<range>` because a recommend can reference a chunk that was never surfaced and the assistant needs the path to judge it. Each H2 is followed by `reason: <one-line justification>` on the next line. Full chunk content is still resolved on demand via `ark chunks <chunkid>`. When the agent emitted no surface or recommend items, no result doc is written (per R2758).
- **~~R2752:~~** (Retired T139 — see R2901) The fire number is a globally monotonic integer counter scoped to one `ark serve` process lifetime, starting at 0 and allocated by the watcher at the moment `pendingTimer` expires. The same fire number ties the curation and result docs for that turn. No persistence is required — `tmp://` is per-process, so in-flight fires don't survive a restart and the counter resets cleanly.
- **R2753:** When at least one paragraph clears `[recall].min_similarity`, the watcher writes the curation doc via the Go-internal `RecallCurationBuilder` (no CLI roundtrip). When zero paragraphs clear, no curation doc is written; `pendingChunks` is cleared regardless and the fire completes silently.
- **R2754:** The curation builder API is Go-only: `db.RecallCurationOpen(session, fire)` returns a builder; `b.Section(sourceChunkID, sourceParagraphText)` opens a `# Source Chunk:` H1 with its blockquoted excerpt; `b.Candidate(chunkID, path, rangeLabel, score, tagNames, proposedTagsWithScores, contentExcerpt)` appends a `## Candidate:` H2 with its bullet block and fenced excerpt; `b.Close()` writes the tmp:// doc.
- **R2755:** `ark connections recall reserve-nonce` returns the next monotonic integer from an in-memory counter per `ark serve` run, starting at `1` and incrementing on each call. The counter resets on `ark serve` restart. Output is the integer followed by a newline on stdout.
- **~~R2756:~~** (Retired T140 — see R2900) `ark connections recall surface FIRE -chunk N -reason TEXT` appends exactly one `## Surface: <chunkid> (<size>)` H2 to the result-doc builder for FIRE, implicitly opening the builder on first call. The server-side handler looks up the chunk's byte size via `db.ChunkTextByID` and stamps it in friendly form (per R2751) so the assistant can scan for fetch cost. One item per invocation. Missing or empty required flag values exit non-zero before any state mutation. Path / range / context are resolved on demand via `ark chunks <chunkid>`.
- **R2757:** `ark connections recall recommend FIRE -chunk N -tag @t[:v] -reason TEXT` appends exactly one `## Recommend:` H2 to the result-doc builder for FIRE with identical open-on-first-call and one-per-invocation semantics as `surface` (R2756).
- **R3111:** The `## Recommend:` line writes its tag **back-quoted** — `` ## Recommend: `@<tag>[:<value>]` on <path>:<range> `` — so the proposal is **inert**: ark never indexes it as a live tag on the doc carrying it, at every indexed hop (curation doc, result doc, session transcript). The `recommend` builder verb wraps the tag; callers pass the bare `@tag[:value]`. This is the **Watermark** pattern — recognizable-but-inert, read back by the assistant, passed over by the indexer — and it prevents a not-yet-approved connection from polluting the index.
- **R3112:** `<RECALL notags/>` — a self-closing watermark the watcher recognizes on an assistant line via the same per-append scan used for `<BLOODHOUND>` — sets tag-recommend suppression for the session's subsequent fires: the curation pass omits its `## Recommend:` items and surfaces chunks only (the `## Surface:` arm is unaffected). It is the ambient counterpart to the bloodhound's per-hunt `<BLOODHOUND notags>` (R3110); the shapes differ because the cadences do (a hunt is discrete, ambient recall fires continuously).
- **R3113:** The ambient tag-recommend opt-out is a **toggle**: `<RECALL tags/>` clears the R3112 suppression (`ClearRecallNotags`), restoring recommends for the session so the user need not restart ark to undo `<RECALL notags/>`. Both markers ride the same per-append scan; when one appended batch carries several, the **last marker wins** — the batch resolves to its final on/off state. The default (no marker seen) is recommends on.
- **R2758:** `ark connections recall close FIRE --nonce N [-preserve-curation]` is the single cleanup verb. `FIRE` is the composite `<session>-<fire>` token (R2901), decomposed server-side for the tmp:// paths. It works whether or not the result builder was ever opened. When `surface` / `recommend` items were added, it writes `tmp://ARK-RECALL/result-<session>-<F>` (the result doc per R2750/R2751); when nothing was added, no result doc is written. Either way (unless `-preserve-curation`), it removes `tmp://ARK-RECALL/curation-<session>-<F>` AND sweeps any orphan curation docs for the same session whose fire number is strictly less than `<F>` (older fires the assistant missed handling). Same-session scope: other sessions' orphans are not touched. It then discovers the calling subagent's JSONL via the `nonce → .meta.json` lookup (R2760), sums tokens (R2761), and appends one record to `~/.ark/monitoring/recall.jsonl` (R2763). An unknown FIRE cookie exits non-zero.
- **R2759:** The spawner constructs the recall agent's Task `description` field as a string containing the substring `nonce <N>`. For the daemon the Luhmann orchestrator builds `ark-recall lotto-tube loop nonce <N>` (no fire — one daemon generation spans many fires). The substring is the only payload `close` / `context` / `inspect-exit` need to discover the calling agent's JSONL: a substring search for `nonce <N>` in the `.meta.json` description field is sufficient (no JSON parsing of the description body), per R2760.
- **R2760:** `close` locates the calling subagent's JSONL by computing `cwd_encoded := replace_slashes_with_dashes(cwd)` and reading `parent_session := $CLAUDE_CODE_SESSION_ID` from its environment, then scanning `~/.claude/projects/<cwd_encoded>/<parent_session>/subagents/*.meta.json` for the first entry whose `description` body contains the substring `nonce <N>`. The matched basename gives the paired `agent-<id>.jsonl`. When no match is found, `close` proceeds without token sums (logs zeroes in the monitoring record) and does not fail.
- **R2761:** Token usage is summed from the matched JSONL by reading `usage.input_tokens` and `usage.output_tokens` across all `"type":"assistant"` records in the file. No `isSidechain` filter is required because the file is the subagent's dedicated transcript.
- **R2762:** The assistant record that issues the `close` tool call has its own usage counted, but any wrap-up response the agent emits after `close` returns is missed (it is not yet in the JSONL when `close` reads). Expected undercount is <1k tokens per fire and acceptable for the monitor metric; documented as a known caveat in the spec.
- **R2763:** `~/.ark/monitoring/recall.jsonl` is an append-only log written by `close`. Each line is one JSON object with fields `fire` (int), `session` (string), `nonce` (int), `in_tokens` (int), `out_tokens` (int), `context_tokens` (int — `cache_creation_input_tokens + cache_read_input_tokens` from the last assistant record at close time, same value R2777 returns; in v2 roughly per-fire static, in Phase 2 the context-creep telemetry), `latency_ms` (int), `surfaced` (int), `recommended` (int), `outcome` (string, one of `"result-emitted"`, `"silent-close"`, or `"error"`), `timestamp` (RFC3339 string). The format is forward-compatible — future fields slot in at the end.
- **~~R2764:~~** (Retired T133 — see R2874) The RJ record value format extends to `varint(counter) + 8-byte BE unix nanos` (the v1 format was just the 8-byte timestamp). The counter increments on every `Store.RejectDerived` call for the same `(chunkid, tagname)`; the timestamp updates to NOW on each write. No data migration is required — `ark connections clean -all` wipes RJ records and the next reject cycle rewrites in the v2 shape.
- **R2765:** The propose pass reads the RJ counter alongside record existence. When `[recall].reject_propose_ceiling > 0` and `counter >= reject_propose_ceiling`, the `(chunkid, tagname)` pair is suppressed before reaching the curation doc, in addition to the existence-based suppression already covered by R2673.
- **R2766:** The assistant's "mention rejected proposals" surface reads the RJ counter. When `[recall].reject_mention_ceiling > 0` and `counter >= reject_mention_ceiling`, the assistant does not surface the pair to the user or count it toward any "you have N rejected proposals" summary. Between the propose-ceiling and mention-ceiling values, the assistant may surface the count without per-record specifics.
- **R2767:** `[recall].reject_propose_ceiling` (int, default `0`) is the rejection-counter threshold above which the propose pass suppresses a `(chunk, tag)` pair (R2765). `0` (unset) means infinite — v1 behavior is preserved when the knob is left at default.
- **R2768:** `[recall].reject_mention_ceiling` (int, default `0`) is the rejection-counter threshold above which the assistant suppresses the `(chunk, tag)` pair from any user-facing mention (R2766). `0` (unset) means infinite.
- **R2769:** The recall agent is defined at `.claude/agents/ark-recall-agent.md`. Model: Haiku 4.5. Frontmatter sets `memory: local` so `MEMORY.md` does not inherit into the subagent.
- **~~R2770:~~** (Retired T126 — see R2853) The recall agent's PreToolUse guard script enforces the hermetic-seal tool allowlist. Permitted Bash commands: `ark fetch tmp://ARK-RECALL/curation-*`, `ark connections recall surface ...`, `ark connections recall recommend ...`, `ark connections recall close ...`. All other Bash invocations are denied. `Read`, `Edit`, `Write`, and network tools are denied as a class.
- **R2771:** The guard's `Read` denial carries the `ark fetch tmp://ARK-RECALL/curation-<session>-<fire>` template in its stderr message. The denial is the recall agent's onboarding runway (fumble-onboarding pattern): the agent's first attempt to read the curation doc via `Read` fails with explicit instructions to retry via `ark fetch`.
- **R2772:** The recall agent's CLI parse failures are recorded in a Fumble Log (silent ride-along) at `~/.ark/monitoring/recall-fumbles.jsonl`. Each entry captures `{timestamp, fire, nonce, command, args, error}`. The fire still completes when individual `surface` / `recommend` calls fail — the malformed call is rejected by the CLI but the pipeline continues. The Fumble Log exists so format tightening can be data-driven.
- **~~R2773:~~** (Retired T127 — see R2854) The recall agent's skill file lives at `~/.ark/skills/ark-recall.md`. The fumble-onboarding runway (R2771) instructs the agent to fetch the skill once per spawn if it doesn't already have the content from session priming.
- **R2774:** The recall agent never writes RJ records. `Store.RejectDerived` is reachable only through the assistant's `ark connections recall reject-derived` invocation, never by the agent. The agent's role is recommend-only; permanent rejection state stays user-controlled via the assistant relay.
- **~~R2775:~~** (Retired T128 — see R2855) The assistant runs two subscriptions, both scoped under its own Claude Code session UUID as the subscription session ID. The curate subscription is bare: `ark subscribe --session <claude-code-session-uuid> --tag ark-recall-curate`; the assistant filters by `@ark-recall-curate` value at receipt to drop curate events for other sessions. The result subscription is value-scoped: `ark subscribe --session <claude-code-session-uuid> --tag ark-recall-result=<claude-code-session-uuid>`, so cross-session result docs never reach the wrong listener.
- **~~R2776:~~** (Retired T129 — see R2850) The assistant uses `ark listen --session <claude-code-session-uuid>` to pop pubsub events for both subscriptions (R2775). On a curate event whose `@ark-recall-curate` value matches the assistant's own Claude Code session UUID, the assistant calls `ark connections recall reserve-nonce`, embeds `(fire, nonce)` in the Task `description` field per R2759, and launches the recall agent via the Task tool with the curation doc path in the agent's prompt body.
- **R2777:** `ark connections recall context --nonce N [--limit N] [--json]` reports the calling subagent's current context fill in tokens, computed as `cache_creation_input_tokens + cache_read_input_tokens` from the most recent `"type":"assistant"` record in the subagent's JSONL that carries a usage object. The lookup reuses the nonce → `.meta.json` discovery from R2760. The sum represents the cumulative input the model just loaded — the same value Claude Code's status indicator reads. Default output is the bare integer; `--json` returns `{tokens, found}`; with `--limit N` the command exits 1 when tokens ≥ N, else 0 (shell-pipeline gating for the long-running lotto-tube recall agent in Phase 2, which self-recycles when context grows past a configurable limit).
- **~~R2850:~~** (Retired T135 — see R2890) The recall agent runs as a long-running daemon executing the `recall-loop.md` lotto-tube loop. Each *generation* runs until its context fills past the configured limit, at which point the agent exits and the Luhmann orchestrator respawns the next generation with a fresh nonce (respawn mechanics owned by `seq-luhmann-supervisor.md`; on-disk state in `recall.jsonl` / RC / RD records / tmp:// docs survives the cut, only the working context is recycled). It is **not** spawned per-fire by user-facing assistants. Supersedes the one-shot framing; replaces retired R2776.
- **~~R2851:~~** (Retired T136 — see R2890) The Luhmann orchestrator delivers the daemon's nonce two ways: in the Task `description` (per R2759, for `close` / `context` / `inspect-exit` JSONL discovery) and in the prompt body — `Start the recall loop now. Nonce: <N>. Context limit: <L>.`. The hermetically-sealed Haiku agent cannot read its own `description`, so the prompt copy is the only way it can address `subscribe --session recall-loop-<N>`, `close --nonce N`, and `context --nonce N`.
- **~~R2852:~~** (Retired T130 — see R2857) The daemon subscribes once to `@ark-recall-curate` (bare, no value constraint) under subscription session `recall-loop-<N>`, then blocks on `ark listen` to pop curate events. It derives each fire `F` from the popped curation-doc path's trailing integer (equivalently the `@ark-recall-fire:` header per R2748). The fire is minted upstream by the watcher (R2752); the daemon never allocates fires, which is why `reserve-nonce` (R2755) has no `reserve-fire` counterpart.
- **~~R2853:~~** (Retired T131 — see R2859) The daemon's PreToolUse guard permits, in addition to the existing `ark fetch tmp://ARK-RECALL/curation-*` and `ark connections recall surface` / `recommend` / `close`: `ark subscribe ... --tag ark-recall-curate`, `ark listen ...`, and `ark connections recall context ...`. `Read`, `Edit`, `Write`, and network tools remain denied as a class. Replaces retired R2770.
- **~~R2854:~~** (Retired T132 — see R2860) The daemon's bootstrap skill is `~/.ark/skills/recall-loop.md` (the lotto-tube loop), loaded first via a body instruction in the agent definition (lifecycle hooks do not fire in subagents) and named as the guard's denial-stderr runway target. `recall-loop.md` delegates each iteration's per-curation-doc work to `~/.ark/skills/ark-recall.md`, which remains the unchanged work skill (and the contract for one-shot curation if revived). Replaces retired R2773.
- **R2855:** A user-facing assistant runs a single subscription — the value-scoped result subscription `ark subscribe --session <cc-session-uuid> --tag ark-recall-result=<cc-session-uuid>` — and uses `ark listen` to pop result events, deciding whether to surface recall to the user (RJ-counter consultation per R2765 / R2766 unchanged). As of seam 3a (R2890) the assistant **also** reserves a nonce and spawns its session's secretary — which owns the value-scoped `@ark-recall-curate=<cc-session-uuid>` subscription via `recall next --session`. So the assistant runs the result-listen loop and the secretary (its subagent) runs the curate loop: two roles in the one session. Replaces retired R2775; supersedes R2776's earlier "assistant does not spawn" framing.
- **R2857:** `ark connections recall next [--session <S>] <NONCE>` is the secretary's single loop verb. On first call it idempotently subscribes — **value-scoped** `@ark-recall-curate=<S>` under subscription session `recall-curate-<S>` when `--session <S>` is passed (the per-session secretary, keyed on the durable session so a restart can't recycle it, R2888/R2902), else bare `@ark-recall-curate` under `recall-<NONCE>` (the legacy one-shot/diagnostic path); later calls are no-ops. With `--session`, dispatch is scoped to that session's docs (R2889) and the returned doc is prefixed with the session's recent conversation (R2891). It then checks the nonce's context fill (the R2777 token sum) against `[luhmann].context_limit`: at or over the limit it returns an **exit** directive (exit status `2` — the secretary's only clean stop). Otherwise it returns the lowest-fire pending `tmp://ARK-RECALL/curation-<session>-<fire>` doc **whose originating session has a result subscriber** (`SubscriberCount("ark-recall-result", session) > 0`) — numeric fire ordering decided server-side — with the doc content as the body (exit status `0`). Docs for sessions with no result subscriber are left pending (they pile up); the secretary never dispatches work `close` would discard — the subscriber-presence gate moves from `close` to dispatch. When no dispatchable doc is pending and the limit is not reached it **blocks up to a keepalive window (~90 seconds)** via a select that wakes on a curate event (the session queue), a **subscription-changed broadcast** (`PubSub.SubChanged`, so a late-arriving result subscriber dispatches piled docs at once rather than after the re-check tick), the window elapsing, or cancellation — refreshing its last-listen each cycle so `Reap` doesn't drop the subscription; if none yields a dispatchable doc by the window it returns a **keepalive directive** (exit status `0`, body "no curation doc yet — run `next` again"). The window is short by design: the recall subagent runs `next` in the **foreground**, and the keepalive must return before the harness's foreground-Bash auto-background threshold (~120s) so the call is never detached. A detached `next` ends the subagent's turn, which makes it emit a per-loop-cycle "completed" notification the spawning assistant cannot distinguish from a real exit; keeping `next` inline keeps the subagent in one continuous turn that only "completes" on a true context-limit exit. (A continuous foreground turn stays cache-warm by activity, so the prompt-cache TTL is not the binding constraint — the foreground timeout is.) Every non-exit return — doc or keepalive — tells the caller to run `next` again (a uniform crank handle); only the exit directive stops, so there is no ambiguous empty to misread as "stop." Processing lowest-fire-first keeps `close`'s same-session orphan sweep (R2758) safe. Replaces retired R2852; the keepalive timeout supersedes 008's pure no-timeout block.
- **R2858:** `recall next` emits dual output. The response body carries crank-handle **prose** instructing the caller — on a doc: "judge the candidates, `surface` / `recommend` the worthy ones, `close <F> --nonce <N>`, then run `ark connections recall next <NONCE>` again"; on exit: "context limit reached — stop." The process **exit status** is machine-readable: `0` when a curation doc was returned, `2` for the exit directive. The prose serves the LLM agent; the status serves human scripts and IDE plugins, so the two audiences share one verb.
- **R2859:** The daemon's PreToolUse guard permits the four recall verbs — `ark connections recall next` / `surface` / `recommend` / `close` — plus `cat <file>` (a single-argument read, no chaining or redirection) so the agent can read the backgrounded `next`'s output file. `Read`, `Edit`, `Write`, network tools, and every other `ark` verb — `subscribe`, `listen`, `files`, `fetch`, `context` — are denied as a class, because `next` absorbs them. Replaces retired R2853.
- **R2860:** The secretary's loop lives in the agent persona (`.claude/agents/ark-recall-agent.md` body): the agent runs `ark connections recall next --session <S> <nonce>` (session + nonce from its prompt) and follows its directive, looping until told to exit. The judgment bar — does a candidate fit the **live conversation** injected into the doc vs. merely resemble the source paragraph, and filter *and* sharpen tags on discrimination — lives in the persona, as it is the secretary's core judgment (R2895). `recall-loop.md` is retired as the loop driver. `~/.ark/skills/ark-recall.md` remains the standalone one-shot work skill (the migration-007 preserved capability), untouched. Replaces retired R2854.
- **R2865:** `ark connections recall listen --session <SID>` is the consumer-side loop verb — the mirror of `next` for a user-facing assistant. On first call it idempotently subscribes the session to its own value-scoped result tag (`@ark-recall-result=<SID>`, parsed as `ark-recall-result=<SID>`) under subscription session `<SID>`; later calls are no-ops. It then **blocks until at least one** `tmp://ARK-RECALL/result-<SID>-<fire>` doc is published for the session, fetches the published doc(s), and returns their content plus crank-handle prose instructing the caller to surface what genuinely helps the user (assistant has final say) and run `recall listen --session <SID>` again. Unlike `next` it has **no keepalive and no context-gate**: the assistant runs it backgrounded and wakes only on a real result (a keepalive would bloat the assistant's context the way per-cycle beats bloat the orchestrator's), so the only non-result return is cancellation. It does **not** filter `## Recommend:` items by RJ ceiling — the RJ consultation (R2765 / R2766) and any `reject-derived` stay the assistant's job. Server-required.
- **R2866:** The `/recall` skill (`.claude/skills/recall/SKILL.md`) starts **both** roles in the user's session, using the session UUID from the `sessionid=${CLAUDE_SESSION_ID}` macro: (1) it reserves a nonce and spawns the session's secretary as a background Task (`subagent_type: ark-recall-agent`, session + nonce in the prompt), respawning it on its context-limit exit (R2890); and (2) it runs `ark connections recall listen --session <SID>` **backgrounded**, surfacing recall to the user on each completion and relaunching the `listen`. Recall is **opt-in**: until `/recall` runs, the session has no `@ark-recall-result` subscriber, so the secretary's `next` subscriber-gate (R2857) leaves its curation docs undispatched and no recall is performed for it. The notification-woken surfacing turn carries `origin.kind: "task-notification"`, so it is not a genuine user record (R2732) and does not arm the watcher — the loop does not feed itself.
- **R2870:** The daemon's curation-doc crank-handle (R2858) instructs the agent to `recommend` tags for `tag-only` candidates (R2869) but never `surface` them, and to surface and/or recommend all other candidates as it judges fit. The deterministic marker plus this prose keep the reader's own conversation out of the surfaced set.
- **R2871:** When a result doc references at least one chat-JSONL chunk — detected by a `<path>` ending in `.jsonl` in the doc body (a Surface/Recommend `<path>:<range>` line; cheap path-shape proxy for a conversation log) — the consumer crank-handle returned by `recall listen` (R2865) includes guidance to apply tags on those chunks as external (`@ext`) tags rather than inline edits — the file is append-only source of truth — and notes that tagging conversation chunks this way is how they enter the hypergraph. The guidance is conditional: omitted when the doc references no `.jsonl` chunk. The internal-vs-external choice remains the assistant's, made per chunk from its path.
- **R2872:** `SurfaceItem(fireToken, loc, reason)` rejects a candidate whose `-loc` path resolves to the fire's own originating session — `sessionFromJSONLPath(<loc path>) == doc.session` — returning an error instead of writing the `## Surface:` item. Such a chunk is a `# Source:` / conversation paragraph already in the reader's context; surfacing it is the redundant self-echo R2869 exists to prevent, reached here when the agent passes the source locator directly (bypassing the candidate-classification marker). The error names the fix (surface a `## Candidate:` locator, never the source one), doubling as fumble-onboarding. This is the deterministic backstop to R2869/R2870 — the agent cannot self-surface regardless of which locator it grabs. `RecommendItem` is **not** gated: an own-session recommend is the intended path for tagging conversations into the hypergraph (R2869).
- **R2873:** The two surfaces the recall daemon actually reads per fire — the persona (`.claude/agents/ark-recall-agent.md`, R2860) and the curation-doc crank-handle (`recallDocPrompt`, R2858/R2870) — name the surfaceable id `<CANDIDATE-CHUNKID>` (matching the `surface`/`recommend` call placeholder verbatim) and mark the `# Source Chunk:` id as the trigger paragraph that must never be passed to `surface`/`recommend`. This removes the bare-`<id>` ambiguity (a chunk id is visible on both the source and candidate lines, and the instruction said only `-chunk <id>`) that let the weak agent grab the more prominent `# Source Chunk:` H1 id. The legacy one-shot skill `.claude/skills/ark/ark-recall.md` (unhooked from the daemon in migration 007, R2854) is marked **obsolete** — retained for reference a cycle or two before removal — and is *not* the live instruction surface, so it is not edited for this clarity.


## Feature: Recall Judgment (signed per-edge relevance) — Secretary seam 1
**Source:** specs/migrations/complete/011-recall-judgment.md

- **R2874:** The RJ record value format becomes v3: `signed-varint(score) + 8-byte BE unix nanos`, where `score` is a signed integer zigzag-encoded via Go's `binary.PutVarint` / `binary.Varint`. `score < 0` is net-rejected (the magnitude `-score` reproduces the v2 reject counter: N rejections with no reinforcement → score `-N`); `score > 0` is reinforced; `score == 0` is neutral and equivalent to record-absent. The trailing timestamp records the most-recent adjustment (enabling decay-on-read as a future knob). The RJ key shape is unchanged. Supersedes the v2 counter format (R2764). The record class is reframed as **Recall Judgment** — "reJection" is its negative tail.
- **~~R2875:~~** (Retired T249 — see R3075) `Store.AdjustJudgment(txn *bbolt.Tx, chunkID uint64, tagname string, delta int64) (newScore int64, err error)` is the single read-modify-write primitive for the Judgment edge: it reads the current score (absent = 0), adds `delta`, stamps the timestamp to NOW, writes the v3 value, and returns the new score. Positive `delta` reinforces; negative decays/rejects. Runs inside the caller's write txn. This is the bidirectional substrate; it has no in-tree caller with a positive delta in this seam.
- **~~R2876:~~** (Retired T250 — see R3059) `Store.ReadJudgment(txn *bbolt.Tx, chunkID uint64, tagname string) (score int64, present bool, err error)` reads the signed score for a `(chunkid, tagname)` edge. Absent → `(0, false, nil)`. A value that does not decode as `signed-varint + 8 bytes` is treated conservatively as rejected — returned as a negative score with `present = true` — so a `reject_propose_ceiling == 0` caller never re-proposes a corrupt edge. "Rejected" is defined as `present && score < 0`.
- **~~R2877:~~** (Retired T247 — see R3069) `Store.RejectDerived(chunkID, tagname)` is reimplemented on the primitive: in one txn it deletes `RC[chunkid+tagname]` then applies `AdjustJudgment(..., -1)`, returning the rejection magnitude (`max(0, -newScore)`) so callers expecting the prior `uint64` counter are unchanged. With no reinforcement producer present, a rejection-only sequence on a fresh edge yields scores `-1, -2, -3, …` — bit-for-bit identical to the v2 monotonic counter. Supersedes the value-write half of R2680 (the RC-deletion half is unchanged).
- **~~R2878:~~** (Retired T251 — see R3070) `Store.HasDerivedRejection(txn, chunkID, tagname) (rejected bool, magnitude uint64, err error)` is reimplemented on `ReadJudgment`: `rejected = present && score < 0`, `magnitude = max(0, -score)`. Signature and caller-visible meaning are unchanged; the propose-pass (R2673/R2765) and mention-path (R2766) readers consume `magnitude` exactly as they consumed the v2 counter.
- **R2879:** The Recall Judgment edge applies to **attached** tags (live F/V hyperedges) as well as derived RC proposals. The key shape (`"RJ" + chunkid varint + tagname`) already addresses any `(chunkid, tagname)`; no key change is required. Reinforcement and pruning of attached-tag edges have no in-tree producer in this seam — the secretary that drives them is a later seam.
- **R2880:** The v3 migration is clean-reset, operator-driven: `ark connections clean -all -checkpoint` wipes RJ records (after `-checkpoint` caps the session JSONLs at current EOF and the Fossil checkpoint makes the wipe recoverable); the next reject/reinforce cycle rewrites RJ in v3 shape. There is no automatic `DB.Open` drop. Because the v2 (`uvarint counter + 8 nanos`) and v3 (`signed-varint score + 8 nanos`) values are structurally indistinguishable, running the v3 binary against un-cleared pre-v3 RJ records is out of contract — the same discipline as RJ's v1→v2 transition (R2764) and old-binary-on-new-DB (Schema Version Protocol).
- **R2881:** The Judgment axis is bidirectional: reinforcement raises the score, decay/rejection lowers it, and a write that returns the score to 0 may delete the record (absent ≡ 0). There is still no manual un-reject verb. Supersedes R2683's v1 stickiness framing — RJ is no longer monotonic-and-never-removed.


## Feature: Recall surface-cooldown (RM) — Secretary seam 2
**Source:** specs/simple-recall.md

- **R2882:** The `RM` record class (Recall surface-cooldown) is a per-(session, chunk) RD-family sibling keyed by chunk instead of tag-value. Key: `"RM"` + session-bytes + `\x00` + chunkid varint (session-bytes is the Claude Code session UUID, variable-length, no `\x00`; the `\x00` separates it from the trailing chunkid varint, mirroring RD). Value: 8-byte big-endian unix nanoseconds — the most recent time this chunk was surfaced to this session. Presence means "surfaced before"; the timestamp drives the surface-cooldown window.
- **R2883:** `Store.MarkSurfaced(session string, chunkID uint64) error` writes or overwrites `RM[session+chunk]` with NOW unix nanoseconds (one write txn, mirroring `AddDiscussed`).
- **R2884:** `Store.LastSurfaced(session string, chunkID uint64) (nanos int64, present bool, err error)` reads the RM timestamp; an absent record returns `(0, false, nil)`. A value that is not exactly 8 bytes is treated as absent (read robustness, mirroring RD).
- **R2885:** `Store.PruneSurfaceCooldown(ttl time.Duration) (int, error)` full-scans the RM prefix and deletes entries whose timestamp is older than `ttl` across all sessions, returning the deleted count (mirrors `PruneDiscussed`). The cooldown read path also treats an entry older than the configured window as expired (lazy expiry, mirroring RD).
- **R2886:** `[recall].surface_cooldown` (Go duration string, default `"24h"`) is the surface-cooldown window — the span within which a previously-surfaced `(session, chunk)` is suppressed. It doubles as the RM record's lazy-expiry TTL: an entry past the window means the chunk is off cooldown and the record is prunable.
- **R2887:** `ark connections clean` wipes RM records alongside RD as per-session recall state; the `-session` scope restricts the RM wipe to the named session(s) exactly as it restricts the RD wipe.


## Feature: Recall Secretary — per-session topology (seam 3a)
**Source:** specs/migrations/complete/012-secretary-pipeline.md

- **R2888:** `ark connections recall next` gains a `--session <S>` flag. With it, the loop verb subscribes **value-scoped** `@ark-recall-curate=<S>` (subscription session `recall-curate-<S>`, keyed on the durable session — R2902) instead of bare `@ark-recall-curate`. Without `--session`, the legacy bare-curate subscription and all-session scan are retained (one-shot/diagnostic path). The per-session secretary always passes `--session`. The context-gate, keepalive window, foreground discipline, and dual output (R2857/R2858) are unchanged.
- **R2889:** When `--session <S>` is set, `lowestPendingCuration` dispatches only `tmp://ARK-RECALL/curation-<S>-<fire>` docs (S-only; no cross-session scan). The per-doc result-subscriber gate (R2857) is unchanged. Without `--session`, the all-session scan is retained.
- **R2890:** The per-session assistant owns the secretary's lifecycle via the `/recall` skill: reserve a nonce (`recall reserve-nonce`), launch the secretary Task in the background with `(session, nonce)` in the prompt and `nonce <N>` in the Task `description` (R2759), run the consumer loop (`recall listen --session <S>`, R2865), and **respawn** the secretary with a fresh nonce when it exits at its context limit (R2857). Subscriptions are in-memory server state (R803) so a respawn loses no queued curation docs. Replaces the Luhmann recall-class spawn/supervise (no streak machine ported in 3a — the interactive assistant respawns directly).
- **R2891:** `recall next --session <S>` prepends the last `N` conversation turns of S to the curation-doc body it returns to the secretary, where `N = [recall].context_turns`. The turns are read server-side from S's JSONL (the genuine-user / assistant records the watcher already recognizes) at hand-off time; the stored curation doc is unchanged. When the JSONL is absent or unreadable, the doc is handed over with no context block and no error (best-effort; never blocks).
- **R2892:** `[recall].context_turns` (int, default `3`) sets how many trailing conversation turns `recall next --session` injects. `0` injects nothing.
- **R2893:** The watcher's `fire()` applies a surface-cooldown floor before writing a candidate to the curation doc: a candidate chunk whose `(session, chunk)` was surfaced within `[recall].surface_cooldown` (seam 2 `Store.LastSurfaced`) is dropped. Candidates outside the window appear normally.
- **R2894:** `SurfaceItem` (the `surface` verb) resolves its `-loc` to a chunkID just-in-time (R2900, `chunkIDForLoc`) and calls `Store.MarkSurfaced(session, chunkID)` for each surfaced chunk, starting the cooldown the moment a chunk is surfaced. The session is the result doc's originating session. Best-effort: an unresolvable loc forgoes the cooldown for that surface (the RM record stays chunkid-keyed; re-keying it on path:range is a deferred follow-on).
- **R2895:** The agent definition (`.claude/agents/ark-recall-agent.md`) is reframed from shared daemon to **per-session secretary**: it judges candidates against the injected conversation turns (R2891), not just the source-paragraph excerpt, and may `recommend` a sharper or additional tag than the thin-context `proposed-tags` suggest — the bar is discrimination (a tag that fits everything sharpens nothing), not mere accuracy. The discriminate-and-surface core and the no-rejection-records rule (R2774) are unchanged. The `recommend` verb already accepts an arbitrary `@t[:v]`, so enhancement is a persona capability, not a new verb.
- **R2896:** `recall next` delivers a dispatched curation doc **by file, not inline**: it materializes the (conversation-injected) doc content to `~/.ark/recall-curation/curation-<session>-<fire>.md` (a real file) and returns a *short* pointer + the judge/surface/recommend/close/next crank-handle. The large content never reaches the agent's foreground-Bash stdout (which the harness truncates to a preview + spill file, defeating inline delivery and the `cat` fallback alike — the bug `/tmp/log.txt` captured). `Store.RejectDerived`-style cleanup: `CloseResult` removes the materialized file alongside the tmp:// curation doc. The `tmp://ARK-RECALL/curation-*` doc remains the canonical store (watcher-written, dispatch-read, close-removed); the file is the agent-Readable materialization.
- **R2897:** The secretary's `tools:` frontmatter includes `Read` (in addition to `Bash`), and the PreToolUse guard permits the Read tool **only** when its `file_path` matches the curation-doc path (`.../recall-curation/curation-*.md`), denying Read for every other file. The Read tool paginates (offset/limit), so it opens a 30 KB doc where bare `cat` re-overflows. This is the one keyhole in the hermetic seal; all other tools (`Write`, `Edit`, network, every non-loop `ark` verb) stay denied as a class.


## Feature: Stubborn recall-next — restart/rebuild-stable identity
**Source:** specs/migrations/stubborn-recall-next.md

- **R2898:** The curation doc references candidate chunks by `<path>:<range>`, not chunkid. Each `## Candidate:` H2 header is `## Candidate: <path>:<range> (<size>)` — path:range first, then the friendly byte-size label (R2749 size format) — and the source heading is `# Source: <path>:<range>` (the originating JSONL chunk's path:range), replacing `# Source Chunk: <jsonl-chunkid>`. No chunkid appears in the curation doc; the watcher resolves each candidate's path:range at build time from the search result it already holds. The score / tags / proposed-tags bullet lines and the fenced content excerpt (R2749) are unchanged. Supersedes the chunkid-leading candidate/source headers of R2749.
- **R2899:** The result doc references chunks by `<path>:<range>`, not chunkid. `## Surface: <path>:<range> (<size>)` recommends showing a chunk (size label per R2751); `## Recommend: @<tag>[:<value>] on <path>:<range>` proposes a tag attach. Each H2 is followed by `reason: <one-line justification>`. No chunkid appears in the result doc. Supersedes the chunkid-leading Surface/Recommend headers of R2751.
- **R2900:** `ark connections recall surface <F> -loc <path>:<range> -reason TEXT` and `ark connections recall recommend <F> -loc <path>:<range> -tag @t[:v] -reason TEXT` identify the chunk by path:range — the `-loc` flag replaces `-chunk N`. The server resolves content, byte size, and the source-session check from the path:range via `ChunkText` / `ChunkInfo`. The consuming assistant fetches surfaced chunks via `ark chunks <path>:<range>`, which already accepts either a chunkid or a path:range (no new fetch mode). Supersedes the `-chunk N` form of R2756.
- **R2901:** The fire number is a **per-session** monotonic counter held in memory (a `map[session]uint64` under the watcher lock), not a single global counter. On the first fire for a session after an `ark serve` start, the counter is seeded by scanning `~/.ark/recall-curation/` for that session's `curation-<session>-<fire>.md` files and taking `max(fire) + 2` (or `1` when the session has no surviving files); thereafter it increments in memory. The `+2` skips a possibly-allocated-but-unmaterialized in-flight doc (one secretary ⇒ lag ≤ 1); the in-memory hold — not a per-allocation dir recompute — closes the allocation→materialization race a constant offset cannot. Because per-session fire numbers are not globally unique, the in-flight `curations` / `results` maps and the CLI `<F>` cookie key on the composite `<session>-<fire>` token (the curation-doc basename), crank-handle-generated and opaque to the secretary. Supersedes the global fire counter of R2752.
- **R2902:** `recall next`'s curate subscription keys its subscription session on the durable `<session>` (the `--session <S>` value), not on `recall-<NONCE>`. Two secretary generations across a restart therefore share one stable, unique subscription key, so the `SubCount == 0` re-subscribe guard never makes a colliding second subscriber a silent no-op. Mirrors the consumer-side result subscription, already keyed on session (R2865). The nonce's other roles (R2755 reserve, R2760 JSONL lookup, R2777 context-gate, R2758/R2763 close/monitor correlation) are unchanged.
- **R2903:** `ark connections recall next` treats an `ark serve` bounce as a wait condition, not an error. Its CLI connection layer reclassifies dial-refused (cold dial — the server is restarting when the secretary invokes `next`) and mid-block EOF (the server dies while `next` blocks) from error to not-yet. On a cold dial it redials with bounded geometric backoff up to a budget (~20s); on a mid-block drop, or once the budget is exhausted, it returns a **keepalive** (exit 0) — never a fatal error and never the context-limit exit — so the secretary's loop re-invokes `next` (re-subscribing idempotently per R2902) and rides out the bounce across iterations. It never hangs, and each call stays well under the harness foreground auto-background threshold (it never reissues a fresh blocking request in the failure path). An in-flight fire abandoned across a bounce is not recovered; the loop re-syncs to a fresh doc.


## Feature: Chimes — standard scheduling tags
**Source:** specs/chimes.md

- **R2778:** Six standard recurring tags are defined as a convention: `@chime-1m:`, `@chime-5m:`, `@chime-15m:`, `@chime-30m:`, `@chime-45m:`, `@chime-60m:`. Each fires on its named cadence. The tag value carries the current date and time at fire moment in RFC 3339 format (e.g. `2026-05-27T14:30:00Z`), so a subscriber receives a usable "now" tick.
- **R2779:** The hosting file `~/.ark/chimes.md` contains the recurrence specs for the six chime tags, one per line, in `@chime-Nm: every Nm` form. The file is a regular ark file — indexed, scanned, scheduled — and routes through the same `[schedule]`-based path as user-authored schedule tags. No special-case code path exists for chimes.
- **R2780:** ark auto-creates `~/.ark/chimes.md` on server startup if missing, populated with the canonical six entries. If the user deletes the file, ark re-creates it on the next startup. The file is owned by ark; users do not manage it directly.
- **R2781:** Default `ark.toml` ships the six chime tag names in `[schedule].tags` so new installs pick up chimes without user configuration. Existing installs that do not list the chime tag names continue to behave as before — chimes only fire when their tag names appear in `[schedule].tags`.
- **R2782:** Subscribers consume chimes with plain `ark subscribe --session ID --tag chime-Nm` (no `--scheduled` / `--recurring` flag — `[schedule].tags` declares schedulability per the scheduling.md "Remove Scheduling from Subscriptions" contract). When the scheduled tick fires, the value the subscriber receives via `ark listen` is the RFC 3339 timestamp from R2778.
- **R2783:** `EventScheduler.AddChime()` is removed. The hardcoded 15-minute quarter-chime is subsumed by `@chime-15m:` (one of the six standard cadences per R2778), which routes through the normal schedule-log path. The previous "Quarter Chimes" section of `specs/pubsub.md` retires in favor of a pointer to `specs/chimes.md`. (R810 retires; this requirement is the replacement anchor.)


## Feature: Monitor CLI
**Source:** specs/monitor.md

- **R2784:** `ark monitor status [--json]` reports a per-class overview of every shipped monitoring class. The shipped classes are `recall` (populated by the recall watcher per R2763) and `luhmann` (populated by the supervisor per R2790). For each class, the command reads the tail of `~/.ark/monitoring/<class>.jsonl`, derives the current state, reports the most recent record's timestamp, and emits class-specific counters. Default output is a small markdown table; `--json` emits one object per class.
- **R2785:** `ark monitor status` derives class state from the latest records. For `luhmann.jsonl`, states are `running` / `paused` / `crashed`, set by the most recent state-defining record: `spawn`, `respawn`, `resume`, `exit`, and `quit-early` (R2861) map to `running` (a `quit-early` is a transient kind, immediately followed by a respawn), `pause` to `paused`, `crash` to `crashed`. For `recall.jsonl` (per-fire records without a lifecycle), state is `active` when the most recent record's timestamp is within a 90-minute freshness window, else `idle`. The `luhmann` block also reports the current `crashes` and `quit_early` counters and the current nonce; the `recall` block reports the recent fire count and average in/out tokens across the last N records.
- **R2786:** `ark monitor recent [-n N] [CLASS] [--json]` prints the tail of one or all monitoring logs. Default `-n` is `20`. With `CLASS`, only that class's log is read. Records are printed in original order (oldest of the selected window first). Default output is markdown bullets with timestamp, kind, and key identifying fields; `--json` emits raw JSONL records one per line.
- **R2787:** `ark monitor pause CLASS [--reason R]` appends a record `{ts, kind: "pause", reason: R}` to `<class>.jsonl`; `--reason` defaults to empty (a plain user pause) and carries a storm reason (`crash-storm` / `quit-early-storm`, R2863) when the supervisor pauses on a tripped ceiling. `ark monitor resume CLASS` appends `{ts, kind: "resume"}`. The consumer of the log (e.g. the Luhmann supervisor for `luhmann`) checks the most recent control record at decision time and acts on it; `monitor` itself does not implement the pause/resume effect, it only signals it.
- **R2788:** `pause` and `resume` are the only `monitor` subcommands that write. They append to the JSONL file via the standard write actor path (no direct file write from the CLI handler).
- **R2789:** `pause` exits non-zero with a diagnostic when the most recent state-defining record was already a `pause` (not followed by `resume`); `resume` exits non-zero when state is already running. The check is best-effort — a race with a concurrent writer can produce a duplicate, which the consumer treats as idempotent.
- **R2790:** `ark monitor status` and `ark monitor recent` run cold (no server required). `pause` and `resume` are server-required because they route through the write actor. All four subcommands accept `--json` where it applies; `--help` on any subcommand prints usage and exits zero.


## Feature: Luhmann support
**Source:** specs/luhmann.md

- **R2791:** `~/.ark/monitoring/luhmann.jsonl` is the orchestrator's append-only supervisor log. Each line is one JSON object describing one lifecycle event for a managed subagent class. The shipped class is `recall`; the file format is class-neutral so additional classes plug in later without record-shape changes.
- **R2792:** Each supervisor record carries fields `ts` (RFC 3339 string), `kind` (string), `class` (string), `nonce` (int — `0` for class-level control records), `task_id` (string — present on `spawn` and `respawn`, empty otherwise), `reason` (string — present on `exit`, `crash`, `quit-early`, and storm `pause` records, empty otherwise), `crashes` (int — consecutive-crash counter at the time of the record), `quit_early` (int — consecutive-quit-early counter at the time of the record, R2861), and `backoff` (int — seconds waited before the next respawn, present on `crash`). The format is forward-compatible: future fields slot in at the end.
- **R2793:** `kind` is one of `spawn`, `exit`, `respawn`, `crash`, `quit-early` (R2861), `pause`, `resume`. `spawn` and `respawn` carry a `task_id`; `exit`, `crash`, and `quit-early` carry a `reason`; `pause` carries a `reason` only when it is a storm pause (R2863); `pause` and `resume` carry no class-instance data beyond `class`.
- **R2794:** `ark luhmann spawn-record --class C --nonce N --task-id T` appends one `kind: "spawn"` record to `luhmann.jsonl` via the write actor. All three flags are required. Server-required.
- **R2795:** `ark luhmann exit-record --class C --nonce N --reason R [--crashes K] [--quit-early K]` appends one record to `luhmann.jsonl`. The reason determines the kind: `context-limit` → `kind: "exit"`; `quit-early` → `kind: "quit-early"` (R2861); any other reason → `kind: "crash"`. Both the `crashes` and `quit_early` counters are recomputed from the previous record's values per R2861: a healthy `exit` resets both to `0`; a `crash` increments `crashes` and holds `quit_early`; a `quit-early` increments `quit_early` and holds `crashes`. `--crashes K` / `--quit-early K` override the respective computed counter when the caller has already classified.
- **R2796:** `ark luhmann inspect-exit --nonce N [--json]` reads the subagent's own JSONL via the nonce → `.meta.json` lookup defined in R2760 and classifies the exit as `healthy`, `quit-early`, `crash`, or `unknown`. `healthy` requires the context fill at close (`tokens_at_close`, the R2777 value) to be at or over `[luhmann].context_limit` — a generation that filled and recycled as designed; with `recall next` the only clean exit is its context-gate directive, so a real recycle always reaches the limit. `quit-early` when the most recent record is a clean turn boundary (a `tool_result` for `ark connections recall next` / `close`, or a `recall.jsonl` outcome of `result-emitted` / `silent-close` / `no-subscriber`) but `tokens_at_close` is below the limit — the agent stopped before filling; the orchestrator respawns it with a fresh nonce like a healthy exit but does not count it as a crash, and the distinct label keeps the early stop visible instead of masquerading as healthy. `crash` when the most recent record is an error tail or the file ends mid-turn. `unknown` when the lookup couldn't find the JSONL. Default output is the label on stdout; `--json` returns an object with `label`, `last_record_kind`, `last_error` (string or null), and `tokens_at_close` (the R2777 value). Cold-start — no server required.
- **R2797:** `[luhmann].context_limit` (int, default `150000`) is the token ceiling the orchestrator passes to each spawned subagent. Consumed by the subagent's self-recycle check via R2777.
- **R2798:** `[luhmann].crash_pause_after` (int, default `3`) is the consecutive-crash count at which the supervisor stops respawning and writes a `pause` record to `luhmann.jsonl`. The user clears the pause with `ark monitor resume`.
- **R2799:** `[luhmann].backoff_seconds` ([]int, default `[1, 5, 30]`) is the schedule of seconds to wait between successive crash respawns. The last value in the list is reused for any further attempts up to `crash_pause_after`.
- **R2800:** `[luhmann].class.<NAME>.enabled` (bool, default `true`) declares whether the orchestrator should host the named subagent class. Setting to `false` disables the class without removing supervisor state from the log; the orchestrator skips spawning that class until the value flips back.
- **R2801:** The `[luhmann]` section follows the same `ark.toml` reload path as the rest of the config. Changes take effect on the next supervisor decision; no restart required. The orchestrator session re-reads the section when it acts on a subagent completion event.
- **R2861:** A `quit-early` exit (R2796) records as its own `kind: "quit-early"` in `luhmann.jsonl` and is tracked by a `quit_early` counter parallel to `crashes`, with symmetric, success-resetting semantics: a healthy `exit` (reason `context-limit`) resets BOTH counters to `0`; a `crash` increments `crashes` and holds `quit_early` at its previous value; a `quit-early` increments `quit_early` and holds `crashes`. The server's record handler holds any counter the current kind does not implicate (the pre-existing `default` branch). `spawn-record` (R2794) carries both counters forward from the most recent record. A `quit-early` is a transient kind — immediately followed by a respawn — so `luhmannState` maps it to `running` (R2785), not a terminal state.
- **R2862:** `[luhmann].quit_early_pause_after` (int, default `3`) is the consecutive-quit-early count at which the supervisor stops respawning and writes a storm `pause` record (reason `quit-early-storm`, R2863). Parallel to `crash_pause_after` (R2798) but on the independent `quit_early` counter (R2861); the user clears the pause with `ark monitor resume`. Follows the same `ark.toml` reload path (R2801).
- **R2863:** A storm `pause` — written when `crashes` reaches `crash_pause_after` (R2798) or `quit_early` reaches `quit_early_pause_after` (R2862) — carries a `reason` of `crash-storm` or `quit-early-storm` respectively, distinguishing it from a plain user pause (empty reason, R2787). `ark monitor status` derives an `emergency` field — `{active, class, reason}` — true when a class's latest state-defining record is a storm pause; it appears in the status table and the `--json` output. A server-side accessor exposes the same emergency state in Go (read from the supervisor log) so Frictionless can reflect it instantly for the downstream emergency-light UI; the orchestrator session additionally escalates the storm in chat/voice when it writes the pause.
- **R3010:** `ark luhmann next --session S [--first | --force] [--keepalive N]` is the orchestrator's drain tube: one blocking verb, launched `run_in_background` so the session stays conversational and keeps supervising while it waits, that returns one work item and is re-invoked in a loop. Same background-lotto-tube family as `ark connections recall next` and the `/ui` skill's `{cmd} event` listener. Server-required.
- **R3011:** A `next` return carries one of three kinds, told apart by the returned body: a **curation task** (a raw bloodhound finding from a CLI hunt — the body carries the `tmp://BLOODHOUND-CLI/<id>` result path and the raw finding — which Luhmann curates and emits via `ark bloodhound add`, then tags the result doc); a **supervisor directive** (`stand up another secretary` / `stop one` pool control, which Luhmann executes via the Task tool and records with `spawn-record` / `exit-record`); a **keepalive** (when neither arrives within the keepalive window, a keepalive crank-handle so Luhmann spends one cheap cached turn and re-invokes, holding the main-agent prompt cache warm).
- **R3012:** On `next`, `--session S` is an ownership identity, not a tube scope: the tube is one global queue of all CLI hunts and directives, and exactly one session may own the Luhmann role at a time. The lease is in-memory server state with no persistence, so a server bounce clears it.
- **R3013:** `next --session S --first` claims the role — succeeds when unowned, errors `you don't have ownership` when a different session already owns it. Plain `next --session S` (no flag) validates and never claims — proceeds only when S is the owner, errors `there are no sessions` when unowned and `you don't have ownership` when a different session owns it. `next --session S --force` reclaims unconditionally (the deliberate takeover of a dead-but-still-registered owner).
- **R3014:** The two error strings drive two orchestrator reflexes, making the ownership protocol self-converging with no persistence and no human arbitration: `there are no sessions` (server reborn by a bounce, unowned) → re-invoke with `--first` and resume; `you don't have ownership` (another session holds the seat) → this Luhmann exits. After a bounce where two orchestrators both see `there are no sessions` and both fire `--first`, one wins and the loser's `--first` returns `you don't have ownership`, so it steps down and exactly one Luhmann survives.
- **R3015:** A server bounce is a wait condition for `next`, not an error (Stubborn Plumbing, as with `ark connections recall next`): it redials with backoff across the bounce, and on reconnect the `there are no sessions` case routes into the re-`--first` path (R3014) rather than failing the loop.
- **R3016:** `--keepalive N` overrides `next`'s idle window (default ≈ 45 minutes). The window must stay under the 1-hour main-agent prompt-cache TTL that governs the backgrounded loop; this is a backgrounded-loop clock, distinct from the recall secretary's 90-second foreground keepalive (an artifact of the ~120 s foreground-Bash auto-background threshold on a dedicated subagent). The `next` drain tube subsumes the standalone `ark heartbeat` keepalive design — one tube carries curation tasks, supervisor directives, and the keepalive chime on a single sub-1-hour clock.
- **R3036:** The `next` **work** crank handles (curation and directive kinds, R3011) **lead with a re-launch-first instruction**: the orchestrator's first action on any return is to fire the successor `ark luhmann next` (backgrounded) *before* processing the item. Loop continuity thus becomes independent of the item's processing — a drift or a malformed tool call mid-work leaves the successor already blocking, so the seat stays drained and the loop survives a broken turn. This replaces the old trailing "run next again" (continuity moves from tail to front). The keepalive return (R3016) already re-launches and is unchanged; the `/luhmann` skill teaches the same order.
- **R3017:** `[luhmann].class.<NAME>.pool_max` (int, default `3`) is the maximum concurrent secretaries the orchestrator stands up for a pooled class (the CLI-bloodhound pool) on `stand up another` directives.
- **R3018:** `[luhmann].class.<NAME>.cooldown_seconds` (int, default `600`) is how long a pooled-class secretary that has returned to idle stays warm before it is eligible for a `stop one` directive (damps spawn/stop churn). The default is warm enough that follow-up hunts in an interactive burst reuse the same secretary rather than paying a cold stand-up (120 s pruned too aggressively for interactive use).
- **R3019:** The CLI-bloodhound secretary pool is a Luhmann-managed class — the first use of the retained supervisor mechanism after recall left (R2800): Luhmann stands up and stops pool secretaries on `next` directives, recording each with `spawn-record` / `exit-record`, and their crashes and quit-earlies feed the same streak machine and `[luhmann]` policy (R2798/R2862). Unlike the former recall class, this pool is Luhmann-hosted — pool secretaries serve every session's CLI hunts, so no single session's assistant owns them.
- **R3129:** `ark luhmann send "<instruction>" [--timeout SECONDS]` is the synchronous producer counterpart to `next`'s drain: where `next` is the orchestrator popping work off the tube, `send` is a caller pushing one instruction onto the same tube and blocking until the orchestrator has handled it, then printing the orchestrator's turns for that instruction. One blocking call performs enqueue → wait → render (Batteries Included). `--timeout SECONDS` (default 120) bounds the wait. Server-required.
- **R3130:** A `next` return carries a fourth kind beyond R3011's three: a **command** — a synchronous CLI instruction pushed by `send`. Its body is the caller's instruction rendered as a **markdown command request** — the CLI does the formatting and the orchestrator reads only the rendered markdown, never a raw wire format (Baby Food). The orchestrator handles a command as an ordinary work item, and like the curation and directive kinds the command crank-handle leads with the re-launch-first instruction (R3036).
- **R3131:** `send` mints a per-command correlation **nonce** and embeds it in the command request as an **inert, backquoted marker** (Watermark): a code literal the orchestrator passes over — it must not nudge the orchestrator's response, since a watermark that changes the turn contaminates what it measures — but a side-signal the server recognizes on the session JSONL tap it already owns. The correlation rides inside the request's own content; the tube's fire-and-forget producers (curation, directive) carry no nonce and are unchanged, so `send` adds correlation without a tube protocol change.
- **R3132:** The rendered window is **bracketed by watermark**: the server watches the orchestrator's session JSONL, opens the bracket where the nonce first appears (the command's delivery as the orchestrator's work), and closes it at the **first turn completion** after that point — the `turn_duration` boundary where the orchestrator yields for input, reusing the RecallWatcher turn signal (R2734/`signalTurnDuration`). Every turn in the window is rendered. Because a single command is one turn even across many tool calls, a `summarize → add an item → discuss` instruction renders as one turn ending in the orchestrator's message or question; `send` never counts turns nor assumes single-turn.
- **R3133:** `send` prints the bracketed turns using the same transcript rendering as `ark chats` with tool calls shown (`--with-tools`), then exits 0. On `--timeout` before a turn completes it exits non-zero, reporting that the instruction was enqueued but no turn completed in time — the enqueue is not undone, so the orchestrator may still act on it. A server bounce mid-wait is a wait condition, not an error (Stubborn Plumbing), consistent with `next` (R3015).
- **R3134:** `send` requires a live orchestrator: when no session owns the tube (`luhmannOwner` empty) it errors orchestrator-not-running — the same live-orchestrator gate the watcher applies before scheduling a CLI hunt (R3020).
- **R3145:** `ark luhmann events --session S` requests Frictionless event routing onto the caller's `next` tube; `--off` releases it. Routing is a privilege of the `next` seat rather than a second identity: the caller must already own the seat (R3012), and a session that does not own it errors `you don't have ownership` (R3013's string, driving the same stand-down reflex). The routing owner is in-memory server state like the lease itself, so a bounce clears it. Server-required.
- **R3146:** While a session owns event routing, ark's server is the **single reader** of the Frictionless event stream: `ark ui event` errors, reporting that an orchestrator owns event routing. Exactly one reader is served because the drain is destructive — each event is delivered once and then cleared from the queue — so two readers would split the stream, each seeing an arbitrary half and neither the whole. When no session owns routing, `ark ui event` is unaffected and serves as it always has; the opt-in changes nothing for a session that never asks.
- **R3147:** A `next` return carries a fifth kind beyond R3011's three and R3130's command: a **frictionless-event** — a Frictionless UI event (the payload a Lua app pushes with `mcp.pushState()`) routed onto the tube while routing is owned, carrying the event's JSON payload. It is fire-and-forget like the curation and directive kinds: nothing blocks on the orchestrator's reply, whose effects reach the user through the app-data mutations the UI reflects and through the conversation itself (only `send`, R3130, blocks). Its crank handle leads with the re-launch-first instruction (R3036).
- **R3148:** Event routing **does not inherit**. It belongs to the session that asked for it, not to the seat, so an orchestrator taking over the seat starts without routing even when its predecessor had it, and must ask for itself. Routing clears when the owner releases it with `--off`, when a *different* session claims or forces the seat (R3013), and on a server bounce along with the lease. The invariant is that the routing owner is either empty or exactly the seat owner. A fresh orchestrator therefore never silently inherits a UI event stream it does not know it is serving; until it asks, the events fall back to `ark ui event` (R3146).


## Feature: Subscriber-presence query
**Source:** specs/subscriber-presence.md

- **R2802:** `db.SubscriberCount(tagName string, tagValue string) int` returns the number of currently-registered subscriptions whose predicate would accept `(tagName, tagValue)` if such a tag were published right now. Match semantics — name normalization, value-mode handling — are identical to publish-time predicate matching in `pubsub.go`.
- **R2803:** `SubscriberCount` ignores per-subscription file filters (`--filter-files` / `--exclude-files`). The query answers "could anyone receive this?", not "would this specific file pass each subscriber's filter?". Over-counting (a subscription whose filter would reject the event counted as present) is acceptable: the failure mode is "we did the work but the event was filtered out", which is the v1 behavior anyway.
- **R2804:** Sessions whose TTL has expired (per the existing reaping behavior in `pubsub.md`) are not counted. The reaper removes their subscriptions before the query runs, or the query treats them as absent.
- **R2805:** `ark subscribers --tag TAG [--quiet]` is the CLI surface for `SubscriberCount`. `--tag` accepts the same sigil syntax as `subscribe --tag` (leading `@` stripped, `[~|:]NAME[(=|:|~)VALUE]` grammar). Default output is the integer count on stdout followed by a newline. `--quiet` suppresses stdout and uses the exit code: `0` if at least one subscriber, `1` if zero. Server-required.
- **R2806:** As a backstop to the watcher activation gate (R2867), the watcher's `fire()` re-queries **both** `SubscriberCount("ark-recall-curate", "<originating-session-uuid>")` and `SubscriberCount("ark-recall-result", "<originating-session-uuid>")` before running the substrate / writing the curation doc. If either is zero (e.g. the consumer dropped during `activation_delay`, after the append-time gate passed), the watcher skips the curation write, clears `pendingChunks` as it would on normal completion, and writes a record to `recall.jsonl` with `outcome: "no-subscriber"`. No fire is counted as missed.
- **R2807:** `ark connections recall close` queries `SubscriberCount("ark-recall-result", "<originating-session-uuid>")` before writing the result doc. If the count is zero, `close` skips the result-doc write, still performs the curation removal + orphan sweep + monitoring-log append per R2758, and records `outcome: "no-subscriber"`. The cleanup side of `close` runs regardless of subscriber presence; only the result-doc *write* is gated.
- **R2808:** The `outcome` field in `~/.ark/monitoring/recall.jsonl` records (R2763) is extended to include the value `"no-subscriber"`. The enumeration becomes `"result-emitted"`, `"silent-close"`, `"no-subscriber"`, `"error"`. The value is written by both gate points (R2806 watcher skip, R2807 close skip). Forward-compatible — R2763's "future fields slot in at the end" property is preserved.
- **~~R2867:~~** (Retired T176 — see R2947) The recall watcher activates per session only while *both* ends of the recall pipe are subscribed: a daemon on the bare `@ark-recall-curate` tag (something to curate the doc) and a client on `@ark-recall-result=<session>` (something to read the result). At each `OnAppend` — after source qualification (R2741) and before accumulation (R2729) — the watcher queries `SubscriberCount("ark-recall-curate", <session>)` and `SubscriberCount("ark-recall-result", <session>)`; if either is zero it ignores the append and **drops the session's in-memory state**: it stops any armed `pendingTimer` and deletes the session's entry from the state map (discarding `pendingChunks`). An unsubscribed session is therefore never accumulated (R2729), armed (R2734), or fired (R2735), and leaves no leaked per-session state.
- **R2868:** Because the activation gate (R2867) drops a session's state while it is unsubscribed and only live appends drive the watcher (no backfill, R2698), a session whose watch (re)activates resumes at the current end of its JSONL: the first fire after (re)subscription processes only chunks appended after activation, never the prior transcript. No persistent per-session recall checkpoint is required — subscriptions are in-memory, so a server restart drops them and re-subscription re-activates at the then-current JSONL end.
- **R2809:** `EventScheduler.EnsureUpcoming` enqueues newly-armed recurring events in-memory (`crankForward(chunk, now, true)`) so they fire within the current `ark serve` session — same-session firing for every recurring tag, not just chimes, without waiting for a restart. `Add` is idempotent per-ID (existing R808, R809), so re-running EnsureUpcoming on an already-armed chunk replaces rather than duplicates. Closes the pre-existing latent gap where EnsureUpcoming wrote the on-disk log but the queue was populated only at startup via ScanScheduleLogs; chimes uncovered the gap first because they're the only auto-created schedule source. Source-file duplication for a tag (e.g. literal `@chime-15m: every 15m` text in a code file) is blocked at config level via `[schedule].exclude_files` (`MatchesScheduleFilterForTag` in the indexer), not in the scheduler.


## Feature: Startup schedule-log reconcile
**Source:** specs/scheduling.md

- **R2810:** `EventScheduler.ScanScheduleLogs` reconciles each schedule-log chunk against the current `[schedule]` configuration on startup. For each chunk in each log file: if the chunk's tag is no longer listed in `[schedule].tags` OR the chunk's `@ark-event-source:` no longer passes `Config.MatchesScheduleFilterForTag(source, tag)`, the chunk is dropped; otherwise the chunk's `@ark-event-upcoming:` entries are validated (past upcomings convert to `@ark-event-fired:`, and `crankForward` ensures one future upcoming exists for recurring specs). If all of a log file's chunks are dropped, the log file itself is deleted. This makes tightening `[schedule].tags` or `[schedule].exclude_files` retroactively prune the matching log entries on next startup, without re-parsing the source files. The source-removal check is the cheap config check — re-validating the source file's tag content remains the indexer's job on its next refresh.


## Feature: Ark-source standard-file coverage
**Source:** specs/schedule-lifecycle.md

- **R2811:** The hardcoded synthetic `~/.ark` source (added by `Config.EnsureArkSource`) includes the ark-managed standard files `chimes.md` and `tags.md` alongside the existing top-level `ark.toml`. Content directories under `~/.ark` are whitelisted per file extension rather than as bare `**` recursion: `schedule/**/*.md` for schedule logs; `apps/**/*.lua`, `apps/**/*.js`, `apps/**/*.html`, `apps/**/*.css`, `apps/**/*.md` for Frictionless app sources; `storage/**/*.md`, `storage/**/*.pdf` for app-managed user data; `external/**/*.md` for external-source mirror chunks. Two effects: (a) ark-managed standard files are indexed regardless of the user's `[[source]]` configuration in `ark.toml` — removing `[[source]] dir = "~/.ark"` does not stop chimes from arming or the tag bible from being indexed; (b) Fossil checkout files (`.fslckout`, `*.fossil`), binary office documents (`*.docx`), undo-tree dumps, and other non-text artifacts under `~/.ark/apps/` and `~/.ark/storage/` are excluded by construction, so they no longer surface as `fts add ...: invalid UTF-8` errors on startup.
- **R2856:** `arkSourceIncludePatterns` includes `skills/**/*.md` so ark-managed agent skill files under `~/.ark/skills/` are indexed. Those entries are symlinks into the repo's skill sources; the scanner's `filepath.WalkDir` descends the real `~/.ark/skills/` directory and visits the symlinked files, and the chunker's `os.Open` follows the symlink to read target content — so the skill is indexed under the `~/.ark/skills/` path. This makes a hermetically-sealed subagent able to bootstrap by fetching its skill (`ark fetch ~/.ark/skills/<skill>.md`); `fetch` serves only indexed content and does not resolve symlinks, so the symlink path must itself be an index key. Consequence: skill content is indexed twice (once under the repo source's `.claude/skills/...` path, once under `~/.ark/skills/...`) — accepted as the cost of keeping the agent-facing path fetchable.


## Feature: Schedule record-only migration
**Source:** specs/migrations/schedule-record-only.md

### Log chunk schema

- **R2813:** Schedule log chunks no longer carry `@ark-event-upcoming:`. The in-memory priority queue is the authoritative "what's next" source. No reader expects an upcoming tag in a chunk; no writer produces one.
- **R2814:** Schedule log chunks no longer carry a current-state `@ark-event-spec:` tag. The active spec for a `(tag, source)` pair is always read from the source file's `@TAG:` value. The log never speaks for the current spec.
- **R2815:** Each log chunk carries exactly one `@ark-event-spec-initial: TIMESTAMP — SPECVALUE` tag recording the chunk's birth time and the spec value at that moment. The timestamp uses `scheduleDateFmt` (`2006-01-02 15:04`); the separator is ` — ` (space, em-dash, space); the trailing text is the verbatim spec value, read as a literal string (never re-parsed through the recurrence parser at read time).
- **R2816:** Each spec change appends one `@ark-event-spec-changed: TIMESTAMP — SPECVALUE` tag to the chunk, in the same format as R2815. Zero or more per chunk. Appearance order in the chunk reflects timeline order.
- **R2817:** Reader contract — historical spec at time T is the spec value from the most-recent `@ark-event-spec-initial:` or `@ark-event-spec-changed:` marker with `timestamp ≤ T`. Era attribution for a `@ark-event-fired:` entry: the most-recent spec marker before that fired entry in document order.

### Source-as-truth queue population

- **R2818:** `EventScheduler.ScanScheduleLogs` is audit-log hygiene + startup queue arming. Its responsibilities are: (a) drop chunks whose tag is no longer declared via a `[schedule.tag.X]` block; (b) drop chunks whose source no longer passes `MatchesScheduleFilterForTag`; (c) drop chunks whose `@ark-event-source:` file is missing or unreadable; (d) delete log files with no surviving chunks; (e) scan for unresolved `@check-gap:` entries within the lookback window; (f) for each surviving chunk, arm the priority queue from the chunk's current spec marker via `crankForwardAndEnqueue`. The arm-from-chunk pass exists because the indexer's reconcile only re-indexes stale or changed files — schedules whose source file is unchanged since the previous startup would otherwise have empty queues until the next source-file edit. `Add` is idempotent per-ID (R808, R809, R2809), so a later `EnsureUpcoming` call from an indexer notification replaces rather than duplicates the startup-armed entry.
- **R2819:** Beyond startup, the priority queue is updated exclusively by the indexer's `EnsureUpcoming` pass — every indexer notification on a source file containing a schedule tag. `EnsureUpcoming(tag, value, sourcePath)` reads the chunk's most-recent spec-marker value, compares it to the incoming `value`, and on difference appends `@ark-event-spec-changed: NOW — value` to the chunk; then re-arms the queue from `value` regardless of whether the log was mutated.
- **R2820:** `EventScheduler.crankForwardAndEnqueue` and any other queue-add site populate `ScheduledEvent.Recurring` from the chunk's current spec marker (or the value passed by `EnsureUpcoming` when called from there). R2812's invariant (Recurring propagated so `fire()` can re-enqueue) is preserved.
- **R2821:** When `ScanScheduleLogs` finds a chunk whose `@ark-event-source:` cannot be opened (file removed, permission denied), the chunk is dropped from the log. Companion of R2810: R2810 retires entries the user excluded via config; R2821 retires entries whose source file ceased to exist.

### Lifecycle categories

- **R2822:** Each declared schedule tag has a `lifecycle` field with one of three string values: `"disk"` (default), `"tmp"`, or `"none"`. The field is read from `[schedule.tag.X] lifecycle = ...` in `ark.toml`. Empty / missing key defaults to `"disk"`. The string-only schema avoids the TOML type ambiguity that would arise from mixing string (`"disk"`/`"tmp"`) and bool (`false`) values in a single field. Exception: the six standard chime cadences (`chime-1m` through `chime-60m`) are auto-declared by `Config.EnsureDefaultScheduleTags` with `lifecycle = "none"` and no `default_duration`; users override by adding an explicit `[schedule.tag.chime-Nm]` block.
- **R2823:** Tags with `lifecycle = "disk"` get an audit log on disk at `~/.ark/schedule/HASH.md` (existing path encoding). The chunk follows R2815-R2817; `@ark-event-fired:` is appended on each fire.
- **R2824:** Tags with `lifecycle = "tmp"` get an audit log at `tmp://schedule/TAG/SOURCE-ENCODED` using the same tilde/underscore/hyphen path encoding the disk log uses. Same chunk schema as disk. The tmp:// doc is created on first fire (or on first `EnsureUpcoming` write of a spec marker), vanishes on server restart per existing tmp:// semantics, and is routed through the centralized tmp:// publish path (per R2281's write-actor invariant).
- **R2825:** Tags with `lifecycle = "none"` have no audit anywhere — no disk log, no tmp:// doc, no spec markers, no fire entries. Real-time consumers receive events via `ark subscribe` / `ark listen` only. The priority queue still arms them; the category only affects audit destination.
- **R2826:** A tag's `lifecycle` value determines audit destination only; arming behavior is identical across categories. `EnsureUpcoming` consults `lifecycle` to decide whether to read/write a log chunk and where; the queue Add is unaffected.

### Audit log trim

- **R2827:** Each `[schedule.tag.X]` block may set `log_cap = N` (positive int, default 1000). At fire time, when appending a `@ark-event-fired:` line would cause the chunk's fired-entry count to exceed `log_cap`, the older half of the existing fired entries is dropped before the new entry is written. Spec markers (`@ark-event-spec-initial:`, `@ark-event-spec-changed:`) are preserved by trim; only fired entries are subject to it.
- **R2828:** `log_cap` applies identically to `lifecycle = "disk"` and `lifecycle = "tmp"` audit destinations. Disk logs are not exempt from trim; users who want longer history with rotation use `~/.ark/schedule-archive/` per the existing convention.
- **R2829:** Trim is per-chunk, not per-file. A log file containing chunks for multiple tags (rare in practice) trims each chunk independently.

### Config schema — `[schedule.tag.X]` blocks

- **R2830:** Schedule tags are declared exclusively by the presence of a `[schedule.tag.X]` block in `ark.toml`. The mere presence of the block declares X as a schedule tag; absence means the tag is not scheduled.
- **R2831:** Per-tag keys recognized in `[schedule.tag.X]`: `lifecycle` (string, R2822), `log_cap` (int, R2827), `default_duration` (string, replaces R854's `[schedule.defaults]`), `filter_files` (string array), `exclude_files` (string array), `suppress` (bool, default false).
- **R2832:** The legacy `tags = [...]` array under `[schedule]` is not parsed by the new code. The legacy `[schedule.defaults]` table is not parsed. The legacy `lifecycle_include` / `lifecycle_exclude` keys under `[schedule]` are not parsed. Hard switch — no deprecation window; users edit `ark.toml` manually to convert.
- **R2833:** `Config.IsScheduleTag(tag)` returns true when a `[schedule.tag.X]` block exists for `tag`, false otherwise. `Config.ScheduleTags()` returns the full set of declared tags by enumerating these blocks.
- **R2834:** `Config.EnsureDefaultScheduleTags` (called at config load) injects synthetic `[schedule.tag.chime-Nm]` entries for the six standard cadences if the user hasn't already declared them. The synthetic entries default to `lifecycle = "none"` (no audit) and no `default_duration` — chimes are heartbeats, not events, and audit + ack-tracking would just accumulate noise (especially `@check-gap:` entries via R965, which is gated by `default_duration != ""`). Users override by adding an explicit `[schedule.tag.chime-Nm]` block to `ark.toml`. `WriteDefault` does NOT ship per-chime blocks; the injection handles the default behavior. Mirrors `EnsureArkSource` (R961) — ark adds defaults that the user can override. The `install/ark.toml` template carries the same blocks.

### Suppress mechanism

- **R2835:** A tag with `suppress = true` is declared (visible to CLI commands, present in the `[schedule.tag.X]` enumeration) but does not arm. `EnsureUpcoming` becomes a no-op for suppressed tags: it neither writes spec markers nor enqueues events.
- **R2836:** On config reload (startup or ark.toml fsnotify), the priority queue drains entries whose tag is now suppressed. Re-enabling (set `suppress = false`) resumes arming on the next indexer notification or restart.
- **R2837:** Past audit history for a suppressed tag is preserved unchanged. Suppress affects future firing only.

### New `ark schedule` subcommands

- **R2838:** `ark schedule upcoming TAG [--all]` prints the next fire for TAG from the in-memory priority queue. Without `--all`, only the head entry for TAG. With `--all`, every queued tag's next fire sorted by NextFire. Output is markdown by default: one line per entry as `@TAG: TIMESTAMP  SOURCE_PATH`. `--json` emits one object per entry. Server-required.
- **R2839:** `ark schedule logs TAG [SOURCE] [-n N] [--json]` reads the audit log for TAG. Without `SOURCE`, lists all sources with a log chunk for TAG. With `SOURCE`, prints the chunk's spec history (all spec markers) and the most recent N fired entries (default 50). Output is markdown by default; `--json` emits a single object `{tag, source, lifecycle, fired: [...], specs: [{kind, ts, value}, ...]}`. For `lifecycle = "none"` tags, prints `(no log — lifecycle = "none")`. For `lifecycle = "tmp"` tags, reads the tmp:// doc (server-required). For `lifecycle = "disk"` tags, the command runs cold (reads the disk log directly, no server required).
- **R2840:** `ark schedule suppress TAG` sets `[schedule.tag.TAG] suppress = true` in `ark.toml` via the existing config-mutation path. The server reloads config; the queue drains matching entries (R2836). If no `[schedule.tag.TAG]` block exists, the command exits non-zero with a diagnostic instructing the user to declare the block first; suppress modifies an existing declaration, it does not create one. Server-required.
- **R2841:** `ark schedule unsuppress TAG` sets `suppress = false` (or removes the key) for the tag. Server reloads; arming resumes on the next indexer notification or restart. Server-required.

### `ark schedule change` tmp:// support

- **R2842:** `Server.handleScheduleChange` (`POST /schedule/change`) detects a `tmp://` prefix on the request path and routes the read through the in-memory tmp:: overlay and the write through `db.UpdateTmpFile`. Non-tmp:// paths use the existing `os.ReadFile` / `os.WriteFile` flow unchanged. The pubsub change notification fires for both flows; tmp:// gets it via `UpdateTmpFile`'s existing R2281-routed publish.
- **R2843:** A tmp:// schedule tag rewrite via `ark schedule change tmp://PATH TAG NEWSTART [NEWEND]` produces the same tag-value-change notification that a disk source produces — subscribers see one `@TAG: NEWVALUE` event, the indexer's deferred schedule processing re-arms the chunk via `EnsureUpcoming`, and the spec-change detection writes a `@ark-event-spec-changed:` marker to the chunk's audit log if the change altered the spec.

### Existing CLI surface updates

- **R2844:** `ark schedule tags` marks suppressed tags with `[suppressed]` and tags with non-default lifecycle with `[lifecycle=tmp]` or `[lifecycle=none]`. The default `[lifecycle=disk]` carries no marker. Output otherwise unchanged.
- **R2845:** `ark schedule search` renders events whose tag is suppressed with a `[suppressed]` prefix on the output line. The search still computes from the suppressed tag's spec — suppression affects firing only, not query results.

### Retired requirements

Retirements landed via `minispec update retire` — T108-T125 in
design.md Gaps mark each retired Rn with replacement and migration
reason.


## Feature: Recurring spec propagated to queued events
**Source:** specs/scheduling.md

- **R2812:** Whenever an event is enqueued from a schedule-log chunk — by `EventScheduler.ScanScheduleLogs` (future-upcoming entries it finds at startup) or by `EventScheduler.crankForward` (the next occurrence it computes after past-upcoming catch-up) — the `Recurring` field of the resulting `ScheduledEvent` is populated with the chunk's `@ark-event-spec:` value (`c.Spec`). Without this, `fire()`'s re-enqueue branch (`if event.Recurring != ""`) never triggers, and a recurring event fires exactly once after server start and then goes silent until the next restart. The bug was latent until chimes (1-minute cadence) exposed it; longer cadences (weekly standups, etc.) were masked by typical server restart frequencies catching the past upcoming on the next boot. Propagating `c.Spec` repairs the contract that both `fire()` and `fireLogMutate` already depend on (each one re-enqueues using `event.Recurring`).


## Feature: Recall substrate v3 — tag-stripped text, tag axis, 2×2 allocation
**Source:** specs/migrations/recall-substrate-v3.md

- **~~R2904:~~** (Retired T141 — see R2913) ark registers a per-chunker content transform (microfts2 `ContentTransform`, attached via `db.addChunker`) on every text chunker (PDF excluded). microfts2 applies it on every content-producing path — index *and* retrieval — so it **removes ark tags from the chunk text** (a full-line tag also removes its trailing newline; an inline tag keeps the rest of its line) and carries the tag-value instances in the chunk's **attributes** (ordered, one per occurrence — deterministic for microfts2's Attrs-inclusive dedup hash). The text that EC-embedding and the trigram index are built from is therefore tag-free, consistently at index and retrieval. Per-chunk tag extraction decodes the attributes (V records); file-level tags + defs are re-extracted from the source content (faithful to content-dedup'd chunks). Forces a one-time re-index + re-embed (operator-run).
- **R2905:** `Librarian.Recall` gains a **tag axis** (retrieval, value→chunk): each tag-value is scored against the input — vector cosine via its EV record, plus trigram-Jaccard computed on the fly from the value string — and the top-scoring values' chunks are pulled in via their V hyperedge records, each carrying the value's score as the chunk's tag-axis component. A chunk can thus be surfaced *because its tags match the input* even when its prose does not. Tag-value trigrams are computed on the fly (~1162 short values); no stored TV record.
- **R2906:** each candidate chunk carries up to four similarity components — `<text-trigram, text-vector, tag-trigram, tag-vector>` — the text pair from content EC/trigram, the tag pair from the value-axis (R2905).
- **R2907:** recall results are allocated across a **2×2 grid** of (main-corpus, conversation) × (meaning, tags), with N chunks per cell (default `3`, set by a `[recall]` config key, R2912). Within a cell, candidates are ranked by that axis's score — `meaning` = max(text-trigram, text-vector), `tags` = max(tag-trigram, tag-vector) — capped at **≤2 chunks per file**, sorted by `<final score, size>` where the size tiebreak prefers **larger** when the winning score was vector and **smaller** when it was trigram.
- **R2908:** a chunk that matches in both a meaning cell and a tags cell is deduped — kept in its higher-scoring cell, with the other cell backfilling. When a cell underfills (e.g. the sparse conversation×tags cell), backfill redistributes across the four cells to reach the per-call target.
- **R2909:** the recall monitor log records each surfaced result's originating cell and its per-component scores, so the per-cell allocation and any weighting can be tuned from real data rather than guessed.
- **R2910:** for the conversation (chat-jsonl) pool, matched JSONL turns are re-chunked with the markdown chunker and ranked at **sub-chunk** granularity: trigram-filter the turns → sub-chunk → sort sub-chunks by trigram similarity → embed only the survivors → vector-check each against the input → sort `<final score, size>`. The surfaced unit is the sub-chunk's `path:range`, not the whole turn. The pre-embed survivor count is a `[recall]` config knob (R2912), logged.
- **R2911:** the derived-tag propose pass scores each recalled chunk's EC against tag-value embeddings (**EV**) in addition to tag-definition embeddings (**ED**), proposing a tag when the chunk resembles an existing *value*, not only the tag's *definition*. The EV leg uses the same `min_propose_similarity` floor as the ED leg.
- **R2912:** new `[recall]` config keys — the per-cell chunk count (default `3`, R2907) and the chat-funnel pre-embed survivor gate (R2910) — tune the allocation and funnel without code changes.
- **R2913:** (supersedes R2904) The **trigram index is full-text** — chunk content is indexed verbatim, ark tags and all, so an FTS query for a literal tag (`@note: bubba`) finds the chunk carrying it. Tag stripping happens on the **meaning axis only**: ark removes ark-tag spans (`stripArkTags` — a full-line tag also removes its trailing newline; an inline tag keeps the rest of its line) from the text it feeds the EC embedder in `BatchEmbedChunks`, and a chunk that is **all** `@tag:` lines strips to empty and is **skipped at embed** (the tag axis carries it; recorded with a nil sentinel EC so it is not re-queued every reconcile — R3004). microfts2 holds no ark-tag logic — the `ContentTransform` hook (commit c5f89d9, index-only in 586a0ae) was rolled back; `AddChunker` is 2-arg again, microfts2 indexes original content, retrieval returns original content, and the chunk-dedup hash is SHA-256 over original content (a tag edit changes content → re-index). Per-chunk tags are re-extracted (`ExtractTagValues` from the chunk's **original** content) in `WithIndexedChunkCallback` and written to F/V records (a content-dedup'd chunk shares the chunkid and its F/V records, so the single fire that lands suffices); file-level tags + defs are re-extracted from the source bytes (this file-level path skips pdf, whose source content is raw bytes; a pdf's tags live in its extracted *chunk* text and are picked up per-chunk like any text chunk). ark's F/V records are the canonical per-chunk tag store — no tag data is duplicated into microfts2 Attrs. Forces a one-time re-index + re-embed (operator-run `ark rebuild`).
- **R2914:** a recall chat sub-chunk (R2910) is addressed by the locator `path:range:"<snippet>"` — the turn's `path:range` plus a string anchor (the matched paragraph's first non-blank line, capped), reusing `@ext`'s string-anchor grammar (specs/at-ext-parsing.md). `ark chunks PATH:RANGE:"<snippet>"` resolves it via `DB.ChatSubchunk`: re-chunk the indexed turn with the same deterministic `chatSubchunks` the funnel runs, return the sub-chunk whose content contains the snippet. Dropping the snippet (fetching `path:range`) returns the whole turn — the zoom-out for fuller context. Chat JSONL is append-only, so the snippet round-trips reliably; an offset locator is deferred (specs/future.md).


## Feature: CLI urfave migration — self-documenting help from the command tree
**Source:** specs/migrations/cli-urfave.md

- **R2916:** ark's CLI is built as a `urfave/cli` v3 `*cli.Command` tree — one node per command and subcommand, each declaring its own `Name`, one-line `Usage`, and `Flags`; `cmd.Run` parses the args and routes to the matching node's `Action`, and `--help` is generated from the tree. Adds the dependency `github.com/urfave/cli/v3` (MIT), imported under an alias distinct from the existing `cli` (which is `github.com/zot/ui-engine/cli`, providing `ExpandVerbosityFlags`).
- **R2917:** every command and subcommand documents itself — its `--help` output is generated from its own `Name`, one-line `Usage`, and its flags' `Usage` strings, with no separate hand-maintained help printer (retiring `printConnectionsHelp`, `uiUsage`, `printConfigHelp`, the `luhmann`/`schedule` usage blocks, and the top-level `usage()`).
- **R2918:** `--help`/`-h` resolves at every node of the tree natively — top-level (`ark --help`), every group (`ark connections --help`), and every leaf (`ark connections recall close --help`) — each showing the full command path. The state-A `config --help` empty-usage bug is gone.
- **R2919:** a parent command's help auto-lists its child commands with their one-line synopses; the parent never restates child help. Help composes down the tree from single per-node sources.
- **R2920:** a command's flag help is derived from its flag declarations, so adding or renaming a flag updates that command's help with no second edit — a flag can never be missing from or stale in its own help.
- **R2921:** a custom-parsing (DSL) command — one that takes all its own raw args rather than letting urfave parse flags — documents itself via a single hand-written `Description` blurb. This is still single-source (one place, drift-proof), just authored rather than auto-derived.
- **R2922:** single-dash-long flag equivalence is preserved — `-scores` ≡ `--scores`, `-k`, `-with`, etc. — because urfave/cli is built on stdlib `flag`, which accepts one or two dashes. No flag, skill, script, or `cli-commands.md` example breaks. (This equivalence is why GNU-style libraries — kong, cobra — were rejected.)
- **R2923:** global flags are recognized before subcommand dispatch — `--dir PATH` and `--dir=PATH` select the database directory (default `~/.ark/`); `-v` increases verbosity, with `-vvvv` expanding to four levels — via urfave global flags.
- **R2924:** the search filter-stack DSL is preserved verbatim — the `search` node receives **all** of its raw args (urfave `SkipFlagParsing`) and hands them to the existing `parseFilterStack` parser unchanged, retaining its order-sensitive sticky `-with`/`-without` polarity and its repeated heterogeneous `(polarity, mode, query)` tuples.
- **R2925:** `search -parse` still prints the disambiguated command (explicit mode flags, quoted values, polarity toggles) and exits without searching, under the custom-parse path.
- **R2926:** exit codes are preserved — `1` = error (matching `fatal()`), `0` = success, and the meaningful non-`1` codes survive: `connections recall next` exits `2` ("done/exit") vs `0` ("handed you a doc"), and the removed connections flags (`--wait`/`--fetch`/`--result`/`--error`) print a one-line hint and exit `2`.
- **R2927:** the error format is preserved — a failing command prints `error: <msg>` to stderr (the `fatal()` shape), not urfave's default error rendering.
- **R2928:** command bodies are unchanged — `tmp://` path handling, server detection (`serverClient`/`withDB`/`requireServer`), server-first proxy-or-cold-start dispatch, and every per-command output format live in the handler bodies and are reached unchanged through an `Action` callback (reading flag values from the command context instead of a `flag.FlagSet`).
- **R2929:** aliases and shims are preserved with identical behavior — `message set-tags` ≈ `tag set`, `message get-tags` ≈ `tag get`, `message check` ≈ `tag check`, and the legacy connections flag shims.
- **~~R2930:~~** (Retired T149 — no replacement) during the staged migration `main()` routes any not-yet-migrated command **by name** to the existing hand-rolled `switch` dispatch (`legacyDispatch`) before urfave parses it — so an un-migrated command's own flags never trip the urfave root, and the CLI works end-to-end at every step. This main-level catch-all and the legacy `switch` are removed when the last command group lands. (A urfave `CommandNotFound` hook was rejected: it fires only for flagless unknown commands — `ark stop -f` trips root flag parsing before it runs — verified by spike.)
- **R2931:** on completion the top-level `usage()` and the per-command `--help` printers no longer exist as hand-maintained strings (they are generated from the tree); `cli-commands.md` remains the hand-kept CLI inventory mirror and behavioral contract, and the CLAUDE.md cross-cutting note ("update `usage()` and every `--help` printer") is rewritten to reflect that the binary's own help is now structurally enforced.
- **R2932:** (inferred) the migration freezes the flag surface — no flag is added, removed, renamed, or switched to GNU-only `--long`. The change is to how a command is *reached* and how its help is *produced*, never to the flags themselves.


## Feature: Sorted human-facing list output
**Source:** specs/cli-commands.md

- **R2953:** human-facing list output is ordered deterministically rather than declaration- or map-iteration order, so it is easy to scan. Every subcommand list in `ark --help` is sorted alphabetically by name at each depth of the command tree (urfave renders commands in slice order; dispatch matches by name, so the sort changes only the help display, not routing). The `strategies:` and `warnings:` lines of `ark status` are sorted by name (both iterate Go maps, which otherwise emit a different order on each run).


## Feature: Bloodhound — directed search via the warm secretary
**Source:** specs/bloodhound.md

- **~~R2933:~~** (Retired T150 — see R2947) Bloodhound recognition rides the recall watcher's existing activation gate and adds no config knob: a session's assistant watermarks are recognized only while *both* `@ark-recall-curate=<S>` (the secretary) and `@ark-recall-result=<S>` (a result consumer) are subscribed — the same both-ends gate ambient recall uses. When recall is off the watermark is ignored (the assistant's fallback is the ephemeral `ark-searcher` spawn, an `ark-search`-skill concern). The gate's write-time backstop applies: if the consumer dropped, no task doc is written.
- **R2934:** The watcher's per-append line scan gains a branch that, for each `type:"assistant"` line in `newBytes`, extracts the text content blocks and matches `<BLOODHOUND …>(.*?)</BLOODHOUND>` (non-greedy, DOTALL so a multi-line payload is captured whole, the opening tag admitting the attribute run of R3184); each captured group is one bloodhound payload. Recognition is deterministic — a regex, no language model — and once-only by construction, since `newBytes` is the newly-appended slice (two identical watermarks are two requests).
- **R2935:** The recognition branch is orthogonal to the turn-boundary machinery — it does not arm, cancel, or read `pendingTimer` / `armReady` / `pendingChunks`, so a watermark dispatches a search task on its own schedule and neither triggers nor suppresses an ambient curation fire.
- **R2936:** Writing the task doc is posted to the watcher's jobs channel (off the indexer goroutine), so `OnAppend` stays a fast line-scan; the regex match itself is the only added synchronous work.
- **R2937:** For each recognized payload the watcher allocates a per-session **bloodhound id** `<B>` — an in-memory monotonic counter (reset on `ark serve` restart; no dir-seeding, since task docs are ephemeral `tmp://`) — writes the task doc at `tmp://ARK-BLOODHOUND/task-<S>-<B>` (the bloodhound's **own tmp:// namespace**, separate from recall's `ARK-RECALL/`, so its docs can never collide with recall's) carrying the `@ark-secretary-work: <S>` tag (the namespace files the doc; the tag drives the secretary's work tube — it already subscribes, so no new subscription), and **retains the raw payload (the clue) keyed by `<B>`** so the finding can echo it back at close.
- **R2938:** The task doc body is the search crank handle (SHERLOCK.md "Build-step 2") with the raw payload pasted verbatim under a `## Search task <B>` first line; that first line is the sole discriminator between a bloodhound doc and a curation doc on the one tube. The crank handle is self-contained (the CLI craft travels in it), so the weak agent executes without planning.
- **R2939:** `next` dispatches a session's pending **bloodhound docs ahead of its pending curation docs** (interactive search served before ambient recall; recall backfills idle time); within each kind, lowest id first.
- **R2940:** A dispatched bloodhound doc is returned like any tube item — the doc body (the search crank handle) plus the close directive framed with `<B>`; `next` recognizes the kind but does not re-author the instructions. Keepalive, context-gate, server-bounce redial, and the foreground window are unchanged.
- **R2941:** `recall-agent-guard.sh` permits the **read-only** search verbs the crank handle uses — `ark search …`, `ark chunks …`, and the read-only `tag` / `files` / `grams` lookups — while `Read`, `Write`, `Edit`, network tools, and every mutating verb stay denied as a class; each denial's stderr remains the runway (Fumble Onboarding).
- **R2942:** The secretary recognizes a `## Search task` doc and follows the crank handle in its body; the search *craft* lives in the crank handle, not the persona (its identity addition is one line — a search task is executed, not curated). The curation path is unchanged.
- **R2943:** A new builder verb `ark connections recall finding <B>` appends **one item per call** (mirroring `surface`): a `-loc <path>:<range>` finding — server resolves content and byte size via `ChunkText(path,range)`, no chunkid on the wire, with an optional `-note "<curated line>"` — or an `-answer "<text>"` finding carrying synthesized text plus its source `-loc`. The requested `want` governs which form and how many calls (`passages`/`pointers`/`inventory` → repeated `-loc`; `answer`/`verdict` → one `-answer`); there are no per-`want` verbs.
- **R2944:** `finding` has **no own-session gate** — a finding whose `-loc` resolves to the requester's own session is accepted, unlike `surface` (a directed search may legitimately point at a chunk already in the requester's session).
- **R2945:** `ark connections recall close <cookie> --nonce <N>` writes `tmp://ARK-BLOODHOUND/finding-<S>-<B>` tagged `@ark-bloodhound-result: <S>` iff any finding was added (else silent-close), removes the `tmp://ARK-BLOODHOUND/task-<S>-<B>` doc, and appends a monitoring record — the same close machinery curation uses. The bloodhound's own tmp:// namespace (`ARK-BLOODHOUND/`, separate from recall's `ARK-RECALL/`) keeps the two independent per-session counters from ever colliding on a path; the cookie carries a kind-marker so the shared `close`/`finding` verbs route to the bloodhound's namespace and its own in-flight maps.
- **R2946:** The result-doc format gains a third H2 kind, `## Finding: <originating clue, echoed verbatim>`, alongside `## Surface:` / `## Recommend:` — optional synthesized answer text followed by `- <path>:<range> (<size>) — <note>` lines. `close` stamps the header with the clue the server retained for `<B>` (R2937), so the echo is the assistant's own words. A finding is a **directed response** the assistant *called for*: it correlates verbatim by that clue and folds the digest into its **own reasoning**, distinct from the *ambient* surface/recommend it gates on "show the user?". Findings are their own docs that **pile up** on the bloodhound's own `@ark-bloodhound-result=<S>` channel and are drained by the assistant's `listen` (which subscribes to it), one at a time; nothing is reused at the doc level, and delivery is asynchronous (the lead arrives a later turn).
- **R3006:** Before writing a bloodhound task doc, the watcher seeds it with the corpus's **deluxe combined search** on the clue — `Librarian.Recall` (per-paragraph, R3043), the same four-substrate combination (`VectorEC`, `TrigramEC`, `TagVector`, `TagTrigram`) ambient recall's fire uses — rendering the top candidates into a `## Recall seed` block placed between the `## Search task` payload and the crank handle. Each candidate is one compact locator line `<path>:<range> (<size>) <score> [tags]` with a short excerpt, carrying the `<path>:<range>` the crank handle feeds to `ark chunks` and **no chunkid on the wire**; an empty result renders an empty-seed note and the task still dispatches. The seed gives every hunt the value→chunk **tag axis** (R2905/R2906) the subagent's own content-only `ark search` cannot reach, and the crank handle's first step reads it before the agent runs its own searches (to widen the trail or when the seed is thin).
- **R3007:** The seed search is **clue-only and session-agnostic** — the input is the clue (split per paragraph, R3043), not the `scope`/`depth`/`want` fields (R3044) and not the conversation, with `RecallOpts.Session` and `.Propose` left off (no discussed-tag exclusion, no derivation side-effects): a directed *pull* hunt should see every match and the seed only reads, so the conversation is **not** folded into the search input (unlike ambient *push* recall, whose input *is* the conversation).
- **R3043:** The Recall seed splits the clue into paragraphs — blank-line-separated, via the same markdown chunker the fire path uses (R2736) — and passes one `Recall` input per paragraph. `Recall` searches each input and **unions** the hits into one ranked top-K through its per-chunk score accumulator (`scoresMap`, keyed by chunkID across all inputs), so each distinct idea in a complex clue contributes its own strong matches. A single-paragraph clue is one input — byte-identical to the pre-split seed (no regression). Rationale: a lone `Recall` over a multi-idea clue embeds the *centroid* of the ideas, matching chunks near none of them and diluting the vector / value→chunk tag-axis reach (R2905/R2906) exactly when the clue is richest.
- **R3044:** Only the **clue** is embedded as seed input (R3043); the `scope`/`depth`/`want` fields are metadata that shape the hunt (they drive the search crank handle and render into the `## Search task` the secretary reads) but are **never** folded into the seed search — they are directives, not search ideas. The task doc still carries every field; the split for seeding operates on the clue alone.
- **R3045:** The seed budget scales with the clue's paragraph (idea) count — `min(base + step·(paras−1), cap)` — so a multi-idea clue's union is not starved by a fixed pool and each idea earns representation; a single-idea clue keeps the base `bloodhoundSeedK`.
- **R3108:** A directed hunt emits `## Recommend:` connecting-tag proposals **alongside** `## Finding:` (Query Crystallization — promoting a proven query into a tag on the chunks it surfaced). The secretary proposes along the clue's polarization angle for the seed chunks it holds (the clue colors the proposed value), skipping a chunk's existing tags, refining an existing tag, or coining a new **name** (definitions stay #5); it emits each via `ark connections recall recommend`, knowing the vocabulary because its agent definition runs `ark tag defs`. The secretary is **disposition-agnostic** and phrases a refinement as English "replace the existing value with …". The deposit targets **only the found chunks** — the bloodhound never receives the current conversation (that is recall's territory).
- **R3109:** A `## Recommend:` fired during a directed hunt is routed into the **bloodhound finding stream** (keyed by the bloodhound cookie), so `close` flushes it into `tmp://ARK-BLOODHOUND/finding-<S>-<B>` alongside the `## Finding:` items under `@ark-bloodhound-result=<S>`. Without this routing a directed recommend lands on the ambient result stream (fire-token keyed), which the bloodhound `close` never touches, and the proposal is silently dropped — the concrete gap this fixes.
- **R3110:** `<BLOODHOUND notags>` suppresses the tag apparatus for **that hunt**: the watcher omits the `[tags]` from the `## Recall seed` lines (R3006) and the crank handle tells the secretary to propose nothing, so the hunt returns findings only. A bare `<BLOODHOUND>` curates.
- **R3184:** The `<BLOODHOUND>` opening tag admits an **attribute run** — the bare `notags` (R3110) plus the repeatable `filter-files="GLOB"` / `exclude-files="GLOB"` scope attributes (R3185). The run must be introduced by whitespace, so `<BLOODHOUNDER>` is not recognized as a watermark; attributes are order-independent and may repeat; an unrecognized attribute is **ignored rather than failing the match**, so a watermark carrying an attribute a future assistant emits still dispatches its hunt.
- **R3185:** A hunt's file scope is carried by two repeatable attributes: `filter-files` is positive — a path must match **at least one** positive glob to be a candidate, and with none present every path is a candidate (the unscoped default stays the norm); `exclude-files` is negative, applied after the positive set, and an exclusion wins over a match. An attribute with an **empty value is ignored** rather than treated as a glob matching nothing, so a stray `filter-files=""` cannot silently empty the corpus.
- **R3192:** A hunt's globs carry exactly the semantics of `ark search -files GLOB` — the same meaning at **both** of a hunt's scope-enforcement points (the Go-side `Recall` seed and the secretary's own CLI searches), since a glob read two ways scopes the hunt to two different things with nothing to say so. A glob beginning `/`, `~`, or `tmp://` is absolute and passes through; any other glob is **relative to the current project** and is joined to it, exactly as the `ark search` CLI joins an unanchored `-files` glob to the client's working directory (R2958). After anchoring, a path matches by basename or by full-path glob — the same test `-files` applies.
- **R3193:** "The current project" is resolved by the surface that knows it, and anchoring happens **once**, so what travels onward is always an absolute glob: a `<BLOODHOUND>` watermark anchors to the **session's own working directory**, read from the `cwd` field of the assistant JSONL line carrying the watermark (every assistant line carries one — no lossy decode of the `~/.claude/projects/<encoded>` directory name and no watcher-held state); `ark bloodhound search` anchors to the **client's** working directory, CLI-side before submitting, as it already does for `ark search -files` (R2958 — only the client knows its own cwd). No working directory is ever inferred server-side and no glob is anchored twice; the seed filters on the absolute glob and the secretary's `ark search -files` receives that same absolute glob, which its own anchoring passes through untouched.
- **R3186:** The scope is applied **at admission, not after ranking** — inside the `Recall` substrate's single per-chunk admission point, so out-of-scope chunks never enter the scored set and the hunt's top-K is computed **within** the scope. The path verdict is memoized **per fileID** (one chunk→file lookup per chunk, one glob evaluation per file), since a hunt touches far fewer files than chunks. Post-filtering the ranked result is rejected: K would be applied first, so a scoped hunt whose global top-K all fell outside the scope would return little or nothing while being indistinguishable from a hunt that genuinely found nothing.
- **R3187:** The scope governs **search candidates only**. Chunks the caller injects as conversation context (`RecallOpts.ConversationChunks`, the `--propose` path of R3082) are the caller's own material rather than search hits and are never path-filtered. A bloodhound seed injects none (R3007), so this is a contract on the shared substrate rather than an observable bloodhound behavior.
- **R3188:** When a hunt carries positive globs and **none of them match any indexed path**, the `## Recall seed` block states that explicitly and names the globs — distinct from the ordinary empty-seed note (R3006). The hunt still dispatches, so the secretary can widen and the reader of the finding learns why the seed was bare. Without the note a mistyped glob is indistinguishable from a corpus that has nothing, and the hunt returns a confident "nothing found" that is wrong.
- **R3189:** The globs never become the secretary's judgment: the crank handle carries the scope as a **ready-made `-with -files` / `-without -files` filter string**, already anchored (R3193) and quoted, which the secretary appends to every search verbatim (Baby Food — it transcribes, it does not compose). The directive rides beside the `no-tags` directive under the `## Search task` header. The Haiku is never instructed to add filters of its own, so a mistyped or forgotten glob cannot be a weak-model decision.
- **R3194:** A hunt's filter string **composes with** the `scope` word's own crank-handle mapping rather than replacing it — both are `-with -files` rows, which intersect, so `scope: code` plus `filter-files="~/work/ark/**"` means Go files under that tree. The seed applies the file globs but **not** the `scope` word (R3044 — a directive, not a search idea); that asymmetry is pre-existing and untouched. The file globs are the part of a hunt's scope made consistent across both enforcement points (R3192).
- **R3190:** A scope is **opt-in per hunt and never inherited** — it lives on the watermark carrying it and nowhere else. Ambient recall does not read the `<BLOODHOUND>` tag and so cannot acquire a scope from it, leaving the standing rule that ambient recall must not filter by current-project scope untouched.


## Feature: Per-capability recall subscriptions (level decoupling)
**Source:** specs/bloodhound.md, specs/simple-recall.md

- **R2947:** (supersedes R2933) The bloodhound is gated by its **own** subscription, decoupled from ambient recall: the watcher recognizes `<BLOODHOUND>` watermarks and dispatches tasks only while the secretary is present (`@ark-secretary-work=<S>` subscriber) **and** the assistant holds `@ark-bloodhound-result=<S>` (the bloodhound opt-in, established by `/bloodhound`'s `listen`). Independent of `@ark-recall-result` (ambient's gate), so a session can run the bloodhound (level 3) with no ambient firing (level 4). The write-time backstop re-checks before each task write; with no bloodhound-result subscriber, no watermark is recognized.
- **R2948:** The secretary's input tube tag is **`@ark-secretary-work=<S>`** (renamed from `@ark-recall-curate`): a single value-scoped work feed the secretary subscribes to via `next`, carrying **all task types** (curation docs + bloodhound tasks), the type distinguished by the doc's tmp:// namespace (`ARK-RECALL/` vs `ARK-BLOODHOUND/`) rather than the tag. Named for the consumer (the secretary), since it feeds hunting as well as curating. The durable subscription-session key aligns to `secretary-work-<S>`.
- **R2949:** Ambient curation firing is gated on the secretary present (`@ark-secretary-work`) **and** `@ark-recall-result=<S>` (the ambient opt-in) — independent of the bloodhound. The watcher accumulates `pendingChunks` and arms the turn-boundary timer only when the recall-result subscriber is present; `OnAppend` drops a session only when the secretary is absent (`@ark-secretary-work` count 0), and within an active session scans bloodhounds iff the bloodhound-result sub is present and arms ambient iff the recall-result sub is present.
- **R2950:** `ark connections recall listen [--ambient]` subscribes **per capability**: the base call establishes `@ark-bloodhound-result=<S>` (findings — level 3); `--ambient` additionally establishes `@ark-recall-result=<S>` (ambient surfaces — level 4), and that recall-result subscription is the ambient opt-in the watcher keys on (R2949). `/bloodhound` runs the base `listen`; `/recall` requires `/bloodhound` and runs `listen --ambient`, reusing the already-spawned secretary (idempotent). Findings and surfaces are distinct tmp:// doc families on distinct tags; one `listen` drains both.


## Feature: Bloodhound CLI — external-app access to the warm bloodhound
**Source:** specs/bloodhound-cli.md

- **R3020:** The CLI bloodhound requires a running Luhmann orchestrator (Luhmann-or-error — no prefer-else-fallback, no multi-listener election). When no Luhmann owns the `next` seat (R3012), `ark bloodhound search` reports the orchestrator is not running, exits non-zero, and submits nothing.
- **R3021:** `ark bloodhound search TERMS... [--wait] [--timeout S]` is synchronous: it (1) creates the request doc `tmp://BLOODHOUND-CLI/<id>` carrying the `TERMS` payload (clue · scope · depth · want) under the watcher tag `@ark-bloodhound-cli` and subscribes to `@ark-bloodhound-cli-result: <id>` before the doc lands — the server accumulates the doc's fields and writes them in one atomic go so the watcher sees a complete request; (2) blocks on its result tag; (3) prints the curated findings as JSONL (one object per line) on stdout.
- **R3022:** `--wait` governs the busy-pool case (no free secretary, pool at `pool_max`): with `--wait` the CLI blocks stubbornly until a slot frees (Stubborn Plumbing — a server bounce is a wait condition, so it redials and keeps waiting); without `--wait` a busy pool fails fast. `--timeout S` bounds the total wait (default `300`).
- **R3023:** The recall watcher is the Fixer for CLI hunts (deterministic Go, no language-model decision), touched on every secretary transition so occupancy and pool bookkeeping stay local to it. On the request tag `@ark-bloodhound-cli` it (a) **enhances** the request doc into a standard bloodhound task doc — adds the `Librarian.Recall` seed (R3006) and the search crank handle (R2938), so a pool secretary runs it identically to an in-session hunt — then (b) **schedules + routes**: a free pool secretary → re-tag the doc to that secretary's `@ark-secretary-work=<pool-sec>` tube (R2948); none free with room (`class.bloodhound.pool_max`) → push a `stand up another secretary` directive onto Luhmann's `next` (R3011) and route when ready; none free with the pool full → a busy error (R3022); too many secretaries idle past cooldown → push a `stop one` directive.
- **R3024:** On the return tag `@ark-bloodhound-cli-return` (a secretary's raw results are in) the watcher **frees the secretary** — it is occupancy-free the instant its return lands at the watcher, *before* curation, and enters a cooldown (`class.bloodhound.cooldown_seconds`) during which it stays warm and preferred for the next hunt; only past cooldown is it eligible for a `stop one`. There is no separate occupancy state machine (free means the return is back at the hub) — then the watcher pushes the request-doc path onto Luhmann's `next` queue as a curation crank-handle (in-process; no doc-tag hop).
- **R3025:** The CLI return is a discernment split carried on the request doc's tag baton: (1) the pool secretary hunts, appends its **raw** results to the request doc, and re-tags it `@ark-bloodhound-cli-return`, handing it back to the watcher (not to Luhmann, not to the CLI), which frees it (R3024); (2) the watcher pushes the request-doc path onto Luhmann's `next` queue (R3011); (3) Luhmann **refines** the raw results and writes the **result doc**, emitting each kept item via `ark bloodhound add` (R3027) and tagging `@ark-bloodhound-cli-result: <id>` to wake the CLI.
- **R3026:** Curation is the **default** (every CLI finding gets Luhmann's judgment) but **opt-out** via `--raw` (R3038–R3040) and decoupled from occupancy — the secretary was already freed at the return (R3024), so curation costs only the CLI's own latency, never a held pool slot. Luhmann's opt-in to serve CLI curation is simply owning the `next` seat (`--first`, R3013); there is no separate parent-signal subscription — the ownership lease is the opt-in, and a session not draining `next` serves no CLI hunts.
- **R3027:** `ark bloodhound add --result tmp://BLOODHOUND-CLI/<id> --loc PATH:RANGE --note NOTE [--chunk TEXT]` is Luhmann's result stencil: each call appends one JSON line (`{path, range, note, chunk?}`) to the **result** doc, one item per call (Stencil pattern, the discipline of `surface` / `recommend` / `finding` — the `--loc` locator matches those siblings). A terminal call `ark bloodhound add --result tmp://BLOODHOUND-CLI/<id> --done` writes the result doc `tmp://BLOODHOUND-CLI-RESULT/<id>` and flips its tag to `@ark-bloodhound-cli-result: <id>`, the notification the waiting CLI is subscribed to; it also drops the internal request doc. An empty hunt (`--done` with no prior `add`) writes an empty-body result doc (R3029). The pool secretary's raw results go instead to the **request** doc via the existing bloodhound builder path (R2943/R2945), re-tagging it to `@ark-bloodhound-cli-return` — two write targets, two docs. (The `--id/--path/--chunk` flag shape was provisional at design time; the `--loc/--note/--chunk` + `--done` contract was finalized at implement, 2026-07-05.)
- **R3028:** Three new tags carry the CLI path (names provisional, `@ark-*` family), plus one reused tube: `@ark-bloodhound-cli` (request — the CLI writes the request doc under it, the watcher wakes on a new hunt); `@ark-secretary-work=<pool-sec>` (**reused**, R2948 — the watcher re-tags the request doc to the chosen pool secretary's value so its `next` picks up the hunt); `@ark-bloodhound-cli-return` (the secretary re-tags the request doc to it when its raw results are in, the watcher wakes to free the secretary and route to curation); `@ark-bloodhound-cli-result: <id>` (value-scoped to the request id — Luhmann tags the result doc, the CLI wakes only on its own result). The watcher → Luhmann hop uses no tag (in-process `next` push). Request docs live in the `tmp://BLOODHOUND-CLI/` namespace, separate from `ARK-RECALL/` and `ARK-BLOODHOUND/`.
- **R3029:** `ark bloodhound search` prints one JSON object per line, each a curated finding carrying at least `path`, `range`, and a curated `note`. An empty hunt prints no lines and exits zero, so a client can treat "no output" as "no findings."
- **R3030:** The CLI-bloodhound pool secretaries reuse the in-session bloodhound's secretary seal, search crank handle, Recall seed, and finding/close mechanism (bloodhound.md, R2937–R3007); the CLI path adds only its own request-doc submit, scheduler/router, tag baton, curation split, and JSONL output, and does not change the in-session watermark path.
- **R3031:** The pipeline is the Fixer pattern with the request doc's tag as a routing baton: the tag is rewritten at each hop (`@ark-bloodhound-cli` → `@ark-secretary-work` → `@ark-bloodhound-cli-return`) to summon the next stage, and every hop is atomic — a stage accumulates its additions and, in one write-actor flush, appends its content **and** rewrites the tag, so the next stage never wakes on a half-built doc (the tag flips because the content is ready). The baton passes CLI → watcher → secretary → watcher → Luhmann → CLI; the sole non-tag hop is watcher → Luhmann (in-process `next`), and the refined result is a separate doc so the CLI never sees the internal request doc.
- **R3032:** A pool secretary's `<pool-sec>` routing identity (R3028) is the composite `<luhmann-session>-<nonce>` — the Luhmann seat owner (the `luhmannOwner` lease, R3012) plus the secretary's reserved nonce. Each pool secretary therefore owns a unique `@ark-secretary-work=<luhmann-session>-<nonce>` tube (no shared queue, no per-hunt claim), and because the value is a real session id with the nonce appended after a dash it keeps the existing `<session>-<segment>` shape, so the pool secretary runs the ordinary `ark connections recall next --session <luhmann-session>-<nonce>` and every subscribe/route/dispatch path works unchanged. The watcher composes the same key from `luhmannOwner` (S1) and the reserved nonce, so both sides agree with no report-back.
- **R3033:** `ark connections recall reserve-nonce --luhmann` reserves a nonce off the monotonic counter (R2755) **and** registers it in the watcher's pool roster (`cliPool`) as a pending pool secretary, so the reservation doubles as pool registration and the watcher (the go-between) learns the nonce Luhmann will spawn with, with no separate report-back call. Plain `reserve-nonce` (no flag) is unchanged.
- **R3034:** A terminal `ark luhmann exit-record` for the bloodhound class (kind `exit` / `crash` / `quit-early`) **deregisters** the pool secretary's nonce from the watcher's roster (`cliPool`) — the symmetric counterpart to R3033's registration. A secretary that exits on its own context limit (not via a watcher `stop` directive) therefore no longer counts toward `pool_max` and is never routed a hunt to its now-dead tube; any lingering `inflight` entry for it is dropped in the same step. Idempotent and self-converging: a no-op for other classes, non-terminal kinds, or an already-removed nonce, so a `prune`-driven stop and an independent self-exit reconcile safely. (Recovery of a hunt lost mid-flight when its secretary dies is out of scope — the CLI's `--timeout` bounds that case.)
- **R3037:** `--markdown` on `ark bloodhound search` renders the curated result client-side as a markdown locator list — one `- ` + `path:range` + ` — note` line per finding, the `chunk` excerpt as a blockquote when present, and a "no findings" line for the empty result. A pure client-side render of the JSONL the CLI already holds (R3029): no server or protocol change. JSONL stays the default and `--markdown` is opt-in, matching the `--json`-bool output convention elsewhere. (Baby Food — the chewed markdown view an agent reads, vs. the JSONL a script consumes.)
- **R3038:** `--raw` on `ark bloodhound search` opts out of Luhmann curation: the CLI writes `curate: false` into the request doc's `TERMS` payload. The watcher records the per-request curate intent in memory keyed by `<id>` when it first wakes on `@ark-bloodhound-cli` — the request-doc body is overwritten at later baton hops (enhance, secretary close), so the flag cannot survive there. A server bounce drops the in-memory intent along with the rest of the roster, and the CLI's `--wait`/`--timeout` re-drives the whole hunt (consistent with R3023's in-memory pool state).
- **R3039:** At the return hop `@ark-bloodhound-cli-return`, the watcher branches on the recorded curate intent (R3038): for a **raw** request it writes the result doc `tmp://BLOODHOUND-CLI-RESULT/<id>` directly from the request doc's `## Raw findings`, flips its tag to `@ark-bloodhound-cli-result: <id>`, drops the request doc, and **skips the Luhmann `next` push**; for a **curated** request it pushes the request-doc path onto Luhmann's `next` queue exactly as R3025. The secretary is freed into cooldown (R3024) identically either way — the branch is purely about who curates, not about occupancy.
- **R3040:** `--raw` output is the secretary's own findings relayed verbatim — the markdown locator lines it wrote to `## Raw findings` (`- path:range (size) — note`); the empty case relays the secretary's `(no findings)` body. This is the middle point of the curation-ownership axis: local `/bloodhound` (you spawn the secretary + you curate), remote `--raw` (Luhmann's warm secretary + you curate, in your own context, no agent management), remote curated default (Luhmann's secretary + Luhmann curates). The CLI prints per the flag it sent — `--raw` → the relayed markdown verbatim (already Baby Food); else the curated JSONL (R3029), `--markdown`-rendered on request (R3037). Because raw output is already markdown, `--markdown` is redundant with `--raw`.
- **R3041:** `class.bloodhound.request_ttl_seconds` (int, default `900`) is the reap TTL for a stranded CLI-bloodhound request. `ark bloodhound search` has no clean abandonment signal — on `--timeout` it simply exits — so a periodic sweep on the watcher's worker reaps any request older than the TTL: it drops the request from the roster's pending / in-flight sets and removes its request doc, so a later stand-up never wastes a hunt on a query nobody awaits. The TTL is set generously longer than a typical `--timeout` (default 300) so a live client is never reaped.
- **R3042:** The same periodic watcher sweep re-issues a dropped stand-up: for any request that has sat pending and unrouted past a re-issue threshold while the pool has room, the watcher re-pushes a `stand up another secretary` directive — bounded by `pool_max` and the count of already-in-flight stand-ups so it never over-spawns. This recovers a work item dropped when Luhmann misses a stand-up directive (a drift or garbled tool call mid-turn) — the item R3036's re-launch-first cannot recover on its own (re-launch-first keeps the loop alive; the dropped *item* still needs re-issuing). Best-effort: a client that already gave up gets nothing either way.
- **R3046:** `ark bloodhound search` takes the clue from positional `CLUE...` (joined into one line — the one-liner case) or `--file PATH` (read the clue from a file, markdown), with `--file -` reading stdin for a **heredoc**-supplied multi-paragraph clue; `CLUE...` and `--file` are mutually exclusive (error if both are given). The `scope`/`depth`/`want` flags are metadata (R3044), separate from the clue. `argv` cannot carry paragraph breaks, so the file/heredoc path is what lets a CLI caller supply the multi-idea clue the per-paragraph seed (R3043) splits — making the CLI a first-class multi-idea search client. The CLI reads the file byte-for-byte (fidelity by construction, as with the messenger's `--content-file`).
- **R3191:** `ark bloodhound search` takes the repeatable `--filter-files GLOB` / `--exclude-files GLOB` flags, carrying exactly the scope semantics of the in-session watermark's attributes (R3185, R3192) with the client's cwd as the anchoring project (R3193). The scope rides the request doc alongside the clue, reaches the same admission-time filter in the `Recall` seed (R3186), renders into the same ready-made secretary filter string (R3189), and reports a zero-match positive glob in the seed block (R3188). An external client scopes a hunt the same way an in-session assistant does — one mechanism, two front doors.


## Feature: yzma embedding engine
**Source:** specs/migrations/yzma-embedding.md

- **R2961:** The in-process embedding engine binds llama.cpp via yzma (`pkg/llama`, purego/ffi), loading the shared library at runtime; embeddings require no CGO.
- **R2962:** (supersedes R1595) Each tier context is a yzma `ContextParams` — `NCtx` from the tier `ctx`, `NSeqMax` from `parallel`, `NBatch`/`NUbatch` sized for the encoder ubatch, `Embeddings=1`, `PoolingType=Mean`; the model loads with GPU offload via `NGpuLayers`.
- **R2963:** Embedding reads copy the `GetEmbeddingsSeq` result, which aliases llama.cpp's internal buffer; the alias is never retained across calls, so distinct inputs yield distinct embeddings.
- **R2964:** (supersedes R1274) `[embedding] model` specifies the GGUF embedding model filename under the ark dir — used for chunk, tag, and query embeddings; empty disables vector-EC.
- **R2965:** (supersedes R1588) `[embedding] tiers` is an array of `{ctx, parallel}` entries configuring the adaptive embedding tiers (`ctx`→`NCtx`, `parallel`→`NSeqMax`).

## Feature: llama.cpp library provisioning
**Source:** specs/llama-libs.md

- **R2966:** `[embedding] lib_dir` (default `<dir>/lib`, beside the index) holds the llama.cpp shared libs; the engine `dlopen`s them at startup.
- **R2967:** `[embedding] backend` selects the llama.cpp build — `auto`, `cpu`, `vulkan`, `cuda`, `metal`, `rocm`. `auto` detects CUDA, else ROCm, else Metal (macOS), else Vulkan when a device exists, else CPU.
- **R2968:** `[embedding] llama_version` pins the llama.cpp release build to provision (within yzma's tested range), keeping release builds reproducible.
- **R2969:** Provisioning downloads the libs for `(platform, backend, version)` into `lib_dir`; it runs during `ark setup` and as a standalone command, is idempotent (skipped when present unless an upgrade is requested), and may fetch the GGUF model when absent.
- **R2970:** When a `model` is configured but `lib_dir` lacks the libs, embedding operations fail with a clear error naming the provisioning command — not a silent drop to FTS-only.

## Feature: Pure-Go build & cross-platform release
**Source:** specs/migrations/yzma-embedding.md

- **R2971:** Ark builds with `CGO_ENABLED=0`; the Makefile no longer builds gollama/llama.cpp from source (the `gollama`/cmake/Vulkan recipe and the `build: gollama` dependency are removed).
- **R2972:** The Makefile provides a `release` target that cross-compiles ark across the supported `GOOS/GOARCH` targets and grafts bundled assets onto each via `ark bundle -src`, producing per-platform release archives.
- **R2973:** The embedding engine silences llama.cpp's own stderr logging (backend device, GPU offload, per-tensor load) by default; the global verbosity at level 3 or above (`-vvv`, above ark's own `Logv` usage which tops out at level 2) leaves it on for confirming GPU offload or debugging the engine.

## Feature: LMDB→BBolt Migration (ark consumer half)
**Source:** specs/migrations/lmdb-to-bbolt.md

- **R2974:** ark binds `go.etcd.io/bbolt` instead of `github.com/bmatsuo/lmdb-go` — removing the last CGO dependency in the ecosystem (the enabler for the `CGO_ENABLED=0` build and release sweep, R2971/R2972).
- **R2975:** ark does not own the database; `OpenStore` takes microfts2's `*bbolt.DB` (`fts.DB()`) and opens/creates an `"ark"` bucket inside it (was: an `ark` named DBI inside microfts2's shared LMDB env). `Store.bolt *bbolt.DB`; the `ark` bucket is obtained per txn via `tx.Bucket`.
- **R2976:** Cross-repo atomicity is preserved — a `bbolt.Tx` spans the `fts` and `ark` buckets, so the ~18 sites reading microfts2 C-records and ark records in one transaction use `tx.Bucket("fts")`/`tx.Bucket("ark")` on a single `*bbolt.Tx`.
- **R2977:** ark consumes microfts2's bbolt API: `fts.DB()` (was `Env()`); `fts.ReadCRecord(tx *bbolt.Tx, …)`; `RemoveCallback`/`ReindexCallback` closures and the `SetChunkResolver` closure take `*bbolt.Tx`.
- **R2978:** ark stops passing `microfts2.Options{MaxDBs, MapSize}` — both fields are removed from microfts2's Options.
- **R2979:** ark store operations map to bbolt: `env.Update/View`→`bolt.Update/View(tx)` deriving the `ark` bucket via `tx.Bucket`; `txn.Get`+`lmdb.IsNotFound`→`bucket.Get` returning nil for absent keys; `txn.Del`(+IsNotFound guard)→`bucket.Delete` (no error on missing, guard dropped); `txn.Put(…,0)`→`bucket.Put`; cursors→`bucket.Cursor()` with `Seek`/`First`/`Next`. The value-valid-only-within-txn contract is unchanged; the existing copy-out discipline is preserved.
- **R2980:** `scanPrefix` is reimplemented delete-safe (collect-then-delete): it walks the prefix range read-only collecting matches (copying key bytes), invokes the per-item callback, and applies any deletes by key after the walk — so the ~15 delete-during-scan callers remain correct under bbolt's page-rebalancing.
- **R2981:** `compact.go` replaces `env.CopyFlag(lmdb.CopyCompact)` with `bolt.Tx.WriteTo` (or a no-op); the `SetMapSize(2<<30)` calls are removed.
- **R2982:** `StatusInfo` reports the database file size (`os.Stat`) in place of LMDB MapSize/MapUsed; `ark status` output adjusts accordingly.
- **R2983:** Before committing, a full `ark rebuild` + search-workload benchmark (BBolt vs LMDB) is run as the gate; the batched write actor (`enqueueWrite`) is expected to neutralize bbolt's fsync-per-commit cost on bulk writes.

## Feature: Rebuild read-only serve
**Source:** specs/rebuild-read-serve.md

- **R2984:** bbolt is single-process — opening `index.db` holds an exclusive file lock, so a standalone `ark rebuild` blocks any other process that opens the database directly (the cross-process MVCC LMDB allowed is gone).
- **R2985:** During the scan phase of a rebuild, ark keeps a read-only server listening on the unix socket, so read commands (`status`, `search`, …) proxy to it and return live, growing results instead of blocking on the file lock.
- **R2986:** The rebuild's indexing runs through the normal write-actor path so co-resident reads stay responsive (heavy indexing off the actor), race-free (reads ride the actor like any server read), and consistent (each read sees a committed snapshot).
- **R2987:** The rebuild's read-only window refuses write/mutation requests (`add`, `remove`, config changes, tmp:// writes) with a "rebuild in progress" error rather than racing the rebuild.
- **R2988:** The rebuild server is the normal server with background subsystems switched off (filesystem watcher, scheduler, embedded UI engine, recall watcher, spectral-search librarian / pubsub reaper); the switches live in the serve options (ServeOpts), each defaulting to on for `ark serve` and off for rebuild.
- **R2989:** A rebuild server runs the scan once and exits when the write queue has drained — every file the scan enqueued indexed and committed; "write queue drained" is the completion signal the rebuild waits on (the idle primitive).
- **R2990:** (inferred) The drop window — while `ark init` deletes and recreates the database file, before the read-only server binds the socket — is not covered by the read window; a read arriving then may briefly block on the file lock. Accepted as short and self-clearing.

## Feature: CLI Proxy/Local Dispatch
**Source:** specs/cli-dispatch.md

- **R2994:** `ark.OpenWithTimeout(dbPath, timeout)` threads a bbolt lock timeout (microfts2 `Options.Timeout`, R672) into the index open so a local open fails fast (`bbolt.ErrTimeout`) instead of hanging while a server holds the single-process index. `ark.Open` delegates with a zero timeout (block until available) for the server's own startup open, which owns the index.
- **R2995:** `proxyOrLocal(proxy, local)` is the central dispatch point for DB-touching CLI commands: when a server's unix socket accepts a connection it runs `proxy`; otherwise it opens the index locally (bounded lock wait) and runs `local`. A nil `proxy` means the command has no server endpoint and needs exclusive local access; with a server running it fails fast ("a server is running and holds the index; stop it with `ark stop`") instead of hanging.
- **R2996:** Dispatch is stubborn (Stubborn Plumbing): a transport-level proxy failure (`client.Do` error, detected as `*url.Error`) is treated as a server bounce and retried until a stubborn window elapses, not surfaced immediately; an application error from a live server (HTTP non-200) is reported at once without retry.
- **R2997:** On a local-open lock timeout (`bbolt.ErrTimeout`) — something holds the index though no server answered the dial (a race with a server coming up, or a bounce in progress) — `proxyOrLocal` loops to recheck server liveness and re-dispatches, surfacing a real error only after the stubborn window closes; a non-timeout open error fails at once.
- **R2998:** `ark chunks` dispatches through `proxyOrLocal`. `POST /chunks` and the CLI local arm both resolve content via `DB.FetchChunkContent` (anchor → chat sub-chunk R2914; empty path → chunkID-only via `ChunkInfo`; else `path:range` via `GetChunks`), returning a `ChunkFetchResult`; `emitChunkResult` formats both arms so output cannot drift.
- **R2999:** `ark grams` dispatches through `proxyOrLocal`. `POST /grams` and the CLI local arm both build decoded `GramCount`s via `DB.GramCounts`.
- **R3000:** `ark schedule tags` dispatches through `proxyOrLocal`. `POST /schedule/tags` and the CLI local arm both build the summary lines via `DB.ScheduleTagSummary(showValues)` (R2844 stable order and R1033/R1034 `--values` detail live there now).
- **R3001:** `ark tag values` dispatches through `proxyOrLocal`. `POST /tags/values` gains a `files` flag selecting the file-resolved `[]TagValueFileInfo` variant for the `--files`/filter path; the flagless form returns `[]TagValueCount` unchanged for the editor. QueryTagValues already sorts by count desc so proxy and local order match.
- **R3002:** `withExclusiveDB(fn)` is `proxyOrLocal` with a nil proxy — for commands that keep their fatal-internally local body and have no server endpoint: `embed text`, `embed bench`, `embed validate`, `config recover`. With a server running they fail fast rather than hang.
- **R3003:** DB-touching CLI commands dispatch through the central `proxyOrLocal` helper (server-first with local fallback) rather than opening the index directly via `withDB`.


## Feature: tag-derived record subsystem (RC/RJ re-key + producer inversion)
**Source:** specs/migrations/tag-derived-subsystem.md

- **R3058:** The `RC` record class is re-keyed to `"RC" + source_tvid varint + target_chunkid varint`, where `source_tvid` is the tvid of the `@ext-candidate` tag whose TARGET named the chunk. Value: varint tally, materialized from the `@count` field of the `@ext-candidate` line (R3074). The proposed tagname and value are **not** stored in the record — both are recovered from the source tvid via `TvidMap.Resolve(source_tvid)` → the `@ext-candidate` value → `ParseExtTarget`, exactly as X recovers its routed tag. Supersedes the `chunkid + tagname → 8-byte tally` shape of R2664.
- **R3059:** The `RJ` record class is re-keyed to `"RJ" + source_tvid varint + target_chunkid varint`, mirroring the new RC key. The value keeps the v3 shape (`signed-varint(score) + 8-byte BE unix nanos`, R2874); the score is materialized from the signed `@count` field of the `@ext-judgment` line (R3074) and the nanos records the derivation time. Supersedes the `chunkid + tagname` key shape that R2665 and the v3 judgment requirements (R2874, R2879) assumed.
- **R3060:** X, RC, and RJ form one tag-derived record family, uniformly keyed `<class-letter> + source_tvid + target_chunkid` and derived from `@ext` / `@ext-candidate` / `@ext-judgment` respectively. The routed `(tagname, value)` is recovered from the source tvid and is never duplicated into the derived record — the same contract X already uses. The value lives once, in the source line's own V record.
- **R3061:** `collectIndexExtPlans` (indexer.go) derives all three classes: it matches `tagExt`, `extCandidateTag`, and `extJudgmentTag` (previously `tagExt` only, indexer.go:431) and builds a per-class derivation plan for each matching chunk tag. Supersedes R2667's statistical-pass path as the RC/RJ derivation source.
- **R3062:** `ExtMap` branches at the write step by class. `@ext` writes an X record, appends the routed tag to the target's V record (the live edge), and bumps `virtualTagCount` (unchanged). `@ext-candidate` writes an RC record only; `@ext-judgment` writes an RJ record only. Candidate and judgment never write the routed V edge and never bump `virtualTagCount` — the derived record stands in place of the live edge, which is the entire proposed-vs-committed-vs-judged distinction.
- **R3063:** RC/RJ derivation is persistent-only: a candidate or judgment whose source or target is an overlay (`tmp://`) chunk writes no derived record. There is no overlay analog of RC/RJ this pass (the X-record `overlayRoutings`/`overlayValues` machinery is not extended to the new classes).
- **R3064:** RC/RJ inherit the `@ext` lifecycle end to end — index-time derivation, `ExtMap.ReresolveOnReindex` (re-resolve the TARGET when the target file reindexes), `ExtMap.CleanupSource` (drop the derived records when the source chunk orphans, discovered via the source F-record tvid trailer), and startup `Rebuild` (scan RC/RJ to repopulate their in-memory maps). Keying by source_tvid is what lets them re-resolve and clean up like X, dissolving the chunkid-orphan strand: the state-A gap where the chunkid-orphan callback cleaned EC/F/V/T/X but never RC/RJ/RF.
- **R3065:** `candidateSourcesByChunk[target_chunkid] → []source_tvid` is the in-memory reverse lookup answering "which candidates were proposed on chunk C." Maintained on index / reindex / cleanup and rebuilt at startup, in the pattern of `routedTagsByTvidExt`. It replaces the RC bbolt prefix scan.
- **R3066:** `rejectByChunk[target_chunkid][tagname] → score` is the in-memory judgment lookup for the reject filter, maintained fresh and rebuilt at startup. "Is tag T net-rejected on chunk C" is one map hit with the negative-score check riding along; the score is derived from the `@ext-judgment` tag (present → negative). Supersedes the direct-key `ReadJudgment` / `HasDerivedRejection` lookup (R2876, R2878) for the reject-filter read path.
- **R3067:** `Store.DerivedProposals` reads `candidateSourcesByChunk`, then `TvidMap.Resolve(source_tvid)` + `ParseExtTarget` to recover each candidate's `(tagname, value)`. `DerivedProposal{ChunkID, Tagname, Tally}` is unchanged in shape and `DerivedProposals` returns tally-descending (R2678's original ordering, valid now that the tally is a real `@count`-materialized value). Supersedes R2678's RC-prefix-scan read. This is the retained **forge-facing** reader (#37); the recall-path `enrichProposedTags` reader that R3067 also formerly covered is superseded by R3080 (compute-for-display).
- **~~R3068:~~** (Retired T252 — see R3079) `runDerivationPass` inverts: each surviving statistical candidate is authored as an `@ext-candidate` file tag through the mirror authoring path (`DB.CandidateExtTag`), not written as a bbolt RC record. The indexer derives RC on the subsequent reindex. Supersedes the direct RC write of R2674 and the RC half of the batched write in R2675.
- **R3069:** `Store.RejectDerived` inverts: it authors an `@ext-judgment` file tag through the mirror authoring path (`DB.RejectExtTag`), not a direct bbolt RJ write. Because reject is idempotent at the file (R3055), the derived RJ is binary present/negative — the accumulated `-1, -2, -3` magnitude of R2877 does not survive file-backing (see R3074). Supersedes the RJ-write half of R2680 / R2877.
- **R3070:** The reject filter lifts out of the statistical pass into `rejectByChunk`, so every producer (statistical level 1, and the later secretary/parent levels) inherits "skip an already-net-rejected `(chunk, tag)`." Supersedes R2673's inline `RJ[chunkid + tagname]` existence check.
- **R3071:** The accept loop closes by construction: `ark ext accept` (R3054) rewrites `@ext-candidate` → `@ext`; on reindex the RC derivation drops and the X + V edge lands, so committing a tag consumes its candidate with no separate bbolt step. `Store.AcceptDerived` is **re-homed to the mirror path**: it resolves the chunk's locator and delegates to `DB.AcceptExtTag`, superseding R2679's direct RC-delete + `AppendTagValues` implementation. It survives as the file-backed accept primitive the forge wiring (#12) will call, symmetric with the inverted `Store.RejectDerived` (R3069); both edit the mirror file rather than writing bbolt records directly.
- **~~R3072:~~** (Retired T255 — no replacement) RF (`"RF" + chunkid`, freshness skip stamp) is unchanged. `runDerivationPass` still computes candidates and skips fresh chunks (R2669); only the survivor action changes (author a file tag instead of writing bbolt RC). RD and RM (session-scoped throttles) are untouched.
- **R3073:** The migration is clear-and-rebuild, operator-driven, like the RJ v3 reset (R2880): `ark connections clean -all -checkpoint` wipes the old-key RC/RJ records; they carry no source tvid and cannot be salvaged into the new key, so they re-derive from the `@ext-candidate` / `@ext-judgment` file tags that now back them. There is no in-place re-key — the old (`chunkid + tagname`) and new (`source_tvid + target_chunkid`) shapes are structurally incompatible.
- **R3074:** Mutable counts live in an `@count` field on the `@ext-candidate` / `@ext-judgment` file line and are materialized into the RC tally and the signed RJ score. `@count` is a reserved metadata field: `ParseExtTarget` excludes it from the routed-tag list (like `@insight`) but it is **retained in the tag's value string**, so the outer tag's V record mirrors the file line faithfully — the index is a performance mirror of the source, never a filtered view. A signed `@count` on `@ext-judgment` carries both judgment directions on one field (negative = net-rejected magnitude, positive = reinforced popularity tally — R2879 / R2881); `@count` on a committed `@ext` is excluded from routing and not materialized. This keeps the mutable accumulators file-backed (surviving rebuild and re-chunk) with the bbolt RC/RJ records holding only a cached copy — no separate bbolt counter, no deferral.
- **R3075:** A count change is a read-modify-write on the mirror line, executed as one closure-actor operation (R986) so concurrent producers cannot lose an update. `DB.CandidateExtTag` on an exact-identity duplicate (same TARGET, insight, tag, value) increments the line's `@count` rather than no-opping (superseding R3053's silent no-op); `DB.RejectExtTag` creates or decrements the signed `@count` on the `@ext-judgment` line; a `@count` that reaches 0 removes the judgment (absent ≡ neutral, R2881). Because `@count` is part of the tvid-bearing value, a bump changes the source tvid: the source-chunk reindex removes the old-count derived record via `CleanupSource` and derives the new one with the new count (R3064). The count survives the tvid churn because the file line is the source of truth; the churn is accepted as the cost of a faithful V mirror.
- **~~R3076:~~** (Retired T253 — see R3079) The propose pass **synchronously materializes** its own proposals so they surface in the same recall call, not on a later call after the async watcher pass. After `runDerivationPass` authors the surviving `@ext-candidate` file tags and stamps RF (all inside one closure-actor op), it reindexes each **distinct** touched mirror once — deduped by target file, so a mirror carrying several proposals reindexes once and authoring for all proposals completes before the sync — via `DB.syncOnePath` (the same source-resolved Add/Refresh dispatch the watcher uses, run on the actor the caller already holds). The reindex derives the RC records so `enrichProposedTags` reads them from `candidateSourcesByChunk` in the same call. Embedding stays deferred to `BatchEmbedChunks`, so the added cost is only the FTS + tag-extraction + derivation of tiny mirror files (measured ~0.14 ms/mirror). This restores the same-call proposal visibility the pre-inversion synchronous RC write had, without giving up the file-is-truth model (R3068).

## Feature: recall proposals for display (discernment-gated authoring)
**Source:** specs/migrations/recall-proposals-for-display.md

- **R3079:** The `--propose` derivation pass is compute-for-display: it computes candidates (R2670, R2671, R2672, R2742, R2911) and returns them transiently for the current recall call; it authors no `@ext-candidate`, writes no RC/RF records, and runs no synchronous materialization. Supersedes R3068 (the author-`@ext-candidate` inversion) and R3076 (the sync-materialize round-trip). The calling agent is now the sole producer of durable candidates (R3081).
- **R3080:** `enrichProposedTags` populates each surfaced chunk's `ProposedTags` / `ProposedTagScores` from R3079's transient computed proposals for this call (chunk-EC ↔ tag-similarity descending, as `selectCandidates` already orders them), not from `Store.DerivedProposals`. `Store.DerivedProposals` and the `candidateSourcesByChunk` reverse lookup (R3065, R3067) are retained unchanged as the forge-facing reader wired by #37; only the recall-path read is removed. Supersedes R3067's recall-path read.
- **R3081:** The calling agent (full live-conversation context) is the sole author of durable `@ext-candidate`s, via `ark ext candidate` (R3075); when no agent is connected, the human authors through the Tag Forge (#37). The recall derivation pass and the recall secretary surface proposals into the ephemeral tmp:// doc only — neither writes durable state. Authority must match discernment (companion to the Secretary pattern and Haiku-delegation-bounds).
- **R3082:** (inferred, #36 core) The recall call accepts an optional live-conversation chunk-ID set (the source-seed chunk plus the recent-N context-turn chunks), watcher-populated and empty for the bloodhound seed. These chunks fold into the R3079 compute with A66 self-exclusion bypassed (they are the query, but earn proposals) and marked `tag-only` (R2869), so their computed proposals surface for the calling agent to author while the chunks themselves are never surfaced back to the reader (R2872).


## Feature: Managed PTY Session
**Source:** specs/managed-pty.md

- **R3114:** ark never proactively starts `claude`: a hosted session begins only on an explicit `ark luhmann launch` (or an explicit UI action). There is no autostart on `ark serve`, no remembered "was running" intent that relaunches after a server bounce, and no auto-wake when a paid session is merely needed. The manual launch is the sole spend-consent gate.
- **R3115:** The pty output stream is machine-opaque (Claude Code is a React TUI that addresses the terminal with control characters). ark *writes* to the pty to send input but never reads the hosted session's state by scraping that output stream; ark's reading of what the session did comes from the JSONL chat log (the RecallWatcher tap). "Send" is the pty write side and "receive" is the JSONL read side — two distinct wires.
- **R3116:** `ark serve` holds the pty master and runs `claude` as a child process of the server. The child dies on `ark stop` and is not auto-relaunched (per R3114); a server restart leaves the session down until a human re-launches it.
- **R3117:** The fan-out multiplexer manages attached clients through a transport-agnostic client interface — a client is anything that can receive the output stream, send input, and report a terminal size. A transport is an implementation of that interface: the CLI `attach` (unix socket) now, the browser xterm.js client (websocket) later. Any number of clients may be attached to one hosted session concurrently, in any mix — several CLI `attach`es (additional terminal tabs, an ssh session) as readily as one CLI plus one browser.
- **R3118:** Output broadcast — the child's output is written to every attached client. Each client has bounded output buffering; a client that cannot keep up is dropped rather than allowed to block the fan-out, so a slow or stalled client never stalls the other clients or the session.
- **R3119:** Input merge — input from any attached client is written to the child, serialized so two clients' bytes cannot interleave mid-sequence. Concurrent typing by two clients is permitted (tmux-style), not arbitrated.
- **R3120:** Resize is smallest-wins — the pty size is the minimum of all attached clients' reported sizes, recomputed on every client attach, detach, or resize and pushed to the child via `SIGWINCH`. A disconnect is not special-cased: it re-runs the same minimum, so the pty grows when the smallest client leaves and shrinks when a smaller client joins. With a single attached client this degenerates to that client's size.
- **R3121:** Attach/detach independence — clients attach and detach freely; zero attached clients leaves the session running (detached), and one client's detach does not disturb another.
- **R3122:** `ark luhmann launch [--bootstrap INPUT]` forks the pty with cwd `~/.ark/luhmann`, starts `claude` as the child, and sends the bootstrap string (default `load /luhmann`, overridable with `--bootstrap`) as the session's first input, sequenced per the confirmation protocol R3126; no project `CLAUDE.md` is required. It is the only CLI door that starts a paid session (the consent gate) and errors when a session is already hosted (one hosted session at a time). It returns success only after R3126 confirms the session attached. Server-required.
- **R3123:** `ark luhmann attach` is a raw-mode client over the unix socket: stdin → pty, pty → stdout, with a tmux-style detach escape and `SIGWINCH` (resize) propagation. Detaching leaves the session running; multiple `attach` clients may be connected concurrently (R3117). Server-required.
- **R3124:** `ark luhmann status [--json]` is the single source of truth for whether a session is hosted: it reports pty-alive state and, folding in the supervisor state from luhmann.md, the pool-secretary roster count. `--json` emits a machine object; default output is human text. Server-required.
- **R3125:** `ark luhmann stop` is a graceful teardown of the hosted session, not a bare kill: because stopping the pty takes the session's in-session pool secretaries down with it, `stop` also releases the seat lease and records those secretaries' exits (via the exit-record path), leaving the monitoring log truthful instead of showing ghosts. UI label: "End session". Server-required.
- **R3126:** `launch` (R3122) confirms the session came up using a content-free signal — never by parsing JSON record content. Before forking it clears any stale seat claim (`ForceReleaseSeat`): a prior session that died without releasing the in-memory lease would otherwise block the new claim, so clearing it unconditionally makes the new session's `--first` the one observed (the managed launch is the authoritative start). It then sends the bootstrap to the pty (Claude Code buffers input, so ordering against startup is safe) and waits for the launched session to **claim the Luhmann seat** via `ark luhmann next --first` — the authoritative confirmation that `/luhmann` loaded and attached, and the event that teaches ark the session's id (from the claim's `--session`). A timeout on the seat-claim wait fails the launch with an error. Content-based idle detection (classifying `origin.kind` / `turn_duration` / pending tool calls) is out of scope here — deferred until a consumer needs it.
- **R3127:** The hosted child launches as a fresh, top-level `claude`: the fork strips the Claude Code session-identity markers `CLAUDECODE`, `CLAUDE_CODE_*`, and `AI_AGENT` from the child's environment before starting it, leaving credentials (`ANTHROPIC*`) and other config intact. Without this, a child spawned by an `ark serve` that is itself running inside a Claude Code session inherits those markers, concludes it is a nested sub-session, and never completes an interactive turn — the bootstrap never submits, no JSONL is written, and the launch fails at R3126's second-record gate.
- **R3128:** When `launch` targets a directory Claude Code has not seen before (no `~/.claude/projects/<cwd-encoded>/` exists), PtyHost auto-accepts the "trust this folder" dialog before sending the bootstrap — otherwise the dialog consumes the bootstrap and the launch fails. It watches the child's early output and, on detecting the dialog, selects the "Yes, I trust" option by the option number **read from the escape-stripped output stream** (reading the number rather than assuming a default or a fixed "1", so a reordered menu is still answered correctly), followed by Enter. The accepter is armed only for new-project launches and disarmed once the dialog is handled or a timeout elapses, so it cannot misfire on a running session's output. It relies on Claude Code rendering the option number before the "Yes … trust" label in stream order; a non-match sends no key (fail-safe), so a future backtracking renderer would surface as a visible launch failure, not a mis-answered dialog.
- **R3135:** ark auto-indexes the orchestrator's own session by adding its Claude Code project directory (`~/.claude/projects/<cwd-encoded>` for cwd `~/.ark/luhmann`) as an in-memory `chat-jsonl` source (`EnsureLuhmannSource`), so the session's conversation log is indexed for recall and the watcher tap with no user configuration — the same ark-managed-content principle as `EnsureArkSource` (R2811), since the project directory exists only because ark forked the pty with that cwd. The global `*.jsonl` → `chat-jsonl` strategy classifies it, and the standard `~/.claude/projects/**` search exclusion keeps its chunks out of ordinary search results (present for recall, absent from search noise). The whole project directory is indexed — not one session file — so the orchestrator's memory spans launches. This is distinct from how `ark luhmann send` (R3132) locates the *live* log (a direct filesystem glob, index-independent): indexing lag would race a command enqueued right after launch.
- **R3136:** The pty host exposes a **forced repaint** — it toggles the pty size by one row (shrink, then restore) to raise a real `SIGWINCH`, which the child (an Ink TUI) repaints the whole screen on. The shrink is **held a short beat before the restore**: a synchronous shrink-then-restore coalesces (signals are not queued), so the child's handler runs once, reads the restored — unchanged — size, and skips the redraw; only an *observed* intermediate size triggers a repaint. A same-size resize will not do either: the kernel drops `TIOCSWINSZ` when the winsize is unchanged, so no `SIGWINCH` fires. The repaint is requested over a `repaint` client→host frame — a third frame kind alongside input and resize — on the same channel, so one host mechanism serves every attach transport uniformly. It is necessary because ark holds no virtual screen (R3115): only the child can repaint, so a repaint is always the child redrawing on a forced `SIGWINCH`, never ark replaying buffered output.
- **R3137:** The `attach` client requests a forced repaint (R3136) immediately after connecting, so a newly attached — or re-attached — client sees the current full screen at once rather than only subsequent output. Without it, a reattach shows a blank or stale screen until the child next repaints on its own, because the byte stream carries no backlog of the prior full-screen paint.
- **R3138:** The `attach` client's detach escape gives visible feedback: on `Ctrl-]` it paints a transient one-line prompt (bottom row) acknowledging the escape and naming the keys — otherwise `Ctrl-]` alone is silent, which is what makes detaching feel untrustworthy. `d`/`D` detaches (R3123); any other key cancels — the client **discards it and the prefix** (a mode key, not input for the child) and requests a forced repaint (R3136) to wipe the prompt, since ark cannot redraw the covered cells itself (R3115). This refines R3123's original send-prefix-and-key handling for the cancel case. On an actual detach, the client instead **clears the prompt itself** from the bottom row before restoring the terminal — an instant local erase, since a child repaint would race the imminent disconnect — so the help text does not linger on the abandoned frame.
- **R3139:** `ark luhmann launch` sets `ARK_MANAGED_PTY=1` in the hosted child's environment (alongside the R3127 marker stripping), so the hosted session can tell it was started by the managed launch — as opposed to a bare `/luhmann` in another session — and surface the attach/detach hint. The `/luhmann` skill reads the marker; the greeting itself is a Claude Code asset, not Go code.
- **R3140:** On exit — detach or disconnect — the `attach` client restores the terminal **fully**. `term.Restore` returns the termios line discipline to cooked mode (echo, canonical input, signal keys); the client additionally emits a sanitizing escape sequence to undo the DEC private modes the full-screen child may have set through its *output* but that `term.Restore` does not touch: cursor visibility (Ink hides the cursor during a render, so detaching mid-render otherwise leaves the terminal looking frozen and "raw"), bracketed paste, and mouse reporting, plus a scroll-region and SGR reset. Without it the shell returns in a broken-looking state needing a manual `reset`; the intermittency tracks the child's mode state at the instant of exit.
- **R3141:** `ark serve` exposes the hosted pty to a browser over its **own** websocket at `GET /luhmann/pty`, registered on the UI HTTP server (the same origin and port the Frictionless UI uses) whenever the UI runtime is running. It is ark's own `gorilla/websocket` upgrade, distinct from ui-engine's structured view-diff `WebSocketEndpoint`, so raw terminal bytes never route through the per-session view executor (which would tie terminal latency to view rendering).
- **R3142:** Binary websocket messages carry raw pty bytes in both directions: a browser→host binary message is input, merged onto the child serialized with every other client's input (R3119); a host→browser binary message is a chunk of the child's output stream, subject to the bounded-buffer drop (R3118). The message boundary is the chunk boundary — no additional framing.
- **R3143:** Text websocket messages are JSON control frames: `{"t":"resize","cols":C,"rows":R}` drives the smallest-wins resize (R3120), and `{"t":"repaint"}` requests the forced child repaint (R3136) the same client-requested way the CLI client does (R3137). The first message on a connection must be a resize, seeding the client's size before it attaches; a connection opening with anything else is closed.
- **R3144:** The websocket client is one implementation of the transport-agnostic `PtyClient` interface (R3117): it plugs into the same fan-out with the same bounded-buffer drop — a stalled browser tab is dropped rather than allowed to stall the fan-out (R3118) — and the same survive-a-disconnect semantics (R3121); its disconnect deregisters it and recomputes the smallest-wins minimum (R3120).
- **R3149:** `GET /luhmann/pty-status` is served on the UI HTTP server as well as the unix-socket API mux — the same handler mirrored on both, as the curation endpoints are for the content view — so a browser client can ask why a `/luhmann/pty` handshake failed. The WebSocket API surfaces a rejected upgrade as an unexplained 1006 close, so without the mirror a browser cannot distinguish R3141's "no session hosted" 409 from a bouncing or absent server.

## Feature: Luhmann Terminal Element
**Source:** specs/luhmann-terminal-element.md

- **R3150:** `<luhmann-terminal>` is a custom element that renders a live terminal on the ark-hosted Luhmann session — the browser's `ark luhmann attach`. It is a composable primitive carrying no chrome: no launch button, no awake/asleep lamp, no panel frame, no toolbar, and no status text of its own. Its placement inside the Frictionless app is a separate UI slice.
- **R3151:** The element takes no attributes. It derives its endpoint as `/luhmann/pty` on the page's own origin, choosing `wss:` when the page is `https:` and `ws:` otherwise. The endpoint is deliberately not configurable: R3141's upgrader enforces same-origin, so any other endpoint would be refused at the handshake. Size and theme come from CSS.
- **R3152:** On connect the element performs the handshake in a fixed order: fit the terminal to its box and send `{"t":"resize","cols":C,"rows":R}` as the **first** message (R3143 closes a connection that opens with anything else), then send `{"t":"repaint"}`. The repaint is the same on-attach request the CLI client makes (R3137) and for the same reason — ark holds no virtual screen (R3115), so a client attaching mid-session sees only subsequent output and would otherwise sit blank until the child next paints.
- **R3153:** Host→element binary messages (R3142) are written directly to xterm, which owns escape-sequence parsing and the screen. Element→host input is sent as binary messages: xterm's `onData` string is UTF-8 encoded, and its `onBinary` byte-per-character string is sent with each character masked to its low byte. The host sends no text messages; the element ignores any it receives rather than guessing at intent.
- **R3154:** The element is sized by CSS. xterm's `FitAddon` converts its box into a (cols, rows) pair and a `ResizeObserver` re-fits and sends a resize control frame on every box change, **debounced** — an undebounced drag turns every intermediate box into a `TIOCSWINSZ` plus a real `SIGWINCH`, making the child re-render the whole screen for sizes the user is passing through rather than choosing. The element reports only its own size; the minimum across clients is the host's (R3120). The element declares its own `display: block` in the injected stylesheet, because a custom element defaults to `display: inline` and width/height do not apply to an inline box — a host's sizing CSS would otherwise be silently ignored and the terminal would render a few cells wide. It declares no size: a terminal has no intrinsic one, and a `height: auto` block would take its height from xterm's content while `FitAddon` sizes that content from the element, a self-referential loop. The host must supply a real height.
- **R3155:** At connect the element reads ark's theme variables off the document and maps them onto xterm's options: `--term-bg`→`theme.background`, `--term-text`→`theme.foreground`, `--term-accent`→`theme.cursor`, `--term-mono`→`fontFamily`, each with a fallback. The 16 ANSI colors stay at xterm's defaults — they are the child's palette, not the app's chrome, so Luhmann's own colored output is not re-hued by the active ark theme. Variables are read once per connect; live theme-switch reflow is out of scope.
- **R3156:** The element intercepts no keys. `Ctrl-]` is ordinary input forwarded to the child, and there is no detach prompt, escape state machine, or cancel-discards-key rule (R3123, R3138) — those exist because a shell terminal hands the child every keystroke, a constraint the browser does not have. Detaching is `disconnectedCallback`: close the socket and dispose the terminal; the session survives (R3121). The element does not emit R3140's DEC-mode sanitize either: that restores the user's *real* terminal, whereas here the terminal is the disposed xterm instance and nothing outlives it.
- **R3157:** The element probes `GET /luhmann/pty-status` (R3149) before every socket open and again on every close, and classifies the outcome: `{"hosted":false}` means the session ended, so it stops and reports `asleep`; `{"hosted":true}` means the session is available, so it connects (or, after a failure, retries); a failed fetch means ark is down or bouncing, so it retries. Probing gates the open rather than explaining a failure after the fact, which matters because `asleep` is the *common* case — Luhmann is usually not running — and socket-first would answer it with a doomed connection and a 409 before reaching the same conclusion. The probe always runs immediately on a close, ahead of any backoff, so `asleep` is reported at once rather than one backoff late; only the reconnect waits. Retrying uses jittered backoff, re-probes rather than blindly reopening (the session may end during the wait), and re-runs the whole handshake (R3152) on every reconnect, which restores exactly the state a bounce erased.
- **R3158:** The element never starts a session, upholding R3114. Connecting cannot start one — the endpoint 409s when nothing is hosted (R3141) — the element never calls `launch`, and R3157's stop-at-`hosted:false` means it does not even wait on a session that has ended. It attaches to what exists or reports that nothing does.
- **R3159:** State changes are announced as a bubbling, composed `luhmann-terminal-status` CustomEvent carrying `{state, session, attempt}`. That event is the element's entire host interface: a host renders a lamp from it and needs to know nothing else.
- **R3160:** The element ships as its own esbuild bundle, `dist/luhmann-terminal-element.js`, layered into `cache/html/` by the root Makefile and installed to `~/.ark/html/` — the same path `pdf-chunk-element.js` takes. Per ark's offline-first stance xterm.js is bundled locally, never loaded from a CDN, and xterm's stylesheet is inlined into the bundle and injected once on load, so a page needs one `<script>` and no stylesheet link.
- **R3161:** `install/html/luhmann-terminal.html` ships a full-viewport terminal on the hosted session with a one-line status strip driven by R3159's events — the browser counterpart of `ark luhmann attach`, and the worked example of the host interface that later UI work follows.
- **R3162:** (inferred) The element's decision logic is factored into a pure module that imports neither DOM nor WebSocket — endpoint derivation, control-frame encoding, input encoding, the R3157 probe→action classification, and the backoff schedule — so it is unit-testable without a browser. The element is the thin shell wiring that core to xterm and a socket.

## Feature: HTTP Operations
**Source:** specs/http-operations.md

- **R3166:** A request-shaped unit of server work is expressed as an **operation object**: a struct with an `init(srv, r)` method that takes what the request carries and binds the DB access the operation is permitted to use, and a `run() (any, error)` method that performs the work and returns a value to serialize. `run` is **HTTP-agnostic** — it never touches the `http.ResponseWriter`, never selects a status code, and never writes bytes. That separation is what lets one operation back three front doors (HTTP, CLI, in-process) instead of one, and it is the contract that distinguishes an operation from an ordinary handler.
- **R3167:** Operations are registered by prototype: `handle(srv, someOp{})` returns an `http.HandlerFunc` that, per request, **copies the zero-value prototype**, calls `init` on the copy, then `run`. The copy is what makes a registration-time value safe to share — no request can observe or mutate another's state. `handle` is a free function taking the server as its first argument rather than a `srv.handle(…)` method, because Go does not permit type parameters on methods and the wrapper needs one to name the operation type; the generic constraint is on `*T` so `init`/`run` can be pointer methods on the copied value.
- **R3168:** `run` classifies failures **semantically** — bad input, not found, unavailable, or (by default) internal — rather than by HTTP status, so a non-HTTP front door can map them its own way. The HTTP wrapper performs the mapping: bad input → 400, not found → 404, unavailable → 503, internal → 500. An operation returning a bare, unclassified error gets the internal treatment, so the safe default costs no annotation.
- **R3169:** A `nil` result from `run` writes no response body (the operation succeeded and has nothing to report); any other result is JSON-encoded. Handlers that must emit a non-JSON body — HTML, byte streams, server-sent events — are **not** operations and remain ordinary `http.HandlerFunc`s, because the HTTP-agnostic `run` contract is precisely what they cannot satisfy. This bounds the pattern rather than forcing every handler through it.
- **R3170:** Adoption of the operation wrapper is **incremental and opportunistic**: the machinery plus a few representative operations establish the shape, and remaining handlers convert as they are touched for other reasons. The one-grep audit property — every `srv.db` use is an operation's binding or a bug — arrives only at complete adoption and does not by itself justify a mechanical sweep; the interim value is that a new handler has a correct pattern to copy rather than a discipline to re-derive, which is how the R3165 races came to be written.

## Feature: Bible Chunker
**Source:** specs/bible-chunker.md

- **R3172:** A `bible` chunking strategy is registered under that name and is selectable wherever any strategy is — globally or per-source (e.g. `strategies = { "books/*" = "bible" }`) — so a scripture checkout opts in by config with no other change.
- **R3173:** The bible chunker emits **one chunk per paragraph**: blank-line-separated blocks of the file, in order, exactly as a reader sees them. A chapter heading is not separated from the block it introduces; a heading followed by a blank line is its own block, matching ordinary markdown structure. Paragraph granularity is the deliberate middle between a chapter (too coarse to be a useful search result or annotation target) and a verse (too fine — an order of magnitude more chunks to serve an addressing need that display-time refinement satisfies for free).
- **R3174:** A verse mark is a **backtick-wrapped integer** (`` `12` ``) appearing inline in the prose. Backticks are what make the mark unambiguous — nothing else in the text can be mistaken for one, and ordinary markdown viewers render it as a small code span. A verse begins at its mark and runs to the next; verses are prose-sized, so several share a paragraph and one may continue past a paragraph break.
- **R3175:** Each chunk carries a `chapter` attribute holding the number from the most recent chapter heading — an ATX heading of any level whose text ends `Chapter N`. The value is carried forward across every paragraph of that chapter until the next chapter heading. It is absent on chunks preceding the file's first chapter heading (a title or preamble), which therefore behave as ordinary markdown.
- **R3176:** Each chunk carries a `verses` attribute spanning the verse marks it contains — `first-last` (`"1-2"`), or the bare number when the chunk holds exactly one mark. It is absent when the chunk contains no marks. Together with `chapter` this is all a verse reference needs to find its paragraph, so no verse dimension enters storage.
- **R3177:** The book name is deliberately **not** recorded as an attribute. The file already names it (`books/mark`) and a reference addresses a file, so storing it would duplicate an identity that cannot disagree with itself — an ext routing names the file and the verse, never a book that could contradict the path.
- **R3178:** The bible strategy is **not writable**. Its chunker reports non-writability the way the PDF chunker does, which by existing behavior means ark never inserts an inline `@tag:` into a bible file's body and annotation degrades to the *external* disposition (a mirror file, placed inside the source when the source sets `ext_mirror`), and the content view presents the text with no edit affordance. A reference corpus's text is fixed, and its verse numbering is what every annotation depends on.
- **R3179:** An `@ext` TARGET may address a bible file by **chapter and verse** in the anchor slot where a line range would otherwise sit: `~/work/KJV/books/mark:12.1` means chapter 12, verse 1. It resolves to the single paragraph chunk whose `chapter` matches and whose `verses` span contains that verse.
- **R3180:** A CHAPTER.VERSE reference naming a chapter or verse that does not exist in the target file resolves to **nothing** — no chunks. It is not an error, and it does not fall back to the file's first chunk the way a bare path target does, because a reference that named a specific verse and silently annotated an unrelated paragraph would be worse than one that annotated nothing.
- **R3181:** A bible file's content view renders through a **bible-specific markdown renderer** rather than the shared one. Each verse mark becomes `<ark-verse n="N"><code>N</code>…</ark-verse>` — the `<code>` preserved so the page still reads as the markdown it is, the wrapper giving the verse an identity in the page. **Every** verse mark is wrapped, not only annotated ones: a verse is the unit a reader refers to, so scroll-to-verse, highlighting, and annotate-this-verse affordances need a target before anything is attached.
- **R3182:** An `@ext` routing whose target names a verse (`…/mark:12.1`) is rendered **inside that verse's `<ark-verse>` element**; routings targeting the same chunk that name no verse — a bare path, a quoted-text or regex anchor — remain in the chunk-level `<ark-ext-tags>` block where every other content kind shows them. The partition is by whether the routing's recorded target anchor parses as CHAPTER.VERSE (R2073 supplies the anchor). Nothing is dropped for lacking a verse and nothing is invented for having one.
- **R3183:** The verse pass is implemented as a second markdown renderer configured for bible files — an AST-level recognition of verse marks emitting a first-class element — **not** by enabling raw HTML in the shared renderer, and **not** by rewriting rendered HTML. Enabling raw HTML would let any indexed markdown file inject markup into a page ark serves, a protection not worth trading for a verse indicator; rewriting output cannot distinguish a numeric span in prose from one inside a fenced code block, whereas the parsed document can. Only numeric marks are recognized, leaving ordinary inline code untouched.
