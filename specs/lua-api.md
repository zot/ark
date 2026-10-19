# Lua API

Canonical reference for the Lua globals and methods that ark, frictionless,
and ui-engine expose to Frictionless apps written in Lua. This document
lists every Go-side `SetField` on a public Lua table, the global it lives
on, and the per-feature spec that owns the deeper semantics.

This document describes the **current** state of the Lua surface. When the
Go code adds, renames, or removes any `SetField` on one of these globals,
update this document along with the per-feature spec. Mini-spec's per-
feature anchoring won't catch the cross-cutting reference on its own.

Language: Go (host) + Lua (apps). Environment: the Frictionless runtime
inside `ark serve` and `ark ui`.

## Globals introduced

| Global    | Owner        | Created by                                                            | Purpose                                                        |
|-----------|--------------|-----------------------------------------------------------------------|----------------------------------------------------------------|
| `mcp`     | frictionless | `Server.setupMCPGlobal` in `internal/mcp/tools.go`                    | App-facing MCP toolset; extended by ark with corpus primitives |
| `MCP`     | frictionless | `Server.setupMCPGlobal` (same file)                                   | Namespace for nested prototypes like `MCP.AppMenuItem`         |
| `sys`     | ark          | `Server.registerLuaFunctions` in `server.go`                          | Ark-specific Go-owned state and bridges into ark's own verbs   |
| `session` | ui-engine    | `LuaSession.createSessionTable` in `internal/lua/runtime.go`          | Per-session runtime: variables, prototypes, timers             |
| `ui`      | ui-engine    | `LuaSession.setupUIMod` (called by runtime setup)                     | Presenter registration + JSON helpers                          |
| `EMPTY`   | ui-engine    | runtime setup in `internal/lua/runtime.go`                            | Sentinel empty table used by `_objectToId` weak-key bookkeeping |
| `diag`    | ui-engine    | runtime setup                                                         | Diagnostic logging hook                                        |
| `print`   | ui-engine    | runtime setup (overrides built-in)                                    | Routed through the runtime's log channel                       |
| `require` | ui-engine    | runtime setup (overrides built-in)                                    | Hot-reload-aware module loader                                 |
| `package` | ui-engine    | runtime setup (overrides built-in)                                    | Companion to the custom `require`                              |

App-facing globals (`mcp`, `MCP`, `sys`, `session`, `ui`) are the
documented surface. The lower-case globals (`EMPTY`, `diag`, `print`,
`require`, `package`) exist because Lua programs rely on them being
present; their internals are not part of the app contract.

## `mcp` — created by frictionless, extended by ark

frictionless creates `mcp` in `Server.setupMCPGlobal`
(`/home/deck/work/frictionless/internal/mcp/tools.go`). It seeds the
table with the runtime-state methods every Frictionless app uses. After
frictionless seeds the table, ark's `Server.registerLuaFunctions`
(`/home/deck/work/ark/server.go`) extends the same table with corpus
primitives. Helpers each side calls — `registerSubscribeMethod` in
frictionless, none in ark — also live on this surface.

When adding a method here, decide which owner it belongs to (runtime vs
corpus) and edit the matching `SetField` site.

### Identity and runtime state (frictionless)

| Field            | Kind     | Description                                                    |
|------------------|----------|----------------------------------------------------------------|
| `mcp.type`       | string   | `"MCP"`; used by viewdef resolution                            |
| `mcp.value`      | any      | App-owned value slot (set by `mcp.app` / `mcp.display`)        |
| `mcp.sessionId`  | string   | The session's internal ID (resolved from `vendedID` at setup)  |

### Event push / poll (frictionless)

| Method                 | Source spec      | Behavior                                                                |
|------------------------|------------------|-------------------------------------------------------------------------|
| `mcp.pushState(event)` | `specs/mcp.md`   | Push a Lua table onto the session's MCP event queue; signal waiters     |
| `mcp.pollingEvents()`  | `specs/mcp.md`   | True iff a `/wait` client is currently connected                        |
| `mcp.waitTime()`       | `specs/mcp.md`   | Returns the session's accumulated wait time (number, milliseconds)      |

