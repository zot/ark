---
name: ark-search
description: "The detective's craft for searching ark. Load when investigating the corpus — what do we know about X, where is Y, did we ever discuss Z — or before designing a feature. Teaches how to direct the search (pull) and read what recall surfaces (push), the filter-stack craft, and when to delegate vs. search directly."
---

# Ark Search — the detective's craft

The binary is `~/.ark/ark`. Never bare `ark` (Linux has an archive manager).

You are **Sherlock**; ark is the **bloodhound**. Two ways the hound works
a scent, and a skilled detective uses both:

- **Pull** — you *direct* the hound down a chosen trail. That's **search**:
  you frame a clue and follow it deliberately.
- **Push** — the hound *brings* you scents you didn't ask for. That's
  **recall** (the per-session Secretary): related material surfaces as the
  conversation unfolds. Fold it in; don't suppress cross-project tangents —
  in this partnership the small talk is often big.

This skill is the **pull** craft — how to investigate well — plus how to read
the **push**. Search will *always* be needed, so the craft lives here, in one
home.

> **The reference is authoritative, by commitment.** ark keeps
> `specs/cli-commands.md` and every `--help` in lockstep with the code
> (principles.md, "the documentation tells the truth"). So this skill teaches
> *intent and patterns* and points you to the canonical reference for exhaustive
> flag lists — it does **not** duplicate them, and you do **not** need a
> "cross-check the source" habit. Trust the docs.

## Direct, bloodhound, or Hermes?

Route by what you're doing, not by how hard it feels:

- **Search directly** (you, one command) — a *known-item lookup*: a specific
  tag def, a known file, "does X exist," a quick yes/no. Fast, no hand-off.
  The filter-stack craft below is for this.
- **Delegate to the bloodhound** (warm Haiku, *forthcoming* — see below) — an
  *open question* or *investigation*: "what do we know about X," anything
  needing query refinement and curation across specs/design/code. ~5× cheaper
  than reading it yourself; you get a curated digest, not a dump.
- **Delegate to `ark-searcher`** (Hermes, *available now*) — the same open
  questions, today, via an ephemeral Haiku spawn:
  `Agent(subagent_type="ark-searcher", prompt="Find notes about append detection.")`.
  Hermes expands queries and curates; never interpret raw results yourself.

When in doubt past a single lookup, delegate. Reading broad cross-layer corpus
is exactly the work to offload to a cheaper model.

## Directing the bloodhound — the `<BLOODHOUND>` watermark

> **Status: live (2026-06-08).** The recall service recognizes `<BLOODHOUND>`
> and dispatches it to the warm secretary, which runs the hunt and returns a
> curated finding through your `recall listen` (Sherlock build-step 3 landed).
> The warm path is available **whenever recall is running**; in a no-recall
> session, fall back to **direct search** (below) or **`ark-searcher`**.

Once live, you delegate a search by **emitting a watermark** in your normal
output — no spawn, no tool call, because the recall watcher already scans the
conversation. The watermark *is* the hand-off:

```
<BLOODHOUND>investigate how recall dedup works across specs and code — want a synthesis, stop when you can name the dedup key</BLOODHOUND>
```

