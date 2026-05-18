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
| `sys`     | ark          | `Server.registerLuaFunctions` in `server.go`                          | Ark-specific Go-owned state; today only `sys.curation`         |
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
| `mcp.definedTags()`                               | `specs/tags.md`                          | Return every defined tag name with metadata                                                           |
| `mcp.chunkInfo(chunkID)`                          | `specs/curation-workshop-primitives.md`  | Return `{chunkID, fileID, path, range, byteStart, byteEnd, writable, commentSyntax}`                  |
| `mcp.chunkText(chunkID)`                          | `specs/curation-workshop-primitives.md`  | Return the chunk's text bytes (or `(nil, err)`)                                                       |
| `mcp.parseTagBlock(text)`                         | `specs/curation-workshop-primitives.md`  | Parse the leading tag block; returns `{tags, body}`                                                   |
| `mcp.extractTagValues(text, strategy)`            | `specs/curation-workshop-primitives.md`  | Extract every `@name: value` anywhere in `text` (mid-chunk + `@id`)                                   |
| `mcp.suggestExtLocator(chunkID)`                  | `specs/curation-workshop-primitives.md`  | Three-layer locator suggestion for `@ext:` authoring                                                  |
| `mcp.setExtTag(targetSpec, tag, value)`           | `specs/curation-workshop-primitives.md`  | Author an `@ext:` routing into the appropriate mirror file under `~/.ark/external/`                   |
| `mcp.removeExtTag(targetSpec, tag)`               | `specs/curation-workshop-primitives.md`  | Remove a previously authored `@ext:` routing                                                          |
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
