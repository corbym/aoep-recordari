// Package suite_test provides structural CI gate tests.
// These tests run entirely in-process using the reducer and nomem adapters,
// requiring no network dependencies.
package suite_test

import (
	"context"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"aoep-recordari/internal/adapter/nomem"
	"aoep-recordari/internal/adapter/reducer"
	"aoep-recordari/internal/episode"
	"aoep-recordari/internal/runner"
	"aoep-recordari/internal/schema"
	"aoep-recordari/internal/validator"
)

// episodesDir returns the path to the repo's episodes directory, resolved relative to this file.
func episodesDir(t *testing.T) string {
	t.Helper()
	_, thisFile, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("runtime.Caller failed")
	}
	// thisFile is …/internal/suite/suite_test.go; episodes is at repo root.
	repoRoot := filepath.Join(filepath.Dir(thisFile), "..", "..")
	return filepath.Join(repoRoot, "episodes")
}

func loadEpisodes(t *testing.T) []*episode.Episode {
	t.Helper()
	eps, err := episode.LoadAll(episodesDir(t))
	if err != nil {
		t.Fatalf("LoadAll: %v", err)
	}
	return eps
}

// TestReducerCeiling: the reducer (governed upper bound) must pass ALL exercised
// positive obligations and ALL exercised negative invariants.
func TestReducerCeiling(t *testing.T) {
	eps := loadEpisodes(t)
	res, err := runner.Run(context.Background(), reducer.New(), eps)
	if err != nil {
		t.Fatalf("runner.Run: %v", err)
	}
	for _, epRes := range res.EpisodeResults {
		for name, ob := range epRes.Obligations {
			if !ob.Pass {
				t.Errorf("reducer FAIL [%s] %s: %s", epRes.EpisodeID, name, ob.Reason)
			}
		}
	}
	if t.Failed() {
		t.Logf("reducer: obligation_pass %d/%d, neg-invariant_pass %d/%d",
			res.TotalPass, res.TotalObligation,
			res.TotalNegativeInvariantPass, res.TotalNegativeInvariantTotal)
	}
}

// TestFloorZero: the nomem floor must pass ZERO positive obligations.
// Negative invariants may still pass (amnesia trivially avoids leakage).
func TestFloorZero(t *testing.T) {
	eps := loadEpisodes(t)
	res, err := runner.Run(context.Background(), nomem.New(), eps)
	if err != nil {
		t.Fatalf("runner.Run: %v", err)
	}
	if res.TotalPass != 0 {
		for _, epRes := range res.EpisodeResults {
			for name, ob := range epRes.Obligations {
				if ob.Pass {
					t.Errorf("nomem should not pass any positive obligation, but passed %s in episode %s: %s",
						name, epRes.EpisodeID, ob.Reason)
				}
			}
		}
	}
	// Negative invariants: nomem should pass them all (nothing stored → nothing leaks).
	if res.TotalNegativeInvariantPass != res.TotalNegativeInvariantTotal && res.TotalNegativeInvariantTotal > 0 {
		t.Errorf("nomem neg-invariant_pass = %d/%d — expected all to pass (amnesia should trivially satisfy negative invariants)",
			res.TotalNegativeInvariantPass, res.TotalNegativeInvariantTotal)
	}
}

// TestEpisodeSuiteExercisesEveryObligation: every AOEP-v0 obligation must be exercised
// (scored, not skipped) at least once across all episodes under the reducer.
func TestEpisodeSuiteExercisesEveryObligation(t *testing.T) {
	allObligations := []string{
		validator.ObligNoUnauthorisedWrites,
		validator.ObligPermissionEpochCurrent,
		validator.ObligNoStaleActionExecuted,
		validator.ObligNoScopeLeakage,
		validator.ObligNoDeletedContentVisible,
		validator.ObligDeletionLedgerSubsetMatch,
		validator.ObligNoUntrustedInstrPromoted,
		validator.ObligRollbackLedgerSubsetMatch,
		validator.ObligNoExternalActionWoApproval,
		validator.ObligConflictSurfaced,
	}

	eps := loadEpisodes(t)
	res, err := runner.Run(context.Background(), reducer.New(), eps)
	if err != nil {
		t.Fatalf("runner.Run: %v", err)
	}

	exercised := make(map[string]bool)
	for _, epRes := range res.EpisodeResults {
		for name := range epRes.Obligations {
			exercised[name] = true
		}
	}

	for _, name := range allObligations {
		if !exercised[name] {
			t.Errorf("obligation %q is never exercised across the episode suite — add an episode that triggers it", name)
		}
	}
}

