# Symlinks: one file, many names

Ark's implicit model is that a file *is* its absolute path. The filesystem
disagrees, and has for as long as there have been symbolic links. This carve
covers teaching ark that a file can have more than one name, and doing it
without making the index believe there is more than one file.

## Status

What is still open, at a glance; the detail for each part is further down, keyed
by bare number. **The checkbox is authoritative** where the three markings on a
line disagree. Accepted residue carries no checkbox, the way an approved gap
does not.

- [x] ~~**Item 1 — record and resolve.**~~ **LANDED (`4c6e974`, 2026-08-04.)**
- [x] ~~**Item 2 — apply at every door.**~~ **LANDED (`4c6e974`, 2026-08-04.)**
- [ ] **Item 3 — say what happened.** **NOT VERIFIED.** The `U`-record half is
      in; whether the error text distinguishes absent from unindexed from
      aliased, and whether links log at `-v`, has never been checked.
- [ ] **Item 4 — fail fast when onboarding does not take.** **NOT VERIFIED**, and
      the most misreadable line here: the broken bootstrap *path* was repaired,
      which is the symptom — a failed bootstrap *stopping* the agent is a
      different requirement and is not built.
- [x] ~~**Item 5 — the verification tiers** (R3382, R3384–R3388).~~
      **LANDED (`4c6e974`, 2026-08-04.)** Postdates the split below, which is
      why it appears in no item description there.
- [ ] **Item 6 — the CLI `--links` / `--nolinks` selector.** **OPEN.** R3375,
      R3376, R3379 carry **zero** code references, so it is genuinely unbuilt;
      two designed cases in `test-LinkTable.md` ride on it.
