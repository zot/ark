# Nano: tool loop

## The one tool

The model is given exactly one tool, `execute_shell`. The tool takes:

- `command` (string, required) — the shell command to run via
  `sh -c`.
- `description` (string, required) — why this command is useful right
  now, in 5 to 10 words. The library enforces the word count and
  rejects calls that miss the band.
- `cwd` (string, optional) — directory to run in. Defaults to the
  agent's `Cwd`.
- `timeout` (integer, optional, seconds) — kill the command after
  this long. Defaults to 60 seconds.
- `env` (object, optional) — additional environment variables, merged
  on top of `os.Environ()`.

The 5–10 word description rule is inherited from nano.py. It forces
the model to state intent every step, which makes approvals readable
and the trace auditable.

## The loop

```
history = [system_prompt]
history.append(user_prompt)
loop up to MaxSteps times:
    assistant = chat(history)
    history.append(assistant)
    if assistant has no tool calls:
        return assistant.content
    for each tool call:
        result = run_tool(call)
        history.append({role: "tool", content: result})
```

`MaxSteps` defaults to 200. If the budget is exhausted the loop
returns `stopped: too many tool calls` and the partial history.

## Approval

Before every command actually runs:

1. The library prints the description, the command (in green), and
   any non-default cwd/timeout/env to stderr.
2. If `ApproveAll` is set, the command runs immediately.
3. Otherwise, the library prompts `Approve? [y] Approve [a] Approve
   All [n] Deny:` and reads one line from stdin.
   - `y` / `yes` — run this command.
   - `a` / `all` — flip `ApproveAll` to true for the rest of the
     run, then run this command.
   - Anything else (including EOF) — deny.
4. A denied command returns the literal string `denied by user` to
   the model as the tool result.

## Execution

Approved commands run as `sh -c <command>` with:
- working directory set to `cwd` (absolute path)
- env = `os.Environ()` plus any `env` map entries
- stdout and stderr merged into one buffer
- a timer enforcing `timeout`; on expiration the process is killed
  and the buffer up to that point is returned.

The tool result string is shaped as either
`$ <command>\nexit <code>\n<output>` or
`$ <command>\ntimeout after <N>s\n<output>` and clipped to the last
12 KB so a single noisy command can't drown the context.

## System prompt

Built once per `Run` call. It tells the model:
- Its identity ("You are Nano, a general-purpose shell agent with
  one tool: execute_shell").
- The cwd, platform (Go's `runtime.GOOS` / `runtime.GOARCH`), and
  the user's `$SHELL`.
- A list of project documentation files (CLAUDE.md, AGENT.md,
  AGENTS.md, README.md — case-insensitive) found under the cwd, with
  a result cap.
- A list of skill files (SKILL.md, SKILLS.md — case-insensitive)
  found in `.claude/skills`, `~/.claude/skills`, `~/.codex/skills`,
  `~/.codex/plugins`.

While walking those directories, nano skips `.git`, `.venv`,
`__pycache__`, `node_modules`, and `venv`.

## Output capping

Every tool result is clipped to the last `Nano.MaxOutputBytes` bytes
before being sent back to the model. The field defaults to 12 000 —
the same cap nano.py used — and library callers can override it for
tasks that need a larger or smaller window.
