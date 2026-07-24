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

local function nowSeconds()
    return os.time()
end

local function fmtScore(s)
    return string.format("%.2f", s or 0)
end

-- Trim leading/trailing ASCII whitespace.
local function trim(s)
    return (s or ""):gsub("^%s+", ""):gsub("%s+$", "")
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

-- Tag-line text transforms. Operate on string-as-bytes; no UTF-8
-- awareness needed (tag lines are ASCII). These feed the approval
-- shims' internal-disposition write paths below.

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
-- Approval shims — the ext-candidate machinery's Lua face
----------------------------------------------------------------------
-- @prototype: go-ext-hooks
-- Shims for the future Go bindings (PENDING #65), named and shaped
-- like the bindings the /mini-spec pass will register on `mcp`:
--   mcp.extAccept(target, tag, value, opts) → result, err
--   mcp.extRemove(target, tag, opts)        → result, err
-- One call = one-shot candidate+accept (the loaded gun). `opts`
-- carries {disposition, replace} (accept) or {disposition} (remove).
-- `result` is {disposition = <actually applied>} so callers can be
-- honest about what happened. Target shapes:
--   external: TARGET spec string (BASE or BASE:NARROWER)
--   internal: {path, byteStart, byteEnd, text} — shim-only; the
--             real hook resolves a spec server-side.
-- The implementations ride the old primitives (setExtTag /
-- removeExtTag / replaceRegion) with the markdown stencil only
-- (per-chunker stencils are the real hook's job). The machinery's
-- ledger trail (candidate line, @count, judgment) DOES NOT exist on
-- this path — no caller may claim it does.

local function extAccept(target, tag, value, opts)
    opts = opts or {}
    if (opts.disposition or "external") == "external" then
        -- setExtTag's set semantics approximate both the add and
        -- replace cells of the external disposition.
        local _, err = mcp.setExtTag(target, tag, value or "")
        if err then return nil, err end
        return { disposition = "external" }, nil
    end
    -- internal: text transform + replaceRegion (markdown stencil).
    local text = target.text or ""
    local newText
    if opts.replace and findTagLine(text, tag) then
        newText = replaceTagLine(text, tag, value or "")
    else
        -- add, or replace degrading to add (no matching line).
        newText = prependTag(text, tag, value or "")
    end
    local _, err = mcp.replaceRegion(target.path, target.byteStart,
        target.byteEnd, newText)
    if err then return nil, err end
    return { disposition = "internal" }, nil
end

local function extRemove(target, tag, opts)
    opts = opts or {}
    if (opts.disposition or "external") == "external" then
        local _, err = mcp.removeExtTag(target, tag)
        if err then return nil, err end
        return { disposition = "external" }, nil
    end
    -- internal remove: the machinery has no verb for this yet (the
    -- internal-remove gap); the shim edits the text directly.
    local text = target.text or ""
    if not findTagLine(text, tag) then
        return nil, "no inline @" .. tag .. ": line in this chunk"
    end
    local _, err = mcp.replaceRegion(target.path, target.byteStart,
        target.byteEnd, removeTagLine(text, tag))
    if err then return nil, err end
    return { disposition = "internal" }, nil
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
    -- Presenter registry: chunkID -> Ark.PinnedChunk. Lets us reach a
    -- card's per-card state (widget stack, edit-mode flags) without
    -- walking the ViewList. Cleared on dismiss; entries for sweept-
    -- away pins get cleaned out on the next pendingCount() pass.
    _presenters = EMPTY,
})
Curation = Ark.Curation

