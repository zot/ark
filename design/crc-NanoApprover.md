# NanoApprover
**Requirements:** R2545, R2546, R2547, R2548, R2549, R2550

The human-in-the-loop gate. Decides whether a given shell command runs.
Reads one line from Stdin; flips ApproveAll on the `a` response so the
rest of the session runs unattended.

## Knows
- Nano.ApproveAll (read and written)
- Nano.Stdin (where the y/a/n answer comes from)
- Nano.Stderr (where the prompt is shown)
- Nano.TTY (whether to render with ANSI color)
- Recognized inputs: `y`, `yes` → approve; `a`, `all` → approve and flip
  ApproveAll; anything else → deny

## Does
- Print description (dim, prefixed `#`), command (green, prefixed `$`),
  and any non-default `cwd`/`timeout`/`env` to Stderr
- Short-circuit and return true when ApproveAll is set
- Otherwise print the approval menu and read one line from Stdin
- On EOF or any unrecognized input, return false (deny)
- On `a`/`all`, set Nano.ApproveAll = true and return true

## Collaborators
- NanoShellTool: the only caller; receives the boolean

## Sequences
- seq-nano-shell-exec.md
