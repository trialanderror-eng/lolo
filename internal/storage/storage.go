// Package storage persists completed investigations so the dashboard
// (and any future consumer) can read them after the fact.
package storage

import (
	"context"
	"time"

	"github.com/trialanderror-eng/lolo/internal/hypothesis"
	"github.com/trialanderror-eng/lolo/internal/incident"
)

// Investigation is the unit lolo writes after each webhook fires.
type Investigation struct {
	Incident   incident.Incident       `json:"incident"`
	Hypotheses []hypothesis.Hypothesis `json:"hypotheses"`
	StartedAt  time.Time               `json:"started_at"`
	Duration   time.Duration           `json:"duration"`
	Resolution Resolution              `json:"resolution,omitempty"`
}

// Resolution is an operator-captured note about what actually fixed the
// incident. A nil/zero Resolution means the investigation isn't marked
// resolved yet. The memory investigator surfaces the Notes field on
// future similar incidents — this is how lolo accumulates
// organization-specific fix knowledge without any external training.
type Resolution struct {
	ResolvedAt time.Time `json:"resolved_at,omitempty"`
	Notes      string    `json:"notes,omitempty"`
	ResolvedBy string    `json:"resolved_by,omitempty"`
}

// Resolved reports whether this investigation has a captured resolution.
func (i Investigation) Resolved() bool { return !i.Resolution.ResolvedAt.IsZero() }

// Storage is the read/write contract. Implementations may bound size,
// expire entries, or persist to disk — that's their concern.
type Storage interface {
	Save(ctx context.Context, inv Investigation) error
	Get(ctx context.Context, id string) (Investigation, bool, error)
	List(ctx context.Context, limit int) ([]Investigation, error)
}
