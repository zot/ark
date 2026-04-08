#!/bin/bash
# PreToolUse hook for search-expansion agent: only allow ~/.ark/ark commands
# Hermetic seal — the agent can ONLY run ark CLI commands, nothing else

INPUT=$(cat)
TOOL=$(echo "$INPUT" | jq -r '.tool_name')

if [ "$TOOL" = Bash ]; then
    CMD=$(echo "$INPUT" | jq -r '.tool_input.command')
    # Allow only ~/.ark/ark commands (no pipes, no heredocs)
    if echo "$CMD" | grep -q '^\s*~/.ark/ark\b\|^\s*\$HOME/.ark/ark\b\|^\s*/home/[^/]*/.ark/ark\b'; then
        # Reject pipes and heredocs even in ark commands
        if echo "$CMD" | grep -q '<<\|[|]'; then
            echo "BLOCKED: No pipes or heredocs. Pass JSON as flag arguments: --fuzzy 'JSON' --search 'JSON' --result ID 'JSON'" >&2
            exit 2
        fi
        exit 0
    fi
fi

echo "BLOCKED: Only ~/.ark/ark commands are allowed. Use ~/.ark/ark search expand subcommands." >&2
exit 2
