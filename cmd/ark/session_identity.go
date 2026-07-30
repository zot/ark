package main

// CRC: crc-CLITree.md | R3313, R3314, R3315, R3316, R3320

import (
	"context"
	"fmt"
	"os"

	ucli "github.com/urfave/cli/v3"
	ark "github.com/zot/ark"
)

const (
	// claudeSessionEnv is the variable Claude Code exports into an agent's shell,
	// naming the session that shell belongs to.
	claudeSessionEnv = "CLAUDE_CODE_SESSION_ID"

	// forceSessionFlag is the global override (R3316). Global rather than
	// per-node because the *need* is declared per node: a per-node flag would
	// have to be added by hand to each declaring node, which is the enumeration
	// this design replaces.
	forceSessionFlag = "force-session"

	// clientSessionWireField is the query/body parameter the resolved identity
	// travels in (R3317) — deliberately not `session`, so one word stops
	// meaning both "who am I" and "which queue" depending on the verb. Aliased
	// from the server-side constant so the two ends of the wire cannot drift.
	clientSessionWireField = ark.ClientSessionField

	// clientSessionNeedKey marks a node as acting *as* the calling session, and
	// clientSessionValueKey holds the value its Before resolved. Both live in
	// the node's Metadata so the tree itself carries the knowledge (R3313).
	clientSessionNeedKey  = "ark.clientSession.needed"
	clientSessionValueKey = "ark.clientSession.value"
)

// resolveClientSession answers "which session is calling me?" (R3314).
//
// `--force-session` wins when given, otherwise the live
// `$CLAUDE_CODE_SESSION_ID`. The environment is preferred over anything typed
// because a typed value can only have arrived as text, and Claude Code may
// re-host a running session with `--session-id <new> --fork-session --resume
// <old>.jsonl` — which replays the earlier conversation, including an
// already-rendered `sessionid=<old>` header, into a process whose real id is
// `<new>`. That header is a skill-template substitution resolved at *load* time,
// a snapshot; the variable is read from the live process, which is the only
// party that knows its own identity.
//
// Why this matters more than it looks: a consumer that subscribes under a stale
// id does not fail. The watcher keys sessions off the JSONL *filename*, so it
// publishes for `<new>` while the consumer waits on `<old>` — blocking forever
// on a channel nobody writes to, which is indistinguishable from a corpus with
// nothing to say.
//
// Neither resolving is an error naming the remedy (R3315), never an empty
// string: state A returned empty and let each caller's own required-flag message
// fire, which here would name a `--session` flag that no longer exists.
func resolveClientSession(force string) (string, error) {
	if force != "" {
		return force, nil
	}
	if env := os.Getenv(claudeSessionEnv); env != "" {
		return env, nil
	}
	return "", fmt.Errorf(
		"cannot tell which session is calling: $%s is unset (not an agent shell?); pass --%s ID",
		claudeSessionEnv, forceSessionFlag)
}

// needsClientSession declares that a node acts as the calling session, and
// installs the resolution that runs before its Action (R3313, R3320).
//
// The declaration is what makes coverage structural. State A applied the rule
// body by body, so the failure mode was forgetting one call site — silent, and
// shaped exactly like the bug the rule exists to prevent. `ark luhmann events`
// was in fact missed that way, despite R3145 routing events onto the *caller's*
// own tube. Here a declaring node whose identity cannot be resolved never
// reaches its body at all, because urfave abandons the Action when Before
// errors.
func needsClientSession(cmd *ucli.Command) *ucli.Command {
	if cmd.Metadata == nil {
		cmd.Metadata = map[string]any{}
	}
	cmd.Metadata[clientSessionNeedKey] = true
	inner := cmd.Before
	cmd.Before = func(ctx context.Context, c *ucli.Command) (context.Context, error) {
		session, err := resolveClientSession(c.String(forceSessionFlag))
		if err != nil {
			return ctx, err
		}
		if c.Metadata == nil {
			c.Metadata = map[string]any{}
		}
		c.Metadata[clientSessionValueKey] = session
		if inner != nil {
			return inner(ctx, c)
		}
		return ctx, nil
	}
	return cmd
}

// clientSession returns the identity resolved for this node. Non-empty for any
// node wrapped in needsClientSession, because its Before either resolved a value
// or failed the command before the Action ran (R3320).
func clientSession(c *ucli.Command) string {
	if c.Metadata == nil {
		return ""
	}
	s, _ := c.Metadata[clientSessionValueKey].(string)
	return s
}

// declaresClientSession reports whether a node declared the need. Read by the
// structural tests, which assert against the real tree rather than grepping
// ark's own source the way state A's guard had to.
func declaresClientSession(cmd *ucli.Command) bool {
	if cmd == nil || cmd.Metadata == nil {
		return false
	}
	declared, _ := cmd.Metadata[clientSessionNeedKey].(bool)
	return declared
}
