# HTTP Operations

The front-door structure for request-shaped work: an **operation object**
served over HTTP by a generic wrapper. The concurrency rules an operation
must obey are canonical in [db-concurrency.md](db-concurrency.md); this
spec owns the *shape* — how an operation is written, how it is registered,
and what the wrapper does on its behalf.

## Problem

A server handler is where a request first touches the DB, so it is where
the actor/copy-view decision gets made. Today that decision is re-derived
by reading each handler: some enter `Sync(srv.db, …)`, some bind a private
`fts.Copy()` view, and the two that did neither were races that sat latent
until a `-race` run found them.

Handlers also fuse three unrelated jobs into one function body: decoding
the request, doing the work, and writing the HTTP response. That fusion is
why the work can't be reused — a CLI or in-process caller that wants the
same result has to either duplicate the logic or fake an
`http.ResponseWriter`.

## Solution

An operation is a struct with two methods:

- `init(srv, r)` — take what the request carries and bind the DB access
  the operation is allowed to use.
- `run() (any, error)` — do the work and return a result to serialize.

`run` is **HTTP-agnostic**. It never touches the `http.ResponseWriter`,
never picks a status code, and never writes bytes. It returns a value and
an error; the wrapper serializes the value and maps the error. This is what
makes one operation serve three front doors — HTTP, CLI, and internal
callers — instead of one.

Registration copies a prototype per request:

```go
mux.HandleFunc("GET /status", handle(srv, statusOp{}))
```

`handle` copies the zero-value prototype for each request, calls `init` on
the copy, then `run`. The copy is what makes an operation safe to share as
a registration-time value: no request mutates another's state.

**Why a free function and not `srv.handle(…)`.** Go does not permit type
parameters on methods, and the wrapper needs one to name the operation
type. So `handle` takes the server as its first argument. This is a
language constraint, not a design preference.

## Error classification

`run` classifies failures **semantically**, not by HTTP status, so a
non-HTTP front door can map them its own way:

- **bad input** — the request is malformed or self-contradictory.
- **not found** — the named thing does not exist.
- **unavailable** — a required subsystem is not configured or not running.
- anything else — an internal failure.

The HTTP wrapper maps these to 400, 404, 503, and 500. A CLI front door
would map them to exit codes. An operation that returns a bare error gets
the internal treatment, so the safe default requires no annotation.

## Response body

A `nil` result writes no body — the operation succeeded and has nothing to
say. Any other result is JSON-encoded. Operations that must write a
non-JSON body (HTML, streams, server-sent events) are **not** operations
under this spec; they stay ordinary handlers, because the HTTP-agnostic
`run` contract is exactly what they cannot satisfy.

## Adoption

Conversion is **incremental and opportunistic**, not a sweep. The
machinery plus a few representative operations establish the shape; the
remaining handlers convert as they are touched for other reasons.

The end state this aims at is a one-grep audit — every `srv.db` use is an
operation's binding or a bug — but that property arrives only when
adoption is complete, and it is not worth a large mechanical refactor on
its own. Until then the value is narrower and still real: a new handler
has a correct pattern to copy rather than a design to re-derive, which is
how the two known races got written in the first place.

## Representative operations

Three operations cover the axes a new one is likely to need:

- **status** — a query parameter, an actor read, and two possible response
  shapes. The simplest case.
- **fetch** — a decoded request body, and a failure that is *not found*
  rather than internal. Shows the wrapper choosing a status code from a
  semantic class.
- **expand-search** — a decoded body plus an off-actor read that binds its
  own private `fts.Copy()` view. Shows an operation whose `init`
  establishes the copy-read discipline that `db-concurrency.md` requires.