-- Presenter (itemWrapper) for each entry in sys.curation.pinned.
-- See ~/.ark/patterns/itemwrapper-presenters.md (or `ark ui patterns`).
-- The presenter carries the per-card workshop state: proposal widget
-- stack, lazy chunkInfo, lazy tag suggestions, edit-mode state.
Ark.PinnedChunk = session:prototype("Ark.PinnedChunk", {
    viewItem = EMPTY,
    -- Bridge-fire nonce. Bump whenever the iframe or editor bridge
    -- JS needs to re-fire. The bridge methods embed this in the
    -- emitted string so the engine's change-detection sees a fresh
    -- value and re-evaluates ui-code.
    _bridgeNonce = 0,
    -- Proposal widget stack (empty-start invariant: always at least
    -- one widget). Independent of edit mode — tag authoring and
    -- content editing are separate paths.
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
    -- Edit-mode state (content edits only — no tag authoring here).
    _editing = false,
    _chunkOriginalText = "",      -- snapshot at [edit] time; dirty check
    _isChunkEdited = false,        -- JS-pushed: editor.getDoc() != original
    _savedEditorText = "",         -- editor draft preserved at [revert]; consumed by next [edit]
    _editorInitialText = "",       -- doc the editor opens on (draft or original); published to JS
    _editorContent = "",           -- JS-synced editor text. Read by Accept.
    -- currentTagsView() result cache. Keyed by a signature of the
    -- inputs (chunk text + ext-tag list). When the signature is
    -- unchanged across calls, the cached row list is returned
    -- verbatim — preserving Lua identity for ViewList so DOM
    -- presenters are reused.
    _currentTagsViewSig = "",
    _currentTagsViewRows = EMPTY,
    -- Per-row reuse table: key = `kind:name:occurrence` →
    -- Ark.CurrentTagRow. Even when the signature changes (text
    -- updated), rows with matching keys are reused; only their
    -- text-derived fields update.
    _currentTagRowCache = EMPTY,
    -- Ext-tag cache: scraped from iframe's <ark-ext-tags> children on
    -- load. Persists across edit-mode transitions so current-tags
    -- continues to surface ext rows without the iframe.
    _extTags = EMPTY,
    -- Inline-tag cache: scraped from the iframe's body-level <ark-tag>
    -- elements (those NOT inside <ark-ext-tags>). Catches @id and any
    -- mid-chunk @name: value tags that ParseTagBlock misses (it only
    -- handles the leading-block tags).
    _inlineScrapedTags = EMPTY,
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

-- A loaded gun in a PinnedChunk's widget stack (renamed from
-- Ark.PendingWidget — nothing is pending anymore). Each widget
-- authors one tag operation against the parent chunk — add/replace
-- (default) or remove (when removeMode); written into the file body
-- (internal disposition) or routed via @ext mirror (external) — and
-- fires it in one gesture through the approval shims.
Ark.ProposalWidget = session:prototype("Ark.ProposalWidget", {
    _chunk = EMPTY,                -- parent Ark.PinnedChunk
    tagName = "",
    tagValue = "",
    removeMode = false,
    -- The machinery's disposition taxonomy. Defaults internal for
    -- writable chunks; locked external when the chunk can't host an
    -- inline tag (the degrade, surfaced as a default).
    disposition = "internal",      -- "internal" | "external"
    -- The machinery's replace token: collapse the tag's values to
    -- this one instead of adding. Pre-set true when the tag already
    -- exists on the chunk (visible default, not a hidden rule).
    replaceMode = false,
    _fireError = "",               -- last fire error; widget keeps state for retry
    -- External-targeting fields, populated on first switch to
    -- external via mcp.suggestExtLocator. The widget reads
    -- `locatorText` from the Go result (`locator` field has a known
    -- bug — same value as locatorKind; tracked as a /mini-spec
    -- follow-up).
    extBase = "",                  -- "uuid" | "path"
    extBaseValue = "",             -- UUID string or absolute path
    extLocatorKind = "",           -- "string" | "regex" | "absolute" | "bare"
    extLocatorText = "",           -- locator value (empty for bare)
    _extScopeChunks = 0,
    _extScopeFiles = 0,
    _withinFileDupCount = 0,
    _extLoaded = false,            -- true after first suggestExtLocator
})
ProposalWidget = Ark.ProposalWidget

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

-- Current-tags row: a read-only view of a single tag on a pinned
-- chunk, as it stands on disk. Inline rows (parsed/scraped from the
-- chunk text) and ext rows (from the iframe <ark-ext-tags> scrape)
-- render with the same shape. The row's affordances (rem checkbox,
-- row click) load pre-filled widgets into the proposal stack; the
-- row itself never fires anything.
Ark.CurrentTagRow = session:prototype("Ark.CurrentTagRow", {
    name = "",
    value = "",
    kind = "inline",          -- "inline" | "ext"
    -- 1-based ordinal of this (name) among same-name occurrences in
    -- the chunk text. Keeps the reuse-cache key unique when multiple
    -- tags share a name.
    occurrence = 1,
    externalfile = "",        -- ext only: mirror file path
    externaltarget = "",      -- ext only: TARGET spec
    _chunk = EMPTY,           -- back-reference for widget loading
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
})
ExtTagRow = Ark.ExtTagRow

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
    local filter = trim(self._focusInput):lower()
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
    local tag = trim(self._focusInput)
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
        -- Start at 1 (not 0) so the iframe bridge's first emitted JS
        -- string already carries a non-default nonce. Initial-fire
        -- semantics for ui-code only run when the stored value is
        -- truthy AND has gone through a change; pre-bumping avoids
        -- a sticky default that masks real transitions.
        _bridgeNonce = 1,
    })
    -- Empty-start invariant: always at least one empty widget visible.
    table.insert(p._widgets, ProposalWidget:new(p))
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
-- the stack might have just gone empty.
function PinnedChunk:_ensureEmptyWidget()
    if #self._widgets == 0 then
        table.insert(self._widgets, ProposalWidget:new(self))
    end
