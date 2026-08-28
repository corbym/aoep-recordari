// Package nomem is the no-memory floor adapter.
// It stores nothing and returns nil for all probes, establishing the lower bound:
// no positive obligation should be vacuously passable by a system with zero memory.
package nomem

import (
	"context"

	"aoep-recordari/internal/schema"
)

// Adapter is the no-memory floor. Stores nothing, observes nothing.
type Adapter struct{}

func New() *Adapter { return &Adapter{} }

func (a *Adapter) Name() string { return "nomem" }

func (a *Adapter) Setup(_ context.Context) (func(context.Context) error, error) {
	return func(_ context.Context) error { return nil }, nil
}

func (a *Adapter) ResetEpisode() {}

// DeliverEvent discards all events and returns no system ID.
func (a *Adapter) DeliverEvent(_ context.Context, _ schema.Event) (string, error) {
	return "", nil
}

// RunProbe returns nil for all probes — the floor has no stored state to surface.
func (a *Adapter) RunProbe(_ context.Context, probe schema.Probe, _ map[string]string) (*schema.ProbeResponse, error) {
	return &schema.ProbeResponse{ProbeID: probe.ID, Value: nil}, nil
}
