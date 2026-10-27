# Config errors

A malformed `ark.toml` should stop ark and say so. Today some paths do and some
quietly carry on with something else, so a typo in the config can show the user
the wrong data with no hint that anything is wrong. This carve makes a parse
failure loud everywhere: at the command line, in `ark status`, and in the UI.

Opened by Bill, 2026-10-27, from windows-support 7.4.6, where a test fixture's
unparseable `ark.toml` made `ark patterns` silently fall back to the default
directory.

## Status

- [x] ~~**Item 1 — every CLI execution checks `ark.toml` at startup.**~~ **LANDED (2026-10-27 — `#179`.)** Exemptions decided. `/mini-spec`. See Item 1.
- [x] ~~**Item 2 — a failed reload records an error; a good read clears it.**~~ **LANDED (2026-10-27 — `#180`.)**
      `/mini-spec`. See Item 2.
- [ ] **Item 3 — the UI shows the error.** **OPEN (not queued.)** Whether the
      UI already shows `E:` records is unmeasured. `/ui-fast`. See Item 3.

## The design as decided

DECIDED (Bill, 2026-10-27):

- **ark errors on an `ark.toml` parse failure.** It never falls back to
  defaults.
- **Every execution of ark checks the file at startup.**
- **A file-sync reparse that fails sets an error record**, so the UI can show
  it.
- **The record clears on a successful read** by `ark serve`, by a file sync, or
  by a CLI execution that opens the DB locally for writing.

## What exists today (measured 2026-10-27)

- **Opening the DB already refuses a bad config.** `OpenWithTimeout` returns
  `load config: …`, so `ark serve` and every local-arm CLI command already fail
  on a parse error.
- **Every local CLI open is a write-capable open.** The local arm of
  `proxyOrLocal` and `withDB` both go through `OpenWithTimeout`, and bbolt opens
  read-write under its single-process lock. "A CLI execution that opens the DB
  locally for writing" is therefore any DB-touching command run without a
  server.
- **A failed reload is logged and nothing more.** The `ark.toml` watcher
  (`watcher.go`, the `configDebounce` branch) logs `config reload failed` and
  keeps the old config; the schedule-suppress handler in `server.go` does the
  same after rewriting the file. Neither writes a record.
- **Six CLI call sites swallow the parse error.** They are in `cmd/ark/main.go`
  (four), `patterns_cli.go` (`patternsCollection`, which falls back to
  `<db>/personal-patterns`), and possibly `embed_cli.go`. A proxied command never
  opens the DB, so it never sees the parse failure at all.
- **The `E:` record class is the natural home.** `E:` + name, JSON value,
  surfaced as `ark status` warnings (`specs/record-formats.md`). Its rule is that
  a condition is re-derived from durable state on every config load rather than
  dismissed (`specs/config-tracking.md`), and a parse failure is witnessed by the
  file itself, so it fits without a new prefix.

## Item 1

One check at CLI startup, before dispatch: if `ark.toml` exists and does not
parse, print the parse error (file, line, what TOML objected to) and exit
non-zero. A missing file is not an error, because a fresh install has none
(R3271). The swallowing call sites then become unreachable on a bad file. They
should still stop swallowing, so a file that breaks between the check and the
read fails rather than defaulting.

**What still runs on a broken config.** DECIDED (Bill, 2026-10-27): four
exemptions, so a user who has broken the file can still fix it and get ark
back:

- `--help`, at any level.
- `ark stop`.
- `ark status`, which reports the parse error as a warning rather than failing
  on it.
- `ark config recover`, which exists to restore config from the stored `I`
  records.

Every other command fails at startup. The error message should name the
repair: fix the file, or run `ark config recover`.

Also DECIDED (Bill, 2026-10-27): recover keeps an ark.toml that does not load
as `ark.toml.broken` before writing, and cold-start `status` (no server) warns,
says the index was not read, and exits 1, since opening the index needs the
config.

Outcome (2026-10-27): R3645-R3650. Every command node's Action is wrapped with
the check except the exempt paths; `--help` needed recognizing in the args,
because a SkipFlagParsing node (`search`, `status`) receives it raw and the
live binary refused `ark search --help` until it did. Found on the way:
`config recover` could not recover anything. It opened the full DB, which
loads ark.toml first, so it failed on exactly the files it exists for, missing
ones included. It now reads the I records through `ark.ReadStoredConfig`,
which opens only the store. The other call sites that swallowed a load error
(setup's llama provisioning, rebuild's closing note, serve's compact default,
`luhmann` inspect's context limit) were left alone: the startup check now
stops a bad file before they run.

## Item 2

A new `E:` condition, provisionally `config_parse`, with the parse error and
the time as its value:

- **Set** when the server's reload fails: the watcher's debounced reload and
  the schedule-suppress handler's reload.
- **Cleared** when `ark.toml` is read successfully by `ark serve` at startup,
  by a reload, or by a local CLI open.

The server keeps running on its last good config while the record is set, and
the warning should say so. A reader of `ark status` needs to know that the
running config is not the file on disk. Updates `specs/record-formats.md`'s
known-conditions table and `specs/config-tracking.md`.

Outcome (2026-10-27): R3651-R3653. Both reload sites go through
`DB.ReloadConfig`, so the record is kept there rather than at each caller:
`recordConfigParse` writes `{error, time, server}` when the load fails and
deletes the record when it succeeds, and `OpenWithTimeout` deletes it once the
store is open, which covers server startup and every local CLI open. The file
is the witness, so the condition conforms to the re-derive rule from the
start. The CLI's warning constant is now the store's condition name, so a
proxied `status` merges them under one key, and the server's record wins:
the CLI's text says to run `ark config recover`, which refuses while a server
runs, while the server's says it is still on its last good config and that
fixing the file reloads it.

## Item 3

The UI shows the condition while it is set. Not measured yet: whether the ark
app already shows `E:` records anywhere, which decides whether this is a new
surface or one more row in an existing one.
