#!/bin/bash
# CRC: crc-RecallAgent.md | Seq: seq-recall-agent.md#3 | R2859, R2771, R2941
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
    if echo "$CMD" | grep -qE '^\s*~/\.ark/ark\s+connections\s+recall\s+(next|surface|recommend|close|finding)(\s|$)'; then
        exit 0
    fi
    # Directed-hunt search verbs (read-only) the search crank handle uses
    # (R2941). Only `search` and `chunks` — mutating verbs stay denied.
    if echo "$CMD" | grep -qE '^\s*~/\.ark/ark\s+(search|chunks)(\s|$)'; then
        exit 0
    fi
    # `next` blocks, so the harness backgrounds it; allow reading the
    # backgrounded command's output file. Single file arg only — the
    # trailing `\s*$` rejects chaining (`cat x; rm y`) and redirection.
    if echo "$CMD" | grep -qE '^\s*cat\s+\S+\s*$'; then
        exit 0
    fi
    echo "BLOCKED: recall-agent may run only — ark connections recall { next | surface | recommend | close | finding }, the read-only ark { search | chunks } for a directed hunt, plus \`cat <file>\`. Run \`~/.ark/ark connections recall next <your nonce>\` and follow what it returns." >&2
    exit 2
fi

if [ "$TOOL" = Read ]; then
    FP=$(echo "$INPUT" | jq -r '.tool_input.file_path')
    # The one keyhole (R2897): the curation doc `next` materialized for this
    # fire, under .../recall-curation/. Everything else stays denied.
    if echo "$FP" | grep -qE '/recall-curation/curation-[^/]*\.md$'; then
        exit 0
    fi
    echo "BLOCKED: the Read tool is permitted ONLY for the curation doc \`next\` names (a path under .../recall-curation/). Run \`~/.ark/ark connections recall next --session <S> <your nonce>\`, then Read the curation file it points you to." >&2
    exit 2
fi

echo "BLOCKED: ark-recall-agent has no access to $TOOL. Run \`~/.ark/ark connections recall next <your nonce>\` and follow its output." >&2
exit 2
