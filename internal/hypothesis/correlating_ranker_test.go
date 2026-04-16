package hypothesis

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/trialanderror-eng/lolo/internal/evidence"
	"github.com/trialanderror-eng/lolo/internal/incident"
)

func TestCorrelatingRanker_boostsMultiSourceGroup(t *testing.T) {
	ev := []evidence.Evidence{
		{Source: "github.deploys", Confidence: 0.7, At: time.Unix(1000, 0),
			Summary: "PR #42 merged", Data: map[string]any{"repo": "acme/api"}},
		// k8s evidence about service "payments" — different scope key, won't merge
		{Source: "kubernetes", Confidence: 0.85, At: time.Unix(1010, 0),
			Summary: "OOM in payments-1", Data: map[string]any{"namespace": "prod", "service": "payments"}},
		// prometheus evidence about service "payments" — should merge with k8s
		{Source: "prometheus", Confidence: 0.75, At: time.Unix(1015, 0),
			Summary: "error rate spike",
			Data: map[string]any{
				"query":  "rate(errors[5m])",
				"labels": map[string]string{"service": "payments"},
			}},
	}

	hs, err := CorrelatingRanker{}.Rank(context.Background(), incident.Incident{}, ev)
	if err != nil {
		t.Fatalf("Rank: %v", err)
	}

	// Expect two hypotheses for grouped scopes (repo:acme/api, service:payments).
	// service:payments should be the top-scored one because it has 2 sources
	// boosted: max(0.85, 0.75) * 1.2 = 1.02 → clamped to 1.0.
	if len(hs) != 2 {
		t.Fatalf("hypothesis count = %d, want 2: %+v", len(hs), hs)
	}
	if hs[0].Score != 1.0 {
		t.Errorf("top score = %v, want 1.0 (clamped from 0.85*1.2)", hs[0].Score)
	}
	if !strings.Contains(hs[0].Summary, "service") || !strings.Contains(hs[0].Summary, "payments") {
		t.Errorf("top summary = %q, want it to mention service:payments", hs[0].Summary)
	}
	if !strings.Contains(hs[0].Reasoning, "kubernetes") || !strings.Contains(hs[0].Reasoning, "prometheus") {
		t.Errorf("reasoning = %q, want both source names", hs[0].Reasoning)
	}
	if len(hs[0].Evidence) != 2 {
		t.Errorf("top hypothesis has %d evidence, want 2", len(hs[0].Evidence))
	}
	// Evidence within a group must be time-sorted.
	if !hs[0].Evidence[0].At.Before(hs[0].Evidence[1].At) {
		t.Errorf("evidence not time-sorted: %v then %v", hs[0].Evidence[0].At, hs[0].Evidence[1].At)
	}
}

func TestCorrelatingRanker_orphansStayPerItem(t *testing.T) {
	ev := []evidence.Evidence{
		{Source: "stub", Confidence: 0.5, Summary: "no scope here", Data: nil},
		{Source: "stub", Confidence: 0.4, Summary: "also no scope", Data: map[string]any{"misc": "x"}},
	}
	hs, err := CorrelatingRanker{}.Rank(context.Background(), incident.Incident{}, ev)
	if err != nil {
		t.Fatalf("Rank: %v", err)
	}
	if len(hs) != 2 {
		t.Errorf("orphan count = %d, want 2 (one hypothesis per orphan)", len(hs))
	}
	for _, h := range hs {
		if !strings.Contains(h.Reasoning, "uncorrelated") {
			t.Errorf("orphan reasoning = %q, want 'uncorrelated' marker", h.Reasoning)
		}
	}
}

func TestCorrelatingRanker_topNTruncates(t *testing.T) {
	var ev []evidence.Evidence
	for i := 0; i < 10; i++ {
		ev = append(ev, evidence.Evidence{
			Source:     "stub",
			Confidence: float64(i) / 10,
			Data:       map[string]any{"service": string(rune('a' + i))},
		})
	}
	hs, err := CorrelatingRanker{TopN: 3}.Rank(context.Background(), incident.Incident{}, ev)
	if err != nil {
		t.Fatalf("Rank: %v", err)
	}
	if len(hs) != 3 {
		t.Fatalf("got %d, want 3 (TopN cap)", len(hs))
	}
	// Should be the top 3 by confidence: 0.9, 0.8, 0.7.
	for i, want := range []float64{0.9, 0.8, 0.7} {
		if hs[i].Score != want {
			t.Errorf("hs[%d].Score = %v, want %v", i, hs[i].Score, want)
		}
	}
}

func TestCorrelatingRanker_emptyInputReturnsNil(t *testing.T) {
	hs, err := CorrelatingRanker{}.Rank(context.Background(), incident.Incident{}, nil)
	if err != nil {
		t.Fatalf("Rank: %v", err)
	}
	if hs != nil {
		t.Errorf("got %v, want nil", hs)
	}
}

func TestBucketKey_extractionPriority(t *testing.T) {
	cases := []struct {
		name string
		data map[string]any
		want string
	}{
		{"service wins over namespace", map[string]any{"service": "s", "namespace": "n"}, "service:s"},
		{"namespace when no service", map[string]any{"namespace": "n", "repo": "r"}, "namespace:n"},
		{"repo when no namespace", map[string]any{"repo": "acme/api"}, "repo:acme/api"},
		{"pod when nothing else", map[string]any{"pod": "api-1"}, "pod:api-1"},
		{"nothing", map[string]any{"random": "x"}, ""},
		{"nil data", nil, ""},
		{"prometheus nested labels (string map)",
			map[string]any{"labels": map[string]string{"service": "payments"}}, "service:payments"},
		{"json-roundtripped labels (any map)",
			map[string]any{"labels": map[string]any{"service": "payments"}}, "service:payments"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got := bucketKey(evidence.Evidence{Data: c.data})
			if got != c.want {
				t.Errorf("got %q, want %q", got, c.want)
			}
		})
	}
}

func TestCorrelatingRanker_threeSourcesBoostMore(t *testing.T) {
	ev := []evidence.Evidence{
		{Source: "github.deploys", Confidence: 0.6, Data: map[string]any{"service": "payments"}},
		{Source: "kubernetes", Confidence: 0.6, Data: map[string]any{"service": "payments"}},
		{Source: "prometheus", Confidence: 0.6, Data: map[string]any{"service": "payments"}},
	}
	hs, _ := CorrelatingRanker{}.Rank(context.Background(), incident.Incident{}, ev)
	if len(hs) != 1 {
		t.Fatalf("got %d hypotheses, want 1", len(hs))
	}
	// max(0.6) * 1.4 = 0.84
	if got := hs[0].Score; got < 0.83 || got > 0.85 {
		t.Errorf("score = %v, want ~0.84 (3-source boost)", got)
	}
}
