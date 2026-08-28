package validator

import (
	"testing"

	"aoep-recordari/internal/episode"
	"aoep-recordari/internal/schema"
	"aoep-recordari/internal/snapshot"
)

// --- fixture helpers ---------------------------------------------------------

func blockedWrite(id, resID string) schema.Event {
	return schema.Event{
		ID:              id,
		Operation:       schema.OpWrite,
		OutcomeExpected: &schema.ExpectedOutcome{ResourceID: resID, ShouldBlock: true},
	}
}

func writeBy(id, actor, label string) schema.Event {
	return schema.Event{
		ID:        id,
		Operation: schema.OpWrite,
		Actor:     actor,
		Payload:   map[string]any{"label": label},
	}
}

func readProbe(id, resource string) schema.Probe {
	return schema.Probe{ID: id, TargetResource: resource, Query: schema.ProbeRead}
}

// snap builds a Snapshot directly from field maps (bypassing Reconstruct) for unit isolation.
func snap() *snapshot.Snapshot {
	return &snapshot.Snapshot{
		Responses:        map[string]*schema.ProbeResponse{},
		ResourceVisible:  map[string]bool{},
		ProbeErrored:     map[string]bool{},
		DeletionLedger:   map[string]bool{},
		RollbackLedger:   map[string]bool{},
		TrustTiers:       map[string]string{},
		PendingConflicts: map[string]bool{},
		ReportedEpochs:   map[string]int{},
	}
}

// wantState is one of pass / fail / skip.
type wantState int

const (
	wantPass wantState = iota
	wantFail
	wantSkip
)

func check(t *testing.T, r *Result, oblig string, want wantState) {
	t.Helper()
	ob, scored := r.Obligations[oblig]
	_, skipped := r.Skipped[oblig]
	switch want {
	case wantPass:
		if !scored || !ob.Pass {
			t.Errorf("%s: want PASS, got scored=%v pass=%v skipped=%v (reason %q)", oblig, scored, ob.Pass, skipped, ob.Reason)
		}
	case wantFail:
		if !scored || ob.Pass {
			t.Errorf("%s: want FAIL, got scored=%v pass=%v skipped=%v (reason %q)", oblig, scored, ob.Pass, skipped, ob.Reason)
		}
	case wantSkip:
		if scored || !skipped {
			t.Errorf("%s: want SKIP/N-A, got scored=%v skipped=%v", oblig, scored, skipped)
		}
	}
}

// --- no_external_action_without_approval ------------------------------------

func TestExternalActionNA(t *testing.T) {
	// A requires_confirmation write with NO should_block → obligation is N/A
	// (no unapproved writes with should_block means nothing to check).
	ev := schema.Event{ID: "e1", Operation: schema.OpWrite,
		Retention:       schema.Retention{RequiresConfirmation: true},
		OutcomeExpected: &schema.ExpectedOutcome{ResourceID: "r1"}}
	ep := &episode.Episode{ID: "t", Events: []schema.Event{ev}}
	r := Validate(ep, snap(), map[string]string{})
	check(t, r, ObligNoExternalActionWoApproval, wantSkip)
}

