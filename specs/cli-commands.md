# CLI Commands

Canonical reference for ark's CLI surface — every top-level command,
every subcommand, every flag.

This document describes the **current** state of the CLI. Per-feature
specs (e.g. `serve-compact.md`, `tag-verify.md`, `tag-block-commands.md`)
describe motivation and per-feature design; this document describes
what the CLI looks like to a caller. When the two disagree, this file
loses — update it to match.

Language: Go. Binary: `~/.ark/ark` (a `~/.ark` symlink populated by
`ark setup`). The Linux `ark` archive manager collides with the bare
name, so always use the absolute path.

## Command Inventory

| Command            | Synopsis                                                                                                             | Server                   | Notes                                                |
|--------------------|----------------------------------------------------------------------------------------------------------------------|--------------------------|------------------------------------------------------|
| `add`              | `add [--strategy S] [--content C \| --from-file F] [--append] PATH...`                                               | optional                 | tmp:// requires server                               |
| `bundle`           | `bundle -o OUT [-src SRC] DIR`                                                                                       | n/a                      | build-time                                           |
| `cat`              | `cat FILE`                                                                                                           | n/a                      | bundled binary only; alias of `bundle cat`           |
| `chats`            | `chats GLOB [--with-tools] [--sidechain] [--wrap N] [--line-length N]`                                               | none                     | walks `~/.claude/projects/`                          |
| `chunks`           | `chunks PATH RANGE [-before N] [-after N] [-wrap N]` <br> `chunks -status [PATTERN...]`                              | optional                 |                                                      |
| `chunk-chat-jsonl` | `chunk-chat-jsonl FILE`                                                                                              | n/a                      | internal chunker (microfts2 protocol)                |
| `config`           | `config [SUBCOMMAND ...]`                                                                                            | optional                 | subcommands below                                    |
| `cp`               | `cp PATTERN DEST-DIR`                                                                                                | n/a                      | bundled binary only; alias of `bundle cp`            |
| `dismiss`          | `dismiss PATTERN...`                                                                                                 | optional                 | drops M records                                      |
| `embed`            | `embed SUBCOMMAND ...`                                                                                               | none                     | subcommands below                                    |
| `fetch`            | `fetch [--wrap N] PATH...`                                                                                           | tmp:// only              | reads file content from index                        |
| `files`            | `files [--status] [--detail] [--filter-files G] [--exclude-files G] [PATTERN...]`                                    | optional                 |                                                      |
| `grams`            | `grams QUERY...`                                                                                                     | none                     | shows trigram index for query                        |
| `init`             | `init [--embed-cmd C] [--query-cmd C] [--case-insensitive] [--aliases A] [--no-setup] [--if-needed]`                 | none                     |                                                      |
| `install`          | (no flags)                                                                                                           | none                     | alias of `ui install`                                |
| `listen`           | `listen --session ID [--timeout N]`                                                                                  | required                 | long-poll; outputs markdown                          |
| `ls`               | `ls`                                                                                                                 | n/a                      | bundled binary only; alias of `bundle ls`            |
| `message`          | `message SUBCOMMAND ...`                                                                                             | mixed                    | subcommands below                                    |
| `missing`          | `missing [PATTERN...]`                                                                                               | optional                 |                                                      |
| `rebuild`          | `rebuild`                                                                                                            | refused                  | drops and rebuilds index; refuses if server up       |
| `refresh`          | `refresh [PATTERN...]`                                                                                               | optional                 | re-index stale files                                 |
| `remove`           | `remove PATTERN...`                                                                                                  | optional                 | tmp:// requires server                               |
| `resolve`          | `resolve PATTERN...`                                                                                                 | optional                 | dismisses unresolved files                           |
| `scan`             | `scan`                                                                                                               | optional                 | walks sources for new files                          |
| `schedule`         | `schedule SUBCOMMAND ...`                                                                                            | required (search/change) | subcommands below                                    |
| `search`           | `search [TERM...] [filter-stack] [options]` <br> `search expand ...`                                                 | preferred                | server proxy keeps caches warm                       |
| `serve`            | `serve [--no-scan] [--force] [--compact]`                                                                            | starts                   |                                                      |
| `setup`            | `setup`                                                                                                              | none                     | bootstrap `~/.ark/` (extract bundle, install skills) |
| `sources`          | `sources [check]`                                                                                                    | optional                 | currently only `check` subcommand                    |
| `stale`            | `stale [PATTERN...]`                                                                                                 | optional                 |                                                      |
| `status`           | `status [--db] [--chunks] [--tokenize] [--filter-files G] [--exclude-files G]`                                       | preferred                |                                                      |
| `stop`             | `stop [-f]`                                                                                                          | required                 | reads PID file, sends SIGTERM (or SIGKILL with `-f`) |
| `subscribe`        | `subscribe --session ID [--tag T] [--value RE] [--cancel] [--list] [--stats] [--filter-files G] [--exclude-files G]` | required                 |                                                      |
| `tag`              | `tag SUBCOMMAND ...`                                                                                                 | mixed                    | subcommands below                                    |
| `ui`               | `ui [SUBCOMMAND ...]`                                                                                                | required (most)          | 16 subcommands                                       |
| `unresolved`       | `unresolved`                                                                                                         | optional                 | lists U records                                      |