end

-- Returns the widget stack. Visible in all states — tag authoring is
-- independent of edit mode.
function PinnedChunk:widgets()
    self:_ensureEmptyWidget()
    return self._widgets
end

function PinnedChunk:addWidget()
    table.insert(self._widgets, ProposalWidget:new(self))
end

-- Spreadsheet-like tab handling. When the [+] button receives
-- focus (via Tab from the last widget's field, or via a mouse
-- click), this fires: ensure there's an empty widget at the
-- bottom of the stack, then JS-focus its tag-name input so the
-- user can immediately start typing. Without the JS focus shift,
-- browser tab focus would skip past the (newly-inserted) widget
-- since it's earlier in DOM than the just-focused [+].
function PinnedChunk:onPlusFocused()
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

function PinnedChunk:pendingCount()
    return self:filledWidgetCount()
end

function PinnedChunk:noPending()
    return self:pendingCount() == 0
end

-- True when dismissing would lose work: unsaved editor changes or
-- filled (unfired) proposal widgets. Guards the dismiss confirmation
-- and Clear unchanged.
function PinnedChunk:hasChanges()
    return self._isChunkEdited or self:filledWidgetCount() > 0
end

function PinnedChunk:noChanges()
    return not self:hasChanges()
end

-- Fire path ---------------------------------------------------------