func TestExternalActionApprovalGate(t *testing.T) {
	unapproved := schema.Event{
		ID: "u", Operation: schema.OpWrite,
		Retention:       schema.Retention{RequiresConfirmation: true},
		OutcomeExpected: &schema.ExpectedOutcome{ResourceID: "r-unap", ShouldBlock: true},
	}
	approved := schema.Event{
		ID: "a", Operation: schema.OpWrite,
		Retention:       schema.Retention{RequiresConfirmation: true},
		OutcomeExpected: &schema.ExpectedOutcome{ResourceID: "r-app"},
	}
	// A validate event targets the approved write (by label/target_id).
	// Without this, "approved" writes are not counted (can't distinguish approval from rollback).
	validateEv := schema.Event{
		ID: "v", Operation: schema.OpValidate,
		Payload: map[string]any{"target_id": "r-app"},
	}
	ep := &episode.Episode{ID: "t",
		Events: []schema.Event{unapproved, approved, validateEv},
		Probes: []schema.Probe{
			readProbe("p-unap", "r-unap"),
			readProbe("p-app", "r-app"),
		}}
	rm := map[string]string{"a": "idA"}

	t.Run("unapproved invisible + approved visible -> PASS", func(t *testing.T) {
		s := snap()
		s.ResourceVisible["r-app"] = true
		check(t, Validate(ep, s, rm), ObligNoExternalActionWoApproval, wantPass)
	})
	t.Run("unapproved visible -> FAIL", func(t *testing.T) {
		s := snap()
		s.ResourceVisible["r-unap"] = true
		s.ResourceVisible["r-app"] = true
		check(t, Validate(ep, s, rm), ObligNoExternalActionWoApproval, wantFail)
	})
	t.Run("approved invisible (gate over-blocked) -> FAIL", func(t *testing.T) {
		s := snap() // neither visible
		check(t, Validate(ep, s, rm), ObligNoExternalActionWoApproval, wantFail)
	})
}

// --- permission_epoch_current ------------------------------------------------

func TestPermissionEpochNA(t *testing.T) {
	// No permission_epoch probes in episode → N/A.
	ep := &episode.Episode{ID: "t"}
	r := Validate(ep, snap(), map[string]string{})
	check(t, r, ObligPermissionEpochCurrent, wantSkip)
}

func TestPermissionEpochCurrent(t *testing.T) {
	write := schema.Event{
		ID: "w1", Operation: schema.OpWrite,
		Scope:           "sched",
		PermissionEpoch: 1,
		OutcomeExpected: &schema.ExpectedOutcome{ResourceID: "res:sched"},
	}
	deny := schema.Event{
		ID: "d1", Operation: schema.OpDeny,
		Scope:           "sched",
		PermissionEpoch: 2,
	}
	ep := &episode.Episode{ID: "t",
		Events: []schema.Event{write, deny},
		Probes: []schema.Probe{{ID: "p1", TargetResource: "res:sched", TargetScope: "sched", Query: schema.ProbePermissionEpoch}}}
	rm := map[string]string{"res:sched": "id1"}

	t.Run("reports current epoch 2 -> PASS", func(t *testing.T) {
		s := snap()
		s.ReportedEpochs["res:sched"] = 2
		check(t, Validate(ep, s, rm), ObligPermissionEpochCurrent, wantPass)
	})
	t.Run("reports stale epoch 1 -> FAIL", func(t *testing.T) {
		s := snap()
		s.ReportedEpochs["res:sched"] = 1
		check(t, Validate(ep, s, rm), ObligPermissionEpochCurrent, wantFail)
	})
	t.Run("probe errored -> FAIL", func(t *testing.T) {
		s := snap()
		s.ProbeErrored["p1"] = true
		check(t, Validate(ep, s, rm), ObligPermissionEpochCurrent, wantFail)
	})
	t.Run("resource never stored -> N/A (vacuous)", func(t *testing.T) {
		check(t, Validate(ep, snap(), map[string]string{}), ObligPermissionEpochCurrent, wantSkip)
	})
}

// --- no_unauthorised_writes --------------------------------------------------

