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
-- For v1, sys.curation.dismiss and sys.curation.sweepOlder are
-- mocked in Lua (in-place mutation of sys.curation.pinned so
-- per-item presenter identity is preserved between mutations).
-- Real Go mutators + the entry-identity fix in refreshLuaTable
-- land in the /mini-spec follow-up — see PLAN-CURATE-CHUNK.md.

local SUGGESTIONS_K = 8
local TOP_CHUNKS_K = 12
local RELATED_TAGS_K = 8

local function nowSeconds()
    return os.time()
end

----------------------------------------------------------------------
-- Lua-side mocks for sys.curation.dismiss / sweepOlder.
-- Both mutate sys.curation.pinned in place so the surviving entry
-- tables keep their identity (preserving the ViewList presenter
-- reuse rule view.BaseItem == item).
--
-- Registered lazily: sys may not exist at module-load time (the
-- runtime registers sys via registerLuaFunctions AFTER app.lua first
-- loads). ensureCurationMocks() is called from every callsite that
-- needs them; it's idempotent. Drops out when the real Go mutators
-- land (Task #8) — their presence makes the `not sys.curation.dismiss`
-- check false.
----------------------------------------------------------------------

local function ensureCurationMocks()
    if not sys or not sys.curation then return end
    if not sys.curation.dismiss then
        sys.curation.dismiss = function(chunkID)
            chunkID = tonumber(chunkID) or 0
            if chunkID == 0 then return end
            for i = #sys.curation.pinned, 1, -1 do
                if sys.curation.pinned[i].chunkID == chunkID then
                    table.remove(sys.curation.pinned, i)
                    return
                end
            end
        end
    end
    if not sys.curation.sweepOlder then
        sys.curation.sweepOlder = function()
            local pins = sys.curation.pinned
            if #pins <= 1 then return end
            for i = #pins, 2, -1 do
                table.remove(pins, i)
            end
        end
    end
end

-- Try once at load. If sys is nil here, the per-call sites will
-- register on demand.
ensureCurationMocks()

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
    -- Tag picker: lazy-loaded list of all defined tags. The Lua-side
    -- popen + text parse is a stop-gap; PLAN.md item "HTTP-JSON calls
    -- from Lua" + Task #9 (mcp.definedTags bridge) replace it.
    _definedTags = EMPTY,
    _definedTagsLoaded = false,
})
Curation = Ark.Curation

-- Presenter (itemWrapper) for each entry in sys.curation.pinned.
-- See ~/.ark/patterns/itemwrapper-presenters.md (or `ark ui patterns`).
Ark.PinnedChunk = session:prototype("Ark.PinnedChunk", {
    viewItem = EMPTY,
    _suggestions = EMPTY,
    _suggestionsLoaded = false,
    _suggestionsError = "",
})
PinnedChunk = Ark.PinnedChunk

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
    instance._focusedChunks = instance._focusedChunks or {}
    instance._focusedRelated = instance._focusedRelated or {}
    instance._focusedDrift = instance._focusedDrift or {}
    return instance
end

function Curation:mutate()
    if self._focusedChunks == nil then self._focusedChunks = {} end
    if self._focusedRelated == nil then self._focusedRelated = {} end
    if self._focusedDrift == nil then self._focusedDrift = {} end
    if self._newCutoff == nil then self._newCutoff = 0 end
    if self._lastViewedAt == nil then self._lastViewedAt = 0 end
    if self._definedTags == nil then self._definedTags = {} end
    if self._definedTagsLoaded == nil then self._definedTagsLoaded = false end
    -- Pre-retrofit field — would shadow the :pinned() method.
    if self.pinned ~= nil then self.pinned = nil end
    if self._stateLoaded ~= nil then self._stateLoaded = nil end
end

-- Lazy-load the corpus's defined-tag list via the ark CLI.
-- Stop-gap: text-parse "tag -- description" lines from `ark tag defs`.
-- Replaced when the mcp.definedTags Lua bridge lands (Task #9).
function Curation:loadDefinedTags()
    if self._definedTagsLoaded then return end
    self._definedTagsLoaded = true
    local bin = (os.getenv("HOME") or "") .. "/.ark/ark"
    local handle = io.popen('"' .. bin .. '" tag defs 2>/dev/null')
    if not handle then return end
    local seen = {}
    local out = {}
    for line in handle:lines() do
        local tag, desc = line:match("^(%S+)%s+%-%-%s*(.*)$")
        if tag and not seen[tag] then
            seen[tag] = true
            table.insert(out, { tag = tag, description = desc or "" })
        end
    end
    handle:close()
    table.sort(out, function(a, b) return a.tag < b.tag end)
    self._definedTags = out
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
    ensureCurationMocks()
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
    return session:create(PinnedChunk, {
        viewItem = listItem,
        _suggestions = {},
        _suggestionsLoaded = false,
        _suggestionsError = "",
    })
end

-- Accessors over the Go-mirrored entry (viewItem.baseItem)
function PinnedChunk:chunkID()  return self.viewItem.baseItem.chunkID end
function PinnedChunk:path()     return self.viewItem.baseItem.path end
function PinnedChunk:pinnedAt() return self.viewItem.baseItem.pinnedAt end

function PinnedChunk:dismiss()
    ensureCurationMocks()
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

-- TagSuggestion methods --------------------------------------------

function TagSuggestion:scoreLabel()
    return string.format("%.2f", self.score or 0)
end

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

function HotChunk:scoreLabel()
    return string.format("%.2f", self.score or 0)
end

-- RelatedTag methods -----------------------------------------------

function RelatedTag:focus()
    if ark and ark._curation then ark._curation:focusTag(self.tag) end
end

function RelatedTag:scoreLabel()
    return string.format("%.2f", self.score or 0)
end

-- DriftPair methods ------------------------------------------------

function DriftPair:shortPathA()
    return compressPath(self.pathA)
end

function DriftPair:shortPathB()
    return compressPath(self.pathB)
end

function DriftPair:scoreLabel()
    return string.format("%.2f", self.score or 0)
end

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
