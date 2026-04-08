# Tag Value Embeddings

Embed tag values with a local nomic model for sub-second spectral
hypergraph traversal. Given a query, find semantically similar tag
values without an LLM round-trip.

Language: Go. Environment: ark server, gollama with Vulkan build.

## Context

Tag values are the "polarizer angle" in the spectral hypergraph
model (see HYPERGRAPH.md). The current fuzzy matching uses trigram
similarity and word containment — fast but purely lexical. Embeddings
add semantic similarity: "clean chats" finds "conversation cleanup"
even though the words don't overlap.

This complements the Haiku sidecar (spectral search), which handles
creative expansion ("what would a human also check?"). Embeddings
handle the geometric question ("what's near this in meaning space?").

## Model

The embedding model is a GGUF file stored in `~/.ark/`. Config:

```toml
# ark.toml
tag_model = "nomic-embed-text-v1.5.Q8_0.gguf"
```

The path is relative to the database directory (`~/.ark/`). If
empty or the file doesn't exist, embedding is disabled — trigram
fuzzy remains the only option.

The model is 768-dimensional, ~139MB. The README should document
where to download it. Auto-download is a future enhancement.

## Model Lifecycle

The model loads in the Librarian on first embedding query. It stays
warm in memory for subsequent queries. The Librarian's TTL governs
unloading — if no queries arrive within the TTL, the model is
unloaded to free memory. Next query reloads it.

## Tag Value IDs

Each unique (tag, value) pair gets a sequential ID (varint). The
ID is appended to the V record value:

```
V[tag]\x00[value] → packed_fileids + tag_value_id (varint)
```

The tag-value-id is stable: assigned on first index, reused on
re-index if the same (tag, value) pair persists. The ID counter
is stored as an ark LMDB setting (`I` prefix, key: `next_tvid`).

## What Gets Embedded

Both tag names and tag-value compounds are embedded:

- **Tag names**: hyphens converted to spaces before embedding.
  `design-decision` → embed "design decision". Enables tag-name
  similarity search ("show me tags like 'decision'").
- **Tag-value compounds**: `"tagname: value"` embedded with the
  colon preserved. `decision: use LMDB` → embed "decision: use LMDB".
  The colon signals label-value structure to the model. Hyphens
  in the tag name are converted to spaces:
  `design-decision: use LMDB` → embed "design decision: use LMDB".

Tag names are identified by tag-name-id (from T records, stored
as an ark LMDB setting). Tag-value compounds use the tag-value-id
from V records.

## Embedding Storage

Embeddings are stored in new LMDB prefixes:

```
ET[tag_name_id: varint] → float32 vector (768 × 4 = 3072 bytes)
EV[tag_value_id: varint] → float32 vector (3072 bytes)
```

ET records embed tag names (~270 entries). EV records embed
tag-value compounds (~3857 entries). Keys are compact (2 prefix
bytes + 1-5 varint bytes). Values are raw float32 arrays.

## Embedding Lifecycle

**Batch embed after reconcile.** When reconciliation completes and
the model is configured, scan all V records that lack E records
and embed them in a batch. This runs in the write goroutine (same
pattern as indexing) to avoid blocking the main actor.

**Incremental.** When a new V record is created during indexing
(new tag+value pair), queue it for embedding. The next reconcile
batch picks it up.

**Rebuild.** `ark rebuild` drops and regenerates all E records
alongside V records.

## Query Path

```
query value → embed with nomic (warm model, ~50ms)
            → brute-force cosine scan all E records (~1ms for 3857)
            → return top-K (tag, value, score) tuples
            → same shape as FuzzyMatchTags result
```

The Librarian offers both paths: trigram fuzzy (no model, instant)
and embedding similarity (with model, ~50ms). The `--fuzzy` CLI
flag gains a `--embed` counterpart. The HTTP endpoint accepts a
`mode` parameter.

When both are available, the default could be embedding with
trigram as fallback.

## CLI

`ark embed TEXT` — embed a text string, print the vector as JSON.
Verifies the model loads and produces output.

`ark embed --bench tags` — embed all tag values, report timing.
Shows per-value and total time.

`ark embed --bench chunks` — read chunks from random indexed files,
embed them, report timing. Benchmarks the model on realistic
content.

## Build

The Vulkan build of gollama avoids SIGILL on Zen 2 (Steam Deck).
The go workspace includes a local gollama with Vulkan-compiled
llama.cpp. For other platforms, the standard CPU build should work
without Vulkan. The build dependency needs refinement for
distribution — document what's required and test whether Vulkan
is strictly necessary or just a Zen 2 workaround.
