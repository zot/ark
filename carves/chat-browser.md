# Browsing conversations, not guessing at them

**Problem.** Ark indexes every Claude Code conversation on the machine and could
render any of them, but there was no way to find out *which* one you wanted. The
only entry point was a directory of UUIDs and mtimes, so choosing a conversation
meant guessing which timestamp it had been.

Opened 2026-07-25 as a working note. Promoted here 2026-07-28 when its first part
landed and it became the record two queue items point at.

| Part | Status |
|---|---|
| ~~#73 — the Go substrate: listing, pairing, the Lua surface~~ | **Landed** 2026-07-28 |
| #71 — the Frictionless chat panel | Open (`/ui-thorough`) |

**Where the decisions went.** Behavior is canonical in
[`specs/chat-transcript.md`](../specs/chat-transcript.md) as **R3284–R3292**,
with the CLI surface mirrored in `specs/cli-commands.md`, the config key in
`specs/config.md`, and the two Lua bindings in `specs/lua-api.md`. Read those for
what the system does; this document is the reasoning and the split.

---

## Origin

A workflow run by hand often enough to name:

```
ls -lt ~/.claude/projects/-home-deck-work-ark | head -5
ark chats <UID>.jsonl | glow | less
```

Two steps, and the first one answers with UUIDs. Ark already indexed all of it.

## The three pieces

**1. The label — the actual feature.** Each row shows the session's first user
turn. A list of UUIDs is a prettier `ls`; a list of opening sentences is a table
of contents. Everything else in the row (date, turn count, path) is supporting
detail.

**2. The pairing.** Nothing recorded that source `/home/deck/work/ark` and source
`~/.claude/projects/-home-deck-work-ark` describe the same project.

**3. Delivery.** The renderer had to be reachable from the UI without shelling
out.

## Decisions

- **DECIDED (Bill, 2026-07-28):** the pairing is **declared**, not computed —
  `[[source]].logs_for` names the source whose work this one records. Claude Code
  derives its project-directory name by mangling the cwd
  (`/home/deck/work/ark` → `-home-deck-work-ark`), and reproducing that inside
  the query path would work today while hardcoding one vendor's naming scheme
  where every caller inherits it, breaking silently if upstream changed it, and
  never serving a non-Claude log source. → R3288, R3289.
- **DECIDED (Bill, 2026-07-28):** the mangling heuristic is still worth having
  **once**, as a populator rather than a rule — `ark sources pair --auto`
  proposes and writes pairings, and lives nowhere near a lookup. Deleting it
  costs the convenience of `--auto` and nothing else. → R3290.
- **DECIDED (Bill, 2026-07-28):** a source's **name** is its `dir`'s last
  segment, with the full `dir` accepted and tried *first* so `dir` remains the
  identity. An ambiguous short name is an error naming every candidate, never a
  silent pick — a silent pick pairs a chat source with the wrong project and
  nothing reports it. → R3292.
- **DECIDED (Bill, 2026-07-28):** labels are read on demand from the file head,
  not cached. A cache would buy a staleness class (an appended session whose
  label never updates) to save work that may already be negligible. Measure
  before adding a record class. → R3286.
- **DECIDED:** sessions come from the index, not a directory walk. Chat
  directories are already ark sources, so listing is a files-in-a-source query;
  an unindexed JSONL does not appear, consistent with every other list verb, and
  `ark chats PATH` still renders it. → R3285.

## What the work turned up

**The design assumed a concept the project did not have.** R3288 said `logs_for`
"names another source" and R3287 offered `--source NAME` / `--for NAME`, but
nothing anywhere defined a source's *name* — `specs/config.md` asserted the
opposite, that `dir` **identifies** the source, and the existing
`ark config --source` flag takes a directory. The gap survived the spec,
requirements, and design phases because every one of them could be written
without noticing. It surfaced the moment code had to compare two strings. R3292
exists to close it, and `specs/config.md`'s `dir` row now states the derivation
rather than contradicting it.

**A requirement about shared code is a claim about package structure.** R3291
says both clients render through the same code "so their output cannot drift" —
but it was written against a renderer that existed only in `cmd/ark/chats.go`
(`package main`) and printed to `stdout`, which a Lua binding in `package ark`
cannot reach. The requirement was not merely unimplemented; it was unsatisfiable
where the code sat. Honouring it meant *moving* the renderer to the root package
(`chats.go`) and parameterizing its writer. Generalizable: when a requirement
says two callers share an implementation, check which packages those callers live
in before calling the design done.

**Two duplications that only building exposed.** The vendor mangling already
existed, at `ptyhost.go` in the Luhmann launch path; adding a second copy in
`config.go` would have been precisely the drift R3289 was written to prevent, so
both now call one `claudeProjectSlug`. Its narrower `/`-and-`.` rule was kept
over the broader one first written for the populator: the two agree on all forty
of the real project directories, and widening it would have changed shipped
launch behaviour for no measured gain. Separately, `printWrapped` carried a
`contentWidth` local that was computed, clamped, and never read; it died with the
move.

**Running the verb found a defect that reading it had not.** `ark sources pair`
proposed for *every* unpaired source, so it reported each ordinary code source as
`? … no matching source` — a permanent complaint about every project directory,
in a verb designed to be re-run. R3290 says **chat** sources; a source the
heuristic does not match is not a failed proposal, it is simply not a log source.
Non-matches are now omitted, and the "names nothing" report moved to where it
actually bites: an **existing** `logs_for` that resolves to nothing, which is a
live defect rather than a pending suggestion.

**A test of a copy proves nothing about the original.** The natural way to test a
Lua binding is to build an `LState` and hand-write a function mirroring the
binding's marshalling — and that test passes forever while the real binding
drifts away from it. Ark has been bitten by exactly this shape before, when
hand-wired fixtures kept the suite green through a three-month outage in `@ext`
routing. So the option parsing was extracted into `chatSessionOptionsFromLua` and
`chatRenderOptionsFromLua`, and the tests drive *those*. What genuinely needs the
UI runtime — the lua-VM-to-binding hop — is gap **O177** rather than a fake.

## Open part

### #71 — the Frictionless chat panel (`/ui-thorough`)

Select a source, get a tab listing that project's conversations newest first,
each openable into the rendered transcript.

The substrate is ready and the panel must use it: call `mcp.chatSessions(opts)`
and `mcp.chatTranscript(path, opts)` (see
[`specs/lua-api.md`](../specs/lua-api.md)) rather than shelling out to
`ark chats`. Going through the bindings is what makes R3291's no-drift guarantee
real, and doing so discharges gap **O177**, which exists only because no caller
had yet exercised that hop.

Still open for the UI pass: does the transcript render in place, in a `tmp://`
document, or at a content URL? Do `--with-tools` / `--thinking` become toggles?

## Where this meets the TUI

The workflow behind all of this is terminal-resident — it ends in `glow | less` —
so a chat browser that exists only in the browser still leaves the alt-tab that
prompted [the TUI carve](ark-tui.md). The chat list is that project's obvious
first screen, and this substrate serves both clients, which is why the two carves
reference each other.
