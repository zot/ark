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
        return "~/" .. "\xE2\x80\xA6" .. lastName
    end

    -- Rule 3: Other user paths /home/otheruser/...
    local otherUser, otherRest = fullPath:match("^/home/([^/]+)(.*)")
    if otherUser then
        local lastName = fullPath:match("([^/]+)$") or fullPath
        if otherRest and #otherRest > 1 then
            return "~" .. otherUser .. "/" .. "\xE2\x80\xA6" .. lastName
        else
            return "~" .. otherUser
        end
    end

    -- Rule 4: Other absolute paths /usr/share/long/path/dirname
    local firstComp = fullPath:match("^/([^/]+)")
    local lastName = fullPath:match("([^/]+)$") or fullPath
    if firstComp and firstComp ~= lastName then
        return "/" .. firstComp .. "/" .. "\xE2\x80\xA6" .. lastName
    end

    return display
end
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
-- Ark (main app)
------------------------------------------------------------------------

Ark = session:prototype("Ark", {
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
    _searching = false,
    _searchView = false,
    _lastSearchedQuery = "",
})

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
})
local Source = Ark.Source

Ark.SearchResult = session:prototype("Ark.SearchResult", {
    path = "",
    score = 0,
    snippet = "",
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

function Ark:mutate()
    if self._searchResults == nil then
        self._searchResults = {}
    end
    if self.searchQuery == nil then
        self.searchQuery = ""
    end
    if self.searchMode == nil then
        self.searchMode = "contains"
    end
end

function Ark:new(instance)
    instance = session:create(Ark, instance)
    instance._sources = instance._sources or {}
    instance._statusCounts = instance._statusCounts or {}
    instance:loadConfig()
    return instance
end

function Ark:loadConfig()
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
        -- Reuse existing Source to preserve loaded state
        local existing = oldSources[dir]
        if existing then
            existing.strategy = strategy
            table.insert(self._sources, existing)
        else
            local s = session:create(Source)
            s.dir = dir
            s.strategy = strategy
            s._visibleNodes = {}
            s._nodeMap = {}
            s._missingPaths = {}
            table.insert(self._sources, s)
        end
    end

    self:checkServer()
end

function Ark:refresh()
    arkCmd("scan")
    arkCmd("refresh")
    self:loadConfig()
    if self.selectedSource and self.selectedSource._loaded then
        self.selectedSource._loaded = false
        self.selectedSource:loadRootNodes()
    end
end

function Ark:selectSource(source)
    self.selectedSource = source
    self.showAddForm = false
    self._searchView = false
    if not source._loaded and not source._loading then
        source:loadRootNodes()
    end
end

function Ark:openAddForm()
    self.showAddForm = true
end

function Ark:cancelAddForm()
    self.showAddForm = false
    self.newDir = ""
    self.newStrategy = "markdown"
end

function Ark:addSource()
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

function Ark:removeSource()
    if not self.selectedSource then return end
    arkCmd('config remove-source "' .. self.selectedSource.dir .. '"')
    self.selectedSource = nil
    self:loadConfig()
end

function Ark:collapseResolved()
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

function Ark:checkServer()
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

function Ark:visibleNodes()
    if self.selectedSource then
        return self.selectedSource._visibleNodes or {}
    end
    return {}
end

function Ark:showSourceDetail()
    return self.selectedSource ~= nil and not self.showAddForm and not self._searchView
end

function Ark:hideSourceDetail()
    return not self:showSourceDetail()
end

function Ark:showPlaceholder()
    return self.selectedSource == nil and not self.showAddForm and not self._searchView
end

function Ark:hidePlaceholder()
    return not self:showPlaceholder()
end

function Ark:hideAddForm()
    return not self.showAddForm
end

function Ark:sourceHeaderText()
    if self.selectedSource then return compressPath(self.selectedSource.dir) end
    return ""
end

function Ark:sourceHeaderFullPath()
    if self.selectedSource then return self.selectedSource.dir end
    return ""
end

function Ark:statusText()
    local c = self._statusCounts or {}
    return string.format("✓ %d  |  ✗ %d  |  ? %d  |  👻 %d",
        c.included or 0, c.stale or 0, c.unresolved or 0, c.missing or 0)
end

function Ark:serverStatusText()
    if self._serverRunning then return "●" end
    return "○"
end

function Ark:prefillAddForm(subpath, strategy)
    self.newDir = HOME .. "/" .. subpath
    self.newStrategy = strategy
    self.showAddForm = true
end

function Ark:quickAddClaudeProjects()
    -- One glob source with include patterns; global strategy mappings handle file types
    arkCmd('config add-source "~/.claude/projects/*"')
    arkCmd('config add-include "*.jsonl" --source "~/.claude/projects/*"')
    arkCmd('config add-include "memory/**" --source "~/.claude/projects/*"')
    arkCmd('config add-strategy "*.jsonl" jsonl')
    arkCmd('config add-strategy "*.md" markdown')
    self:loadConfig()
end

-- Search: incremental via variable check cycle.
-- onSearchInput() runs on every variable check when searchQuery changes.
-- If the query changed, has 3+ chars, and no search is pending → fire search.
-- io.popen blocks Lua, so keystrokes naturally compress during search.
local MIN_QUERY_LEN = 3

function Ark:onSearchInput()
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

function Ark:search()
    if self.searchQuery == "" then return end
    self._lastSearchedQuery = self.searchQuery
    self._searching = true
    self._searchResults = {}

    local flag = "--" .. self.searchMode
    local output = arkCmd('search ' .. flag .. ' "' .. self.searchQuery .. '" -k 20 -scores')
    self._searching = false

    if not output or output == "" then
        self._searchView = true
        return
    end

    local results = {}
    local rank = 0
    for line in output:gmatch("[^\n]+") do
        rank = rank + 1
        -- Format: "path:startline-endline\tscore" or just "path:startline-endline"
        local pathpart, score = line:match("^(.+)\t([%d%.]+)$")
        if not pathpart then
            pathpart = line:match("^%s*(.+)%s*$")
            score = "0"
        end
        if pathpart and pathpart ~= "" then
            -- Strip :startline-endline suffix
            local path = pathpart:match("^(.+):%d+%-%d+$") or pathpart
            local r = session:create(SearchResult)
            r.path = path
            r.score = tonumber(score) or 0
            r._rank = rank
            -- Shorten home paths for display
            if path:sub(1, #HOME) == HOME then
                r.snippet = "~" .. path:sub(#HOME + 1)
            else
                r.snippet = path
            end
            table.insert(results, r)
        end
    end

    self._searchResults = results
    self._searchView = true
end

function Ark:clearSearch()
    self.searchQuery = ""
    self._searchResults = {}
    self._searchView = false
end

function Ark:setModeAbout()
    self.searchMode = "about"
end

function Ark:setModeContains()
    self.searchMode = "contains"
end

function Ark:setModeRegex()
    self.searchMode = "regex"
end

function Ark:modeIsAbout()
    return self.searchMode == "about"
end

function Ark:modeIsContains()
    return self.searchMode == "contains"
end

function Ark:modeIsRegex()
    return self.searchMode == "regex"
end

function Ark:searchResults()
    return self._searchResults or {}
end

function Ark:hasSearchResults()
    return self._searchView
end

function Ark:hideSearchResults()
    return not self._searchView
end

function Ark:searchResultCount()
    local n = self._searchResults and #self._searchResults or 0
    if n == 0 and self._searchView then return "No results" end
    if n == 1 then return "1 result" end
    return tostring(n) .. " results"
end

------------------------------------------------------------------------
-- Source methods
------------------------------------------------------------------------

function Source:displayDir()
    return compressPath(self.dir)
end

function Source:selectMe()
    ark:selectSource(self)
end

function Source:isSelected()
    return self == ark.selectedSource
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
    ark:removeSource()
end

function Source:isLoading()
    return self._loading
end

function Source:notLoading()
    return not self._loading
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
    local source = ark.selectedSource
    if not source then return end
    source:loadChildren(self)
    self.expanded = true
end

function Node:collapse()
    if not self.isDir or not self.expanded then return end
    local source = ark.selectedSource
    if not source then return end
    source:removeDescendants(self)
    self.expanded = false
end

function Node:cycleState()
    local source = ark.selectedSource
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
    local source = ark.selectedSource
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
-- Instance creation
------------------------------------------------------------------------

if not session.reloading then
    ark = Ark:new()
end
