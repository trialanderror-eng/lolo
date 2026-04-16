package deploys

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/trialanderror-eng/lolo/internal/incident"
)

func TestInvestigate_returnsPRsAndReleasesInWindow(t *testing.T) {
	triggered := time.Date(2026, 4, 16, 17, 30, 0, 0, time.UTC)

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/repos/acme/api/pulls":
			fmt.Fprint(w, `[
				{"number":42,"title":"hot deploy 5min before","html_url":"https://x/42","merged_at":"2026-04-16T17:25:00Z","user":{"login":"alice"},"base":{"ref":"main"},"labels":[{"name":"hotfix"}]},
				{"number":41,"title":"too old","html_url":"https://x/41","merged_at":"2026-04-16T16:00:00Z","user":{"login":"bob"},"base":{"ref":"main"}},
				{"number":40,"title":"unmerged","html_url":"https://x/40","user":{"login":"carol"},"base":{"ref":"main"}}
			]`)
		case "/repos/acme/api/releases":
			fmt.Fprint(w, `[
				{"name":"v1.2.3","tag_name":"v1.2.3","html_url":"https://x/r/v1.2.3","published_at":"2026-04-16T17:20:00Z"},
				{"name":"draft","tag_name":"vX","html_url":"https://x/r/vX","published_at":"2026-04-16T17:28:00Z","draft":true}
			]`)
		default:
			t.Errorf("unexpected path %s", r.URL.Path)
			http.NotFound(w, r)
		}
	}))
	defer srv.Close()

	inv := New("test-token", []string{"acme/api"})
	inv.client.baseURL = srv.URL // point at the fake

	inc := incident.Incident{
		ID:          "i-1",
		TriggeredAt: triggered,
		Window:      30 * time.Minute,
	}
	ev, err := inv.Investigate(context.Background(), inc)
	if err != nil {
		t.Fatalf("Investigate: %v", err)
	}

	// expect: PR #42 (in window), release v1.2.3 (in window). PR #41 too old, #40 unmerged, draft skipped.
	if got := len(ev); got != 2 {
		t.Fatalf("evidence count = %d, want 2: %+v", got, ev)
	}
	gotKinds := map[string]bool{ev[0].Kind: true, ev[1].Kind: true}
	if !gotKinds["merged_pr"] || !gotKinds["release"] {
		t.Errorf("kinds = %v, want merged_pr + release", gotKinds)
	}

	// Confidence: merged 5 min before trigger → 0.9 bucket
	for _, e := range ev {
		if e.Kind == "merged_pr" && e.Confidence != 0.9 {
			t.Errorf("PR confidence = %v, want 0.9 for 5min-before", e.Confidence)
		}
	}
}

func TestInvestigate_noTokenIsNoOp(t *testing.T) {
	inv := New("", []string{"acme/api"})
	ev, err := inv.Investigate(context.Background(), incident.Incident{Window: 30 * time.Minute, TriggeredAt: time.Now()})
	if err != nil {
		t.Fatalf("Investigate: %v", err)
	}
	if ev != nil {
		t.Errorf("evidence = %v, want nil when token absent", ev)
	}
}

func TestInvestigate_noReposIsNoOp(t *testing.T) {
	inv := New("test-token", nil)
	ev, err := inv.Investigate(context.Background(), incident.Incident{Window: 30 * time.Minute, TriggeredAt: time.Now()})
	if err != nil {
		t.Fatalf("Investigate: %v", err)
	}
	if ev != nil {
		t.Errorf("evidence = %v, want nil when no repos in scope or defaults", ev)
	}
}

func TestRepoSet_dedupesAndKeepsOrder(t *testing.T) {
	got := repoSet([]string{"a/b", "c/d"}, []string{"a/b", "e/f"})
	want := []string{"a/b", "c/d", "e/f"}
	if len(got) != len(want) {
		t.Fatalf("got %v, want %v", got, want)
	}
	for i := range got {
		if got[i] != want[i] {
			t.Errorf("got[%d]=%s want %s", i, got[i], want[i])
		}
	}
}
