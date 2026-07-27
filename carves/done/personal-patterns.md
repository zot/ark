# Carve: personal patterns as an ark feature

**Opened** 2026-07-26 · **Closed** 2026-07-27 · **Items** #75, #77 — both landed

**CLOSED.** The feature shipped and the hand-maintained tag mirror is gone; each
pattern file is now the single source of its own definition. Nothing here is
pending. Retained as the record of why it was built this way.

Making a person's private working vocabulary a first-class part of ark: named
techniques that prime every session cheaply and can be retrieved in full when
the conversation nears one.

## What this is

A **pattern** is a reusable technique with a name — the name is the point,
because a named technique can be invoked in one word instead of re-derived.
Bill has kept 54 of them at `~/.claude/personal/patterns/` for years (Closure
Actor, Baby Food, Straycat, Soviet Supermarket…), and their index is injected
into every Claude Code session's system prompt.

Shipping this in ark makes the technique available to anyone, which is Bill's
framing: *"a way for people to easily access that powerful tool."*

## The mechanism, stated correctly

Getting this right decides the design, and it was got wrong once already.

Daneel's first proposal was to *replace* the system-prompt injection with
recall, on the arithmetic: the injected index costs ~15 KB (~4K tokens) per
session, in every project, forever, while carrying only names and summaries —
94% of what makes a pattern useful (247 KB of bodies) is exactly what it
can't fit.

**Bill's correction (2026-07-26), which is the load-bearing insight:**

> There's one huge advantage to dumping the names and descriptions into every
> session — they're all in-context at the *top* of the context. So they
> actively bias generated code, even if the agent doesn't load the pattern.
> But since they're there, the agent will normally load the pattern when it's
> relevant. This keeps our "private language" on the tip of your tongue. You
> don't need to know in advance whether, and why, a pattern is worth searching
> for.

These are **two different mechanisms** and one cannot do the other's job:

| | Priming (injection) | Retrieval (recall) |
|---|---|---|
| When | Before generation | After a trigger |
| Needs | Nothing | To suspect the pattern exists |
| Carries | Names + summaries | Full bodies |
| Effect | Biases what gets written at all | Answers a question already asked |

Recall requires knowing there is something to look for. Priming works on the
patterns you'd never have thought to search. The 4K tokens are not overhead;
they buy the shared vocabulary.

This is the Soviet Supermarket pattern turned on the pattern collection
itself: position it where the agent looks first, because the doc is a runway
rather than a reference manual.

**So the feature does both**, with a clean split of duties:

- **`ark patterns list` → the injection block.** Names, tags, summaries.
  Cheap, always on, and **must not require the server** (Bill, 2026-07-26) —
  it runs at session start, the same way `personal patterns` does today.
  A plain directory walk over frontmatter, no index dependency.
- **The ark index → bodies on demand.** Patterns become ordinary corpus
  content: searchable, and surfaced by `/recall` with their full text when the
  conversation nears them. This is the half only ark can add.

## Lineage: this is the skills mechanism

Bill, 2026-07-26: *"It's really the same as Anthropic's 'skills' approach —
not new tech from me. Just my own spin on it."* True and worth stating
plainly, in the announcement too: skills carry frontmatter whose descriptions
are injected into context, with the body loaded on demand. Identical shape.

Two differences are load-bearing for the design rather than for credit:

- **A skill is a procedure you invoke; a pattern is vocabulary that biases
  you.** An uninvoked skill does nothing at all. An unloaded pattern still
  changes the code that gets written, because the name was in context. That
  asymmetry is why the injection block is worth its tokens here — priming is
  the *primary* effect for patterns and merely a routing hint for skills.
- **Skills are authored for the agent; patterns are extracted from work.** The
  collection grows by noticing something twice, which is what the `add` verb
  (fork 2) exists to make cheap.

What ark adds that neither has: patterns living in a searchable corpus, so a
third mode appears alongside injected-summary and loaded-on-request — surfaced
*unprompted* when the conversation drifts near one.

