package main

// The `ark bloodhound` command group — external-app access to the warm
// bloodhound (bloodhound-cli.md, S3). `search` submits a directed hunt and
// prints the curated findings as JSONL; the whole protocol (request doc, tag
// baton, pool secretary, Luhmann curation) is hidden behind the one command
// (Batteries Included).

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"strings"
	"time"

	ucli "github.com/urfave/cli/v3"
)

// CRC: crc-CLITree.md | Seq: seq-bloodhound-cli.md#1.1 | R3021, R3022, R3029
func bloodhoundCommand() *ucli.Command {
	return &ucli.Command{
		Name:  "bloodhound",
		Usage: "external directed hunts against the warm bloodhound (requires a running Luhmann)",
		Commands: []*ucli.Command{
			{
				Name:      "search",
				Usage:     "run a directed hunt and print curated findings as JSONL",
				ArgsUsage: "CLUE...",
				Flags: []ucli.Flag{
					&ucli.StringFlag{Name: "scope", Value: "all", Usage: "search scope: code | specs | design | notes | chat | all"},
					&ucli.StringFlag{Name: "depth", Value: "lookup", Usage: "depth: lookup | investigate"},
					&ucli.StringFlag{Name: "want", Value: "passages", Usage: "want: answer | passages | ..."},
					&ucli.BoolFlag{Name: "wait", Usage: "block stubbornly on a busy pool / server bounce instead of failing fast"},
					&ucli.IntFlag{Name: "timeout", Value: 300, Usage: "seconds to wait for the curated result (default 300)"},
				},
				Action: bloodhoundSearchAction,
			},
			{
				Name:      "add",
				Usage:     "Luhmann's result stencil: append one curated finding to a CLI hunt, or --done to finish",
				ArgsUsage: "--result tmp://BLOODHOUND-CLI/<id> (--loc PATH:RANGE --note TEXT [--chunk TEXT] | --done)",
				Flags: []ucli.Flag{
					&ucli.StringFlag{Name: "result", Usage: "the request-doc path handed to `luhmann next` (tmp://BLOODHOUND-CLI/<id>)"},
					&ucli.StringFlag{Name: "loc", Usage: "the curated finding's PATH:RANGE"},
					&ucli.StringFlag{Name: "note", Usage: "one-line curated note: why it answers the query"},
					&ucli.StringFlag{Name: "chunk", Usage: "optional chunk excerpt to carry in the JSONL"},
					&ucli.BoolFlag{Name: "done", Usage: "terminal call: write the result doc and notify the waiting CLI"},
				},
				Action: bloodhoundAddAction,
			},
		},
	}
}

// CRC: crc-CLITree.md | Seq: seq-bloodhound-cli.md#1.5.2 | R3027, R3028
func bloodhoundAddAction(_ context.Context, c *ucli.Command) error {
	result := c.String("result")
	if result == "" {
		fmt.Fprintln(os.Stderr, "ark bloodhound add --result tmp://BLOODHOUND-CLI/<id> (--loc PATH:RANGE --note TEXT [--chunk TEXT] | --done)")
		os.Exit(2)
	}
	done := c.Bool("done")
	loc := c.String("loc")
	if !done && loc == "" {
		fmt.Fprintln(os.Stderr, "ark bloodhound add: --loc required (or --done to finish)")
		os.Exit(2)
	}
	client := requireServer("bloodhound add")
	if err := proxyOK(client, "POST", "/bloodhound/add", map[string]any{
		"result": result, "loc": loc, "note": c.String("note"), "chunk": c.String("chunk"), "done": done,
	}); err != nil {
		fatal(err)
	}
	return nil
}

// CRC: crc-CLITree.md | Seq: seq-bloodhound-cli.md#1.1 | R3020, R3021, R3022, R3029
func bloodhoundSearchAction(_ context.Context, c *ucli.Command) error {
	clue := strings.TrimSpace(strings.Join(c.Args().Slice(), " "))
	if clue == "" {
		fmt.Fprintln(os.Stderr, "ark bloodhound search CLUE... [--scope S] [--depth D] [--want W] [--wait] [--timeout SECONDS]")
		os.Exit(2)
	}
	// The TERMS payload the watcher wraps as the ## Search task the secretary
	// reads — clue · scope · depth · want (R3021).
	payload := fmt.Sprintf("clue: %s\nscope: %s\ndepth: %s\nwant: %s\n",
		clue, c.String("scope"), c.String("depth"), c.String("want"))
	wait := c.Bool("wait")
	timeout := c.Int("timeout")

	// Submit. The server gates on a live Luhmann (503, R3020) and — without
	// --wait — a busy pool (429, R3022); either surfaces as a fatal exit and
	// submits nothing.
	client := requireServer("bloodhound search")
	var resp struct {
		ID string `json:"id"`
	}
	if err := proxyDecode(client, "POST", "/bloodhound/search",
		map[string]any{"payload": payload, "wait": wait}, &resp); err != nil {
		fatal(err)
	}

	// Block on the curated result up to --timeout (R3021). With --wait the block
	// is stubborn across a server bounce (redials); without it, a drop fails.
	resultURL := fmt.Sprintf("http://ark/bloodhound/result?id=%s&timeout=%d", resp.ID, timeout)
	deadline := time.Now().Add(time.Duration(timeout)*time.Second + 15*time.Second)
	backoff := recallNextRedialBackoff
	for {
		client = serverClient(arkDir)
		if client == nil {
			if !wait || time.Now().After(deadline) {
				fatal(errors.New("ark server unreachable while awaiting result"))
			}
			time.Sleep(backoff)
			backoff = min(backoff*2, recallNextRedialMaxBackoff)
			continue
		}
		req, err := http.NewRequest("GET", resultURL, nil)
		if err != nil {
			fatal(err)
		}
		httpResp, derr := client.Do(req)
		if derr != nil {
			if !wait || time.Now().After(deadline) {
				fatal(fmt.Errorf("awaiting result: %w", derr))
			}
			time.Sleep(backoff)
			continue
		}
		data, _ := io.ReadAll(httpResp.Body)
		httpResp.Body.Close()
		if httpResp.StatusCode != http.StatusOK {
			fatal(fmt.Errorf("server error (%d): %s", httpResp.StatusCode, strings.TrimSpace(string(data))))
		}
		if httpResp.Header.Get("X-Bloodhound-Timeout") == "1" {
			fmt.Fprintln(os.Stderr, "bloodhound search: timed out awaiting the curated result")
			os.Exit(1)
		}
		// R3029: the result doc is already JSONL (one curated finding per line);
		// an empty hunt is an empty body — no lines, exit 0.
		os.Stdout.Write(data)
		return nil
	}
}
