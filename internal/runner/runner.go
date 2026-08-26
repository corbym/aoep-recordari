// Package runner orchestrates the full AOEP benchmark run for a given system adapter.
package runner

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"aoep-recordari/internal/adapter"
	"aoep-recordari/internal/episode"
	"aoep-recordari/internal/schema"
	"aoep-recordari/internal/snapshot"
	"aoep-recordari/internal/validator"
)

// RunResult is the aggregate output of running all episodes through one system.
type RunResult struct {
	System          string
	RunAt           time.Time
	EpisodeResults  []*validator.Result
	TotalObligation int
	TotalPass       int
	TotalEpisodes   int
}

// Run executes all episodes against the given adapter and returns the aggregate result.
func Run(ctx context.Context, a adapter.SystemAdapter, episodes []*episode.Episode) (*RunResult, error) {
	teardown, err := a.Setup(ctx)
	if err != nil {
		return nil, fmt.Errorf("setup %s: %w", a.Name(), err)
	}
	defer func() {
		if err := teardown(ctx); err != nil {
			fmt.Fprintf(os.Stderr, "teardown error: %v\n", err)
		}
	}()

	result := &RunResult{
		System: a.Name(),
		RunAt:  time.Now().UTC(),
	}

	for _, ep := range episodes {
		epResult, err := runEpisode(ctx, a, ep)
		if err != nil {
			fmt.Fprintf(os.Stderr, "episode %s failed: %v\n", ep.ID, err)
			continue
		}
		epResult.SystemName = a.Name()
		result.EpisodeResults = append(result.EpisodeResults, epResult)
		result.TotalObligation += epResult.TotalObligations
		result.TotalPass += epResult.ObligationPass
		result.TotalEpisodes++
	}

	return result, nil
}

func runEpisode(ctx context.Context, a adapter.SystemAdapter, ep *episode.Episode) (*validator.Result, error) {
	// resourceMap: AOEP resource name (from event payload "label" or event ID) → system-assigned ID.
	resourceMap := make(map[string]string)

	// 1. Deliver events (stripped of outcome-revealing fields).
	strippedEvents := episode.StripForDelivery(ep)
	for i, ev := range strippedEvents {
		sysID, err := a.DeliverEvent(ctx, ev)
		if err != nil {
			// Log but continue — ungoverned systems may reject valid writes.
			fmt.Fprintf(os.Stderr, "  event %s delivery error: %v\n", ev.ID, err)
		}
		// Map: prefer payload label, fallback to event ID, fallback to idempotency_key.
		key := ev.ID
		if ev.Payload != nil {
			if label, ok := ev.Payload["label"].(string); ok && label != "" {
				key = label
			}
		}
		if sysID != "" {
			resourceMap[key] = sysID
		}
		// Also map idempotency_key → sysID for idempotency checks.
		if ev.IdempotencyKey != "" && sysID != "" {
			resourceMap[ev.IdempotencyKey] = sysID
		}
		_ = i
	}

	// 2. Run probes (neutral queries naming only actor + target resource).
	var probeResponses []*schema.ProbeResponse
	for _, probe := range ep.Probes {
		resp, err := a.RunProbe(ctx, probe, resourceMap)
		if err != nil {
			resp = &schema.ProbeResponse{ProbeID: probe.ID, Error: err.Error()}
		}
		probeResponses = append(probeResponses, resp)
	}

	// 3. Reconstruct state snapshot from probe responses.
	snap := snapshot.Reconstruct(ep.Probes, probeResponses)

	// 4. Validate against ground truth.
	return validator.Validate(ep, snap, resourceMap), nil
}

// WriteResults writes the run result to a JSON file in outDir.
func WriteResults(outDir string, result *RunResult) error {
	if err := os.MkdirAll(outDir, 0o755); err != nil {
		return err
	}
	filename := fmt.Sprintf("%s_%s.json", result.System, result.RunAt.Format("20060102T150405Z"))
	path := filepath.Join(outDir, filename)

	data, err := json.MarshalIndent(result, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(path, data, 0o644)
}

// PrintComparison prints a side-by-side comparison table of two run results.
func PrintComparison(a, b *RunResult) {
	fmt.Printf("\n=== AOEP-v0 Benchmark Results ===\n\n")
	fmt.Printf("%-40s %12s %12s\n", "Obligation", a.System, b.System)
	fmt.Printf("%s\n", repeatStr("-", 66))

	obligations := []string{
		validator.ObligNoUnauthorisedWrites,
		validator.ObligPermissionEpochCurrent,
		validator.ObligNoStaleActionExecuted,
		validator.ObligNoScopeLeakage,
		validator.ObligNoDeletedContentVisible,
		validator.ObligDeletionLedgerSubsetMatch,
		validator.ObligNoUntrustedInstrPromoted,
		validator.ObligRollbackLedgerSubsetMatch,
		validator.ObligNoExternalActionWoApproval,
	}

	aByName := aggregateByObligation(a)
	bByName := aggregateByObligation(b)

	for _, name := range obligations {
		aPass, aTotal := aByName[name][0], aByName[name][1]
		bPass, bTotal := bByName[name][0], bByName[name][1]
		fmt.Printf("%-40s %5d/%-5d %5d/%-5d\n", name, aPass, aTotal, bPass, bTotal)
	}

	fmt.Printf("%s\n", repeatStr("-", 66))
	fmt.Printf("%-40s %5d/%-5d %5d/%-5d\n", "TOTAL (obligation_pass)",
		a.TotalPass, a.TotalObligation,
		b.TotalPass, b.TotalObligation)
	fmt.Println()
}

func aggregateByObligation(r *RunResult) map[string][2]int {
	out := make(map[string][2]int)
	for _, ep := range r.EpisodeResults {
		for name, ob := range ep.Obligations {
			prev := out[name]
			prev[1]++
			if ob.Pass {
				prev[0]++
			}
			out[name] = prev
		}
	}
	return out
}

func repeatStr(s string, n int) string {
	var sb strings.Builder
	for range n {
		sb.WriteString(s)
	}
	return sb.String()
}
