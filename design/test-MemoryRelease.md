# Test Design: MemoryRelease
**Source:** crc-DB.md, crc-Indexer.md, crc-CLI.md
**Requirements:** R3601-3603, R3605-3607

## Test: scan-burst release honors its threshold
**Purpose:** below `releaseAfterScanBytes` nothing is released; at it the count resets (R3602)
**Input:** zero-value DB with the count one under, then at, the threshold; `releaseAfterScanBurst`
**Expected:** count kept, then reset to 0
**Refs:** crc-DB.md
**Code:** memory_test.go
**Fire alarm:** drop the threshold check so every empty registry releases; red is "below threshold: count = 0, want it kept"
**Inject:** db.go:releaseAfterScanBurst
**Pulled:** 2026-10-25 — rang ("below threshold: count = 0, want it kept")

## Test: release waits for an empty registry
**Purpose:** a release never runs while a scan is in flight, so a live session's steady trickle of changes is not met with a full collection mid-burst (R3602)
**Input:** two stamped scans, count at the threshold; retire one, then the other
**Expected:** count kept after the first retire, reset after the last
**Refs:** crc-DB.md, seq-file-scan-actor.md
**Code:** memory_test.go
**Fire alarm:** release on every retire, not only when the registry empties; red is "released with a scan still in flight"
**Inject:** db.go:retireFileScanIfCurrent
**Pulled:** 2026-10-25 — rang ("released with a scan still in flight")

## Test: chunked bytes count what was chunked
**Purpose:** an append counts its new bytes, a full chunk the file, an unchanged skip nothing (R3602, R3603)
**Input:** scanPrep and refreshPrep in each shape
**Expected:** 7, 100, 0, 5, 50
**Refs:** crc-Indexer.md
**Code:** memory_test.go
**Fire alarm:** count the whole file for an unchanged refresh; red is "refresh unchanged = 50, want 0"
**Inject:** indexer.go:refreshPrep.chunkedBytes
**Pulled:** 2026-10-25 — rang ("refresh unchanged = 50, want 0")

## Test: memory status is read
**Purpose:** heap figures populated and consistent; Linux reports RSS (R3605)
**Input:** `memoryStatus()`
**Expected:** HeapInUse > 0, GoTotal ≥ HeapInUse, RSS > 0 on Linux
**Refs:** crc-DB.md
**Code:** memory_test.go
**Fire alarm:** look for a misspelled `/proc` key so RSS is never found; red is "linux: no resident size read"
**Inject:** memory.go:processRSS
**Pulled:** 2026-10-25 — rang ("linux: no resident size read")

## Test: status prints the memory line only when served
**Purpose:** one `memory:` line before `server:` when served, none otherwise (R3605)
**Input:** printStatus with and without a MemoryStatus
**Expected:** the line present and ordered; absent unserved
**Refs:** crc-CLI.md
**Code:** cmd/ark/status_memory_test.go
**Fire alarm:** print the line after `server:`; red is "memory line must come before server"
**Inject:** cmd/ark/main.go:printStatus
**Pulled:** 2026-10-25 — rang ("memory line must come before server")

## Test: the heap profile is written
**Purpose:** the server can be asked what holds its heap (R3606)
**Input:** `writeHeapProfile` to a temp path
**Expected:** a non-empty file
**Refs:** crc-Server.md
**Code:** memory_test.go
**Fire alarm:** return before writing the profile; red is "no heap profile written"
**Inject:** memory.go:writeHeapProfile
**Pulled:** 2026-10-25 — rang ("no heap profile written", a zero-size file)

## Test: the heap endpoint refuses a relative path
**Purpose:** the server never writes a profile relative to its own working directory (R3606)
**Input:** POST /status/heap with `heap.pprof`
**Expected:** 400
**Refs:** crc-Server.md
**Code:** memory_test.go
**Fire alarm:** drop the absolute-path check; red is "relative path: status 200, want 400"
**Inject:** server.go:handleStatusHeap
**Pulled:** 2026-10-25 — rang ("relative path: status 200, want 400")

## Test: --heap refuses without a server
**Purpose:** the CLI never profiles itself in place of the server (R3606)
**Input:** heapNeedsServer with and without a server and a file
**Expected:** refusal only for no server with a file
**Refs:** crc-CLI.md
**Code:** cmd/ark/status_memory_test.go
**Fire alarm:** always return nil; red is "no server + --heap: got <nil>, want the refusal"
**Inject:** cmd/ark/main.go:heapNeedsServer
**Pulled:** 2026-10-25 — rang ("got <nil>, want the refusal")

## Test: a popped closure stops being reachable
**Purpose:** a queue pop clears the vacated slot, so what a run closure captured can be collected while the queue lives on (R3607)
**Input:** a heap-held queue grown by append whose head closure captures a 1 MiB array; a weak pointer to the array; popFront; runtime.GC
**Expected:** the weak pointer is nil
**Refs:** crc-DB.md, crc-ChanSvc.md
**Code:** memory_test.go
**Fire alarm:** stop clearing the slot (`(*q)[0] = zero`); red is "popped closure's capture is still reachable through the queue". The first two drafts of this test never rang: the closure took `len(p)` of a fixed-size array, a constant, so it captured nothing.
**Inject:** util.go:popFront
**Pulled:** 2026-10-25 — rang ("popped closure's capture is still reachable through the queue")
