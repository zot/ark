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

## Conventions

**A carve is promoted here the moment it spawns its first queue item.** Before
that it is brainstorming and lives in the private scratch area; after it, other
work references it and it becomes a plan.

**A finished carve moves to `carves/done/`**, so completed ones do not crowd
out live ones. The move rewrites the links pointing at it rather than leaving a
stub.

**When a part lands, its decisions belong in `specs/` and `design/`** — as
requirements, spec prose, or a comment at the code. The carve then points at
where they went. A carve that keeps code facts to itself is the failure mode
this directory exists to prevent; unlike a migration, nothing forces the
handoff, so it has to be a habit.
