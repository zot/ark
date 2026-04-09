# SearchAPI
**Requirements:** R1352, R1353, R1354, R1355

TypeScript interface defining the contract between the search
component and the server. The search-relevant subset of HostAPI.

## Knows
- nothing — it is an interface, not a class

## Does
- search(query, mode?): returns grouped results
- tagComplete(prefix): returns tag name completions from D records
- tagValueComplete(tag, prefix): returns value completions for a tag
- navigate(path): asks host to open a different file
- showInFolder?(path): show file in native file manager

## Collaborators
- ArkSearchElement: depends on this interface
- HostAPI: extends this interface with save(), setTags()

## Sequences
- seq-tag-click.md
- seq-ark-search-render.md