### App lifecycle (frictionless)

| Method                       | Behavior                                                                                                |
|------------------------------|---------------------------------------------------------------------------------------------------------|
| `mcp.app(name)`              | Load and switch to app `name` from `<baseDir>/apps/`; runs the app's `init.lua` if present              |
| `mcp.display(value)`         | Set `mcp.value` to `value` and tell the runtime to render it; one-shot view publish                     |
| `mcp.status()`               | Return a Lua table snapshot: `base_dir`, `version`, `url`, `mcp_port`, `sessions`                       |
| `mcp.reinjectThemes()`       | Re-run `InjectAllThemeBlocks(baseDir)` so theme CSS is reapplied after edits                            |
| `mcp.renderMarkdown(text)`   | Render markdown to HTML (the same renderer used by the runtime); returns HTML string                    |
| `mcp.subscribe(topic, fn)`   | Wire a Lua callback into the session's pub/sub channel. Defined in `internal/mcp/subscribe.go`          |

### Corpus search and curation (ark)

| Method                                            | Source spec                              | Behavior                                                                                              |
|---------------------------------------------------|------------------------------------------|-------------------------------------------------------------------------------------------------------|
| `mcp.search_grouped(req)`                         | `specs/ark-search.md`                    | Run a grouped search; returns `{ groups = [{path, strategy, chunks = [...]}, ...] }`                  |
| `mcp.open(path, range)`                           | `specs/cli-commands.md`                  | Open a file/chunk in the configured editor                                                            |
| `mcp.sort(rows, key, dir)`                        | —                                        | Pure-Lua-side sort helper exposed for app code                                                        |
| `mcp.parseJson(text)`                             | —                                        | Parse a JSON string into a Lua table                                                                  |
| `mcp.readJsonFile(path)`                          | —                                        | Read+parse a JSON file from disk                                                                      |
| `mcp.setTags(file, tags)`                         | `specs/tag-block-commands.md`            | Replace the leading tag block of `file` with `tags` (table of `{name, value}`); atomic write          |
| `mcp.readMessage(file)`                           | `specs/messaging.md`                     | Read a message file and return `{tags, html}`                                                         |
| `mcp.indexing()`                                  | —                                        | Return the list of source paths currently being indexed                                               |
| `mcp.listSource(name, glob)`                      | `specs/sources.md`                       | List indexed file paths within a source, optionally glob-filtered                                     |
| `mcp.chatSessions(opts)`                          | `specs/chat-transcript.md`               | Indexed conversations newest first as `{label, path, source, turns, modified}`; `opts` = `{source=, ["for"]=, limit=}`, same narrowing as `ark chats list` |
| `mcp.chatTranscript(path, opts)`                  | `specs/chat-transcript.md`               | Rendered markdown transcript for one session; `opts` = `{withTools=, withThinking=, sidechain=, lineLength=}`. Calls the same renderer `ark chats` uses, so CLI and UI output cannot drift |
| `mcp.definedTags()`                               | `specs/tags.md`                          | Return every defined tag name with metadata                                                           |
| `mcp.chunkInfo(chunkID)`                          | `specs/curation-workshop-primitives.md`  | Return `{chunkID, fileID, path, range, byteStart, byteEnd, writable, commentSyntax}`                  |
| `mcp.chunkText(chunkID)`                          | `specs/curation-workshop-primitives.md`  | Return the chunk's text bytes (or `(nil, err)`)                                                       |
| `mcp.parseTagBlock(text)`                         | `specs/curation-workshop-primitives.md`  | Parse the leading tag block; returns `{tags, body}`                                                   |
| `mcp.extractTagValues(text, strategy)`            | `specs/curation-workshop-primitives.md`  | Extract every `@name: value` anywhere in `text` (mid-chunk + `@id`)                                   |
| `mcp.suggestExtLocator(chunkID)`                  | `specs/curation-workshop-primitives.md`  | Three-layer locator suggestion for `@ext:` authoring                                                  |
| `mcp.setExtTag(targetSpec, tag, value)`           | `specs/curation-workshop-primitives.md`  | Author an `@ext:` routing into the appropriate mirror file under `~/.ark/external/`                   |
| `mcp.removeExtTag(targetSpec, tag)`               | `specs/curation-workshop-primitives.md`  | Remove a previously authored `@ext:` routing                                                          |
| `mcp.extAccept(targetSpec, tag, value, opts)`     | `specs/curation-workshop-primitives.md`  | One-shot candidate+accept on the DB actor; returns `{disposition}` actually applied                   |
| `mcp.extRemove(targetSpec, tag, opts)`            | `specs/curation-workshop-primitives.md`  | Disposition-dispatched remove: external mirror routings, or the inline line (no degrade)              |
| `mcp.extReject(targetSpec, tag, value)`           | `specs/curation-workshop-primitives.md`  | Reject matching `@ext-candidate`(s) — the durable signed `@ext-judgment`                              |
| `mcp.extAmend(sourceTvid, tag, value, opts)`      | `specs/curation-workshop-primitives.md`  | Revise the candidate line `sourceTvid` names, in place; omitted `opts` fields carry forward           |
| `mcp.extPending(opts)`                            | `specs/curation-workshop-primitives.md`  | Read the pending `@ext-candidate` ledger, ranked; `opts` = `{chunk, limit}`                           |
| `mcp.replaceRegion(path, byteStart, byteEnd, t)`  | `specs/curation-workshop-primitives.md`  | Atomically replace a byte range in an indexed file                                                    |
| `mcp.suggestTagNames(chunkText, ...)`             | `specs/suggest-tag-names.md`             | LLM-backed tag-name suggestions for a chunk                                                           |
| `mcp.chunksForTag(tag, opts)`                     | `specs/chunks-for-tag.md`                | Resolve a tag to candidate chunks                                                                     |
| `mcp.chunksForTagDef(tagDef, opts)`               | `specs/chunks-for-tag.md`                | Like above but driven by a tag definition                                                             |
| `mcp.topKChunksForTag(tag, k)`                    | `specs/chunks-for-tag.md`                | Top-k chunks ranked for `tag`                                                                         |
| `mcp.relatedTags(tag, opts)`                      | `specs/tag-overview.md`                  | Tags that co-occur with `tag`                                                                         |
| `mcp.tagPairConflict(tagA, tagB)`                 | `specs/hot-correlations.md`              | Diagnostic for known-conflicting tag pairs                                                            |
| `mcp.tagDrift(tag)`                               | `specs/hot-correlations.md`              | Drift signal for a tag's distribution over time                                                       |
| `mcp.sweepHotCorrelations()`                      | `specs/hot-correlations.md`              | Synchronous sweep; returns the result table                                                           |
| `mcp.sweepHotCorrelationsAsync()`                 | `specs/hot-correlations.md`              | Enqueue the sweep through the write actor; result reaches subscribers via `tmp://sweep/...`           |
| `mcp.findConnections(req)`                        | `specs/find-connections.md`              | LLM-orchestrated themes + shared-tag candidates from pinned chunks                                    |
| `mcp.subscribe(spec, fn)`                         | `specs/tmp-subscription.md`              | Subscribe a Lua callback to ark tag publications (distinct from the frictionless `mcp.subscribe`)     |
| `mcp.onpublish(fn)`                               | `specs/tmp-subscription.md`              | Register a publish callback for the session                                                           |
| `mcp.cancel(spec)`                                | `specs/tmp-subscription.md`              | Unsubscribe                                                                                           |

