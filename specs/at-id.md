# `@id` Indexing

`@id: UUID` declares identity for a chunk. It is a regular tag — no
special record type, no extra wiring beyond the V/F/T machinery the
chunkid migration already put in place. The semantic weight comes
from how `@link` (point 5) and `@ext` (points 6–9) consume it.

## Behavior

`@id: UUID` extracts and indexes like any other tag value:

- `V[id]\0[UUID]\0[tvid]` → packed chunkids that carry the id
- `F[chunkid][id]` → count + tvid trail for that chunk's id values
- `T[id]` → global count

Tag extraction (`ExtractTagValues`) and chunkid-keyed writes
(`Store.UpdateTagValues`) handle `@id:` without modification.

## Granularity is the chunker's choice

The chunk that holds an `@id:` tag *is* the resolved target. There is
no separate "section anchor" concept — the markdown chunker is
already heading-scoped, so:

- `@id` in a file's preamble (text before any heading) → preamble
  chunk. Resolves to the whole file's leading section.
- `@id` under a heading → that heading's chunk. Resolves to the
  section.

Other strategies inherit their own granularity:

- `lines`: one chunk per ~N lines; `@id` resolves to that window.
- `chat-jsonl`: one chunk per JSON line; `@id` is unusual here but
  would resolve to the message.
- PDF: one chunk per heading-or-paragraph block (per PDF chunker).

The contract is: **whatever the chunker emits as the chunk
containing the `@id:`, that chunk is the resolved target**.

## Resolution chain

Given a UUID, the resolution path is:

```
TvidMap.Lookup("id", UUID)              → tvid
Store.TagValueFiles("id", UUID)         → []chunkid
microfts2 CRecord.FileIDs (per chunkid) → fileid
microfts2.FileInfoByID(fileid).Names    → path
microfts2 FileChunkEntry.Location       → range within file
```

`Store.TagValueFiles` already returns chunkids after the chunkid
migration, and the in-memory `TvidMap` resolves the tvid in O(1).
The chunkid → (fileid, path, location) leg lives in microfts2 and is
exercised today by content rendering.

## Multiple chunks with the same UUID

The same UUID across multiple chunks resolves to *all* matching
chunks. `Store.TagValueFiles` returns the chunkid list. Callers that
want a single target choose by policy (first, all, error) — `@link`
(point 5) defaults to all matches per HYPERGRAPH conventions.
Authoring tools should treat duplicate UUIDs as a warning, but the
index does not enforce uniqueness.

## tmp:// content

`@id` works the same way for `tmp://` documents. Tag extraction runs
through the same `ExtractTagValues` pipeline; `TmpTagStore` stores
tvids per chunk; `Store.TagValueFiles` unions persistent and overlay
results. A `tmp://` document can declare a UUID that other content
links to during the server's lifetime — useful for ephemeral
schedule logs, agent DMs, and watchdog reports that want stable
references within a session.

## Out of scope

- `@link` rendering (point 5) — consumes `@id` resolution.
- `@ext` parsing and storage (points 6–7).
- Hash-based fallback when a UUID lookup fails — nice-to-have for
  `@link: path` resilience, deferred until `@link` lands.
- Uniqueness enforcement on `@id` values across the index. Out of
  scope; treat duplicates as authoring concern.
