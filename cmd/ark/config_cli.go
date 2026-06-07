package main

// The `ark config` command group, migrated to the urfave/cli v3 command
// tree (Stage 2 of the CLI urfave migration). config {show (default),
// add-source, remove-source, add-include, add-exclude, remove-pattern,
// show-why, add-strategy, recover}. Each node declares its flags so
// --help is generated (replacing the hand-rolled printConfigHelp); each
// Action keeps the existing server-proxy-or-cold-start body. The parent's
// default Action runs `show`, matching the legacy "no/unknown subcommand
// → show" dispatch. withConfig (config-only) moves here with the group;
// reorderArgs stays in main.go (shared) — urfave intermixes flags and
// positionals natively, so the actions don't need it.
// (R85, R143-R147, R159, R1569; see crc-CLITree.md.)

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"

	ucli "github.com/urfave/cli/v3"
	"github.com/zot/ark"
)

// CRC: crc-CLITree.md, crc-CLI.md | Seq: seq-cli-urfave.md#3.3 | R85, R143, R144, R145, R146, R147, R159, R1569
func configCommand() *ucli.Command {
	sourceFlag := func() []ucli.Flag {
		return []ucli.Flag{&ucli.StringFlag{Name: "source", Usage: "source directory (empty for global)"}}
	}
	return &ucli.Command{
		Name:   "config",
		Usage:  "show or modify configuration",
		Action: configShowAction, // default: `config` alone / unknown args → show
		Commands: []*ucli.Command{
			{
				Name:      "add-source",
				Usage:     "add a source directory (or glob pattern) to ark.toml",
				ArgsUsage: "<dir>",
				Action:    configAddSourceAction,
			},
			{
				Name:      "remove-source",
				Usage:     "remove a source directory from ark.toml",
				ArgsUsage: "<dir>",
				Action:    configRemoveSourceAction,
			},
			{
				Name:      "add-include",
				Usage:     "add an include pattern (global, or per-source with --source)",
				ArgsUsage: "<pattern>",
				Flags:     sourceFlag(),
				Action:    configAddIncludeAction,
			},
			{
				Name:      "add-exclude",
				Usage:     "add an exclude pattern (global, or per-source with --source)",
				ArgsUsage: "<pattern>",
				Flags:     sourceFlag(),
				Action:    configAddExcludeAction,
			},
			{
				Name:      "remove-pattern",
				Usage:     "remove an include/exclude pattern (global, or per-source with --source)",
				ArgsUsage: "<pattern>",
				Flags:     sourceFlag(),
				Action:    configRemovePatternAction,
			},
			{
				Name:      "show-why",
				Usage:     "explain why a file is included, excluded, or unresolved",
				ArgsUsage: "<file-path>",
				Action:    configShowWhyAction,
			},
			{
				Name:      "add-strategy",
				Usage:     "map a file glob to a chunking strategy",
				ArgsUsage: "<pattern> <strategy>",
				Action:    configAddStrategyAction,
			},
			{
				Name:   "recover",
				Usage:  "recover ark.toml from stored config in the database",
				Action: configRecoverAction,
			},
		},
	}
}

// withConfig loads ark.toml, applies a mutation, and saves it back.
func withConfig(dbPath string, fn func(cfg *ark.Config) error) {
	configPath := filepath.Join(dbPath, "ark.toml")
	cfg, err := ark.LoadConfig(configPath)
	if err != nil {
		fatal(err)
	}
	if err := fn(cfg); err != nil {
		fatal(err)
	}
	if err := cfg.SaveConfig(configPath); err != nil {
		fatal(err)
	}
}

// CRC: crc-CLITree.md, crc-CLI.md | R85
func configShowAction(_ context.Context, _ *ucli.Command) error {
	if client := serverClient(arkDir); client != nil {
		data, err := proxyRaw(client, "GET", "/config", nil)
		if err != nil {
			fatal(err)
		}
		os.Stdout.Write(data)
		return nil
	}
	configPath := filepath.Join(arkDir, "ark.toml")
	data, err := os.ReadFile(configPath)
	if err != nil {
		fatal(err)
	}
	os.Stdout.Write(data)
	return nil
}

// CRC: crc-CLITree.md, crc-CLI.md | R1569
func configRecoverAction(_ context.Context, _ *ucli.Command) error {
	withDB(func(d *ark.DB) {
		stored, err := d.Store().ReadConfig()
		if err != nil {
			fatal(err)
		}
		if stored == nil {
			fatal(fmt.Errorf("no stored config found in database"))
		}
		configPath := filepath.Join(arkDir, "ark.toml")
		if err := stored.SaveConfig(configPath); err != nil {
			fatal(err)
		}
		fmt.Printf("recovered config written to %s\n", configPath)
	})
	return nil
}

