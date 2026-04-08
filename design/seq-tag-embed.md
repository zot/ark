# Sequence: Tag Value Embedding

**Requirements:** R1274-R1301

## Query Path (warm model)

```
Browser/CLI       Server           Librarian              Store/LMDB
  |                  |                  |                      |
  |--embed query---->|                  |                      |
  |                  |--EmbedSimilar--->|                      |
  |                  |  TagValues()     |                      |
  |                  |                  |--EmbedQuery(text)     |
  |                  |                  |  model.Embed() ~50ms  |
  |                  |                  |                      |
  |                  |                  |--scan EV records----->|
  |                  |                  |  cosine similarity    |
  |                  |                  |<--top-K (tvid,score)--|
  |                  |                  |                      |
  |                  |                  |--resolve tvid→tag,val |
  |                  |                  |  + paths from V recs  |
  |                  |                  |<--TagMatch tuples-----|
  |                  |<--[]TagMatch-----|                      |
  |<--JSON results---|                  |                      |
```

## Cold Start (model not loaded)

```
Browser/CLI       Librarian              gollama
  |                  |                      |
  |--EmbedQuery()--->|                      |
  |                  |--loadModel()         |
  |                  |  llama.LoadModel()--->|
  |                  |  model.NewContext()-->|
  |                  |<--model + ctx--------|
  |                  |--start TTL timer     |
  |                  |--model.Embed()------>|
  |                  |<--[]float32----------|
  |<--vector---------|                      |
```

## TTL Expiry (unload model)

```
                  Librarian              gollama
                      |                      |
                      |--timer fires         |
                      |--unloadModel()       |
                      |  ctx.Close()-------->|
                      |  model.Close()------>|
                      |  nil model, ctx      |
```

## Batch Embed After Reconcile

```
Server              DB Actor            Store           Librarian
  |                    |                   |                |
  |--reconcile done--->|                   |                |
  |                    |--enqueue embed    |                |
  |                    |  batch (write     |                |
  |                    |  goroutine)       |                |
  |                    |                   |                |
  |                    |  (write goroutine)|                |
  |                    |--scan T records-->|                |
  |                    |  missing ET recs  |                |
  |                    |<--tag names-------|                |
  |                    |                   |                |
  |                    |--scan V records-->|                |
  |                    |  missing EV recs  |                |
  |                    |<--tag+value pairs-|                |
  |                    |                   |                |
  |                    |--EmbedQuery()-----|--------------->|
  |                    |  (for each        |                |
  |                    |   missing entry)  |                |
  |                    |<--vectors---------|----------------|
  |                    |                   |                |
  |                    |--write ET/EV----->|                |
  |                    |  records          |                |
```