"Server" column:
- **none** — never proxies, never opens DB
- **optional** — proxies when server is running, falls back to direct DB access otherwise
- **preferred** — proxies if available; specific behavior degrades when server is absent
- **required** — exits with error if server is not running
- **refused** — exits with error if server *is* running
- **starts** — this command is what brings the server up
- **n/a** — no DB or server interaction
- **tmp:// only / mixed** — depends on argument shape (see per-command section)

## Global Flags

Parsed in `main()` before subcommand dispatch:

| Flag                         | Default                                  | Meaning                                                                                                           |
|------------------------------|------------------------------------------|-------------------------------------------------------------------------------------------------------------------|
| `--dir PATH` or `--dir=PATH` | `~/.ark` (or `.ark` if HOME unavailable) | Database directory                                                                                                |
| `-v` (repeatable)            | `0`                                      | Increase verbosity. `-vv`, `-vvv`, `-vvvv` are equivalent to repeating. Bound to package-level `Logv(level, ...)` |
| `--help` / `-h` / `help`     | —                                        | Print top-level usage and exit 0                                                                                  |

Verbosity expansion: `-vvv` is preprocessed into `-v -v -v` by
`cli.ExpandVerbosityFlags` so users can stack the flag without
worrying about the underlying tokenization.

## Conventions

### Server detection (`serverClient`)

Every command that may benefit from the running server calls
`serverClient(arkDir)` first. It dials the Unix socket at
`<dir>/ark.sock`. Connection success returns an `http.Client` that
proxies subsequent calls through the socket; failure returns `nil`,
and the command falls back to opening the LMDB environment directly
via `withDB`.

Server-first commands proxy as a transparent optimization: the server
keeps caches warm (file-name maps, LMDB pages, embedding model). Cold-
start fallbacks rebuild caches per invocation.

### Cold-start (`withDB`)

`withDB(fn)` opens the LMDB environment (`ark.Open(arkDir)`), runs
`fn`, and closes. Use is restricted to commands that don't require the
embedding model (about-search, tag embeddings) or live state
(subscriptions, schedule, dm, ext-routing maps).

### Exit codes

| Code | Meaning                                                                                                                                                     |
|------|-------------------------------------------------------------------------------------------------------------------------------------------------------------|
| `0`  | Success                                                                                                                                                     |
| `1`  | Operation failed (bad input, missing file, server not running for a server-required command, search returned nothing actionable, validation found problems) |
| `2`  | Verification failed at the tool level (e.g. `tag verify` could not run; bad `--scope` value)                                                                |

`fatal(err)` prints `error: <message>` to stderr and exits 1.

### Output formats

- **Plain text** is the default for status, lists, and human-oriented
  output. Tab-separated columns where multiple fields appear on one
  line.
- **JSON** is emitted when a subcommand has a structured result that
  callers may parse (`config show-why`, `embed text`).
- **JSONL** (one object per line) is emitted by `search --chunks`,
  `search --file-content`, and `chunks` (default).
- **`--wrap NAME`** wraps each output unit in `<NAME source=PATH ...>`
  XML tags. Closing `</NAME>` inside content is escaped to
  `&lt;/NAME>` to prevent premature tag closure. Used by `search`,
  `chunks`, `fetch`, `chats`. Convention: `memory` for experience,
  `knowledge` for facts.

### `tmp://` paths

Paths beginning with `tmp://` refer to ephemeral documents held in
server memory. Operations on them require a running server.
Affected commands: `add`, `remove`, `fetch`, `message dm`. The
server route mappings:

| CLI                | Endpoint                                           |
|--------------------|----------------------------------------------------|
| `add tmp://...`    | `POST /tmp/add` (or `/tmp/append` with `--append`) |
| `remove tmp://...` | `POST /tmp/remove`                                 |
| `fetch tmp://...`  | `POST /fetch` (server reads from memory)           |
| `message dm`       | `POST /tmp/append` to `tmp://<from>/dm-<to>`       |

### `reorderArgs`

