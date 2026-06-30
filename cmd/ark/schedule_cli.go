package main

// The `ark schedule` command group, migrated to the urfave/cli v3 command
// tree (Stage 2 of the CLI urfave migration). schedule {search, change,
// tags, parse, upcoming, logs, suppress, unsuppress}. Each node declares
// its flags so --help is generated; each Action keeps the existing body
// (most are server-required; `tags` is cold-start via withDB; `parse` is
// pure-local). urfave intermixes flags with positionals, so the legacy
// reorderArgs shim is unneeded here (it stays in main.go for other groups).
// (R914-R925, R2838-R2841, R2844, R2845, R2849; see crc-CLITree.md.)

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"strings"
	"time"

	ucli "github.com/urfave/cli/v3"
	"github.com/zot/ark"
)

// CRC: crc-CLITree.md, crc-CLI.md | Seq: seq-cli-urfave.md#3.3 | R914, R921, R926, R2838, R2839, R2840, R2841, R2844
func scheduleCommand() *ucli.Command {
	return &ucli.Command{
		Name:  "schedule",
		Usage: "query and modify scheduled events (requires server)",
		Commands: []*ucli.Command{
			{
				Name:      "search",
				Usage:     "query scheduled events for a date or range",
				ArgsUsage: "DATE",
				Flags: []ucli.Flag{
					&ucli.StringFlag{Name: "tag", Usage: "filter to a specific schedule tag"},
					&ucli.BoolFlag{Name: "gaps", Usage: "show only past events with no acknowledgment"},
					&ucli.BoolFlag{Name: "json", Usage: "output JSON instead of markdown"},
				},
				Action: scheduleSearchAction,
			},
			{
				Name:      "change",
				Usage:     "rewrite the date in a schedule tag value",
				ArgsUsage: "PATH TAG NEWSTART [NEWEND]",
				Flags: []ucli.Flag{
					&ucli.BoolFlag{Name: "dry-run", Usage: "show what would change without writing"},
				},
				Action: scheduleChangeAction,
			},
			{
				Name:  "tags",
				Usage: "show configured schedule tags and default durations",
				Flags: []ucli.Flag{
					&ucli.BoolFlag{Name: "values", Usage: "show tag values and next upcoming dates"},
				},
				Action: scheduleTagsAction,
			},
			{
				Name:      "parse",
				Usage:     "parse a date expression and show the result",
				ArgsUsage: "DATE",
				Action:    scheduleParseAction,
			},
			{
				Name:      "upcoming",
				Usage:     "show next fire(s) from the in-memory priority queue",
				ArgsUsage: "TAG",
				Flags: []ucli.Flag{
					&ucli.BoolFlag{Name: "all", Usage: "list all upcoming events across tags"},
					&ucli.BoolFlag{Name: "json", Usage: "JSON output"},
				},
				Action: scheduleUpcomingAction,
			},
			{
				Name:      "logs",
				Usage:     "show audit log (fired entries + spec history) for a tag",
				ArgsUsage: "TAG [SOURCE]",
				Flags: []ucli.Flag{
					&ucli.IntFlag{Name: "n", Value: 50, Usage: "max fired entries to show"},
					&ucli.BoolFlag{Name: "json", Usage: "JSON output"},
				},
				Action: scheduleLogsAction,
			},
			{
				Name:      "suppress",
				Usage:     "stop a tag from firing without removing its declaration",
				ArgsUsage: "TAG",
				Action:    scheduleSuppressAction,
			},
			{
				Name:      "unsuppress",
				Usage:     "resume firing for a suppressed tag",
				ArgsUsage: "TAG",
				Action:    scheduleUnsuppressAction,
			},
		},
	}
}

