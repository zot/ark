# Tag Definition Embeddings

Embed every tag-definition text so a chunk can retrieve the tag
*names* whose definitions describe it. Substrate for V3 manual
chunk curation: the user clicks Curate on a chunk, ark proposes
tag names whose ED vectors are nearest the chunk's EC vector.

Language: Go. Environment: ark server, yzma (purego) loading
runtime-provisioned llama.cpp shared libs (`ark embed install`).

## Context

Ark already has three embedding record classes:

- **T** — tag-name embeddings, inline on T records.
- **EV** — tag-value compound embeddings, keyed by tvid.
- **EC** — chunk content embeddings, keyed by chunkid.

What's missing is "this chunk is *about* the kind of thing tag X
describes." Tag *names* alone (T) are too short to carry that
signal — `@decision` says nothing until you read what `@decision`
*means*. The definition text in the D record carries that meaning.
Embedding it gives chunk → tag-name retrieval a real semantic
ranking instead of trigram coincidence.

This is the no-agent spine of V3 chunk curation. Phase 1B's
`SuggestTagNames` is a pure cosine-distance lookup against ED;
no model call, no agent in the loop.

## Record

ED — embedding of a tag's definition text.

- **Key:** `ED` + tagname + 8-byte big-endian fileid. Mirrors D
  exactly — same one-record-per-(tag, file) shape.
- **Value:** float32 vector (3072 bytes for nomic-768).

One ED per D. A tag with definitions in three files has three D
records and three ED records. The duplication is intentional:
different files may give the same tag different definitions, and
the curate UI shows *which* file's definition motivated each
candidate.

## What Gets Embedded

The description text alone — not the tag name.

- `@design-decision: choosing LMDB over SQLite for the index`
  embeds as `"choosing LMDB over SQLite for the index"`.

Tag names are mnemonics. They may or may not line up with what
the tag actually means, and including the name biases the vector
toward the name's lexical surface instead of the meaning. The
goal here is "what is this tag *about*" — that lives in the
description.

This is a deliberate departure from EV, which embeds
`"tagname: value"`. EV's name carries vocabulary cues that
disambiguate values across tags ("priority: high" vs "status:
high"). ED is queried in the opposite direction — chunk → tag —
so name-as-cue cuts against the goal.

## Lifecycle

ED follows D's lifecycle one-for-one. The Indexer writes D
records synchronously inside the transaction; ED writes
happen lazily on the next batch-embed pass, so embedding compute
never blocks the actor.

- **Create / re-index** (`Store.UpdateTagDefs`): replace D
  records for a fileid; in the same transaction, drop the
  fileid's old ED records. New ED records are queued implicitly
  by being missing — the next batch-embed pass picks them up.
- **Append** (`Store.AppendTagDefs`): write D records without
  removing existing ones. ED records for newly added (tag,
  fileid) pairs land in the next batch-embed pass.
- **Remove** (`Store.RemoveTagDefs`): delegates to
  `UpdateTagDefs(fileid, nil)`, which drops both D and ED for
  the fileid.
- **Rebuild** (`ark rebuild`): regenerates D records from
  scratch, same as T/F/V; ED records follow via the same drop +
  re-embed path used by EV.
- **Drop** (`Store.DropEmbeddings` / `ec_version` mismatch
  cousin): an ED-equivalent drop path exists so a model swap
  invalidates ED alongside T-name and EV vectors. The stored model
  filename in `[embedding] model` already gates this — ED uses the same
  `[embedding] model` as T and EV.

## Batch Embed

Reuses the existing post-reconcile batch-embed pass that writes
T-name and EV vectors. Three sources of missing embeddings now,
not two: tag names, tag values, tag definitions.

`Store.MissingTagDefEmbeddings()` returns the list of `(tag,
fileid)` pairs that have a D record but no ED record. The
Librarian embeds the raw description text (no tag name, no
hyphen-to-space rewrite — see "What Gets Embedded" above) in
tier-appropriate batches and writes ED records via
`Store.WriteTagDefEmbedding(tag, fileid, vec)`.

Detection is a pure DB scan: enumerate D records, enumerate ED
records, return the set difference. No transient queue, no
"this fileid changed" marker. This means the pass is
crash-safe and self-recovering:

- If ark exits mid-batch with N pairs unwritten, the next
  reconcile sees them in `MissingTagDefEmbeddings()` and
  finishes the work.
- If a corpus is indexed without `[embedding] model` configured, D
  records land but no ED records do. When the user later
  configures `[embedding] model` and restarts, the next reconcile pass
  picks up every missing pair from scratch.

The query-time path doesn't change for existing callers — this
is purely a write-side addition. Phase 1B adds the read API.

## Storage Scale

Order-of-magnitude estimate at current corpus size:

- ~270 tags with definitions (one per tag in `~/.ark/tags.md`),
  plus a handful of tag-defining files in the corpus.
- Each ED is 3072 bytes.
- ~300 ED records ≈ 900KB total. Comfortably below T+EV combined.

Rebuild cost grows linearly with tag-def count. Current numbers
are well within budget; revisit if tag-def count crosses ~10k.

## Drop and Rebuild

`Store.DropEmbeddings` already strips T-name vectors and deletes
all EV records on a model-mismatch event. ED is added to that
drop set. `ark rebuild` already drops embeddings as part of its
existing reset; ED is included automatically because it lives
behind the same drop API.

## What This Does Not Do

- Does not add a query API. Phase 1B's `SuggestTagNames` is a
  separate slice — this spec only covers writing ED records.
- Does not change V/F/T/D record shapes. ED is purely additive.
- Does not change the tag-def extraction syntax in indexed files.
  D records already exist; ED is one new vector per existing D.
- Does not introduce a separate `ed_version` schema marker. ED
  is gated by `[embedding] model` like T-name and EV vectors; a model
  swap drops all three together.
