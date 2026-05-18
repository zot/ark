# Tags-Output Baby Food

Language: Go. Environment: CLI (part of the `ark` binary).

`ark search -tags` extracts the @tag activity inside the matched
chunks and prints it as agent-readable markdown. The output is meant
for Haiku-class models running in a loop — terse, structurally
self-describing, no JSON parsing required.

## Bug fix: server path was silently empty

The CLI sends `Tags: true` to `POST /search`. The server returns a
JSON array of `TagResult` (`tag`, `count`, `bestScore`). The CLI
previously decoded the response into `[]SearchResultEntry`, then
ran `ExtractResultTags` *again* on the empty entries — so every
proxied `-tags` invocation returned nothing. The local LMDB path
(`ark serve` not running) worked correctly because it never crossed
the wire.

Fix: when `Tags: true`, the CLI decodes into `[]ark.TagResult` and
calls the printer directly. The server already returns the shape
the printer expects; no server change is needed for the fix
itself.

## Output shape

The output is a markdown bullet tree with four layers, top to
bottom:

```
@tag-name           (level 1)
  value             (level 2)
    file.md         (level 3)
      :range        (level 4 — appended to file line)
```

A populated tree (no filter, no suppression):

```
- @status (8 in 6 files)
  - done (5)
    - foo.md:1-7
    - bar.md:33-40
  - open (3)
    - baz.md:1-12
- @priority (4 in 3 files)
  - high (3)
    - foo.md:1-7
  - low (1)
    - bar.md:33-40
```

The tag-level count `(N)` is the total chunk count and is always
emitted. The `in M files` suffix is appended only when the tag
appears in more than one file. The counts on each value line are
chunk counts for that (tag, value) pair.

When the output collapses past the value layer (`-no-files`), the
chunk count stays on the tag line and each value line carries its
own count:

```
- @status (8 in 2 files)
  - done (5)
  - open (3)
```

## Adaptive defaults

Each `-with -tag NAME[:VALUE]` filter on the command line implies a
layer of the output the agent already knows. Those layers are
suppressed by default for that tag:

- `-with -tag NAME` (no value): hide `@NAME` from level 1; the
  value layer becomes the top of that subtree.
- `-with -tag NAME:VALUE`: hide both `@NAME` and the `VALUE`; chunk
  locations become the top of that subtree.

`-without -tag …` filters do NOT trigger suppression — the agent
asked to exclude that tag, so showing what else is there is the
useful answer.

When every `-with -tag` filter is fully specified (NAME:VALUE), the
output collapses to a flat list of file/chunk locations, matching
the shape of plain `ark search` without `-tags`. This is the
intended convergence — the agent sees `-tags` as a markdown view
that adapts to how much it already knows.

Tags NOT named in any `-with -tag` filter appear in full hierarchy
alongside the suppressed ones.

## Suppression flags

Two independent axes:

- **Value axis** (orthogonal): `-no-values` collapses the value
  layer. Tag names jump straight to files/chunks; chunks from
  different values are merged into one list under the tag name.
- **Location axis** (hierarchical):
  - default — full file:range references
  - `-no-chunks` — file paths only, no `:range` suffix; duplicate
    file paths under one value are deduplicated
  - `-no-files` — no locations at all; only tag/value counts

`-no-files` subsumes `-no-chunks` (a chunk reference needs a file
path). `-no-values` composes freely with either location flag.

Combinations:

| Flags                       | Layers emitted                       |
|-----------------------------|--------------------------------------|
| (none)                      | tag → value → file → range           |
| `-no-chunks`                | tag → value → file                   |
| `-no-files`                 | tag → value                          |
| `-no-values`                | tag → file → range                   |
| `-no-values -no-chunks`     | tag → file                           |
| `-no-values -no-files`      | tag (names only)                     |

The flags compose with adaptive defaults: a `-with -tag NAME`
filter still hides the tag name; the suppression flags determine
what's left below.

## Multiple `-tag` filters

Each `-with -tag NAME[:VALUE]` filter independently suppresses its
own subtree. Other tags found in matched chunks appear in full
hierarchy. `-without -tag …` filters never trigger suppression.

## Extraction scope

Tags are extracted from chunk text that survived the filter stack
(unchanged from the existing behavior). The extractor recognizes
inline `@name:` and `@name: value` patterns in the matched chunks
— same regex used today, extended to capture the value when one is
present on the same line.

## `-json` for programmatic consumers

`ark search -tags -json` emits the same `TagResult` data as JSONL
— one object per line — instead of the markdown bullet tree.
Schema is the Go `ark.TagResult` shape verbatim: `tag`, `count`,
`bestScore`, `fileCount`, plus a `values` array of
`{value, count, locations: [{path, range}]}`.

The suppression flags (`-no-values` / `-no-chunks` / `-no-files`)
and adaptive `-with -tag` defaults do NOT apply to `-json` output
— JSON is the raw structured view, so consumers can filter
themselves with `jq` or equivalent. The flags are markdown-render
concerns.

## `cli-commands.md`

The `ark search` section's flag table lists `-tags`, `-no-values`,
`-no-chunks`, `-no-files`, `-json`, and notes the adaptive
interaction with `-with -tag`.
