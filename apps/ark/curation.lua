-- Curation view (vocabulary-maintenance workshop)
-- Loaded via require("ark.curation") from app.lua
--
-- Architecture: the canonical pinned-chunks store is in Go
-- (srv.curation.pinned). Lua sees it through sys.curation.pinned,
-- a host-mirrored table refreshed inside the same Lua-executor
-- closure that mutates the Go slice. Each entry has chunkID,
-- fileID, path, pinnedAt fields.
--
-- The pinned list binding uses Frictionless's itemWrapper to layer
-- a per-item presenter (Ark.PinnedChunk) over each host-mirrored
-- entry. The presenter carries reactive UI state (suggestions,
-- error flags); the entry table is a pure data mirror. See
-- ~/.ark/patterns/itemwrapper-presenters.md (or `ark ui patterns`).
--
-- sys.curation provides pin/dismiss/sweepOlder mutators backed by
-- the Go write actor; refreshLuaTable preserves per-entry identity
-- so presenters survive mutations.

local SUGGESTIONS_K = 8
local TOP_CHUNKS_K = 12
local RELATED_TAGS_K = 8

local SWEEP_DOC_PATH = "tmp://sweep/hot-correlations.md"
local CONNECTIONS_DOC_PREFIX = "tmp://connections/"

local function nowSeconds()
    return os.time()
end

local function fmtScore(s)
    return string.format("%.2f", s or 0)
end

-- JSON decoder bound at module load. Frictionless ships rxi/json
-- under apps/mcp/json.lua; we reuse it across apps via
-- require("mcp.json").
local json_lib = require("mcp.json")
jsonDecode = function(s)
    if not s or s == "" then return {} end
    local ok, result = pcall(json_lib.decode, s)
    if not ok then return {} end
    return result
end
jsonEncode = function(v)
    local ok, result = pcall(json_lib.encode, v)
    if not ok then return "[]" end
    return result
end

-- Fold helpers for applyPendingsToText. Operate on string-as-bytes;
-- no UTF-8 awareness needed (tag lines are ASCII).

