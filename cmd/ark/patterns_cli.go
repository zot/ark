package main

// The `ark patterns` command group: the personal pattern collection.
//
// Both subcommands are filesystem-only and never contact the server (R3270).
// `list` runs at session start, before anything is up — a collection that
// primes only when a daemon happens to be running primes intermittently,
// which from outside is indistinguishable from inconsistent judgment. So
// there is no proxyOrLocal dispatch here, and deliberately no DB open.
//
// Distinct from `ark ui patterns`, which lists the separate Frictionless
// UI-development collection in ~/.ark/patterns/ (R3277).
// (R3269-R3273; see crc-Patterns.md, crc-CLITree.md.)

import (
	"context"
	"fmt"
	"os"
	"path/filepath"

	ucli "github.com/urfave/cli/v3"
	"github.com/zot/ark"
)

// CRC: crc-CLITree.md, crc-CLI.md | Seq: seq-cli-urfave.md#3.3 | R3269, R3270, R3272, R3273
func patternsCommand() *ucli.Command {
	return &ucli.Command{
		Name:  "patterns",
		Usage: "personal pattern collection (list, add)",
		Commands: []*ucli.Command{
			{
				Name:  "list",
				Usage: "write the injection block for the pattern collection",
				Flags: []ucli.Flag{
					&ucli.BoolFlag{Name: "json", Usage: "emit JSON array instead of the markdown block"},
				},
				Action: patternsListAction,
			},
			{
				Name:      "add",
				Usage:     "author a new pattern file",
				ArgsUsage: "SLUG",
				Flags: []ucli.Flag{
					&ucli.StringFlag{Name: "name", Usage: "display name (required)"},
					&ucli.StringFlag{Name: "summary", Usage: "one-line summary (required)"},
					&ucli.StringFlag{Name: "tags", Usage: "comma-separated tags"},
					&ucli.StringFlag{Name: "body-file", Usage: "file whose contents become the pattern body"},
				},
				Action: patternsAddAction,
			},
		},
	}
}

// patternsCollection resolves the configured collection without opening the
// index. A missing or unreadable ark.toml falls back to the default location
// rather than failing: a fresh install has no config yet, and `patterns list`
// must still work there (R3268, R3271).
// CRC: crc-CLITree.md | R3268, R3270
func patternsCollection() *ark.Patterns {
	cfg, err := ark.LoadConfig(filepath.Join(arkDir, "ark.toml"))
	if err != nil {
		return ark.NewPatterns(filepath.Join(arkDir, "personal-patterns"))
	}
	return ark.NewPatterns(cfg.PatternsRoot())
}

// CRC: crc-CLITree.md | R3269, R3272
func patternsListAction(_ context.Context, c *ucli.Command) error {
	p := patternsCollection()
	if c.Bool("json") {
		return p.WriteJSON(os.Stdout)
	}
	return p.WriteBlock(os.Stdout)
}

// CRC: crc-CLITree.md | R3273
func patternsAddAction(_ context.Context, c *ucli.Command) error {
	if c.Args().Len() != 1 {
		return fmt.Errorf("usage: ark patterns add SLUG --name NAME --summary TEXT [--tags \"a, b\"] [--body-file FILE]")
	}
	var body []byte
	if bf := c.String("body-file"); bf != "" {
		b, err := os.ReadFile(bf)
		if err != nil {
			return err
		}
		body = b
	}
	path, err := patternsCollection().Add(
		c.Args().First(), c.String("name"), c.String("summary"), c.String("tags"), body)
	if err != nil {
		return err
	}
	fmt.Println(path)
	return nil
}
