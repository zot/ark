package main

// The `ark ui` command group, migrated to the urfave/cli v3 command tree
// (Stage 2 of the CLI urfave migration). This is the deepest group: 16
// subcommands plus nested checkpoint / theme / linkapp subtrees, so the
// tree declaration is what makes the whole `ui` help self-documenting.
//
// Design: thin tree. Each Action delegates (via uiDelegate) to the
// existing cmdUI* / checkpoint-dispatcher body kept in main.go — the
// node declarations supply the auto-generated help; the bodies (and the
// shared uiClient/uiRequest/fossil*/checkpoint* infrastructure, plus
// cmdUIInstall which the top-level `install` command also calls) stay
// where they are. Only the legacy cmdUI dispatcher + uiUsage are retired.
// (R429-R435 et al. remain anchored on the kept bodies in main.go; see
// crc-CLITree.md + crc-CLI.md.)

import (
	"context"
	"fmt"
	"os"

	ucli "github.com/urfave/cli/v3"
)

// uiDelegate adapts a legacy `func(args []string)` handler into an urfave
// Action. For nested subtrees (checkpoint/theme/linkapp) it prepends the
// fixed sub-verb so the kept positional dispatcher sees the same argv it
// did before (e.g. `ui checkpoint save APP` → cmdUICheckpoint{"save","APP"}).
func uiDelegate(fn func([]string), prefix ...string) ucli.ActionFunc {
	return func(_ context.Context, c *ucli.Command) error {
		args := append(append([]string{}, prefix...), c.Args().Slice()...)
		fn(args)
		return nil
	}
}

// CRC: crc-CLITree.md, crc-CLI.md | Seq: seq-cli-urfave.md#3.3
func uiCommand() *ucli.Command {
	return &ucli.Command{
		Name:  "ui",
		Usage: "Frictionless UI operations (run, display, checkpoint, theme, ...)",
		// bare `ark ui` → open the browser; an unknown subcommand errors
		// (urfave routes unmatched args to the parent Action).
		Action: func(_ context.Context, c *ucli.Command) error {
			if c.Args().Len() > 0 {
				fmt.Fprintf(os.Stderr, "unknown ui subcommand: %s\n", c.Args().First())
				os.Exit(1)
			}
			cmdUIOpen(nil)
			return nil
		},
		Commands: []*ucli.Command{
			{Name: "open", Usage: "open browser to the current UI session", Action: uiDelegate(cmdUIOpen)},
			{Name: "run", Usage: "execute Lua code in the UI session", ArgsUsage: "'<lua>'", Action: uiDelegate(cmdUIRun)},
			{Name: "display", Usage: "display an app in the browser", ArgsUsage: "APP", Action: uiDelegate(cmdUIDisplay)},
			{Name: "event", Usage: "wait for the next UI event (120s timeout)", Action: uiDelegate(cmdUIEvent)},
			{Name: "audit", Usage: "run code quality audit on an app", ArgsUsage: "APP", Action: uiDelegate(cmdUIAudit)},
			{Name: "reload", Usage: "reload the UI (fresh Lua VM)", Action: uiDelegate(cmdUIReload)},
			{Name: "status", Usage: "ui-engine server status", Action: uiDelegate(cmdUIStatus)},
			{Name: "state", Usage: "get the current session state", Action: uiDelegate(cmdUIState)},
			{Name: "variables", Usage: "get current variable values", Action: uiDelegate(cmdUIVariables)},
			{Name: "patterns", Usage: "list available patterns", Action: uiDelegate(cmdUIPatterns)},
			{Name: "progress", Usage: "report build progress", ArgsUsage: "APP PERCENT STAGE", Action: uiDelegate(cmdUIProgress)},
			{Name: "install", Usage: "connect this project to ark", Action: uiDelegate(cmdUIInstall)},
			{
				Name:   "update",
				Usage:  "smart update, or version check with -t",
				Flags:  []ucli.Flag{&ucli.BoolFlag{Name: "t", Usage: "version check only (no update)"}},
				Action: func(_ context.Context, c *ucli.Command) error {
					if c.Bool("t") {
						cmdUIUpdate([]string{"-t"})
					} else {
						cmdUIUpdate(nil)
					}
					return nil
				},
			},
			{
				Name:  "checkpoint",
				Usage: "manage app checkpoints (fossil-backed)",
				Commands: []*ucli.Command{
					{Name: "save", Usage: "save a checkpoint", ArgsUsage: "APP [MSG]", Action: uiDelegate(cmdUICheckpoint, "save")},
					{Name: "list", Usage: "list checkpoints", ArgsUsage: "APP", Action: uiDelegate(cmdUICheckpoint, "list")},
					{Name: "rollback", Usage: "rollback to nth checkpoint (default: undo last)", ArgsUsage: "APP [N]", Action: uiDelegate(cmdUICheckpoint, "rollback")},
					{Name: "diff", Usage: "diff against nth checkpoint (default: 1)", ArgsUsage: "APP [N]", Action: uiDelegate(cmdUICheckpoint, "diff")},
					{Name: "clear", Usage: "reset to baseline", ArgsUsage: "APP", Action: uiDelegate(cmdUICheckpoint, "clear")},
					{Name: "baseline", Usage: "set current state as baseline", ArgsUsage: "APP", Action: uiDelegate(cmdUICheckpoint, "baseline")},
					{Name: "count", Usage: "count checkpoints", ArgsUsage: "APP", Action: uiDelegate(cmdUICheckpoint, "count")},
					{Name: "update", Usage: "save to the updates branch", ArgsUsage: "APP [MSG]", Action: uiDelegate(cmdUICheckpoint, "update")},
					{Name: "local", Usage: "save to the local branch", ArgsUsage: "APP [MSG]", Action: uiDelegate(cmdUICheckpoint, "local")},
				},
			},
			{
				Name:  "theme",
				Usage: "theme management",
				Commands: []*ucli.Command{
					{Name: "list", Usage: "list available themes", Action: uiDelegate(cmdUITheme, "list")},
					{Name: "classes", Usage: "list theme classes", ArgsUsage: "[THEME]", Action: uiDelegate(cmdUITheme, "classes")},
					{Name: "audit", Usage: "audit an app's theme class usage", ArgsUsage: "APP [THEME]", Action: uiDelegate(cmdUITheme, "audit")},
				},
			},
			{
				Name:  "linkapp",
				Usage: "manage app symlinks (lua + viewdefs)",
				Commands: []*ucli.Command{
					{Name: "add", Usage: "link an app's lua + viewdefs into ~/.ark", ArgsUsage: "APP", Action: uiDelegate(cmdUILinkapp, "add")},
					{Name: "remove", Usage: "unlink an app", ArgsUsage: "APP", Action: uiDelegate(cmdUILinkapp, "remove")},
				},
			},
		},
	}
}
