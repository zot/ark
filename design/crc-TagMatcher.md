# TagMatcher
**Requirements:** R2442, R2443, R2444, R2445, R2446, R2447, R2448, R2449, R2450, R2451

Shared parser and predicate evaluator for the `-tag` / `-file-tag`
sigil match syntax. Converts a single argument of the form
`[~]NAME [(=|:|~) VALUE]` into a four-tuple predicate that every
caller (search CLI, subscribe CLI, server JSON, ark-search element
backend) evaluates identically. Stateless — no DB handle, no caches.

## Knows
### MatchPredicate
- NameMode: enum {Exact, Contains, Regex} — how to compare the tag name
- NameStr: string — literal, contains source, or RE2 pattern
- NameTokens: []string — pre-tokenized lowercased list for Contains mode
- NameRE: *regexp.Regexp — compiled at parse time when NameMode == Regex (case-insensitive)
- ValueMode: enum {Any, Exact, Contains, Regex} — how to compare the value
- ValueStr: string — literal, contains source, or RE2 pattern
- ValueTokens: []string — pre-tokenized lowercased list for Contains mode
- ValueRE: *regexp.Regexp — compiled at parse time when ValueMode == Regex

## Does
- **ParseMatchSyntax(arg string) (MatchPredicate, error)**: parses one
  argument into a `MatchPredicate`. Steps:
  1. `@` normalization (R2449): strip a leading `@` wherever it
     appears in the prefix region — `@T`, `@~T`, `~@T` all normalize
     to the same shape.
  2. Trailing-`:` normalization on the name (R2450): if the name
     portion ends with `:` and no value separator follows, strip it.
     This preserves the long-standing UX where users paste `@tag:`
     directly from a file.
  3. Name-mode detection (R2443): leading `~` → NameMode = Regex
     (RE2, case-insensitive); leading `:` → NameMode = Contains
     (lowercase + whitespace-split tokens, substring-AND); otherwise
     NameMode = Exact (case-insensitive literal). Strip the sigil
     after recognition.
  4. Separator scan: walk to the first occurrence of `=`, `:`, or
     `~` in the remaining string. If absent → ValueMode = Any.
  5. Value-mode detection (R2444–R2446): the separator picks the
     value mode. Everything after the separator is ValueStr.
  6. Empty-value handling (R2448): `T=` → ValueMode = Exact with
     ValueStr "" (match only empty); `T:` and `T~` → ValueMode = Any
     (degenerate, accepted).
  7. Compile any regex; pre-tokenize the contains bag (lowercase +
     `strings.Fields`).
- **(p MatchPredicate) MatchName(name string) bool**: returns
  whether the predicate accepts the given tag name. Exact:
  case-insensitive equality (`strings.EqualFold`). Contains:
  lowercase `name`; every token in `p.NameTokens` must be a substring
  (substring-AND). Regex: `p.NameRE.MatchString(name)` — the regex
  itself carries the case-insensitive flag.
- **(p MatchPredicate) MatchValue(value string) bool**: returns
  whether the predicate accepts the given tag value.
  - Any: always true (R2447).
  - Exact: literal compare.
  - Contains: lowercase `value`; every token in `p.ValueTokens` must
    be a substring (substring-AND, order-independent, R2445).
  - Regex: `p.ValueRE.MatchString` (no anchoring; partial matches
    succeed, R2446).
- **(p MatchPredicate) Match(tv TagValue) bool**: convenience —
  `MatchName(tv.Tag) && MatchValue(tv.Value)`.
- **(p MatchPredicate) Describe() string**: human-readable form used
  by `ark search -parse` (R2451), e.g.
  `exact:status regex:^(open|in-progress)$`. Used by both CLI parse
  output and any debug tooling.

## Collaborators
- Searcher: builds `TagChunkFilter` and `FileTagChunkFilter` from
  parsed predicates rather than from `(tag string, value string)`
  pairs.
- PubSub: stores a `MatchPredicate` on each `TagSub` instead of the
  current `Tag string + ValueRE *regexp` pair.
- Server: decodes a `ChunkFilterRow` JSON shape that names mode and
  value via the same parser when the row is a `-tag` or `-file-tag`
  entry (so the parser is shared end-to-end and behavior is
  identical across CLI and HTTP callers).
- CLI: `cmdSearch` and `cmdSubscribe` call `ParseMatchSyntax` once
  per `-tag` / `-file-tag` argument; `-parse` output uses
  `Describe()`.
