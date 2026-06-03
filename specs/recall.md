# Recall

This spec covers the **chunk-similarity substrate** that the
recall feature stands on — what shipped as `ark connections
recall`. The agent-mediated, tag-shaped, dedup-aware `ark recall`
feature on top of this substrate is still in design; see
[.scratch/CONTEXTUAL-RECALL.md](../.scratch/CONTEXTUAL-RECALL.md)
for the current picture of that layer and the open questions
still being settled.

## Substrate: `ark connections recall`

Given some context, return the top-K chunks the corpus already
has that are most relevant. Reuses the EC substrate from Phase 2A
([find-connections-substrate.md](find-connections-substrate.md))
but projects chunks directly instead of voting them onto tags. By
default drops candidates that carry no V records (tagless chunks
can't contribute tag information downstream); `-all` retains them
when the caller wants pure substrate output.

The shape is **baby-food markdown to stdout** — each call is
self-contained. A consumer (a CLI user, a script, or the future
agent-mediated recall pipeline) reads the surfacing and decides
what to do with it; this substrate has no built-in trigger model,
no agent of its own.

Language: Go (Librarian projection + CLI subcommand). Environment:
ark server with the embedding model loaded on demand.

The substrate does not require Anthropic's `claude` CLI on PATH —
vector-EC + trigram-EC are local-only. `NewLibrarian` succeeds
whether or not `claude` is on PATH; the `Available()` method
reports the claude-dependent capability (spectral expansion)
rather than gating the Librarian's existence. Operations that
don't need claude (recall, embed, substrate passes, tag
embedding) work regardless of claude availability.

This substrate is one of three internal pieces of the full recall
feature (substrate → AI synthesis → dedup filter). It also serves
as the chunk-similarity primitive for other consumers: ambient
watchers, scripts, direct CLI use.

## Why a Sibling Verb in `connections`

`ark connections find` already produces ranked supporting chunks
as evidence on tag-name candidates. The chunk-similarity
primitive could in principle be projected from a `find` result,
but two things diverge:

- **Primary axis.** `find` ranks by tag-name aggregate; this
  substrate ranks by chunk score. The merge math is different.
- **Output shape.** `find`'s `## Proposals` body is sized for the
  workshop's accept loop. The substrate's stencil is sized for a
  downstream consumer's reasoning pass — chunks with content
  snippets and tag evidence, not tag candidates with chunk
  evidence.

So `connections recall` is a sibling of `connections find` in
the same command family, not a downstream consumer of it.

## Public Lua/Go API

```go
// Recall returns the top-K chunks ranked by similarity to the
// given context. Same input shape as FindConnections (chunkID,
// path+range, text). Reuses normalizeInputs.
func (l *Librarian) Recall(inputs []ConnectionsInput, opts RecallOpts) (*RecallResult, error)

type RecallOpts struct {
    K              int    // top-K chunks (default 20, clamped [1, 200])
    IncludeContent bool   // when true, fill RecalledChunk.Content from chunk cache (default true)
    KeepTagless    bool   // when false (default), drop chunks with no V records
                          // during scoring — tagless chunks can't contribute tag
                          // info to downstream tag-shaped recall.
}

type RecallResult struct {
    Chunks  []RecalledChunk `json:"chunks"`
    Warning string          `json:"warning,omitempty"` // "embedding unavailable" when ec-vector substrate skipped
}

type RecalledChunk struct {
    ChunkID      uint64           `json:"chunkID"`
    Path         string           `json:"path"`
    Range        string           `json:"range"`           // chunker's range label (e.g. "12-18"); a chat sub-chunk appends :"snippet"
    Score        float64          `json:"score"`           // max across the four components
    Cell         string           `json:"cell,omitempty"`  // 2×2 grid cell: {main|conversation}-{meaning|tags}
    PerSubstrate ChunkSubstrate   `json:"perSubstrate"`    // four per-component scores after cross-input max
    Tags         []TagValue       `json:"tags"`            // chunk's V records (AllTagsForChunk)
    Content      string           `json:"content,omitempty"` // empty when opts.IncludeContent=false
}

type ChunkSubstrate struct {
    VectorEC   float64 `json:"vectorEc"`   // text-vector
    TrigramEC  float64 `json:"trigramEc"`  // text-trigram
    TagVector  float64 `json:"tagVector"`  // tag axis: EV cosine
    TagTrigram float64 `json:"tagTrigram"` // tag axis: value-string trigram
}
```

