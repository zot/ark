package ark

// CRC: crc-LinkTable.md | Seq: seq-link-resolve.md
//
// Ark's map of directory symlinks that join one indexed region to another,
// and the translation between an address a caller used and the canonical
// path a file is indexed under.
//
// Wholly in memory (R3383). The index persists only *that* a path is a
// directory symlink; everything derived from its target is rebuilt from the
// filesystem by Rebuild, one readlink chain per recorded link.
//
// No forward resolution is cached. Resolve walks the table on every call,
// which is why nothing here can go stale: the table's only irreplaceable
// content is byTarget, the reverse direction, which the filesystem cannot
// answer. An earlier design cached forward resolutions and needed back
// pointers, watches, and revalidation to keep them honest; all of that was
// defending a cache that did not need to exist.
//
// The table is actor-owned state. Off-actor readers take a copy rather than
// a lock, per specs/db-concurrency.md; nothing here synchronizes.

import (
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync/atomic"
)

// LinkTier says how a tracked link's resolution is verified — each the
// cheapest mechanism that is still correct for that shape of chain.
// Derived state, re-derived on every recomputation and never assumed
// stable: a single-hop link becomes multi-hop the moment its target is
// replaced by a symlink, and keeping the old tier would leave the
// association on a verification schedule for a shape it no longer has.
// CRC: crc-LinkTable.md | R3384, R3385
type LinkTier int

const (
	// TierSingleHop is one link, whose own node lives inside a source, so
	// the change event names the link's own path and a lookup by path
	// suffices. R3384
	TierSingleHop LinkTier = iota
	// TierInternalMultiHop is a chain whose every hop lies inside a
	// source: dirtied by any file change, recomputed on retrieval.
	// Precise invalidation would need a hop-to-dependents reverse index
	// costing more to maintain than the dirtying saves. R3384
	TierInternalMultiHop
	// TierExternalMultiHop is a chain with some hop outside every source,
	// which no event can reach, so it is recomputed on every retrieval and
	// never cached. R3384
	TierExternalMultiHop
)

// CRC: crc-LinkTable.md | R3384
func (t LinkTier) String() string {
	switch t {
	case TierSingleHop:
		return "single-hop"
	case TierInternalMultiHop:
		return "internal-multi-hop"
	case TierExternalMultiHop:
		return "external-multi-hop"
	}
	return "unknown"
}

// LinkStatus is the outcome of the one predicate that decides a directory
// symlink's fate — over (target path, source config), with no index lookup.
// That is what lets a user add links and sources in either order and reach
// the same state: adding the source later is a config change, and
// re-deriving the predicate at config load promotes a proposal to a tracked
// link with no additional trigger.
// CRC: crc-LinkTable.md | R3381
type LinkStatus int

const (
	// LinkTracked means the target lies inside a source. The link becomes
	// a legal address prefix. R3381
	LinkTracked LinkStatus = iota
	// LinkProposed means the target lies outside every source. The link
	// proposes its target as an unresolved-file candidate; it never
	// admits it. R3355, R3381
	LinkProposed
	// LinkUnresolvable means the chain cycles, or the path is not a
	// symlink at all. Neither tracked nor proposed: there is nothing
	// coherent to rewrite to and nothing sane to offer the user as a
	// source. R3353
	LinkUnresolvable
)

// maxLinkRewrites bounds prefix rewriting and chain following. The guard is
// not defensive: the layout permits a cycle, and ark already contains a link
// pointing at the farm that contains it. R3353, R3374
const maxLinkRewrites = 64

// maxAliasDepth bounds how many substitutions deep Expand will go.
//
// A visited set alone cannot terminate reverse expansion, because under a
// cycle the address set is genuinely infinite rather than repeating: given
// a/link -> b and b/link -> a, every additional /link component names a
// further real, legal address of the same file, and each is a string nobody
// has seen before. So the bound is a statement about completeness rather
// than a safety net — beyond it, addresses exist that Expand does not
// return. R3353's guard, in the direction the cycle actually bites, where
// forward rewriting instead shrinks because each substitution consumes a
// prefix.
//
// Do not remove this as defensive. Without it TestLinkCycleTerminates does
// not fail, it *hangs*.
//
// Eight is generous against observed need: the deepest real case on this
// corpus takes two substitutions (R3374).
// CRC: crc-LinkTable.md | R3389
const maxAliasDepth = 8

