# Vector Benchmark

Benchmark in-process embedding performance against real chunks from
the LMDB index. The goal is to answer: "Is vector search viable at
ark's scale on this hardware?"

The current architecture shells out to an external command for every
embedding (microvec's Embedder). This means every embedding pays
model-load cost. For benchmarking, we need the model loaded once and
held warm across all embeddings, which requires in-process embedding
via gollama (the llama.cpp Go binding already used in microvec's CLI).

## `ark vec bench`

Loads a GGUF embedding model, pulls chunks from the existing LMDB
index, embeds each one in-process, and reports timing statistics.

- `--model PATH` — path to GGUF model file (required)
- `--n N` — number of chunks to embed (default 10)
- `--random` — select chunks randomly instead of sequentially
  (default: sequential from start of index)
- `--ctx N` — context window size in tokens (default 2048)
- `--prefix TEXT` — text prepended before each chunk (e.g.
  "search_document: " for nomic models)

Output: per-chunk timing, then summary stats (min, max, mean, median,
total, chunks/sec). Include chunk byte length in per-chunk output so
we can see if size correlates with embedding time.

The model is loaded once at command start. Only the embedding
computation is timed, not model loading. Model load time is reported
separately.

This command does NOT write to the database. It is read-only: pull
chunks, embed them in memory, report timings, exit.

## `ark vec bench-search`

Same model loading, but benchmarks the full search path: embed a
query, then brute-force cosine similarity against stored vectors.

- `--model PATH` — path to GGUF model file (required)
- `--query TEXT` — the search query to embed and compare
- `--k N` — number of results to return (default 10)
- `--prefix TEXT` — query prefix (e.g. "search_query: ")

This only works if vectors are already stored in the index (which
they probably aren't for most of our corpus). Report how many
vectors exist and how long the search took.

## Language and Environment

Go. Uses gollama (github.com/godeps/gollama) for in-process llama.cpp
embedding. Reads chunks from the existing LMDB index via microfts2.
