# DirLink
**Requirements:** R3453, R3454, R3455

Portable directory-link primitive. Makes one directory link per call — a
symbolic link on Unix, a directory junction on Windows — so skills, agents,
and Frictionless apps wire into the places the runtime and Claude Code look
without copying and without privilege. Shared by setup, `--local`, and the
updater (and by app-activation, out of this slice).

## Knows
- platform: build-tag-selected implementation (Unix symlink vs. Windows junction)

## Does
- MakeDirLink(linkPath, targetDir): create a directory link at `linkPath`
  pointing at `targetDir`. Unix uses `os.Symlink`; Windows writes a
  junction through `golang.org/x/sys/windows` reparse points, needing no
  `SeCreateSymbolicLinkPrivilege` and no Developer Mode, and never shelling
  out to `mklink` (R3453). Idempotent: when `linkPath` is already a link
  this helper owns (`os.Readlink` reports the same target), it is left in
  place (R3455).
- RemoveDirLink(linkPath): remove the link with `os.Remove`. Refuses to
  delete a real (non-link) directory at `linkPath`, since a non-link
  directory there is the user's data (R3454). On Windows a junction reads
  as `ModeIrregular` under `Lstat` (measured, build 26200), so the
  link-vs-directory test cannot rely on `ModeSymlink` alone.
- IsOurLink(linkPath, targetDir) bool: `os.Readlink` returns a junction's
  absolute target on Windows, so the helper can distinguish "already our
  link" from someone else's link or a real directory (R3455).

## Collaborators
- (none — leaf primitive over the OS)

## Sequences
- seq-install.md