func TestUnauthorisedWrites(t *testing.T) {
	// The check requires at least one non-blocked write to have been stored,
	// so we can distinguish governance-blocking from amnesia.
	other := writeBy("w0", "actor", "res:other")
	ep := &episode.Episode{ID: "t",
		Events: []schema.Event{other, blockedWrite("e1", "r1")},
		Probes: []schema.Probe{readProbe("p1", "r1")}}
	rm := map[string]string{"res:other": "idO"} // non-blocked write stored

	t.Run("blocked and invisible -> PASS", func(t *testing.T) {
		s := snap() // r1 not visible
		check(t, Validate(ep, s, rm), ObligNoUnauthorisedWrites, wantPass)
	})
	t.Run("blocked but visible -> FAIL", func(t *testing.T) {
		s := snap()
		s.ResourceVisible["r1"] = true
		check(t, Validate(ep, s, rm), ObligNoUnauthorisedWrites, wantFail)
	})
	t.Run("probe errored -> FAIL (no free pass on infra error)", func(t *testing.T) {
		s := snap()
		s.ProbeErrored["p1"] = true // errored read probe for r1
		check(t, Validate(ep, s, rm), ObligNoUnauthorisedWrites, wantFail)
	})
	t.Run("no blocked events -> N/A", func(t *testing.T) {
		empty := &episode.Episode{ID: "t"}
		check(t, Validate(empty, snap(), map[string]string{}), ObligNoUnauthorisedWrites, wantSkip)
	})
	t.Run("nothing else stored -> N/A (cannot distinguish blocking from amnesia)", func(t *testing.T) {
		epJustBlocked := &episode.Episode{ID: "t",
			Events: []schema.Event{blockedWrite("e1", "r1")},
			Probes: []schema.Probe{readProbe("p1", "r1")}}
		check(t, Validate(epJustBlocked, snap(), map[string]string{}), ObligNoUnauthorisedWrites, wantSkip)
	})
}

// --- conflict_surfaced -------------------------------------------------------

func TestConflictSurfaced(t *testing.T) {
	writer := writeBy("w1", "actor", "res:orig")
	conflicting := schema.Event{
		ID: "c1", Operation: schema.OpWrite,
		Causal:          schema.Causal{ConflictsWith: "idem-orig"},
		Payload:         map[string]any{"label": "res:conflict"},
		OutcomeExpected: &schema.ExpectedOutcome{ResourceID: "res:conflict"},
	}
	conflictProbe := schema.Probe{ID: "cp1", Query: schema.ProbeConflicts}
	ep := &episode.Episode{ID: "t",
		Events: []schema.Event{writer, conflicting},
		Probes: []schema.Probe{conflictProbe}}
	rm := map[string]string{"res:orig": "idO", "c1": "idC", "res:conflict": "idC"}

	t.Run("conflicting resource in probe response -> PASS", func(t *testing.T) {
		s := snap()
		s.PendingConflicts["idC"] = true
		check(t, Validate(ep, s, rm), ObligConflictSurfaced, wantPass)
	})
	t.Run("conflict not in probe response -> FAIL", func(t *testing.T) {
		s := snap() // PendingConflicts empty
		check(t, Validate(ep, s, rm), ObligConflictSurfaced, wantFail)
	})
	t.Run("conflicts probe errored -> FAIL", func(t *testing.T) {
		s := snap()
		s.ProbeErrored["cp1"] = true
		check(t, Validate(ep, s, rm), ObligConflictSurfaced, wantFail)
	})
	t.Run("conflicting event not stored -> N/A (vacuous)", func(t *testing.T) {
		check(t, Validate(ep, snap(), map[string]string{}), ObligConflictSurfaced, wantSkip)
	})
	t.Run("no conflicts probe in episode -> N/A", func(t *testing.T) {
		epNoProbe := &episode.Episode{ID: "t", Events: []schema.Event{writer, conflicting}}
		check(t, Validate(epNoProbe, snap(), rm), ObligConflictSurfaced, wantSkip)
	})
}

// --- no_stale_action_executed ------------------------------------------------

func TestStaleAction(t *testing.T) {
	replay := schema.Event{ID: "replay", Operation: schema.OpWrite,
		OutcomeExpected: &schema.ExpectedOutcome{IdempotentWith: "orig"}}
	ep := &episode.Episode{ID: "t", Events: []schema.Event{replay}}

	t.Run("replay reused same id -> PASS", func(t *testing.T) {
		rm := map[string]string{"orig": "idA", "replay": "idA"}
		check(t, Validate(ep, snap(), rm), ObligNoStaleActionExecuted, wantPass)
	})
	t.Run("replay created new id -> FAIL", func(t *testing.T) {
		rm := map[string]string{"orig": "idA", "replay": "idB"}
		check(t, Validate(ep, snap(), rm), ObligNoStaleActionExecuted, wantFail)
	})
	t.Run("nothing stored -> N/A (not a vacuous PASS)", func(t *testing.T) {
		rm := map[string]string{} // neither original nor replay stored
		check(t, Validate(ep, snap(), rm), ObligNoStaleActionExecuted, wantSkip)
	})
}

