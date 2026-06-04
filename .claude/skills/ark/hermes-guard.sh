#!/bin/bash
# PreToolUse hook for ark-hermes: only allow ~/.ark/ark commands
# Denies Read, Grep, Glob, and non-ark Bash commands

INPUT=$(cat)
TOOL=$(echo "$INPUT" | jq -r '.tool_name')

if [ "$TOOL" = Bash ]; then
    CMD=$(echo "$INPUT" | jq -r '.tool_input.command')
    # Reject *shell* pipes and heredocs — but allow a '|' inside a quoted
    # --content/--issue value (legitimate in markdown tables, code, prose).
    # A crude grep can't tell a shell pipe from a pipe character inside a
    # quoted argument; shlex tokenizes the command so a quoted '|' stays
    # inside its token while a real pipe operator becomes a standalone '|'.
    # Unparseable (unbalanced quotes) → reject, the safe default.
    SHELLOP=$(printf '%s' "$CMD" | python3 -c '
import shlex, sys
try:
    toks = shlex.split(sys.stdin.read())
except ValueError:
    print("parse"); sys.exit(0)
if "|" in toks or any(t.startswith("<<") for t in toks):
    print("op")
')
    if [ "$SHELLOP" = op ]; then
        echo "BLOCKED: shell pipes/heredocs are not allowed. Put body text in a quoted --content value (a '|' inside the quotes is fine). To filter inbox, use ark's own flags (--from/--to/--unmatched), not '| grep'." >&2
        exit 2
    fi
    if [ "$SHELLOP" = parse ]; then
        echo "BLOCKED: could not parse the command (unbalanced quotes?). Re-issue with balanced quotes around --content/--issue values." >&2
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
