#!/bin/bash
# PreToolUse hook for ark-hermes: only allow ~/.ark/ark commands
# Denies Read, Grep, Glob, and non-ark Bash commands

INPUT=$(cat)
TOOL=$(echo "$INPUT" | jq -r '.tool_name')

if [ "$TOOL" = Bash ]; then
    CMD=$(echo "$INPUT" | jq -r '.tool_input.command')
    if echo "$CMD" | grep -q '^\s*~/.ark/ark\b\|^\s*\$HOME/.ark/ark\b\|^\s*/home/[^/]*/.ark/ark\b'; then
      exit 0  # allow ark commands
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
