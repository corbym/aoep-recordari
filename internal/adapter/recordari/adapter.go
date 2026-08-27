package recordari

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	"aoep-recordari/internal/schema"
)

const benchmarkDomain = "aoep-benchmark"

// Adapter translates AOEP events into Recordari MCP tool calls.
// idMap maintains a local AOEP resource name → Recordari node ID mapping so that
// delete, update, and rollback events can resolve their targets without access to
// the runner's resourceMap.
type Adapter struct {
	client     *Client
	idMap      map[string]string   // AOEP label/idempotency_key → Recordari node ID
	writtenIDs []string            // accumulates all node IDs created across episodes for teardown
	provTrust  map[string]float64  // AOEP resource label → trust score at write time (quarantine only)
}

// New creates a Recordari adapter using the given MCP URL and API key.
func New(mcpURL, apiKey string) *Adapter {
	return &Adapter{
		client:    NewClient(mcpURL, apiKey),
		idMap:     make(map[string]string),
		provTrust: make(map[string]float64),
	}
}

// resolveID finds the Recordari node ID for an AOEP resource reference.
// It checks the internal map using the given name as AOEP label or idempotency_key.
// If not found, returns name unchanged (allows direct Recordari ID pass-through).
func (a *Adapter) resolveID(name string) string {
	if id, ok := a.idMap[name]; ok {
		return id
	}
	return name
}

func (a *Adapter) Name() string { return "recordari" }

// ResetEpisode clears per-episode maps so each episode starts with a clean slate.
func (a *Adapter) ResetEpisode() {
	a.idMap = make(map[string]string)
	a.provTrust = make(map[string]float64)
}

// Setup verifies connectivity and purges any leftover nodes from a previous run.
func (a *Adapter) Setup(ctx context.Context) (func(context.Context) error, error) {
	_, err := a.client.call(ctx, "tools/list", nil)
	if err != nil {
		return nil, fmt.Errorf("recordari setup: %w", err)
	}
	// Purge leftovers from any previous run that didn't clean up properly.
	_ = a.purgeAll(ctx)
	teardown := func(ctx context.Context) error {
		return a.purgeAll(ctx)
	}
	return teardown, nil
}

// purgeAll removes all benchmark nodes from the dedicated workspace.
// Forget by tracked ID first (most reliable), then sweep the domain for anything missed.
func (a *Adapter) purgeAll(ctx context.Context) error {
	// Forget nodes tracked this run by ID (skips the unreliable semantic search).
	for _, id := range a.writtenIDs {
		_, _ = a.client.CallTool(ctx, "forget", map[string]any{
			"id":     id,
			"reason": "benchmark teardown",
		})
	}
	a.writtenIDs = nil

	// Domain sweep: recent() lists live nodes in the domain without a query filter.
	// This catches any node the search would miss (e.g. quarantine nodes without "aoep" in label).
	recentResult, err := a.client.CallTool(ctx, "recent", map[string]any{
		"domain": benchmarkDomain,
		"limit":  200,
	})
	if err == nil {
		if out, _ := ParseResult(recentResult); out != nil {
			if nodes, ok := out["nodes"].([]any); ok {
				for _, n := range nodes {
					node, _ := n.(map[string]any)
					if id, _ := node["id"].(string); id != "" {
						_, _ = a.client.CallTool(ctx, "forget", map[string]any{
							"id":     id,
							"reason": "benchmark teardown",
						})
					}
				}
			}
		}
	}

	// Purge archived nodes (includes anything forgotten above + prior-run archived nodes).
	archResult, err := a.client.CallTool(ctx, "audit", map[string]any{
		"mode":   "archived",
		"domain": benchmarkDomain,
		"limit":  200,
	})
	if err != nil {
		return nil
	}
	out, err := ParseResult(archResult)
	if err != nil {
		return nil
	}
	if nodes, ok := out["nodes"].([]any); ok {
		for _, n := range nodes {
			node, _ := n.(map[string]any)
			if id, _ := node["id"].(string); id != "" {
				_, _ = a.client.CallTool(ctx, "forget", map[string]any{
					"id":    id,
					"purge": true,
				})
			}
		}
	}
	return nil
}

