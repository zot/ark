# Sequence: ark-search Block Rendering and Mode Cycling

An ark-search fenced code block renders as a live search panel.
User cycles between view modes.

## Participants
- User
- ArkSearchBlock (crc-ArkSearchBlock.md)
- HostAPI (crc-HostAPI.md)
- SearchResultView (crc-SearchResultView.md)

## Flow — Initial Render

```
ArkSearchBlock                  HostAPI              SearchResultView
  |                                |                       |
  |--parse fence info string------>|                       |
  |  (extract mode= attribute)    |                       |
  |                                |                       |
  |--search(query from block)----->|                       |
  |                                |                       |
  |<--grouped results (raw+html)---|                       |
  |                                                        |
  |--render initial mode (first in mode list)------------->|
  |  both: source editor + results                         |
  |  results: results panel only                           |
  |  src: code block only                                  |
```

## Flow — Mode Cycling

```
User          ArkSearchBlock              SearchResultView
  |                |                           |
  |--click toggle->|                           |
  |                |--advance to next mode     |
  |                |  in allowed list--------->|
  |                |                           |
  |                |  (if only one mode,       |
  |                |   click is no-op)         |
  |<--view updates-|                           |
```

## Flow — Query Edit (both or src mode)

```
User          ArkSearchBlock        HostAPI         SearchResultView
  |                |                   |                   |
  |--edit query--->|                   |                   |
  |                |--search(new q)--->|                   |
  |                |<--new results-----|                   |
  |                |--update results------------------>|
```

## Notes
- Default mode order: both,results,src
- mode=results makes the search read-only in read mode
- Edit mode (file-level) overrides mode= restriction:
  all three modes become available
- Query edits only possible in both or src modes
