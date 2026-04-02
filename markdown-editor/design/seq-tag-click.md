# Sequence: Tag Click → Search Results

User clicks an `@tag: value` widget in the document (or in a
search result). A search panel appears below the line with results.

## Participants
- User
- TagWidget (crc-TagWidget.md)
- HostAPI (crc-HostAPI.md)
- SearchResultView (crc-SearchResultView.md)

## Flow

```
User            TagWidget              HostAPI           SearchResultView
  |                |                      |                    |
  |--click tag---->|                      |                    |
  |                |--search("@tag: v")-->|                    |
  |                |                      |--grouped results-->|
  |                |                      |                    |
  |                |                SearchResultView            |
  |                |                creates panel               |
  |                |                below tag line              |
  |                |                      |                    |
  |                |                      |    [for each chunk]|
  |                |                      |    md? -> CM6 inst |
  |                |                      |    else -> HTML    |
  |                |                      |                    |
  |<---panel with results, tag text pre-selected in field------|
  |                |                      |                    |
  |--type to refine query---------------->|                    |
  |                |                      |--updated results-->|
  |                |                      |                    |
  |--click result path------------------->|                    |
  |                |           navigate(path)                  |
```

## Notes
- Search field shows `@tag: value` with full text selected
- User can read results as-is, type to replace query, or type
  after selection to refine
- Escape dismisses the panel
- Tag clicks inside markdown search results trigger this same
  sequence recursively
