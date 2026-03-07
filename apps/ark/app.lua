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
        t = t:sub(1, 400) .. "\xe2\x80\xa6"
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
    selected = true,
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

function Ark:mutate()
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
        local inc = src.Include or src.include or {}
        local exc = src.Exclude or src.exclude or {}

        -- Reuse existing Source to preserve loaded state
        local existing = oldSources[dir]
        if existing then
            existing.strategy = strategy
            existing._includePatterns = inc
            existing._excludePatterns = exc
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
            s.includeText = table.concat(inc, "\n")
            s.excludeText = table.concat(exc, "\n")
            table.insert(self._sources, s)
        end
    end

    self:checkServer()
    self:buildDisplayItems()
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

function Ark:saveSelectedPatterns()
    if not self.selectedSource then return end
    self.selectedSource:savePatterns()
    if (self.selectedSource._patternError or "") == "" then
        mcp:notify("Patterns saved", "success")
    else
        mcp:notify("Pattern errors: " .. self.selectedSource._patternError, "danger")
    end
end

function Ark:togglePatterns()
    self._showPatterns = not self._showPatterns
end

function Ark:hidePatterns()
    return not self._showPatterns or not self.selectedSource
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

function Ark:quickAddClaudeProjects()
    -- One glob source with include patterns; global strategy mappings handle file types
    arkCmd('config add-source "~/.claude/projects/*"')
    arkCmd('config add-include "*.jsonl" --source "~/.claude/projects/*"')
    arkCmd('config add-include "memory/**" --source "~/.claude/projects/*"')
    arkCmd('config add-strategy "*.jsonl" jsonl')
    arkCmd('config add-strategy "*.md" markdown')
    self:loadConfig()
end

------------------------------------------------------------------------
-- Project search panel
------------------------------------------------------------------------

function Ark:openProjectSearch()
    self._projectSearchOpen = true
    self._projectSearchQuery = ""
    self:scanProjectCandidates()
end

function Ark:closeProjectSearch()
    self._projectSearchOpen = false
    self._projectCandidates = {}
    self._projectSearchQuery = ""
end

function Ark:hideProjectSearch()
    return not self._projectSearchOpen
end

function Ark:scanProjectCandidates()
    local claudeDir = HOME .. "/.claude/projects"
    local entries = listDir(claudeDir)

    -- Collect existing project slugs to exclude
    local existingSlugs = {}
    for _, p in ipairs(self._projects or {}) do
        existingSlugs[p.slug] = true
    end

    local candidates = {}
    for _, entry in ipairs(entries) do
        if entry.isDir and entry.name:sub(1, 1) == "-" then
            local slug = entry.name
            if not existingSlugs[slug] then
                local c = session:create(ProjectCandidate)
                c.slug = slug
                local name, resolvedPath = resolveSlugName(slug)
                c.name = name
                c._resolvedPath = resolvedPath or ""
                c.selected = true
                table.insert(candidates, c)
            end
        end
    end

    table.sort(candidates, function(a, b) return a.name:lower() < b.name:lower() end)
    self._projectCandidates = candidates
end

function Ark:refreshProjectSearch()
    self:scanProjectCandidates()
end

function Ark:filteredProjectCandidates()
    local q = (self._projectSearchQuery or ""):lower()
    if q == "" then return self._projectCandidates or {} end
    local results = {}
    for _, c in ipairs(self._projectCandidates or {}) do
        if c.name:lower():find(q, 1, true) or c.slug:lower():find(q, 1, true) then
            table.insert(results, c)
        end
    end
    return results
end

function Ark:addSelectedProjects()
    -- Ensure Claude glob source exists
    arkCmd('config add-source "~/.claude/projects/*"')
    arkCmd('config add-include "*.jsonl" --source "~/.claude/projects/*"')
    arkCmd('config add-include "memory/**" --source "~/.claude/projects/*"')
    arkCmd('config add-strategy "*.jsonl" jsonl')
    arkCmd('config add-strategy "*.md" markdown')

    for _, c in ipairs(self._projectCandidates or {}) do
        if c.selected and c._resolvedPath ~= "" then
            arkCmd('config add-source "' .. c._resolvedPath .. '"')
        end
    end

    self:closeProjectSearch()
    self:loadConfig()
