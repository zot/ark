# ArkTagParser
**Requirements:** R5, R6

Lezer markdown parser extension that recognizes `@word: value`
patterns in document text and produces typed ArkTag AST nodes.

## Knows
- the tag pattern: `@` + word chars + `:` (the colon disambiguates from emails)
- node type definitions for ArkTag nodes

## Does
- parseInline: recognizes `@word: value` and emits ArkTag nodes with tag name and value ranges
- defineNodes: registers ArkTag node type with the Lezer grammar
- props: attaches highlighting/styling tags to ArkTag nodes

## Collaborators
- @codemirror/lang-markdown: this extension is passed to markdown() config
- TagWidget: consumes ArkTag nodes to place decorations

## Sequences
- seq-tag-click.md (provides the nodes TagWidget decorates)