Go's `flag` package stops at the first non-flag argument. Commands
that mix flags with positional arguments preprocess `args` through
`reorderArgs` to move flags to the front. Affected: `chunks`,
`config add-include`, `config add-exclude`, `config remove-pattern`,
`schedule search`, `schedule change`, `chats`.

### Filter stack (search)

The `search` command parses a filter stack before `flag.Parse` via
`parseFilterStack`. Mode flags (`-contains`, `-fuzzy`, `-regex`,
`-tag`, `-about`, `-files`) consume the next argument as the query.
Polarity flags (`-with`, `-without`) toggle the polarity of
subsequent entries. `--filter-k N` after an `-about` entry overrides
the per-row top-K. `-parse` prints the disambiguated command and
exits without searching. Bare terms coalesce into a single `-contains`
group. The first entry becomes the primary search; the rest become
chunk-level post-filters. See `search-cli-filters.md` for examples
and rationale.

### Path filters (`--filter-files` / `--exclude-files`)

Repeatable glob flags supported by `status`, `files`, `tag files`,
`tag values`, and `subscribe`. Patterns use `~` expansion via
`ark.ExpandTildeSlice`. `--filter-files` is positive; without any
positive filters, all paths are candidates. `--exclude-files` is
negative and applies after the positive set.

### Aliases

| Alias              | Resolves to  |
|--------------------|--------------|
| `cat`              | `bundle cat` |
| `cp`               | `bundle cp`  |
| `ls`               | `bundle ls`  |
| `install`          | `ui install` |
| `message set-tags` | `tag set`    |
| `message get-tags` | `tag get`    |

## Commands

### `add` — index files

```
ark add [options] PATH...
```

| Flag               | Default          | Meaning                                                    |
|--------------------|------------------|------------------------------------------------------------|
| `--strategy NAME`  | (chunker config) | Override chunking strategy                                 |
| `--content TEXT`   | —                | Inline content for tmp:// paths                            |
| `--from-file PATH` | —                | Read tmp:// content from file                              |
| `--append`         | `false`          | Append to an existing tmp:// document instead of replacing |

For tmp:// paths: server is required; content comes from `--content`,
`--from-file`, or stdin in that order; default strategy is `lines`.
For ordinary paths: server-proxy if running, otherwise `withDB`.

### `bundle` — graft assets onto binary

```
ark bundle -o OUT [-src SRC] DIR
```

| Flag        | Default            | Meaning                            |
|-------------|--------------------|------------------------------------|
| `-o PATH`   | required           | Output path for the bundled binary |
| `-src PATH` | current executable | Source binary to bundle            |

Build-time. Zip-grafts `DIR` onto `SRC` so a single binary contains
its UI assets.

### `cat`, `cp`, `ls` — bundled-asset access

```
ark ls
ark cat FILE
ark cp PATTERN DEST-DIR
```

These three are aliases for the corresponding `bundle ls / cat / cp`
operations. Available only when the running binary is bundled (see
`bundle`). `cat` writes the named file to stdout. `cp` matches files
against `PATTERN` (against basename and full path), preserving file
mode and recreating symlinks. Each exits 1 if the binary is not
bundled or the pattern matches nothing.

### `chats` — render JSONL transcripts

```
ark chats GLOB [options]
```

| Flag              | Default | Meaning                                  |
|-------------------|---------|------------------------------------------|
| `--with-tools`    | `false` | Render tool calls and results            |
| `--sidechain`     | `false` | Include subagent traffic                 |
| `--wrap NAME`     | —       | Wrap entire output in `<NAME>...</NAME>` |
| `--line-length N` | `100`   | Word-wrap column                         |

GLOB matches against the filename (not full path); files are
discovered by walking `~/.claude/projects/`. User turns prefix `❯`,
assistant turns prefix `●`. Exits 1 when no files match.

### `chunks` — chunk content / chunk size status

```
ark chunks PATH RANGE [-before N] [-after N] [-wrap NAME]
ark chunks -status [PATTERN...]
```

| Flag         | Default | Meaning                                     |
|--------------|---------|---------------------------------------------|
| `-before N`  | `0`     | Chunks before the target                    |
| `-after N`   | `0`     | Chunks after the target                     |
| `-wrap NAME` | —       | XML wrapping                                |
| `-status`    | `false` | Switch to status mode: `SIZE FILE:LOCATION` |

Default mode emits JSONL (one chunk per line, fields: path, range,
score, content). Status mode is right-aligned text.

### `chunk-chat-jsonl` — internal chunker

```
ark chunk-chat-jsonl FILE
```

A microfts2 chunker (v0.4 protocol): reads a file and emits
`range\tcontent` lines, one per newline-delimited input line.
Internal — invoked by microfts2, not by users.