// LinkEntry is one directory symlink ark has seen.
// CRC: crc-LinkTable.md | R3369, R3383
type LinkEntry struct {
	// Path is the link's own path — the key the scan found it under.
	Path string
	// Target is the immediate target exactly as readlink gave it, which
	// may be relative. readlink succeeds on a dangling link, so this is
	// readable even when the target's volume is unmounted, which is what
	// lets ark report what a link intends to point at with nothing
	// persisted. R3383
	Target string
	// Resolved is the directory the chain ends at, absolute and cleaned.
	// May not exist: the predicate is lexical over the target path, so a
	// dangling link still gets a truthful tracked-or-proposed answer.
	Resolved string
	// Hops is every link component on the path, in traversal order,
	// starting with Path itself. Recording each hop rather than only the
	// endpoint is what makes a mid-chain repoint noticeable: for a -> b ->
	// c, repointing b leaves both ends untouched. R3369
	Hops []string
	// Tier is derived from Hops and source membership, re-derived on every
	// recomputation. R3384, R3385
	Tier LinkTier
}

// LinkTable holds the tracked directory symlinks and the reverse index from
// resolved directory to the links that name it.
// CRC: crc-LinkTable.md | R3383
// linkSnapshot is one immutable version of the table's contents. Nothing
// mutates a published snapshot; a change builds the next one.
// CRC: crc-LinkTable.md | R3359
type linkSnapshot struct {
	// links is the tracked set, keyed by the link's own path.
	links map[string]*LinkEntry
	// byTarget is the reverse index: resolved directory -> the link paths
	// that resolve to it. It cannot be derived from the filesystem, which
	// answers "what does this point at" and never "what points here", and
	// that is the only reason a table exists rather than plain on-demand
	// resolution.
	byTarget map[string][]string
}

// clone copies the maps so a mutation can build the next version without
// touching the one readers are holding. The entries themselves are shared
// and never mutated in place.
func (s *linkSnapshot) clone() *linkSnapshot {
	next := &linkSnapshot{
		links:    make(map[string]*LinkEntry, len(s.links)),
		byTarget: make(map[string][]string, len(s.byTarget)),
	}
	for k, v := range s.links {
		next.links[k] = v
	}
	for k, v := range s.byTarget {
		next.byTarget[k] = append([]string(nil), v...)
	}
	return next
}

// LinkTable is written only on the DB actor and read from anywhere.
//
// Every mutator — Observe, SyncPath, Remove, Rebuild — runs on the actor, and
// every *door* that resolves a caller's path (Fetch, IsIndexed,
// ReadSourceFile, the content URLs, the search file filters) is an off-actor
// read. A bare map read from a door would be the same latent race O154 was.
//
// So the contents live in an immutable snapshot behind an atomic pointer: a
// mutator builds the next version and publishes it with one store, a reader
// takes the current version with one load and holds a consistent picture for
// as long as it needs. The actor remains the only writer, so this is
// publication rather than locking — no reader ever blocks a writer or another
// reader, and the "actors, not mutexes" rule is kept.
//
// Publication belongs to the table rather than to its callers deliberately.
// A scheme where the actor mutates and then remembers to publish is one a
// future edit forgets, and the symptom would be a door quietly resolving
// against a stale table — the same shape as every other silent failure in
// this feature.
//
// cfg is a separate matter: it is read by the predicate and by tier
// classification, both actor-only, and written only by SetConfig, also
// actor-only. No door touches it.
// CRC: crc-LinkTable.md | R3359, R3383
type LinkTable struct {
	cfg  *Config
	snap atomic.Pointer[linkSnapshot]
}

// load returns the currently published snapshot. Readers call this once and
// work from the result, so a mutation mid-read cannot change what they see.
func (lt *LinkTable) load() *linkSnapshot { return lt.snap.Load() }

