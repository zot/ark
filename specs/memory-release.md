# Memory release after bursts

**Problem.** Go's garbage collector frees a burst's memory but returns the pages
to the operating system only gradually, through its background scavenger. Measured
2026-10-25: re-chunking 2857 chat transcripts after a chunker version bump peaked
at 7.0 GB resident and still held 4.8 GB of anonymous memory, idle, two minutes
after the work ended; it fell to about 2 GB only around the three-minute mark.
Nothing in ark asked for the memory back.

**Release at the end of a burst.** After a burst of indexing or embedding, ark
forces a collection and returns freed pages to the operating system at once
(`debug.FreeOSMemory`). A forced collection stops the whole program while it runs,
so it happens only at quiet points and only after real bursts, never per file:
while a Claude Code session is live its transcript changes every few seconds, and
releasing on every change would cost more than it saves.

- **File scans.** ark counts the bytes it has chunked since the last release:
  the whole file for a full chunk, only the new bytes for an append. When the
  file-scan registry empties (no scan in flight) and the count has reached the
  scan threshold, ark releases and resets the count. The release runs off the
  DB actor, so the actor never waits on it.
- **Stale refresh.** A refresh of stale files (`ark refresh` and the refresh it
  runs for a command) releases when it finishes, if the bytes it chunked reached
  the same threshold.
- **Embedding.** After each embedding tier's batch, once the tier's model context
  is closed, ark releases if that tier embedded at least the embed threshold of
  chunks. Closing the context frees the model's C memory; the release returns
  the Go side.

The thresholds start at **256 MiB** chunked and **512** chunks embedded. Both
are first guesses, not measurements, and should be revisited once the `memory`
line below has been watched across a few bursts. Each release logs one line
naming what triggered it.

**Seeing it.** A served `ark status` reports a `memory:` line: the live Go heap
(what the last collection found reachable, the figure that says whether anything
is holding a burst), Go heap in use (which also counts garbage not yet swept, so
it swings between collections), Go heap idle but not yet returned, everything
Go holds from the operating system, and, on Linux, the process's resident size. Resident size well above
Go's total is memory Go cannot see, chiefly the embedding model's C allocations,
which a release does not touch. A status read without a server has no
long-running process to describe and omits the line. See
[status-enhanced.md](status-enhanced.md).

**Finding what holds it.** A release returns only what nothing references, so a
live heap that stays high after a burst is something holding on, and only a heap
profile names it. `ark status --heap FILE` asks the running server to collect and
then write its heap profile to FILE (read it with `go tool pprof`). It needs a
server: the CLI process has no history worth profiling, so without one the flag
refuses rather than profile itself. Measured 2026-10-25, the case that asked for
it: re-chunking 925 MB of transcripts left the live heap at 1.3 to 1.5 GB against
a 23 MB idle baseline, rising slowly after the burst ended.

**What held it: queue pops.** The first profile named the holder. Of 1.36 GB
live after that burst, 856 MB was file contents read by the scans and 386 MB
their trigram counts: every scan's prepared data, still reachable after its store
ran. The write queue popped its head with `q = q[1:]` and left the popped slot
pointing at the store closure, and Go keeps a slice's whole backing array, dead
slots included, reachable until an append reallocates it. On a quiet server that
can be a long time. So a queue that outlives its items clears the slot it pops
before advancing; the closure actors' own queue (`ChanSvc`) had the same pop.

Not done: a memory limit (`debug.SetMemoryLimit`), which would lower a burst's
peak rather than shorten its tail. It waits until the numbers above say what
limit would not make the collector thrash during a large re-chunk.