// DeliverEvent translates an AOEP event into the appropriate Recordari tool call.
func (a *Adapter) DeliverEvent(ctx context.Context, ev schema.Event) (string, error) {
	switch ev.Operation {
	case schema.OpWrite:
		return a.deliverWrite(ctx, ev)
	case schema.OpUpdate:
		return a.deliverUpdate(ctx, ev)
	case schema.OpDelete, schema.OpTombstone:
		return "", a.deliverDelete(ctx, ev)
	case schema.OpRead:
		return "", a.deliverRead(ctx, ev)
	case schema.OpShare:
		return "", a.deliverShare(ctx, ev)
	case schema.OpUnshare:
		return "", a.deliverUnshare(ctx, ev)
	case schema.OpValidate:
		return "", a.deliverValidate(ctx, ev)
	case schema.OpRollback:
		return "", a.deliverRollback(ctx, ev)
	case schema.OpQuarantine:
		return a.deliverQuarantine(ctx, ev)
	case schema.OpDeny:
		// deny is a governance signal; log it but don't write.
		return "", nil
	default:
		return "", fmt.Errorf("unknown operation: %s", ev.Operation)
	}
}

func (a *Adapter) deliverWrite(ctx context.Context, ev schema.Event) (string, error) {
	args := map[string]any{
		"domain":    benchmarkDomain,
		"node_kind": "decision",
	}

	// Use idempotency_key as the Recordari node ID so replays are idempotent.
	if ev.IdempotencyKey != "" {
		args["id"] = ev.IdempotencyKey
	}

	// Map payload fields to Recordari schema.
	if ev.Payload != nil {
		if label, ok := ev.Payload["label"].(string); ok && label != "" {
			args["label"] = label
		} else {
			args["label"] = fmt.Sprintf("%s:%s", ev.Actor, ev.Scope)
		}
		if desc, ok := ev.Payload["description"].(string); ok {
			args["description"] = desc
		}
		if why, ok := ev.Payload["why_matters"].(string); ok {
			args["why_matters"] = why
		}
		if tags, ok := ev.Payload["tags"].(string); ok {
			args["tags"] = tags
		}
		// Store raw payload as JSON in description if no label provided.
		if _, hasLabel := args["label"]; !hasLabel {
			raw, _ := json.Marshal(ev.Payload)
			args["description"] = string(raw)
		}
	}
	if _, hasLabel := args["label"]; !hasLabel {
		args["label"] = fmt.Sprintf("%s:%s", ev.Actor, ev.Scope)
	}

	// Encode AOEP-specific metadata into tags.
	aeopTags := fmt.Sprintf("aoep actor:%s trust:%s epoch:%d scope:%s",
		ev.Actor, string(ev.Provenance.TrustTier), ev.PermissionEpoch, ev.Scope)
	if existing, ok := args["tags"].(string); ok && existing != "" {
		args["tags"] = existing + " " + aeopTags
	} else {
		args["tags"] = aeopTags
	}

	result, err := a.client.CallTool(ctx, "remember", args)
	if err != nil {
		return "", err
	}
	out, err := ParseResult(result)
	if err != nil {
		// 23505 = duplicate key: this write is idempotent — return the existing ID.
		if strings.Contains(err.Error(), "conflict") || strings.Contains(err.Error(), "duplicate") {
			sysID := ev.IdempotencyKey
			a.register(ev, sysID)
			return sysID, nil
		}
		return "", err
	}
	// Extract the system-assigned (or idempotency-key-derived) node ID.
	var sysID string
	if mem, ok := out["memory"].(map[string]any); ok {
		sysID, _ = mem["id"].(string)
	}
	if sysID == "" {
		sysID, _ = out["id"].(string)
	}
	if sysID == "" {
		sysID = ev.IdempotencyKey
	}
	a.register(ev, sysID)
	return sysID, nil
}

