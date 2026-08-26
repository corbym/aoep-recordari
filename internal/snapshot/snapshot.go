// Package snapshot reconstructs the expected AOEP state from probe responses.
package snapshot

import (
	"aoep-recordari/internal/schema"
)

// Snapshot is the reconstructed system state after an episode run.
// All fields are derived from probe responses; the harness never trusts system self-reports.
type Snapshot struct {
	// Responses maps probe ID → raw probe response from the system under test.
	Responses map[string]*schema.ProbeResponse

	// ResourceVisible maps resource name → whether it appeared in any probe response.
	ResourceVisible map[string]bool

	// DeletionLedger is the set of resource IDs the SUT reports as deleted.
	DeletionLedger map[string]bool

	// RollbackLedger is the set of resource IDs the SUT reports as rolled back.
	RollbackLedger map[string]bool

	// PermissionEpochs maps resource name → the epoch string the SUT reported.
	PermissionEpochs map[string]string

	// TrustTiers maps resource name → the trust tier the SUT reported for that resource.
	TrustTiers map[string]string

	// PendingConflicts is the raw conflict list the SUT reported.
	PendingConflicts []any
}

// Reconstruct builds a Snapshot from a probe/response set and the episode's resource map.
// resourceMap maps AOEP resource names → system-assigned IDs (from event delivery).
func Reconstruct(probes []schema.Probe, responses []*schema.ProbeResponse) *Snapshot {
	s := &Snapshot{
		Responses:        make(map[string]*schema.ProbeResponse),
		ResourceVisible:  make(map[string]bool),
		DeletionLedger:   make(map[string]bool),
		RollbackLedger:   make(map[string]bool),
		PermissionEpochs: make(map[string]string),
		TrustTiers:       make(map[string]string),
	}

	for _, r := range responses {
		if r != nil {
			s.Responses[r.ProbeID] = r
		}
	}

	for _, probe := range probes {
		resp := s.Responses[probe.ID]
		if resp == nil || resp.Value == nil {
			continue
		}

		switch probe.Query {
		case schema.ProbeRead:
			// A non-nil response means the resource is visible to this actor.
			s.ResourceVisible[probe.TargetResource] = true

		case schema.ProbeList:
			if m, ok := resp.Value.(map[string]any); ok {
				if nodes, ok := m["nodes"].([]any); ok {
					s.ResourceVisible[probe.TargetResource] = len(nodes) > 0
				}
			}

		case schema.ProbeDeletionLedger:
			// audit(mode=archived) returns {nodes:[{id,label,...},...]} for archived (tombstoned) memories.
			if m, ok := resp.Value.(map[string]any); ok {
				if nodes, ok := m["nodes"].([]any); ok {
					for _, n := range nodes {
						if node, ok := n.(map[string]any); ok {
							if id, _ := node["id"].(string); id != "" {
								s.DeletionLedger[id] = true
							}
						}
					}
				}
				// Also handle a flat list of ids or string entries.
				if entries, ok := m["entries"].([]any); ok {
					for _, e := range entries {
						if entry, ok := e.(map[string]any); ok {
							if id, _ := entry["id"].(string); id != "" {
								s.DeletionLedger[id] = true
							}
						}
					}
				}
			}

		case schema.ProbeRollbackLedger:
			if m, ok := resp.Value.(map[string]any); ok {
				if entries, ok := m["entries"].([]any); ok {
					for _, e := range entries {
						if entry, ok := e.(map[string]any); ok {
							if op, _ := entry["operation"].(string); op == "restore" {
								if id, _ := entry["node_id"].(string); id != "" {
									s.RollbackLedger[id] = true
								}
							}
						}
					}
				}
			}

		case schema.ProbePermissionEpoch:
			if m, ok := resp.Value.(map[string]any); ok {
				if tags, _ := m["tags"].(string); tags != "" {
					s.PermissionEpochs[probe.TargetResource] = tags
				}
			}

		case schema.ProbeTrustTier:
			if m, ok := resp.Value.(map[string]any); ok {
				if node, ok := m["node"].(map[string]any); ok {
					if tags, _ := node["tags"].(string); tags != "" {
						s.TrustTiers[probe.TargetResource] = tags
					}
				}
			}

		case schema.ProbeConflicts:
			if m, ok := resp.Value.(map[string]any); ok {
				if conflicts, ok := m["conflicts"].([]any); ok {
					s.PendingConflicts = conflicts
				}
			}
		}
	}

	return s
}
