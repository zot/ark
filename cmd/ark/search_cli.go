package main

// The `search` command — the filter-stack DSL keeper, and the last group
// to migrate to the urfave/cli v3 tree. Unlike every other migrated
// command, search keeps parsing all of its own args: the order-sensitive
// filter stack (sticky -with/-without polarity, repeated (polarity, mode,
// query) tuples, -parse) cannot be modelled as urfave flags. So the node
// sets SkipFlagParsing:true and hands the raw argv to the unchanged
// cmdSearch/parseFilterStack parser (kept in main.go).

import (
	ucli "github.com/urfave/cli/v3"
)

// searchHelp is the single-source DSL help for `search`. The node's
// Description (rendered by `ark help search`) and cmdSearch's fs.Usage
// (printed by `ark search -h` — which urfave never intercepts, because
// SkipFlagParsing leaves -h in the raw args) render the same text, so the
// authored DSL blurb lives in exactly one place (R2921: one authored
// source, not two drifting copies). The flag-by-flag list is appended by
// cmdSearch's fs.PrintDefaults() under an "Output:" header on the -h path.
const searchHelp = `Usage: ark search [TERM...] [filters] [options]

Filter modes (composable, repeatable):
  -contains TERM    substring match (default for bare terms)
  -fuzzy TERM       typo-tolerant match
  -regex PATTERN    regular expression match
  -tag TAG          tag filter (@name:value or name:value, @ optional)
  -file-tag TAG     file-tag filter (every chunk on a file with the tag)
  -about QUERY      vector similarity match
  -files GLOB       file path glob filter

Polarity (default: -with):
  -with             subsequent filters intersect (must match)
  -without          subsequent filters subtract (must not match)

Per-filter tuning:
  --filter-k N      after an -about filter, override the top-K chunk
                    cap for that row (default: about_filter_top_k=200)

  -parse            print disambiguated command and exit

Bare terms coalesce into a single -contains. The first filter is the
primary search; the rest are chunk-level post-filters. Use -parse to
verify how your args are interpreted.

Examples:
  ark search fred ethel
      Searches for "fred ethel" (bare terms coalesce)

  ark search fred -without -tag status:done -with -files '*.md'
      Search "fred", exclude done items, limit to markdown files

  ark search -fuzzy concurency -without -regex '(?i)test'
      Fuzzy primary, exclude chunks matching "test"

  ark search -about "machine learning" -without -tag project:archive
      Vector similarity search, exclude archived project

  ark search -parse fred -without -tag done -files '*.md'
      Print disambiguated command and exit`

// searchCommand returns the `search` DSL node. SkipFlagParsing routes ALL
// raw args to the unchanged cmdSearch (via flatDelegate, which passes
// c.Args().Slice()), so parseFilterStack, the -parse short-circuit, and
// the `expand` subcommand keep working exactly as before. Self-documents
// via the single hand-written Description above.
// CRC: crc-CLITree.md | Seq: seq-cli-urfave.md#2.1 | R2921, R2924, R2925
func searchCommand() *ucli.Command {
	return &ucli.Command{
		Name:            "search",
		Usage:           "search the index (filter-stack DSL; subcommand: expand)",
		ArgsUsage:       "[TERM...] [filters] [options]",
		Description:     searchHelp,
		SkipFlagParsing: true,
		Action:          flatDelegate(cmdSearch),
	}
}