-- Return the byte range [a, b) of the line beginning with `@tag:`
-- (case-sensitive) in `text`. Matches only the leading tag block —
-- lines after the first blank line are not searched. Returns nil if
-- not found.
function findTagLine(text, tag)
    if not text or text == "" then return nil end
    local prefix = "@" .. tag .. ":"
    local pos = 1
    while pos <= #text do
        local lineEnd = text:find("\n", pos, true)
        local line
        if lineEnd then
            line = text:sub(pos, lineEnd - 1)
        else
            line = text:sub(pos)
        end
        -- End of leading tag block: a blank line breaks it.
        if line:match("^%s*$") and pos > 1 then
            return nil
        end
        if line:sub(1, #prefix) == prefix then
            return pos, (lineEnd or #text) + 1
        end
        if not lineEnd then break end
        pos = lineEnd + 1
    end
    return nil
end

function replaceTagLine(text, tag, newValue)
    local a, b = findTagLine(text, tag)
    if not a then return text end
    local replacement = "@" .. tag .. ": " .. (newValue or "") .. "\n"
    return text:sub(1, a - 1) .. replacement .. text:sub(b)
end

function removeTagLine(text, tag)
    local a, b = findTagLine(text, tag)
    if not a then return text end
    return text:sub(1, a - 1) .. text:sub(b)
end

-- Prepend a new "@tag: value\n" line at the top of the leading tag
-- block. If the text starts with @tag lines, the new line goes
-- above the first one. Otherwise it goes at the very top.
function prependTag(text, tag, value)
    local line = "@" .. tag .. ": " .. (value or "") .. "\n"
    if not text or text == "" then return line end
    -- Always insert above existing tag block (or body).
    return line .. text
end

----------------------------------------------------------------------
-- Prototypes
----------------------------------------------------------------------

Ark.Curation = session:prototype("Ark.Curation", {
    focusedTag = "",
    _focusInput = "",
    _focusedChunks = EMPTY,
    _focusedRelated = EMPTY,
    _focusedDrift = EMPTY,
    _focusError = "",
    -- isNew threshold: a pin is NEW if its pinnedAt > _newCutoff.
    -- _lastViewedAt rotates into _newCutoff on each onViewOpen.
    _newCutoff = 0,
    _lastViewedAt = 0,
    -- Async sweep state. _sweepBusy is true between sweepNow() and the
    -- terminal @sweep-status event; _sweepProgress carries the live
    -- @sweep-progress value; _sweepResult is the final summary.
    _sweepBusy = false,
    _sweepProgress = "",
    _sweepResult = "",
    -- Tag picker: lazy-loaded list of all defined tags via
    -- mcp.definedTags(). Each entry is { tag = "...", description = "..." }.
    _definedTags = EMPTY,
    _definedTagsLoaded = false,
    -- Find Connections orchestration. Set when findConnections() is in
    -- flight; cleared on dismiss or new request.
    _connRequestID = "",
    _connStatus = "",
    _connProgress = "",
    _connElapsed = 0,
    _connError = "",
    _connThemes = EMPTY,
    _connSharedTags = EMPTY,
    -- Accept warning dialog visibility (M > 0 pending pendings).
    _acceptWarnVisible = false,
    -- Presenter registry: chunkID -> Ark.PinnedChunk. Lets us reach a
    -- card's per-card state (widget stack, edit-mode flags) without
    -- walking the ViewList. Cleared on dismiss; entries for sweept-
    -- away pins get cleaned out on the next pendingCount() pass.
    _presenters = EMPTY,
})
Curation = Ark.Curation

-- Presenter (itemWrapper) for each entry in sys.curation.pinned.
-- See ~/.ark/patterns/itemwrapper-presenters.md (or `ark ui patterns`).
-- The presenter carries the per-card workshop state: pending widget
-- stack, staged-ops buffer (ready for Accept), lazy chunkInfo, lazy
-- tag suggestions.
Ark.PinnedChunk = session:prototype("Ark.PinnedChunk", {
    viewItem = EMPTY,
    -- Bridge-fire nonce. Bump whenever the iframe or editor bridge
    -- JS needs to re-fire. The bridge methods embed this in the
    -- emitted string so the engine's change-detection sees a fresh
    -- value and re-evaluates ui-code.
    _bridgeNonce = 0,
    -- Widget stack (empty-start invariant: always at least one widget
    -- when not in edit mode; hidden during edit mode).
    _widgets = EMPTY,
    -- Lazy chunkInfo (mcp.chunkInfo): {chunkID, fileID, path, range,
    -- byteStart, byteEnd, writable, commentSyntax}
    _chunkInfo = EMPTY,
    _chunkInfoLoaded = false,
    _chunkInfoError = "",
    -- Lazy chunk text (mcp.chunkText). Populated on [edit] click or
    -- when the current-tags collapsible first needs the inline tags.
    _chunkText = "",
    _chunkTextLoaded = false,
    _chunkTextError = "",
    -- Edit-mode state.
    _editing = false,
    _chunkOriginalText = "",      -- snapshot at [edit] time; dirty check
    _isChunkEdited = false,        -- JS-pushed: editor.getDoc() != original
    _savedPendings = EMPTY,        -- inline widgets folded at [edit]; restored on [revert]
    _savedEditorText = "",         -- editor draft preserved at [revert]; consumed by next [edit]
    _editorContent = "",           -- JS-synced editor text. Read by Accept.
    -- Editor command queue. Each entry is a structured table like
    --   {kind="replace-tag-line", name, occurrence, newName, newValue}
    --   {kind="remove-tag-line",  name, occurrence}
    --   {kind="insert-tag",       name, value}
    -- Drained by the editor bridge JS on each fire; cleared by Lua
    -- once dispatched. Used to apply current-tags row edits as CM6
    -- transactions that are individually undoable.
    _pendingEditorCmds = EMPTY,
    -- currentTagsView() result cache. Keyed by a signature of the
    -- inputs (editor content + ext-tag list + widget list). When the
    -- signature is unchanged across calls, the cached row list is
    -- returned verbatim — preserving Lua identity for ViewList so
    -- DOM presenters are reused.
    _currentTagsViewSig = "",
    _currentTagsViewRows = EMPTY,
    -- Per-row reuse table: key = `kind:name:occurrence` →
    -- Ark.CurrentTagRow. Even when the signature changes (text
    -- updated), rows with matching keys are reused; only their
    -- text-derived fields update. The row's _lastSyncedValue
    -- determines whether re-derivation may overwrite value (no
    -- overwrite if user has typed since last sync — preserves the
    -- in-progress edit through cycles).
    _currentTagRowCache = EMPTY,
    _foldedText = "",              -- pre-computed fold result; published to JS via hidden span
    -- Ext-tag cache: scraped from iframe's <ark-ext-tags> children on
    -- load. Persists across edit-mode transitions so current-tags
    -- continues to surface ext rows without the iframe.
    _extTags = EMPTY,
    -- Inline-tag cache: scraped from the iframe's body-level <ark-tag>
    -- elements (those NOT inside <ark-ext-tags>). Catches @id and any
    -- mid-chunk @name: value tags that ParseTagBlock misses (it only
    -- handles the leading-block tags).
    _inlineScrapedTags = EMPTY,
    _extTagsLoaded = false,
    -- Lazy tag suggestions (mcp.suggestTagNames) — surfaces under the
    -- "tag scores" collapsible.
    _suggestions = EMPTY,
    _suggestionsLoaded = false,
    _suggestionsError = "",
    -- Per-card Accept error
    _acceptError = "",
    -- Confirm-dismiss UI flag (set when dismiss is called with pending > 0)
    _confirmDismiss = false,
})
PinnedChunk = Ark.PinnedChunk

-- One pending tag operation in a PinnedChunk's widget stack. Each
-- widget authors a single (tag, value) pair against the parent
-- chunk: add/change (default) or remove (when removeMode); inline
-- text edit (default) or routed via @ext mirror (when extMode).
Ark.PendingWidget = session:prototype("Ark.PendingWidget", {
    _chunk = EMPTY,                -- parent Ark.PinnedChunk
    tagName = "",
    tagValue = "",
    removeMode = false,
    extMode = false,
    -- Ext-mode fields, populated on first toggleExt() via
    -- mcp.suggestExtLocator. The widget reads `locatorText` from the
    -- Go result (`locator` field has a known bug — same value as
    -- locatorKind; tracked as a /mini-spec follow-up).
    extBase = "",                  -- "uuid" | "path"
    extBaseValue = "",             -- UUID string or absolute path
    extLocatorKind = "",           -- "string" | "regex" | "absolute" | "bare"
    extLocatorText = "",           -- locator value (empty for bare)
    _extScopeChunks = 0,
    _extScopeFiles = 0,
    _withinFileDupCount = 0,
    _extLoaded = false,            -- true after first suggestExtLocator
})
PendingWidget = Ark.PendingWidget

Ark.TagSuggestion = session:prototype("Ark.TagSuggestion", {
    tag = "",
    score = 0,
    motivatingFiles = EMPTY,
})
TagSuggestion = Ark.TagSuggestion

Ark.HotChunk = session:prototype("Ark.HotChunk", {
    chunkID = 0,
    fileID = 0,
    path = "",
    score = 0,
})
HotChunk = Ark.HotChunk

Ark.RelatedTag = session:prototype("Ark.RelatedTag", {
    tag = "",
    score = 0,
    srcPath = "",
    dstPath = "",
})
RelatedTag = Ark.RelatedTag

Ark.DriftPair = session:prototype("Ark.DriftPair", {
    pathA = "",
    pathB = "",
    score = 0,
})
DriftPair = Ark.DriftPair

-- Presenter (itemWrapper) for each row in the tag picker.
Ark.DefinedTag = session:prototype("Ark.DefinedTag", {
    viewItem = EMPTY,
})
DefinedTag = Ark.DefinedTag

-- Current-tags row: desired-state view of a single tag on a pinned
-- chunk. Inline rows (from mcp.parseTagBlock of the chunk text) and
-- ext rows (from the iframe <ark-ext-tags> scrape) render with the
-- same shape. status carries the pending-op overlay: "" (no pending),
-- "added", "changed", "removed".
Ark.CurrentTagRow = session:prototype("Ark.CurrentTagRow", {
    name = "",
    value = "",
    kind = "inline",          -- "inline" | "ext"
    -- Plain boolean mirroring `kind == "ext"`. Bound to the
    -- sl-checkbox via `ui-value`, which the engine sets and reads
    -- as a true boolean — no presence-attribute ambiguity. Synced
    -- from kind in reuseOrCreate; user clicks fire `setExtToggle`
    -- which routes through toggleExt's transition logic.
    extChecked = false,
    -- 1-based ordinal of this (name) among same-name occurrences in
    -- the chunk text. The Nth row with name=X corresponds to the
    -- Nth `@X:` line in the editor. Lets us route per-row edits to
    -- the exact line when multiple tags share a name.
    occurrence = 1,
    externalfile = "",        -- ext only: mirror file path
    externaltarget = "",      -- ext only: TARGET spec
    status = "",              -- "" | "added" | "changed" | "removed"
    -- Last value this row pushed to the editor (or saw from text on
    -- creation). `value` is updated by ui-value writeback on every
    -- keystroke; `_lastSyncedValue` is updated when we apply the
    -- edit. The two diverge while the user is typing — that
    -- divergence drives onValueChange's incremental-search style
    -- pump.
    _lastSyncedValue = "",
    -- Focus state of the value input. Set by sl-focus / sl-blur
    -- handlers. While true, re-derivation skips value overwrites
    -- (preserving the user's in-progress cursor + typing). Once
    -- false, external CM6 changes (like Ctrl-Z undo) flow back
    -- into the row.
    _inputHasFocus = false,
    _chunk = EMPTY,           -- back-reference for edit-mode mutations
})
CurrentTagRow = Ark.CurrentTagRow

-- Ext-tag row scraped from the iframe's <ark-ext-tags> element on
-- chunk-text-iframe load. Cached on the parent PinnedChunk so it
-- survives edit-mode iframe teardown.
Ark.ExtTagRow = session:prototype("Ark.ExtTagRow", {
    name = "",
    value = "",
    externalfile = "",
    externaltarget = "",
    -- True when the user has converted this ext entry to inline.
    -- The row vanishes from the current-tags display while hidden;
    -- re-toggling ext on the now-inline row restores it.
    _hidden = false,
})
ExtTagRow = Ark.ExtTagRow

-- Theme proposal from the ark-connections sidecar. Rendered in the
-- Find Connections panel.
Ark.ConnectionTheme = session:prototype("Ark.ConnectionTheme", {
    text = "",
    evidence = EMPTY,         -- chunk ID list
})
ConnectionTheme = Ark.ConnectionTheme

-- Shared-tag candidate from the sidecar. Each row has a [Fill]
-- button that injects pre-filled pending widgets into evidence chunks.
Ark.ConnectionSharedTag = session:prototype("Ark.ConnectionSharedTag", {
    tag = "",
    value = "",
    evidence = EMPTY,         -- chunk ID list
    _curation = EMPTY,        -- back-reference for fill action
    _index = 0,               -- index in _connSharedTags (for fillProposal)
})
ConnectionSharedTag = Ark.ConnectionSharedTag

----------------------------------------------------------------------
-- Curation methods
----------------------------------------------------------------------

function Curation:new(instance)
    instance = session:create(Curation, instance)
    -- Table fields default to nil via EMPTY; init them so iteration is safe.
    instance._focusedChunks = {}
    instance._focusedRelated = {}
    instance._focusedDrift = {}
    instance._presenters = {}
    return instance
end

-- Lazy-load the corpus's defined-tag list. The Go bridge returns
-- a deduped, alphabetically-sorted array of { tag, description }.
function Curation:loadDefinedTags()
    if self._definedTagsLoaded then return end
    self._definedTagsLoaded = true
    local defs, _ = mcp.definedTags()
    self._definedTags = defs or {}
end

-- Filtered view over _definedTags. Element identity is preserved across
-- filter changes (each entry is a stable table) so the picker's
-- ViewList presenters survive when the filter narrows.
function Curation:filteredDefinedTags()
    self:loadDefinedTags()
    local raw = self._focusInput or ""
    local filter = raw:lower():gsub("^%s+", ""):gsub("%s+$", "")
    local src = self._definedTags or {}
    if filter == "" then return src end
    local out = {}
    for _, d in ipairs(src) do
        if d.tag:lower():find(filter, 1, true) then
            table.insert(out, d)
        end
    end
    return out
end

function Curation:filteredDefinedTagCount()
    return #self:filteredDefinedTags()
end

-- Method accessor that the viewdef binds to. Returns the host-mirrored
-- array directly; identity of the array is irrelevant — ViewList
-- compares per-element identity (view.BaseItem == item) for presenter
-- reuse.
function Curation:pinned()
    return sys.curation.pinned
end

-- Public entry: always-add, never-flip. Re-pinning bumps to top.
function Curation:curate(chunkID, fileID, path)
    sys.curation.pin(tonumber(chunkID) or 0, tonumber(fileID) or 0, path or "")
end

function Curation:sweepOlder()
    sys.curation.sweepOlder()
end

function Curation:pinnedCount()
    return #sys.curation.pinned
end

function Curation:newCount()
    local n = 0
    local cutoff = self._newCutoff or 0
    for _, p in ipairs(sys.curation.pinned) do
        if p.pinnedAt > cutoff then n = n + 1 end
    end
    return n
end

function Curation:noNew()
    return self:newCount() == 0
end

function Curation:hasPinned()
    return #sys.curation.pinned > 0
end

function Curation:noPinned()
    return #sys.curation.pinned == 0
end

function Curation:onViewOpen()
    -- Rotate: the timestamp from the *previous* view-open becomes the
    -- NEW cutoff for this session. Pins added since then show NEW
    -- throughout this session; they roll off on the next view-open.
    self._newCutoff = self._lastViewedAt or 0
    self._lastViewedAt = nowSeconds()
end

-- Tag focus ---------------------------------------------------------

function Curation:focusTagFromInput()
    local tag = self._focusInput or ""
    tag = tag:gsub("^%s+", ""):gsub("%s+$", "")
    if tag == "" then return end
    self:focusTag(tag)
end

function Curation:focusTag(tag)
    self.focusedTag = tag
    self._focusError = ""
    self._focusInput = tag

    local hot, hotErr = mcp.topKChunksForTag(tag, TOP_CHUNKS_K)
    if hotErr then
        self._focusError = hotErr
        hot = {}
    end
    -- Fall back to live ChunksForTag when the cached path returned nothing.
    -- The cache may be empty for a brand-new tag, or stale on the very
    -- first sweep after a corpus change.
    if (not hot or #hot == 0) and self._focusError == "" then
        local live, liveErr = mcp.chunksForTag(tag, TOP_CHUNKS_K)
        if liveErr then
            self._focusError = liveErr
        else
            hot = live or {}
        end
    end
    local hotList = {}
    for _, h in ipairs(hot or {}) do
        local hc = session:create(HotChunk)
        hc.chunkID = tonumber(h.chunkID) or 0
        hc.fileID = tonumber(h.fileID) or 0
        hc.path = h.path or ""
        hc.score = tonumber(h.score) or 0
        table.insert(hotList, hc)
    end
    self._focusedChunks = hotList

    local related, relErr = mcp.relatedTags(tag, RELATED_TAGS_K)
    if relErr and self._focusError == "" then
        self._focusError = relErr
    end
    local relList = {}
    for _, r in ipairs(related or {}) do
        local rt = session:create(RelatedTag)
        rt.tag = r.tag or ""
        rt.score = tonumber(r.score) or 0
        rt.srcPath = r.srcPath or ""
        rt.dstPath = r.dstPath or ""
        table.insert(relList, rt)
    end
    self._focusedRelated = relList

    local drift, driftErr = mcp.tagDrift(tag)
    if driftErr and self._focusError == "" then
        self._focusError = driftErr
    end
    local driftList = {}
    for _, d in ipairs(drift or {}) do
        local dp = session:create(DriftPair)
        dp.pathA = d.pathA or ""
        dp.pathB = d.pathB or ""
        dp.score = tonumber(d.score) or 0
        table.insert(driftList, dp)
    end
    self._focusedDrift = driftList
end

function Curation:clearFocus()
    self.focusedTag = ""
    self._focusInput = ""
    self._focusError = ""
    self._focusedChunks = {}
    self._focusedRelated = {}
    self._focusedDrift = {}
end

function Curation:isFocused()     return self.focusedTag ~= "" end
function Curation:notFocused()    return self.focusedTag == "" end
function Curation:focusError()    return self._focusError end
function Curation:noFocusError()  return self._focusError == "" end
function Curation:focusedChunkCount()   return #self._focusedChunks end
function Curation:focusedRelatedCount() return #self._focusedRelated end
function Curation:focusedDriftCount()   return #self._focusedDrift end

-- Sweep (async retrofit) ------------------------------------------

-- Fire-and-forget. Calls mcp.sweepHotCorrelationsAsync and subscribes
-- to tmp://sweep/hot-correlations.md for live status/progress events.
-- The header reflects sweepBusy() throughout; sweepStatusText() shows
-- live progress.
function Curation:sweepNow()
    if self._sweepBusy then return end
    self._sweepBusy = true
    self._sweepProgress = ""
    self._sweepResult = ""
    -- Subscribe before the async kickoff so we don't miss early events.
    if not self._sweepSubscribed then
        self:_ensureSubscriptions()
    end
    local _, err = pcall(mcp.sweepHotCorrelationsAsync)
    if err then
        self._sweepBusy = false
        self._sweepResult = "Error: " .. tostring(err)
        return
    end
end

-- Subscription callback for tmp://sweep/hot-correlations.md events.
-- Receives a compressed event batch from pubsub. We collect updates
-- per-tag and apply the latest values; on terminal status, fetch the
-- final body via mcp.tmp_get for the summary line.
function Curation:onSweepEvent(events)
    if type(events) ~= "table" then return end
    local terminal = nil
    for _, e in ipairs(events) do
        if e.path == SWEEP_DOC_PATH then
            if e.tag == "sweep-status" then
                if e.value == "completed" or e.value == "errored"
                   or e.value == "embedding-unavailable" then
                    terminal = e.value
                end
            elseif e.tag == "sweep-progress" then
                self._sweepProgress = e.value or ""
            end
        end
    end
    if terminal then
        self._sweepBusy = false
        self._sweepProgress = ""
        if terminal == "embedding-unavailable" then
            self._sweepResult = "Embedding model unavailable — sweep skipped"
        elseif terminal == "errored" then
            local body, _ = mcp.tmp_get(SWEEP_DOC_PATH)
            self._sweepResult = "Sweep error: " .. (body or "")
        else
            local body, _ = mcp.tmp_get(SWEEP_DOC_PATH)
            if body and body ~= "" then
                local parsed = mcp.parseTagBlock(body)
                local tags = (parsed and parsed.tags) or {}
                local dur, rebuilt, touched = "?", "?", "?"
                for _, t in ipairs(tags) do
                    if t.name == "sweep-duration-ms" then dur = t.value end
                    if t.name == "sweep-tags-rebuilt" then rebuilt = t.value end
                    if t.name == "sweep-tags-touched" then touched = t.value end
                end
                self._sweepResult = string.format(
                    "Swept in %s ms — %s tags rebuilt, %s touched",
                    dur, rebuilt, touched)
            else
                self._sweepResult = "Sweep complete"
            end
        end
        -- Fresh sweep may have changed the focused tag's HC entries.
        if self:isFocused() then
            self:focusTag(self.focusedTag)
        end
    end
end

function Curation:sweepBusy()
    return self._sweepBusy
end

function Curation:sweepStatusText()
    if self._sweepBusy then
        if self._sweepProgress ~= "" then
            return "Sweeping… " .. self._sweepProgress
        end
        return "Sweeping…"
    end
    if self._sweepResult ~= "" then return self._sweepResult end
    return "Idle"
end

----------------------------------------------------------------------
-- PinnedChunk (presenter) methods
----------------------------------------------------------------------

function PinnedChunk:new(listItem)
    local p = session:create(PinnedChunk, {
        viewItem = listItem,
        _widgets = {},
        _chunkInfo = {},
        _suggestions = {},
        _extTags = {},
        _savedPendings = {},
        -- Start at 1 (not 0) so the iframe bridge's first emitted JS
        -- string already carries a non-default nonce. Initial-fire
        -- semantics for ui-code only run when the stored value is
        -- truthy AND has gone through a change; pre-bumping avoids
        -- a sticky default that masks real transitions.
        _bridgeNonce = 1,
    })
    -- Empty-start invariant: always at least one empty widget visible.
    table.insert(p._widgets, PendingWidget:new(p))
    -- Register with the parent Curation so pendingCount/editedCount can
    -- find us.
    if ark and ark._curation then
        ark._curation._presenters[p:chunkID()] = p
    end
    return p
end

-- Accessors over the Go-mirrored entry (viewItem.baseItem)
function PinnedChunk:chunkID()  return self.viewItem.baseItem.chunkID end
function PinnedChunk:path()     return self.viewItem.baseItem.path end
function PinnedChunk:pinnedAt() return self.viewItem.baseItem.pinnedAt end

-- Dismiss with confirmation when there are pending changes or the card
-- is in edit mode.
function PinnedChunk:dismiss()
    if self:hasChanges() then
        self._confirmDismiss = true
        return
    end
    self:_doDismiss()
end

function PinnedChunk:confirmDismiss()
    self._confirmDismiss = false
    self:_doDismiss()
end

function PinnedChunk:cancelDismiss()
    self._confirmDismiss = false
end

function PinnedChunk:dismissPending()
    return self._confirmDismiss
end

function PinnedChunk:notDismissPending()
    return not self._confirmDismiss
end

function PinnedChunk:_doDismiss()
    if ark and ark._curation then
        ark._curation._presenters[self:chunkID()] = nil
    end
    sys.curation.dismiss(self:chunkID())
end

function PinnedChunk:contentURL()
    local p = self:path()
    if p == "" then return "" end
    return "/content" .. p
end

function PinnedChunk:isNew()
    local cutoff = (ark and ark._curation and ark._curation._newCutoff) or 0
    return self:pinnedAt() > cutoff
end

function PinnedChunk:notNew()
    return not self:isNew()
end

function PinnedChunk:shortPath()
    return compressPath(self:path())
end

function PinnedChunk:loadSuggestions()
    if self._suggestionsLoaded then return end
    self._suggestionsLoaded = true
    local out, err = mcp.suggestTagNames(self:chunkID(), SUGGESTIONS_K)
    if err then
        self._suggestionsError = err
        self._suggestions = {}
        return
    end
    local list = {}
    for _, s in ipairs(out or {}) do
        local row = session:create(TagSuggestion)
        row.tag = s.tag or ""
        row.score = tonumber(s.score) or 0
        row.motivatingFiles = s.motivatingFiles or {}
        table.insert(list, row)
    end
    self._suggestions = list
end

function PinnedChunk:suggestions()
    if not self._suggestionsLoaded then
        self:loadSuggestions()
    end
    return self._suggestions
end

function PinnedChunk:suggestionsError()
    return self._suggestionsError
end

function PinnedChunk:noSuggestionsError()
    return self._suggestionsError == ""
end

function PinnedChunk:hasSuggestions()
    return self._suggestionsLoaded and #self._suggestions > 0
end

-- chunkInfo --------------------------------------------------------

function PinnedChunk:loadChunkInfo()
    if self._chunkInfoLoaded then return end
    self._chunkInfoLoaded = true
    local info, err = mcp.chunkInfo(self:chunkID())
    if err then
        self._chunkInfoError = err
        self._chunkInfo = {}
        return
    end
    self._chunkInfo = info or {}
end

function PinnedChunk:chunkInfo()
    if not self._chunkInfoLoaded then self:loadChunkInfo() end
    return self._chunkInfo
end

function PinnedChunk:writable()
    local info = self:chunkInfo()
    -- Default to true until chunkInfo loads; safer than guessing read-only.
    if info == nil then return true end
    if info.writable == nil then return true end
    return info.writable
end

function PinnedChunk:readOnly()
    return not self:writable()
end

-- Widget stack -----------------------------------------------------

-- Empty-start invariant: the widget stack always shows at least one
-- (empty) widget so the user has somewhere to type. Called wherever
-- the stack might have just gone empty. No-op in edit mode (stack
-- hidden anyway).
function PinnedChunk:_ensureEmptyWidget()
    if self._editing then return end
    if #self._widgets == 0 then
        table.insert(self._widgets, PendingWidget:new(self))
    end
end

-- Returns the widget stack. Empty during edit mode (the editor is the
-- active authoring surface; pendings hide).
function PinnedChunk:widgets()
    if self._editing then return {} end
    self:_ensureEmptyWidget()
    return self._widgets
end

-- True during edit mode (stack hidden); used by ui-class-hidden on
-- the pending-widget stack.
function PinnedChunk:hideWidgets()
    return self._editing
end

function PinnedChunk:addWidget()
    if self._editing then return end
    table.insert(self._widgets, PendingWidget:new(self))
end

-- Spreadsheet-like tab handling. When the [+] button receives
-- focus (via Tab from the last widget's field, or via a mouse
-- click), this fires: ensure there's an empty widget at the
-- bottom of the stack, then JS-focus its tag-name input so the
-- user can immediately start typing. Without the JS focus shift,
-- browser tab focus would skip past the (newly-inserted) widget
-- since it's earlier in DOM than the just-focused [+].
function PinnedChunk:onPlusFocused()
    if self._editing then return end
    -- Capture the widget count BEFORE the add so the JS can poll
    -- until the engine has pushed the new DOM element. Without
    -- this we'd focus the previous-last input (which is what was
    -- there at JS-fire time, before the new widget rendered).
    local cid = self:chunkID()
    local beforeCount = #(self._widgets or {})
    self:addWidget()
    mcp.code = string.format([[
// cur-plus-focus-%d
(function() {
    const root = document.querySelector('[data-cur-chunkid="%d"]');
    if (!root) return;
    const before = %d;
    const start = Date.now();
    // Try to focus a custom element's inner input. The element may
    // not have rendered its shadow DOM at the moment we look it up,
    // so wait for `updateComplete` (Lit-based) and two animation
    // frames, then retry a couple of times if the focus didn't take.
    function focusInput(el) {
        if (!el) return;
        const tryNow = () => {
            const inner = el.shadowRoot && el.shadowRoot.querySelector('input, textarea');
            if (inner && typeof inner.focus === 'function') {
                inner.focus();
                if (document.activeElement === inner) return true;
            }
            if (typeof el.focus === 'function') {
                el.focus();
                if (document.activeElement === el) return true;
            }
            return false;
        };
        const settle = el.updateComplete && typeof el.updateComplete.then === 'function'
            ? el.updateComplete
            : Promise.resolve();
        settle.then(() => requestAnimationFrame(() => requestAnimationFrame(() => {
            if (!tryNow()) {
                setTimeout(tryNow, 30);
                setTimeout(tryNow, 120);
            }
        })));
    }
    // Poll for the new widget to appear in the DOM, then focus it.
    function pollForNew() {
        const inputs = root.querySelectorAll('.cur-pin-widgets .cur-widget-tag');
        if (inputs.length > before) {
            focusInput(inputs[inputs.length - 1]);
            return;
        }
        if (Date.now() - start < 500) setTimeout(pollForNew, 20);
    }
    setTimeout(pollForNew, 0);
})();
]], math.random(1, 2147483647), cid, beforeCount)
end

function PinnedChunk:removeWidget(widget)
    for i, w in ipairs(self._widgets) do
        if w == widget then
            table.remove(self._widgets, i)
            break
        end
    end
    self:_ensureEmptyWidget()
end

function PinnedChunk:filledWidgetCount()
    local n = 0
    for _, w in ipairs(self._widgets) do
        if w:isFilled() then n = n + 1 end
    end
    return n
end

function PinnedChunk:filledInlineWidgets()
    local out = {}
    for _, w in ipairs(self._widgets) do
        if w:isFilled() and not w.extMode then
            table.insert(out, w)
        end
    end
    return out
end

function PinnedChunk:filledExtWidgets()
    local out = {}
    for _, w in ipairs(self._widgets) do
        if w:isFilled() and w.extMode then
            table.insert(out, w)
        end
    end
    return out
end

function PinnedChunk:pendingCount()
    return self:filledWidgetCount()
end

function PinnedChunk:noPending()
    return self:pendingCount() == 0
end

-- True when the chunk contributes to Accept(N): edited OR has filled
-- pending widgets. Drives the icon color cue.
function PinnedChunk:hasChanges()
    return self._isChunkEdited or self:filledWidgetCount() > 0
end

function PinnedChunk:noChanges()
    return not self:hasChanges()
end

-- Icon for the [edit|revert] button. pencil-square when not in edit
-- mode; arrow-counterclockwise when editing.
function PinnedChunk:editButtonIcon()
    if self._editing then return "arrow-counterclockwise" end
    return "pencil-square"
end

-- True when the icon should render in accent color (chunk has
-- changes); false for the gray clean state. Both states use the same
-- icon glyph; only the color differs.
function PinnedChunk:editButtonAccent()
    return self:hasChanges()
end

function PinnedChunk:notEditing()
    return not self._editing
end

function PinnedChunk:isEditing()
    return self._editing
end

-- Public accessors for fields that are private (underscore prefix)
-- but need to surface to viewdef bindings or JS bridge.
function PinnedChunk:chunkOriginalText() return self._chunkOriginalText or "" end
function PinnedChunk:foldedText()        return self._foldedText or "" end

-- JS-side setter shims. ui-value bindings on hidden inputs invoke
-- these when the JS bridge calls updateValue.
function PinnedChunk:setEditorContent(text)
    self._editorContent = text or ""
    -- Recompute dirty on every content push so Accept(N) stays in sync.
    self:onEditorDocChanged(self._editorContent ~= self._chunkOriginalText,
                            self._editorContent)
end

function PinnedChunk:setEditorDirty(flag)
    -- The flag can arrive as bool, "true"/"false", or 0/1.
    local b
    if type(flag) == "boolean" then b = flag
    elseif flag == "true" or flag == "1" or flag == 1 then b = true
    else b = false
    end
    self:onEditorDocChanged(b, self._editorContent)
end

function PinnedChunk:setExtTagsJSON(jsonStr)
    self:onExtTagsScraped(jsonStr or "[]")
end

-- Toggle handler for the [edit|revert] button. Routes to edit() or
-- revert() based on current mode.
function PinnedChunk:toggleEdit()
    if self._editing then
        self:revert()
    else
        self:edit()
    end
end

-- editorBridgeCode emits the inline JS bound to ui-code on the
-- per-card editor block. Re-runs whenever the bound string changes —
-- which happens on every edit/revert transition since the string
-- carries the editing flag. Idempotent via dataset.armed guards.
--
-- Two branches:
--   editing: import /ark-markdown-editor.js, mount createInkArkEditor,
--            dispatch the fold transition, start polling for
--            dirty/content sync back to Lua via updateValue.
--   not editing: destroy any prior editor instance, clear poller.
function PinnedChunk:editorBridgeCode()
    local cid = self:chunkID()
    if self._editing then
        -- Encode the pending command queue. The bridge JS ack-drains
        -- via ackEditorCmds() after applying; we don't clear here
        -- (the engine may evaluate this method multiple times per
        -- scan cycle and we'd lose commands queued between evals).
        local cmdsJSON = jsonEncode(self._pendingEditorCmds or {})
        return string.format([[
// editor-bridge-mount-%d-%d
(async function() {
    const root = document.querySelector('[data-cur-chunkid="%d"]');
    if (!root) return;
    const editorBlock = root.querySelector('.cur-pin-chunk-editor');
    if (!editorBlock) return;
    const cmds = %s;

    // Apply each command as a single CM6 transaction so undo
    // (Ctrl-Z) reverts one logical edit at a time. After applying,
    // ack the count to Lua so the queue can drain.
    function applyCmds(editor) {
        if (!cmds || cmds.length === 0) return;
        if (editor) {
            const doc = editor.getDoc ? editor.getDoc() : '';
            let cur = doc;
            for (const cmd of cmds) {
                cur = applyOne(cur, cmd);
            }
            if (cur !== doc && editor.update) {
                editor.update(cur);
            }
        }
        const ackInput = editorBlock.querySelector('.cur-pin-editor-ack-input');
        if (ackInput) {
            window.uiApp.updateValue(ackInput.id, String(cmds.length));
        }
    }
    function applyOne(text, cmd) {
        if (cmd.kind === 'replace-tag-line') {
            return replaceNthTagLine(text, cmd.name, cmd.occurrence, cmd.newName, cmd.newValue);
        }
        if (cmd.kind === 'remove-tag-line') {
            return removeNthTagLine(text, cmd.name, cmd.occurrence);
        }
        if (cmd.kind === 'insert-tag') {
            return insertTagAtTop(text, cmd.name, cmd.value);
        }
        return text;
    }
    function nthTagLineRange(text, name, occurrence) {
        const prefix = '@' + name + ':';
        let pos = 0;
        let n = 0;
        while (pos < text.length) {
            const nl = text.indexOf('\n', pos);
            const lineEnd = nl < 0 ? text.length : nl;
            const line = text.slice(pos, lineEnd);
            if (line.trimStart().startsWith(prefix)) {
                n++;
                if (n === occurrence) {
                    return { start: pos, end: nl < 0 ? text.length : nl + 1 };
                }
            }
            if (nl < 0) break;
            pos = nl + 1;
        }
        return null;
    }
    function replaceNthTagLine(text, name, occurrence, newName, newValue) {
        const r = nthTagLineRange(text, name, occurrence);
        if (!r) return text;
        const repl = '@' + newName + ': ' + newValue + '\n';
        return text.slice(0, r.start) + repl + text.slice(r.end);
    }
    function removeNthTagLine(text, name, occurrence) {
        const r = nthTagLineRange(text, name, occurrence);
        if (!r) return text;
        return text.slice(0, r.start) + text.slice(r.end);
    }
    function insertTagAtTop(text, name, value) {
        return '@' + name + ': ' + value + '\n' + text;
    }

    // First-mount branch: build the editor.
    if (root.dataset.editorArmed !== '1') {
        root.dataset.editorArmed = '1';
        const mount  = editorBlock.querySelector('.cur-pin-editor-mount');
        const origEl = editorBlock.querySelector('.cur-pin-editor-orig');
        const foldEl = editorBlock.querySelector('.cur-pin-editor-fold');
        const pathEl = editorBlock.querySelector('.cur-pin-editor-path');
        const dirtyInput   = editorBlock.querySelector('.cur-pin-editor-dirty-input');
        const contentInput = editorBlock.querySelector('.cur-pin-editor-content-input');
        if (!mount || !origEl || !foldEl || !pathEl) return;
        const original = origEl.textContent || '';
        const folded   = foldEl.textContent || '';
        const path     = pathEl.textContent || '';
        while (mount.firstChild) mount.removeChild(mount.firstChild);
        let editor;
        try {
            const mod = await import('/ark-markdown-editor.js');
            editor = await mod.createInkArkEditor({
                parent: mount,
                doc: original,
                path: path,
                api: {
                    save: (_p, content) => {
                        if (contentInput) {
                            window.uiApp.updateValue(contentInput.id, content);
                        }
                    },
                    search: () => Promise.resolve([]),
                },
            });
        } catch (e) {
            console.error('cur-pin editor mount failed', e);
            return;
        }
        if (folded !== original && editor && editor.update) {
            editor.update(folded);
        }
        window['__curEditor_%d'] = editor;
        applyCmds(editor);
        // Poll for changes every 400ms. Only push to Lua when the
        // value has actually changed — otherwise we flood the
        // message channel.
        let lastContent = original;
        let lastDirty = null;
        const poller = setInterval(() => {
            const e = window['__curEditor_%d'];
            if (!e || typeof e.getDoc !== 'function') {
                clearInterval(poller);
                return;
            }
            const cur = e.getDoc();
            if (cur !== lastContent) {
                lastContent = cur;
                if (contentInput) {
                    window.uiApp.updateValue(contentInput.id, cur);
                }
            }
            const dirty = (cur !== original) ? 'true' : 'false';
            if (dirty !== lastDirty) {
                lastDirty = dirty;
                if (dirtyInput) {
                    window.uiApp.updateValue(dirtyInput.id, dirty);
                }
            }
        }, 400);
        window['__curPoller_%d'] = poller;
    } else {
        // Subsequent fire — just drain commands against the live editor.
        applyCmds(window['__curEditor_%d']);
    }
})();
]], cid, self._bridgeNonce or 0, cid, cmdsJSON, cid, cid, cid, cid)
    end
    return string.format([[
// editor-bridge-destroy-%d-%d
(function() {
    const root = document.querySelector('[data-cur-chunkid="%d"]');
    if (root) { delete root.dataset.editorArmed; }
    const e = window['__curEditor_%d'];
    if (e && typeof e.destroy === 'function') { e.destroy(); }
    delete window['__curEditor_%d'];
    const p = window['__curPoller_%d'];
    if (p) { clearInterval(p); delete window['__curPoller_%d']; }
})();
]], cid, self._bridgeNonce or 0, cid, cid, cid, cid, cid)
end

-- Bump the bridge nonce. Call this whenever a transition should
-- cause iframe/editor bridge JS to re-fire (edit/revert, freshly-
-- pinned iframe, etc.). The nonce embeds in the emitted JS string
-- so the engine's change detection sees a new value.
function PinnedChunk:bumpBridgeNonce()
    self._bridgeNonce = (self._bridgeNonce or 0) + 1
end

-- Queue an editor command (a structured table). The bridge JS will
-- consume the queue on its next fire, applying each command as a
-- single CM6 transaction so each is individually undoable.
function PinnedChunk:queueEditorCmd(cmd)
    if type(self._pendingEditorCmds) ~= "table" then
        self._pendingEditorCmds = {}
    end
    table.insert(self._pendingEditorCmds, cmd)
    self:bumpBridgeNonce()
end

-- JS ack callback. The bridge JS calls this after applying N
-- commands; Lua drops the first N entries from the queue. We can't
-- clear inside editorBridgeCode itself because the engine may
-- evaluate that method multiple times per change cycle and would
-- lose commands queued between evals.
function PinnedChunk:ackEditorCmds(count)
    local n = tonumber(count) or 0
    if n <= 0 then return end
    if type(self._pendingEditorCmds) ~= "table" then return end
    local remaining = {}
    for i = n + 1, #self._pendingEditorCmds do
        table.insert(remaining, self._pendingEditorCmds[i])
    end
    self._pendingEditorCmds = remaining
end

-- iframeBridgeCode emits the JS that runs on the read-only iframe:
--   1. Scrapes <ark-ext-tags> children and pushes them to Lua.
--   2. Injects a CSS rule hiding the chunk's curate-pin button (the
--      chunk is already pinned — the button is redundant inside the
--      workshop card).
--   3. Wires a click listener on the iframe contentDocument that
--      fires [edit] only when the click lands on plain chunk text,
--      not on the tag-overview sidebar or the <ark-ext-tags>
--      indicator (which has its own dropdown behavior).
-- Idempotent via the iframe's own dataset flags.
function PinnedChunk:iframeBridgeCode()
    local cid = self:chunkID()
    if self._editing then
        -- iframe is hidden during edit mode; no work.
        return "// iframe-bridge-skip-" .. tostring(self._bridgeNonce or 0)
    end
    return string.format([[
// iframe-bridge-%d-%d
(function() {
    const root = document.querySelector('[data-cur-chunkid="%d"]');
    if (!root) return;
    const iframe = root.querySelector('.cur-pin-iframe');
    const tagsInput = root.querySelector('.cur-pin-ext-tags-input');
    if (!iframe || !tagsInput) return;
    function arm() {
        if (iframe.dataset.armed === '1') return;
        try {
            const doc = iframe.contentDocument;
            if (!doc) return;
            // Skip while the iframe is still on its blank doc — readyState
            // can be 'complete' for about:blank too. Wait for actual
            // chunk content (the page's body has the .ark-chunk class
            // wrapper or any heading element).
            if (!doc.body || doc.body.childElementCount === 0) return;
            iframe.dataset.armed = '1';
            // Scrape every <ark-tag> in the chunk render. Tags whose
            // parent is <ark-ext-tags> are ext-routed; the rest are
            // inline tags found in the chunk body (including @id and
            // any mid-chunk @tag: value lines parseTagBlock misses).
            const rows = [];
            doc.querySelectorAll('ark-tag').forEach(t => {
                const inExt = !!t.closest('ark-ext-tags');
                rows.push({
                    name: t.querySelector('name') ? t.querySelector('name').textContent : '',
                    value: t.querySelector('value') ? t.querySelector('value').textContent : '',
                    externalfile: inExt ? (t.getAttribute('externalfile') || '') : '',
                    externaltarget: inExt ? (t.getAttribute('externaltarget') || '') : '',
                    kind: inExt ? 'ext' : 'inline',
                });
            });
            window.uiApp.updateValue(tagsInput.id, JSON.stringify(rows));
            // Hide the per-chunk curate-pin button (chunk is already
            // pinned in the workshop — the button is redundant).
            const style = doc.createElement('style');
            style.textContent = '.ark-curate-pin{display:none!important;}';
            doc.head.appendChild(style);
            // Size the iframe to its content (capped at 280px). The
            // wrapper uses max-height so short chunks shrink to fit;
            // we can't rely on height:100% (auto-parent collapses it).
            // ResizeObserver catches late growth (images loading,
            // ark-tag-overview rendering its sidebar).
            const fitIframe = () => {
                const h = Math.min(doc.body.scrollHeight, 280);
                iframe.style.height = h + 'px';
            };
            fitIframe();
            try { new ResizeObserver(fitIframe).observe(doc.body); }
            catch (e) { /* older browser — single measure is fine */ }
            // Wire click-to-edit. The click must land on actual
            // chunk text — not on a widget, an overlay, the gap
            // around content, or whitespace inside a paragraph.
            // We check three things:
            //   1. The target must NOT be inside any of the
            //      interactive widgets (ext-tags indicator, tag-
            //      overview panel, curate-pin button, etc.).
            //   2. The target must be inside the .ark-chunk content.
            //   3. The caret-from-point at the click coordinates must
            //      resolve to a non-empty Text node — proving the
            //      user clicked on rendered text, not padding.
            doc.addEventListener('click', function(ev) {
                const t = ev.target;
                if (!t || !t.closest) return;
                if (t.closest('ark-ext-tags') ||
                    t.closest('ark-tag') ||
                    t.closest('.ark-tag-overview') ||
                    t.closest('.ark-tag-overview-panel') ||
                    t.closest('.ark-tag-action') ||
                    t.closest('.ark-curate-pin')) {
                    return;  // let those handlers run
                }
                if (!t.closest('.ark-chunk')) return;
                // Caret check: did the click land on actual text?
                let caret = null;
                if (doc.caretPositionFromPoint) {
                    caret = doc.caretPositionFromPoint(ev.clientX, ev.clientY);
                } else if (doc.caretRangeFromPoint) {
                    const r = doc.caretRangeFromPoint(ev.clientX, ev.clientY);
                    if (r) caret = { offsetNode: r.startContainer };
                }
                if (!caret || !caret.offsetNode) return;
                const node = caret.offsetNode;
                if (node.nodeType !== 3) return;  // not a text node
                if (!(node.nodeValue || '').trim()) return;
                const btn = root.querySelector('.cur-pin-edit-btn');
                if (btn && !btn.hasAttribute('disabled')) btn.click();
            }, true);
        } catch (e) {
            // Cross-origin or transient — ignore.
        }
    }
    iframe.addEventListener('load', arm);
    // The iframe may already be loaded by the time this fires.
    if (iframe.contentDocument
        && iframe.contentDocument.readyState === 'complete') {
        arm();
    }
})();
]], cid, self._bridgeNonce or 0, cid)
end

-- Chunk-text lookup ------------------------------------------------

function PinnedChunk:loadChunkText()
    if self._chunkTextLoaded then return end
    self._chunkTextLoaded = true
    local text, err = mcp.chunkText(self:chunkID())
    if err then
        self._chunkTextError = err
        self._chunkText = ""
        return
    end
    self._chunkText = text or ""
end

function PinnedChunk:chunkText()
    if not self._chunkTextLoaded then self:loadChunkText() end
    return self._chunkText
end

-- Inline iframe URL for read-only chunk-text view.
function PinnedChunk:iframeURL()
    local info = self:chunkInfo()
    if not info or not info.path or info.path == "" then return "" end
    local range = info.range or ""
    if range == "" then
        return "/content" .. info.path .. "?toggle=false&tag-overview=collapsed"
    end
    return string.format("/content%s?range=%s&toggle=false&tag-overview=collapsed",
        info.path, range)
end

-- Edit / Revert ---------------------------------------------------

-- [edit] click handler.
-- 1. Snapshot _chunkOriginalText and _savedPendings.
-- 2. Compute foldedText from filled inline widgets.
-- 3. Clear folded inline widgets from the stack (ext widgets stay).
-- 4. Set _editing = true. The JS bridge mounts CM6 with initial doc =
--    _savedEditorText (consumed if present) else _chunkOriginalText,
--    then dispatches one transaction to apply the fold.
function PinnedChunk:edit()
    if self._editing then return end
    if self:readOnly() then return end
    self:loadChunkText()
    self._chunkOriginalText = self._chunkText or ""
    -- Snapshot inline pendings for restore on [revert].
    local inlines = self:filledInlineWidgets()
    self._savedPendings = {}
    for _, w in ipairs(inlines) do
        table.insert(self._savedPendings, {
            tagName = w.tagName,
            tagValue = w.tagValue,
            removeMode = w.removeMode,
        })
    end
    -- Fold inline ops into the draft text. Ext widgets are skipped —
    -- they don't fold into chunk text.
    self._foldedText = self:applyPendingsToText(self._chunkOriginalText, inlines)
    -- Drop folded inline widgets; keep ext widgets and empty rows.
    local kept = {}
    for _, w in ipairs(self._widgets) do
        if not (w:isFilled() and not w.extMode) then
            table.insert(kept, w)
        end
    end
    self._widgets = kept
    self._isChunkEdited = (self._foldedText ~= self._chunkOriginalText)
    self._editing = true
    self._acceptError = ""
    self:bumpBridgeNonce()
end

-- [revert] click handler.
-- 1. Snapshot _savedEditorText (perfect-restore for next [edit]).
-- 2. Destroy editor (handled JS-side via ui-class binding).
-- 3. Restore _widgets from _savedPendings; ext widgets stay.
-- 4. Clear edit-mode state.
function PinnedChunk:revert()
    if not self._editing then return end
    -- _editorContent is JS-synced — Lua already has the latest text.
    self._savedEditorText = self._editorContent
    self._editorContent = ""
    -- Restore folded inline widgets, skipping any with empty tag name.
    local restored = {}
    for _, snap in ipairs(self._savedPendings or {}) do
        local nm = (snap.tagName or ""):gsub("^%s+", ""):gsub("%s+$", "")
        if nm ~= "" then
            local w = PendingWidget:new(self)
            w.tagName = snap.tagName or ""
            w.tagValue = snap.tagValue or ""
            w.removeMode = snap.removeMode and true or false
            table.insert(restored, w)
        end
    end
    -- Keep filled ext widgets only; the empty-start invariant
    -- will re-add a fresh trailing empty after.
    for _, w in ipairs(self._widgets) do
        if w.extMode and w:isFilled() then
            table.insert(restored, w)
        end
    end
    self._widgets = restored
    self._savedPendings = {}
    self._chunkOriginalText = ""
    self._foldedText = ""
    self._isChunkEdited = false
    self._editing = false
    self:_ensureEmptyWidget()
    self._acceptError = ""
    self:bumpBridgeNonce()
end

-- Fold inline widgets into chunk text. Pure function: no state
-- mutation. Strategy:
--   inline-add (removeMode=false): prepend "@tag: value\n" to the
--     leading tag block (above any blank line, above body).
--   inline-change (same tag exists, removeMode=false): replace the
--     existing "@tag: ..." line in place.
--   inline-remove (removeMode=true): delete the matching "@tag: ..."
--     line.
-- Operations apply in widget order; later operations see the
-- intermediate text from earlier ones.
function PinnedChunk:applyPendingsToText(text, inlineWidgets)
    local out = text or ""
    -- Collect fresh prepends in widget order; concat once so the
    -- order in the chunk matches the user's widget stack rather than
    -- being reversed by individual prepends.
    local prepends = {}
    for _, w in ipairs(inlineWidgets or {}) do
        local tag = (w.tagName or ""):gsub("^%s+", ""):gsub("%s+$", "")
        if tag ~= "" then
            if w.removeMode then
                out = removeTagLine(out, tag)
            else
                local exists = findTagLine(out, tag)
                if exists then
                    out = replaceTagLine(out, tag, w.tagValue or "")
                else
                    table.insert(prepends,
                        "@" .. tag .. ": " .. (w.tagValue or "") .. "\n")
                end
            end
        end
    end
    if #prepends > 0 then
        out = table.concat(prepends, "") .. out
    end
    return out
end

-- JS bridge: docChanged callback. Receives the dirty flag from JS
-- (editor.getDoc() != _chunkOriginalText). Two reconciliation
-- passes happen here:
--   1. Fold-undo: pendings were folded at [edit] and the user has
--      undone back to the original; exit edit mode without
--      restoring pendings.
--   2. Ext-conversion-undo: when a hidden ext entry's inline line
--      no longer appears in the chunk text (user pressed Ctrl-Z
--      to undo our insert-tag), restore the ext entry to visible
--      and drop the matching ext-remove pending widget. The tag
--      returns to its original ext-routed state cleanly.
function PinnedChunk:onEditorDocChanged(dirty, content)
    self._isChunkEdited = dirty and true or false
    if content then self._editorContent = content end
    -- (1) Fold-undo auto-exit. When the user Ctrl-Z's the very
    -- first change to the chunk (fold transaction), restore the
    -- saved pending stack and exit edit mode. Same outcome as an
    -- explicit `[revert]` — least surprise.
    if not self._isChunkEdited
        and self._savedPendings
        and #self._savedPendings > 0
    then
        -- Drop existing empty widgets so the restored pendings don't
        -- end up after a sprinkling of empties. Restore in order;
        -- skip any saved snapshot whose tagName is empty (defensive
        -- against any edge case where an empty made it into the
        -- saved set).
        local kept = {}
        for _, w in ipairs(self._widgets or {}) do
            if w:isFilled() then
                table.insert(kept, w)
            end
        end
        self._widgets = kept
        for _, snap in ipairs(self._savedPendings) do
            local nm = (snap.tagName or ""):gsub("^%s+", ""):gsub("%s+$", "")
            if nm ~= "" then
                local w = PendingWidget:new(self)
                w.tagName = snap.tagName or ""
                w.tagValue = snap.tagValue or ""
                w.removeMode = snap.removeMode and true or false
                table.insert(self._widgets, w)
            end
        end
        self._editing = false
        self._chunkOriginalText = ""
        self._foldedText = ""
        self._savedPendings = {}
        self._editorContent = ""
        self._savedEditorText = ""
        self:_ensureEmptyWidget()
        self:bumpBridgeNonce()
        return
    end
    -- (2) Ext-conversion-undo
    self:_reconcileHiddenExtEntries()
end

-- For every hidden ext entry, check whether its @name: line still
-- appears in the editor's current text. If not, the user has
-- undone our insert-tag cmd; restore the ext entry to visible and
-- drop the matching ext-remove pending widget.
function PinnedChunk:_reconcileHiddenExtEntries()
    if not self._extTags or #self._extTags == 0 then return end
    local text = self._editorContent or ""
    for _, et in ipairs(self._extTags) do
        if et._hidden then
            local hasInline = textHasTagLine(text, et.name)
            if not hasInline then
                et._hidden = false
                -- Drop matching ext-remove widget.
                local kept = {}
                for _, w in ipairs(self._widgets or {}) do
                    local drop = w.extMode and w.removeMode
                        and (w.tagName or "") == (et.name or "")
                    if not drop then table.insert(kept, w) end
                end
                self._widgets = kept
            end
        end
    end
end

-- True iff the chunk text contains any leading-of-line `@name:`.
function textHasTagLine(text, name)
    if not text or text == "" or not name or name == "" then return false end
    local prefix = "@" .. name .. ":"
    local pos = 1
    while pos <= #text do
        local lineEnd = text:find("\n", pos, true)
        local line
        if lineEnd then
            line = text:sub(pos, lineEnd - 1)
        else
            line = text:sub(pos)
        end
        local trimmed = line:gsub("^%s+", "")
        if trimmed:sub(1, #prefix) == prefix then return true end
        if not lineEnd then break end
        pos = lineEnd + 1
    end
    return false
end

-- JS bridge: tag-scrape callback. Receives a JSON array of
-- {name, value, externalfile, externaltarget, kind} from every
-- <ark-tag> in the iframe's chunk render. Splits the list into ext
-- (those whose iframe parent was <ark-ext-tags>) and inline (the
-- rest — body-level inline tags including @id and any mid-chunk
-- @name: value lines that parseTagBlock misses).
function PinnedChunk:onExtTagsScraped(jsonStr)
    self._extTagsLoaded = true
    local ok, list = pcall(jsonDecode, jsonStr or "[]")
    if not ok or type(list) ~= "table" then
        self._extTags = {}
        self._inlineScrapedTags = {}
        return
    end
    local extRows = {}
    local inlineRows = {}
    for _, raw in ipairs(list) do
        if raw.kind == "ext" then
            local row = session:create(ExtTagRow)
            row.name = raw.name or ""
            row.value = raw.value or ""
            row.externalfile = raw.externalfile or ""
            row.externaltarget = raw.externaltarget or ""
            table.insert(extRows, row)
        else
            table.insert(inlineRows, {
                name = raw.name or "",
                value = raw.value or "",
            })
        end
    end
    self._extTags = extRows
    self._inlineScrapedTags = inlineRows
end

-- Current-tags desired-state view.
-- Sources for inline tags:
--   read-only mode: prefer _inlineScrapedTags (catches @id and any
--     mid-chunk @tag: value lines the iframe rendered as <ark-tag>),
--     falling back to mcp.parseTagBlock on the chunk text if the
--     scrape hasn't run yet.
--   edit mode: use mcp.parseTagBlock on the editor draft (the
--     iframe is destroyed; the draft is the source of truth).
-- Ext tags always come from _extTags (the iframe scrape), which
-- persists across edit-mode transitions.
function PinnedChunk:currentTagsView()
    -- Signature of the upstream sources that drive the row list.
    -- If unchanged from the last call, return the cached row list
    -- (preserving Lua identity) so ViewList reuses DOM presenters
    -- and any user input mid-typing isn't snapped back. Per the
    -- text-as-truth principle: re-derivation only happens when the
    -- underlying text or ext/widget state actually changed.
    local srcText
    if self._editing then
        srcText = self._editorContent ~= ""
            and self._editorContent or self._foldedText
    else
        srcText = self:chunkText() or ""
    end
    local extSig = "ext:" .. #(self._extTags or {})
    local widgetSig = "w:" .. #(self._widgets or {}) ..
                      ":f" .. self:filledWidgetCount()
    local sig = string.format("e:%d:%s:%s:%s",
        #srcText, srcText:sub(1, 64), extSig, widgetSig)
    if sig == self._currentTagsViewSig
        and type(self._currentTagsViewRows) == "table"
        and #self._currentTagsViewRows > 0 then
        return self._currentTagsViewRows
    end
    self._currentTagsViewSig = sig

    -- Per-row reuse: match against the cache by (kind, name,
    -- occurrence). Reused rows keep their Lua identity (so ViewList
    -- doesn't replace DOM presenters and the user's input keeps
    -- focus); their text-derived fields update only when the user
    -- isn't mid-edit (value == _lastSyncedValue).
    local cache = self._currentTagRowCache
    if type(cache) ~= "table" then cache = {} end
    local newCache = {}
    -- Re-derivation rule for the row's value field:
    --   - while the user has focus in the row's value input, the
    --     input is the source of truth → don't overwrite (would
    --     teleport the cursor when fast-typing races a poller push)
    --   - when the user has blurred (or never focused), external
    --     CM6 changes — including Ctrl-Z undo — propagate back
    --   - even when allowed, only write if the value actually
    --     differs (avoids no-op DOM updates)
    -- Status and external{file,target} always refresh.
    local function reuseOrCreate(kind, name, occurrence, freshValue, opts)
        local key = kind .. ":" .. name .. ":" .. tostring(occurrence)
        local row = cache[key]
        if row then
            if not row._inputHasFocus and row.value ~= freshValue then
                row.value = freshValue
                row._lastSyncedValue = freshValue
            end
            row.status = opts and opts.status or ""
            row.externalfile = opts and opts.externalfile or ""
            row.externaltarget = opts and opts.externaltarget or ""
            row.extChecked = (kind == "ext")
        else
            row = session:create(CurrentTagRow)
            row.name = name
            row.value = freshValue
            row._lastSyncedValue = freshValue
            row.kind = kind
            row.occurrence = occurrence
            row._chunk = self
            row.status = opts and opts.status or ""
            row.externalfile = opts and opts.externalfile or ""
            row.externaltarget = opts and opts.externaltarget or ""
            row.extChecked = (kind == "ext")
        end
        newCache[key] = row
        return row
    end

    -- Helper: build inline rows from a list of {name, value} tables.
    -- Assigns 1-based occurrence ordinals so per-row edits can route
    -- to the Nth `@name:` line in the editor.
    local function buildInline(list)
        local seen = {}
        local rows = {}
        for _, t in ipairs(list or {}) do
            local nm = t.name or ""
            seen[nm] = (seen[nm] or 0) + 1
            local row = reuseOrCreate("inline", nm, seen[nm], t.value or "", nil)
            table.insert(rows, row)
        end
        return rows
    end

    local inlineRows = {}
    if self._editing then
        -- Use mcp.extractTagValues over the editor draft so mid-chunk
        -- @tag: value lines (including @id after a heading) surface in
        -- current-tags. Match strategy is "markdown" by default.
        local sourceText = self._editorContent ~= ""
            and self._editorContent or self._foldedText
        inlineRows = buildInline(mcp.extractTagValues(sourceText or "", "markdown"))
    elseif self._inlineScrapedTags and #self._inlineScrapedTags > 0 then
        inlineRows = buildInline(self._inlineScrapedTags)
    else
        -- Fallback before the iframe scrape lands: extractTagValues
        -- catches all tags anywhere in the chunk text.
        inlineRows = buildInline(mcp.extractTagValues(self:chunkText() or "", "markdown"))
    end
    -- Source 2: ext tags from the iframe scrape. Reused via cache
    -- so the input stays stable across re-derivations (ext tags
    -- typically have one occurrence per name, but use the seen
    -- counter to handle any duplicates uniformly). Entries with
    -- `_hidden = true` (converted to inline) are skipped — the
    -- corresponding inline row carries the tag now; re-toggling
    -- ext on that row restores the hidden entry.
    local extRows = {}
    local extSeen = {}
    for _, et in ipairs(self._extTags or {}) do
        if not et._hidden then
            local nm = et.name or ""
            extSeen[nm] = (extSeen[nm] or 0) + 1
            local row = reuseOrCreate("ext", nm, extSeen[nm], et.value or "",
                { externalfile = et.externalfile or "",
                  externaltarget = et.externaltarget or "" })
            table.insert(extRows, row)
        end
    end
    -- Overlay pending ops onto current-tags ONLY in edit mode. In
    -- non-edit mode the pending widget stack above the URL is the
    -- visible authoring surface; current-tags should reflect the
    -- file's actual state (chunk text + ext entries), not pending
    -- drafts. In edit mode the pending stack is hidden, so the
    -- only place ext-pending changes can surface is via this overlay.
    local removed = {}    -- map: "kind:name" → true
    local added = {}      -- list of rows to append
    if self._editing then
        for _, w in ipairs(self._widgets or {}) do
            if w:isFilled() then
                local key = (w.extMode and "ext:" or "inline:") .. (w.tagName or "")
                if w.removeMode then
                    removed[key] = true
                elseif w.extMode then
                    local found = false
                    for _, row in ipairs(extRows) do
                        if row.name == w.tagName then
                            row.value = w.tagValue or ""
                            row.status = "changed"
                            found = true
                            break
                        end
                    end
                    if not found then
                        local row = session:create(CurrentTagRow)
                        row.name = w.tagName or ""
                        row.value = w.tagValue or ""
                        row.kind = "ext"
                        row.status = "added"
                        row._chunk = self
                        table.insert(added, row)
                    end
                end
                -- Inline pendings in edit mode are already folded
                -- into the editor draft, which extractTagValues
                -- catches in inlineRows. No additional overlay needed.
            end
        end
    end
    -- Compose: inline rows then ext rows then any added rows. Mark
    -- removals via status (or filter out — chose to mark for now so
    -- the user sees the strike-through).
    local out = {}
    for _, row in ipairs(inlineRows) do
        local key = "inline:" .. row.name
        if removed[key] then row.status = "removed" end
        table.insert(out, row)
    end
    for _, row in ipairs(extRows) do
        local key = "ext:" .. row.name
        if removed[key] then row.status = "removed" end
        table.insert(out, row)
    end
    for _, row in ipairs(added) do
        table.insert(out, row)
    end
    self._currentTagRowCache = newCache
    self._currentTagsViewRows = out
    return out
end

-- Edit-mode actions for current-tag rows. Each action is a one-way
-- mutation: it produces either (a) a CM6 command queued for the
-- bridge JS to apply as a transaction, or (b) a pending op queued
-- for Accept-time dispatch (ext-set/ext-remove). Both paths converge
-- on the next docChanged → re-derivation pass, so the row visually
-- updates without holding its own authoritative state.

-- Read-only mode: queue a pending remove widget. Acts as a
-- convenience that promotes the row's intent into the pending stack.
function CurrentTagRow:queueRemove()
    if not self._chunk then return end
    -- Edit mode delegates directly to applyRemove which dispatches
    -- through the proper channel (CM6 cmd or pending op).
    if self._chunk._editing then
        self:applyRemove()
        return
    end
    local w = PendingWidget:new(self._chunk)
    w.tagName = self.name or ""
    w.tagValue = self.value or ""
    w.removeMode = true
    if self.kind == "ext" then
        w.extMode = true
        w.extLocatorKind = "absolute"
        w.extLocatorText = self.externaltarget or ""
        w._extLoaded = true
    end
    -- Insert above the empty row so the new widget is visible.
    local widgets = self._chunk._widgets or {}
    table.insert(widgets, 1, w)
end

-- Edit mode: user changed the row's value. For inline rows, queue a
-- CM6 transaction replacing the Nth `@<name>:` line. For ext rows,
-- queue an ext-set pending op carrying the new value.
function CurrentTagRow:applyValueEdit(newValue)
    if not self._chunk then return end
    local newVal = newValue or ""
    if self.kind == "inline" then
        self._chunk:queueEditorCmd({
            kind = "replace-tag-line",
            name = self.name or "",
            occurrence = self.occurrence or 1,
            newName = self.name or "",
            newValue = newVal,
        })
        -- Track the new value locally so the input doesn't snap back
        -- until docChanged re-derivation lands.
        self.value = newVal
    else
        -- Ext row: queue an ext-set pending op. The pending widget
        -- becomes a current-tags overlay (status=changed) via
        -- currentTagsView's pending merge.
        local w = PendingWidget:new(self._chunk)
        w.tagName = self.name or ""
        w.tagValue = newVal
        w.extMode = true
        w.extLocatorKind = "absolute"
        w.extLocatorText = self.externaltarget or ""
        w._extLoaded = true
        table.insert(self._chunk._widgets or {}, w)
        self.value = newVal
    end
end

-- Edit mode: user changed the row's name. Inline only — ext rename
-- isn't a single operation (would require removing the old ext and
-- writing a new one with new tag name).
function CurrentTagRow:applyNameEdit(newName)
    if not self._chunk or self.kind ~= "inline" then return end
    local newN = newName or ""
    self._chunk:queueEditorCmd({
        kind = "replace-tag-line",
        name = self.name or "",
        occurrence = self.occurrence or 1,
        newName = newN,
        newValue = self.value or "",
    })
    self.name = newN
end

-- Edit mode: user clicked rem. For inline, queue a CM6 transaction
-- removing the Nth `@<name>:` line. For ext, queue an ext-remove
-- pending op for Accept.
function CurrentTagRow:applyRemove()
    if not self._chunk then return end
    if self.kind == "inline" then
        self._chunk:queueEditorCmd({
            kind = "remove-tag-line",
            name = self.name or "",
            occurrence = self.occurrence or 1,
        })
    else
        local w = PendingWidget:new(self._chunk)
        w.tagName = self.name or ""
        w.removeMode = true
        w.extMode = true
        w.extLocatorKind = "absolute"
        w.extLocatorText = self.externaltarget or ""
        w._extLoaded = true
        table.insert(self._chunk._widgets or {}, w)
    end
end

-- Edit mode: user toggled the ext checkbox.
--   ext → inline: insert into chunk text + queue ext-remove. Hide
--     the source ext entry. Any stale ext-set/ext-remove pending
--     widgets for this tag are dropped first (transition-clean).
--   inline → ext: two cases:
--     (a) Hidden ext entry pairs by name → un-hide; if the inline
--         value diverged from the original ext value, queue an
--         ext-set carrying the divergent value.
--     (b) Fresh conversion: queue an ext-set with a suggested
--         locator. Either way, stale ext widgets for this tag are
--         dropped first.
function CurrentTagRow:toggleExt()
    if not self._chunk or not self._chunk._editing then return end
    local name = self.name or ""
    if self.kind == "ext" then
        -- ext → inline
        local entry = self:_findExtEntry()
        if entry then entry._hidden = true end
        self:_dropPendingExt(name)
        self._chunk:queueEditorCmd({
            kind = "insert-tag",
            name = name,
            value = self.value or "",
        })
        self:_addPendingExtRemove(name)
    else
        -- inline → ext
        self:_dropPendingExt(name)
        local hidden = self:_findHiddenExt()
        self._chunk:queueEditorCmd({
            kind = "remove-tag-line",
            name = name,
            occurrence = self.occurrence or 1,
        })
        if hidden then
            hidden._hidden = false
            -- If the user edited the value while it was inline,
            -- preserve the change via an ext-set; otherwise just
            -- a clean un-hide.
            if (self.value or "") ~= (hidden.value or "") then
                self:_addPendingExtSet(name, self.value or "")
            end
        else
            -- Fresh inline → ext.
            self:_addPendingExtSet(name, self.value or "")
        end
    end
end

-- Find the ExtTagRow in the chunk's scrape cache matching this
-- row's name. Returns nil if not found.
function CurrentTagRow:_findExtEntry()
    if not self._chunk then return nil end
    for _, et in ipairs(self._chunk._extTags or {}) do
        if et.name == self.name then return et end
    end
    return nil
end

-- Find a hidden ext entry whose name matches this inline row.
function CurrentTagRow:_findHiddenExt()
    if not self._chunk then return nil end
    for _, et in ipairs(self._chunk._extTags or {}) do
        if et.name == self.name and et._hidden then return et end
    end
    return nil
end

-- Drop every ext-mode pending widget (ext-set OR ext-remove) for
-- the named tag. Used at the start of every ext-toggle transition
-- to clean up stale ops before queueing fresh ones.
function CurrentTagRow:_dropPendingExt(name)
    if not self._chunk then return end
    local kept = {}
    for _, w in ipairs(self._chunk._widgets or {}) do
        local drop = w.extMode and (w.tagName or "") == (name or "")
        if not drop then table.insert(kept, w) end
    end
    self._chunk._widgets = kept
end

-- Add an ext-remove pending widget for the named tag.
function CurrentTagRow:_addPendingExtRemove(name)
    if not self._chunk then return end
    local w = PendingWidget:new(self._chunk)
    w.tagName = name or ""
    w.removeMode = true
    w.extMode = true
    w.extLocatorKind = "absolute"
    w.extLocatorText = self.externaltarget or ""
    w._extLoaded = true
    table.insert(self._chunk._widgets or {}, w)
end

-- Add an ext-set pending widget for the named tag with the given
-- value. Pulls locator defaults from mcp.suggestExtLocator.
function CurrentTagRow:_addPendingExtSet(name, value)
    if not self._chunk then return end
    local w = PendingWidget:new(self._chunk)
    w.tagName = name or ""
    w.tagValue = value or ""
    w.extMode = true
    local sug, _ = mcp.suggestExtLocator(self._chunk:chunkID())
    if sug then
        w.extBase = sug.base or "path"
        w.extBaseValue = sug.baseValue or self._chunk:path()
        w.extLocatorKind = sug.locatorKind or "absolute"
        w.extLocatorText = sug.locator or sug.locatorText or ""
        w._extLoaded = true
    end
    table.insert(self._chunk._widgets or {}, w)
end

-- ui-value=tagName setter shim. The viewdef wires sl-input change
-- events to set the row's tagName via this method, which routes
-- through applyNameEdit.
function CurrentTagRow:setTagName(v)
    if self._chunk and self._chunk._editing then
        self:applyNameEdit(v)
    else
        self.name = v or ""
    end
end

-- ui-value=tagValue setter shim.
function CurrentTagRow:setTagValue(v)
    if self._chunk and self._chunk._editing then
        self:applyValueEdit(v)
    else
        self.value = v or ""
    end
end

-- Display helpers for current-tag rows.
function CurrentTagRow:isInline()
    return self.kind == "inline"
end

function CurrentTagRow:isExt()
    return self.kind == "ext"
end

function CurrentTagRow:parentEditing()
    return self._chunk and self._chunk._editing or false
end

function CurrentTagRow:parentNotEditing()
    return not (self._chunk and self._chunk._editing)
end

-- sl-change handler for the value input. Commits the typed value
-- through applyValueEdit (which queues the CM6 command). Used as
-- the on-blur fallback; the live-as-you-type path goes through
-- onValueChange below.
function CurrentTagRow:onValueCommit()
    if self:parentNotEditing() then return end
    self:applyValueEdit(self.value)
end

-- Focus / blur handlers for the value input. While focus is held,
-- re-derivation in currentTagsView skips overwrites of row.value
-- so the user's cursor + in-progress typing aren't disturbed by a
-- stale poller push. On blur, the flag clears and external
-- changes (CM6 undo, edits to the same line) flow back into the
-- row on the next re-derivation.
function CurrentTagRow:onValueFocus()
    self._inputHasFocus = true
end

function CurrentTagRow:onValueBlur()
    self._inputHasFocus = false
end

-- Incremental-search-style pump. Bound to a hidden span via
-- `ui-value="onValueChange()"` so the engine re-evaluates it on
-- every variable scan. When the user types into the value input,
-- the writeback updates `self.value` but `self._lastSyncedValue`
-- lags behind. We detect the divergence and apply the edit
-- immediately — propagating live to the CM6 editor. Returns ""
-- (the span's value is just a trigger; we don't display it).
function CurrentTagRow:onValueChange()
    if self:parentNotEditing() then return "" end
    if self.value == self._lastSyncedValue then return "" end
    self._lastSyncedValue = self.value
    if self.kind == "inline" then
        self._chunk:queueEditorCmd({
            kind = "replace-tag-line",
            name = self.name or "",
            occurrence = self.occurrence or 1,
            newName = self.name or "",
            newValue = self.value or "",
        })
    else
        -- ext: if the new value matches the original ext entry's
        -- value, the edit is a no-op — drop any existing ext-set
        -- pending widget so the row shows as unchanged. Otherwise
        -- find/create an ext-set widget with the new value. Accept
        -- will dispatch via mcp.setExtTag → the mirror file either
        -- gets the line updated in place or appended if new.
        local orig = self:_findExtEntry()
        local origValue = orig and orig.value or nil
        if origValue ~= nil and (self.value or "") == origValue then
            -- Edited back to original. Drop any ext-set widget.
            local kept = {}
            for _, w in ipairs(self._chunk._widgets or {}) do
                local drop = w.extMode and not w.removeMode
                    and (w.tagName or "") == (self.name or "")
                if not drop then table.insert(kept, w) end
            end
            self._chunk._widgets = kept
            return ""
        end
        local found = nil
        for _, w in ipairs(self._chunk._widgets or {}) do
            if w.extMode and not w.removeMode
                and (w.tagName or "") == (self.name or "")
            then
                found = w
                break
            end
        end
        if not found then
            found = PendingWidget:new(self._chunk)
            found.tagName = self.name or ""
            found.extMode = true
            found.extLocatorKind = "absolute"
            found.extLocatorText = self.externaltarget or ""
            found._extLoaded = true
            table.insert(self._chunk._widgets or {}, found)
        end
        found.tagValue = self.value or ""
    end
    return ""
end

function CurrentTagRow:isRemoved()
    return self.status == "removed"
end

function CurrentTagRow:isChanged()
    return self.status == "changed"
end

function CurrentTagRow:isAdded()
    return self.status == "added"
end

function CurrentTagRow:noStatus()
    return self.status == ""
end

-- Accept-error display helpers
function PinnedChunk:acceptError()      return self._acceptError end
function PinnedChunk:hasAcceptError()   return self._acceptError ~= "" end
function PinnedChunk:clearAcceptError() self._acceptError = "" end

-- TagSuggestion methods --------------------------------------------

function TagSuggestion:scoreLabel() return fmtScore(self.score) end

-- HotChunk methods --------------------------------------------------

function HotChunk:pin()
    sys.curation.pin(self.chunkID, self.fileID, self.path)
end

function HotChunk:contentURL()
    if self.path == "" then return "" end
    return "/content" .. self.path
end

function HotChunk:shortPath()
    return compressPath(self.path)
end

function HotChunk:scoreLabel() return fmtScore(self.score) end

-- RelatedTag methods -----------------------------------------------

function RelatedTag:focus()
    if ark and ark._curation then ark._curation:focusTag(self.tag) end
end

function RelatedTag:scoreLabel() return fmtScore(self.score) end

-- DriftPair methods ------------------------------------------------

function DriftPair:shortPathA()
    return compressPath(self.pathA)
end

function DriftPair:shortPathB()
    return compressPath(self.pathB)
end

function DriftPair:scoreLabel() return fmtScore(self.score) end

-- DefinedTag (tag-picker presenter) methods ------------------------

function DefinedTag:new(listItem)
    return session:create(DefinedTag, { viewItem = listItem })
end

function DefinedTag:tag()
    return self.viewItem.baseItem.tag
end

function DefinedTag:description()
    return self.viewItem.baseItem.description
end

function DefinedTag:focus()
    if ark and ark._curation then
        ark._curation:focusTag(self:tag())
    end
end

----------------------------------------------------------------------
-- Curation: Accept-changes (panel-level) methods
----------------------------------------------------------------------

-- Sum of (filled widgets + staged ops) across every PinnedChunk
-- presenter. Drives the `Accept changes (N)` badge in the header.
-- Skips presenters whose chunk has been dismissed (chunkID no longer
-- in sys.curation.pinned) — those entries linger in _presenters
-- until GC.
-- Live-only presenter iteration. Cleans out _presenters entries whose
-- chunkID is no longer in sys.curation.pinned (sweep/dismiss tombstones).
function Curation:_livePresenters()
    local live = {}
    for _, p in ipairs(sys.curation.pinned) do
        live[p.chunkID] = true
    end
    local out = {}
    for chunkID, presenter in pairs(self._presenters or {}) do
        if live[chunkID] then
            table.insert(out, presenter)
        else
            self._presenters[chunkID] = nil
        end
    end
    return out
end

-- Count of cards in editing-dirty state. Drives the "N changed" half
-- of the Accept button label.
function Curation:editedCount()
    local n = 0
    for _, p in ipairs(self:_livePresenters()) do
        if p._isChunkEdited then n = n + 1 end
    end
    return n
end

-- Count of cards with any filled pending widget. Drives the "M
-- pending" half of the Accept button label and the warning dialog.
function Curation:pendingCount()
    local n = 0
    for _, p in ipairs(self:_livePresenters()) do
        n = n + p:filledWidgetCount()
    end
    return n
end

function Curation:acceptDisabled()
    return self:editedCount() == 0
end

function Curation:acceptLabel()
    local n = self:editedCount()
    local m = self:pendingCount()
    if n == 0 and m == 0 then return "Accept (no changes)" end
    if n == 0 and m > 0 then return string.format("Accept — %d pending", m) end
    if n > 0 and m == 0 then return string.format("Accept (%d changed)", n) end
    return string.format("Accept (%d changed, %d pending)", n, m)
end

-- Click handler for the panel-level Accept button.
-- If any cards have unstaged pendings, surface the warning dialog
-- first. Otherwise execute directly.
function Curation:acceptChanges()
    if self:pendingCount() > 0 then
        self._acceptWarnVisible = true
        return
    end
    self:_doAccept()
end

function Curation:confirmAccept()
    self._acceptWarnVisible = false
    self:_doAccept()
end

function Curation:cancelAccept()
    self._acceptWarnVisible = false
end

function Curation:acceptWarnVisible()
    return self._acceptWarnVisible
end

-- Execute Accept: chunk-edit op for each editing-dirty card, plus
-- ext-set / ext-remove for every card's filled ext widgets. Inline
-- pending widgets on non-edited cards are NOT executed — they must
-- be promoted via [edit] first.
function Curation:_doAccept()
    for _, p in ipairs(self:_livePresenters()) do
        local ok = true
        local err = nil
        -- chunk-edit op (editing-dirty cards)
        if p._isChunkEdited then
            local info = p:chunkInfo()
            if p._chunkInfoError ~= "" then
                p._acceptError = "chunkInfo: " .. p._chunkInfoError
                ok = false
            else
                local path = (info and info.path) or p:path()
                local byteStart = tonumber((info and info.byteStart) or 0) or 0
                local byteEnd = tonumber((info and info.byteEnd) or 0) or byteStart
                local text = p._editorContent ~= "" and p._editorContent or p._foldedText
                local _, e = mcp.replaceRegion(path, byteStart, byteEnd, text)
                if e then
                    p._acceptError = "replaceRegion: " .. tostring(e)
                    ok = false
                end
            end
        end
        -- ext widgets (regardless of edit mode)
        if ok then
            for _, w in ipairs(p:filledExtWidgets()) do
                local rec = w:stagedOpRecord()
                local _, e
                if rec.kind == "ext-set" then
                    _, e = mcp.setExtTag(rec.targetSpec, rec.tagName, rec.tagValue or "")
                elseif rec.kind == "ext-remove" then
                    _, e = mcp.removeExtTag(rec.targetSpec, rec.tagName)
                end
                if e then
                    p._acceptError = (rec.kind or "ext") .. ": " .. tostring(e)
                    ok = false
                    break
                end
            end
        end
        -- On success, return the card to clean state.
        if ok then
            p._isChunkEdited = false
            p._editing = false
            p._chunkOriginalText = ""
            p._foldedText = ""
            p._editorContent = ""
            p._savedEditorText = ""
            p._savedPendings = {}
            -- Drop ext widgets that just dispatched; keep any other pendings.
            local kept = {}
            for _, w in ipairs(p._widgets or {}) do
                if not (w:isFilled() and w.extMode) then
                    table.insert(kept, w)
                end
            end
            p._widgets = kept
            p:_ensureEmptyWidget()
            p._acceptError = ""
            -- Force chunk text reload on next access (file changed).
            p._chunkTextLoaded = false
            p._chunkText = ""
        end
    end
end

-- Dismiss every pinned chunk that has no changes (no edits, no filled
-- pendings). Useful after a batch commit.
function Curation:clearUnchanged()
    local toDismiss = {}
    for _, p in ipairs(self:_livePresenters()) do
        if p:noChanges() then
            table.insert(toDismiss, p:chunkID())
        end
    end
    for _, chunkID in ipairs(toDismiss) do
        self._presenters[chunkID] = nil
        sys.curation.dismiss(chunkID)
    end
end

function Curation:hasUnchanged()
    for _, p in ipairs(self:_livePresenters()) do
        if p:noChanges() then return true end
    end
    return false
end

function Curation:noUnchanged()
    return not self:hasUnchanged()
end

----------------------------------------------------------------------
-- Find Connections orchestration
----------------------------------------------------------------------

-- Kick off a find-connections request. Pins the current pinned-chunk
-- IDs as the request's evidence set. Returns immediately; live status
-- arrives via onConnectionsEvent.
function Curation:findConnections()
    if self._connRequestID ~= "" then return end  -- in flight
    local ids = {}
    for _, p in ipairs(sys.curation.pinned) do
        table.insert(ids, p.chunkID)
    end
    if #ids == 0 then
        self._connError = "No pinned chunks"
        return
    end
    local id, err = mcp.findConnections(ids, {})
    if err then
        self._connError = err
        return
    end
    self._connRequestID = id or ""
    self._connStatus = "pending"
    self._connProgress = ""
    self._connElapsed = 0
    self._connError = ""
    self._connThemes = {}
    self._connSharedTags = {}
    self:_ensureSubscriptions()
end

function Curation:clearConnections()
    self._connRequestID = ""
    self._connStatus = ""
    self._connProgress = ""
    self._connElapsed = 0
    self._connError = ""
    self._connThemes = {}
    self._connSharedTags = {}
end

function Curation:noConnections()
    return self._connRequestID == ""
end

function Curation:connectionsInFlight()
    return self._connStatus == "pending" or self._connStatus == "working"
end

function Curation:hasConnectionsError()
    return self._connError ~= ""
end

function Curation:connectionsError()
    return self._connError or ""
end

function Curation:connectionsStatusText()
    if self._connStatus == "" then return "" end
    if self._connStatus == "completed" then
        return string.format("Completed (%d themes, %d shared tags)",
            #(self._connThemes or {}), #(self._connSharedTags or {}))
    end
    if self._connStatus == "errored" then
        return "Errored: " .. (self._connError or "")
    end
    if self._connProgress ~= "" then
        return string.format("%s — %s (%ds)",
            self._connStatus, self._connProgress, self._connElapsed or 0)
    end
    return string.format("%s (%ds)", self._connStatus, self._connElapsed or 0)
end

function Curation:connectionsThemes()
    return self._connThemes or {}
end

function Curation:connectionsSharedTags()
    return self._connSharedTags or {}
end

function Curation:connectionsThemeCount()
    return #(self._connThemes or {})
end

function Curation:connectionsSharedTagCount()
    return #(self._connSharedTags or {})
end

function Curation:noConnectionsThemes()
    return #(self._connThemes or {}) == 0
end

function Curation:noConnectionsSharedTags()
    return #(self._connSharedTags or {}) == 0
end

-- Subscription callback for tmp://connections/<id>.md events. Updates
-- live status; on terminal, fetches the body via mcp.tmp_get and
-- parses themes / shared-tag candidates.
function Curation:onConnectionsEvent(events)
    if type(events) ~= "table" then return end
    if self._connRequestID == "" then return end
    local docPath = CONNECTIONS_DOC_PREFIX .. self._connRequestID .. ".md"
    local terminal = nil
    for _, e in ipairs(events) do
        if e.path == docPath then
            if e.tag == "connections-status" then
                self._connStatus = e.value or ""
                if e.value == "completed" or e.value == "errored" then
                    terminal = e.value
                end
            elseif e.tag == "connections-progress" then
                self._connProgress = e.value or ""
            elseif e.tag == "connections-elapsed" then
                self._connElapsed = tonumber(e.value) or 0
            elseif e.tag == "connections-error" then
                self._connError = e.value or ""
            end
        end
    end
    if terminal == "completed" then
        local body, _ = mcp.tmp_get(docPath)
        self:_parseConnectionsBody(body or "")
    end
end

-- Parse the connections doc body. The body has two markdown sections:
--   ## Themes
--   - @theme-evidence: 1,2,3
--     Theme text
--   ## Shared Tag Candidates
--   - @shared-tag: tagname
--     @shared-tag-value: value
--     @shared-tag-evidence: 1,2,3
function Curation:_parseConnectionsBody(body)
    self._connThemes = {}
    self._connSharedTags = {}
    if not body or body == "" then return end
    local section = nil
    local theme, shared
    local function flushTheme()
        if theme then
            table.insert(self._connThemes, theme)
            theme = nil
        end
    end
    local function flushShared()
        if shared then
            shared._index = #self._connSharedTags + 1
            shared._curation = self
            table.insert(self._connSharedTags, shared)
            shared = nil
        end
    end
    for line in body:gmatch("[^\n]*") do
        local trimmed = line:gsub("^%s+", ""):gsub("%s+$", "")
        if trimmed:sub(1, 2) == "##" then
            flushTheme()
            flushShared()
            if trimmed:lower():find("themes") then section = "themes"
            elseif trimmed:lower():find("shared") then section = "shared"
            else section = nil
            end
        elseif section == "themes" then
            local evidence = trimmed:match("^@theme%-evidence:%s*(.+)")
            if evidence then
                flushTheme()
                theme = session:create(ConnectionTheme)
                theme.evidence = parseChunkIDList(evidence)
                theme.text = ""
            elseif theme and trimmed ~= "" then
                if theme.text == "" then theme.text = trimmed
                else theme.text = theme.text .. " " .. trimmed end
            end
        elseif section == "shared" then
            local tag = trimmed:match("^@shared%-tag:%s*(.+)")
            local val = trimmed:match("^@shared%-tag%-value:%s*(.+)")
            local ev = trimmed:match("^@shared%-tag%-evidence:%s*(.+)")
            if tag then
                flushShared()
                shared = session:create(ConnectionSharedTag)
                shared.tag = tag
                shared.value = ""
                shared.evidence = {}
            elseif val and shared then
                shared.value = val
            elseif ev and shared then
                shared.evidence = parseChunkIDList(ev)
            end
        end
    end
    flushTheme()
    flushShared()
end

-- Fill action: inject pre-filled inline pending widgets into every
-- evidence chunk's pending stack. Cards not currently pinned are
-- skipped silently.
function Curation:fillProposal(idx)
    local proposal = (self._connSharedTags or {})[idx]
    if not proposal then return end
    for _, chunkID in ipairs(proposal.evidence or {}) do
        local presenter = (self._presenters or {})[chunkID]
        if presenter and not presenter._editing then
            local w = PendingWidget:new(presenter)
            w.tagName = proposal.tag or ""
            w.tagValue = proposal.value or ""
            -- Insert above the empty-row so the new widget shows up.
            table.insert(presenter._widgets, 1, w)
        end
    end
end

-- Subscriptions setup. Idempotent — safe to call multiple times.
-- Registers the onpublish callback once and adds subscriptions for
-- both the sweep doc and any in-flight connections doc.
function Curation:_ensureSubscriptions()
    local sid = "curation"
    if not self._onpublishRegistered then
        mcp.onpublish(sid, function(events)
            self:onSweepEvent(events)
            self:onConnectionsEvent(events)
        end)
        self._onpublishRegistered = true
    end
    -- Sweep subscriptions
    mcp.subscribe(sid, { tag = "sweep-status",
        filterFiles = { SWEEP_DOC_PATH } })
    mcp.subscribe(sid, { tag = "sweep-progress",
        filterFiles = { SWEEP_DOC_PATH } })
    -- Connections subscriptions (when a request is in flight)
    if self._connRequestID ~= "" then
        local docPath = CONNECTIONS_DOC_PREFIX .. self._connRequestID .. ".md"
        mcp.subscribe(sid, { tag = "connections-status",
            filterFiles = { docPath } })
        mcp.subscribe(sid, { tag = "connections-progress",
            filterFiles = { docPath } })
        mcp.subscribe(sid, { tag = "connections-elapsed",
            filterFiles = { docPath } })
        mcp.subscribe(sid, { tag = "connections-error",
            filterFiles = { docPath } })
    end
    self._sweepSubscribed = true
end

-- Parse a comma-separated list of chunk IDs into a numeric array.
function parseChunkIDList(s)
    local out = {}
    if not s then return out end
    for id in s:gmatch("[^,%s]+") do
        local n = tonumber(id)
        if n then table.insert(out, n) end
    end
    return out
end

----------------------------------------------------------------------
-- ConnectionSharedTag / ConnectionTheme display helpers
----------------------------------------------------------------------

function ConnectionTheme:evidenceList()
    return table.concat(self.evidence or {}, ", ")
end

function ConnectionTheme:noEvidence()
    return #(self.evidence or {}) == 0
end

function ConnectionSharedTag:evidenceList()
    return table.concat(self.evidence or {}, ", ")
end

function ConnectionSharedTag:label()
    if not self.value or self.value == "" then
        return "@" .. (self.tag or "")
    end
    return "@" .. (self.tag or "") .. ": " .. self.value
end

function ConnectionSharedTag:fill()
    if self._curation and self._index > 0 then
        self._curation:fillProposal(self._index)
    end
end

----------------------------------------------------------------------
-- PendingWidget methods
----------------------------------------------------------------------

function PendingWidget:new(chunk)
    return session:create(PendingWidget, { _chunk = chunk })
end

local function trim(s)
    return (s or ""):gsub("^%s+", ""):gsub("%s+$", "")
end

function PendingWidget:isFilled()
    -- A widget is "filled" once it has a tag name. Remove ops are
    -- valid even with empty value; add ops without value still apply
    -- (bare `@tag` is a meaningful annotation).
    return trim(self.tagName) ~= ""
end

function PendingWidget:toggleRemove()
    self.removeMode = not self.removeMode
end

function PendingWidget:toggleExt()
    if self.extMode then
        -- Force-on when chunk is read-only.
        if self._chunk and self._chunk:readOnly() then return end
        self.extMode = false
        return
    end
    self.extMode = true
    if not self._extLoaded then
        self:_loadExtSuggestion()
    end
end

function PendingWidget:_loadExtSuggestion()
    self._extLoaded = true
    if not self._chunk then return end
    local sug, err = mcp.suggestExtLocator(self._chunk:chunkID())
    if err or sug == nil then return end
    self.extBase = sug.base or "path"
    self.extBaseValue = sug.baseValue or self._chunk:path()
    self.extLocatorKind = sug.locatorKind or "absolute"
    self.extLocatorText = sug.locator or sug.locatorText or ""
    self._withinFileDupCount = tonumber(sug.withinFileDupCount) or 0
    if type(sug.crossFileScope) == "table" then
        self._extScopeChunks = tonumber(sug.crossFileScope.chunks) or 0
        self._extScopeFiles = tonumber(sug.crossFileScope.files) or 0
    end
end

function PendingWidget:extToggleLocked()
    return self._chunk and self._chunk:readOnly()
end

function PendingWidget:notExt()
    return not self.extMode
end

function PendingWidget:noDupFlag()
    return not self:hasDupFlag()
end

function PendingWidget:noScopeReadout()
    return not self:hasScopeReadout()
end

function PendingWidget:locatorBare()
    return self.extLocatorKind == "bare"
end

function PendingWidget:kill()
    if self._chunk then self._chunk:removeWidget(self) end
end

-- Bound to ui-event-sl-change on the base dropdown. The ui-value
-- binding has already updated self.extBase; this handler refreshes
-- the locator defaults to match the new base.
function PendingWidget:onBaseChange()
    if self.extBase ~= "uuid" and self.extBase ~= "path" then return end
    self._extLoaded = false
    self:_loadExtSuggestion()
end

-- Bound to ui-event-sl-change on the locator-kind dropdown. The
-- ui-value binding has already updated self.extLocatorKind; this
-- handler clears the locator text when kind is bare.
function PendingWidget:onLocatorKindChange()
    if self.extLocatorKind == "bare" then
        self.extLocatorText = ""
    end
end

function PendingWidget:dupFlag()
    if (self._withinFileDupCount or 0) <= 1 then return "" end
    local shortVal = self.extBaseValue or ""
    if #shortVal > 12 then shortVal = shortVal:sub(1, 8) .. "..." end
    return string.format("UUID: %s (×%d in this file)",
        shortVal, self._withinFileDupCount)
end

function PendingWidget:hasDupFlag()
    return (self._withinFileDupCount or 0) > 1
end

function PendingWidget:scopeReadout()
    local c = self._extScopeChunks or 0
    local f = self._extScopeFiles or 0
    if c == 0 and f == 0 then return "" end
    return string.format("will route to %d chunk%s across %d file%s",
        c, c == 1 and "" or "s",
        f, f == 1 and "" or "s")
end

function PendingWidget:hasScopeReadout()
    return (self._extScopeChunks or 0) > 0 or (self._extScopeFiles or 0) > 0
end

-- Tab-out auto-add: called from a viewdef keypress handler when the
-- user tabs past the last input of this widget. If this widget is
-- filled AND is the last in its chunk's stack, append a new empty
-- widget below it. Empty widgets don't auto-add (lets focus escape
-- the stack naturally).
function PendingWidget:autoAddOnTab()
    if not self:isFilled() then return end
    if not self._chunk then return end
    local widgets = self._chunk._widgets
    if widgets[#widgets] ~= self then return end
    self._chunk:addWidget()
end

-- Compose the @ext target spec from current ext fields. Used by
-- stagedOpRecord() when extMode is on.
function PendingWidget:targetSpec()
    local base = self.extBaseValue or ""
    local kind = self.extLocatorKind or ""
    local text = self.extLocatorText or ""
    if kind == "bare" or text == "" then
        return base
    end
    if kind == "string" then
        return base .. ':"' .. text .. '"'
    elseif kind == "regex" then
        return base .. ":/" .. text .. "/"
    end
    -- absolute (or anything else): emit as-is
    return base .. ":" .. text
end

-- Build a record for the parent PinnedChunk's _stagedOps buffer.
function PendingWidget:stagedOpRecord()
    local tag = trim(self.tagName)
    local value = trim(self.tagValue)
    if self.extMode then
        if self.removeMode then
            return { kind = "ext-remove", tagName = tag, tagValue = value, targetSpec = self:targetSpec() }
        end
        return { kind = "ext-set", tagName = tag, tagValue = value, targetSpec = self:targetSpec() }
    end
    if self.removeMode then
        return { kind = "inline-remove", tagName = tag, tagValue = value }
    end
    return { kind = "inline-add", tagName = tag, tagValue = value }
end
