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

local function nowSeconds()
    return os.time()
end

local function fmtScore(s)
    return string.format("%.2f", s or 0)
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
    _sweepBusy = false,
    _sweepResult = "",
    -- Tag picker: lazy-loaded list of all defined tags via
    -- mcp.definedTags(). Each entry is { tag = "...", description = "..." }.
    _definedTags = EMPTY,
    _definedTagsLoaded = false,
    -- Presenter registry: chunkID -> Ark.PinnedChunk. Lets us reach a
    -- card's per-card state (widget stack, staged ops) without walking
    -- the ViewList. Cleared on dismiss; entries for sweept-away pins
    -- get cleaned out on the next totalPendingCount() pass.
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
    -- Widget stack (empty-start invariant: always at least one widget)
    _widgets = EMPTY,
    -- Per-card staged-ops buffer: array of {kind, tagName, tagValue, targetSpec}.
    -- kind ∈ {"inline-add", "inline-remove", "ext-set", "ext-remove"}.
    _stagedOps = EMPTY,
    -- Lazy chunkInfo (mcp.chunkInfo): {chunkID, fileID, path, range,
    -- byteStart, byteEnd, writable, commentSyntax}
    _chunkInfo = EMPTY,
    _chunkInfoLoaded = false,
    _chunkInfoError = "",
    -- Lazy tag suggestions (mcp.suggestTagNames)
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

-- Sweep -------------------------------------------------------------

function Curation:sweepNow()
    if self._sweepBusy then return end
    self._sweepBusy = true
    local result, err = mcp.sweepHotCorrelations()
    self._sweepBusy = false
    if err then
        self._sweepResult = "Error: " .. tostring(err)
        return
    end
    if type(result) == "table" and result.status == "embedding-unavailable" then
        self._sweepResult = "Embedding model unavailable — sweep skipped"
        return
    end
    if type(result) == "table" then
        self._sweepResult = string.format(
            "Swept in %d ms — %d tags rebuilt, %d touched, %d EDs / %d ECs changed%s",
            tonumber(result.durationMs) or 0,
            tonumber(result.tagsRebuilt) or 0,
            tonumber(result.tagsTouched) or 0,
            tonumber(result.changedEDs) or 0,
            tonumber(result.changedECs) or 0,
            result.fromScratch and " (full rebuild)" or "")
    else
        self._sweepResult = "Sweep complete"
    end
    -- A fresh sweep may have changed the focused tag's HC entries; reload.
    if self:isFocused() then
        self:focusTag(self.focusedTag)
    end
end

function Curation:sweepBusy()
    return self._sweepBusy
end

function Curation:sweepStatusText()
    if self._sweepBusy then return "Sweeping…" end
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
        _stagedOps = {},
        _chunkInfo = {},
        _suggestions = {},
    })
    -- Empty-start invariant: always at least one empty widget visible.
    table.insert(p._widgets, PendingWidget:new(p))
    -- Register with the parent Curation so totalPendingCount can find us.
    if ark and ark._curation then
        ark._curation._presenters[p:chunkID()] = p
    end
    return p
end

-- Accessors over the Go-mirrored entry (viewItem.baseItem)
function PinnedChunk:chunkID()  return self.viewItem.baseItem.chunkID end
function PinnedChunk:path()     return self.viewItem.baseItem.path end
function PinnedChunk:pinnedAt() return self.viewItem.baseItem.pinnedAt end

-- Dismiss with confirmation when there are pending changes.
function PinnedChunk:dismiss()
    if self:hasPending() then
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

function PinnedChunk:commentSyntax()
    local info = self:chunkInfo()
    return (info and info.commentSyntax) or ""
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
-- the stack might have just gone empty.
function PinnedChunk:_ensureEmptyWidget()
    if #self._widgets == 0 then
        table.insert(self._widgets, PendingWidget:new(self))
    end
end

function PinnedChunk:widgets()
    self:_ensureEmptyWidget()
    return self._widgets
end

function PinnedChunk:addWidget()
    table.insert(self._widgets, PendingWidget:new(self))
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

function PinnedChunk:pendingCount()
    return self:filledWidgetCount() + #self._stagedOps
end

function PinnedChunk:hasPending()
    return self:pendingCount() > 0
end

function PinnedChunk:noPending()
    return self:pendingCount() == 0
end

function PinnedChunk:cannotStage()
    return self:filledWidgetCount() == 0
end

function PinnedChunk:cannotRevert()
    return #self._stagedOps == 0
end

-- Stage / Revert ---------------------------------------------------

function PinnedChunk:stage()
    -- Move every filled widget into the staged-ops buffer; drop
    -- staged widgets from the stack. Empty widgets stay.
    local kept = {}
    for _, w in ipairs(self._widgets) do
        if w:isFilled() then
            table.insert(self._stagedOps, w:stagedOpRecord())
        else
            table.insert(kept, w)
        end
    end
    self._widgets = kept
    self:_ensureEmptyWidget()
    self._acceptError = ""