You state the clue in **natural prose**; the canonical *want-words* (below) are
the reliable anchor the hound keys on. It fills in the CLI craft from its crank
handle. **The watermark displays to the user by design** — they get to follow
the hunt (and judge whether you're on the trail). Write it readable.

**Async by default** — fire the hound and keep reasoning; leads surface in a
later turn, like recall. For "need it now," emit the watermark and explicitly
block on the result.

### The four fields you supply (the rest is the hound's craft)

| field     | what it carries                                                        |
|-----------|------------------------------------------------------------------------|
| **clue**  | what to find — the question, or the distinctive terms                  |
| **scope** | corpus: `code` / `specs` / `notes` / `chat` / `all`, + project + globs |
| **depth** | `lookup` (one pass) or `investigate` (refine-and-narrow loop)          |
| **want**  | the return *shape* (table below)                                       |

For `depth: investigate`, add an optional **stop** condition — "stop when you
can name the dedup key" — so the hound knows when to quit refining.

### `want` — the return shape (the tightest coupling)

`want` is orthogonal to `depth`: `want` is the output *shape*, `depth`+`stop`
is *how hard to look*. Five values span "give me a conclusion" → "give me
everything":

| `want`        | you want…                    | the hound returns                                |
|---------------|------------------------------|--------------------------------------------------|
| **answer**    | a conclusion                 | 1–3 synthesized sentences + cited sources        |
| **passages**  | material to read yourself    | the few curated chunks + sources (no synthesis)  |
| **pointers**  | just where to look           | paths / chunk locators only (no chunk text)      |
| **inventory** | the complete set             | every match, de-duped (paths or @tags), no read  |
| **verdict**   | yes/no                       | "yes" + the one best source, or "no, not in …"   |

Each value is one emit-branch the hound's crank handle teaches — the words on
both sides match exactly. Phrase naturally, but land on a want-word.

**Legible watermarks** (natural phrasing carries the fields; the want-word is
the anchor):

- `<BLOODHOUND>did we ever discuss BM25 for recall? check the chat history — yes/no with the turn</BLOODHOUND>`  *(verdict · chat)*
- `<BLOODHOUND>where's the tag-strip-at-embed logic? just point me at the file</BLOODHOUND>`  *(pointers · lookup)*
- `<BLOODHOUND>list every spec that mentions @ext routing — the complete set</BLOODHOUND>`  *(inventory · tag lens)*
- `<BLOODHOUND>show me the passages on how the curation workshop stages ops, across the specs</BLOODHOUND>`  *(passages · specs)*
- `<BLOODHOUND>what did we decide about the seehuhn GPL boundary? answer with the source</BLOODHOUND>`  *(answer · notes / cross-project)*

### Reading the return

A **curated digest** — the few passages that answer, with sources — not a raw
dump. Trust the curation. If it comes back thin, hand back a *sharper clue* or
*wider scope*; don't re-run raw searches yourself. That's the whole point of
the hound.

## Searching directly — the filter-stack craft

When you *do* search yourself (known-item lookups, or no warm hound around),
this is how ark's `search` actually works.

### Finding things, fast

```bash
~/.ark/ark tag defs --path TAG       # where a tag is defined and what it means
~/.ark/ark tag files --context TAG   # which files use a tag, with context
~/.ark/ark tag list                  # tag counts
~/.ark/ark files '**/pattern*'       # file by name pattern (all projects)
~/.ark/ark fetch --wrap knowledge /path/to/file   # a known file from the index
```

### The filter stack

**Search is a filter stack, not standalone flags.** The first filter is the
primary search; the rest are chunk-level post-filters — composable, repeatable.
Bare terms coalesce into one `-contains`. Run `-parse` to see exactly how your
args were read.

| Mode             | Match                                                  |
|------------------|--------------------------------------------------------|
| `-contains TERM` | substring (default for bare terms)                     |
| `-fuzzy TERM`    | trigram similarity (generous)                          |
| `-regex PAT`     | Go RE2 (no lookahead/backreferences)                   |
| `-tag TAG`       | tag filter (uses tag index, fast)                      |
| `-file-tag TAG`  | every chunk on a file carrying the tag                 |
| `-about QUERY`   | vector similarity (server required)                    |
| `-files GLOB`    | file path glob (doublestar `/**/` supported)           |

Polarity is sticky until changed: `-with` (must match, default) / `-without`
(subtract). Tag sigils: `name:value` = value-contains, `name=value` =
value-exact, bare `name` = any value.

**Match the matcher to the clue:**

- exact, distinctive phrase → `-contains`
- approximate term / typo-tolerance → `-fuzzy` (trigram; *generous* — the
  largest prose corpus can swamp a common-word query; tighten or switch to
  `-contains`)
- meaning, not words → `-about` (needs the server; best-effort — vectors are
  progressive while trigram stays primary, so never rely on it as the only pass)

```bash
~/.ark/ark search fred ethel                          # bare terms → -contains
~/.ark/ark search QUERY -with -files 'specs/**'       # scope to specs/ under the current project
~/.ark/ark search -contains "phrase" -regex '@tag:.*value'   # FTS + regex post-filter
~/.ark/ark search fred -without -tag status:done -with -files '**/*.md'
~/.ark/ark search QUERY -with -file-tag status        # only chunks on files carrying a tag
~/.ark/ark search -about "machine learning" -without -tag project:archive
~/.ark/ark search -parse fred -without -files '**/*.md'  # verify the parse, don't search
```

### Searching conversation history (chat recall)

Conversation logs (`~/.claude/projects/**`) and schedule logs are **excluded
from search by default** (`[search].search_exclude`) — right for normal corpus
search, wrong when the *point* is what was *discussed*. Do a **second,
chat-scoped pass**:

```bash
# Pass 1 — corpus (chat auto-excluded):
~/.ark/ark search -fuzzy "cerro gordo" -scores -k 20
# Pass 2 — chat history (a positive -files both scopes to chat AND disables the exclude):
~/.ark/ark search -files '~/.claude/projects/**' -fuzzy "cerro gordo" -scores -k 20
```

**Two passes — don't merge them.** Fuzzy scores saturate at `1.0000` for short
queries (too few trigrams to separate near-match from exact), so one combined
search can't sort the corpus and chat pools usefully — one drowns the other.
Keep them separate; merge with judgment. Single-quote globs; ark expands a
leading `~/` itself (R950).

### Output shapes (after the filter stack)

```bash
~/.ark/ark search QUERY -wrap knowledge      # XML-wrapped — best for context injection
~/.ark/ark search QUERY -chunks              # chunk text as JSONL
~/.ark/ark search QUERY -file-content        # full file text as JSONL
~/.ark/ark search QUERY -tags                # extracted @tag activity as bullets
~/.ark/ark search QUERY -k 50 -scores        # cap results, show scores
~/.ark/ark search QUERY -chunks -preview 200 # preview window around the match
~/.ark/ark chunks /path/to/file 150-175 -before 2 -after 2   # expand context around a hit
```

## The right tool for the question

| The question you're really asking          | Reach for                              |
|---------------------------------------------|----------------------------------------|
| "Does X exist / where is it?"               | `search -contains` (content) · `files` (by name) |
| "What does this tag mean / who uses it?"    | `tag defs` · `tag files --context`     |
| "What's the full set of @X?"                | `search -tags` · `tag values`          |
| "What do we know about X?" (open)           | delegate — bloodhound / `ark-searcher` |
| "Did we ever discuss X?"                    | chat two-pass (above)                  |
| "Why did/didn't this query match?"          | `grams QUERY` · `search -parse`        |
| "Read this passage + its surroundings"      | `chunks PATH:RANGE -before/-after`     |
| "Is the corpus even healthy / indexed?"     | `status` · `files` · `missing/stale`   |

## The investigation loop

For anything past a single lookup, work the trail in this order:

1. **Orient** — if it's a mini-spec project, load `/minimap` and read the root
   index first; one small read replaces grepping the tree.
2. **Find** — match the matcher to the clue; cast with `-fuzzy`, then tighten.
3. **Narrow** — too noisy? scope by path or tag: `-with -files '**/*.md'`,
   `-with -files 'specs/**'`, or `-with -tag NAME`. Too thin? widen: `-fuzzy`,
   drop a filter, `-about`.
4. **Read** — pull the top hits with `chunks … -before 2 -after 2`.
5. **Follow tags** — a shared tag is a cross-file link; `tag files --context
   TAG` walks the link graph to every section that carries it.
6. **Corroborate** — don't stop at one hit; confirm against a second source
   before you conclude.

## Search before you design

Before planning or building a feature, ask ark what it already knows — it
connects dots across projects and old design conversations you've forgotten:

```bash
~/.ark/ark search --contains "topic keywords" --chunks --wrap recall
```

Read the results before writing anything; they often change the approach.

## Gotchas (the ones that bite)

- **Chat is excluded by default** — use the positive `-files` pass to include it.
- **`-fuzzy` is generous** — a big project swamps common-word queries; short
  queries (≤~3 trigrams) saturate at `1.0000`, so judge by content, not score.
- **`-files` globs are cwd-relative** — a relative glob resolves under the
  directory you ran ark from (UNIX-style): `'specs/**'` matches this project's
  `specs/`. A bare `'*.md'` is cwd top-level only — use `'**/*.md'` for any
  depth. Reach outside the cwd with an absolute path or `~/`; `tmp://` passes
  through untouched.
- **Always wrap retrieved content** (`-wrap` / `--wrap`) — gives source attribution.
- **`ark tag defs`** not grep, for tag definitions. **`ark fetch`** not Read,
  for indexed files from other projects.
- **Tags are line-start-only and single-line** — indented or wrapped `@tag:`
  won't index as you expect.

## The authoritative reference

The exhaustive, current inventory is `specs/cli-commands.md`, mirrored by every
subcommand's `--help` (kept in lockstep with the code by project commitment).
When you need a flag this skill didn't name, go there — not to the source.
