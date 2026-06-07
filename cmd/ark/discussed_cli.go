package main

// The `ark discussed` command group, migrated to the urfave/cli v3
// command tree (Stage 2 of the CLI urfave migration). discussed {add,
// list, clear, prune}: per-session recall dedup state (RD records). Each
// node declares its flags so --help is generated; each Action keeps the
// existing server-proxy-or-cold-start body unchanged (seq-cli-dispatch.md).
// urfave parses flags intermixed with positionals, so the legacy
// reorderArgsForFlagSet shim is no longer needed here. The shared parsers
// parseDiscussedTagArg / parseDiscussedList stay in main.go
// (parseDiscussedList is also used by `connections recall --discussed`).
// (R2650-R2654, R2662; see crc-CLITree.md.)

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"time"

	ucli "github.com/urfave/cli/v3"
	"github.com/zot/ark"
)

// CRC: crc-CLITree.md, crc-CLI.md | Seq: seq-cli-urfave.md#3.3 | R2650, R2651, R2652, R2653
func discussedCommand() *ucli.Command {
	return &ucli.Command{
		Name:  "discussed",
		Usage: "per-session recall dedup state (add, list, clear, prune)",
		Commands: []*ucli.Command{
			{
				Name:      "add",
				Usage:     "mark tags discussed in a session (server or cold-start)",
				ArgsUsage: "@tag[:value] [@tag[:value] ...]",
				Flags: []ucli.Flag{
					&ucli.StringFlag{Name: "session", Usage: "session ID (required)"},
				},
				Action: discussedAddAction,
			},
			{
				Name:  "list",
				Usage: "list unexpired discussed tags for a session",
				Flags: []ucli.Flag{
					&ucli.StringFlag{Name: "session", Usage: "session ID (required)"},
					&ucli.StringFlag{Name: "since", Usage: "keep entries newer than DUR (e.g. 30m, 24h)"},
					&ucli.BoolFlag{Name: "json", Usage: "emit JSON array instead of @-line markdown"},
				},
				Action: discussedListAction,
			},
			{
				Name:  "clear",
				Usage: "delete every discussed-tag record under one session",
				Flags: []ucli.Flag{
					&ucli.StringFlag{Name: "session", Usage: "session ID (required)"},
				},
				Action: discussedClearAction,
			},
			{
				Name:  "prune",
				Usage: "sweep RD records across all sessions, dropping expired",
				Flags: []ucli.Flag{
					&ucli.StringFlag{Name: "ttl", Usage: "override TTL (e.g. 24h, 7d); empty uses [recall].discussed_ttl"},
				},
				Action: discussedPruneAction,
			},
		},
	}
}

// CRC: crc-CLITree.md, crc-CLI.md | Seq: seq-discussed.md#1.4 | R2650, R2662
func discussedAddAction(_ context.Context, c *ucli.Command) error {
	session := c.String("session")
	if session == "" {
		fatal(fmt.Errorf("session ID required"))
	}
	posArgs := c.Args().Slice()
	if len(posArgs) == 0 {
		fatal(fmt.Errorf("no tags specified"))
	}
	tags := make([]ark.Discussed, 0, len(posArgs))
	for _, a := range posArgs {
		tag, value, err := parseDiscussedTagArg(a)
		if err != nil {
			fatal(err)
		}
		tags = append(tags, ark.Discussed{Tag: tag, Value: value})
	}
	if client := serverClient(arkDir); client != nil {
		body := map[string]any{"session": session, "tags": tags}
		if err := proxyOK(client, "POST", "/discussed/add", body); err != nil {
			fatal(err)
		}
		return nil
	}
	withDB(func(db *ark.DB) {
		for _, t := range tags {
			if err := db.AddDiscussed(session, t.Tag, t.Value); err != nil {
				fatal(err)
			}
		}
	})
	return nil
}

// CRC: crc-CLITree.md, crc-CLI.md | R2651
func discussedListAction(_ context.Context, c *ucli.Command) error {
	session := c.String("session")
	if session == "" {
		fatal(fmt.Errorf("session ID required"))
	}
	sinceStr := c.String("since")
	var since time.Duration
	if sinceStr != "" {
		d, err := time.ParseDuration(sinceStr)
		if err != nil {
			fatal(fmt.Errorf("invalid --since: %w", err))
		}
		since = d
	}

	var entries []ark.Discussed
	if client := serverClient(arkDir); client != nil {
		body := map[string]any{"session": session}
		if sinceStr != "" {
			body["since"] = sinceStr
		}
		if err := proxyDecode(client, "POST", "/discussed/list", body, &entries); err != nil {
			fatal(err)
		}
	} else {
		withDB(func(db *ark.DB) {
			es, err := db.ListDiscussed(session, since)
			if err != nil {
				fatal(err)
			}
			entries = es
		})
	}

	if c.Bool("json") {
		data, err := json.MarshalIndent(entries, "", "  ")
		if err != nil {
			fatal(err)
		}
		fmt.Println(string(data))
		return nil
	}
	for _, e := range entries {
		if e.Value == "" {
			fmt.Printf("@%s\n", e.Tag)
		} else {
			fmt.Printf("@%s: %s\n", e.Tag, e.Value)
		}
	}
	return nil
}

// CRC: crc-CLITree.md, crc-CLI.md | R2652
func discussedClearAction(_ context.Context, c *ucli.Command) error {
	session := c.String("session")
	if session == "" {
		fatal(fmt.Errorf("session ID required"))
	}
	var count int
	if client := serverClient(arkDir); client != nil {
		var resp struct {
			Count int `json:"count"`
		}
		if err := proxyDecode(client, "POST", "/discussed/clear",
			map[string]any{"session": session}, &resp); err != nil {
			fatal(err)
		}
		count = resp.Count
	} else {
		withDB(func(db *ark.DB) {
			n, err := db.ClearDiscussed(session)
			if err != nil {
				fatal(err)
			}
			count = n
		})
	}
	fmt.Fprintf(os.Stderr, "cleared %d entries\n", count)
	return nil
}

// CRC: crc-CLITree.md, crc-CLI.md | R2653
func discussedPruneAction(_ context.Context, c *ucli.Command) error {
	ttlStr := c.String("ttl")
	var ttl time.Duration
	if ttlStr != "" {
		d, err := time.ParseDuration(ttlStr)
		if err != nil {
			fatal(fmt.Errorf("invalid --ttl: %w", err))
		}
		ttl = d
	}

	var count int
	if client := serverClient(arkDir); client != nil {
		body := map[string]any{}
		if ttlStr != "" {
			body["ttl"] = ttlStr
		}
		var resp struct {
			Count int `json:"count"`
		}
		if err := proxyDecode(client, "POST", "/discussed/prune", body, &resp); err != nil {
			fatal(err)
		}
		count = resp.Count
	} else {
		withDB(func(db *ark.DB) {
			n, err := db.PruneDiscussed(ttl)
			if err != nil {
				fatal(err)
			}
			count = n
		})
	}
	fmt.Fprintf(os.Stderr, "pruned %d entries\n", count)
	return nil
}
