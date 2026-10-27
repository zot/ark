# Test Design: ConfigErrors
**Source:** crc-CLI.md, crc-DB.md

## Test: an ark.toml that does not load is an error, a missing one is not
**Purpose:** R3645 — `configLoadError` returns nil for a missing file and for a good one, and an error naming `ark config recover` for one that does not parse
**Input:** temp ark dir with no ark.toml; with a valid one; with `dotfiles = = true`
**Expected:** nil, nil, error containing the parse failure and `ark config recover`
**Refs:** crc-CLI.md, R3645
**Fire alarm:** return nil after loading, whatever the error
**Inject:** cmd/ark/configcheck.go:configLoadError
**Pulled:** 2026-10-27 (local) — rang: `broken ark.toml: got <nil>, want an error naming`; the guard test also went red (`[search foo]: err=<nil> ran=true`)

## Test: every command node is guarded except the exempt paths and help
**Purpose:** R3645, R3646 — after `guardConfigActions`, dispatching a guarded node returns the config error without running its body, while `stop`, `status`, `config recover`, and `search --help` on a SkipFlagParsing node run
**Input:** a synthetic tree (`search` with SkipFlagParsing, `stop`, `status`, `config recover`, `config show`) whose Actions record that they ran, dispatched through `Run`; an ark dir whose ark.toml does not parse
**Expected:** `search foo` and `config show` return the error and do not run; the three exempt paths and `search --help` run
**Refs:** crc-CLI.md, R3645, R3646
**Fire alarm:** ignore the exemption set
**Inject:** cmd/ark/configcheck.go:guardConfigActions
**Pulled:** 2026-10-27 (local) — rang: `[stop]`, `[status]`, `[config recover]` each refused with the parse error
**Fire alarm:** never recognize a help request
**Inject:** cmd/ark/configcheck.go:asksForHelp
**Pulled:** 2026-10-27 (local) — rang: `[search --help]` refused with the parse error. The defect is real: the live binary refused `ark search --help` before asksForHelp existed

## Test: the exempt paths exist in the real tree
**Purpose:** R3646 — a renamed command would silently lose its exemption, so the real command tree must contain every exempt path
**Input:** `buildArkCommand()`
**Expected:** each exempt path resolves to a node with an Action
**Refs:** crc-CLI.md, R3646

## Test: status warns on an ark.toml that does not parse
**Purpose:** R3647 — with no server, `ark status` prints a `config_parse` warning, says the index was not read, and exits 1
**Input:** temp ark dir with an unparseable ark.toml; `cmdStatus` run in a child copy of the test binary (it ends in fatal)
**Expected:** exit 1; output holds `config_parse:` and `index not read`
**Refs:** crc-CLI.md, R3647
**Fire alarm:** skip the cold-status branch
**Inject:** cmd/ark/main.go:cmdStatus
**Pulled:** 2026-10-27 (local) — rang: `status output lacks "config_parse:"`, `lacks "index not read"`

## Test: recover works on an ark.toml that does not parse
**Purpose:** R3648, R3649 — `config recover` reads the I records without loading ark.toml, keeps the broken file as `ark.toml.broken`, and writes a loadable ark.toml
**Input:** `ark.Init` a temp dir with `dotfiles = true`, then overwrite ark.toml with unparseable text; run `configRecoverAction`
**Expected:** `ark.toml.broken` holds the broken text; ark.toml loads; dotfiles is still true
**Refs:** crc-CLI.md, crc-DB.md, R3648, R3649
**Fire alarm:** require a loadable ark.toml before reading the snapshot, as the full DB open did
**Inject:** cmd/ark/config_cli.go:configRecoverAction
**Pulled:** 2026-10-27 (local) — rang: the test process exited on `error: …/ark.toml does not load: parse config`
**Fire alarm:** skip the rename
**Inject:** cmd/ark/config_cli.go:configRecoverAction
**Pulled:** 2026-10-27 (local) — rang: `ark.toml.broken = "", open …/ark.toml.broken: no such file or directory`

## Test: patterns falls back only when ark.toml is missing
**Purpose:** R3650 — `patternsCollectionFor` returns the default collection for a missing ark.toml and an error for one that does not parse
**Input:** temp dir without ark.toml; with unparseable ark.toml
**Expected:** default dir, nil; an error
**Refs:** crc-CLI.md, R3650
**Fire alarm:** fall back to the default on any load error
**Inject:** cmd/ark/patterns_cli.go:patternsCollectionFor
**Pulled:** 2026-10-27 (local) — rang: `broken ark.toml: got "…/personal-patterns", want an error`. A first injection that left imports unused failed to compile instead; it was re-pulled
