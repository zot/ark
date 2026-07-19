# HTTPOperation
**Requirements:** R3166, R3167, R3168, R3169, R3170

The generic front door that serves an operation object over HTTP. Owns the
per-request prototype copy, the semantic-error → status-code mapping, and
the response encoding — so an operation's own `run` never sees HTTP.

## Knows
- opError: an operation failure paired with a semantic class, not a status code
- opClass: the failure vocabulary — bad input, not found, unavailable, internal
- statusFor: the class → HTTP status mapping (400 / 404 / 503 / 500)

## Does
- handle: turn an operation prototype into an `http.HandlerFunc`; copy the
  prototype per request, `init` the copy, `run` it, then encode or map the error
- copy-per-request: the registration-time prototype is never mutated, so one
  registered value serves every concurrent request
- encode: JSON-encode a non-nil result; write no body for a nil one
- classify: `badInput` / `notFound` / `unavailable` wrap an error with its class;
  an unwrapped error defaults to internal

## Collaborators
- Server: supplied to `init` so an operation can reach the DB, librarian, and
  runtime state it needs
- DB: an operation's `init` binds either the actor (`Sync`) or a private
  `withFTS(fts.Copy())` read view, per crc-DB.md and specs/db-concurrency.md

## Sequences
- seq-http-operation.md
