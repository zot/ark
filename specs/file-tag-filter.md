# Tag Match Syntax & `-file-tag` Filter

Language: Go. Environment: CLI + server (part of the `ark` binary).

Two related changes:

1. **Match syntax for `-tag` and `-file-tag`.** A compact sigil-based
   form that lets one flag express name-mode × value-mode without
   adding new flags. Covers exact, contains, and regex matching for
   each side.
2. **New `-file-tag` filter.** A file-level match alongside the
   existing chunk-level `-tag`. A file F is considered to "have" a
   tag T when at least one chunk in F carries T in its extracted tag
   set. Authoritative against the per-file tag index — never against
   the content of a single chunk in isolation.

The new syntax and the new filter ship together because they share
the predicate shape. They apply to `ark search`, `ark subscribe`, and
the `ark-search` web component.

`-tag` and `-file-tag` are different filter modes:

- `-tag` matches chunks whose own text carries the tag.
- `-file-tag` matches every chunk in a file that has the tag
  somewhere.

A file with `@to-project: ark` in its frontmatter chunk has one chunk
matching `-tag to-project=ark` but every chunk matching `-file-tag
to-project=ark`.

## Match syntax

Both `-tag` and `-file-tag` accept the same form:

```
[~|:]NAME [SEP VALUE]
```

The leading sigil (optional) selects the **name match mode**; the
internal separator (optional) selects the **value match mode**.

| Form     | Name match | Value match | Notes                  |
|----------|------------|-------------|------------------------|
| `T=V`    | exact      | exact       |                        |
| `T:V`    | exact      | contains    | substring-AND on value |
| `T~V`    | exact      | regex (RE2) |                        |
| `:T=V`   | contains   | exact       | substring-AND on name  |
| `:T:V`   | contains   | contains    |                        |
| `:T~V`   | contains   | regex       |                        |
| `~T=V`   | regex (RE2)| exact       |                        |
| `~T:V`   | regex      | contains    |                        |
| `~T~V`   | regex      | regex       |                        |
| `T` / `:T` / `~T` | exact / contains / regex, any value         |

### Name modes

- **Exact:** bare `NAME` is a case-insensitive literal match against
  the tag name.
- **Contains:** prefix `:` marks NAME as a whitespace-separated list
  of tokens. NAME and the tag name are both lowercased; the predicate
  matches iff every token is a substring of the lowercased tag name
  (substring-AND, order-independent). Matches the behavior the UI
  exposes via `MatchTagNames`.
- **Regex:** prefix `~` marks NAME as a case-insensitive RE2 pattern
  matched against the tag name.

A leading sigil is consumed. To match a tag whose name genuinely
starts with `~` or `:`, use the regex form (`~\~name` or `~:name`).

### `@` normalization

Users naturally type the `@tag:` form they see in files — the CLI
shouldn't punish that. A leading `@` is stripped wherever it appears
in the prefix region, before any other processing:

| Input    | Becomes |
|----------|---------|
| `@T`     | `T`     |
| `@~T`    | `~T`    |
| `~@T`    | `~T`    |
| `@T:V`   | `T:V`   |
| `@~T=V`  | `~T=V`  |

The `@` is decorative — the regex marker `~` stays. After
normalization the form follows the table at the top of this section.

### Value modes

- **Exact (`=VALUE`):** value compared as a literal string. No
  normalization beyond what the tag extractor already does.
- **Contains (`:VALUE`):** VALUE and the chunk tag value are both
  lowercased. VALUE is split on whitespace into tokens; the match
  succeeds iff every token is a substring of the lowercased chunk
  value (substring-AND, order-independent). Mirrors the existing UI
  contains semantics in `MatchTagValues`.
- **Regex (`~VALUE`):** VALUE is an RE2 pattern. Match succeeds iff
  the pattern matches the chunk's tag value (no anchoring; partial
  matches succeed).

### Empty values after a separator

- `T=` — match only tags whose value is the empty string. Useful for
  finding `@status:` (no value yet).
- `T:` — degenerate; the empty search token list matches every
  value. Equivalent to bare `T`. Accepted.
- `T~` — degenerate; the empty regex matches every value.
  Equivalent to bare `T`. Accepted.

### Quoting

The sigils `~`, `=`, `:` are all shell-safe in normal positions:
bash only does `~` expansion when a token starts with `~` followed by
`/` or a username. `-tag ~stat:open` won't be rewritten by the shell.
A token like `-tag ~/foo:bar` would be expanded — quote it if needed.

Quote values that contain whitespace: `-tag status:"in progress"`.

### Parse output

`ark search -parse` already prints the disambiguated filter stack.
Each `-tag` and `-file-tag` row prints with explicit name-mode and
value-mode tags, e.g.:

```
-tag exact:status regex:^(open|in-progress)$
-file-tag regex:^to- contains:"ark request"
```

This makes typos obvious and gives the user a reliable way to verify
what the parser understood.

## Wire-format consolidation

The server retires the `tag-contains` chunk-filter mode. A single
`tag` mode now covers every name and value match shape via its
sigil-form Query. Callers that previously emitted
`{mode: "tag-contains", query: "ntok:vtok"}` migrate to
`{mode: "tag", query: ":ntok:vtok"}` (leading `:` selects the
contains-name mode).

There is no semantic flip on the value side: `:V` already meant
contains (substring-AND tokens via `MatchTagValues`). The sigil
makes that explicit and adds `=` (exact) and `~` (regex) as new
value modes. Existing callers sending `{mode: "tag", query: "name:value"}`
keep their current behavior — the query happens to be valid sigil-form
with the same contains-on-value semantics.