The Lua side gets `sys.recall(inputs, opts)` with the same input
canonicalization rule as `sys.findConnections` (typed entries
plus bare-integer-array sugar).

## Algorithm

Recall scores each candidate chunk on **four similarity components** —
`<text-trigram, text-vector, tag-trigram, tag-vector>` (`ChunkSubstrate`) —
then allocates results across a **2×2 grid** rather than one ranked pool.

**Embedding is tag-free.** The EC (meaning) vector is built from
tag-stripped chunk text (`stripArkTags` at embed; see
[chunk-embeddings.md](chunk-embeddings.md)), while the trigram index stays
full-text (tags kept). So the *text* axis is pure prose and the *tag* axis
carries tag semantics, with no double-count (see index.md theme "Text vs
meaning").

For each normalized input:

1. **text-vector** — `SearchChunks(queryVec, 50)`; cosine normalized
   `(cos+1)/2` to `[0,1]`.
2. **text-trigram** — `SearchFuzzy(queryText, 50)`, each candidate
   re-scored as Jaccard `|Tq∩Tc|/|Tq∪Tc|` with a query-coverage floor
   (`trigramCoverageFloor`, `0.1`) short-circuiting marginal overlaps to 0.
3. **tag axis (value→chunk, retrieval)** — score each attached tag-value
   against the input: tag-vector = EV-cosine (normalized), tag-trigram =
   on-the-fly Jaccard of the value string. Pull the chunks carrying the
   top-scoring values via the V hyperedge records (`TagValueChunks`, which
   unions inline V + ext-routed + overlay), each chunk taking the value's
   scores as its tag-axis components. A chunk surfaces *because its tags
   match the input* even when its prose does not. ~1162 short values →
   brute-force scan, no stored TV record (deferred; see future.md).

Each component aggregates across inputs by max.

**2×2 allocation.** Results fill a grid of (main-corpus, conversation) ×
(meaning, tags) — source via the file's chunker strategy (`chat-jsonl` =
conversation), N chunks per cell (default 3, `[recall].per_cell_count`).
Within a cell, rank by that axis's score (meaning = max(text-tri, text-vec);
tags = max(tag-tri, tag-vec)), ≤2 chunks per file, sort `<score, size>` with
the size tiebreak **larger on a vector win, smaller on a trigram win**
(vector cosine is size-robust; Jaccard size-sensitive). A chunk eligible in
both its meaning and tags cell dedups to its stronger cell; underfilling
cells backfill round-robin toward the 4×N target; a final flat sort by
overall score caps at K. Each surfaced result records its originating cell
(`@chunk-cell`) and per-component scores, logged for data-driven tuning.

**Conversation funnel.** A chat-jsonl turn is a whole "chunk" — too coarse.
Its *meaning* is funneled to sub-chunks: pool the markdown sub-chunks of
matched turns, trigram-sort, embed only the top `[recall].chat_funnel_gate`
survivors, vector-check against the input, and surface the matched paragraph
located by `path:range:"<snippet>"` (resolve via `ark chunks`, see
[cli-commands.md](cli-commands.md); drop the snippet for the whole turn). Its
*tags* surface the whole turn (tags attach to turns). Main-corpus chunks
surface whole.

After allocation, resolve each surfaced chunk's path/range/tags
(`ChunkInfo` + `AllTagsForChunk`) and content (if `IncludeContent`) from the
chunk cache. Self-chunkID is excluded from its own results.

## Empty / Error Cases

- Empty input → reject at enqueue with
  `chunkIDs/text/range empty`.
- Unknown chunkID → reject with `unknown chunk <id>`
  (recall uses strict normalize like normal-mode 2A).
- Embedding unavailable → vector-EC skipped; trigram-EC still
  runs. Result carries `Warning: "embedding unavailable"`.
- No chunks match → return `(&RecallResult{Chunks: nil}, nil)`.
  The CLI emits `## Chunks\n\n_no results_\n` on stdout.
- `tag_model` configured but file missing (CLI in-process path)
  → exit non-zero with `error: configured tag_model not found
  at <PATH>`. Distinct from "server not running"; catches typos
  in `ark.toml`.

## CLI

```
ark connections recall <inputs>... [options]
```

Inputs match `ark connections find` — decimal chunkIDs,
`PATH:N-M` / `PATH:N` locators, or bare text. The
`--type chunk|text` flag forces interpretation; without it,
each token is auto-detected.

Options:

