# Symlinks

Ark knows a file by its absolute path. The filesystem lets one file have
several paths, so those two facts disagree, and until they are reconciled a
path reached through a symlink names a file ark cannot find — even when that
file is indexed under its real name.

The reconciliation has one shape: **ark records aliases and resolves into a
single canonical namespace.** It does not follow links while scanning, and it
does not treat an alias as a second file.

Reasoning and the decisions behind it: [carves/symlinks.md](../carves/symlinks.md).

## Why users make symlinks

The design must serve two habits that want opposite things, so both are
stated rather than assumed.

**Pointing at the most up-to-date version.** A link into a development
project, a dotfile manager, one artifact shared between projects. The user
would say the file *is* in the project and merely *appears* at the link.

**Filesystem concerns.** Some files must live on an encrypted partition and
the rest must not; a volume runs low and a new one is grafted on to extend
space; a stable name points at a rotating target (`libfoo.so` →
`libfoo.so.0.0.1`). Here the link is where the file logically lives and the
target is a storage decision.

The first wants the target's name; the second wants the link's. Nothing in
the filesystem distinguishes them, so ark does not try to tell them apart.

## Scanning records links; it does not follow them

A scan that meets a symlink records it — the link's path and the path it
points at — and does not descend into it.

Recording rather than following is what keeps one file from becoming several
index entries governed by different include/exclude rules. Recording rather
than *skipping* is what makes the link something ark can talk about: a
dangling link becomes a statable fact instead of an invisible omission, and
"why is this file not indexed?" becomes answerable without reading code.

Cycles terminate. A link may point at a directory containing links back to
it, and a scan meeting one must not loop.

A **symlinked source directory** is resolved, not walked as a link. Without
this a source whose `dir` is a symlink appears to scan successfully while
indexing nothing at all.

## One predicate decides a link's fate

A directory symlink is **tracked** when its target lies inside a source, and
**proposed** as an unresolved candidate when it does not. One test over the
target path and the source configuration, with no index lookup.

In particular, *not* "when the target has indexed files beneath it." That
alternative sounds more careful and is worse in three ways. A target
acquiring its first indexable file would have to re-evaluate links living in a
different source, needing a reverse index from directory to link consulted on
every file addition. Tracked status would flap during ordinary editing —
removing the last indexable file untracks the link, adding one retracks it —
which is how intermittent, unreproducible failures are made. And it would
split into two unrelated tests what is really one predicate with two arms.

Being one predicate is what lets a user **add links and sources in either
order** and arrive at the same place. Add the link first and its target is
outside every source, so it is a candidate; add that source later and the
predicate, re-derived at config load, promotes the candidate to a tracked link
with nothing else to trigger.

The cost is a tracked link whose target holds nothing indexable: an empty
alias namespace, which answers nothing. That is the truthful answer.

## A link proposes its target; it never admits it

Sources define what ark may index. A link whose target is not covered by any
source does **not** cause that target to be indexed. Instead the target is
recorded as an unresolved-file candidate, carrying the link that proposed it,
and the user decides — normally by adding the target's directory as a source.

This is deliberate in both directions. Following the link would silently
expand what ark indexes on the user's behalf, and an enormous foreign tree
would be discovered from a disk-usage graph rather than from a decision.
Ignoring the link would lose content the user plainly wants, since the
storage-placement habit above puts wanted files outside every source *by
construction*.

## Unresolved-file candidates carry a reason

An unresolved file is one ark noticed and did not index. There is more than
one way for that to happen, and they call for different responses, so each
candidate records which applies:

- **Not covered by configuration** — no include pattern matches it. The user
  resolves it by editing patterns.
- **Not indexable** — no chunker handles this kind of file. No pattern change
  will help today.
- **Proposed by a link** — reached only through a symlink, from outside every
  source. The user resolves it by adding a source, and the proposing link is
  recorded so the candidate can explain itself.

**Not indexable means hidden, never excluded.** It is a statement about ark's
current set of chunkers, not about the file. A user interface may hide these
by default as a convenience, since they are the great majority. They remain
candidates, so adding a chunker for that kind of file makes every one of them
eligible again with no user action. Excluding them instead would look
identical on the day it was done and silently lose that revival.

## Every path ark accepts is resolved

Resolution is not a property of one command. Any path arriving from outside
ark is resolved into the canonical namespace before it is used: paths given
to the CLI, source directories in `ark.toml`, `@ext` tag targets, content
URLs, and the file filters in the search stack.

A partial fix is worse than none here, because a surface that resolves and a
surface that does not will disagree about whether the same file exists.

**Resolution is component-wise, and any component may be a link.** Not only
the one the user wrote. Resolving a path means walking every component, not
resolving the string as a whole.

Mid-path links are ordinary rather than exotic. macOS ships `/tmp` and `/var`
as links; home directories are routinely mounted through one; ark's own layout
reaches files through a linked directory two levels above the filename. Most
paths a user types are alias-bearing without the user having chosen it.

