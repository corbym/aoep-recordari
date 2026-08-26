// Package episode loads AOEP episode JSON files and strips outcome-revealing fields.
package episode

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"aoep-recordari/internal/schema"
)

// Episode is the full in-memory representation of an AOEP episode.
type Episode struct {
	ID          string         `json:"id"`
	Name        string         `json:"name"`
	Description string         `json:"description"`
	Events      []schema.Event `json:"events"`
	Probes      []schema.Probe `json:"probes"`
}

// LoadAll loads all episode JSON files from dir, sorted by filename.
func LoadAll(dir string) ([]*Episode, error) {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return nil, fmt.Errorf("read episodes dir: %w", err)
	}

	var episodes []*Episode
	for _, e := range entries {
		if e.IsDir() || !strings.HasSuffix(e.Name(), ".json") {
			continue
		}
		ep, err := Load(filepath.Join(dir, e.Name()))
		if err != nil {
			return nil, fmt.Errorf("load %s: %w", e.Name(), err)
		}
		episodes = append(episodes, ep)
	}

	sort.Slice(episodes, func(i, j int) bool {
		return episodes[i].ID < episodes[j].ID
	})
	return episodes, nil
}

// Load reads a single episode file.
func Load(path string) (*Episode, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	var ep Episode
	if err := json.Unmarshal(data, &ep); err != nil {
		return nil, fmt.Errorf("parse json: %w", err)
	}
	return &ep, nil
}

// StripForDelivery returns the events with outcome-revealing fields removed.
// The original episode is not modified.
func StripForDelivery(ep *Episode) []schema.Event {
	stripped := make([]schema.Event, len(ep.Events))
	for i, ev := range ep.Events {
		ev.OutcomeExpected = nil
		stripped[i] = ev
	}
	return stripped
}
