// Package reducer is the pure in-process governed reducer (§9.2 upper-bound reference).
// It implements every AOEP-v0 governance rule and must PASS all exercised obligations
// and all negative invariants. It has no network dependency — all state lives in memory.
package reducer

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"sync"

	"aoep-recordari/internal/schema"
)

// resource is the internal state of one stored memory node.
type resource struct {
	id          string
	label       string
	description string
	tags        string
	scope       string
	actor       string
	epoch       int // permission_epoch at write time
	trustTier   string
	pending     bool // held pending confirmation gate
	deleted     bool
	rolledBack  bool
}

// conflictPair records two resources that were written with conflictsWith links.
type conflictPair struct {
	a, b string // resource IDs
}

// Adapter is the governed in-process reducer.
type Adapter struct {
	mu sync.Mutex

	// resources is the live store, keyed by system ID.
	resources map[string]*resource
	// idMap maps idempotency_key → system ID (for dedup).
	idMap map[string]string
	// pendingByTarget maps validate-event target_id → resource ID awaiting confirmation.
	pendingByTarget map[string]string
	// epochByScope tracks the current permission epoch per scope.
	// Incremented by each OpDeny event in that scope.
	epochByScope map[string]int
	// deletionLedger records IDs of deleted resources.
	deletionLedger map[string]bool
	// rollbackLedger records IDs of rolled-back resources.
	rollbackLedger map[string]bool
	// conflicts is the list of known conflict pairs.
	conflicts []conflictPair

	counter int // monotonic ID counter, reset per episode
}

// New returns a ready Adapter.
func New() *Adapter {
	a := &Adapter{}
	a.reset()
	return a
}

func (a *Adapter) Name() string { return "reducer" }

func (a *Adapter) reset() {
	a.resources = make(map[string]*resource)
	a.idMap = make(map[string]string)
	a.pendingByTarget = make(map[string]string)
	a.epochByScope = make(map[string]int)
	a.deletionLedger = make(map[string]bool)
	a.rollbackLedger = make(map[string]bool)
	a.conflicts = nil
	a.counter = 0
}

func (a *Adapter) Setup(_ context.Context) (func(context.Context) error, error) {
	a.mu.Lock()
	a.reset()
	a.mu.Unlock()
	return func(_ context.Context) error { return nil }, nil
}

func (a *Adapter) ResetEpisode() {
	a.mu.Lock()
	a.reset()
	a.mu.Unlock()
}

func (a *Adapter) DeliverEvent(_ context.Context, ev schema.Event) (string, error) {
	a.mu.Lock()
	defer a.mu.Unlock()

	switch ev.Operation {
	case schema.OpWrite, schema.OpUpdate, schema.OpQuarantine:
		return a.deliverWrite(ev)
	case schema.OpDelete, schema.OpTombstone:
		a.deliverDelete(ev)
	case schema.OpRollback:
		a.deliverRollback(ev)
	case schema.OpDeny:
		// The deny event carries the NEW epoch value. Set the scope epoch to its permission_epoch.
		// Any write with permission_epoch < this value is now stale and must be rejected.
		if ev.PermissionEpoch > a.epochByScope[ev.Scope] {
			a.epochByScope[ev.Scope] = ev.PermissionEpoch
		}
	case schema.OpValidate:
		// Release a pending-confirmation write.
		a.deliverValidate(ev)
	}
	return "", nil
}