### `config` — show or modify ark.toml

```
ark config                         # show
ark config add-source DIR
ark config remove-source DIR
ark config add-include PATTERN [--source DIR]
ark config add-exclude PATTERN [--source DIR]
ark config remove-pattern PATTERN [--source DIR]
ark config show-why FILE
ark config add-strategy PATTERN STRATEGY
ark config recover
```

Default (`config` with no subcommand): proxies to server if running
(`GET /config`), otherwise reads `~/.ark/ark.toml` directly.

| Subcommand                              | Body                                                  |
|-----------------------------------------|-------------------------------------------------------|
| `add-source DIR`                        | append to `[sources]` in ark.toml                     |
| `remove-source DIR`                     | drop from `[sources]` (existing files become orphans) |
| `add-include PATTERN [--source DIR]`    | append to global or per-source `include`              |
| `add-exclude PATTERN [--source DIR]`    | append to global or per-source `exclude`              |
| `remove-pattern PATTERN [--source DIR]` | drop matching include/exclude entry                   |
| `show-why FILE`                         | JSON: which include/exclude rules cover FILE          |
| `add-strategy PATTERN STRATEGY`         | append to chunker strategies                          |
| `recover`                               | rewrite ark.toml from the LMDB I-record snapshot      |

Mutating subcommands proxy to the server's `/config/...` endpoints
when the server is running so the in-memory config is kept current.
Cold-start mutations call `withConfig` (load → mutate → save).

### `dismiss` — drop missing files

```
ark dismiss PATTERN...
```

Removes M records (and associated entries) for files matching the
patterns. Server-proxy if available.

### `embed` — embedding operations

```
ark embed text TEXT...
ark embed bench (tags|chunks) [--ctx N] [--parallel N]
ark embed validate [--fix] [-v]
```

All embed subcommands run via `withDB`; they require `tag_model`
configured in ark.toml.

| Subcommand     | Flags                                                | Behavior                                                           |
|----------------|------------------------------------------------------|--------------------------------------------------------------------|
| `text`         | —                                                    | Embed the joined text and print the vector as JSON                 |
| `bench tags`   | `--ctx N` (default 2048), `--parallel N` (default 8) | Embed every (tag,value) compound, compare batch vs single          |
| `bench chunks` | as above                                             | Sample 200 chunks via file-first sampling, compare batch vs single |
| `validate`     | `--fix`, `-v`                                        | Cross-check EC/EF records against FTS chunks                       |

`embed validate` exits 1 if any problem is found (orphan EC, missing
EC, orphan EF, dimension inconsistency); `--fix` deletes orphan EC,
orphan EF, and wrong-dimension EC records.

### `fetch` — read indexed file content

```
ark fetch [--wrap NAME] PATH...
```

For tmp:// paths: server is required, reads via `POST /fetch`. For
ordinary paths: `withDB` reads via `db.Fetch(path)` (mmap shares
pages with server).

### `files` — list indexed files

```
ark files [options] [PATTERN...]
```

| Flag                   | Default | Meaning                                                                                  |
|------------------------|---------|------------------------------------------------------------------------------------------|
| `--status`             | `false` | Add `STATUS BYTES CHUNKS PATH` columns; status is `G` (good), `S` (stale), `M` (missing) |
| `--detail`             | `false` | With `--status`, add per-file chunk size statistics line                                 |
| `--filter-files GLOB`  | —       | Repeatable positive path filter                                                          |
| `--exclude-files GLOB` | —       | Repeatable negative path filter                                                          |

Positional patterns narrow within the filter-files set. Without
`--status`, output is one path per line.

### `grams` — show trigrams

```
ark grams QUERY...
```

Joins arguments with spaces and prints `"trigram"\tcount` for each
matching trigram in the FTS index. Cold-start only.

### `init` — create database

```
ark init [options]
```

| Flag                    | Default | Meaning                                    |
|-------------------------|---------|--------------------------------------------|
| `--embed-cmd CMD`       | —       | Embedding command (enables vector search)  |
| `--query-cmd CMD`       | —       | Query embedding command                    |
| `--case-insensitive`    | `true`  | Case-insensitive indexing                  |
| `--aliases A=B,C=D,...` | —       | Single-byte alias map                      |
| `--no-setup`            | `false` | Skip `setup` even when not bootstrapped    |
| `--if-needed`           | `false` | Exit silently if `data.mdb` already exists |

Without `--if-needed`, removes existing `data.mdb`/`lock.mdb` before
creating fresh. Auto-runs `setup` when `~/.ark/html/` is missing
(unless `--no-setup`). Seeds tags and config from the bundle's
`install/tags.md` and `install/ark.toml` when present. Creates
`~/.ark/searching/CLAUDE.md` with the default spectral search prompt
if missing.