## Decided

- **Location:** `~/.ark/personal-patterns/`. Bill: moving his collection there
  is fine. Name is `personal-patterns`, not `usage-patterns` — one collection,
  not two, with ark-usage techniques distinguished by tag rather than by
  directory (Earned Generalization: split only when a second instance demands
  it).
- **`ark patterns list` works server-less**, matching today's behavior.
- Ark-usage techniques found on 2026-07-26 (below) are seed entries for the
  collection, tagged `ark, usage`.

## Open forks — all settled 2026-07-27

1. **Frontmatter, ark tags, or derived? → derive at index time.**
   **DECIDED (Bill, 2026-07-27).** Frontmatter stays the file format and the
   single source of truth; ark computes the tag definition from `name` and
   `summary` when it indexes the file, writing no `@tag:` line into it.

   The suspicion recorded here was right, and measuring it turned the fork
   from a preference into a repair. `~/.ark/tags.md` was not a coincidence but
   a **hand-maintained mirror**: of its 69 `@tag:` definitions, **40 were
   pattern slugs restating the frontmatter summary by hand**, and **14 of the
   54 patterns had no definition at all** — the recent ones (batteries-included,
   call-button, living-heir, shoulder-the-fat, try-the-handle, turnstile,
   watermark, and seven more). A 26%-stale mirror, drifting in exactly the
   direction that hurts: newest patterns least covered. Derivation closed all
   14 on the first index pass.
2. **Does it author? → yes.** `ark patterns add` (R3273). Refuses an existing
   slug rather than overwriting, since a pattern is edited in place.
3. **Adoption vs migration → both.** `patterns_dir` (R3268) makes the location
   configurable so an existing collection is adopted where it lives; the
   default is `<db>/personal-patterns`. Bill copied his there and will point
   the `personal` script at `ark patterns list`.
4. **Does `list` emit the block verbatim? → yes** (R3269). Its bytes are the
   contract and a test pins them, so the prompt snippet is generated rather
   than hand-maintained.

## What landed (#75, 2026-07-27)

R3267–R3278. `ark patterns list` / `add`, the `patterns_dir` key, frontmatter-
derived tag definitions, and a shared frontmatter reader with `ark ui patterns`.
Behavior lives in `specs/personal-patterns.md`; structure in `crc-Patterns.md`
and `seq-patterns-derive.md`.

Three things the build found that the design had not anticipated:

- **The default location was not indexed.** The hardcoded `~/.ark` source uses
  an explicit include allowlist, which did not cover `personal-patterns/`. The
  failure mode is the nasty kind: `list` reads the directory rather than the
  index, so priming would have kept working perfectly while search and recall
  found nothing, with nothing reporting the discrepancy. Fixed by R3278; the
  reasoning is recorded at the include list itself, not only here.
- **`ark ui patterns` already existed**, server-less, doing the same
  frontmatter walk over a *different* collection (`~/.ark/patterns/`, the
  Frictionless UI techniques, keyed on `description`). The two now share a
  reader and nothing else (R3277). Its spec also claimed every UI subcommand
  needs the server, which was false for this one — corrected.
- **One of the 54 pattern files has no frontmatter at all**
  (`self-contained-tools.md`, which uses a prose `Tags:` line). The shell
  implementation printed it as `- **** (…)`: a blank entry that has been
  sitting in every session's system prompt, priming nothing, looking like a
  pattern. Skipping it is R3271; the file itself still wants fixing.

## The tag mirror, removed (#77, 2026-07-27)

The item was filed as "strip 40 redundant duplicates." Measuring it first showed
that framing was wrong twice over, and acting on it literally would have
destroyed data.

