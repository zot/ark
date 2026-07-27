# Personal patterns

A **pattern** is a reusable technique with a name. The name is the
point: a named technique can be invoked in one word instead of being
re-derived. A collection of them is a private working vocabulary
between a person and their assistant.

Ark holds the collection as ordinary files on disk and serves it two
different ways, because **priming and retrieval are different
mechanisms and neither can do the other's job**:

| | Priming (`ark patterns list`) | Retrieval (search / recall) |
|---|---|---|
| When | Before generation | After a trigger |
| Needs | Nothing | To suspect the pattern exists |
| Carries | Names, tags, summaries | Full bodies |
| Effect | Biases what gets written at all | Answers a question already asked |

Recall requires knowing there is something to look for. Priming works
on the patterns you would never have thought to search for. The design
follows from that: the summaries are cheap enough to inject into every
session unconditionally, and the bodies are too big to inject and are
therefore reached through the index.

This is the same shape as an agent skill — frontmatter injected,
body loaded on demand — with one difference that drives the design. An
uninvoked skill does nothing; an unloaded pattern still changes the
code that gets written, because its name was in context. Priming is the
*primary* effect for patterns, not a routing hint.

Language: Go. Environment: the `ark` CLI binary.

## The collection

A directory of markdown files, one per pattern, each with YAML
frontmatter:

```markdown
---
name: Straycat
tags: performance, strings, concatenation, lua, foundational
summary: Collect string pieces in an array and join once at the end — never concatenate in a loop
---

# Straycat

<body: description, examples, history>
```

- `name` — display name. Required.
- `tags` — comma-separated. Optional.
- `summary` — one line. Required; it is what gets injected and what
  becomes the tag definition.

The **slug** is the filename without `.md`, lowercased. It is the
pattern's identity: the tag name it derives, and the argument `add`
takes.

Location is the `patterns_dir` config key, default
`~/.ark/personal-patterns/`. Configurable so an existing collection is
adopted where it already lives rather than being moved.

The **default** location is indexed out of the box: the hardcoded
`~/.ark` source's include list covers `personal-patterns/**/*.md`, which
is what makes bodies searchable and recallable without any setup. A
collection relocated by `patterns_dir` must fall under some configured
source to be retrievable.

That entry is load-bearing in a way worth stating, because its absence
does not look like a failure. `ark patterns list` reads the directory
rather than the index, so priming keeps working perfectly while search
and recall find nothing — the block looks healthy and the deep half is
silently dead.

## `ark patterns list`

Writes the injection block: the collection's names, tags, and summaries
in markdown, ordered by slug.

```
ark patterns list [--json]
```

**Must not require the server.** It runs at session start, before
anything is up, and a pattern collection that only primes when a daemon
happens to be running is worse than useless — it primes
*intermittently*, which is indistinguishable from the assistant having
inconsistent judgment. A plain directory walk over frontmatter, no
index dependency, no socket.

Output is the block verbatim, suitable for pasting into a system prompt
or a CLAUDE.md, so the snippet is generated rather than hand-maintained:

```
These patterns are in <dir>
When you notice a recurring technique worth naming, suggest adding it as a new pattern.

- **Straycat** (straycat.md)
  Tags: performance, strings, concatenation, lua, foundational
  Collect string pieces in an array and join once at the end — never concatenate in a loop
```

A file with no frontmatter, or with no `name`, is skipped rather than
listed blank. A missing or empty directory prints nothing and exits 0 —
having no patterns yet is the normal state for a new user, not an error.

`--json` emits `[{slug, name, tags, summary, path}]` for machine
consumers.

## `ark patterns add`

Authors a pattern file so nothing has to hand-format frontmatter.

```
ark patterns add <slug> --name NAME --summary TEXT [--tags "a, b"] [--body-file FILE]
```

Writes `<patterns_dir>/<slug>.md` with the frontmatter block and a
`# NAME` heading, appending `--body-file`'s contents when given, and
prints the path. Refuses to overwrite an existing slug — a pattern is
edited in place, and silently clobbering one would lose a body nobody
asked to delete. Creates the directory if absent.

The CLI owns the file shape for the same reason `ark message
new-request` does: a collection that is cheap to add to grows, and one
that requires remembering a format decays.

## Derived tag definitions

Each pattern file contributes a **tag definition** — `slug` defined as
its `summary` — derived from frontmatter at index time. No `@tag:` line
is written into the file; the frontmatter is the single source of
truth, and the definition is computed from it.

This makes the pattern vocabulary part of the tag vocabulary: `@straycat:`
becomes a tag with a real definition, `ark tag defs` lists it,
tag-definition embeddings cover it, and recall can surface the pattern
when a conversation drifts near its meaning without anyone having
searched.

Derivation applies only to files directly inside `patterns_dir`, and
only when the content being indexed carries frontmatter. On an append,
the appended bytes do not contain the frontmatter, so nothing is
derived and the definition written at full index survives — which is
correct, since an append cannot have changed the frontmatter.

A definition written by hand in a `@tag:` line elsewhere in the corpus
is unaffected: definitions are stored per file, so both coexist and
`ark tag defs` shows each with its source.

## Not `ark ui patterns`

`ark ui patterns` lists `~/.ark/patterns/`, a separate collection of
Frictionless UI development techniques with its own frontmatter shape
(`description`, not `summary`). The two are deliberately distinct: one
is a person's cross-project vocabulary, the other is documentation for
building apps against this project's UI engine. They share a frontmatter
reader and nothing else.