A separate sanity sweep across agent skills and scripts is queued
to make sure any caller using the old `tag-contains` shape is moved
to the new `:T:V` form.

## `ark search`

`-tag` accepts the new syntax. `-file-tag TAG[SEP VALUE]` is added
to the filter stack alongside `-tag`. Both compose with `-with` and
`-without` polarity. Both are repeatable. They co-exist freely in a
single query.

Per-search caching. For each search batch, each file's tag set is
resolved once and reused across all chunks from that file. Uses the
existing per-file tag index — no new index. The cache lives for the
duration of one search.

## `ark subscribe`

### Unified `-tag`

`-tag` accepts the new syntax. The old `-value RE` flag is **retired**.

- "Match tag T with value V" → `-tag T=V`.
- "Match tag T with value containing V" → `-tag T:V`.
- "Match tag T with value matching regex RE" → `-tag T~RE`.

`-tag` becomes repeatable. Multiple `-tag` entries OR together (any
match fires).

Existing `ark subscribe --cancel --tag T --value V` callers rewrite
to `ark subscribe --cancel --tag T=V` (or `T~RE` for the rare
regex-of-value case).

### New `-file-tag`

`-file-tag TAG[SEP VALUE]` registers interest in **any chunk indexed
on a file that has the tag** (with the value matching per the
specified mode). Repeatable. Multiple `-file-tag` entries OR together.

`-tag` and `-file-tag` are independent axes of one subscription. A
subscription may use either, both, or many of each.

### Membership set

A subscription with one or more `-file-tag` filters maintains an
in-memory set of fileIDs that currently match **at least one** of
its file-tag filters (OR semantics, consistent with the `-tag` and
`-file-tag` repeat rules above). On every chunk indexed for file F
(regardless of whether F is currently a member), the publisher:

1. Re-evaluates each `-file-tag` predicate against F's authoritative
   tag index. Removing a tag from one chunk does **not** imply the
   file lost the tag — another chunk in F may still carry it. The
   tag index is the source of truth.
2. Compares the new membership boolean to the prior state in the
   set.
3. Acts on the transition:
   - **Was=N, is=N:** no action. The chunk is not delivered for this
     subscription. (Other subscriptions on the same session may
     still deliver it.)
   - **Was=N, is=Y:** add F to the set. Deliver this chunk as the
     entry event. No backfill of prior chunks on F.
   - **Was=Y, is=Y:** F remains a member. Deliver this chunk.
   - **Was=Y, is=N:** remove F from the set. Deliver this chunk as
     the exit event — the moment T disappears is itself relevant
     activity, symmetric with the entry rule.

### Lifecycle and reaping

Membership sets live with their parent subscription. When the
subscription is cancelled or the session is TTL-reaped, the set is
discarded.

Membership state is in-memory only, like the rest of the subscription
registry. On server restart, sets start empty; the next chunk indexed
on each matching file re-populates them through the normal "was=N,
is=Y" path. No persistent set is needed.

### Mute interaction

`@mute: true` in a file still silences all events from that file,
including file-tag-driven deliveries. The mute check happens before
membership evaluation.

### Self-notification

A session does not receive `-file-tag` notifications for chunks it
itself indexed, same as the existing `-tag` rule.

## `ark-search` web component

The component gains a `-file-tag` filter row alongside its `-tag`
row. Both rows accept the new sigil syntax (UI may still expose name
and value mode dropdowns for accessibility — the component serializes
to the same sigil form on the wire). Polarity and repeat semantics
match the existing `-tag` row. The component serializes both into
the existing `chunk_filters` request shape that the server already
handles for `-tag`.

## CLI surface (cli-commands.md)

`subscribe` row updates:

| Flag           | Default | Meaning                                                  |
|----------------|---------|----------------------------------------------------------|
| `--tag T`      | —       | Tag matcher (see Match syntax). Repeatable.              |
| `--file-tag T` | —       | File-tag matcher (see Match syntax). Repeatable.         |

`--value` is removed from the `subscribe` row entirely.

`search` row gains a `--file-tag TAG[SEP VALUE]` entry following the
existing `--tag` row. Polarity-aware (consumes the current `-with` /
`-without` state).

## Implementation notes

- **Parser.** A single helper parses the sigil form into a
  `(NameMode, NameStr, ValueMode, ValueStr)` quad. Used by both
  `-tag` and `-file-tag` parsing across search, subscribe, and the
  server JSON.
- **Search chunk filter.** Existing `TagChunkFilter` extended to
  accept the four-tuple; new `FileTagChunkFilter` parallel to it
  consults the per-file tag aggregate (`FileTagValues`). Cache one
  approval set per file per search.
- **Subscribe.** Extend `TagSub` to hold the parsed quad (name
  mode/string, value mode/string) instead of the current `Tag string
  + ValueRE *Regexp` pair. A separate `FileTagSub` shape covers the
  file-tag entries.
- **Re-check on tag removal.** The publish path runs once per
  indexing event for file F. The full set of file-tags currently on
  F is available from the index after indexing settles; the
  publisher reads it rather than inferring from the chunk's local
  tag delta.

## Anchoring

- `specs/cli-commands.md` — flag-table rows for `search` and
  `subscribe`; update existing `--tag` row descriptions.
- `specs/pubsub.md` — Subscribe section: replace `--value` text,
  add `--file-tag` semantics and membership-set behavior, point
  `--tag` at the Match syntax section.
- `specs/search-cli-filters.md` — update `-tag` to point at the
  Match syntax section; add `-file-tag TAG` to the modes list.
