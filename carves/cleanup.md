# Cleanup: retired features that are still wired in

Ark keeps things after it has decided to stop needing them. A feature is
superseded, the spec gets a banner saying so, and the code, the agent, the skill,
and the global links that advertise it all stay in place. Each leftover costs
something small: a skill description loaded into every Claude session, an
endpoint nobody calls, a spec a reader has to be warned off. Together they make
the system look larger and less settled than it is.

This carve collects those leftovers so they can be retired deliberately, rather
than rediscovered one at a time by someone who takes them for live features. It
is expected to grow: an item lands here when a supersession is decided but the
removal is not done.

## Status

- [ ] **Item 1 — the Gen-2 spectral sidecar.** **OPEN (#26.)** Superseded by
      the bloodhound; the machinery is still wired in, and the bloodhound does
      not yet cover the tag-search panel's expansion role.
- [ ] **Item 2 — the `ark-connections` sidecar.** **OPEN (not queued.)** Replaced by the
      luhmann secretary pool; no queue item covers it yet.
- [ ] **Item 3 — trajectory validation debt.** **OPEN (not queued.)**
      `minispec validate trajectory` fails on problems that predate
      2026-09-24.

Not a defect, recorded so nobody repairs it: ark's vendored `mini-spec` and
`minimap` skills are deliberate copies. See [below](#vendored).

<a id="1"></a>
## Item 1

`specs/spectral-search.md` opens with a SUPERSEDED banner: the feature is slated
for removal in favor of the bloodhound ([bloodhound.md](../specs/bloodhound.md)).
The original `claude --print` co-process was retired 2026-07-03. What remains is
the Gen-2 sidecar, and its spectral-toggle UI (R1255–R1265) was never built, so
no consumer ever called it. The pieces still in place (2026-09-24):

- The Librarian's expansion queue (`librarian.go`, `ExpandRequest`), created in
  `server.go` (R1248, R1379).
- The `/search/expand` and `/search/curate` endpoints (R1383).
- The `ark-expansion` agent, which drains the queue.
- The `search-expansion` skill in `.claude/skills/`, unchanged since it was
  created (`c9c521f`, 2026-04-08). It is not in the Makefile's bundled skill
  list, so installed users never got it. The dev box's global links to it were
  removed 2026-09-24; the repo copy goes with this item.
- The CLI command family that fronts the queue, in `cmd/ark/main.go`.
- `specs/spectral-search.md` itself, which retires with the sidecar.

This is O140 in `design/design.md`, which also lists the requirements that
retire to the bloodhound. **It is not a pure deletion.** O140 was DEFERRED by
Bill on 2026-07-03 as its own slice because the bloodhound does not yet replace
the tag-search panel's expansion role in code. That gap closes first, or the
removal takes a working (if unused) path away with nothing in its place.

<a id="2"></a>
## Item 2

The `ark-connections` sidecar (the Find Connections agent for the curation view)
is obsolete, replaced by the luhmann secretary pool. #26 as written does not
cover it. On pickup, decide whether it widens #26 into a single dead-sidecars
item or gets a sibling. Like `ark-expansion`, its agent is not in the Makefile's
bundled agent list.

<a id="vendored"></a>
## Vendored mini-spec

**DECIDED (Bill, 2026-09-24): ark keeps project-local copies of the `mini-spec`
and `minimap` skills.** The canonical copies live in the mini-spec repository,
and the global `~/.claude/skills/` links point there. The project copies are
kept on purpose, because mini-spec is critical to how a project is built: a
repository developed with mini-spec carries a record of the version that built
it. In ark sessions the project copy takes precedence over the global one, which
is the point.

So a difference between the two is expected, and not drift to repair. Refreshing
the vendored copy is a deliberate act, done when the project should adopt the
newer mini-spec. As of 2026-09-24 ark's copy is one commit behind: mini-spec
`b89dd42` (2026-09-16) moved trajectory entries flush left, and ark's
`trajectory-format.md` predates it.

<a id="3"></a>
## Item 3

`minispec validate trajectory` fails, measured 2026-09-24, on four kinds of
leftover, none of them from recent work:

- **The two queue readers disagree.** Four pending and 24 done item IDs are
  seen by the line scan but not by the document reader, and one the other way.
  The tool names an unclosed backtick span or fence above them as the usual
  cause, so the first step is finding that opener.
- **Status-block bullets without a checkbox**, read as unkeyed parts, in
  `actor-delivery.md` (two gap bullets) and `symlinks.md` (one accepted
  residue). Like this carve's own vendored-skills note, they want to be prose
  outside the part list.
- **Two orphans.** `chat-browser.md` part 1 and `forge-review-console.md` part 1
  are marked landed against queue items with no done entry.
- **Old done headers** with no identifier slot, which the tool asks to migrate
  to the `- **YYYY-MM-DD — <ids>: <title>.**` shape.

