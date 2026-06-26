package main

// The `ark tag` command group, migrated to the urfave/cli v3 command
// tree (Stage 2 of the CLI urfave migration). tag {list, counts, files,
// values, defs, set, get, check, verify, inspect}. Each node declares its
// flags so --help is generated; each Action keeps the existing
// server-proxy-or-cold-start bodies (seq-cli-dispatch.md).
//
// set/get/check stay in main.go (cmdTagSet/cmdTagGet/cmdTagCheck) because
// the `message` group wraps them (cmdMessageSetTags/GetTags/Check); their
// Actions here are thin adapters over c.Args().Slice(). The shared helpers
// matchPath / cmdTagFilesContext / normalizeTagName(s) also stay in main.go.
// (R122-R125, R607-R612, R615, R674, R1131-R1138, R2092-R2099, R2113-R2117;
// see crc-CLITree.md.)

import (
	"context"
	"fmt"
	"net/http"
	"os"
	"sort"
	"strings"

	ucli "github.com/urfave/cli/v3"
	"github.com/zot/ark"
)

// CRC: crc-CLITree.md, crc-CLI.md | Seq: seq-cli-urfave.md#3.3 | R122, R123, R124, R615, R131
func tagCommand() *ucli.Command {
	fileFilterFlags := func() []ucli.Flag {
		return []ucli.Flag{
			&ucli.StringSliceFlag{Name: "filter-files", Usage: "path-based positive filter (repeatable, glob pattern)"},
			&ucli.StringSliceFlag{Name: "exclude-files", Usage: "path-based negative filter (repeatable, glob pattern)"},
		}
	}
	return &ucli.Command{
		Name:  "tag",
		Usage: "tag operations (list, counts, files, values, defs, set, get, check, verify, inspect)",
		Commands: []*ucli.Command{
			{
				Name:   "list",
				Usage:  "list all known tags with counts",
				Action: tagListAction,
			},
			{
				Name:      "counts",
				Usage:     "show count for each specified tag",
				ArgsUsage: "<tag>...",
				Action:    tagCountsAction,
			},
			{
				Name:      "files",
				Usage:     "show files containing specified tags",
				ArgsUsage: "<tag>...",
				Flags: append([]ucli.Flag{
					&ucli.BoolFlag{Name: "context", Usage: "show tag occurrences with context (line from tag to EOL)"},
				}, fileFilterFlags()...),
				Action: tagFilesAction,
			},
			{
				Name:      "values",
				Usage:     "show known values for tags",
				ArgsUsage: "<tag>...",
				Flags: append([]ucli.Flag{
					&ucli.BoolFlag{Name: "files", Usage: "show file paths for each value"},
				}, fileFilterFlags()...),
				Action: tagValuesAction,
			},
			{
				Name:      "defs",
				Usage:     "show tag definitions (from tags.md)",
				ArgsUsage: "[TAG...]",
				Flags: []ucli.Flag{
					&ucli.BoolFlag{Name: "path", Usage: "show source file path, not deduplicated"},
				},
				Action: tagDefsAction,
			},
			{
				Name:      "set",
				Usage:     "update or add tags in a file's tag block",
				ArgsUsage: "FILE TAG VALUE [TAG VALUE ...]",
				Action:    tagSetAction,
			},
			{
				Name:      "get",
				Usage:     "read tags from a file's tag block",
				ArgsUsage: "FILE [TAG ...]",
				Action:    tagGetAction,
			},
			{
				Name:      "check",
				Usage:     "validate tag block structure",
				ArgsUsage: "FILE [HEADING ...]",
				Action:    tagCheckAction,
			},
			{
				Name:  "verify",
				Usage: "verify ext routings, X records, and tag counts",
				Flags: []ucli.Flag{
					&ucli.BoolFlag{Name: "repair", Usage: "write corrections (default: read-only)"},
					&ucli.StringFlag{Name: "scope", Value: "all", Usage: "ext | tag-totals | all"},
				},
				Action: tagVerifyAction,
			},
			{
				Name:  "inspect",
				Usage: "dump @ext disk + in-memory state (read-only)",
				Flags: []ucli.Flag{
					&ucli.StringFlag{Name: "scope", Value: ark.ScopeExt, Usage: "ext (v1 only)"},
					&ucli.StringFlag{Name: "target", Usage: "path filter (narrow output to one file)"},
					&ucli.BoolFlag{Name: "json", Usage: "machine-readable output"},
				},
				Action: tagInspectAction,
			},
		},
	}
}

// CRC: crc-CLITree.md, crc-CLI.md | R122
func tagListAction(_ context.Context, _ *ucli.Command) error {
	proxyOrLocal(
		func(client *http.Client) error {
			var tags []ark.TagCount
			if err := proxyDecode(client, "GET", "/tags", nil, &tags); err != nil {
				return err
			}
			for _, t := range tags {
				fmt.Printf("%s\t%d\n", t.Tag, t.Count)
			}
			return nil
		},
		func(d *ark.DB) error {
			tags, err := d.TagList()
			if err != nil {
				return err
			}
			for _, t := range tags {
				fmt.Printf("%s\t%d\n", t.Tag, t.Count)
			}
			return nil
		},
	)
	return nil
}

