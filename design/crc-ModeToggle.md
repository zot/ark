# ModeToggle
**Requirements:** R1348, R1349, R1350, R1351

Manages the document-level read/edit mode state and the toggle
action. Provides a StateField that other extensions check.

## Knows
- current mode: read-only or edit
- the file path being edited

## Does
- provides a CM6 StateField<boolean> for read/edit mode
- toggleMode: dispatches a state effect to flip the mode
- on entering edit mode: makes the editor editable, ark-search blocks gain all three view modes
- on entering read mode: makes the editor read-only, widgets activate, ark-search blocks respect mode= restrictions
- on save (from edit mode): calls HostAPI.save(path, content)
- on tag edit: calls HostAPI.setTags(path, tags) for atomic updates

## Collaborators
- HostAPI: save() and setTags()
- TagWidget: reads mode state to enable/suppress interactivity
- ArkSearchBlock: reads mode state for mode= override logic

## Sequences
- seq-mode-toggle.md
- seq-save.md
