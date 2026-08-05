# Carves

A **carve** is the working document for one coherent problem that is too big
for a single work session. It holds the decisions, the open forks, and the
split into schedulable pieces.

It exists because coherence and schedulability have different natural units. A
problem is coherent at the size of "the Tag Forge as a review console" — change
one decision and the others move. Work is schedulable only in pieces that fit
one session with one skill loaded, and in ark that limit is explicit: the Go
and the Frictionless-UI workflows are forbidden from mixing in the same stream.
So no single session can hold a coherent problem, and the work queue can only
hold pieces. The carve carries the whole.

This directory is the layer the project was missing:

| Layer | Answers |
|---|---|
| `specs/` | what the system does |
| `design/` | how it is built |
| **`carves/`** | **why it was done this way, and how the work split** |

A carve is deliberately not a spec. Specs describe how the system *is*; a carve
is a work-management artifact, and it records wrong turns alongside the
decisions that survived. It is also not a migration (`specs/migrations/`): a
migration has a defined end state and expires by design, while a carve has no
terminal moment and decays as its parts land.

## Reading one

- **`#N`** refers to an item in the project's work queue. The queue itself is a
  private working file, so the number will not resolve to anything here.
  Landed items name their commit, which does.
- **`Rn`** is a requirement in `design/requirements.md`; **`On`** is a gap in
  `design/design.md`. Both are in this repo.
- **SHOUTING-NAME.md** in prose, without a link, is a private working note.
  What it contributed is stated in the sentence that names it, so nothing is
  lost by not having the file.
- **`DECIDED (name, date)`** marks a settled call, with who settled it. Prose
  without that marker is thinking, not a decision.
- **A `## Status` block near the top** is the whole answer to "what is left?".
  One checkbox line per part, and nothing below it repeats a part's title — so
  the two places carry different things and cannot disagree. See *Status* under
  Conventions for the shape.

## Conventions

**The test is "is this a meaty task?", and it is the maintainer's call.** No
count of queue items makes a document a carve. What makes one is that the
problem is substantial enough to stay open for a while and to be worth naming —
because once it has a name, the work around it is easier to manage.

The item count is a **prompt to ask**, not a test that decides. Once a document
has spawned a second item it is worth raising; before that it usually is not.
But a single-item document can be carved on the expectation of more, and a
two-item document can stay a working note if nobody needs the name. Ark's TUI
carve was promoted while it had exactly one item, for precisely that reason.

Aim for a small number of live carves. They are a management tool, and a
directory full of them stops being one.

**This directory is public, so promotion is publication** — and that is a
second, independent question from the one above. A carve whose
*reasoning* is private (positioning deliberations, anything citing the
fossil-only working docs) has a private counterpart directory and stays there;
publishing it would hand a reader dangling pointers to files the repository
does not contain. Ask both questions, not just the first.

**DECIDED (Bill, 2026-08-04): the checkbox block, over the two-column table.**
Three carves had independently reached for a status view — struck headings with
`LANDED`/`DONE` markers, a header summary line, and a `| Part | Status |` table
in `chat-browser.md` (`2b17abb`, 2026-07-28). The table was the closest of the
three and worked; it lost on uniformity rather than on merit. `- [ ]` / `- [x]`
is the notation `design/design.md` already uses for gaps, so both documents read
the same way, and one grep answers across every carve at once — which a table
cannot do unless every carve independently picks the same columns, which is
precisely what did not happen. Recorded because the table is still in the
history and would otherwise look like something lost by accident.

**Status: a `## Status` block near the top, and bare `Item N` below it.** A
carve runs for months and grows past the length anyone reads end to end, so
"what is still open?" has to be answerable from the top of the file. The
block is one line per part:

```markdown
## Status

- [x] ~~**Item 1 — record and resolve.**~~ **LANDED (4c6e974, 2026-08-04.)**
- [ ] **Item 4 — fail fast when onboarding does not take.** **OPEN (#122.)**
```

Further down, the part's detail is keyed by **bare number, with no title and no
description**:

```markdown
- ~~**Item 1**~~ (`7892e96`, `2e66ad2`, `4c6e974`). LinkTable, both scanner
  halves, the `L` record class with its `arkLabels` label.
```

**That split is the point, not a style preference.** The block owns the title
and the status; the body owns the detail. Neither repeats the other, so there is
no second copy to drift — which is the failure this project keeps meeting
everywhere else, and the reason a status *table* duplicating body prose would be
worse than no table at all.

Three conventions inside it:

- **The checkbox is authoritative.** The line encodes the same fact three times
  — checkbox, strikethrough, `LANDED` — because each is read by someone
  different (a grep, a skimmer, a reader wanting provenance). When they
  disagree, the checkbox wins; it is the one a tool can read, and it matches the
  `- [ ]` / `- [x]` gap notation in `design/design.md`.
- **`LANDED (commit, date)` mirrors `DECIDED (name, date)`** deliberately —
  same shape, same greppability, and the commit is what makes the claim
  checkable. `LANDED` rather than `DONE`, because `DONE` is taken: the project's
  work queue uses it for completed queue items, a different thing.
- **State the negative.** A part with no marker is indistinguishable from a part
  nobody has thought about. `OPEN (#N)` naming its queue item, or `OPEN` alone
  where none exists, and `NOT VERIFIED` where a part looks landed but has not
  been checked — that last one is worth its own words, because a repaired
  *symptom* reads exactly like a satisfied *requirement*.

Three things the format allows to vary, because forcing them would lose
information:

- **Key parts however the carve already does** — `Item N` where the carve wrote
  a numbered split, a queue `#N` where every part has one. The queue number is
  better when it exists, since it is the same identifier the work is scheduled
  under.
- **The verb says what actually happened.** `SENT` for a part discharged by a
  cross-project message, `DEFERRED` for one deliberately parked rather than
  merely unstarted. Forcing `LANDED` onto a message that has no commit would
  make the marker a worse record than the sentence it replaced.
- **Not everything takes a checkbox.** Banked gaps, accepted residue, and
  already-in-place preconditions are listed without one — the same distinction
  `design/design.md` draws when an approved gap carries no box because there is
  nothing to close.

The at-a-glance view is the block itself. A machine one, across every live
carve at once:

```
grep -n '^- \[ \]' carves/*.md --exclude=README.md
```

(Excluding this file, whose examples above would otherwise answer.)

**A finished carve moves to `carves/done/`**, so completed ones do not crowd
out live ones. The move rewrites the links pointing at it rather than leaving a
stub.

**When a part lands, its decisions belong in `specs/` and `design/`** — as
requirements, spec prose, or a comment at the code. The carve then points at
where they went. A carve that keeps code facts to itself is the failure mode
this directory exists to prevent; unlike a migration, nothing forces the
handoff, so it has to be a habit.
