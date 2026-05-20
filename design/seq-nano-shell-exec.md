# Sequence: Shell execution

One tool call's life from "model returned tool_calls" to "result
string ready to feed back". Owned by NanoShellTool with NanoApprover as
gatekeeper.

**Participants:** NanoShellTool, NanoApprover

## Steps

- 1. **Dispatch.** NanoShellTool.handle(tc) — tc is one element of
  assistant.ToolCalls.
  - 1.1. If tc.Function.Name != "execute_shell", return "unknown tool"
    (R2536).
  - 1.2. json.Unmarshal(tc.Function.Arguments) → ShellArgs. On error,
    return "bad arguments: <err>".
  - 1.3. Count whitespace-separated words in args.Description. If <5 or
    >10, return "bad arguments: description must be 5-10 words" (R2539).
- 2. **Approve.** NanoApprover.approve(args) → bool.
  - 2.1. Print description (dim, `# ...`), command (green, `$ ...`),
    and any non-default cwd/timeout/env to Stderr (R2545).
  - 2.2. If Nano.ApproveAll is true, return true immediately (R2546).
  - 2.3. Print menu, read one line from Stdin.
  - 2.4. On `a`/`all`, flip ApproveAll to true and return true (R2548).
  - 2.5. On `y`/`yes`, return true (R2547).
  - 2.6. On EOF or anything else, return false (R2549).
- 3. **Deny short-circuit.** If approve returned false, return literal
  string "denied by user" (R2550).
- 4. **Execute.**
  - 4.1. Compute timeout: args.Timeout if non-zero, else 60 (R2540).
  - 4.2. Compute cwd: filepath.Abs(args.Cwd) if non-empty, else
    Nano.Cwd (R2541).
  - 4.3. Build exec.Command("sh", "-c", args.Command) (R2551).
  - 4.4. Set cmd.Env = os.Environ() ++ args.Env (R2542).
  - 4.5. Combined stdout+stderr into one buffer.
  - 4.6. Start the command. Arm a time.AfterFunc(timeout, kill).
  - 4.7. Wait for cmd to exit. Stop the timer.
- 5. **Format.**
  - 5.1. If the kill timer fired, format
    "$ <cmd>\ntimeout after <N>s\n<output>" (R2552, R2554).
  - 5.2. Otherwise, format "$ <cmd>\nexit <code>\n<output>" using
    ExitError.ExitCode() or 0 (R2552).
  - 5.3. Clip to last Nano.MaxOutputBytes bytes (R2553).
- 6. **Return** the formatted string to the Run loop, which appends it
  as a Message{role:"tool"}.
