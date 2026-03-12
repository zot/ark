# Ark

Digital zettelkasten — files on disk, LMDB index. Hybrid trigram + vector search.

load /ark first. This will let you remember and access memories.

## Build

```
go build -buildvcs=false ./...
```

The `-buildvcs=false` is needed because the repo has both git and fossil.

## Frictionless UI

The Frictionless command is `~/.ark/ark ui`. UI skills use `{cmd}` as a placeholder for this.

## Quick Recall

Use /ark to query the knowledge base or write tagged content.

## Frictionless Integration

See [ACCESSING-FRICTIONLESS.md](ACCESSING-FRICTIONLESS.md) for how ark registers Go functions in Lua (type chain, session model, active vs passive execution).

## Code Changes

Go files use tabs for indentation. The Read tool displays them as spaces, so the Edit tool's `old_string` often fails on the first attempt. Use `cat -A` to see the actual whitespace when an edit doesn't match.

Don't forget about gofmt -- no need to sed your way into poverty!