**Only 13 of the 37 overlapping definitions were actually identical.** The other
24 were independently edited variants, diverging in *both* directions — the
`tags.md` line expanded the ANLP acronym, labelled `lock-flap` an anti-pattern,
and noted that `sublooper` composes with lotto-tube, while the pattern file was
richer for `alibi-stamp`, `baby-food`, and `librarian-search`. Stripping either
side blindly loses something written deliberately. On inspection only **4**
carried a clause the pattern file did not already hold — the rest differed by
punctuation, ordering, or facts already living in the file's `name`/`tags`
fields, which is why "24 conflicts" collapsed to four one-line merges.

**And 3 would have been orphaned outright.** `context-coupling`, `secretary`,
and `self-contained-tools` had incomplete frontmatter and derived nothing, so
their `tags.md` line was their *only* definition. This is also what produced the
blank `- **** (self-contained-tools.md)` entry: `List` needs only `name`,
derivation needs `name` **and** `summary`, so a file can list while silently
deriving nothing. All three repaired; `secretary` also had YAML-array tags
(`[agentic, …, delegation]`) that left brackets stuck to the first and last tag.

**DECIDED (Bill, 2026-07-27) — what `self-contained-tools` actually is.** Its
tag definition described self-contained *output* ("pairs with crank-handle")
while its body described self-contained *distribution*, and `batteries-included`
seemed to cover the latter. Daneel proposed retiring or splitting it. Both were
wrong, and the collection already held the answer: `batteries-included.md` cites
`[[self-contained-tools]]` and draws the line itself — *"the system-side version:
the tool carries its whole world, needs nothing external to **run**. Batteries
Included is the interface-side corollary: the client needs nothing external to
**use** it."* So the **body was right and the tag definition was the drift**; the
stale definition described crank-handle. Kept, with a summary drawn from the
body and the neighbour's characterisation.

The lesson generalises past this file. A mature pattern here states its own
borders — `turnstile.md` has a "Relationship to other patterns" section
distinguishing it from Hermetic Seal, Crank Handle, Greased Pig, and Lotto Tube.
`self-contained-tools.md` predates frontmatter and could not, so its neighbour
had to carry the distinction, and the drift went unnoticed for as long as nobody
read the neighbour. Stating borders is what makes drift visible.

**Result:** 40 definitions removed, 29 non-pattern ones untouched, all 54
patterns deriving exactly one definition. The two section intros in `tags.md`
were rewritten rather than deleted — the convention prose is still useful, but
it pointed at the pre-move path and implied the definitions lived below it.

One apparent duplicate survives and is correct: `crank-handle` resolves to two
definitions because Bill's blog posts define the tag for their own audience.
That is R3276 working as designed — D records are keyed per file, so corpora
coexist with attribution instead of clobbering each other.

## Why it belongs in the release

Ties **#20** (pre-release announcement). The hook is stronger than any search
feature: *your assistant learns your private language, and the corpus keeps it
on the tip of its tongue.* Patterns are the artifact that makes an AI
collaborator improve across sessions, and ark is what makes them both ambient
(primed) and deep (recalled).

## Seed entries from 2026-07-26

Ark-usage techniques found during #67's first live test — see
the private note EXT-FIRST-LIGHT-20260726.md:

- **Targeted reindex** — `ark remove <path|pattern>` + `ark scan` rebuilds only
  those files; far faster than a full rebuild (Bill, 2026-07-26).
- **You cannot force re-derivation by touching a file** — a touch takes the
  append path (`newChunks=0`); restoring the original bytes hits the
  `unchanged` short-circuit. Both silently do nothing.
- **Isolate a test drive with `HOME`, not `--db`** — and a source over the DB
  directory needs an explicit `include` list, or the scan indexes zero files,
  including unrelated sources.
- **`ark connections clean`** wipes recall substrate (RC/RD/RM; `--all` for
  RF/RJ) for testing.
- **Check the binary's date before believing a surprising result.**
- **Disbelieve a zero** — a diagnostic reporting 0 looks identical whether the
  count is zero or the counter is broken. Cross-check against the verb that
  reads the records.
