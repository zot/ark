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

@CLAUDE.local.md
