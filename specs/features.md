# Features

Canonical reference for ark's main capabilities — what each one is
for, what it does, and where to find its per-feature spec.

This document describes the **current** state of the feature
inventory. Per-feature specs in `specs/*.md` own the behavior and
design rationale; this document describes ark's *capabilities axis*
and pins motivation against objective so the project's intent stays
visible above the implementation. When the two disagree, this file
loses — update it to match the per-feature spec.

Like `specs/cli-commands.md`, `specs/record-formats.md`, and
`specs/lua-api.md`, this is a **summary spec**: it doesn't introduce
behavior, it indexes behavior already specified elsewhere along a
single cross-cutting axis. Mini-spec's per-feature anchoring won't
catch cross-cutting updates on its own — when a new feature ships or
a feature's motivation changes, update this file explicitly.

Language: Go (core) + Lua (apps). Environment: ark CLI binary at
`~/.ark/ark` with the `~/.ark/` directory as its unified home.

## Feature Inventory

| Feature                                     | Status                                     | Spec                                                                          |
|---------------------------------------------|--------------------------------------------|-------------------------------------------------------------------------------|
| Files-on-disk + index                       | shipping                                   | `principles.md` (project-level commitment); `record-formats.md` (index layout) |
| Ark tags & hypergraph structure             | shipping                                   | `ark-tags.md`                                                                 |
| Tag drill-down (`<ark-tag>` element)        | shipping                                   | `ark-tag-element.md`                                                          |
| Source monitoring & chunking                | shipping                                   | `chunkers.md`, `source-monitoring.md`                                         |
| Hybrid full-text + vector search            | shipping                                   | `search.md`, `fuzzy-search.md`, `tag-search-filters.md`                       |
| Tag definitions (D records)                 | shipping                                   | `tag-defs.md`                                                                 |
| Find connections (Tag Forge)                | 2A shipping; 2B/2C in progress             | `find-connections-substrate.md`, `tag-forge.md`                               |
| Recall                                      | substrate shipping (`ark connections recall`); statistical derivation pass (`--propose`) shipping; simple-recall watcher v1 shipping; v2 agent layer in spec (long-running Haiku curator daemon between watcher and assistant, spawned once per generation by the Luhmann orchestrator; RJ rejection counter + propose / mention ceiling thresholds) | `recall.md`, `simple-recall.md`, `discussed-tags.md`, `derived-tags.md`, `.scratch/SIMPLE-RECALL.md` (working notes) |
| Luhmann orchestrator (supervisor + chimes)  | Phase 2 in spec (Go-side `ark monitor` / `ark luhmann` CLI, `[luhmann]` config, `@chime-Nm` standard scheduling tags, subscriber-presence gate). Skill / agent files separate. | `luhmann.md`, `monitor.md`, `chimes.md`, `subscriber-presence.md`, `.scratch/LUHMANN-ORCHESTRATOR.md` (working notes) |
| Managed PTY session (ark hosts Luhmann)     | Phase 1 shipping (Go CLI pty core: `ark luhmann launch`/`attach`/`status`/`stop`, fan-out multiplexer, content-free launch confirmation). Phase-2 browser transport shipping (Go `GET /luhmann/pty` websocket, ark's own `gorilla/websocket`, binary pty bytes + JSON resize/repaint control frames); the xterm.js terminal + desk-lamp UI stay deferred to the phase-3 `/ui-thorough` slice (#39). | `managed-pty.md`, `.scratch/LUHMANN-MANAGED-SESSION-20260709.md` (working notes) |
| Curation workshop (Tag Forge UI)            | shipping                                   | `tag-forge.md`, `curation.md`                                                 |
| Internal-disposition tagging                | Go core shipping (accept writes the tag into the source file body for markdown/bracket/indent, else external; every accept reinforces with a positive `@ext-judgment`) | `internal-disposition.md`, `at-ext-parsing.md` |
| CLI-first agent integration                 | shipping                                   | `cli-commands.md`, `VISION.md`                                                |
| Cross-project messaging                     | shipping                                   | `ARK-MESSAGING.md`                                                            |
| Messaging dashboard (kanban view)           | shipping                                   | `apps/ark/requirements.md` (Messaging Dashboard section)                      |
| Sidecar agents (spectral, curation, recall) | spectral shipping; others growing          | `spectral-search.md`, `find-connections-substrate.md`                         |
| Frictionless UI                             | shipping                                   | `ACCESSING-FRICTIONLESS.md`, `lua-api.md`                                     |
| Bundle distribution                         | shipping                                   | `cli-commands.md` (bundle/setup commands)                                     |
| Self-provisioned in-process inference | engine landed (gollama→yzma): llama.cpp via yzma purego/`dlopen`, runtime-provisioned libs (`ark embed install`), no GPU-native compile-time dep | `yzma-embedding.md`, `llama-libs.md` |
| Cross-platform `CGO_ENABLED=0` binaries | shipping: `lmdb-go`→bbolt (store) and gollama→yzma (embedding) removed the last C dependencies; the Makefile builds CGO-free and the `release` target cross-compiles the supported `GOOS/GOARCH` targets, grafting bundled assets via `ark bundle -src` (R2971/R2972). v0.5.0 shipped (linux-amd64) | `llama-libs.md`, `record-formats.md` |

The columns:

- **Status.** *Shipping* means the feature is wired end-to-end and
  in daily use. *In progress* means the spec and some implementation
  exist but the feature isn't yet load-bearing. *Planned* means
  motivation is captured but design hasn't landed.
- **Spec.** The per-feature spec(s) that own the deeper material.
  When more than one is listed, the first is canonical and the
  others are supporting.

## Files-on-disk + index

**Motivation.** Index files lose; source files don't. Ark commits to
the Fossil principle: the markdown, code, and other source files
the user edits are authoritative, and ark's index is rebuildable
derived state. Without this guarantee, ark would be another
proprietary store that holds the user's data hostage. With it, ark
is a *projection* over content the user controls.

**Objective.** Every fact ark surfaces — chunks, tags, embeddings —
is reconstructible by re-running the indexer over the source files.
`ark rebuild` is the operational expression of this guarantee.
Index-only state (caches, indexes, sidecar request queues) is fair
game; index-only *user data* would be a bug.

**Surface.** `ark serve`, `ark rebuild`, `ark refresh`, `ark add`,
the indexer pipeline, all the index records described in
`specs/record-formats.md`.

## Ark tags & hypergraph structure

**Motivation.** Pairwise hyperlinks (the form most knowledge tools
offer) under-specify the connections in a thinker's corpus. Ark's
tags add second-order set membership and third-order spectral
attenuation, then let those compose into stacked filters — a fourth
order that emerges from composition. The hypergraph isn't queried
out of a separate structure; it's *constituted by* the tags the
user wrote in prose.

**Objective.** `@name: value` syntax, parsed wherever it appears in
indexed content, produces hyperedge membership (every chunk carrying
a given name is a member) and value-attenuated ranking (each chunk's
value participates in similarity scoring). Tag values are full-text
searchable; tag names index hyperedges directly. `@ext:` lets a tag
in one file attach to a chunk in another, so the system supports
community curation: you can tag content you don't own.

**Surface.** `ark-tags.md` is the operational explainer. The tag
parser is shared between the Go indexer and the Frictionless
markdown editor.

## Tag drill-down (recursive hypergraph navigation)

**Motivation.** `ark-tags.md` makes three structural claims —
second-order set membership, third-order spectral attenuation,
fourth-order stacked filters as virtual hyperedges constructed
on demand. Those are *theoretical* until the user has an
operational way to walk them. Tag drill-down is that walk: every
tag in every result is itself a portal into its hyperedge, the
portals nest, and the user composes a tree of nested views that
mirrors the path they're taking through the corpus.

**Objective.** Each tag in a search result renders via the
`<ark-tag>` web component, with a disclosure triangle to its
left. Expanding the triangle reveals not a fixed sub-view but a
full nested `<ark-search>` component, pre-scoped to the tag's
name+value — same filter row, same "+ add filter" button, same
result chips, same `+ save` affordance. The user can drill three
directions, recursively:

- **Deeper** — open a tag inside a nested result to spawn another
  search one level down.
- **Across** — open a sibling tag in the same result to spawn a
  search adjacent to the current one.
- **Sideways into refinement** — modify the nested search
  itself (change terms, add filters, save as a view) without
  collapsing the surrounding tree.

The composition is the point. A single click sequence walks
`@project: ark` → its results → `@to-project: frictionless` →
those results → another tag → and so on. The user never leaves
their initial query context; the hypergraph paths they explore
remain visible above them. It's "open one more book without
closing the current one" applied to a corpus.

**Surface.** `<ark-tag>` web component
(`markdown-editor/src/`), the Go post-processor that wraps
`@name: value` patterns in `<ark-tag>` elements, the recursive
`<ark-search>` composition. Specs: `ark-tag-element.md`
(component), `tag-search-filters.md` (search row semantics).

**Possible future directions.** The recursive-nesting UI keeps
the whole drill tree visible at once — every level expanded
in-place, scroll-as-you-go. That's one form, not the only one.
Drill-down is fundamentally a **graph browser** for the
hypergraph — analogous to how a file browser navigates a tree —
and a file browser has more than one presentation. Two
alternatives worth keeping in mind:

@future: ark tag drill-down

- **Single panel with breadcrumb drop-downs.** Show only the
  current view; breadcrumbs above name the path. Each crumb is a
  drop-down that snaps back to that level or lets the user
  branch sideways. Compact; reads like a graph-aware path bar.
- **Column-style "Finder" view.** Each drill level becomes a
  vertical column to the right of its parent; sibling drills
  share the same column. Walks the graph horizontally; lets the
  user see siblings without expanding them.

A user could plausibly want to switch presentations for the same
underlying drill state — nested for exploration, breadcrumbs for
narrow follow-up, columns for systematic sibling review. The
underlying primitive (each tag is a search) doesn't change; the
presentation layer does.

## Source monitoring & chunking

**Motivation.** Keeping the index synchronized with source files by
hand would be a chore that defeats the system. The user's job is to
write; ark's job is to notice what they wrote and react.

**Objective.** A scanner watches sources for new and changed files;
an indexer chunks them according to per-file-type strategies
(markdown by heading/paragraph, code by top-level definition,
chat-jsonl per turn, lines for line-based files, future bracket and
indent chunkers); tag/embedding/V-record state is incrementally
updated. The user does not run "reindex" — they edit their files.

**Surface.** `ark serve` runs the scanner; `ark scan`, `ark refresh`,
`ark resolve` are manual entry points. Chunker strategies are
registered in `db.go`.

## Hybrid full-text + vector search

**Motivation.** Pure FTS misses semantic matches; pure vector misses
literal matches. Real users want both, with the ability to
constrain by tag, by file, by "aboutness." Boris Cherny's
observation — agentic search using FTS routinely beats vector RAG —
informs the default: lead with literal, refine with semantic, never
force the user to pick.

**Objective.** One search call combines trigram FTS, vector
similarity, tag-name/value filters, and natural-language "about"
filters. Filters stack: each row composes a polarizer; the result
is a virtual hyperedge built on demand. Results stream back as
chunk groups with relevance scores and highlighted previews.

**Surface.** `ark search`, the `<ark-search>` web component, the
search subcommands documented in `specs/cli-commands.md`.

## Tag definitions (D records)

**Motivation.** A tag *name* alone is opaque — `@decision:` is just
a label until something tells the system what a "decision" tag
means. Tag definitions give tag names per-file descriptive text
that the spectral substrate uses for ED scoring. Without them, the
tag-name vector space is empty.

**Objective.** Tag-name descriptions live in source files (or in
tag-def markdown) and get indexed as D records keyed by
`(tagname, fileid)`. The same tag may carry different definitions
in different corpora; D records are per-file by design. ED
embeddings derive from these definitions.

**Surface.** `ark tag defs`, the D-record entry in
`specs/record-formats.md`, the ED substrate documented in
`specs/find-connections-substrate.md`.

## Find connections (Tag Forge)

**Motivation.** When the user is writing, the tags they *should*
attach to a chunk are often tags they've used before in other
contexts. Surfacing those candidates closes a Luhmann-style mental
gap — "this reminds me of something I tagged before" — without
forcing the user to remember.

**Objective.** Given a chunk, a `path:range` reference, or bare
text, find connections returns ranked tag-*name* candidates from
four substrates: vector-against-tag-defs (ED), trigram-against-
tag-defs (ED), vector-against-chunks (EC), trigram-against-chunks
(EC). Each candidate carries evidence — supporting chunks,
motivating files, per-substrate scores — so the user can judge
why the candidate was proposed. The output drives the curation
workshop.

**Surface.** `ark connections find`, the substrate code in
`connections_substrate.go`, the workshop's `## Proposals` body
section.

## Recall

**Motivation.** Indexing is dormant. A corpus full of tagged
chunks does no work until something causes the past to enter the
present. For a human, that's the *Schlagwortregister* moment —
"this reminds me of something I wrote." Luhmann did it in his head
for forty years. For an AI agent collaborating with the user, the
moment is harder still: the agent has no memory across sessions
and can't search for what it doesn't realize exists. Recall is
the primitive that lets prior thinking re-enter the workspace,
turning ark from an archive into a thinking surface.

**Objective.** Given a conversation turn (or any context payload),
return the tag names the user's corpus has touched on this topic
before *and which the conversation has not yet brought up*. Recall
is **tag-shaped**: the output is a small set of tag names the
agent should consider mentioning; chunks are intermediate evidence,
not the deliverable. An LLM step synthesizes the substrate's
chunk-level signal into tag suggestions and filters against a
discussion history so the same tag doesn't surface twice in a row.

**Surface.** `ark connections recall` (CLI substrate primitive,
with `--session` / `--discussed` for the dedup filter and
`--propose` for the statistical derivation pass that surfaces
derived-tag candidates per chunk),
`ark discussed add/list/clear/prune` (per-session dedup state
backed by RD records — see `specs/discussed-tags.md`),
`Store.DerivedProposals` / `AcceptDerived` / `RejectDerived` (Go
API consumed by the Tag Forge — see `specs/derived-tags.md` for
the RC/RJ/RF record classes), `sys.recall` (Lua bridge), the
substrate primitives shared with find connections. Statistical
derivation runs on each recall call and **computes** candidates for
display (#36); the pass persists nothing — the calling agent authors
the durable `@ext-candidate`s it chooses (`ark ext candidate`), so
curation is discernment-gated rather than passively accrued. The
**simple-recall watcher** — a
deterministic subsystem of `ark serve` — runs the substrate
against Claude Code JSONL chunks as they land and writes a
**curation doc** to `tmp://ARK-RECALL/`. The **recall agent**
(a long-running Haiku daemon, spawned once per generation by the
Luhmann orchestrator; it subscribes to the curate tag and loops,
processing one curation doc per fire) filters the candidates and
writes a **result doc** the assistant reads — keeping an LLM
out of the high-frequency watcher path while a cheap model
curates before anything reaches the user. The assistant consumes
that result doc through `ark connections recall listen --session SID`,
a backgrounded consumer loop the **`/recall` skill** starts: it
subscribes the session to its result tag and blocks until a doc
arrives. That subscription is also the daemon's **opt-in gate** —
until a session calls `listen`, its curation docs pile up
undispatched, so recall costs nothing for sessions that never asked
for it. RJ records carry a
**rejection counter** consulted by two `[recall]` ceilings
(`reject_propose_ceiling`, `reject_mention_ceiling`) that fade
chronically-rejected `(chunk, tag)` pairs out of view in two
stages. Both v1 watcher and v2 agent layer are spec'd in
`specs/simple-recall.md`; new-tag-definition invention (RP/RPE/RR
records) remains deferred. Phase 2 (a long-running orchestrator
named Luhmann that owns the agent and a monitor CLI/view) is in
`.scratch/SIMPLE-RECALL.md` and the Luhmann orchestrator capability
below.

**The bloodhound (directed search).** Ambient recall is *push* — the
corpus surfaces material unasked. Its *pull* counterpart, the
**bloodhound**, lets the assistant *direct* a search: it emits a
`<BLOODHOUND>…</BLOODHOUND>` watermark in its normal output, the watcher
recognizes it (deterministically, no LLM) and drops a directed-search task
onto the secretary's shared `@ark-secretary-work` tube — in its own
`tmp://ARK-BLOODHOUND/` namespace. The secretary runs the search (a
self-contained crank handle carries the CLI craft) and returns a curated
**finding** on its **own** `@ark-bloodhound-result` channel (distinct from
ambient recall's `@ark-recall-result`), which the assistant's `listen`
subscribes to — a *response* the assistant called for, correlated by the
echoed clue, distinct from the ambient surface/recommend it didn't ask for.
A directed hunt also **curates**: alongside its finding the bloodhound
recommends *connecting tags* on the chunks it surfaced, promoting a query that
proved its worth into a persistent tag (**Query Crystallization**) — the
assistant winnows the proposals at its discernment gate and authors the durable
candidates.
The two are independent opt-ins (each its own subscription), so the
bloodhound (level 3) can run without the ambient firehose (level 4). Async
by default. The secretary is one agent doing both — a
zettelkasten-keeper on an eternal hunt to flesh out the collection, curating
what drifts past and tracking what it's sent after. Spec'd in
`specs/bloodhound.md`.

## Luhmann orchestrator (supervisor + chimes)

**Motivation.** v2 recall ships ambient curation as a thing the
assistant *requests* (subscribe, listen, spawn one-shot agent per
curate event). The orchestrator inverts that: it hosts the recall
loop continuously so the user-facing assistant just listens for the
result. Recall becomes a thing that *happens to you* mid-conversation,
not a thing you *invoke*. Long-running orchestrators run into a
companion problem — Anthropic's prompt cache TTL expires during long
idles, so every supervisor decision pays a rebuild cost. The chime
convention solves that as a generic primitive (useful well beyond
Luhmann).

**Objective.** A Claude Code session — invoked via the `luhmann`
skill, or auto-started in a dedicated CC project via a `CLAUDE.md`
HELLO pattern — supervises lotto-tube subagents per the sublooper
pattern, optionally chats with the user about the corpus, and stays
cache-warm via a chime subscription. The Go side of ark provides
the supporting CLI and configuration; the orchestrator session
itself is a `.claude/skills/luhmann.md` skill plus a companion
`.claude/agents/luhmann-researcher.md` (skill / agent files are
not in this spec's scope).

**Surface.** `ark monitor` (read `~/.ark/monitoring/*.jsonl` — see
[monitor.md](monitor.md)), `ark luhmann` (write
`luhmann.jsonl` — see [luhmann.md](luhmann.md)), `ark subscribers`
(subscriber-presence query consumed by the watcher and recall
`close` — see [subscriber-presence.md](subscriber-presence.md)),
the `[luhmann]` `ark.toml` section, and the `@chime-Nm` standard
scheduling tags (`@chime-1m:` ... `@chime-60m:`, see
[chimes.md](chimes.md)). The pre-existing `AddChime()` hardcoded
15-minute event is retired in favor of `@chime-15m:`.

## Curation workshop (Tag Forge UI)

**Motivation.** Tag changes affect the hypergraph; mechanical
auto-tagging would erode the user's curation judgment. The
workshop puts a human between AI proposals and accepted state:
the system proposes, the user stages or rejects, the user commits.

**Objective.** A Frictionless app where pinned chunks drive a
connection-finding loop, proposals appear with substrate evidence,
the user accepts/rejects/edits via a stage/revert/accept loop, and
accepted changes write back to the source files (tags live in the
prose, not in a sidecar).

**Surface.** `apps/ark/` (the Frictionless app), `specs/tag-forge.md`,
`specs/curation.md`, the `## Proposals` Lua workshop section.

## CLI-first agent integration

**Motivation.** Ark deliberately does not ship an MCP server. MCP
adds a process, a protocol, a permissions surface, and a class of
bugs (state sync, restart races, tool authorization) for value the
CLI already provides. Markdown is the LLM's native input;
subcommands are the LLM's native verb. Going through a CLI keeps
the integration honest, debuggable from a terminal, and inspectable
under `strace`.

**Objective.** Every capability ark exposes to an agent goes
through a CLI subcommand emitting baby-food markdown (or JSON when
the consumer is a script). Agents — Claude Code, Hermes, custom
sidecars — invoke the binary; ark stays a tool, not a server.

**Surface.** `specs/cli-commands.md` is the canonical inventory.
`VISION.md` documents the no-MCP decision rationale.

## Cross-project messaging

**Motivation.** Multiple ark-instrumented projects living on the
same machine need to communicate — a sidecar in one project needs
to ask a question of another; an agent in project A needs to
deliver a result to an agent in project B. A central message bus
would be overkill and would couple the projects' lifecycles.

**Objective.** File-based request/response messages live in each
project's `requests/` directory. Messages are markdown files
carrying `@ark-request:` / `@ark-response:` lifecycle tags and
`@archived:` for orthogonal visibility control. Senders write
files; recipients read them; the Hermes agents (messenger and
searcher) abstract the protocol.

**Surface.** `ark message` subcommands, the `requests/` directory
convention, `ARK-MESSAGING.md`, the `ark-messenger` and
`ark-searcher` Hermes agents.

## Messaging dashboard (kanban view)

**Motivation.** The messaging protocol is correct but invisible —
files on disk, lifecycle tags, no overview. Without a view, a
user with several open conversations across projects has to grep
their `requests/` directory and reconstruct status mentally. The
kanban makes the lifecycle visible: which conversations are
waiting on whom, which have been accepted but not delivered,
which can be archived. It also gives the user a place to *steer*
the conversation set without dropping to the CLI.

**Objective.** A Frictionless view in `apps/ark/` that reads from
`mcp:inbox()` and groups messages into status columns
(Future, Open, Accepted, In-Progress, Completed, Denied) with
only the columns that have items shown. Each card is a merged
request+responses conversation. Project chips above the board
filter by participant; status chips toggle column visibility.
The board is designed for normal monitors (external display or
XR), not the Steam Deck's 7-inch panel.

**Surface.** The Messaging Dashboard section of
`apps/ark/requirements.md`, the messaging viewdef in
`apps/ark/viewdefs/`, the `mcp:inbox()` Lua bridge.

## Sidecar agents

**Motivation.** Some workloads need LLM intelligence but should not
burden the user-facing session — spectral query expansion,
connection refinement, ambient recall synthesis. Running these in
the same Claude context as the user would crowd the conversation
and pay the full token cost for background work. A sidecar is a
loop, an inbox, a cheaper model.

**Objective.** Sidecars block on a CLI command that pops one item
from an event stream (the *lotto tube* pattern), call out to a
model (Haiku typically), and post results back into ark. The
user-facing session stays uninterrupted; the work happens in
parallel; weak models perform well because each iteration is a
self-contained baby-food prompt.

**Surface.** Spectral expansion sidecar (`ark search expand`),
the find-connections refinement sidecar, the planned recall agent.
The pattern itself is documented in
`~/.claude/personal/patterns/sidecar-agent.md` and
`lotto-tube.md`.

## Frictionless UI

**Motivation.** A fixed-app GUI calcifies; ark grows continuously
as the corpus does. Per-corpus, per-task views — written in Lua and
declared as viewdefs — keep the UI moving at the speed of the work.
Embedding the Frictionless runtime in ark, rather than running it
as a separate process, means the UI sees the same in-memory state
the CLI does.

**Objective.** Apps live under `apps/<name>/` (Lua + viewdefs +
HTML + theme). The ark binary embeds the Frictionless library
(`flib`), wraps the ui-engine, and exposes ark's Go-side primitives
through Lua bridges (`sys.*`, the `mcp.*` extensions). One Unix
socket and one HTTP port serve both CLI and browser callers.

**Surface.** `~/.ark/ark ui`, `apps/ark/`, the
`ACCESSING-FRICTIONLESS.md` integration doc, `specs/lua-api.md`
for the Lua surface.

## Bundle distribution

**Motivation.** A single artifact deployable to a fresh machine
keeps the install story simple: one binary, no companion files to
forget. The user runs `ark setup` once and the environment
unpacks.

**Objective.** `ark bundle` zip-grafts content (HTML assets,
viewdefs, skill files, the embedding model) onto the binary
itself; `ark setup` extracts the bundle into `~/.ark/` and
installs ark's skill and agent files into `~/.claude/` so Claude
can use them. The runtime then reads from `~/.ark/` as its unified
home. No `apt install` and no separate service; the only files
setup writes outside `~/.ark/` are that skill and agent markdown
in `~/.claude/`.

**Surface.** `ark bundle`, `ark setup`, `ark install`, the
`~/.ark/` directory layout, the bundle subcommands documented in
`specs/cli-commands.md`.

## How to update this file

When a new feature ships or a feature's motivation shifts:

1. Make sure the per-feature spec in `specs/<feature>.md` carries
   the canonical behavior and design.
2. Update the inventory table here — add the row or change
   status / spec links.
3. Add or rewrite the per-feature `##` section with the new
   motivation and objective. Keep it short; the per-feature spec
   has the depth.
4. If the feature spans multiple existing axes — touching the CLI
   surface, the index layout, or the Lua API — update those
   summary specs too. CLAUDE.md's *cross-cutting spec references*
   section is the maintenance checklist.