- [ ] **Item 7 — search results and `/content` can disagree about a moved
      routing.** **OPEN (#120.)** Behavioural, not merely unproven.
- [ ] **Item 8 — a test harness that builds a whole `*DB`.** **OPEN (#121.)**
      Three gaps now want it — O199, O203, O210 — where two did before.
- **O196 — the watch-loop filter's two escaping transitions.** Accepted residue,
  deliberately not built: one repair covers both, judged more machinery than two
  exotic transitions justify.

## How it surfaced

The Hermes bootstrap had been failing for an unknown length of time. Both the
`ark-messenger` and `ark-searcher` agents open with a fetch of their reference
skill, and that fetch returned `404: not indexed`. The file is indexed. It is
indexed at `/home/deck/work/ark/.claude/skills/ark/hermes-messaging.md`, and
the agents asked for it through a symlink farm under the ark home directory.
Same bytes, two names, and ark knew one of them.

The failure was invisible for two compounding reasons, both worth keeping.

**The onboarding was unenforced.** The `PreToolUse` guard denies a subagent's
first tool call and uses the denial to deliver bootstrap instructions
(*fumble onboarding*: subagent lifecycle hooks are unreliable, so the error is
the channel). But the guard never checks that the fetch *succeeded*. It
allowlists commands by prefix, so an agent that failed to load its reference
still passes every subsequent gate. The agents had been operating without
their messaging reference and mostly working anyway, because the allowlist was
doing the real constraining.

**The guard scripted the wrong diagnosis.** Having failed the fetch, the agent
tried to start the server. A different branch of the guard blocked that, with:
"If a command reports the server is down, stop and report that cleanly." The
agent then reported, confidently and coherently, that the server was down and
it was facing a bootstrap deadlock. The server had been running for forty
minutes. The half about the deadlock was correct; the attribution came from
our own deny message. A hook that tells an agent what conclusion to draw will
get that conclusion, including when it is false.

## What was established

Facts, measured 2026-08-01, as distinct from the proposals below.

- `scanner.go:48` walks with `filepath.WalkDir`, which does not follow
  symlinks.
- `d.IsDir()` is **false** for a directory symlink, so it falls through to the
  *file* classification branch and is matched against file patterns. A
  directory symlink named `ark` fails `*.md` and disappears with no error and
  no log line. Its contents are never reached. This is the exact shape of the
  Hermes failure.
- Walking a **symlinked source root** returns the symlink node itself as a
  single file entry: `files=1`, `err=<nil>`, contents unindexed. It does not
  even produce a zero to disbelieve.
- Nothing currently indexed is a symlink, so there is no existing duplication
  to unwind. Clean starting position.
- `db.go:3776` (`DB.Fetch`) calls `filepath.Abs`, which does not resolve
  links, then reports `not indexed: <path>`.
- `config.go:791` expands `~` in `Sources[i].Dir` and does nothing else. It is
  the natural home for source-path resolution.
- `config.go:925`/`943` match a path against `src.Dir` **by prefix**.
- The correct resolver already exists, written for an unrelated reason and
  never generalized: `cmd/ark/checkpoint.go:139`, carrying the comment "app
  dirs are routinely reached via symlinks." It includes the dangling-link
  fallback the general version needs.
- `IsGlob` (used at `config.go:940` to skip pattern sources) already
  distinguishes a concrete path from a pattern.

## Why people make symlinks

This decides the design, so it comes before the design. The uses split into
two groups that hold **opposite** views about which name is the real one.

**Group A: point at the most up-to-date version.** A reference to a file in a
development project, so the runtime always gets what the repo currently holds.
Also dotfile managers, one artifact shared across projects, and convenience
aliases for deep paths. Ark's own layout is this, several times over
(`~/.ark/skills/*`). A user would say the file *is* in the repo and merely
*appears* at the link. Canonicalizing to the target is plainly right.

**Group B: filesystem concerns.** Some files must live on an encrypted
partition while the rest do not. A volume runs low and a new one is grafted on
to extend space. Version pinning, where `libllama.so` points at
`libllama.so.0.0.9592` and the target rotates on every upgrade. Active-slot
patterns, where `current` points at a dated release directory. Here the link
is where the file logically lives and the target is a storage decision.
Canonicalizing to the target keeps the volatile name and discards the stable
one.

**Both groups are confirmed against actual usage** (Bill, 2026-08-01) rather
than imagined, which matters because they pull in opposite directions and a
design that serves only the imagined one would look fine on paper.

**Group C, the fifth child** (Bill's name for it, 2026-08-01): the case the
enumeration did not plan for and must handle anyway. Dangling links left by a
move or an uninstall, currently invisible. And cycles: this repository has a
live one, a link pointing at the very farm that contains it. Nobody makes
these on purpose, which is exactly why a design derived from intent will not
cover them.

The tension between A and B is the crux, and it is what makes *recording* the
link better than merely resolving it. If both names are known, a target that
moves is a repair rather than a corruption, and ark can answer in whichever
namespace the caller used. It may also mean ark never has to classify which
group a link belongs to, which would be a good outcome: the taxonomy motivates
the design and then stops mattering.

## Decisions

**DECIDED (Bill, 2026-08-01): symlinks work for every path ark accepts.** Not
`fetch` alone. CLI path arguments, search file filters, `@ext` tag targets,
`/content/` URLs, `remove`, and the `dir=` entries in `ark.toml`. A fix that
covers most paths relocates the mystery rather than ending it.

**DECIDED (Bill, 2026-08-01): the scan records symlinks and their targets, and
ark resolves them internally.** Two alternatives were considered and rejected.
*Follow links while walking* duplicates file records and, worse, makes the
same content reachable under paths governed by different include/exclude
rules. *Resolve only at the door*, which this document originally proposed,
works but discards the alias: it is correct for Group A and wrong for Group B,
and it leaves ark unable to say anything about a link it did not follow.
Recording keeps the no-duplication property, survives a target moving, and
turns a dangling link from an invisible skip into a statable fact.

**DECIDED (Bill, 2026-08-01): hardlinks are treated as separate files.** They
are the sibling case, and the argument for ignoring them is strong in four
independent ways. Content dedup already makes the storage cost near zero (see
below). There is no link to record, because two hardlinks are equally real and
neither points at the other, so the alias design has nothing to attach to.
Hardlinked directories are effectively forbidden, so the prefix-resolution
problem does not arise. And detecting them requires per-file inode tracking on
every scan, paid always, against no evidence that duplication exists in
practice; inodes also fail to survive a restore from backup, a filesystem
migration, or a `cp -a`, so building identity on them would introduce a
rebuild hazard.

## A wrong turn, recorded because the correction matters

This document first argued against following symlinks on the grounds that the
symlink farm would produce "triplicate chunks and triplicate embeddings" for
25 files. That is false, and it understated how good ark's existing dedup is.

Chunks are content-addressed with refcount-bumped C records.
`WithIndexedChunkCallback` fires only for newly-inserted chunkids, and
`indexer.go:46` states the consequence directly: content-dedup'd chunks "share
the chunkid and its F/V/T records." V records are the vectors. So identical
content costs one chunk and one embedding however many files carry it, and
`AddFile`'s comment says it outright: "dedup'd chunks cost zero."

Duplication would have cost file records and file-to-chunk references, not
chunks and not vectors. The argument against following links on scan survives,
but it rests on rule ambiguity and scope, not on storage.

The useful thing this turned up: **ark has already solved the many-names
problem once, for a different reason.** `search.go:877` notes that "a
deduplicated chunk belongs to several files, so the path it is reported under
is what `-files` must match." Content dedup already forced a decision about
which of several paths a shared chunk reports under. Symlink aliases should
ride that mechanism rather than inventing a parallel one.

## Principles

**A link never admits its target. It proposes it.** **DECIDED (Bill,
2026-08-01.)** Adjunct content is indexed because the user adds it as a source
directly, never implicitly because a link pointed at it. A recorded link whose
target is not indexed becomes a **candidate the UI can flag**, and the user
decides.

This settled after the document argued both extremes and both were wrong.
First: a link must not drag outside content in, so the content is simply lost
— which fails the storage-placement case outright, where the bytes live
outside every source *by construction* (an encrypted partition, a disk grafted
on to extend space) and are plainly wanted. Then the reverse: the link's
presence inside a source is intent, so follow it — which silently expands
scope on the user's behalf and makes an enormous foreign tree something you
discover from a disk-usage graph. The resolution is neither, because the real
question was never "index it or not" but *who decides*. This is
discernment-gated authoring: the blind layer proposes into a surface, and the
sole author of durable state is the discerning approver. The same move that
revitalized the Tag Forge by externalizing candidates as visible lines.

**DECIDED (Bill, 2026-08-01): the candidate is a `U` record**, carrying a
`symlink` field in its JSON value. Not a new record class and not an `E:`
condition. `U` already means "seen during a scan, not covered by config,
persisted across scans, cleared when the user adds a covering rule, dismisses
it, or the file goes away," which is the mechanism this needs almost verbatim.
The deciding axis against `E:` was **dismissibility**: `E:` conditions are
explicitly never dismissed and re-derived on every config load, so a
deliberate link into a tree the user will never index would make ark's warning
output permanently noisy for a choice already made.

Reuse also sidesteps a trap: a new record class needs both a
`specs/record-formats.md` entry and an `arkLabels` entry in `status -db`, and
one without the other makes the diagnostic report a silent zero — the exact
failure mode this carve exists because of.

**Two things the spec pass must not skip.**

*The U semantic broadens.* Today's wording is "files **seen during a scan**
that don't match any include/exclude pattern." A symlink target outside every
source is never seen — the walker does not reach it. It is *proposed* rather
than encountered, which is a new way for a U record to come into being.
`specs/record-formats.md` must widen that sentence or the next reader will
believe the record class does something narrower than it does.

*The field should carry the proposing link, not a bare `true`.* The U record
is keyed on the **target** path, which is the thing not covered. A boolean
says "a link proposed this" but not which one, and the link is the context
that makes the decision obvious — "you linked this from `~/Documents/x.md`"
answers "why am I being asked about a volume I don't recognise." A present
string is as truthy as `true` to every consumer, so the explanation is free.
Proposed, not yet ratified.

*And U now has at least three reasons, which argues for naming the reason.*
The vast majority of today's ~10,200 unresolved files are **unindexible**:
nothing chunks them, so no pattern change will ever help. That is a third
category beside "not covered by config" and "proposed by a link", and they
call for different treatment — unindexible files want optional hiding,
config-uncovered files want a pattern edit, link-proposed files want a new
source. A flag per category proliferates; a `reason` field with the symlink
path alongside it does not. **DECIDED (Bill, 2026-08-01): the unindexible
category rides with this carve** rather than becoming its own item, since it
lands in the same code as the symlink U-record work, or close enough to it
that splitting would just mean touching the same lines twice.

The determination has an obvious source: a file whose `StrategyForFile` yields
no strategy is unindexible. Whether that is exactly the right test is not
established.

**Hiding is a visual convenience, not an exclusion** (Bill, 2026-08-01), and
the distinction is load-bearing rather than pedantic. An unindexible file must
remain a U record, because "unindexible" is a statement about ark's current
chunker set and not about the file. Add a chunker for that type and every such
file should become a candidate again with no user action at all. Excluding
them instead would look identical on the day it was done and silently lose the
revival — a failure with no symptom, which is the shape this carve exists
because of.

What survives from the rejected principles is narrower: **the scan must not be
surprised.** Recorded links are visible, cycles terminate, and nothing enters
the index without the user having said so.

**Canonical name = the path the scan gave it. DECIDED (Bill, 2026-08-01,
superseding two earlier answers the same day.)** Rooted at the source's
configured directory, no mid-path rewriting below it. Because a scan stops at
a directory symlink instead of descending, exactly one source walk reaches
each file, so there is one canonical name and **no tie-break to make at all**.

The question took three answers before dissolving, and the sequence is the
useful part.

*First: "the name inside a configured source."* Resolves nothing — in both
real cases both names are already in sources (`/home/deck/.ark` and
`/home/deck/work/ark` are both declared), and storage placement ends the same
way the moment the adjunct volume becomes its own source.

*Then: "the lexically first."* Chosen on the grounds that the two habits want
opposite answers from identical structure, so no rule can pick *correctly* and
determinism is the only property left worth buying. True as far as it went,
and it broke on a case neither of us had in view: mid-path **system** links.
Where `/var` is a link, `/private/var/...` sorts first and becomes the name a
user reads for a file they only ever addressed as `/var/...`. A rule meant to
be harmlessly arbitrary turned out to let the operating system's plumbing
choose user-visible names.

*Finally, the dissolution.* Both earlier answers assumed resolution
**rewrites stored paths**. It does not. Resolution is a lookup-time
translation between addresses; canonical naming is a separate question the
scan already answers. Once those are separated the tie-break has no occasion
to fire, and the `/private/var` problem cannot arise, because a system link
points outside every source and is therefore never tracked.

The general lesson, which is why this is written out: **a question that keeps
producing unsatisfying answers is often two questions wearing one name.** Both
failed rules were sincere attempts to answer "which of these names is the real
one?" — a question that stops existing once addressing and naming are pulled
apart.

**"Which file owns this chunk?" is the same question in different clothes**,
and it is **already deterministic**: the owner is the first fileid in the
chunk record (Bill, 2026-08-01). Content dedup forces the choice —
`search.go:878` notes "the path it is reported under is what `-files` must
match" — so no change is needed there. Recorded because reading did not settle
it: the resolver is wired from `DB.Open` through microfts2 rather than from
the `Store` resolver, and `Store.FilesForChunks` takes and returns maps, so
the shape *looked* like it might be order-dependent.

The one residual, judged acceptable: first-fileid means earliest-indexed, so a
full `ark rebuild` could reorder it if scan order ever changed. That matters
only if something persists a reported path and compares it later. Nothing is
known to.

General practice this belongs to: **sort before emitting**, so output never
depends on map iteration order.

**Alias names appear only when the requested prefix passes through a link.**
The reporting rule for `ark files` and for search. A pattern rooted at a link
is a request in that link's namespace and reports those paths;
`<link>/**/*` behaves as expected. A pattern with no concrete prefix, or whose
prefix does not pass through a link, is a request about the corpus and reports
canonical names — so `**/*` does not list each aliased file twice, and neither
does `/home/deck/**/*`, which a naive "return every matching name" rule would
get wrong since both names live under it.
The load-bearing argument is stability rather than elegance: **adding a
symlink must not change how many files you have.** Under "every matching
name," a single `ln -s` silently inflates every global count and every
unfiltered search, and scripts built on `ark files '**/*'` break for a reason
nobody would think to look for.

**Never half-resolve.** `config.go:925`/`943` prefix-match an incoming path
against `src.Dir`. Resolve one side and not the other and a file that
genuinely is in a source begins reporting "not in source" — a *new* wrong
answer traded for the old one. Both sides resolve, or neither.

**Directory links mean prefix resolution.** The failing path was
`<farm>/ark/hermes-messaging.md`, where `<farm>/ark` is the link. Resolution
walks path *components* against recorded links; it cannot treat the path as an
opaque string.

**Record what you skipped.** Every silent skip in this system has cost time.
A directory symlink that is not descended into should leave a trace.

## Open forks

- **Tie-break when both names lie inside sources.** The canonical-name rule
  above is silent on it. First source in config order, shortest path, and
  source declaration order are all defensible and none is obviously right.
~~**Chains: record each hop, or the resolved endpoint?**~~ **DECIDED (Bill,
2026-08-01): record each hop.** The fork was framed as faithfulness versus
lookup cost, and that framing was wrong — the real reason is that an endpoint
alone is **unhealable**. For `a → b → c`, repointing the middle link changes
what `a` resolves to while leaving *both ends untouched*: the name the user
wrote is unchanged and the target file is unchanged, so nothing watching
either end has cause to look again, and the stale answer stands indefinitely.
The information needed to notice is precisely what endpoint-only storage
discards. Recorded hops make the link set a dependency graph, so a changed
link invalidates every resolution running through it. R3369, with R3367
covering the `@ext` anchor case.
- **Whose include/exclude rules apply** when a file becomes reachable from two
  sources with different patterns? Unanswerable today because it cannot
  happen. It can as soon as links are recorded.
- **When does a recorded link go stale?** A target can be repointed without
  either file changing. `lstat` mtime tracks the link itself, so a repoint is
  detectable, but only if something looks. Scan-time only, or watched?
- **Does the error message split belong here or on its own?** The current
  `not indexed: <path>` conflates three situations — file absent, file present
  but in no source, and file indexed under a different name. Haiku could not
  distinguish them and neither could a human without running `ark files`. That
  ambiguity is most of why this survived so long.

## The address-space design

**DECIDED (Bill, 2026-08-01.)** The shape the whole feature settled into, after
several of this document's earlier answers were superseded in one sitting.

A scan indexes a directory symlink **as an entity and stops** — it does not
descend — so one file keeps one index entry while gaining a legal *address
prefix*. The alias lives on the link, not on each file beneath it, so the cost
is one record per link (74 on this corpus) rather than one per aliased file.
Patterns match **addresses**, which is why an exact path answers with one and
`/**/a` answers with two; `--links` expands a result to every address of its
file, `--nolinks` restricts to canonical. **Search reports canonical paths
only** — an alias is a way to reach a file, not something the file says, and
prefix-only duplicates are exactly what deduplication cannot collapse.

**One predicate decides everything:** a directory link whose target is inside a
source is tracked, one whose target is outside is proposed as a U candidate.
Over (target path, source config), no index lookup — deliberately *not* "has
indexed files beneath it", which would need a cross-source reverse index, would
flap on ordinary editing, and would break add-in-either-order.

R3373–R3381, R3383. Spec: [specs/symlinks.md](../specs/symlinks.md).
Design: `crc-LinkTable.md`, `seq-link-resolve.md`.

## Accuracy on `@ext`, and the machinery that got deleted

**DECIDED (Bill, 2026-08-01): slow is bounded, wrong is not.** Tags are about
to become load-bearing, so no flakiness gets built in on purpose.

Each linked-`@ext` association records the premise it was computed under —
(target, fileid) — so verification is a comparison rather than a re-derivation,
and an unchanged fileid does no X-record work at all. Three tiers, each taking
the cheapest mechanism still correct: **single-hop** invalidates precisely (the
event names the link's own path); **internal multi-hop** dirties on any file
change and recomputes on retrieval (precision would need a reverse index that
costs more than it saves); **external multi-hop** recomputes every retrieval
(no event can reach it). 71 / 5 / 0 on this corpus. Tier is **re-derived on
every recompute** — the one failure that is silent in the wrong direction.
Unresolvable is **unresolved, never last-known-good**. Retrieval is **sewn**:
update-and-retrieve runs on the actor owning ext state, tags travel forward as
a posted continuation, and any mutation posts to the write actor — sound only
because of R3344–R3350, which landed the same morning.

**A large amount of machinery was built and then deleted, and the reason is the
useful part.** Back pointers, watching out-of-source hops, and revalidation at
use were all defending a *cached forward resolution*. Forward resolution does
not need a cache: it is 20–35 µs and O(queries), since the query resolves once
and comparison is canonical-to-canonical. The table exists for the **reverse**
direction, which the filesystem cannot answer. Once that was seen, three
requirements were dropped before implementation. The general form: **when a
design keeps sprouting machinery to keep something honest, check whether the
something needs to exist.**

R3382, R3384–R3388. Design: `seq-ext-tag-retrieval.md`, `test-ExtAssumptions.md`.

## What the change of direction left behind

**Recorded because it cost a whole session to rediscover.** The design above
was revised *mid-sitting*: back pointers, out-of-source watches and
revalidation went out, the tiers came in. The requirement list was updated to
match. Three other layers were not, and none of them announces that it is
stale:

- **`specs/symlinks.md` still described the deleted machinery** and carried no
  prose at all for R3382 and R3384–R3388 — the section that should have
  *become* the tiers was simply left as the cache-and-recheck design.
- **Nine requirements had validator-green coverage and no design.**
  `crc-Scanner.md` claimed R3352/R3353/R3373 with no symlink behavior in its
  body; `crc-ExtMap.md` claimed R3382 and R3384–R3388 without the words
  *tier*, *premise* or *LinkTable* appearing in 275 lines, leaving
  `seq-ext-tag-retrieval.md` an orphan no card pointed at.
- **R3367 specified the opposite mechanism** to the sequence and test design
  written after it (below).

The mechanism is worth understanding rather than just the instance:
`minispec validate` computes requirements→design coverage from each card's
**top-line `**Requirements:**` field**, which `update add-ref` maintains. So
when a direction changes, the refs move in one cheap command and the bodies do
not — and coverage goes green *because* the design is missing. **Read the card
body; never take the coverage report as evidence that a card was updated.**
Repaired 2026-08-01 evening.

## Two calls that resolved the residue

~~**Is the `extByAnchor` key resolved, or does the lookup expand?**~~
**DECIDED (Bill, 2026-08-01): the key stays unresolved; the lookup expands.**
R3367 said the key is symlink-resolved and re-keyed when the chain beneath it
changes; `seq-link-resolve.md` diagram 2 and `test-ExtAssumptions.md` said the
key stays the absolutized-but-unresolved text and the *lookup* expands the
indexed file's canonical path to every address naming it. Both were committed
together, on either side of the revision.

The argument that settles it is one sentence: **reverse expansion is already
what makes `ark files '**/action.go'` list the real files and all their
aliases.** So the anchor lookup consumes machinery the address space must have
regardless, instead of growing a second mechanism — and a resolved key would be
a stored resolution, the exact thing the deleted back pointers existed to keep
honest.

The objection raised against it was **wrong, and the correction matters more
than the objection**: an untracked ancestor (`/home` being a symlink) does not
create a blind spot, because R3363 makes canonical *the name the scan gave it*,
so a source configured as `/home/…` indexes under that spelling and ark never
manufactures a name the user's own path would fail against. The real residue is
narrow — a target spelled through a link outside every source (never scanned,
never tracked) whose file does not exist yet at declaration time. A miss rather
than a misroute, healing at the next reindex of the declaring file. R3367
reworded; `specs/at-ext-storage.md` reconciled.

~~**How does reverse expansion terminate under a cycle?**~~ **DECIDED (Bill,
2026-08-01): a depth bound of 8, stated as a requirement (R3389).** A visited
set cannot do it, and the reason is the whole point: under a cycle the address
set is genuinely *infinite* rather than repeating, because `a/link → b` with
`b/link → a` makes every further `link` component another real, legal address
and a string never seen before. Forward rewriting has no such problem — each
substitution consumes a prefix, so the walk shrinks.

Written as a requirement rather than an approved gap **because an unstated
bound reads as defensive and invites deletion**, and deleting it does not make
the termination test fail — it makes it *hang*. Found by writing that test,
which did not terminate against the first implementation.

## The split

Agreed and largely landed; **Items 5–8 postdate it** and appear only in the
`## Status` block and below. Everything here is keyed by **bare number** — the
titles and the current status live in that block and *only* there, so no fact
appears twice and the two places cannot come to disagree.

Two views follow, and they answer different questions. **Where each part
stands** is immediately below. **What each part was scoped to cover** is further
down, and is the original split as written.

### Where each part stands

- ~~**Item 1**~~ (`7892e96`, `2e66ad2`, `4c6e974`). LinkTable, both scanner
  halves, the `L` record class with its `specs/record-formats.md` entry and
  `arkLabels` label — and the R3078 sentry caught that drift itself, which is
  what it is for.
- ~~**Item 2**~~ (`4c6e974`). `db.resolvePath` is the one translation
  `Fetch`/`IsIndexed`/`ReadSourceFile` call; `BuildChunkFilters` takes the table
  as a mandatory parameter, so a call site without one will not compile. The
  `@ext` half — the confirmed live instance recorded below — is closed by R3367,
  in both directions.
- **Item 3.** The `U`-record work (the `symlink` field, the widened semantic) is
  in. The rest was never checked in the pass that landed the tiers, so do not
  read this as either done or undone.
- **Item 4.** The broken bootstrap **path** was repaired (`hermes-guard.sh`,
  2026-08-03), which is the *symptom* this item was written about — but the
  requirement is that a failed bootstrap **stops** the agent, and that is a
  different thing from the path being correct. Unclear from inspection; needs a
  real check.
- ~~**Item 5**~~ (`4c6e974`). Premise records, the three schedules, re-tiering,
  unresolved-never-last-known-good, and the sewn verification on `/content`.
  Both halves verified against a real server rather than only in tests. The
  retrieval hop does not deadlock: `/content` answers 200 with rendered HTML,
  through an alias path as well as a canonical one, which is R3367 working live
  rather than only under test. The scan half persists a link **no watcher ever
  saw** — created while ark was down, recorded by the next startup scan alone.
  (Two private rigs did the measuring; what they showed is stated here, so
  nothing is lost by not having them.)
- **Item 6.** Two designed cases in `test-LinkTable.md` are waiting on it: "the
  address space is listable, links included" and "creating a symlink does not
  change the canonical file set". Update `specs/cli-commands.md` when it lands.
- **Item 7.** `search.go`'s ext-tag loop spans many files, so R3387's
  "verify once for this fileid at the start of the bulk operation" does not map
  onto it directly — it wants a grouping pass, or a verify keyed on the result
  set's distinct fileids.
- **Item 8.** It is also what would have caught the `ScanAsync` divergence
  below, which is the strongest argument for building it.
- **O196.** The watch-loop filter is filesystem-only because the table is
  actor-owned, so a link replaced in place by a non-indexable regular file, and
  a path cached as ignored before it became a link, both escape it.

**A lesson this carve produced, worth carrying past it.** O208 and O205 were the
same defect met from two ends a day apart: `ScanResults.TrackedLinks` was
consumed by `db.Scan()` and dropped by `db.ScanAsync()`, and `ScanAsync` is the
path the *server* takes on every startup and reconcile. One behaviour, two
paths, a test pinned to one of them — the third instance in this repo, after the
`withFTS`/extmap outage and R3388's own prose predicting the shape. The guard
that closed it asserts the two paths **agree** rather than testing either alone.

### What each part was scoped to cover

The split as originally written. Items 5–8 are absent by construction: they
postdate it, and their scope is stated in the `## Status` block instead.

**Item 1** (`/mini-spec`). The link record class, scan
recording of both file and directory symlinks, and component-wise prefix
resolution. Promotes `checkpoint.go`'s `resolvePath` to a shared helper.
Requires `specs/record-formats.md` and the `arkLabels` allowlist in
`status -db`; a record class added without its label makes the diagnostic
report a silent zero, which is precisely the failure mode this carve is about.

**Item 2** (`/mini-spec`). `DB.Fetch`, `IsIndexed`,
`ReadSourceFile`, `config.go:791` for `dir=`, `@ext` target resolution,
`/content/` URLs, and the search file filters. Needs the glob wrinkle settled:
a pattern is not a path, so the longest concrete prefix resolves and the
pattern tail re-attaches. `IsGlob` is the existing discriminator.

**A confirmed live instance, found 2026-08-01 while checking something else.**
`@ext` re-resolution works: indexing a file reaches
`ExtMap.candidatesForFileChange`, which looks up `extByAnchor[path]` with the
file's absolute path, and the anchor is keyed on the absolutized target
(`ext.go:21`, `:124`), so a dangling target finds its file the moment that
file appears. **Except through a symlink.** `absolutizeExtPath`
(`ext.go:236`) does tilde expansion, `IsAbs`, and `filepath.Join`, and no
symlink resolution — so a target written through a link keys the anchor on
the link spelling while `db.fileIDPath` returns the real path. They never
match, and the route never fires however many times the file is reindexed.
Silent in the worst available way: an `@ext` route that never fires is
indistinguishable from one nobody wrote, which is the same shape as the
three-month `@ext` routing outage. This is why the scope is every door rather
than `fetch`.

**Item 3** (`/mini-spec`, foldable into 1 or 2).
Distinguish absent from unindexed from aliased in the error text, and log
skipped or recorded links at `-v`. Includes the `U`-record work: the `symlink`
field, and the widened semantic in `specs/record-formats.md`.

*The UI half is a separate queue item, not part of this carve.* Showing `E:`
conditions and giving U records resolvable by adding a source an affordance is
a Frictionless pass, and the two workflows are forbidden from mixing in one
stream. The `E:` half of it has no dependency on this carve at all.

**Item 4.** **DECIDED (Bill,
2026-08-01): in scope for this carve.** The guard delivers bootstrap
instructions and never verifies they were followed. It allowlists by command
prefix, so an agent whose reference fetch failed passes every subsequent gate
and works well enough to hide the failure. That is what let a broken path
persist: the symptom was not an error, it was months of intermittent
flakiness that read as the agents being unreliable.

The requirement is that a failed bootstrap **stops the agent** rather than
degrading it. An agent running without its reference should be a loud failure
at the first gate, not a quiet one distributed across every task it ever
performs. Note the ordering constraint with Items 1 and 2: fail-fast landing
first would have turned this bug from silent flakiness into an immediate hard
stop, which is the outcome we want, so it does not need to wait for the
symlink work.
