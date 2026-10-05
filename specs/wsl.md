# ark from inside WSL2

**Language and environment:** Go, the `cmd/ark` CLI. Windows-only behavior,
tested on Linux through pure functions with injected inputs.

## The scenario

Windows users who run Claude Code inside WSL2 keep their projects on NTFS
(`/mnt/c/...`), since the Linux image is close to disposable. The ark server runs
natively on Windows, beside the projects. Claude, on the Linux side, runs the ark
CLI as the Windows binary `ark.exe` through WSL interop, and `ark.exe` reaches the
server over its AF_UNIX socket as any Windows process does. There is one binary:
no Linux build, no TCP transport.

Two things do not line up on their own. WSL translates the working directory
(`/mnt/c/Users/zot` arrives as `C:\Users\zot`) but not the arguments, and the
Windows paths ark prints (`C:\Users\zot\notes.md`) are not paths Linux tools can
open. So `ark.exe` translates both, **in the client only**: the server never knows
WSL exists.

## WSL mode

`ark.exe` is in **WSL mode** when it was launched from WSL. Off Windows it is
never in WSL mode.

- **Detection.** A Windows process launched through interop has `wsl.exe` as its
  parent process, both for `wsl.exe -e ...` sessions and for an interactive WSL
  terminal. WSL's own Linux variables (`WSL_DISTRO_NAME` and the like) do not reach
  a Windows process, and Windows Terminal's `WT_SESSION` appears in every tab, so
  the parent's image name is the signal: WSL mode when it is `wsl.exe`, compared
  case-insensitively.
- **Override.** `ARK_WSL=1` forces WSL mode on and `ARK_WSL=0` forces it off, for
  shims and tests. The `~/.ark/ark` shim (below) sets `ARK_WSL=1`, so a call through
  the shim never depends on the parent check.
- **No recursion, no leaking.** Once in WSL mode, `ark.exe` removes `ARK_WSL` and
  `ARK_WSL_MOUNTS` from its environment and sets `ARK_WSL_CHILD=1`, which forces WSL
  mode off. Its re-executed child and anything a WSL-started server later spawns
  (a hosted Claude session calling `ark.exe` natively, for instance) are therefore
  ordinary Windows processes.

## The mount map

Which Linux directory shows which Windows drive is WSL's business: `/mnt/<letter>`
by default, but `/etc/wsl.conf` can move the automount root, and a drive can be
mounted elsewhere. Inside WSL2 every drive mount is a `9p` mount whose options carry
`aname=drvfs` and `path=X:\`, with the mount point as `/proc/mounts` field 2.

- The shim reads `/proc/mounts` on every run and hands the result to `ark.exe` in
  `ARK_WSL_MOUNTS`, formatted `C:=/mnt/c;D:=/mnt/d` (drive, `:=`, mount point,
  entries separated by `;`). Both `ARK_WSL` and `ARK_WSL_MOUNTS` are listed in
  `WSLENV` with the `/u` flag (passed only from WSL to Windows) and no path flag,
  since the value is ark's own format.
- When `ARK_WSL_MOUNTS` is present it is authoritative for both directions. When it
  is absent (a direct `ark.exe` call that detected its `wsl.exe` parent), each drive
  maps to `/mnt/<lowercase letter>`.
- A malformed `ARK_WSL_MOUNTS` is an error naming the bad entry.

**Limit.** A mount point containing a space is written in `/proc/mounts` with an
octal escape (`\040`), and the shim passes it through unescaped, so such a mount
will not match. A mount point containing `;` would break the format.

## Arguments

Every argument, and the value part of every `--flag=value` argument, is
translated when it names a path under a mount point: `/mnt/c/Users/zot/proj`
becomes `C:\Users\zot\proj`, and `/mnt/c` alone becomes `C:\`. Separate flag
values are ordinary arguments and are translated the same way.

**WSL-native paths are an error for now.** A Linux path outside every drive mount,
such as `/home/zot/proj`, cannot be seen by Windows ark: reaching it would need the
distro name, which does not reach a Windows process. `ark.exe` refuses with a
message telling the user to keep projects under `/mnt/c`.

Because arguments are also search terms, regexes and Lua code, the rule for "is a
WSL-native path" is deliberately narrow: the argument starts with `/`, contains a
further `/`, and its first component is a standard Linux top-level directory
(`home`, `root`, `tmp`, `usr`, `opt`, `var`, `etc`, `srv`, `mnt`, `media`, `run`,
`proc`, `sys`, `dev`, `bin`, `sbin`, `lib`, `lib64`, `snap`, `boot`). So
`/home/zot/proj`, `/tmp/x` and `/mnt/wsl/x` are refused, while `/api/v1`, a regex
`/foo/`, and a bare `/home` pass through untouched. The trade-off: a search term that
happens to look like `/usr/lib` is refused, and a Linux path under a non-standard
top-level directory (`/data/proj`) slips through untranslated and fails later as an
unknown Windows path.

## Output

Translation of output is generic, not per command. In WSL mode `stdout` and
`stderr` pass through a filter that rewrites each Windows drive path to its Linux
form: `C:\Users\zot\a.md` becomes `/mnt/c/Users/zot/a.md`, and the JSON-escaped form
`C:\\Users\\zot` (as in `"C:\\Users\\zot"`) becomes `/mnt/c/Users/zot`, so JSON
output stays valid. A drive the mount map does not list is left as it is.

**Where a path starts.** A letter, a colon and a backslash, where the letter is not
preceded by a letter, digit or underscore. So `https://x` and `abc:\` are untouched.

