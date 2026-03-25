#!/bin/bash
# PreToolUse hook for ark-hermes: only allow ~/.ark/ark commands
# Denies Read, Grep, Glob, and non-ark Bash commands

INPUT=$(cat)
TOOL=$(echo "$INPUT" | jq -r '.tool_name')

if [ "$TOOL" = Bash ]; then
    CMD=$(echo "$INPUT" | jq -r '.tool_input.command')
    # Reject heredocs and pipes — even in ark commands
    if echo "$CMD" | grep -q '<<\|[|]'; then
        echo "BLOCKED: No heredocs or pipes. Use --content flag: ~/.ark/ark message new-request --from X --to Y --issue '...' --content 'body text' requests/file.md" >&2
        exit 2
    fi
    if echo "$CMD" | grep -q '^\s*~/.ark/ark\b\|^\s*\$HOME/.ark/ark\b\|^\s*/home/[^/]*/.ark/ark\b'; then
        echo >> /tmp/allowed
        echo "$INPUT" >> /tmp/allowed
      exit 0  # allow ark commands
    fi
fi

# Allow Read on requests/ paths (Write removed — use --content flag instead)
if [ "$TOOL" = Read ]; then
    FPATH=$(echo "$INPUT" | jq -r '.tool_input.file_path')
    if echo "$FPATH" | grep -qE '/requests/|^requests/'; then
        echo >> /tmp/allowed
        echo "$INPUT" >> /tmp/allowed
        exit 0
    fi
fi

echo >> /tmp/denied
echo "$INPUT" >> /tmp/denied

AGENT=$(echo "$INPUT" | jq -r '.agent_type // "messenger"')
case "$AGENT" in
  ark-searcher) SKILL="hermes-search.md" ;;
  *)            SKILL="hermes-messaging.md" ;;
esac

echo "BLOCKED. Run this command exactly: \`~/.ark/ark fetch --wrap knowledge ~/.ark/skills/$SKILL\` — then use ~/.ark/ark commands for everything." >&2
exit 2
