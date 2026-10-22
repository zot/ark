---
name: bloodhound
description: "Turn on the warm search bloodhound for this session — subscribe to findings, and optionally spawn a per-session secretary — and the craft of directing a hunt: the <BLOODHOUND>…</BLOODHOUND> watermark, its file-scope attributes, the want-words, and how to read a finding. Load when the user types /bloodhound, asks to turn on directed search, or wants to emit a watermark; the watermark is inert without this skill's subscription. CLI searching you run yourself is /ark-search. Requires /ark."
---
<!-- CRC: crc-RecallAgent.md, crc-RecallAgentBuilder.md | R2890, R2950, R3279, R3296, R3303, R3312 -->

# Turn on the bloodhound (directed search — level 3)

**First, invoke `/ark`** (which loads `/ark-search` — the CLI craft: how to run
`ark search` / `ark chunks` / `ark fetch` yourself). This skill is the *warm*
path alongside it: with the machinery below running, you direct a search by
emitting a `<BLOODHOUND>…</BLOODHOUND>` watermark in your normal output, and a
curated **finding** comes back through your `listen`. (Ambient recall — the
corpus surfacing material *unasked* — is the next level up: `/recall`, which
builds on this.)

**The two skills split by what a move depends on.** The watermark and its craft
live *here*, because they are inert without the secretary and `listen` this
skill sets up. Anything you type at the CLI lives in `/ark-search`, because it
works in any session with no machinery at all.

Two roles: something that **runs** the hunt, and **you**, who consume its
findings. Only the second is yours to set up. Subscribing (step 1) is the whole
precondition — ark recognizes a watermark exactly while you hold
`@ark-bloodhound-result`, and *who serves it* is a separate decision ark makes
for you.

**So the per-session secretary is an optimization, not a requirement — at either
level.** Spawn one and it serves your hunts *and* your recollections, dedicated
and already warm. Skip it and the work falls through to a Luhmann-hosted pool,
if an orchestrator is seated. What that costs is scheduling latency and sharing
a secretary with other sessions. What it does **not** cost is the tag
recommends: a hunt served by the pool on your behalf proposes connecting tags
exactly as your own secretary would.

**The two levels borrow from separate pools**, `bloodhound` for hunts and
`recall` for recollections, so a busy ambient stream can never eat the
secretary your watermark needs, and vice versa. You never choose between them;
the watcher routes each piece of work to the right class and stands a secretary
up when one is needed.

If nothing can serve your work, you get a doc that **says so** rather than
silence: a `## Finding:` for a hunt, a `## Unservable:` for a recollection. That
includes a hunt accepted and then never run because no secretary became
available. So work that produced nothing is work that reported nothing; you
never have to guess whether something is still in flight.

One distinction worth holding, because it is the difference between waiting and
acting: **unservable is not the same as slow.** A pool that is merely busy will
get to you, and says nothing in the meantime. A report means nothing is coming —
usually because no Luhmann is seated — and the remedy is yours: spawn your own
secretary (step 2), or tell the user the orchestrator is down.

**The routing, so you can tell which remedy applies.** A watermark goes to your
own secretary if you have one; else to the seated Luhmann's pool; else it errors.
Your own secretary is therefore sufficient on its own — an unseated Luhmann does
not block a hunt you can serve yourself, which is exactly when step 2 stops being
an optimization.

Do **not** reach for `ark bloodhound search` as the fallback here — neither form
helps. Both route through Luhmann's secretary pool, so both need the seat and
both fail on the same missing orchestrator that just failed your watermark.

The flag only decides **who curates**. Without `--raw`, Luhmann curates the pool's
findings and returns those. With `--raw`, you get the secretary's own findings
back and curate in your own context, which is what tools and integrations built
on ark consume. Neither is an escape hatch from a missing seat. If a "Not served"
message suggests `--raw` to you, the message is wrong; spawn the secretary.

## 1. Subscribe — the consumer loop

Start the consumer, backgrounded:
```
~/.ark/ark bloodhound listen
```
Run it with `run_in_background: true` so you stay conversational. It subscribes to
`@ark-bloodhound-result` (the bloodhound opt-in) and blocks until a result
arrives, then completes and the harness notifies you. At this level the results
are **`## Finding:`** items — a curated answer to a `<BLOODHOUND>` you emitted,
headed by your own clue echoed back. **Fold a finding into your reasoning** (you
asked for it); surface it to the user only if it helps them. Then **relaunch**
`listen` to keep the loop going.

