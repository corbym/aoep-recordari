// Package mem0 provides a Mem0 adapter for the AOEP harness.
// Mem0 baseline: paper methodology uses local OpenAI-compatible embedder + in-process vector store.
// This adapter is a stub — implement by running `pip install mem0ai` and wiring the Python bridge,
// or by using mem0's REST API if a local server is running.
package mem0

import (
	"context"
	"fmt"

	"aoep-recordari/internal/schema"
)

// Adapter translates AOEP events into Mem0 API calls.
// The paper got Mem0 3/15 — replicate methodology to confirm the baseline.
type Adapter struct {
	baseURL string
}

// New creates a Mem0 adapter.
// baseURL should point to a local Mem0 REST server (e.g. http://localhost:8888).
func New(baseURL string) *Adapter {
	return &Adapter{baseURL: baseURL}
}

func (a *Adapter) Name() string { return "mem0" }

func (a *Adapter) ResetEpisode() {}

func (a *Adapter) Setup(ctx context.Context) (func(context.Context) error, error) {
	return func(ctx context.Context) error { return nil }, fmt.Errorf("mem0 adapter: not yet implemented — wire a local mem0ai server")
}

func (a *Adapter) DeliverEvent(ctx context.Context, ev schema.Event) (string, error) {
	return "", fmt.Errorf("mem0 adapter: not yet implemented")
}

func (a *Adapter) RunProbe(ctx context.Context, probe schema.Probe, resourceMap map[string]string) (*schema.ProbeResponse, error) {
	return nil, fmt.Errorf("mem0 adapter: not yet implemented")
}
