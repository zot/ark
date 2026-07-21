# Chunker Strategy Registration

Language: Go. Environment: CLI (part of the `ark` binary).

Ark registers bracket and indent chunkers from microfts2 as named
strategies. This enables scope-aware chunking for code files — search
results respect function, class, and block boundaries instead of
splitting on raw lines.

## Strategy globs

The `strategies` map (top-level and per-source) keys chunker names by
glob. These are **source-scoped** globs matched by the one shared matcher
(`ark.Matcher`): the source's directory is the root, so a bare `*.md`
matches markdown at any depth within the source, and `./src/**` anchors to
the source root. See [main.md](main.md#glob-patterns).

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
non-default escape characters, or scan-restricted contexts (template
literal interpolation) use the full form with inline tables.

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

### Bracket modes (full form only)

Brackets and strings are the same construct in microfts2 — strings
are scan-restricted brackets. Ark hides this behind separate `strings`
and `brackets` (easy form), and behind `string_defs` and `bracket_defs`
(full form). Internally each entry maps to a single `BracketGroup`:

- `strings` / `string_defs` → restricted-mode group (only the close
  marker, the escape, and any explicitly listed inner openers are
  recognized; everything else is literal).
- `brackets` / `bracket_defs` → code-mode group (full scanning inside).

`bracket_defs` entries support three optional fields that expose the
underlying microfts2 model for languages that need them:

- `escape` — escape character honored inside the group (rare for code
  brackets; useful for word brackets that need `\` interpretation).
- `allowed_inner` — list of opener strings that *re-enter* code mode
  from inside an otherwise restricted group. The classic case is the
  JavaScript template literal `` ` `` re-entering code via `${`.
  Setting `allowed_inner` (even to an empty list) puts the group into
  scan-restricted mode — making `bracket_defs` capable of expressing
  string-like brackets as well.
- `allowed_parent` — list of parent openers in which this bracket may
  be recognized. The classic case is the JS interpolation bracket
  `${` ... `}`, which should only be recognized while inside a
  backtick template literal. Outside that context, `${` is plain text.

Example — JavaScript template literal with interpolation:

```toml
[[chunker]]
name = "bracket-js"
type = "bracket-full"
line_comments = ["//"]
block_comments = [["/*", "*/"]]
string_defs = [
  {open = "\"", close = "\"", escape = "\\"},
  {open = "'",  close = "'",  escape = "\\"},
]
bracket_defs = [
  {open = ["{"], close = ["}"]},
  {open = ["("], close = [")"]},
  {open = ["["], close = ["]"]},
  # backquote string that re-enters code via ${...}
  {open = ["`"], close = ["`"], escape = "\\", allowed_inner = ["${"]},
  # ${...} only recognized inside backquote groups
  {open = ["${"], close = ["}"], allowed_parent = ["`"]},
]
```

Notes on semantics:

- Easy-form `strings` always use scan-restricted mode with the
  configured escape (default `\`) and no inner openers — the common
  case.
- `allowed_inner = []` and an omitted `allowed_inner` are
  *semantically distinct* in microfts2: omitted means code mode,
  empty list means restricted with no inner openers (pure raw
  string). In TOML, omit the field for code mode; write
  `allowed_inner = []` for raw-string mode.
- A `bracket_defs` entry with `allowed_inner` set is functionally a
  string. Use `string_defs` for ordinary strings; reach for the
  `bracket_defs` form only when you need `allowed_inner` to name
  inner openers (template literals).

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
