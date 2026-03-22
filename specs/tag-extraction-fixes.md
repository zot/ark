# Tag Extraction Fixes

Two bugs in tag extraction.

## Inline tags

The tag regex `(?:^|\n)@tag:` only matches tags at the start of a
line. This is a bug — inline tags like `some text @status: open` are
valid tags and should be indexed. The `@` character plus trailing
colon is sufficient disambiguation from emails and mentions.

Fix: remove the line-start anchor from `tagRegex` in indexer.go.
The regex should match `@tag:` anywhere in the content.

The `tagDefRegex` (for `@tag: name description`) should keep its
line-start anchor — definitions are a structured format that only
makes sense at line start.

The `tagPattern` in search.go already matches inline (no line-start
anchor). The `tagLineRegex` and `strayTagRegex` in tagblock.go are
for tag-block parsing which is inherently line-oriented — leave
those alone.

### Compound tags

Inline tag support is required for compound tags of the form:

    @ref: file:location @item: body

`@ref:` is a compound tag — its value runs to EOL and incorporates
the nested `@item:` tag. Both `@ref` and `@item` must be indexed
in the file where they appear. The inline fix enables this.

Future (hypergraph): `@ref:` projects the nested `@item: body` onto
the referenced file, so search results show it as if the tag were
in that file. That projection is not part of this fix.

## Append-detection tag boundary

When a file is appended to, `ExtractTags` runs on `newBytes` starting
at the old `FileLength` offset. If a tag straddles the boundary
(`@st` in old content, `atus: open` in new content), the tag is
silently lost.

In practice this is unlikely because appends land at line boundaries,
but it is possible.

Fix: when computing `newBytes` for tag extraction during append
detection, back up from the split point to the previous newline in
the full file data. Scan tags from there. This does not affect the
bytes sent to `AppendChunks` (which must start exactly at
`FileLength`) — only the tag extraction window is widened.

The same boundary issue applies to `ExtractTagDefs`.