// mutate builds the next snapshot from the current one and publishes it.
// Actor-only. The clone is what makes the published version immutable, which
// is what lets readers go lock-free.
func (lt *LinkTable) mutate(fn func(*linkSnapshot)) {
	next := lt.load().clone()
	fn(next)
	lt.snap.Store(next)
}

// NewLinkTable returns an empty table bound to a config. The config
// supplies source membership, which is the whole of the track/propose
// predicate and of tier classification.
// CRC: crc-LinkTable.md | R3381
func NewLinkTable(cfg *Config) *LinkTable {
	lt := &LinkTable{cfg: cfg}
	lt.snap.Store(&linkSnapshot{
		links:    make(map[string]*LinkEntry),
		byTarget: make(map[string][]string),
	})
	return lt
}

// Observe classifies a directory symlink and, when tracked, records it.
// Always recomputes from the filesystem, which is what re-derives the tier
// (R3385) rather than carrying a stale one forward.
//
// A LinkProposed result carries the target the caller should record as an
// unresolved-file candidate naming this link (R3355, R3357). A link never
// admits its target; it only proposes it.
// CRC: crc-LinkTable.md | Seq: seq-link-resolve.md#1.2 | R3352, R3355, R3381
func (lt *LinkTable) Observe(linkPath string) (*LinkEntry, LinkStatus) {
	linkPath = filepath.Clean(ExpandTilde(linkPath))
	target, resolved, hops, ok := followLinkChain(linkPath)
	// R3629: a link renamed only in case still lstats under its old
	// spelling; that spelling names no link any more.
	if !ok || respelled(linkPath) {
		lt.Remove(linkPath)
		return nil, LinkUnresolvable
	}
	entry := &LinkEntry{Path: linkPath, Target: target, Resolved: resolved, Hops: hops}
	entry.Tier = lt.classify(hops)
	if _, inSource := lt.cfg.SourceForPath(resolved); !inSource {
		// Outside every source: the link proposes, the user disposes —
		// normally by adding the target's directory as a source. Following
		// it would silently expand what ark indexes on the user's behalf.
		// R3355
		lt.Remove(linkPath)
		return entry, LinkProposed
	}
	// One publication, not two: dropping the old reverse-index entry and
	// adding the new one are a single change, and a reader must never see
	// the gap between them.
	lt.mutate(func(s *linkSnapshot) {
		s.forget(linkPath)
		s.links[linkPath] = entry
		s.byTarget[resolved] = append(s.byTarget[resolved], linkPath)
	})
	return entry, LinkTracked
}

// SyncPath brings the table in line with what is on disk at one path — the
// per-path counterpart of the walk's Observe, for the change events a link
// produces once it exists. A link lives in a source like any other entry, so
// repointing, deleting or replacing it produces an ordinary change event and
// never a full scan; the single-hop tier is defined on exactly that event
// (R3384), which is why nothing verified by it can be trusted without this.
//
// Lstat first, and dispatch on what the path *is* rather than on what it
// points at. That is the order the scan already uses, and getting it backwards
// is the whole defect: following the link and asking whether the destination
// is a directory answers yes for a perfectly ordinary repoint, so the caller
// concludes there is nothing to do.
//
// Bounded by what lstat can state on its own. "Is a symlink now" (created or
// repointed) and "is gone" (deleted) are answered exactly; a link replaced in
// place by a non-indexable regular file is indistinguishable from any other
// uninteresting file, and only the table knows the path used to be an alias
// prefix — see gap O196.
//
// CRC: crc-LinkTable.md | Seq: seq-file-change.md#1.3.5 | R3390
func (lt *LinkTable) SyncPath(path string) (*LinkEntry, LinkStatus) {
	p := filepath.Clean(ExpandTilde(path))
	fi, err := os.Lstat(p)
	if err != nil || fi.Mode()&os.ModeSymlink == 0 {
		// Gone, or no longer a symlink. Either way it has stopped being an
		// alias prefix, and a link that disappears must return whatever
		// depended on it to unresolved rather than leave it pointing at a
		// path it can no longer reach. R3369
		lt.Remove(p)
		return nil, LinkUnresolvable
	}
	if linksToRegularFile(p) {
		return nil, LinkUnresolvable
	}
	return lt.Observe(p)
}

