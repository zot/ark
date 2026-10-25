# Bloodhound fixes

The bloodhound is the most important feature ark has (Bill, 2026-10-22): a
session asks the corpus a question by writing a `<BLOODHOUND>` watermark in its
ordinary output, and a curated finding comes back. On 2026-10-22 a hunt emitted
in the middle of a turn was never served, and it took an hour to find out why.
Nothing reported a problem: the secretary waited, `listen` waited, and the log
said nothing. This carve gathers what that run exposed.

Opened by Bill, 2026-10-22.

## Status

- [x] ~~**Item 1 — a watermark must be in the message to the user.**~~ **LANDED (2026-10-22 — `#152`.)** Skill and doc edits. See Item 1.
- [ ] **Item 2 — paraphrased prose in the transcripts ark indexes.** **OPEN (not
      queued), NOT VERIFIED.** What recall and chat indexing see. See Item 2.
- [x] ~~**Item 3 — a multi-paragraph watermark came back as one hunt.**~~ **LANDED (2026-10-22 — `#157`.)** See Item 3.
- [x] ~~**Item 4 — a finding labels itself ambient recall.**~~ **LANDED (2026-10-22 — `#156`.)** See Item 4.
- [ ] **Item 5 — nothing shows whether a watermark was recognized.** **OPEN
      (not queued.)** Overlaps the observability carve. See Item 5.
- **Item 6 — delegation through the Luhmann pool.** Split into the defects
      reported so far. See Item 6.
  - [x] ~~**6.1 — pool-served hunts do not reach the emitting session.**~~ **LANDED (2026-10-22 — `#153`.)** Probably one defect with #106. See Item 6.
  - [x] ~~**6.2 — a stopped Luhmann session leaves its seat process running.**~~ **LANDED (2026-10-24 — `#163`.)**
        **OPEN (not queued), NOT VERIFIED** beyond HollowRpg's report. See Item 6.
  - [x] ~~**6.3 — a scoped hunt's seed is cut before its scope.**~~ **LANDED (2026-10-24 — `#159`.)**
        Measured 2026-10-24; not pool-specific, and not #107. All three legs cut inside the scope (the trigram leg through microfts2 70dbbcd). See Item 6.
  - [x] ~~**6.4 — the secretary loop quits while idle, and keepalives suggest
        `--tube`.**~~ **LANDED (2026-10-22 — `#155`.)** Seen 2026-10-20 and 2026-10-22. See
        Item 6.
- [x] ~~**Item 7 — sweep the trajectory for bloodhound stragglers.**~~ **LANDED (2026-10-22 — `#154`.)** No skill. See Item 7.
- [x] ~~**Item 8 — one reader for Claude Code transcripts.**~~ **LANDED (2026-10-23 — `#158`.)**
      `/mini-spec`. The vehicle for Item 2. See Item 8.
- **Item 9 — seeds.** What reaches the secretary before it searches. Both parts
      closed 2026-10-24. See Item 9.
  - [x] ~~**9.1 — three seed defects.**~~ **LANDED (2026-10-24 — `#107`.)** The first was not a
        defect (seeds must reach the chats); the other two are fixed (R3576,
        R3577). See Item 9.
  - [x] ~~**9.2 — a hunt's seeds include its own session.**~~ **LANDED (2026-10-24 — `#27`.)**
        Closed without a change: it did not reproduce, and a directed hunt may
        cite its own session by design. See Item 9.
- [x] ~~**Item 10 — re-index the chat sources.**~~ **LANDED (2026-10-23 — `#160`.)** So the
      harness chunks #158 stopped producing leave the index. See Item 10.
- [ ] **Item 11 — a chunker version stamp.** **OPEN (not queued.)** A file
      chunked under older rules is stale, so a rule change re-chunks itself.
      `/mini-spec`. See Item 11.
- [x] ~~**Item 12 — retrieval after a chunker rule change shows the wrong chunk.**~~ **LANDED (2026-10-24 — `#161`.)**
      Measured 2026-10-23. microfts2's half landed 2026-10-24 (`GetChunks` matches by
      range and reports a stale chunk `Missing`); #161 is ark's half. See Item 12.
- [x] ~~**Item 13 — a file added mid-session has no embeddings until the next
      reconcile.**~~ **LANDED (2026-10-25 — `#164`.)** As specified, but it
      blinds the meaning leg to new files. See Item 13.