// CRC: crc-CLITree.md, crc-CLI.md | R123
func tagCountsAction(_ context.Context, c *ucli.Command) error {
	tags := normalizeTagNames(c.Args().Slice())
	if len(tags) == 0 {
		fatal(fmt.Errorf("no tags specified"))
	}
	proxyOrLocal(
		func(client *http.Client) error {
			var counts []ark.TagCount
			if err := proxyDecode(client, "POST", "/tags/counts", map[string]any{"tags": tags}, &counts); err != nil {
				return err
			}
			for _, t := range counts {
				fmt.Printf("%s\t%d\n", t.Tag, t.Count)
			}
			return nil
		},
		func(d *ark.DB) error {
			counts, err := d.TagCounts(tags)
			if err != nil {
				return err
			}
			for _, t := range counts {
				fmt.Printf("%s\t%d\n", t.Tag, t.Count)
			}
			return nil
		},
	)
	return nil
}

// CRC: crc-CLITree.md, crc-CLI.md | R124, R125, R674
func tagFilesAction(_ context.Context, c *ucli.Command) error {
	filterFiles := c.StringSlice("filter-files")
	excludeFiles := c.StringSlice("exclude-files")
	tags := normalizeTagNames(c.Args().Slice())
	if len(tags) == 0 {
		fatal(fmt.Errorf("no tags specified"))
	}
	if c.Bool("context") {
		cmdTagFilesContext(tags, filterFiles, excludeFiles)
		return nil
	}
	proxyOrLocal(
		func(client *http.Client) error {
			var files []ark.TagFileInfo
			if err := proxyDecode(client, "POST", "/tags/files", map[string]any{"tags": tags}, &files); err != nil {
				return err
			}
			for _, f := range files {
				if matchPath(f.Path, filterFiles, excludeFiles) {
					fmt.Printf("%s\t%d\n", f.Path, f.Size)
				}
			}
			return nil
		},
		func(d *ark.DB) error {
			files, err := d.TagFiles(tags)
			if err != nil {
				return err
			}
			for _, f := range files {
				if matchPath(f.Path, filterFiles, excludeFiles) {
					fmt.Printf("%s\t%d\n", f.Path, f.Size)
				}
			}
			return nil
		},
	)
	return nil
}

// CRC: crc-CLITree.md, crc-CLI.md | R1131, R1132, R1133, R1134, R1135, R1136, R1137, R1138
func tagValuesAction(_ context.Context, c *ucli.Command) error {
	showFiles := c.Bool("files")
	filterFiles := c.StringSlice("filter-files")
	excludeFiles := c.StringSlice("exclude-files")
	tags := normalizeTagNames(c.Args().Slice())
	if len(tags) == 0 {
		fatal(fmt.Errorf("no tags specified"))
	}
	filtering := len(filterFiles) > 0 || len(excludeFiles) > 0
	withFiles := showFiles || filtering

	// emitFiles/emitCounts format both arms identically. R3001
	emitFiles := func(tag string, values []ark.TagValueFileInfo) {
		for _, v := range values {
			matched := v.Files
			if filtering {
				matched = nil
				for _, f := range v.Files {
					if matchPath(f, filterFiles, excludeFiles) {
						matched = append(matched, f)
					}
				}
				if len(matched) == 0 {
					continue
				}
			}
			fmt.Printf("%s\t%s\t%d\n", tag, v.Value, len(matched))
			if showFiles {
				for _, f := range matched {
					fmt.Printf("\t%s\n", f)
				}
			}
		}
	}
	emitCounts := func(tag string, values []ark.TagValueCount) {
		for _, v := range values {
			fmt.Printf("%s\t%s\t%d\n", tag, v.Value, v.Count)
		}
	}

	// R3001, R3003: /tags/values proxies (files flag → file-resolved
	// variant); the same DB methods run locally with no server.
	proxyOrLocal(
		func(client *http.Client) error {
			for _, tag := range tags {
				if withFiles {
					var values []ark.TagValueFileInfo
					if err := proxyDecode(client, "POST", "/tags/values", map[string]any{"tag": tag, "files": true}, &values); err != nil {
						return err
					}
					emitFiles(tag, values)
				} else {
					var values []ark.TagValueCount
					if err := proxyDecode(client, "POST", "/tags/values", map[string]any{"tag": tag}, &values); err != nil {
						return err
					}
					emitCounts(tag, values)
				}
			}
			return nil
		},
		func(d *ark.DB) error {
			for _, tag := range tags {
				if withFiles {
					values, err := d.TagValuesWithFiles(tag, "")
					if err != nil {
						return err
					}
					emitFiles(tag, values)
				} else {
					values, err := d.TagValues(tag, "")
					if err != nil {
						return err
					}
					emitCounts(tag, values)
				}
			}
			return nil
		},
	)
	return nil
}

