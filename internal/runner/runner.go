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
	// TotalNegativeInvariantPass / TotalNegativeInvariantTotal: neg-invariant scores
	// tracked separately from positive obligations (amnesia scores 100% here).
	TotalNegativeInvariantPass  int
	TotalNegativeInvariantTotal int
	// TotalDeliveryErrors counts event-delivery failures across all episodes. A non-zero
	// value means some writes never reached the SUT, so "resource absent" scores derived
	// from those episodes are unreliable — the run should not be published as-is.
	TotalDeliveryErrors int
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
		a.ResetEpisode()
		epResult, err := runEpisode(ctx, a, ep)
		if err != nil {
			fmt.Fprintf(os.Stderr, "episode %s failed: %v\n", ep.ID, err)
			continue
		}
		epResult.SystemName = a.Name()
		result.EpisodeResults = append(result.EpisodeResults, epResult)
		result.TotalObligation += epResult.TotalObligations
		result.TotalPass += epResult.ObligationPass
		result.TotalNegativeInvariantPass += epResult.NegativeInvariantPass
		result.TotalNegativeInvariantTotal += epResult.NegativeInvariantTotal
		result.TotalDeliveryErrors += len(epResult.DeliveryErrors)
		result.TotalEpisodes++
	}

	if result.TotalDeliveryErrors > 0 {
		fmt.Fprintf(os.Stderr,
			"WARNING: %s run had %d delivery error(s); scores derived from absent resources are unreliable\n",
			a.Name(), result.TotalDeliveryErrors)
	}

	return result, nil
}

func runEpisode(ctx context.Context, a adapter.SystemAdapter, ep *episode.Episode) (*validator.Result, error) {
	// resourceMap: AOEP resource name (from event payload "label" or event ID) → system-assigned ID.
	resourceMap := make(map[string]string)

	// 1. Deliver events (stripped of outcome-revealing fields).
	var deliveryErrors []string
	strippedEvents := episode.StripForDelivery(ep)
	for i, ev := range strippedEvents {
		sysID, err := a.DeliverEvent(ctx, ev)
		if err != nil {
			// Record the failure so it reaches the results JSON. A swallowed delivery
			// error would let "the write never landed" masquerade as "governance blocked it".
			deliveryErrors = append(deliveryErrors, fmt.Sprintf("event %s: %v", ev.ID, err))
			fmt.Fprintf(os.Stderr, "  event %s delivery error: %v\n", ev.ID, err)
		}
		// Map by event ID (always) and by payload label when present.
		// Both keys are needed: the stale-action check looks up ev.ID / IdempotentWith;
		// other checks (deletion, scope) look up the resource label.
		// When a label is absent, key == ev.ID so the second assignment is redundant but harmless.
		key := ev.ID
		if ev.Payload != nil {
			if label, ok := ev.Payload["label"].(string); ok && label != "" {
				key = label
			}
		}
		if sysID != "" {
			resourceMap[ev.ID] = sysID
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
	res := validator.Validate(ep, snap, resourceMap)
	res.DeliveryErrors = deliveryErrors
	return res, nil
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

// PrintComparison prints a side-by-side comparison table of one or more run results.
func PrintComparison(results ...*RunResult) {
	if len(results) == 0 {
		return
	}

	// Positive obligations (§9.3 headline metric).
	positiveObligations := []string{
		validator.ObligNoUnauthorisedWrites,
		validator.ObligPermissionEpochCurrent,
		validator.ObligNoStaleActionExecuted,
		validator.ObligNoExternalActionWoApproval,
		validator.ObligConflictSurfaced,
		validator.ObligDeletionLedgerSubsetMatch,
		validator.ObligRollbackLedgerSubsetMatch,
	}
	// Negative invariants (amnesia trivially passes all; NOT included in headline).
	negativeInvariants := []string{
		validator.ObligNoScopeLeakage,
		validator.ObligNoDeletedContentVisible,
		validator.ObligNoUntrustedInstrPromoted,
	}

	byName := make([]map[string][2]int, len(results))
	for i, r := range results {
		byName[i] = aggregateByObligation(r)
	}

	headerFmt := "%-42s"
	colFmt := " %11s"
	var rb strings.Builder
	rb.WriteString("%-42s")
	for range results {
		rb.WriteString(" %5d/%-5d")
	}
	rb.WriteString("\n")
	rowFmt := rb.String()

	width := 42 + 12*len(results)

	fmt.Printf("\n=== AOEP-v0 Benchmark Results (exercised episodes only) ===\n\n")
	fmt.Printf(headerFmt, "Obligation")
	for _, r := range results {
		fmt.Printf(colFmt, r.System)
	}
	fmt.Println()
	fmt.Println(repeatStr("-", width))

	fmt.Println("--- Positive Obligations (headline obligation_pass) ---")
	for _, name := range positiveObligations {
		args := []any{name}
		for i := range results {
			args = append(args, byName[i][name][0], byName[i][name][1])
		}
		fmt.Printf(rowFmt, args...)
	}

	fmt.Println(repeatStr("-", width))
	totalArgs := []any{"TOTAL obligation_pass"}
	for _, r := range results {
		totalArgs = append(totalArgs, r.TotalPass, r.TotalObligation)
	}
	fmt.Printf(rowFmt, totalArgs...)

	fmt.Println()
	fmt.Println("--- Negative Invariants (amnesia scores 100% — NOT the headline) ---")
	for _, name := range negativeInvariants {
		args := []any{name}
		for i := range results {
			args = append(args, byName[i][name][0], byName[i][name][1])
		}
		fmt.Printf(rowFmt, args...)
	}

	fmt.Println(repeatStr("-", width))
	negArgs := []any{"TOTAL neg-invariant_pass"}
	for _, r := range results {
		negArgs = append(negArgs, r.TotalNegativeInvariantPass, r.TotalNegativeInvariantTotal)
	}
	fmt.Printf(rowFmt, negArgs...)
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
