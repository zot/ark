#!/bin/bash
# CRC: crc-RecallAgent.md | Seq: seq-recall-agent.md#3 | R2859, R2771
# PreToolUse hook for ark-recall-agent (the long-running daemon).
# Hermetic seal: only the four recall verbs the loop uses are permitted —
#   ark connections recall next                       (the loop driver)
#   ark connections recall surface | recommend | close   (per-fire work)
# `next` absorbs subscribe / listen / files / fetch / context entirely,
# so none of those are allowed. Read / Edit / Write / network: denied.
# `next` blocks (true lotto-tube), so the harness backgrounds it; the one
# extra command allowed is `cat <file>` — a single-arg read, no chaining
# or redirection — so the agent can pick up the backgrounded output.
# The denials double as the agent's runway: they point back at `next`.

INPUT=$(cat)
TOOL=$(echo "$INPUT" | jq -r '.tool_name')

if [ "$TOOL" = Bash ]; then
    CMD=$(echo "$INPUT" | jq -r '.tool_input.command')
    if echo "$CMD" | grep -qE '^\s*~/\.ark/ark\s+connections\s+recall\s+(next|surface|recommend|close)(\s|$)'; then
        exit 0
    fi
    # `next` blocks, so the harness backgrounds it; allow reading the
    # backgrounded command's output file. Single file arg only — the
    # trailing `\s*$` rejects chaining (`cat x; rm y`) and redirection.
    if echo "$CMD" | grep -qE '^\s*cat\s+\S+\s*$'; then
        exit 0
    fi
    echo "BLOCKED: recall-agent (daemon) may run only — ark connections recall { next | surface | recommend | close }, plus \`cat <file>\` to read a backgrounded \`next\`. Run \`~/.ark/ark connections recall next <your nonce>\`; when it finishes in the background, \`cat\` its output file and act on the curation doc." >&2
    exit 2
fi

if [ "$TOOL" = Read ]; then
    echo "BLOCKED: the Read tool is denied. Read \`next\`'s output with \`cat\` instead: run \`~/.ark/ark connections recall next <your nonce>\`, end your turn, and when it completes in the background \`cat\` its output file and act on it. Loop on that." >&2
    exit 2
fi

echo "BLOCKED: ark-recall-agent has no access to $TOOL. Run \`~/.ark/ark connections recall next <your nonce>\` and follow its output." >&2
exit 2
