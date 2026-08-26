// Package adapter defines the SystemAdapter interface that each system under test must implement.
package adapter

import (
	"context"

	"aoep-recordari/internal/schema"
)

// SystemAdapter is the integration layer between AOEP events and a specific memory system.
// Each implementation translates AOEP operations into the target system's native API.
type SystemAdapter interface {
	// Name returns the system name (e.g. "recordari", "mem0").
	Name() string

	// Setup initialises a clean workspace for the benchmark run.
	// Returns a teardown function that removes all benchmark data.
	Setup(ctx context.Context) (teardown func(context.Context) error, err error)

	// DeliverEvent sends a single AOEP event to the system under test.
	// The event has already had outcome-revealing fields stripped.
	// Returns the system-assigned resource ID (may be empty for non-write ops).
	DeliverEvent(ctx context.Context, ev schema.Event) (resourceID string, err error)

	// RunProbe executes a neutral probe against the system and returns the raw response.
	RunProbe(ctx context.Context, probe schema.Probe, resourceMap map[string]string) (*schema.ProbeResponse, error)
}
