# Test Design: FindConnections

**Source:** crc-Librarian.md, crc-Server.md, crc-CLI.md,
seq-find-connections.md

These tests live in `librarian_test.go` (orchestrator + bridge) and
`cmd/ark/main_test.go` (CLI) where the existing spectral-expand
tests live. They use the **test-as-subscriber pattern** from R2312
— subscribing through `PubSub.Subscribe` + `Listen` against the
same tmp:// path the orchestrator writes, no mocks of the substrate.

## Test: enqueue creates the tmp:// doc with pending header tags

**Purpose:** Validates R2319, R2326, R2327 — the orchestrator
creates `tmp://connections/<id>.md` with `@connections-status:
pending`, `@connections-request-id`, `@connections-pinned-chunks`,
`@connections-started`, `@connections-progress: fetching`,
`@connections-elapsed: 0` on enqueue.

**Input:** Server with three chunks indexed. Call
`Librarian.FindConnections([]uint64{c1, c2, c3}, opts{}).` Subscribe
through `PubSub.Subscribe` to the resulting tmp:// path with
`tag="*"`.

**Expected:** Returned requestID is non-empty. The tmp:// doc exists
and exposes the five header tags. The first `Listen` batch carries
`@connections-status: pending` plus the other header tags from the
centralized publish (R2281).

**Refs:** crc-Librarian.md, seq-find-connections.md (Happy Path),
seq-tmp-subscription.md.

## Test: full happy-path drive (--result writes body, flips completed)

**Purpose:** Validates R2317, R2329, R2330, R2333. Drives the full
sidecar-as-stub sequence through the CLI subcommand.

**Input:** Enqueue a request. Call `ark connections --wait` (CLI)
to drain. Call `ark connections --fetch <id>` and verify the
response JSON. POST a valid `--result` JSON payload through the
HTTP handler (`SetConnectionsResult`).

**Expected:** The tmp:// doc transitions through `working` →
`completed`. The body carries `## Themes` and `## Shared Tag
Candidates` sections with the expected `@theme-evidence`,
`@shared-tag`, `@shared-tag-value`, `@shared-tag-evidence` tag
lines. `@connections-completed` is set; `@connections-error` is
absent. The test subscribes to the tmp:// path through
`PubSub.Subscribe`; events fire for each terminal transition.

**Refs:** crc-Librarian.md, crc-CLI.md, seq-find-connections.md.

## Test: timeout flips status to errored

**Purpose:** Validates R2331. The orchestrator's deadline timer
flips a hung request to `errored` with `@connections-error:
timeout`.

**Input:** Enqueue with `opts.timeoutSeconds=1`. Do not post a
result.

**Expected:** Within ~1.2 s, the doc exposes
`@connections-status: errored`, `@connections-error: timeout`,
`@connections-completed` set, `@connections-progress: done`. A late
`SetConnectionsResult` after this point returns no error and the
doc state is unchanged (R2331 second sentence).

**Refs:** seq-find-connections.md (Timeout).

## Test: --fetch returns chunk content for valid IDs

**Purpose:** Validates R2316, R2341. The fetch payload matches the
indexed chunk content.

**Input:** Index two chunks across two files. Enqueue a request,
drain via `--wait`, call `--fetch` with the request ID.

**Expected:** JSON array of two objects, each carrying `chunkID`,
`fileID`, `path`, `content`. Content matches the on-disk chunk
exactly. Order matches the request's chunkIDs order.

**Refs:** crc-CLI.md, crc-Librarian.md.

## Test: --fetch errors on unknown chunkIDs

**Purpose:** Validates R2324 (unknown chunkIDs surface at fetch,
not enqueue) and R2341.

**Input:** Enqueue a request whose chunkIDs include one nonexistent
ID. Sidecar calls `--fetch`.

**Expected:** `--fetch` returns a non-zero exit with an error
message naming the unknown chunk ID. The orchestrator's downstream
`--error` call (driven by the sidecar) flips the doc to `errored`
with `@connections-error: unknown chunk <id>`.

**Refs:** crc-CLI.md.

## Test: --result rejects empty evidence

**Purpose:** Validates R2317, R2342. Protocol-violation handling.

**Input:** Enqueue a request, drain via `--wait`, post a `--result`
JSON payload where one theme entry has `Evidence: []`.

**Expected:** The HTTP handler returns 400. The doc flips to
`errored` with `@connections-error` naming the offending entry
(e.g. `"protocol: empty evidence in theme[0]"`).

**Refs:** crc-Librarian.md, seq-find-connections.md (Bad Sidecar
Output).

## Test: mcp.findConnections returns request ID, never blocks

**Purpose:** Validates R2322, R2323, R2325. Lua bridge contract.

**Input:** From a flib runtime test, call
`mcp.findConnections({c1, c2}, {timeoutSeconds=10})`. Time the call.

**Expected:** Returns a string requestID. Call duration <
1 millisecond (no sidecar wait). A second call with no opts also
returns a requestID. A call with `opts.timeoutSeconds=2000` results
in the orchestrator clamping to 300; verify via the timer fires at
~300 s in a fast-clock variant, or just by inspecting the
`@connections-* ` header on the doc carrying the clamped value if
the orchestrator records it. (Simpler variant: unit-test the clamp
function directly.)

**Refs:** crc-Server.md.

## Test: mcp.findConnections returns (nil, errstring) when unavailable

**Purpose:** Validates R2320, R2324. Bridge contract on no
available sidecar.

**Input:** flib runtime with no `--wait` consumer registered (no
calls to `Librarian.WaitForConnectionsRequest` or its HTTP
equivalent). Call `mcp.findConnections({c1, c2})`.

**Expected:** Returns `(nil, "agent unavailable")`. No tmp:// doc
is created.

**Refs:** crc-Server.md, crc-Librarian.md.

## Test: empty chunkIDs rejected at bridge

**Purpose:** Validates R2324.

**Input:** Call `mcp.findConnections({}, {})`.

**Expected:** Returns `(nil, "chunkIDs empty")`. No tmp:// doc, no
queue entry.

**Refs:** crc-Server.md.

## Test: subscription fires on terminal transition (test-as-subscriber)

**Purpose:** Validates R2333, R2339, and the substrate's
publish-only-on-change rule for the connections tag schema.

**Input:** Subscribe through `PubSub.Subscribe` with
`filter={tag: "connections-status", filterFiles: [...path...]}`.
Enqueue a request, drive `--result` from the test as the sidecar
stub.

**Expected:** `Listen` returns an event batch containing the
terminal `@connections-status: completed` event. Intermediate
`@connections-status: working` also surfaces. Repeating the same
elapsed-tick value across two updates does **not** generate an
event (R2284 / centralized publish only-on-change behavior).

**Refs:** seq-find-connections.md, seq-tmp-subscription.md.
