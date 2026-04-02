# TagCompletion
**Requirements:** R12, R13

CM6 completion source that provides tag name and tag value
autocompletion while editing.

## Knows
- trigger pattern: `@` at word start triggers tag name completion
- trigger pattern: colon after `@tagname:` triggers value completion

## Does
- provides a CompletionSource for CM6's autocompletion extension
- on `@` prefix: calls HostAPI.tagComplete(prefix), returns tag names from D records
- on post-colon: calls HostAPI.tagValueComplete(tag, prefix), returns known values

## Collaborators
- HostAPI: tagComplete() and tagValueComplete() provide the data
- @codemirror/autocomplete: this is registered as a completion source

## Sequences
- seq-tag-completion.md
