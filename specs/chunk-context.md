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
- `attrs` — the chunk's attributes, when it has any
- `missing` — present and true when the chunk is stale (below); `content`
  is then empty and `attrs` absent

Chunks in positional order (ascending index). With `--wrap`, a stale
chunk's element holds the placeholder text instead of its content.

## A stale chunk

A chunk can be in the index while the file's current chunker no longer
produces it: the chunking rules changed after the file was indexed.
Retrieval reports such a chunk as **missing**. It keeps its place in the
file's chunk list (its range and position), but its text is unavailable.

Wherever ark reads a chunk from its file, a missing chunk is absent. Its
content and its stored attributes are not used: not matched against an
anchor, not measured, not embedded, not used to place a reference, not
used to draw a PDF overlay. The attributes were recorded for a chunk the
file no longer has, so they are as stale as its text. A search or recall
hit whose text comes back unavailable is dropped, not returned empty.

What only the index holds about a missing chunk (its tag values, its
vector, its trigram postings) is not checked: telling that a chunk is
missing means re-chunking its file, which is too costly at every read.
Those go when the file is re-chunked. Until then a stale chunk can still
affect ranking, but it is never shown or returned.

Anything that displays chunks to a reader shows this placeholder where
the chunk would be, so the file keeps its shape and the chunk's id, pins
and tags still have a place to land:

> This chunk's text is unavailable: the file was indexed under older
> chunking rules. `ark refresh --force PATH` re-chunks it.

`ark refresh --force` re-chunks the file in place, and chunks whose text
did not change keep their ids.

Supports `--wrap <name>` for XML wrapping, consistent with
`ark search` and `ark fetch`.

## Implementation

Calls `microfts2.DB.GetChunks()` directly. Dispatches via `proxyOrLocal`:
it proxies to `POST /chunks` when a server holds the single-process index,
otherwise cold-starts locally — a fast, read-only operation either way.

## Why this matters

This is the difference between a librarian who reads the card catalog
and one who walks into the stacks and browses the shelf. The hit tells
you *where* to look; the context tells you *what it means*.
