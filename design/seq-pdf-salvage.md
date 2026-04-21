# Sequence: PDF Salvage Fallback

**Requirements:** R1652, R1653, R1654, R1655, R1656, R1657, R1658, R1659, R1660

Invoked when seehuhn's `pdf.NewReader` rejects a file that still
contains readable content streams — most often a PDF with a
malformed xref table. The salvage path skips structure detection and
does a straight byte scan for text operators.

```
microfts2            PDFChunker                  salvageText        compress/zlib
   |                      |                           |                    |
   |--FileChunks(path,h)->|                           |                    |
   |                      |--os.ReadFile + hash------>|                    |
   |                      |--pdf.NewReader(bytes)                          |
   |                      |    [error — xref reject etc.]                  |
   |                      |--log: "pdf: salvage %s: %v"                    |
   |                      |--salvageText(data, yield)>|                    |
   |                      |                           |                    |
   |                      |        [for each stream..endstream pair]       |
   |                      |                           |--scan for /Filter->|
   |                      |                           |  if /FlateDecode:  |
   |                      |                           |--zlib.NewReader()->|
   |                      |                           |<--decoded bytes----|
   |                      |                           |                    |
   |                      |        [scan decoded bytes for Tj/TJ/'/"]      |
   |                      |                           |  (Tj)              |
   |                      |                           |  (TJ array)        |
   |                      |                           |  unescape \(...)   |
   |                      |                           |                    |
   |<----yield(chunk)-----|<--chunk(salvage/N, text)--|                    |
   |                      |                           |                    |
   |<----(hash, nil)------|                           |                    |
```

## Key points

- Salvage only runs after seehuhn fails. Successful parses never
  invoke it — the structured path (tables, headings, paragraphs)
  wins whenever available. (R1652)
- One chunk per content stream, `Range = "salvage/N"`. N is
  1-indexed in stream encounter order. (R1657)
- Salvage chunks do not carry a `rect` attribute (no coordinates
  were consulted) and do not carry `font_size`. `page` is set to
  `"1"` as a conservative default. (R1658)
- Unknown `/Filter` values (LZW, DCT, encrypted streams, etc.) cause
  the stream to be skipped. If no streams yield text, the file takes
  the standard log-once path and is skipped on subsequent scans.
  (R1654, R1659)
- The salvage path is shared by both `Chunks` (byte-in, for tmp
  documents) and `FileChunks` (file-in, for indexed files) — the
  implementation operates on `[]byte` so both callers converge on
  the same routine. (R1660)

## String unescape

The text-showing operators wrap their arguments in parentheses, e.g.
`(Hello)Tj`. Inside, the standard PDF escapes apply:

- `\(` / `\)` — literal paren
- `\\` — literal backslash
- `\n` `\r` `\t` `\b` `\f` — control characters
- `\ddd` — three-digit octal byte

Everything else is taken verbatim. Nested parens are balanced, so
`(a(b)c)` yields `a(b)c`. (R1656)

## What salvage deliberately does NOT do

- Font encoding resolution (ToUnicode / CMap): PDFs that encode text
  through a font subset will return bytes that aren't readable
  Unicode. Salvage takes them as-is — garbage in, garbage out.
- Coordinate tracking: no X/Y positions, no line merging, no
  paragraph detection. The output is a flat run of extracted
  strings per stream.
- Multi-page structure: all salvage chunks claim `page = 1`. A
  salvaged PDF with multiple pages will have multiple `salvage/N`
  chunks but no per-page grouping.