### Schedule and tag completion (ark)

| Method                                            | Source spec            | Behavior                                                                                                                       |
|---------------------------------------------------|------------------------|--------------------------------------------------------------------------------------------------------------------------------|
| `mcp.scheduled(startDate, endDate)`               | `specs/scheduling.md`  | Schedule events overlapping the range (via `EventScheduler.QueryRange`; endDate inclusive); `[{date, endDate, tag, summary, path, recurring, allDay}]` |
| `mcp.reschedule(path, tag, newDate, newEndDate)`  | `specs/scheduling.md`  | Rewrite the date in the `@tag` value (preserving trailing text), write back (disk or `tmp://` via the write actor), re-index; returns `{old, new}` |
| `mcp.tagComplete(prefix)`                         | `specs/scheduling.md`  | Tag-name completions with descriptions, or value completions when `prefix` is `"tag:valuePrefix"`                              |
| `mcp.fileStatus(path)`                            | `specs/scheduling.md`  | `{indexed, chunks=[{chunkID, tags=[{tag, value}]}], schedule=[...]}` — per-chunk tags read from F records, not a file rollup    |

### `tmp://` document overlay (ark)

| Method                                | Source spec               | Behavior                                                                              |
|---------------------------------------|---------------------------|---------------------------------------------------------------------------------------|
| `mcp.tmp_add(path, content)`          | `specs/tmp-documents.md`  | Create a `tmp://` document with `content`                                             |
| `mcp.tmp_update(path, content)`       | `specs/tmp-documents.md`  | Replace `tmp://` document content                                                     |
| `mcp.tmp_remove(path)`                | `specs/tmp-documents.md`  | Remove a `tmp://` document                                                            |
| `mcp.tmp_list(prefix)`                | `specs/tmp-documents.md`  | List `tmp://` documents (optional prefix filter)                                      |
| `mcp.tmp_get(path)`                   | `specs/tmp-documents.md`  | Read `tmp://` document content                                                        |
| `mcp.inbox(opts)`                     | `specs/cli-commands.md`   | Inbox view (same data the `ark message inbox` CLI shows)                              |

