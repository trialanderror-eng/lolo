// Package memory is an Investigator that surfaces past investigations
// similar to the current incident. It gives lolo a sense of "we've seen
// this before" without any external model or fine-tuning — the
// knowledge is the operator's own accumulated investigation history.
package memory

import (
	"context"
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/trialanderror-eng/lolo/internal/evidence"
	"github.com/trialanderror-eng/lolo/internal/incident"
	"github.com/trialanderror-eng/lolo/internal/storage"
)

const (
	defaultMinScore = 0.3
	defaultTopK     = 3
	lookbackLimit   = 500
)

type Investigator struct {
	store     storage.Storage
	publicURL string // prefixed onto evidence Links so they work outside the dashboard
	minScore  float64
	topK      int
	now       func() time.Time // injectable for tests
}

// New wires the investigator to a Storage. publicURL is the external base
// URL of lolo (e.g., "https://lolo.internal"); when empty, evidence Links
// use a relative path and only resolve inside the dashboard.
func New(store storage.Storage, publicURL string) *Investigator {
	return &Investigator{
		store:     store,
		publicURL: strings.TrimRight(publicURL, "/"),
		minScore:  defaultMinScore,
		topK:      defaultTopK,
		now:       time.Now,
	}
}

func (*Investigator) Name() string { return "memory" }

func (i *Investigator) Investigate(ctx context.Context, inc incident.Incident) ([]evidence.Evidence, error) {
	if i.store == nil {
		return nil, nil
	}
	past, err := i.store.List(ctx, lookbackLimit)
	if err != nil {
		return nil, err
	}

	type scored struct {
		inv   storage.Investigation
		score float64
		match []string
	}
	var hits []scored
	for _, p := range past {
		if p.Incident.ID == inc.ID {
			continue
		}
		s := similarityScore(p.Incident, inc)
		if s < i.minScore {
			continue
		}
		hits = append(hits, scored{inv: p, score: s, match: matchedOn(p.Incident, inc)})
	}

	sort.SliceStable(hits, func(a, b int) bool { return hits[a].score > hits[b].score })
	if len(hits) > i.topK {
		hits = hits[:i.topK]
	}

	out := make([]evidence.Evidence, 0, len(hits))
	for _, h := range hits {
		ago := i.now().Sub(h.inv.StartedAt).Round(time.Minute)
		top := topHypothesisSummary(h.inv)
		summary := fmt.Sprintf("similar incident %s ago (matched %s): %s",
			ago, strings.Join(h.match, ", "), h.inv.Incident.Signal.Summary)
		if top != "" {
			summary += " — prior top hypothesis: " + top
		}
		out = append(out, evidence.Evidence{
			Source:     "memory",
			Kind:       "similar_incident",
			At:         h.inv.StartedAt,
			Confidence: h.score,
			Summary:    summary,
			Data: map[string]any{
				"past_incident_id": h.inv.Incident.ID,
				"matched_on":       h.match,
				"similarity":       h.score,
			},
			Links: []evidence.Link{{
				Label: "past RCA",
				URL:   i.publicURL + "/investigations/" + h.inv.Incident.ID,
			}},
		})
	}
	return out, nil
}

// similarityScore mixes scope overlap (jaccard) and signal-summary exact
// match. It is deliberately simple — good enough for MVP, easy to extend
// later with evidence-reason matching or embeddings.
func similarityScore(past, cur incident.Incident) float64 {
	const (
		wScope  = 0.7
		wSignal = 0.3
	)
	scopeSim := jaccard(scopeSet(past.Scope), scopeSet(cur.Scope))
	signalMatch := 0.0
	if past.Signal.Summary != "" && past.Signal.Summary == cur.Signal.Summary {
		signalMatch = 1.0
	}
	return wScope*scopeSim + wSignal*signalMatch
}

func scopeSet(s incident.Scope) map[string]bool {
	out := map[string]bool{}
	for _, x := range s.Services {
		out["svc:"+x] = true
	}
	for _, x := range s.Namespaces {
		out["ns:"+x] = true
	}
	for _, x := range s.Clusters {
		out["cluster:"+x] = true
	}
	for _, x := range s.Repos {
		out["repo:"+x] = true
	}
	return out
}

func jaccard(a, b map[string]bool) float64 {
	if len(a) == 0 && len(b) == 0 {
		return 0
	}
	inter, union := 0, 0
	for k := range a {
		if b[k] {
			inter++
		}
		union++
	}
	for k := range b {
		if !a[k] {
			union++
		}
	}
	if union == 0 {
		return 0
	}
	return float64(inter) / float64(union)
}

func matchedOn(past, cur incident.Incident) []string {
	var out []string
	if intersects(past.Scope.Services, cur.Scope.Services) {
		out = append(out, "service")
	}
	if intersects(past.Scope.Namespaces, cur.Scope.Namespaces) {
		out = append(out, "namespace")
	}
	if intersects(past.Scope.Clusters, cur.Scope.Clusters) {
		out = append(out, "cluster")
	}
	if intersects(past.Scope.Repos, cur.Scope.Repos) {
		out = append(out, "repo")
	}
	if past.Signal.Summary != "" && past.Signal.Summary == cur.Signal.Summary {
		out = append(out, "signal")
	}
	if len(out) == 0 {
		out = []string{"weak"}
	}
	return out
}

func intersects(a, b []string) bool {
	for _, x := range a {
		for _, y := range b {
			if x == y {
				return true
			}
		}
	}
	return false
}

func topHypothesisSummary(inv storage.Investigation) string {
	if len(inv.Hypotheses) == 0 {
		return ""
	}
	return inv.Hypotheses[0].Summary
}
