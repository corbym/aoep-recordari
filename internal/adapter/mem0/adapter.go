// Package mem0 provides a Mem0 adapter for the AOEP-v0 benchmark harness.
// Drives a local FastAPI bridge (mem0_server/main.py) that wraps mem0.Memory.
// Paper baseline: Mem0 scored 3/15 on AOEP-v0.
package mem0

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"

	"aoep-recordari/internal/schema"
)

// Adapter translates AOEP events into HTTP calls against the local mem0 bridge.
type Adapter struct {
	baseURL string
	http    *http.Client
	idMap   map[string]string // AOEP key → mem0 memory ID
}

// New creates a Mem0 adapter pointing at the given bridge URL.
func New(baseURL string) *Adapter {
	return &Adapter{
		baseURL: strings.TrimRight(baseURL, "/"),
		http:    &http.Client{},
		idMap:   make(map[string]string),
	}
}

func (a *Adapter) Name() string { return "mem0" }

func (a *Adapter) ResetEpisode() {
	a.idMap = make(map[string]string)
	_ = a.deleteReq("/reset_all", nil) // best-effort; errors non-fatal
}

func (a *Adapter) Setup(ctx context.Context) (func(context.Context) error, error) {
	resp, err := a.http.Get(a.baseURL + "/health")
	if err != nil {
		return nil, fmt.Errorf("mem0 bridge not reachable at %s: %w", a.baseURL, err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("mem0 bridge unhealthy: HTTP %d", resp.StatusCode)
	}
	_ = a.deleteReq("/reset_all", nil)
	return func(ctx context.Context) error {
		_ = a.deleteReq("/reset_all", nil)
		return nil
	}, nil
}

func (a *Adapter) DeliverEvent(ctx context.Context, ev schema.Event) (string, error) {
	switch ev.Operation {
	case schema.OpWrite, schema.OpUpdate, schema.OpQuarantine:
		return a.deliverWrite(ctx, ev)
	case schema.OpDelete, schema.OpTombstone:
		return "", a.deliverDelete(ev)
	case schema.OpRollback:
		// mem0 can delete but cannot restore — rollback appears as a delete only.
		return "", a.deliverDelete(ev)
	case schema.OpDeny:
		return "", nil // harness signals: block this write
	default:
		return "", nil // read, share, unshare, validate — no state change
	}
}

func (a *Adapter) deliverWrite(_ context.Context, ev schema.Event) (string, error) {
	body := map[string]any{
		"content": buildContent(ev),
		"user_id": ev.Actor,
		"metadata": map[string]any{
			"aoep_event_id":    ev.ID,
			"aoep_idempotency": ev.IdempotencyKey,
			"trust_tier":       string(ev.Provenance.TrustTier),
			"scope":            ev.Scope,
		},
	}
	result, err := a.post("/add", body)
	if err != nil {
		return "", err
	}
	memID, _ := result["id"].(string)
	a.register(ev, memID)
	return memID, nil
}

func (a *Adapter) deliverDelete(ev schema.Event) error {
	target := payloadTarget(ev)
	memID := a.resolveID(target)
	if memID == "" {
		return nil
	}
	return a.deleteReq("/delete/"+memID, nil)
}

func (a *Adapter) RunProbe(_ context.Context, probe schema.Probe, resourceMap map[string]string) (*schema.ProbeResponse, error) {
	switch probe.Query {
	case schema.ProbeRead:
		return a.probeRead(probe, resourceMap)
	case schema.ProbeList:
		return a.probeList(probe)
	case schema.ProbeDeletionLedger, schema.ProbeRollbackLedger,
		schema.ProbePermissionEpoch, schema.ProbeConflicts, schema.ProbeTrustTier:
		// mem0 has no governance ledgers or epoch tracking.
		return &schema.ProbeResponse{ProbeID: probe.ID, Value: nil}, nil
	default:
		return &schema.ProbeResponse{ProbeID: probe.ID}, nil
	}
}

func (a *Adapter) probeRead(probe schema.Probe, resourceMap map[string]string) (*schema.ProbeResponse, error) {
	// Try direct lookup by system ID first.
	if memID, ok := resourceMap[probe.TargetResource]; ok {
		got, err := a.getJSON("/get/" + memID)
		if err == nil && got != nil {
			return &schema.ProbeResponse{ProbeID: probe.ID, Value: got}, nil
		}
	}
	// Semantic search fallback — uses probe.Actor for user-scope isolation.
	body := map[string]any{
		"query":   probe.TargetResource,
		"user_id": probe.Actor,
		"limit":   5,
	}
	result, err := a.post("/search", body)
	if err != nil {
		return &schema.ProbeResponse{ProbeID: probe.ID}, nil
	}
	mems, _ := result["memories"].([]any)
	if len(mems) == 0 {
		return &schema.ProbeResponse{ProbeID: probe.ID, Value: nil}, nil
	}
	return &schema.ProbeResponse{ProbeID: probe.ID, Value: result}, nil
}

func (a *Adapter) probeList(probe schema.Probe) (*schema.ProbeResponse, error) {
	got, err := a.getJSON("/list?user_id=" + probe.Actor)
	if err != nil {
		return &schema.ProbeResponse{ProbeID: probe.ID}, err
	}
	return &schema.ProbeResponse{ProbeID: probe.ID, Value: got}, nil
}

// --- ID tracking ---

func (a *Adapter) register(ev schema.Event, memID string) {
	if memID == "" {
		return
	}
	if ev.IdempotencyKey != "" {
		a.idMap[ev.IdempotencyKey] = memID
	}
	if ev.ID != "" {
		a.idMap[ev.ID] = memID
	}
	if ev.Payload != nil {
		if label, ok := ev.Payload["label"].(string); ok && label != "" {
			a.idMap[label] = memID
		}
	}
}

func (a *Adapter) resolveID(name string) string {
	if id, ok := a.idMap[name]; ok {
		return id
	}
	return name
}

// --- HTTP helpers ---

func (a *Adapter) post(path string, body map[string]any) (map[string]any, error) {
	data, err := json.Marshal(body)
	if err != nil {
		return nil, err
	}
	resp, err := a.http.Post(a.baseURL+path, "application/json", bytes.NewReader(data))
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	raw, _ := io.ReadAll(resp.Body)
	if resp.StatusCode >= 400 {
		return nil, fmt.Errorf("POST %s: HTTP %d: %s", path, resp.StatusCode, string(raw))
	}
	var out map[string]any
	_ = json.Unmarshal(raw, &out)
	return out, nil
}

func (a *Adapter) getJSON(path string) (map[string]any, error) {
	resp, err := a.http.Get(a.baseURL + path)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode == http.StatusNotFound {
		return nil, nil
	}
	raw, _ := io.ReadAll(resp.Body)
	if resp.StatusCode >= 400 {
		return nil, fmt.Errorf("GET %s: HTTP %d", path, resp.StatusCode)
	}
	var out map[string]any
	_ = json.Unmarshal(raw, &out)
	return out, nil
}

func (a *Adapter) deleteReq(path string, body map[string]any) error {
	var reqBody io.Reader
	if body != nil {
		data, _ := json.Marshal(body)
		reqBody = bytes.NewReader(data)
	}
	req, err := http.NewRequest(http.MethodDelete, a.baseURL+path, reqBody)
	if err != nil {
		return err
	}
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	resp, err := a.http.Do(req)
	if err != nil {
		return err
	}
	resp.Body.Close()
	return nil
}

// --- Content helpers ---

func buildContent(ev schema.Event) string {
	var sb strings.Builder
	if ev.Payload != nil {
		if label, ok := ev.Payload["label"].(string); ok && label != "" {
			sb.WriteString(label)
			sb.WriteString(": ")
		}
		for _, key := range []string{"content", "text", "summary"} {
			if v, ok := ev.Payload[key].(string); ok && v != "" {
				sb.WriteString(v)
				break
			}
		}
		if sb.Len() == 0 || (len([]rune(sb.String())) > 0 && !strings.Contains(sb.String(), ":")) {
			data, _ := json.Marshal(ev.Payload)
			sb.Reset()
			sb.Write(data)
		}
	}
	if sb.Len() == 0 {
		sb.WriteString(ev.ID)
	}
	return sb.String()
}

func payloadTarget(ev schema.Event) string {
	if ev.Payload != nil {
		if t, ok := ev.Payload["target"].(string); ok && t != "" {
			return t
		}
	}
	if ev.Causal.Parent != "" {
		return ev.Causal.Parent
	}
	return ""
}
