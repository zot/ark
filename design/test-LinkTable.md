# Test Design: LinkTable
**Source:** crc-LinkTable.md

All of these run against a temporary directory tree and a zero-value table
with a fake Config. No DB, no server, no fixtures — the logic under test is
the track/propose predicate, component-wise resolution, and reverse
expansion, none of which need indexed content.

## Test: a directory link whose target is in a source is tracked

**Purpose:** the track arm of the single predicate (R3381).
**Input:** sources `s1`, `s2`; `s2/link` → `s1`.
**Expected:** `s2/link` is tracked; `s2/link` resolves to `s1`.
**Refs:** crc-LinkTable.md, seq-link-resolve.md#1.2

## Test: a directory link whose target is outside every source is proposed

**Purpose:** the propose arm — same predicate, other branch (R3355, R3381).
**Input:** source `s2` only; `s2/link` → `outside/`.
**Expected:** not tracked; a U-record candidate for `outside/` naming
`s2/link` as the proposing link, reason `symlink`.
**Refs:** crc-LinkTable.md, crc-Store.md

## Test: a link tracks with nothing indexable under its target

**Purpose:** the predicate does not consult the index (R3381). The
rejected alternative — track only when the target has indexed files —
would make this untracked.
**Input:** sources `s1`, `s2`; `s1` empty; `s2/link` → `s1`.
**Expected:** tracked. Expansion under it yields nothing, which is the
truthful answer, not an error.
**Refs:** crc-LinkTable.md

## Test: adding link and source in either order reaches the same state

**Purpose:** the ordering independence the single predicate buys (R3381).
**Input:** (a) create link to a non-source, then declare that directory a
source; (b) declare the source first, then create the link.
**Expected:** identical final state — tracked in both, and in (a) the U
candidate is gone. No extra trigger beyond re-deriving at config load.
**Refs:** crc-LinkTable.md, crc-Config.md

## Test: resolution is component-wise, with the link mid-path

**Purpose:** the originating defect (R3360). A leaf-only resolver passes
every other test here and fails this one.
**Input:** `s2/link` → `s1`; real file `s1/a.md`; resolve
`s2/link/a.md`.
**Expected:** `s1/a.md`. The leaf is a real file and resolves to itself;
the link is two components up.
**Refs:** crc-LinkTable.md, seq-link-resolve.md#1.2.1

## Test: tilde expansion alone does not resolve

**Purpose:** lexically absolute is not canonical (R3372) — the exact shape
of the `@ext` defect, where `absolutizeExtPath` stopped at `IsAbs`.
**Input:** a path whose expanded form still traverses a link.
**Expected:** resolution continues past expansion and reaches the
canonical path; asserting the expanded-but-unresolved form is *not*
returned.
**Refs:** crc-LinkTable.md, seq-link-resolve.md#1.2.2

## Test: a dangling link falls back to the literal path

**Purpose:** a resolution failure must not become an error (R3361). Ark has
already had broken symlinks crash rebuild and reconcile once.
**Input:** `s2/link` → a deleted directory; resolve `s2/link/a.md`.
**Expected:** the literal path is returned, and the caller's ordinary
not-indexed answer follows. No error, no panic.
**Refs:** crc-LinkTable.md, seq-link-resolve.md#1.2.3

## Test: only the wildcard-free prefix of a pattern resolves

**Purpose:** a pattern is not a path (R3362), and this is the mechanism
behind namespace-preserving answers.
**Input:** `s2/link/**/*.md` with `s2/link` → `s1`.
**Expected:** the concrete prefix resolves, the `**/*.md` tail is
re-attached unchanged, and matching happens against canonical paths.
**Refs:** crc-LinkTable.md, seq-link-resolve.md#1.1

## Test: reverse expansion iterates to a fixed point

**Purpose:** a link may point into a tree another link also targets
(R3374). Iteration is not defensive: ark contains
`.claude/skills/ark-usage/ark` → `.claude/skills/ark`, a sibling inside a
tree other links target.
**Input:** `a/ → b/`, `b/x → c/`, file `c/f.md`.
**Expected:** the address set for `c/f.md` includes the address reached by
two substitutions, not only the one-step form.
**Refs:** crc-LinkTable.md, seq-link-resolve.md#2.2.2

## Test: a cycle terminates

