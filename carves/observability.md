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

**Item 2.** The server activities that publish into it: embedding first (the
case that prompted this), then reconciliation, indexing and the queues.

**Item 3.** A CLI view of the document, live while the server is busy (form to
settle: a `status` flag, its own verb, or both).

**Item 4.** A Frictionless view of the same document (ui-\* work, separate pass).

## Open questions

@undecided: the document's name and whether it is one doc or one per activity; settled when Item 1 is designed.
@undecided: update rate and coalescing under load; settled by measuring a rewrite's cost during a busy embed.
@undecided: whether the existing `ark status` reads the document when a server is up; settled with Item 3.
