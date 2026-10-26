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
- Index storage via microfts2 (shared)
- Unix domain socket for server communication

## Shared Index

Ark opens microfts2 first (which creates the index), then
opens its own named bucket for metadata (missing files list,
etc.), tags, and chunk embeddings (EC records). The Store and the
Librarian share that index. The ark bucket holds its records
alongside microfts2's — no separate vector bucket (R1911).

## Glob Patterns

Ark matches paths against globs in three places: CLI flags, `ark.toml`
keys, and the source-directory expansion. **One matcher serves all of
them** (`ark.Matcher`, doublestar syntax). They differ only in what an
unanchored pattern is measured against.

### Pattern language

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
A literal wildcard character in a filename is written in bracket form
(`[*]`, `[?]`, `[[]`), which works on every platform. Off Windows a
backslash also escapes one (`\*`, `\?`, `\[`).

**On Windows a backslash is a separator**, because that is what someone
writing a Windows path means by it: `docs\*.md` and `docs/*.md` are the
same pattern, and a path is matched the same way whichever separator it
was written with. Matching runs on the slash form of both pattern and
path, so every rule above, `**` and the trailing `/` among them, means
the same thing on Windows as elsewhere. Measured 2026-10-26: before this,
every pattern holding a separator failed on Windows, so a nested
`node_modules/` or `.git/` from the default excludes was indexed.
`ark init` ships with default excludes for `.git/`, `.env`, etc.

### Three anchoring forms

| Form   | Meaning                                                                                                                                              |
|--------|------------------------------------------------------------------------------------------------------------------------------------------------------|
| `/X`   | **Filesystem-absolute.** Matched against the file's absolute path on disk, whichever source it belongs to. Enables blanket rules such as `/tmp/**`. On Windows a pattern that starts with a drive (`C:/X`, `C:\X`) is this form too. |
| `./X`  | **Anchored to the contextual root.** After stripping `./`, the remainder matches against the path relative to that root.                              |
| `X`    | **Relative to where you are standing.** What that means depends on the context below.                                                                 |

### Three contexts

The contextual root differs by where the glob is written:

| Context                   | Root                  | Bare `X` means                    | Applies to                                                                                                                       |
|---------------------------|-----------------------|-----------------------------------|-----------------------------------------------------------------------------------------------------------------------------------|
| **CLI flags**             | the current directory | `$PWD/X`                          | `-files` and the rest of the filter stack                                                                                        |
| **Source-scoped config**  | the source's dir      | `SOURCE/**/X` (any depth in it)   | `default_include`, `default_exclude`, `[[source]].include`, `[[source]].exclude`, `strategies`, `[[source]].strategies`          |
| **Rootless**              | none                  | `**/X` (any depth, any source)    | `system_exclude`, `search_exclude`, `[schedule] filter_files`, `[schedule] exclude_files`, `[schedule.tag.<NAME>]` overrides, and the Lua/MCP `filter_files` / `exclude_files` opts |

The rule in one sentence: **a bare glob is relative to where you are
standing.** At the CLI that is the current directory, so `*.md` means what it
means in a shell. In `ark.toml` there is nowhere to stand, so it means any
depth.

Only the bare form varies. `/X` and `./X` mean the same thing wherever they
are written, which makes them the portable forms. Prefer them when copying a
pattern between a command line and a config file, because a bare glob moved
from `ark.toml` to the CLI silently *narrows* (from any-depth to top-level)
rather than failing.

The Lua/MCP surface is rootless for the same reason `ark.toml` is: a call
arriving at the server carries no current directory to anchor against.

Rootless keys have no root for `./X` to anchor to, so there it falls
back to matching the absolute path. Naming "the `specs` directory at the root
of source X" from `[schedule]` requires writing that absolute path.

Examples (source-scoped context):
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

### `[[source]].dir` is an expansion, not a filter

A source's `dir` may itself be a glob (`~/work/*`), expanded against the
filesystem to produce one source per matching directory. This is shell-style
path expansion rather than path filtering, so the anchoring rules above do not
apply. One rewriting is done: `~` expansion.

