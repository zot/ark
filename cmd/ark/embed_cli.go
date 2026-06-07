package main

// The `ark embed` command group, migrated to the urfave/cli v3 command
// tree (Stage 2 of the CLI urfave migration). embed {text, bench,
// validate}: each node declares its flags so --help is generated, and
// each Action reuses the existing ark.Librarian / Store helpers (the
// bench helpers cmdEmbedBenchTags/cmdEmbedBenchChunks stay in main.go).
// (R1791–R1813; see crc-CLITree.md.)
//
// Note: `validate`'s `-v` (per-file detail) is shadowed during the
// transition — main() pre-parses every `-v` as global verbosity before
// the tree sees it, exactly as the legacy flag.FlagSet path was. The
// final globals-fold step resolves the collision.

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"strings"

	ucli "github.com/urfave/cli/v3"
	"github.com/zot/ark"
)

// CRC: crc-CLITree.md, crc-CLI.md | Seq: seq-cli-urfave.md#3.3 | R1302-R1305, R1790, R1791, R1792, R1793, R1794
func embedCommand() *ucli.Command {
	return &ucli.Command{
		Name:  "embed",
		Usage: "embedding operations (text, bench, validate)",
		Commands: []*ucli.Command{
			{
				Name:      "text",
				Usage:     "embed text, print vector as JSON",
				ArgsUsage: "TEXT...",
				Action:    embedTextAction,
			},
			{
				Name:      "bench",
				Usage:     "benchmark embedding performance (tags or chunks)",
				ArgsUsage: "<tags|chunks>",
				Flags: []ucli.Flag{
					&ucli.IntFlag{Name: "ctx", Value: 2048, Usage: "embedding context window size in tokens"},
					&ucli.IntFlag{Name: "parallel", Value: 8, Usage: "number of parallel sequences per batch"},
				},
				Action: embedBenchAction,
			},
			{
				Name:  "validate",
				Usage: "cross-reference embedding records (EC/EF) against FTS chunks",
				Flags: []ucli.Flag{
					&ucli.BoolFlag{Name: "fix", Usage: "delete orphan EC/EF records and wrong-dimension EC records"},
					&ucli.BoolFlag{Name: "v", Usage: "show per-file detail"},
				},
				Action: embedValidateAction,
			},
		},
	}
}

// CRC: crc-CLITree.md, crc-CLI.md | R1791, R1795, R1796, R1797
func embedTextAction(_ context.Context, c *ucli.Command) error {
	rest := c.Args().Slice()
	if len(rest) < 1 {
		fatal(fmt.Errorf("ark embed text: TEXT is required"))
	}
	text := strings.Join(rest, " ")
	withDB(func(db *ark.DB) {
		lib := ark.NewLibrarian(db, arkDir)
		if !lib.Available() {
			fatal(fmt.Errorf("claude not on PATH"))
		}
		if !lib.EmbeddingAvailable() {
			fatal(fmt.Errorf("tag_model not configured in ark.toml or model file not found"))
		}
		vec, err := lib.EmbedQuery(text)
		if err != nil {
			fatal(err)
		}
		out, err := json.Marshal(vec)
		if err != nil {
			fatal(err)
		}
		os.Stdout.Write(out)
		fmt.Fprintln(os.Stdout)
	})
	return nil
}

// CRC: crc-CLITree.md, crc-CLI.md | R1792, R1793, R1798, R1799, R1800, R1801
func embedBenchAction(_ context.Context, c *ucli.Command) error {
	if c.Args().Len() < 1 {
		fatal(fmt.Errorf("ark embed bench: mode is required (tags or chunks)"))
	}
	mode := c.Args().First()
	if mode != "tags" && mode != "chunks" {
		fatal(fmt.Errorf("unknown bench mode: %s (expected tags or chunks)", mode))
	}
	ctxSize := c.Int("ctx")
	parallel := c.Int("parallel")
	withDB(func(db *ark.DB) {
		lib := ark.NewLibrarian(db, arkDir)
		if !lib.Available() {
			fatal(fmt.Errorf("claude not on PATH"))
		}
		if !lib.EmbeddingAvailable() {
			fatal(fmt.Errorf("tag_model not configured in ark.toml or model file not found"))
		}
		lib.SetCtxSize(ctxSize)
		lib.SetParallel(parallel)

		switch mode {
		case "tags":
			cmdEmbedBenchTags(db, lib)
		case "chunks":
			cmdEmbedBenchChunks(db, lib, ctxSize, parallel)
		}
	})
	return nil
}

// CRC: crc-CLITree.md, crc-CLI.md | R1794
func embedValidateAction(_ context.Context, c *ucli.Command) error {
	runEmbedValidate(c.Bool("fix"), c.Bool("v"))
	return nil
}

