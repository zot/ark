# Ark

Digital zettelkasten — files on disk, LMDB index. Hybrid trigram + vector search.

load /ark first. This will let you remember and access memories.

## Build

```
go build -buildvcs=false ./...
```

The `-buildvcs=false` is needed because the repo has both git and fossil.

The ark CLI should have nice --help. All subcommands should support --help.

## Frictionless UI

The Frictionless command is `~/.ark/ark ui`. UI skills use `{cmd}` as a placeholder for this.

**The ark app lives in both Git and Fossil.** The app source is at `apps/ark/` inside this Git repo, but since it's also a Frictionless app it uses the Fossil checkpoint flow (`{cmd} checkpoint local`, `{cmd} checkpoint baseline`). After a `/ui-thorough` pass, do both: `checkpoint local` for the Fossil branch, then a Git commit for the repo. This is non-standard — most Frictionless apps only use Fossil.

## Quick Recall

Use /ark to query the knowledge base or write tagged content.

## Frictionless Integration

See [ACCESSING-FRICTIONLESS.md](ACCESSING-FRICTIONLESS.md) for how ark registers Go functions in Lua (type chain, session model, active vs passive execution).

## Code Changes

**Go changes must go through mini-spec.** Load `/mini-spec` and update the design (requirements, CRC cards, sequences) before modifying any Go files. Unanchored Go changes — code not tracked by the spec — can silently drift or disappear in future sessions. The spec is the anchor.

**Separate Go and UI work.** The mini-spec and Frictionless UI workflows overlap significantly. Do Go/spec changes in one pass, UI changes (Lua, viewdefs) in another. Don't mix them in the same work stream.

Go files use tabs for indentation. The Read tool displays them as spaces, so the Edit tool's `old_string` often fails on the first attempt. Use `cat -A` to see the actual whitespace when an edit doesn't match.

Don't forget about gofmt -- no need to sed your way into poverty!
