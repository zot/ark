# Test Design: Nano
**Source:** crc-Nano.md

## Test: model required
**Purpose:** R2493 — Run on a zero-value Nano returns an error
**Input:** `Nano{}.Run("hi", nil)`
**Expected:** non-nil error containing "Model is required"; no HTTP call made
**Refs:** crc-Nano.md

## Test: defaults applied
**Purpose:** R2494, R2495, R2496, R2497, R2498, R2499, R2559 — init() populates the zero
fields a caller didn't set
**Input:** `Nano{Model: "x"}.Run(...)` with a stub HTTPClient that records
the request URL
**Expected:** BaseURL == "http://localhost:11434", MaxSteps == 200,
MaxOutputBytes == 12000, SessionsPath ends with "/.ark/nano-sessions.json"
**Refs:** crc-Nano.md, seq-nano-run-loop.md

## Test: fresh-history seeding
**Purpose:** R2501 — Run with nil history seeds a system message first
**Input:** Run("hi", nil) against a fake server that records messages
**Expected:** request.messages[0].role == "system"; request.messages[1] ==
user "hi"
**Refs:** crc-Nano.md, seq-nano-run-loop.md#1.1

## Test: MaxSteps budget
**Purpose:** R2543, R2544 — loop stops cleanly when the model never returns a
final answer
**Input:** Nano with MaxSteps=3 against a fake server that always returns
a tool_call; ApproveAll=true; tool returns "ok"
**Expected:** Run returns ("stopped: too many tool calls", history, nil)
after 3 iterations; history contains 3 assistant + 3 tool messages
**Refs:** crc-Nano.md, seq-nano-run-loop.md#3

## Test: REPL nil ReadLineFunc fallback
**Purpose:** R2503 — passing nil falls back to bufio on Stdin
**Input:** REPL with nil readLine, Stdin pre-loaded with ":q\n"
**Expected:** REPL returns nil without hanging
**Refs:** crc-Nano.md
