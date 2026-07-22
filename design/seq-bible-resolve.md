# Sequence: Bible addressing — startup hook and two-stage resolve

**Requirements:** R3214, R3216, R3217, R3218, R3219, R3220, R3221, R3179, R3180

Two flows: the per-source setup that runs at config-resolve (every startup),
and the address resolution that turns a friendly `BIBLE/<Book>:ch.v` into a
chunk.

## Startup — per-source chunker activation

```
1. config resolves (every startup, every ReloadConfig)
1.1  activateSourceChunkers(config)                              [R3217]
1.2  clear config.derivedStrategies — the pass re-derives, never
       accumulates; nothing it registers is persisted            [R3218]
1.3  for each source, for each PER-SOURCE strategy entry:
1.4    chunker ← chunkerFor(strategy)
1.5    if chunker implements SourceChunker:                      [R3217]
1.6      chunker.ActivateForSource(source, register)
1.7        BibleChunker: register "<source.Dir>/BIBLE/** → bible"
             into config.derivedStrategies (absolute form)       [R3218]
1.8        BibleChunker: if a real <source.Dir>/BIBLE path exists,
             return an error → source load fails                 [R3219]
1.9      record source under strategy in `declared` (recorded even
           if 1.8 failed — declaring is the reconcile's criterion)
1.10   (globally-mapped chunkers are never called)               [R3217]
1.11 BibleChunker.ReconcileBookIndex(declared["bible"])          [R3221]
1.12   PruneBookIndex: drop every B record whose source is not
         in that list — including when the list is empty         [R3221]
1.13 recordActivationFailures(failed)                            [R3219]
1.14   any failed → write E:source_activation (dir → message);
         none → delete it, so a fix clears it on reload          [R3219]
```

## Index time — the book index is written

```
2. BibleChunker.Chunks(path, content, yield) over a *.text.xhtml
2.1  emit prose/stanza chunks with chapter+verses from the ids
2.2  for each chapter present in the file:
2.3    write B<source>\0<book>\0<chapter> → path  (via write actor) [R3214]
```

## Resolve — `~/work/esv/BIBLE/John:3.16` → chunk

```
3. ResolveExtTarget("~/work/esv/BIBLE/John:3.16")
3.1  FileStrategy("~/work/esv/BIBLE/John") = bible
       (matches the source-prefixed in-memory entry, absolute)   [R3218]
3.2  dispatch → resolveBibleTarget(parts)                        [R3220]
3.3    path has a BIBLE/<Book> segment → virtual                 [R3216]
3.4      parseChapterVerse(anchor) → chapter 3, verse 16
3.5      lookupBookFile(esv, "John", 3)
           read B<esv>\0John\03 → OEBPS/Text/b43.00.John.text.xhtml [R3214]
3.6      rewrite BASE to the real file
3.7    parseChapterVerse decode; return the chunk whose chapter=3
         and whose verses span contains 16                       [R3179]
3.8    a nonexistent book, chapter, or verse → nothing (no fallback) [R3180]
```

The real file path is addressable directly too:
`~/work/esv/OEBPS/Text/b43.00.John.text.xhtml:3.16` skips steps 3.3–3.6 and
enters at step 3.7, reaching the same chunk. (Keep a dotted number off the
start of a prose line here — the validator reads one as a step ID.)