-- Fire one proposal widget through the approval shims (one-shot
-- candidate+accept). On success the widget leaves the stack and the
-- card's tag data refreshes; on error the widget keeps its state for
-- retry. Internal targets are re-fetched fresh at fire time — the
-- chunk's byte range shifts after every write. (Shim limitation: a
-- fire racing the async reindex of a just-fired write can still see
-- a stale range; the real Go hook resolves targets server-side.)
function PinnedChunk:fireWidget(widget)
    if not widget:isFilled() then return end
    widget._fireError = ""
    local tag = widget:trimmedTag()
    local disposition = widget:effectiveDisposition()
    local result, err
    if disposition == "external" then
        if not widget._extLoaded then widget:_loadExtSuggestion() end
        local target = widget:targetSpec()
        if widget.removeMode then
            result, err = extRemove(target, tag, { disposition = "external" })
        else
            result, err = extAccept(target, tag, widget.tagValue,
                { disposition = "external", replace = widget.replaceMode })
        end
    else
        -- Fresh info + text: byte ranges go stale after any write.
        self._chunkInfoLoaded = false
        self._chunkTextLoaded = false
        local info = self:chunkInfo()
        if self._chunkInfoError ~= "" then
            widget._fireError = "chunkInfo: " .. self._chunkInfoError
            return
        end
        local target = {
            path = (info and info.path) or self:path(),
            byteStart = tonumber((info and info.byteStart) or 0) or 0,
            byteEnd = tonumber((info and info.byteEnd) or 0) or 0,
            text = self:chunkText() or "",
        }
        if widget.removeMode then
            result, err = extRemove(target, tag, { disposition = "internal" })
        else
            result, err = extAccept(target, tag, widget.tagValue,
                { disposition = "internal", replace = widget.replaceMode })
        end
    end
    if err then
        widget._fireError = tostring(err)
        return
    end
    -- Honest feedback: say what actually happened. The shims write
    -- no candidate ledger — never claim one.
    local verb = widget.removeMode and "removed" or "applied"
    local how = (result and result.disposition == "external")
        and "routed via @ext mirror" or "written into the file"
    mcp:notify(string.format("@%s %s — %s", tag, verb, how), "success")
    self:removeWidget(widget)
    self:refreshTags()
end

-- Post-fire refresh: drop the text/info/scrape caches and reload the
-- iframe so the rendered chunk, ext-tag indicator, and current-tags
-- rows all reflect the just-written state.
function PinnedChunk:refreshTags()
    self._chunkTextLoaded = false
    self._chunkText = ""
    self._chunkInfoLoaded = false
    -- Drop the stale scrape and force currentTagsView to re-derive —
    -- the signature alone can miss a same-shape change (e.g. an
    -- internal remove leaves the ext count and text prefix intact).
    self._inlineScrapedTags = {}
    self._currentTagsViewSig = ""
    self:bumpBridgeNonce()
    local cid = self:chunkID()
    mcp.code = string.format([[
// cur-refresh-%d-%d
(function() {
    const root = document.querySelector('[data-cur-chunkid="%d"]');
    if (!root) return;
    const iframe = root.querySelector('.cur-pin-iframe');
    if (iframe) {
        delete iframe.dataset.armed;
        try { iframe.contentWindow.location.reload(); } catch (e) {}
    }
})();
]], cid, self._bridgeNonce or 0, cid)
end

-- Icon for the [edit|revert] button. pencil-square when not in edit
-- mode; arrow-counterclockwise when editing.
function PinnedChunk:editButtonIcon()
    if self._editing then return "arrow-counterclockwise" end
    return "pencil-square"
end

-- True when the icon should render in accent color (the editor holds
-- unsaved changes); false for the gray clean state. Both states use
-- the same icon glyph; only the color differs.
function PinnedChunk:editButtonAccent()
    return self._isChunkEdited
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
function PinnedChunk:editorInitialText() return self._editorInitialText or "" end

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
--   editing: import /ark-markdown-editor.js, mount createInkArkEditor
--            on the initial doc (original or preserved draft), start
--            polling for dirty/content sync back to Lua via
--            updateValue. No fold pass — tag operations never enter
--            the editor.
--   not editing: destroy any prior editor instance, clear poller.
function PinnedChunk:editorBridgeCode()
    local cid = self:chunkID()
    if self._editing then
        return string.format([[
// editor-bridge-mount-%d-%d
(async function() {
    const root = document.querySelector('[data-cur-chunkid="%d"]');
    if (!root) return;
    const editorBlock = root.querySelector('.cur-pin-chunk-editor');
    if (!editorBlock) return;
    if (root.dataset.editorArmed === '1') return;
    root.dataset.editorArmed = '1';
    const mount  = editorBlock.querySelector('.cur-pin-editor-mount');
    const origEl = editorBlock.querySelector('.cur-pin-editor-orig');
    const initEl = editorBlock.querySelector('.cur-pin-editor-initial');
    const pathEl = editorBlock.querySelector('.cur-pin-editor-path');
    const dirtyInput   = editorBlock.querySelector('.cur-pin-editor-dirty-input');
    const contentInput = editorBlock.querySelector('.cur-pin-editor-content-input');
    if (!mount || !origEl || !initEl || !pathEl) return;
    const original = origEl.textContent || '';
    const initial  = initEl.textContent || '';
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
    if (initial !== original && editor && editor.update) {
        editor.update(initial);
    }
    window['__curEditor_%d'] = editor;
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
})();
]], cid, self._bridgeNonce or 0, cid, cid, cid, cid)
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
-- Content edits only: tag authoring fires through the approval
-- shims and never touches the editor.

