package memory

import (
	"context"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/trialanderror-eng/lolo/internal/hypothesis"
	"github.com/trialanderror-eng/lolo/internal/incident"
	"github.com/trialanderror-eng/lolo/internal/storage"
	memstore "github.com/trialanderror-eng/lolo/internal/storage/memory"
)

func seed(t *testing.T, invs ...storage.Investigation) *memstore.Storage {
	t.Helper()
	s := memstore.New(100)
	for _, inv := range invs {
		if err := s.Save(context.Background(), inv); err != nil {
			t.Fatalf("seed Save: %v", err)
		}
	}
	return s
}

func mkInv(id, summary string, scope incident.Scope, started time.Time, topHypothesis string) storage.Investigation {
	inv := storage.Investigation{
		StartedAt: started,
		Incident: incident.Incident{
			ID:          id,
			TriggeredAt: started,
			Scope:       scope,
			Signal:      incident.Signal{Source: "alertmanager", Summary: summary},
		},
	}
	if topHypothesis != "" {
		inv.Hypotheses = []hypothesis.Hypothesis{{Summary: topHypothesis, Score: 0.8}}
	}
	return inv
}

func TestInvestigate_findsSimilarByScope(t *testing.T) {
	now := time.Date(2026, 4, 17, 13, 0, 0, 0, time.UTC)
	past := mkInv("Past-1", "broken pod test",
		incident.Scope{Namespaces: []string{"sre-learning"}},
		now.Add(-45*time.Minute),
		"namespace `sre-learning` — 4 signals from kubernetes")
	unrelated := mkInv("Unrelated", "db slow",
		incident.Scope{Namespaces: []string{"infra"}},
		now.Add(-30*time.Minute), "")

	inv := New(seed(t, past, unrelated), "https://lolo.example.com")
	inv.now = func() time.Time { return now }

	cur := incident.Incident{
		ID:     "WebDown-now",
		Scope:  incident.Scope{Namespaces: []string{"sre-learning"}},
		Signal: incident.Signal{Summary: "broken pod test"},
	}
	ev, err := inv.Investigate(context.Background(), cur)
	if err != nil {
		t.Fatalf("Investigate: %v", err)
	}
	if len(ev) != 1 {
		t.Fatalf("got %d evidence, want 1 (Past-1 only): %+v", len(ev), ev)
	}
	e := ev[0]
	if e.Kind != "similar_incident" {
		t.Errorf("Kind = %q, want similar_incident", e.Kind)
	}
	if !strings.Contains(e.Summary, "broken pod test") {
		t.Errorf("Summary = %q, want it to mention the past summary", e.Summary)
	}
	if !strings.Contains(e.Summary, "45m0s ago") {
		t.Errorf("Summary = %q, want it to show elapsed time", e.Summary)
	}
	if !strings.Contains(e.Summary, "prior top hypothesis") {
		t.Errorf("Summary = %q, want it to surface the prior hypothesis", e.Summary)
	}
	if got := e.Links[0].URL; got != "https://lolo.example.com/investigations/Past-1" {
		t.Errorf("Link URL = %q, want absolute URL with publicURL prefix", got)
	}
	// Scope + signal match both → raw score = 1.0, capped to maxMemoryConfidence
	// so the memory evidence doesn't outrank live-state investigators.
	if e.Confidence != maxMemoryConfidence {
		t.Errorf("Confidence = %v, want %v (capped)", e.Confidence, maxMemoryConfidence)
	}
	if sim, _ := e.Data["similarity"].(float64); sim < 0.99 {
		t.Errorf("Data[similarity] = %v, want ~1.0 (raw score preserved)", sim)
	}
	matched, _ := e.Data["matched_on"].([]string)
	wantMatched := map[string]bool{"namespace": true, "signal": true}
	for _, m := range matched {
		if !wantMatched[m] {
			t.Errorf("matched_on has unexpected %q (want namespace and signal)", m)
		}
	}

	// Scope hint for the matched namespace must be stamped so
	// CorrelatingRanker groups this evidence with other sre-learning evidence.
	if got := e.Data["namespace"]; got != "sre-learning" {
		t.Errorf("Data[namespace] = %v, want sre-learning (scope hint for ranker)", got)
	}
}

func TestInvestigate_stampsMatchedScopeIntoData(t *testing.T) {
	now := time.Now()
	past := mkInv("past", "sig",
		incident.Scope{Services: []string{"payments"}, Namespaces: []string{"prod"}, Repos: []string{"acme/api"}},
		now.Add(-1*time.Hour), "")
	inv := New(seed(t, past), "")
	cur := incident.Incident{
		Scope:  incident.Scope{Services: []string{"payments"}, Namespaces: []string{"prod"}, Repos: []string{"acme/api"}},
		Signal: incident.Signal{Summary: "sig"},
	}
	ev, err := inv.Investigate(context.Background(), cur)
	if err != nil || len(ev) != 1 {
		t.Fatalf("Investigate: len=%d err=%v", len(ev), err)
	}
	want := map[string]string{"service": "payments", "namespace": "prod", "repo": "acme/api"}
	for k, v := range want {
		if got := ev[0].Data[k]; got != v {
			t.Errorf("Data[%s] = %v, want %q", k, got, v)
		}
	}
}

func TestInvestigate_skipsSelf(t *testing.T) {
	now := time.Now()
	self := mkInv("same-id", "x", incident.Scope{Namespaces: []string{"ns"}}, now.Add(-time.Hour), "")
	other := mkInv("other-id", "x", incident.Scope{Namespaces: []string{"ns"}}, now.Add(-2*time.Hour), "")

	inv := New(seed(t, self, other), "")
	ev, err := inv.Investigate(context.Background(), incident.Incident{
		ID:     "same-id",
		Scope:  incident.Scope{Namespaces: []string{"ns"}},
		Signal: incident.Signal{Summary: "x"},
	})
	if err != nil {
		t.Fatalf("Investigate: %v", err)
	}
	for _, e := range ev {
		if id, _ := e.Data["past_incident_id"].(string); id == "same-id" {
			t.Errorf("investigator returned self as similar: %+v", e)
		}
	}
}

