# Chunk Stats

`ark status --chunks` shows chunk size statistics across the indexed corpus.
This data informs embedding context window tuning — knowing the distribution
of chunk sizes tells you where to set `n_ctx_seq` so most chunks fit without
truncation.

## Flags

`--chunks` activates chunk statistics output on `ark status`. Without it,
behavior is unchanged.

`--filter-files GLOB` and `--exclude-files GLOB` (repeatable) scope the
file set. Same semantics as search: filter-files is a positive filter
(only matching files), exclude-files carves out exceptions. When neither
is specified, all indexed files are included.

`--tokenize` loads the configured embedding model and counts tokens per
chunk instead of bytes. This is slower (requires model load for its
vocabulary) but gives exact token counts — the unit that matters for
`n_ctx_seq`. Without `--tokenize`, sizes are in bytes.

## Output

When `--chunks` is active, after the normal status output, print a
chunk statistics table. First row is "all" (aggregate), then one row
per strategy sorted alphabetically. Header line labels the unit
(bytes or tokens).

```
chunk sizes (bytes):
strategy    count   min   max  mean median   p90   p95   p99
all          4523    12  3847   482    391  1024  1580  2891
chat-jsonl   1800    12  2100   340    280   780  1050  1800
lines         623    15  1200   290    220   600   800  1100
markdown     2100    24  3847   610    498  1280  1900  3100
```

With `--tokenize`, the header says "tokens" and names the model:

```
chunk sizes (tokens, nomic-embed-text-v1.5):
strategy    count  min   max  mean median  p90   p95   p99
all          4523    4  1102   138    112   310   460   830
...
```

Columns are right-aligned and padded to the widest value in each column.

## Data source

For each file in scope, use `DB.AllChunks(path)` to get chunk content.
`len(chunk.Content)` gives byte size. With `--tokenize`, tokenize each
chunk's content through the model's tokenizer and count the resulting
tokens.

## Tokenizer context

For `--tokenize`, create a minimal llama context (small `n_ctx`, no
`WithEmbeddings()`) — just enough to access the tokenizer. The model
must be loaded for its vocabulary, but no KV cache or embedding
extraction is needed. Use the same model path as the configured
`tag_model` in ark.toml.

## Error cases

- `--tokenize` without a configured `tag_model`: print error and exit.
- Files that fail to read (missing from disk): skip silently, they'll
  show up in the normal missing count.
- Zero chunks after filtering: print "no chunks found" and exit the
  chunk stats section.
