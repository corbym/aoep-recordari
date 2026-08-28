// Command harness runs the AOEP-v0 benchmark against one or more memory systems.
package main

import (
	"context"
	"flag"
	"fmt"
	"os"

	"aoep-recordari/internal/adapter/mem0paper"
	"aoep-recordari/internal/adapter/nomem"
	"aoep-recordari/internal/adapter/recordari"
	"aoep-recordari/internal/adapter/reducer"
	"aoep-recordari/internal/episode"
	"aoep-recordari/internal/runner"
)

func main() {
	episodesDir := flag.String("episodes", "./episodes", "Directory containing episode JSON files")
	system := flag.String("system", "recordari", "System to benchmark: recordari, mem0paper, or all")
	outDir := flag.String("out", "./results", "Directory to write result JSON files")
	flag.Parse()

	episodes, err := episode.LoadAll(*episodesDir)
	if err != nil {
		fatalf("load episodes: %v", err)
	}
	fmt.Printf("Loaded %d episodes from %s\n", len(episodes), *episodesDir)

	ctx := context.Background()

	switch *system {
	case "reducer":
		a := reducer.New()
		result, err := runner.Run(ctx, a, episodes)
		if err != nil {
			fatalf("run: %v", err)
		}
		printSummary(result)
		if err := runner.WriteResults(*outDir, result); err != nil {
			fatalf("write results: %v", err)
		}

	case "nomem":
		a := nomem.New()
		result, err := runner.Run(ctx, a, episodes)
		if err != nil {
			fatalf("run: %v", err)
		}
		printSummary(result)
		if err := runner.WriteResults(*outDir, result); err != nil {
			fatalf("write results: %v", err)
		}

	case "recordari":
		a := recordari.New(mustEnv("RECORDARI_MCP_URL"), mustEnv("RECORDARI_API_KEY"))
		result, err := runner.Run(ctx, a, episodes)
		if err != nil {
			fatalf("run: %v", err)
		}
		printSummary(result)
		if err := runner.WriteResults(*outDir, result); err != nil {
			fatalf("write results: %v", err)
		}

	case "mem0paper":
		mem0URL := os.Getenv("MEM0_URL")
		if mem0URL == "" {
			mem0URL = "http://localhost:8888"
		}
		a := mem0paper.New(mem0URL)
		result, err := runner.Run(ctx, a, episodes)
		if err != nil {
			fatalf("run: %v", err)
		}
		printSummary(result)
		if err := runner.WriteResults(*outDir, result); err != nil {
			fatalf("write results: %v", err)
		}

	case "all":
		rRed := reducer.New()
		resRed, err := runner.Run(ctx, rRed, episodes)
		if err != nil {
			fatalf("reducer run: %v", err)
		}

		rNom := nomem.New()
		resNom, err := runner.Run(ctx, rNom, episodes)
		if err != nil {
			fatalf("nomem run: %v", err)
		}

		rA := recordari.New(mustEnv("RECORDARI_MCP_URL"), mustEnv("RECORDARI_API_KEY"))
		resA, err := runner.Run(ctx, rA, episodes)
		if err != nil {
			fatalf("recordari run: %v", err)
		}

		mem0URL := os.Getenv("MEM0_URL")
		if mem0URL == "" {
			mem0URL = "http://localhost:8888"
		}
		rB := mem0paper.New(mem0URL)
		resB, err := runner.Run(ctx, rB, episodes)
		if err != nil {
			fatalf("mem0paper run: %v", err)
		}

		runner.PrintComparison(resRed, resNom, resA, resB)

		for _, res := range []*runner.RunResult{resRed, resNom, resA, resB} {
			if err := runner.WriteResults(*outDir, res); err != nil {
				fatalf("write %s results: %v", res.System, err)
			}
		}

	default:
		fatalf("unknown system %q — use reducer, nomem, recordari, mem0paper, or all", *system)
	}
}

func printSummary(r *runner.RunResult) {
	fmt.Printf("\n[%s] %d episodes — obligation_pass %d/%d, neg-invariant_pass %d/%d\n\n",
		r.System, r.TotalEpisodes,
		r.TotalPass, r.TotalObligation,
		r.TotalNegativeInvariantPass, r.TotalNegativeInvariantTotal)
	for _, ep := range r.EpisodeResults {
		fmt.Print(ep.Summary())
	}
}

func mustEnv(key string) string {
	v := os.Getenv(key)
	if v == "" {
		fatalf("required env var %s is not set", key)
	}
	return v
}

func fatalf(format string, args ...any) {
	fmt.Fprintf(os.Stderr, "harness: "+format+"\n", args...)
	os.Exit(1)
}