-- [edit] click handler. Snapshot the original, pick the initial doc
-- (a preserved draft from a prior [revert], else the original), and
-- let the JS bridge mount CM6.
function PinnedChunk:edit()
    if self._editing then return end
    if self:readOnly() then return end
    self:loadChunkText()
    self._chunkOriginalText = self._chunkText or ""
    if self._savedEditorText ~= "" then
        self._editorInitialText = self._savedEditorText
        self._savedEditorText = ""
    else
        self._editorInitialText = self._chunkOriginalText
    end
    self._isChunkEdited = (self._editorInitialText ~= self._chunkOriginalText)
    self._editing = true
    self._acceptError = ""
    self:bumpBridgeNonce()
end

-- [revert] click handler. Preserve the draft for perfect restore on
-- the next [edit]; the JS bridge destroys the editor.
function PinnedChunk:revert()
    if not self._editing then return end
    -- _editorContent is JS-synced — Lua already has the latest text.
    self._savedEditorText = self._editorContent
    self._editorContent = ""
    self._chunkOriginalText = ""
    self._editorInitialText = ""
    self._isChunkEdited = false
    self._editing = false
    self._acceptError = ""
    self:bumpBridgeNonce()
end

-- JS bridge: docChanged callback. Receives the dirty flag from JS
-- (editor.getDoc() != _chunkOriginalText).
function PinnedChunk:onEditorDocChanged(dirty, content)
    self._isChunkEdited = dirty and true or false
    if content then self._editorContent = content end
end

-- JS bridge: tag-scrape callback. Receives a JSON array of
-- {name, value, externalfile, externaltarget, kind} from every
-- <ark-tag> in the iframe's chunk render. Splits the list into ext
-- (those whose iframe parent was <ark-ext-tags>) and inline (the
-- rest — body-level inline tags including @id and any mid-chunk
-- @name: value lines that parseTagBlock misses).
function PinnedChunk:onExtTagsScraped(jsonStr)
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
    -- Invalidate the current-tags cache: the scrape is authoritative
    -- and may change rows without changing the signature's inputs.
    self._currentTagsViewSig = ""
end

