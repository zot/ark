# Table Sort

Go-side sort for Lua tables. Sorting in Go is significantly faster
than Lua, and the UI needs sortable columns for the messaging
dashboard (and potentially other views).

## Function

`mcp:sort(table, property, isDate, descending)`

- `table` — Lua array of tables (objects with string keys)
- `property` — string field name to sort by
- `isDate` — boolean. If true, parse values as dates (`2026-03-22`
  format) for comparison. If false, compare as case-insensitive
  strings.
- `descending` — boolean. If true, sort largest/latest first.

Returns the input table, sorted in place. Items where the property
is missing or nil sort to the end regardless of direction.

## Date parsing

Date values are `YYYY-MM-DD` strings (the format `@status-date:`
uses). Compare lexicographically — this format sorts correctly
as strings, so no actual date parsing is needed. The `isDate`
flag exists to document intent and allow future format support.

## Use case

The messaging dashboard kanban columns need sorting by date
(most recent first), to-project, from-project, or subject.
The UI cycles sort order on column header click.
