# Sequence: Recall (Phase 2B)

**Requirements:** R2617–R2641, R2905, R2906, R2907, R2908, R2910

Diagram of the CLI recall routing, server HTTP handling, and in-process database fallback flows.

## 1. Recall CLI Routing & Local Fallback

```
CLI              Server (HTTP)      Librarian          DB (Local LMDB)
 |                     |                |                    |
1.1
 |- ark recall ------->|                |                    |
 |                     |                |                    |
 |--- [If Server is Running] --------------------------------|
1.2
 |  |- POST /recall ----------------------------------------->|
 |  |                  |                |                    |
1.3
 |  |                  |- Librarian.Recall() --------------->|
 |  |                  |                |                    |
1.4
 |  |                  |                |- Normalize inputs
 |  |                  |                |                    |
1.5
 |  |                  |                |- Score content (Vector-EC if model, Trigram-EC) + tag axis (EV cosine + on-the-fly value trigrams -> V records); 4-component
 |  |                  |                |                    |
1.6
 |  |                  |                |- Exclude self; allocate 2x2 (source x axis, <=3/cell, <=2/file, vec-larger/tri-smaller tiebreak), dedup+backfill; chat pool via sub-chunk funnel
 |  |                  |                |                    |
1.7
 |  |                  |                |- Resolve tags & chunk content
 |  |                  |                |                    |
1.8
 |  |                  |                |<-- RecallResult ---|
 |  |                  |                |                    |
1.9
 |  |<-- JSON ---------|                |                    |
 |  |                                                        |
1.10
 |  |- Format stdout (stencil/JSON)                          |
 |                     |                |                    |
 |--- [If Server is Down] -----------------------------------|
1.11
 |  |- Check tag_model config           |                    |
 |  |                                                        |
 |  |--- [If model is configured] ---------------------------|
1.12
 |  |  |- Exit 1 (error: server not running; model configured)
 |  |                                                        |
 |  |--- [If no model configured] ---------------------------|
1.13
 |  |  |- withDB(read-only) -------------------------------->|
 |  |  |                                                     |
1.14
 |  |  |  |- Librarian.Recall(Trigram-only) ------------------>|
 |  |  |  |                                                  |
1.15
 |  |  |  |                             |- Perform Trigram-EC search
 |  |  |  |                             |                    |
1.16
 |  |  |  |                             |- Trigram-EC + tag-trigram only (no model); exclude self; allocate 2x2
 |  |  |  |                             |                    |
1.17
 |  |  |  |                             |- Resolve tags & chunk content
 |  |  |  |                             |                    |
1.18
 |  |  |  |<-- RecallResult ---------------------------------|
 |  |  |  |                                                  |
1.19
 |  |  |<-- RecallResult ------------------------------------|
 |  |  |                                                     |
1.20
 |  |  |- Format stdout (stencil/JSON)                       |
```

## 2. Lua Bridge sys.recall Flow

```
Frictionless / Lua             Server (Lua bridge)         Librarian
       |                                |                      |
2.1
       |- sys.recall(inputs, opts)----->|                      |
       |                                |                      |
2.2
       |                                |- Convert Lua table to ConnectionsInput
       |                                |                      |
2.3
       |                                |- Librarian.Recall() >|
       |                                |                      |
2.4
       |                                |                      |- Perform recall query
       |                                |                      |
2.5
       |                                |                      |<-- RecallResult --|
       |                                |                      |
2.6
       |                                |- Convert RecallResult to Lua table
       |                                |                      |
2.7
       |<-- Lua Table ------------------|                      |
```
