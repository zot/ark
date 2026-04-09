# SearchAPI
**Requirements:** R1352, R1353, R1354, R1355, R1384, R1385

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
- embedMatch?(query, k?): embedding similarity search, returns TagMatch[]
- expandSearch?(tags): search for file results matching tag/value pairs
- curateRequest?(tag, value, candidates): queue Haiku curation, returns requestId
- curateResult?(id): poll for curation result (curated + rejected TagMatch[])

## Collaborators
- ArkSearchElement: depends on this interface
- HostAPI: extends this interface with save(), setTags()

## Sequences
- seq-tag-click.md
- seq-ark-search-render.md
