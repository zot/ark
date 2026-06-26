package main

// The `ark subscribe`, `ark subscribers`, and `ark listen` commands —
// the pub/sub trio — migrated to the urfave/cli v3 command tree (Stage 2
// of the CLI urfave migration). All three are flat, server-required
// top-level commands; each node declares its flags so --help is generated
// and each Action keeps the existing proxy body. Repeatable flags
// (--tag/--file-tag/--filter-files/--exclude-files) become StringSliceFlag.
// (R937, R942, R944, R2442, R2457-R2461, R2805; see crc-CLITree.md.)

import (
	"context"
	"fmt"
	"net/url"
	"os"
	"strings"

	ucli "github.com/urfave/cli/v3"
	"github.com/zot/ark"
)

// CRC: crc-CLITree.md, crc-CLI.md | Seq: seq-pubsub.md | R937, R944, R2442, R2457, R2458, R2459, R2460, R2461
func subscribeCommand() *ucli.Command {
	return &ucli.Command{
		Name:  "subscribe",
		Usage: "manage tag subscriptions (requires server)",
		Description: "Match syntax: [~|:]NAME[(=|:|~)VALUE]\n" +
			"  name side  — bare = exact, ':' prefix = contains, '~' prefix = regex\n" +
			"  value side — '=V' exact, ':V' contains, '~V' regex",
		Flags: []ucli.Flag{
			&ucli.StringFlag{Name: "session", Usage: "session ID (required unless --list/--stats)"},
			&ucli.BoolFlag{Name: "cancel", Usage: "cancel subscriptions"},
			&ucli.BoolFlag{Name: "list", Usage: "list active subscriptions"},
			&ucli.BoolFlag{Name: "stats", Usage: "show hit/drop statistics"},
			&ucli.StringSliceFlag{Name: "tag", Usage: "tag match in sigil form [~|:]NAME[(=|:|~)VALUE] (repeatable)"},
			&ucli.StringSliceFlag{Name: "file-tag", Usage: "file-tag match: every chunk on a file with the tag (repeatable)"},
			&ucli.StringSliceFlag{Name: "filter-files", Usage: "only match files matching glob (repeatable)"},
			&ucli.StringSliceFlag{Name: "exclude-files", Usage: "exclude files matching glob (repeatable)"},
		},
		Action: subscribeAction,
	}
}

func subscribeAction(_ context.Context, c *ucli.Command) error {
	client := serverClient(arkDir)
	if client == nil {
		fatal(fmt.Errorf("server not running (subscribe requires server)"))
	}
	session := c.String("session")
	tagArgs := c.StringSlice("tag")
	fileTagArgs := c.StringSlice("file-tag")
	filterFiles := c.StringSlice("filter-files")
	excludeFiles := c.StringSlice("exclude-files")

	if c.Bool("list") {
		var infos []ark.SubInfo
		if err := proxyDecode(client, "POST", "/subscribe", map[string]any{
			"session": session,
			"list":    true,
		}, &infos); err != nil {
			fatal(err)
		}
		for _, info := range infos {
			fmt.Printf("%s\t%s\t%s\t%d\t%d\n", info.SessionID, info.Kind, info.Tag, info.Hits, info.Drops)
		}
		return nil
	}

	if c.Bool("stats") {
		var st []ark.SubStats
		if err := proxyDecode(client, "POST", "/subscribe", map[string]any{
			"session": session,
			"stats":   true,
		}, &st); err != nil {
			fatal(err)
		}
		for _, s := range st {
			fmt.Printf("%s\t%d subs\t%d hits\t%d drops\n", s.SessionID, s.SubCount, s.Hits, s.Drops)
		}
		return nil
	}

	if session == "" {
		fatal(fmt.Errorf("--session is required"))
	}

	if c.Bool("cancel") {
		// R2458: at most one --tag is meaningful for cancel; the server
		// parses the sigil and drops every entry whose stored predicate
		// accepts the (name, value) pair.
		cancelTag := ""
		if len(tagArgs) > 0 {
			cancelTag = tagArgs[0]
		}
		if err := proxyOK(client, "POST", "/subscribe", map[string]any{
			"session": session,
			"cancel":  true,
			"tag":     cancelTag,
		}); err != nil {
			fatal(err)
		}
		return nil
	}

	if len(tagArgs) == 0 && len(fileTagArgs) == 0 {
		fatal(fmt.Errorf("--tag or --file-tag is required for subscribe"))
	}

	subs := make([]any, 0, len(tagArgs)+len(fileTagArgs))
	for _, t := range tagArgs {
		sub := map[string]any{"tag": t, "kind": "tag"}
		if len(filterFiles) > 0 {
			sub["filter_files"] = ark.ExpandTildeSlice(filterFiles)
		}
		if len(excludeFiles) > 0 { // R944
			sub["exclude_files"] = ark.ExpandTildeSlice(excludeFiles) // R946: wire key renamed from except_files
		}
		subs = append(subs, sub)
	}
	for _, t := range fileTagArgs {
		sub := map[string]any{"tag": t, "kind": "file-tag"}
		if len(filterFiles) > 0 {
			sub["filter_files"] = ark.ExpandTildeSlice(filterFiles)
		}
		if len(excludeFiles) > 0 {
			sub["exclude_files"] = ark.ExpandTildeSlice(excludeFiles)
		}
		subs = append(subs, sub)
	}

	if err := proxyOK(client, "POST", "/subscribe", map[string]any{
		"session": session,
		"subs":    subs,
	}); err != nil {
		fatal(err)
	}
	return nil
}