// linksToRegularFile reports whether a symlink's chain ends at something that
// is not a directory. Such a link is a file by another name rather than an
// alias prefix, and keeps ark's pre-existing handling.
//
// One function on purpose. The scan and the per-path refresh must dispose of
// the same link identically — two surfaces that disagree about what a link is
// are worse than one that is consistently wrong — and a duplicated three-line
// predicate is exactly the shape that drifts.
//
// CRC: crc-LinkTable.md | R3352, R3390
func linksToRegularFile(path string) bool {
	info, err := os.Stat(path)
	return err == nil && !info.IsDir()
}

// Remove forgets a link, from both the forward map and the reverse index.
// A link that disappears returns whatever depended on it to unresolved
// rather than leaving it pointing at a file it can no longer reach. R3369
//
// Cleans its argument for the same reason Lookup does: the table is keyed on
// the cleaned form, so a caller passing the path a user or a watcher gave it
// would otherwise silently forget nothing.
// CRC: crc-LinkTable.md | R3369
func (lt *LinkTable) Remove(linkPath string) {
	linkPath = filepath.Clean(ExpandTilde(linkPath))
	if _, ok := lt.load().links[linkPath]; !ok {
		return // nothing to publish
	}
	lt.mutate(func(s *linkSnapshot) { s.forget(linkPath) })
}

// forget drops a link from both indexes. Operates on an unpublished
// snapshot, so callers must be inside mutate.
func (s *linkSnapshot) forget(linkPath string) {
	entry, ok := s.links[linkPath]
	if !ok {
		return
	}
	delete(s.links, linkPath)
	var rest []string
	for _, p := range s.byTarget[entry.Resolved] {
		if p != linkPath {
			rest = append(rest, p)
		}
	}
	if len(rest) == 0 {
		delete(s.byTarget, entry.Resolved)
	} else {
		s.byTarget[entry.Resolved] = rest
	}
}

// SetConfig rebinds the table to a reloaded config. Source membership is the
// whole of the track/propose predicate and of tier classification, so a table
// left pointing at the previous config re-derives against source membership
// that no longer exists — the failure would be a link classified correctly for
// a configuration nobody is running. Actor-only, like every other writer here.
// CRC: crc-LinkTable.md | R3381
func (lt *LinkTable) SetConfig(cfg *Config) { lt.cfg = cfg }

// Rebuild reconstructs the whole table from the persisted
// is-a-directory-symlink facts, one readlink chain each. This is both the
// startup path (R3383) and the config-load re-derivation that promotes a
// proposal to a tracked link when its target's directory becomes a source
// (R3381) — the same predicate, run again, with no additional trigger.
//
// Returns the entries that came back proposed, so the caller can refresh
// their unresolved-file candidates.
// CRC: crc-LinkTable.md | R3381, R3383
func (lt *LinkTable) Rebuild(linkPaths []string) []*LinkEntry {
	lt.snap.Store(&linkSnapshot{
		links:    make(map[string]*LinkEntry, len(linkPaths)),
		byTarget: make(map[string][]string, len(linkPaths)),
	})
	var proposed []*LinkEntry
	for _, p := range linkPaths {
		if entry, status := lt.Observe(p); status == LinkProposed {
			proposed = append(proposed, entry)
		}
	}
	return proposed
}

// Lookup returns the tracked entry for a link path.
// CRC: crc-LinkTable.md | R3384
func (lt *LinkTable) Lookup(linkPath string) (*LinkEntry, bool) {
	entry, ok := lt.load().links[filepath.Clean(ExpandTilde(linkPath))]
	return entry, ok
}

// Links returns every tracked link path, sorted. A tracked link appears in
// path listings as if it were a real directory: listing the addresses that
// exist only because of a link while hiding the link itself would describe
// an address space whose own structure is invisible. R3380
// CRC: crc-LinkTable.md | R3380
func (lt *LinkTable) Links() []string {
	snap := lt.load()
	out := make([]string, 0, len(snap.links))
	for p := range snap.links {
		out = append(out, p)
	}
	sort.Strings(out)
	return out
}

