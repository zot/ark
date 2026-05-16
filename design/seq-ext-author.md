# Sequence: @ext authoring (setExtTag / removeExtTag)

Covers the workshop's `mcp.setExtTag` and `mcp.removeExtTag`
flows from Lua call through mirror file write, watcher pickup,
and reindex into the in-memory ExtMap.

The mirror tree under `~/.ark/external/` is itself an ark
source (added to `arkSourceIncludePatterns` via `external/**`).
When `setExtTag` modifies a mirror file, the watcher and indexer
treat it as any other content change — routings flow through
`runExtRouting` and `ExtMap.IndexExt` exactly as they would for
a user-authored source file. No special "mirror" branch in the
indexer.

The mirror file is written via temp+rename (matching the
`mcp.setTags` precedent for Lua-driven file mutation) — not
through `enqueueWrite`. Atomicity is provided by the rename;
reindex is asynchronous via the watcher.

## Participants
- Lua viewdef / app code
- Server (mcp bridge)
- DB
- mirror file (`~/.ark/external/<source-slug>/<target-path>.md`)
- microfts2 watcher
- Indexer
- ExtMap

## Flow: setExtTag

```
1.   Lua viewdef ──> mcp.setExtTag(targetSpec, tag, value)
1.1.   Server bridge ──> DB.SetExtTag(targetSpec, tag, value)
1.2.     DB resolves targetSpec to a target file:
           ParseExtTargetParts(targetSpec, "") → parts
           if BaseKind == "uuid":
             resolveExtUUIDBase → []chunkid
             chunkFileID(chunkid) → fileid
             fileIDPath(fileid) → target_path
           else:
             target_path := parts.BaseValue (absolutized path)
1.3.     DB computes mirror file path:
           source_root := Config.SourceRootForPath(target_path)
           source_slug := strings.ReplaceAll(source_root[1:], "/", "-")
           mirror_path := ~/.ark/external/{slug}/{target-path}.md
1.4.     read mirror file (missing → empty bytes)
1.5.     applyExtMirrorEdit:
1.5.1.     walk lines, parse @ext lines via mutateExtLine
1.5.2.     if line's TARGET matches byte-for-byte AND tag list
             contains tag:
               replace value in place; preserve other tags
             else:
               continue
1.5.3.     if no match across all lines:
               append new line: `@ext: {target} @{tag}: {value}`
1.6.     os.MkdirAll(dirname(mirror_path))
1.7.     atomicWriteFile(mirror_path, newData):
1.7.1.     write `{mirror_path}.tmp`
1.7.2.     rename `{mirror_path}.tmp` → `{mirror_path}`
1.8.   Lua bridge returns (true, nil) to caller
         // bridge returns immediately after the rename; reindex
         // is asynchronous via the watcher (steps 1.9+ below)
1.9.   microfts2 watcher fires on mirror file change
1.9.1.   Indexer.RefreshFile(mirror_path) → reindex chunk(s)
1.9.2.   indexed-chunk callback for the chunk containing the new line
1.9.3.   ExtMap.IndexExt(tvid_ext, sourceChunkID, value,
                          sourceFileid, txn, tt)
            // see seq-ext-routing.md for the full @ext routing
            // flow — narrower handling, BASE-keyed extByAnchor,
            // X / V record writes
```

## Flow: removeExtTag

```
2.   Lua viewdef ──> mcp.removeExtTag(targetSpec, tag)
2.1.   Server bridge ──> DB.RemoveExtTag(targetSpec, tag)
2.2.   DB computes mirror file path (same algorithm as 1.2 + 1.3)
2.3.   read mirror file:
2.3.1.   missing → silent no-op, return (true, nil)
2.4.   applyExtMirrorEdit (remove=true):
2.4.1.   walk lines, parse @ext lines via mutateExtLine
2.4.2.   if line's TARGET matches AND tag list contains tag:
2.4.2.1.   single-tag line → drop the line entirely
             (mutateExtLine returns dropLine=true)
2.4.2.2.   multi-tag line → remove only the `@{tag}: value` span,
             preserve the rest
2.4.3.   if no matching line → silent no-op, return (true, nil)
2.5.   atomicWriteFile(mirror_path, newData):
2.5.1.   write `{mirror_path}.tmp`
2.5.2.   rename `{mirror_path}.tmp` → `{mirror_path}`
2.6.   Lua bridge returns (true, nil)
2.7.   microfts2 watcher fires on mirror file change
2.7.1.   Indexer.RefreshFile(mirror_path)
2.7.2.   if line was deleted entirely, the orphan callback fires
           for the chunk's tvid_ext
2.7.3.     ExtMap.CleanupSource(sourceChunkID, tvid_ext, txn, tt)
              // see seq-ext-routing.md — strikes target chunkid
              // from each routed tag's V record, drops the X
              // record, decrements virtualTagCount, frees the
              // tvid_ext if its V record empties
2.7.4.   if the line was reshaped (multi-tag), reindex emits
           IndexExt for the new chunk text — ReresolveOnReindex
           handles the diff for the affected tvid_ext
```

## Notes

The atomic write at 1.7 (and 2.5) is what gives the caller
`(true, nil)` as soon as the file is on disk. Reindex is
asynchronous via the watcher; the workshop UI subscribes to the
relevant `@ext` tag changes via `mcp.subscribe` if it needs to
react to the new routing landing.

The source-slug derivation at step 1.3 depends on `Config`
knowing which source root contains `target_path`. If no source
contains the target path (e.g., user `@ext`'d to a path that
isn't indexed), the call returns `(false, "no source root
contains <path>")` — no mirror file is created.
