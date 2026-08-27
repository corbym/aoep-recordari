package snapshot

import (
	"testing"

	"aoep-recordari/internal/schema"
)

func TestErroredProbeRecorded(t *testing.T) {
	probes := []schema.Probe{
		{ID: "ok", TargetResource: "r1", Query: schema.ProbeRead},
		{ID: "boom", TargetResource: "r2", Query: schema.ProbeRead},
		{ID: "missing", TargetResource: "r3", Query: schema.ProbeRead},
	}
	responses := []*schema.ProbeResponse{
		{ProbeID: "ok", Value: map[string]any{"node": map[string]any{"id": "x"}}},
		{ProbeID: "boom", Error: "http 500"},
		// "missing" has no response at all
	}
	s := Reconstruct(probes, responses)

	if !s.ResourceVisible["r1"] {
		t.Error("r1 should be visible")
	}
	if !s.ProbeErrored["boom"] {
		t.Error("errored probe not recorded")
	}
	if !s.ProbeErrored["missing"] {
		t.Error("missing-response probe should be recorded as errored")
	}
	if s.ResourceVisible["r2"] {
		t.Error("errored probe must not mark resource visible")
	}
}

func TestRollbackLedgerParsesNodeShape(t *testing.T) {
	// Recordari returns audit(mode=stale) as {nodes:[{id,...}]} — the same shape as the
	// deletion ledger. The rollback parser must read it (not a nonexistent {entries:[...]}).
	probes := []schema.Probe{{ID: "p", TargetResource: "r", Query: schema.ProbeRollbackLedger}}
	responses := []*schema.ProbeResponse{
		{ProbeID: "p", Value: map[string]any{"nodes": []any{map[string]any{"id": "idR"}}}},
	}
	s := Reconstruct(probes, responses)
	if !s.RollbackLedger["idR"] {
		t.Error("rollback ledger did not parse {nodes:[...]} shape")
	}
}

func TestDeletionLedgerParsesTextShape(t *testing.T) {
	probes := []schema.Probe{{ID: "p", TargetResource: "r", Query: schema.ProbeDeletionLedger}}
	responses := []*schema.ProbeResponse{
		{ProbeID: "p", Value: map[string]any{"text": "[node-42] some label — excerpt (dom, decision)"}},
	}
	s := Reconstruct(probes, responses)
	if !s.DeletionLedger["node-42"] {
		t.Error("deletion ledger did not parse lean-list text shape")
	}
}