// Len reports the tracked link count.
// CRC: crc-LinkTable.md | R3383
func (lt *LinkTable) Len() int { return len(lt.load().links) }

// Resolve translates a caller's address into the canonical namespace by
// longest-prefix rewrite, iterated until no tracked prefix matches.
//
// Component-wise, not string-wise, and every component is a candidate
// rather than only the one the user wrote (R3372): the link is usually a
// directory, and tilde expansion yields a *lexically* absolute path that
// may still traverse links, which is exactly how an @ext target came to be
// stored under a spelling its own file would never match.
//
// Iteration is required rather than defensive — ark contains a link inside
// the very tree other links point at, so some paths take two rewrites — and
// a path that cannot be resolved falls back to its literal form, so a
// dangling link yields ark's ordinary not-found rather than an error
// (R3361).
// CRC: crc-LinkTable.md | Seq: seq-link-resolve.md#1.2 | R3359, R3360, R3361, R3372, R3374
func (lt *LinkTable) Resolve(path string) string {
	resolved, _ := lt.ResolveVia(path)
	return resolved
}

// ResolveVia resolves exactly as Resolve does and additionally reports
// every link component the walk traversed, in traversal order.
//
// The hops are what let a caller classify the *resolution chain* rather
// than a single entry, and the distinction is the whole reason this exists.
// A tier is a property of the chain, not of a LinkEntry: one address can
// cross two separately-tracked links — ark's own layout does, since a link
// living inside another link's target tree takes two rewrites — and an
// association built on that address has to be verified on a schedule
// covering both. Concatenating each traversed entry's own Hops yields the
// full chain, which is what classify already reasons about, so a
// two-link address classifies as multi-hop without a special case.
//
// hops is nil when the address crosses no tracked link, which is the signal
// that the caller needs no premise record at all (R3372), and nil likewise
// when the walk falls back to the literal form — a cycle or rewrite
// exhaustion leaves no resolution to record a premise against, so the
// routing resolves against the literal path and fails closed (R3386).
// CRC: crc-LinkTable.md | Seq: seq-link-resolve.md#1.2 | R3359, R3360, R3361, R3372, R3374, R3384, R3385
func (lt *LinkTable) ResolveVia(path string) (resolved string, hops []string) {
	snap := lt.load() // one picture for the whole walk
	// R3627: spelled as the disk spells it before any rewrite, so every
	// surface resolving through here agrees on one spelling per file.
	literal := DiskSpelling(filepath.Clean(ExpandTilde(path)))
	p := literal
	seen := make(map[string]bool, 4)
	for i := 0; i < maxLinkRewrites; i++ {
		if seen[p] {
			return literal, nil // cycle among tracked prefixes: literal, never a loop
		}
		seen[p] = true
		prefix := snap.longestTrackedPrefix(p)
		if prefix == "" {
			return p, hops
		}
		entry := snap.links[prefix]
		hops = append(hops, entry.Hops...)
		p = filepath.Clean(entry.Resolved + p[len(prefix):])
	}
	return literal, nil
}

// LinkView selects which of the three views of the address space a listing
// answers in. One axis, three points (R3375, R3378): the default matches
// addresses, LinkViewExpand matches then expands each result to every address
// of that file, and LinkViewCanonical matches canonical addresses only.
//
// Defined here rather than in the CLI so the cold and proxied doors share one
// type. Two parallel enums would drift, and the drift would show as a listing
// that answers differently depending on whether a server happened to be up —
// the failure R3359 exists to prevent.
// CRC: crc-LinkTable.md | R3375, R3378
type LinkView int

const (
	// LinkViewDefault matches addresses: `**/a` answers the canonical path and
	// the alias both, because both are strings in the address space.
	LinkViewDefault LinkView = iota
	// LinkViewExpand (--links) matches addresses, then expands each result to
	// every address of that file. The inverse question: what else is this file
	// called? (R3378)
	LinkViewExpand
	// LinkViewCanonical (--nolinks) matches canonical addresses only, as if no
	// link were indexed — how a caller needing a stable file set gets one,
	// since creating a link does change what an unfiltered listing returns
	// (R3375).
	LinkViewCanonical
)