**Purpose:** the guard (R3353, R3374). The layout permits a cycle and ark
already has a link pointing at the farm containing it.
**Input:** `a/link → b/`, `b/link → a/`.
**Expected:** resolution and expansion both terminate; no hang, no stack
exhaustion.
**Refs:** crc-LinkTable.md, seq-link-resolve.md#2.2.3

## Test: a scan records a directory symlink and does not descend

**Purpose:** R3352, R3373 — the originating defect, at the site that
caused it. `WalkDir` does not follow links, so `d.IsDir()` is false for a
directory symlink and it falls through to the *file* branch, is matched
against file patterns, and is silently dropped when its name fails them.
Its contents are then unreachable with no error and no log line.
**Input:** sources `s1`, `s2`; a file `s1/a.txt`; `s2/link` → `s1`. Scan.
**Expected:** `s2/link` in TrackedLinks; `s1/a.txt` in NewFiles exactly
once; neither `s2/link` nor `s2/link/a.txt` in NewFiles. One file keeps
one index entry while gaining a legal address, because the alias lives on
the link rather than on each file beneath it.
**Fire alarm:** delete the symlink branch and confirm this goes red. The
link then falls to the file branch exactly as it does today, which is the
state the whole feature exists to change.
**Refs:** crc-Scanner.md, crc-LinkTable.md

## Test: a symlinked source root is refused, at both doors

