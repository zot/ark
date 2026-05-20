# NanoShellTool
**Requirements:** R2536, R2537, R2538, R2539, R2540, R2541, R2542, R2551, R2552, R2553, R2554

The single tool exposed to the model. Parses arguments, enforces the
description word count, defers to NanoApprover, and runs the command. Owns
the result string format and the output cap.

## Knows
- The tool's JSON Schema (command, description, cwd, timeout, env)
- Default timeout (60 seconds)
- Default cwd (Nano.Cwd)
- The 5–10 word band for `description`
- The result string shape: `$ <command>\nexit <code>\n<output>` or
  `$ <command>\ntimeout after <N>s\n<output>`

## Does
- Reject calls whose tool name is not `execute_shell` with `unknown tool`
- Unmarshal the tool call's arguments into ShellArgs
- Reject malformed JSON arguments with `bad arguments: <err>`
- Reject description outside 5–10 words with
  `bad arguments: description must be 5-10 words`
- Ask NanoApprover before running
- Run via `sh -c <command>` with merged environment
  (`os.Environ()` ++ args.Env)
- Enforce timeout via `time.AfterFunc` that kills the process
- Capture stdout + stderr into one buffer
- Format and clip the result via Nano.MaxOutputBytes

## Collaborators
- NanoApprover: gates execution
- Nano: source of Cwd, environment, MaxOutputBytes

## Sequences
- seq-nano-shell-exec.md