func (v LinkView) String() string {
	switch v {
	case LinkViewExpand:
		return "links"
	case LinkViewCanonical:
		return "nolinks"
	}
	return "default"
}

// AddressSpaceFor returns the addresses a listing matches against under a given
// view, from a canonical file set.
//
// Under LinkViewCanonical the canonical set is returned untouched — that is the
// whole point of the view, and it is why creating a link cannot change what a
// `--nolinks` listing returns (R3375).
//
// Otherwise every alias of every file joins the space, and so does each tracked
// link's own path (R3380): the link is an indexed entity, so `ark files '**/*'`
// lists it beside the files addressable beneath it. Omitting the link while
// listing paths that exist only because of it would describe an address space
// whose own structure is invisible.
// CRC: crc-LinkTable.md | R3375, R3378, R3380
func (lt *LinkTable) AddressSpaceFor(canonical []string, view LinkView) []string {
	if view == LinkViewCanonical || lt == nil {
		return canonical
	}
	seen := make(map[string]bool, len(canonical)*2)
	out := make([]string, 0, len(canonical)*2)
	add := func(p string) {
		if !seen[p] {
			seen[p] = true
			out = append(out, p)
		}
	}
	for _, c := range canonical {
		for _, addr := range lt.Expand(c) {
			add(addr)
		}
	}
	// R3380: the links themselves, as directory entries.
	for _, linkPath := range lt.Links() {
		add(linkPath)
	}
	return out
}

// ExpandMatches turns matched addresses into every address of each matched
// file, which is what --links asks for (R3378). Applied *after* matching, so
// `ark --links files ~/source1/a` answers both addresses while the bare form
// answers the one the pattern named.
//
// Canonicalizes first, because two matched aliases of one file must expand to
// the same set rather than to two overlapping ones.
// CRC: crc-LinkTable.md | R3378
func (lt *LinkTable) ExpandMatches(matched []string) []string {
	if lt == nil {
		return matched
	}
	seen := make(map[string]bool, len(matched)*2)
	out := make([]string, 0, len(matched)*2)
	for _, m := range matched {
		for _, addr := range lt.Expand(lt.Resolve(m)) {
			if !seen[addr] {
				seen[addr] = true
				out = append(out, addr)
			}
		}
	}
	return out
}

// PathUnder reports whether p is prefix itself, or lies beneath it on a
// component boundary. A bare string prefix is wrong here — `/a/bc` is not
// under `/a/b` — and that is the same component-vs-string distinction Resolve
// rests on.
// CRC: crc-LinkTable.md | R3374
func PathUnder(p, prefix string) bool {
	if p == prefix {
		return true
	}
	return len(p) > len(prefix) && strings.HasPrefix(p, prefix) && p[len(prefix)] == filepath.Separator
}

// ClassifyChain derives the verification tier for a resolution chain — the
// hop list ResolveVia reports — rather than for a single entry. Exported
// because the tier is ExtMap's to record and re-derive (R3385) while source
// membership is the table's to know.
// CRC: crc-LinkTable.md | R3384, R3385
func (lt *LinkTable) ClassifyChain(hops []string) LinkTier { return lt.classify(hops) }

// ResolvePattern resolves only the longest wildcard-free leading portion of
// a pattern and re-attaches the remainder unchanged. A pattern is not a
// path: there is no file at `**/*.md` to resolve. This is also the
// mechanism behind namespace-preserving answers — it is what lets
// `<link>/**/*` answer in the link's namespace while `/**/*` answers
// canonically.
// CRC: crc-LinkTable.md | Seq: seq-link-resolve.md#1.1 | R3362, R3642
func (lt *LinkTable) ResolvePattern(pattern string) string {
	p := ExpandTilde(pattern)
	if !IsGlob(p) {
		return lt.Resolve(p)
	}
	wild := strings.IndexAny(p, "*?[")
	cut := strings.LastIndexAny(p[:wild], "/"+string(filepath.Separator)) // R3642: either separator on Windows
	if cut <= 0 {
		return p // the wildcard is in the first component: nothing concrete to resolve
	}
	return lt.Resolve(p[:cut]) + p[cut:]
}

