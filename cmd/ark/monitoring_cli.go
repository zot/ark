package main

// The `ark monitor` and `ark luhmann` command groups, migrated to the
// urfave/cli v3 command tree (Stage 2 of the CLI urfave migration). Both
// are small diagnostic CLIs; each node declares its flags so --help is
// generated, and each Action reuses the existing ark.Monitor* / Luhmann
// helpers. (R2916–R2932; see crc-CLITree.md.)

import (
	"context"
	"encoding/json"
	"fmt"
	"sort"
	"strings"

	ucli "github.com/urfave/cli/v3"
	"github.com/zot/ark"
)

// CRC: crc-CLITree.md, crc-Monitor.md | Seq: seq-cli-urfave.md#3.3 | R2784, R2785, R2786, R2787, R2788, R2789, R2790, R2863
func monitorCommand() *ucli.Command {
	return &ucli.Command{
		Name:  "monitor",
		Usage: "inspect monitoring logs (status, recent, pause, resume)",
		Commands: []*ucli.Command{
			{
				Name:   "status",
				Usage:  "per-class state + counters (cold-start)",
				Flags:  []ucli.Flag{&ucli.BoolFlag{Name: "json", Usage: "emit JSON"}},
				Action: monitorStatusAction,
			},
			{
				Name:      "recent",
				Usage:     "tail one or all monitoring logs (cold-start)",
				ArgsUsage: "[CLASS]",
				Flags: []ucli.Flag{
					&ucli.IntFlag{Name: "n", Value: 20, Usage: "number of records to show"},
					&ucli.BoolFlag{Name: "json", Usage: "emit raw JSONL"},
				},
				Action: monitorRecentAction,
			},
			{
				Name:      "pause",
				Usage:     "append a pause control record (server)",
				ArgsUsage: "CLASS",
				Flags:     []ucli.Flag{&ucli.StringFlag{Name: "reason", Usage: "pause reason; storm reasons (crash-storm / quit-early-storm) light the emergency flag"}},
				Action:    func(_ context.Context, c *ucli.Command) error { return monitorControlAction("pause", c) },
			},
			{
				Name:      "resume",
				Usage:     "append a resume control record (server)",
				ArgsUsage: "CLASS",
				Action:    func(_ context.Context, c *ucli.Command) error { return monitorControlAction("resume", c) },
			},
		},
	}
}

// CRC: crc-CLITree.md | R2784
func monitorStatusAction(_ context.Context, c *ucli.Command) error {
	sums, err := ark.MonitorStatus(arkDir)
	if err != nil {
		fatal(err)
	}
	if c.Bool("json") {
		for _, s := range sums {
			data, _ := json.Marshal(s)
			fmt.Println(string(data))
		}
		return nil
	}
	for _, s := range sums {
		fmt.Printf("## %s — state: %s\n", s.Class, s.State)
		if s.Emergency != nil && s.Emergency.Active {
			fmt.Printf("- 🚨 EMERGENCY: %s (since %s)\n", s.Emergency.Reason, s.Emergency.Since)
		}
		if s.LatestTimestamp != "" {
			fmt.Printf("- latest: %s", s.LatestTimestamp)
			if s.LatestKind != "" {
				fmt.Printf(" (kind=%s)", s.LatestKind)
			}
			fmt.Println()
		}
		keys := make([]string, 0, len(s.Counters))
		for k := range s.Counters {
			keys = append(keys, k)
		}
		sort.Strings(keys)
		for _, k := range keys {
			fmt.Printf("- %s: %v\n", k, s.Counters[k])
		}
		fmt.Println()
	}
	return nil
}

// CRC: crc-CLITree.md | R2785
func monitorRecentAction(_ context.Context, c *ucli.Command) error {
	class := ""
	if c.Args().Len() > 0 {
		class = c.Args().First()
		if !ark.IsKnownMonitorClass(class) {
			fatal(fmt.Errorf("unknown class %q (known: %s)", class, strings.Join(ark.MonitorClasses, ", ")))
		}
	}
	recs, err := ark.MonitorTail(arkDir, class, c.Int("n"))
	if err != nil {
		fatal(err)
	}
	asJSON := c.Bool("json")
	for _, r := range recs {
		if asJSON {
			data, _ := json.Marshal(r)
			fmt.Println(string(data))
		} else {
			fmt.Println(ark.FormatMonitorBullet(r))
		}
	}
	return nil
}