// deliverWrite stores the resource, enforcing all governance rules.
func (a *Adapter) deliverWrite(ev schema.Event) (string, error) {
	// --- Idempotency dedup ---
	if ev.IdempotencyKey != "" {
		if existing, ok := a.idMap[ev.IdempotencyKey]; ok {
			return existing, nil
		}
	}

	// --- Epoch gate: reject stale writes ---
	currentEpoch := a.epochByScope[ev.Scope]
	if ev.PermissionEpoch < currentEpoch {
		// Stale write — governance blocks it. Return "" to signal not stored.
		return "", nil
	}

	// --- Conflict tracking ---
	label := ev.ID
	if ev.Payload != nil {
		if l, ok := ev.Payload["label"].(string); ok && l != "" {
			label = l
		}
	}

	var conflictWithID string
	if ev.Causal.ConflictsWith != "" {
		// Resolve the conflict-with target to a stored resource ID.
		conflictWithID = a.resolveConflictTarget(ev.Causal.ConflictsWith)
	}

	// --- Assign system ID ---
	a.counter++
	sysID := fmt.Sprintf("red-%d", a.counter)

	// --- Trust tier ---
	tier := "trusted"
	if ev.Provenance.TrustTier == schema.TrustUntrusted {
		tier = "untrusted"
	}

	// --- Tags ---
	tags := fmt.Sprintf("scope:%s,actor:%s,epoch:%d,trust:%s", ev.Scope, ev.Actor, ev.PermissionEpoch, tier)
	if ev.Payload != nil {
		if t, ok := ev.Payload["tags"].(string); ok && t != "" {
			tags += "," + t
		}
	}

	r := &resource{
		id:        sysID,
		label:     label,
		scope:     ev.Scope,
		actor:     ev.Actor,
		epoch:     ev.PermissionEpoch,
		trustTier: tier,
		tags:      tags,
	}
	if ev.Payload != nil {
		if d, ok := ev.Payload["description"].(string); ok {
			r.description = d
		}
	}

	// --- Confirmation gate ---
	// requires_confirmation writes are stored as pending (invisible to reads) but DO get a
	// sysID so that the runner can track them for rollback/deletion/idempotency checks.
	// "pending" controls read-visibility, not storage.
	if ev.Retention.RequiresConfirmation {
		r.pending = true
		// Register as pending by label so OpValidate can find it.
		a.pendingByTarget[label] = sysID
		a.pendingByTarget[ev.ID] = sysID
		a.resources[sysID] = r
		if ev.IdempotencyKey != "" {
			a.idMap[ev.IdempotencyKey] = sysID
		}
		a.idMap[ev.ID] = sysID
		a.idMap[label] = sysID
		// Return sysID so the runner can map it — rollback/deletion/idempotency need it.
		return sysID, nil
	}

	a.resources[sysID] = r
	if ev.IdempotencyKey != "" {
		a.idMap[ev.IdempotencyKey] = sysID
	}
	a.idMap[ev.ID] = sysID
	a.idMap[label] = sysID

	// Register conflict pair after both sides are stored.
	if conflictWithID != "" {
		a.conflicts = append(a.conflicts, conflictPair{a: conflictWithID, b: sysID})
	}

	return sysID, nil
}

// resolveConflictTarget finds the stored system ID for a conflict_with reference.
// The reference may be an event ID or a resource label.
func (a *Adapter) resolveConflictTarget(ref string) string {
	if id, ok := a.idMap[ref]; ok {
		return id
	}
	return ""
}

func (a *Adapter) deliverDelete(ev schema.Event) {
	target := payloadTarget(ev)
	sysID := a.idMap[target]
	if sysID == "" {
		sysID = target
	}
	if r, ok := a.resources[sysID]; ok {
		r.deleted = true
		a.deletionLedger[sysID] = true
	}
}

func (a *Adapter) deliverRollback(ev schema.Event) {
	target := payloadTarget(ev)
	sysID := a.idMap[target]
	if sysID == "" {
		sysID = target
	}
	if r, ok := a.resources[sysID]; ok {
		r.rolledBack = true
		a.rollbackLedger[sysID] = true
	}
}

func (a *Adapter) deliverValidate(ev schema.Event) {
	// target_id in payload names the resource being approved.
	var targetID string
	if ev.Payload != nil {
		if t, ok := ev.Payload["target_id"].(string); ok {
			targetID = t
		}
	}
	if targetID == "" {
		targetID = ev.Causal.Parent
	}
	sysID := a.pendingByTarget[targetID]
	if sysID == "" {
		sysID = a.idMap[targetID]
	}
	if r, ok := a.resources[sysID]; ok && r.pending {
		r.pending = false
	}
}

// RunProbe answers a probe query against the in-process state.
func (a *Adapter) RunProbe(_ context.Context, probe schema.Probe, _ map[string]string) (*schema.ProbeResponse, error) {
	a.mu.Lock()
	defer a.mu.Unlock()

	switch probe.Query {
	case schema.ProbeRead:
		return a.probeRead(probe)
	case schema.ProbeList:
		return a.probeList(probe)
	case schema.ProbeDeletionLedger:
		return a.probeLedger(probe, a.deletionLedger)
	case schema.ProbeRollbackLedger:
		return a.probeLedger(probe, a.rollbackLedger)
	case schema.ProbePermissionEpoch:
		return a.probePermissionEpoch(probe)
	case schema.ProbeConflicts:
		return a.probeConflicts(probe)
	case schema.ProbeTrustTier:
		return a.probeTrustTier(probe)
	default:
		return &schema.ProbeResponse{ProbeID: probe.ID}, nil
	}
}

func (a *Adapter) probeRead(probe schema.Probe) (*schema.ProbeResponse, error) {
	r := a.findResource(probe.TargetResource)
	if r == nil || r.deleted || r.rolledBack || r.pending {
		return &schema.ProbeResponse{ProbeID: probe.ID, Value: nil}, nil
	}
	// Scope isolation: identity namespace (scope = "actor:...") — deny cross-actor reads.
	if a.isIdentityScope(r.scope) && r.actor != probe.Actor {
		return &schema.ProbeResponse{ProbeID: probe.ID, Value: nil}, nil
	}
	val := map[string]any{
		"id":          r.id,
		"label":       r.label,
		"description": r.description,
		"tags":        r.tags,
		"scope":       r.scope,
	}
	return &schema.ProbeResponse{ProbeID: probe.ID, Value: val}, nil
}

