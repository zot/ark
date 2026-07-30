package main

// CRC: crc-CLITree.md | R3309, R3310, R3311

import (
	"fmt"
	"os"
)

// claudeSessionEnv is the variable Claude Code exports into an agent's shell,
// naming the session that shell belongs to.
const claudeSessionEnv = "CLAUDE_CODE_SESSION_ID"

// resolveOwnSession resolves a `--session` flag that names **the caller's own
// session**. It returns the session to use and, when the flag disagreed with the
// live environment, a non-empty report for the caller to print to stderr.
//
// The environment wins a disagreement (R3309). The flag's value can only have
// reached the command line through text a model typed, and Claude Code may
// re-host a running session with `--session-id <new> --fork-session --resume
// <old>.jsonl` — which replays the earlier conversation, including an
// already-interpolated `sessionid=<old>`, into a process whose real id is
// `<new>`. The environment is read from that live process, which is the only
// party that knows its own identity.
//
// Why this matters more than it looks: a consumer that subscribes under a stale
// id does not fail. The watcher keys sessions off the JSONL *filename*, so it
// publishes for `<new>` while the consumer waits on `<old>` — and the consumer
// blocks forever on a channel nobody writes to, which is indistinguishable from
// a corpus with nothing to say.
//
// The disagreement is reported rather than silently corrected (R3310): fixing
// each call while hiding the condition would leave no way to learn that sessions
// are being re-hosted at all.
//
// NOT for `connections recall next --session` (R3311) — that flag is a routing
// identity, the pool composite `<luhmann-session>-<nonce>`, deliberately no
// process's own id. Resolving it here would aim every pool secretary at its
// parent's tube.
func resolveOwnSession(flag string) (session, report string) {
	env := os.Getenv(claudeSessionEnv)
	switch {
	case env == "":
		// Outside an agent shell (a human, a script). Nothing to check against,
		// so the flag stands — including when it too is empty, leaving the
		// caller's own required-flag error to fire.
		return flag, ""
	case flag == "", flag == env:
		return env, ""
	default:
		return env, fmt.Sprintf(
			"ark: --session %s disagrees with this session's live id %s; using the live id.\n"+
				"     A re-hosted session (--fork-session --resume) replays a stale id into skill text.\n"+
				"     Omit --session and let ark resolve it.",
			flag, env)
	}
}

// applyOwnSession resolves the flag and prints any disagreement report, keeping
// the two-line dance out of every call site.
func applyOwnSession(flag string) string {
	session, report := resolveOwnSession(flag)
	if report != "" {
		fmt.Fprintln(os.Stderr, report)
	}
	return session
}
