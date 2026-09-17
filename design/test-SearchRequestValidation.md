# Test Design: Search requests are refused at the door

**Source:** crc-Server.md

Guards R3399. `POST /search` answered **500** for a malformed regex — the server
reporting that it had malfunctioned, when it had understood the request
perfectly and found it invalid. Bad input is not an internal failure, and
`specs/http-operations.md` already says so (R3165); but `/search` is an ordinary
handler rather than an operation, so it never inherited that mapping and had to
draw the line itself.

The reason it went unnoticed for so long is worth keeping: **one defect hid the
other.** While the CLI swallowed proxy errors and fell through to a local open
(R3398), this path reported a *lock* — so the wrong status code was never
visible to the only client that exercises it. Repairing the swallow is what
exposed the 500, on the very first live run.

## Test: what the server can decide from the request alone

**Purpose:** R3399 — `validateSearchRequest` accepts what the server can honour
and rejects what it cannot, with a message the caller can act on.
**Input:** decoded `searchRequest` values, table-driven. Pure over the request,
so no server, no socket and no index: the whole table is one call each.
**Expected:** empty and ordinary requests pass; valid patterns pass; an
uncompilable `regex` or `exceptRegex` is rejected, as are the mutually exclusive
output modes (R110). Every rejection carries a non-empty message — a refusal the
caller cannot act on is barely better than the 500 it replaced.
**Cases that earn their place, and one that had to be corrected:** `[` in
`regex` is the demonstration case from the live run. The **chunk-filter** rows
are the ones that matter most, and they exist because the first version of this
test was wrong in a way that passed: it asserted a two-element `Regex`, and the
CLI never sends that. `ark search --regex ok --regex '('` puts only the *first*
pattern in `Regex`; every later one arrives as a `chunkFilters` row of mode
`regex`, which `BuildChunkFilters` hands to microfts2 uncompiled. So the test
was green and the real command still returned 500. The live run caught it —
which is the argument for running the command rather than re-reading the request
struct, since a test can only ask the question you thought to ask. Both
polarities are covered (`without` takes a different microfts2 option), and a
`contains` row carrying a paren is covered from the other side: it is text, not
a pattern, and compiling it would reject a legitimate search.
**Alarm:** 1
**Fire alarm:** two, pulled separately. Delete the `regex`/`exceptRegex` loops,
leaving only the pre-existing mode check — literally the state that produced the
500 — and confirm those rows go red while the mode row stays green. Then delete
the chunk-filter loop and confirm only the chunk-filter rows go red. Pulling
them apart is what shows the second loop is load-bearing rather than a
restatement of the first.
**Inject:** server.go:validateSearchRequest
**Refs:** crc-Server.md, specs/search.md, specs/http-operations.md, R3399,
R3165, R110
**Code:** httpop_test.go

## Not covered here, and deliberately

That the handler maps this error to 400 rather than some other status is
asserted by review, not by test: it is one `http.Error(..., StatusBadRequest)`
beside the call. Verified live instead — `ark search --regex '['` against a
running server, which reports the status class the CLI prints. The part worth
pinning is *which requests are decidable at the door*, because that is the set a
future rule gets added to.

Validation uses `regexp.Compile`, the same call microfts2 makes, so the two
cannot disagree about the dialect. If they ever did, the failure mode is a
pattern that passes validation and errors deeper — the status quo ante, not a
new hazard.