| Flag             | Default | Meaning                                                                |
|------------------|---------|------------------------------------------------------------------------|
| `--k N`          | 20      | Top-K chunks (clamped to [1, 200])                                     |
| `--type T`       | (auto)  | `chunk` or `text` — same semantics as `connections find`               |
| `--no-content`   | false   | Omit per-chunk content body (header rows only)                         |
| `--json`         | false   | Emit JSON instead of markdown stencil                                  |
| `-all`           | false   | Keep tagless chunks (default drops them, since tag-shaped consumers can't use them) |
| `--propose`      | false   | Run the statistical derivation pass on returned chunks; persist surviving candidates as RC records. Recall output is unchanged; the proposals are a side effect consumed by the Tag Forge. See [derived-tags.md](derived-tags.md). |

If the server is running, the CLI proxies the `recall` command via HTTP/Unix socket to the server (`POST /recall`), using the warm model if configured.

If the server is not running:
- If `tag_model` is configured in `ark.toml` **and the model file exists**, the CLI exits non-zero with `error: server not running; model configured. Please start the server with: ark serve`.
- If `tag_model` is configured in `ark.toml` **but the model file is missing**, the CLI exits non-zero with `error: configured tag_model not found at <PATH>`. This catches typos and stale paths instead of silently degrading to trigram-only.
- If `tag_model` is not configured, the CLI opens the database locally in-process via `withDB` in read-only mode and executes a local trigram-only recall query.

### Stencil shape (markdown)

```markdown
## Chunks

- @chunk-id: 4711
  @chunk-path: notes/asparagus.md
  @chunk-range: 12-18
  @chunk-score: 0.84
  @chunk-cell: main-meaning
  @chunk-evidence-vector-ec: 0.91
  @chunk-evidence-trigram-ec: 0.62
  @chunk-evidence-tag-vector: 0.55
  @chunk-evidence-tag-trigram: 0.00
  @chunk-tags: cooking, vegetable, recipe, course, cuisine
  - @chunk-tag-value: course: main
  - @chunk-tag-value: cuisine: italian
  > Asparagus risotto pairs well with a dry white wine.
  > The asparagus should be blanched briefly first.

- @chunk-id: 5023
  @chunk-path: notes/risotto-techniques.md
  @chunk-range: 1-7
  @chunk-score: 0.76
  @chunk-cell: main-meaning
  @chunk-evidence-vector-ec: 0.81
  @chunk-evidence-trigram-ec: 0.55
  @chunk-evidence-tag-vector: 0.00
  @chunk-evidence-tag-trigram: 0.00
  @chunk-tags: cooking, technique
  > Risotto starts by toasting the rice in fat, then deglazing.
```

`@chunk-tags` lists every tag *name* attached to the chunk
(comma-separated, names only — values never appear on this
line). Each tag that carries a non-empty value gets its own
markdown sub-list item:

    - @chunk-tag-value: <name>: <value>

`@chunk-tag-value` is itself a legal ark tag (see
[ark-tags.md](../ark-tags.md): a tag is `@name: value`); its
value is the literal text `<name>: <value>`, which an agent
reading the stencil can split on the first `: ` to recover the
original tag's name and value. Splitting values out as list
items avoids the quoting trouble of packing
`name=value` pairs (whose values can contain spaces and colons)
into a single line. Tags without values appear only in
`@chunk-tags`; tags with values appear in both places.
Sub-items are emitted in the same order tags appear in
`@chunk-tags`.

Content lines are quoted with `> ` (markdown blockquote) so an
agent reading the stencil sees the chunk text as quoted prose,
not as freeform markdown that competes with the surrounding
stencil structure.

When `--propose` is set ([derived-tags.md](derived-tags.md))
and a surfaced chunk has accumulated RC records, a
`@chunk-proposed-tags` line is added after `@chunk-tags`,
carrying comma-separated derived-tag candidates in similarity-
descending order with parenthesized cosine scores:

    @chunk-tags: cooking, vegetable, recipe
    @chunk-proposed-tags: priority (0.72), status (0.61), axis (0.58)

The score is the chunk-EC ↔ tag max cosine — over the tag's ED
definitions *and* its EV values (R2911) — the same value the
propose pass uses for its threshold cut. The line is
omitted (not emitted empty) for chunks with no RC records. See
[derived-tags.md](derived-tags.md) for the derivation pass and
the `ProposedTags` / `ProposedTagScores` JSON fields.

When no chunks match:

```markdown
## Chunks

_no results_
```

When embedding is unavailable, the warning lands above the
section:

```markdown
@recall-warning: embedding unavailable

## Chunks
...
```

### JSON shape (`--json`)

Single JSON object matching `RecallResult` exactly:

```json
{
  "chunks": [
    {
      "chunkID": 4711,
      "path": "notes/asparagus.md",
      "range": "12-18",
      "score": 0.84,
      "cell": "main-meaning",
      "perSubstrate": {"vectorEc": 0.91, "trigramEc": 0.62, "tagVector": 0.55, "tagTrigram": 0.0},
      "tags": [{"tag": "cooking"}, {"tag": "vegetable"}, {"tag": "recipe"}],
      "content": "Asparagus risotto pairs well..."
    }
  ]
}
```

## What This Substrate Does Not Do

These are intentional non-features of the chunk-similarity
substrate. They belong to the agent-mediated `ark recall`
feature above this layer (see
[.scratch/CONTEXTUAL-RECALL.md](../.scratch/CONTEXTUAL-RECALL.md)),
not here.

- **No agent inside ark.** No Claude call, no sidecar. Cosine +
  trigram-Jaccard + V-record lookup. Deterministic on its inputs.
- **No tmp:// doc.** Stdout-only. The agent-mediated layer (or
  a watcher) decides whether to write a tmp:// doc; this
  substrate doesn't commit.
