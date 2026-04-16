package hypothesis

import (
	"context"
	"fmt"
	"sort"

	"github.com/trialanderror-eng/lolo/internal/evidence"
	"github.com/trialanderror-eng/lolo/internal/incident"
)

// DefaultRanker groups evidence by source and emits one hypothesis per group,
// scoring by the mean confidence. It is deliberately dumb — a placeholder for
// a real rules-based or LLM-backed ranker. Useful until one of those lands.
type DefaultRanker struct{}

func (DefaultRanker) Rank(_ context.Context, _ incident.Incident, ev []evidence.Evidence) ([]Hypothesis, error) {
	if len(ev) == 0 {
		return nil, nil
	}
	bySource := map[string][]evidence.Evidence{}
	for _, e := range ev {
		bySource[e.Source] = append(bySource[e.Source], e)
	}
	out := make([]Hypothesis, 0, len(bySource))
	for src, group := range bySource {
		var sum float64
		for _, e := range group {
			sum += e.Confidence
		}
		score := sum / float64(len(group))
		out = append(out, Hypothesis{
			Summary:   fmt.Sprintf("%d signal(s) from %s", len(group), src),
			Score:     score,
			Evidence:  group,
			Reasoning: "grouped by source (default ranker — replace for real scoring)",
		})
	}
	sort.SliceStable(out, func(i, j int) bool { return out[i].Score > out[j].Score })
	return out, nil
}