**A `dir` that is itself a symlink is rejected**, both as written and when a
glob expands onto one ([symlinks.md](symlinks.md)). A source root that is a
link asks ark to follow one, which it does not do; a real directory containing
links achieves the same thing through machinery that already exists. Unchecked,
the failure is a convincing success rather than an error — the walk lstats its
root, so it finds the link node as a single file entry and none of the
contents.

**`**` is rejected in `[[source]].dir`.** A recursive source glob makes every
subdirectory its own source root, so a single file change fires one watcher
event per ancestor level. The breakage is *latent*: `~/work/**` is harmless
while `~/work` has no nested directories, and silently begins multiplying
events the day someone creates one. Validating the expansion at load time
cannot catch that, so the pattern itself is refused with an error naming `*`
as the single-level alternative.

Multiple non-nested directories remain fine. `~/work/*` and the `from_glob`
reconciliation it drives are unaffected.

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

Source `include` / `exclude` / `strategies` patterns are the
**source-scoped context** described under [Glob Patterns](#glob-patterns)
above: the source's own directory is the root, so a bare pattern matches at
any depth within that source. `[[source]].dir` itself is an expansion rather
than a filter, and rejects `**` — see the same section.

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
- Index store (index.db)
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
  `[embedding] model` is set) — no separate vector-store init step
- Create ark's own bucket
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
commands (persisted in index settings).

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
- every record type other than user and assistant turns (indexing.md)
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
- `role` — `assistant` for an assistant turn; `skill` for a skill
  injection or an agent report; `human` for any other user turn
  (indexing.md). Used by the content view to
  render conversation structure with role indicators.
- `skill` — for skill chunks only, the last path component from the
  `Base directory for this skill: PATH` line (e.g. `ark`, `mini-spec`).
- `timestamp` — the record's `timestamp` field, if present.

### Skipped record types

Every record type other than user and assistant turns is skipped. The canonical
rules are in [indexing.md](indexing.md), "chat-jsonl chunker".

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
- Writes a PID file `ark.pid` inside the ark home directory, beside `ark.sock` (for emergency kill only)
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
correctness mechanism. It lives at `<ark-home>/ark.pid` (e.g.
`~/.ark/ark.pid`), beside `ark.sock` and `index.db` — ark
infrastructure, not corpus content. It is deliberately **not** in
`os.TempDir()`: that path honors `TMPDIR`, so a server started from a
plain shell and an `ark stop` run from a shell with a different
`TMPDIR` (e.g. a nix-shell) disagreed on the location, and `stop`
reported "no PID file found" against a running server.

### Server Lifecycle

