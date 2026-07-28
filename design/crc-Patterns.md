# Patterns
**Requirements:** R3267, R3269, R3271, R3272, R3273, R3277, R3297

Reads and writes the personal pattern collection — a directory of
markdown files whose frontmatter carries a named, summarized technique.
Owns the file shape, the injection block, and the frontmatter reader
that `ark ui patterns` also uses.

**Filesystem only.** Patterns holds no DB handle and no HTTP client, and
nothing in its API can reach one. That is not incidental: `list` runs at
session start before a server exists (R3270), and the cheapest way to
guarantee a verb never needs the server is to give its component no way
to ask for one.

The **slug** — basename without `.md`, lowercased — is a pattern's
identity throughout (R3267). Display names are data; slugs are keys.

## Knows
- dir: string — the collection directory, from `patterns_dir` (R3268, owned by crc-Config.md)
- Pattern: {Slug, Name, Summary, Path string; Tags []string} — one parsed file

## Does
- List: walk `dir` for `*.md`, parse each file's frontmatter, skip any file lacking frontmatter or `name`, return sorted by slug (R3267, R3271). An absent or empty directory returns an empty slice and no error — a new installation has no patterns yet, which is not a failure (R3271).
- WriteBlock: render the injection block — directory header, the suggest-a-pattern line, then `- **Name** (slug.md)` with indented `Tags:` and summary lines (R3269).
- WriteJSON: render `[{slug, name, tags, summary, path}]` in the same slug order, `tags` as an array (R3272).
- Add: write `<dir>/<slug>.md` with frontmatter plus a `# Name` heading, append a body file when given, create `dir` if absent, and refuse an existing slug with an error rather than overwriting (R3273).
- DerivedDef: given a file path and the content being indexed, return `(slug, summary, ok)` when the path is a `*.md` directly inside `dir` **and** the content carries frontmatter with `name` and `summary`; `ok=false` otherwise. The content test is what makes the append path derive nothing (R3274, R3275) — see crc-Indexer.md.
- parseFrontmatter: read a leading `---`-delimited block into `map[string]string`. Shared with `ark ui patterns`, which keys on `description` where this collection keys on `summary` (R3277).

## Collaborators
- Config: supplies `dir` via `patterns_dir` (R3268)
- Indexer: calls DerivedDef during file-level tag extraction, turning the result into a D record (R3274)
- CLI: `ark patterns list` / `ark patterns add` (crc-CLITree.md, R3270)

## Sequences
- seq-patterns-derive.md