-- Current-tags view: the chunk's tags as they stand on disk. With
-- one-shot firing there is no pending overlay — a fired operation is
-- committed, and refreshTags() re-derives this list.
-- Sources for inline tags: prefer _inlineScrapedTags (catches @id
-- and any mid-chunk @tag: value lines the iframe rendered as
-- <ark-tag>), falling back to mcp.extractTagValues on the chunk
-- text if the scrape hasn't run yet. Ext tags always come from
-- _extTags (the iframe scrape), which persists across edit-mode
-- transitions.
function PinnedChunk:currentTagsView()
    -- Signature of the upstream sources that drive the row list.
    -- If unchanged from the last call, return the cached row list
    -- (preserving Lua identity) so ViewList reuses DOM presenters.
    local srcText = self:chunkText() or ""
    local extSig = "ext:" .. #(self._extTags or {})
    local sig = string.format("e:%d:%s:%s",
        #srcText, srcText:sub(1, 64), extSig)
    if sig == self._currentTagsViewSig
        and type(self._currentTagsViewRows) == "table"
        and #self._currentTagsViewRows > 0 then
        return self._currentTagsViewRows
    end
    self._currentTagsViewSig = sig

    -- Per-row reuse: match against the cache by (kind, name,
    -- occurrence). Reused rows keep their Lua identity so ViewList
    -- doesn't replace DOM presenters.
    local cache = self._currentTagRowCache
    if type(cache) ~= "table" then cache = {} end
    local newCache = {}
    local function reuseOrCreate(kind, name, occurrence, freshValue, opts)
        local key = kind .. ":" .. name .. ":" .. tostring(occurrence)
        local row = cache[key]
        if row then
            row.value = freshValue
            row.externalfile = opts and opts.externalfile or ""
            row.externaltarget = opts and opts.externaltarget or ""
        else
            row = session:create(CurrentTagRow)
            row.name = name
            row.value = freshValue
            row.kind = kind
            row.occurrence = occurrence
            row._chunk = self
            row.externalfile = opts and opts.externalfile or ""
            row.externaltarget = opts and opts.externaltarget or ""
        end
        newCache[key] = row
        return row
    end

    local out = {}
    -- Source 1: inline tags. Occurrence ordinals keep the reuse key
    -- unique when multiple tags share a name.
    local inlineList
    if self._inlineScrapedTags and #self._inlineScrapedTags > 0 then
        inlineList = self._inlineScrapedTags
    else
        inlineList = mcp.extractTagValues(srcText, "markdown")
    end
    local seen = {}
    for _, t in ipairs(inlineList or {}) do
        local nm = t.name or ""
        seen[nm] = (seen[nm] or 0) + 1
        table.insert(out, reuseOrCreate("inline", nm, seen[nm], t.value or "", nil))
    end
    -- Source 2: ext tags from the iframe scrape.
    local extSeen = {}
    for _, et in ipairs(self._extTags or {}) do
        local nm = et.name or ""
        extSeen[nm] = (extSeen[nm] or 0) + 1
        table.insert(out, reuseOrCreate("ext", nm, extSeen[nm], et.value or "",
            { externalfile = et.externalfile or "",
              externaltarget = et.externaltarget or "" }))
    end
    self._currentTagRowCache = newCache
    self._currentTagsViewRows = out
    return out
end

-- Current-tag row affordances. Rows are read-only; each affordance
-- loads a pre-filled proposal widget into the stack for the user to
-- fire — destructive operations stay behind an explicit fire click,
-- never firing from the row itself.

-- Build a widget pre-filled from this row: tag name, disposition
-- matching the row's kind, external targeting from the row's TARGET
-- spec for ext rows. Inserted at the top of the stack so it's
-- immediately visible.
function CurrentTagRow:_loadWidget()
    local w = ProposalWidget:new(self._chunk)
    w.tagName = self.name or ""
    w.tagValue = self.value or ""
    if self.kind == "ext" then
        -- The row's externaltarget is the full TARGET spec; carry it
        -- as a bare base so targetSpec() emits it verbatim.
        w.disposition = "external"
        w.extBaseValue = self.externaltarget or ""
        w.extLocatorKind = "bare"
        w._extLoaded = true
    else
        w.disposition = "internal"
    end
    table.insert(self._chunk._widgets or {}, 1, w)
    return w
end

-- `rem` checkbox: load a pre-filled remove widget.
function CurrentTagRow:loadRemoveWidget()
    if not self._chunk then return end
    local w = self:_loadWidget()
    w.removeMode = true
end

-- Row click: load a pre-filled replace widget ready to revise.
function CurrentTagRow:loadReplaceWidget()
    if not self._chunk then return end
    local w = self:_loadWidget()
    w.replaceMode = true
end

-- Display helpers for current-tag rows.
function CurrentTagRow:isInline()
    return self.kind == "inline"
end

function CurrentTagRow:isExt()
    return self.kind == "ext"
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

-- Sum of filled proposal widgets across all cards. Guards dismiss
-- confirmation and Clear unchanged; Accept ignores it (tag
-- operations fire per-widget).
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
    if n == 0 then return "Accept (no changes)" end
    return string.format("Accept (%d changed)", n)
end

-- Click handler for the panel-level Accept button. Content edits
-- only — no warning dialog; tag operations are already committed by
-- their own fire clicks.
function Curation:acceptChanges()
    self:_doAccept()
end

-- Execute Accept: a chunk-edit op for each editing-dirty card.
function Curation:_doAccept()
    for _, p in ipairs(self:_livePresenters()) do
        if p._isChunkEdited then
            local ok = true
            local info = p:chunkInfo()
            if p._chunkInfoError ~= "" then
                p._acceptError = "chunkInfo: " .. p._chunkInfoError
                ok = false
            else
                local path = (info and info.path) or p:path()
                local byteStart = tonumber((info and info.byteStart) or 0) or 0
                local byteEnd = tonumber((info and info.byteEnd) or 0) or byteStart
                local text = p._editorContent ~= "" and p._editorContent
                    or p._editorInitialText
                local _, e = mcp.replaceRegion(path, byteStart, byteEnd, text)
                if e then
                    p._acceptError = "replaceRegion: " .. tostring(e)
                    ok = false
                end
            end
            -- On success, return the card to viewing state. Proposal
            -- widgets are untouched — they're an independent path.
            if ok then
                p._isChunkEdited = false
                p._editing = false
                p._chunkOriginalText = ""
                p._editorInitialText = ""
                p._editorContent = ""
                p._savedEditorText = ""
                p._acceptError = ""
                -- Force chunk text reload on next access (file changed).
                p._chunkTextLoaded = false
                p._chunkText = ""
            end
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

-- Subscriptions setup. Idempotent — safe to call multiple times.
-- Registers the onpublish callback once and adds the sweep-doc
-- subscriptions.
function Curation:_ensureSubscriptions()
    local sid = "curation"
    if not self._onpublishRegistered then
        mcp.onpublish(sid, function(events)
            self:onSweepEvent(events)
        end)
        self._onpublishRegistered = true
    end
    mcp.subscribe(sid, { tag = "sweep-status",
        filterFiles = { SWEEP_DOC_PATH } })
    mcp.subscribe(sid, { tag = "sweep-progress",
        filterFiles = { SWEEP_DOC_PATH } })
    self._sweepSubscribed = true
end

----------------------------------------------------------------------
-- ProposalWidget methods
----------------------------------------------------------------------

function ProposalWidget:new(chunk)
    local w = session:create(ProposalWidget, { _chunk = chunk })
    -- The degrade, surfaced as a default: chunks that can't host an
    -- inline tag start (and stay) external.
    if chunk and chunk:readOnly() then
        w.disposition = "external"
    end
    return w
end

function ProposalWidget:trimmedTag()
    return trim(self.tagName)
end

function ProposalWidget:isFilled()
    -- A widget is "filled" once it has a tag name. Remove ops are
    -- valid even with empty value; add ops without value still apply
    -- (bare `@tag` is a meaningful annotation).
    return trim(self.tagName) ~= ""
end

-- The loaded gun's trigger.
function ProposalWidget:fire()
    if not self._chunk then return end
    self._chunk:fireWidget(self)
end

function ProposalWidget:fireDisabled()
    return not self:isFilled()
end

function ProposalWidget:fireError()      return self._fireError end
function ProposalWidget:noFireError()    return self._fireError == "" end
function ProposalWidget:clearFireError() self._fireError = "" end

function ProposalWidget:toggleRemove()
    self.removeMode = not self.removeMode
end

-- Effective disposition: the field, overridden to external when the
-- chunk can't host an inline tag (degrade as a lock, not a surprise).
function ProposalWidget:effectiveDisposition()
    if self:dispositionLocked() then return "external" end
    return self.disposition == "external" and "external" or "internal"
end

function ProposalWidget:isInternal()
    return self:effectiveDisposition() == "internal"
end

function ProposalWidget:isExternal()
    return self:effectiveDisposition() == "external"
end

function ProposalWidget:dispositionLabel()
    return self:isInternal() and "int" or "ext"
end

-- Flip internal ↔ external. No-op when locked. On first switch to
-- external, pull targeting defaults from mcp.suggestExtLocator.
function ProposalWidget:toggleDisposition()
    if self:dispositionLocked() then return end
    if self:effectiveDisposition() == "external" then
        self.disposition = "internal"
        return
    end
    self.disposition = "external"
    if not self._extLoaded then
        self:_loadExtSuggestion()
    end
end

function ProposalWidget:toggleReplace()
    self.replaceMode = not self.replaceMode
end

function ProposalWidget:replaceLabel()
    return self.replaceMode and "replace" or "add"
end

-- Tag-name change handler: when the entered tag already exists on
-- the chunk, pre-set replaceMode — the visible default replacing the
-- old infer-by-existence rule. Only auto-sets, never auto-clears
-- (the user may have flipped it deliberately).
function ProposalWidget:onTagNameChange()
    if self.replaceMode then return end
    local tag = trim(self.tagName)
    if tag == "" or not self._chunk then return end
    for _, row in ipairs(self._chunk:currentTagsView()) do
        if row.name == tag then
            self.replaceMode = true
            return
        end
    end
end

function ProposalWidget:_loadExtSuggestion()
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

-- Locked to external when the parent chunk can't host an inline tag.
function ProposalWidget:dispositionLocked()
    return (self._chunk and self._chunk:readOnly()) and true or false
end

function ProposalWidget:noDupFlag()
    return not self:hasDupFlag()
end

function ProposalWidget:noScopeReadout()
    return not self:hasScopeReadout()
end

function ProposalWidget:locatorBare()
    return self.extLocatorKind == "bare"
end

function ProposalWidget:kill()
    if self._chunk then self._chunk:removeWidget(self) end
end

-- Bound to ui-event-sl-change on the base dropdown. The ui-value
-- binding has already updated self.extBase; this handler refreshes
-- the locator defaults to match the new base.
function ProposalWidget:onBaseChange()
    if self.extBase ~= "uuid" and self.extBase ~= "path" then return end
    self._extLoaded = false
    self:_loadExtSuggestion()
end

-- Bound to ui-event-sl-change on the locator-kind dropdown. The
-- ui-value binding has already updated self.extLocatorKind; this
-- handler clears the locator text when kind is bare.
function ProposalWidget:onLocatorKindChange()
    if self.extLocatorKind == "bare" then
        self.extLocatorText = ""
    end
end

function ProposalWidget:dupFlag()
    if (self._withinFileDupCount or 0) <= 1 then return "" end
    local shortVal = self.extBaseValue or ""
    if #shortVal > 12 then shortVal = shortVal:sub(1, 8) .. "..." end
    return string.format("UUID: %s (×%d in this file)",
        shortVal, self._withinFileDupCount)
end

function ProposalWidget:hasDupFlag()
    return (self._withinFileDupCount or 0) > 1
end

function ProposalWidget:scopeReadout()
    local c = self._extScopeChunks or 0
    local f = self._extScopeFiles or 0
    if c == 0 and f == 0 then return "" end
    return string.format("will route to %d chunk%s across %d file%s",
        c, c == 1 and "" or "s",
        f, f == 1 and "" or "s")
end

function ProposalWidget:hasScopeReadout()
    return (self._extScopeChunks or 0) > 0 or (self._extScopeFiles or 0) > 0
end

-- Tab-out auto-add: called from a viewdef keypress handler when the
-- user tabs past the last input of this widget. If this widget is
-- filled AND is the last in its chunk's stack, append a new empty
-- widget below it. Empty widgets don't auto-add (lets focus escape
-- the stack naturally).
function ProposalWidget:autoAddOnTab()
    if not self:isFilled() then return end
    if not self._chunk then return end
    local widgets = self._chunk._widgets
    if widgets[#widgets] ~= self then return end
    self._chunk:addWidget()
end

-- Compose the @ext target spec from the external-targeting fields.
-- Used by fire() to build the external target.
function ProposalWidget:targetSpec()
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

