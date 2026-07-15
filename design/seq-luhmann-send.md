# Sequence: `ark luhmann send` — synchronous command bridge

**Requirements:** R3129, R3130, R3131, R3132, R3133, R3134

How one CLI instruction becomes a bracketed transcript of the orchestrator's
reply. `send` proxies to the server; the server produces onto the same tube
`next` drains, then reads the reply off the JSONL tap it already owns.

Participants: **CLI** (`luhmannSendAction`, crc-CLITree.md) · **LuhmannSend**
(crc-LuhmannSend.md) · **Tube** (`nextQueue` + `LuhmannNext`, crc-LuhmannCLI.md)
· **Luhmann** (the hosted orchestrator session) · **JSONL** (Luhmann's session
chat log).

```
1.    CLI: ark luhmann send "summarize this listing and add a job item" [--timeout 120]
1.1     CLI -> LuhmannSend: POST /luhmann/send {instruction, timeout}
1.2     LuhmannSend: gate — LuhmannOwner()=="" ? -> 4xx orchestrator-not-running (R3134); no enqueue
1.3     LuhmannSend: n = sendCounter++ ; build markdown request w/ inert `LSEND:n` marker (R3131)
1.4     LuhmannSend -> Tube: EnqueueLuhmann(command{request, nonce:n})  (R3129, R3130)
1.5     LuhmannSend: offset = end of locateSessionJSONL(owner) [glob ~/.claude/projects/*/<uuid>.jsonl, NOT indexed] (R3132)
1.6     Tube -> Luhmann: LuhmannNext returns the command crank-handle (leads w/ re-launch-first, R3036)
1.7     Luhmann -> JSONL: tool_result carrying `LSEND:n`  <-- OPEN bracket (R3132)
1.8     Luhmann -> JSONL: fires next#2 (bg), does the work (tool calls), writes its reply
1.9     Luhmann -> JSONL: system/turn_duration  <-- CLOSE: first signalTurnDuration after OPEN (R3132)
1.10    LuhmannSend: tail found OPEN..CLOSE via scanNewBytes -> return raw window lines
1.11    LuhmannSend -> CLI: 200 body = window JSONL  (or: timeout -> non-2xx, enqueue stands, R3133)
1.12    CLI: renderChatLines(window, withTools=true) -> print turns ; exit 0  (R3129, R3133)
1.13    CLI: on timeout body -> stderr "enqueued, no turn completed in <D>s" ; exit non-zero (R3133)
```

Note — the OPEN line is a `user` tool_result whose content is a tool_result
block (not text), so the transcript renderer skips it: only Luhmann's assistant
turns between OPEN and CLOSE render. A server bounce during the 1.10 tail is a
wait condition (Stubborn Plumbing, R3133), not a failure.