// CRC: crc-CLITree.md, crc-CLI.md | Seq: seq-scheduling.md | R914-R920, R2845
func scheduleSearchAction(_ context.Context, c *ucli.Command) error {
	if c.Args().Len() < 1 {
		fatal(fmt.Errorf("ark schedule search: DATE is required"))
	}
	// Join all positional args — allows "April 15 2026" as separate words.
	dateArg := strings.Join(c.Args().Slice(), " ")

	client := serverClient(arkDir)
	if client == nil {
		fatal(fmt.Errorf("server not running (schedule requires server)"))
	}

	reqBody := map[string]any{"date": dateArg}
	if tag := c.String("tag"); tag != "" {
		reqBody["tag"] = tag
	}
	if c.Bool("gaps") {
		reqBody["gaps"] = true
	}

	if c.Bool("json") {
		data, err := proxyRaw(client, "POST", "/schedule/search", reqBody)
		if err != nil {
			fatal(err)
		}
		fmt.Println(string(data))
		return nil
	}

	// Decode and format as markdown
	// CRC: crc-CLI.md | R916
	var events []ark.ScheduleEvent
	if err := proxyDecode(client, "POST", "/schedule/search", reqBody, &events); err != nil {
		fatal(err)
	}
	// R2845: identify suppressed tags so we can mark their events.
	suppressed := map[string]bool{}
	withDB(func(db *ark.DB) {
		cfg := db.Config()
		for name := range cfg.ScheduleTags() {
			if cfg.IsSuppressed(name) {
				suppressed[name] = true
			}
		}
	})
	lastKey := ""
	for _, ev := range events {
		key := ev.Date + "|" + ev.Tag + "|" + ev.Source
		if key != lastKey {
			if lastKey != "" {
				fmt.Println()
			}
			marker := ""
			if suppressed[ev.Tag] {
				marker = " [suppressed]" // R2845
			}
			fmt.Printf("## %s — @%s:%s (%s)\n\n", ev.Date, ev.Tag, marker, ev.Source)
			lastKey = key
		}
		// Collapse the time range when start==end and drop the trailing
		// ": " when there's no summary, so zero-duration ticks don't
		// render as "- 13:45–13:45:".
		// CRC: crc-CLI.md | R2849
		var label string
		if ev.AllDay {
			label = "all day"
		} else if ev.End.Equal(ev.Start) {
			label = ev.Start.Format("15:04")
		} else {
			label = ev.Start.Format("15:04") + "–" + ev.End.Format("15:04")
		}
		if ev.Summary != "" {
			fmt.Printf("- %s: %s\n", label, ev.Summary)
		} else {
			fmt.Printf("- %s\n", label)
		}
		fmt.Println()
	}
	return nil
}

// CRC: crc-CLITree.md, crc-CLI.md | Seq: seq-scheduling.md | R921-R925
func scheduleChangeAction(_ context.Context, c *ucli.Command) error {
	if c.Args().Len() < 3 {
		fatal(fmt.Errorf("ark schedule change: PATH TAG NEWSTART are required"))
	}
	path := c.Args().Get(0)
	tagName := c.Args().Get(1)
	newStart := c.Args().Get(2)
	newEnd := ""
	if c.Args().Len() > 3 {
		newEnd = c.Args().Get(3)
	}

	client := serverClient(arkDir)
	if client == nil {
		fatal(fmt.Errorf("server not running (schedule requires server)"))
	}

	reqBody := map[string]any{"path": path, "tag": tagName, "new_start": newStart}
	if newEnd != "" {
		reqBody["new_end"] = newEnd
	}
	if c.Bool("dry-run") {
		reqBody["dry_run"] = true
		var result map[string]string
		if err := proxyDecode(client, "POST", "/schedule/change", reqBody, &result); err != nil {
			fatal(err)
		}
		fmt.Printf("old: %s\nnew: %s\n", result["old"], result["new"])
		return nil
	}

	if err := proxyOK(client, "POST", "/schedule/change", reqBody); err != nil {
		fatal(err)
	}
	return nil
}

// CRC: crc-CLITree.md, crc-CLI.md | R2844
func scheduleTagsAction(_ context.Context, c *ucli.Command) error {
	showValues := c.Bool("values")
	emit := func(lines []string) {
		for _, l := range lines {
			fmt.Println(l)
		}
	}
	// R3000, R3003: /schedule/tags proxies; ScheduleTagSummary builds the
	// identical lines locally (R2844 stable order, --values detail both live there now).
	// CRC: crc-CLI.md | R1033, R1034
	proxyOrLocal(
		func(client *http.Client) error {
			var lines []string
			if err := proxyDecode(client, "POST", "/schedule/tags", map[string]any{"values": showValues}, &lines); err != nil {
				return err
			}
			emit(lines)
			return nil
		},
		func(db *ark.DB) error {
			emit(db.ScheduleTagSummary(showValues))
			return nil
		},
	)
	return nil
}

