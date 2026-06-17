# Sequence: Tag Value Embedding

**Requirements:** R1274-R1301, R1315, R1316, R2154-R2158

## Query Path (warm model, two-step narrowing)

```
Browser/CLI       Server           Librarian              Store/index
  |                  |                  |                      |
  |--embed query---->|                  |                      |
  |                  |--EmbedSimilar--->|                      |
  |                  |  TagValues()     |                      |
  |                  |                  |--EmbedQuery(text)     |
  |                  |                  |  model.Embed() ~50ms  |
  |                  |                  |                      |
  |                  |                  |  Step 1: narrow tags  |
  |                  |                  |--ScanTagNameEmbed---->|
  |                  |                  |  cosine ~270 T recs   |
  |                  |                  |<--top-K tags----------|
  |                  |                  |                      |
  |                  |                  |  Step 2: scan values  |
  |                  |                  |--scan EV records----->|
  |                  |                  |  only for matched tags|
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
Browser/CLI       Librarian              yzma
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
                  Librarian              yzma
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
  |                    |--MissingTagName-->|                |
  |                    |  Embeddings()     |                |
  |                    |<--[]string tags---|                |
  |                    |                   |                |
  |                    |--MissingTagValue->|                |
  |                    |  Embeddings()     |                |
  |                    |<--[]uint64 tvids--|                |
  |                    |                   |                |
  |                    |--ScanVRecordTvids |                |
  |                    |<--tvid→{tag,val}--|                |
  |                    |                   |                |
  |                    |  for each missing tag:             |
  |                    |--EmbedQuery()-----|--------------->|
  |                    |  "tag name"       |   (hyphens→    |
  |                    |  (spaces)         |    spaces)     |
  |                    |<--vector----------|----------------|
  |                    |--WriteTagNameEmb->|                |
  |                    |  (tag, vec)       |                |
  |                    |                   |                |
  |                    |  for each missing tvid:            |
  |                    |  resolve tvid→tag+value            |
  |                    |--EmbedQuery()-----|--------------->|
  |                    |  "tag: value"     |   (hyphens→    |
  |                    |  (spaces)         |    spaces)     |
  |                    |<--vector----------|----------------|
  |                    |--WriteTagValueEmb>|                |
  |                    |  (tvid, vec)      |                |
  |                    |                   |                |
  |                    |--MissingTagDef--->|                |
  |                    |  Embeddings()     |                |
  |                    |<--[]TagDefRef-----|                |
  |                    |  (tag, fileid)    |                |
  |                    |                   |                |
  |                    |  for each missing (tag, fileid):   |
  |                    |  D record → description            |
  |                    |--EmbedQuery()-----|--------------->|
  |                    |  description      |  (no tag name) |
  |                    |  (raw text)       |                |
  |                    |<--vector----------|----------------|
  |                    |--WriteTagDefEmb-->|                |
  |                    |  (tag, fileid,    |                |
  |                    |   vec)            |                |
```

## Drop on Model Swap (DropEmbeddings)

```
DB.Open              Store
  |                    |
  |--[embedding] model |
  |  changed?--------->|
  |                    |
  |<--yes--------------|
  |                    |
  |--DropEmbeddings()->|
  |                    |  strip vectors from T records (keep count)
  |                    |  delete all EV records
  |                    |  delete all ED records
  |<--ok---------------|
  |
  | next batch-embed pass picks up missing T-name + EV + ED
```
