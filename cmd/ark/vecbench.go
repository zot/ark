package main

// CRC: crc-CLI.md

import (
	"flag"
	"fmt"
	"math"
	"math/rand"
	"os"
	"sort"
	"time"

	"github.com/zot/ark"

	llama "github.com/godeps/gollama"
)

func cmdVec(args []string) {
	if len(args) == 0 {
		fmt.Fprintln(os.Stderr, "usage: ark vec <bench|bench-search> [options]")
		os.Exit(1)
	}
	sub := args[0]
	subArgs := args[1:]
	switch sub {
	case "bench":
		cmdVecBench(subArgs)
	case "bench-search":
		cmdVecBenchSearch(subArgs)
	default:
		fmt.Fprintf(os.Stderr, "unknown vec subcommand: %s\n", sub)
		os.Exit(1)
	}
}

// R547, R548, R549, R550, R551, R552, R553, R554, R555, R556, R557
func cmdVecBench(args []string) {
	fs := flag.NewFlagSet("vec bench", flag.ExitOnError)
	modelPath := fs.String("model", "", "path to GGUF model file (required)")
	n := fs.Int("n", 10, "number of chunks to embed")
	random := fs.Bool("random", false, "select chunks randomly")
	ctxSize := fs.Int("ctx", 2048, "context window size in tokens")
	prefix := fs.String("prefix", "", "text prepended before each chunk")
	fs.Parse(args)

	if *modelPath == "" {
		fmt.Fprintln(os.Stderr, "error: -model is required")
		fs.Usage()
		os.Exit(1)
	}

	// Load model and time it.
	fmt.Fprintf(os.Stderr, "loading model %s...\n", *modelPath)
	modelStart := time.Now()
	model, err := llama.LoadModel(*modelPath, llama.WithSilentLoading())
	if err != nil {
		fatal(fmt.Errorf("load model: %w", err))
	}
	defer model.Close()
	modelDur := time.Since(modelStart)
	fmt.Fprintf(os.Stderr, "model loaded in %s\n", modelDur.Round(time.Millisecond))

	ctx, err := model.NewContext(
		llama.WithEmbeddings(),
		llama.WithContext(*ctxSize),
	)
	if err != nil {
		fatal(fmt.Errorf("create context: %w", err))
	}
	defer ctx.Close()

	// Pull chunks from the index.
	chunks := collectBenchChunks(*n, *random)
	if len(chunks) == 0 {
		fmt.Fprintln(os.Stderr, "no chunks found in index")
		os.Exit(1)
	}

	// Embed each chunk and collect timings.
	fmt.Fprintf(os.Stderr, "embedding %d chunks...\n", len(chunks))
	var durations []time.Duration
	for i, chunk := range chunks {
		text := chunk
		if *prefix != "" {
			text = *prefix + text
		}
		start := time.Now()
		_, err := ctx.GetEmbeddings(text)
		dur := time.Since(start)
		if err != nil {
			fmt.Fprintf(os.Stderr, "chunk %d: error: %v\n", i, err)
			continue
		}
		durations = append(durations, dur)
		fmt.Printf("chunk %d: %6d bytes  %s\n", i, len(chunk), dur.Round(time.Microsecond))
	}

	if len(durations) == 0 {
		fmt.Fprintln(os.Stderr, "no successful embeddings")
		os.Exit(1)
	}

	printBenchSummary(durations, modelDur)
}

// R558, R559, R560, R561, R562
func cmdVecBenchSearch(args []string) {
	fs := flag.NewFlagSet("vec bench-search", flag.ExitOnError)
	modelPath := fs.String("model", "", "path to GGUF model file (required)")
	query := fs.String("query", "", "search query (required)")
	k := fs.Int("k", 10, "number of results")
	prefix := fs.String("prefix", "", "query embedding prefix")
	ctxSize := fs.Int("ctx", 2048, "context window size in tokens")
	fs.Parse(args)

	if *modelPath == "" || *query == "" {
		fmt.Fprintln(os.Stderr, "error: -model and -query are required")
		fs.Usage()
		os.Exit(1)
	}

	fmt.Fprintf(os.Stderr, "loading model %s...\n", *modelPath)
	modelStart := time.Now()
	model, err := llama.LoadModel(*modelPath, llama.WithSilentLoading())
	if err != nil {
		fatal(fmt.Errorf("load model: %w", err))
	}
	defer model.Close()
	modelDur := time.Since(modelStart)
	fmt.Fprintf(os.Stderr, "model loaded in %s\n", modelDur.Round(time.Millisecond))

	ctx, err := model.NewContext(
		llama.WithEmbeddings(),
		llama.WithContext(*ctxSize),
	)
	if err != nil {
		fatal(fmt.Errorf("create context: %w", err))
	}
	defer ctx.Close()

	text := *query
	if *prefix != "" {
		text = *prefix + text
	}

	embedStart := time.Now()
	_, err = ctx.GetEmbeddings(text)
	embedDur := time.Since(embedStart)
	if err != nil {
		fatal(fmt.Errorf("embed query: %w", err))
	}
	fmt.Printf("query embedding: %s\n", embedDur.Round(time.Microsecond))

	// Check how many vectors exist in the index.
	withDB(func(d *ark.DB) {
		info, err := d.Status()
		if err != nil {
			fatal(err)
		}
		fmt.Printf("indexed chunks: %d\n", info.Chunks)
		fmt.Printf("search with k=%d would need stored vectors (not yet benchmarked)\n", *k)
	})
}

// collectBenchChunks reads files from the index and extracts text chunks.
func collectBenchChunks(n int, random bool) []string {
	var chunks []string
	withDB(func(d *ark.DB) {
		files, err := d.Files()
		if err != nil {
			fatal(err)
		}
		if len(files) == 0 {
			return
		}

		if random {
			rand.Shuffle(len(files), func(i, j int) {
				files[i], files[j] = files[j], files[i]
			})
		}

		// Read files and split into ~512 byte chunks until we have enough.
		for _, fpath := range files {
			if len(chunks) >= n {
				break
			}
			data, err := os.ReadFile(fpath)
			if err != nil {
				continue // skip unreadable files
			}
			if len(data) == 0 {
				continue
			}
			for off := 0; off < len(data) && len(chunks) < n; off += 512 {
				end := off + 512
				if end > len(data) {
					end = len(data)
				}
				chunks = append(chunks, string(data[off:end]))
			}
		}
	})
	return chunks
}

func printBenchSummary(durations []time.Duration, modelDur time.Duration) {
	sort.Slice(durations, func(i, j int) bool {
		return durations[i] < durations[j]
	})

	var total time.Duration
	for _, d := range durations {
		total += d
	}

	n := len(durations)
	mean := total / time.Duration(n)
	median := durations[n/2]
	min := durations[0]
	max := durations[n-1]

	chunksPerSec := float64(n) / total.Seconds()

	fmt.Println()
	fmt.Printf("model load:   %s\n", modelDur.Round(time.Millisecond))
	fmt.Printf("chunks:       %d\n", n)
	fmt.Printf("min:          %s\n", min.Round(time.Microsecond))
	fmt.Printf("max:          %s\n", max.Round(time.Microsecond))
	fmt.Printf("mean:         %s\n", mean.Round(time.Microsecond))
	fmt.Printf("median:       %s\n", median.Round(time.Microsecond))
	fmt.Printf("total:        %s\n", total.Round(time.Millisecond))
	fmt.Printf("chunks/sec:   %.1f\n", math.Round(chunksPerSec*10)/10)
}
