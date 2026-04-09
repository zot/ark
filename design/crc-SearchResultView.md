# SearchResultView
**Requirements:** R1344, R1345, R1346

Renders search results from HostAPI.search(). Chooses rendering
strategy based on content type: markdown chunks get a read-only
CM6 instance with the full ark extension set; everything else
uses pre-rendered HTML.

## Knows
- the result set: array of {path, chunks} where each chunk has raw content, content type, pre-rendered HTML
- which chunks are markdown (render in CM6) vs other (render as HTML)

## Does
- renders grouped results: file path header, then chunks
- markdown chunks: creates a read-only CM6 EditorView with ArkTagParser + TagWidget + ArkSearchBlock extensions active
- ark-search blocks in results default to src,both,results (no search fires until user clicks)
- non-markdown chunks: renders pre-rendered HTML directly
- click on a result path: calls HostAPI.navigate(path)
- click on a tag in a markdown result: triggers search (recursive interaction)

## Collaborators
- HostAPI: navigate() for result clicks, search() for nested tag clicks
- ArkTagParser: reused in read-only CM6 instances for markdown results
- TagWidget: reused in read-only CM6 instances for markdown results
- ArkSearchBlock: reused in read-only CM6 instances, configured with src-first default

## Sequences
- seq-tag-click.md
- seq-ark-search-render.md
