# Sequence: PDF Chunk Slice-And-Insert

Clicking an `<ark-tag>` overlay inside a `<pdf-chunk>` reshapes
the DOM: the chunk splits at the tag's line, revealing an inline
`<ark-search>` panel between the two halves. Closing the panel
re-merges the halves.

## Participants
- User
- `<ark-tag>` overlay (crc-ArkTagElement.md)
- `<pdf-chunk>` (crc-PdfChunkElement.md)
- `<ark-search>` panel (crc-ArkSearchElement.md)

## Flow — Slice On Click

```
User         <ark-tag>      <pdf-chunk>       <ark-search>
  |              |                |                 |
  |--click------>|                |                 |
  |              |--dispatch 'ark-tag-click' event  |
  |              |  (bubbles)     |                 |
  |              |                |                 |
  |              |                |--intercept event|
  |              |                |--compute slice Y from tag rect
  |              |                |                 |
  |              |                |--build top <pdf-chunk>:
  |              |                |  same src/page/x/width
  |              |                |  height trimmed to above slice
  |              |                |  tag-rect children above slice
  |              |                |
  |              |                |--build bottom <pdf-chunk>:
  |              |                |  same src/page/x/width
  |              |                |  y below sliced tag's line
  |              |                |  tag-rect children below,
  |              |                |  remapped to new local coord
  |              |                |
  |              |                |--build <ark-search>:
  |              |                |  pre-fill tag and value
  |              |                |
  |              |                |--replaceWith(top, search, bottom)
  |              |                |                 |
  |              |              (self disconnects)  |
  |                                                 |
  |  top and bottom <pdf-chunk> connect             |
  |  (reuse host's cached page image — no re-render)
  |                                                 |
  |  <ark-search> connects, begins query            |
  |                                       |<--render panel-->|
```

## Flow — Close / Re-Merge

```
User         <ark-search> panel    top/bottom <pdf-chunk>   parent
  |                |                     |                      |
  |--close-------->|                     |                      |
  |                |--dispatch 'close' event                    |
  |                |                                            |
  |                |  handler on parent listens and calls       |
  |                |  mergeOnClose() for the pair               |
  |                |                                            |
  |                              --compute original rect        |
  |                              --gather all tag-rect children |
  |                              --build merged <pdf-chunk>     |
  |                              --parent.replaceChildren(merged)
  |                              (three disconnect,             |
  |                               one connects)                 |
```

## Flow — Recursive Slice

```
User clicks a tag in top <pdf-chunk> (a slice of an earlier slice)

Same as initial slice — top splits again into top/search/bottom.
Only one <ark-search> per immediate parent container is open at
a time. Opening one in a sibling closes any existing one in that
same container.
```

## Flow — Cleanup On Host Disconnect

```
<ark-search> host element                  every URL in blobUrls
  |                                             |
  |--disconnectedCallback---------------------->|
  |--for url in blobUrls: URL.revokeObjectURL-->|
  |--for doc in docCache: doc.destroy()         |
  |--clear docCache, pageCache, blobUrls        |
```

## Flow — Safety Net On Page Unload

```
window                 <ark-search> hosts in document
  |                         |
  |--beforeunload---------->|
  |                         |--for each host: run same
  |                         |  cleanup as disconnect
```

## Notes
- The host's page-image cache survives the three-way
  replacement: the new top and bottom read the same cached
  `{url, w, h}` their predecessor used. No re-render.
- `blobUrls` is never pruned during slicing. Entries are only
  reclaimed on host disconnect or `beforeunload`. This matches
  the "lifecycle-coupled state" model: the URL's life is the
  host's life.
- Tag rects in the top slice are passed through; rects in the
  bottom slice are remapped to the new local coord system
  (shifted by the slice Y offset). Rects intersecting the slice
  line are dropped.
