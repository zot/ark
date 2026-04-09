# Sequence: Read/Edit Mode Toggle

User toggles the document between read-only and edit mode.

## Participants
- User
- ModeToggle (crc-ModeToggle.md)
- TagWidget (crc-TagWidget.md)
- ArkSearchBlock (crc-ArkSearchBlock.md)

## Flow — Read → Edit

```
User        ModeToggle          TagWidget         ArkSearchBlock
  |              |                  |                   |
  |--toggle----->|                  |                   |
  |              |--dispatch edit-->|                   |
  |              |  mode effect     |                   |
  |              |                  |--suppress widgets |
  |              |                  |  (standard CM6    |
  |              |                  |   editing active) |
  |              |                  |                   |
  |              |--------------------------------->|
  |              |        all three modes available |
  |              |        regardless of mode= attr  |
```

## Flow — Edit → Read

```
User        ModeToggle          TagWidget         ArkSearchBlock
  |              |                  |                   |
  |--toggle----->|                  |                   |
  |              |--dispatch read-->|                   |
  |              |  mode effect     |                   |
  |              |                  |--activate widgets |
  |              |                  |                   |
  |              |--------------------------------->|
  |              |        revert to mode= restriction|
```

## Notes
- Mode is a CM6 StateField<boolean> — all extensions read it reactively
- Toggle does not auto-save; save is a separate action