end

function PinnedChunk:revert()
    -- Pop staged ops back into widgets so the user can re-edit.
    local newWidgets = {}
    for _, op in ipairs(self._stagedOps) do
        local w = PendingWidget:new(self)
        w.tagName = op.tagName or ""
        w.tagValue = op.tagValue or ""
        if op.kind == "inline-remove" or op.kind == "ext-remove" then
            w.removeMode = true
        end
        if op.kind == "ext-set" or op.kind == "ext-remove" then
            w.extMode = true
            -- Best-effort reconstruction: stash targetSpec into locatorText
            -- so the user sees what they had. The ext fields proper are
            -- repopulated from suggestExtLocator on next toggleExt cycle.
            w.extLocatorKind = "absolute"
            w.extLocatorText = op.targetSpec or ""
            w._extLoaded = true
        end
        table.insert(newWidgets, w)
    end
    -- Keep any non-filled widgets the user had below the staged ones.
    for _, w in ipairs(self._widgets) do
        if not w:isFilled() then table.insert(newWidgets, w) end
    end
    self._widgets = newWidgets
    self._stagedOps = {}
    self:_ensureEmptyWidget()
    self._acceptError = ""
end

-- Execute staged ops via mcp bridges. Implicit-stages any still-filled
-- widgets first. Returns true on success, false on error (sets
-- _acceptError). On success clears _stagedOps.
function PinnedChunk:executeStagedOps()
    -- Implicit-stage any unstaged-but-filled widgets so Accept doesn't
    -- leave a widget behind.
    self:stage()
    if #self._stagedOps == 0 then return true end

    local info = self:chunkInfo()
    if self._chunkInfoError ~= "" then
        self._acceptError = "chunkInfo: " .. self._chunkInfoError
        return false
    end
    local path = (info and info.path) or self:path()
    local byteStart = tonumber((info and info.byteStart) or 0) or 0
    local comment = self:commentSyntax()

    for _, op in ipairs(self._stagedOps) do
        local ok, err = self:_dispatchOp(op, path, byteStart, comment)
        if not ok then
            self._acceptError = err or "unknown error"
            return false
        end
    end
    self._stagedOps = {}
    self._acceptError = ""
    return true
end

function PinnedChunk:_dispatchOp(op, path, byteStart, comment)
    if op.kind == "inline-add" then
        local line = self:_formatInline(op.tagName, op.tagValue, comment)
        local _, err = mcp.replaceRegion(path, byteStart, byteStart, line)
        if err then return false, err end
        return true
    elseif op.kind == "inline-remove" then
        -- Per-line removal is a /mini-spec follow-up. v1 inserts a
        -- removal marker so the user can see we tried.
        local marker = self:_formatInline("removed-" .. op.tagName, op.tagValue or "", comment)
        local _, err = mcp.replaceRegion(path, byteStart, byteStart, marker)
        if err then return false, err end
        return true
    elseif op.kind == "ext-set" then
        local _, err = mcp.setExtTag(op.targetSpec, op.tagName, op.tagValue or "")
        if err then return false, err end
        return true
    elseif op.kind == "ext-remove" then
        local _, err = mcp.removeExtTag(op.targetSpec, op.tagName)
        if err then return false, err end
        return true
    end
    return false, "unknown op kind: " .. tostring(op.kind)
end

-- Format an inline tag insertion per the chunker's comment syntax.
-- Markdown chunks have empty commentSyntax and get bare `@tag: value`.
function PinnedChunk:_formatInline(tag, value, comment)
    local body
    if value == "" then
        body = "@" .. tag
    else
        body = "@" .. tag .. ": " .. value
    end
    if comment == "" then
        return body .. "\n"
    end
    return comment .. " " .. body .. "\n"
end

-- Accept-error display helpers
function PinnedChunk:acceptError()      return self._acceptError end
function PinnedChunk:hasAcceptError()   return self._acceptError ~= "" end

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
function Curation:totalPendingCount()
    local live = {}
    for _, p in ipairs(sys.curation.pinned) do
        live[p.chunkID] = true
    end
    local n = 0
    for chunkID, presenter in pairs(self._presenters or {}) do
        if live[chunkID] then
            n = n + presenter:pendingCount()
        else
            self._presenters[chunkID] = nil
        end
    end
    return n
end

function Curation:noPendingChanges()
    return self:totalPendingCount() == 0
end

-- Iterate live presenters; for each card with any work, execute its
-- staged ops via the per-card executeStagedOps (which implicit-stages
-- first). Per-card errors land on the card's _acceptError; the panel
-- continues with the next card.
function Curation:acceptChanges()
    for _, p in ipairs(sys.curation.pinned) do
        local presenter = (self._presenters or {})[p.chunkID]
        if presenter and (presenter:filledWidgetCount() > 0 or #presenter._stagedOps > 0) then
            presenter:executeStagedOps()
        end
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
