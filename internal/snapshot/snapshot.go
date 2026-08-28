// Package snapshot reconstructs the expected AOEP state from probe responses.
package snapshot

import (
	"encoding/json"
	"strconv"
	"strings"

	"aoep-recordari/internal/schema"
)

// extractIDsFromText parses a Recordari lean-list text response and adds any
// node IDs it finds to dest.
//
// Recordari returns audit results as a JSON array of strings, each with format:
//
//	"[node-id] label — excerpt (domain, node_kind)"
//
// It also handles plain-text line-per-line format as a fallback.
func extractIDsFromText(text string, dest map[string]bool) {
	text = strings.TrimSpace(text)

	// Try JSON array first (the most common format from Recordari).
	if strings.HasPrefix(text, "[") {
		var items []string
		if err := json.Unmarshal([]byte(text), &items); err == nil {
			for _, item := range items {
				extractIDFromLine(item, dest)
			}
			return
		}
	}

	// Fallback: plain text, one entry per line.
	for _, line := range strings.Split(text, "\n") {
		extractIDFromLine(line, dest)
	}
}

// extractIDFromLine extracts the node ID from a single lean-list line.
// Format: "[node-id] ..."
func extractIDFromLine(line string, dest map[string]bool) {
	line = strings.TrimSpace(line)
	if !strings.HasPrefix(line, "[") {
		return
	}
	end := strings.Index(line, "]")
	if end <= 1 {
		return
	}
	id := line[1:end]
	if id != "" {
		dest[id] = true
	}
}

// collectNodeIDs pulls node IDs out of a probe response value that may be
// either {"nodes":[{"id":...}]} JSON or a {"text":"..."} lean-list wrapper.
func collectNodeIDs(v any, dest map[string]bool) {
	m, ok := v.(map[string]any)
	if !ok {
		return
	}
	if nodes, ok := m["nodes"].([]any); ok {
		for _, n := range nodes {
			if node, ok := n.(map[string]any); ok {
				if id, _ := node["id"].(string); id != "" {
					dest[id] = true
				}
			}
		}
	}
	if text, ok := m["text"].(string); ok {
		extractIDsFromText(text, dest)
	}
}

// Snapshot is the reconstructed system state after an episode run.
// All fields are derived from probe responses; the harness never trusts system self-reports.
type Snapshot struct {
	// Responses maps probe ID → raw probe response from the system under test.
	Responses map[string]*schema.ProbeResponse

	// ResourceVisible maps resource name → whether it appeared in any probe response.
	ResourceVisible map[string]bool

	// ProbeErrored maps probe ID → whether the probe returned a transport/tool error
	// (or produced no response at all). An errored probe carries no evidence about the
	// system's governance behaviour: the validator must not read a nil/absent value from
	// an errored probe as a PASS.
	ProbeErrored map[string]bool

	// DeletionLedger is the set of resource IDs the SUT reports as deleted.
	DeletionLedger map[string]bool

	// RollbackLedger is the set of resource IDs the SUT reports as rolled back / restored.
	RollbackLedger map[string]bool

	// TrustTiers maps resource name → the trust tier the SUT reported for that resource.
	TrustTiers map[string]string

	// PendingConflicts is the set of resource IDs/labels the SUT reported as in conflict.
	PendingConflicts map[string]bool

	// ReportedEpochs maps resource name → the permission epoch the SUT reported for it.
	ReportedEpochs map[string]int
}

// Reconstruct builds a Snapshot from a probe/response set.
func Reconstruct(probes []schema.Probe, responses []*schema.ProbeResponse) *Snapshot {
	s := &Snapshot{
		Responses:        make(map[string]*schema.ProbeResponse),
		ResourceVisible:  make(map[string]bool),
		ProbeErrored:     make(map[string]bool),
		DeletionLedger:   make(map[string]bool),
		RollbackLedger:   make(map[string]bool),
		TrustTiers:       make(map[string]string),
		PendingConflicts: make(map[string]bool),
		ReportedEpochs:   make(map[string]int),
	}

	for _, r := range responses {
		if r != nil {
			s.Responses[r.ProbeID] = r
		}
	}

	for _, probe := range probes {
		resp := s.Responses[probe.ID]
		// A missing response or a transport/tool error is recorded as errored so the
		// validator can distinguish "system refused / absent" from "we never observed it".
		if resp == nil || resp.Error != "" {
			s.ProbeErrored[probe.ID] = true
			continue
		}
		if resp.Value == nil {
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
			// audit(mode=archived) may return {nodes:[{id,...}]} JSON or a lean-list text wrapper.
			collectNodeIDs(resp.Value, s.DeletionLedger)

		case schema.ProbeRollbackLedger:
			// Recordari has no dedicated rollback ledger; the probe returns audit(mode=stale)
			// nodes (same {nodes:[...]} / text shape as the deletion ledger). Parsing it this
			// way means a restored resource that is genuinely absent from the audit surface
			// yields an honest "not found" FAIL rather than a parser-shape mismatch.
			collectNodeIDs(resp.Value, s.RollbackLedger)

		case schema.ProbeTrustTier:
			if m, ok := resp.Value.(map[string]any); ok {
				if score, ok := m["trust_score"].(string); ok && score != "" {
					s.TrustTiers[probe.TargetResource] = score
				}
			}

		case schema.ProbeConflicts:
			// Collect all node IDs/labels reported as pending conflicts.
			collectNodeIDs(resp.Value, s.PendingConflicts)

		case schema.ProbePermissionEpoch:
			// The reducer returns {"epoch": N} (float64 from JSON decode).
			// Recordari may return a tags string like "epoch:3".
			if m, ok := resp.Value.(map[string]any); ok {
				if ep, ok := m["epoch"].(float64); ok {
					s.ReportedEpochs[probe.TargetResource] = int(ep)
				} else if epStr, ok := m["epoch"].(string); ok {
					if n, err := strconv.Atoi(epStr); err == nil {
						s.ReportedEpochs[probe.TargetResource] = n
					}
				} else if tags, ok := m["tags"].(string); ok {
					s.ReportedEpochs[probe.TargetResource] = parseEpochFromTags(tags)
				}
			}
		}
	}

	return s
}

// parseEpochFromTags extracts an epoch integer from a tags string.
// Handles both comma-separated (reducer: "scope:X,epoch:N") and
// space-separated (Recordari: "aoep actor:X epoch:N scope:Y") formats.
func parseEpochFromTags(tags string) int {
	for _, part := range strings.FieldsFunc(tags, func(r rune) bool {
		return r == ',' || r == ' '
	}) {
		if after, ok := strings.CutPrefix(part, "epoch:"); ok {
			n, err := strconv.Atoi(after)
			if err == nil {
				return n
			}
		}
	}
	return 0
}
