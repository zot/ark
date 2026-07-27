# Test Design: Patterns
**Source:** crc-Patterns.md

All cases run against a `t.TempDir()` collection. No DB, no server, no
fixtures — Patterns is filesystem-only by design (crc-Patterns.md), which
is exactly what makes it cheap to pin.

## Test: List parses and orders by slug
**Purpose:** R3267, R3269 — slug is the key and the order is stable.
**Input:** three files written out of alphabetical order (`straycat.md`, `alibi-stamp.md`, `baby-food.md`), each with `name`, `tags`, `summary`.
**Expected:** three Patterns returned in slug order alibi-stamp, baby-food, straycat; `Tags` split and trimmed into a slice; `Slug` lowercased basename.
**Refs:** crc-Patterns.md

## Test: List skips unusable files
**Purpose:** R3271 — a bad file is skipped, not emitted blank.
**Input:** a file with no frontmatter at all, a file with frontmatter but no `name`, a non-`.md` file, and one valid pattern.
**Expected:** exactly one Pattern returned; no error.
**Refs:** crc-Patterns.md

## Test: List tolerates an absent or empty collection
**Purpose:** R3271 — a new installation is not an error.
**Input:** (a) a `dir` that does not exist; (b) an existing empty dir.
**Expected:** empty slice, nil error, in both cases.
**Refs:** crc-Patterns.md

## Test: WriteBlock emits the injection block verbatim
**Purpose:** R3269 — the block is pasted into system prompts, so its bytes are the contract.
**Input:** one pattern with tags, one without.
**Expected:** exact string match — directory header line, suggest-a-pattern line, blank line, then per pattern `- **Name** (slug.md)`, an indented `Tags: ...` line only when tags exist, the indented summary, and a trailing blank line.
**Refs:** crc-Patterns.md

## Test: WriteJSON shape
**Purpose:** R3272 — machine consumers never parse the markdown.
**Input:** one pattern with two tags, one with none.
**Expected:** array in slug order; `tags` is `["a","b"]` and `[]` respectively (never null); `path` absolute.
**Refs:** crc-Patterns.md

## Test: Add writes the file shape and creates the directory
**Purpose:** R3273 — the CLI owns the format.
**Input:** `Add` into a `dir` that does not exist yet, with tags and a body file.
**Expected:** directory created; file contains the `---` frontmatter block with name/tags/summary, then `# Name`, then the body file's contents; a subsequent `List` returns it.
**Refs:** crc-Patterns.md

## Test: Add refuses an existing slug
**Purpose:** R3273 — never clobber a body nobody asked to delete.
**Input:** `Add` twice with the same slug, the second with a different summary.
**Expected:** second call returns an error; the file on disk is byte-identical to what the first call wrote.
**Refs:** crc-Patterns.md

## Test: DerivedDef accepts a pattern file with frontmatter
**Purpose:** R3274 — the definition is computed, never written into the file.
**Input:** full content of a valid pattern file directly inside `dir`.
**Expected:** `(slug, summary, true)`; the file on disk is unmodified (no `@tag:` line added).
**Refs:** crc-Patterns.md, seq-patterns-derive.md#1.1.3

## Test: DerivedDef rejects out-of-scope paths
**Purpose:** R3275 — derivation is scoped to the collection's top level.
**Input:** (a) a valid pattern file in a sibling directory; (b) the same content at `dir/nested/x.md`; (c) `dir/notes.txt`.
**Expected:** `ok=false` for all three.
**Refs:** crc-Patterns.md, seq-patterns-derive.md#1.1.3.1

## Test: DerivedDef rejects append bytes — the R3275 case
**Purpose:** R3275 — the append path must derive nothing, and must do so without knowing it is the append path.
**Input:** the path of a real pattern file, paired with a content slice that is a *mid-file* fragment (no leading `---`), as `executeAppend` passes.
**Expected:** `ok=false`, so `AppendTagDefs` receives no derived entry and the definition written at full index survives.
**Refs:** crc-Patterns.md, seq-patterns-derive.md#2.1.2.2

## Test: CLI resolves the collection without an index
**Purpose:** R3268, R3270, R3271 — the three ways `patternsCollection` can be asked for a directory, none of which may open the DB.
**Input:** an arkDir temp directory that (a) has an ark.toml setting `patterns_dir` elsewhere, (b) has an ark.toml setting nothing, (c) has no ark.toml at all.
**Expected:** (a) the configured path; (b) `<arkDir>/personal-patterns` — which passes only if LoadConfig supplied dbPath; (c) the same default, and listing it returns empty with no error.
**Refs:** crc-Patterns.md, crc-CLITree.md

## Test: parseFrontmatter serves both collections
**Purpose:** R3277 — one reader, two schemas.
**Input:** a personal-pattern file (`name`/`tags`/`summary`) and a UI-pattern file (`name`/`description`).
**Expected:** both parse to maps carrying their respective keys; the reader itself privileges neither schema.
**Refs:** crc-Patterns.md
