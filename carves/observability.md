# Observability

When `ark serve` starts it may spend a long time embedding, reconciling or
indexing, and today the only way to see that is to tail the server log. The CLI
and the UI both go blind exactly when the server is busiest. The move: the
server keeps one live **state document** at a `tmp://` path, and every surface
that wants to show ark's state reads or subscribes to it rather than asking the
server ad hoc.

A skeleton (Bill, 2026-10-07): the parts below are the expected split, none is
designed yet.

## Status

- [ ] **Item 1 — the state document.** **OPEN (not queued.)**
- [ ] **Item 2 — publishers.** **OPEN (not queued.)**
- [ ] **Item 3 — the CLI view.** **OPEN (not queued.)**
- [ ] **Item 4 — the UI view.** **OPEN (not queued.)**

<a id="problem"></a>
## The problem

Found starting ark on 2026-10-07: the server was embedding, and nothing outside
the log file said so. The same holds for every long-running activity the server
owns (reconciliation after a restart, a re-index, the embedding queue, watcher
backlogs). `ark status` reports counts as of its last pass, not what is
happening now.

A second blind spot, found 2026-10-12: nothing says *which build* is serving.
That morning's server ran a binary built an hour before ui-engine's per-app
static serving was committed, so `/mcp/css/` returned 404 after `html/mcp` was
removed. Only `go tool nm` on the binary showed why. Every dev build since the
release reports `0.5.0`, so the version string alone would not have told them
apart; a build stamp would have.

<a id="model"></a>
## The model

- **One document, owned by the server.** A `tmp://` doc (name to settle) that
  the server rewrites as its state changes. It lives only while the server
  runs, which is exactly the lifetime of the state it reports
  ([tmp-documents.md](../specs/tmp-documents.md)).
- **Readers do not poll the server for state.** The CLI and the UI read the
  document, and follow it through the existing tmp:// subscription path
  ([tmp-subscription.md](../specs/tmp-subscription.md), whose "Ark monitor" use
  case this is).
- **Tagged, so it is searchable and subscribable.** Each activity is a tagged
  line, so a subscriber can follow just the tags it cares about and the state
  is findable like any other content.

## Parts

**Item 1.** The document's name, its shape (which activities, which tags, how
progress is expressed), and its update rate. Rewriting a tmp:// doc re-indexes
it, so the rate has to stay cheap while the server is under exactly the load it
is reporting on.

The document opens with an **identity header**: ark's version plus a build
stamp (commit and build time), written once at startup and never rewritten, so
it costs nothing under load. The stamp is injected by the Makefile through
`-ldflags`, as `Version` is today; Go's own VCS stamping is off
(`-buildvcs=false`, since the repo is both git and fossil), so it has to come
from the build. The commit comes from git, and a dirty tree is marked as such.

**Item 2.** The server activities that publish into it: embedding first (the
case that prompted this), then reconciliation, indexing and the queues.

The identity also goes to Lua directly, as a `sys.version` string (and the
build stamp beside it, name to settle), set once in `registerLuaFunctions`.
A constant needs no subscription, so the UI can read it without the document
existing yet. This adds to `specs/lua-api.md`.

**Item 3.** A CLI view of the document, live while the server is busy (form to
settle: a `status` flag, its own verb, or both).

**Item 4.** A Frictionless view of the same document (ui-\* work, separate pass).

Its first piece is the identity in the mcp status bar, at the far right after
the status text: `ark v<version>` with the build stamp on hover. It lives in
ark's copy of the mcp app only, since Frictionless's copy has no `sys` global.
It reads `sys.version` until the document view arrives, then can switch to the
document's header.

### Identity decisions

- DECIDED (Bill, 2026-10-12): the version string and the status-bar display
  join this carve, the Lua support with Item 2 and the status bar with Item 4.
  The identity slice (header, `sys.version`, status bar) can land ahead of the
  rest of the carve, since it depends on none of the open questions below.

## Open questions

@undecided: the document's name and whether it is one doc or one per activity; settled when Item 1 is designed.
@undecided: update rate and coalescing under load; settled by measuring a rewrite's cost during a busy embed.
@undecided: whether the existing `ark status` reads the document when a server is up; settled with Item 3.
