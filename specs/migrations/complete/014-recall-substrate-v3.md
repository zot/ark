# Migration: recall substrate v3 — tag-stripped text, tag axis, 2×2 allocation

Language: Go. Environment: built-in subsystem of `ark serve` — the recall
substrate (`librarian.go`, `connections.go`, `recall.go`), the per-chunker
content transform (chunker integration / indexer), the derived-tag propose
pass (`recall.go` / derived-tags), and `[recall]` config in `ark.toml`.
A one-time re-index + re-embed is required.

The settled design lives in [`.scratch/SIGNAL.md`](../../.scratch/SIGNAL.md)
(Ideas 1–3); this spec carves **landing 1** — everything the recall
*results* read from. Ideas 4–6 (the `ark guarded` bloodhound, the
`<CONSIDERING/>` watermark, and the Sherlock /ark-usage work) are later
landings.

## Problem (state A)

Recall today scores chunks on **content only**, over text that still
contains tags, and ranks them in **one undifferentiated pool**:

- **Tags pollute the embedding.** A chunk's `@tag: value` lines are part
  of the text that gets EC-embedded and trigram-indexed. Tag semantics
  already live in their own records (ED for tag definitions, EV for tag
  values), so baking them into the content embedding double-represents
  them on the wrong axis — and meta-tags (`@top-priority:`,
  `@favorite-food:`) that say nothing about the prose still pull the
  chunk toward every other chunk that carries them (a "taggedness"
  direction in EC space).
- **No tag axis in recall.** `Librarian.Recall` runs EC-vector +
  trigram-Jaccard on content (recall.md); it does **not** consult the
  ED/EV tag substrates. A conversation *about* a tag concept ("Italian
  food") can't surface a chunk that's *tagged* `@cuisine: italian` unless
  the prose also matches — the power of tagging isn't reachable from
  recall.
- **One pool, prone to flooding.** Results are a single ranked list
  (top-K by aggregate score). One prolific file or one material type
  (chat-jsonl — known to flood) can monopolize the top, and conversation
  "chunks" are whole turns, too coarse to score precisely.
- **Propose pass is ED-only.** Derived-tag proposals score chunk-EC
  against tag-**ED** (definitions) only (`min_propose_similarity` =
  "Chunk-EC ↔ tag-ED cosine floor"); a chunk that resembles an existing
  *value* but not the tag's *definition* gets no proposal.

## Solution (state B)

Per SIGNAL.md Ideas 1–3. Five coupled changes; implement in this order.

### 1. Tags stay in full-text; only the EC embedding strips them → re-embed

The trigram index is **full-text**: chunk content is indexed verbatim, ark
tags and all, so an FTS query for `@note: bubba` still finds the chunk that
literally carries it — a full-text index that drops its own text lies about
its corpus. **Stripping happens on the meaning axis only.** ark removes ark
tag spans (`stripArkTags`) from the chunk text it feeds to the EC embedder
(`BatchEmbedChunks`) — full-line tags also remove their newline, inline tags
keep the rest of the line — so the chunk *embedding* is tag-free while the
trigram index is not. A chunk that is **all** `@tag:` lines strips to empty
and is skipped at embed time (the tag axis carries it).

microfts2 holds no ark-tag logic. The per-chunker ContentTransform hook
(added in commit c5f89d9, made index-only in 586a0ae) was **rolled back** —
it asked a full-text index to hide text from itself. microfts2 now indexes
original content, retrieval returns original content, and the chunk-dedup
hash is SHA-256 over original content. Per-chunk tags reach the indexer by
**re-extracting** (`ExtractTagValues`) from the chunk's original content in
`WithIndexedChunkCallback` (the callback carries original content; dedup'd
chunks share a chunkid, so the one fire that lands writes the shared F/V
records); file-level tags + defs are re-extracted from the source bytes.
ark's `F`/`V` records are the canonical per-chunk tag store, so no tags are
duplicated into microfts2 Attrs. The conversion is a one-time **re-index +
re-embed** (`ark rebuild`): re-index so the trigram index keeps the tag text
it previously stripped, re-embed so the EC side is tag-free.

### 2. Tag axis in recall (retrieval) + 4-component score

`Librarian.Recall` gains a **tag axis** alongside content. A chunk can be
surfaced *because its tags match the conversation*, not only its prose.
Mechanically, **value → chunk**: score each tag-value against the input
(vector via EV; trigram computed on the fly from the short value
strings), take the top values, and pull in the chunks carrying them via
the **V hyperedge records**, with the value's score as the chunk's
tag-axis component. Tag-value trigrams are computed on the fly (~1162
values, all short) — **no stored TV record** (deferred; SIGNAL `@watch`).

Each chunk thus has up to four similarity components:
`<text-trigram, text-vector, tag-trigram, tag-vector>`.

### 3. 2×2 allocation (source × axis)

Results are allocated across a **2×2 grid** — (main corpus, conversation)
× (meaning, tags) — **N per cell, default 3, configurable** via a
`[recall]` key. Within a cell: rank by that axis's score (`meaning` =
max(text-tri, text-vec); `tags` = max(tag-tri, tag-vec)), **≤2 chunks per
file**, sort by `<final score, size>` — the size tiebreak going **larger
when the winning score was vector, smaller when it was trigram** (vector
cosine is size-robust so a big high-scorer is densely on-topic; trigram
Jaccard is size-sensitive so a small high-scorer is a concentrated hit;
SIGNAL Q2.1). Per-axis budget means content and tags never compete in one
score, so the cross-axis merge question dissolves; the consuming
secretary/assistant does the cross-cell judgment.

- **Dedup across cells:** a chunk matching both content and its own tags
  keeps its stronger cell; the other backfills.
