---
name: search-expansion
description: "Sidecar agent for spectral search. Spawns a persistent Haiku agent that loops: wait for requests, expand, fuzzy match, curate, search, post results. Invoke when the user wants spectral search running."
---

# Search Expansion Sidecar

Launch the `ark-expansion` agent to handle spectral search requests.

## Starting

```
Agent(
  subagent_type="ark-expansion",
  description="Spectral search expansion loop",
  run_in_background=true,
  prompt="Start the expansion loop now."
)
```

The agent runs until the session ends. It is hermetically sealed
— the guard script (`expansion-guard.sh`) only allows `~/.ark/ark`
commands. No curl, no file access, no other tools.

## How It Works

The agent loops forever:
1. `~/.ark/ark search expand --wait` — lotto tube, blocks for requests
2. Haiku suggests alternative tags/values (inline, no tool call)
3. `echo JSON | ~/.ark/ark search expand --fuzzy` — match V records
4. Haiku curates the matches (inline)
5. `echo JSON | ~/.ark/ark search expand --search` — get chunk results
6. `echo JSON | ~/.ark/ark search expand --result ID` — post back

## CLI Reference

| Command | Description |
|---------|-------------|
| `ark search expand --wait` | Block until requests arrive (lotto tube) |
| `ark search expand --fuzzy` | Fuzzy match alternatives from stdin against V records |
| `ark search expand --search` | Search curated pairs from stdin, return chunks |
| `ark search expand --result ID` | Post result JSON from stdin for request ID |
| `ark search expand --error ID=MSG` | Post error for request ID |
| `ark search expand TAG [VALUE]` | Queue request and wait for result (client use) |

## Architecture

```
Browser → POST /search/expand → queue
                                  ↓
              ark-expansion agent: --wait (lotto tube)
                                  ↓
              Haiku: suggest alternatives (inline)
                                  ↓
              --fuzzy: match V records + resolve paths
                                  ↓
              Haiku: curate matches (inline)
                                  ↓
              --search: get chunk-level results
                                  ↓
              --result: post back to server
                                  ↓
Browser ← GET /search/expand/result/{id}
```

Hermetically sealed. The agent only uses `~/.ark/ark` commands.
The guard script blocks everything else.
