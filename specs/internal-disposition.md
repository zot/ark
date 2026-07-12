# Internal-disposition tagging

An `@ext-candidate` carries a **disposition** (see `at-ext-parsing.md`):
`external` (the default) routes an accepted tag to the target's **mirror
file**, as `@ext` always has; `internal` writes the `@tag: value` into the
**target file's own body**, where a human reading the source sees it. Internal
disposition is not a new place to store tags — it grants the curating agent the
pen for a location humans already write (an inline tag or a file-tag block).
This spec covers the internal write path: which file types can host an internal
tag, where the tag lands, how `ark ext accept` applies it, and how it degrades.

## Capability — which file types can take an internal tag

The gate is two axes, both of which map onto the chunker type:

1. **Writable** — ark can change the file's bytes at all.
2. **Multi-line chunk** — a dedicated `@tag:` line fits inside the chunk it
   annotates (a tag runs to the newline, so a single-line chunk cannot host a
   dedicated tag line without becoming its own chunk).

| Chunker      | internal tag? | why                                                          |
|--------------|:-------------:|--------------------------------------------------------------|
| `markdown`   | **yes**       | `@tag:` is native; a tag line lands inside the section chunk |
| `bracket-*`  | **yes\***     | comment-wrapped tag line inside the block keeps source valid |
| `indent-*`   | **yes\***     | same, with the tag line at the block's body indentation      |
| `lines`      | no            | chunk = one line; a tag line becomes its own chunk           |
| `chat-jsonl` | no            | effectively read-only (another writer owns the transcript)   |
| `pdf`        | no            | binary, `IsWritable=false`; newlines are inferred            |

\* **only when the language has a line comment.** A code chunk's tag line must
be comment-wrapped to keep the source valid in its own language, so a
bracket/indent language with an empty `CommentSyntax()` (e.g. `bracket-json`)
**cannot** host an internal tag and **degrades to external**. Markdown needs no
comment (a bare `@tag:` line is valid markdown), so it is unconditional. Net
predicate: *markdown → always; bracket/indent → iff `CommentSyntax() != ""`;
everything else → external.*

The capability is exposed as a **type-assertion gate**: each writable text
chunker is wrapped in an `internalTagChunker` that adds an `InsertTag` method
(the `tagInserter` interface). "Can this file type take an internal tag?"
becomes a `chunkerByName[strategy].(tagInserter)` assertion. The wrapper embeds
the union of every microfts2 chunker interface the chunker satisfies (Chunker,
AppendAwareChunker, RandomAccessChunker, FileChunker, ChunkerMetadata), so all
of microfts2's index- and retrieval-time fast paths still reach the underlying
chunker by promotion; the wrapper is transparent for everything but `InsertTag`.

## Placement — position is the file-vs-chunk choice

Where the tag lands picks its scope, mirroring the `@ext` address granularity:

- **Chunk-level** (an address with an anchor — `:range`, `%uuid`, `:"snippet"`)
  — insert the tag **inside the target chunk, right under its structural
  opener** (the markdown heading, the bracket opening line, the indent block
  header). The chunker merges the `@`-run into that chunk, so the tag **stays
  with the chunk** it was chosen for — imperative, because a tag chosen for
  chunk C is about C's centroid and must not drift to a neighbor or to the
  file. (Inserting a tag does not move C's embedding: `stripArkTags` runs at
  embed time, so the embedded text stays tag-free.) A **headingless prose
  chunk** has no opener; the tag goes at the top of the chunk's line range.
- **File-level** (a bare-path target) — insert at the **top of the file**,
  where the `@`-run stands as **its own chunk**, annotating the whole file
  without touching any section.

Each writable chunker owns its own insertion **stencil** — the rigid per-language
format: markdown emits a bare `@tag:` line; bracket comment-wraps it inside the
block (column cosmetic); indent comment-wraps it **and matches the block body's
indentation** (column load-bearing, or the line re-chunks out of scope).
`InsertTag` is a pure function `(fileBytes, targetChunk, tag, value, scope) →
newBytes` — testable in isolation, and pinned by a Sentry test that re-chunks
the output and asserts a chunk-level tag rejoins its target chunk and a
file-level tag stands alone.

## Accept — `ark ext accept` reads the disposition

`ark ext accept <target> <tag> [value]` resolves every matching `@ext-candidate`
**per its own disposition** (internal and external are distinct proposals, so a
target/tag/value that matches both is resolved both ways):

- **external** → route to the mirror as an `@ext` edge (source untouched) — the
  established behavior.
- **internal** → write the tag into the source file body via the chunker's
  stencil (file-level or chunk-level per the target granularity).
- **internal, but the target can't host it** → **fall back to external.**
  Degrade, never refuse. Fallback fires when the chunker type is incapable
  (`lines`/`chat-jsonl`/`pdf`), the code language is comment-less, the target
  resolves to more than one chunk, the path is in a read-only zone, or the file
  is not writable on disk.

The internal write is a temp+rename on the **DB actor** — the same goroutine
that serializes the mirror write — so concurrent accepts to one file (every
session proxies to the one server) serialize. The written tag becomes a plain
inline/file-block tag, indistinguishable from a human-typed one; the normal
reindex the file change triggers materializes it (no new DB mutation path), and
re-proposal is self-suppressed once it indexes.

## Positive judgment — every accept reinforces

Every accept — internal or external — also writes a positive `@ext-judgment
@count:+1` (deduped per tag), the accept-time producer of the signed-RJ axis.
The net-rejected filter (R3070) fires on **negative** counts only, so a positive
judgment reinforces the proposal without suppressing it. This is symmetric with
reject, which writes `@count:-1`.

## Preference — conversational, default external

The internal-vs-external preference is **not** config: it lives in the
user-facing assistant's conversation, defaults to **external** (the reversible,
non-mutating mirror path), and is elicited lazily — on a tag suggestion the
agent may offer internal, never blocking the first recall to ask. A simple
refusal keeps the whole session external; a refusal-with-guidelines lets the
agent interpret per-file-type / per-axis nuance in natural language (deferring
any config taxonomy until real use shapes it). The Haiku recall secretary stays
disposition-agnostic — it only proposes; the assistant applies disposition at
its discernment gate. In the Tag Forge (no conversation), the human is the
discerner and picks disposition per proposal. See the `/recall` skill.