**Purpose:** R3354. **Reframed 2026-08-02 (Bill's call):** this case was
designed as "resolves rather than scanning to one entry", and a symlinked
source root is now *refused* instead — resolving it would make the user's own
configured path a name ark cannot answer, since a scan starts below the root
and never meets the link that would make it resolvable.

The failure being prevented is unchanged and is why the check exists at all:
`filepath.WalkDir` lstats its root, so a symlinked one yields the link node as
a single *file* entry and none of the contents. The scan reports one file
having indexed nothing, and nothing about that looks anomalous — it is not even
a zero to disbelieve.

**Input:** two doors, because there are two. `ValidateSourceDir` on a symlink,
a real directory, a glob pattern, and a missing path. Then `ResolveGlobs` over
a directory holding two real subdirectories and one symlink to one of them.
**Expected:** the symlink is refused with a message naming the reason; the real
directory, the glob pattern (which names no file to lstat) and the missing path
(the sources check's business, R198) all pass. The glob expansion adds the two
real directories and not the link.
**Fire alarm:** disable the lstat branch and confirm the first goes red; put
`os.Stat` back in the expansion — the literal pre-fix line, which follows the
link so it passes `IsDir()` — and confirm the second reports the link among the
added sources.
**Guard the guard:** the expansion test fails if it added nothing, since an
expansion that matched no directories would pass the assertion vacuously.
**Refs:** crc-Config.md, crc-Scanner.md, specs/symlinks.md, R3354
**Code:** config_test.go

## Test: the predicate is re-derived against the new config

**Purpose:** R3381 — a config change is the whole trigger for reclassifying a
link, since the predicate is a test over (target, source config) with no index
lookup. `SetConfig` before `Rebuild` is not optional: the table's config *is*
the source membership being tested, so rebuilding against the previous one
classifies links correctly for a configuration nobody is running, which
presents as the reload having done nothing.
**Input:** a link inside one source pointing at a directory that is a second
source, so it starts tracked. Then remove the target's source — the edit a user
makes in `ark.toml` — rebind, and rebuild.
**Expected:** the link is no longer tracked and comes back as a proposal naming
its own path.
**Fire alarm:** make `SetConfig` a no-op and confirm red on both assertions.
Injected in `links.go` rather than by deleting the call in the test, which only
produced an unused variable and a *build* failure — a build error is not
evidence that a test detects anything.
**Covers the demotion direction only.** Promotion (proposed → tracked) needs
the proposal list, which lives in U records that do not yet carry the proposing
link — gap O202, blocked on the U-record storage item.
**Refs:** crc-Config.md, crc-LinkTable.md, specs/symlinks.md, R3381
**Code:** links_test.go

## Test: the address space is listable, links included

**Purpose:** R3380 — a tracked link appears as if it were a real directory,
and `--nolinks` omits both it and the addresses beneath it.
**Input:** `s2/link` → `s1` with `s1/a.md`.
**Expected:** default listing contains `s2/link` and `s2/link/a.md` and
`s1/a.md`; `--nolinks` contains only `s1/a.md`.
**Refs:** crc-LinkTable.md

## Test: tier is re-derived when a link's shape changes

**Purpose:** R3385, and the only failure in the scheme that is silent in
the *wrong* direction. A recompute that refreshes fileid but keeps the tier
leaves a link verified on a schedule for a shape it no longer has.
**Input:** a single-hop tracked link; replace its target with a symlink so
the chain becomes two hops; recompute.
**Expected:** the entry's tier is now multi-hop. Asserting the tier
directly, because every observable behavior still looks correct at the
moment of the change — the divergence only appears later, when the
unverified hop moves.
**Refs:** crc-LinkTable.md, crc-ExtMap.md

## Test: a repointed link is followed through the per-path refresh

**Purpose:** R3390, and the load-bearing half of it. A link change never
arrives as a scan, so `SyncPath` is the *only* thing that re-derives a
tracked link's resolution — and the single-hop tier (R3384) is defined on
exactly that event.
**Input:** `s2/link` → `s1a`, observed and tracked. Repoint it to `s1b`
(unlink + symlink, which is what `ln -sfn` does). `SyncPath(s2/link)`.
**Expected:** the entry resolves to `s1b`, and `Resolve(s2/link/a.md)`
answers under `s1b`. Asserting the *new* target rather than merely that
something changed, because the old answer is a real path that still exists
and still reads as correct.
**Fire alarm:** return early when `Lookup` already knows the path — the
"we've seen this link" shortcut, which is the plausible wrong
implementation. Confirm this goes red; every other test here still passes.
**Refs:** crc-LinkTable.md, crc-DB.md, seq-file-change.md#1.3.5

## Test: a deleted link is forgotten

**Purpose:** R3369 through the same path — a link that disappears must return
whatever depended on it to unresolved rather than leave it pointing at a file
it can no longer reach.
**Input:** a tracked `s2/link` → `s1`; remove the link node;
`SyncPath(s2/link)`.
**Expected:** the table no longer tracks it, and `Resolve(s2/link/a.md)`
falls back to the literal address. Not an error — a vanished alias is
ark's ordinary not-indexed answer (R3361).
**Fire alarm:** drop the `Remove` from the gone branch, returning the status
alone. Confirm red.
**Refs:** crc-LinkTable.md

## Test: a link replaced by a regular file stops being an alias prefix

**Purpose:** R3390's dispatch order. `lstat` before following is the whole
correction: a routine that stats first sees a directory for a repoint and a
file here, and in both cases has already lost the question it was asked.
**Input:** a tracked `s2/link` → `s1`; replace the link with a real file at
the same path; `SyncPath(s2/link)`.
**Expected:** untracked, and the path is handed back as not the table's
business so the caller indexes it as the file it now is.
**Fire alarm:** swap `os.Lstat` for `os.Stat` in `SyncPath`. The repoint test
above goes red too, which is the point — one wrong call breaks both
directions.
**Refs:** crc-LinkTable.md

## Test: a proposed link's target is recorded as an unresolved candidate

**Purpose:** R3355, R3357 through the refresh rather than the scan. The scan
and the refresh must dispose of the same link identically; two surfaces that
disagree about what a link is are worse than one that is consistently wrong.
**Input:** a DB whose only source is `s2`; create `s2/link` → `outside/`;
`DB.SyncLinkPath(s2/link)`.
**Expected:** an unresolved record for `outside/`, and the link untracked.
**Fire alarm:** delete the proposal arm of `SyncLinkPath`. Confirm red.
**Refs:** crc-DB.md, crc-Store.md

## Test: creating a symlink does not change the canonical file set

**Purpose:** R3375's reason for existing. `--nolinks` is what a script
uses to get a stable set.
**Input:** list with `--nolinks`; create a new tracked link; list again.
**Expected:** identical output both times, while the default listing grows.
**Refs:** crc-LinkTable.md

## Test: resolution is component-wise, and what falls back

**Purpose:** R3359, R3360, R3361, R3362 — the doors share one translation, so
this exercises the translation rather than each door. Asserting a single door
would be the wrong shape: what R3359 protects is that the *set* agrees.
**Input:** a tracked directory link with a subtree; a mid-path address; an
unrelated path; a pattern with a resolvable prefix; a pattern whose wildcard is
in the first component; then the same link left dangling.
**Expected:** the mid-path link resolves — the original defect was exactly this
shape, a file at `<farm>/<dir>/<file>` where `<farm>/<dir>` was the link, so a
leaf-only lookup would miss it. An unrelated path comes back unchanged. Only a
pattern's wildcard-free prefix resolves. A **dangling** link still resolves, to
a path that then does not exist, so the door replies with ark's ordinary
not-indexed: a resolver that refused here would turn a missing file into a
resolution error, which is what R3361 forbids.
**Guard the guard:** the dangling case asserts the resolved path really does
not exist, or it would prove nothing about dangling links.
**Corrected while writing:** the first version asserted that a dangling link
stops being tracked and that resolution falls back to the literal path. Both
were assumptions rather than the spec's words — `followLinkChain` reads the
chain with `readlink`, which does not stat the target. Falling back to the
literal is the **cycle** case, below.
**Fire alarm:** replace the prefix walk with a leaf-only lookup — the pre-fix
shape — and confirm the mid-path case goes red.
**Refs:** crc-LinkTable.md, crc-DB.md, crc-Searcher.md, specs/symlinks.md,
R3359, R3360, R3361, R3362
**Code:** links_test.go

## Test: a cycle falls back to the literal path

**Purpose:** R3361, R3374 — the case that genuinely cannot be resolved. The
layout permits it rather than merely allowing it in theory: ark already
contains a link pointing at the farm that contains it.
**Input:** two links pointing at each other, both tracked.
**Expected:** the literal path, not a loop and not an error.
**Refs:** crc-LinkTable.md, R3361, R3374
**Code:** links_test.go

## Test: readers never see a half-built table

**Purpose:** R3359 — every mutator is on the DB actor and every door is an
off-actor read, so the contents live in an immutable snapshot behind an atomic
pointer.
**Input:** a goroutine running `Observe`/`Remove`/`Rebuild` flat out while the
main goroutine runs every reader — `Resolve`, `ResolvePattern`, `Expand`,
`Links`, `Len`, `Lookup`. Judged by `-race`, which is the only thing that can
see this.
**Expected:** no race, and no torn read.
**Fire alarm:** have `mutate` write the published snapshot in place instead of
cloning and swapping, and confirm `-race` reports an actual `DATA RACE`. That
is the point of the whole design, and it is not visible any other way — the
test passes without the detector.
**Refs:** crc-LinkTable.md, specs/db-concurrency.md, R3359
**Code:** links_test.go

## Test: an alias-spelled `-files` filter finds what the canonical spelling finds

**Purpose:** R3400 — a file filter is resolved where it becomes a *closure*,
not where it enters a struct. This is the case that was silently broken on
both doors while every unit test stayed green (gap O204/O206), because the
existing tests exercised `ResolvePattern` directly and the door under test
was never on the path the CLI or the server actually takes.
**Input:** a tracked directory link, one indexed file beneath its subtree and
two fillers outside it, a `files` row spelled through the link, built into
search options by `BuildChunkFilters` and carried as **extraOpts** — the
CLI's own shape.
**Expected:** the canonical spelling, the alias glob, and the alias exact
path each return exactly the one file.
**Why extraOpts and not `opts.ChunkFilters`:** routing the row through
`ChunkFilters` would let `Searcher.normalizeOpts` resolve it, and the test
would pass with the fix removed — testing the door that already worked.
**Why three files and not one:** the trigram filter (R572, `FilterByRatio`
0.50) discards any trigram carried by more than half the chunks, so a
one-chunk corpus finds nothing for reasons unrelated to the filter under
test. Learned by measuring: bare and `-contains` searches return nothing in a
one-file rig while `-regex` finds the file.
**Guard the guard:** the canonical spelling is asserted *first* and with
`t.Fatalf`. Without it every assertion below is vacuous — an earlier
measurement of this same defect proved nothing because both spellings
returned nothing and the corpus simply was not matching.
**Fire alarm:** delete the resolution from `BuildChunkFilters` and confirm
the alias glob and the alias exact path both go red while the control stays
green. **Pulled 2026-08-03: rang** — both alias assertions reported "found 0
results", the control passed, and the restore was byte-clean.
**Refs:** crc-Searcher.md, seq-filter-stack.md, specs/symlinks.md, R3359,
R3362, R3400
**Code:** links_test.go
