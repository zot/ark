# Ark

Digital zettelkasten — files on disk, LMDB index. Hybrid trigram + vector search.

## Build

```
go build -buildvcs=false ./...
```

The `-buildvcs=false` is needed because the repo has both git and fossil.

## Frictionless UI

The Frictionless command is `~/.ark/ark ui`. UI skills use `{cmd}` as a placeholder for this.

## Quick Recall

Use /ark to query the knowledge base or write tagged content.