`ark serve` exits 0 if a server is already running (intent: "make
sure it's up" — already up means success). Message on stderr for
humans.

`ark stop` requests a graceful shutdown over the unix socket: it POSTs
`/shutdown` and treats a 200 response or the connection dropping
mid-request as "teardown initiated". It then polls for release by
dialing the socket — a successful dial means the server is still up, a
refused dial means it is down and has released. It never uses `ark
status` as a liveness check (status succeeds even with no server). The
release wait is bounded by a timeout.

On timeout the fallback is platform-split. On Linux, `stop` force-kills
the PID (read from the ark-home PID file, falling back once to the
legacy temp-dir path, logging that it did) and re-polls, erroring if
the kill fails or the process still will not release; the OS frees the
socket and DB lock on death, so a kill is a safe last resort. On
Windows, `stop` errors without killing — a failed graceful stop is
surfaced for the user to resolve rather than risking a
`TerminateProcess` that can leave the socket or DB lock held.

`ark stop -f` force-kills the server on every platform (the one
sanctioned `TerminateProcess` on Windows), the explicit escape hatch
the user owns.

Server shutdown: the graceful teardown (stop scheduler, close watcher,
shut the UI runtime, close socket, close DB, exit 0) is a single
reusable path invoked both by the Unix SIGTERM/SIGINT (Ctrl+C) handler
and by the `POST /shutdown` endpoint, so `ark stop` shuts the server
down the same way on every platform — nothing external need deliver a
signal, which Windows does not do for SIGTERM. The endpoint responds
and flushes before running the teardown so the client receives its
reply before the process exits. The server process never removes the
PID file — stale PID files are safe because `ark stop` verifies before
killing.

A requested shutdown exits 0 deterministically. The teardown sets a
`shuttingDown` latch before it closes the listener, so the main serve
loop reads the resulting closed-listener `Accept` error as an expected
shutdown and returns cleanly instead of propagating it as a fatal
error. Without the latch that error would reach `ark serve`'s
top-level `fatal` and exit 1, racing the teardown's own `os.Exit(0)` —
a supervisor would read a normal stop as a crash, and `db.Close()`
might not run. With it, the teardown's exit is the sole, deterministic
one and the database always closes cleanly.

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
  are preserved — only the index is rebuilt. During the re-scan a
  read-only server stays up on the socket, so `ark status` / `ark
  search` from another terminal return live progress instead of
  blocking on bbolt's single-process file lock (rebuild-read-serve.md).
- `ark setup [--local]`
  The install path. Global (no flag): create `~/.ark/`, self-copy the
  binary, extract the bundle, create the database, link every bundled
  skill and the agents directory into `~/.claude/`, provision llama libs,
  and download the configured model if missing. Idempotent on the marker
  (the database file). `--local` connects the current directory to an
  existing install and prints the CLAUDE.md hint. See Setup and Install.
- `ark update [--no-exe]`
  Refresh an install to the running project's latest GitHub release.
  Version-checked (no-op when current, never downgrades), two-phase,
  manifest-aware, downloads the model if missing, and leaves everything
  intact on failure. Gripes on a symlinked `~/.ark/ark`. `--no-exe`
  refreshes assets from the running binary's own bundle without touching
  the exe. See Setup and Install.
- `ark _release-asset [--terse] OS ARCH`
  Internal, for the Makefile: report the release asset name `ark update`
  downloads for that platform at this binary's version, in baby food by
  default; `--terse` prints the bare name. See Release assets.
- `ark add [--dir <path>] [-strategy <name>] <file-or-dir>...`
  Add files. If directory, walk per config. If file, add directly.
- `ark remove [--dir <path>] <file-or-pattern>...`
  Remove files from both engines. Accepts paths or glob patterns.
- `ark scan [--dir <path>]`
  Walk configured directories. Index new files matching include
  rules, flag new unresolved files. Does not re-check existing files.
- `ark refresh [--dir <path>] [--force] [<pattern>...]`
  Re-index stale files. If patterns given, only refresh matching
  files. No patterns = all stale files. Report missing files.
  `--force` re-chunks every file the patterns match, stale or not, through
  the same in-place reindex, for when the chunker's rules changed but the
  files did not. Unchanged chunk text keeps its chunk id, and so its
  embedding and everything that points at it; only chunks the current rules
  no longer produce are dropped, and new ones are embedded. `--force`
  requires a pattern: forcing the whole index is rarely meant, and a
  pattern of `/**` asks for it on purpose. The re-chunking runs in the
  background, one file at a time off the DB actor; the command answers at
  once with how many files it queued.
- `ark search [--dir <path>] [-k <num>] [--scores] [--after <date>] [--chunks] [--file-content] [filter-stack] <query>...`
  Combined search. `--chunks` emits chunk text as JSONL.
  `--file-content` emits full file content as JSONL. The filter stack
  (`-with` / `-without` with `-contains`, `-tag`, `-files`, `-about`, …)
  scopes the search (see specs/search-cli-filters.md).
- `ark search [--dir <path>] -about <text> -contains <text> [-regex R] [-k <num>] [--scores] [--chunks] [--file-content] [filter-stack]`
  Split search.
- `ark serve [--dir <path>]`
  Start the server.
- `ark status [--dir <path>]`
  Show counts: files, stale, missing, unresolved. Index built.
  Server running or not. The stale/missing/unresolved counts are as-of
  the last reconcile (status never re-walks the corpus) — see
  specs/status-enhanced.md.
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
  Works whether or not a server is running: when a server is up it
  holds the index open, so fetch reads through it (`POST /fetch`);
  with no server, fetch opens the index directly. The index is
  single-process, so a direct open while the server holds it would
  block — routing through the server avoids that.
- `ark unresolved [--dir <path>]`
  List files that don't match any include or exclude pattern.
- `ark resolve [--dir <path>] <pattern>...`
  Dismiss unresolved files matching the given patterns (remove from
  unresolved list without adding a permanent rule). Patterns use the
  same glob syntax as include/exclude. Write a literal wildcard
  character in bracket form (`file[*]name`); off Windows a backslash
  escape (`file\*name`) works too.

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
- `POST /chunks` — return chunk content by chunkID, `path:range`, or chat sub-chunk anchor
- `POST /grams` — return decoded trigram document-frequency counts for a query
- `POST /schedule/tags` — return the `schedule tags` summary lines (`values` includes per-event detail)

## Ark Bucket

Index bucket `ark` stores tag records (T/F/V/D), embedding records
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

Ark embeds Frictionless, so there is no separate UI install. One binary
carries the full stack: search engine, HTTP server, and UI assets. `ark
setup` is the install path from binary to working system, and `ark
update` keeps an install current. Nothing is copied where a directory
link will do, which is what makes install work without privilege on
Windows.

### The portable directory-link helper

Skills, agents, and Frictionless apps are wired into the places the
runtime and Claude Code look by a **directory link**. One helper makes
that link portably: a symbolic link on Unix and a directory junction on
Windows (R3453). A junction needs no privilege and no Developer Mode, so
the shipped Windows binary can install without elevation; it is created
through `golang.org/x/sys/windows` reparse points, not by shelling out to
`mklink`.

Two properties keep the helper from destroying data. Removing a link uses
`os.Remove` on the link itself and refuses to delete a real (non-link)
directory at the destination, since a non-link directory there is
someone's data (R3454). Re-linking is idempotent: `os.Readlink` reports a
link's target, so the helper recognizes a link it already owns and leaves
it in place (R3455).

### `ark setup` — the install path

`ark setup` takes a machine from a downloaded binary to a working ark. It
keys idempotency on a **marker** — the database file — rather than on
`~/.ark/` existing, because the README install places the binary at
`~/.ark/ark` before running it, so `~/.ark/` is present on every fresh
install and cannot be the test. With the marker present, the global part
of setup does nothing (R3456). "Installed" means the index exists, which
is exactly what the marker records.

Neither setup nor update starts a server, since installing and running are
separate acts. Both **stop a running ark server first**, through the same
graceful path `ark stop` uses, before they change anything (R3481). On Windows
this is required rather than tidy: a running server holds the llama.cpp
libraries under `~/.ark/lib` and the apps it loaded, and Windows refuses to
overwrite a file a live process has loaded. If the graceful stop does not
complete, setup and update **abort** and tell the user to run `ark stop -f` and
retry, rather than overwriting files a live server still holds. The exceptions
are `update --check` and `update --stage`, which change nothing a server holds
and so never stop one (R3529; see "A requested update" below).

When the marker is absent, setup runs the global install in order:

1. **Create `~/.ark/`** if it does not exist (R3457).

2. **Copy the running executable into `~/.ark/`** (`ark.exe` on Windows),
   so setup can run from wherever the user downloaded it. When setup is
   already running from `~/.ark/`, the copy is skipped (R3458). On Windows
   a running exe can copy itself elsewhere even though it cannot overwrite
   itself in place.

3. **Extract the bundled assets** (html/, lua/, viewdefs/, apps/, skills/,
   agents/) from the binary's zip appendix into `~/.ark/` (R3459). The
   bundle holds no symlinks, so extraction needs no link privilege on
   Windows.

   Then **activate every bundled app**: one directory link
   `~/.ark/lua/<app>` → `~/.ark/apps/<app>` for each top-level directory
   under the bundle's `apps/`, made with the directory-link helper (a
   symlink on Unix, a junction on Windows). Activation is that one link;
   the runtime reads the app's entry file, modules, viewdefs and static
   files through it (see ui-engine's app-discovery spec). Library apps
   with no `app.lua`, like `viewlist`, are activated the same way.

4. **Create the database.** This is the marker. Setup and `ark rebuild`
   both create the database through one shared function, so setup no
   longer runs `ark init` and the old init→setup recursion cannot return
   (R3460).

5. **Link skills and agents into `~/.claude/`.** One directory link per
   bundled skill directory, globbing `~/.ark/skills/*` with no hardcoded
   list, so every bundled skill (the ui-* skills included) reaches
   `~/.claude/skills/` and a newly bundled skill appears without a code
   change (R3461). One directory link `~/.claude/agents/ark` →
   `~/.ark/agents` carries the whole bundled agent set, because Claude
   Code scans the agents tree recursively and takes each agent's identity
   from its `name:` frontmatter, not its path (R3462). Both use the
   directory-link helper. `~/.ark/` is already one install per user, so
   user-scope links serve every project, and a project definition still
   overrides a user one when someone needs that.

6. **Provision the llama.cpp libraries** when a model is configured and
   they are absent (R2969).

7. **Download the configured embedding model** when the model file is
   absent, from the configured per-model URL, verifying the download
   against the configured pinned SHA-256 (R3463). A present file or a link
   (the dev box links it from a sibling checkout) is left alone. Without
   this a default install points at a model it never fetched and search
   silently falls back to trigram-only (R1276).

8. **Report, and note the next session.** Setup prints that newly
   installed agents appear in the *next* Claude session, since agent
   definitions are read at session start (R3464).

Setup copies no skills or agents anywhere; the directory links are the
whole mechanism (R3465).

### `ark setup --local` — connect a project

`ark setup --local` connects the current directory to an existing
install. It requires the marker and refuses to run without it, because it
connects a project to an install rather than creating one (R3466). It
makes the same directory links — one per bundled skill plus
`agents/ark` — into the current directory's `.claude/` (R3467), and it
prints the crank-handle hint asking Claude to add `load /ark first` to the
project's CLAUDE.md. The binary never edits CLAUDE.md; the hint is written
so any model tier, Haiku included, can follow it (R3468). This is the
first setup path that reads the working directory, so its help says so.

### Retiring old copies, and duplicate names

Earlier installs left flat `~/.claude/agents/<name>.md` copies where the
`ark/` directory link now goes. Two files in one agents tree that declare
the same `name:` make Claude Code load only one, chosen by filesystem read
order, so a leftover copy can silently shadow the linked set. Setup
removes a flat `~/.claude/agents/<name>.md` only when it is a regular file
byte-identical to the bundled agent of that name (R3469). A file that
differs from the bundle, or a hand-made link, is reported and left in
place, since either may be the user's own (R3470).

### `ark update` — refresh an install

`ark update` brings an install up to the running project's latest release.
It checks the version first: it compares the build-time `ark.Version`
against the latest GitHub release tag, does nothing when already current,
and never downgrades (R3471).

An update is **two-phase**, because the running binary carries the *old*
bundle. The running binary downloads the new release binary for the
current platform, verifies it against the release's published checksum,
and swaps it into `~/.ark/`; the new binary then refreshes the rest of the
assets from its own bundle (R3472). On Windows the swap renames the
running `~/.ark/ark.exe` aside, writes the new binary at the original
name, and deletes the aside copy on a later run, since a running exe
cannot delete its own aside copy while alive (R3473).

The asset refresh is **manifest-aware**. Installed-file hashes and a
per-file collision policy live in `~/.ark/install-manifest.json`, reusing
the `{hash, onCollision, group}` entry shape rather than living in
`ark.toml`, whose hand-edited config would otherwise churn on every update
(R3479). Update reads the manifest so it never clobbers a user-edited file
and never replaces a link with a copy (R3474). Built-in apps are extracted
into `~/.ark/apps` precisely so a user can edit them in place, and those
edits survive an update. An app that this update extracts for the first
time (no `~/.ark/apps/<app>` before the refresh) is activated as setup
activates it; an app already present keeps whatever activation the user
left it with, so update never re-activates an app the user deactivated. Update also downloads the configured embedding
model when it is missing, the same way setup does (R3475). Any failure — a
failed download, a bad checksum, a failed swap — leaves the old binary and
`~/.ark/` intact (R3476).

The download URL and pinned SHA-256 for the model come from `[embedding]`
config keys (`model_url` and `model_sha256`), so update stays
model-neutral and fetches whatever model the config names (R3480). The
bundled reference `install/ark.toml` carries both keys for its default model,
nomic-embed-text-v1.5 Q8_0, so every fresh install knows where its model comes
from (R3482). An older `ark.toml` without them gets a message saying the model
is missing and how to add the keys, not a download (R3483). A new
ark release must carry one binary asset per platform under a predictable
name plus a published checksum for update to find and verify; the release
process owns producing those, and update only consumes them.

**Release assets.** `make release` produces exactly what update consumes, in a
directory of its own, `release/<version>/`, so a release is published by
uploading that directory as it stands:

- one binary per supported platform, named `ark-<version>-<os>-<arch>` with
  `.exe` on Windows, `<version>` carrying no leading `v`;
- `SHA256SUMS`, in `sha256sum` format, listing every file in the directory;
- nothing else: that directory is emptied first, so a release never mixes
  versions. Anything else under `release/` (an older version's directory, or an
  asset of a separate release kept there by hand) is left alone.

The names come from the binary, not from the Makefile. For each platform the
Makefile asks `ark _release-asset --terse OS ARCH`, which answers with the same naming
function update uses to choose its download, at its own build-time version. The
release and the updater therefore cannot disagree about a name. `make
release-archives` adds a tar.gz (Unix) or zip (Windows) beside each binary for
manual download and rewrites `SHA256SUMS` to cover them too.

**Trying a release before it is published.** When `ARK_RELEASE_URL` is set,
update reads its release document from that URL instead of GitHub's
latest-release API. The document has the API's shape: `tag_name`, and an
`assets` list of `name` and `browser_download_url` pairs. Each asset URL is
resolved against the document's own URL, so a document served beside a
`release/<version>/` directory can name its assets by file name alone, while
GitHub's absolute URLs pass through unchanged. Every path that asks for a release
honours the variable: plain `update`, `--check`, `--stage`, and the UI's update
bridge. Nothing in ark writes such a document. A test that serves a release
directory writes its own, so `release/<version>/` stays exactly what gets
published. A document that cannot be fetched or parsed fails update the way an
unreachable GitHub does, leaving everything as it was.

**A symlinked `~/.ark/ark` is a developer install.** Where `~/.ark/ark`
links into a source tree, swapping it would replace the link with a
downloaded file and break the build-and-run loop. On such a binary, plain
`ark update` gripes — telling the user to use `--no-exe` — and refreshes
nothing (R3477).

`ark update --no-exe` refreshes everything except the executable. It
assumes the running exe is already the new one, the developer case where
`~/.ark/ark` links to a freshly built binary, so every asset (skills,
agents, apps, and the model if missing) comes from the *running* binary's
own bundle and nothing is downloaded for the exe. The refresh stays
manifest-aware and never replaces a link with a copy, and on a symlinked
`~/.ark/ark` it proceeds normally rather than griping (R3478). Pairing a
new release's assets with the old exe is the mismatch the plain-update
gripe exists to avoid.

### A requested update, staged and taken at the next serve

`ark update` has to stop a running server before it touches anything, so a
user in the UI cannot complete an update from inside the server they are
using. Instead the UI **stages** one: it downloads the new binary beside the
installed one, and the next `ark serve` takes it. The server never restarts
itself to do this; pending requests, a running Luhmann session and connected
UIs make a self-restart too involved, and taking the update at the next
start costs little.

**Nothing happens unless the user asks.** The UI may *check* whether a newer
release exists and say so, but it downloads only when the user requests the
update. Everything below coordinates a requested update and nothing else: a
`~/.ark/ark.new` with no request behind it is ignored.

**Staging.** `ark update --stage` checks the latest release (doing nothing
when already current, never downgrading), downloads its binary for this
platform, verifies it against the release checksum exactly as plain update
does, and writes it to `~/.ark/ark.new` (`ark.new.exe` on Windows),
completing the file before it takes that name so a half-written download is
never mistaken for a staged one. It records the request in
`~/.ark/update.json`: the version staged and a `downloadAttempts` count,
starting at 0. It touches nothing else and does not stop a server.
`ark update --check` only reports the latest release version and whether it
is newer than the running one. Both refuse on a developer install (a
symlinked `~/.ark/ark`) the way plain update does.

**At `ark serve` startup**, before anything else, when `update.json` records
a request and no ark server is already running (a second `ark serve` against a
live server must not swap the binary under it):

1. **Validate.** Run `ark.new version`. It must succeed *and* report the
   version `update.json` recorded.
2. **On failure**, delete `ark.new`, increment `downloadAttempts`, and, below
   three attempts, stage it again in the background while the server carries
   on starting normally; the swap waits for the next start. At three failed
   attempts the request is dropped (`update.json` removed) with a log line
   saying so, rather than downloading on every start forever.
3. **On success**, rename `ark` to `ark.old` and `ark.new` to `ark`, then
   **chain** to the new binary with the same command line: a true `exec` on
   Unix; on Windows, which has no `exec`, start the new binary with the same
   arguments and standard streams, wait for it, and exit with its exit code.
4. **The new binary finishes the update.** It finds a request whose version
   is its own, so instead of swapping it refreshes the assets from its own
   bundle (the same manifest-aware refresh as `update --no-exe`, model
   included), removes `update.json`, and starts serving. No server is
   running yet at this point, which is why the refresh can happen here and
   not inside a live server.

`ark.old` (`ark.exe.old` on Windows) is deleted by the next `ark serve` start, whether or not another update is pending (Windows cannot delete it while the chaining process still runs).
Only `ark serve` does any of this: CLI commands run constantly while a server
is up, and a swap under a live server would pair the new binary with the old
assets. The startup step is skipped on a developer install and whenever the
running binary is not the one in `~/.ark` (WSL interop, a binary run straight
from a build directory). A lock file in `~/.ark` keeps two starting processes
from both renaming.

**The UI's side** is three `sys` methods (see `specs/lua-api.md`):
`sys.checkForUpdate()` and `sys.requestUpdate()` start a check or a staging
in the background (running ark's own binary with `update --check` /
`update --stage`, no shell) and return at once; `sys.updateStatus()` reports
where things stand, for the UI to poll while one is in flight. A staged
update is reported as "takes effect the next time ark starts", naming its
version, which the status reads from `update.json` so the UI can still name it
after a restart.

### `ark init` and `ark ui install`

`ark init` still creates a database directly for callers that want only
that (initializes microfts2 and the ark bucket, writes default config; no
microvec init step, R1912), and `ark ui install` (alias `ark install`)
still performs the older per-project symlink install. Both remain for now;
`ark setup` is the path a new install should take.

### README-driven installation

The user's only action is telling Claude: "use the ark README to install
it." The README has Claude check whether `~/.ark/ark` exists, download the
binary to `~/.ark/ark` if not, and run `~/.ark/ark setup`. That one
command creates `~/.ark/`, extracts the bundle, creates the database, and
links the skills and agents into `~/.claude/` at user scope, so every
project sees them. In a specific project, `~/.ark/ark setup --local`
prints the CLAUDE.md bootstrap hint. No separate Frictionless install and
no `.ui/` prerequisite.
