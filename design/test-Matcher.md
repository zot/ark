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
**Purpose:** Pattern `docs/*` matches docs/readme.md but not docs/api/spec.md
**Input:** pattern=`docs/*`, paths=["docs/readme.md", "docs/api/spec.md"]
**Expected:** first matches, second does not
**Refs:** crc-Matcher.md, R17

## Test: doublestar matches any depth
**Purpose:** Pattern `src/**` matches at any depth under src
**Input:** pattern=`src/**`, paths=["src/main.go", "src/pkg/db/store.go"]
**Expected:** both match
**Refs:** crc-Matcher.md, R17

## Test: doublestar with extension
**Purpose:** Pattern `**/*.md` matches .md files at any depth
**Input:** pattern=`**/*.md`, paths=["readme.md", "docs/guide.md", "a/b/c/notes.md"]
**Expected:** all match
**Refs:** crc-Matcher.md, R17

## Test: doublestar mid-pattern
**Purpose:** Pattern `docs/**/*.txt` matches .txt under docs/ at any depth
**Input:** pattern=`docs/**/*.txt`, paths=["docs/a.txt", "docs/sub/b.txt", "other/c.txt"]
**Expected:** first two match, third does not
**Refs:** crc-Matcher.md, R17

## Test: alternation braces
**Purpose:** Pattern `*.{md,txt}` matches both extensions
**Input:** pattern=`*.{md,txt}`, paths=["readme.md", "notes.txt", "data.csv"]
**Expected:** first two match, third does not
**Refs:** crc-Matcher.md, R19

## Test: dotfiles match by default
**Purpose:** `*` matches dotfiles when dotfiles=true
**Input:** pattern=`*`, path=".gitignore", dotfiles=true
**Expected:** matches
**Refs:** crc-Matcher.md, R18

## Test: dotfiles excluded when disabled
**Purpose:** `*` does not match dotfiles when dotfiles=false
**Input:** pattern=`*`, path=".gitignore", dotfiles=false
**Expected:** does not match
**Refs:** crc-Matcher.md, R18

## Test: ./pattern only matches at the contextual root
**Purpose:** Pattern "./vendor/" only matches at the root it is anchored to
**Input:** pattern="./vendor/", sourceDir="/proj", absPaths=["/proj/vendor", "/proj/pkg/vendor"]
**Expected:** root matches, nested does not
**Refs:** crc-Matcher.md, R3196

## Test: bare pattern matches at any depth below the contextual root
**Purpose:** Pattern `node_modules/` matches at any depth in the source-scoped context
**Input:** pattern=`node_modules/`, sourceDir="/proj", absPaths=[`/proj/node_modules`, `/proj/pkg/node_modules`]
**Expected:** both match
**Refs:** crc-Matcher.md, R3196, R3198