- [x] ~~**Item 14 — `search_exclude` does two jobs.**~~ **LANDED (2026-10-25 — `#165`.)** A
      fence around ark's system files and a noise filter for interactive search,
      split into settings. It also decides what is embedded, which is
      [chat-embeddings.md](chat-embeddings.md). `/mini-spec`. See Item 14.
- [ ] **Item 15 — chasing a promising chat.** **OPEN (not queued).** The judgment
      belongs to the caller, not the Haiku secretary. See Item 15.

## Item 1

**What happened.** The watermark was written as visible text in the middle of
a turn, just before a tool call. The watcher scans the session's JSONL
transcript, and the watermark never reached it. The text was not dropped: it was
stored as a `thinking` block whose signature carries the label `narration`
(`bmFycmF0aW9u` in base64) and whose content is a paraphrase ("Both searches are
running now, digging into…"). The verbatim text, the `<BLOODHOUND>` tags
included, is in the transcript nowhere. Re-emitted as the turn's final message,
the same hunt was served within minutes.

**Why (Bill, 2026-10-22).** Claude Code summarizes thinking blocks, and a
mid-turn text can be recorded that way. Only the message to the user is kept
verbatim.

**Measured 2026-10-22** over `~/.claude/projects/*/*.jsonl`:

- Across the 40 most recently modified sessions, 1,313 narration blocks against
  2,342 verbatim text blocks, in 26 of the 40 sessions.
- By month, across every session: 1,327 narration blocks in September 2026, 961
  in October through the 22nd, and 120 on lines without a timestamp. None
  earlier, so it began in September or later.
- Of 14,078 turn endings, 9 ended in a narration block. The final message is
  the one position that is reliably verbatim, though not perfectly.

**It explains HollowRpg's two unrecognized watermarks.** HollowRpg reported on
2026-10-20 that two of its watermarks never got a hunt id. In that session's
transcript (checked 2026-10-22), every watermark that was served has a verbatim
text record (four of them, three in the turn's final message). The first
watermark has no verbatim record at all, only a narration block paraphrasing it
("my watermark can be served by the pool"). Around the later three-paragraph
watermark there is likewise no verbatim record, only narration. So both were
probably paraphrased away before the watcher could read them, and are not
evidence about the pool.

**The fix.** The bloodhound skill says a watermark goes in the message to the
user, at the end of the turn, never between tool calls, and says why. The same
rule goes wherever the watermark craft is taught (the `/ark` and `/luhmann`
skills point at it). The 9 exceptions mean the rule narrows the failure rather
than closing it, which is part of why Item 5 matters.

## Item 2

The watcher is not the only reader of these transcripts. Ambient recall builds
its seeds from the conversation, and ark indexes chat logs as a corpus
(`ark chats`, the chat chunker). Wherever they read assistant text, a third or
more of it may now be a paraphrase written by another model rather than what
was said. Unmeasured: which readers take `thinking` blocks at all, whether any
of them can tell a narration block from real thinking, and whether a
paraphrase is good enough for each reader's purpose. Done when each reader has
a recorded answer.

**The chunk is the canonical form, so the chunker's answer is the one that
matters most (Bill, 2026-10-22).** Chunk text is what the indexer sees and what a
person or agent sees in search results. Measured the same day, the `chat-jsonl`
chunker puts `thinking` text into chunk text, matching by key name rather than
block type, so paraphrased narration is indexed and shown as if it were what was
said. Deciding that is deciding what the corpus says, not a parsing detail; see
the Lingua Franca pattern.

The queue carried this as a note of Bill's before it was measured: thinking
blocks now hold summaries, so the chunker and the chat renderers (`ark chats`
and `/content`) need updating, and thinking blocks should be shown with a
thought-balloon emoji.

## Item 3

The three-paragraph watermark came back as a single `## Finding:` whose heading
joins all three paragraphs, and the secretary reported closing one hunt. The
bloodhound skill says each paragraph becomes its own hunt. Not yet checked:
whether the split happens and only the reporting joins them, or whether the
paragraphs really were served as one clue (which would blend their vectors,
the failure the skill warns against). Bill had flagged the same doubt in the queue
("verify paragraphs in bloodhound blocks") before this run confirmed it.

## Item 4

The finding document `listen` printed ends with a footer that calls itself
ambient recall and talks about `## Surface:` and `## Recommend:` items. It was
a directed hunt with neither. A reader following the footer would be told the
wrong thing about what it is holding.

## Item 5

A watermark the watcher never sees, one it sees but cannot route, and a hunt
that is merely slow all look the same from the emitting session: silence. The
skill promises that unservable work reports itself, and that promise does not
cover a watermark that was never recognized. There is no verb that answers
"did the watcher see my watermark, and where did it go?". The log has no line
for it either. HollowRpg reported the same blindness on 2026-10-20, for hunts
routed to the Luhmann pool. The observability carve's hunt-path testbed
([observability.md](observability.md) Item 5) is the instrument. This part asks that it cover
recognition, so that the first question in any lost hunt has an answer.

## Item 6

When a session has no secretary of its own, its hunts are meant to be served by
a secretary from the pool that the seated Luhmann supervises. HollowRpg's
session on 2026-10-20 is the fullest record of that path failing. With Item 1
now accounting for its two unrecognized watermarks, four defects remain.

**6.1.** HollowRpg's recognized hunts were never served by the pool. They
waited until the session spawned a secretary of its own, and nothing showed
they were waiting. #106, filed 2026-07-31, describes a fault that would produce
exactly that from the emitting session's side: a pool-served finding is
published on the CLI result tag, because the request's origin is empty where
its destination is chosen, so the session's `listen` never receives it (a
violation of R3281). The queue's own future-work note says the same thing in
fewer words ("pooled secretary seems to be returning a CLI result"). Probably
one defect. Confirm before fixing: reproduce with a seated Luhmann and no
session secretary, and see where the finding lands.

**6.2.** Stopping a Luhmann Claude session leaves its `ark luhmann next
--first` running. HollowRpg found three seat processes blocked at once, one
reparented to init. Whether a stale waiter can take work meant for the live
seat is not known.

*Read 2026-10-24, and the answer is yes.* `LuhmannNext` checked ownership once,
at the claim, then waited on the server's one shared queue with no further
check, so every blocked call was an equal candidate for the next item. An item
an orphan won went to an output file nobody reads, and that hunt's CLI timed
out. Three blocked at once is consistent with one session id resumed (a
newcomer under another id is refused while an orphan is in flight) or a
`--force`; either way they shared the queue. Claude Code does not stop a
backgrounded command when its session stops, so the orphan waits out its
keepalive. Fixed by #163 (R3578): a claim supersedes the call already draining
the seat, which stands down at once. An in-flight orphan under a different
session id still holds the lease and is not covered; `--force` is the remedy.

**6.3.** The three hunts HollowRpg's own secretary served had empty seed sets,
so it searched the scope by hand, although a plain `ark search` found a dozen
hits in that scope. #107 (seed quality: excluded paths reaching the seed set,
the seeder matching its own scaffolding) is about the seeds being wrong, not
absent. Whether this is the same fault or a new one is not known.

