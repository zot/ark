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

## Tag Value IDs and Storage

Each unique (tag, value) pair gets a sequential numeric ID called a
tvid. Tvids are stable: assigned on first index, reused if the same
(tag, value) persists across re-indexes. The tvid is the join key
between V records (the tag-value index) and EV records (the
tag-value compound embedding).

Tag name embeddings live inline on T records — appended to the
count — so no separate per-tag-embedding prefix is needed. Tag-value
compound embeddings are stored under the EV prefix, keyed by tvid.
F records carry a list of tvids in their value, enabling targeted
V-record cleanup on file removal (read F records for the fileid →
collect tvids → remove the fileid from exactly those V records;
replaces the older full-scan approach in `removeFileidFromAllV`).

The tvid counter is an I record (`next_tvid`).

Record key/value layouts: see [record-formats.md](record-formats.md)
(T, V, F, EV, I sections).

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

## Storage scale

~270 tags × 3082 bytes ≈ 810KB for inline T-record embeddings.
~3857 EV entries with raw float32 arrays at 3072 bytes each.

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

`ark embed text TEXT...` — embed a text string, print the vector as JSON.
Verifies the model loads and produces output.

`ark embed bench tags` — embed all tag values, report timing.
Shows per-value and total time.

`ark embed bench chunks` — read chunks from random indexed files,
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