### Two `subscribe`s — careful

`mcp.subscribe` exists in two layers: frictionless registers a generic
session-event subscriber (`internal/mcp/subscribe.go`); ark overwrites or
augments the field with its tag-driven subscriber documented in
`specs/tmp-subscription.md`. Whichever is set last wins; today ark's
extension runs after frictionless's setup, so ark's subscriber is the
visible one for ark-served Frictionless apps. If the layering changes,
update this paragraph along with the relevant per-feature spec.

## `MCP` — namespace (frictionless)

`MCP` is an empty Lua table created alongside `mcp`. App code uses it as
a namespace for nested prototypes:

```lua
MCP.AppMenuItem = session:prototype(...)
```

Frictionless seeds nothing into it; everything under `MCP` is set by Lua
app code at runtime.

## `sys` — ark-owned state

`sys` lives in ark's `Server.registerLuaFunctions`. Today it carries
only the curation subtable. Future ark-owned Lua-visible state belongs
under `sys`, not `mcp` — `mcp` is shared with frictionless, while `sys`
is ark's namespace.

| Field                              | Kind     | Description                                                                             |
|------------------------------------|----------|-----------------------------------------------------------------------------------------|
| `sys.curation.pinned`              | table    | Lua-side mirror of the Go-owned pinned set (Frictionless watches this)                  |
| `sys.curation.pin(chunkID, ...)`   | method   | Pin a chunk; updates the Go slice and refreshes the mirror in the same tick. R2356     |
| `sys.curation.dismiss(chunkID)`    | method   | Unpin a chunk                                                                           |
| `sys.curation.sweepOlder()`        | method   | Drop pinned entries older than the configured cutoff                                    |
| `sys.findConnections(inputs, opts)` | method  | Unified find-connections entry (normal + turbo). Returns `(requestID, nil)` or `(nil, err)`. R2567, R2600 |
| `sys.recall(inputs, opts)`         | method   | Retrieve the top-K chunks relevant to `inputs` via `Librarian.Recall`. R2628, R2660      |
| `sys.discussed.{add,list,clear,prune}` | methods | Mirror the `ark discussed` CLI verbs, per session. R2661                              |
| `sys.luhmannLaunch(bootstrap?, model?)` | method   | **Start** a managed-pty launch and return at once — `(true, nil)` accepted, `(nil, err)` if one is already hosted. **Asynchronous by necessity**: the launch waits out the seat claim, and blocking here would freeze the UI it serves, so the return value is the *acceptance* and the outcome arrives as a publication on `tmp://luhmann/launch.md` instead (`@luhmann-hosted:` / `@luhmann-launch-failed:`). `model` names the model for this launch — the same parameter the CLI `--model` flag sets, falling back to `[luhmann] model` then the compiled-in default — so a model chooser in a view needs no Go work of its own. Never starts a session unbidden — the caller is the app's paid-session confirm dialog. R3325, R3326, R3332 |
| `sys.checkForUpdate()`             | method   | **Start** a background check for a newer ark release (`ark update --check`, no shell) and return at once; the result shows up in `sys.updateStatus()`. Downloads nothing. See `specs/main.md`, "A requested update" |
| `sys.requestUpdate()`              | method   | **Start** staging the update the user asked for (`ark update --stage`, no shell) and return at once. The download lands as `~/.ark/ark.new` and takes effect at the next `ark serve`; the server never restarts itself |
| `sys.updateStatus()`               | method   | Where an update stands, as a table: `current` (running version), `latest` (or `""` before a check), `newer` (bool), `state` (`idle`, `checking`, `downloading`, `staged`, `failed`), `error` (message when `failed`). Cheap and synchronous, for the UI to poll while a check or download runs; `staged` also covers a request left from an earlier session |