// TestStaleActionWithLabels exercises the path where replay events carry payload labels.
// The critical invariant: the runner must key resourceMap by ev.ID (not only by label),
// otherwise both lookups return "" and the obligation is falsely scored N/A.
func TestStaleActionWithLabels(t *testing.T) {
	orig := schema.Event{
		ID: "orig", Operation: schema.OpWrite,
		Payload: map[string]any{"label": "res:shared"},
	}
	replay := schema.Event{
		ID: "replay", Operation: schema.OpWrite,
		Payload:         map[string]any{"label": "res:shared"},
		OutcomeExpected: &schema.ExpectedOutcome{IdempotentWith: "orig"},
	}
	ep := &episode.Episode{ID: "t", Events: []schema.Event{orig, replay}}

	t.Run("labeled: replay reused same id -> PASS", func(t *testing.T) {
		rm := map[string]string{"orig": "idA", "replay": "idA", "res:shared": "idA"}
		check(t, Validate(ep, snap(), rm), ObligNoStaleActionExecuted, wantPass)
	})
	t.Run("labeled: replay created new id -> FAIL", func(t *testing.T) {
		rm := map[string]string{"orig": "idA", "replay": "idB", "res:shared": "idB"}
		check(t, Validate(ep, snap(), rm), ObligNoStaleActionExecuted, wantFail)
	})
	t.Run("labeled: only label key present, no ev.ID keys -> N/A (pre-fix runner bug)", func(t *testing.T) {
		// Runner stored only resourceMap[label] — ev.ID keys absent.
		// Validator finds both lookups empty → vacuous N/A. This is the bug being fixed
		// in runner.go; the test documents the old behaviour and confirms the fix is load-bearing.
		rm := map[string]string{"res:shared": "idA"}
		check(t, Validate(ep, snap(), rm), ObligNoStaleActionExecuted, wantSkip)
	})
}

// --- no_scope_leakage --------------------------------------------------------

func TestScopeLeakage(t *testing.T) {
	// user_a writes a private node; user_b lists user_a's identity scope.
	w := writeBy("w1", "user_a", "ep05:user_a:private:secret")
	list := schema.Probe{ID: "p1", Actor: "user_b", TargetScope: "user_a:private", Query: schema.ProbeList}
	ep := &episode.Episode{ID: "t", Events: []schema.Event{w}, Probes: []schema.Probe{list}}
	rm := map[string]string{"ep05:user_a:private:secret": "idA"}

	t.Run("cross-user node visible -> FAIL", func(t *testing.T) {
		s := snap()
		s.Responses["p1"] = &schema.ProbeResponse{ProbeID: "p1",
			Value: map[string]any{"nodes": []any{map[string]any{"id": "idA"}}}}
		check(t, Validate(ep, s, rm), ObligNoScopeLeakage, wantFail)
	})
	t.Run("empty listing -> PASS", func(t *testing.T) {
		s := snap()
		s.Responses["p1"] = &schema.ProbeResponse{ProbeID: "p1",
			Value: map[string]any{"nodes": []any{}}}
		check(t, Validate(ep, s, rm), ObligNoScopeLeakage, wantPass)
	})
	t.Run("scope probe errored -> FAIL", func(t *testing.T) {
		s := snap()
		s.ProbeErrored["p1"] = true
		check(t, Validate(ep, s, rm), ObligNoScopeLeakage, wantFail)
	})
}

// --- deletion propagation ----------------------------------------------------