(`ark connections recall listen` is the older spelling of the same verb and
still works. Prefer `ark bloodhound listen`: one implementation serves both, so
they cannot drift, but the name now sits where the concept does.)

This subscription is what makes a watermark mean anything. Without it a
`<BLOODHOUND>` is ordinary prose — nothing recognizes it, and nothing reports
that nothing did.

## 2. Optionally, spawn your own secretary

Worth doing in two cases: **no Luhmann is seated**, so there is no pool to
borrow from and nothing else can serve you; or you expect heavy iterative
search and would rather not share. Skip it otherwise — a one-off hunt, or an
ordinary session with an orchestrator up, is served perfectly well by the pool.

**You do not need your session id** (R3322, 2026-07-30). The secretary's tube is
derived server-side from what the call already carries — `client_session` plus
the nonce, against the server's own nonce→class and nonce→parent records — so
there is no address to type and none to go stale under a re-host. `--tube`
survives only as an override.

Reserve a nonce:
```
~/.ark/ark connections recall reserve-nonce
```
Then launch it with the **Task tool**, `run_in_background: true`:
- `subagent_type`: `ark-recall-agent`
- `description`: `ark-recall secretary loop nonce <N>` — the `nonce <N>`
  substring is how the server finds the subagent's transcript; it must be present.
- `prompt`: `Start the recall secretary loop now. Nonce: <N>.` — no session
  id: the secretary's `next` resolves its own tube server-side, so there is no
  address to type and none to go stale.

The secretary loops internally (`recall next <N>`), draining
its `@ark-secretary-work` tube — search tasks now, curation docs too once ambient
is on — until its context fills, then it exits and the harness notifies you
(step 3).

**Ambient recall does not require one either.** `/recall` builds on this skill,
and its gate is its own subscription — nothing about it needs a secretary of
yours. Spawning one still helps for the reasons above; it is never the thing
that turns ambient on.

## 3. Respawn the secretary when it exits

When the secretary subagent **completes** (the harness notifies you), it hit its
context limit — normal and expected, not a failure. Reserve a fresh nonce and
spawn it again exactly as in step 2. That is the whole of supervision: no streak
machine, no backoff. If it fails *repeatedly* in quick succession (not a clean
context-limit exit), stop respawning and tell the user something is wrong.

## Directing a hunt — the `<BLOODHOUND>` watermark

This is what the machinery above is *for*. With the secretary running and
`listen` draining, you delegate a search by **emitting a watermark** in your
normal output — no spawn, no tool call, because the recall watcher already scans the
conversation. The watermark *is* the hand-off:

```
<BLOODHOUND>investigate how recall dedup works across specs and code — want a synthesis, stop when you can name the dedup key</BLOODHOUND>
```