// CRC: crc-CLITree.md, crc-CLI.md | R506, R507, R508, R509
func tagDefsAction(_ context.Context, c *ucli.Command) error {
	showPath := c.Bool("path")
	tags := normalizeTagNames(c.Args().Slice())

	printDefs := func(defs []ark.TagDefInfo) {
		if showPath {
			sort.Slice(defs, func(i, j int) bool {
				if defs[i].Path != defs[j].Path {
					return defs[i].Path < defs[j].Path
				}
				if defs[i].Tag != defs[j].Tag {
					return defs[i].Tag < defs[j].Tag
				}
				return defs[i].Description < defs[j].Description
			})
			for _, d := range defs {
				path := strings.ReplaceAll(d.Path, " ", "\\ ")
				fmt.Printf("%s %s %s\n", path, d.Tag, d.Description)
			}
		} else {
			sort.Slice(defs, func(i, j int) bool {
				if defs[i].Tag != defs[j].Tag {
					return defs[i].Tag < defs[j].Tag
				}
				return defs[i].Description < defs[j].Description
			})
			seen := make(map[string]bool)
			for _, d := range defs {
				key := d.Tag + "\t" + d.Description
				if seen[key] {
					continue
				}
				seen[key] = true
				fmt.Printf("%s %s\n", d.Tag, d.Description)
			}
		}
	}

	proxyOrLocal(
		func(client *http.Client) error {
			var defs []ark.TagDefInfo
			if err := proxyDecode(client, "POST", "/tags/defs", map[string]any{"tags": tags}, &defs); err != nil {
				return err
			}
			printDefs(defs)
			return nil
		},
		func(d *ark.DB) error {
			defs, err := d.TagDefs(tags)
			if err != nil {
				return err
			}
			printDefs(defs)
			return nil
		},
	)
	return nil
}

// tagSetAction / tagGetAction / tagCheckAction are thin adapters: the
// bodies (cmdTagSet/cmdTagGet/cmdTagCheck) stay in main.go because the
// `message` group wraps them. urfave intercepts -h/--help before the
// Action, so the handlers receive clean positionals via c.Args().Slice().

// CRC: crc-CLITree.md, crc-CLI.md | R607
func tagSetAction(_ context.Context, c *ucli.Command) error {
	cmdTagSet(c.Args().Slice())
	return nil
}

// CRC: crc-CLITree.md, crc-CLI.md | R608, R609
func tagGetAction(_ context.Context, c *ucli.Command) error {
	cmdTagGet(c.Args().Slice())
	return nil
}

// CRC: crc-CLITree.md, crc-CLI.md | R610, R611, R612
func tagCheckAction(_ context.Context, c *ucli.Command) error {
	cmdTagCheck(c.Args().Slice())
	return nil
}

// CRC: crc-CLITree.md, crc-CLI.md | R2092, R2093, R2099
func tagVerifyAction(_ context.Context, c *ucli.Command) error {
	repair := c.Bool("repair")
	scope := c.String("scope")
	switch scope {
	case "ext", "tag-totals", "all":
	default:
		fmt.Fprintf(os.Stderr, "error: invalid --scope %q (want ext, tag-totals, or all)\n", scope)
		os.Exit(2)
	}

	if client := serverClient(arkDir); client != nil {
		fmt.Fprintln(os.Stderr, "error: ark tag verify requires the server to be stopped (uses LMDB write txn)")
		fmt.Fprintln(os.Stderr, "       run 'ark stop' first")
		os.Exit(2)
	}

	var result ark.VerifyResult
	var verifyErr error
	withDB(func(d *ark.DB) {
		result, verifyErr = d.Verify(ark.VerifyOptions{Repair: repair, Scope: scope}, os.Stdout)
	})
	if verifyErr != nil {
		fmt.Fprintf(os.Stderr, "verify failed: %v\n", verifyErr)
		os.Exit(2)
	}
	if result.Issues > result.Repaired {
		os.Exit(1)
	}
	return nil
}

// CRC: crc-CLITree.md, crc-CLI.md | R2113, R2115, R2116, R2117
func tagInspectAction(_ context.Context, c *ucli.Command) error {
	scope := c.String("scope")
	target := c.String("target")
	asJSON := c.Bool("json")
	if scope != ark.ScopeExt {
		fmt.Fprintf(os.Stderr, "error: invalid --scope %q (only %q supported in v1)\n", scope, ark.ScopeExt)
		os.Exit(2)
	}

	emit := func(rep *ark.ExtInspectReport) {
		if asJSON {
			if err := rep.WriteJSON(os.Stdout); err != nil {
				fatal(err)
			}
			return
		}
		rep.WriteText(os.Stdout)
	}

	proxyOrLocal(
		func(client *http.Client) error {
			var rep ark.ExtInspectReport
			body := map[string]any{"scope": scope}
			if target != "" {
				body["target"] = target
			}
			if err := proxyDecode(client, "POST", "/tags/inspect", body, &rep); err != nil {
				return err
			}
			emit(&rep)
			return nil
		},
		func(d *ark.DB) error {
			rep, err := d.InspectExt(ark.InspectOptions{Scope: scope, Target: target})
			if err != nil {
				return err
			}
			rep.ServerSide = false
			rep.UnavailNote = "ExtMap state unavailable — server not running. Disk view only."
			rep.InMemory = nil
			emit(rep)
			return nil
		},
	)
	return nil
}
