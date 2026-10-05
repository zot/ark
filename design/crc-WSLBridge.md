# WSLBridge
**Requirements:** R3494, R3495, R3496, R3497, R3498, R3499, R3500, R3501, R3502, R3503, R3504, R3505, R3506, R3507, R3508, R3509, R3510, R3511, R3512, R3513, R3514

The client-side bridge that lets the Windows `ark.exe` serve a Claude Code
session running inside WSL2 (specs/wsl.md). It decides whether this process was
launched from WSL, translates path arguments Linux to Windows, filters output
Windows to Linux, and prints the `wsl-init` script that sets up the Linux side.
The server is untouched: every responsibility here runs in the CLI process before
or around the command tree (R3494).

## Knows
- wslMounts: the drive-to-mount-point map; nil means the `/mnt/<letter>` default (R3498)
- env names: `ARK_WSL` (override), `ARK_WSL_MOUNTS` (map), `ARK_WSL_CHILD` (no-recursion marker)
- exempt command paths: `luhmann attach`, `nano`, `serve`, `wsl-init` (R3506)
- linuxTopDirs: the standard Linux top-level directory names the WSL-native rule tests (R3500)
- the shim text, including the `/proc/mounts` parser shared with its test (R3510)

## Does
- wslModeOn(goos, getenv, parentName): the pure detection decision: off off Windows, off when
  `ARK_WSL_CHILD` is set, `ARK_WSL` 1/0 forces, else parent image `wsl.exe` case-insensitively (R3494-R3496)
- parentExeName: Windows-only toolhelp snapshot lookup of the parent's image name; "" elsewhere (R3495)
- parseWSLMounts(s): parse `ARK_WSL_MOUNTS`, error naming a bad entry (R3498)
- toWindows(linuxPath): longest mount point match → `X:\...` (R3499)
- winPathToLinux(winPath): `X:\rest` → mount point + `/rest`; false for a drive the map lacks.
  The one Windows-to-Linux function, used by the output filter and by wsl-init (R3501, R3514)
- translateWSLArgs(args, mounts): rewrite every path argument and `-flag=value` value, or
  return the WSL-native-path error (R3499, R3500)
- translateWinPaths(b, final, prev, mounts): the streaming scanner: start rule, end rule,
  escaped form, and the incomplete-candidate hold (R3501-R3504)
- wslFilter: an io.Writer over a downstream writer that runs the scanner, keeps the held tail
  and the previous byte, bounds the hold, and flushes on demand (R3504)
- wslPrepare(args): main's entry: detect, scrub the environment and set the child marker,
  translate arguments, run exempt commands in-process, otherwise re-exec filtered (R3497, R3505, R3506)
- runWSLFiltered(exe, args, stdin, stdout, stderr, mounts): run the child with stdin passed
  through and both outputs filtered; ignore interrupts; return the child's exit code (R3505)
- wslInitScript(winArkDir, winExe, entries, links, mounts): build the `wsl-init` script text:
  subshell, `~/.ark` guard, the shim heredoc, entry links, `.claude` links (R3508-R3514)
- cmdWSLInit: refuse off Windows; gather the ark directory's entries, the installed exe, and
  `installLinkSet`; print the script (R3507)

## Collaborators
- CLI: `installLinkSet` is the one link list shared with `installLinks` (R3512); main calls wslPrepare first
- CLITree: the `wsl-init` command node

## Sequences
- seq-wsl.md