### `install` — connect a project to ark

Alias for `ark ui install`. See that subcommand for behavior.

### `listen` — long-poll for tag notifications

```
ark listen --session ID [--timeout N]
```

| Flag           | Default  | Meaning                      |
|----------------|----------|------------------------------|
| `--session ID` | required | Session ID                   |
| `--timeout N`  | `120`    | Long-poll timeout in seconds |

Server-required. Returns markdown crank handles describing fired
subscriptions. HTTP 204 (no events within timeout) is treated as a
successful non-event return.

### `message` — messaging operations

```
ark message new-request   --from P --to P --issue TEXT [--content BODY] FILE
ark message new-response  --from P --to P --request ID [--content BODY] FILE
ark message set-tags      FILE TAG VAL [TAG VAL ...]
ark message get-tags      FILE [TAG ...]
ark message check         FILE
ark message inbox         [--project P] [--to P] [--from P] [--all] [--include-archived] [--counts] [--unmatched]
ark message dm            --from S --to S [--ref ID] --content TEXT
```

| Subcommand     | Flags                                                                | Behavior                                                                                                                                        |
|----------------|----------------------------------------------------------------------|-------------------------------------------------------------------------------------------------------------------------------------------------|
| `new-request`  | `--from`, `--to`, `--issue`, `--content` (else stdin until lone `.`) | Create FILE (must not exist), set ark-request/from-project/to-project/status=open/status-date=today/issue tags. Body from `--content` or stdin. |
| `new-response` | `--from`, `--to`, `--request`, `--content` (else stdin)              | Create FILE, set ark-response=ID/status=accepted/status-date=today.                                                                             |
| `set-tags`     | none                                                                 | Alias for `tag set`                                                                                                                             |
| `get-tags`     | none                                                                 | Alias for `tag get`                                                                                                                             |
| `check`        | none                                                                 | Calls `tag check` with the standard message heading list                                                                                        |
| `inbox`        | see below                                                            | Server-first; pairs requests/responses by ID                                                                                                    |
| `dm`           | `--from`, `--to`, `--ref`, `--content`                               | Server-required; appends a tagged chunk to `tmp://<from>/dm-<to>`                                                                               |

`message inbox` flags:

| Flag                 | Default | Meaning                                                     |
|----------------------|---------|-------------------------------------------------------------|
| `--project P`        | —       | Filter by EITHER `to-project` OR `from-project` (R2431)     |
| `--to P`             | —       | Filter by `to-project` (R2430; the old `--project` meaning) |
| `--from P`           | —       | Filter by `from-project`                                    |
| `--all`              | `false` | Include completed/done/denied messages                      |
| `--include-archived` | `false` | Include `@archived: true` messages                          |
| `--counts`           | `false` | Output `STATUS\tCOUNT` lines instead of rows                |
| `--unmatched`        | `false` | Show only requests with no matching response                |

Filters combine as intersection: `--from ark --to frictionless`
shows only the ark→frictionless slice.

Default inbox row is tab-separated:
`DATE STATUS TO FROM SUMMARY PATH LAG`. Lag format
`lag:PROJECT:STATUS` describes a stale `response-handled` /
`request-handled` bookmark relative to the counterpart's status.

### `missing`, `stale`, `unresolved`, `resolve`

```
ark missing    [PATTERN...]
ark stale      [PATTERN...]
ark unresolved
ark resolve    PATTERN...
```

`missing` lists files indexed but no longer on disk (M records).
`stale` lists files whose mtime is newer than their last index pass.
`unresolved` lists files seen during a scan that don't match any
include/exclude rule (U records). `resolve PATTERN` dismisses
matching unresolved entries.

All four proxy to the server when running.

### `rebuild` — drop and rebuild

```
ark rebuild
```

Refuses if the server is running. Calls `init --no-setup` (which
clears `data.mdb`/`lock.mdb` and re-creates from ark.toml), then
`scan`. Embeddings regenerate on the next `ark serve`.

### `refresh` — re-index stale files

```
ark refresh [PATTERN...]
```

Re-indexes files whose mtime is newer than the index. Optional
glob patterns scope the refresh.

### `remove` — drop files from index

```
ark remove PATTERN...
```

Glob patterns. tmp:// paths require server.

### `scan` — walk source dirs

```
ark scan
```

Walks configured source directories, indexes new files, flags
unresolved. Does not re-check existing files (`refresh` does that).
Reports `new files: N, new unresolved: N`.

### `schedule` — query and modify scheduled events

