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
	client *Client
	idMap  map[string]string // AOEP label/idempotency_key → Recordari node ID
}

// New creates a Recordari adapter using the given MCP URL and API key.
func New(mcpURL, apiKey string) *Adapter {
	return &Adapter{
		client: NewClient(mcpURL, apiKey),
		idMap:  make(map[string]string),
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

// ResetEpisode clears the internal ID map so each episode starts with a clean slate.
func (a *Adapter) ResetEpisode() {
	a.idMap = make(map[string]string)
}

// Setup verifies connectivity; the benchmark API key already scopes to a clean workspace.
func (a *Adapter) Setup(ctx context.Context) (func(context.Context) error, error) {
	// Verify the endpoint is reachable by listing tools.
	_, err := a.client.call(ctx, "tools/list", nil)
	if err != nil {
		return nil, fmt.Errorf("recordari setup: %w", err)
	}
	teardown := func(ctx context.Context) error {
		return a.purgeAll(ctx)
	}
	return teardown, nil
}

// purgeAll removes all benchmark nodes from the dedicated workspace.
// Two-step: archive live nodes first, then purge archived ones.
func (a *Adapter) purgeAll(ctx context.Context) error {
	// Archive live nodes.
	result, err := a.client.CallTool(ctx, "search", map[string]any{
		"query":  "aoep",
		"domain": benchmarkDomain,
		"limit":  100,
	})
	if err == nil {
		if out, err := ParseResult(result); err == nil {
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

	// Purge archived nodes.
	archResult, err := a.client.CallTool(ctx, "audit", map[string]any{
		"mode":   "archived",
		"domain": benchmarkDomain,
		"limit":  100,
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

// register stores all useful AOEP → system ID mappings for this write event.
func (a *Adapter) register(ev schema.Event, sysID string) {
	if sysID == "" {
		return
	}
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
	args := map[string]any{
		"domain":    benchmarkDomain,
		"node_kind": "transient",
		"label":     fmt.Sprintf("quarantined:%s:%s", ev.Actor, ev.Scope),
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
	return sysID, nil
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
		resp.Value = out

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
