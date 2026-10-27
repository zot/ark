# FileEncoding
**Requirements:** R3656-3664

A user's file may be UTF-16; everything ark hands a chunker, a tag scanner or a
browser is UTF-8. This component is the edge where the file's encoding is read
and written, and the chunker wrapper that keeps the chunkers encoding-blind.

## Knows
- FileEncoding: `utf-8`, `utf-16le`, `utf-16be`. Read from the byte-order mark
  only (`FF FE`, `FE FF`); anything else, the UTF-8 mark included, is UTF-8 and
  passes through untouched, so no file indexed before this changes.
- decodingChunker: embeds a fatChunker; holds no per-file state of its own.

## Does
- DetectEncoding(data): the encoding named by data's byte-order mark. R3656
- DecodeText(data): UTF-8 text, the encoding, and whether it round-trips
  (EncodeText of the text reproduces data). UTF-8 data is returned as is. R3657,
  R3661
- EncodeText(text, enc): text in enc, mark included; identity for UTF-8. R3662
- ReadEditableText(path): the file's text and encoding, or errNotWritable when
  it does not round-trip. R3661, R3662
- EncodeForFile(path, text): text in the encoding the file on disk has now
  (UTF-8 when it does not exist), or errNotWritable. R3662
- decodingChunker.Chunks / GetChunk / FileChunks: decode a UTF-16 file before
  the wrapped chunker sees it. GetChunk caches the decoded text in the
  customData slot beside the inner chunker's own; FileChunks reads, hashes the
  raw bytes for the staleness short-circuit, and chunks the decoded text.
  UTF-8 data reaches the wrapped chunker unchanged. AppendChunks and
  ChunkerMetadata promote. R3657
- wrapDecoding(c): decodingChunker around a fatChunker; any other chunker
  unchanged. Registered inside wrapInternalTagChunker for markdown, bracket
  and indent. R3657
- decodingFunc(fn): a strategy function that decodes before calling fn; the
  `lines` strategy registers through it. A function has no optional
  interfaces to mirror. ark's own chat-jsonl and bible chunkers decode at the
  top of their own Chunks instead of being wrapped, because ark calls the
  registered bible chunker directly (FlushBookIndex, ActivateForSource). The
  PDF chunker is binary; the external-command strategies open the file
  themselves and are not covered. R3664

## Collaborators
- Indexer: decodes content before fileLevelTags, records the N record, gates
  appends. R3658, R3660
- Store: the N record. R3660
- Server, DB: the text sites and the content routes. R3662, R3663