```
ark schedule search DATE [--tag T] [--gaps] [--json]
ark schedule change PATH TAG NEWSTART [NEWEND] [--dry-run]
ark schedule tags [--values]
ark schedule parse DATE
```

| Subcommand | Flags | Behavior |
|------------|-------|----------|
| `search DATE` | `--tag` (filter), `--gaps` (only past unacked), `--json` (raw response) | Server-required. DATE is a single date or `START..END` range; `dateparse` handles flexible formats. Default output is markdown grouped by date. |
| `change PATH TAG NEWSTART [NEWEND]` | `--dry-run` | Server-required. Rewrites date in schedule tag value, preserves trailing description. Re-indexes after write. Updates `@ark-event-upcoming:` for recurring events. |
| `tags` | `--values` | Cold-start. Lists configured schedule tags from ark.toml. With `--values`, also reads schedule logs (`~/.ark/schedule/`) for next upcoming dates. |
| `parse DATE` | — | Cold-start (no DB). Parses a date expression, prints start/end/all-day/text. Recognizes recurring specs and computes next occurrence. |

### `search` — search the index

```
ark search [TERM...] [filter-stack] [options]
ark search expand [SUBCOMMAND...]
```

Filter-stack flags (parsed before `flag.Parse`):

| Flag             | Meaning                                                 |
|------------------|---------------------------------------------------------|
| `-contains TERM` | Substring match (default for bare terms)                |
| `-fuzzy TERM`    | Typo-tolerant match                                     |
| `-regex PATTERN` | RE2 match                                               |
| `-tag TAG`       | Tag filter (`name`, `name:value`, optional `@` prefix)  |
| `-about QUERY`   | Vector similarity (server required for embedding model) |
| `-files GLOB`    | Path glob filter                                        |
| `-with`          | Subsequent filters intersect (default polarity)         |
| `-without`       | Subsequent filters subtract                             |
| `--filter-k N`   | After an `-about` entry, override per-row top-K         |
| `-parse`         | Print disambiguated command and exit without searching  |

Conventional flags:

| Flag               | Default | Meaning                                                                                            |
|--------------------|---------|----------------------------------------------------------------------------------------------------|
| `-k N`             | `20`    | Max results                                                                                        |
| `-scores`          | `false` | Show scores; multi-strategy results show strategy column                                           |
| `-after DATE`      | —       | Only results after date                                                                            |
| `-before DATE`     | —       | Only results before date                                                                           |
| `-like-file PATH`  | —       | Find similar files (FTS density). Mutually exclusive with `-contains`/`-regex`                     |
| `-score MODE`      | `auto`  | `auto`/`coverage`/`density`. Mutually exclusive with `-multi`                                      |
| `-multi`           | `false` | Run all strategies (coverage/density/overlap/bm25), merge. Excludes `-about`/`-regex`/`-like-file` |
| `-proximity`       | `false` | Rerank top 2k candidates by query-term proximity                                                   |
| `-session NAME`    | —       | Named session for cross-query cache (server only)                                                  |
| `-no-tmp`          | `false` | Exclude tmp:// documents                                                                           |
| `-tags`            | `false` | Output extracted tag names instead of content                                                      |
| `-chunks`          | `false` | Emit chunk text as JSONL. Mutually exclusive with `-file-content`                                  |
| `-file-content`    | `false` | Emit full file content as JSONL                                                                    |
| `-preview N`       | `0`     | With `-chunks`: extract N-char preview window around match                                         |
| `-wrap NAME`       | —       | Wrap output in `<NAME>` tags                                                                       |
| `-cpuprofile FILE` | —       | Write CPU profile                                                                                  |
| `-memprofile FILE` | —       | Write heap profile (post-GC, on exit)                                                              |
| `-trace FILE`      | —       | Write runtime/trace execution trace                                                                |

Server is preferred (warm caches). When the server is absent, about-
mode primary or filter searches exit with an error directing the user
to start `ark serve`. `-multi` requires the LMDB Multi search path.

`search expand` is the spectral-search sidecar entry point:

| Flag                 | Meaning                                                                       |
|----------------------|-------------------------------------------------------------------------------|
| `--wait`             | Lotto tube: block until expansion requests arrive (`GET /search/curate/wait`) |
| `--fuzzy JSON`       | Fuzzy-match JSON `[{tag,value},...]` against V records                        |
| `--search JSON`      | Search curated `[{tag,value},...]` and return chunk results                   |
| `--result ID JSON`   | Search curated JSON and post the result for request ID                        |
| `--error ID=MESSAGE` | Post an error for request ID                                                  |

With no flag and a positional `<tag> [value]`: queue an expansion via
`POST /search/curate` and poll `/search/curate/result/<id>` until
done. Server is required.