// CRC: crc-CLITree.md, crc-CLI.md | Seq: seq-subscriber-presence.md | R2805
func subscribersCommand() *ucli.Command {
	return &ucli.Command{
		Name:  "subscribers",
		Usage: "count subscriptions matching a tag (requires server)",
		Flags: []ucli.Flag{
			&ucli.StringFlag{Name: "tag", Usage: "sigil-form tag predicate (required)"},
			&ucli.BoolFlag{Name: "quiet", Usage: "no stdout; exit code carries presence (0=any, 1=zero)"},
		},
		Action: subscribersAction,
	}
}

func subscribersAction(_ context.Context, c *ucli.Command) error {
	tag := c.String("tag")
	if tag == "" {
		fatal(fmt.Errorf("--tag is required"))
	}
	client := requireServer("subscribers")
	var resp struct {
		Count int `json:"count"`
	}
	path := "/subscribers?tag=" + url.QueryEscape(tag)
	if err := proxyDecode(client, "GET", path, nil, &resp); err != nil {
		fatal(err)
	}
	if c.Bool("quiet") {
		if resp.Count == 0 {
			os.Exit(1)
		}
		return nil
	}
	fmt.Println(resp.Count)
	return nil
}

// CRC: crc-CLITree.md, crc-CLI.md | Seq: seq-pubsub.md
func listenCommand() *ucli.Command {
	return &ucli.Command{
		Name:  "listen",
		Usage: "long-poll for tag notifications (requires server)",
		Flags: []ucli.Flag{
			&ucli.StringFlag{Name: "session", Usage: "session ID (required)"},
			&ucli.IntFlag{Name: "timeout", Value: 120, Usage: "long-poll timeout in seconds"},
		},
		Action: listenAction,
	}
}

func listenAction(_ context.Context, c *ucli.Command) error {
	session := c.String("session")
	if session == "" {
		fatal(fmt.Errorf("--session is required"))
	}
	client := serverClient(arkDir)
	if client == nil {
		fatal(fmt.Errorf("server not running (listen requires server)"))
	}
	path := fmt.Sprintf("/listen?session=%s&timeout=%d", url.QueryEscape(session), c.Int("timeout"))
	data, err := proxyRaw(client, "GET", path, nil)
	if err != nil {
		// 204 No Content = timeout with no events, not an error
		if strings.HasPrefix(err.Error(), "server error (204)") {
			return nil
		}
		fatal(err)
	}
	fmt.Print(string(data))
	return nil
}
