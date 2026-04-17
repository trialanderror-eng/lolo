package dashboard

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/trialanderror-eng/lolo/internal/evidence"
	"github.com/trialanderror-eng/lolo/internal/hypothesis"
	"github.com/trialanderror-eng/lolo/internal/incident"
	"github.com/trialanderror-eng/lolo/internal/storage"
	"github.com/trialanderror-eng/lolo/internal/storage/memory"
)

func mkStore(t *testing.T) *memory.Storage {
	t.Helper()
	s := memory.New(10)
	_ = s.Save(context.Background(), storage.Investigation{
		StartedAt: time.Date(2026, 4, 17, 0, 3, 0, 0, time.UTC),
		Duration:  150 * time.Millisecond,
		Incident: incident.Incident{
			ID:          "WebDown-1",
			TriggeredAt: time.Date(2026, 4, 17, 0, 3, 0, 0, time.UTC),
			Window:      30 * time.Minute,
			Scope:       incident.Scope{Services: []string{"payments"}, Namespaces: []string{"prod"}},
			Signal:      incident.Signal{Source: "alertmanager", Summary: "broken pod test"},
		},
		Hypotheses: []hypothesis.Hypothesis{{
			Summary:   "service `payments` — 2 signal(s)",
			Score:     0.9,
			Reasoning: "cross-source correlation",
			Evidence: []evidence.Evidence{{
				Source: "kubernetes", Kind: "pod_unhealthy",
				At:         time.Date(2026, 4, 17, 0, 2, 50, 0, time.UTC),
				Confidence: 0.6, Summary: "prod/brokenpod phase=Pending reason=ImagePullBackOff",
				Links: []evidence.Link{{Label: "graph", URL: "http://prom/graph?g0.expr=up"}},
			}},
		}},
	})
	return s
}

func newDashboardMux(s storage.Storage) *http.ServeMux {
	mux := http.NewServeMux()
	Register(mux, s)
	return mux
}

func TestIndex_rendersInvestigations(t *testing.T) {
	mux := newDashboardMux(mkStore(t))
	req := httptest.NewRequest(http.MethodGet, "/", nil)
	w := httptest.NewRecorder()
	mux.ServeHTTP(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", w.Code)
	}
	body := w.Body.String()
	for _, want := range []string{
		"WebDown-1",
		"broken pod test",
		"service: payments",
		"ns: prod",
		"score 0.90",
		"meta http-equiv=\"refresh\"",
	} {
		if !strings.Contains(body, want) {
			t.Errorf("index body missing %q", want)
		}
	}
}

func TestIndex_emptyState(t *testing.T) {
	mux := newDashboardMux(memory.New(10))
	req := httptest.NewRequest(http.MethodGet, "/", nil)
	w := httptest.NewRecorder()
	mux.ServeHTTP(w, req)
	if !strings.Contains(w.Body.String(), "No investigations yet") {
		t.Errorf("empty state not rendered: %s", w.Body.String())
	}
}

func TestDetail_rendersHypothesisAndEvidence(t *testing.T) {
	mux := newDashboardMux(mkStore(t))
	req := httptest.NewRequest(http.MethodGet, "/investigations/WebDown-1", nil)
	w := httptest.NewRecorder()
	mux.ServeHTTP(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", w.Code)
	}
	body := w.Body.String()
	for _, want := range []string{
		"WebDown-1",
		"cross-source correlation",
		"prod/brokenpod phase=Pending reason=ImagePullBackOff",
		"http://prom/graph?g0.expr=up",
		"[graph]",
		"alertmanager",
	} {
		if !strings.Contains(body, want) {
			t.Errorf("detail body missing %q", want)
		}
	}
}

func TestDetail_404ForUnknown(t *testing.T) {
	mux := newDashboardMux(mkStore(t))
	req := httptest.NewRequest(http.MethodGet, "/investigations/missing", nil)
	w := httptest.NewRecorder()
	mux.ServeHTTP(w, req)
	if w.Code != http.StatusNotFound {
		t.Errorf("status = %d, want 404", w.Code)
	}
}

func TestAPIList_returnsJSON(t *testing.T) {
	mux := newDashboardMux(mkStore(t))
	req := httptest.NewRequest(http.MethodGet, "/api/investigations", nil)
	w := httptest.NewRecorder()
	mux.ServeHTTP(w, req)
	if got := w.Header().Get("Content-Type"); got != "application/json" {
		t.Errorf("content-type = %q, want application/json", got)
	}
	var body struct {
		Investigations []storage.Investigation `json:"investigations"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &body); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if len(body.Investigations) != 1 || body.Investigations[0].Incident.ID != "WebDown-1" {
		t.Errorf("got %+v, want one investigation with id=WebDown-1", body.Investigations)
	}
}

func TestAPIGet_returnsSingleInvestigation(t *testing.T) {
	mux := newDashboardMux(mkStore(t))
	req := httptest.NewRequest(http.MethodGet, "/api/investigations/WebDown-1", nil)
	w := httptest.NewRecorder()
	mux.ServeHTTP(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", w.Code)
	}
	var inv storage.Investigation
	if err := json.Unmarshal(w.Body.Bytes(), &inv); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if inv.Incident.ID != "WebDown-1" {
		t.Errorf("got %+v, want WebDown-1", inv.Incident.ID)
	}
}
