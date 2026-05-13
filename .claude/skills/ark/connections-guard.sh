#!/bin/bash
# PreToolUse hook for find-connections agent: only allow ~/.ark/ark commands.
# Hermetic seal — the agent can ONLY run ark CLI commands.
# Pipes from `echo ... | ~/.ark/ark connections --result ID` are allowed
# because the protocol uses stdin for the result payload.

INPUT=$(cat)
TOOL=$(echo "$INPUT" | jq -r '.tool_name')

if [ "$TOOL" = Bash ]; then
    CMD=$(echo "$INPUT" | jq -r '.tool_input.command')
    # Allow the echo-into-ark pipe pattern for --result.
    if echo "$CMD" | grep -qE '^\s*echo[[:space:]].*\|[[:space:]]*(~/.ark/ark|\$HOME/.ark/ark|/home/[^/]*/.ark/ark)\b'; then
        exit 0
    fi
    # Allow plain ~/.ark/ark commands.
    if echo "$CMD" | grep -qE '^\s*(~/.ark/ark|\$HOME/.ark/ark|/home/[^/]*/.ark/ark)\b'; then
        if echo "$CMD" | grep -q '<<'; then
            echo "BLOCKED: No heredocs. Use a piped echo for --result payloads, or pass JSON via flag." >&2
            exit 2
        fi
        exit 0
    fi
fi

echo "BLOCKED: Only ~/.ark/ark commands are allowed. Use ~/.ark/ark connections subcommands." >&2
exit 2
