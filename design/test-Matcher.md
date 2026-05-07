# Test Design: Matcher
**Source:** crc-Matcher.md

## Test: file pattern matches file only
**Purpose:** Pattern "readme" matches file, not directory
**Input:** pattern="readme", path="readme" (file), path="readme" (dir)
**Expected:** file matches, directory does not
**Refs:** crc-Matcher.md, R16

## Test: directory pattern matches directory only
**Purpose:** Pattern "vendor/" matches directory, not file
**Input:** pattern="vendor/", path="vendor" (file), path="vendor" (dir)
**Expected:** directory matches, file does not
**Refs:** crc-Matcher.md, R16

## Test: single star matches within component
**Purpose:** Pattern "docs/*" matches docs/readme.md but not docs/api/spec.md
**Input:** pattern="docs/*", paths=["docs/readme.md", "docs/api/spec.md"]
**Expected:** first matches, second does not
**Refs:** crc-Matcher.md, R17

## Test: doublestar matches any depth
**Purpose:** Pattern "src/**" matches at any depth under src
**Input:** pattern="src/**", paths=["src/main.go", "src/pkg/db/store.go"]
**Expected:** both match
**Refs:** crc-Matcher.md, R17

## Test: doublestar with extension
**Purpose:** Pattern "**/*.md" matches .md files at any depth
**Input:** pattern="**/*.md", paths=["readme.md", "docs/guide.md", "a/b/c/notes.md"]
**Expected:** all match
**Refs:** crc-Matcher.md, R17

## Test: doublestar mid-pattern
**Purpose:** Pattern "docs/**/*.txt" matches .txt under docs/ at any depth
**Input:** pattern="docs/**/*.txt", paths=["docs/a.txt", "docs/sub/b.txt", "other/c.txt"]
**Expected:** first two match, third does not
**Refs:** crc-Matcher.md, R17

## Test: alternation braces
**Purpose:** Pattern "*.{md,txt}" matches both extensions
**Input:** pattern="*.{md,txt}", paths=["readme.md", "notes.txt", "data.csv"]
**Expected:** first two match, third does not
**Refs:** crc-Matcher.md, R19

## Test: dotfiles match by default
**Purpose:** * matches dotfiles when dotfiles=true
**Input:** pattern="*", path=".gitignore", dotfiles=true
**Expected:** matches
**Refs:** crc-Matcher.md, R18

## Test: dotfiles excluded when disabled
**Purpose:** * does not match dotfiles when dotfiles=false
**Input:** pattern="*", path=".gitignore", dotfiles=false
**Expected:** does not match
**Refs:** crc-Matcher.md, R18

## Test: source-anchored ./pattern only matches at source root
**Purpose:** Pattern "./vendor/" only matches at source-directory root
**Input:** pattern="./vendor/", sourceDir="/proj", absPaths=["/proj/vendor", "/proj/pkg/vendor"]
**Expected:** root matches, nested does not
**Refs:** crc-Matcher.md, R2133

## Test: bare pattern matches at any depth in source
**Purpose:** Pattern "node_modules/" matches at any depth in source
**Input:** pattern="node_modules/", sourceDir="/proj", absPaths=["/proj/node_modules", "/proj/pkg/node_modules"]
**Expected:** both match
**Refs:** crc-Matcher.md, R2133

## Test: filesystem-absolute pattern matches by absolute path
**Purpose:** Pattern "/tmp/**" matches any file under /tmp regardless of source
**Input:** pattern="/tmp/**", sourceDir="/home/me/proj", absPaths=["/tmp/foo", "/home/me/proj/tmp/foo"]
**Expected:** first matches (under /tmp); second does not (under source's tmp/, but pattern is filesystem-rooted)
**Refs:** crc-Matcher.md, R2133

## Test: filesystem-absolute pattern unrelated to source is a no-op
**Purpose:** Pattern "/var/log/**" with source elsewhere matches nothing in that source
**Input:** pattern="/var/log/**", sourceDir="/home/me/proj", absPath="/home/me/proj/var/log/x"
**Expected:** does not match (pattern's prefix does not contain the file's abs path)
**Refs:** crc-Matcher.md, R2133

## Test: include wins over exclude
**Purpose:** Classify returns included when both match
**Input:** includes=["*.md"], excludes=["*.md", "*.log"], path="readme.md"
**Expected:** classified as included
**Refs:** crc-Matcher.md, R10

## Test: unresolved when nothing matches
**Purpose:** Classify returns unresolved for no-match files
**Input:** includes=["*.md"], excludes=["*.log"], path="data.csv"
**Expected:** classified as unresolved
**Refs:** crc-Matcher.md, R15

## Test: glob wildcards
**Purpose:** ? and [abc] wildcards work
**Input:** pattern="file?.txt", paths=["file1.txt", "file12.txt"]
**Expected:** first matches, second does not
**Refs:** crc-Matcher.md, R19

## Test: backslash escapes
**Purpose:** \* matches literal asterisk in filename
**Input:** pattern="file\\*name", path="file*name"
**Expected:** matches
**Refs:** crc-Matcher.md, R20
