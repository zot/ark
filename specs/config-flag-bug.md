# Bug: Config mutation flags silently ignored

Go's `flag` package stops parsing at the first non-flag argument.
All config mutation subcommands that take a positional argument
followed by optional flags are affected.

Example:
```
ark config add-include "*.md" --source "/home/deck/work/bill"
```

The `"*.md"` is a positional argument that appears before `--source`.
Go's flag parser stops at `"*.md"`, never sees `--source`, and the
flag keeps its default value (empty string). The pattern silently
goes to global includes instead of per-source.

This affects: `add-include`, `add-exclude`, `remove-pattern` — any
config subcommand where a positional arg precedes an optional flag.

## Fix

Parse flags before extracting positional args. The flag set must
see all arguments, so either:

1. Require flags before positional args (document the convention), or
2. Use a flag parser that handles interspersed flags and positional
   args (Go's `flag` package does not — but we can reorder args
   before parsing)

Option 2 is more user-friendly. Reorder `args` so flags come first
before calling `fs.Parse`. This is a local fix in each affected
`cmdConfig*` function.
