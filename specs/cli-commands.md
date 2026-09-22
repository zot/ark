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
**A verb that acts as the calling session takes no identity flag at all.**
`bloodhound listen`, `connections recall listen`, `luhmann next`, `luhmann
events`, and `connections recall next` **declare** a session need on their
command node; the CLI resolves it from the global `--force-session` when given,
otherwise from `$CLAUDE_CODE_SESSION_ID`, and **errors naming `--force-session`
when neither resolves**. A skill's rendered `sessionid=` can be stale after a
session is re-hosted (`--fork-session --resume`), and a stale id there fails
silently rather than loudly, so the value is resolved where the command runs
rather than typed. `connections recall next --tube` is **not** an identity — it
is a routing address (the pool composite `<luhmann-session>-<nonce>`), which is
why it is no longer spelled `--session`. It is also **optional**: omitted, the
server derives the address from the identity and nonce the call already carries,
so no agent types a routing address either. Canonical:
[session-identity.md](session-identity.md).

| `bloodhound`       | `bloodhound SUBCOMMAND ...`                                                                                          | required                 | `search`/`add` need a Luhmann; `listen [--recall]` is the consumer loop and needs only the server |
| `bundle`           | `bundle -o OUT [-src SRC] DIR`                                                                                       | n/a                      | build-time                                           |
| `cat`              | `cat FILE`                                                                                                           | n/a                      | bundled binary only; alias of `bundle cat`           |
| `chats`            | `chats PATH-OR-GLOB... [--with-tools] [--thinking] [--all] [--sidechain] [--wrap N] [--line-length N]`               | none                     | existing files direct; globs walk `~/.claude/projects/` |
| `chats list`       | `chats list [--source NAME] [--for NAME] [--limit N] [--json]`                                                       | optional                 | indexed conversations, newest first, labelled by first user turn |
| `chunks`           | `chunks CHUNKID [-before N] [-after N] [-wrap N]` <br> `chunks PATH:RANGE [-before N] [-after N] [-wrap N]` <br> `chunks PATH:RANGE:"SNIPPET" [-wrap N]` <br> `chunks PATH RANGE [-before N] [-after N] [-wrap N]` <br> `chunks -status [PATTERN...]` | optional                 | `CHUNKID` resolves via `db.ChunkInfo`; `PATH:RANGE` accepts `NN` and `NN-MM` range labels; `PATH:RANGE:"SNIPPET"` is the recall chat sub-chunk locator (R2914) — returns the matched paragraph; drop the snippet for the whole turn |
| `chunk-chat-jsonl` | `chunk-chat-jsonl FILE`                                                                                              | n/a                      | internal chunker (microfts2 protocol)                |
| `config`           | `config [SUBCOMMAND ...]`                                                                                            | optional                 | subcommands below                                    |
| `connections`      | `connections SUBCOMMAND ...`                                                                                          | required                 | substrate + sidecar CLI (subcommands below)          |
| `cp`               | `cp PATTERN DEST-DIR`                                                                                                | n/a                      | bundled binary only; alias of `bundle cp`            |
| `discussed`        | `discussed SUBCOMMAND ...`                                                                                            | optional                 | per-session recall dedup state (subcommands below)   |
| `dismiss`          | `dismiss PATTERN...`                                                                                                 | optional                 | drops M records                                      |
| `embed`            | `embed SUBCOMMAND ...`                                                                                               | none                     | subcommands below                                    |
| `fetch`            | `fetch [--wrap N] PATH...`                                                                                           | optional                 | reads file content from index; proxies to server when up; tmp:// requires server |
| `files`            | `files [--status] [--detail] [-with\|-without] [-files G]... [PATTERN...]`                                    | optional                 |                                                      |
| `grams`            | `grams QUERY...`                                                                                                     | optional                 | shows trigram index for query; proxies to server when up, else local |
| `init`             | `init [--embed-cmd C] [--query-cmd C] [--case-insensitive] [--aliases A] [--no-setup] [--if-needed]`                 | none                     |                                                      |
| `install`          | (no flags)                                                                                                           | none                     | alias of `ui install`                                |
| `listen`           | `listen --session ID [--timeout N]`                                                                                  | required                 | long-poll; outputs markdown                          |
| `ls`               | `ls`                                                                                                                 | n/a                      | bundled binary only; alias of `bundle ls`            |
| `luhmann`          | `luhmann SUBCOMMAND ...`                                                                                             | mixed                    | orchestrator supervisor + hosted-session lifecycle (subcommands below) |
| `monitor`          | `monitor SUBCOMMAND ...`                                                                                             | mixed                    | inspect `~/.ark/monitoring/*.jsonl` (subcommands below) |
| `message`          | `message SUBCOMMAND ...`                                                                                             | mixed                    | subcommands below                                    |
| `missing`          | `missing [PATTERN...]`                                                                                               | optional                 |                                                      |
| `nano`             | `nano [-m model] [-c \| -s] [--base-url URL] [--max-steps N] [--approve-all] [--stream] [prompt...]`                 | none                     | embedded shell-agent loop (Ollama-backed)            |
| `patterns`         | `patterns list [--json]` <br> `patterns show SLUG` <br> `patterns add SLUG --name N --summary S [--tags T] [--body-file F]` | none                     | personal pattern collection; never uses the server   |
| `rebuild`          | `rebuild`                                                                                                            | refused                  | drops and rebuilds index; refuses if server up; read-only serve during scan |
| `refresh`          | `refresh [PATTERN...]`                                                                                               | optional                 | re-index stale files                                 |
| `remove`           | `remove PATTERN...`                                                                                                  | optional                 | tmp:// requires server                               |
| `resolve`          | `resolve PATTERN...`                                                                                                 | optional                 | dismisses unresolved files                           |
| `scan`             | `scan`                                                                                                               | optional                 | walks sources for new files                          |
| `schedule`         | `schedule SUBCOMMAND ...`                                                                                            | required (search/change) | subcommands below                                    |
| `search`           | `search [TERM...] [filter-stack] [options]` <br> `search expand ...`                                                 | preferred                | server proxy keeps caches warm                       |
| `serve`            | `serve [--no-scan] [--force] [--compact]`                                                                            | starts                   |                                                      |
| `setup`            | `setup`                                                                                                              | none                     | bootstrap `~/.ark/` (extract bundle, install skills) |
| `sources`          | `sources [check | pair]`                                                                                             | optional                 | `check` reconciles globs; `pair` proposes `logs_for` |
| `stale`            | `stale [PATTERN...]`                                                                                                 | optional                 |                                                      |
| `status`           | `status [--db] [--chunks] [--tokenize] [-with\|-without] [-files G]...`                                       | preferred                |                                                      |
| `stop`             | `stop [-f]`                                                                                                          | required                 | graceful shutdown over the socket (`POST /shutdown`); `-f` force-kills |
| `subscribe`        | `subscribe --session ID [--tag T]... [--file-tag T]... [--cancel] [--list] [--stats] [-with\|-without] [-files G]...` | required                 |                                                      |
| `subscribers`      | `subscribers --tag T [--quiet]`                                                                                      | required                 | count subscriptions matching a tag                   |
| `sweep`            | `sweep correlations`                                                                                                | required                 | hot-correlations top-K cache refresh (subcommands below) |
| `tag`              | `tag SUBCOMMAND ...`                                                                                                 | mixed                    | subcommands below                                    |
| `ui`               | `ui [SUBCOMMAND ...]`                                                                                                | required (most)          | 16 subcommands                                       |
| `unresolved`       | `unresolved [--reason KIND]`                                                                                         | optional                 | lists U records with why; `--reason` narrows to one  |
| `version`          | `version`                                                                                                           | none                     | prints `ark <version>` (from README.md)              |

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
| `--force-session ID`         | unset                                    | Act as session `ID` instead of `$CLAUDE_CODE_SESSION_ID`. Consulted **only** by verbs that declare a session need (see [session-identity.md](session-identity.md)); deliberately not copied into a package global, so a verb that does not declare the need cannot consume it |
| `--links`                    | unset                                    | Expand each result to **every** address of that file, canonical included ([symlinks.md](symlinks.md)). Consulted **only** by nodes that emit paths as addresses, declared node-by-node the way `--force-session` is; nodes that emit content — `search` above all — never consult it |
| `--nolinks`                  | unset                                    | Match canonical addresses only, as if no symlink were indexed; the way a script gets a file set that does not change when someone creates a link. Giving both `--links` and `--nolinks` is an **error**, not a preference to resolve: they are opposite ends of one axis |
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
and the command falls back to opening the index directly
via `withDB`.

