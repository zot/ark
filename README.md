# Ark

A [zettelkasten](https://en.wikipedia.org/wiki/Zettelkasten) is a personal knowledge system -- cards in a box,
connected by cross-references, that grows with you. This one is
digital and connected to your AI. Files on disk, LMDB index, hybrid
trigram + vector search. Long-term memory for Claude Code across
every session on your machine.

Ark indexes your files -- projects, notes, claude's chat logs and
memories, whatever you point it at -- and gives Claude persistent
recall. Your assistant arrives already knowing what you've worked on
and decided. Results come back in microseconds, fast enough for
auto-querying on every message in a conversation.

## What It Does

- **Indexes everything, owns nothing.** Point ark at your directories
  and it indexes them. Claude projects auto-index session chats and
  memory. Your files stay where they are; the index is disposable. You
  and your AI add `@tags` as you work, connecting files together or
  marking what matters. The connections live in your files, not in
  ark's database.
- **Sub-second search as you type.** Simple Google-like interface with
  optional rich filtering by source, tags, content, and file patterns.
- **Remind and inspire.** Ark can surface anything you've discussed in
  past conversations and connect it with relevant knowledge from your
  files, automatically, as you talk.
- **Approve once, recall everything.** Adding a file to Ark implies
  read-approval. An agent with access to `~/.ark/ark` can view any
  indexed file -- no per-file permission popups.
- **Works without a model.** Trigram search and tags give fully
  functional recall on any hardware. Vector search enhances results
  when available but isn't required.
- **Tags as graph edges.** `@decision:`, `@pattern:`, `@question:` --
  tags in your files form a navigable graph with human-readable
  dimensions. Vector embeddings cluster documents but you can't ask
  why, and you can't fix it when the clustering is wrong. Tags give
  you edges you can read, grep for, and edit. When a connection is
  wrong, you delete a word. You can add your own edges at any time,
  and each tag carries a textual body (`@decision: use LMDB for
  index`) so the edge itself has meaning, not just a label. The
  graph survives an index rebuild because it lives in the files, not
  the database.
- **Knowledge vs memory.** Ark distinguishes distilled facts (notes,
  code, docs) from experience (conversations, process, wrong turns).
  Claude knows which kind of recall it's looking at.
- **Built-in console.** Ark ships with its own UI for browsing your
  knowledge, searching, and managing sources. It's a Frictionless app,
  so you can personalize it by asking Claude to change it.
- **Bundles the Frictionless platform.** The entire
  [Frictionless](https://github.com/zot/frictionless) app platform
  comes built into ark. Build your own hot-loadable apps where Claude
  has full access to the running state. No API layer, no frontend
  code, no sync wiring.
- **One server, all agents.** A single `ark serve` process shares the
  index across every Claude session on your machine. CLI commands
  proxy automatically.

## Install

```
make install
```

Or without make:

```
go build -buildvcs=false -o ~/.ark/ark ./cmd/ark
```

The `-buildvcs=false` flag is needed because the repo carries both git
and fossil history.

Then, in any Claude Code project:

```
~/.ark/ark install
```

This installs the `/ark` skill and agent. The install command outputs
instructions -- it's a crank handle (see below), so the model just
follows the prompts.

## Usage

```bash
# Start the server (one per machine, first wins)
~/.ark/ark serve &

# Add directories to the index
~/.ark/ark add ~/notes ~/work/project

# Search
~/.ark/ark search "LMDB performance"
~/.ark/ark search --tags decision
~/.ark/ark search --chunks --regex '@pattern:.*actor'

# Fetch full file content (bypasses permission gates)
~/.ark/ark fetch ~/notes/architecture.md

# Tag operations
~/.ark/ark tag list
~/.ark/ark tag counts decision pattern question
~/.ark/ark tag defs

# Cross-project messaging
~/.ark/ark message inbox --to my-project
~/.ark/ark message new-request --from ark --to microfts2 --issue "Chunker interface"
```

From Claude, just type `/ark` and ask a question. The skill spawns a
Haiku agent that handles the search.

## Architecture

### Two Listeners

`ark serve` runs one process with two listeners:

1. **Unix socket** -- the Ark API. Every session uses this via the CLI:
   orchestrator, project sessions, subagents, direct use. The embedded
   Frictionless UI endpoints are also mounted here via `RegisterAPI`.
2. **HTTP port** -- the browser UI for searching your stuff
   interactively.

### Why CLI, Not MCP

Ark is deliberately not an MCP server.

- **MCP from background subagents is unreliable.** Claude Code's MCP
  support from background agents has several bugs. A CLI command works
  from any agent depth without issues.
- **Zero installation friction.** MCP servers require `claude mcp add`
  per project plus a restart. A skill that points to a binary needs
  nothing.
- **No context pollution.** MCP tool descriptions eat tokens on every
  message whether the tools are used or not. The `/ark` skill loads on
  demand. Zero cost when idle.

### Files on Disk, Index in LMDB

Ark doesn't copy your files. It keeps a lightweight index in LMDB and
tracks when files change, disappear, or appear new. Delete the index,
run `ark rebuild`, get the same results -- because the source of truth
is always the files.

### Search

Powered by [microfts2](https://github.com/zot/microfts2), a trigram
full-text search library. Optional vector search enhances results but
the system is fully functional without it. Search supports:

- Full-text queries with scoring strategies
- Tag-based filtering (`--filter-file-tags`, `--exclude-file-tags`)
- Content-based filtering (`--filter`, `--except`)
- Regex matching (`--regex`)
- Chunk retrieval with preview windows (`--chunks`, `--preview`)
- Source type filtering (`--filter-files`, `--exclude-files`)

## The Inside-Out Graph

Ark is a graph database built from the opposite direction. Traditional
graph DBs start with schema and populate it. Ark starts with documents
and discovers the schema through use.

- **Nodes**: files and chunks (in LMDB)
- **Edges**: tags (indexed, bidirectional via FTS)
- **Schema**: tag vocabulary (emerges from use, documented in `tags.md`)

Tags are human-readable embeddings. A vector database says "these
documents are near each other in semantic space." A tag says "these
documents share `@decision: LMDB`" -- same clustering, but the
dimensions have names you can read. And you can fix a wrong tag. You
can't fix a wrong embedding.

The inside-out principle: delete the index, rebuild it, same graph --
because the tags are in the files. The graph survives because it was
never stored in the index.

See [GRAPH.md](GRAPH.md) for the full picture, including prospector
agents that mine conversation logs and externalize structure as tags.

## Agentic Patterns

These patterns emerged from building Ark's multi-agent system. Cheap
models (Haiku, $0.25/M tokens) are economically necessary for routine
agentic work but behaviorally unreliable without mechanical constraints.
Every pattern below was discovered through iterative failure, and the
documented "what doesn't work" matters as much as the solution.

### Hermetic Seal

Constrain a subagent's tool access through two layers: narrative
framing that shapes intent, and a PreToolUse hook that enforces
boundaries. Either layer alone is insufficient. Narrative framing alone
breaks after 2-3 calls when the model gets frustrated. Hook enforcement
alone wastes calls as the model repeatedly hits the wall.

Together: the narrative produces 80-90% correct tool selection, the
hook catches the rest and teaches the model to self-correct through
denial messages.

Discovered through 12 iterations of documented failure. The pattern
file lists every approach that didn't work and why.

A critical sub-discovery: **word priming**. Tool-name words in the
prompt activate the corresponding tools. "Find what is hidden" makes
Haiku reach for `find`. Replace with "uncover" and usage measurably
drops.

### Crank Handle

A tool outputs a self-contained prompt telling the AI what to do next.
The AI reads it, follows it, calls the tool again, gets the next
prompt. Repeat until done. The sequencing intelligence lives in the
tool, not the model.

This matters because strong models (Opus) can infer multi-step
sequences from a description. Weak models need each step handed to
them. The crank handle externalizes planning so a $0.25/M model does
the same job as a $15/M model.

Each output is self-contained and unambiguous. It either completes
the workflow or hands off the next step.

### Stencil

CLI commands read and write rigidly formatted files so models never
touch the format directly. The shape is fixed; the model provides
content. `ark message set-tags FILE status done` stamps the value into
the correct slot. `ark message new-request` generates the entire file
with correct structure.

Even Opus needed stencils for structured document formats. Models don't
see format -- they see likely next tokens. You cannot prompt your way
out of this.

### Soviet Supermarket

A Latvian immigrant faints in an American supermarket -- not from hunger
but from the coffee aisle. Hundreds of choices after a lifetime of one.

Haiku does the same thing. Given an agent doc with 30 commands, it
defaults to the patterns it already knows (grep, awk, wc) even when
better tools are documented. The fix: put the right answer where the
model looks, not where it "belongs" in the document structure. Agent
docs are runways. First examples are the flight path; everything after
is scenery.

### Solarian Viewer

Three-layer runtime: Skill (viewer) -> Agent (robot) -> CLI (controls).

- **Skill**: the UX layer, loaded into the caller's context. Tiny
  footprint. Routes requests, speaks plain English.
- **Agent**: the expertise layer, runs as a Haiku subagent. Knows every
  flag, convention, and search strategy. Context lives and dies in the
  subagent -- the caller never sees it.
- **CLI**: the mechanism layer. Enforces format, executes operations,
  returns structured output.

Each layer exists because removing it forces one of the others to do
something it's bad at. Named for the Solarian viewing rooms in Asimov.

### Closure Actor (ChanSvc)

Actor model using closures instead of typed messages. The caller writes
the operation inline; the actor executes whatever arrives on the
channel. Eliminates the message-type enum and dispatch switch that
plague traditional actor implementations.

```go
type ChanSvc chan func()
```

All access to protected state is serialized through the channel -- no
mutexes, no races. Fire-and-forget with `Svc`, synchronous with
`SvcSync`. Originally developed in Java in 2003, predating
`ExecutorService`.

## Cross-Project Messaging

Projects communicate through tagged files that Ark indexes and connects
automatically. The cardinal rule: always write to YOUR project's
`requests/` directory. Never modify files in another project.

A request from Ark to microfts2 lives in `ark/requests/`. A response
from microfts2 lives in `microfts2/requests/`. The response file's
existence is the acknowledgment -- no cross-project writes, ever.

Tags provide discoverability: `@ark-request:`, `@ark-response:`,
`@from-project:`, `@to-project:`, `@status:`. Status lifecycle:
open -> accepted -> in-progress -> completed (or denied, future).

See [ARK-MESSAGING.md](ARK-MESSAGING.md) for the full protocol.

## Embedded UI

Ark embeds a [Frictionless](https://github.com/zot/frictionless) UI
served in-process on the unix socket. The browser connects via HTTP.
The UI is built with [ui-engine](https://github.com/zot/ui-engine),
a Lua-powered framework where you describe what you want and Claude
builds it. Hot-loadable, no restart, no deploy.

`~/.ark/ark ui` subcommands drive the UI from any agent depth without
MCP restrictions.

## Related Projects

- [**mini-spec**](https://github.com/zot/mini-spec) -- Spec-driven
  development workflow. Ark was built entirely using mini-spec.
- [**Frictionless**](https://github.com/zot/frictionless) -- Embedded
  UI framework. Ark imports frictionless/flib and wraps ui-engine.
- [**ui-engine**](https://github.com/zot/ui-engine) -- Lua-powered
  UI engine underneath Frictionless.
- [**microfts2**](https://github.com/zot/microfts2) -- Trigram
  full-text search library that powers Ark's search.
- [**Humble Master**](https://github.com/zot/humble-master) --
  Narrative alignment research for LLMs. The agentic patterns
  documented above connect to the broader question of how personas
  shape AI behavior.

## Key Files

| File               | Contents                                             |
|--------------------|------------------------------------------------------|
| `ARK-MESSAGING.md` | Cross-project messaging protocol                     |
| `specs/`           | Human-readable spec files                            |
| `design/`          | CRC cards, sequence diagrams, requirements (R1-R540) |

## Status

V2 complete, V2.5 in progress. FTS works, source monitoring done,
multi-agent system operational. See PLAN.md for current work and
future direction.

## Author

Bill Burdick -- 35+ years in software architecture, from Rich Internet
Applications (1995, seven years before Macromedia coined "RIA") through
language design, blockchain systems, and now AI development tools.

## License

MIT