## `session` — ui-engine per-session table

`LuaSession.createSessionTable` (`/home/deck/work/ui-engine/internal/lua/runtime.go`)
builds the `session` global by:

1. Calling Lua's `Session.new(vendedID)` (defined in ui-engine's Lua
   stdlib) if present; falling back to a Go-built table otherwise.
2. Setting bookkeeping fields directly: `_sessionID`, `reloading`,
   `_variables`, `_watchers`, `_objectToId`.
3. Calling `injectSessionFunctions(session, vendedID)` — wires Go
   callbacks into Lua-side `_set*Fn` setters defined by `Session.new`.
   These callbacks are internal (`_setGetValueFn`, `_setGetPropertyFn`,
   `_setCreateFn`, etc.) and not part of the app contract.
4. Calling `addGoSessionMethods(session, vendedID)` — adds the public
   methods listed below directly.

### Internal state fields

| Field             | Kind      | Description                                                                |
|-------------------|-----------|----------------------------------------------------------------------------|
| `_sessionID`      | string    | Vended session ID                                                          |
| `reloading`       | boolean   | True while a hot-reload is in flight                                       |
| `_variables`      | table     | id → variable for the session's variable store                             |
| `_watchers`       | table     | id → watcher callback                                                      |
| `_objectToId`     | table     | weak-key map: Lua object → variable id                                     |

### Variable lifecycle (`addGoSessionMethods`)

| Method                                          | Behavior                                                            |
|-------------------------------------------------|---------------------------------------------------------------------|
| `session:createAppVariable(obj)`                | Create the per-app top-level variable; backs `mcp.value`             |
| `session:getApp()`                              | Return the current app variable (or nil)                            |
| `session:createVariable(parentId, obj, ...)`    | Create a variable bound to a parent; assigns a fresh id              |
| `session:destroyVariable(id)`                   | Drop a variable and its watchers                                    |
| `session:newVersion(id)`                        | Bump the variable's version so watchers refire                       |
| `session:getVersion(id)`                        | Read the current version (number)                                   |
| `session:needsMutation(id)`                     | True if the variable's mutable copy is stale                        |

### Prototypes and instances

| Method                                  | Behavior                                                                  |
|-----------------------------------------|---------------------------------------------------------------------------|
| `session:prototype(name, def)`          | Register a prototype `name` with the given definition table; `def.__index` is set; `def.new` returns a fresh instance |
| `session:create(name, ...)`             | Shorthand for `prototype.new(...)` with extra runtime bookkeeping         |
| `session:removePrototype(name)`         | Unregister a prototype                                                    |

### Module hot-reload