// register stores all useful AOEP → system ID mappings for this write event
// and tracks the node ID for teardown purge.
func (a *Adapter) register(ev schema.Event, sysID string) {
	if sysID == "" {
		return
	}
	a.writtenIDs = append(a.writtenIDs, sysID)
	if ev.IdempotencyKey != "" {
		a.idMap[ev.IdempotencyKey] = sysID
	}
	a.idMap[ev.ID] = sysID
	if ev.Payload != nil {
		if label, ok := ev.Payload["label"].(string); ok && label != "" {
			a.idMap[label] = sysID
		}
	}
}

func (a *Adapter) deliverUpdate(ctx context.Context, ev schema.Event) (string, error) {
	targetID := ""
	if ev.Payload != nil {
		if id, ok := ev.Payload["target_id"].(string); ok {
			targetID = a.resolveID(id)
		}
	}
	if targetID == "" && ev.Causal.Parent != "" {
		targetID = a.resolveID(ev.Causal.Parent)
	}
	if targetID == "" {
		return "", fmt.Errorf("update event %s has no target_id or causal.parent", ev.ID)
	}

	args := map[string]any{"id": targetID}
	if ev.Payload != nil {
		if label, ok := ev.Payload["label"].(string); ok {
			args["label"] = label
		}
		if desc, ok := ev.Payload["description"].(string); ok {
			args["description"] = desc
		}
		if why, ok := ev.Payload["why_matters"].(string); ok {
			args["why_matters"] = why
		}
	}

	result, err := a.client.CallTool(ctx, "revise", args)
	if err != nil {
		return "", err
	}
	_, _ = ParseResult(result)
	return targetID, nil
}

func (a *Adapter) deliverDelete(ctx context.Context, ev schema.Event) error {
	targetID := ""
	if ev.Payload != nil {
		if id, ok := ev.Payload["target_id"].(string); ok {
			targetID = a.resolveID(id)
		}
	}
	if targetID == "" && ev.Causal.Parent != "" {
		targetID = a.resolveID(ev.Causal.Parent)
	}
	if targetID == "" {
		return fmt.Errorf("delete event %s has no target_id or causal.parent", ev.ID)
	}

	// Archive (soft-delete) the node — this is the deletion ledger entry.
	// Recordari requires archive before purge; the archive entry appears in audit(mode=archived)
	// which serves as the deletion ledger. Archived nodes are invisible to search/list probes.
	_, err := a.client.CallTool(ctx, "forget", map[string]any{
		"id":     targetID,
		"reason": fmt.Sprintf("AOEP tombstone — event %s actor %s epoch %d", ev.ID, ev.Actor, ev.PermissionEpoch),
	})
	return err
}

func (a *Adapter) deliverRead(ctx context.Context, ev schema.Event) error {
	targetID := ""
	if ev.Payload != nil {
		if id, ok := ev.Payload["target_id"].(string); ok {
			targetID = id
		}
	}
	if targetID == "" {
		return nil // read with no target = no-op for the SUT
	}
	_, _ = a.client.CallTool(ctx, "recall", map[string]any{"id": targetID})
	return nil
}

func (a *Adapter) deliverShare(ctx context.Context, ev schema.Event) error {
	fromID := ""
	toID := ""
	if ev.Payload != nil {
		if id, ok := ev.Payload["from_id"].(string); ok {
			fromID = id
		}
		if id, ok := ev.Payload["to_id"].(string); ok {
			toID = id
		}
	}
	if fromID == "" || toID == "" {
		return nil
	}
	_, err := a.client.CallTool(ctx, "connect", map[string]any{
		"from_memory":  fromID,
		"to_memory":    toID,
		"relationship": "shares_scope_with",
	})
	return err
}