- **Tag axis is value→chunk, not ED-def ranking.** Recall's tag
  axis pulls chunks by their *values* (EV value→chunk, R2905); it
  does not rank chunks by scoring against tag *definitions* (ED).
  ED is consulted only by the `--propose` derivation pass
  (alongside EV; see [derived-tags.md](derived-tags.md)), never to
  rank recall results.
- **No theme detection, no acceptance loop.** Recall surfaces
  chunks; the workshop's accept flow belongs to find-connections.
  (`--propose` writes derived-tag candidates as a *side effect* —
  see [derived-tags.md](derived-tags.md) — but does not change what
  recall surfaces.)
- **No fancy snippet extraction.** Chunk content is emitted whole
  (or omitted via `--no-content`). The substrate doesn't compute
  context-relevant snippets; the chunk content *is* the
  baby-food.
- **No conversation memory.** Each call is independent. The
  substrate doesn't dedupe; dedup is the agent-mediated layer's
  job.

## Performance

Target: under 200 ms wall time for a small input set (≤ 5
inputs, ≤ 50K EC chunks) on the Steam Deck. Breakdown:

- Per-input vector_ec pass: ~150 ms (same as 2A — dominated by
  the EC cursor walk via `SearchChunks`).
- Per-input trigram_ec pass: ~10 ms (microfts2 fuzzy).
- Per-chunk path / range / tag resolution post-merge:
  ~5 ms total at K=20.
- Per-chunk content read (chunk cache): ~1 ms each, capped at K.

Faster than 2A by the absence of ED scan and tag-vote
aggregation. Total path:range expansion and ad-hoc text
embedding follow the same cost model as 2A.

## Test Strategy

- `Recall([]{Text: "...""}, opts)` against a small corpus returns
  ranked chunks; first chunk has the highest aggregate score.
- `Recall([]{ChunkID: c1}, opts)` skips c1 itself from the
  output (self-exclusion).
- Mixed inputs (chunkID + path:range + text) produce a merged
  ranking; per-chunk evidence includes only the substrates that
  contributed.
- `--type text 42` treats `42` as text, not as chunkID (parity
  with 2A's --type flag).
- Embedding unavailable → trigram-only result with warning
  field set.
- Self-chunk exclusion: an input chunk c1 never appears as one
  of its own recalled chunks.
- `--no-content` omits the Content field / quoted body.
- `--json` parses as valid `RecallResult`.

## Convergence with the Recall Feature

This substrate is the chunk-similarity step of the
agent-mediated recall pipeline. The full pipeline:

1. Caller embeds a turn (conversation context).
2. Caller runs `ark connections recall <turn>` → chunks + tags.
3. Caller DMs (turn + chunks + tags) to the recall agent.
4. Recall agent LLM step: which tags are relevant *and* not yet
   discussed?
5. Recall agent DMs tag suggestions back to the target session.

The substrate (step 2) provides absolute-scored EC candidates
restricted to tag-bearing chunks by default. The agent layer
(steps 3–5) is still being designed; see
[.scratch/CONTEXTUAL-RECALL.md](../.scratch/CONTEXTUAL-RECALL.md).
The V4 ambient watcher uses the same pipeline with a JSONL tail
driving step 1 instead of a user-facing tool call.