func TestInvestigate_topKCap(t *testing.T) {
	now := time.Now()
	var seeded []storage.Investigation
	for i := 0; i < 6; i++ {
		seeded = append(seeded, mkInv(
			fmt.Sprintf("past-%d", i), "shared signal",
			incident.Scope{Namespaces: []string{"ns"}},
			now.Add(-time.Duration(i+1)*time.Minute), ""))
	}
	s := seed(t, seeded...)
	inv := New(s, "")
	cur := incident.Incident{ID: "now", Scope: incident.Scope{Namespaces: []string{"ns"}}, Signal: incident.Signal{Summary: "shared signal"}}
	ev, err := inv.Investigate(context.Background(), cur)
	if err != nil {
		t.Fatalf("Investigate: %v", err)
	}
	if len(ev) != defaultTopK {
		t.Errorf("got %d, want topK=%d", len(ev), defaultTopK)
	}
}

func TestInvestigate_minScoreFilter(t *testing.T) {
	now := time.Now()
	// No scope overlap, different summary → score 0, below min
	past := mkInv("unrelated", "db slow", incident.Scope{Services: []string{"payments"}}, now.Add(-time.Hour), "")
	inv := New(seed(t, past), "")
	cur := incident.Incident{
		ID:     "now",
		Scope:  incident.Scope{Services: []string{"web"}},
		Signal: incident.Signal{Summary: "broken pod test"},
	}
	ev, err := inv.Investigate(context.Background(), cur)
	if err != nil {
		t.Fatalf("Investigate: %v", err)
	}
	if len(ev) != 0 {
		t.Errorf("got %d, want 0 (below minScore)", len(ev))
	}
}

func TestSimilarityScore_tableDriven(t *testing.T) {
	cases := []struct {
		name       string
		past, cur  incident.Incident
		wantMin    float64
		wantExact  float64 // use when we can pin it; otherwise check >= wantMin
		checkExact bool
	}{
		{
			name:       "no overlap",
			past:       incident.Incident{Scope: incident.Scope{Services: []string{"a"}}, Signal: incident.Signal{Summary: "x"}},
			cur:        incident.Incident{Scope: incident.Scope{Services: []string{"b"}}, Signal: incident.Signal{Summary: "y"}},
			wantExact:  0,
			checkExact: true,
		},
		{
			name:       "full scope match, different signal",
			past:       incident.Incident{Scope: incident.Scope{Services: []string{"a"}}, Signal: incident.Signal{Summary: "x"}},
			cur:        incident.Incident{Scope: incident.Scope{Services: []string{"a"}}, Signal: incident.Signal{Summary: "y"}},
			wantExact:  0.7, // 0.7*1 + 0.3*0
			checkExact: true,
		},
		{
			name:       "full scope + signal match",
			past:       incident.Incident{Scope: incident.Scope{Services: []string{"a"}}, Signal: incident.Signal{Summary: "x"}},
			cur:        incident.Incident{Scope: incident.Scope{Services: []string{"a"}}, Signal: incident.Signal{Summary: "x"}},
			wantExact:  1.0,
			checkExact: true,
		},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got := similarityScore(c.past, c.cur)
			if c.checkExact && got != c.wantExact {
				t.Errorf("got %v, want %v", got, c.wantExact)
			}
		})
	}
}

func TestInvestigate_surfacesResolutionWhenPastIsResolved(t *testing.T) {
	now := time.Date(2026, 4, 17, 18, 0, 0, 0, time.UTC)
	past := mkInv("past", "broken pod test",
		incident.Scope{Namespaces: []string{"sre-learning"}},
		now.Add(-2*time.Hour),
		"namespace `sre-learning` — 2 signals from kubernetes")
	past.Resolution = storage.Resolution{
		ResolvedAt: now.Add(-90 * time.Minute),
		Notes:      "rolled back payments to sha abc123",
		ResolvedBy: "alice",
	}

	inv := New(seed(t, past), "")
	inv.now = func() time.Time { return now }

	cur := incident.Incident{
		ID:     "now",
		Scope:  incident.Scope{Namespaces: []string{"sre-learning"}},
		Signal: incident.Signal{Summary: "broken pod test"},
	}
	ev, err := inv.Investigate(context.Background(), cur)
	if err != nil || len(ev) != 1 {
		t.Fatalf("Investigate: len=%d err=%v", len(ev), err)
	}
	e := ev[0]
	if !strings.Contains(e.Summary, "PRIOR FIX: rolled back payments to sha abc123") {
		t.Errorf("Summary should surface resolution text, got %q", e.Summary)
	}
	if strings.Contains(e.Summary, "prior top hypothesis") {
		t.Errorf("Summary should prefer resolution over top hypothesis: %q", e.Summary)
	}
	if got := e.Data["prior_fix"]; got != "rolled back payments to sha abc123" {
		t.Errorf("Data[prior_fix] = %v", got)
	}
	if got := e.Data["prior_fix_by"]; got != "alice" {
		t.Errorf("Data[prior_fix_by] = %v", got)
	}
}

func TestInvestigate_nilStorageIsSafe(t *testing.T) {
	inv := &Investigator{} // store nil
	ev, err := inv.Investigate(context.Background(), incident.Incident{})
	if err != nil || ev != nil {
		t.Errorf("got ev=%v err=%v, want nil,nil", ev, err)
	}
}