- **Backfill:** if a cell underfills (e.g. the sparse conversation×tags
  cell), redistribute across the four cells to reach the target.
- **Instrumentation:** the recall log records per-cell contribution +
  scores, so allocation/weighting can be tuned from real data rather than
  guessed (SIGNAL Q-alloc.2 / Q3.3).

### 4. chat-jsonl sub-chunk funnel

A JSONL "chunk" is a whole turn — too coarse. For the conversation pool,
re-chunk matched turns with the markdown chunker and rank **sub-chunks**:
trigram-filter → sub-chunk → trigram-sort → embed only the survivors →
vector-check against the input → sort `<final score, size>`. Surfaces the
specific paragraph that matched, at bounded embed cost. The pre-embed
gate (how many sub-chunks survive trigram before the embed step) is a
config knob, logged, tuned from data (SIGNAL Q2.3).

The surfaced sub-chunk's locator is `PATH:RANGE:"<snippet>"` — the turn's
path:range plus a string anchor (the matched paragraph's first line), the
same anchor grammar `@ext` uses (at-ext-parsing.md). `ark chunks
PATH:RANGE:"<snippet>"` resolves it; dropping the snippet fetches the whole
turn (zoom-out for fuller context).

@future: chat-jsonl is append-only ⇒ chunk ranges are stable, so a positional
locator (`:N` sub-index or the markdown line-range `:5-7`) would round-trip
reliably and resolve by slicing instead of re-scanning for the snippet —
deferred behind the string-anchor form. See [../future.md](../future.md).

### 5. Propose pass gains an EV leg

The derived-tag propose pass scores chunk-EC against tag-**EV** (existing
values) in addition to tag-**ED** (definitions), so a chunk earns a tag
for resembling an existing value, not only the definition. Same
`min_propose_similarity` floor for the EV leg to start (split into its
own knob later only if the data wants it). Extends the existing O(N·M)
ED scan (design.md O115) by the value set; consistent with O115's "simple
first, index later."

## Migration (full re-index + re-embed; no ark record-format change)

Part 1 changes what the trigram index and EC are built from. The trigram
index now keeps the tag text it previously stripped (the rolled-back hook
had stripped it); the EC side is rebuilt from tag-free text. So the one-time
conversion is a full **`ark rebuild`** (re-index + re-embed), not a re-embed
alone — re-indexing rewrites the stored content with tags kept, then
re-embedding makes the EC side tag-free. Chunk identity is the SHA-256 of the
original content (no Attrs in the hash), so a tag edit changes the content
and re-indexes naturally. No ark record class or key encoding changes; EV /
ED / V are unaffected (tags already lived in their own records). Scoring
(parts 2–4) and the propose EV leg (part 5) take
effect with the new binary.

The rebuild is **operator-run by Bill** — the code change does not
trigger it, and no session should auto-`ark rebuild` the corpus.

## What this does NOT do

- **No stored TV (tag-value trigram) record** — computed on the fly,
  deferred behind SIGNAL's `@watch` until the corpus is tagged densely
  enough to need it.
- **No vector index** for the tag-axis or propose scans — brute-force
  over ~1162 values, consistent with O115's "simple first; HNSW is the
  later lever."
- **No per-pool weighting policy baked in** — equal cells + backfill +
  logging; weighting decided later from data.
- **No agent / assistant changes** — `ark guarded` (Idea 4) and the
  `<CONSIDERING/>` / Sherlock work (Ideas 5–6) are later landings.

## Test strategy

- **embed-time tag-strip** — `stripArkTags` on a full-line `@tag: v` and an
  inline tag yields tag-free text (full-line newline gone, inline line kept;
  mentions untouched), and a chunk that is all `@tag:` lines strips to empty
  and is skipped at embed (TestStripArkTags + an embed test). The trigram
  index keeps the tag text (full-text search for a literal tag still hits).
  Per-chunk tags re-extracted from original content in the indexed callback
  still land in F/V records (harness indexing tests).
- **tag axis retrieval** — an input matching a value surfaces a chunk
  carrying that value via V, even when the chunk's prose doesn't match
  (the `@cuisine: italian` case).
- **4-component / 2×2** — results fill the four cells; ≤2 per file;
  larger-on-vector-tie, smaller-on-trigram-tie; dedup keeps the stronger
  cell; backfill fills a starved cell.
- **chat funnel** — a long matched turn surfaces the relevant sub-chunk,
  not the whole turn; only trigram-survivors are embedded.
- **propose EV leg** — a chunk resembling an existing value (not the
  definition) gets that tag proposed; the ED-only parity cases still hold.

## On completion (folding plan)

1. **`specs/recall.md`** — rewrite the Algorithm (4-component score, tag
   axis value→chunk), the output stencil, and add the 2×2 allocation +
   chat funnel; note tag-free embedding.
2. **`specs/derived-tags.md`** — add the EV leg to the propose pass.
3. **`specs/chunkers.md`** (summary spec) — note the trigram index is
   full-text (tags kept); the microfts2 ContentTransform hook was rolled
   back. Within-file duplicate *defs* stay collapsed (deferred, ARK-STATE
   #14).
4. **`specs/chunk-embeddings.md`** — EC is built from tag-free text
   (`stripArkTags` at embed); all-`@tag:` chunks are skipped.
5. **`specs/config.md`** — new `[recall]` keys (per-cell count, chat-funnel
   gate, any tiebreak/weight knobs).
6. **`specs/record-formats.md`** — confirm unchanged (no new record).
7. **Retire / reword** the state-A recall-scoring requirements (single-pool
   top-K; EC-includes-tags; propose ED-only) → the new Rn.
8. `minispec update migration-complete recall-substrate-v3`.
