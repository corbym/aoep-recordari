// Command harness runs the AOEP-v0 benchmark against one or more memory systems.
package main

import (
	"context"
	"flag"
	"fmt"
	"os"

	"aoep-recordari/internal/adapter/mem0paper"
	"aoep-recordari/internal/adapter/recordari"
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

		runner.PrintComparison(resA, resB)

		if err := runner.WriteResults(*outDir, resA); err != nil {
			fatalf("write recordari results: %v", err)
		}
		if err := runner.WriteResults(*outDir, resB); err != nil {
			fatalf("write mem0paper results: %v", err)
		}

	default:
		fatalf("unknown system %q — use recordari, mem0paper, or all", *system)
	}
}

func printSummary(r *runner.RunResult) {
	fmt.Printf("\n[%s] %d episodes, %d/%d obligations pass\n\n",
		r.System, r.TotalEpisodes, r.TotalPass, r.TotalObligation)
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