Server-first commands proxy as a transparent optimization: the server
keeps caches warm (file-name maps, index pages, embedding model). Cold-
start fallbacks rebuild caches per invocation.

### Cold-start (`withDB`)

`withDB(fn)` opens the index (`ark.Open(arkDir)`), runs
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

### Proxy/local dispatch

The index is single-process (bbolt file lock), so a DB-touching
command can't open it directly while `ark serve` holds it. Such
commands dispatch through the central `proxyOrLocal` helper: proxy
to the server when its socket answers, else open the index locally
with a bounded lock wait. Dispatch is stubborn — a server bounce is
a wait-and-retry, not an error — and rechecks server liveness if a
local open hits lock contention. Full behavior: specs/cli-dispatch.md.

Commands with no server endpoint that need exclusive local access
(`embed text`, `embed bench`, `embed validate`, `config recover`,
`status --chunks --tokenize`) fail fast with a "stop the server"
message when a server is running, rather than hanging. Server-backed
read commands proxy to: `/fetch` (fetch), `/chunks` (chunks),
`/grams` (grams), `/schedule/tags` (schedule tags), `/tags/values`
(tag values, `files` flag for the file-resolved variant),
`/schedule/search` (schedule search), `/status/chunks`
(status `--chunks`).

A command that proxied successfully does **not** then open the index
for the remainder of its output — the server still holds the lock, so
that second open cannot succeed. Whatever is left comes from the
server too, or the command declares itself server-must-be-stopped.

### `reorderArgs`

Go's `flag` package stops at the first non-flag argument. Commands
that mix flags with positional arguments preprocess `args` through
`reorderArgs` to move flags to the front. Affected: `chunks`,
`config add-include`, `config add-exclude`, `config remove-pattern`,
`schedule search`, `schedule change`, `chats`.

### Filter stack

The filter stack is parsed before `flag.Parse` via `parseFilterStack`.
Mode flags consume the next argument as the query; polarity flags
(`-with`, `-without`) toggle the polarity of subsequent entries, with
`-with` the default.

`search` uses the **full** mode set (`-contains`, `-fuzzy`, `-regex`,
`-tag`, `-about`, `-files`). `--filter-k N` after an `-about` entry
overrides the per-row top-K. `-parse` prints the disambiguated command
and exits without searching. Bare terms coalesce into a single
`-contains` group. The first entry becomes the primary search; the rest
become chunk-level post-filters. See `search-cli-filters.md` for
examples and rationale.

`files`, `status`, `tag files`, `tag values`, and `subscribe` use the
**`-files`-only** subset: `-files GLOB` rows with `-with` / `-without`
polarity, no query modes. This is what makes a path glob mean the same
thing on every command (see below).

### Path filters (`-files`)

`status`, `files`, `tag files`, `tag values`, `subscribe`, and `search` all
take path globs through the same `-with` / `-without` + `-files` filter stack.
`-with` is the default polarity; `-without -files GLOB` subtracts. Without any
positive `-files` row, all paths are candidates.

