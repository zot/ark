# Test Design: NanoShellTool
**Source:** crc-NanoShellTool.md

## Test: description word-count band
**Purpose:** R2539 — descriptions outside 5–10 words are rejected
**Input:** four cases — descriptions of 4, 5, 10, 11 words against a
ToolCall with name "execute_shell"
**Expected:** 4-word and 11-word produce "bad arguments: description must
be 5-10 words"; 5-word and 10-word reach approval
**Refs:** crc-NanoShellTool.md, seq-nano-shell-exec.md#1.3

## Test: unknown tool name
**Purpose:** R2536 — any tool name other than execute_shell returns
"unknown tool"
**Input:** ToolCall{Function.Name = "rm_rf"}
**Expected:** result == "unknown tool"
**Refs:** crc-NanoShellTool.md, seq-nano-shell-exec.md#1.1

## Test: timeout kills process
**Purpose:** R2554 — command is killed when timeout elapses
**Input:** command "sleep 10" with timeout=1 and ApproveAll=true
**Expected:** total elapsed time < 3s; result string starts
"$ sleep 10\ntimeout after 1s\n"
**Refs:** crc-NanoShellTool.md, seq-nano-shell-exec.md#5.1

## Test: output clipping
**Purpose:** R2553 — long output is clipped to the trailing
MaxOutputBytes bytes
**Input:** command "head -c 20000 /dev/zero | tr '\\0' 'a'" with
MaxOutputBytes=100, ApproveAll=true
**Expected:** len(result) == 100; the clipped tail is all 'a's
**Refs:** crc-NanoShellTool.md, seq-nano-shell-exec.md#5.3

## Test: env merge
**Purpose:** R2542 — args.Env is merged on top of os.Environ()
**Input:** command "echo $X$Y" with env={X:"a", Y:"b"}, ApproveAll=true
**Expected:** result contains "ab"
**Refs:** crc-NanoShellTool.md, seq-nano-shell-exec.md#4.4

## Test: denied user
**Purpose:** R2550 — approver returning false produces "denied by user"
**Input:** ApproveAll=false, Stdin pre-loaded with "n\n"
**Expected:** result == "denied by user"; no process spawned
**Refs:** crc-NanoShellTool.md, seq-nano-shell-exec.md#3
