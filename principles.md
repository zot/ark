# ark — Principles

Project-level commitments about what ark is *for* and how it
treats the user. Principles are stronger than design decisions:
they constrain every feature, every error path, every API.

Each principle below has a name, a statement, the reasoning
behind it, and concrete consequences in the codebase.

---

## User's access to their own data is primary

**Statement.** Ark never refuses to index, search, render, or
return the user's data because of a parser quirk, a non-conforming
chunk, a missing dependency, or an unexpected format. When
something doesn't fit, the *feature* degrades; the *data* stays
accessible. When a feature can't be applied, ark surfaces the
reason loudly enough that the user knows why — and offers a
fallback path.

**Why.** The user authored the data. Whatever they wrote, ark's
job is to make it findable and usable to them. Strict
conformance regimes turn small authoring quirks into data
inaccessibility, which is the worst possible outcome for a
zettelkasten. The user can't fix what they can't see; the user
shouldn't have to.

**Consequences.**

- **Soft contracts, not hard.** Chunker contracts (e.g., range
  strings shouldn't start with `"` or `/`) are documented as
  "should not," with explicit degradation paths. A chunker that
  emits a non-conforming range still indexes and is searchable;
  only the advanced features that depend on the contract become
  unavailable for that chunk. See `specs/at-ext-parsing.md`
  "Chunker contract."
- **Loud about failures, never silent.** When a feature falls
  back, the UI shows the reason. Silent fallback is forbidden —
  the user can't act on what they can't see.
- **Indexing never aborts on bad input.** A malformed tag, a
  weird filename, a binary file in a text source — none of
  these stop the indexer. They get logged, surfaced where
  appropriate, and the rest of the corpus indexes normally.
- **No "this file is incompatible" walls.** Every file ark
  scans contributes what it can. PDFs are read-only but
  searchable. JSONL chats produce per-chunk results. Empty
  files are skipped (not erroring) and reported.
- **Search is the floor.** No matter what else fails, FTS
  works. The trigram index is the always-available recall path;
  vector search, ext routing, hot correlations, and other
  advanced features sit on top of it but never gate it.

**Boundary.** This principle is about data access, not
authorization. Ark trusts its operator with their own data —
this is local-first software, not a server with users. The
principle does not weaken with multi-user scenarios because ark
does not have multi-user scenarios.

---

## All data lives in your files; the index is rebuildable

**Statement.** Every piece of persistent state in ark is
derivable from the user's source files. The LMDB index is a
cache, not a database. `ark rebuild` reconstructs the entire
index from disk and loses nothing the user wrote. Tags — `@id`,
`@ext`, `@link`, inline annotations — carry the knowledge graph
*inside* the source content. Vectors and trigram indexes
enhance discovery but do not comprise the graph; changing the
embedding model, dropping the trigram index, or wiping
`~/.ark/data.mdb` does not destroy any structural relationship
the user authored.

**Why.** A knowledge graph the user can lose by upgrading
software is a knowledge graph the user doesn't really own. Many
notes tools build their structure inside opaque proprietary
state (cloud servers, app-specific databases, vector stores).
That ties the user's accumulated work to a specific vendor, a
specific version, a specific model. Ark inverts that: the
files *are* the knowledge graph; ark is one of many possible
readers. The index is performance optimization, not the
representation.

**Consequences.**

- **`ark rebuild` is total recovery.** Wipe `~/.ark/data.mdb`
  and rebuild: tag taxonomy returns (from `~/.ark/tags.md`),
  `@ext` routings return (from source content + mirror files),
  `@id` chunks reattach, file relationships rebuild. No
  user-visible loss.
- **Vector model is swappable.** Change `tag_model` in
  `ark.toml` and re-embed. The structural graph (tags, ext
  routings, `@id` links) is untouched. Vectors are an
  enhancement layer, not the foundation. The corollary: ark
  never embeds *user-visible meaning* in vectors. Vectors are
  for similarity discovery, not for representing the
  user's authored relationships.
- **Tags are the link primitive.** `@ext`, `@id`, `@link`,
  inline `@tag: value` annotations — all live in source files
  (or in the workshop's `~/.ark/external/` mirror tree, which
  is itself plain markdown). They survive every ark version
  bump, every model change, every index format migration.
- **Mirror files are user-readable.** When the workshop authors
  `@ext` routings into `~/.ark/external/`, it writes plain
  markdown files with `@ext:` lines. The user can read, edit,
  back up, version-control, or hand-port them. They are not
  opaque state.
- **No "ark is the only way to access this."** Every artifact
  ark produces that the user might want — tag definitions,
  ext routings, computed correlations — either lives in a
  user-readable file already, or is recomputable from files.
  Cloud-style "your data is held hostage by the app" is
  forbidden.
- **Index-only state must be cheap to lose.** Anything stored
  *only* in LMDB (trigram tries, embedding vectors, fileid
  allocations, runtime counters) is either small enough to
  regenerate quickly, or is non-essential. The rebuild
  performance target is "tolerable wait, not days."

**Boundary.** This principle doesn't say ark cannot have a
fast index — it says the fast index cannot become the source
of truth. Performance optimizations are welcome; they just
have to remain optimizations.

---

## Text is the truth; views are derived

**Statement.** Inside any editing surface, the user's bytes are
the source of truth — never a parallel authoritative state in
row objects, presenter caches, or widget records. The text
buffer holds what is. Everything the UI shows about that text
(tag rows, structural outlines, render previews, dropdown
indicators) is a *projection* that re-computes when the bytes
change. The user can edit, undo, redo, switch surfaces — the
view follows the text, never leads it.

**Why.** When two surfaces both claim authority for the same
information, they drift. Reconciling the drift takes either
heavy synchronization or careful one-way pipes; either way the
user pays in surprising behavior when undo, refresh, or
multi-surface editing puts the two out of phase. Picking *the
text* as truth at every scale — file bytes for the corpus, CM6
buffer for the in-progress chunk — makes undo trivial, makes
multi-occurrence handling natural (the Nth `@tag:` line is the
Nth `@tag:` line, however the rows render it), and keeps the
mental model clean across all editing modes.

This is the in-session analog of the persistence principle
above. Where that one says "files are the truth, the index is a
cache," this one says "the buffer is the truth, the rows are a
cache." Same shape at different scales.

**Consequences.**

- **No row-level state.** Tag row presenters don't hold
  authoritative tag values. The row's `name` and `value` are
  set by re-extraction from the editor's current text on every
  docChanged. Edits propagate by mutating the text (one CM6
  transaction per logical edit), not by mutating row fields
  directly.
- **Undo works for free.** Each row-driven edit is a single CM6
  transaction. Ctrl-Z in the editor reverts the text; the next
  docChanged re-derives row state from the rolled-back text;
  the row visually returns to its previous values. No row-level
  undo stack needed.
- **Multi-occurrence is positional.** When the same tag name
  appears multiple times in a chunk (`@topic: streaming` and
  `@topic: realtime`), each row carries its 1-based occurrence
  ordinal. Edits route to the Nth `@topic:` line. Re-extraction
  on docChanged preserves correct mapping because the ordinals
  match.
- **External state is the lone exception, and it earns it.**
  `@ext` mirror files live outside the chunk text by design.
  They are their own files — themselves text-as-truth. The card
  surfaces them as ext rows; pending ext-set / ext-remove ops
  apply at Accept time, writing to those mirror files. The ext
  rows in current-tags are a projection of (`~/.ark/external/`
  mirror state + pending ops), the same way inline rows project
  the chunk text.
- **Side-by-side bypass forbidden.** A new editing affordance
  must edit *the bytes* (or queue a pending op that edits
  bytes), never write directly to row state and call it done.
  If the rows hold state the bytes don't, the surfaces have
  drifted.

**Boundary.** This principle is about authoritative state, not
display caches. Of course the UI may cache scraped values or
parsed lists for performance; it just can't *author* into those
caches independently of the underlying bytes.

---

<!-- More principles accrete here as they are named in design
discussions. Candidates already implicit in the codebase:
"local-first" (no cloud dependency), "human-readable embeddings"
(tags as named, correctable dimensions), CLI parity (every
operation available from the command line), "batteries included"
(AI makes it more powerful but you can operate without it -- 
"free", "as in beer"). Each will get its own section when it's
worth canonizing. -->
