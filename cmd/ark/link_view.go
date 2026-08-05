package main

// CRC: crc-CLITree.md | R3375, R3378, R3379

import (
	"context"
	"fmt"

	ark "github.com/zot/ark"

	ucli "github.com/urfave/cli/v3"
)

const (
	// linksFlag and nolinksFlag are the two ends of the link-view axis. Global
	// rather than per-node for the reason --force-session is (R3316, R3379):
	// applicability is a property of the command, so one global cannot drift
	// from the set of nodes honoring it, while a per-node flag has to be added
	// by hand to each and will eventually miss one.
	linksFlag   = "links"
	nolinksFlag = "nolinks"

	// linkViewNeedKey marks a node as emitting paths *as addresses*, and
	// linkViewValueKey holds the view its Before resolved. Both live in the
	// node's Metadata so the tree itself carries the knowledge (R3313).
	linkViewNeedKey  = "ark.linkView.needed"
	linkViewValueKey = "ark.linkView.value"
)

// resolveLinkView answers "which view was asked for?" — and refuses the
// contradiction rather than silently preferring one.
//
// --links and --nolinks are opposite ends of one axis (R3378), so giving both
// is not a preference to resolve but a question with no answer. Erroring is the
// same choice R3354 made for a symlinked source `dir`: where two readings are
// equally defensible, refuse and make the caller say which they meant.
// CRC: crc-CLITree.md | R3375, R3378, R3379
func resolveLinkView(links, nolinks bool) (ark.LinkView, error) {
	switch {
	case links && nolinks:
		return ark.LinkViewDefault, fmt.Errorf("--%s and --%s are opposite ends of one axis; give at most one", linksFlag, nolinksFlag)
	case links:
		return ark.LinkViewExpand, nil
	case nolinks:
		return ark.LinkViewCanonical, nil
	}
	return ark.LinkViewDefault, nil
}

// emitsAddresses marks a node as one that emits paths as addresses, so the
// link-view selector applies to it (R3379).
//
// The declaration is the point. Nodes that emit *content* rather than
// addresses — search above all — must NOT be wrapped, because an alias is a way
// to reach a file rather than something the file says, and propagating aliases
// into results would return the same passage once per address (R3377). Wrapping
// is therefore a deliberate statement about what a command answers with, and
// the structural test reads this rather than grepping ark's own source.
func emitsAddresses(cmd *ucli.Command) *ucli.Command {
	if cmd.Metadata == nil {
		cmd.Metadata = map[string]any{}
	}
	cmd.Metadata[linkViewNeedKey] = true
	inner := cmd.Before
	cmd.Before = func(ctx context.Context, c *ucli.Command) (context.Context, error) {
		view, err := resolveLinkView(c.Bool(linksFlag), c.Bool(nolinksFlag))
		if err != nil {
			return ctx, err
		}
		if c.Metadata == nil {
			c.Metadata = map[string]any{}
		}
		c.Metadata[linkViewValueKey] = view
		currentLinkView = view
		if inner != nil {
			return inner(ctx, c)
		}
		return ctx, nil
	}
	return cmd
}

// canonicalizePatterns maps path patterns into the canonical namespace, which
// is the only namespace `--status` answers in (R3376).
//
// The plain listing must NOT do this. It matches in the *address* namespace and
// keeps whatever prefix the caller wrote (R3364), so resolving there would
// answer `<link>/**` with canonical paths and destroy the namespace-preserving
// property. `--status` is the opposite view by design — the one that says what
// each file *is*, and therefore the way to discover that two listed addresses
// are one file.
//
// Anchors before resolving because resolution is prefix-matching against
// absolute tracked paths: a relative pattern has no prefix to match and would
// silently pass through unresolved. Anchoring is idempotent on absolute
// patterns, so the later anchor inside filterPaths is a no-op.
// CRC: crc-CLITree.md | R3376
func canonicalizePatterns(lt *ark.LinkTable, patterns []string) []string {
	if lt == nil || len(patterns) == 0 {
		return patterns
	}
	anchored := anchorGlobsToCwd(patterns)
	out := make([]string, len(anchored))
	for i, p := range anchored {
		out[i] = lt.ResolvePattern(p)
	}
	return out
}

// currentLinkView is the view resolved for the running node, read by handlers
// that take raw argv and therefore never see the *ucli.Command. Set by
// emitsAddresses' Before, which runs before any Action.
//
// A package global for the same reason arkDir is one: the flat handlers are
// argv-shaped by R3204 and cannot be handed the command. It is deliberately
// NOT set for nodes that never declared the need, so a command that does not
// emit addresses cannot consume the flag by accident.
var currentLinkView ark.LinkView

// linkViewOf returns the view resolved for a node, or the default when the node
// never declared the need. Used by the structural tests.
func linkViewOf(c *ucli.Command) ark.LinkView {
	if c == nil || c.Metadata == nil {
		return ark.LinkViewDefault
	}
	v, _ := c.Metadata[linkViewValueKey].(ark.LinkView)
	return v
}

// declaresLinkView reports whether a node declared that it emits addresses.
// Read by the structural tests, which assert against the real tree.
func declaresLinkView(cmd *ucli.Command) bool {
	if cmd == nil || cmd.Metadata == nil {
		return false
	}
	declared, _ := cmd.Metadata[linkViewNeedKey].(bool)
	return declared
}