func (a *Adapter) probeList(probe schema.Probe) (*schema.ProbeResponse, error) {
	// Identity namespace: if the scope is "owner:*", only the owner may list it.
	parts := strings.SplitN(probe.TargetScope, ":", 2)
	if len(parts) == 2 && parts[0] != probe.Actor {
		// Cross-user identity-namespace list: return empty list (scope isolation).
		return &schema.ProbeResponse{ProbeID: probe.ID, Value: map[string]any{"nodes": []any{}}}, nil
	}

	var nodes []any
	for _, r := range a.resources {
		if r.deleted || r.rolledBack || r.pending {
			continue
		}
		if r.scope != probe.TargetScope {
			continue
		}
		nodes = append(nodes, map[string]any{
			"id":    r.id,
			"label": r.label,
			"tags":  r.tags,
		})
	}
	if nodes == nil {
		nodes = []any{}
	}
	return &schema.ProbeResponse{ProbeID: probe.ID, Value: map[string]any{"nodes": nodes}}, nil
}

func (a *Adapter) probeLedger(probe schema.Probe, ledger map[string]bool) (*schema.ProbeResponse, error) {
	var nodes []any
	for id := range ledger {
		nodes = append(nodes, map[string]any{"id": id})
	}
	if nodes == nil {
		nodes = []any{}
	}
	return &schema.ProbeResponse{ProbeID: probe.ID, Value: map[string]any{"nodes": nodes}}, nil
}

func (a *Adapter) probePermissionEpoch(probe schema.Probe) (*schema.ProbeResponse, error) {
	epoch := a.epochByScope[probe.TargetScope]
	return &schema.ProbeResponse{
		ProbeID: probe.ID,
		Value:   map[string]any{"epoch": float64(epoch)},
	}, nil
}

func (a *Adapter) probeConflicts(probe schema.Probe) (*schema.ProbeResponse, error) {
	var nodes []any
	seen := make(map[string]bool)
	for _, cp := range a.conflicts {
		for _, id := range []string{cp.a, cp.b} {
			if seen[id] {
				continue
			}
			seen[id] = true
			r := a.resources[id]
			label := id
			if r != nil {
				label = r.label
			}
			nodes = append(nodes, map[string]any{"id": id, "label": label})
		}
	}
	if nodes == nil {
		nodes = []any{}
	}
	return &schema.ProbeResponse{ProbeID: probe.ID, Value: map[string]any{"nodes": nodes}}, nil
}

func (a *Adapter) probeTrustTier(probe schema.Probe) (*schema.ProbeResponse, error) {
	r := a.findResource(probe.TargetResource)
	if r == nil {
		return &schema.ProbeResponse{ProbeID: probe.ID, Value: nil}, nil
	}
	score := "1.0"
	if r.trustTier == "untrusted" {
		score = "0.1"
	}
	return &schema.ProbeResponse{
		ProbeID: probe.ID,
		Value:   map[string]any{"trust_score": score},
	}, nil
}

// findResource looks up a resource by label, event ID, or system ID.
func (a *Adapter) findResource(name string) *resource {
	// Try direct system ID lookup first.
	if r, ok := a.resources[name]; ok {
		return r
	}
	// Try idMap (label or event ID → sysID).
	if sysID, ok := a.idMap[name]; ok {
		return a.resources[sysID]
	}
	return nil
}

// isIdentityScope returns true when the scope is an identity namespace ("actor:...").
func (a *Adapter) isIdentityScope(scope string) bool {
	// Identity namespaces are "actor:..." scopes. Functional scopes (e.g. "session-state",
	// "tool-outputs", "workspace") don't contain a ":" so they're not identity namespaces.
	return strings.Contains(scope, ":")
}

func payloadTarget(ev schema.Event) string {
	if ev.Payload != nil {
		if t, ok := ev.Payload["target"].(string); ok && t != "" {
			return t
		}
		if t, ok := ev.Payload["target_id"].(string); ok && t != "" {
			return t
		}
	}
	if ev.Causal.Parent != "" {
		return ev.Causal.Parent
	}
	return ""
}

// marshalJSON is a helper so ProbeResponse.Value round-trips correctly in tests.
func marshalJSON(v any) string {
	b, _ := json.Marshal(v)
	return string(b)
}

var _ = marshalJSON // suppress unused warning
