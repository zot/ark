# HostAPI
**Requirements:** R1326, R1327, R1328, R1353

TypeScript interface defining the contract between the viewer and
its host. Extends SearchAPI with CM6-specific methods. The viewer
receives an implementation at construction and never imports ark
or Frictionless directly.

## Knows
- nothing — it is an interface, not a class

## Does
- (inherited from SearchAPI) search, tagComplete, tagValueComplete, navigate, showInFolder
- save(path, content): writes file, triggers re-index
- setTags(path, tags): atomic tag block update

## Collaborators
- SearchAPI: HostAPI extends this interface
- Every other markdown-editor component depends on this interface

## Sequences
- seq-tag-click.md
- seq-tag-completion.md
- seq-ark-search-render.md
- seq-save.md
