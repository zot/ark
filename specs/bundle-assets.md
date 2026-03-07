# Bundle and Asset Commands

Ark ships as a single binary with UI assets grafted on as a zip
appendix (ui-engine's bundle system). These commands manage the
embedded assets.

## Bundle Mechanism

The ui-engine `bundle` package appends a zip archive to the end of a
Go binary. The binary remains executable — the OS ignores the
trailing zip data. At runtime, the bundle functions read from the
running executable to find and extract the zip.

This replaces `//go:embed`. The zip-graft approach lets the Makefile
pipeline layer assets from multiple sources (ui-engine, frictionless,
ark's own app) without recompilation.

## Commands

### `ark bundle -o <output> [-src <binary>] <dir>`

Graft a directory onto a binary as a zip appendix.

- `-o` is required — the output path for the bundled binary
- `-src` specifies the source binary (default: current executable)
- The positional argument is the directory to bundle
- The source binary must exist
- The directory must exist
- On success, prints "Created bundled binary: <output>"

This is a build-time command used by the Makefile, not by end users.

### `ark ls`

List embedded assets in the running binary.

- If the binary is not bundled, print an error and exit 1
- Lists one file per line
- Symlinks show as `path -> target`

### `ark cat <file>`

Print an embedded file to stdout.

- If the binary is not bundled, print an error and exit 1
- The file path must match an entry in the bundle
- Output is raw bytes (no trailing newline added)

### `ark cp <pattern> <dest-dir>`

Extract embedded files matching a glob pattern to a directory.

- If the binary is not bundled, print an error and exit 1
- Pattern matches against both basename and full path
- Creates destination directories as needed
- Preserves file permissions from the bundle
- Recreates symlinks as symlinks (not copies)
- Removes existing files/symlinks before writing (allows overwrite)
- Reports each copied file
- If no files match, print an error and exit 1

## Upstream Dependency

Ark calls ui-engine's exported bundle functions directly:
- `cli.IsBundled` — check if running binary has a bundle
- `cli.BundleListFilesWithInfo` — list with metadata (symlinks, mode)
- `cli.BundleReadFile` — read a single file from the bundle

Two functions need to be re-exported from ui-engine for ark's use:
- `bundle.CreateBundle(src, dir, output)` — graft zip onto binary
- `bundle.ExtractBundle(targetDir)` — extract all files

## Makefile Asset Pipeline

The build pipeline layers assets from three sources:

1. Build frictionless (which already bundles ui-engine assets)
2. Use frictionless's `cp` command to extract its assets into a cache
3. Layer ark's own assets (apps/ark/) on top
4. Build the ark Go binary
5. Use `ark bundle` to graft the cache onto the binary

This produces one binary that contains the full UI stack.

## Spec Update: ark install

The existing install spec (in main.md) describes `//go:embed` for UI
assets. The actual mechanism is the bundle system described here.
`ark install` should use `bundle.ExtractBundle` to extract assets to
`~/.ark/` rather than go:embed directives. The install flow is
otherwise unchanged.