// CRC: crc-CLITree.md, crc-CLI.md | R1016
func scheduleParseAction(_ context.Context, c *ucli.Command) error {
	if c.Args().Len() == 0 {
		return ucli.ShowSubcommandHelp(c)
	}
	input := strings.Join(c.Args().Slice(), " ")
	loc := time.Now().Location()

	// Check for recurring with bounds
	if ark.IsRecurringSpec(input) {
		nb, na, spec := ark.ExtractBounds(input, loc)
		fmt.Printf("recurring: %s\n", spec)
		if !nb.IsZero() {
			fmt.Printf("start:     %s\n", nb.Format("2006-01-02"))
		}
		if !na.IsZero() {
			fmt.Printf("end:       %s\n", na.Format("2006-01-02"))
		}
		next := ark.ComputeNext(spec, time.Now(), na)
		if !next.IsZero() {
			fmt.Printf("next:      %s\n", next.Format("2006-01-02 15:04"))
		} else {
			fmt.Println("next:      (none — past end bound)")
		}
		return nil
	}

	dr, err := ark.ParseDateValue(input, "", loc)
	if err != nil {
		fmt.Fprintf(os.Stderr, "error: %v\n", err)
		os.Exit(1)
	}
	fmt.Printf("start:     %s\n", dr.Start.Format("2006-01-02 15:04"))
	if dr.End != dr.Start {
		fmt.Printf("end:       %s\n", dr.End.Format("2006-01-02 15:04"))
	}
	if dr.AllDay {
		fmt.Println("all-day:   true")
	}
	if dr.Description != "" {
		fmt.Printf("text:      %s\n", dr.Description)
	}
	return nil
}

// cmdScheduleUpcoming prints next-fire entries from the in-memory priority
// queue. Server-required (the queue lives in the running scheduler).
// CRC: crc-CLITree.md, crc-CLI.md | R2838
func scheduleUpcomingAction(_ context.Context, c *ucli.Command) error {
	all := c.Bool("all")
	tag := ""
	if c.Args().Len() > 0 {
		tag = c.Args().First()
	}
	if !all && tag == "" {
		fatal(fmt.Errorf("ark schedule upcoming: TAG is required (or use --all)"))
	}
	client := serverClient(arkDir)
	if client == nil {
		fatal(fmt.Errorf("server not running (schedule upcoming requires server)"))
	}
	var entries []struct {
		Tag      string    `json:"tag"`
		Value    string    `json:"value"`
		Path     string    `json:"path"`
		NextFire time.Time `json:"next_fire"`
	}
	if err := proxyDecode(client, "POST", "/schedule/upcoming",
		map[string]string{"tag": tag}, &entries); err != nil {
		fatal(err)
	}
	if !all && len(entries) > 0 {
		entries = entries[:1] // head only
	}
	if c.Bool("json") {
		data, _ := json.MarshalIndent(entries, "", "  ")
		fmt.Println(string(data))
		return nil
	}
	if len(entries) == 0 {
		fmt.Println("(no upcoming events)")
		return nil
	}
	for _, e := range entries {
		fmt.Printf("@%s: %s  %s\n", e.Tag, e.NextFire.Local().Format("2006-01-02 15:04"), e.Path)
	}
	return nil
}