## Test: filesystem-absolute pattern matches by absolute path
**Purpose:** Pattern `/tmp/**` matches any file under /tmp regardless of source
**Input:** pattern=`/tmp/**`, sourceDir="/home/me/proj", absPaths=["/tmp/foo", "/home/me/proj/tmp/foo"]
**Expected:** first matches (under /tmp); second does not (under source's tmp/, but pattern is filesystem-rooted)
**Refs:** crc-Matcher.md, R3196

## Test: filesystem-absolute pattern unrelated to source is a no-op
**Purpose:** Pattern `/var/log/**` with source elsewhere matches nothing in that source
**Input:** pattern=`/var/log/**`, sourceDir="/home/me/proj", absPath="/home/me/proj/var/log/x"
**Expected:** does not match (pattern's prefix does not contain the file's abs path)
**Refs:** crc-Matcher.md, R3196

## Test: include wins over exclude
**Purpose:** Classify returns included when both match
**Input:** includes=[`*.md`], excludes=[`*.md`, `*.log`], path="readme.md"
**Expected:** classified as included
**Refs:** crc-Matcher.md, R10

## Test: unresolved when nothing matches
**Purpose:** Classify returns unresolved for no-match files
**Input:** includes=[`*.md`], excludes=[`*.log`], path="data.csv"
**Expected:** classified as unresolved
**Refs:** crc-Matcher.md, R15

## Test: glob wildcards
**Purpose:** ? and [abc] wildcards work
**Input:** pattern="file?.txt", paths=["file1.txt", "file12.txt"]
**Expected:** first matches, second does not
**Refs:** crc-Matcher.md, R19

## Test: backslash escapes
**Purpose:** `\*` matches literal asterisk in filename, off Windows (on Windows a
backslash is a separator)
**Input:** pattern=`file\*name`, path=`file*name`
**Expected:** matches; skipped on Windows
**Refs:** crc-Matcher.md, R3632

## Test: bracket escapes
**Purpose:** `[*]` matches a literal asterisk on every platform
**Input:** pattern=`file[*]name`, paths `file*name` and "fileXname"
**Expected:** the first matches, the second does not
**Refs:** crc-Matcher.md, R3632

## Test: rootless context — bare pattern reaches any depth
**Purpose:** With no contextual root, a bare pattern means `**/X` — the
`ark.toml` reading, where there is nowhere to stand
**Input:** pattern=`*.md`, sourceDir="", absPaths=["/a/x.md", "/a/b/c/y.md"]
**Expected:** both match
**Refs:** crc-Matcher.md, R3199

## Test: rootless context — slash-bearing relative pattern matches
**Purpose:** The regression O160 named: `specs/**` in a rootless key must
match, not silently match nothing. Pins the retirement of the basename-first
`pathMatchesGlob` and pubsub's `anchorGlob`, both of which prefixed `**/`
only when the pattern had no slash.
**Input:** pattern=`specs/**`, sourceDir="", absPath="/home/me/proj/specs/x.md"
**Expected:** matches
**Refs:** crc-Matcher.md, R3195, R3199, R3207

## Test: rootless ./ falls back to the absolute path
**Purpose:** A rootless `./X` has no root to anchor to; documented as a
degradation, not a fix — the reason `[schedule]` needs absolute paths to
name a directory inside one source
**Input:** pattern=`./specs/**`, sourceDir="", absPath="/home/me/proj/specs/x.md"
**Expected:** does not match (the pattern is tested against the absolute path)
**Refs:** crc-Matcher.md, R3199

## Test: CLI context — a bare glob is top-level-only after anchoring
**Purpose:** **The one intentional behavior change of #51**, pinned so it
reads as decided rather than accidental. `-files '*.go'` anchors to
`$PWD/*.go` and therefore stops matching nested files; `/**/*` is the
explicit any-depth form.
**Input:** `AnchorGlobToDir("*.go", "/proj")` → pattern; sourceDir="",
absPaths=["/proj/main.go", "/proj/pkg/db.go"]
**Expected:** "/proj/main.go" matches, "/proj/pkg/db.go" does not.
With glob `/**/*.go` anchoring passes it through and both match.
**Refs:** crc-Matcher.md, crc-Searcher.md, R3197, R3196

## Test: separators on Windows
**Purpose:** R3633, R3634. Measured 2026-10-26: on Windows every pattern holding
a separator failed, so a nested `node_modules/` was not excluded.
**Input:** source `C:\t172\src`; `docs/*.md`, `docs\*.md`, `./docs/*.md`,
`node_modules/` at depth, a backslash absolute path, a slash absolute glob, and
negative cases. Runs on Windows (`match_windows_test.go`, on the VM).
**Expected:** each as a Unix user would expect of the same pattern.
**Refs:** crc-Matcher.md
**Code:** match_windows_test.go
**Fire alarm:** drop the slash conversion of the matched path; the separator
cases fail
**Inject:** match.go:Matcher.Match
**Pulled:** 2026-10-26 (windows-gpu) — rang: TestMatchSeparatorsOnWindows fails, and so does the older TestMatchBareMatchesAnyDepth (`bare node_modules/ should match nested`), which would have caught this had the suite ever run on Windows

## Test: embed-priority globs on Windows
**Purpose:** R3633, the librarian's matcher.
**Input:** `**/docs/*.md` against `C:\t172\src\docs\Casey.md`.
**Expected:** matches.
**Refs:** crc-Matcher.md
**Code:** match_windows_test.go
**Fire alarm:** match the raw backslash path; red names the pattern
**Inject:** librarian.go:matchesAnyGlob
**Pulled:** 2026-10-26 (windows-gpu) — rang: "`**/docs/*.md` should match `C:\t172\src\docs\Casey.md`"
