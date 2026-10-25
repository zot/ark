# Test Design: tag-value path resolution under the excludes
**Source:** crc-Librarian.md

Found by an injection past #165's alarm list (2026-10-25): disabling either
check in `resolveTagValuePaths` left the whole suite green.

## Test: a tag-value lookup honors the fence and the noise filter
**Purpose:** fenced paths never resolve; noise paths drop (R3585)
**Input:** setupRecall; one `cuisine: thai` chunk in each of fenced/, noise/, open/; system_exclude naming fenced/, search_exclude naming noise/
**Expected:** only the open/ path
**Refs:** crc-Librarian.md
**Code:** tagvalue_paths_test.go
**Fire alarm:** skip the fence check (`matchesAnyGlob(path, fence) && false`); red names the fenced path
**Inject:** librarian.go:Librarian.resolveTagValuePaths
**Pulled:** 2026-10-25 — rang

## Test: a routed value keeps a noise path
**Purpose:** a value a mirror routed onto a noise-covered file keeps its path (R3587)
**Input:** setupFileBackedRecall; a target file named by search_exclude; a mirror file (extMirrorSource) with `@ext: <target> @note: hello`
**Expected:** `resolveTagValuePaths(note, hello)` contains the target
**Refs:** crc-Librarian.md, crc-ExtMap.md
**Code:** tagvalue_paths_test.go
**Fire alarm:** ignore the routed set (`(routedFiles[fid] || true) && …`); red, the target is missing
**Inject:** librarian.go:Librarian.resolveTagValuePaths
**Pulled:** 2026-10-25 — rang