// Expand returns every address naming the file at a canonical path, the
// canonical path included. This is the inverse of ordinary matching and the
// way to ask "what else is this file called?" — without it an alias address
// is discoverable only by already knowing the link exists.
//
// Iterates to a fixed point, since a link may point into a tree another
// link also targets, and the visited set is the cycle guard.
// CRC: crc-LinkTable.md | Seq: seq-link-resolve.md#2 | R3364, R3374, R3378
func (lt *LinkTable) Expand(canonical string) []string {
	snap := lt.load() // one picture for the whole expansion
	start := filepath.Clean(ExpandTilde(canonical))
	out := []string{start}
	seen := map[string]bool{start: true}
	type step struct {
		path  string
		depth int
	}
	for queue := []step{{start, 0}}; len(queue) > 0; {
		cur := queue[0]
		queue = queue[1:]
		if cur.depth >= maxAliasDepth {
			continue
		}
		// Walk cur.path's ancestors longest-first against the reverse index.
		for dir := cur.path; ; dir = filepath.Dir(dir) {
			for _, linkPath := range snap.byTarget[dir] {
				alias := filepath.Clean(linkPath + cur.path[len(dir):])
				if !seen[alias] {
					seen[alias] = true
					out = append(out, alias)
					queue = append(queue, step{alias, cur.depth + 1})
				}
			}
			if parent := filepath.Dir(dir); parent == dir {
				break
			}
		}
	}
	sort.Strings(out)
	return out
}

// longestTrackedPrefix returns the longest tracked link path that prefixes
// p on a component boundary, or "". Walking up from p visits candidates
// longest-first, so the first hit is the answer — which is what makes
// nested links deterministic when both a directory and one of its ancestors
// are tracked. R3374
// CRC: crc-LinkTable.md | R3374
func (s *linkSnapshot) longestTrackedPrefix(p string) string {
	for dir := p; ; dir = filepath.Dir(dir) {
		if _, ok := s.links[dir]; ok {
			return dir
		}
		if parent := filepath.Dir(dir); parent == dir {
			return ""
		}
	}
}

// classify derives the verification tier from the hop list and source
// membership. hops[0] is the link's own node, which lives inside a source
// by construction — a scan of a source is what found it — so only later
// hops can sit in territory ark never looks at.
// CRC: crc-LinkTable.md | R3384, R3385
func (lt *LinkTable) classify(hops []string) LinkTier {
	if len(hops) <= 1 {
		return TierSingleHop
	}
	for _, hop := range hops[1:] {
		if _, ok := lt.cfg.SourceForPath(hop); !ok {
			return TierExternalMultiHop
		}
	}
	return TierInternalMultiHop
}

// followLinkChain walks linkPath through successive symlinks, recording
// every link component it traverses.
//
// Returns the immediate target as readlink gave it, the directory the chain
// ends at, the hops, and whether the chain is coherent. It is not coherent
// when linkPath is not a symlink at all, or when the chain cycles — the
// layout permits a cycle and ark already has a link pointing at the farm
// containing it, so termination is a property of this walk rather than of
// the data (R3353).
//
// A chain ending at a path that does not exist still resolves: readlink
// fails, the walk stops, and the lexical target is returned. That keeps the
// track/propose predicate answerable for a dangling link (R3381) and keeps
// resolution falling back rather than erroring (R3361).
// CRC: crc-LinkTable.md | Seq: seq-link-resolve.md#1.2 | R3353, R3369
func followLinkChain(linkPath string) (target, resolved string, hops []string, ok bool) {
	cur := linkPath
	seen := make(map[string]bool, 4)
	for i := 0; i < maxLinkRewrites; i++ {
		if seen[cur] {
			return target, "", hops, false // cycle
		}
		seen[cur] = true
		next, err := os.Readlink(cur)
		if err != nil {
			break // not a link: the chain ends here
		}
		hops = append(hops, cur)
		if i == 0 {
			target = next
		}
		if !filepath.IsAbs(next) {
			next = filepath.Join(filepath.Dir(cur), next)
		}
		cur = filepath.Clean(next)
	}
	if len(hops) == 0 {
		return "", "", nil, false // not a symlink at all
	}
	return target, cur, hops, true
}
