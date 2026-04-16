package slack

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/trialanderror-eng/lolo/internal/evidence"
	"github.com/trialanderror-eng/lolo/internal/hypothesis"
	"github.com/trialanderror-eng/lolo/internal/incident"
)

func TestEmit_postsValidBlockKitPayload(t *testing.T) {
	var got Payload
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		if err := json.Unmarshal(body, &got); err != nil {
			t.Errorf("invalid JSON: %v", err)
		}
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()

	inc := incident.Incident{
		ID:          "HighErrorRate-1",
		TriggeredAt: time.Date(2026, 4, 16, 17, 30, 0, 0, time.UTC),
		Window:      30 * time.Minute,
		Signal:      incident.Signal{Source: "alertmanager", Summary: "Error rate 5%"},
	}
	hs := []hypothesis.Hypothesis{{
		Summary:   "1 signal(s) from github.deploys",
		Score:     0.9,
		Reasoning: "deploy 5 min before page",
		Evidence: []evidence.Evidence{{
			Source:  "github.deploys",
			Kind:    "merged_pr",
			Summary: "acme/api: PR #42 merged",
			Links:   []evidence.Link{{Label: "PR", URL: "https://github.com/acme/api/pull/42"}},
		}},
	}}

	if err := New(srv.URL).Emit(context.Background(), inc, hs); err != nil {
		t.Fatalf("Emit: %v", err)
	}

	if !strings.Contains(got.Text, "HighErrorRate-1") {
		t.Errorf("fallback text missing incident id: %q", got.Text)
	}
	if len(got.Blocks) < 4 {
		t.Fatalf("want header+section+divider+hypothesis blocks, got %d", len(got.Blocks))
	}
	if got.Blocks[0].Type != "header" {
		t.Errorf("block 0 = %q, want header", got.Blocks[0].Type)
	}
	// Somewhere in the blocks we must find a link to the PR (from evidence context).
	found := false
	for _, b := range got.Blocks {
		for _, el := range b.Elements {
			if strings.Contains(el.Text, "https://github.com/acme/api/pull/42") {
				found = true
			}
		}
	}
	if !found {
		t.Errorf("PR link not rendered into any context block: %+v", got.Blocks)
	}
}

func TestEmit_emptyWebhookURLIsNoOp(t *testing.T) {
	// No server — if it tried to POST it would fail.
	err := New("").Emit(context.Background(), incident.Incident{}, nil)
	if err != nil {
		t.Errorf("Emit with empty URL returned %v, want nil", err)
	}
}

func TestEmit_truncatesToMaxHypotheses(t *testing.T) {
	var got Payload
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		_ = json.Unmarshal(body, &got)
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()

	hs := make([]hypothesis.Hypothesis, maxHypotheses+3)
	for i := range hs {
		hs[i] = hypothesis.Hypothesis{Summary: "h", Score: float64(i)}
	}
	if err := New(srv.URL).Emit(context.Background(), incident.Incident{ID: "i"}, hs); err != nil {
		t.Fatalf("Emit: %v", err)
	}
	// last block should be the "+N more omitted" context
	last := got.Blocks[len(got.Blocks)-1]
	if last.Type != "context" || !strings.Contains(last.Elements[0].Text, "3 more") {
		t.Errorf("tail block = %+v, want '3 more hypotheses omitted' context", last)
	}
}

func TestEmit_noHypothesesEmitsEmptyNotice(t *testing.T) {
	var got Payload
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		_ = json.Unmarshal(body, &got)
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()

	if err := New(srv.URL).Emit(context.Background(), incident.Incident{ID: "i"}, nil); err != nil {
		t.Fatalf("Emit: %v", err)
	}
	last := got.Blocks[len(got.Blocks)-1]
	if !strings.Contains(last.Text.Text, "No hypotheses") {
		t.Errorf("want empty-notice block, got %+v", last)
	}
}

func TestEmit_returnsErrorOnNon2xx(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusTeapot)
	}))
	defer srv.Close()

	err := New(srv.URL).Emit(context.Background(), incident.Incident{ID: "i"}, nil)
	if err == nil || !strings.Contains(err.Error(), "418") {
		t.Errorf("err = %v, want 418 status error", err)
	}
}