You state the clue in **natural prose**; the canonical *want-words* (below) are
the reliable anchor the hound keys on. It fills in the CLI craft from its crank
handle. **The watermark displays to the user by design** — they get to follow
the hunt (and judge whether you're on the trail). Write it readable.

**Put the watermark in your message to the user — the last text of your turn,
never between tool calls.** The watcher reads the session transcript, and the
transcript is not always what you wrote. Text you emit mid-turn, before a tool
call, may be stored only as a paraphrase in a summarized thinking block
(signed `narration`), and a paraphrase carries no `<BLOODHOUND>` tags. The
watcher then has nothing to find, and nothing reports it: no hunt id, no
finding, no "not served". Measured 2026-10-22, about a third of recent
assistant text was stored that way, while the final message of a turn was
verbatim in all but 9 of 14,078 turns. A lost hunt you can't explain is most
likely this, so check where you emitted it before suspecting the machinery.

**Async by default** — end your turn with the watermark; the finding arrives
through `listen` and surfaces in a later turn, like recall. There is no way to
emit mid-turn and block on the result in the same turn, so for "need it now",
say so in the message, end the turn, and pick the finding up when `listen`
completes.

### Three layers, and only the last one has judgment

Knowing the shape tells you where your effort lands:

1. **A paragraph-based vector search** (the watcher) builds the seed set.
   Mechanical similarity — no judgment, no idea what you meant.
2. **A weak model** (the secretary) reads those seeds, widens, and curates. It
   **only adds**, so a miss at layer 1 survives unless layer 2 happens to search
   into it anyway. Layer 1 bounds the whole result from below.
3. **You.** The only layer with judgment. What comes back is a draft.

Everything below is layer-1 craft, because layer 1 is the part you steer.

**One ask per paragraph — separate ideas with blank lines.** Layer 1 embeds
*paragraphs*. Three questions packed into one paragraph become a single blended
vector sitting near none of them, and the seeds come back vaguely about your
topic and specifically about nothing. Split them and each gets its own seed.

**Those paragraphs ride in one watermark, not three.** A single block carries as
many paragraphs as you like, and it is **one hunt**: the seed search runs once
per paragraph and unions the hits, so each idea gets its own vector, then one
secretary works the whole clue and returns one `## Finding:` that answers every
ask. Splitting them across three blocks costs three of everything and buys
nothing — if you catch yourself emitting a second watermark in the same breath
as the first, they were always one block:

```
<BLOODHOUND>
where did we settle the chunker interface? pointers

did we ever benchmark trigram against vector recall on the same corpus? verdict

what did we decide about the GPL boundary in vendored code? answer with the source
</BLOODHOUND>
```

Note that `want` is per-paragraph here: the secretary answers each ask in the
shape its paragraph asked for, within the one finding. The tag attributes (`filter-files`, `exclude-files`, `notags`) are not — they sit
on the opening tag and scope every paragraph in the block alike. When one ask
needs a different file scope than the others, that one earns its own watermark.

The CLI half works the same way: `ark bloodhound search --file -` with a heredoc
takes the identical multi-paragraph clue, as one hunt seeded per paragraph.

**Scope when your own corpus restates the topic.** The general advice below is to
scope sparingly, and it holds. But a claim you have written up many times —
marketing copy, resumes, drafts, pitches — exists as many near-duplicate
restatements, and near-duplicates are worse than exact ones: dedup cannot catch
them, and each variant sits at a slightly different point, so together they
blanket the neighbourhood around the claim rather than competing for one slot.
When you know that is true of a topic, `filter-files` to where the evidence
lives.

**Ask in the vocabulary of the evidence, not of the claim.** Similarity ranks by
resemblance to your *question*, so asking in the claim's own words returns
whatever states the claim most cleanly — usually the advocacy, not the research.
The summary outranks the source. Ask instead for what only the evidence contains:
effect sizes, cited studies, the control condition, the numbers.

### The four fields you supply (the rest is the hound's craft)

| field     | what it carries                                                        |
|-----------|------------------------------------------------------------------------|
| **clue**  | what to find — the question, or the distinctive terms                  |
| **scope** | corpus: `code` / `specs` / `notes` / `chat` / `all` (prose; **file globs are attributes** — below) |
| **depth** | `lookup` (one pass) or `investigate` (refine-and-narrow loop)          |
| **want**  | the return *shape* (table below)                                       |

For `depth: investigate`, add an optional **stop** condition — "stop when you
can name the dedup key" — so the hound knows when to quit refining.

### Scoping by file — the one part that isn't prose

Everything else in a watermark is natural prose. **File scope is not**: it rides
as *attributes on the opening tag*. That is what keeps globs out of the clue,
where they would be searched as though they were ideas.

```
<BLOODHOUND filter-files="~/work/ark/**" exclude-files="~/.claude/projects/**">
where did we settle the chunker interface? pointers
</BLOODHOUND>
```

Both attributes **repeat** — several includes, several excludes, in any order,
and either may sit beside `notags`. They mean exactly what `ark search -files`
means, and the hound's own widening searches receive the identical string, so
both halves of a hunt are scoped the same way.

Unanchored globs are relative to **your current project**; a leading `/` escapes
it:

| glob | means |
|---|---|
| `**/*.go` | Go files at any depth **in this project** |
| `/**/*.go` | any indexed Go file, **anywhere** in the corpus |
| `*.go` | this project's **top-level** Go files only — no depth |
| `~/work/ark/**` | one named tree, wherever you happen to be working |

The third row is the trap: a bare `*.go` is joined to the project directory
*before* matching, so it never becomes the match-any-basename pattern it
resembles. Depth always needs an explicit `**/`.

**Reach for scope sparingly.** The corpus answering from somewhere you didn't
expect is the *feature* — the small talk is often big. Narrow when you genuinely
know where the answer lives (a source tree, a spec directory), or when chat-log
chunks are drowning a hunt (`exclude-files="~/.claude/projects/**"`). Otherwise
leave it off; the unscoped hunt is still the norm.

Two properties worth trusting: a glob matching **no indexed file** says so in the
finding instead of returning an empty hunt, so a typo reads as a typo and not as
"we never discussed it"; and scope narrows **before** ranking, so a scoped hunt
returns the best hits *within* the scope rather than whatever survived a
corpus-wide top-K.

### `want` — the return shape (the tightest coupling)

`want` is orthogonal to `depth`: `want` is the output *shape*, `depth`+`stop`
is *how hard to look*. Five values span "give me a conclusion" → "give me
everything":

| `want`        | you want…                    | the hound returns                                |
|---------------|------------------------------|--------------------------------------------------|
| **answer**    | a conclusion                 | 1–3 synthesized sentences + cited sources        |
| **passages**  | material to read yourself    | the few curated chunks + sources (no synthesis)  |
| **pointers**  | just where to look           | paths / chunk locators only (no chunk text)      |
| **inventory** | the complete set             | every match, de-duped (paths or @tags), no read  |
| **verdict**   | yes/no                       | "yes" + the one best source, or "no, not in …"   |

Each value is one emit-branch the hound's crank handle teaches — the words on
both sides match exactly. Phrase naturally, but land on a want-word.

**Legible watermarks** (natural phrasing carries the fields; the want-word is
the anchor):

- `<BLOODHOUND>did we ever discuss BM25 for recall? check the chat history — yes/no with the turn</BLOODHOUND>`  *(verdict · chat)*
- `<BLOODHOUND>where's the tag-strip-at-embed logic? just point me at the file</BLOODHOUND>`  *(pointers · lookup)*
- `<BLOODHOUND>list every spec that mentions @ext routing — the complete set</BLOODHOUND>`  *(inventory · tag lens)*
- `<BLOODHOUND>show me the passages on how the curation workshop stages ops, across the specs</BLOODHOUND>`  *(passages · specs)*
- `<BLOODHOUND>what did we decide about the seehuhn GPL boundary? answer with the source</BLOODHOUND>`  *(answer · notes / cross-project)*

Each is written on one line to isolate one want-word. Don't read that as the
shape of a real hunt — when you have several asks at once, they belong in one
block separated by blank lines, each with its own want-word, as above.

### Reading the return

A **curated digest** — the few passages that answer, with sources — not a raw
dump. Trust the curation. If it comes back thin, hand back a *sharper clue* or
*wider scope* in a fresh watermark; don't re-run raw searches yourself. That's
the whole point of the hound.

**Telling a thin hunt from a thin corpus.** These look identical and mean
opposite things — one says re-run, the other says stop. The reliable check is
whether the return contains **the places you named**. If you asked about a
project, a directory, or a class of document and nothing from it came back, the
*query* is the problem: sharpen and re-run before believing anything about what
exists. A hunt that names a place and returns nothing from that place is thin by
construction, however plausible its contents look. Reporting "we have little on
X" from such a hunt is a claim about your watermark, not about the corpus.

**Chasing a finding yourself is a different skill.** Once a finding lands and
you want to open what it pointed at, widen around it, or run a known-item
lookup of your own, that is the ark **CLI** — `ark search`, `ark chunks`,
`ark fetch` — and its craft lives in **`/ark-search`**. The boundary is simple:
the watermark is this skill, because it needs this skill's machinery; anything
you type at the CLI works in any session and belongs there.

## The rules

Run `listen` — and the secretary, if you spawned one — backgrounded; never block
the conversation; never poll or narrate the waiting. A finding is **async** —
emit the watermark, keep working, fold the answer in when it lands. To stop the
bloodhound, stop relaunching `listen` (that subscription is what recognizes a
watermark, so dropping it is what makes the service go quiet) and stop respawning
any secretary you started. To add **ambient recall** on top, run `/recall` — one
flag on the same `listen`, and no secretary required.