**One mechanism, one meaning.** Globs are anchored to the current directory
CLI-side before the request is sent, so `*.md` means what it means in a shell
(top level only) and `/**/*.md` means "any directory". The full vocabulary —
the three anchoring forms and the three contexts — is in
[main.md](main.md#glob-patterns); this table does not restate it.

**Positional globs are the same mechanism.** `files`, `stale`, and `missing`
narrow their output by trailing positional globs (`ark files '*.md'`). Those
are anchored exactly like a `-files` row, so a command cannot carry two glob
rules at once: `ark files '*.md'` and `ark files -files '*.md'` mean the same
thing. A positional glob is a *shorthand* for a positive row, not a second
surface.

Commands that *hand their patterns onward* rather than filtering their own
output — `refresh`, `resolve`, `remove`, `dismiss` — are matched where the
work happens, with no client working directory in play, so a bare glob there
keeps the rootless "any depth" reading.

`bloodhound search --filter-files` keeps its flag *name* (it is two repeatable
flags on a non-search command, not a filter stack) but has always carried these
same semantics, because a hunt's globs also reach the secretary's own `ark
search` calls and the two halves must agree ([bloodhound.md](bloodhound.md),
"Globs mean what `ark search -files` means").

> **Removed:** `--filter-files` / `--exclude-files` on `status`, `files`,
> `tag files`, `tag values`, and `subscribe`. They exit non-zero with an error
> naming `-files` **and** the semantic change, because the one pattern shape
> that changed meaning (a bare no-slash glob is now top-level-only) would
> otherwise fail *silently* by returning fewer results. An alias would have
> reproduced exactly the invisible failure this unification removed.

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

### `bloodhound` — external directed hunts

External-app access to the warm bloodhound ([bloodhound-cli.md](bloodhound-cli.md)).
`search` and `add` require `ark serve` **and** a running Luhmann orchestrator;
`listen` needs only `ark serve`.

```
ark bloodhound search [CLUE...] [--file PATH|-] [--scope S] [--depth D] [--want W] [--filter-files G] [--exclude-files G] [--wait] [--timeout SECONDS] [--raw] [--markdown]
ark bloodhound add --result tmp://BLOODHOUND-CLI/<id> --loc PATH:RANGE --note NOTE [--chunk TEXT]
ark bloodhound add --result tmp://BLOODHOUND-CLI/<id> --done
ark bloodhound listen [--session SID] [--recall]
```

**`search`** — submit a directed hunt and print the findings (default: curated
JSONL, one object per line; an empty hunt prints nothing and exits 0). The whole
protocol (request doc, tag baton, pool secretary, Luhmann curation) is hidden
behind the one command (Batteries Included).

| Flag           | Default      | Meaning                                                                 |
|----------------|--------------|-------------------------------------------------------------------------|
| `--file PATH`  | —            | read the clue from a file (markdown); `--file -` reads stdin (heredoc) for a multi-paragraph clue. Mutually exclusive with positional `CLUE...` |
| `--scope S`    | `all`        | search scope: `code` \| `specs` \| `design` \| `notes` \| `chat` \| `all` |
| `--depth D`    | `lookup`     | `lookup` (one pass) \| `investigate` (tune until the stop condition)     |
| `--want W`     | `passages`   | `answer` \| `passages` \| …                                             |
| `--filter-files GLOB`  | — | Repeatable positive path filter scoping the hunt. Two repeatable flags rather than a filter stack, but the glob semantics are the project-wide ones (see [main.md](main.md#glob-patterns)): unanchored globs are anchored CLI-side to the client's cwd |
| `--exclude-files GLOB` | — | Repeatable negative path filter, applied after the positive set (`search -without -files`)                                     |
| `--wait`       | `false`      | block stubbornly on a busy pool / server bounce instead of failing fast |
| `--timeout N`  | `300`        | seconds to wait for the result                                          |
| `--raw`        | `false`      | skip Luhmann curation: return the secretary's own findings (markdown, Baby Food for agents) — you curate in your own context |
| `--markdown`   | `false`      | render the curated findings as a markdown locator list instead of JSONL (client-side; redundant with `--raw`) |

**`add`** — Luhmann's result stencil (one curated item per call), plus the
terminal `--done` that writes the result doc and wakes the waiting CLI. Not for
external callers; the Luhmann orchestrator runs it while draining `luhmann next`.

| Flag             | Default | Meaning                                                            |
|------------------|---------|--------------------------------------------------------------------|
| `--result PATH`  | —       | the request-doc path handed to `luhmann next` (`tmp://BLOODHOUND-CLI/<id>`) |
| `--loc PATH:RANGE` | —     | the curated finding's locator (required unless `--done`)           |
| `--note TEXT`    | —       | one-line curated note: why it answers the query                    |
| `--chunk TEXT`   | —       | optional chunk excerpt carried in the JSONL                        |
| `--done`         | `false` | terminal call: write the result doc and notify the waiting CLI     |

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
ark chats PATH-OR-GLOB... [options]
```

| Flag              | Default | Meaning                                  |
|-------------------|---------|------------------------------------------|
| `--with-tools`    | `false` | Render tool calls and results            |
| `--thinking`      | `false` | Render chain-of-thought (thinking) blocks as `✻ …` |
| `--all`           | `false` | Complete transcript: `--with-tools` + `--thinking` + `--sidechain` |
| `--sidechain`     | `false` | Include subagent traffic                 |
| `--wrap NAME`     | —       | Wrap entire output in `<NAME>...</NAME>` |
| `--line-length N` | `100`   | Word-wrap column                         |

Takes one or more positional arguments. An argument naming an
existing file is rendered directly (indexed or not, anywhere on
disk); otherwise it is a glob matched against filenames (not full
paths) discovered by walking `~/.claude/projects/`. User turns
prefix `❯`, assistant turns prefix `●`. Arguments matching nothing
warn on stderr; exits 1 when no argument yields any file.

#### `chats list` — what conversations exist

```
ark chats list [--source NAME] [--for NAME] [--limit N] [--json]
```

| Flag            | Default | Meaning                                                   |
|-----------------|---------|-----------------------------------------------------------|
| `--source NAME` | —       | Restrict to one chat source (name or full directory)      |
| `--for NAME`    | —       | Restrict to chat sources whose `logs_for` names this source |
| `--limit N`     | `20`    | Maximum rows, newest first                                |
| `--json`        | `false` | One JSON object per line                                  |

Answers the question that precedes rendering a transcript. Each row
carries the session's **label** — its first user turn, flattened to
one line and truncated — plus modified time, turn count, and the path
`ark chats` takes, so the two verbs compose. Sidechain records count
toward neither the label nor the turn count.

Sessions come from the index, not a directory walk: a JSONL on disk
but unindexed does not appear, and `ark chats PATH` still renders it.
`--source` and `--for` are mutually exclusive. A `NAME` matching more
than one source is an error naming every candidate; the full
directory disambiguates. Server-proxy if available.

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
| `-anchor`    | `false` | Switch to anchor mode: print the opinionated `@ext` target (address) |

Default mode emits JSONL (one chunk per line, fields: path, range,
score, content). Status mode is right-aligned text.

Anchor mode (`-anchor`) prints a single line — the opinionated, durable
`@ext` TARGET string for the chunk (`%uuid`, `/path:1-5`,
`/path:"unique snippet"`, or `/path:/regex/`), reusing `SuggestExtLocator`'s
`@id`→unique-string→range heuristic. It's the generic chunk-addressing
counterpart to `mcp.suggestExtLocator`, and its output feeds straight into
`ark ext {candidate,set,add} <target> …`. Proxies to `POST /chunks/anchor`
when the server is up.

> **Consistency debt (O-gap):** `-status` and `-anchor` are output *modes*
> selected by flags rather than subcommands, because `chunks` is a **leaf**
> command taking positional args (`CHUNKID` / `PATH RANGE`) — a subcommand
> token would be ambiguous with a positional. See the `ark chunks` mode-flag
> gap in `design.md`.

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
| `recover`                               | rewrite ark.toml from the index I-record snapshot     |

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
ark connections recall reserve-nonce [--luhmann] [--class C]
ark connections recall next [--tube ADDR] NONCE
ark connections recall surface FIRE -loc PATH:RANGE -reason TEXT
ark connections recall recommend FIRE -loc PATH:RANGE -tag @t[:v] -reason TEXT
ark connections recall finding COOKIE (-loc PATH:RANGE [-note TEXT] | -answer TEXT)
ark connections recall close FIRE --nonce N [-preserve-curation]
ark connections recall context --nonce N [--limit N] [--json]
ark connections recall listen [--session SID] [--ambient]      (older spelling of `ark bloodhound listen`)
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
  TTL semantics. `--propose` runs the compute-for-display
  derivation pass on the substrate's full scored chunk set and
  adds a `@chunk-proposed-tags` line to each surfaced chunk with
  this call's computed proposals (similarity-desc order); it
  persists nothing — the calling agent is the sole author of
  durable `@ext-candidate`s, via `ark ext candidate`. See
  [derived-tags.md](derived-tags.md). The simple-recall watcher
  built on top of this substrate is described in
  [simple-recall.md](simple-recall.md).
- `recall reserve-nonce [--luhmann] [--class C]` returns the next monotonic integer the
  Luhmann orchestrator uses as the recall daemon's per-generation
  nonce (the `nonce → .meta.json` discovery key). The counter is
  in-memory and resets on `ark serve` restart. With `--luhmann` the server
  also registers the nonce as a pool secretary in the watcher's roster for
  the class named by **`--class`** (`bloodhound`, the default, or `recall`) —
  the reservation doubles as pool registration. Classes hold separate
  rosters and separate `pool_max` budgets, so the class decides which
  workload the secretary will be sent.

  **Every reservation also captures the caller's session**, with or
  without `--luhmann`. The CLI reads `$CLAUDE_CODE_SESSION_ID` from its
  own environment — it runs in the reserving agent's shell — and sends
  it, so the server can later resolve that nonce's subagent transcript
  (`simple-recall.md` §Subagent JSONL discovery). There is no flag for
  it: the value is always present where this command runs and never
  present where the lookup runs, so a flag would only add a way to
  forget. Run outside an agent shell there is no session, nothing is
  registered, and later lookups report *unmeasured* rather than zero.
  See [secretary-pools.md](secretary-pools.md),
  [bloodhound-cli.md](bloodhound-cli.md), and
  [simple-recall.md](simple-recall.md).
- `recall next [--tube ADDR] NONCE` is the recall secretary's entire
  loop — a batteries-included crank handle. `--tube` is an **override**;
  omitted, the server resolves the address from the caller's identity and
  NONCE (`<client-session>-<nonce>` when that nonce sits in a pool roster,
  the bare `<client-session>` otherwise). With `--tube ADDR` it
  subscribes **value-scoped** `@ark-recall-curate=<ADDR>` and dispatches the
  curation docs **addressed** to it — each doc's `@ark-secretary-work` head
  tag, which the watcher wrote when it chose a server. For a session's own
  secretary that address is its session; for a `recall`-class pool secretary
  it is the composite `<luhmann-session>-<nonce>`, so the same call serves
  both roles and there is no role for the caller to declare. The
  conversation prepended is always the **doc's origin** session's (the
  session in its path), not the caller's — a pool secretary's composite has
  no conversation, and handing the curator none would be worst exactly when
  judging fit matters most. Without `--tube`, the
  address is derived server-side, so there is no unaddressed path: the
  bare-curate, all-session scan is **retired** (it dispatched docs addressed
  to other secretaries, and only ever made a misconfigured setup look idle).
  On first call it idempotently subscribes — subscription session
  `recall-curate-<ADDR>`, keyed on the durable address so a restart can't
  recycle it (R2902); thereafter
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
- `recall listen [--ambient]` is the consumer-side loop
  verb — the mirror of `recall next` for the user-facing assistant, and
  the older spelling of **`ark bloodhound listen [--recall]`**, where the
  concept lives. Both declare a session need, so neither takes an identity
  flag. One implementation serves both,
  so the spellings cannot drift; `--recall` and `--ambient` are the same
  flag. On
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
ark embed install [--force] [--backend B] [--version bNNNN]
```

The `text`/`bench`/`validate` subcommands run via `withDB` and require
`[embedding] model` configured in ark.toml; `install` provisions the
llama.cpp shared libs and needs only the `[embedding]` config.

| Subcommand     | Flags                                                | Behavior                                                           |
|----------------|------------------------------------------------------|--------------------------------------------------------------------|
| `text`         | —                                                    | Embed the joined text and print the vector as JSON                 |
| `bench tags`   | `--ctx N` (default 2048), `--parallel N` (default 8) | Embed every (tag,value) compound, compare batch vs single          |
| `bench chunks` | as above                                             | Sample 200 chunks via file-first sampling, compare batch vs single |
| `validate`     | `--fix`, `-v`                                        | Cross-check EC/EF records against FTS chunks                       |
| `install`      | `--force`, `--backend B`, `--version bNNNN`          | Provision the llama.cpp libs into the lib dir (R2969); `--force` re-downloads, flags override `[embedding]` backend/llama_version |

`embed validate` exits 1 if any problem is found (orphan EC, missing
EC, orphan EF, dimension inconsistency); `--fix` deletes orphan EC,
orphan EF, and wrong-dimension EC records.

### `ext` — author and stage `@ext` routings

```
ark ext set       <target> <tag> <value>
ark ext add       <target> <tag> <value>
ark ext remove    <target> <tag> [value] [--disposition internal|external] [--internal]
ark ext candidate <target> <tag> [value] [--insight "why"] [--disposition internal|external] [--internal] [--replace]
ark ext accept    <target> <tag> [value]
ark ext reject    <target> <tag> [value]
ark ext amend     <tvid> <tag> [value] [--insight "why"] [--disposition internal|external] [--internal] [--replace] [--target TARGET]
ark ext pending   [--chunk <target>] [--limit N] [--json]
```

Author `@ext` routings into the target's **mirror file**
(`~/.ark/external/<slug>/<target-path>.md`, or
`<source>/<ext_mirror>/<target-path>.md` when the target's source sets
`ext_mirror` — see `specs/curation-workshop-primitives.md`) — the CLI
counterpart to the Frictionless workshop's `mcp.setExtTag`/`mcp.removeExtTag`,
so a plain-session assistant can apply an `@ext` proposal (e.g. a
recall recommend) without the UI. Every verb acts only on the
mirror file; they never scan or edit hand-authored `@ext` lines
elsewhere in the corpus. The `candidate`/`accept`/`reject`/`amend` verbs
stage a routing through its proposed → committed → judged lifecycle (see
`specs/curation-workshop-primitives.md` "Staging ledger"), and `pending`
reads that ledger back. `<target>` is a TARGET string per
`specs/at-ext-parsing.md` (absolute paths for read-only chunks).
The mutating verbs proxy through the running server (mutation runs
in the DB actor) or open the index exclusively when stopped —
mirrors the `config` add/remove pattern. `pending` is a read, so it
proxies to `GET /ext/pending` when a server is up and opens the index
locally when none is. `pending`'s `--chunk` TARGET is resolved wherever
the index lives — server-side over the wire, locally otherwise — and a
TARGET matching no chunk is an error rather than a silent widening to
the whole corpus.

| Subcommand | Server | Behavior |
|------------|--------|----------|
| `set <target> <tag> <value>` | proxy-or-exclusive | Collapse every `(target, tag)` value in the mirror file to the single `<value>`; append when none exist |
| `add <target> <tag> <value>` | proxy-or-exclusive | Append a `(target, tag, value)` routing (multiple values per tag allowed); an exact duplicate is a silent no-op |
| `remove <target> <tag> [value] [--disposition internal\|external] [--internal]` | proxy-or-exclusive | External (default): remove `(target, tag)` mirror routings — all values, or only the matching `<value>` when given; missing file/line is a silent no-op. Internal: delete the inline `@tag:` line from the target file's own body via the chunker's removal stencil — **no degrade**, errors when it cannot apply (see `internal-disposition.md` "Remove") |
| `candidate <target> <tag> [value] [--insight "why"] [--disposition internal\|external] [--internal] [--replace]` | proxy-or-exclusive | Author an `@ext-candidate` (proposed routing) in the mirror file, stamped with today's first-seen date; optional quoted `--insight` rationale, `--disposition internal\|external` / `--internal` shorthand (default external), and `--replace` (accept collapses the target's values of the tag instead of the default add). Both disposition and `--replace` are part of the line identity — a differing disposition or a differing add-vs-replace is a distinct proposal; an exact-identity repeat increments the line's `@count` |
| `accept <target> <tag> [value]` | proxy-or-exclusive | Commit matching `@ext-candidate`(s), each per its disposition **and** `replace` token → four cells: external-add appends the `@ext` mirror edge, external-replace collapses the mirror's values (`ext set` semantics), internal-add inserts an inline tag, internal-replace rewrites the existing inline tag (fallback to external when the type can't host it or the file isn't writable — see `internal-disposition.md`). Consumes the candidate(s) (leading date, disposition, `replace`, `insight` dropped) and writes a positive `@ext-judgment @count:+1` |
| `reject <target> <tag> [value]` | proxy-or-exclusive | Rewrite matching `@ext-candidate` line(s) → a single dated `@ext-judgment: <date> TARGET @tag: @count: -N` (tag-name only, durable signed rejection) |
| `amend <tvid> <tag> [value] [--insight "why"] [--disposition internal\|external] [--internal] [--replace] [--target TARGET]` | proxy-or-exclusive | Revise the `@ext-candidate` line whose source tvid is `<tvid>` (the handle `ark ext pending` prints): retire it and author the revision as one actor op, so the proposal stays pending rather than orphaning. Flags left off carry forward from the old line, `insight` included. The revision is a new identity at `@count: 1`; a revision colliding with an existing line merges into that line's tally |
| `pending [--chunk <target>] [--limit N] [--json]` | proxy-or-local | List the pending `@ext-candidate` ledger, one row per candidate line, ranked unattached-first then tally-descending (see `curation-workshop-primitives.md` "Reading the ledger"). Corpus-wide by default; `--chunk` narrows to proposals landing on a TARGET's chunks, and a TARGET matching nothing is an error, not a silent corpus-wide scan. Markdown by default (an unattached row's chunk cell is an em dash), `--json` for machine reads |

### `fetch` — read indexed file content

```
ark fetch [--wrap NAME] PATH...
```

For tmp:// paths: server is required, reads via `POST /fetch`. For
ordinary paths: if a server is running, fetch proxies to it via
`POST /fetch`; otherwise it opens the index locally via `withDB` and
reads `db.Fetch(path)`. The index is single-process (bbolt file
lock), so a local open while the server holds the DB would block —
proxying when the server is up is what keeps `ark fetch` from
hanging. Mirrors the `tag defs` server-proxy-or-local pattern (R510).

### `files` — list indexed files

```
ark files [options] [PATTERN...]
```

| Flag                   | Default | Meaning                                                                                  |
|------------------------|---------|------------------------------------------------------------------------------------------|
| `--status`             | `false` | Add `STATUS BYTES CHUNKS PATH` columns; status is `G` (good), `S` (stale), `M` (missing) |
| `--detail`             | `false` | With `--status`, add per-file chunk size statistics line                                 |
| `-files GLOB`  | —       | Repeatable positive path filter                                                          |
| `-without -files GLOB` | —       | Repeatable negative path filter                                                          |

Positional patterns narrow within the `-files` set. Without
`--status`, output is one path per line.

`files` emits paths as addresses, so it honors the global `--links` /
`--nolinks` selector; `--status` answers in the canonical namespace whatever
spelling the query used. Both work against a running server as well as cold:
matching is client-side either way, and `--links`' post-match expansion needs
the link table, which a CLI cannot hold while a server owns the index — so
that half is asked of the server over `POST /files/expand`.

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
| `--if-needed`           | `false` | Exit silently if `index.db` already exists |

Without `--if-needed`, removes existing `index.db` before
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

### `luhmann` — orchestrator supervisor + hosted-session lifecycle

```
ark luhmann spawn-record --class C --nonce N --task-id T
ark luhmann exit-record  --class C --nonce N --reason R [--crashes K] [--quit-early K] [--backoff S]
ark luhmann inspect-exit --nonce N [--json]
ark luhmann next [--first | --force] [--keepalive SECONDS]
ark luhmann send "<instruction>" [--timeout SECONDS]
ark luhmann events [--off]
ark luhmann launch [--bootstrap INPUT] [--model MODEL]
ark luhmann attach
ark luhmann status [--json]
ark luhmann stop
```

| Subcommand       | Flags                                              | Behavior                                                                                                                                                       |
|------------------|----------------------------------------------------|----------------------------------------------------------------------------------------------------------------------------------------------------------------|
| `spawn-record`   | `--class`, `--nonce`, `--task-id` (all required)   | Server-required. Append a `kind: "spawn"` record to `~/.ark/monitoring/luhmann.jsonl` via the write actor (carries both counters forward).                     |
| `exit-record`    | `--class`, `--nonce`, `--reason` (required); `--crashes` / `--quit-early` (counter overrides); `--backoff S` (records the seconds the supervisor will wait before respawn) | Server-required. Reason → kind: `context-limit`→`exit` (resets both counters), `quit-early`→`quit-early` (increments `quit_early`, holds `crashes`), else→`crash` (increments `crashes`, holds `quit_early`). |
| `inspect-exit`   | `--nonce`; `--json`                                | Cold-start. Classify a subagent exit as `healthy` / `quit-early` / `crash` / `unknown` via the nonce → `.meta.json` lookup. Default prints the label; `--json` adds details. |
| `next`           | `--first` (claim when unowned, on self, or over a stale incumbent), `--force` (reclaim unconditionally); `--keepalive SECONDS` (idle window, default ~2700 = 45m, capped under the 1h cache) | Server-required. The orchestrator's blocking drain tube: returns one curation task, supervisor directive, command, frictionless-event (when routing is on, see `events`), or keepalive crank-handle, guarded by an in-memory ownership lease. `--first`/plain/`--force` per the lease; a **live** foreign owner exits 2 (stand down), an unowned server on a plain call returns the reclaim crank-handle. The lease is held by the drain rather than by a timer — live while the owner has a call in flight and for a grace window after — so a departed orchestrator's seat is claimable by `--first`, while plain never takes over. Backgrounded + re-invoked in a loop; subsumes the standalone `ark heartbeat` keepalive. |
| `send`           | `"<instruction>"` (required positional); `--timeout SECONDS` (wait bound, default 120) | Server-required. Synchronous producer for the drain tube: enqueues the instruction as a `command`-kind work item with an inert backquoted correlation nonce (Watermark), waits for the orchestrator to handle it, then prints the orchestrator's turns for that instruction — bracketed from the nonce's appearance in the session JSONL to the first turn completion — rendered like `ark chats --with-tools`. Errors orchestrator-not-running when no session owns the tube; on `--timeout` exits non-zero (the enqueue stands). |
| `events`         | `--off` (release)                                  | Server-required. Route Frictionless UI events onto this session's `next` tube as a `frictionless-event` kind. Routing is a privilege of the `next` seat: a session not owning it errors `you don't have ownership`. While owned, `ark ui event` errors 409 — the drain is destructive, so exactly one reader is served. `--off` releases and restores `ark ui event`. Routing does not inherit: a seat change or a bounce clears it, so a new orchestrator must ask for itself. |
| `launch`         | `--bootstrap INPUT` (default `load /luhmann`), `--model MODEL` | Server-required. Fork a pty (cwd `~/.ark/luhmann`), start the child command, and run the content-free confirmation (await the 2nd session JSONL record → send the bootstrap → await the seat claim), then print the confirmed session id. `--model` names the model for this launch only, falling back to `[luhmann] model` then the compiled-in default; it never writes config. Errors if combined with a `[luhmann] command` override, which supplies its own argv. The **spend consent gate** — the only CLI door that starts a paid session; errors if one is already hosted. Blocks until confirmed. |
| `attach`         | none                                               | Server-required. Raw-mode terminal client over the unix socket: stdin → pty, pty → stdout, with `SIGWINCH` (smallest-wins) resize and a tmux-style detach escape (`Ctrl-]` then `d`). Detaching leaves the session running; multiple `attach` clients may connect at once. |
| `status`         | `--json`                                           | Server-required. The single source of truth: whether a session is hosted, its id, and the pool-secretary roster count. Human text by default; `--json` for machine reads. |
| `stop`           | none                                               | Server-required. Graceful teardown ("End session"): asks the child to exit and lets it flush its transcript before the kill, releasing the seat lease and recording the hosted pool secretaries' exits — leaving the monitoring log truthful (no ghosts). Those two obligations belong to the **teardown**, not to this verb: a child that exits on its own discharges the same pair, and the exit record's reason (`luhmann-stopped` vs `luhmann-exited`) is what says which happened. |

See: `monitor`, `connections recall context`, [luhmann.md](luhmann.md), [managed-pty.md](managed-pty.md)

### `message` — messaging operations

```
ark message new-request   --from P --to P (--issue TEXT | --issue-file PATH) [--content BODY | --content-file PATH] FILE
ark message new-response  --from P --to P --request ID [--content BODY | --content-file PATH] FILE
ark message set-tags      FILE TAG VAL [TAG VAL ...]
ark message get-tags      FILE [TAG ...]
ark message check         FILE
ark message inbox         [--project P] [--to P] [--from P] [--all] [--include-archived] [--compact] [--counts] [--unmatched]
ark message dm            (--from S | --from-service NAME) --to R [--to R2 ...] [--subject TEXT] [--ref ID] --content TEXT
```

| Subcommand     | Flags                                                                | Behavior                                                                                                                                        |
|----------------|----------------------------------------------------------------------|-------------------------------------------------------------------------------------------------------------------------------------------------|
| `new-request`  | `--from`, `--to`, `--issue`\|`--issue-file`, `--content`\|`--content-file` (else stdin until lone `.`) | Create FILE (must not exist), set ark-request/from-project/to-project/status=open/status-date=today/issue tags. Issue from `--issue` or verbatim `--issue-file` (one required). Body from `--content`, verbatim `--content-file`, or stdin. `*-file` flags read verbatim (trailing newline trimmed) and are mutually exclusive with their inline twin. |
| `new-response` | `--from`, `--to`, `--request`, `--content`\|`--content-file` (else stdin)              | Create FILE, set ark-response=ID/status=accepted/status-date=today. Body from `--content`, verbatim `--content-file`, or stdin.                                                                             |
| `set-tags`     | none                                                                 | Alias for `tag set`                                                                                                                             |
| `get-tags`     | `--compact`                                                          | Alias for `tag get` (bullet list default, `--compact` for TSV)                                                                                 |
| `check`        | none                                                                 | Calls `tag check` with the standard message heading list; prints a well-formed line on success                                                  |
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
| `--compact`          | `false` | Legacy tab-separated output instead of the markdown tree    |
| `--counts`           | `false` | Status counts instead of rows: `- status (N)` bullets by default, `STATUS\tCOUNT` under `--compact`; takes precedence over the tree |
| `--unmatched`        | `false` | Show only requests with no matching response. Pair lookup is global — `--unmatched` combines correctly with `--to`/`--from`. |

Filters combine as intersection: `--from ark --to frictionless`
shows only the ark→frictionless slice.

**Default output is an agent-readable markdown tree** (the same "baby
food" style as `ark search -tags`). One bullet per request — its
status, `from → to`, summary, `@status-date`, and path — with its
paired response nested beneath (the response's own status, `from →
to`, `@status-date`, and path). Each side's bookmark lag is stated in
words ("PROJECT has not handled this response yet" / "PROJECT has not
handled this request yet"); an unanswered request prints an explicit
`no response` line. **A response nests under its request even when the
default completed/denied hide or a directional filter would drop it as
its own row** — pairing precedes filtering, so this is display-only.
When no message matches, the tree names the from-/to-projects present
in the unfiltered set, so a mistyped filter is correctable.

`--compact` emits the legacy tab-separated rows instead, byte-for-byte:
`DATE STATUS TO FROM SUMMARY PATH LAG`. Lag format
`lag:PROJECT:STATUS` describes a stale `response-handled` /
`request-handled` bookmark relative to the counterpart's status.

Pair lookup (used by `--unmatched`, by the nested-response display, and
by the LAG column / bookmark lines) consults the **full inbox**, not
the post-filter slice. A directional or status filter changes what's
*shown*; the matcher always sees both sides of a pair. See R2484.

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
ark unresolved [--reason uncovered|unindexable|symlink]
ark resolve    PATTERN...
```

