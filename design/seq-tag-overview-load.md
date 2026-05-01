# Sequence: Tag Overview Load
**Requirements:** R2032, R2037, R2045, R2062, R2063, R2065, R2073, R2074, R2075, R2076, R2078, R2079, R2080, R2083

A `/content/` response is enriched server-side with the elements
the sidebar needs (ext indicators, IDs on tag/heading elements,
PDF heading rects). The TagOverviewSidebar mounts client-side,
scans the DOM, loads persisted widths, and starts auto-track.

## Server-side enrichment (R2065, R2073-R2076, R2078-R2080)

```
Browser              Server                  DB / Indexer       PDFChunker / pdftext
  |                    |                          |                    |
  |--GET /content/PATH->                          |                    |
  |                    |--render content-------->|                    |
  |                    |  (existing path)        |                    |
  |                    |<--HTML / PDF chunks-----|                    |
  |                    |                          |                    |
  |                    |--enrichContent(...)----->|                   |
  |                    |  for each chunk:         |                    |
  |                    |    --tagsForChunk------->|                    |
  |                    |    <--inline tags--------|                    |
  |                    |    --chunkToTargets----->|                    |
  |                    |    <--ext-tag list-------|                    |
  |                    |    [headings (markdown)]                       |
  |                    |    [PDF: heading rects]----------------------->|
  |                    |    [PDF: heading rects]<-----------------------|
  |                    |                                                |
  |                    |  emit:                                         |
  |                    |   - <ark-ext-tags> at top of chunks            |
  |                    |     with <ark-tag externalFile externalTarget> |
  |                    |   - id="..." on inline <ark-tag>               |
  |                    |   - id="..." on <h1>-<h6>                      |
  |                    |   - <ark-heading rect="..."> for PDF           |
  |                    |                                                |
  |<--enriched HTML----|                                                |
```

## Client-side mount (R2032, R2037, R2045, R2062, R2063, R2083)

```
Browser              TagOverviewSidebar            Server (HTTP)
  |                    |                                |
  |--DOMContentLoaded->|                                |
  |                    |--mount(host)                   |
  |                    |  scanDOM:                      |
  |                    |    <h1>-<h6>, <ark-tag>,       |
  |                    |    <ark-ext-tags>              |
  |                    |  --GET widths I-records------->|
  |                    |  <-- {abbrev, full} -----------|
  |                    |                                |
  |                    |  if entries empty: render none, return (R2037)
  |                    |  else:                         |
  |                    |    set mode = abbreviated      |
  |                    |    set width = widths.abbrev or 25% viewport (R2062, R2063)
  |                    |    render badge + sidebar      |
  |                    |    install IntersectionObserver|
  |                    |       on chunk + heading       |
  |                    |       boundaries (R2045)       |
  |                    |    update auto-track highlight |
```

## Search-iframe inheritance (R2083)

```
Browser              ArkSearchElement       /content/ iframe        TagOverviewSidebar
  |                    |                          |                       |
  |--render result---->|                          |                       |
  |                    |--insert <iframe          |                       |
  |                    |    src="/content/...">-->|                       |
  |                    |                          |--load enriched HTML-->|
  |                    |                          |  (same enrichment)    |
  |                    |                          |--mount(host)--------->|
  |                    |                          |  (same code path,     |
  |                    |                          |   different host)     |
```