*Measured 2026-10-24, and the item was misnamed.* All four empty seeds that day
(not three, and from the session's own secretary, not the pool) carried a scope,
`~/work/HollowStuff/HollowRpg/published/**`; hunts with no scope seeded
normally.  Each of the seed's three search legs took a corpus-wide top 50 and
only then dropped out-of-scope chunks, which R3186 rejects one level
up. `published/` is symlinks whose targets are indexed under their own paths
with the same text, so those twins, older drafts and months of chat filled every
leg's cut. Reproduced through `POST /recall` with the same clue: 10 hits
unscoped, 2 of 10 scoped.  R3570 puts the scope inside each leg. The meaning leg
and the tag axis are fixed; the trigram leg passes the scope as `WithOnly`, but
microfts2's `SearchFuzzy` applied that after its own top-k (O228); microfts2
fixed it the same day, so all three legs now cut inside the scope. The same
reading found O229: the secretary's scope directive intersects several positive
globs where the seed unions them.

**6.4.** The secretary's loop gives up while idle. On 2026-10-20 one returned
after seven idle keepalives; on 2026-10-22 one returned after eleven (about
106 seconds each) and described its calls as having "returned immediately".
Both times the keepalive text suggested a `next --tube <session>` form, which
R3322 made unnecessary. The keepalive text and the loop instructions should
both say to keep waiting, and stop offering `--tube`.

**It also trips the crash counter.** Later on 2026-10-22 a pool secretary served
one hunt correctly and then stopped, having used about 25K tokens of its
context. Luhmann's exit check classified that as a crash and recorded it. Three
crashes in a row is a crash storm, which pauses the pool. So in ordinary use,
three secretaries that each serve a hunt and then quit would switch the pool
off. Either the loop stops quitting, or the exit check learns that a clean early
return is not a crash, or both. The first is the real fix; the second keeps the
pool alive meanwhile.

## Item 7

Bloodhound problems have accumulated in the queue: #106, #107, #27 (the
own-session seed filter), two future-work headings (one on paragraphs in
watermark blocks and pool results, one on thinking summaries in chat
rendering), and their planning notes. Same rule as the messaging carve
(DECIDED, Bill, 2026-10-22): once a straggler's content is fully in this carve,
it is removed from the trajectory file it came from, leaving a one-line note
that points here. Done when every hit has been folded in or closed and its
source replaced by the note.