**Absolute is not the same as resolved, and tilde expansion is where the two
part company.** Expanding `~/x` to `/home/u/x` produces a *lexically* absolute
path whether or not `/home/u` is itself a link. Code that expands the tilde,
confirms the result starts with a separator, and stops has resolved nothing
while looking finished — which is precisely how `@ext` targets came to be
stored under a spelling their own files would never match.

A path whose resolution equals its literal form traverses no link at the time
it is resolved, and needs no alias tracking. That is the cheap test for
whether any of this applies to a given path at all.

**A link that cannot be resolved falls back to the literal path.** A dangling
symlink must produce ark's ordinary "not found" or "not indexed" answer, not
a resolution failure.

**A pattern is not a path.** Globs cannot be resolved wholesale. The longest
leading portion of a pattern that contains no wildcards is resolved, and the
pattern's remainder applies unchanged after it.

## What is stored records the premise it was computed under

Ark sees only the links it scans, and it scans only inside sources. A link
elsewhere on a resolution path, an intermediate hop in a directory ark does not
index or a remounted ancestor, can change with nothing to witness it.

That failure is worse than the one this feature exists to fix. A stale answer
does not degrade into "not indexed"; it keeps naming the file an address *used*
to reach, so the address quietly answers with another file's contents. Tags are
about to become load-bearing, so nothing flaky is built in on purpose: slowness
is bounded and visible, wrongness is neither.

Most of the problem never arises, because **no forward resolution is cached**.
Resolving an address costs tens of microseconds and scales with queries rather
than with files, so a resolution performed at the moment of use cannot be stale.
What needs keeping honest is the narrow set of facts computed *from* a
resolution and then stored: an `@ext` routing recorded against the chunk it
resolved to.

**Each such association records the premise it was computed under** — the target
and the fileid it resolved to. That pair turns verification into a comparison
rather than a re-derivation, so an unchanged fileid means no records are
rewritten and no maps are touched. The schedule decides *when* to verify; the
recorded fileid decides *whether* to act.

### Three tiers, each the cheapest mechanism still correct

**A tracked link's own node is not one of those blind spots.** It lives inside a
source, since a scan of a source is what found it, so repointing it is an
ordinary change to a directory entry and is seen by the machinery that sees
every other change in a source. Only *later* hops in a chain can sit in
territory ark never looks at. That gives three shapes, and they do not share a
mechanism.

- **Single hop.** Recompute when the link entry itself changes: repointed,
  replaced by a real file, or deleted. Precise invalidation is affordable here
  because the event names the link's own path, so a lookup by path suffices and
  no reverse index is needed.
- **Internal multi-hop**, every hop inside a source. Mark dirty on any file
  change and recompute at the next retrieval. Precise invalidation would need a
  hop-to-dependents reverse index, and dirtying a handful of entries costs less
  to maintain than that index.
- **External multi-hop**, some hop outside every source. Recompute on every
  retrieval and never cache, because no event can reach it.

Measured on this corpus: 71 single-hop, 5 internal multi-hop, no external ones.

**A tier is derived state, re-derived on every recomputation.** A single-hop
link becomes multi-hop the moment its target is replaced by a symlink, and an
internal chain becomes external when an intermediate moves out of a source. If
recomputation refreshes the fileid but keeps the old tier, that association
holds a verification schedule suited to a shape it no longer has, and stops
being checked for the hop that now exists beyond it. It then reads as
trustworthy while its premise goes unchecked, which is the one way this scheme
fails silently in the wrong direction. Re-tiering is therefore part of
recomputation rather than a pass of its own.

**An association that cannot be computed is unresolved, never last-known-good.**
An unmounted volume or a vanished intermediate makes resolution fail at
retrieval. A tag that does not apply is an absence someone can see and a number
they can watch; a tag routed to the wrong chunk is neither.

A target directory vanishing while its link is untouched is deliberately not a
trigger. No event names the link, and the target's files leave the index through
the ordinary removal path, so the stale association is cleared by the orphan
cleanup that already exists rather than by a fourth mechanism.

### Retrieval is sewn, not called

A retrieval that finds a changed fileid must mutate, so a read path cannot
compute over ext state without touching actor-owned state off the actor.
Update-and-retrieve therefore runs on the actor that owns that state, any
mutation posts to the write actor, and the retrieved tags travel forward as a
posted continuation. Nothing is held at any instant.

This is sound only because the closure actor delivers in order. Posting a
follow-up behind a message is what sequences the continuation after the mutation
it depends on, and before that guarantee was restored the continuation could
overtake it. A recomputation triggered by a changed fileid reuses the existing
re-resolution path rather than a parallel one, so the records and maps that must
move together keep moving together.

## Chains are recorded hop by hop, so a resolution can heal

A link may point at another link. Ark records each hop rather than only the
final destination, and a stored resolution is invalidated when any link on its
path changes.

Recording only the endpoint would be unhealable. Given `a → b → c`, repointing
the middle link `b` to `d` changes what `a` resolves to while leaving **both
ends untouched** — the name written by the user is unchanged and the target
file is unchanged — so nothing that watches either end has any reason to look
again. The stale answer would stand indefinitely, and the information needed
to notice is exactly what endpoint-only storage throws away.