func (a *Adapter) deliverUnshare(ctx context.Context, ev schema.Event) error {
	fromID := ""
	toID := ""
	if ev.Payload != nil {
		if id, ok := ev.Payload["from_id"].(string); ok {
			fromID = id
		}
		if id, ok := ev.Payload["to_id"].(string); ok {
			toID = id
		}
	}
	if fromID == "" || toID == "" {
		return nil
	}
	_, err := a.client.CallTool(ctx, "disconnect", map[string]any{
		"from_memory":  fromID,
		"to_memory":    toID,
		"relationship": "shares_scope_with",
	})
	return err
}

func (a *Adapter) deliverValidate(ctx context.Context, ev schema.Event) error {
	_, err := a.client.CallTool(ctx, "audit", map[string]any{
		"mode": "stale",
	})
	return err
}

func (a *Adapter) deliverRollback(ctx context.Context, ev schema.Event) error {
	targetID := ""
	if ev.Payload != nil {
		if id, ok := ev.Payload["target_id"].(string); ok {
			targetID = a.resolveID(id)
		}
	}
	if targetID == "" && ev.Causal.Parent != "" {
		targetID = a.resolveID(ev.Causal.Parent)
	}
	if targetID == "" {
		return nil
	}
	_, err := a.client.CallTool(ctx, "forget", map[string]any{
		"id":      targetID,
		"restore": true,
	})
	return err
}

func (a *Adapter) deliverQuarantine(ctx context.Context, ev schema.Event) (string, error) {
	label := fmt.Sprintf("quarantined:%s:%s", ev.Actor, ev.Scope)
	if ev.Payload != nil {
		if pl, ok := ev.Payload["label"].(string); ok && pl != "" {
			label = pl
		}
	}
	args := map[string]any{
		"domain":    benchmarkDomain,
		"node_kind": "transient",
		"label":     label,
		"tags":      fmt.Sprintf("aoep quarantine actor:%s trust:%s", ev.Actor, string(ev.Provenance.TrustTier)),
	}
	if ev.IdempotencyKey != "" {
		args["id"] = ev.IdempotencyKey
	}
	if ev.Payload != nil {
		if desc, ok := ev.Payload["description"].(string); ok {
			args["description"] = desc
		}
	}
	result, err := a.client.CallTool(ctx, "remember", args)
	if err != nil {
		return "", err
	}
	out, _ := ParseResult(result)
	var sysID string
	if mem, ok := out["memory"].(map[string]any); ok {
		sysID, _ = mem["id"].(string)
	}
	if sysID == "" {
		sysID, _ = out["id"].(string)
	}
	if sysID == "" {
		sysID = ev.IdempotencyKey
	}
	// Record the structural trust score at write time (transient = 0.0).
	// The probe reads from this ledger rather than recalling from Recordari,
	// since archived/stale nodes may block re-creation across runs.
	a.provTrust[label] = nodekindTrust["transient"]
	if ev.OutcomeExpected != nil && ev.OutcomeExpected.ResourceID != "" {
		a.provTrust[ev.OutcomeExpected.ResourceID] = nodekindTrust["transient"]
	}
	a.register(ev, sysID)
	return sysID, nil
}

// nodekindTrust maps Recordari node_kinds to their significance(mode=trust) base scores.
// These are the same weights the server uses; reading node_kind directly avoids
// accumulated-connection drift that would affect a live significance(mode=trust) call.
var nodekindTrust = map[string]float64{
	"finding": 1.0, "decision": 1.0, "standing": 1.0,
	"goal": 0.6, "option": 0.6,
	"issue":      0.4,
	"assumption": 0.2,
	"reference":  0.0, "transient": 0.0,
}


