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

## Test: a symlinked source root resolves rather than scanning to one entry

**Purpose:** R3354. The failure this prevents does not present as zero —
`filepath.WalkDir` on a symlinked root yields the link node as a single
*file* entry and none of the contents, so the scan looks like it worked.
**Input:** a source whose `dir` is a symlink to a directory of files.
**Expected:** the source's files are found. Asserting a count greater than
one, since one is the broken outcome.
**Refs:** crc-Config.md, crc-Scanner.md

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

## Test: creating a symlink does not change the canonical file set

**Purpose:** R3375's reason for existing. `--nolinks` is what a script
uses to get a stable set.
**Input:** list with `--nolinks`; create a new tracked link; list again.
**Expected:** identical output both times, while the default listing grows.
**Refs:** crc-LinkTable.md