Recording the hops is what makes a mid-chain change noticeable at all. A link
that disappears returns whatever depended on it to unresolved rather than
leaving it pointing at a file it can no longer reach.

The hop list is also the input to the tier question above: whether every hop
lies inside a source is what separates a chain ark can invalidate on an event
from one it must recompute on every use.

## A tracked directory symlink makes its subtree addressable

Suppose `~/source1` and `~/source2` are both real directories and both
sources, `~/source1/a` is a file, and `~/source2/link` is a symlink to
`~/source1`.

Scanning `~/source2` indexes `~/source2/link` **as a symlinked directory** and
stops. It does not descend, so `a` is still indexed exactly once, by the walk
of `~/source1`. What the link record buys is that `~/source2/link` becomes a
legal *address prefix*: paths beneath it name real files, and answers keep the
prefix unaltered.

Because the link entry is indexed like any other entry in its source, a change
to it — repointed, replaced, removed — arrives through the ordinary refresh
path. That path must therefore recognise a directory-symlink entry and update
the link table. It does not today: a directory symlink is classified as a
non-directory, tested against file patterns, and dropped, so the change event
fires and nothing acts on it. Making that path handle link entries is what
lets everything below rely on links staying current.

```
ark files ~/source2/link/a       →  ~/source2/link/a
ark files '~/source2/link/**/*'  →  every file of ~/source1, prefixed ~/source2/link
ark files '/**/a'                →  ~/source1/a  and  ~/source2/link/a
```

(The third uses the `/`-anchored form deliberately: a bare CLI glob is
measured against the current directory, per the glob-anchoring rules in
[main.md](main.md#glob-patterns), so `**/a` would ask a narrower question than
"anywhere in the index".)

The alias lives on the *link*, not on each file beneath it, so the cost is one
record per link rather than one per aliased file.

A tracked link **appears in path listings as if it were a real directory** —
`ark files '**/*'` lists `~/source2/link` alongside the addresses beneath it.
Listing paths that exist only because of a link while hiding the link would
describe an address space whose own structure is invisible. `--nolinks` omits
both.

**Canonical means "the name the scan gave it."** A file's canonical path is
where the scan indexed it, rooted at its source's configured directory with no
mid-path rewriting below that root. Because a scan stops at a directory
symlink instead of descending, exactly one source walk reaches each file, so
there is one canonical name and no tie-break to make. Resolution is a
lookup-time translation between addresses; it never rewrites what is stored.

Applying a tracked link is a longest-prefix rewrite, **iterated** until no
tracked prefix matches, with a cycle guard. Iteration is not defensive: ark
already contains a link inside the very tree that other links point at, so
some paths take two rewrites. The guard is not defensive either — the layout
permits a cycle, and one link already points at the farm containing it.

**Going the other way is bounded by depth, and the bound is a real limit
rather than a safety net.** Listing every address of a file cannot be
terminated by remembering which addresses have been seen, because under a
cycle the set is genuinely infinite rather than repeating: given `a/link`
pointing at `b` and `b/link` pointing at `a`, every further `link`
component names another real, legal address of the same file, and each is a
string nobody has seen before. So expansion stops at a fixed number of
substitutions, and beyond it there are addresses ark does not report. Eight
is generous against a corpus whose deepest real case is two.

## Three views of the address space

A pattern matches *addresses*. This is why `~/source1/a` answers with one path
while `'**/a'` answers with two: the first matches one string in the address
space, the second matches two.

- **Default** — match addresses.
- **`--links`** — match addresses, then expand each result to *every* address
  of that file, canonical included. The inverse question: what else is this
  file called? Without it, an alias is discoverable only by already knowing
  the link exists.
- **`--nolinks`** — match canonical addresses only, as if no link were
  indexed. How a caller that needs a stable file set gets one, since creating
  a link does change what an unfiltered listing returns.

`--status` reports canonical paths whatever spelling the query used, so it is
also how two listed addresses are revealed to be one file.

The selector is a global flag, consulted only by commands that emit paths as
addresses.

## Aliases are addresses, not content

**Search reports canonical paths only.** An alias is a way to reach a file,
not something the file says, and a chunk has exactly one home.

Letting aliases into results would return the same passage once per address —
seven times over for files in ark's most-aliased directory — and duplicates
differing only by path prefix are exactly the kind deduplication cannot
collapse, because the content is identical while the file records are not.
That is the failure mode that degrades recall and search seeding, so the
boundary between addressing and content is load-bearing rather than tidy.

## Hardlinks are separate files

Ark makes no attempt to detect that two paths are the same inode.

Two hardlinks are equally real and neither points at the other, so there is
no alias to record and no basis for choosing a canonical name. Content
deduplication already means identical content costs one chunk and one
embedding however many files carry it, so the cost of treating them
separately is a second file record. Detecting them would require tracking
inodes for every file on every scan, and inode numbers do not survive a
restore from backup, a filesystem migration, or a copy — so identity built on
them would break in ways a user could not diagnose.