### `serve` — start the server

```
ark serve [--no-scan] [--force] [--compact]
```

| Flag        | Default | Meaning                                                                                                             |
|-------------|---------|---------------------------------------------------------------------------------------------------------------------|
| `--no-scan` | `false` | Skip startup reconciliation                                                                                         |
| `--force`   | `false` | Accept config changes, clear E records                                                                              |
| `--compact` | `false` | Run `mdb_env_copy2(MDB_CP_COMPACT)` against each LMDB env before opening. When the flag is supplied (either form), it overrides `auto_compact` in ark.toml; when omitted, falls back to the toml setting (default `false`). See `serve-compact.md`. |

Exits 0 with a stderr message if a server is already running. The
process holds the file lock on `~/.ark/`, writes a PID file, and
listens on `<dir>/ark.sock` (and an HTTP port for browsers).

### `setup` — bootstrap `~/.ark/`

```
ark setup
```

Idempotent. Extracts bundled assets (`html/`, `lua/`, `viewdefs/`,
`apps/`, `skills/`, `agents/`) into `~/.ark/`, runs `linkapp` for the
`ark` app, and installs global skills + agent into `~/.claude/`.
Requires the running binary to be bundled.

### `sources` — manage source directories

```
ark sources [check]
```

Default subcommand is `check`: expand globs in ark.toml's
`[sources]`, add new directories, report orphans. Output uses `+` /
`-` / `?` prefixes (added / MIA / orphaned). Server-proxy if
available.

### `status` — show database status

```
ark status [options]
```

| Flag                   | Default | Meaning                                                                                |
|------------------------|---------|----------------------------------------------------------------------------------------|
| `--db`                 | `false` | Show LMDB record counts by prefix (microfts2 + ark, with totals)                       |
| `--chunks`             | `false` | Show chunk size statistics (count, min, max, mean, median, p90, p95, p99) per strategy |
| `--tokenize`           | `false` | With `--chunks`: measure in tokens; requires `tag_model`                               |
| `--filter-files GLOB`  | —       | Repeatable positive path filter for `--chunks`                                         |
| `--exclude-files GLOB` | —       | Repeatable negative path filter                                                        |

Default output: file/chunk/source counts, map usage, server/UI
status, plus a `warnings:` section listing E records (each
`name: payload`). Prefers server proxy.

### `stop` — stop the server

```
ark stop [-f]
```

Reads the PID file, verifies the process is alive, sends SIGTERM (or
SIGKILL with `-f`), polls every 100ms for up to 10 seconds. Exits 1
on missing PID file, dead PID, or timeout.

### `subscribe` — manage tag subscriptions

```
ark subscribe --session ID [options]
```

| Flag                   | Default                      | Meaning                                                        |
|------------------------|------------------------------|----------------------------------------------------------------|
| `--session ID`         | required for register/cancel | Session ID                                                     |
| `--cancel`             | `false`                      | Cancel matching subscription(s)                                |
| `--list`               | `false`                      | List active subscriptions (`session\ttag\tvalue\thits\tdrops`) |
| `--stats`              | `false`                      | Per-session totals                                             |
| `--tag T`              | —                            | Tag name. Leading `@` and trailing `:` stripped                |
| `--value RE`           | —                            | RE2 value filter; omit to match all values                     |
| `--filter-files GLOB`  | —                            | Repeatable positive path filter                                |
| `--exclude-files GLOB` | —                            | Repeatable negative path filter                                |

Server-required. Without `--list`/`--stats`/`--cancel`, a register
call requires `--session` and `--tag`.

### `tag` — tag operations

```
ark tag list
ark tag counts TAG...
ark tag files TAG... [--context] [--filter-files G] [--exclude-files G]
ark tag values TAG... [--files] [--filter-files G] [--exclude-files G]
ark tag defs [TAG...] [--path]
ark tag set FILE TAG VAL [TAG VAL ...]
ark tag get FILE [TAG ...]
ark tag check FILE [HEADING...]
ark tag verify [--repair] [--scope SCOPE]
ark tag inspect [--scope SCOPE] [--target PATH] [--json]
```

