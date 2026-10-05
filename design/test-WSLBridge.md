# Test Design: WSLBridge
**Source:** crc-WSLBridge.md

Pure functions with injected inputs: no Windows, no server. The invisible-when-violated
properties here are a filter that silently drops or mistranslates a path split across two
writes, a native-path guard that eats ordinary arguments, and a wsl-init link set that drifts
from setup's. Each reads as working until a WSL user hits it.

## Test: detection decision
**Purpose:** WSL mode follows the parent name and the overrides, and is off off Windows (R3494, R3495, R3496)
**Input:** wslModeOn with goos windows/linux, parent wsl.exe / WSL.EXE / explorer.exe, ARK_WSL 1/0, ARK_WSL_CHILD set
**Expected:** on for wsl.exe and WSL.EXE; off for explorer.exe; ARK_WSL=1 on with explorer; ARK_WSL=0 off with wsl.exe; child marker off even with ARK_WSL=1; linux off even with ARK_WSL=1
**Refs:** crc-WSLBridge.md, seq-wsl.md#1.2
**Code:** cmd/ark/wsl_test.go
**Fire alarm:** compare the parent name case-sensitively; the WSL.EXE case goes red
**Inject:** cmd/ark/wsl.go:wslModeOn
**Pulled:** 2026-10-05 — rang: TestWSLModeOn `parent WSL.EXE: got false, want true`

## Test: mount map parsing
**Purpose:** ARK_WSL_MOUNTS parses to the map, rejects malformed entries, empty means default (R3498)
**Input:** "C:=/mnt/c;D:=/mnt/d", "", "c:=/c;", "C=/mnt/c", "C:=mnt/c", "CC:=/x"
**Expected:** two entries upper-cased; nil for empty; trailing ; tolerated; errors for the malformed three naming the entry
**Refs:** crc-WSLBridge.md, seq-wsl.md#1.3
**Code:** cmd/ark/wsl_test.go

## Test: argument translation
**Purpose:** paths under a mount become Windows paths; non-paths are untouched; WSL-native paths are refused (R3499, R3500)
**Input:** `/mnt/c/x`, `--files=/mnt/d/a b`, `/mnt/c`, `search`, `/api/v1`, `https://x`, `/home`, `/home/zot/p`, `--dir=/tmp/x`, and with a custom map `C:=/c` the arg `/c/u`
**Expected:** `C:\x`, `--files=D:\a b`, `C:\`, unchanged non-paths, `/home/zot/p` and `--dir=/tmp/x` return the native-path error, `/c/u` → `C:\u`
**Refs:** crc-WSLBridge.md, seq-wsl.md#1.5
**Code:** cmd/ark/wsl_test.go
**Fire alarm:** drop the native-path check so `/home/zot/p` passes through untouched; the error case goes red
**Inject:** cmd/ark/wsl.go:translateWSLArg
**Pulled:** 2026-10-05 — rang: TestTranslateWSLArgs `/home/zot/p: no error` (and `--dir=/tmp/x`, `/mnt/wsl/x`, the custom-map case)

## Test: output filter rewrites
**Purpose:** plain and JSON-escaped drive paths are rewritten; spaces follow the continuation rule; non-paths and URLs are untouched (R3501, R3502, R3503)
**Input:** lines with `C:\Users\zot\a.md:10`, `"C:\\Users\\zot"`, `C:\Program Files\ark\x.md`, `C:\a and D:\b`, `C:\a;D:\b`, `https://x`, `abc:\x`, `E:\x` with a map lacking E, `"C:\\a\u00e9\\b"`
**Expected:** each rewritten per the rule; untouched text byte-identical
**Refs:** crc-WSLBridge.md, seq-wsl.md#1.9
**Code:** cmd/ark/wsl_test.go
**Fire alarm:** treat a space as an unconditional terminator; the `Program Files` case keeps `ark\x.md` and goes red
**Inject:** cmd/ark/wsl.go:scanWinPath
**Pulled:** 2026-10-05 — rang: TestTranslateWinPaths `program files: got "see /mnt/c/Program Files\\ark\\x.md now"` and the JSON variant