// cmdScheduleLogs prints the audit log for a tag. Disk-backed tags
// (lifecycle=disk) can be read cold; tmp:// tags require the server.
// CRC: crc-CLITree.md, crc-CLI.md | R2839
func scheduleLogsAction(_ context.Context, c *ucli.Command) error {
	if c.Args().Len() == 0 {
		fatal(fmt.Errorf("ark schedule logs: TAG is required"))
	}
	tag := c.Args().Get(0)
	source := ""
	if c.Args().Len() > 1 {
		source = c.Args().Get(1)
	}
	asJSON := c.Bool("json")
	n := c.Int("n")
	client := serverClient(arkDir)
	if client == nil {
		fatal(fmt.Errorf("server not running (schedule logs requires server for tmp:// or unknown lifecycle)"))
	}
	body := map[string]string{"tag": tag, "source": source}
	if source == "" {
		var resp struct {
			Tag       string   `json:"tag"`
			Lifecycle string   `json:"lifecycle"`
			Note      string   `json:"note,omitempty"`
			Sources   []string `json:"sources"`
		}
		if err := proxyDecode(client, "POST", "/schedule/logs", body, &resp); err != nil {
			fatal(err)
		}
		if resp.Note != "" {
			fmt.Println(resp.Note)
			return nil
		}
		if asJSON {
			data, _ := json.MarshalIndent(resp, "", "  ")
			fmt.Println(string(data))
			return nil
		}
		fmt.Printf("@%s  (lifecycle=%s)\n", resp.Tag, resp.Lifecycle)
		if len(resp.Sources) == 0 {
			fmt.Println("  (no sources with audit log)")
			return nil
		}
		for _, s := range resp.Sources {
			fmt.Printf("  %s\n", s)
		}
		return nil
	}
	var resp struct {
		Tag       string `json:"tag"`
		Source    string `json:"source"`
		Lifecycle string `json:"lifecycle"`
		Fired     []string
		Specs     []struct {
			Kind string    `json:"kind"`
			Time time.Time `json:"time"`
			Spec string    `json:"spec"`
		}
		CheckGaps []string `json:"check_gaps,omitempty"`
	}
	if err := proxyDecode(client, "POST", "/schedule/logs", body, &resp); err != nil {
		fatal(err)
	}
	if asJSON {
		data, _ := json.MarshalIndent(resp, "", "  ")
		fmt.Println(string(data))
		return nil
	}
	fmt.Printf("@%s  %s  (lifecycle=%s, %d fires)\n",
		resp.Tag, resp.Source, resp.Lifecycle, len(resp.Fired))
	if len(resp.Specs) > 0 {
		fmt.Println("  spec history:")
		for _, m := range resp.Specs {
			ts := m.Time.Local().Format("2006-01-02 15:04")
			if m.Time.IsZero() {
				ts = "(migrated)"
			}
			fmt.Printf("    %-7s %s — %s\n", m.Kind, ts, m.Spec)
		}
	}
	if len(resp.Fired) > 0 {
		shown := resp.Fired
		if n > 0 && len(shown) > n {
			shown = shown[len(shown)-n:]
		}
		fmt.Println("  recent fires:")
		// Most-recent-first display.
		for i := len(shown) - 1; i >= 0; i-- {
			fmt.Printf("    %s\n", shown[i])
		}
	}
	return nil
}

// CRC: crc-CLITree.md, crc-CLI.md | R2840
func scheduleSuppressAction(_ context.Context, c *ucli.Command) error {
	runScheduleSuppress(c, true)
	return nil
}

// CRC: crc-CLITree.md, crc-CLI.md | R2841
func scheduleUnsuppressAction(_ context.Context, c *ucli.Command) error {
	runScheduleSuppress(c, false)
	return nil
}

// runScheduleSuppress sets `[schedule.tag.TAG] suppress = <suppress>` via
// the running server (shared by suppress/unsuppress).
func runScheduleSuppress(c *ucli.Command, suppress bool) {
	sub := "unsuppress"
	if suppress {
		sub = "suppress"
	}
	if c.Args().Len() != 1 {
		fatal(fmt.Errorf("ark schedule %s: exactly one TAG is required", sub))
	}
	tag := c.Args().First()
	client := serverClient(arkDir)
	if client == nil {
		fatal(fmt.Errorf("server not running (schedule %s requires server)", sub))
	}
	if err := proxyOK(client, "POST", "/schedule/suppress",
		map[string]any{"tag": tag, "suppress": suppress}); err != nil {
		fatal(err)
	}
	if suppress {
		fmt.Printf("@%s: suppressed\n", tag)
	} else {
		fmt.Printf("@%s: unsuppressed\n", tag)
	}
}