| Subcommand | Server | Behavior |
|------------|--------|----------|
| `list` | optional | All tags with totals (`tag\tcount`) |
| `counts TAG...` | optional | Counts for the named tags |
| `files TAG...` | optional | Files containing each tag (`path\tsize`); `--context` switches to per-occurrence lines |
| `values TAG...` | cold-start | Values for each tag (`tag\tvalue\tcount`); with `--files`, follows up with per-file lines |
| `defs [TAG...]` | optional | Tag definitions (`tag description`); with `--path`, prefixes the source path (no dedup) |
| `set FILE TAG VAL ...` | none (file I/O) | Pairs of TAG VAL into FILE's tag block. Setting `status` auto-sets `status-date` to today. Hint when setting `*-handled` bookmarks |
| `get FILE [TAG...]` | none | Read tags from FILE's tag block. Without TAGs, dump all. Missing tags exit 1 |
| `check FILE [HEADING...]` | none | Validate FILE's tag block. Optional headings restrict allowed body headings |
| `verify` | refused | Cross-check F/V/T/X records and the in-memory ExtMap. `--repair` writes corrections in a single LMDB write txn. `--scope` is `ext`, `tag-totals`, or `all` (default). Refuses if the server is running. Exit 1 on issues, 2 on tool failure or invalid scope |
| `inspect` | optional | Read-only observability for `@ext` state. Server-aware: proxies via the running server (in-memory ExtMap section included) or opens LMDB read-only when stopped (disk-only with a note). `--scope ext` (v1 only); `--target PATH` narrows to one file's chunks; `--json` for machine output. Output sections: on-disk (X / V[ext] / F[ext]), in-memory ExtMap maps, per-tvid_ext bridges with decoded paths and routed (tag, value) pairs |

### `ui` — UI operations

```
ark ui                          # open browser
ark ui audit APP
ark ui checkpoint CMD APP [MSG]
ark ui display APP
ark ui event
ark ui install
ark ui linkapp (add|remove) APP
ark ui patterns
ark ui progress APP PCT STAGE
ark ui reload
ark ui run 'LUA'
ark ui state
ark ui status
ark ui theme (list|classes [THEME]|audit APP [THEME])
ark ui update [-t]
ark ui variables
```

All UI subcommands except `open` (the no-args default) require the
server: they send HTTP requests through `<dir>/ark.sock`. Most are
thin wrappers over Lua API endpoints.

| Subcommand | Behavior |
|------------|----------|
| (none) | Read `<dir>/ui-port`, open browser via `xdg-open`/`open` |
| `audit APP` | `POST /api/ui_audit` — code quality audit |
| `checkpoint CMD APP [MSG]` | Fossil-backed checkpoints (see below) |
| `display APP` | `POST /api/ui_display` — show app in browser |
| `event` | `GET /wait?timeout=120` — block until next UI event; retries on transient |
| `install` | Per-project setup (alias `ark install`): runs `init --if-needed`, starts server if down, symlinks `~/.ark/skills/{ark,ui}` and `~/.ark/agents/ark.md` into `.claude/`, prints crank-handle prompt |
| `linkapp add APP` / `remove APP` | Manage `~/.ark/lua/<app>` and `~/.ark/viewdefs/` symlinks |
| `patterns` | List `~/.ark/patterns/*.md` with frontmatter description |
| `progress APP PCT STAGE` | `mcp:appProgress` + agent-thinking line |
| `reload` | `POST /ui/reload` — fresh Lua VM |
| `run 'LUA'` | `POST /api/ui_run` — execute Lua code |
| `state` | `GET /state` — current session state |
| `status` | `GET /status` — UI port, indexing flag |
| `theme list` / `theme classes [THEME]` / `theme audit APP [THEME]` | `POST /api/ui_theme` |
| `update [-t]` | `POST /api/ui_update`; `-t` switches to a version-check (`GET /api/ui_status`) |
| `variables` | `GET /variables` |

`ui checkpoint` subcommands (Fossil-backed; require `~/.claude/bin/fossil`,
which the command will print download instructions for if missing):

| Sub-subcommand               | Behavior                                                                             |
|------------------------------|--------------------------------------------------------------------------------------|
| `save APP [MSG]`             | Save a checkpoint (initializes Fossil repo on first call)                            |
| `list APP`                   | Timeline of checkpoints (Fossil footer trimmed)                                      |
| `rollback APP [N]`           | Without N: `fossil undo`. With N: checkout the Nth most recent commit                |
| `diff APP [N]`               | Diff against the Nth checkpoint (default 1)                                          |
| `clear APP` / `baseline APP` | Reset to baseline; preserves `updates` and `local` branches via bundle export/import |
| `count APP`                  | Print number of trunk checkpoints (excluding baseline)                               |
| `update APP [MSG]`           | Save to `updates` branch (refuses if uncommitted trunk checkpoints exist)            |
| `local APP [MSG]`            | Save to `local` branch                                                               |

## Notes

- `cmd*` functions live in `cmd/ark/main.go` (and `cmd/ark/chats.go`
  for `chats`). Each subcommand has its own `cmdFooBar` function.
- `tag-overview` and other UI features that don't add CLI surface are
  documented in their per-feature specs only.