## Item 8

DECIDED (Bill, 2026-10-22): ark parses Claude Code's JSONL transcripts in one
place. A census of ark's non-test Go code that day (a delegated read of every
match, not a sample) found **12 functions that decode transcript records, in 5
styles**: typed per-line structs (6), a named two-step `RawMessage` chain (2),
`map[string]any` navigation (2), hand-written byte scanning with no JSON decode
(2, one of them the `chat-jsonl` chunker), and a shared first-byte sniffer under
several of them.

Divergence is not hypothetical. `ark luhmann inspect-exit` read a top-level
`tool_result` and a top-level `usage`, neither of which Claude Code writes, so it
never saw a secretary close a hunt and called every bloodhound pool exit a crash
(fixed in place by #155, sharing the R2777 token reader). The readers also
disagree about `thinking` blocks: the chunker indexes their text as content, the
chat renderer shows them only on request, the watcher and recall injection drop
them, and no reader looks at the `narration` signature at all. That last fact is
Item 2's whole question, and answering it twelve times is how the answers drift.

DECIDED (Bill, 2026-10-22): the reader translates into **ark's own struct**,
which picks out only what ark needs, and nothing else in ark sees Claude Code's
JSON:

```
[transcript] -> [our struct] <- [our code]      not      [transcript] <- [our code]
```

So most of ark is immune to changes in Anthropic's format, and a format change
is a change to one section of parsing code. The struct carries ark's
vocabulary (role, origin, text, tool calls with their inputs, tool results,
thinking with its kind, usage, sidechain, cwd, timestamp), named for what ark
does with each, not after Claude Code's field names. Every current parser is
rebuilt on it. The chunker's byte scanner exists for speed, so measure before
replacing it; if it stays, it lives inside the same section and produces the
same struct, with a parity test against the full decode over the real corpus.
It is never a second reader of the format somewhere else. The census
noted one read it could not verify: `isError` and `error` taken from the top
level of a record.

## Item 9

**The secretary only adds; it cannot recover what was never put in front of
it.** A hunt starts from a seed set built by mechanical search (the watcher's
"deluxe combined search", four substrates). Folded in 2026-10-22 from the queue
and its planning notes; the measurements are from 2026-07-31 unless dated.

**9.1, three defects (queue item #107).**

- *Search-excluded paths reach the seed set.* **Not a defect.** One hunt's 24
  seeds held 8 chunks from `~/.claude/projects/**`, which `search_exclude`
  covers, while `ark search` returned none from there. Re-measured 2026-10-24:
  three unrelated clues through `ark connections recall`, the same
  `Librarian.Recall` an unscoped seed runs, each returned 2 chat chunks in 12-14,
  and `ark search` on the same text returned 0. No substrate leaks: Recall never
  reads `search_exclude`, and that is right. DECIDED (Bill, 2026-10-24): the
  bloodhound and recall must search the chats, and `search_exclude` keeps them
  out of interactive search as noise, not as a boundary. The setting does a second
  job that Recall does need to honor, which is Item 14.
- *The seeder retrieves its own scaffolding.* A seed block opened with two lines
  of `recall_agent_builder.go` at similarity 1.00: the code that builds the
  prompt, matched by the prompt's own boilerplate. The same self-match surfaced
  in a *finding* on 2026-08-03, which cited the hunting session's own transcript
  from minutes earlier as "a prior discussion"; that half belongs with 9.2.
  Re-measured 2026-10-24: a clue alone seeds no scaffolding (three clues, none;
  a boilerplate clue as positive control ranked `recall_agent_builder.go`
  first). The live cause is metadata *after* the clue: `clueOf` stripped
  `depth:`/`want:` lines only at the head, and a watermark is prose, so a
  trailing metadata paragraph became its own seed input and returned the
  bloodhound skill and the crank handle's own documents. Fixed by #107 (R3576):
  metadata lines are dropped wherever they sit.
- *JSONL is deprioritized by file extension, not by chunker strategy* (latent).
  The librarian ranks any `.jsonl` last by suffix, while the watcher's trigger
  path asks `strategy == "chat-jsonl"`: two definitions of "chat log" in one
  subsystem. Inert while every indexed `.jsonl` is a chat file (Bill checked).
  Bill's fix: keep the cheap suffix test as a gate, then check the strategy. It
  changes intent, so it wants a requirement rather than a bug-fix edit. Fixed by
  #107 (R3577), reading the indexed strategy (`DB.FileStrategy`) instead of
  `Config.StrategyForPath`, since that is the test recall already uses
  (Bill, 2026-10-24).

Not a defect, recorded so it is not chased as one: even with nothing leaking,
that hunt's seeds held none of the research asked for, because the corpus
restates the same claim in 27 markdown files and 11 PDFs, and near-duplicates
blanket the neighbourhood. Whether to exclude those files is a configuration
decision for Bill.

**9.2, the hunt's own session (queue item #27).** A clue that brushes a common
tag name can seed from the requester's own session (observed 2026-07-03), and a
finding has cited the hunting session's own words back as evidence (2026-08-03).
The chat half is already servable per hunt with
`exclude-files="~/.claude/projects/**"` (#48). What remains is the own-session
filter itself, and whether that exclusion should be a default rather than per
hunt. Related: 6.3's pool hunts arrived with *empty* seed sets, which is the
opposite failure and may share a cause.

Re-measured 2026-10-24: a CLI hunt's seed runs the same `Librarian.Recall` as
`ark connections recall`, so four probes went through that with the requesting
session's own words (three phrases and one paragraph verbatim, each confirmed
indexed by `ark search -contains`). None returned a chunk from the requesting
session; chat chunks from other sessions did come back. The 07-03 case, a clue
brushing a common tag name, was not reproduced. The 08-03 finding was the
secretary's choice of what to cite, not a seed it could not avoid.

DECIDED (Bill, 2026-10-24): close 9.2. The code already takes a position, and it
stands: `FindingItem` has no own-session gate, because a directed search may
point at the requester's own session. That is unlike ambient recall, which never
shows a session its own words (R2869, R2872). The difference is deliberate:
after compaction, the earlier part of a session is exactly what the requester no
longer holds, so "we decided this hours ago, here" is a real finding. Excluding
the chats by default was answered the same day (Item 9.1: the bloodhound must
search them).

Held in reserve, should self-seeding reappear: exclude only the part of the
session still in the requester's context (the watermark's own turn and the
recent turns, as ambient recall treats its own input), and keep the compacted
past findable. It needs a definition of "still in context", and the watcher
already tracks a recent-turn window.

## Item 10

#158 stopped indexing attachments and API request copies (R3567), but an index
built before it still holds them: about 24,400 chunks of system reminders and
skill listings, competing with conversation in every search. Appends only chunk
new lines, so they leave only when each chat file is chunked again.

A full `ark rebuild` would do it, and would take a very long time because every
chunk is embedded again (Bill, 2026-10-22). Bill's approach instead: `ark remove`
the chat files (`~/.claude/projects/**/*`) and let the next scan, which still
covers them as a source, add them back through the new chunker.

To settle before running it, each by checking rather than assuming:

- Does the next scan re-add a removed file that its source still covers, or does
  removal mark it excluded?
- Are the re-added chunks' embeddings reused by content (embed dedup) or
  computed again? Conversation text is unchanged, so reuse would make this cheap.
- What else is keyed to chat chunks or files: `@ext` tags and routings,
  curation and recall records, discussed tags, pinned chunks. Removing a file may
  drop them.
- The live session's own transcript is being appended to while this runs.

**What the code says (read 2026-10-23, not yet run).**

- *Re-adding works.* `ark remove` records nothing, no exclusion or tombstone,
  and the scan and watcher add any file their include globs cover that is not
  indexed, so a removed transcript comes back (an active one on its next write).
- *But it would re-embed, and it strands references.* Chunk ids are sequential,
  not content hashes. Removing a file deletes the embedding of every chunk no
  other file shares, and re-adding allocates new ids with no embedding.
  (Correction, 2026-10-23, after the run: on this index chat chunks have never
  been embedded, because `search_exclude` covers the chat directory and also
  gates embedding, PENDING #81; none of the log's 353 embed-queue lines is for a
  chat file. So re-embedding was not a real cost here. The stranded references
  below were, and they decided it.) Records that point at a transcript chunk
  survive its removal under the old id and are not re-attached: `@ext` routings
  (X records and routed tag values), recall candidates and judgments (RC, RJ),
  surface cooldowns (RM), and curation pins until the next start.
  `specs/at-ext-storage.md` says a removed target leaves its routings
  unresolved; the code does not do that. That gap applies to any file removal,
  not only this one.
- *A refresh avoids both.* microfts2's reindex adds the new version before it
  drops the old one (`reindexPrepared`), so an unchanged chunk's content hash
  finds its existing id and keeps it, with its embedding and everything that
  points at it. Only the chunks the new rules no longer produce, the harness
  ones, become orphans.
- *Nothing can force one today.* `ark refresh` touches only stale files, and
  staleness falls back to the content hash (`classifyFile`), so touching a file
  does not make it stale.

DECIDED (Bill, 2026-10-23): do not use remove and re-add. Re-chunk through the
refresh path, forced with a new `ark refresh --force [PATTERN...]` (option A),
which re-indexes matching files whether or not they are stale. The general
answer, a chunker version stamp, is Item 11.

**Result (run 2026-10-23 18:35).** `ark refresh --force '/home/deck/.claude/projects/**'`
queued 3,220 files in 3 seconds and re-chunked them off the DB actor; the server
answered throughout (an `ark status` took 17 s early on, `ark chunks` under a
second). The index went from 300,683 chunks to 281,918, 18,765 harness chunks
fewer. Verified by asking for ranges only the old rules produced: on the probe
file, 10-10 (an attachment) now reports not found and 39-39 returns its own
text; on eight sampled files where the old and new rules differ, the old-only
range is gone in all eight. Measuring this went wrong first, twice: counting a
file's chunks through `ark chunks` reflects the running chunker, not the index
(Item 12); and a search for attachment text found nothing while those chunks
were still indexed, probably because search also reads chunk text through the
positional re-chunk (not verified).

## Item 11

A chunker's rules are the canonical form: change them and every file they
chunked holds the old form until it is chunked again. #158 changed the chat
chunker's rules and nothing noticed, which is why Item 10 needed a forced
refresh. The general fix: each chunker declares a version, each file records the
version it was chunked with, and a file chunked under an older version counts as
stale, so the ordinary refresh re-chunks it through the reindex path, keeping
every unchanged chunk's id and embedding. Costs a field in the file record and a
migration. Filed by Bill, 2026-10-23, after choosing the forced refresh for now.

It is also the other half of #161 (Bill, 2026-10-24). A stale chunk should be
treated as missing in everything associated with it, but ark can tell a chunk
is stale only by re-chunking its file. #161 covers every path that already
re-chunks: display, anchors, the locator, verse references, statistics, the
embedding pass, and search and recall hits, which are dropped. What only the
index holds is left: the stale chunk's tag values, its vector (possibly a
neighbor's text, from the old positional `GetChunks`), its trigram postings,
and its recall, cooldown and pin records. These can still affect ranking. This
item removes them at the source, by re-chunking the file, instead of filtering
them at every reader.

## Item 12

microfts2's `GetChunks` (R530, R655), which serves `ark chunks` and the text
behind search, recall and bloodhound results, takes the file's chunk list from
the index but each chunk's range and content from re-running the current
chunker, matched to the index **by position**. When a chunker's rules change,
every position after the first difference shifts. Measured 2026-10-23 on a chat
transcript last indexed before #158 (574 chunks in the index, 213 under the new
rules): asking for the indexed range 10-10 (an attachment) returned a chunk
labelled 38-38 with another line's text, and ranges 1001-1001 and 1008-1008
returned nothing at all, silently, because they lie past the new rules' 213th
chunk.