## Test: output filter across write boundaries
**Purpose:** a path split at every byte position between two writes translates the same as one write (R3504)
**Input:** the plain and escaped samples, written to a wslFilter in two pieces split at each offset, then Flush
**Expected:** the output equals the single-write translation for every split
**Refs:** crc-WSLBridge.md, seq-wsl.md#1.9
**Code:** cmd/ark/wsl_test.go
**Fire alarm:** make the scanner treat end of buffer as final (never hold); splits inside `C:\` or a body go red
**Inject:** cmd/ark/wsl.go:wslFilter.Write
**Pulled:** 2026-10-05 — rang: TestWSLFilterSplits split mismatch on every sample (first at offset 1) and `unfinished path not held`

## Test: re-exec wiring
**Purpose:** the child's stdout and stderr are filtered, stdin passes through, and the exit code is the child's (R3505)
**Input:** runWSLFiltered with exe /bin/sh, a script printing a plain path to stdout, an escaped path to stderr, echoing stdin, and exiting 3
**Expected:** translated stdout including the stdin text, translated stderr, return 3
**Refs:** crc-WSLBridge.md, seq-wsl.md#1.7, seq-wsl.md#1.10
**Code:** cmd/ark/wsl_test.go
**Fire alarm:** return 0 instead of the child's exit code; the test goes red on code 3
**Inject:** cmd/ark/wsl.go:runWSLFiltered
**Pulled:** 2026-10-05 — rang: TestRunWSLFiltered `exit code 0, want 3`

## Test: exempt commands
**Purpose:** luhmann attach, nano, serve and wsl-init are exempt, found past global flags; others are not (R3506)
**Input:** wslExempt over `luhmann attach`, `--dir C:\x luhmann attach`, `-v nano`, `serve --no-scan`, `wsl-init`, `luhmann status`, `search x`
**Expected:** first five exempt, last two not
**Refs:** crc-WSLBridge.md, seq-wsl.md#1.6
**Code:** cmd/ark/wsl_test.go
**Fire alarm:** drop `serve` from the exempt list; `serve --no-scan` reports not exempt and the test goes red
**Inject:** cmd/ark/wsl.go:wslExemptPaths
**Pulled:** 2026-10-05 — rang: TestWSLExempt `"serve --no-scan": got false, want true`

## Test: wsl-init script content and link set
**Purpose:** the script carries the shim, skips ark.exe and `ark`, and its .claude links equal the links installLinks really makes (R3509, R3511, R3512, R3514)
**Input:** a fake ark dir with skills/a, skills/b, agents/; run installLinks into a temp .claude and collect its link names; build the script from installLinkSet with the Windows-style ark dir `C:\Users\zot\.ark`
**Expected:** the shim execs `/mnt/c/Users/zot/.ark/ark.exe`; no ark_link for ark.exe or ark; the `.claude` link names in the script equal the set installLinks created
**Refs:** crc-WSLBridge.md, crc-CLI.md, seq-wsl.md#2.4
**Code:** cmd/ark/wsl_test.go
**Fire alarm:** make wslInitScript skip the agents link; the set comparison goes red
**Inject:** cmd/ark/wsl.go:wslInitScript
**Pulled:** 2026-10-05 — rang: TestWSLInitScriptLinkSet `script links [skills/a skills/b], installLinks made [agents/ark skills/a skills/b]`

## Test: wsl-init script runs under sh
**Purpose:** the script is valid sh, creates the shim and links in a temp HOME, is idempotent, and skips a real file with a warning (R3508, R3509, R3511, R3513)
**Input:** `sh -n` on the script; then run it with HOME=temp twice, with a pre-existing real file at one entry link name
**Expected:** `sh -n` passes; `~/.ark/ark` is executable and holds the marker; entry links are symlinks to /mnt paths; the real file is untouched and a warning names it; second run succeeds with the same result
**Refs:** crc-WSLBridge.md, seq-wsl.md#2.6
**Code:** cmd/ark/wsl_test.go
**Fire alarm:** let ark_link replace anything (`rm -rf` then link); the pre-existing real file is replaced and the test goes red
**Inject:** cmd/ark/wsl.go:wslInitPrelude
**Pulled:** 2026-10-05 — rang: TestWSLInitScriptRuns `no warning for the real file` and `real file at .../.ark/html was replaced`

## Test: shim mount parser under sh
**Purpose:** the shim's /proc/mounts parser yields the map ARK_WSL_MOUNTS expects, from a fixture with drvfs and non-drvfs lines (R3510, R3498)
**Input:** the shim's ark_mounts function run by sh on a fixture holding `C:\134 /mnt/c 9p rw,noatime,aname=drvfs;path=C:\;uid=1000 0 0`, a D: line, and a non-drvfs 9p line
**Expected:** `C:=/mnt/c;D:=/mnt/d`, which parseWSLMounts accepts
**Refs:** crc-WSLBridge.md, seq-wsl.md#1.1
**Code:** cmd/ark/wsl_test.go
**Fire alarm:** drop the aname=drvfs filter in the shim parser; the non-drvfs line leaks into the map and the test goes red
**Inject:** cmd/ark/wsl.go:wslMountsShFunc
**Pulled:** 2026-10-05 — rang: TestShimMountParser `got "C:=/mnt/c;X:=/mnt/x;D:=/mnt/d"`

## Test: a drive letter inside a word is not a path start
**Purpose:** `abc:\x` and `yC:\z` are left alone; only a letter not preceded by a letter, digit or underscore starts a path (R3502)
**Input:** the "word before" case of TestTranslateWinPaths
**Expected:** the text is byte-identical
**Refs:** crc-WSLBridge.md
**Code:** cmd/ark/wsl_test.go
**Fire alarm:** disable the preceding-byte check in driveAt; `abc:\x` becomes `ab/mnt/c/x`
**Inject:** cmd/ark/wsl.go:driveAt
**Pulled:** 2026-10-05 — rang: TestTranslateWinPaths `word before: got "ab/mnt/c/x y/mnt/c/z"`

## Test: wsl-init rewrites its own stale shim
**Purpose:** re-running wsl-init updates a `~/.ark/ark` that carries the shim marker (a moved ark.exe reaches the shim) while a foreign file stays (R3513)
**Input:** after a run, overwrite the shim with a marker-bearing one that execs `/old/ark.exe`; run the script again
**Expected:** the shim now execs `/mnt/c/Users/zot/.ark/ark.exe`
**Refs:** crc-WSLBridge.md, seq-wsl.md#2.6
**Code:** cmd/ark/wsl_test.go
**Fire alarm:** drop the `grep -qx` marker clause from the shim guard, so an existing shim is always skipped; found by a past-the-list probe that stayed green before this case was added
**Inject:** cmd/ark/wsl.go:wslInitScript
**Pulled:** 2026-10-05 — rang: TestWSLInitScriptRuns `a stale shim carrying the marker was not rewritten`