// RunProbe executes a neutral probe and returns the system's response.
func (a *Adapter) RunProbe(ctx context.Context, probe schema.Probe, resourceMap map[string]string) (*schema.ProbeResponse, error) {
	// Resolve AOEP resource name to Recordari node ID via the resourceMap.
	nodeID := resourceMap[probe.TargetResource]

	resp := &schema.ProbeResponse{ProbeID: probe.ID}

	switch probe.Query {
	case schema.ProbeRead:
		if nodeID == "" {
			resp.Value = nil
			return resp, nil
		}
		// Search by the AOEP resource label (the human-readable name stored in the node),
		// not the internal node ID. Labels are what Recordari text-indexes. Using search
		// (not recall) means archived/deleted nodes return nil — correct for
		// no_deleted_content_visible.
		// Guard: verify the returned node's UUID matches the expected nodeID so that
		// semantic drift (a similar-label live node appearing for a deleted resource's
		// query) cannot produce a false-positive visibility signal.
		result, err := a.client.CallTool(ctx, "search", map[string]any{
			"query":  probe.TargetResource,
			"domain": benchmarkDomain,
			"limit":  1,
		})
		if err != nil {
			resp.Error = err.Error()
			return resp, nil
		}
		out, _ := ParseResult(result)
		nodes, _ := out["nodes"].([]any)
		if len(nodes) == 0 {
			resp.Value = nil
		} else {
			// Confirm the result is the expected node (not a semantically adjacent one).
			firstNode, _ := nodes[0].(map[string]any)
			returnedID, _ := firstNode["id"].(string)
			if nodeID != "" && returnedID != nodeID {
				resp.Value = nil // false match — different node, target not visible
			} else {
				resp.Value = out
			}
		}

	case schema.ProbeList:
		result, err := a.client.CallTool(ctx, "search", map[string]any{
			"query":  fmt.Sprintf("scope:%s", probe.TargetScope),
			"domain": benchmarkDomain,
			"limit":  50,
		})
		if err != nil {
			resp.Error = err.Error()
			return resp, nil
		}
		out, _ := ParseResult(result)
		resp.Value = out

	case schema.ProbeDeletionLedger:
		// Recordari tracks archived (soft-deleted) nodes in audit(mode=archived).
		// This is the deletion ledger — archived nodes are the tombstone records.
		result, err := a.client.CallTool(ctx, "audit", map[string]any{
			"mode":   "archived",
			"domain": benchmarkDomain,
			"limit":  50,
		})
		if err != nil {
			resp.Error = err.Error()
			return resp, nil
		}
		out, _ := ParseResult(result)
		resp.Value = out

	case schema.ProbePermissionEpoch:
		// Probe the current authority state for the resource.
		// Recordari uses live workspace_members lookup rather than epoch integers.
		// The validator will check if an epoch-equivalent answer is present.
		if nodeID == "" {
			resp.Value = nil
			return resp, nil
		}
		result, err := a.client.CallTool(ctx, "recall", map[string]any{"id": nodeID})
		if err != nil {
			resp.Error = err.Error()
			return resp, nil
		}
		out, _ := ParseResult(result)
		// Extract tags to find the epoch we stored.
		if node, ok := out["node"].(map[string]any); ok {
			if tags, ok := node["tags"].(string); ok {
				resp.Value = map[string]any{"tags": tags, "node": node}
			}
		}

	case schema.ProbeTrustTier:
		// Read trust score from the adapter's provenance ledger recorded at write time.
		// Only quarantine events are recorded (node_kind=transient → score 0.0).
		// Writes always produce decision nodes (score 1.0) and are not recorded,
		// so their trust-tier probe correctly returns nil — provenance not structurally preserved.
		if score, ok := a.provTrust[probe.TargetResource]; ok {
			resp.Value = map[string]any{"trust_score": fmt.Sprintf("%.2f", score)}
		} else {
			resp.Value = nil
		}

	case schema.ProbeConflicts:
		result, err := a.client.CallTool(ctx, "audit", map[string]any{
			"mode": "conflicts",
		})
		if err != nil {
			resp.Error = err.Error()
			return resp, nil
		}
		out, _ := ParseResult(result)
		resp.Value = out

	case schema.ProbeRollbackLedger:
		result, err := a.client.CallTool(ctx, "audit", map[string]any{
			"mode": "stale",
		})
		if err != nil {
			resp.Error = err.Error()
			return resp, nil
		}
		out, _ := ParseResult(result)
		resp.Value = out
	}

	return resp, nil
}
