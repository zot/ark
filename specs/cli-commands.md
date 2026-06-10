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

**Maintaining this file.** This is the canonical CLI inventory. Since the
2026-06-08 `urfave/cli` v3 migration, the binary's own `--help` is
**generated from the command tree** — one self-documenting source per
command (its node's `Name` / `Usage` / `Flags`, or, for the `search` DSL,
a single hand-written `Description`) — so it can no longer drift from the
code. The hand-maintained `usage()` and the per-command `--help` printers
(`printConnectionsHelp`, `uiUsage`, `printConfigHelp`, the `luhmann` /
`schedule` usage blocks) no longer exist. Two surfaces remain, and only
one is hand-kept:

1. the `urfave/cli` command tree (`cmd/ark/*_cli.go` + `arkCommands()`) —
   makes each command work **and** generates its help;
2. this file — the hand-kept inventory mirror and behavioral contract.

Cheap completeness check: diff the binary's top-level command names
(`ark --help`) against this file's inventory table. (Before the migration,
a CLI change had to be landed in four hand-maintained places that freely
diverged; a 2026-05-30 audit found nine drifts at once. That class of bug
is now structurally impossible for the binary's own help.)

**Output ordering.** Human-facing list output is sorted deterministically so it
is easy to scan: the binary's `--help` lists subcommands alphabetically at every
depth (matching this inventory's order below), and `ark status` sorts its
`strategies:` and `warnings:` lines by name (both back onto Go maps, which
otherwise emit a different order each run). R2953.

## Command Inventory

| Command            | Synopsis                                                                                                             | Server                   | Notes                                                |
|--------------------|----------------------------------------------------------------------------------------------------------------------|--------------------------|------------------------------------------------------|
| `add`              | `add [--strategy S] [--content C \| --from-file F] [--append] PATH...`                                               | optional                 | tmp:// requires server                               |
| `bundle`           | `bundle -o OUT [-src SRC] DIR`                                                                                       | n/a                      | build-time                                           |
| `cat`              | `cat FILE`                                                                                                           | n/a                      | bundled binary only; alias of `bundle cat`           |
| `chats`            | `chats GLOB [--with-tools] [--sidechain] [--wrap N] [--line-length N]`                                               | none                     | walks `~/.claude/projects/`                          |
| `chunks`           | `chunks CHUNKID [-before N] [-after N] [-wrap N]` <br> `chunks PATH:RANGE [-before N] [-after N] [-wrap N]` <br> `chunks PATH:RANGE:"SNIPPET" [-wrap N]` <br> `chunks PATH RANGE [-before N] [-after N] [-wrap N]` <br> `chunks -status [PATTERN...]` | optional                 | `CHUNKID` resolves via `db.ChunkInfo`; `PATH:RANGE` accepts `NN` and `NN-MM` range labels; `PATH:RANGE:"SNIPPET"` is the recall chat sub-chunk locator (R2914) — returns the matched paragraph; drop the snippet for the whole turn |
| `chunk-chat-jsonl` | `chunk-chat-jsonl FILE`                                                                                              | n/a                      | internal chunker (microfts2 protocol)                |
| `config`           | `config [SUBCOMMAND ...]`                                                                                            | optional                 | subcommands below                                    |
| `connections`      | `connections SUBCOMMAND ...`                                                                                          | required                 | substrate + sidecar CLI (subcommands below)          |
| `cp`               | `cp PATTERN DEST-DIR`                                                                                                | n/a                      | bundled binary only; alias of `bundle cp`            |
| `discussed`        | `discussed SUBCOMMAND ...`                                                                                            | optional                 | per-session recall dedup state (subcommands below)   |
| `dismiss`          | `dismiss PATTERN...`                                                                                                 | optional                 | drops M records                                      |
| `embed`            | `embed SUBCOMMAND ...`                                                                                               | none                     | subcommands below                                    |
| `fetch`            | `fetch [--wrap N] PATH...`                                                                                           | tmp:// only              | reads file content from index                        |
| `files`            | `files [--status] [--detail] [--filter-files G] [--exclude-files G] [PATTERN...]`                                    | optional                 |                                                      |
| `grams`            | `grams QUERY...`                                                                                                     | none                     | shows trigram index for query                        |
| `init`             | `init [--embed-cmd C] [--query-cmd C] [--case-insensitive] [--aliases A] [--no-setup] [--if-needed]`                 | none                     |                                                      |
| `install`          | (no flags)                                                                                                           | none                     | alias of `ui install`                                |
| `listen`           | `listen --session ID [--timeout N]`                                                                                  | required                 | long-poll; outputs markdown                          |
| `ls`               | `ls`                                                                                                                 | n/a                      | bundled binary only; alias of `bundle ls`            |
| `luhmann`          | `luhmann SUBCOMMAND ...`                                                                                             | mixed                    | orchestrator supervisor log writer (subcommands below) |
| `monitor`          | `monitor SUBCOMMAND ...`                                                                                             | mixed                    | inspect `~/.ark/monitoring/*.jsonl` (subcommands below) |
| `message`          | `message SUBCOMMAND ...`                                                                                             | mixed                    | subcommands below                                    |
| `missing`          | `missing [PATTERN...]`                                                                                               | optional                 |                                                      |
| `nano`             | `nano [-m model] [-c \| -s] [--base-url URL] [--max-steps N] [--approve-all] [--stream] [prompt...]`                 | none                     | embedded shell-agent loop (Ollama-backed)            |
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
| `subscribe`        | `subscribe --session ID [--tag T]... [--file-tag T]... [--cancel] [--list] [--stats] [--filter-files G] [--exclude-files G]` | required                 |                                                      |
| `subscribers`      | `subscribers --tag T [--quiet]`                                                                                      | required                 | count subscriptions matching a tag                   |
| `sweep`            | `sweep correlations`                                                                                                | required                 | hot-correlations top-K cache refresh (subcommands below) |
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

`urfave/cli` root flags, recognized **before** the subcommand; a root
`Before` hook copies them into the package globals the handler bodies read:

| Flag                         | Default                                  | Meaning                                                                                                           |
|------------------------------|------------------------------------------|-------------------------------------------------------------------------------------------------------------------|
| `--dir PATH` or `--dir=PATH` | `~/.ark` (or `.ark` if HOME unavailable) | Database directory                                                                                                |
| `-v` (repeatable)            | `0`                                      | Increase verbosity. `-vv`, `-vvv`, `-vvvv` are equivalent to repeating. Bound to package-level `Logv(level, ...)` |
| `--help` / `-h`              | —                                        | Show tree-generated help and exit 0 — at any node (`ark --help`, `ark connections --help`, `ark connections recall close --help`), each with the full command path |

`help [COMMAND]` and bare `ark` also print the generated command list
(exit 0); an unknown command name prints `unknown command: <name>` and
exits 1. Verbosity expansion: `-vvvv` is preprocessed into `-v -v -v -v`
by `cli.ExpandVerbosityFlags` (urfave does not bundle short flags), and the
root's `-v` count flag accumulates them.

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
| `message dm`       | `POST /tmp/append` to `tmp://<sender>/dm-<to0>` (sender = `--from` session or `--from-service` identity; `<to0>` = first `--to`) |

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
| `--strategy NAME`  | (resolved from source) | Override the chunking strategy; required for a file outside every source |
| `--content TEXT`   | —                | Inline content for tmp:// paths                            |
| `--from-file PATH` | —                | Read tmp:// content from file                              |
| `--append`         | `false`          | Append to an existing tmp:// document instead of replacing |

For tmp:// paths: server is required; content comes from `--content`,
`--from-file`, or stdin in that order; default strategy is `lines`.
For ordinary paths: server-proxy if running, otherwise `withDB`. With
no `--strategy`, a single file's strategy is resolved from its
enclosing source (the same resolution the directory walk uses,
default `lines`); a file outside every configured source has nothing
to resolve against and is rejected as a client error (HTTP 400) — pass
`--strategy` to add it anyway.

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
ark chunks CHUNKID [-before N] [-after N] [-wrap NAME]
ark chunks PATH:RANGE [-before N] [-after N] [-wrap NAME]
ark chunks PATH:RANGE:"SNIPPET" [-wrap NAME]
ark chunks PATH RANGE [-before N] [-after N] [-wrap NAME]
ark chunks -status [PATTERN...]
```

The single-argument forms make it easy to paste a line straight from
`ark search`, `ark recall`, or recall-DM output. `CHUNKID` (all digits)
resolves to (path, range) via `db.ChunkInfo`; `PATH:RANGE` splits on
the last `:` and accepts `NN` or `NN-MM` range labels.

`PATH:RANGE:"SNIPPET"` is the **recall chat sub-chunk locator** (R2914):
the quoted snippet selects the matched markdown sub-chunk (paragraph)
within a conversation turn — `db.ChatSubchunk` re-chunks the turn and
returns the sub-chunk whose content contains the snippet. **Drop the
`:"SNIPPET"` (fetch `PATH:RANGE`) for the whole turn** — the zoom-out
for fuller context.

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

### `connections` — find-connections substrate + sidecar CLI

```
ark connections find INPUTS... [--mode normal|turbo] [--k N]
                              [--purpose curate|recall] [--timeout S]
                              [--type chunk|text]
                              [--wait] [--json]
ark connections recall INPUTS... [--k N] [-all] [--no-content]
                                 [--type chunk|text] [--json]
                                 [--session SID] [--discussed @t[:v][,@t[:v]...]]
                                 [--propose]
ark connections recall reserve-nonce
ark connections recall next [--session SID] NONCE
ark connections recall surface FIRE -loc PATH:RANGE -reason TEXT
ark connections recall recommend FIRE -loc PATH:RANGE -tag @t[:v] -reason TEXT
ark connections recall finding COOKIE (-loc PATH:RANGE [-note TEXT] | -answer TEXT)
ark connections recall close FIRE --nonce N [-preserve-curation]
ark connections recall context --nonce N [--limit N] [--json]
ark connections recall listen --session SID [--ambient]
ark connections wait PATH [--timeout S] [--json]
ark connections show PATH [--status] [--tags] [--tag NAME]
                          [--threshold N] [--json]
ark connections list [--json]
ark connections clean [-all] [-checkpoint] [-session ID|project]
ark connections sidecar-wait
ark connections sidecar-fetch ID
ark connections sidecar-result ID            # stdin JSON
ark connections sidecar-error ID MESSAGE
```

Substrate + lifecycle for `sys.findConnections`. The doc lifecycle
lives at `tmp://connections/<id>.md` with `@purpose` /
`@connections-mode` / `@connections-status` headers (see
[find-connections-substrate.md](find-connections-substrate.md) and
[find-connections.md](find-connections.md)).

**Public subcommands (humans + downstream agents):**

- `find` accepts mixed inputs:
  - decimal `NNNNNN` → chunkID
  - `PATH:N-M` or `PATH:N` → file path with line range (1-based inclusive)
  - anything else → bare text, embedded on the fly (no quoting required)

  Each token is auto-detected by default. `--type chunk|text` forces
  every positional input to a single category. `chunk` still accepts
  both decimal chunkIDs and `PATH:locator` forms (shape selects which).
  `--type text` is the way to feed literal text that happens to look
  like a chunkID or path:locator.

  Returns the `tmp://connections/<id>.md` path on stdout. With
  `--wait`, blocks until `@connections-status` is terminal and
  prints the body. With `--json`, emits the parsed projection.
- `recall` is the chunk-similarity substrate primitive (see
  [recall.md](recall.md)). Same input forms as `find`. Returns the
  top-K chunks ranked by EC similarity (vector + trigram-Jaccard),
  with their tags + per-substrate evidence. Output is a baby-food
  markdown stencil (or JSON with `--json`); not a `tmp://` doc.
  By default drops tagless chunks since they can't contribute tag
  information to downstream tag-shaped consumers; `-all` keeps
  them. `--no-content` omits per-chunk content. `--session SID`
  reads the session's recall-discussed tags (RD records) and
  strips them from candidate chunks; `--discussed` carries an
  explicit caller-supplied list and unions with the session
  list. Per-chunk filter is permissive — a chunk survives if it
  has any non-discussed tag left. See
  [discussed-tags.md](discussed-tags.md) for the dedup story and
  TTL semantics. `--propose` runs the statistical derivation
  pass on the substrate's full scored chunk set, persists
  surviving candidates as RC records, and adds a
  `@chunk-proposed-tags` line to each surfaced chunk that
  accumulated proposals (similarity-desc order). See
  [derived-tags.md](derived-tags.md). The simple-recall watcher
  built on top of this substrate is described in
  [simple-recall.md](simple-recall.md).
- `recall reserve-nonce` returns the next monotonic integer the
  Luhmann orchestrator uses as the recall daemon's per-generation
  nonce (the `nonce → .meta.json` discovery key). The counter is
  in-memory and resets on `ark serve` restart. See
  [simple-recall.md](simple-recall.md).
- `recall next [--session SID] NONCE` is the recall secretary's entire
  loop — a batteries-included crank handle. With `--session SID` (the
  per-session secretary, seam 3a) it subscribes **value-scoped**
  `@ark-recall-curate=<SID>` and dispatches only that session's curation
  docs, prepending the session's last-N conversation turns
  (`[recall].context_turns`) to the doc it hands back; without it, the
  legacy bare-curate, all-session scan is retained (one-shot/diagnostic).
  On first call it idempotently subscribes — subscription session
  `recall-curate-<SID>` with `--session` (keyed on the durable session so a
  restart can't recycle it, R2902), else legacy `recall-<NONCE>`; thereafter
  it context-gates, then returns the
  lowest-fire pending curation doc **whose session has a result
  subscriber** (docs for unsubscribed sessions pile up, never dispatched),
  with crank-handle prose telling the caller to judge, surface /
  recommend, close, and loop. When none is dispatchable it
  **blocks up to a ~90-second keepalive**, then returns a keepalive
  directive ("run `next` again"). The window is sized under the harness
  foreground-Bash auto-background threshold (~120s) so `next` returns
  inline and the recall subagent stays in one continuous turn — a
  detached call would end the turn and emit a per-cycle "completed"
  beat the orchestrator can't tell from a real exit. At
  `[luhmann].context_limit` it returns an exit directive instead. Dual
  output: exit status `0` = doc *or* keepalive (loop), `2` = exit/done;
  a hand-written `while` loop is as good a client as the agent.
  Requires `ark serve` — but an `ark serve` bounce is absorbed as a
  wait condition: the CLI redials (cold dial) or returns a keepalive
  (mid-block / budget), never a fatal error and never the exit
  directive, so the loop rides out a restart (R2903).
- `recall surface FIRE -loc PATH:RANGE -reason TEXT` implicitly opens
  the result-doc builder for `FIRE` on first call and adds one
  `## Surface:` item. `FIRE` is the composite `<session>-<fire>` token
  the crank-handle emits (R2901); the caller passes the candidate's
  `<path>:<range>` and the server resolves size + content (R2900). One
  item per call; repeated `-loc` flags are not accepted. Called by the
  recall agent only.
- `recall recommend FIRE -loc PATH:RANGE -tag @t[:v] -reason TEXT` —
  same shape, adds one `## Recommend:` item.
- `recall finding COOKIE (-loc PATH:RANGE [-note TEXT] | -answer TEXT)`
  adds one `## Finding:` item to the **directed-search** (bloodhound)
  builder for `COOKIE` — the kind-marked `<session>-b<B>` token the search
  crank-handle emits. A `-loc` finding renders a curated `<path>:<range>`
  line (server-resolved size; optional `-note`); an `-answer` carries a
  synthesized answer/verdict. One item per call, no own-session gate
  (unlike `surface`). The same `recall close COOKIE --nonce N` finalizes
  it, writing `tmp://ARK-BLOODHOUND/finding-<S>-<B>`. Called by the
  secretary on a directed hunt; see [bloodhound.md](bloodhound.md).
- `recall context --nonce N [--limit N] [--json]` reports the
  calling subagent's current context fill (sum of
  `cache_creation_input_tokens` + `cache_read_input_tokens` from
  the most recent assistant turn in its JSONL — the same number
  Claude Code's status indicator reads). The recall daemon no
  longer calls it: `recall next` performs the same context-gate
  internally. It remains the introspection primitive the Luhmann
  orchestrator's `inspect-exit` builds on, and is available for
  diagnostics. Default output is the bare integer; `--json`
  returns `{tokens, found}`; with `--limit N` the command exits 1
  when tokens >= N, else 0 (suitable for shell-pipeline gating).
- `recall close FIRE --nonce N [-preserve-curation]` is the
  single cleanup verb. Writes `tmp://ARK-RECALL/result-<S>-<F>`
  iff items were added; removes the curation doc unless
  `-preserve-curation`; locates the calling subagent's JSONL via
  the `nonce → .meta.json` lookup, sums tokens, and appends one
  record to `~/.ark/monitoring/recall.jsonl`.
- `recall listen --session SID [--ambient]` is the consumer-side loop
  verb — the mirror of `recall next` for the user-facing assistant. On
  first call it idempotently subscribes **per capability**: always to
  `@ark-bloodhound-result=<SID>` (findings — level 3), and, with
  `--ambient`, also to `@ark-recall-result=<SID>` (ambient surfaces —
  level 4); the recall-result sub is the ambient opt-in the watcher keys
  on. Thereafter it blocks until a result doc is published, then returns
  the content(s) plus crank-handle prose telling the assistant to surface
  what helps the user and run `listen` again. No keepalive and no
  context-gate: the assistant runs it backgrounded and wakes only on a
  real result.
  This subscription is also what the daemon's subscriber-gate keys on
  — until a session calls `listen` (via the `/recall` skill) its
  curation docs pile up undispatched. See
  [simple-recall.md](simple-recall.md). Requires `ark serve`.
- `wait PATH` blocks until terminal. On `--timeout SEC` expiry,
  exits non-zero with the last-seen status on stderr.
- `show PATH` parses the persisted doc and projects fields. Without
  flags, prints a structured markdown summary. `--status` prints
  only the status. `--tags` lists tag-name proposals one per line.
  `--tag NAME` filters to rows whose `@proposal-value` equals NAME.
  `--threshold N` drops proposals below the score. `--json` emits
  the parsed projection. Distinct from `ark fetch PATH` which
  dumps the raw body.
- `list` lists in-flight requests (markdown table; `--json` for an
  array).

**Sidecar subcommands (turbo agent internal protocol, replace the
old `--wait` / `--fetch` / `--result` / `--error` flags):**

- `sidecar-wait` drains the turbo queue (lotto tube; JSON).
- `sidecar-fetch ID` returns chunk content (JSON array of
  `{chunkID, fileID, path, content}`).
- `sidecar-result ID` reads result JSON from stdin and posts it.
- `sidecar-error ID MESSAGE` posts an error (MESSAGE is positional,
  not `ID=MESSAGE`).

The removed flags print a one-line migration hint pointing at the
new subcommand name and exit with status 2.

### `discussed` — recall dedup state

```
ark discussed add    --session SID @tag[:value] [@tag[:value] ...]
ark discussed list   --session SID [--since DUR] [--json]
ark discussed clear  --session SID
ark discussed prune  [--ttl DUR]
```

Per-session "the conversation has already covered this" store
backing the recall substrate's `--session` flag. The recall
agent calls `discussed add` after emitting a batch of tag
suggestions; the substrate skips those tags on subsequent
recall calls for the same session. TTL applied lazily on read
(default 24h, configurable via `[recall].discussed_ttl` in
`ark.toml`); `prune` is the explicit sweep.

| Subcommand | Flags / args                                  | Behavior                                                                 |
|------------|-----------------------------------------------|--------------------------------------------------------------------------|
| `add`      | `--session SID @t[:v]...`                     | Write one RD record per tag, stamped with `NOW`                          |
| `list`     | `--session SID [--since DUR] [--json]`        | Range-scan, drop expired, print one per line (or JSON)                   |
| `clear`    | `--session SID`                               | Delete every RD record under one session                                 |
| `prune`    | `[--ttl DUR]`                                 | Sweep across all sessions; drop entries older than TTL                   |

Tag arguments follow ark tag syntax: bare `@name` matches any
value (substrate-side), `@name:value` matches the exact pair.
Server-proxy when available; cold-start via `withDB` otherwise.
Spec: [discussed-tags.md](discussed-tags.md).

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

See: `subscribe`

### `luhmann` — orchestrator supervisor log writer

```
ark luhmann spawn-record --class C --nonce N --task-id T
ark luhmann exit-record  --class C --nonce N --reason R [--crashes K] [--quit-early K] [--backoff S]
ark luhmann inspect-exit --nonce N [--json]
```

| Subcommand       | Flags                                              | Behavior                                                                                                                                                       |
|------------------|----------------------------------------------------|----------------------------------------------------------------------------------------------------------------------------------------------------------------|
| `spawn-record`   | `--class`, `--nonce`, `--task-id` (all required)   | Server-required. Append a `kind: "spawn"` record to `~/.ark/monitoring/luhmann.jsonl` via the write actor (carries both counters forward).                     |
| `exit-record`    | `--class`, `--nonce`, `--reason` (required); `--crashes` / `--quit-early` (counter overrides); `--backoff S` (records the seconds the supervisor will wait before respawn) | Server-required. Reason → kind: `context-limit`→`exit` (resets both counters), `quit-early`→`quit-early` (increments `quit_early`, holds `crashes`), else→`crash` (increments `crashes`, holds `quit_early`). |
| `inspect-exit`   | `--nonce`; `--json`                                | Cold-start. Classify a subagent exit as `healthy` / `quit-early` / `crash` / `unknown` via the nonce → `.meta.json` lookup. Default prints the label; `--json` adds details. |

See: `monitor`, `connections recall context`, [luhmann.md](luhmann.md)

### `message` — messaging operations

```
ark message new-request   --from P --to P (--issue TEXT | --issue-file PATH) [--content BODY | --content-file PATH] FILE
ark message new-response  --from P --to P --request ID [--content BODY | --content-file PATH] FILE
ark message set-tags      FILE TAG VAL [TAG VAL ...]
ark message get-tags      FILE [TAG ...]
ark message check         FILE
ark message inbox         [--project P] [--to P] [--from P] [--all] [--include-archived] [--counts] [--unmatched]
ark message dm            (--from S | --from-service NAME) --to R [--to R2 ...] [--subject TEXT] [--ref ID] --content TEXT
```

| Subcommand     | Flags                                                                | Behavior                                                                                                                                        |
|----------------|----------------------------------------------------------------------|-------------------------------------------------------------------------------------------------------------------------------------------------|
| `new-request`  | `--from`, `--to`, `--issue`\|`--issue-file`, `--content`\|`--content-file` (else stdin until lone `.`) | Create FILE (must not exist), set ark-request/from-project/to-project/status=open/status-date=today/issue tags. Issue from `--issue` or verbatim `--issue-file` (one required). Body from `--content`, verbatim `--content-file`, or stdin. `*-file` flags read verbatim (trailing newline trimmed) and are mutually exclusive with their inline twin. |
| `new-response` | `--from`, `--to`, `--request`, `--content`\|`--content-file` (else stdin)              | Create FILE, set ark-response=ID/status=accepted/status-date=today. Body from `--content`, verbatim `--content-file`, or stdin.                                                                             |
| `set-tags`     | none                                                                 | Alias for `tag set`                                                                                                                             |
| `get-tags`     | none                                                                 | Alias for `tag get`                                                                                                                             |
| `check`        | none                                                                 | Calls `tag check` with the standard message heading list                                                                                        |
| `inbox`        | see below                                                            | Server-first; pairs requests/responses by ID                                                                                                    |
| `dm`           | `--from` ⨯ `--from-service`, `--to` (repeatable), `--subject`, `--ref`, `--content` | Server-required; appends a tagged chunk to `tmp://<sender>/dm-<to0>` where sender is the `--from` session or the `--from-service` identity (e.g. `ARK-RECALL`). Emits `@dm: r1 r2: subject` per the multi-recipient/subject grammar. See [messaging.md](messaging.md). |

`message inbox` flags:

| Flag                 | Default | Meaning                                                     |
|----------------------|---------|-------------------------------------------------------------|
| `--project P`        | —       | Filter by EITHER `to-project` OR `from-project` (R2431)     |
| `--to P`             | —       | Filter by `to-project` (R2430; the old `--project` meaning) |
| `--from P`           | —       | Filter by `from-project`                                    |
| `--all`              | `false` | Include `completed`/`denied` messages in the display (they are always considered for pair lookup) |
| `--include-archived` | `false` | Include `@archived: true` messages                          |
| `--counts`           | `false` | Output `STATUS\tCOUNT` lines instead of rows                |
| `--unmatched`        | `false` | Show only requests with no matching response. Pair lookup is global — `--unmatched` combines correctly with `--to`/`--from`. |

Filters combine as intersection: `--from ark --to frictionless`
shows only the ark→frictionless slice.

Default inbox row is tab-separated:
`DATE STATUS TO FROM SUMMARY PATH LAG`. Lag format
`lag:PROJECT:STATUS` describes a stale `response-handled` /
`request-handled` bookmark relative to the counterpart's status.

Pair lookup (used by `--unmatched` and by the LAG column) consults
the **full inbox**, not the post-filter slice. A directional or
status filter changes what's *shown*; the matcher always sees both
sides of a pair. See R2484.

### `nano` — embedded shell-agent loop

```
ark nano [-m model] [-c | -s] [--base-url URL] [--max-steps N]
         [--approve-all] [--stream] [prompt...]
```

Vendored Go port of nano.py (see [`readme-nano.md`](../readme-nano.md)
for attribution and the MIT license). Talks to a local Ollama server
via `/api/chat`. With a prompt, runs one-shot and exits. Without a
prompt, drops into an interactive REPL backed by `chzyer/readline`.

Flags (positional order is free; flags must precede the prompt):

| Flag                | Required? | What it does                                                                            |
|---------------------|-----------|-----------------------------------------------------------------------------------------|
| `-m <model>`        | yes       | Ollama model name. No environment-variable fallback.                                    |
| `--base-url <url>`  | no        | Ollama server base URL. Default `http://localhost:11434`.                               |
| `--max-steps <N>`   | no        | Tool-call budget per task. Default 200. Non-integer arg exits with a parse error.       |
| `--approve-all`     | no        | Auto-approve every shell command. Equivalent to typing `a` at the first approval prompt. |
| `--stream`          | no        | Stream content tokens to stdout as Ollama emits them; suppresses the thinking spinner.  |
| `-c`                | no        | Continue the most recent session whose `cwd` matches the current directory.             |
| `-s`                | no        | Pick from up to ten recent sessions in this directory.                                  |
| `-h`, `--help`      | no        | Print usage and exit 0. Accepted at any position, including after `-m`.                 |

`-c` or `-s` with no matching sessions exits with `no sessions in
this directory`. Missing model exits with `model not set: pass -m
<model>`.

Sessions persist to `~/.ark/nano-sessions.json` (one entry per
turn). The schema and locking rules are documented in
[`specs/nano-sessions.md`](nano-sessions.md). The schema is
incompatible with both `~/.nano_sessions.json` (nano.py) and
`~/.nano-go_sessions.json` (standalone nano-go).

The model is given exactly one tool, `execute_shell`. Every command
is shown to the user with its 5–10-word description before running
unless `--approve-all` is in effect. Tool results are clipped to
the last 12 000 bytes. See [`specs/nano-tool-loop.md`](nano-tool-loop.md)
for the loop, approval, and execution details.

REPL controls:

- `:q`, `quit`, `exit` — end the session.
- `:reset`, `reset` — clear history and start over.
- Ctrl-D / EOF — exit cleanly.

No environment variables are read. The standalone nano-go reads
`OLLAMA_MODEL`, `OLLAMA_BASE_URL`, `NANO_MAX_STEPS`, and
`NANO_APPROVE`; ark drops those hooks in favor of explicit flags
(R2515, R2516, R2517, R2518 retired by T88–T91; flags landed as
R2511, R2561, R2562, R2563).

### `monitor` — inspect monitoring JSONL logs

```
ark monitor status [--json]
ark monitor recent [-n N] [CLASS] [--json]
ark monitor pause CLASS [--reason R]
ark monitor resume CLASS
```

| Subcommand        | Flags                  | Behavior                                                                                                                                                                                                                                |
|-------------------|------------------------|-----------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------|
| `status`          | `--json`               | Cold-start. Read the tail of `~/.ark/monitoring/<class>.jsonl` for every shipped class (`recall`, `luhmann`); report current state, latest timestamp, class-specific counters (incl. `crashes` / `quit_early`), and an `emergency` flag (🚨) when the class is in a storm pause. Default is a markdown table; `--json` emits objects. |
| `recent`          | `-n N`, `[CLASS]`, `--json` | Cold-start. Print the tail of one or all monitoring logs. Default `-n` is `20`. With `CLASS`, restrict to that file. Default output is markdown bullets; `--json` emits raw JSONL records.                                            |
| `pause CLASS`     | `--reason R`           | Server-required. Append `kind: "pause"` (with optional `reason`) to `<class>.jsonl` via the write actor. A storm reason (`crash-storm` / `quit-early-storm`) lights the `status` emergency flag. Exits non-zero if the class is already paused. |
| `resume CLASS`    | —                      | Server-required. Append `kind: "resume"`. Exits non-zero if already running.                                                                                                                                                            |

See: [monitor.md](monitor.md), [luhmann.md](luhmann.md),
[simple-recall.md](simple-recall.md)

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
ark schedule upcoming TAG [--all] [--json]
ark schedule logs TAG [SOURCE] [-n N] [--json]
ark schedule suppress TAG
ark schedule unsuppress TAG
```

| Subcommand | Flags | Behavior |
|------------|-------|----------|
| `search DATE` | `--tag` (filter), `--gaps` (only past unacked), `--json` (raw response) | Server-required. DATE is a single date or `START..END` range; `dateparse` handles flexible formats (see parse-robustness note below). Default output is markdown grouped by date; each bullet collapses `START–END` when start==end and drops the trailing `: ` when there's no summary (so chime ticks don't render as `- 13:45–13:45:`). |
| `change PATH TAG NEWSTART [NEWEND]` | `--dry-run` | Server-required. Rewrites date in schedule tag value, preserves trailing description. Re-indexes after write. Updates `@ark-event-upcoming:` for recurring events. |
| `tags` | `--values` | Cold-start. Lists configured schedule tags from ark.toml. With `--values`, also reads schedule logs (`~/.ark/schedule/`) for next upcoming dates. |
| `parse DATE` | — | Cold-start (no DB). Parses a date expression, prints start/end/all-day/text. Recognizes recurring specs and computes next occurrence. |
| `upcoming TAG` | `--all`, `--json` | Server-required. Print the next fire(s) from the in-memory priority queue for TAG. `--all` lists upcoming events across all tags; `--json` for raw output. |
| `logs TAG [SOURCE]` | `-n N` (default 50), `--json` | Server-required. Print the audit log for TAG. Without SOURCE, lists every source file with a chunk for TAG; with SOURCE, shows that chunk's spec history plus the most recent N fired entries. |
| `suppress TAG` | — | Server-required. Stop TAG from firing without removing its `[schedule.tag.TAG]` declaration (sets `suppress = true`). Tag must already be declared. |
| `unsuppress TAG` | — | Server-required. Resume firing for a suppressed TAG. Tag must already be declared. |

Parse-robustness note (applies to every path that parses a schedule
tag value — `search`, `change`, `parse`, and the source-file scan):
the dash-joined form `YYYY-MM-DD-HH:MM` is normalized to its `T` form
before parsing (`dateparse` otherwise reads the time as a timezone
offset and returns midnight); a value that parses to a date with a
timezone but no time-of-day, or an ambiguous mm/dd vs dd/mm value, is
rejected with an error rather than silently mis-parsed. See
`specs/scheduling.md`.

### `search` — search the index

```
ark search [TERM...] [filter-stack] [options]
ark search expand [SUBCOMMAND...]
```

Filter-stack flags (parsed before `flag.Parse`):

| Flag             | Meaning                                                                                           |
|------------------|---------------------------------------------------------------------------------------------------|
| `-contains TERM` | Substring match (default for bare terms)                                                          |
| `-fuzzy TERM`    | Typo-tolerant match                                                                               |
| `-regex PATTERN` | RE2 match                                                                                         |
| `-tag TAG`       | Tag filter, sigil syntax `[~|:]NAME[(=|:|~)VALUE]` (see [file-tag-filter.md](file-tag-filter.md)) |
| `-file-tag TAG`  | File-tag filter — accepts every chunk on a file with the tag (same sigil syntax)                  |
| `-about QUERY`   | Vector similarity (server required for embedding model)                                           |
| `-files GLOB`    | Path glob filter                                                                                  |
| `-with`          | Subsequent filters intersect (default polarity)                                                   |
| `-without`       | Subsequent filters subtract                                                                       |
| `--filter-k N`   | After an `-about` entry, override per-row top-K                                                   |
| `-parse`         | Print disambiguated command and exit without searching                                            |

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
| `-tags`            | `false` | Output extracted @tag activity as markdown bullets. See [tags-baby-food.md](tags-baby-food.md). Each `-with -tag NAME[:VALUE]` suppresses its own subtree from the output (the agent already knows what it filtered for) |
| `-no-values`       | `false` | With `-tags`: collapse the value layer (tag → files/chunks). Orthogonal to the file/chunk axis     |
| `-no-chunks`       | `false` | With `-tags`: drop `:range` from locations (file paths only); duplicate files dedup under one value |
| `-no-files`        | `false` | With `-tags`: drop file/chunk locations entirely; only tag/value counts remain. Subsumes `-no-chunks` |
| `-json`            | `false` | With `-tags`: emit `TagResult` JSONL (one object per line) instead of markdown bullets. Suppression flags do NOT apply — JSON is the raw structured view |
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

### `subscribe` — manage tag subscriptions for `listen`

```
ark subscribe --session ID [options]
```

| Flag                   | Default                      | Meaning                                                                                |
|------------------------|------------------------------|----------------------------------------------------------------------------------------|
| `--session ID`         | required for register/cancel | Session ID                                                                             |
| `--cancel`             | `false`                      | Cancel matching subscription(s); pair with `--tag` to scope                            |
| `--list`               | `false`                      | List active subscriptions (`session\tkind\tsigil\thits\tdrops`)                        |
| `--stats`              | `false`                      | Per-session totals                                                                     |
| `--tag T`              | —                            | Sigil-form match `[~|:]NAME[(=|:|~)VALUE]`. Leading `@` stripped. Repeatable           |
| `--file-tag T`         | —                            | Same syntax; matches every chunk on a file that has the tag. Repeatable                |
| `--filter-files GLOB`  | —                            | Repeatable positive path filter                                                        |
| `--exclude-files GLOB` | —                            | Repeatable negative path filter                                                        |

Match syntax is the same as `ark search -tag`. Name-side sigils:
bare = exact, `:` prefix = contains (substring-AND), `~` prefix =
regex (RE2). Value-side separators: `=V` exact, `:V` contains,
`~V` regex. Each `--tag` and `--file-tag` becomes its own
subscription entry; entries OR together at delivery time. The old
`--value` flag is removed — its work is absorbed by the value
sigil (`T=V`, `T:V`, `T~V`).

Server-required. Without `--list`/`--stats`/`--cancel`, a register
call requires `--session` and at least one `--tag` or `--file-tag`.

See: `listen`, `subscribers`

### `subscribers` — count subscriptions matching a tag

```
ark subscribers --tag T [--quiet]
```

| Flag      | Default | Meaning                                                                                                                            |
|-----------|---------|------------------------------------------------------------------------------------------------------------------------------------|
| `--tag T` | required | Sigil-form match `[~|:]NAME[(=|:|~)VALUE]` (same grammar as `subscribe --tag`). Leading `@` is stripped.                          |
| `--quiet` | `false`  | Suppress stdout; exit code carries the answer (`0` if at least one subscriber, `1` if zero). Designed for shell-pipeline gating. |

Server-required. Returns the count of currently-registered
subscriptions whose predicate would accept the named tag (and
optional value) if it were published right now. File filters on
subscriptions are intentionally ignored — the question is "could
anyone receive this?", not "would this specific file pass each
subscriber's filter?". See [subscriber-presence.md](subscriber-presence.md).

### `sweep` — corpus-wide sweeps

```
ark sweep correlations
```

Server-required. A namespace for periodic corpus-wide passes; `correlations`
is the only subcommand today (future phases may add others, e.g.
chunk-pairwise). `sweep correlations` refreshes the hot-correlations top-K
cache per tag: it reads the `I:hcsweep` bookmark, walks the S substrate for
ED/EC records changed since the bookmark, fully recomputes top-K for tags
whose definitions moved, and displaces individual changed chunks against
unchanged tags. Per-tag write transactions; the bookmark advances only on
full success. Progress publishes through `tmp://sweep/hot-correlations.md`
(throttled 250ms, terminal status flushed immediately;
`@tag: sweep-status`). See [hot-correlations.md](hot-correlations.md).

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

Bare TAG arguments to `counts`, `files`, `values`, `defs`, `set`, and
`get` are normalized: a single leading `@` and a single trailing `:`
are stripped before use, so `@status:` and `status` resolve to the
same tag. This catches the common copy-paste error where the rendered
form leaks into the command line. Matches the `-tag` sigil
normalization (see "Filter stack").

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
