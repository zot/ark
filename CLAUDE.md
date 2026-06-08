# Ark

Digital zettelkasten — files on disk, LMDB index. Hybrid trigram + vector search.

load /ark first. This will let you remember and access memories.

See [principles.md](principles.md) for ark's project-level
commitments — what the system stands by and what those
commitments imply for every feature and error path.

## Build

```
go build -buildvcs=false ./...
```

The `-buildvcs=false` is needed because the repo has both git and fossil.

The ark CLI should have nice --help. All subcommands should support --help.

**Cross-cutting spec references need explicit updates.**
Mini-spec's per-feature anchoring won't catch the canonical
reference docs — update them yourself when their surface changes:

- `README.md` — the public front door, part of the released
  files. Keep its capability claims, install/run instructions,
  command examples, and feature descriptions consistent with the
  code **and** with the rest of the public docs (especially
  `specs/features.md`). A contradiction here is the most damaging
  kind — it's the first thing a new user reads. The released
  project must have no contradictions at all.
- `specs/cli-commands.md` — every CLI subcommand/flag and its
  semantics. Update when adding, renaming, or changing flags or
  subcommands. **This is a project commitment** (principles.md, "The
  documentation tells the truth"). Since the `urfave/cli` migration
  (2026-06-08), the binary's own `--help` is **generated from the
  command tree** (`cmd/ark/*_cli.go` + `arkCommands()`): a flag/subcommand
  declared on its node documents itself, so help can no longer drift from
  the code and there is no `usage()` or per-command `--help` printer to
  hand-maintain. Two surfaces remain — the command tree (which both makes
  a command work and generates its help) and this spec (the hand-kept
  inventory mirror). So a CLI change touches the node declaration **and**
  this spec; the help text follows for free. The docs are authoritative;
  readers shouldn't have to re-check the source.
- `specs/record-formats.md` — every LMDB record prefix, key shape,
  and value layout in the `ark` subdatabase. Update when adding a
  new record class, changing a key/value encoding, or retiring a
  prefix. (Also remember `ark status -db`: it lists these records
  and must stay in sync.)
- `specs/lua-api.md` — every Go-side `SetField` on the `mcp`,
  `MCP`, `sys`, `session`, and `ui` Lua globals. Update when
  adding, renaming, or removing a Lua method. Three repos
  contribute (`ark/server.go:registerLuaFunctions`,
  `frictionless/internal/mcp/tools.go:setupMCPGlobal`, and
  `ui-engine/internal/lua/runtime.go:createSessionTable` plus the
  helpers each calls).
- `specs/features.md` — every main capability with motivation and
  objective. Update when a new feature ships, a feature's status
  changes, or its motivation shifts. This is the project's
  capabilities-axis summary; the per-feature specs own the
  behavior.
- `specs/config.md` — every `ark.toml` key (top-level + every
  `[table]` and `[[array.table]]`) with type, default, one-line
  meaning, and the per-feature spec that owns it. Update when a
  per-feature spec adds, renames, or retires an ark.toml key.
- `specs/chunkers.md` — every registered chunker and the microfts2
  chunker interfaces it implements (the interface matrix), content
  source, and registration path. Update when adding/renaming a
  chunker, or when a chunker's implemented interface set changes.

## Frictionless UI

The Frictionless command is `~/.ark/ark ui`. UI skills use `{cmd}` as a placeholder for this.

**The ark app lives in both Git and Fossil.** The app source is at `apps/ark/` inside this Git repo, but since it's also a Frictionless app it uses the Fossil checkpoint flow (`{cmd} checkpoint local`, `{cmd} checkpoint baseline`). After a `/ui-thorough` pass, do both: `checkpoint local` for the Fossil branch, then a Git commit for the repo. This is non-standard — most Frictionless apps only use Fossil.

**New viewdefs need a `linkapp` to load.** The runtime reads viewdefs from `~/.ark/viewdefs/` (symlink directory). Adding a new viewdef to `apps/ark/viewdefs/` does not register it automatically — run `~/.ark/ark ui linkapp add ark` once to refresh the symlinks. The view will render nothing until you do.

## Quick Recall

Use /ark to query the knowledge base or write tagged content.

## Frictionless Integration

See [ACCESSING-FRICTIONLESS.md](ACCESSING-FRICTIONLESS.md) for how ark registers Go functions in Lua (type chain, session model, active vs passive execution).

## Repo Layout

```
cmd/ark/main.go        CLI entry point, all subcommands
*.go                   Core library: db, store, indexer, search, server, config, etc.
design/                Mini-spec: requirements.md, crc-*, seq-*, test-*, design.md
specs/                 Human-readable feature specs (input to mini-spec)
apps/ark/              Frictionless UI app (Lua + viewdefs, also in Fossil)
markdown-editor/       CM6 markdown editor (TypeScript, esbuild)
  src/                 index.ts, ark-tag-parser, tag-widget, tag-completion, etc.
  dist/                ark-markdown-editor.js (~1.1 MB, bundles full CodeMirror 6)
franklin/              Personal assistant agent design
requests/              Cross-project ark messages (messaging protocol)
knowledge/             Ark knowledge base files
librarian/             Librarian search module
cache/                 Build cache (Makefile asset pipeline)
```

## Code Changes

We need all code/spec/design changes to be **anchored**. Make sure to load the required skill before making changes.

Simple bug fixes to anchored code are OK to make without loading the skills because the code is already anchored.

**The apps/ directory contains Frictionless code** use `/ui-fast` or `/ui-thorough` to anchor those changes.
- Before changing Frictionless code, ask the user if mutation is needed. If the user can simply restart the server, then you are free to **save tokens** and not worry about mutation code **at all**.

**Code changes outside app/ contains mini-spec code** use `/mini-spec` to anchor the changes.


**Separate /mini-spec and /ui-* work.** The mini-spec and Frictionless UI workflows overlap significantly. Do mini-spec code/spec/design changes in one pass, Frictionless changes (Lua, viewdefs) in another. Don't mix them in the same work stream because they have conflicting instructions that have caused confusion in the past.

Go files use tabs for indentation. The Read tool displays them as spaces, so the Edit tool's `old_string` often fails on the first attempt. Use `cat -A` to see the actual whitespace when an edit doesn't match.

Don't forget about gofmt -- no need to sed your way into poverty!

Note that the Tag Forge is the same as the "Curation Workshop" or "Curation View"

@CLAUDE.local.md
