package main

// The `ark ext` command group — author @ext routings into a target's
// mirror file from a plain session (the CLI counterpart to the
// Frictionless workshop's mcp.setExtTag/mcp.removeExtTag). set/add/
// remove each proxy to the running server (POST /ext/{set,add,remove})
// or open the index exclusively when stopped, mirroring the `config`
// add/remove dispatch. All three act only on the target's mirror file.
// (R3048; see crc-CLITree.md.)

import (
	"context"
	"fmt"

	ucli "github.com/urfave/cli/v3"
	"github.com/zot/ark"
)

// CRC: crc-CLITree.md | Seq: seq-ext-author.md#3 | R3048, R3056
func extCommand() *ucli.Command {
	return &ucli.Command{
		Name:  "ext",
		Usage: "author @ext routings in a target's mirror file",
		Commands: []*ucli.Command{
			{
				Name:      "set",
				Usage:     "replace all values of <tag> on <target> with <value>",
				ArgsUsage: "<target> <tag> <value>",
				Action:    extSetAction,
			},
			{
				Name:      "add",
				Usage:     "append a <value> for <tag> on <target> (multi-value; exact dup no-ops)",
				ArgsUsage: "<target> <tag> <value>",
				Action:    extAddAction,
			},
			{
				Name:      "remove",
				Usage:     "remove <tag> routings on <target> (all values, or just <value>)",
				ArgsUsage: "<target> <tag> [value]",
				Action:    extRemoveAction,
			},
			{
				Name:      "candidate",
				Usage:     "propose <tag> on <target> as an @ext-candidate",
				ArgsUsage: "<target> <tag> [value]",
				Flags: []ucli.Flag{
					&ucli.StringFlag{Name: "insight", Usage: "quoted rationale carried with the proposal"},
				},
				Action: extCandidateAction,
			},
			{
				Name:      "accept",
				Usage:     "commit matching @ext-candidate(s) on <target> to @ext",
				ArgsUsage: "<target> <tag> [value]",
				Action:    extAcceptAction,
			},
			{
				Name:      "reject",
				Usage:     "reject matching @ext-candidate(s) on <target> (durable @ext-judgment)",
				ArgsUsage: "<target> <tag> [value]",
				Action:    extRejectAction,
			},
		},
	}
}

// extProxyOrLocal runs an @ext mirror mutation: proxy to the running
// server (POST /ext/<verb>) when one is up, else open the index
// exclusively and call local. Mirrors the config add/remove dispatch.
// insight rides along only for candidate (empty for the other verbs).
// CRC: crc-CLITree.md | R3048, R3056
func extProxyOrLocal(verb, target, tag, value, insight string, local func(*ark.DB) error) {
	if client := serverClient(arkDir); client != nil {
		body := map[string]string{"target": target, "tag": tag, "value": value}
		if insight != "" {
			body["insight"] = insight
		}
		if err := proxyOK(client, "POST", "/ext/"+verb, body); err != nil {
			fatal(err)
		}
		return
	}
	withExclusiveDB(func(d *ark.DB) {
		if err := local(d); err != nil {
			fatal(err)
		}
	})
}

// CRC: crc-CLITree.md | Seq: seq-ext-author.md#3 | R3048
func extSetAction(_ context.Context, c *ucli.Command) error {
	target, tag, value := extArgs(c, true)
	extProxyOrLocal("set", target, tag, value, "", func(d *ark.DB) error { return d.SetExtTag(target, tag, value) })
	return nil
}

// CRC: crc-CLITree.md | Seq: seq-ext-author.md#3 | R3048
func extAddAction(_ context.Context, c *ucli.Command) error {
	target, tag, value := extArgs(c, true)
	extProxyOrLocal("add", target, tag, value, "", func(d *ark.DB) error { return d.AddExtTag(target, tag, value) })
	return nil
}

// CRC: crc-CLITree.md | Seq: seq-ext-author.md#3 | R3048
func extRemoveAction(_ context.Context, c *ucli.Command) error {
	target, tag, value := extArgs(c, false)
	extProxyOrLocal("remove", target, tag, value, "", func(d *ark.DB) error { return d.RemoveExtTag(target, tag, value) })
	return nil
}

// CRC: crc-CLITree.md | Seq: seq-ext-author.md#4.2 | R3056
func extCandidateAction(_ context.Context, c *ucli.Command) error {
	target, tag, value := extArgs(c, false)
	insight := c.String("insight")
	extProxyOrLocal("candidate", target, tag, value, insight, func(d *ark.DB) error {
		return d.CandidateExtTag(target, tag, value, insight)
	})
	return nil
}

// CRC: crc-CLITree.md | Seq: seq-ext-author.md#4.3 | R3056
func extAcceptAction(_ context.Context, c *ucli.Command) error {
	target, tag, value := extArgs(c, false)
	extProxyOrLocal("accept", target, tag, value, "", func(d *ark.DB) error { return d.AcceptExtTag(target, tag, value) })
	return nil
}

// CRC: crc-CLITree.md | Seq: seq-ext-author.md#4.4 | R3056
func extRejectAction(_ context.Context, c *ucli.Command) error {
	target, tag, value := extArgs(c, false)
	extProxyOrLocal("reject", target, tag, value, "", func(d *ark.DB) error { return d.RejectExtTag(target, tag, value) })
	return nil
}

// extArgs pulls <target> <tag> [value] positionals. requireValue
// fatals when <value> is absent (set/add need it; remove treats it as
// an optional filter, defaulting to "" = all values).
// CRC: crc-CLITree.md | R3048
func extArgs(c *ucli.Command, requireValue bool) (target, tag, value string) {
	args := c.Args()
	need := "<target> <tag> [value]"
	if requireValue {
		need = "<target> <tag> <value>"
	}
	if args.Len() < 2 || (requireValue && args.Len() < 3) {
		fatal(fmt.Errorf("usage: ark ext %s %s", c.Name, need))
	}
	// Args().Get(n) returns "" when out of range, so value is empty
	// for `remove <target> <tag>` (remove-all).
	return args.Get(0), args.Get(1), args.Get(2)
}
