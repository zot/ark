# Future Ideas

A holding pen for ideas we deliberately deferred — captured so they aren't
lost before ark is fully integrated with mini-spec and can track them
natively. Keyed into [index.md](index.md).

Each entry carries an `@future:` tag so `ark search @future:` finds it
(dogfooding). When an idea is taken up, fold it into the owning feature spec
+ a requirement and delete the entry here.

## Recall & substrate

- **Offset-addressed chat sub-chunks.** The recall chat sub-chunk locator
  ships as `PATH:RANGE:"<snippet>"` (a string anchor; substrate-v3 migration,
  recall.md). Chat JSONL is append-only ⇒ chunk ranges are stable, so a
  positional form — a `:N` sub-index or the sub-chunk's markdown line-range
  (`:5-7`) — would round-trip just as reliably and resolve by *slicing* the
  turn instead of re-scanning for the snippet. Deferred as an optimization;
  the snippet anchor ships first.
  `@future: offset-addressed chat sub-chunks`