So until a file is re-chunked, its search hits and recollections can show text
from the wrong line, or none. Re-chunking the chat sources (Item 10) repairs the
data; Item 11 would have prevented it. The defect itself: retrieval should match
by range (or locator) rather than by position, so a mismatch is an honest
"not found" instead of a wrong answer. That fix is microfts2's.

The measurement that exposed it also misled first: counting a file's chunks
through `ark chunks` returns the new chunker's count however many the index
holds, so "the index already matches the new chunker" was the instrument
reflecting the running code. Asking for a range only the old rules produced is
the reliable probe.

*microfts2 fixed it 2026-10-24* (its `fa68efb`): retrieval pairs by range, and a
chunk the current chunker does not produce comes back flagged `Missing` with
empty content, so a stale file now shows as misses rather than wrong text. Ark's
callers have to honor the flag (#161). Reading them found a consequence of the
old behavior: the embedding pass indexes a file's chunks by position, and the
chunk cache used to skip stale entries silently, so after the first stale chunk
every later one was embedded with a neighbor's text. Today a Missing entry would
instead be marked as an all-tag chunk with no vector.

## Item 13

Chunk embeddings are computed only after a reconcile (specs/chunk-embeddings.md,
"Batch Embedding Pipeline"), which runs at startup and after a config change. A
file the watcher indexes in between is searchable by trigram at once but has no
vectors until the next restart. Measured 2026-10-24 in the server log: every hour
with chunk-embed queueing matches a startup reconcile, none falls between them,
and HollowRpg's `published/` books, created at 18:42 on 2026-10-20, were queued
for embedding at 09:09 the next morning. For those hours the meaning leg of every
hunt, and of ambient recall, could not see them. Whether new files should be
embedded as they arrive, and how that interacts with the write actor and the GPU,
is a design question; nothing here is a defect against the current spec.

The gap is wider than new files. The 2026-10-24 23:40 pass also queued chunks of
files *edited* that day (`specs/cli-commands.md`: 2 new of 220), so the meaning
leg trails whatever is being worked on, which is usually what a hunt is about.
A full pass costs about 13 seconds, about 11 of them checking all 280k chunks for
a missing vector, to embed a few dozen.

DECIDED (Bill, 2026-10-24): embed stored files with a pass over just those
files, once the set has been quiet for 15 seconds, and no later than one minute
after its first file, so one change every ten seconds cannot delay it
indefinitely. #164 (R3579, R3580): a file joins the set when its scan's writes
land, so its chunks exist; `search_exclude` keeps the chat logs out; the limited
pass shares the one embed-pass guard, and a full pass that finds a pass running
now waits for it instead of being skipped.

## Item 14

`search_exclude` does two jobs (Bill, 2026-10-24). It **fences ark's system
files** (the live config lists `~/.ark/schedule/**`), which nothing should serve
as evidence, and it **filters noise** out of interactive search (the chat logs
under `~/.claude/projects/**`, whose JSONL chunks flood ordinary results). The
two want opposite treatment from recall and the bloodhound: the fence holds
everywhere, while the noise filter must not apply to them, since the chats are
part of the memory those features exist to reach. Recall today honors neither,
so the fence is open to it.

**Measured 2026-10-25: the fence has leaked, and still would.** The fenced
population is one indexed file, the scheduler's state file
`~/.ark/schedule/fb02b2eb95f31742.md`, nearly all `@ark-event-*` tag lines.
Every Claude Code transcript under `~/.claude/projects` (all projects, Luhmann's
included) was scanned for its path, and each mention classed by the tool call
that carried it. All but one were deliberate work (scheduling development, a
scheduling inventory, a log). The exception: on 2026-06-10 a recall secretary's
curation docs offered two of its chunks as candidates, both through the
tag-vector leg (`cell: main-tags`, `tag-vec=0.79`). The secretary rejected both,
so nothing reached a conversation. Chunk-text vectors cannot reach the file (it
is not embedded, and an all-tag chunk strips to nothing), but its tags are
embedded like any other file's. Live today, `ark connections recall --type text`
returns the file for three probe texts, as the top candidate for two. No
bloodhound hunt carried it. The instrument sees only what reached a transcript;
curation docs on disk are pruned (two remain), so a seed that used a fenced
chunk without printing its path would not show.

Where `search_exclude` is consulted today: interactive search (`search.go`,
R939-R942), tag-value path resolution (`Librarian.resolveTagValuePaths`), tag
pubsub subscriptions that give no file filters (`server.go`, R941/R942), and
the embedding passes (full pass, `AllChunkIDsPartitioned`, and #164's
`noteStoredFile`). Recall is not among them. Each of these is a surface the
split must assign to the fence, the noise filter, or both.

DECIDED (Bill, 2026-10-25): tag search may honor the noise filter, but **external
tags are never filtered out by it**, because an `@ext` routing was made on
purpose to preserve information and associations. Today the filter would drop
one: `SearchTagChunks` and `resolveTagValuePaths` test the *target* chunk's
path, so a routing onto a chat chunk (the likely first use, from recall's
`@ext-candidate` lines) would vanish from tag search while its mirror sits
outside the filter. Nothing is lost yet: measured the same day, the index holds
no `X`, `RC` or `RJ` records, the global mirror `~/.ark/external/` is empty,
and no source sets `ext_mirror`. Two consequences for the design. A tag value a
mirror routed survives the noise filter, though the fence still applies. And a
built-in fence must name ark's system directories one by one (`schedule/`,
not `~/.ark/**`), since the global mirror tree, the patterns and the skills
all live under `~/.ark`.

