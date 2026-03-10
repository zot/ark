# Chunk Context Expansion

Source: librarian/specs/chunk-context.md (librarian's research requirement)

## The problem

Search returns individual chunks — slices of files with line ranges
and scores. To evaluate whether a hit actually answers the question,
the librarian needs to see what's around it. Right now that means
`ark fetch` on the whole file, which is expensive for large files
and dumps navigation burden on the agent.

## ark chunks

```
ark chunks <path> <range> [-before N] [-after N]
```

Returns the target chunk plus N neighboring chunks before and after.
Output is JSONL, same format as `ark search --chunks`.

- `<path>` — file path (as returned by search results)
- `<range>` — range label from search results (opaque, strategy-dependent)
- `-before N` — include N chunks before the target (default 0)
- `-after N` — include N chunks after the target (default 0)

Expansion unit is chunks, not lines or bytes. Range labels are
strategy-dependent (`17-35` for markdown, record numbers for JSONL).
The only universal unit is chunk position.

## Output

JSONL, one object per line. Each object has:
- `path` — file path
- `range` — chunk's range label
- `content` — chunk text
- `index` — 0-based position in the file's chunk list

Chunks in positional order (ascending index).

Supports `--wrap <name>` for XML wrapping, consistent with
`ark search` and `ark fetch`.

## Implementation

Calls `microfts2.DB.GetChunks()` directly. Cold-start only (withDB) —
this is a fast, read-only operation. No server proxy needed.

## Why this matters

This is the difference between a librarian who reads the card catalog
and one who walks into the stacks and browses the shelf. The hit tells
you *where* to look; the context tells you *what it means*.
