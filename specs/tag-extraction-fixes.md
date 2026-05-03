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

A line of the form `@a: TARGET @b: v1 @c: v2` is a *compound* tag.
Compound semantics are **per-outer-tag** — different outer tags use
the embedded `@x: y` segments differently:

- `@ext:` routes the embedded tags as annotations applying to a
  different chunk (the TARGET); the embedded tags are NOT inline
  tags on the source. See `specs/at-ext-parsing.md` and
  `specs/at-ext-storage.md`.
- A hypothetical `@priority:` would meta-modify the next tag (and
  could be recursive: `@priority: 12 @priority: high @ext: …`).
- Future shapes (annotation tags, conditional tags, …) will define
  their own embedded semantics.

`ExtractTagValues` therefore returns only the *outer* tag of each
compound line — one `(tag, value)` pair where the value is the
substring from after the first `@x:` to end of line. Each consuming
code path dispatches on the outer tag name to the embedded-tag
handler that owns those semantics. The default for unknown outer
tags is no embedded handling.

Naming rule: any helper that splits a compound value must encode
the *owner-tag-specific* semantics in its name (`ParseExtTarget`,
not `splitCompoundTags`).

Future (hypergraph): the `@ext:` projection — embedded tags showing
up as if they lived in the target file — is provided by the X record
+ V record + ExtMap layer, not by inline-extraction peeling.

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