// CRC: crc-CLITree.md | R2786, R2787
func monitorControlAction(kind string, c *ucli.Command) error {
	if c.Args().Len() == 0 {
		fatal(fmt.Errorf("ark monitor %s: CLASS is required", kind))
	}
	class := c.Args().First()
	if !ark.IsKnownMonitorClass(class) {
		fatal(fmt.Errorf("unknown class %q (known: %s)", class, strings.Join(ark.MonitorClasses, ", ")))
	}
	body := map[string]any{"class": class, "kind": kind}
	if kind == "pause" {
		if reason := c.String("reason"); reason != "" {
			body["reason"] = reason
		}
	}
	client := requireServer(fmt.Sprintf("monitor %s", kind))
	if err := proxyOK(client, "POST", "/monitor/control", body); err != nil {
		fatal(err)
	}
	return nil
}

// CRC: crc-CLITree.md, crc-LuhmannCLI.md | Seq: seq-luhmann-supervisor.md | R2794, R2795, R2796, R2861
func luhmannCommand() *ucli.Command {
	return &ucli.Command{
		Name:  "luhmann",
		Usage: "orchestrator supervisor log writer (spawn-record, exit-record, inspect-exit)",
		Commands: []*ucli.Command{
			{
				Name:  "spawn-record",
				Usage: "record a subagent spawn (server)",
				Flags: []ucli.Flag{
					&ucli.StringFlag{Name: "class", Usage: "managed subagent class (required)"},
					&ucli.IntFlag{Name: "nonce", Usage: "nonce for this spawn (required)"},
					&ucli.StringFlag{Name: "task-id", Usage: "Claude Code Task identifier (required)"},
				},
				Action: luhmannSpawnRecordAction,
			},
			{
				Name:  "exit-record",
				Usage: "record a subagent exit (server)",
				Flags: []ucli.Flag{
					&ucli.StringFlag{Name: "class", Usage: "managed subagent class (required)"},
					&ucli.IntFlag{Name: "nonce", Usage: "nonce for the exiting subagent (required)"},
					&ucli.StringFlag{Name: "reason", Usage: "exit reason (required; \"context-limit\"=healthy, \"quit-early\"=quit-early, else crash)"},
					&ucli.IntFlag{Name: "crashes", Value: -1, Usage: "override computed crashes counter (default: compute from previous record)"},
					&ucli.IntFlag{Name: "quit-early", Value: -1, Usage: "override computed quit_early counter (default: compute from previous record)"},
					&ucli.IntFlag{Name: "backoff", Usage: "seconds the supervisor will wait before respawn"},
				},
				Action: luhmannExitRecordAction,
			},
			{
				Name:  "inspect-exit",
				Usage: "classify a subagent exit (cold-start)",
				Flags: []ucli.Flag{
					&ucli.IntFlag{Name: "nonce", Usage: "subagent nonce to classify (required)"},
					&ucli.BoolFlag{Name: "json", Usage: "emit JSON object"},
				},
				Action: luhmannInspectExitAction,
			},
		},
	}
}

// CRC: crc-CLITree.md | R2794
func luhmannSpawnRecordAction(_ context.Context, c *ucli.Command) error {
	class, nonce, taskID := c.String("class"), c.Int("nonce"), c.String("task-id")
	if class == "" || nonce == 0 || taskID == "" {
		fatal(fmt.Errorf("--class, --nonce, --task-id are required"))
	}
	client := requireServer("luhmann spawn-record")
	if err := proxyOK(client, "POST", "/luhmann/record", map[string]any{
		"kind": "spawn", "class": class, "nonce": nonce, "task_id": taskID,
	}); err != nil {
		fatal(err)
	}
	return nil
}

// CRC: crc-CLITree.md | R2795
func luhmannExitRecordAction(_ context.Context, c *ucli.Command) error {
	class, nonce, reason := c.String("class"), c.Int("nonce"), c.String("reason")
	if class == "" || nonce == 0 || reason == "" {
		fatal(fmt.Errorf("--class, --nonce, --reason are required"))
	}
	kind, _ := ark.ClassifyLuhmannReason(reason)
	body := map[string]any{
		"kind": kind, "class": class, "nonce": nonce, "reason": reason, "backoff": c.Int("backoff"),
	}
	if crashes := c.Int("crashes"); crashes >= 0 {
		body["crashes"] = crashes
	}
	if quitEarly := c.Int("quit-early"); quitEarly >= 0 {
		body["quit_early"] = quitEarly
	}
	client := requireServer("luhmann exit-record")
	if err := proxyOK(client, "POST", "/luhmann/record", body); err != nil {
		fatal(err)
	}
	return nil
}

// CRC: crc-CLITree.md | R2796
func luhmannInspectExitAction(_ context.Context, c *ucli.Command) error {
	nonce := c.Int("nonce")
	if nonce == 0 {
		fatal(fmt.Errorf("--nonce is required"))
	}
	jsonl := findSubagentJSONLCold(nonce)
	result := classifySubagentExit(arkDir, jsonl, nonce)
	if c.Bool("json") {
		data, _ := json.Marshal(result)
		fmt.Println(string(data))
		return nil
	}
	fmt.Println(result.Label)
	return nil
}
