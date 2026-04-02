# HostAPI
**Requirements:** R1, R2, R3

TypeScript interface defining the contract between the viewer and
its host. The viewer receives an implementation at construction
and never imports ark or Frictionless directly.

## Knows
- nothing — it is an interface, not a class

## Does
- search(query): returns grouped results with raw chunk content, content type, and pre-rendered HTML
- tagComplete(prefix): returns tag name completions from D records
- tagValueComplete(tag, prefix): returns value completions for a tag
- save(path, content): writes file, triggers re-index
- navigate(path): asks host to open a different file
- setTags(path, tags): atomic tag block update

## Collaborators
- Every other component depends on this interface

## Sequences
- seq-tag-click.md
- seq-tag-completion.md
- seq-ark-search-render.md
- seq-save.md