`missing` lists files indexed but no longer on disk (M records).
`stale` lists files whose mtime is newer than their last index pass.
`unresolved` lists files ark noticed and did not index (U records).
`resolve PATTERN` dismisses matching unresolved entries.

Path-listing commands honor the global link-view selector — see
[symlinks.md](symlinks.md). Briefly: a pattern matches *addresses*, so
`~/s2/link/a` and `~/s1/a` are both legal names for one file and `'**/a'`
returns both. `--links` expands each result to every address of that file;
`--nolinks` restricts matching to canonical addresses, which is how a script
gets a file set that does not change when someone creates a symlink.
`ark files --status` reports canonical paths whatever spelling was asked, so
it is also how two listed addresses are shown to be one file. Search does not
consult the selector: it reports canonical paths only, because an alias is a
way to reach a file rather than something the file says.

Each unresolved entry names its **reason**, and `--reason` narrows to
one kind ([symlinks.md](symlinks.md)):

- `uncovered` — matched no include/exclude rule; fix by editing patterns.
- `unindexable` — no chunker handles this kind of file; the great
  majority of entries. Fix by adding a chunker, which revives every
  such entry with no user action, since these are hidden rather than
  excluded.
- `symlink` — reached only through a symlink pointing outside every
  source; fix by adding a source. The entry names the proposing link,
  since the record is keyed on the target and would otherwise be
  offering a directory the user does not recognise.

