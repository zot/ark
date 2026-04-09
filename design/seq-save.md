# Sequence: Save

User saves the document from edit mode.

## Participants
- User
- ModeToggle (crc-ModeToggle.md)
- HostAPI (crc-HostAPI.md)

## Flow

```
User        ModeToggle              HostAPI
  |              |                     |
  |--save------->|                     |
  |              |--save(path, content)->|
  |              |                     |--write file + re-index
  |              |<--ok/error----------|
  |<--feedback---|                     |
```

## Flow — Tag Edit (atomic)

```
User        ModeToggle              HostAPI
  |              |                     |
  |--tag edit--->|                     |
  |              |--setTags(path, {    |
  |              |    tag: value})---->|
  |              |                     |--update tag block
  |              |                     |--re-index
  |              |<--ok/error----------|
```

## Notes
- save() sends full document content
- setTags() is for atomic tag-only updates (e.g. status dropdown change)
- setTags() can be used in read mode (status dropdown doesn't require edit mode)