end

function Ark:hideAddSelectedBtn()
    for _, c in ipairs(self._projectCandidates or {}) do
        if c.selected then return false end
    end
    return true
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
    local mainRest = rest:match("^([^%-].-)[%-][%-]") or rest

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
            return remaining, current .. "/" .. remaining
        end
    end

    -- All parts consumed — last directory is the name
    local name = current:match("([^/]+)$")
    if current == HOME then
        if mainRest:sub(1, 5) == "work-" then mainRest = mainRest:sub(6) end
        return mainRest, HOME .. "/" .. mainRest
    end
    return name or rest, current
end

------------------------------------------------------------------------
-- Display grouping: sources → projects + data sources
------------------------------------------------------------------------

function Ark:buildDisplayItems()
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
function Ark:computeFilterState(filterType)
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

function Ark:toggleFilterData()
    local newVal = nextToggleState(self:computeFilterState("data"))
    for _, d in ipairs(self._dataSources or {}) do
        d.dataOn = newVal
    end
    self:refilterSearch()
end

function Ark:toggleFilterProject()
    local newVal = nextToggleState(self:computeFilterState("project"))
    for _, p in ipairs(self._projects or {}) do
        if p._hasFiles then p.filesOn = newVal end
    end
    self:refilterSearch()
end

function Ark:toggleFilterMemory()
    local newVal = nextToggleState(self:computeFilterState("memory"))
    for _, p in ipairs(self._projects or {}) do
        if p._hasMemory then p.memoryOn = newVal end
    end
    self:refilterSearch()
end

function Ark:toggleFilterChats()
    local newVal = nextToggleState(self:computeFilterState("chats"))
    for _, p in ipairs(self._projects or {}) do
        if p._hasChats then p.chatsOn = newVal end
    end
    self:refilterSearch()
end

-- Solo: double-click turns only this type on, all others off
function Ark:soloFilterData()
    for _, d in ipairs(self._dataSources or {}) do d.dataOn = true end
    for _, p in ipairs(self._projects or {}) do
        p.filesOn = false; p.memoryOn = false; p.chatsOn = false
    end
    self:refilterSearch()
end

function Ark:soloFilterProject()
    for _, d in ipairs(self._dataSources or {}) do d.dataOn = false end
    for _, p in ipairs(self._projects or {}) do
        p.filesOn = p._hasFiles; p.memoryOn = false; p.chatsOn = false
    end
    self:refilterSearch()
end

function Ark:soloFilterMemory()
    for _, d in ipairs(self._dataSources or {}) do d.dataOn = false end
    for _, p in ipairs(self._projects or {}) do
        p.filesOn = false; p.memoryOn = p._hasMemory; p.chatsOn = false
    end
    self:refilterSearch()
end

function Ark:soloFilterChats()
    for _, d in ipairs(self._dataSources or {}) do d.dataOn = false end
    for _, p in ipairs(self._projects or {}) do
        p.filesOn = false; p.memoryOn = false; p.chatsOn = p._hasChats
    end
    self:refilterSearch()
end

-- Reset: project ON, memory ON, data ON, chats OFF
function Ark:resetFilters()
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
function Ark:invertFilters()
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
function Ark:refilterSearch()
    if self._searchView and self.searchQuery ~= "" then
        self:search()
    end
end

-- Filter bar icon methods (filled = on, outline = off/mixed)
function Ark:filterDataIcon()
    return self:computeFilterState("data") == "off" and "file-earmark" or "file-earmark-fill"
end
function Ark:filterProjectIcon()
    return self:computeFilterState("project") == "off" and "folder" or "folder-fill"
end
function Ark:filterMemoryIcon()
    return self:computeFilterState("memory") == "off" and "lightbulb" or "lightbulb-fill"