The reason is what lets a caller hide the `unindexable` bulk without
losing it — a convenience for readers, never an exclusion.

All four proxy to the server when running.

### `patterns` — personal pattern collection

```
ark patterns list [--json]
ark patterns show SLUG
ark patterns add SLUG --name NAME --summary TEXT [--tags "a, b"] [--body-file FILE]
```

`list` writes the injection block — every pattern's name, tags, and
summary, ordered by slug — from a plain directory walk over frontmatter.
**No subcommand ever contacts the server**, because `list` runs at
session start before anything is up. An absent or empty collection
prints nothing and exits 0. `--json` emits `[{slug, name, tags,
summary, path}]`.

`show` prints one pattern's body for reading, **without the
frontmatter** — `name`/`tags`/`summary` are what `list` already put in
the context and what led the reader to this slug, so reprinting them
charges twice for the field whose job was getting them here. A single
header line names the pattern and its file, so a body pasted onward
stays attributable; the body's own `# NAME` heading is dropped so the
name appears once, while a leading H1 that is *not* the name is content
and survives. Only the YAML block goes: the body's `@tag:` lines carry
origin and neighbouring patterns, which is what a reader opened the file
for. An unknown slug errors and names the collection directory.

`add` writes `<patterns_dir>/<slug>.md` with the frontmatter block and
a `# NAME` heading, appends `--body-file` when given, and refuses an
existing slug rather than clobbering a body.

