# Test Design: Config
**Source:** crc-Config.md

## Test: load valid config
**Purpose:** Parse a well-formed ark.toml with global and per-source patterns
**Input:** TOML with dotfiles=true, global include/exclude, two [[source]] entries
**Expected:** Config struct populated, sources have correct strategy, no errors
**Refs:** crc-Config.md

## Test: per-source include replaces default
**Purpose:** R2143, R2144 — per-source include patterns replace default_include for that source
**Input:** default_include ["*.md", "*.go"], source.Include ["*.txt"]
**Expected:** EffectivePatterns returns ["*.txt"]
**Refs:** crc-Config.md, R2143, R2144

## Test: per-source omitted inherits default
**Purpose:** R2144 — when a source omits `include`, default_include applies; setting only `exclude` preserves default_include
**Input:** default_include ["*.md", "*.go"], source.Exclude ["drafts/"], source.Include unset
**Expected:** EffectivePatterns returns includes=["*.md", "*.go"], excludes=["drafts/"]
**Refs:** crc-Config.md, R2143, R2144

## Test: per-source include.add extends default
**Purpose:** R2146 — `include.add = [...]` appends to default_include rather than replacing
**Input:** default_include ["*.md", "*.go"], source written as `include.add = ["*.lua"]`
**Expected:** EffectivePatterns returns includes=["*.md", "*.go", "*.lua"]
**Refs:** crc-Config.md, R2146

## Test: per-source exclude.add extends default
**Purpose:** R2146 — `exclude.add = [...]` appends to default_exclude rather than replacing
**Input:** default_exclude [".git/"], source written as `exclude.add = ["drafts/"]`
**Expected:** EffectivePatterns returns excludes=[".git/", "drafts/"]
**Refs:** crc-Config.md, R2146

## Test: identical include exclude is error
**Purpose:** Config validation catches identical include and exclude strings
**Input:** include ["*.md"], exclude ["*.md"]
**Expected:** Validate returns error, HasErrors() true
**Refs:** crc-Config.md, R11

## Test: write default config
**Purpose:** WriteDefault creates ark.toml with default excludes
**Input:** Empty directory path
**Expected:** File created with .git/, .env in exclude, dotfiles=true
**Refs:** crc-Config.md, R22

## Test: missing source dir not an error
**Purpose:** Config loads even if a source directory doesn't exist yet
**Input:** TOML with dir pointing to nonexistent path
**Expected:** Config loads successfully (runtime check during scan, not load)
**Refs:** crc-Config.md

## Test: add-include per-source round-trip
**Purpose:** AddInclude with sourceDir persists through SaveConfig/LoadConfig
**Input:** Config with a source, AddInclude("*.org", sourceDir), save, reload
**Expected:** Reloaded config has "*.org" in that source's Include list
**Refs:** crc-Config.md, R235

## Test: reorderArgs puts flags before positional args
**Purpose:** CLI helper ensures Go flag parsing sees all flags
**Input:** ["*.md", "--source", "/path/to/dir"]
**Expected:** Reordered to ["--source", "/path/to/dir", "*.md"]
**Refs:** crc-CLI.md, R232, R233

