# Chat embeddings: should the conversations have vectors?

Ark is a shared memory space for human-agent collaboration, and the
conversations are part of that memory: much of the reasoning behind a decision
is said in a chat and written down nowhere else. Recall and the bloodhound
search the chat logs for that reason. But they reach them through the trigram
legs only, because the chats have no embeddings, so the meaning leg is blind to
the part of the corpus that holds the most unwritten reasoning.

Nobody decided that, on this machine. The embedding pass skips any file matching
`search_exclude` (R1611), and `ark embed validate` counts those chunks as
excluded rather than missing (R1865). `search_exclude` lists the chat logs
because their JSONL chunks flood interactive search, so the chats went unembedded
as a side effect of a noise filter. Found 2026-10-24 while re-measuring
[bloodhound-fix.md](bloodhound-fix.md) Item 9.1. That carve's Item 14 splits the
setting's other two jobs (a fence around ark's system files, and the noise
filter). This carve owns the third.

The same conflation was first noticed 2026-07-27, when `ark status -db` showed
75,110 chunk embeddings against 134,729 chunks and the shortfall was almost
exactly the chat corpus. Bill proposed then a separate `embed_exclude` setting,
with both it and `search_exclude` defaulting to the chat and schedule globs.
That is now Item 1.

Opened 2026-10-24.

## Status

- [ ] **Item 1 — an embedding-exclusion knob.** **OPEN (#81.)** What is
      embedded, decided by its own setting rather than by `search_exclude`.
      `/mini-spec`. See Item 1.
- [ ] **Item 2 — measure the cost.** **OPEN (not queued.)** Index growth,
      process size, time to embed, the ongoing per-session cost. Needs Item 1.
      See Item 2.
- [ ] **Item 3 — measure the gain.** **OPEN (not queued.)** Does a meaning leg
      over the chats find what the trigram legs miss? See Item 3.
- [ ] **Item 4 — decide.** **OPEN (not queued.)** Bill's call, from Items 2
      and 3. See Item 4.

## What is known (2026-10-24)

Measured with `ark status --chunks` against the live index:

| | chunks |
|---|---|
| Under `~/.claude/projects/**` | 118,340 (115,037 `chat-jsonl`) |
| Whole index | 280,132 |
| Chats' share | 42% |

At 3072 bytes per chunk vector (768 float32s, `specs/record-formats.md`), the
chats come to about 364 MB of raw vector data. The whole index is 1.4 GB today.
Not measured: bbolt's overhead on top of the raw bytes, and the time to embed
118k chunks on the Deck. The chats also grow every session while most of the
corpus does not, so this is a rate as well as a size.

Probed the same day: a meaning-only search scoped to `~/.claude/projects/**`
returned nothing, while `-contains` scoped the same way found chat chunks. That
confirms the chats have no vectors in practice, not just in the code.

Two qualifications, so the sentence "the chats are not embedded" is not read too
broadly:

- **It is this machine's configuration, not the product's.** `search_exclude`
  defaults to `[]` (`specs/config.md`), so a fresh install embeds its chat logs
  today. The 42% is what one `ark.toml` avoids.
- **Recall embeds some chat text anyway.** Its chat funnel (`specs/recall.md`,
  `[recall].chat_funnel_gate`) embeds the top markdown sub-chunks of chat turns
  the trigram legs already matched, at recall time and transiently, on a path
  separate from the batch embedding pass. An embedding knob gates the batch pass
  and should leave the funnel alone, and the spec should say so.

**Bill's concern is process size, not disk** (2026-10-24). The index is
memory-mapped, so every megabyte added to it adds to the server's process size.
`ark serve` already peaks around 4.4 GB at startup and has been killed by earlyoom
when a browser held a lot of memory, so this is not hypothetical.

## Item 1

A setting that decides which files are embedded, separate from what interactive
search hides, replaceable in `ark.toml` and on the CLI like bloodhound-fix's
Item 14 settings. Its first purpose is to make Item 2 possible: the cost of
embedding the chats can only be measured by turning it on somewhere.

Bill's 2026-07-27 proposal names it `embed_exclude` and defaults it to the chat
and schedule globs, so the chats stay unembedded until Item 4 says otherwise.
Changing `search_exclude` must no longer change what is embedded, and R1611 and
R1865 are rewritten to read the new setting. Whether it is a list of exclusions
or of inclusions is the design.

To settle while designing it:

- **Migration.** An install that relied on `search_exclude` to keep its chats
  unembedded would start embedding them once the gate moves, unless the new
  default covers it. A default changing from `[]` also changes every install that
  never set the key. Decide whether the default applies to fresh configs only.
- **Which embeddings it gates.** Chunk embeddings for certain; file centroids
  follow, since an unembedded file has no chunks to average, but say so. Tag-name
  and tag-value embeddings are not per-file and stay out of it.
- **Search still reaches excluded files.** Neither setting removes anything from
  the index, and `--filter-files` still finds what `search_exclude` hides. Wherever
  the default is documented, say so, because "excluded" reads like "unsearchable".

## Item 2

With Item 1, embed the chats on an isolated copy of the index, never the live
`~/.ark`, and measure:

- index size before and after;
- the server's resident size at startup and once settled, before and after;
- the wall time to embed the backlog;
- the ongoing cost, as chunks embedded per day of normal use.

Options that change the cost, recorded so Item 4 can weigh them and not decided:

- **Truncated vectors for chats.** [embedding-review.md](embedding-review.md)
  records that nomic is Matryoshka-trained down to 64 dimensions. A 64-dimension
  chat vector is 256 bytes, against 3072. What that does to retrieval quality is
  Item 3's to measure.
- **Embed some chats.** Recent sessions only, or the current project's, or chunks
  above a size floor (the median chat chunk is 184 bytes, and many are tool
  noise).
- **Keep chat vectors elsewhere,** outside the mapped index, so they cost process
  size only while in use.

## Item 3

The cost only matters if the gain is real. Run hunts and ambient-recall fires
with and without chat vectors on the same isolated copy, and compare what each
surfaces from the chats. embedding-review's Item 1 (a retrieval yardstick) is the
natural instrument if it exists by then.

## Item 4

Bill's call: embed the chats, embed some of them, or leave them to trigram. The
answer becomes Item 1's default.

## Overlaps

- **[bloodhound-fix.md](bloodhound-fix.md) Item 14** splits `search_exclude`'s
  other two jobs. The two carves must agree on the settings' names and on which
  surfaces honor each.
- **[embedding-review.md](embedding-review.md)** owns which model embeds. A model
  change changes the per-vector size and the Matryoshka floor, so its answer moves
  Item 2's numbers.
