# Secretary pools — Luhmann-hosted pools of Haiku secretaries

A **pool** is a set of interchangeable Haiku secretaries that Luhmann hosts on
the watcher's behalf, serving work that belongs to no single session. Luhmann is
the hands (it holds the Task tool, so it launches and stops them); the watcher is
the go-between that decides when one is needed and which one gets a given piece
of work.

This spec owns the machinery **every** pool shares. What a pool's work *is* —
the doc shapes, the tags that trigger it, what a secretary does with it — belongs
to the class's own spec:

| Class | Serves | Spec |
|---|---|---|
| `bloodhound` | external `ark bloodhound search` hunts and stranded watermarks | [bloodhound-cli.md](bloodhound-cli.md) |
| `recall` | ambient curation docs for sessions with no secretary of their own | [simple-recall.md](simple-recall.md) |

## Why the class is the unit

A pool is a **class-scoped** resource, and every part of it is named for its
class: the roster, the config keys (`class.<NAME>.pool_max`,
`class.<NAME>.cooldown_seconds`), the stand-up and stop directives Luhmann
receives, and the spawn/exit records that feed the streak machine.

That has to stay true as classes are added. A second kind of work does not
become a *customer* of an existing pool; it becomes a **class**, with its own
roster, its own limits, and its own autoscale. Declaring work a user of a pool
without making it a member of a class is how ambient recall came to fire
curation docs that no secretary could see: the concept had no structural home,
every requirement traced, and nothing was reported missing.

**Classes do not share slots.** Two classes with independent `pool_max` cannot
starve one another, and their limits are tuned for their own arrival rates —
which differ sharply here, a deliberate watermark against every turn boundary of
every enabled session. Dispatch-order preference within a class is a separate
matter, owned by the class.

## Identity — where a secretary's nonce comes from

A pool secretary's identity is born where the launch happens, and the watcher
learns it from the reservation rather than minting it.

- **`ark connections recall reserve-nonce --luhmann --class C`** reserves the
  next monotonic nonce and registers it in class `C`'s roster as a pending pool
  secretary. The reservation doubles as registration, so the watcher learns the
  nonce with no report-back call. `--class` defaults to `bloodhound`.
- Luhmann spawns the secretary with its **nonce** and records it with
  `spawn-record --class C`. It does not pass an address: the secretary's `next`
  derives the composite itself (below). The watcher holds both halves already,
  the seat owner's lease and the reserved nonce, so it composes the identical
  composite with nothing reported back, and addresses work to that secretary by
  tagging `@ark-secretary-work=<composite>`.
- **`ark luhmann exit-record --class C`** deregisters the nonce when the
  secretary finishes, whether by a clean context-limit fill, a crash, or a
  quit-early. Without the bookend a self-exited secretary lingers in the roster,
  miscounting `pool_max` and drawing work to a dead tube. Deregistration is
  idempotent with pruning, so a self-exit and a stop directive reconcile safely.

The composite is the secretary's **address**, and it is per-secretary rather than
per-class, so a class's members never contend for the same tube.

### The address is derived, never typed

A secretary is not told its own tube. `ark connections recall next` already
sends the calling agent's identity alongside its nonce, and the server already
holds both halves of the answer: the nonce's class roster, filed by
`reserve-nonce`, and the nonce → parent-session registration made at reservation
and refreshed on every iteration. So when `--tube` is omitted the server derives
the address. A nonce in a pool roster resolves to `<client-session>-<nonce>`; a
nonce that is not resolves to the bare `<client-session>`, which is a session's
own secretary. `--tube` survives only as an explicit override.

The alternative is a *typed* id, and an id a model types is a snapshot. Claude
Code may re-host a session under a new id while replaying the old one into the
conversation, so a secretary spawned with the stale composite subscribes to a
tube nobody publishes to and blocks forever, which reads exactly like a corpus
with nothing to say. Resolving at use time from the calling process's own
environment removes that class of failure instead of narrowing its window.

**One boundary, stated here rather than discovered later.** The derivation is
only as fresh as the *secretary's* environment, which is fixed when it is
spawned, since a subagent's `$CLAUDE_CODE_SESSION_ID` reports its parent's id as
of that moment (see [subagent-environment.md](subagent-environment.md)). A
secretary whose parent is re-hosted mid-life would therefore still compute the
pre-fork composite. That does not bite in practice: subagents live inside the
parent process and die with it, so a re-hosted orchestrator spawns fresh
secretaries rather than inheriting the old ones.

## Occupancy — busy on dispatch, free on return

The watcher is touched on every secretary transition, so occupancy stays entirely
local to it and needs no separate state machine.

A secretary is marked **busy** when the watcher dispatches work to it, and free
the instant that work's return lands back at the watcher. Freeing happens
*before* any downstream curation of the result: the secretary's occupancy ends
when it stops working, not when the work's consequences finish.

A freed secretary enters a **cooldown** (`class.<NAME>.cooldown_seconds`) during
which it stays warm and is preferred for the next dispatch. Only past cooldown is
it eligible to be stopped.

**Occupancy is marked by the dispatching path, so every kind of work a class
serves must dispatch through it.** Work that a secretary finds for itself,
without passing the watcher, is invisible to the pruner — which then reads a
working secretary as idle and retires it. This is not a hazard to be remembered;
it is a reason for the watcher to be the only way work reaches a secretary.

## Autoscale — the watcher sizes, Luhmann staffs

The watcher pushes directives onto Luhmann's `next` seat and Luhmann carries
them out:

- **Stand up another** — when no secretary of the class is free and the roster
  is under `class.<NAME>.pool_max`. The pending work is dispatched once the new
  secretary registers.
- **Stop one** — when a secretary of the class has been idle past its cooldown.

Both directives name their class, and the crash and quit-early streak machines
count per class ([luhmann.md](luhmann.md)), so one class storming does not pause
another.

When no secretary is free and the roster is already at `pool_max`, the work
waits. What waiting *means* is the class's to define: a CLI hunt reports a busy
pool that `--wait` rides out, while an ambient recollection simply queues.

## When a pool cannot serve at all

A pool exists only while a Luhmann is seated. With no seat there is no one to
stand a secretary up, so work addressed to a pool cannot be served now or later.

**That state is reported, never silent.** The requester is told its work will not
be served, in the same channel a result would have arrived on, so it can act —
by standing up a secretary of its own, or by telling the user no Luhmann is
seated. A caller must never have to distinguish "still working" from "nobody is
coming" by waiting.

## Configuration

All keys live under `[luhmann].class.<NAME>` and are read live
([config.md](config.md)). `pool_max` (default 3) bounds the roster;
`cooldown_seconds` (default 600) is the warmth window before a secretary may be
stopped. A class may add keys of its own, read by whoever owns that class's
behavior.
