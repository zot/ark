# Sequence: Tag Overview Click Dispatch
**Requirements:** R2046, R2047, R2048, R2049, R2050, R2051, R2068, R2069, R2070, R2071

How sidebar row interactions and body indicator interactions
both produce the same end state. The sidebar is a remote
control: its icons dispatch the body-element handlers.

## Sidebar row text/row click — scroll only (R2046, R2047)

```
User                TagOverviewSidebar             Document
  |                    |                              |
  |--click row text--->|                              |
  |                    |--scrollIntoView(chunkID)---->|
  |                    |                              |
```

## Sidebar 🔍 click on inline tag — body dispatch (R2048)

```
User                TagOverviewSidebar       ArkTagElement (body)        ArkSearchElement
  |                    |                          |                          |
  |--click 🔍--------->|                          |                          |
  |                    |--scrollIntoView--------->|                          |
  |                    |--dispatch click--------->|                          |
  |                    |                          |--toggle inline panel---->|
  |                    |                          |  (existing behavior)     |
```

## Sidebar 🔍 click on ext tag — bypass pick-list (R2049)

```
User                TagOverviewSidebar       ArkExtTagsElement       ArkSearchElement
  |                    |                          |                       |
  |--click 🔍--------->|                          |                       |
  |                    |--scrollIntoView--------->|                       |
  |                    |--openPanelForTag(t,v)--->|                       |
  |                    |                          |--insert/split-------->|
  |                    |                          |  panel with seed t:v  |
  |                    |                          |  (no dropdown shown)  |
```

## Sidebar ↗ click on ext tag — leave to source doc (R2050, R2051)

```
User                TagOverviewSidebar              Browser
  |                    |                                |
  |--hover ↗---------->|                                |
  |                    |--render tooltip                |
  |                    |  DEFINITION-PATH               |
  |                    |  ---------------               |
  |                    |  THIS-PATH                     |
  |                    |  anchor: ANCHOR-SPEC (omit if empty)
  |                    |                                |
  |--click ↗---------->|                                |
  |                    |--navigate /content/EXTERNALFILE>
  |                    |  (browser scroll to anchor)    |
```

## Body indicator click — pick-list dropdown (R2068-R2071)

```
User                ArkExtTagsElement            ArkSearchElement
  |                    |                              |
  |--mousedown-------->|                              |
  |                    |--open dropdown               |
  |                    |  (always shown — no shortcut |
  |                    |   even with one tag, R2071)  |
  |                    |                              |
  | (drag onto tag)                                   |
  |--mouseup on row--->|                              |
  |                    |--openPanelForTag(t,v)------->|
  |                    |                              |--insert panel----->
  |                    |                              |  seeded t:v        |
  |                    |                              |
  | OR (click-and-release)                            |
  |--mouseup off rows->|                              |
  |                    |  dropdown stays open         |
  |--click on row----->|                              |
  |                    |--openPanelForTag(t,v)------->|
  |                    |                              |
  |--Escape / outside->|                              |
  |                    |  dismiss dropdown            |
```