end
function Ark:filterChatsIcon()
    return self:computeFilterState("chats") == "off" and "chat" or "chat-fill"
end

-- Filter bar boolean methods for ui-class bindings
function Ark:filterDataIsOn() return self:computeFilterState("data") == "on" end
function Ark:filterDataIsMixed() return self:computeFilterState("data") == "mixed" end
function Ark:filterProjectIsOn() return self:computeFilterState("project") == "on" end
function Ark:filterProjectIsMixed() return self:computeFilterState("project") == "mixed" end
function Ark:filterMemoryIsOn() return self:computeFilterState("memory") == "on" end
function Ark:filterMemoryIsMixed() return self:computeFilterState("memory") == "mixed" end
function Ark:filterChatsIsOn() return self:computeFilterState("chats") == "on" end
function Ark:filterChatsIsMixed() return self:computeFilterState("chats") == "mixed" end

-- Tooltip text for filter buttons
function Ark:filterDataTooltip()
    return "Data sources (" .. self:computeFilterState("data") .. ") — double-click to solo"
end
function Ark:filterProjectTooltip()
    return "Project files (" .. self:computeFilterState("project") .. ") — double-click to solo"
end
function Ark:filterMemoryTooltip()
    return "Memories (" .. self:computeFilterState("memory") .. ") — double-click to solo"
end
function Ark:filterChatsTooltip()
    return "Chat logs (" .. self:computeFilterState("chats") .. ") — double-click to solo"
end

-- Build filter flags from source filter buttons + filter panel fields
function Ark:buildFilterFlags()
    local flags = {}

    -- Source filter: uses --filter-files when any source needs sub-filtering,
    -- otherwise uses --exclude-files for fully disabled sources.
    -- Because --filter-files is a positive filter (only matching paths survive),
    -- ALL enabled sources must be listed when any --filter-files is emitted.
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
        local filterPatterns = {}

        for _, p in ipairs(self._projects or {}) do
            if p._fileSource and p.filesOn then
                table.insert(filterPatterns, p._fileSource.dir .. "/**")
            end
            if p._claudeSource then
                local cdir = p._claudeSource.dir
                if p.memoryOn and p.chatsOn then
                    table.insert(filterPatterns, cdir .. "/**")
                elseif p.memoryOn then
                    table.insert(filterPatterns, cdir .. "/memory/**")
                elseif p.chatsOn then
                    table.insert(filterPatterns, cdir .. "/**")
                    table.insert(flags, '--exclude-files "' .. cdir .. '/memory/**"')
                end
                -- Both off: omit → filtered out
            end
        end

        for _, d in ipairs(self._dataSources or {}) do
            if d.dataOn then
                table.insert(filterPatterns, d._source.dir .. "/**")
            end
        end

        for _, pat in ipairs(filterPatterns) do
            table.insert(flags, '--filter-files "' .. pat .. '"')
        end
    else
        -- Simple exclude mode: only fully disabled sources
        for _, p in ipairs(self._projects or {}) do
            if p._fileSource and not p.filesOn then
                table.insert(flags, '--exclude-files "' .. p._fileSource.dir .. '/**"')
            end
            if p._claudeSource then
                if not p.memoryOn and not p.chatsOn then
                    table.insert(flags, '--exclude-files "' .. p._claudeSource.dir .. '/**"')
                end
            end
        end

        for _, d in ipairs(self._dataSources or {}) do
            if not d.dataOn then
                table.insert(flags, '--exclude-files "' .. d._source.dir .. '/**"')
            end
        end
    end

    -- Filter panel fields (one pattern per line)
    for line in self.filterFiles:gmatch("[^\n]+") do
        local pat = line:match("^%s*(.-)%s*$")
        if pat and pat ~= "" then
            table.insert(flags, '--filter-files "' .. pat .. '"')
        end
    end
    for line in self.excludeFiles:gmatch("[^\n]+") do
        local pat = line:match("^%s*(.-)%s*$")
        if pat and pat ~= "" then
            table.insert(flags, '--exclude-files "' .. pat .. '"')
        end
    end
    for line in self.filterContent:gmatch("[^\n]+") do
        local q = line:match("^%s*(.-)%s*$")
        if q and q ~= "" then
            table.insert(flags, '--filter "' .. q .. '"')
        end
    end
    for line in self.excludeContent:gmatch("[^\n]+") do
        local q = line:match("^%s*(.-)%s*$")
        if q and q ~= "" then
            table.insert(flags, '--except "' .. q .. '"')
        end
    end

    return table.concat(flags, " ")