**Where a path ends.** Only the backslashes inside a path change, so the end
matters only for which backslashes become slashes. A path ends at:

- a line end, any other control character, or one of `"` `'` `` ` `` `<` `>` `|`
  `*` `?` (none can appear in a Windows path);
- the start of another drive path (so `C:\a;D:\b` is two paths);
- in the escaped form, a JSON escape other than `\\` and `\uXXXX`;
- a space, **unless** the next space-separated word contains a backslash and is not
  itself a drive path. So `C:\Program Files\ark\x.md` is one path, and `C:\a and
  D:\b` is two.

**Limit.** A path whose last component holds a space (`C:\Users\zot\my notes.md`)
ends at the space, which is harmless because the rest has no backslash to convert.
A space followed by an unrelated word that contains a backslash (`C:\x then\y`)
over-extends the path, turning that backslash into a slash.

**Chunk boundaries.** Output arrives in arbitrary pieces, so a path can be split
between two writes. The filter holds back only an unfinished candidate path at the
end of what it has received, translates the rest at once, and flushes everything at
end of output. The hold is bounded (64 KiB); a longer run is translated as if final.

## How output is filtered

ark exits through `os.Exit` in many places, which skips deferred work, so an
in-process filter would lose buffered output. Instead, when WSL mode is detected,
`ark.exe` re-executes itself as a child with the translated arguments and
`ARK_WSL_CHILD=1`, passes its own standard input straight to the child, copies the
child's `stdout` and `stderr` through the filter, ignores interrupts while the
child runs (the child decides how to stop), and exits with the child's exit code.

Commands that need the real console, or whose output is not a client's, run
directly in WSL mode with translated arguments and no filter:

- `luhmann attach` (raw-mode terminal),
- `nano` (interactive REPL that probes its terminal),
- `serve` (the server, not a client),
- `wsl-init` (its output is already a Linux script).

**Limit.** `stdout` and `stderr` are separate pipes in the child, so their
relative order on a shared terminal is not preserved, and a command probing whether
its output is a terminal sees a pipe.

## `ark wsl-init`

Run inside WSL as

```
eval "$(/mnt/c/Users/<you>/.ark/ark.exe wsl-init)"
```

`wsl-init` **prints** a POSIX `sh` script and creates nothing itself: a Windows
process cannot make Linux symlinks, and `wsl.exe` started from Windows reaches only
the default distro. Printing works whether or not WSL mode was detected; on a
non-Windows build `wsl-init` fails, saying it is meant for the Windows `ark.exe`.

The script, safe to re-run:

- runs in a subshell, so an early stop never ends the user's shell;
- creates `~/.ark/` as a real directory in the Linux home, and stops with a warning
  if `~/.ark` is a symlink or not a directory;
- writes `~/.ark/ark`, an executable `sh` shim that reads the mount map, exports
  `ARK_WSL=1` and `ARK_WSL_MOUNTS` through `WSLENV`, and `exec`s the Windows
  `ark.exe` (the installed copy in the ark directory when present, else the running
  executable) with `"$@"`;
- symlinks every other entry of the Windows `.ark` directory into `~/.ark/`, except
  `ark.exe` itself and anything that would clash with the shim;
- links `~/.claude/skills/<name>` and `~/.claude/agents/ark` to the Windows
  install's real skill and agent directories under the drive mount (not to the
  Windows `~/.claude` junctions, which the drive mount may not resolve). The link set
  is the one `ark setup` makes on the native side, taken from the same list, so the
  two cannot drift;
- never overwrites a real (non-symlink) file or directory: it skips it with a
  warning. An existing symlink is replaced; the shim is rewritten only when the file
  at `~/.ark/ark` is a symlink or carries the shim's marker line.

Every Linux path in the script is derived from its Windows path by the same
translation and mount map the output filter uses. The script is generated by
`ark.exe`, which only has the shim's mount map when it was itself run through the
shim; the first run (`eval "$(/mnt/c/.../ark.exe wsl-init)"`) uses the
`/mnt/<letter>` default. **Limit:** with a non-default automount root, run the
first `wsl-init` with the map given by hand, for example
`ARK_WSL_MOUNTS='C:=/c' WSLENV=ARK_WSL_MOUNTS/u /c/Users/<you>/.ark/ark.exe wsl-init`.
