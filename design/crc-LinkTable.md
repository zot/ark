# LinkTable
**Requirements:** R3359, R3360, R3361, R3362, R3363, R3364, R3372, R3373, R3374, R3375, R3376, R3378, R3379, R3380, R3381, R3383, R3365, R3369, R3353, R3389, R3390

Ark's map of directory symlinks that join one indexed region to another,
and the translation between an address a caller used and the canonical
path a file is indexed under. Wholly in memory (R3383): the index persists
only *that* a path is a directory symlink, and everything derived from its
target is rebuilt from the filesystem.

Owns the address space. It does **not** own `@ext` routings or their
assumption records — those are ExtMap's, and consult this component
(R3382, R3384).

## Knows
- links: map[linkPath]LinkEntry — the tracked set, keyed by the link's own path
- LinkEntry: target (immediate, as `readlink` gave it), resolved (final directory), hops []string (every link component on the path, R3369), tier (R3384/R3385)
- byTarget: map[canonicalPrefix][]linkPath — the reverse index. Cannot be derived from the filesystem, which answers "what does this point at" and never "what points here"; this is the only reason a table exists at all rather than plain on-demand resolution
- config: *Config — source membership, the single predicate behind track-vs-propose (R3381)

## Concurrency — published snapshots, not locks

Every mutator (`Observe`, `SyncPath`, `Remove`, `Rebuild`, `SetConfig`) runs on
the DB actor. Every **door** that resolves a caller's path — `Fetch`,
`IsIndexed`, `ReadSourceFile`, the `/content` URLs, the search file filters —
is an off-actor read. A bare map read from a door is the same latent race O154
was.

So the contents live in an immutable `linkSnapshot` behind an
`atomic.Pointer`. A mutator clones the current snapshot, changes the clone, and
publishes it with one store; a reader takes the current snapshot with one load
and works from it. The actor stays the only writer, so this is **publication
rather than locking** — no reader blocks a writer or another reader, and
"actors, not mutexes" holds. (Bill's call, 2026-08-02, over a `Copy()` bound
like `withFTS` and over resolving on the actor via `Sync`; the latter would
deadlock from any door already on the actor.)

**Publication belongs to the table, not to its callers.** A scheme where the
actor mutates and then remembers to publish is one a future edit forgets, and
the symptom would be a door quietly resolving against a stale table — the same
silent shape as everything else in this feature. Making the mutators clone-and-
swap internally means every existing call site is unchanged and mutating
without publishing is not expressible.

Two consequences worth stating. A reader that needs several lookups — `Resolve`
walking prefixes, `Expand` walking the reverse index — loads **once** and holds
one picture, so a mutation mid-walk cannot show it a half-changed table. And
`Observe` publishes **once** for what is logically one change: dropping the old
reverse-index entry and adding the new one must not be visible separately.

`cfg` is not in the snapshot. It is read by the predicate and by tier
classification, both actor-only, and written only by `SetConfig`, also
actor-only. No door touches it.

## Does
- **Track or propose** (R3381): a directory symlink whose target lies inside a source is tracked; one whose target lies outside every source is proposed as a U-record candidate. One predicate over (target path, source config), no index lookup — which is what lets links and sources be added in either order and reach the same state
- **Resolve** (R3359, R3360): translate a caller's address to the canonical path, component-wise, since the link is usually a directory and any component may be one (R3372). Falls back to the literal path when resolution fails, so a dangling link yields ark's ordinary not-found rather than a resolution error (R3361)
- **ResolvePattern** (R3362): resolve only the longest wildcard-free leading portion, re-attaching the remainder unchanged. A pattern is not a path; this is the mechanism that makes `<link>/**/*` answer in the link's namespace while `/**/*` answers canonically
- **Expand** (R3378): canonical path → every address naming that file, canonical included. Backs `--links`, and ExtMap's anchor lookup depends on it (R3367)
- **Addresses** (R3364, R3380): enumerate alias addresses for listing, including the tracked links themselves as if they were real directories
- **View selection** (R3375, R3379): the global link-view selector — default matches addresses, `--links` expands each result to all of them, `--nolinks` matches canonical only. Consulted by nodes that emit paths as addresses and not by those that emit content (R3377)
- **Classify tier** (R3385): single-hop / internal multi-hop / external multi-hop, from the hop list and source membership. **Derived, never cached across a recompute** — a single-hop link becomes multi-hop the moment its target is replaced by a symlink, and keeping a stale tier means keeping a verification schedule for a shape the link no longer has, which fails open
- **Rebuild**: reconstruct the whole table from the persisted is-a-directory-symlink facts, one `readlink` chain each, at startup
- **SyncPath** (R3390): the per-path counterpart of the walk's Observe, for
  the change events a link produces once it exists. `lstat` first, then
  dispatch on what the path *is* rather than on what it points at — the
  order the scan already uses, so the two surfaces cannot disagree about
  what a link is
  - a symlink to a directory (or a dangling one) → Observe, which re-derives
    the tier as well as the resolution (R3385)
  - gone, or no longer a symlink → forget it. A link that disappears returns
    whatever depended on it to unresolved rather than leaving it pointing at
    a path it can no longer reach (R3369)
  - a symlink to a regular file, or a path that was never a link → not this
    component's business; the caller treats it as an ordinary file, exactly
    as the scan does
  - **Bounded by what `lstat` can state.** *Is a symlink now* and *is gone*
    are answered exactly; a link replaced in place by a non-indexable
    regular file is indistinguishable from any other uninteresting file and
    is not (gap O196)

## Collaborators
- Config: source membership for the track/propose predicate and for tier classification
- Scanner: reports directory symlinks found during a walk (R3352, R3373)
- Store: persists the is-a-directory-symlink fact; holds U records for proposed links (R3355)
- ExtMap: consults Resolve and Expand; owns its own assumption records
- Matcher: pattern semantics for the wildcard-free-prefix split

## Sequences
- seq-link-resolve.md

## Deliberately does not
- **Detect hardlinks** (R3365). Two hardlinks are equally real and neither
  points at the other, so there is no alias to record and no basis for a
  canonical name — this component's whole design has nothing to attach to.
  Content deduplication already means identical content costs one chunk and
  one embedding however many files carry it, so treating them separately
  costs a second file record. Detection would need per-file inode tracking on
  every scan, and inode numbers do not survive a restore from backup, a
  filesystem migration, or a copy, so identity built on them would break in
  ways a user could not diagnose.
- **Cache a forward resolution.** Resolution is performed on demand
  (measured 20–35 µs, and O(queries) rather than O(files), since the query
  resolves once and comparison is canonical-to-canonical). The table exists
  for the *reverse* direction, which the filesystem cannot answer. An earlier
  design cached forward resolutions and needed back pointers, watches, and
  revalidation to keep them honest; all of that was defending a cache that
  did not need to exist.

## Notes
**Canonical is what the scan produced** (R3363), rooted at the source's
configured directory with no mid-path rewriting below it. Because a scan
stops at a directory symlink rather than descending, exactly one source
walk reaches each file, so there is one canonical name and no tie-break.
Resolution translates between addresses; it never rewrites stored paths.
Two earlier rules — "the name inside a configured source" and "the
lexically first" — were tried and failed, both because they assumed
resolution rewrites what is stored. See carves/symlinks.md.