end

-- Kept for backward compat during transition
function Ark:sourceFilterFlags()
    return self:buildFilterFlags()
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

    local flag = "--" .. self.searchMode
    local filterFlags = self:buildFilterFlags()

    -- Determine k: if grouping, ask for more chunks to fill groups
    local hpf = tonumber(self._hitsPerFile) or 1
    local k = hpf == 0 and 100 or 20 * hpf
    if k > 100 then k = 100 end

    local cmd = 'search ' .. flag .. ' "' .. q .. '" -k ' .. tostring(k) .. ' -chunks --preview 300'
    if filterFlags ~= "" then
        cmd = cmd .. " " .. filterFlags
    end

    -- Read pipe line-by-line: avoids building a 2MB string then splitting it
    -- (straycat: process each line as it arrives, no gmatch over the whole output)
    local dbFlag = '--dir "' .. DB_PATH .. '"'
    local fullCmd = '"' .. ARK_BIN .. '" search ' .. dbFlag .. ' ' .. flag
        .. ' "' .. q .. '" -k ' .. tostring(k) .. ' -chunks --preview 300'
    if filterFlags ~= "" then
        fullCmd = fullCmd .. " " .. filterFlags
    end
    local handle = io.popen(ARK_PATH .. " " .. fullCmd .. " 2>/dev/null")
    self._searching = false

    if not handle then
        self._searchView = true
        return
    end

    -- Parse JSONL chunks line-by-line and group by file
    -- For "contains" mode, post-filter: trigram index returns superset,
    -- verify chunk text actually contains the query string
    local fileOrder = {}   -- ordered list of paths (first seen)
    local fileMap = {}     -- path → { chunks = {}, topScore = 0 }

    for line in handle:lines() do
        local path = line:match('"path":"(.-)"')
        if not path then goto continue end

        local range = line:match('"range":"(.-)"') or ""
        local score = tonumber(line:match('"score":([%d%.]+)')) or 0

        -- Extract preview field (server-side windowed, ~300 chars)
        local text = ""
        local previewStart = line:find('"preview":"')
        if previewStart then
            local ps = previewStart + 11
            -- Find closing quote (not escaped)
            local pe = ps
            while pe <= #line do
                local ch = line:sub(pe, pe)
                if ch == '\\' then
                    pe = pe + 2  -- skip escaped char
                elseif ch == '"' then
                    break
                else
                    pe = pe + 1
                end
            end
            text = line:sub(ps, pe - 1)
            -- Unescape JSON string
            text = text:gsub('\\n', '\n'):gsub('\\t', '\t'):gsub('\\"', '"'):gsub('\\\\', '\\')
        end

        -- No post-filter needed: the trigram index verified the match,
        -- and --preview centers the window on it when possible.
        -- When the match is deep in a huge chunk, the preview shows what
        -- it can — dropping the result would be worse than showing it.

        if not fileMap[path] then
            fileMap[path] = { chunks = {}, topScore = 0 }
            table.insert(fileOrder, path)
        end
        local group = fileMap[path]
        if score > group.topScore then
            group.topScore = score
        end

        local r = session:create(SearchResult)
        r.path = path
        r.score = score
        r.range = range
        r.text = text
        if path:sub(1, #HOME) == HOME then
            r.snippet = "~" .. path:sub(#HOME + 1)
        else
            r.snippet = path
        end
        table.insert(group.chunks, r)
        ::continue::
    end
    handle:close()

    -- Build grouped results — insert into existing table for ViewList
    for rank, path in ipairs(fileOrder) do
        local gdata = fileMap[path]
        local g = session:create(SearchFileGroup)
        g.path = path
        g.topScore = gdata.topScore
        g._chunks = gdata.chunks
        g._rank = rank
        g._expanded = false
        table.insert(self._searchGroups, g)
    end

    self._searchView = true
end

function Ark:clearSearch()
    self.searchQuery = ""
    self._searchResults = {}
    if self._searchGroups then
        for i = #self._searchGroups, 1, -1 do
            table.remove(self._searchGroups, i)
        end
    end
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
    return self._searchGroups or {}
end

function Ark:hideSearchResults()
    return not self._searchView
end

function Ark:searchResultCount()
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
function Ark:toggleFilterPanel()
    self._showFilterPanel = not self._showFilterPanel
end

function Ark:hideFilterPanel()
    return not self._showFilterPanel
end

function Ark:filterPanelIcon()
    if self._showFilterPanel then return "funnel-fill" end
    -- Show filled funnel if any filter is active
    if self.filterFiles ~= "" or self.excludeFiles ~= ""
        or self.filterContent ~= "" or self.excludeContent ~= "" then
        return "funnel-fill"
    end
    return "funnel"
end

function Ark:hasActiveFilters()
    return self.filterFiles ~= "" or self.excludeFiles ~= ""
        or self.filterContent ~= "" or self.excludeContent ~= ""
end

-- Hits per file
function Ark:hitsPerFileText()
    if self._hitsPerFile == "0" then return "all" end
    return self._hitsPerFile
end

function Ark:cycleHitsPerFile()
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
    if ark._filterClicked then
        ark._filterClicked = false
        return
    end
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
    ark:loadConfig()

    -- Refresh tree if loaded
    if self._loaded then
        self._loaded = false
        self:loadRootNodes()
    end
end

function Source:patternError()
    return self._patternError or ""
end

function Source:hasPatternError()
    return (self._patternError or "") ~= ""
end

------------------------------------------------------------------------
-- Project methods
------------------------------------------------------------------------

function Project:selectMe()
    if ark._filterClicked then
        ark._filterClicked = false
        return
    end
    local source = self._fileSource or self._claudeSource
    if source then
        ark:selectSource(source)
    end
end

function Project:isSelected()
    if not ark.selectedSource then return false end
    return ark.selectedSource == self._fileSource or ark.selectedSource == self._claudeSource
end

function Project:toggleFiles()
    ark._filterClicked = true
    if self._hasFiles then
        self.filesOn = not self.filesOn
        ark:refilterSearch()
    end
end

function Project:toggleMemory()
    ark._filterClicked = true
    if self._hasMemory then
        self.memoryOn = not self.memoryOn
        ark:refilterSearch()
    end
end

function Project:toggleChats()
    ark._filterClicked = true
    if self._hasChats then
        self.chatsOn = not self.chatsOn
        ark:refilterSearch()
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
    if self._fileSource then
        arkCmd('config remove-source "' .. self._fileSource.dir .. '"')
    end
    if self._claudeSource then
        arkCmd('config remove-source "' .. self._claudeSource.dir .. '"')
    end
    if ark.selectedSource == self._fileSource or ark.selectedSource == self._claudeSource then
        ark.selectedSource = nil
    end
    ark:loadConfig()
end

------------------------------------------------------------------------
-- DataSource methods
------------------------------------------------------------------------

function DataSource:selectMe()
    if ark._filterClicked then
        ark._filterClicked = false
        return
    end
    if self._source then
        ark:selectSource(self._source)
    end
end

function DataSource:isSelected()
    return ark.selectedSource == self._source
end

function DataSource:toggleData()
    ark._filterClicked = true
    self.dataOn = not self.dataOn
    ark:refilterSearch()
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
    if ark.selectedSource == self._source then
        ark.selectedSource = nil
    end
    ark:loadConfig()
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
