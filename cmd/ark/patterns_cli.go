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
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"

	ucli "github.com/urfave/cli/v3"
	"github.com/zot/ark"
)

// CRC: crc-CLITree.md, crc-CLI.md | Seq: seq-cli-urfave.md#3.3 | R3269, R3270, R3272, R3273
func patternsCommand() *ucli.Command {
	return &ucli.Command{
		Name:  "patterns",
		Usage: "personal pattern collection (list, show, add)",
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
				// R3297: reading, not priming — the frontmatter has already been
				// paid for by `list`, so it is not reprinted.
				Name:      "show",
				Usage:     "print one pattern's body, without its frontmatter",
				ArgsUsage: "SLUG",
				Action:    patternsShowAction,
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
// index, and exits on an ark.toml that does not load (patternsCollectionFor).
// CRC: crc-CLITree.md | R3268, R3270
func patternsCollection() *ark.Patterns {
	p, err := patternsCollectionFor(arkDir)
	if err != nil {
		fatal(err)
	}
	return p
}

// patternsCollectionFor resolves the collection for the ark dir. Only a
// missing ark.toml falls back to the default (R3271); one that does not load
// is an error, never a reason to show another collection.
// CRC: crc-CLITree.md | R3268, R3271, R3650
func patternsCollectionFor(dir string) (*ark.Patterns, error) {
	cfg, err := ark.LoadConfig(filepath.Join(dir, "ark.toml"))
	if errors.Is(err, fs.ErrNotExist) {
		return ark.NewPatterns(filepath.Join(dir, "personal-patterns")), nil
	}
	if err != nil {
		return nil, err
	}
	return ark.NewPatterns(cfg.PatternsRoot()), nil
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

// patternsShowAction prints one pattern's body for reading.
// CRC: crc-CLI.md | R3297
func patternsShowAction(_ context.Context, c *ucli.Command) error {
	if c.Args().Len() != 1 {
		return fmt.Errorf("usage: ark patterns show SLUG")
	}
	return patternsCollection().Show(os.Stdout, c.Args().First())
}
