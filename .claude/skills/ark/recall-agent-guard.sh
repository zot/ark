#!/bin/bash
# CRC: crc-RecallAgent.md | Seq: seq-recall-agent.md#3 | R2770, R2771
# PreToolUse hook for ark-recall-agent. Hermetic seal: only the four
# `ark connections recall` verbs and `ark fetch tmp://ARK-RECALL/...`
# are permitted. The `Read` denial doubles as the agent's runway —
# the stderr template names the canonical `ark fetch` command so the
# agent retries via the allowed path (fumble-onboarding pattern).

INPUT=$(cat)
TOOL=$(echo "$INPUT" | jq -r '.tool_name')

if [ "$TOOL" = Bash ]; then
    CMD=$(echo "$INPUT" | jq -r '.tool_input.command')
    # ark fetch tmp://ARK-RECALL/curation-*
    if echo "$CMD" | grep -qE '^\s*~/\.ark/ark\s+fetch\s+tmp://ARK-RECALL/curation-'; then
        exit 0
    fi
    # ark connections recall { surface | recommend | close }
    if echo "$CMD" | grep -qE '^\s*~/\.ark/ark\s+connections\s+recall\s+(surface|recommend|close)\s'; then
        exit 0
    fi
    echo "BLOCKED: only ark fetch tmp://ARK-RECALL/curation-<F> and ark connections recall { surface | recommend | close } are permitted. See ~/.ark/skills/ark-recall.md." >&2
    exit 2
fi

# Read is the runway. Tell the agent to use ark fetch instead.
if [ "$TOOL" = Read ]; then
    echo "BLOCKED: use \`~/.ark/ark fetch tmp://ARK-RECALL/curation-<SESSION>-<FIRE>\` to read the curation doc. The Read tool is denied for recall-agent. See ~/.ark/skills/ark-recall.md." >&2
    exit 2
fi

echo "BLOCKED: ark-recall-agent has no access to $TOOL. See ~/.ark/skills/ark-recall.md." >&2
exit 2
