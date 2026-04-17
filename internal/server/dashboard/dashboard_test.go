package dashboard

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
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

func TestDetail_unresolvedShowsForm(t *testing.T) {
	mux := newDashboardMux(mkStore(t))
	req := httptest.NewRequest(http.MethodGet, "/investigations/WebDown-1", nil)
	w := httptest.NewRecorder()
	mux.ServeHTTP(w, req)
	body := w.Body.String()
	if !strings.Contains(body, `action="/investigations/WebDown-1/resolve"`) {
		t.Error("detail missing resolve form action")
	}
	if !strings.Contains(body, "What fixed it?") {
		t.Error("detail missing form label")
	}
}

func TestResolve_happyPath(t *testing.T) {
	store := mkStore(t)
	mux := newDashboardMux(store)

	form := url.Values{}
	form.Set("notes", "rolled back payments to sha abc123")
	req := httptest.NewRequest(http.MethodPost, "/investigations/WebDown-1/resolve",
		strings.NewReader(form.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.SetBasicAuth("alice", "pw")

	w := httptest.NewRecorder()
	mux.ServeHTTP(w, req)
	if w.Code != http.StatusSeeOther {
		t.Fatalf("status = %d, want 303", w.Code)
	}
	if got := w.Header().Get("Location"); got != "/investigations/WebDown-1" {
		t.Errorf("redirect = %q, want /investigations/WebDown-1", got)
	}

	// Now GET the detail — should show the resolution block, not the form.
	req = httptest.NewRequest(http.MethodGet, "/investigations/WebDown-1", nil)
	w = httptest.NewRecorder()
	mux.ServeHTTP(w, req)
	body := w.Body.String()
	if !strings.Contains(body, "rolled back payments to sha abc123") {
		t.Error("resolution notes not rendered on reload")
	}
	if !strings.Contains(body, "by <span class=\"text-emerald-200\">alice</span>") {
		t.Errorf("ResolvedBy not rendered: %s", body[strings.Index(body, "Resolved"):min(len(body), strings.Index(body, "Resolved")+300)])
	}
	if strings.Contains(body, "What fixed it?") {
		t.Error("resolve form still shown after resolving")
	}

	// And the index should have a "resolved" chip.
	req = httptest.NewRequest(http.MethodGet, "/", nil)
	w = httptest.NewRecorder()
	mux.ServeHTTP(w, req)
	if !strings.Contains(w.Body.String(), ">resolved<") {
		t.Error("index missing resolved chip")
	}
}

func TestResolve_emptyNotesIs400(t *testing.T) {
	mux := newDashboardMux(mkStore(t))
	req := httptest.NewRequest(http.MethodPost, "/investigations/WebDown-1/resolve",
		strings.NewReader("notes="))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	w := httptest.NewRecorder()
	mux.ServeHTTP(w, req)
	if w.Code != http.StatusBadRequest {
		t.Errorf("status = %d, want 400", w.Code)
	}
}

func TestResolve_unknownIDIs404(t *testing.T) {
	mux := newDashboardMux(mkStore(t))
	req := httptest.NewRequest(http.MethodPost, "/investigations/nope/resolve",
		strings.NewReader("notes=x"))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	w := httptest.NewRecorder()
	mux.ServeHTTP(w, req)
	if w.Code != http.StatusNotFound {
		t.Errorf("status = %d, want 404", w.Code)
	}
}

func min(a, b int) int {
	if a < b {
		return a
	}
	return b
}
