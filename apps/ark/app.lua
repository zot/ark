-- Ark Index Manager
-- Shells out to `ark` CLI for all operations

local json = require("mcp.json")

local HOME = os.getenv("HOME") or ""
local DB_PATH = HOME .. "/.ark"

-- Display helper: compress long paths for UI display
-- Full path is always preserved for functional use; this is display-only
local COMPRESS_THRESHOLD = 30
local CLAUDE_PROJECTS_PREFIX = HOME .. "/.claude/projects/"

local function compressPath(fullPath)
    if not fullPath or fullPath == "" then return "" end

    -- Step 1: substitute HOME with ~
    local display = fullPath
    if fullPath:sub(1, #HOME) == HOME then
        display = "~" .. fullPath:sub(#HOME + 1)
    end

    -- Short enough already? Return the ~ form
    if #display <= COMPRESS_THRESHOLD then return display end

    -- Rule 1: Claude project paths ~/.claude/projects/SLUG/... or glob ~/.claude/projects/*
    if fullPath:sub(1, #CLAUDE_PROJECTS_PREFIX) == CLAUDE_PROJECTS_PREFIX then
        local rest = fullPath:sub(#CLAUDE_PROJECTS_PREFIX + 1)
        -- Glob source: rest is "*" or "*/memory" etc
        if rest == "*" then
            return "Claude Projects (glob)"
        elseif rest:sub(1, 1) == "*" then
            return "Claude Projects" .. rest:sub(2)
        end
        -- Concrete: rest is like "-home-deck-work-ark" or "-home-deck-work-ark/memory"
        local slug, tail = rest:match("^([^/]+)(.*)")
        if slug then
            -- Extract last segment of the slug (split on -)
            local projName = slug:match(".-([^%-]+)$") or slug
            if tail and tail ~= "" then
                return "Project: " .. projName .. tail
            else
                return "Project: " .. projName
            end
        end
    end

    -- Rule 2: Home paths ~/very/long/path/dirname
    if fullPath:sub(1, #HOME) == HOME then
        local lastName = fullPath:match("([^/]+)$") or fullPath
        return "~/…" .. lastName
    end

    -- Rule 3: Other user paths /home/otheruser/...
    local otherUser, otherRest = fullPath:match("^/home/([^/]+)(.*)")
    if otherUser then
        local lastName = fullPath:match("([^/]+)$") or fullPath
        if otherRest and #otherRest > 1 then
            return "~" .. otherUser .. "/…" .. lastName
        else
            return "~" .. otherUser
        end
    end

    -- Rule 4: Other absolute paths /usr/share/long/path/dirname
    local firstComp = fullPath:match("^/([^/]+)")
    local lastName = fullPath:match("([^/]+)$") or fullPath
    if firstComp and firstComp ~= lastName then
        return "/" .. firstComp .. "/…" .. lastName
    end

    return display
end
-- Forward declarations
-- searching: child types (Source, Project, etc.) reference the
-- searching view via this local. Assigned during instance creation.
local searching
-- Forward-declared so Ark:new/mutate can see them before their definitions
local Searching, Messaging, MessageColumn, Message

local ARK_BIN = DB_PATH .. "/ark"
local ARK_PATH = "PATH=" .. DB_PATH .. ":" .. (os.getenv("PATH") or "")

-- Helper: run a command and return stdout
local function run(cmd)
    local handle = io.popen(ARK_PATH .. " " .. cmd .. " 2>/dev/null")
    if not handle then return nil end
    local output = handle:read("*a")
    handle:close()
    return output
end

-- Helper: run ark command with db path
-- subcmd starts with the command name, -db is inserted after it
local function arkCmd(subcmd)
    local cmd, rest = subcmd:match("^(%S+)%s*(.*)")
    if not cmd then return nil end
    local dbFlag = '--dir "' .. DB_PATH .. '"'
    if rest and rest ~= "" then
        return run('"' .. ARK_BIN .. '" ' .. cmd .. ' ' .. dbFlag .. ' ' .. rest)
    else
        return run('"' .. ARK_BIN .. '" ' .. cmd .. ' ' .. dbFlag)
    end
end

-- Helper: parse JSON from ark output, return table or nil
local function arkJSON(subcmd)
    local output = arkCmd(subcmd)
    if not output or output == "" then return nil end
    local ok, result = pcall(json.decode, output)
    if ok then return result end
    return nil
end

-- Helper: list directory entries with type info
-- Uses ls -1AF to classify entries in a single subprocess (F appends / to dirs)
local function listDir(dirPath)
    local entries = {}
    local handle = io.popen('ls -1AF "' .. dirPath .. '" 2>/dev/null')
    if not handle then return entries end
    for line in handle:lines() do
        if line ~= "" then
            local isDir = line:sub(-1) == "/"
            local name = isDir and line:sub(1, -2) or line
            -- ls -F also appends * @ | for executables, symlinks, pipes — strip those
            if not isDir and name:match("[%*@|]$") then
                name = name:sub(1, -2)
            end
            table.insert(entries, {name = name, isDir = isDir})
        end
    end
    handle:close()
    table.sort(entries, function(a, b)
        if a.isDir ~= b.isDir then return a.isDir end
        return a.name < b.name
    end)
    return entries
end

-- Helper: query show-why for a path and apply results to a node
local function applyWhy(node)
    local why = arkJSON('config show-why "' .. node.fullPath .. '"')
    if why then
        node.state = why.status or "unresolved"
        node.whyPatterns = why.patterns and table.concat(why.patterns, ", ") or ""
        node.whySources = why.sources and table.concat(why.sources, ", ") or ""
        node.whyConflict = why.conflict or false
    end
    node._whyLoaded = true
end

-- Helper: build the pattern string for a node (appends / for dirs, /** for dir includes)
local function nodePattern(node, mode)
    if mode == "include" and node.isDir then
        return node.relPath .. "/**"
    elseif node.isDir then
        return node.relPath .. "/"
    else
        return node.relPath
    end
end

------------------------------------------------------------------------
-- Ark (root shell — routes between Searching and Messaging views)
------------------------------------------------------------------------

Ark = session:prototype("Ark", {
    _viewMode = "searching",
    _searching = EMPTY,
    _messaging = EMPTY,
})

function Ark:new(instance)
    instance = session:create(Ark, instance)
    instance._searching = Searching:new()
    instance._messaging = Messaging:new()
    return instance
end

function Ark:currentView()
    if self._viewMode == "messaging" then
        if not self._messaging then
            self._messaging = Messaging:new()
        end
        return self._messaging
    end
    return self._searching
end

function Ark:showSearching()
    self._viewMode = "searching"
end

function Ark:showMessaging()
    self._viewMode = "messaging"
    if not self._messaging then
        self._messaging = Messaging:new()
    end
    self._messaging:refresh()
end

function Ark:mutate()
    if self._searching == nil then
        self._searching = Searching:new()
    end
    if self._messaging == nil then
        self._messaging = Messaging:new()
    end
end

------------------------------------------------------------------------
-- Ark.Searching (index manager + full-text search)
------------------------------------------------------------------------

Ark.Searching = session:prototype("Ark.Searching", {
    _sources = EMPTY,
    selectedSource = EMPTY,
    showAddForm = false,
    newDir = "",
    newStrategy = "markdown",
    _dbPath = DB_PATH,
    _serverRunning = false,
    _statusCounts = EMPTY,
    -- Search
    searchQuery = "",
    searchMode = "contains",
    _searchResults = EMPTY,
    _searchGroups = EMPTY,
    _searching = false,
    _searchView = false,
    _lastSearchedQuery = "",
    _hitsPerFile = "1",
    -- Filter panel
    _showFilterPanel = false,
    filterFiles = "",
    excludeFiles = "",
    filterContent = "",
    excludeContent = "",
    -- Display grouping
    _displayItems = EMPTY,
    _projects = EMPTY,
    _dataSources = EMPTY,
    -- Project search panel
    _projectSearchOpen = false,
    _projectSearchQuery = "",
    _projectCandidates = EMPTY,
    -- Pattern editing
    _showPatterns = false,
})
Searching = Ark.Searching

Ark.Source = session:prototype("Ark.Source", {
    dir = "",
    strategy = "",
    includedCount = 0,
    excludedCount = 0,
    unresolvedCount = 0,
    _visibleNodes = EMPTY,
    _nodeMap = EMPTY,
    _missingPaths = EMPTY,
    _loaded = false,
    _loading = false,
    _searchIncluded = true,
    -- Pattern editing
    _includePatterns = EMPTY,
    _excludePatterns = EMPTY,
    includeText = "",
    excludeText = "",
    _patternError = "",
})
local Source = Ark.Source

Ark.SearchFileGroup = session:prototype("Ark.SearchFileGroup", {
    path = "",
    _chunks = EMPTY,
    _expanded = false,
    topScore = 0,
    _rank = 0,
})
local SearchFileGroup = Ark.SearchFileGroup

function SearchFileGroup:displayPath()
    return compressPath(self.path)
end

function SearchFileGroup:scoreText()
    if self.topScore == 0 then return "" end
    return string.format("%.2f", self.topScore)
end

function SearchFileGroup:chunkCountText()
    local n = self._chunks and #self._chunks or 0
    if n <= 1 then return "" end
    if self._expanded then return "" end
    return "+" .. tostring(n - 1) .. " more"
end

function SearchFileGroup:visibleChunks()
    if not self._chunks or #self._chunks == 0 then return {} end
    if self._expanded then return self._chunks end
    return { self._chunks[1] }
end

function SearchFileGroup:toggleExpand()
    self._expanded = not self._expanded
end

function SearchFileGroup:expandIcon()
    if not self._chunks or #self._chunks <= 1 then return "dot" end
    if self._expanded then return "chevron-down" end
    return "chevron-right"
end

function SearchFileGroup:isExpandable()
    return self._chunks and #self._chunks > 1
end

function SearchFileGroup:notExpandable()
    return not self:isExpandable()
end

Ark.SearchResult = session:prototype("Ark.SearchResult", {
    path = "",
    score = 0,
    snippet = "",
    text = "",
    range = "",
    _rank = 0,
})
local SearchResult = Ark.SearchResult

function SearchResult:displayPath()
    return compressPath(self.path)
end

function SearchResult:scoreText()
    if self.score == 0 then return "" end
    return string.format("%.2f", self.score)
end

function SearchResult:lineRange()
    if not self.range or self.range == "" then return "" end
    local s, e = self.range:match("^(%d+)-(%d+)$")
    if not s then return "L" .. self.range end
    if s == e then return "L" .. s end
    return "L" .. s .. "-" .. e
end

function SearchResult:previewText()
    if not self.text or self.text == "" then return "" end
    local t = self.text
    if #t > 400 then
        t = t:sub(1, 400) .. "…"
    end
    return t
end

function SearchResult:hasPreview()
    return self.text ~= nil and self.text ~= ""
end

function SearchResult:hidePreview()
    return not self:hasPreview()
end

Ark.Project = session:prototype("Ark.Project", {
    name = "",
    slug = "",
    _fileSource = EMPTY,
    _claudeSource = EMPTY,
    _resolvedPath = "",
    filesOn = true,
    memoryOn = true,
    chatsOn = false,
    _hasFiles = false,
    _hasMemory = false,
    _hasChats = false,
})
local Project = Ark.Project

Ark.DataSource = session:prototype("Ark.DataSource", {
    _source = EMPTY,
    dataOn = true,
    name = "",
    _dir = "",
})
local DataSource = Ark.DataSource

Ark.ProjectCandidate = session:prototype("Ark.ProjectCandidate", {
    slug = "",
    name = "",
    _resolvedPath = "",
    selected = false,
    _wasConfigured = false,
})
local ProjectCandidate = Ark.ProjectCandidate

Ark.Node = session:prototype("Ark.Node", {
    name = "",
    relPath = "",
    fullPath = "",
    isDir = false,
    state = "unresolved",
    depth = 0,
    expanded = false,
    _childrenLoaded = false,
    isMissing = false,
    whyPatterns = "",
    whySources = "",
    whyConflict = false,
    _whyLoaded = false,
    hasIgnoreFile = false,
    honorIgnore = true,
    exceptionPattern = "",
})
local Node = Ark.Node

------------------------------------------------------------------------
-- Ark methods
------------------------------------------------------------------------

function Searching:mutate()
    if self._searchResults == nil then
        self._searchResults = {}
    end
    if self._searchGroups == nil then
        self._searchGroups = {}
    end
    if self.searchQuery == nil then
        self.searchQuery = ""
    end
    if self.searchMode == nil then
        self.searchMode = "contains"
    end
    if self._hitsPerFile == nil then
        self._hitsPerFile = "1"
    end
    if self.filterFiles == nil then self.filterFiles = "" end
    if self.excludeFiles == nil then self.excludeFiles = "" end
    if self.filterContent == nil then self.filterContent = "" end
    if self.excludeContent == nil then self.excludeContent = "" end
    if self._showFilterPanel == nil then self._showFilterPanel = false end
    if self._displayItems == nil then
        self._displayItems = {}
        self._projects = {}
        self._dataSources = {}
        self:buildDisplayItems()
    end
    if self._projectCandidates == nil then
        self._projectCandidates = {}
    end
end

function Searching:new(instance)
    instance = session:create(Searching, instance)
    instance._sources = instance._sources or {}
    instance._statusCounts = instance._statusCounts or {}
    instance:loadConfig()
    return instance
end

function Searching:loadConfig()
    local cfg = arkJSON("config")
    if not cfg then return end

    -- Rebuild sources from config
    local oldSources = {}
    for _, s in ipairs(self._sources or {}) do
        oldSources[s.dir] = s
    end

    self._sources = {}
    local sources = cfg.Sources or cfg.sources or {}
    for _, src in ipairs(sources) do
        local dir = src.Dir or src.dir or ""
        local strategy = src.Strategy or src.strategy or ""
        local inc = src.Include or src.include or {}
        local exc = src.Exclude or src.exclude or {}
        local fromGlob = src.FromGlob or src.from_glob or ""

        -- Reuse existing Source to preserve loaded state
        local existing = oldSources[dir]
        if existing then
            existing.strategy = strategy
            existing._includePatterns = inc
            existing._excludePatterns = exc
            existing._fromGlob = fromGlob
            existing.includeText = table.concat(inc, "\n")
            existing.excludeText = table.concat(exc, "\n")
            table.insert(self._sources, existing)
        else
            local s = session:create(Source)
            s.dir = dir
            s.strategy = strategy
            s._visibleNodes = {}
            s._nodeMap = {}
            s._missingPaths = {}
            s._includePatterns = inc
            s._excludePatterns = exc
            s._fromGlob = fromGlob
            s.includeText = table.concat(inc, "\n")
            s.excludeText = table.concat(exc, "\n")
            table.insert(self._sources, s)
        end
    end

    self:checkServer()
    self:buildDisplayItems()
end

function Searching:refresh()
    arkCmd("scan")
    arkCmd("refresh")
    self:loadConfig()
    if self.selectedSource and self.selectedSource._loaded then
        self.selectedSource._loaded = false
        self.selectedSource:loadRootNodes()
    end
end

function Searching:selectSource(source)
    self.selectedSource = source
    self.showAddForm = false
    self._searchView = false
    if not source._loaded and not source._loading then
        source:loadRootNodes()
    end
end

function Searching:openAddForm()
    self.showAddForm = true
end

function Searching:cancelAddForm()
    self.showAddForm = false
    self.newDir = ""
    self.newStrategy = "markdown"
end

function Searching:addSource()
    if self.newDir == "" then return end
    arkCmd('config add-source --strategy ' .. self.newStrategy .. ' "' .. self.newDir .. '"')
    self:cancelAddForm()
    self:loadConfig()
    -- Select the newly added source
    for _, s in ipairs(self._sources) do
        -- Match by expanded home path or original
        if s.dir == self.newDir or s.dir:match(self.newDir:gsub("~/", "")) then
            self:selectSource(s)
            break
        end
    end
end

function Searching:removeSource()
    if not self.selectedSource then return end
    arkCmd('config remove-source "' .. self.selectedSource.dir .. '"')
    self.selectedSource = nil
    self:loadConfig()
end

function Searching:saveSelectedPatterns()
    if not self.selectedSource then return end
    self.selectedSource:savePatterns()
    if (self.selectedSource._patternError or "") == "" then
        mcp:notify("Patterns saved", "success")
    else
        mcp:notify("Pattern errors: " .. self.selectedSource._patternError, "danger")
    end
end

function Searching:togglePatterns()
    self._showPatterns = not self._showPatterns
end

function Searching:hidePatterns()
    return not self._showPatterns or not self.selectedSource
end

function Searching:collapseResolved()
    if not self.selectedSource then return end
    local nodes = self.selectedSource._visibleNodes
    -- Iterate backwards to safely remove descendants
    local i = 1
    while i <= #nodes do
        local node = nodes[i]
        if node.isDir and node.expanded and (node.state == "included" or node.state == "excluded") then
            node:collapse()
        else
            i = i + 1
        end
    end
end

function Searching:checkServer()
    local output = arkCmd("status")
    if not output or output == "" then
        self._serverRunning = false
        return
    end
    -- Parse "key: value" lines
    local counts = {}
    for key, val in output:gmatch("(%w+):%s*(.-)%s*\n") do
        counts[key] = val
    end
    self._serverRunning = (counts.server == "running")
    self._statusCounts = {
        included = tonumber(counts.files) or 0,
        stale = tonumber(counts.stale) or 0,
        missing = tonumber(counts.missing) or 0,
        unresolved = tonumber(counts.unresolved) or 0,
    }
end

function Searching:visibleNodes()
    if self.selectedSource then
        return self.selectedSource._visibleNodes or {}
    end
    return {}
end

function Searching:showSourceDetail()
    return self.selectedSource ~= nil and not self.showAddForm and not self._searchView
end

function Searching:hideSourceDetail()
    return not self:showSourceDetail()
end

function Searching:showPlaceholder()
    return self.selectedSource == nil and not self.showAddForm and not self._searchView
end

function Searching:hidePlaceholder()
    return not self:showPlaceholder()
end

function Searching:hideAddForm()
    return not self.showAddForm
end

function Searching:sourceHeaderText()
    if self.selectedSource then return compressPath(self.selectedSource.dir) end
    return ""
end

function Searching:sourceHeaderFullPath()
    if self.selectedSource then return self.selectedSource.dir end
    return ""
end

function Searching:statusText()
    local c = self._statusCounts or {}
    return string.format("✓ %d  |  ✗ %d  |  ? %d  |  👻 %d",
        c.included or 0, c.stale or 0, c.unresolved or 0, c.missing or 0)
end

function Searching:serverStatusText()
    if self._serverRunning then return "●" end
    return "○"
end


------------------------------------------------------------------------
-- Slug resolution: find real directory name from Claude project slug
------------------------------------------------------------------------

-- Resolve a Claude project slug to a human-readable project name.
-- The slug encodes a path with / replaced by -, which is ambiguous when
-- directory names contain hyphens. We resolve by walking the filesystem.
-- Returns name, resolvedPath
local function resolveSlugName(slug)
    local homeSlug = "-" .. HOME:sub(2):gsub("/", "-") .. "-"
    local rest = slug
    if slug:sub(1, #homeSlug) == homeSlug then
        rest = slug:sub(#homeSlug + 1)
    elseif rest:sub(1, 1) == "-" then
        rest = rest:sub(2)
    end

    -- Split on hyphens
    local parts = {}
    for part in rest:gmatch("[^%-]+") do
        table.insert(parts, part)
    end
    if #parts == 0 then return rest end

    -- Handle double-dash (nested Claude project scope): take only before --
    local mainRest, scopeSuffix = rest:match("^([^%-].-)[%-][%-](.*)")
    mainRest = mainRest or rest

    -- Split on single hyphens
    local parts = {}
    for part in mainRest:gmatch("[^%-]+") do
        table.insert(parts, part)
    end
    if #parts == 0 then return rest end

    -- Walk from HOME, trying LONGEST match first at each step
    local current = HOME
    local i = 1
    while i <= #parts do
        local found = false
        -- Try longest combination first (most specific match)
        for j = #parts, i, -1 do
            local combined = table.concat(parts, "-", i, j)
            local candidate = current .. "/" .. combined
            local fh = io.popen('test -d "' .. candidate .. '" && echo y 2>/dev/null')
            local exists = fh and fh:read("*a"):match("y") or false
            if fh then fh:close() end
            if exists then
                current = candidate
                i = j + 1
                found = true
                break
            end
        end
        if not found then
            -- Remaining unconsumed parts are the project name
            local remaining = table.concat(parts, "-", i)
            if scopeSuffix then remaining = remaining .. " (" .. scopeSuffix .. ")" end
            return remaining, current .. "/" .. remaining
        end
    end

    -- All parts consumed — last directory is the name
    local name = current:match("([^/]+)$")
    if scopeSuffix then name = (name or rest) .. " (" .. scopeSuffix .. ")" end
    if current == HOME then
        if mainRest:sub(1, 5) == "work-" then mainRest = mainRest:sub(6) end
        local n = mainRest
        if scopeSuffix then n = n .. " (" .. scopeSuffix .. ")" end
        return n, HOME .. "/" .. mainRest
    end
    return name or rest, current
end

------------------------------------------------------------------------
-- Project search panel
------------------------------------------------------------------------

function Searching:openProjectSearch()
    self._projectSearchOpen = true
    self._projectSearchQuery = ""
    self:scanProjectCandidates()
end

function Searching:closeProjectSearch()
    self._projectSearchOpen = false
    self._projectCandidates = {}
    self._projectSearchQuery = ""
end

function Searching:hideProjectSearch()
    return not self._projectSearchOpen
end

function Searching:scanProjectCandidates()
    local claudeDir = HOME .. "/.claude/projects"
    local entries = listDir(claudeDir)

    -- Collect existing project slugs
    local existingSlugs = {}
    for _, p in ipairs(self._projects or {}) do
        existingSlugs[p.slug] = true
    end

    local candidates = {}
    for _, entry in ipairs(entries) do
        if entry.isDir and entry.name:sub(1, 1) == "-" then
            local slug = entry.name
            local c = session:create(ProjectCandidate)
            c.slug = slug
            local name, resolvedPath = resolveSlugName(slug)
            c.name = name
            c._resolvedPath = resolvedPath or ""
            local configured = existingSlugs[slug] or false
            c.selected = configured
            c._wasConfigured = configured
            table.insert(candidates, c)
        end
    end

    table.sort(candidates, function(a, b) return a.name:lower() < b.name:lower() end)
    self._projectCandidates = candidates
end

function Searching:refreshProjectSearch()
    self:scanProjectCandidates()
end


function Searching:saveProjects()
    local claudeDir = HOME .. "/.claude/projects"
    local added, removed = 0, 0

    for _, c in ipairs(self._projectCandidates or {}) do
        if c.selected and not c._wasConfigured then
            -- Newly selected: add sources
            arkCmd('config add-strategy "*.jsonl" chat-jsonl')
            arkCmd('config add-strategy "*.md" markdown')
            local dir = claudeDir .. "/" .. c.slug
            arkCmd('config add-source "' .. dir .. '"')
            arkCmd('config add-include "*.jsonl" --source "' .. dir .. '"')
            arkCmd('config add-include "memory/*.md" --source "' .. dir .. '"')
            if c._resolvedPath ~= "" then
                arkCmd('config add-source "' .. c._resolvedPath .. '"')
            end
            added = added + 1
        elseif not c.selected and c._wasConfigured then
            -- Deselected: remove sources
            local dir = claudeDir .. "/" .. c.slug
            arkCmd('config remove-source "' .. dir .. '"')
            arkCmd('remove "' .. dir .. '/**"')
            -- Remove file source if it was matched
            for _, p in ipairs(self._projects or {}) do
                if p.slug == c.slug and p._fileSource then
                    arkCmd('config remove-source "' .. p._fileSource.dir .. '"')
                    break
                end
            end
            removed = removed + 1
        end
    end

    self:closeProjectSearch()
    self:loadConfig()

    if added > 0 or removed > 0 then
        local parts = {}
        if added > 0 then table.insert(parts, added .. " added") end
        if removed > 0 then table.insert(parts, removed .. " removed") end
        mcp:notify(table.concat(parts, ", "), "success")
    end
end

function Searching:hasProjectChanges()
    for _, c in ipairs(self._projectCandidates or {}) do
        if c.selected ~= c._wasConfigured then return true end
    end
    return false
end

function Searching:hideProjectSave()
    return not self:hasProjectChanges()
end

function Searching:projectNewCount()
    local n = 0
    for _, c in ipairs(self._projectCandidates or {}) do
        if c.selected and not c._wasConfigured then n = n + 1 end
    end
    return n
end

function Searching:projectRemovedCount()
    local n = 0
    for _, c in ipairs(self._projectCandidates or {}) do
        if not c.selected and c._wasConfigured then n = n + 1 end
    end
    return n
end

function Searching:projectSaveText()
    local added = self:projectNewCount()
    local removed = self:projectRemovedCount()
    if added == 0 and removed == 0 then return "No changes" end
    local parts = {}
    if added > 0 then table.insert(parts, added .. " new") end
    if removed > 0 then table.insert(parts, removed .. " removed") end
    return "Save · " .. table.concat(parts, ", ")
end

function Searching:projectSelectedCount()
    local n = 0
    for _, c in ipairs(self._projectCandidates or {}) do
        if c.selected then n = n + 1 end
    end
    return n
end

function Searching:projectSelectedHeader()
    return "Projects (" .. self:projectSelectedCount() .. ")"
end

function Searching:projectAvailableCount()
    local q = (self._projectSearchQuery or ""):lower()
    local n = 0
    for _, c in ipairs(self._projectCandidates or {}) do
        if not c.selected then
            if q == "" or c.name:lower():find(q, 1, true) or c.slug:lower():find(q, 1, true) then
                n = n + 1
            end
        end
    end
    return n
end

function Searching:projectAvailableHeader()
    return "Available (" .. self:projectAvailableCount() .. ")"
end

function Searching:projectCandidatesUpper()
    -- Upper section: selected OR was configured (the "result" view)
    local q = (self._projectSearchQuery or ""):lower()
    local results = {}
    for _, c in ipairs(self._projectCandidates or {}) do
        if c.selected or c._wasConfigured then
            if q == "" or c.name:lower():find(q, 1, true) or c.slug:lower():find(q, 1, true) then
                table.insert(results, c)
            end
        end
    end
    table.sort(results, function(a, b) return a.name:lower() < b.name:lower() end)
    return results
end

function Searching:projectCandidatesLower()
    -- Lower section: not selected AND not configured (purely available)
    local q = (self._projectSearchQuery or ""):lower()
    local results = {}
    for _, c in ipairs(self._projectCandidates or {}) do
        if not c.selected and not c._wasConfigured then
            if q == "" or c.name:lower():find(q, 1, true) or c.slug:lower():find(q, 1, true) then
                table.insert(results, c)
            end
        end
    end
    table.sort(results, function(a, b) return a.name:lower() < b.name:lower() end)
    return results
end

function Searching:upperSectionCount()
    local n = 0
    for _, c in ipairs(self._projectCandidates or {}) do
        if c.selected or c._wasConfigured then n = n + 1 end
    end
    return n
end

function Searching:lowerSectionCount()
    local q = (self._projectSearchQuery or ""):lower()
    local n = 0
    for _, c in ipairs(self._projectCandidates or {}) do
        if not c.selected and not c._wasConfigured then
            if q == "" or c.name:lower():find(q, 1, true) or c.slug:lower():find(q, 1, true) then
                n = n + 1
            end
        end
    end
    return n
end

function Searching:hideUpperSection()
    return self:upperSectionCount() == 0
end

function Searching:hideAvailableSection()
    return self:lowerSectionCount() == 0
end

function Searching:resetProjectSelections()
    for _, c in ipairs(self._projectCandidates or {}) do
        c.selected = c._wasConfigured
    end
end

function Searching:hideProjectReset()
    return not self:hasProjectChanges()
end

------------------------------------------------------------------------
-- Display grouping: sources → projects + data sources
------------------------------------------------------------------------

function Searching:buildDisplayItems()
    local claudePrefix = HOME .. "/.claude/projects/"
    local claudeBySlug = {}   -- slug → Source
    local fileSources = {}    -- array of {dir, source}

    for _, src in ipairs(self._sources or {}) do
        if src.dir:sub(1, #claudePrefix) == claudePrefix then
            local rest = src.dir:sub(#claudePrefix + 1)
            if rest ~= "*" and rest ~= "" then
                claudeBySlug[rest] = src
            end
        else
            table.insert(fileSources, src)
        end
    end

    -- Build projects from Claude dirs
    local projects = {}
    local matchedFileDirs = {}

    for slug, claudeSrc in pairs(claudeBySlug) do
        local proj = session:create(Project)
        proj.slug = slug
        proj._claudeSource = claudeSrc
        proj._hasMemory = true
        proj._hasChats = true
        proj.memoryOn = true
        proj.chatsOn = false

        -- Match file source by converting dir → slug
        for _, fileSrc in ipairs(fileSources) do
            local testSlug = "-" .. fileSrc.dir:sub(2):gsub("/", "-")
            if testSlug == slug then
                proj._fileSource = fileSrc
                proj._hasFiles = true
                proj.filesOn = true
                matchedFileDirs[fileSrc.dir] = true
                break
            end
        end

        -- Name: from file source dir, or resolve slug via filesystem
        if proj._fileSource then
            proj.name = proj._fileSource.dir:match("([^/]+)$") or slug
            proj._resolvedPath = proj._fileSource.dir
        else
            local name, resolvedPath = resolveSlugName(slug)
            proj.name = name
            proj._resolvedPath = resolvedPath
        end

        table.insert(projects, proj)
    end

    table.sort(projects, function(a, b) return a.name:lower() < b.name:lower() end)

    -- Emacs-style disambiguation: name<parent> with escalation
    -- _resolvedPath was set during name resolution (the actual filesystem path)
    -- For file sources, use the known dir; otherwise keep the resolved path

    local function disambiguate(items)
        -- Group by name
        local groups = {}
        for _, p in ipairs(items) do
            groups[p.name] = groups[p.name] or {}
            table.insert(groups[p.name], p)
        end

        for name, group in pairs(groups) do
            if #group > 1 then
                -- Try parent directory at increasing depth until unique
                local depth = 1
                local resolved = false
                while not resolved and depth <= 5 do
                    local suffixes = {}
                    local allUnique = true
                    for _, p in ipairs(group) do
                        -- Walk up the path to get parent components
                        local parts = {}
                        for seg in p._resolvedPath:gmatch("[^/]+") do
                            table.insert(parts, seg)
                        end
                        -- Get 'depth' parent components above the project name
                        local suffix = {}
                        local nameIdx = #parts  -- last component is the name
                        for d = 1, depth do
                            local idx = nameIdx - d
                            if idx >= 1 then
                                table.insert(suffix, 1, parts[idx])
                            end
                        end
                        local key = table.concat(suffix, "/")
                        suffixes[p] = key
                        -- Check uniqueness
                        for _, other in ipairs(group) do
                            if other ~= p and suffixes[other] == key then
                                allUnique = false
                            end
                        end
                    end
                    if allUnique then
                        for _, p in ipairs(group) do
                            p.name = name .. "<" .. suffixes[p] .. ">"
                        end
                        resolved = true
                    end
                    depth = depth + 1
                end
                -- If still not unique after 5 levels, append slug hash
                if not resolved then
                    for _, p in ipairs(group) do
                        p.name = name .. "<" .. p.slug:sub(-8) .. ">"
                    end
                end
            end
        end
    end

    disambiguate(projects)

    -- Re-sort after disambiguation
    table.sort(projects, function(a, b) return a.name:lower() < b.name:lower() end)

    -- Data sources: unmatched file sources
    local dataSources = {}
    for _, fileSrc in ipairs(fileSources) do
        if not matchedFileDirs[fileSrc.dir] then
            local ds = session:create(DataSource)
            ds._source = fileSrc
            ds._dir = fileSrc.dir
            ds.name = compressPath(fileSrc.dir)
            ds.dataOn = true
            table.insert(dataSources, ds)
        end
    end

    table.sort(dataSources, function(a, b) return a.name:lower() < b.name:lower() end)

    -- Preserve existing filter states on rebuild
    if self._projects then
        local oldBySlug = {}
        for _, p in ipairs(self._projects) do oldBySlug[p.slug] = p end
        for _, p in ipairs(projects) do
            local old = oldBySlug[p.slug]
            if old then
                p.filesOn = old.filesOn
                p.memoryOn = old.memoryOn
                p.chatsOn = old.chatsOn
            end
        end
    end
    if self._dataSources then
        local oldByDir = {}
        for _, d in ipairs(self._dataSources) do oldByDir[d._dir] = d end
        for _, ds in ipairs(dataSources) do
            local old = oldByDir[ds._dir]
            if old then ds.dataOn = old.dataOn end
        end
    end

    self._projects = projects
    self._dataSources = dataSources

    -- Combined display list: projects first, then data
    self._displayItems = {}
    for _, p in ipairs(projects) do
        table.insert(self._displayItems, p)
    end
    for _, d in ipairs(dataSources) do
        table.insert(self._displayItems, d)
    end
end

------------------------------------------------------------------------
-- Filter bar: tri-state toggles, solo, reset, invert
------------------------------------------------------------------------

-- Compute tri-state for a filter type: "on", "off", "mixed"
function Searching:computeFilterState(filterType)
    local allOn, allOff = true, true
    local hasItems = false

    if filterType == "data" then
        for _, d in ipairs(self._dataSources or {}) do
            hasItems = true
            if d.dataOn then allOff = false else allOn = false end
        end
    else
        for _, p in ipairs(self._projects or {}) do
            if filterType == "project" and p._hasFiles then
                hasItems = true
                if p.filesOn then allOff = false else allOn = false end
            elseif filterType == "memory" and p._hasMemory then
                hasItems = true
                if p.memoryOn then allOff = false else allOn = false end
            elseif filterType == "chats" and p._hasChats then
                hasItems = true
                if p.chatsOn then allOff = false else allOn = false end
            end
        end
    end

    if not hasItems then return "off" end
    if allOn then return "on"
    elseif allOff then return "off"
    else return "mixed" end
end

-- Generic toggle: mixed/off → on, on → off
local function nextToggleState(state)
    if state == "on" then return false end
    return true  -- mixed or off → on
end

function Searching:toggleFilterData()
    local newVal = nextToggleState(self:computeFilterState("data"))
    for _, d in ipairs(self._dataSources or {}) do
        d.dataOn = newVal
    end
    self:refilterSearch()
end

function Searching:toggleFilterProject()
    local newVal = nextToggleState(self:computeFilterState("project"))
    for _, p in ipairs(self._projects or {}) do
        if p._hasFiles then p.filesOn = newVal end
    end
    self:refilterSearch()
end

function Searching:toggleFilterMemory()
    local newVal = nextToggleState(self:computeFilterState("memory"))
    for _, p in ipairs(self._projects or {}) do
        if p._hasMemory then p.memoryOn = newVal end
    end
    self:refilterSearch()
end

function Searching:toggleFilterChats()
    local newVal = nextToggleState(self:computeFilterState("chats"))
    for _, p in ipairs(self._projects or {}) do
        if p._hasChats then p.chatsOn = newVal end
    end
    self:refilterSearch()
end

-- Solo: double-click turns only this type on, all others off
function Searching:soloFilterData()
    for _, d in ipairs(self._dataSources or {}) do d.dataOn = true end
    for _, p in ipairs(self._projects or {}) do
        p.filesOn = false; p.memoryOn = false; p.chatsOn = false
    end
    self:refilterSearch()
end

function Searching:soloFilterProject()
    for _, d in ipairs(self._dataSources or {}) do d.dataOn = false end
    for _, p in ipairs(self._projects or {}) do
        p.filesOn = p._hasFiles; p.memoryOn = false; p.chatsOn = false
    end
    self:refilterSearch()
end

function Searching:soloFilterMemory()
    for _, d in ipairs(self._dataSources or {}) do d.dataOn = false end
    for _, p in ipairs(self._projects or {}) do
        p.filesOn = false; p.memoryOn = p._hasMemory; p.chatsOn = false
    end
    self:refilterSearch()
end

function Searching:soloFilterChats()
    for _, d in ipairs(self._dataSources or {}) do d.dataOn = false end
    for _, p in ipairs(self._projects or {}) do
        p.filesOn = false; p.memoryOn = false; p.chatsOn = p._hasChats
    end
    self:refilterSearch()
end

-- Reset: project ON, memory ON, data ON, chats OFF
function Searching:resetFilters()
    for _, p in ipairs(self._projects or {}) do
        p.filesOn = p._hasFiles
        p.memoryOn = p._hasMemory
        p.chatsOn = false
    end
    for _, d in ipairs(self._dataSources or {}) do
        d.dataOn = true
    end
    self:refilterSearch()
end

-- Invert all filters
function Searching:invertFilters()
    for _, p in ipairs(self._projects or {}) do
        if p._hasFiles then p.filesOn = not p.filesOn end
        if p._hasMemory then p.memoryOn = not p.memoryOn end
        if p._hasChats then p.chatsOn = not p.chatsOn end
    end
    for _, d in ipairs(self._dataSources or {}) do
        d.dataOn = not d.dataOn
    end
    self:refilterSearch()
end

-- Re-run search if active
function Searching:refilterSearch()
    if self._searchView and self.searchQuery ~= "" then
        self:search()
    end
end

-- Filter bar icon methods (filled = on, outline = off/mixed)
function Searching:filterDataIcon()
    return self:computeFilterState("data") == "off" and "file-earmark" or "file-earmark-fill"
end
function Searching:filterProjectIcon()
    return self:computeFilterState("project") == "off" and "folder" or "folder-fill"
end
function Searching:filterMemoryIcon()
    return self:computeFilterState("memory") == "off" and "lightbulb" or "lightbulb-fill"
end
function Searching:filterChatsIcon()
    return self:computeFilterState("chats") == "off" and "chat" or "chat-fill"
end

-- Filter bar boolean methods for ui-class bindings
function Searching:filterDataIsOn() return self:computeFilterState("data") == "on" end
function Searching:filterDataIsMixed() return self:computeFilterState("data") == "mixed" end
function Searching:filterProjectIsOn() return self:computeFilterState("project") == "on" end
function Searching:filterProjectIsMixed() return self:computeFilterState("project") == "mixed" end
function Searching:filterMemoryIsOn() return self:computeFilterState("memory") == "on" end
function Searching:filterMemoryIsMixed() return self:computeFilterState("memory") == "mixed" end
function Searching:filterChatsIsOn() return self:computeFilterState("chats") == "on" end
function Searching:filterChatsIsMixed() return self:computeFilterState("chats") == "mixed" end

-- Tooltip text for filter buttons
function Searching:filterDataTooltip()
    return "Data sources (" .. self:computeFilterState("data") .. ") — double-click to solo"
end
function Searching:filterProjectTooltip()
    return "Project files (" .. self:computeFilterState("project") .. ") — double-click to solo"
end
function Searching:filterMemoryTooltip()
    return "Memories (" .. self:computeFilterState("memory") .. ") — double-click to solo"
end
function Searching:filterChatsTooltip()
    return "Chat logs (" .. self:computeFilterState("chats") .. ") — double-click to solo"
end

-- Build filter opts table from source filter buttons + filter panel fields.
-- Returns a table with filter_files, exclude_files, filter, except arrays
-- suitable for passing to mcp:search_grouped().
function Searching:buildFilterOpts()
    local filter_files = {}
    local exclude_files = {}
    local filter = {}
    local except = {}

    -- Source filter: uses filter_files when any source needs sub-filtering,
    -- otherwise uses exclude_files for fully disabled sources.
    -- Because filter_files is a positive filter (only matching paths survive),
    -- ALL enabled sources must be listed when any filter_files is emitted.
    local hasPartial = false  -- any Claude source with only memory or only chats

    for _, p in ipairs(self._projects or {}) do
        if p._claudeSource then
            if (p.memoryOn and not p.chatsOn) or (p.chatsOn and not p.memoryOn) then
                hasPartial = true
                break
            end
        end
    end

    if hasPartial then
        -- Positive filter mode: list every enabled source explicitly
        for _, p in ipairs(self._projects or {}) do
            if p._fileSource and p.filesOn then
                table.insert(filter_files, p._fileSource.dir .. "/**")
            end
            if p._claudeSource then
                local cdir = p._claudeSource.dir
                if p.memoryOn and p.chatsOn then
                    table.insert(filter_files, cdir .. "/**")
                elseif p.memoryOn then
                    table.insert(filter_files, cdir .. "/memory/**")
                elseif p.chatsOn then
                    table.insert(filter_files, cdir .. "/**")
                    table.insert(exclude_files, cdir .. "/memory/**")
                end
                -- Both off: omit → filtered out
            end
        end

        for _, d in ipairs(self._dataSources or {}) do
            if d.dataOn then
                table.insert(filter_files, d._source.dir .. "/**")
            end
        end
    else
        -- Simple exclude mode: only fully disabled sources
        for _, p in ipairs(self._projects or {}) do
            if p._fileSource and not p.filesOn then
                table.insert(exclude_files, p._fileSource.dir .. "/**")
            end
            if p._claudeSource then
                if not p.memoryOn and not p.chatsOn then
                    table.insert(exclude_files, p._claudeSource.dir .. "/**")
                end
            end
        end

        for _, d in ipairs(self._dataSources or {}) do
            if not d.dataOn then
                table.insert(exclude_files, d._source.dir .. "/**")
            end
        end
    end

    -- Filter panel fields (one pattern per line)
    for line in self.filterFiles:gmatch("[^\n]+") do
        local pat = line:match("^%s*(.-)%s*$")
        if pat and pat ~= "" then
            table.insert(filter_files, pat)
        end
    end
    for line in self.excludeFiles:gmatch("[^\n]+") do
        local pat = line:match("^%s*(.-)%s*$")
        if pat and pat ~= "" then
            table.insert(exclude_files, pat)
        end
    end
    for line in self.filterContent:gmatch("[^\n]+") do
        local q = line:match("^%s*(.-)%s*$")
        if q and q ~= "" then
            table.insert(filter, q)
        end
    end
    for line in self.excludeContent:gmatch("[^\n]+") do
        local q = line:match("^%s*(.-)%s*$")
        if q and q ~= "" then
            table.insert(except, q)
        end
    end

    local opts = {}
    if #filter_files > 0 then opts.filter_files = filter_files end
    if #exclude_files > 0 then opts.exclude_files = exclude_files end
    if #filter > 0 then opts.filter = filter end
    if #except > 0 then opts.except = except end
    return opts
end

-- Search: incremental via variable check cycle.
-- onSearchInput() runs on every variable check when searchQuery changes.
-- If the query changed, has 3+ chars, and no search is pending → fire search.
-- mcp:search_grouped blocks Lua, so keystrokes naturally compress during search.
local MIN_QUERY_LEN = 3

function Searching:onSearchInput()
    local q = self.searchQuery
    if q == self._lastSearchedQuery then return "" end

    if q == "" then
        self:clearSearch()
        return ""
    end

    if #q < MIN_QUERY_LEN then return "" end

    self:search()
    return ""
end

function Searching:search()
    local q = (self.searchQuery:match("^%s*(.-)%s*$") or "")
    if q == "" then return end
    self._lastSearchedQuery = self.searchQuery
    self._searching = true
    -- Clear in-place to preserve table reference for ViewList diffing
    if self._searchGroups then
        for i = #self._searchGroups, 1, -1 do
            table.remove(self._searchGroups, i)
        end
    else
        self._searchGroups = {}
    end

    -- Determine k: if grouping, ask for more chunks to fill groups
    local hpf = tonumber(self._hitsPerFile) or 1
    local k = hpf == 0 and 100 or 20 * hpf
    if k > 100 then k = 100 end

    -- Build opts for mcp:search_grouped
    local opts = self:buildFilterOpts()
    opts.mode = self.searchMode
    opts.k = k

    local results, err = mcp.search_grouped(q, opts)
    self._searching = false

    if not results then
        self._searchView = true
        return
    end

    -- Results are already grouped by file, sorted by best score.
    -- Each group: {path, strategy, chunks={range, score, preview}}
    for rank, group in ipairs(results) do
        local g = session:create(SearchFileGroup)
        g.path = group.path
        g._rank = rank
        g._expanded = false

        local topScore = 0
        local chunks = {}
        for _, chunk in ipairs(group.chunks) do
            local r = session:create(SearchResult)
            r.path = group.path
            r.score = chunk.score
            r.range = chunk.range
            r.text = chunk.preview
            if group.path:sub(1, #HOME) == HOME then
                r.snippet = "~" .. group.path:sub(#HOME + 1)
            else
                r.snippet = group.path
            end
            table.insert(chunks, r)
            if chunk.score > topScore then
                topScore = chunk.score
            end
        end
        g.topScore = topScore
        g._chunks = chunks
        table.insert(self._searchGroups, g)
    end

    self._searchView = true
end

function Searching:clearSearch()
    self.searchQuery = ""
    self._searchResults = {}
    if self._searchGroups then
        for i = #self._searchGroups, 1, -1 do
            table.remove(self._searchGroups, i)
        end
    end
    self._searchView = false
end

function Searching:setModeAbout()
    self.searchMode = "about"
end

function Searching:setModeContains()
    self.searchMode = "contains"
end

function Searching:setModeRegex()
    self.searchMode = "regex"
end

function Searching:modeIsAbout()
    return self.searchMode == "about"
end

function Searching:modeIsContains()
    return self.searchMode == "contains"
end

function Searching:modeIsRegex()
    return self.searchMode == "regex"
end

function Searching:searchResults()
    return self._searchGroups or {}
end

function Searching:hideSearchResults()
    return not self._searchView
end

function Searching:searchResultCount()
    local groups = self._searchGroups or {}
    local nFiles = #groups
    local nChunks = 0
    for _, g in ipairs(groups) do
        nChunks = nChunks + (g._chunks and #g._chunks or 0)
    end
    if nFiles == 0 and self._searchView then return "No results" end
    if nChunks == nFiles then
        if nFiles == 1 then return "1 result" end
        return tostring(nFiles) .. " results"
    end
    return tostring(nChunks) .. " hits across " .. tostring(nFiles) .. " files"
end

-- Filter panel toggle
function Searching:toggleFilterPanel()
    self._showFilterPanel = not self._showFilterPanel
end

function Searching:hideFilterPanel()
    return not self._showFilterPanel
end

function Searching:filterPanelIcon()
    if self._showFilterPanel then return "funnel-fill" end
    -- Show filled funnel if any filter is active
    if self.filterFiles ~= "" or self.excludeFiles ~= ""
        or self.filterContent ~= "" or self.excludeContent ~= "" then
        return "funnel-fill"
    end
    return "funnel"
end

function Searching:hasActiveFilters()
    return self.filterFiles ~= "" or self.excludeFiles ~= ""
        or self.filterContent ~= "" or self.excludeContent ~= ""
end

-- Hits per file
function Searching:hitsPerFileText()
    if self._hitsPerFile == "0" then return "all" end
    return self._hitsPerFile
end

function Searching:cycleHitsPerFile()
    if self._hitsPerFile == "1" then
        self._hitsPerFile = "3"
    elseif self._hitsPerFile == "3" then
        self._hitsPerFile = "0"
    else
        self._hitsPerFile = "1"
    end
    -- Re-search to apply new grouping
    if self._searchView and self.searchQuery ~= "" then
        self:search()
    end
end

------------------------------------------------------------------------
-- Source methods
------------------------------------------------------------------------

function Source:displayDir()
    return compressPath(self.dir)
end

function Source:selectMe()
    if searching._filterClicked then
        searching._filterClicked = false
        return
    end
    searching:selectSource(self)
end

function Source:isSelected()
    return self == searching.selectedSource
end

function Source:countsText()
    return string.format("%d / %d / %d", self.includedCount, self.excludedCount, self.unresolvedCount)
end

function Source:loadRootNodes()
    self._loading = true
    self._visibleNodes = {}
    self._nodeMap = {}

    -- Get missing files for this source
    self._missingPaths = {}
    local missing = arkJSON("missing")
    if missing then
        for _, m in ipairs(missing) do
            local path = m.Path or m.path or ""
            if path:sub(1, #self.dir) == self.dir then
                self._missingPaths[path] = true
            end
        end
    end

    -- Walk root directory
    local entries = listDir(self.dir)
    local included, excluded, unresolved = 0, 0, 0

    for _, entry in ipairs(entries) do
        local node = self:makeNode(entry.name, entry.isDir, 0)
        table.insert(self._visibleNodes, node)
        if node.state == "included" then included = included + 1
        elseif node.state == "excluded" then excluded = excluded + 1
        else unresolved = unresolved + 1 end
    end

    -- Add missing files not on disk at root level
    for path in pairs(self._missingPaths) do
        local rel = path:sub(#self.dir + 2) -- strip source dir + /
        if not rel:find("/") and not self._nodeMap[rel] then
            local node = self:makeNode(rel:match("[^/]+$") or rel, false, 0)
            node.isMissing = true
            node.state = "included" -- missing files were indexed, so included
            table.insert(self._visibleNodes, node)
        end
    end

    self.includedCount = included
    self.excludedCount = excluded
    self.unresolvedCount = unresolved
    self._loaded = true
    self._loading = false
end

function Source:makeNode(relPath, isDir, depth)
    local fullPath = self.dir .. "/" .. relPath

    local node = session:create(Node)
    node.name = relPath:match("[^/]+$") or relPath
    node.relPath = relPath
    node.fullPath = fullPath
    node.isDir = isDir
    node.depth = depth

    -- Classify via show-why
    applyWhy(node)

    -- Check for ignore files
    if isDir then
        for _, ignName in ipairs({".gitignore", ".arkignore"}) do
            local fh = io.open(fullPath .. "/" .. ignName, "r")
            if fh then
                fh:close()
                node.hasIgnoreFile = true
                break
            end
        end
    end

    -- Check if missing
    if self._missingPaths[fullPath] then
        node.isMissing = true
    end

    self._nodeMap[relPath] = node
    return node
end

function Source:loadChildren(parentNode)
    if parentNode._childrenLoaded then return end

    local entries = listDir(parentNode.fullPath)
    local nodes = {}

    for _, entry in ipairs(entries) do
        local relPath = parentNode.relPath .. "/" .. entry.name
        local node = self:makeNode(relPath, entry.isDir, parentNode.depth + 1)
        table.insert(nodes, node)
    end

    -- Add missing files in this directory not on disk
    local prefix = parentNode.fullPath .. "/"
    for path in pairs(self._missingPaths) do
        if path:sub(1, #prefix) == prefix then
            local rel = path:sub(#prefix + 1)
            if not rel:find("/") then
                local relPath = parentNode.relPath .. "/" .. rel
                if not self._nodeMap[relPath] then
                    local node = self:makeNode(relPath, false, parentNode.depth + 1)
                    node.isMissing = true
                    node.state = "included"
                    table.insert(nodes, node)
                end
            end
        end
    end

    -- Find parent index and insert children after it
    local parentIdx = nil
    for i, n in ipairs(self._visibleNodes) do
        if n == parentNode then
            parentIdx = i
            break
        end
    end

    if parentIdx then
        for j, node in ipairs(nodes) do
            table.insert(self._visibleNodes, parentIdx + j, node)
        end
    end

    parentNode._childrenLoaded = true
end

function Source:removeDescendants(parentNode)
    local parentIdx = nil
    for i, n in ipairs(self._visibleNodes) do
        if n == parentNode then
            parentIdx = i
            break
        end
    end
    if not parentIdx then return end

    local removeFrom = parentIdx + 1
    local removeTo = parentIdx
    for i = removeFrom, #self._visibleNodes do
        if self._visibleNodes[i].depth <= parentNode.depth then
            break
        end
        removeTo = i
    end

    for _ = removeFrom, removeTo do
        table.remove(self._visibleNodes, removeFrom)
    end
end

function Source:refreshCounts()
    local inc, exc, unr = 0, 0, 0
    for _, node in pairs(self._nodeMap) do
        if node.state == "included" then inc = inc + 1
        elseif node.state == "excluded" then exc = exc + 1
        else unr = unr + 1 end
    end
    self.includedCount = inc
    self.excludedCount = exc
    self.unresolvedCount = unr
end

function Source:removeMe()
    searching:removeSource()
end

function Source:isLoading()
    return self._loading
end

function Source:notLoading()
    return not self._loading
end

-- Parse textarea text into pattern list (one per line, skip blanks)
local function parsePatterns(text)
    local patterns = {}
    for line in (text or ""):gmatch("[^\n]+") do
        local trimmed = line:match("^%s*(.-)%s*$")
        if trimmed ~= "" then
            table.insert(patterns, trimmed)
        end
    end
    return patterns
end

function Source:savePatterns()
    local newInc = parsePatterns(self.includeText)
    local newExc = parsePatterns(self.excludeText)
    local oldInc = self._includePatterns or {}
    local oldExc = self._excludePatterns or {}

    -- Build sets for diffing
    local oldIncSet, newIncSet = {}, {}
    for _, p in ipairs(oldInc) do oldIncSet[p] = true end
    for _, p in ipairs(newInc) do newIncSet[p] = true end

    local oldExcSet, newExcSet = {}, {}
    for _, p in ipairs(oldExc) do oldExcSet[p] = true end
    for _, p in ipairs(newExc) do newExcSet[p] = true end

    local errors = {}
    local sourceFlag = '--source "' .. self.dir .. '"'

    -- Remove old includes not in new
    for _, p in ipairs(oldInc) do
        if not newIncSet[p] then
            local out = arkCmd('config remove-pattern "' .. p .. '" ' .. sourceFlag)
            if out and out:match("error") then table.insert(errors, out) end
        end
    end

    -- Remove old excludes not in new
    for _, p in ipairs(oldExc) do
        if not newExcSet[p] then
            local out = arkCmd('config remove-pattern "' .. p .. '" ' .. sourceFlag)
            if out and out:match("error") then table.insert(errors, out) end
        end
    end

    -- Add new includes not in old
    for _, p in ipairs(newInc) do
        if not oldIncSet[p] then
            local out = arkCmd('config add-include "' .. p .. '" ' .. sourceFlag)
            if out and out:match("error") then table.insert(errors, out) end
        end
    end

    -- Add new excludes not in old
    for _, p in ipairs(newExc) do
        if not oldExcSet[p] then
            local out = arkCmd('config add-exclude "' .. p .. '" ' .. sourceFlag)
            if out and out:match("error") then table.insert(errors, out) end
        end
    end

    if #errors > 0 then
        self._patternError = table.concat(errors, "\n")
    else
        self._patternError = ""
    end

    -- Reload to sync state
    searching:loadConfig()

    -- Refresh tree if loaded
    if self._loaded then
        self._loaded = false
        self:loadRootNodes()
    end
end


------------------------------------------------------------------------
-- Project methods
------------------------------------------------------------------------

function Project:selectMe()
    if not searching then return end
    if searching._filterClicked then
        searching._filterClicked = false
        return
    end
    local source = self._fileSource or self._claudeSource
    if source then
        searching:selectSource(source)
    end
end

function Project:isSelected()
    if not searching.selectedSource then return false end
    return searching.selectedSource == self._fileSource or searching.selectedSource == self._claudeSource
end

function Project:toggleFiles()
    searching._filterClicked = true
    if self._hasFiles then
        self.filesOn = not self.filesOn
        searching:refilterSearch()
    end
end

function Project:toggleMemory()
    searching._filterClicked = true
    if self._hasMemory then
        self.memoryOn = not self.memoryOn
        searching:refilterSearch()
    end
end

function Project:toggleChats()
    searching._filterClicked = true
    if self._hasChats then
        self.chatsOn = not self.chatsOn
        searching:refilterSearch()
    end
end

function Project:filesIcon()
    if not self._hasFiles then return "folder" end
    return self.filesOn and "folder-fill" or "folder"
end

function Project:memoryIcon()
    if not self._hasMemory then return "lightbulb" end
    return self.memoryOn and "lightbulb-fill" or "lightbulb"
end

function Project:chatsIcon()
    if not self._hasChats then return "chat" end
    return self.chatsOn and "chat-fill" or "chat"
end

function Project:filesIsOn()
    return self._hasFiles and self.filesOn
end
function Project:filesIsDisabled()
    return not self._hasFiles
end

function Project:memoryIsOn()
    return self._hasMemory and self.memoryOn
end
function Project:memoryIsDisabled()
    return not self._hasMemory
end

function Project:chatsIsOn()
    return self._hasChats and self.chatsOn
end
function Project:chatsIsDisabled()
    return not self._hasChats
end

function Project:filesTooltip()
    if not self._hasFiles then return "No file source configured" end
    return self.filesOn and "Files: included in search" or "Files: excluded from search"
end

function Project:memoryTooltip()
    if not self._hasMemory then return "No memory patterns configured" end
    return self.memoryOn and "Memory: included in search" or "Memory: excluded from search"
end

function Project:chatsTooltip()
    if not self._hasChats then return "No chat patterns configured" end
    return self.chatsOn and "Chats: included in search" or "Chats: excluded from search"
end

function Project:displayPath()
    if self._fileSource then
        return compressPath(self._fileSource.dir)
    end
    return compressPath(self._resolvedPath or "")
end

function Project:fullPath()
    if self._fileSource then return self._fileSource.dir end
    return self._resolvedPath or ""
end

function Project:removeMe()
    -- Open the project editor instead of removing directly
    searching:openProjectSearch()
end

------------------------------------------------------------------------
-- DataSource methods
------------------------------------------------------------------------

function DataSource:selectMe()
    if searching._filterClicked then
        searching._filterClicked = false
        return
    end
    if self._source then
        searching:selectSource(self._source)
    end
end

function DataSource:isSelected()
    return searching.selectedSource == self._source
end

function DataSource:toggleData()
    searching._filterClicked = true
    self.dataOn = not self.dataOn
    searching:refilterSearch()
end

function DataSource:dataIcon()
    return self.dataOn and "file-earmark-fill" or "file-earmark"
end

function DataSource:dataIsOn()
    return self.dataOn
end

function DataSource:dataTooltip()
    return self.dataOn and "Included in search" or "Excluded from search"
end

function DataSource:displayDir()
    return self.name
end

function DataSource:fullDir()
    return self._dir
end

function DataSource:removeMe()
    if self._source then
        arkCmd('config remove-source "' .. self._source.dir .. '"')
    end
    if searching.selectedSource == self._source then
        searching.selectedSource = nil
    end
    searching:loadConfig()
end

------------------------------------------------------------------------
-- ProjectCandidate methods
------------------------------------------------------------------------

function ProjectCandidate:toggleSelect()
    self.selected = not self.selected
end

function ProjectCandidate:isSelected()
    return self.selected
end

function ProjectCandidate:isDeselected()
    return not self.selected
end

function ProjectCandidate:checkIcon()
    if self.selected then return "check-square-fill" end
    return "square"
end

function ProjectCandidate:isNew()
    return self.selected and not self._wasConfigured
end

function ProjectCandidate:hideNew()
    return not self:isNew()
end

function ProjectCandidate:isRemoved()
    return not self.selected and self._wasConfigured
end

function ProjectCandidate:hideRemoved()
    return not self:isRemoved()
end

function ProjectCandidate:displayPath()
    return compressPath(self._resolvedPath or "")
end

------------------------------------------------------------------------
-- Node methods
------------------------------------------------------------------------

function Node:toggle()
    if not self.isDir then return end
    if self.expanded then
        self:collapse()
    else
        self:expand()
    end
end

function Node:expand()
    if not self.isDir or self.expanded then return end
    local source = searching.selectedSource
    if not source then return end
    source:loadChildren(self)
    self.expanded = true
end

function Node:collapse()
    if not self.isDir or not self.expanded then return end
    local source = searching.selectedSource
    if not source then return end
    source:removeDescendants(self)
    self.expanded = false
end

function Node:cycleState()
    local source = searching.selectedSource
    if not source then return end
    local srcFlag = ' --source "' .. source.dir .. '"'

    if self.state == "included" then
        -- Switch to excluded: remove current pattern, add exclude
        arkCmd('config remove-pattern "' .. self.name .. '"' .. srcFlag)
        arkCmd('config add-exclude "' .. nodePattern(self, "exclude") .. '"' .. srcFlag)
    elseif self.state == "excluded" then
        -- Clear to unresolved: remove the exclude pattern
        arkCmd('config remove-pattern "' .. nodePattern(self, "exclude") .. '"' .. srcFlag)
    else
        -- Unresolved → included
        arkCmd('config add-include "' .. nodePattern(self, "include") .. '"' .. srcFlag)
    end

    -- Re-classify this node
    applyWhy(self)
    source:refreshCounts()
end

function Node:stateIcon()
    if self.state == "included" then return "✓" end
    if self.state == "excluded" then return "✗" end
    return "?"
end

function Node:stateIncluded()
    return self.state == "included"
end

function Node:stateExcluded()
    return self.state == "excluded"
end

function Node:stateUnresolved()
    return self.state == "unresolved"
end

function Node:indentPx()
    return tostring(self.depth * 20) .. "px"
end

function Node:notExpandable()
    return not self.isDir
end

function Node:expandIcon()
    if self.expanded then return "▼" end
    return "▶"
end

function Node:showExpandIcon()
    return self.isDir
end

function Node:hideExpandIcon()
    return not self.isDir
end

function Node:whyTooltip()
    if not self._whyLoaded then
        self:loadWhy()
    end
    if self.whyPatterns == "" then
        return "No matching pattern"
    end
    local text = self.state:sub(1,1):upper() .. self.state:sub(2) .. " by: " .. self.whyPatterns
    if self.whySources ~= "" then
        text = text .. " (" .. self.whySources .. ")"
    end
    if self.whyConflict then
        text = text .. " — include wins conflict"
    end
    return text
end

function Node:loadWhy()
    applyWhy(self)
end

function Node:hasExceptions()
    return self.isDir and self.state ~= "unresolved"
end

function Node:noExceptions()
    return not self:hasExceptions()
end

function Node:reloadChildren()
    if self.expanded then
        self:collapse()
        self._childrenLoaded = false
        self:expand()
    end
end

function Node:applyException()
    if self.exceptionPattern == "" then return end
    local source = searching.selectedSource
    if not source then return end

    local pattern = self.relPath .. "/" .. self.exceptionPattern

    if self.state == "included" then
        arkCmd('config add-exclude "' .. pattern .. '" --source "' .. source.dir .. '"')
    else
        arkCmd('config add-include "' .. pattern .. '" --source "' .. source.dir .. '"')
    end

    self.exceptionPattern = ""
    self:reloadChildren()
end

function Node:showIgnoreCheckbox()
    return self.isDir and self.hasIgnoreFile
end

function Node:hideIgnoreCheckbox()
    return not self:showIgnoreCheckbox()
end

function Node:toggleHonorIgnore()
    self.honorIgnore = not self.honorIgnore
    self:reloadChildren()
end

------------------------------------------------------------------------
-- Ark.Messaging (cross-project message dashboard)
------------------------------------------------------------------------

Ark.Messaging = session:prototype("Ark.Messaging", {
    _messages = EMPTY,
    _loading = false,
    _chips = EMPTY,       -- Ark.FilterChip[] — one per project
    _statusChips = EMPTY, -- Ark.StatusChip[] — one per status
})
Messaging = Ark.Messaging

Ark.MessageColumn = session:prototype("Ark.MessageColumn", {
    status = "",
    _items = EMPTY,
})
MessageColumn = Ark.MessageColumn

-- Filter chip: one per project, cycles none → to → from → both → none
-- modes: "none", "to", "from", "both"
Ark.FilterChip = session:prototype("Ark.FilterChip", {
    project = "",
    mode = "all",      -- default: unfiltered (show all involving this project)
    matchCount = 0,
    toCount = 0,       -- messages where this project is the target
    fromCount = 0,     -- messages where this project is the sender
})
local FilterChip = Ark.FilterChip

function FilterChip:cycle()
    -- Build available states based on counts
    local states = {"all"}  -- unfiltered is always available
    if self.toCount > 0 then table.insert(states, "to") end
    if self.fromCount > 0 then table.insert(states, "from") end
    table.insert(states, "none")  -- deselected is always available
    -- Find current and advance
    for i, s in ipairs(states) do
        if s == self.mode then
            self.mode = states[(i % #states) + 1]
            return
        end
    end
    self.mode = "all"
end

function FilterChip:label()
    local counts = " " .. self.toCount .. "/" .. self.fromCount
    if self.mode == "all" or self.mode == "none" then
        return self.project .. counts
    end
    return self.mode .. ": " .. self.project .. counts
end

function FilterChip:isActive()
    return self.mode ~= "none"
end

function FilterChip:chipClass()
    if self.mode == "none" then return "msg-chip msg-chip-inactive" end
    if self.matchCount == 0 then return "msg-chip msg-chip-empty" end
    return "msg-chip msg-chip-active"
end

-- Status chip: toggle column visibility
Ark.StatusChip = session:prototype("Ark.StatusChip", {
    status = "",
    visible = true,
    count = 0,
})
local StatusChip = Ark.StatusChip

local STATUS_LABELS = {
    open = "open",
    accepted = "accepted",
    ["in-progress"] = "in progress",
    future = "future",
    completed = "completed",
    denied = "denied",
}

local STATUS_CSS = {
    open = "msg-status-open",
    accepted = "msg-status-active",
    ["in-progress"] = "msg-status-active",
    future = "msg-status-future",
    completed = "msg-status-done",
    denied = "msg-status-denied",
}

function StatusChip:toggle()
    self.visible = not self.visible
end

function StatusChip:label()
    return (STATUS_LABELS[self.status] or self.status) .. " " .. self.count
end

function StatusChip:chipClass()
    local base = "msg-schip"
    local color = STATUS_CSS[self.status] or ""
    if not self.visible then return base .. " msg-schip-hidden " .. color end
    if self.count == 0 then return base .. " msg-schip-empty " .. color end
    return base .. " msg-schip-visible " .. color
end

-- Ark.Message represents a conversation: a request + optional response.
-- Column placement uses the request's @status (requester owns the issue).
Ark.Message = session:prototype("Ark.Message", {
    requestId = "",
    kind = "",          -- "request", "response", or "self"
    -- Request fields
    reqStatus = "",
    reqTo = "",
    reqFrom = "",
    reqSummary = "",
    reqPath = "",
    -- Response fields (empty if no response)
    respStatus = "",
    respTo = "",
    respFrom = "",
    respSummary = "",
    respPath = "",
    _hasResponse = false,
    -- Bookmark fields (populated from @response-handled / @request-handled)
    -- Empty until Go inbox plumbing lands; UI is ready for them.
    reqResponseHandled = "",   -- request's @response-handled value
    respRequestHandled = "",   -- response's @request-handled value
})
Message = Ark.Message

-- Status rank for determining column placement (higher = more advanced)
local STATUS_RANK = {
    open = 1, future = 2, accepted = 3,
    ["in-progress"] = 4, completed = 5, done = 5, denied = 6,
}

function Messaging:new(instance)
    instance = session:create(Messaging, instance)
    instance._messages = {}
    instance._chips = {}
    instance._statusChips = {}
    return instance
end

function Messaging:mutate()
    if self._statusChips == nil then
        self._statusChips = {}
    end
    if self._chips == nil then
        self._chips = {}
        self:refresh()
    end
end

function Messaging:refresh()
    self._loading = true
    local entries = mcp.inbox(true)
    self._loading = false
    if not entries then
        self._messages = {}
        return
    end
    -- Group by requestId
    local byId = {}   -- requestId -> {request=entry, response=entry}
    local order = {}   -- preserve first-seen order
    for _, e in ipairs(entries) do
        local id = e.requestId or ""
        if id == "" then id = e.path end  -- fallback for untagged
        if not byId[id] then
            byId[id] = {}
            table.insert(order, id)
        end
        if e.kind == "response" then
            byId[id].response = e
        else
            byId[id].request = e
        end
    end
    -- Build merged Message objects
    local msgs = {}
    for _, id in ipairs(order) do
        local pair = byId[id]
        local m = session:create(Message)
        m.requestId = id
        local req = pair.request
        local resp = pair.response
        if req then
            m.kind = req.kind or "request"
            m.reqStatus = req.status
            m.reqTo = req.to
            m.reqFrom = req.from
            m.reqSummary = req.summary
            m.reqPath = req.path
            m.reqResponseHandled = req.responseHandled or ""
        end
        if resp then
            m._hasResponse = true
            m.respStatus = resp.status
            m.respTo = resp.to
            m.respFrom = resp.from
            m.respSummary = resp.summary
            m.respPath = resp.path
            m.respRequestHandled = resp.requestHandled or ""
            if not req then
                -- Orphan response with no request — use response fields for display
                m.kind = "response"
                m.reqTo = resp.from    -- swap: response's from is request's to
                m.reqFrom = resp.to
                m.reqSummary = resp.summary
            end
        end
        table.insert(msgs, m)
    end
    self._messages = msgs
    -- Rebuild chips: one per distinct project, preserving existing modes
    local oldModes = {}
    for _, chip in ipairs(self._chips or {}) do
        oldModes[chip.project] = chip.mode
    end
    local seen = {}
    local projects = {}
    for _, m in ipairs(msgs) do
        for _, p in ipairs({m.reqFrom, m.reqTo}) do
            if p and p ~= "" and not seen[p] then
                seen[p] = true
                table.insert(projects, p)
            end
        end
    end
    table.sort(projects)
    local chips = {}
    for _, p in ipairs(projects) do
        local chip = session:create(FilterChip)
        chip.project = p
        chip.mode = oldModes[p] or "all"
        table.insert(chips, chip)
    end
    self._chips = chips
    -- Rebuild status chips, preserving visibility
    local oldVis = {}
    for _, sc in ipairs(self._statusChips or {}) do
        oldVis[sc.status] = sc.visible
    end
    local statusOrder = {"open", "accepted", "in-progress", "future", "completed", "denied"}
    local schips = {}
    for _, s in ipairs(statusOrder) do
        local sc = session:create(StatusChip)
        sc.status = s
        sc.visible = oldVis[s] ~= false  -- default true
        table.insert(schips, sc)
    end
    self._statusChips = schips
end

function Messaging:statusChips()
    return self._statusChips or {}
end

function Messaging:columns()
    -- Update chip match counts
    local msgs = self._messages or {}
    for _, chip in ipairs(self._chips or {}) do
        local toC, fromC = 0, 0
        local p = chip.project
        for _, m in ipairs(msgs) do
            if m.reqTo == p then toC = toC + 1 end
            if m.reqFrom == p then fromC = fromC + 1 end
        end
        chip.toCount = toC
        chip.fromCount = fromC
        local mode = chip.mode
        if mode == "to" then chip.matchCount = toC
        elseif mode == "from" then chip.matchCount = fromC
        elseif mode == "all" then
            -- Count distinct messages (don't double-count self-messages)
            local allC = 0
            for _, m in ipairs(msgs) do
                if m.reqTo == p or m.reqFrom == p then allC = allC + 1 end
            end
            chip.matchCount = allC
        else chip.matchCount = 0
        end
    end
    -- Build status visibility lookup and count per status
    local statusVisible = {}
    for _, sc in ipairs(self._statusChips or {}) do
        statusVisible[sc.status] = sc.visible
    end
    local colOrder = {"open", "accepted", "in-progress", "future", "completed", "denied"}
    local filtered = self:filteredMessages()
    -- Count items per status (for chip counts) and build visible columns
    local statusCounts = {}
    for _, m in ipairs(filtered) do
        local s = m:effectiveStatus()
        statusCounts[s] = (statusCounts[s] or 0) + 1
    end
    -- Update status chip counts
    for _, sc in ipairs(self._statusChips or {}) do
        sc.count = statusCounts[sc.status] or 0
    end
    local cols = {}
    for _, status in ipairs(colOrder) do
        if statusVisible[status] ~= false then
            local items = {}
            for _, m in ipairs(filtered) do
                if m:effectiveStatus() == status then
                    table.insert(items, m)
                end
            end
            if #items > 0 then
                local col = session:create(MessageColumn)
                col.status = status
                col._items = items
                table.insert(cols, col)
            end
        end
    end
    return cols
end

function Messaging:messageCount()
    return #(self._messages or {})
end

function Messaging:isLoading()
    return self._loading
end

function Messaging:isEmpty()
    return self:messageCount() == 0 and not self._loading
end

function Messaging:statusText()
    local filtered = self:filteredMessages()
    local total = #(self._messages or {})
    local n = #filtered
    if total == 0 then return "No messages" end
    if n == total then
        if n == 1 then return "1 conversation" end
        return tostring(n) .. " conversations"
    end
    return tostring(n) .. " of " .. tostring(total) .. " conversations"
end

function Messaging:chips()
    return self._chips or {}
end

function Messaging:hasActiveFilters()
    for _, chip in ipairs(self._chips or {}) do
        if chip.mode ~= "none" then return true end
    end
    return false
end

function Messaging:filteredMessages()
    local msgs = self._messages or {}
    if not self:hasActiveFilters() then return msgs end
    local result = {}
    for _, m in ipairs(msgs) do
        local matched = false
        for _, chip in ipairs(self._chips) do
            if chip.mode ~= "none" then
                local p = chip.project
                if chip.mode == "from" and m.reqFrom == p then matched = true end
                if chip.mode == "to" and m.reqTo == p then matched = true end
                if chip.mode == "all" and (m.reqFrom == p or m.reqTo == p) then matched = true end
            end
        end
        if matched then table.insert(result, m) end
    end
    return result
end

function MessageColumn:items()
    return self._items or {}
end

function MessageColumn:statusLabel()
    local labels = {
        open = "Open",
        accepted = "Accepted",
        ["in-progress"] = "In Progress",
        future = "Future",
        completed = "Completed",
        denied = "Denied",
    }
    return labels[self.status] or self.status
end

function MessageColumn:itemCount()
    return #(self._items or {})
end

function MessageColumn:statusClass()
    local s = self.status
    if s == "open" then return "msg-status-open" end
    if s == "accepted" or s == "in-progress" then return "msg-status-active" end
    if s == "completed" or s == "done" then return "msg-status-done" end
    if s == "denied" then return "msg-status-denied" end
    if s == "future" then return "msg-status-future" end
    return ""
end

-- The effective status determines column placement.
-- Request's status drives the column — the requester owns the issue.
function Message:effectiveStatus()
    local s = self.reqStatus
    if s == "done" then s = "completed" end
    return s
end

function Message:openFile()
    -- Open the request file (primary); response can be opened separately
    local path = self.reqPath
    if path == "" then path = self.respPath end
    mcp.open(path)
end

function Message:openResponse()
    if self._hasResponse and self.respPath ~= "" then
        mcp.open(self.respPath)
    end
end

function Message:shortSummary()
    local s = self.reqSummary or ""
    if #s > 60 then
        s = s:sub(1, 57) .. "..."
    end
    return s
end

function Message:projectLabel()
    local from = self.reqFrom or "?"
    local to = self.reqTo or "?"
    return from .. " → " .. to
end

function Message:hasResponse()
    return self._hasResponse
end

function Message:noResponse()
    return not self._hasResponse
end

function Message:responseStatusLabel()
    if not self._hasResponse then return "" end
    local labels = {
        accepted = "accepted",
        ["in-progress"] = "in progress",
        completed = "completed",
        done = "completed",
        denied = "denied",
    }
    return labels[self.respStatus] or self.respStatus
end

function Message:statusClass()
    local s = self:effectiveStatus()
    if s == "open" then return "msg-status-open" end
    if s == "accepted" or s == "in-progress" then return "msg-status-active" end
    if s == "completed" or s == "done" then return "msg-status-done" end
    if s == "denied" then return "msg-status-denied" end
    if s == "future" then return "msg-status-future" end
    return ""
end

-- Return stale bookmark chips as "PROJECT:status" strings.
-- Empty table when all bookmarks are current.
function Message:bookmarkChips()
    local chips = {}
    -- Check request side: is reqResponseHandled behind respStatus?
    if self._hasResponse and self.respStatus ~= "" then
        local handled = self.reqResponseHandled
        if handled == "" or handled ~= self.respStatus then
            -- Requester hasn't caught up with response
            table.insert(chips, self.reqFrom .. ":" .. (handled ~= "" and handled or "unseen"))
        end
    end
    -- Check response side: is respRequestHandled behind reqStatus?
    if self._hasResponse and self.reqStatus ~= "" then
        local handled = self.respRequestHandled
        if handled == "" or handled ~= self.reqStatus then
            -- Responder hasn't caught up with request
            table.insert(chips, self.respFrom .. ":" .. (handled ~= "" and handled or "unseen"))
        end
    end
    return chips
end

function Message:hasBookmarkLag()
    return #self:bookmarkChips() > 0
end

function Message:noBookmarkLag()
    return #self:bookmarkChips() == 0
end

function Message:bookmarkLabel()
    local chips = self:bookmarkChips()
    if #chips == 0 then return "" end
    local parts = {}
    for _, chip in ipairs(chips) do
        table.insert(parts, '<span class="msg-bookmark-pill">' .. chip .. '</span>')
    end
    return table.concat(parts, "")
end

------------------------------------------------------------------------
-- Instance creation
------------------------------------------------------------------------

if not session.reloading or not ark then
    ark = Ark:new()
end
-- Update the forward-declared local (line 77) that all methods close over
searching = ark._searching
