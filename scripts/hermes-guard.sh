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

echo "Use ~/.ark/ark commands instead. ark search, ark fetch, ark files, and ark message can do everything you need."
exit 2
