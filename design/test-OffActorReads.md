# Test Design: Off-actor read views (HTTP handlers)
**Source:** crc-Server.md

Guards the O154 race class at its remaining off-actor entry points. The
readers themselves are safe when reached from inside the actor —
`InvalidateCaches` runs only there — so what these tests pin is the
*binding* at each off-actor entry point, and the shape of the view it
binds.

## Test: handleContentView reads don't race InvalidateCaches
**Purpose:** The content-view render subtree runs on the HTTP goroutine and reads the fts Go-side caches in five places (`ChunkIDByLocation`, `ChunkIDsForPath`, `ResolveLink` under `wrapTagElements`, `resolveFilePath` under `ExtRoutingsForTargetChunk`, `ChunkIDsForPath` inside `renderPdfChunksByPage`). Verify the handler's bound read view keeps all of them off the shared caches.
**Input:** A `&Server{db}` over a test DB with a source covering the test dir and on-disk content templates; one indexed markdown file. Concurrently: a writer goroutine driving no-op writes through `enqueueWrite` (so the actor's completion closure runs `InvalidateCaches` repeatedly) and two reader goroutines issuing `GET /content/<path>` — full-file and `?range=` — through `httptest`.
**Expected:** Race-clean under `-race`. Pre-fix (view aliased to the live `srv.db`) this reports a DATA RACE between `microfts2.(*DB).InvalidateCaches` and `lookupFileByPath` — verified 2026-07-18, which is what makes the green result meaningful.
**Refs:** crc-Server.md, crc-DB.md, R3165, R995

## Test: the content read view is private and complete
**Purpose:** A `-race` test only trips when timing lines up, so assert the view's structure directly. A refactor that dropped the `fts.Copy()`, or left the Searcher or ExtMap unbound, would silently restore the race class.
**Input:** `db.withFTS(db.fts.Copy())` over the same fixture.
**Expected:** the view's `fts` differs from the original; `search.fts` is rebound to the copy; `extmap` is carried (so @ext routings resolve without the live DB); `svc` is nil (reads-only, no actor); and `ChunkIDsForPath` still resolves through the view.
**Refs:** crc-DB.md, R3165
