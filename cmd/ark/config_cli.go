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
	"strings"

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

// mutateConfigCLI runs one config mutation through the server when one is up
// (so its in-memory config stays current), else on ark.toml directly, and then
// says what it did. Both paths exit on failure, so the line means success.
// CRC: crc-CLI.md | R3677
func mutateConfigCLI(route string, body map[string]string, local func(*ark.Config) error, done string) {
	if client := serverClient(arkDir); client != nil {
		if err := proxyOK(client, "POST", route, body); err != nil {
			fatal(err)
		}
	} else {
		withConfig(arkDir, local)
	}
	fmt.Println(done)
}

// listedSource is what `ark sources` shows of a source: decoded from the
// server's GET /config JSON or taken from a local load, so neither path depends
// on how the pattern fields are encoded.
type listedSource struct {
	Dir      string
	FromGlob string
	LogsFor  string
}

// sourcesListAction is bare `ark sources`: the effective sources, read-only.
// The server's list when one runs; else ark.toml with the built-in sources
// ensured on this private copy, as the server ensures them, so the answer does
// not depend on whether a server is up.
// CRC: crc-CLI.md | R3679
func sourcesListAction(_ context.Context, _ *ucli.Command) error {
	var sources []listedSource
	if client := serverClient(arkDir); client != nil {
		data, err := proxyRaw(client, "GET", "/config", nil)
		if err != nil {
			fatal(err)
		}
		var cfg struct{ Sources []listedSource }
		if err := json.Unmarshal(data, &cfg); err != nil {
			fatal(fmt.Errorf("read the server's config: %w", err))
		}
		sources = cfg.Sources
	} else {
		cfg, err := ark.LoadConfig(filepath.Join(arkDir, "ark.toml"))
		if err != nil {
			fatal(err)
		}
		cfg.EnsureArkSource()
		cfg.EnsureLuhmannSource()
		for _, src := range cfg.Sources {
			sources = append(sources, listedSource{Dir: src.Dir, FromGlob: src.FromGlob, LogsFor: src.LogsFor})
		}
	}
	fmt.Print(formatSourceList(sources, func(dir string) bool {
		_, err := os.Stat(dir)
		return err == nil
	}))
	return nil
}

// formatSourceList renders the sources as baby-food bullets: a glob source as
// its pattern, a concrete one as its directory, each with the marks that apply.
// CRC: crc-CLI.md | R3679
func formatSourceList(sources []listedSource, exists func(string) bool) string {
	var b strings.Builder
	b.WriteString("# Sources\n\n")
	if len(sources) == 0 {
		b.WriteString("No sources are configured.\n")
		return b.String()
	}
	for _, src := range sources {
		var marks []string
		switch {
		case ark.IsGlob(src.Dir):
			marks = append(marks, "glob")
		case !exists(src.Dir):
			marks = append(marks, "missing")
		}
		if src.FromGlob != "" {
			marks = append(marks, "from glob `"+src.FromGlob+"`")
		}
		if src.LogsFor != "" {
			marks = append(marks, "logs for "+src.LogsFor)
		}
		b.WriteString("- `" + src.Dir + "`")
		if len(marks) > 0 {
			b.WriteString(" — " + strings.Join(marks, ", "))
		}
		b.WriteString("\n")
	}
	return b.String()
}