The collection lives at the `patterns_dir` config key (default
`~/.ark/personal-patterns/`). Each file also derives a tag definition
(`slug` → its `summary`) at index time. Full behavior:
[personal-patterns.md](personal-patterns.md).

Distinct from `ark ui patterns`, which lists the separate Frictionless
UI-development collection in `~/.ark/patterns/`.

### `rebuild` — drop and rebuild

```
ark rebuild
```

Refuses if the server is running. Calls `init --no-setup` (which
clears `index.db` and re-creates from ark.toml), then `scan`. During
the scan a read-only server stays up on the socket so `status` /
`search` from another terminal return live progress instead of
blocking on bbolt's single-process file lock (rebuild-read-serve.md).
Embeddings regenerate on the next `ark serve`.

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
| `search DATE` | `--tag` (filter), `--gaps` (only past unacked), `--json` (raw response) | Proxy/local — the local arm builds its own scheduler from `ark.toml` and `~/.ark/schedule/`. Returned events carry their own suppression state, marked `[suppressed]` in the render. DATE is a single date or `START..END` range; `dateparse` handles flexible formats (see parse-robustness note below). Default output is markdown grouped by date; each bullet collapses `START–END` when start==end and drops the trailing `: ` when there's no summary (so chime ticks don't render as `- 13:45–13:45:`). |
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
to start `ark serve`. `-multi` requires the Multi search path.

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
| `--compact` | `false` | Compact the database (bbolt `Tx.WriteTo`) before opening. When the flag is supplied (either form), it overrides `auto_compact` in ark.toml; when omitted, falls back to the toml setting (default `false`). See `serve-compact.md`. |

