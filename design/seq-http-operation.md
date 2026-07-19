# Sequence: serving an operation over HTTP

How one request flows through `handle` into an operation and back out.
Participants: Client, handle (the generic wrapper), Op (the operation
object), DB.

## Diagram 1 — a request served end to end

```
1. Client                 handle                  Op                    DB
1.1  POST /fetch  ───────>│
1.2                       │ op := proto (copy)
1.3                       │ ─────────────────────>│ init(srv, r)
1.4                       │                       │ decode body / read query
1.5                       │                       │ bind DB access ─────────>│
1.6                       │ ─────────────────────>│ run()
1.7                       │                       │ do the work ────────────>│
1.8                       │ <─────────────────────│ (result, error)
1.9                       │ err != nil? statusFor(class) ──> http.Error
1.10                      │ result != nil? writeJSON        ──> body
1.11 <────────────────────│ response
```

- **1.2** is the whole reason a prototype is safe to register: every
  request works on its own copy, so the registered value is never mutated.
- **1.5** is where the concurrency decision is made *once*, per
  `specs/db-concurrency.md` — the operation binds either the actor
  (`Sync(srv.db, …)`) or a private `srv.db.withFTS(srv.db.fts.Copy())`
  read view. Nothing downstream re-decides.
- **1.8** returns a semantic error class, never a status code — that is
  what keeps `run` reusable by a CLI or in-process caller.
- **1.9 / 1.10** are the only steps that know HTTP exists.
