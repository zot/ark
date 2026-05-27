# Luhmann supervisor lifecycle

How the orchestrator session records a managed subagent's
spawn, observes its exit, classifies the exit, and decides
whether to respawn or pause.

The orchestrator session is a Claude Code session running the
`luhmann` skill. It calls the `ark luhmann` and `ark monitor`
verbs; this Go side never observes the Task completion directly —
the Claude Code harness does, and the skill drives the next step.

```
1. Spawn managed subagent
   1.1. Orchestrator (skill)         → reserve nonce N (e.g. via `ark connections recall reserve-nonce`)
   1.2. Orchestrator (skill)         → launch Task(subagent_type, description="... nonce <N>", task_id=T, run_in_background=true)
   1.3. Orchestrator (skill)         → `ark luhmann spawn-record --class C --nonce N --task-id T`
   1.4. LuhmannCLI                   → POST /luhmann/record (kind=spawn)
   1.5. Server.HandleLuhmannRecord   → tail luhmann.jsonl for prior crashes value
   1.6. Server.HandleLuhmannRecord   → enqueue write-actor append: {ts, kind:"spawn", class:C, nonce:N, task_id:T, crashes:<carried>}

2. Subagent exits (Claude Code harness notifies the orchestrator)
   2.1. Orchestrator (skill)         → `ark luhmann inspect-exit --nonce N --json`
   2.2. LuhmannCLI                   → discoverSubagentJSONL(N) via RecallAgentBuilder helper
   2.3. LuhmannCLI                   → read subagent JSONL backwards; classify as healthy / crash / unknown
   2.4. LuhmannCLI                   → emit JSON {label, last_record_kind, last_error, tokens_at_close}
   2.5. Orchestrator (skill)         → read label; pick R: context-limit (healthy) | error string (crash)

3. Record exit
   3.1. Orchestrator (skill)         → `ark luhmann exit-record --class C --nonce N --reason R`
   3.2. LuhmannCLI                   → POST /luhmann/record (kind decided from reason)
   3.3. Server.HandleLuhmannRecord   → tail luhmann.jsonl for prior crashes
   3.4a. healthy path                → kind=exit, crashes:=0, append record
   3.4b. crash path                  → kind=crash, crashes:=prev+1, backoff:=cfg.BackoffSeconds[min(crashes-1, len-1)], append record

4. Decide next step
   4.1a. healthy path                → orchestrator immediately loops back to step 1 with a fresh nonce
   4.1b. crash path with crashes < cfg.CrashPauseAfter → orchestrator sleeps `backoff` seconds, loops to step 1
   4.1c. crash path with crashes ≥ cfg.CrashPauseAfter → orchestrator calls `ark monitor pause C` and surfaces in chat
       4.1c.1. monitor (skill)       → `ark monitor pause C`
       4.1c.2. Monitor               → POST /monitor/control (kind=pause)
       4.1c.3. Server.HandleMonitorControl → append-via-write-actor {ts, kind:"pause", class:C, nonce:0}
       4.1c.4. Orchestrator (skill)  → persona-shaped chat message to the user about the pause

5. User clears pause (later)
   5.1. User (in chat)               → "go ahead and resume recall"
   5.2. Orchestrator (skill)         → `ark monitor resume C`
   5.3. Monitor                      → POST /monitor/control (kind=resume)
   5.4. Server.HandleMonitorControl  → append {ts, kind:"resume", class:C, nonce:0}
   5.5. Orchestrator (skill)         → loops back to step 1
```

The supervisor log is the source of truth — the orchestrator
re-reads it on every decision so a session crash + restart
recovers the right state from disk.
