# Chunker Strategy Registration

Language: Go. Environment: CLI (part of the `ark` binary).

Ark registers bracket and indent chunkers from microfts2 as named
strategies. This enables scope-aware chunking for code files — search
results respect function, class, and block boundaries instead of
splitting on raw lines.

## Config-driven language definitions

Chunker language configs live in `ark.toml` as TOML, not in Go code.
Users can add, modify, or remove language definitions without
recompiling. Ark reads the config, constructs `microfts2.BracketLang`
values, and registers them via `microfts2.AddChunker`.

### Easy form — flat pairs

Most languages use symmetric delimiters and simple bracket pairs.
The easy form uses flat arrays of string pairs with sensible defaults
(escape character defaults to `\` for strings).

```toml
[[chunker]]
name = "bracket-go"
type = "bracket"
line_comments = ["//"]
block_comments = [["/*", "*/"]]
strings = [["\"", "\""], ["`", "`"]]
brackets = [["{", "}"], ["(", ")"], ["[", "]"]]
```

```toml
[[chunker]]
name = "indent-python"
type = "indent"
tab_width = 4
line_comments = ["#"]
strings = [["\"", "\""], ["'", "'"], ["\"\"\"", "\"\"\""]]
```

### Full form — struct-level control

Languages with word brackets (begin/end), separators (then/else),
or non-default escape characters use the full form with inline
tables.

```toml
[[chunker]]
name = "bracket-shell"
type = "bracket-full"
line_comments = ["#"]
string_defs = [{open = "\"", close = "\"", escape = "\\"}, {open = "'", close = "'"}]
bracket_defs = [{open = ["if"], separators = ["then", "elif", "else"], close = ["fi"]}, {open = ["do"], close = ["done"]}]
```

```toml
[[chunker]]
name = "indent-lua"
type = "indent-full"
tab_width = 4
line_comments = ["--"]
string_defs = [{open = "\"", close = "\"", escape = "\\"}, {open = "'", close = "'", escape = "\\"}, {open = "[[", close = "]]"}]
```

### Type field

- `bracket` — easy form, flat string pairs, default escape `\`
- `bracket-full` — full form, inline table structs
- `indent` — easy form for indent chunker
- `indent-full` — full form for indent chunker

The `tab_width` field applies to indent types (default 4 if omitted).

Unknown types produce a warning at init, not a fatal error.

## Skeleton ark.toml

Default chunker configs ship in `install/ark.toml` as part of the
source tree. This file is bundled into the binary via the same
mechanism as `install/tags.md` (`BundleReadFile` with fallback).

`ark init` seeds `ark.toml` from `install/ark.toml` when no
ark.toml exists. If ark.toml already exists, `ark init` preserves
it (same behavior as today with other settings).

Custom distributions replace `install/ark.toml` before bundling —
same mechanism as `install/tags.md`. An org that uses Rust and
Kotlin but not Pascal or Lisp ships their own skeleton with their
language configs. No code changes, no build flags.

The skeleton includes configs for:
- `bracket-go` — Go
- `bracket-c` — C/C++
- `bracket-java` — Java
- `bracket-js` — JavaScript
- `bracket-lisp` — Lisp
- `bracket-nginx` — nginx config
- `bracket-pascal` — Pascal
- `bracket-shell` — Shell/Bash
- `indent-python` — Python (tab width 4)
- `indent-yaml` — YAML (tab width 2)

## Registration

On `DB.Init`, ark reads the `[[chunker]]` entries from ark.toml,
constructs `BracketLang` values from the TOML fields, and calls
`microfts2.AddChunker(name, chunker)` for each. This replaces the
current hardcoded `funcStrategies` map for these languages.

Existing strategies (`lines`, `markdown`, `chat-jsonl`,
`lines-overlap`, `words-overlap`) remain hardcoded — they don't
need language configs.

If a `[[chunker]]` entry has a name that conflicts with a hardcoded
strategy, the TOML config wins (override).

Invalid configs (missing required fields, bad type) produce a
warning at init and are skipped.

## User configuration

Users assign strategies to sources in `ark.toml`:

```toml
[[sources]]
dir = "~/work/myproject"
strategy = "bracket-go"
```

No new CLI commands are needed. The strategies appear in
`ark strategy list` alongside existing strategies.

## Refresh behavior

Files already indexed with a different strategy (e.g., `lines`) are
not automatically re-indexed when a new strategy is assigned. Users
run `ark rebuild` to re-index with the new chunker. This is
consistent with existing strategy-change behavior.
