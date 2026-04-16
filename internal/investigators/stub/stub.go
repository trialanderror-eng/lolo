package stub

import (
	"context"
	"time"

	"github.com/trialanderror-eng/lolo/internal/evidence"
	"github.com/trialanderror-eng/lolo/internal/incident"
)

type Investigator struct{}

func New() *Investigator { return &Investigator{} }

func (*Investigator) Name() string { return "stub" }

func (*Investigator) Investigate(_ context.Context, inc incident.Incident) ([]evidence.Evidence, error) {
	return []evidence.Evidence{{
		Source:     "stub",
		Kind:       "placeholder",
		At:         time.Now(),
		Confidence: 0.0,
		Summary:    "stub investigator — replace with real data sources",
		Data:       map[string]any{"incident_id": inc.ID},
	}}, nil
}
