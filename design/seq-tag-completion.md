# Sequence: Tag Completion

User types `@` to get tag name suggestions, then `:` to get
value suggestions.

## Participants
- User
- TagCompletion (crc-TagCompletion.md)
- HostAPI (crc-HostAPI.md)
- CM6 autocomplete

## Flow

```
User          CM6 autocomplete     TagCompletion          HostAPI
  |                |                    |                    |
  |--type "@st"--->|                    |                    |
  |                |--query context---->|                    |
  |                |                    |--tagComplete("st")->|
  |                |                    |<--[status,standup]--|
  |                |<--completions------|                    |
  |<--dropdown-----|                    |                    |
  |                |                    |                    |
  |--select "status"->|                |                    |
  |--type ": op"----->|                |                    |
  |                |--query context---->|                    |
  |                |              tagValueComplete           |
  |                |                ("status","op")          |
  |                |                    |------------------->|
  |                |                    |<--[open]-----------|
  |                |<--completions------|                    |
  |<--dropdown-----|                    |                    |
```

## Notes
- `@` at word start triggers tag name completion
- Colon after a recognized tag name triggers value completion
- Only active in edit mode
