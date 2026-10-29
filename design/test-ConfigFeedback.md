# Test Design: config feedback and defaults
**Source:** crc-CLI.md, crc-Config.md

Four papercuts the 2026-10-27 Windows end-to-end run found (windows-support
13.3). The CLI tests run the real command tree (`arkCommands()`) against a
throwaway ark home with a default `ark.toml` and no server, so each command
takes the path a user's would.

## Test: the default excludes keep editors' side files out, and `**` takes them
**Purpose:** R3676: a newly written default config, both from the bundled `install/ark.toml` (what `ark setup` seeds) and from the built-in fallback, excludes Vim's and Emacs's side files under the defaults, and the note beside them is not excluded. Include wins, pinned so the spec's sentence stays true: under `include = ["**"]` every side file is included, and under the bundled defaults Emacs's lock file `.#b.md` is included because the default `*.md` matches it
**Input:** WriteDefaultConfig (with the bundled seed, and with none), LoadConfig, the real Matcher; `notes/b.md` and six side files (`b.md~`, `.b.md.un~`, `.b.md.swp`, `#b.md#`, `.#b.md`, `top.md~`)
**Expected:** under the defaults each side file excluded unless a default include matches it, and at most one (the lock file) so matched; all included under `**`; `b.md` never excluded
**Refs:** crc-Config.md, R3676
**Code:** config_defaults_test.go
First written against the fallback only (a nil seed), which hid that the bundled file's `default_include` matches `.#b.md`. Testing the seed `ark setup` actually uses found it on the first run.
**Fire alarm:** drop `*~` from the bundled defaults
**Inject:** install/ark.toml
**Pulled:** 2026-10-29 (local) — rang: `only 3 of 6 side files are excluded under the defaults`, `notes/b.md~ under the defaults = 2, want 1`
**Fire alarm:** drop `*~` from the built-in fallback
**Inject:** config.go:WriteDefaultConfig
**Pulled:** 2026-10-29 (local) — rang: `only 4 of 6 side files are excluded under the defaults`, `notes/b.md~ under the defaults = 2, want 1`

## Test: a pattern list given as a string names both accepted forms
**Purpose:** R3678: `include = "**"` does not load, and the error names the array and the `add` table forms, not only the type
**Input:** an ark.toml with `include = "**"` on a source; LoadConfig
**Expected:** an error containing `include = ["**"]` and `include.add = ["**"]`
**Refs:** crc-Config.md, R3678
**Code:** config_defaults_test.go
**Fire alarm:** the old message, `unsupported pattern form: %T`
**Inject:** config.go:PatternSpec.UnmarshalTOML
**Pulled:** 2026-10-29 (local) — rang: the error `… unsupported pattern form: string` does not name either form

## Test: config mutations say what they did
**Purpose:** R3677: add-source, add-exclude, add-include with `--source`, remove-pattern, add-strategy and remove-source each print one line naming what changed and where, and the change is in ark.toml
**Input:** the six commands through the real tree, no server
**Expected:** `added source DIR`, `added exclude *.bak (global)`, `added include *.org (source DIR)`, `removed pattern *.bak (global)`, `added strategy *.org = lines`, `removed source DIR`; the strategy in ark.toml
**Refs:** crc-CLI.md, R3677
**Code:** cmd/ark/config_feedback_test.go
**Fire alarm:** mutateConfigCLI prints nothing
**Inject:** cmd/ark/config_cli.go:mutateConfigCLI
**Pulled:** 2026-10-29 (local) — rang: `ark config add-source … printed "", want "added source …"`, for each command

## Test: bare `ark sources` lists, read-only, with no index
**Purpose:** R3679: bare `ark sources` lists the configured sources and the built-in ark home, marks a missing directory, leaves ark.toml byte-identical, and needs no index. It ran `check` until 2026-10-28, which opens the index and can add sources
**Input:** a home with no index, one added source and one hand-written source whose directory is missing; `ark sources` run in a child copy of the test binary, since a path that opens the index ends in fatal
**Expected:** `# Sources`, both directories and the ark home listed, the missing one marked `missing`, ark.toml unchanged, exit 0
**Refs:** crc-CLI.md, R3679
**Code:** cmd/ark/config_feedback_test.go
**Fire alarm:** point bare `sources` back at `check`
**Inject:** cmd/ark/flat_cli.go:flatCommands
**Pulled:** 2026-10-29 (local) — rang: `bare ark sources did not list without an index (a read-only listing needs none): exit status 1`. A first version ran the command in-process, and the alarm only crashed the test binary with an index-open error that named nothing about `ark sources`; the child process made the failure say what broke

## Test: the source list's marks
**Purpose:** R3679: a glob source shows as its pattern marked `glob`; a glob-made source names its glob; a log source names the source it records
**Input:** formatSourceList over three sources
**Expected:** the three bullets with their marks
**Refs:** crc-CLI.md, R3679
**Code:** cmd/ark/config_feedback_test.go
