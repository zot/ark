# Test Design: HTTPOperation
**Source:** crc-HTTPOperation.md

The wrapper is pure, deterministic logic — no DB, no server, no fixtures.
A fake operation (a struct with `init`/`run` recording what it saw) plus
`httptest` covers every behavior. Nothing here needs an `O`-gap.

## Test: prototype is copied per request
**Purpose:** validates R3167 — a registered prototype is never mutated, so
concurrent requests cannot observe each other's state.
**Input:** an operation whose `init` writes the request's query parameter
into a struct field and whose `run` returns that field. Serve it, then
issue several requests with different parameters, including concurrently.
**Expected:** each response carries its own parameter; the prototype value
held by the closure is unchanged after all requests complete.
**Refs:** crc-HTTPOperation.md, seq-http-operation.md#1.2

## Test: semantic error classes map to status codes
**Purpose:** validates R3168 — the wrapper, not the operation, chooses the
HTTP status.
**Input:** table-driven; an operation whose `run` returns each of
`badInput`, `notFound`, `unavailable`, a bare `errors.New`, and `nil`.
**Expected:** 400, 404, 503, 500, and 200 respectively; the error text
reaches the body in every failing case.
**Refs:** crc-HTTPOperation.md, seq-http-operation.md#1.9

## Test: nil result writes no body
**Purpose:** validates R3169 — success with nothing to report emits an
empty body rather than `null`.
**Input:** an operation whose `run` returns `(nil, nil)`.
**Expected:** 200 with a zero-length body, and no `Content-Type: application/json`.
**Refs:** crc-HTTPOperation.md, seq-http-operation.md#1.10

## Test: non-nil result is JSON encoded
**Purpose:** validates R3169's other half.
**Input:** an operation returning a struct and one returning a slice.
**Expected:** `Content-Type: application/json` and a body that decodes back
to the same value.
**Refs:** crc-HTTPOperation.md, seq-http-operation.md#1.10

## Test: init failure short-circuits run
**Purpose:** an operation that cannot decode its request must not run.
Guards the decode-then-work ordering in seq step 1.4 → 1.6.
**Input:** an operation whose `init` records a malformed body and whose
`run` returns the stored decode error; a `run` that also sets a
"did work" flag.
**Expected:** 400, and the "did work" flag is false.
**Refs:** crc-HTTPOperation.md, seq-http-operation.md#1.4
