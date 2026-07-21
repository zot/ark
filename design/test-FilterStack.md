# Test Design: FilterStack
**Source:** crc-CLI.md, crc-CLITree.md, seq-filter-stack.md

The filter stack was a `search`-only DSL exercised only by hand (gap O80).
#51 makes it the single path-glob surface for six commands, so its arg
walker is now load-bearing for `files`, `status`, `tag files`, `tag values`,
and `subscribe` as well. It is a pure deterministic string transform — no
DB, no server, no fixtures — so it is tested rather than deferred.

These tests cover `parsePathFilterStack`, the **path-only subset** the five
non-search commands use. It is a sibling of `search`'s `parseFilterStack`,
not a call into it, because the search walker coalesces bare terms into a
`-contains` group and would swallow these commands' positional arguments.
The two share the polarity grammar and give a `-files` row identical
meaning. (`parseFilterStack` itself remains untested — the residue of O80.)

## Test: polarity is sticky across entries
**Purpose:** `-without` applies to every subsequent row until `-with`
**Input:** `-files a.md -without -files b.md -with -files c.md`
**Expected:** include = [$PWD/a.md, $PWD/c.md], exclude = [$PWD/b.md]
**Refs:** crc-CLI.md, seq-filter-stack.md, R3204

## Test: non-stack args pass through untouched
**Purpose:** The walker claims only its own tokens; flags *and* positionals
survive for the caller's flag.Parse. This is the reason the path stack is a
sibling of the search walker rather than a call into it — `ark tag files
mytag` must keep its TAG.
**Input:** `--status -files '*.md' mytag --detail extra`
**Expected:** one anchored include row; remaining = [--status, mytag, --detail, extra]
**Refs:** crc-CLI.md, seq-filter-stack.md, R3204

## Test: double-dash normalizes to single
**Purpose:** `--files` and `-files` are one token — which is precisely why
the `tag values` boolean had to become `--show-files`
**Input:** `--files status`
**Expected:** one row whose glob is the anchored `status`; nothing remains
**Refs:** crc-CLI.md, crc-CLITree.md, R3206

## Test: no positive row means all paths are candidates
**Purpose:** A stack of only `-without` rows narrows rather than empties
**Input:** `-without -files 'vendor/**'`
**Expected:** a path outside the exclusion still passes; one inside is rejected
**Refs:** crc-CLI.md, R3204

## Test: --filter-files exits non-zero and names the semantic change
**Purpose:** A pointing error, not an alias — the shape whose meaning changed
fails silently (fewer results), so the user must learn both facts at once
**Input:** `ark files --filter-files '*.md'`
**Expected:** non-zero exit; stderr names `-files` **and** states that a bare
no-slash glob is now top-level-only. Same for `--exclude-files`, and on all
five commands.
**Refs:** crc-CLI.md, crc-CLITree.md, R3205

## Test: `tag values --files` does not swallow the TAG
**Purpose:** The collision R3206 exists to prevent — `--files` normalizes to
`-files` before flag.Parse and would consume the following TAG as its glob
**Input:** `ark tag values --show-files status` (correct form), and
`ark tag values --files status` (retired form)
**Expected:** the first lists per-file lines for tag `status`; the second is
an error, never a silent misparse in which `status` became a path glob
**Refs:** crc-CLI.md, crc-CLITree.md, R3206

## Test: anchoring is idempotent
**Purpose:** Relative rows anchor, absolute and `~` rows pass through, and a
second pass over an already-anchored list is a no-op — which is what lets
`filterPaths` anchor on every call without compounding
**Input:** `-files '*.md' -files '/**/*.md' -files '~/x/**'`, cwd=/proj
**Expected:** [$PWD/*.md, /**/*.md, ~/x/**]; re-anchoring returns the same list
**Refs:** crc-CLI.md, crc-Searcher.md, seq-filter-stack.md, R3197, R3208
