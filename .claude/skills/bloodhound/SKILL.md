---
name: bloodhound
description: "Turn on the warm search bloodhound for this session — spawn the per-session secretary and listen for findings — and the craft of directing a hunt: the <BLOODHOUND>…</BLOODHOUND> watermark, its file-scope attributes, the want-words, and how to read a finding. Load when the user types /bloodhound, asks to turn on directed search, or wants to emit a watermark; the watermark is inert without this skill's machinery. CLI searching you run yourself is /ark-search. Requires /ark."
---
<!-- CRC: crc-RecallAgent.md, crc-RecallAgentBuilder.md | R2890, R2947, R2950 -->

sessionid=${CLAUDE_SESSION_ID}

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

Two roles, both in your session: a per-session **secretary** (a Haiku subagent
you spawn) that runs the hunt, and **you**, who consume its findings. Until both
run, the bloodhound does nothing.

## 1. Spawn the secretary

Reserve a nonce:
```
~/.ark/ark connections recall reserve-nonce
```
Then launch it with the **Task tool**, `run_in_background: true`:
- `subagent_type`: `ark-recall-agent`
- `description`: `ark-recall secretary loop nonce <N>` — the `nonce <N>`
  substring is how the server finds the subagent's transcript; it must be present.
- `prompt`: `Start the recall secretary loop now. Session: <sessionid>. Nonce: <N>.`

The secretary loops internally (`recall next --session <sessionid> <N>`), draining
its `@ark-secretary-work` tube — search tasks now, curation docs too once ambient
is on — until its context fills, then it exits and the harness notifies you
(step 3).

## 2. Run the consumer loop

Start the consumer, backgrounded:
```
~/.ark/ark connections recall listen --session <sessionid>
```
Run it with `run_in_background: true` so you stay conversational. It subscribes to
`@ark-bloodhound-result` (the bloodhound opt-in) and blocks until a result
arrives, then completes and the harness notifies you. At this level the results
are **`## Finding:`** items — a curated answer to a `<BLOODHOUND>` you emitted,
headed by your own clue echoed back. **Fold a finding into your reasoning** (you
asked for it); surface it to the user only if it helps them. Then **relaunch**
`listen` to keep the loop going.

## 3. Respawn the secretary when it exits

When the secretary subagent **completes** (the harness notifies you), it hit its
context limit — normal and expected, not a failure. Reserve a fresh nonce and
spawn it again exactly as in step 1. That is the whole of supervision: no streak
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

**Async by default** — fire the hound and keep reasoning; leads surface in a
later turn, like recall. For "need it now," emit the watermark and explicitly
block on the result.

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

### Reading the return

A **curated digest** — the few passages that answer, with sources — not a raw
dump. Trust the curation. If it comes back thin, hand back a *sharper clue* or
*wider scope* in a fresh watermark; don't re-run raw searches yourself. That's
the whole point of the hound.

**Chasing a finding yourself is a different skill.** Once a finding lands and
you want to open what it pointed at, widen around it, or run a known-item
lookup of your own, that is the ark **CLI** — `ark search`, `ark chunks`,
`ark fetch` — and its craft lives in **`/ark-search`**. The boundary is simple:
the watermark is this skill, because it needs this skill's machinery; anything
you type at the CLI works in any session and belongs there.

## The rules

Run both the secretary and `listen` backgrounded; never block the conversation;
never poll or narrate the waiting. A finding is **async** — emit the watermark,
keep working, fold the answer in when it lands. To stop the bloodhound, stop
relaunching `listen` and stop respawning the secretary (its subscriptions drop
and the service goes quiet for this session). To add **ambient recall** on top,
run `/recall`.
