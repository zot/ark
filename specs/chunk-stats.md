# Chunk Stats

`ark status --chunks` shows chunk size statistics across the indexed corpus.
This data informs embedding context window tuning — knowing the distribution
of chunk sizes tells you where to set `n_ctx_seq` so most chunks fit without
truncation.

## Flags

`--chunks` activates chunk statistics output on `ark status`. Without it,
behavior is unchanged.

`-files GLOB` rows (repeatable) scope the file set through the same
filter stack `search` uses: `-with` (default) is positive, `-without`
carves out exceptions. Globs follow the project-wide rules in
[main.md](main.md#glob-patterns). When no row is given, all indexed files
are included.

`--tokenize` loads the configured embedding model and counts tokens per
chunk instead of bytes. This is slower (requires model load for its
vocabulary) but gives exact token counts — the unit that matters for
`n_ctx_seq`. Without `--tokenize`, sizes are in bytes.

`--tokenize` **requires the server stopped.** It loads the embedding
model for itself, which is the same thing `embed bench`, `embed
validate` and `embed text` do, and they refuse for the same reason. With
a server running it says so and exits rather than reaching for the
index — see [cli-dispatch.md](cli-dispatch.md). Byte-mode `--chunks` has
no such restriction.

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

## Dispatch

Chunk statistics are index data, so with a server running they come
**from the server** — the CLI sends the filter stack and renders what
comes back. It does not open the index for them, which is the rule in
[cli-dispatch.md](cli-dispatch.md); doing so was what made this command
hang, since `--chunks` runs *after* the ordinary status block has already
been proxied and printed.

The warnings block above the chunk table is index data too, and it rode
in the same local open. It now travels in the status response instead —
see [config-tracking.md](config-tracking.md#status-display).

## Tokenizer context

For `--tokenize`, create a minimal llama context (small `n_ctx`, no
`WithEmbeddings()`) — just enough to access the tokenizer. The model
must be loaded for its vocabulary, but no KV cache or embedding
extraction is needed. Use the same model path as the configured
`[embedding] model` in ark.toml.

## Error cases

- `--tokenize` without a configured `[embedding] model`: print error and exit.
- Files that fail to read (missing from disk): skip silently, they'll
  show up in the normal missing count.
- Zero chunks after filtering: print "no chunks found" and exit the
  chunk stats section.