Exits 0 with a stderr message if a server is already running. The
process holds the file lock on `~/.ark/`, writes a PID file, and
listens on `<dir>/ark.sock` (and an HTTP port for browsers).

### `setup` — bootstrap `~/.ark/`

```
ark setup
```

Idempotent. Extracts bundled assets (`html/`, `lua/`, `viewdefs/`,
`apps/`, `skills/`, `agents/`) into `~/.ark/`, runs `linkapp` for the
`ark` app, and installs global skills + every bundled agent into `~/.claude/`.
Requires the running binary to be bundled.

### `sources` — manage source directories

```
ark sources [check]
ark sources pair [--auto] [--dry-run]
```

Default subcommand is `check`: expand globs in ark.toml's
`[sources]`, add new directories, report orphans. Output uses `+` /
`-` / `?` prefixes (added / MIA / orphaned). Server-proxy if
available.

`pair` proposes a `logs_for` for each unpaired **chat** source, by
matching a source's directory name against the mangled form of each
other source's directory. This is the only place that mangling is
applied — never the lookup path, which resolves `logs_for` by name —
so the vendor scheme stays deletable.

| Flag        | Default | Meaning                                        |
|-------------|---------|------------------------------------------------|
| `--auto`    | `false` | Write the proposed pairings into `ark.toml`    |
| `--dry-run` | `false` | Print the proposals and write nothing          |

