# TagBlock
**Requirements:** R443, R444, R445, R446, R447, R448, R449, R478, R459, R460, R461, R463, R465, R472, R473, R474, R475, R476

Parses and manipulates the tag block at the top of a markdown file.
The tag block is consecutive `@tag: value` lines starting from line 1.

## Knows
- tags: ordered list of (name, value) pairs
- bodyOffset: byte offset where the body begins (after the blank separator line)
- raw: the original file bytes (for body preservation)

## Does
- Parse(data []byte): scan lines from top, collect tags until first
  non-tag line. Record body offset (after blank separator if present).
  Return TagBlock.
- Tags(): return ordered slice of (name, value) pairs
- Get(name string): return value for a tag, and whether it was found
- Set(name, value string): if tag exists, replace value in place;
  if not, append to end of tag list
- Render(): emit tag block bytes — one `@name: value\n` per tag,
  then `\n`, then body from bodyOffset onward
- Validate(): return list of problems (blank lines in block, missing
  separator, malformed lines). Each problem includes line number and
  description.
- ScanBody(): scan body for stray tag-like patterns (`@word:` at
  line start, `## Word:` headings). Return findings with line numbers.

## Collaborators
- None — pure data structure, no dependencies

## Sequences
- seq-message.md
