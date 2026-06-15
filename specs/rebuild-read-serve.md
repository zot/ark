# Rebuild Read-Only Serve

Go. Linux.

## Problem

`ark rebuild` drops and recreates the index, then re-scans every
source. It refuses to run while `ark serve` holds the database, because
it deletes and recreates the database file — so a rebuild is always a
standalone, no-server operation.

Under the bbolt storage engine the database is **single-process**: a
process that opens `index.db` holds an exclusive file lock for as long
as it keeps the database open, and a second process that tries to open
the same file **blocks** until the first releases it. LMDB was
multi-process — several processes could open the environment at once and
read consistent MVCC snapshots — so `ark status` in another terminal
returned immediately even while a rebuild ran. bbolt removes that.

The consequence: during a standalone rebuild, any other ark command that
needs the database — `ark status` to watch progress, `ark search` to
inspect the partial index — has no running server to proxy through, opens
the file directly, and **hangs** on the file lock until the rebuild
finishes. On a large corpus that is minutes of apparent silence.

## Behavior

During the scan phase of a rebuild, ark keeps a **read-only server**
listening on the unix socket. Read commands behave exactly as they do
against a normal running server: `ark status`, `ark search`, and the
other read endpoints find the socket, proxy to it, and return live
results — including the **growing** chunk and file counts as the rebuild
progresses. When indexing completes, the read-only server shuts down and
the rebuild command exits.

The rebuild's indexing runs through the same write path the normal server
uses, so:

- reads stay **responsive** — the heavy indexing work runs off the
  coordination actor, which stays free to answer reads between files;
- reads are **race-free** — they ride the actor like any server read, so
  they never touch the in-flight index caches unsafely;
- reads are **consistent** — each read sees a committed snapshot.

The read-only window is **read-only**. Write or mutation requests
(`add`, `remove`, config changes, tmp:// writes) that arrive during a
rebuild are refused with a "rebuild in progress" error rather than
racing the rebuild.

## Constrained server

The rebuild's server is the normal server with its **background
subsystems switched off**: no filesystem watcher, no scheduler, no
embedded UI engine, no recall watcher, no spectral-search librarian or
pubsub reaper. Only the database, the coordination actor, and the
read-only request handlers are active. The switches live in the serve
options (alongside the existing no-scan / force / compact options), each
defaulting to on for a normal `ark serve` and turned off for a rebuild.

A rebuild server does not block forever the way `ark serve` does. It runs
the scan once and exits when the **write queue has drained** — when every
file the scan enqueued has been indexed and committed. "Write queue
drained" is the completion signal the rebuild waits on.

## Drop window

The brief moment while `ark init` deletes and recreates the database
file — before the read-only server binds the socket — is not covered by
the read window. A read arriving in that sub-second window may block on
the file lock, as before. This is acceptable: the window is short and
self-clears.

## Out of scope

- A fail-fast open timeout that converts the drop-window block into an
  immediate "index locked" error. A possible future refinement; the
  window is brief enough not to need it now.
- Making `ark serve` itself exit-on-idle. The constrained,
  exit-when-done behavior is specific to rebuild.
- Concurrent writes during a rebuild. The window is strictly read-only.