| Method                              | Behavior                                                                       |
|-------------------------------------|--------------------------------------------------------------------------------|
| `session:unloadModule(name)`        | Drop a single module from `package.loaded` and the runtime's tracking          |
| `session:unloadDirectory(prefix)`   | Drop every module whose name starts with `prefix`                              |

### Timers

| Method                                 | Behavior                                                                   |
|----------------------------------------|----------------------------------------------------------------------------|
| `session:setImmediate(fn)`             | Run `fn` on the next runtime tick; returns a clear-token                   |
| `session:setTimeout(fn, ms)`           | Run `fn` after `ms` milliseconds                                           |
| `session:setInterval(fn, ms)`          | Run `fn` every `ms` milliseconds                                           |
| `session:clearImmediate(tok)`          | Cancel a pending immediate                                                 |
| `session:clearTimeout(tok)`            | Cancel a pending timeout                                                   |
| `session:clearInterval(tok)`           | Cancel a recurring interval                                                |

`clearImmediate`, `clearTimeout`, and `clearInterval` share one Go
closure — any cancel-token works with any of them.

## `ui` — ui-engine presenter + helpers

`ui` is created by ui-engine's runtime setup. It is the bridge for app
code that defines presenter types or needs language-level helpers the
host owns.

| Method                                       | Behavior                                                                |
|----------------------------------------------|-------------------------------------------------------------------------|
| `ui.registerPresenter(name, methods)`        | Register a presenter type; `methods` is a Lua table of methods           |
| `ui.registerWrapper(name, init)`             | Register a wrapper around a presenter type                              |
| `ui.log([level,] message)`                   | Log through the runtime's log channel; level defaults to 0               |
| `ui.json_encode(value)`                      | Encode a Lua value to a JSON string                                     |
| `ui.json_decode(text)`                       | Decode a JSON string to a Lua value                                     |
| `ui.viewdefCollisions()`                     | The live viewdef-name conflicts: array of `{key, winner, winnerPath, loser, loserPath}` (same list as `ark ui conflicts`) |
| `ui.mkdir(path)`                             | Create a directory and its missing parents; existing is success. `true` or `(nil, err)` |
| `ui.isDir(path)`                             | `true` if `path` is a directory, following links and junctions; `false` otherwise, never an error |
| `ui.listDir(path)`                           | Entries sorted by name, hidden included: array of `{name, isDir}` (`isDir` follows links), or `(nil, err)` |
| `ui.base64Decode(text)`                      | Standard base64 to a byte string, or `(nil, err)`                       |
| `ui.exec(argv)`                              | Run `argv[1]` with the rest as arguments, **no shell**; returns stdout, or `(nil, err)` with the exit status and stderr. Blocks the session while it runs |
| `ui.platform()`                              | `{os, arch}` from the server's `GOOS` / `GOARCH`                        |

The six functions after `viewdefCollisions` are the portable OS primitives
(ui-engine `specs/interfaces.md`, R146–R151 there). Apps use them instead of
`os.execute` / `io.popen`, because `mkdir -p`, `ls`, `test -d`, `base64 -d` and
POSIX shell syntax (`VAR=x cmd`, `2>/dev/null`) do not exist on Windows.

## Cross-cutting concerns

### When adding a Lua method, also update

1. The per-feature spec that owns the method (see the Source spec column
   above for established methods).
2. `specs/lua-api.md` (this file) — add the new field to the right
   global's table.
3. `design/requirements.md` — add an R-number for the new method.
4. The relevant CRC card (typically `crc-Server.md` for `mcp.*`,
   `crc-Curation.md` for `sys.curation.*`).

### When renaming or removing a Lua method

1. Retire the old R-number via `minispec update retire R<old> R<new>`.
2. Update every site that calls the method in Lua app code.
3. Update this document and the per-feature spec.

### Don't conflate `sys` and `mcp`

`mcp` is the shared surface frictionless ships and ark extends. App code
that runs under either ark or frictionless can call any `mcp.*` method
that's defined in the layer it's running under.

`sys` is ark-only. App code that uses `sys.*` will not run under a
plain frictionless server — make that an explicit constraint in the
app's setup or guard with `if sys then ...`.