// CRC: crc-CLITree.md, crc-CLI.md | R143
func configAddSourceAction(_ context.Context, c *ucli.Command) error {
	if c.Args().Len() < 1 {
		fatal(fmt.Errorf("directory path required"))
	}
	dir := c.Args().First()
	if client := serverClient(arkDir); client != nil {
		if err := proxyOK(client, "POST", "/config/add-source", map[string]string{"dir": dir}); err != nil {
			fatal(err)
		}
		return nil
	}
	withConfig(arkDir, func(cfg *ark.Config) error { return cfg.AddSource(dir) })
	return nil
}

// CRC: crc-CLITree.md, crc-CLI.md | R159
func configRemoveSourceAction(_ context.Context, c *ucli.Command) error {
	if c.Args().Len() < 1 {
		fatal(fmt.Errorf("directory path required"))
	}
	dir := c.Args().First()
	if client := serverClient(arkDir); client != nil {
		if err := proxyOK(client, "POST", "/config/remove-source", map[string]string{"dir": dir}); err != nil {
			fatal(err)
		}
		return nil
	}
	withConfig(arkDir, func(cfg *ark.Config) error { return cfg.RemoveSource(dir) })
	return nil
}

// CRC: crc-CLITree.md, crc-CLI.md | R144
func configAddIncludeAction(_ context.Context, c *ucli.Command) error {
	if c.Args().Len() < 1 {
		fatal(fmt.Errorf("pattern required"))
	}
	pattern := c.Args().First()
	source := c.String("source")
	if client := serverClient(arkDir); client != nil {
		if err := proxyOK(client, "POST", "/config/add-include", map[string]string{"pattern": pattern, "source": source}); err != nil {
			fatal(err)
		}
		return nil
	}
	withConfig(arkDir, func(cfg *ark.Config) error { return cfg.AddInclude(pattern, source) })
	return nil
}

// CRC: crc-CLITree.md, crc-CLI.md | R145
func configAddExcludeAction(_ context.Context, c *ucli.Command) error {
	if c.Args().Len() < 1 {
		fatal(fmt.Errorf("pattern required"))
	}
	pattern := c.Args().First()
	source := c.String("source")
	if client := serverClient(arkDir); client != nil {
		if err := proxyOK(client, "POST", "/config/add-exclude", map[string]string{"pattern": pattern, "source": source}); err != nil {
			fatal(err)
		}
		return nil
	}
	withConfig(arkDir, func(cfg *ark.Config) error { return cfg.AddExclude(pattern, source) })
	return nil
}

// CRC: crc-CLITree.md, crc-CLI.md | R146
func configRemovePatternAction(_ context.Context, c *ucli.Command) error {
	if c.Args().Len() < 1 {
		fatal(fmt.Errorf("pattern required"))
	}
	pattern := c.Args().First()
	source := c.String("source")
	if client := serverClient(arkDir); client != nil {
		if err := proxyOK(client, "POST", "/config/remove-pattern", map[string]string{"pattern": pattern, "source": source}); err != nil {
			fatal(err)
		}
		return nil
	}
	withConfig(arkDir, func(cfg *ark.Config) error { return cfg.RemovePattern(pattern, source) })
	return nil
}

// CRC: crc-CLITree.md, crc-CLI.md | R147
func configShowWhyAction(_ context.Context, c *ucli.Command) error {
	if c.Args().Len() < 1 {
		fatal(fmt.Errorf("file path required"))
	}
	filePath := c.Args().First()
	if client := serverClient(arkDir); client != nil {
		var result ark.WhyResult
		if err := proxyDecode(client, "POST", "/config/show-why", map[string]string{"path": filePath}, &result); err != nil {
			fatal(err)
		}
		data, _ := json.MarshalIndent(result, "", "  ")
		fmt.Println(string(data))
		return nil
	}
	configPath := filepath.Join(arkDir, "ark.toml")
	cfg, err := ark.LoadConfig(configPath)
	if err != nil {
		fatal(err)
	}
	result, err := cfg.ShowWhy(filePath)
	if err != nil {
		fatal(err)
	}
	data, _ := json.MarshalIndent(result, "", "  ")
	fmt.Println(string(data))
	return nil
}

// CRC: crc-CLITree.md, crc-CLI.md
func configAddStrategyAction(_ context.Context, c *ucli.Command) error {
	if c.Args().Len() < 2 {
		fatal(fmt.Errorf("pattern and strategy required (e.g. '*.md' markdown)"))
	}
	pattern := c.Args().Get(0)
	strategy := c.Args().Get(1)
	if client := serverClient(arkDir); client != nil {
		if err := proxyOK(client, "POST", "/config/add-strategy", map[string]string{"pattern": pattern, "strategy": strategy}); err != nil {
			fatal(err)
		}
		return nil
	}
	withConfig(arkDir, func(cfg *ark.Config) error { return cfg.AddStrategy(pattern, strategy) })
	return nil
}
