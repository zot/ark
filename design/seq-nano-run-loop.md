# Sequence: Run loop

The `Run(prompt, history)` method on `Nano`. Drives the chat-tool loop
until the model produces a plain assistant message or the step budget
runs out.

**Participants:** Nano, NanoOllamaClient, NanoShellTool

## Steps

- 1. **Setup.** Apply defaults (init). Refuse if Model is unset (R2493).
  - 1.1. If history is nil, seed it with a Message{role:"system",
    content: NanoSystemPromptBuilder.build()} (R2501, R2555–R2558).
  - 1.2. Append Message{role:"user", content: prompt} to history.
- 2. **Loop.** Up to `MaxSteps` iterations (R2543):
  - 2.1. Call NanoOllamaClient.chat(history) → assistant Message.
  - 2.2. Append the assistant Message to history.
  - 2.3. If assistant.ToolCalls is empty, return (assistant.Content,
    history, nil) (R2500). Loop exits.
  - 2.4. For each tool call, run NanoShellTool.handle(tc) (see
    seq-nano-shell-exec.md) → result string.
  - 2.5. Append Message{role:"tool", content: result} to history for
    each call, preserving the order of tool calls.
  - 2.6. Continue loop.
- 3. **Budget exhausted.** Return ("stopped: too many tool calls",
  history, nil) (R2544).
