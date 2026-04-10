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

The model loads eagerly after reconcile to batch-embed any V/T
records missing embeddings (T records without inline vectors, V
records without EV records). This amortizes the load cost into
startup rather than penalizing the first query. After the batch
completes, the model stays warm — the TTL timer starts from the
end of the batch, so a query arriving shortly after startup hits
a warm model. If no queries arrive within the TTL, the model
unloads to free memory. Next query reloads it.

If deferred to first query, users who query infrequently would
always hit cold-start latency, driving them to query even less.

## Tag Value IDs

Each unique (tag, value) pair gets a sequential ID (varint). The
ID is part of the V record key:

```
V[tag]\x00[value]\x00[tvid: varint] → packed_fileids
```

The tag-value-id is stable: assigned on first index, reused on
re-index if the same (tag, value) pair persists. The ID counter
is stored as an ark LMDB setting (`I` prefix, key: `next_tvid`).

Forward lookup: prefix scan `V[tag]\x00[value]\x00` returns one
record with the tvid in the key suffix. Reverse lookup: scan V
prefix, parse tvid from each key.

## F Records Carry TVIDs

F records track which tags appear in a file. The value is extended
to include tvids:

```
F[fileid:8][tag] → count:4bytes + packed tvid varints
```

On file removal or re-index, read F records for the fileid to get
all tvids, then remove the fileid from exactly those V records.
This replaces the current full-scan approach in `removeFileidFromAllV`.

## What Gets Embedded

Both tag names and tag-value compounds are embedded. Hyphens in
tag names are converted to spaces in all embedding contexts — this
lets the model leverage individual word semantics instead of treating
hyphenated compounds as opaque tokens.

- **Tag names**: `design-decision` → embed "design decision".
  Enables tag-name similarity search ("show me tags like 'decision'").
- **Tag-value compounds**: `"tagname: value"` with colon preserved.
  `design-decision: use LMDB` → embed "design decision: use LMDB".
  The colon signals label-value structure to the model.

## Embedding Storage

Tag name embeddings are stored inline in T records. Tag-value
compound embeddings use a separate EV prefix:

```
T[tag_name] → count:4bytes + optional float32 vector (3072 bytes)
EV[tvid: varint] → float32 vector (3072 bytes)
```

T records already exist for every tag. The embedding vector is
appended to the count — if `len(value) == 4`, no embedding yet;
if `len(value) == 4+3072`, embedding is present. ~270 tags × 3082
bytes ≈ 810KB total. No separate ET prefix needed.

EV records use the compact numeric tvid from V records (~3857
entries). Values are raw float32 arrays.

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

Two-step narrowing — tags first, then values:

```
query → embed with nomic (warm model, ~50ms)
      → cosine scan T record embeddings (~270 tags, <1ms)
      → top-K matching tags
      → cosine scan EV records only for those tags (~50-100 values)
      → return top-K (tag, value, score) tuples
      → same shape as FuzzyMatchTags result
```

This avoids scanning all ~3857 EV records. Tag-level narrowing
reduces the search space by ~10x. The tag embedding score can also
weight the final result — a value match under a weakly-matching
tag is less interesting than one under a strongly-matching tag.

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

## Use vs Mention Filtering

Tags that are mentioned (discussed, quoted, exemplified) rather
than used (annotating content) should be skipped during extraction.
Mentioned tags produce no V, T, F, or EV records — they are prose
about tags, not tags.

Four heuristics, applied in order. If any matches, the tag is a
mention and is skipped.

### 1. No preceding space (all strategies)

A `@` that is not at the start of a line and not preceded by
whitespace is part of a larger token — an email address, a
compound identifier, etc. Not a tag. Since tag values capture
to end of line, tags inside punctuation like `(@tag: value)`
produce malformed values anyway.

- `user@domain:` → not a tag (no space before `@`)
- `foo@note: bar` → not a tag
- `(@note: value)` → not a tag (value would include `)`)
- `@note: bar` at line start → tag
- `see @note: bar` after space → tag

### 2. Odd quote count (all strategies)

Count backtick and double-quote characters before the `@` on
the same line. If the count is odd, the tag is inside a quote —
it's a mention.

- `@decision: use LMDB` → annotation (zero quotes before)
- `` `@decision: use LMDB` `` → mention (one backtick before)
- `"@decision: use LMDB"` → mention (one double-quote before)

### 3. Fenced code block (markdown strategy only)

Tags inside fenced code blocks (``` or ~~~) are examples or
documentation, not annotations. Track fence state across lines
within the chunk: count fence delimiters above the current line,
odd = inside fence.

### 4. Indented code block (markdown strategy only)

Lines starting with 4+ spaces or a tab (in markdown context)
are code blocks. Tags on these lines are mentions.

### Interaction with strategies

Heuristics 1 and 2 apply to all indexing strategies (markdown,
lines, chat-jsonl, bracket, indent). Heuristics 3 and 4 apply
only to the markdown strategy — indentation and fences have no
special meaning in code files or chat logs.

## Build

Two build issues resolved:

1. **GGML_NATIVE=OFF** — llama.cpp's `-march=native` enables
   instructions that Zen 2 reports but can't execute (SIGILL).
   Disabling native detection and using explicit AVX/AVX2 flags
   fixes the crash on all platforms.

2. **Vulkan GPU acceleration** — offloads embedding compute to
   the GPU. 45ms/chunk on GPU vs 235ms/chunk on CPU (5x). The
   binary is larger (69MB vs 28MB) due to SPIR-V shader blobs,
   but the performance gain justifies it. Only runtime dependency
   is `libvulkan.so.1` (standard on GPU-capable systems).

The gollama build is statically linked (BUILD_SHARED_LIBS=OFF)
so the ark binary is self-contained — no shared lib management.