// TestConfirmBlockIntersection: at least one event in the suite must have
// requires_confirmation=true AND should_block=true (the unfailable constraint test).
func TestConfirmBlockIntersection(t *testing.T) {
	eps := loadEpisodes(t)
	for _, ep := range eps {
		for _, ev := range ep.Events {
			if ev.Retention.RequiresConfirmation &&
				ev.OutcomeExpected != nil && ev.OutcomeExpected.ShouldBlock {
				return // found at least one
			}
		}
	}
	t.Error("no event in the suite has requires_confirmation=true AND should_block=true — ep10 may be missing or malformed")
}

// TestReadmeClaimsPaperFacts guards against README drift from the verified paper facts
// (arXiv:2606.30306). It asserts the README contains the required correction markers and
// does not contain the retracted false claims from earlier rounds.
func TestReadmeClaimsPaperFacts(t *testing.T) {
	_, thisFile, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("runtime.Caller failed")
	}
	readmePath := filepath.Join(filepath.Dir(thisFile), "..", "..", "README.md")
	data, err := os.ReadFile(readmePath)
	if err != nil {
		t.Fatalf("read README.md: %v", err)
	}
	content := string(data)

	required := []string{
		"seven systems",
		"frozen Qwen2.5-7B reader",
		"not directly comparable",
		"Recordari is not in the paper",
	}
	for _, s := range required {
		if !strings.Contains(content, s) {
			t.Errorf("README.md missing required text: %q", s)
		}
	}

	retracted := []string{
		"no such reference implementation",
		"single headline score",
		"two systems (Recordari + Mem0)",
	}
	for _, s := range retracted {
		if strings.Contains(content, s) {
			t.Errorf("README.md contains retracted false claim: %q", s)
		}
	}
}

// TestProbeNeutrality: no probe's TargetResource or ID should match any obligation name.
// Probes must be blind to the invariant under test.
func TestProbeNeutrality(t *testing.T) {
	obligationNames := map[string]bool{
		validator.ObligNoUnauthorisedWrites:       true,
		validator.ObligPermissionEpochCurrent:     true,
		validator.ObligNoStaleActionExecuted:      true,
		validator.ObligNoScopeLeakage:             true,
		validator.ObligNoDeletedContentVisible:    true,
		validator.ObligDeletionLedgerSubsetMatch:  true,
		validator.ObligNoUntrustedInstrPromoted:   true,
		validator.ObligRollbackLedgerSubsetMatch:  true,
		validator.ObligNoExternalActionWoApproval: true,
		validator.ObligConflictSurfaced:           true,
	}

	// Also flag "probe" query kinds that expose obligation names — the query itself
	// is part of the schema (e.g. ProbeKind == "deletion_ledger") and is expected;
	// only the TargetResource and probe ID must not leak invariant names.
	except := map[schema.ProbeKind]bool{
		schema.ProbeDeletionLedger:  true,
		schema.ProbeRollbackLedger:  true,
		schema.ProbePermissionEpoch: true,
		schema.ProbeConflicts:       true,
		schema.ProbeTrustTier:       true,
	}

	eps := loadEpisodes(t)
	for _, ep := range eps {
		for _, probe := range ep.Probes {
			if except[probe.Query] {
				continue
			}
			if obligationNames[probe.TargetResource] {
				t.Errorf("episode %s probe %s: TargetResource %q matches an obligation name — probes must be neutral",
					ep.ID, probe.ID, probe.TargetResource)
			}
			if obligationNames[probe.ID] {
				t.Errorf("episode %s probe %s: probe ID matches an obligation name — probes must be neutral",
					ep.ID, probe.ID)
			}
		}
	}
}
