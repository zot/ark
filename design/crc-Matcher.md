# Matcher
**Requirements:** R9, R10, R15, R16, R17, R18, R19, R20, R21, R147

Evaluates the four-form pattern language against file paths. Stateless
— receives patterns and paths, returns classification.

## Knows
- dotfiles: bool — whether * matches dotfiles

## Does
- Match(pattern, path): bool — does this pattern match this path?
- Classify(includes, excludes, path): included | excluded | unresolved
  - If any include matches → included (include wins)
  - If any exclude matches and no include → excluded
  - If nothing matches → unresolved
- IsDirectory(pattern): bool — does pattern end with /?
- IsAnchored(pattern): bool — does pattern start with /?
- ParsePattern(pattern): parsed form (name, name/, name/*, name/**)

## Collaborators
- None (stateless utility)

## Sequences
- seq-add.md
- seq-config-mutate.md
