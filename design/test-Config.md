# Test Design: Config
**Source:** crc-Config.md

## Test: load valid config
**Purpose:** Parse a well-formed ark.toml with global and per-source patterns
**Input:** TOML with dotfiles=true, global include/exclude, two [[source]] entries
**Expected:** Config struct populated, sources have correct strategy, no errors
**Refs:** crc-Config.md

## Test: per-source patterns are additive
**Purpose:** Verify per-source include/exclude combine with global, not replace
**Input:** Global include ["*.md"], source include ["*.txt"]
**Expected:** EffectivePatterns returns ["*.md", "*.txt"] for that source
**Refs:** crc-Config.md

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