A source already carrying `logs_for` is left untouched, so re-running
is safe and a hand-corrected pairing is never overwritten. A source
the heuristic does not match is not a log source and is omitted. An
existing `logs_for` naming no source is reported on stderr with `!` —
it is a live defect rather than a pending suggestion. Writes go
through the config-mutation path, so they proxy to a running server.

### `status` — show database status

```
ark status [options]
```

| Flag                   | Default | Meaning                                                                                |
|------------------------|---------|----------------------------------------------------------------------------------------|
| `--db`                 | `false` | Show index record counts by prefix (microfts2 + ark, with totals)                      |
| `--chunks`             | `false` | Show chunk size statistics (count, min, max, mean, median, p90, p95, p99) per strategy |
| `--tokenize`           | `false` | With `--chunks`: measure in tokens; requires `[embedding] model` **and the server stopped** — it loads the model for its vocabulary, like `embed bench` |
| `-files GLOB`  | —       | Repeatable positive path filter for `--chunks`                                         |
| `-without -files GLOB` | —       | Repeatable negative path filter                                                        |

Default output: file/chunk/source counts, database file size,
server/UI status, plus a `warnings:` section listing E records (each
`name: payload`). Prefers server proxy.

### `stop` — stop the server

```
ark stop [-f]
```

Requests a graceful shutdown over the unix socket (`POST /shutdown`),
which responds then tears the server down (stop scheduler, close
watcher, shut UI runtime, close socket, close DB, exit 0) — the same
teardown the Unix SIGTERM/SIGINT handler runs. A 200 or the connection
dropping mid-request counts as "teardown initiated". `stop` then polls
for release by dialing the socket (a refused dial means down); it does
**not** use `ark status`, which succeeds even with no server. The
release wait is bounded by a timeout.

On timeout the fallback is platform-split. On Linux, `stop` force-kills
the PID (from the ark-home PID file, falling back once to the legacy
temp-dir path) and re-polls, erroring if the kill fails or the process
still will not release. On Windows, `stop` errors without killing — a
failed graceful stop is left for the user to resolve rather than
risking a `TerminateProcess` that can leave the socket or DB lock held.

`-f` force-kills immediately on every platform (`TerminateProcess` on
Windows), the sanctioned ungraceful kill. Exits 1 when the server is
not running, on a Windows graceful-stop timeout, or if a kill fails to
release.

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
| `-files GLOB`  | —                            | Repeatable positive path filter                                                        |
| `-without -files GLOB` | —                            | Repeatable negative path filter                                                        |

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
ark tag files TAG... [--context] [-with|-without] [-files G]...
ark tag values TAG... [--show-files] [-with|-without] [-files G]...
ark tag defs [TAG...] [--path]
ark tag set FILE TAG VAL [TAG VAL ...]
ark tag get FILE [TAG ...] [--all] [--compact]
ark tag chunk FILE | FILE:TARGET [--all]
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
| `values TAG...` | cold-start | Values for each tag (`tag\tvalue\tcount`); with `--show-files`, follows up with per-file lines |
| `defs [TAG...]` | optional | Tag definitions (`tag description`); with `--path`, prefixes the source path (no dedup) |
| `set FILE TAG VAL ...` | none (file I/O) | Pairs of TAG VAL into FILE's tag block. Setting `status` auto-sets `status-date` to today. Hint when setting `*-handled` bookmarks |
| `get FILE [TAG...] [--all] [--compact]` | none / optional (`--all`) | Read tags from FILE's tag block as a `- tag: value` bullet list (values verbatim); `--compact` gives the legacy `tag\tvalue` TSV. Without TAGs, dump all. Missing tags exit 1. `--all`: every tag in the file (union across all chunks), not just the block — needs the index; the `[TAG...]` filter composes |
| `chunk ADDRESS [--all]` | none / optional | List tags at a file or chunk address, reusing the `ark chunks` / `@ext` grammar. Bare `FILE`: the file tag block (== `get`, no index). `FILE --all`: file-wide union across all chunks. `FILE:TARGET` (RANGE, `:"SNIPPET"`, or chunkID): that chunk's tag union — the only per-chunk view. Index-backed forms proxy to the server or cold-start |
| `check FILE [HEADING...]` | none | Validate FILE's tag block. Prints a well-formed line on success (exit 0), the problems on failure (exit 1). Optional headings restrict allowed body headings |
| `verify` | refused | Cross-check F/V/T/X records and the in-memory ExtMap. `--repair` writes corrections in a single write transaction. `--scope` is `ext`, `tag-totals`, or `all` (default). Refuses if the server is running. Exit 1 on issues, 2 on tool failure or invalid scope |
| `inspect` | optional | Read-only observability for `@ext` state. Server-aware: proxies via the running server (in-memory ExtMap section included) or opens the index read-only when stopped (disk-only with a note). `--scope ext` (v1 only); `--target PATH` narrows to one file's chunks; `--json` for machine output. Output sections: on-disk (X / V[ext] / F[ext]), in-memory ExtMap maps, per-tvid_ext bridges with decoded paths and routed (tag, value) pairs |

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

Most UI subcommands require the server: they send HTTP requests
through `<dir>/ark.sock`, and most are thin wrappers over Lua API
endpoints. Two do not — `open` (the no-args default), and `patterns`,
which is a plain directory walk over `~/.ark/patterns/` and never
opens a socket.

| Subcommand | Behavior |
|------------|----------|
| (none) | Read `<dir>/ui-port`, open browser via `xdg-open`/`open` |
| `audit APP` | `POST /api/ui_audit` — code quality audit |
| `checkpoint CMD APP [MSG]` | Fossil-backed checkpoints (see below) |
| `display APP` | `POST /api/ui_display` — show app in browser |
| `event` | `GET /wait?timeout=120` — block until next UI event; retries on transient. Errors (409, exit 1) while a Luhmann orchestrator owns event routing (`ark luhmann events`), which reroutes these events onto its own tube — not retryable, since only that owner releases it. Unaffected when no orchestrator has asked. |
| `install` | Per-project setup (alias `ark install`): runs `init --if-needed`, starts server if down, symlinks `~/.ark/skills/{ark,ui}` and every `~/.ark/agents/*.md` into `.claude/`, prints crank-handle prompt |
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
which the command will print download instructions for if missing).
Safety contract in [ui-checkpoints.md](ui-checkpoints.md): the app
directory's checkout binding is verified before every fossil step (abort
loudly on mismatch), opens are `--nested`/`--keep`, branch saves never
modify the working tree, and every fossil failure surfaces on stderr with
a nonzero exit — no unconditional success messages:

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
