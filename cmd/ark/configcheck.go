package main

// A malformed ark.toml stops the command (specs/config-tracking.md, "An
// ark.toml that does not load"). A command that quietly carried on with
// defaults would show the user the wrong data with no hint why.

import (
	"context"
	"errors"
	"fmt"
	"io/fs"
	"path/filepath"

	ucli "github.com/urfave/cli/v3"
	"github.com/zot/ark"
)

// configParseWarning names the condition in `ark status` warnings.
const configParseWarning = ark.ECondConfigParse

// configExempt lists the command paths that still run on an ark.toml that
// does not load, so a user who broke the file can repair it and get ark back.
// Help is exempt too, by asksForHelp rather than by entry here.
// CRC: crc-CLI.md | R3646
var configExempt = map[string]bool{
	"stop":           true,
	"status":         true,
	"config recover": true,
}

// configLoadError reports an ark.toml in dir that exists and does not load,
// with the repair. A missing file is not an error: a fresh install has none.
// CRC: crc-CLI.md | R3645
func configLoadError(dir string) error {
	path := filepath.Join(dir, "ark.toml")
	_, err := ark.LoadConfig(path)
	if err == nil || errors.Is(err, fs.ErrNotExist) {
		return nil
	}
	return fmt.Errorf("%s does not load: %w\n  fix the file, or run `ark config recover` to restore the config the index last stored", path, err)
}

// guardConfigActions wraps the Action of every command node under cmds, at
// any depth, so it checks ark.toml before running — except the exempt paths.
// prefix is the space-separated path of cmds' parent ("" at the root).
// CRC: crc-CLI.md | R3645, R3646
func guardConfigActions(cmds []*ucli.Command, prefix string) {
	for _, c := range cmds {
		path := c.Name
		if prefix != "" {
			path = prefix + " " + c.Name
		}
		if body := c.Action; body != nil && !configExempt[path] {
			c.Action = func(ctx context.Context, cmd *ucli.Command) error {
				if asksForHelp(cmd) {
					return body(ctx, cmd)
				}
				if err := configLoadError(arkDir); err != nil {
					return err
				}
				return body(ctx, cmd)
			}
		}
		guardConfigActions(c.Commands, path)
	}
}

// asksForHelp reports a help request that reached a node's Action. urfave
// answers --help itself for most nodes, but a SkipFlagParsing node (search,
// status, files) receives its flags raw and prints its own usage, so the
// check has to recognize the request rather than rely on urfave intercepting
// it (measured 2026-10-27: `ark search --help` was refused without this).
// CRC: crc-CLI.md | R3646
func asksForHelp(cmd *ucli.Command) bool {
	for _, a := range cmd.Args().Slice() {
		switch a {
		case "--":
			return false
		case "-h", "-help", "--help":
			return true
		}
	}
	return false
}

// withConfigParseWarning adds the CLI's own ark.toml check to a server's
// warnings (R3647). The server's `config_parse` record wins when it has one,
// because it says what is true of the running config; the CLI's fills in only
// in its absence, as in the moment before the watcher rereads the file.
// CRC: crc-CLI.md | R3647, R3653
func withConfigParseWarning(warnings map[string]string, cfgErr error) map[string]string {
	if cfgErr == nil {
		return warnings
	}
	if _, recorded := warnings[configParseWarning]; recorded {
		return warnings
	}
	if warnings == nil {
		warnings = map[string]string{}
	}
	warnings[configParseWarning] = cfgErr.Error()
	return warnings
}
