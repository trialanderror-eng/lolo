package hypothesis

import (
	"context"
	"errors"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/trialanderror-eng/lolo/internal/evidence"
	"github.com/trialanderror-eng/lolo/internal/incident"
)

type fakeLLM struct {
	calls    atomic.Int32
	response string
	err      error
	record   func(system, user string)
}

func (f *fakeLLM) Narrate(_ context.Context, system, user string) (string, error) {
	f.calls.Add(1)
	if f.record != nil {
		f.record(system, user)
	}
	return f.response, f.err
}

type fixedRanker struct {
	hs []Hypothesis
}

func (r fixedRanker) Rank(context.Context, incident.Incident, []evidence.Evidence) ([]Hypothesis, error) {
	out := make([]Hypothesis, len(r.hs))
	copy(out, r.hs)
	return out, nil
}

func TestLLMRanker_replacesReasoningOnTopN(t *testing.T) {
	inner := fixedRanker{hs: []Hypothesis{
		{Summary: "first", Score: 0.9, Reasoning: "original-1"},
		{Summary: "second", Score: 0.8, Reasoning: "original-2"},
		{Summary: "third", Score: 0.7, Reasoning: "original-3"},
	}}
	f := &fakeLLM{response: "LLM narrative."}
	r := LLMRanker{Inner: inner, Client: f, TopN: 2}

	hs, err := r.Rank(context.Background(), incident.Incident{}, nil)
	if err != nil {
		t.Fatalf("Rank: %v", err)
	}
	if int(f.calls.Load()) != 2 {
		t.Errorf("LLM calls = %d, want 2 (TopN cap)", f.calls.Load())
	}
	if hs[0].Reasoning != "LLM narrative." || hs[1].Reasoning != "LLM narrative." {
		t.Errorf("top-2 Reasoning not replaced: %+v", hs[:2])
	}
	if hs[2].Reasoning != "original-3" {
		t.Errorf("hs[2].Reasoning = %q, want original preserved", hs[2].Reasoning)
	}
}

func TestLLMRanker_preservesReasoningOnError(t *testing.T) {
	inner := fixedRanker{hs: []Hypothesis{{Summary: "x", Score: 0.9, Reasoning: "original"}}}
	f := &fakeLLM{err: errors.New("model offline")}
	r := LLMRanker{Inner: inner, Client: f}

	hs, err := r.Rank(context.Background(), incident.Incident{}, nil)
	if err != nil {
		t.Fatalf("Rank should not surface LLM errors: got %v", err)
	}
	if hs[0].Reasoning != "original" {
		t.Errorf("Reasoning = %q, want 'original' (LLM failed)", hs[0].Reasoning)
	}
}

func TestLLMRanker_nilClientPassThrough(t *testing.T) {
	inner := fixedRanker{hs: []Hypothesis{{Summary: "x", Reasoning: "orig"}}}
	r := LLMRanker{Inner: inner, Client: nil}
	hs, err := r.Rank(context.Background(), incident.Incident{}, nil)
	if err != nil {
		t.Fatalf("Rank: %v", err)
	}
	if hs[0].Reasoning != "orig" {
		t.Errorf("Reasoning changed despite nil client: %+v", hs[0])
	}
}

func TestLLMRanker_promptIncludesIncidentAndEvidence(t *testing.T) {
	var gotUser string
	f := &fakeLLM{response: "ok", record: func(_, user string) { gotUser = user }}
	r := LLMRanker{
		Inner: fixedRanker{hs: []Hypothesis{{
			Summary: "namespace `prod` — 2 signals",
			Score:   0.9,
			Evidence: []evidence.Evidence{{
				Source: "kubernetes", Kind: "pod_unhealthy",
				At:      time.Date(2026, 4, 17, 12, 0, 0, 0, time.UTC),
				Summary: "prod/api phase=Pending",
			}},
		}}},
		Client: f,
	}

	_, _ = r.Rank(context.Background(),
		incident.Incident{
			ID: "WebDown-1", Window: 30 * time.Minute,
			Scope:  incident.Scope{Namespaces: []string{"prod"}, Services: []string{"api"}},
			Signal: incident.Signal{Summary: "pods unhealthy"},
		}, nil)

	for _, want := range []string{"WebDown-1", "pods unhealthy", "namespaces=prod", "services=api", "kubernetes", "prod/api phase=Pending"} {
		if !strings.Contains(gotUser, want) {
			t.Errorf("user prompt missing %q\nfull:\n%s", want, gotUser)
		}
	}
}

func TestLLMRanker_timeoutRespected(t *testing.T) {
	f := &fakeLLMWithDelay{delay: 200 * time.Millisecond, response: "late"}
	r := LLMRanker{
		Inner:   fixedRanker{hs: []Hypothesis{{Summary: "x", Score: 0.9, Reasoning: "orig"}}},
		Client:  f,
		Timeout: 20 * time.Millisecond,
	}
	hs, _ := r.Rank(context.Background(), incident.Incident{}, nil)
	if hs[0].Reasoning != "orig" {
		t.Errorf("Reasoning = %q, want 'orig' (LLM should have timed out)", hs[0].Reasoning)
	}
}

type fakeLLMWithDelay struct {
	delay    time.Duration
	response string
}

func (f *fakeLLMWithDelay) Narrate(ctx context.Context, _, _ string) (string, error) {
	select {
	case <-time.After(f.delay):
		return f.response, nil
	case <-ctx.Done():
		return "", ctx.Err()
	}
}