// runEmbedValidate cross-references EC/EF embedding records against FTS
// chunks, reporting (and, with fix, deleting) orphans, missing records,
// and dimension inconsistencies. Extracted from the legacy
// cmdEmbedValidate so the urfave Action stays a thin adapter; exits 1
// when problems remain.
// CRC: crc-CLITree.md, crc-CLI.md | Seq: seq-embed-validate.md | R1794, R1805, R1806, R1807, R1808, R1809, R1810, R1811, R1812, R1813
func runEmbedValidate(fix, verbose bool) {
	problems := 0
	withDB(func(db *ark.DB) {
		store := db.Store()

		// R1855: scan EC records (keyed by chunkID)
		ecDims, err := store.ScanChunkEmbeddingKeys()
		if err != nil {
			fatal(fmt.Errorf("scan EC records: %w", err))
		}

		// R1856, R1865: collect chunkIDs partitioned by search_exclude
		excludePatterns := db.Config().SearchExclude
		ftsChunkIDs, excludedChunkIDs, err := db.AllChunkIDsPartitioned(excludePatterns)
		if err != nil {
			fatal(fmt.Errorf("scan chunk IDs: %w", err))
		}

		// Scan EF records
		efCounts, err := store.ScanFileCentroidCounts()
		if err != nil {
			fatal(fmt.Errorf("scan EF records: %w", err))
		}

		// R1855: orphan EC — chunkID has EC record but no C record
		var orphanIDs []uint64
		for chunkID := range ecDims {
			if !ftsChunkIDs[chunkID] {
				orphanIDs = append(orphanIDs, chunkID)
			}
		}
		if len(orphanIDs) > 0 {
			fmt.Printf("orphan EC records: %d (chunkID without C record)\n", len(orphanIDs))
			problems += len(orphanIDs)
			if verbose {
				for _, id := range orphanIDs {
					fmt.Printf("  chunkID=%d\n", id)
				}
			}
		}

		// R1856, R1865: missing EC — embeddable chunkID has C record but no EC record
		var missingCount int
		for chunkID := range ftsChunkIDs {
			if _, has := ecDims[chunkID]; !has {
				missingCount++
			}
		}
		if missingCount > 0 {
			fmt.Printf("missing EC records: %d (unique chunks without embeddings)\n", missingCount)
			problems += missingCount
		}
		// R1866: report excluded chunks separately
		if len(excludedChunkIDs) > 0 {
			fmt.Printf("excluded chunks: %d (in search_exclude files only, not embedded)\n", len(excludedChunkIDs))
		}

		// R1857: EF consistency — check centroid counts
		ftsFiles, err := db.FileChunkCounts()
		if err != nil {
			fatal(fmt.Errorf("scan FTS files: %w", err))
		}
		var orphanEFIDs []uint64
		for fid := range efCounts {
			if _, hasFTS := ftsFiles[fid]; !hasFTS {
				orphanEFIDs = append(orphanEFIDs, fid)
			}
		}
		if len(orphanEFIDs) > 0 {
			fmt.Printf("orphan EF records: %d\n", len(orphanEFIDs))
			problems += len(orphanEFIDs)
		}

		// Separate sentinels (dim=0) from real embeddings
		sentinelCount := 0
		dimCounts := make(map[int]int)
		for _, dim := range ecDims {
			if dim == 0 {
				sentinelCount++
			} else {
				dimCounts[dim]++
			}
		}
		if sentinelCount > 0 {
			fmt.Printf("sentinel EC records: %d (chunks exceeding all embed tiers)\n", sentinelCount)
		}

		// Dimension consistency (R1806) — sentinels excluded
		var majorityDim, majorityCount int
		for d, c := range dimCounts {
			if c > majorityCount {
				majorityDim = d
				majorityCount = c
			}
		}
		realEC := len(ecDims) - sentinelCount
		wrongDim := realEC - majorityCount
		if len(dimCounts) > 1 {
			fmt.Printf("dimension inconsistency: majority dim=%d (%d records)\n", majorityDim, majorityCount)
			for d, c := range dimCounts {
				if d != majorityDim {
					fmt.Printf("  dim=%d: %d records\n", d, c)
				}
			}
			problems += wrongDim
		}

		if problems == 0 {
			fmt.Printf("clean: %d EC records (%d embedded, %d sentinel), %d embeddable chunks, %d excluded chunks, %d EF records\n",
				len(ecDims), realEC, sentinelCount, len(ftsChunkIDs), len(excludedChunkIDs), len(efCounts))
		}

		// R1858: fix
		if fix && problems > 0 {
			var fixedOrphanEC, fixedOrphanEF, fixedWrongDim int

			for _, chunkID := range orphanIDs {
				if err := store.DeleteChunkEmbedding(chunkID); err != nil {
					fmt.Fprintf(os.Stderr, "fix: delete EC chunkID=%d: %v\n", chunkID, err)
				} else {
					fixedOrphanEC++
				}
			}

			for _, fid := range orphanEFIDs {
				if err := store.DeleteFileCentroid(fid); err != nil {
					fmt.Fprintf(os.Stderr, "fix: delete EF fileID=%d: %v\n", fid, err)
				} else {
					fixedOrphanEF++
				}
			}

			if len(dimCounts) > 1 {
				for chunkID, dim := range ecDims {
					if dim != majorityDim {
						if err := store.DeleteChunkEmbedding(chunkID); err != nil {
							fmt.Fprintf(os.Stderr, "fix: delete wrong-dim EC chunkID=%d: %v\n", chunkID, err)
						} else {
							fixedWrongDim++
						}
					}
				}
			}

			fmt.Printf("fixed: %d orphan EC, %d orphan EF, %d wrong-dim EC deleted\n",
				fixedOrphanEC, fixedOrphanEF, fixedWrongDim)
		}
	})
	if problems > 0 {
		os.Exit(1)
	}
}