// patternScope names where a pattern mutation applies, for its confirmation.
func patternScope(source string) string {
	if source == "" {
		return "global"
	}
	return "source " + source
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

// configRecoverAction rewrites ark.toml from the I-record snapshot. It reads
// the snapshot without opening the DB, because opening needs an ark.toml that
// loads (R3648), and keeps a file that does not load as ark.toml.broken so the
// user's edit survives (R3649).
// CRC: crc-CLITree.md, crc-CLI.md | R1569, R3648, R3649
func configRecoverAction(_ context.Context, _ *ucli.Command) error {
	if serverClient(arkDir) != nil {
		fatal(errServerHoldsIndex)
	}
	stored, err := ark.ReadStoredConfig(arkDir, dispatchLockTimeout)
	if err != nil {
		fatal(err)
	}
	if stored == nil {
		fatal(fmt.Errorf("no stored config found in database"))
	}
	configPath := filepath.Join(arkDir, "ark.toml")
	if configLoadError(arkDir) != nil {
		broken := configPath + ".broken"
		if err := os.Rename(configPath, broken); err != nil {
			fatal(err)
		}
		fmt.Printf("the ark.toml that did not load is kept as %s\n", broken)
	}
	if err := stored.SaveConfig(configPath); err != nil {
		fatal(err)
	}
	fmt.Printf("recovered config written to %s\n", configPath)
	return nil
}

// CRC: crc-CLITree.md, crc-CLI.md | R143, R3677
func configAddSourceAction(_ context.Context, c *ucli.Command) error {
	if c.Args().Len() < 1 {
		fatal(fmt.Errorf("directory path required"))
	}
	dir := c.Args().First()
	mutateConfigCLI("/config/add-source", map[string]string{"dir": dir},
		func(cfg *ark.Config) error { return cfg.AddSource(dir) },
		"added source "+dir)
	return nil
}

// CRC: crc-CLITree.md, crc-CLI.md | R159, R3677
func configRemoveSourceAction(_ context.Context, c *ucli.Command) error {
	if c.Args().Len() < 1 {
		fatal(fmt.Errorf("directory path required"))
	}
	dir := c.Args().First()
	mutateConfigCLI("/config/remove-source", map[string]string{"dir": dir},
		func(cfg *ark.Config) error { return cfg.RemoveSource(dir) },
		"removed source "+dir)
	return nil
}

// CRC: crc-CLITree.md, crc-CLI.md | R144, R3677
func configAddIncludeAction(_ context.Context, c *ucli.Command) error {
	if c.Args().Len() < 1 {
		fatal(fmt.Errorf("pattern required"))
	}
	pattern := c.Args().First()
	source := c.String("source")
	mutateConfigCLI("/config/add-include", map[string]string{"pattern": pattern, "source": source},
		func(cfg *ark.Config) error { return cfg.AddInclude(pattern, source) },
		"added include "+pattern+" ("+patternScope(source)+")")
	return nil
}

// CRC: crc-CLITree.md, crc-CLI.md | R145, R3677
func configAddExcludeAction(_ context.Context, c *ucli.Command) error {
	if c.Args().Len() < 1 {
		fatal(fmt.Errorf("pattern required"))
	}
	pattern := c.Args().First()
	source := c.String("source")
	mutateConfigCLI("/config/add-exclude", map[string]string{"pattern": pattern, "source": source},
		func(cfg *ark.Config) error { return cfg.AddExclude(pattern, source) },
		"added exclude "+pattern+" ("+patternScope(source)+")")
	return nil
}

// CRC: crc-CLITree.md, crc-CLI.md | R146, R3677
func configRemovePatternAction(_ context.Context, c *ucli.Command) error {
	if c.Args().Len() < 1 {
		fatal(fmt.Errorf("pattern required"))
	}
	pattern := c.Args().First()
	source := c.String("source")
	mutateConfigCLI("/config/remove-pattern", map[string]string{"pattern": pattern, "source": source},
		func(cfg *ark.Config) error { return cfg.RemovePattern(pattern, source) },
		"removed pattern "+pattern+" ("+patternScope(source)+")")
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
		// R3403: absolutize against the client cwd; ShowWhy matches the path
		// against absolute source dirs, so a relative path resolves to nothing.
		if err := proxyDecode(client, "POST", "/config/show-why", map[string]string{"path": absClientPath(filePath)}, &result); err != nil {
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

// CRC: crc-CLITree.md, crc-CLI.md | R3677
func configAddStrategyAction(_ context.Context, c *ucli.Command) error {
	if c.Args().Len() < 2 {
		fatal(fmt.Errorf("pattern and strategy required (e.g. '*.md' markdown)"))
	}
	pattern := c.Args().Get(0)
	strategy := c.Args().Get(1)
	mutateConfigCLI("/config/add-strategy", map[string]string{"pattern": pattern, "strategy": strategy},
		func(cfg *ark.Config) error { return cfg.AddStrategy(pattern, strategy) },
		"added strategy "+pattern+" = "+strategy)
	return nil
}