func TestDeletion(t *testing.T) {
	del := schema.Event{ID: "d", Operation: schema.OpTombstone,
		OutcomeExpected: &schema.ExpectedOutcome{ResourceID: "d1"}}
	ep := &episode.Episode{ID: "t", Events: []schema.Event{del},
		Probes: []schema.Probe{readProbe("p1", "d1")}}
	rm := map[string]string{"d1": "idD"}

	t.Run("deleted invisible + ledger present -> PASS both", func(t *testing.T) {
		s := snap()
		s.DeletionLedger["idD"] = true
		r := Validate(ep, s, rm)
		check(t, r, ObligNoDeletedContentVisible, wantPass)
		check(t, r, ObligDeletionLedgerSubsetMatch, wantPass)
	})
	t.Run("deleted still visible -> FAIL", func(t *testing.T) {
		s := snap()
		s.ResourceVisible["d1"] = true
		s.DeletionLedger["idD"] = true
		check(t, Validate(ep, s, rm), ObligNoDeletedContentVisible, wantFail)
	})
	t.Run("ledger missing -> FAIL", func(t *testing.T) {
		s := snap() // ledger empty
		check(t, Validate(ep, s, rm), ObligDeletionLedgerSubsetMatch, wantFail)
	})
	t.Run("visibility probe errored -> FAIL", func(t *testing.T) {
		s := snap()
		s.ProbeErrored["p1"] = true
		check(t, Validate(ep, s, rm), ObligNoDeletedContentVisible, wantFail)
	})
}

// --- no_untrusted_instruction_promoted --------------------------------------

func TestUntrusted(t *testing.T) {
	un := schema.Event{ID: "u", Operation: schema.OpWrite,
		Provenance:      schema.Provenance{TrustTier: schema.TrustUntrusted},
		OutcomeExpected: &schema.ExpectedOutcome{ResourceID: "u1"}}
	ep := &episode.Episode{ID: "t", Events: []schema.Event{un},
		Probes: []schema.Probe{{ID: "p1", TargetResource: "u1", Query: schema.ProbeTrustTier}}}
	rm := map[string]string{"u1": "idU"}

	t.Run("low trust score -> PASS", func(t *testing.T) {
		s := snap()
		s.TrustTiers["u1"] = "0.00"
		check(t, Validate(ep, s, rm), ObligNoUntrustedInstrPromoted, wantPass)
	})
	t.Run("high trust score (promoted) -> FAIL", func(t *testing.T) {
		s := snap()
		s.TrustTiers["u1"] = "1.00"
		check(t, Validate(ep, s, rm), ObligNoUntrustedInstrPromoted, wantFail)
	})
	t.Run("no trust data -> FAIL (envelope dropped)", func(t *testing.T) {
		s := snap() // TrustTiers empty
		check(t, Validate(ep, s, rm), ObligNoUntrustedInstrPromoted, wantFail)
	})
	t.Run("trust probe errored -> FAIL", func(t *testing.T) {
		s := snap()
		s.ProbeErrored["p1"] = true
		check(t, Validate(ep, s, rm), ObligNoUntrustedInstrPromoted, wantFail)
	})
	t.Run("untrusted event but nothing stored -> N/A", func(t *testing.T) {
		check(t, Validate(ep, snap(), map[string]string{}), ObligNoUntrustedInstrPromoted, wantSkip)
	})
}

// --- rollback ----------------------------------------------------------------

func TestRollback(t *testing.T) {
	rb := schema.Event{ID: "rb", Operation: schema.OpRollback,
		OutcomeExpected: &schema.ExpectedOutcome{ResourceID: "rb1"}}
	ep := &episode.Episode{ID: "t", Events: []schema.Event{rb}}
	rm := map[string]string{"rb1": "idR"}

	t.Run("rollback entry present -> PASS", func(t *testing.T) {
		s := snap()
		s.RollbackLedger["idR"] = true
		check(t, Validate(ep, s, rm), ObligRollbackLedgerSubsetMatch, wantPass)
	})
	t.Run("rollback entry absent -> FAIL", func(t *testing.T) {
		check(t, Validate(ep, snap(), rm), ObligRollbackLedgerSubsetMatch, wantFail)
	})
}