DECIDED (Bill, 2026-10-25): **the fence is built in, overridable in `ark.toml`.**
And **the noise filter keeps applying to tag pubsub subscriptions and to tag
search, but never filters out an ext tag.** A tag written inline in a chat is
almost always a mention (talking about `@status:`, quoting a watermark), and
the risk of use-mention confusion is too high to let chat tags through
subscriptions or search by default. A chat chunk's association matters only
once a mirror routes it with `@ext`, which makes it a use. So the test is the
tag value's provenance (did it arrive through a routing?), not the target
chunk's path.

DECIDED (Bill, 2026-10-25): the fence's key is **`system_exclude`**, joining
`search_exclude` and #81's `embed_exclude`. ~~An explicit `-files` filter naming a
fenced path breaches the fence **for CLI search only**, as R940 does for
`search_exclude`; recall, the bloodhound and subscriptions never breach it.~~
**Superseded the same day:** the secretary's own searches are CLI `ark search
-files` calls, so that rule let a hunt scoped into the fence leak there.
DECIDED (Bill, 2026-10-25): only an explicit `ark search --system` lifts the
fence; no file filter ever does (flag name provisional). And
**a list in `ark.toml` replaces the built-in default, never amends it**, for
both keys, so `search_exclude` gains a built-in default too. Today it has none
(`[]`, `specs/config.md`): commenting the line out on 2026-10-25, expecting a
code default, let the embedding pass start on the chat logs within seconds
(10:18:55 to 10:19:21, until the line was restored). The defaults: `search_exclude`
covers the chat logs (`~/.claude/projects/**`), `system_exclude` covers
`<ark home>/schedule/**` only. Not `chimes.md`, though ark maintains it too: a
fired chime carries its source path, so fencing that file would silence every
chime subscriber. The scheduler reads its logs directly and is untouched by the fence.

**Open, not decided (raised 2026-10-25):** recall's tag axis pulls chunks
through `TagValueChunks`, which unions inline and ext-routed tag values, so a
chat turn can surface in recall because it *mentions* a tag inline. That is the
use-mention confusion the noise-filter decision keeps out of search and
subscriptions, but recall is not noise-filtered (the chats are its memory).
Whether recall's tag axis should count only routed tag values for chat chunks is
Bill's call, and outside #165 as specified.

DECIDED (Bill, 2026-10-24): split it into two settings, each replaceable in
`ark.toml` and on the CLI. The names, the defaults (whether ark's own system
paths are built in or listed), what becomes of the existing key, and which
surfaces honor each are the design. `specs/config.md`, `specs/search-filtering.md` (R938-
R943) and `specs/cli-commands.md` follow.

**A third job: what gets embedded.** The embedding pass skips files matching
`search_exclude` too, so the chat logs have no vectors and recall reaches them
through the trigram legs only. That question, and the knob that separates it from
the other two, moved to [chat-embeddings.md](chat-embeddings.md) (2026-10-24).
The settings split here must leave that knob's behaviour to it: changing what
interactive search hides must not silently change what is embedded.

## Item 15

A hunt's seed often holds a chat chunk that looks like the thread being chased,
and the place to follow it is that session's neighbourhood, not a fresh corpus
search. Today the secretary runs its chat pass only when the hunt is
chat-scoped or the clue says "did we discuss". Widening that trigger to "a chat
in the seed looks promising" is a judgment the secretary cannot be trusted
with: every secretary is Haiku, pool secretaries included, and Bill expects one
told to peek at chats would wade through reams of them (2026-10-24). So the chase
belongs to the caller, the Opus or Fable assistant that reads the finding.
Proposed, not decided: leave the crank handle's chat trigger as it is, make a
seed or finding's chat hits recognizable as leads, and teach the caller in the
bloodhound skill when to open one. Skill and builder edits; scope not settled.

## Overlaps

- **[observability.md](observability.md) Item 5**, the hunt-path testbed, is
  the instrument for Items 5 and 6: seat holders, hunts by status, queued hunts
  with what they wait for, listeners, secretaries. It stays there, because it
  is the testbed for the whole state document. This carve owns the defects it
  will show.
- **[orchestrator-scaling.md](orchestrator-scaling.md)** owns capacity: a second
  Luhmann seat, `pool_max`, bursty load. This carve owns correctness: a hunt
  served at any scale must reach the session that asked.
- **[messaging-fixes.md](messaging-fixes.md)** has the queue heading about
  delegating messaging to Luhmann. That is a messaging feature and stays there.
