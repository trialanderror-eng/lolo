package hypothesis

import (
	"context"

	"github.com/trialanderror-eng/lolo/internal/evidence"
	"github.com/trialanderror-eng/lolo/internal/incident"
)

type Hypothesis struct {
	Summary     string
	Score       float64
	Evidence    []evidence.Evidence
	Reasoning   string
	Remediation []string
}

type Ranker interface {
	Rank(ctx context.Context, inc incident.Incident, ev []evidence.Evidence) ([]Hypothesis, error)
}
