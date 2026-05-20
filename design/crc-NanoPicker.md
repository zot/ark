# NanoPicker
**Requirements:** R2513

The interactive session selector invoked when the CLI is given `-s`.
Prints a numbered list of the ten most recent sessions in the cwd along
with a relative-time hint, reads one digit from stdin, and returns the
chosen NanoSession.

## Knows
- The 10-session display cap
- The age-to-text mapping: `<3600s → Nm`, `<86400s → Nh`, otherwise `Nd`
- The list orientation: most recent at the top, indices increase
  downward starting at zero

## Does
- NanoSessionsInCwd to get the candidate list
- Truncate to the last ten if there are more
- Render each row with index, label, and `<age> ago`
- Print the prompt `nano# ` (bold/dim when TTY)
- Read one token from stdin; treat malformed input as an error
- Return the NanoSession at the chosen index, or an error if out of range

## Collaborators
- NanoSessionStore: NanoSessionsInCwd is the data source
- CLI: the only caller

## Sequences
- seq-nano-session-resume.md
