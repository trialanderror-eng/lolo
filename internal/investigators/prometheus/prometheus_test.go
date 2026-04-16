package prometheus

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"time"

	"github.com/trialanderror-eng/lolo/internal/incident"
)

// alertmanagerPayload is the shape extractQueries expects after an
// Alertmanager webhook has been decoded into a generic map.
func alertmanagerPayload(generatorURL string) map[string]any {
	return map[string]any{
		"alerts": []any{
			map[string]any{"generatorURL": generatorURL},
		},
	}
}

func TestInvestigate_emitsSeriesEvidence(t *testing.T) {
	promResp := `{
		"status": "success",
		"data": {
			"resultType": "matrix",
			"result": [
				{"metric":{"service":"payments"},"values":[[1000,"0.01"],[1015,"0.05"],[1030,"0.12"]]},
				{"metric":{"service":"web"},"values":[[1000,"0.01"],[1015,"0.011"],[1030,"0.012"]]}
			]
		}
	}`
	var seenQuery string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/v1/query_range" {
			t.Errorf("unexpected path %s", r.URL.Path)
		}
		seenQuery = r.URL.Query().Get("query")
		w.Header().Set("Content-Type", "application/json")
		w.Write([]byte(promResp))
	}))
	defer srv.Close()

	inv := New(srv.URL, "")
	raw := alertmanagerPayload(
		"http://prometheus.local/graph?g0.expr=" + url.QueryEscape(`rate(errors_total[5m])`) + "&g0.tab=1",
	)
	inc := incident.Incident{
		ID:          "i-1",
		TriggeredAt: time.Unix(1030, 0).UTC(),
		Window:      30 * time.Second,
		Signal:      incident.Signal{Source: "alertmanager", Raw: raw},
	}

	ev, err := inv.Investigate(context.Background(), inc)
	if err != nil {
		t.Fatalf("Investigate: %v", err)
	}
	if seenQuery != "rate(errors_total[5m])" {
		t.Errorf("query forwarded to Prometheus = %q, want rate(errors_total[5m])", seenQuery)
	}
	if len(ev) != 2 {
		t.Fatalf("evidence count = %d, want 2 (one per returned series)", len(ev))
	}
	// Series 1: 0.01 → 0.12, 12x spike → high confidence (>=0.9 bucket)
	// Series 2: 0.01 → 0.012, 1.2x → low confidence (< 1.25 bucket)
	var highSeen, lowSeen bool
	for _, e := range ev {
		if e.Confidence >= 0.9 {
			highSeen = true
		}
		if e.Confidence < 0.5 {
			lowSeen = true
		}
	}
	if !highSeen || !lowSeen {
		t.Errorf("expected both a high-confidence and low-confidence evidence; got %+v", ev)
	}
}

func TestInvestigate_noBaseURLIsNoOp(t *testing.T) {
	inv := New("", "")
	ev, err := inv.Investigate(context.Background(),
		incident.Incident{Signal: incident.Signal{Raw: alertmanagerPayload("http://x?g0.expr=up")}})
	if err != nil {
		t.Fatalf("Investigate: %v", err)
	}
	if ev != nil {
		t.Errorf("want nil evidence when baseURL unset, got %+v", ev)
	}
}

func TestInvestigate_noGeneratorURLIsNoOp(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		t.Error("Prometheus should not be called when there are no queries")
	}))
	defer srv.Close()
	inv := New(srv.URL, "")
	ev, err := inv.Investigate(context.Background(),
		incident.Incident{Signal: incident.Signal{Raw: map[string]any{}}})
	if err != nil {
		t.Fatalf("Investigate: %v", err)
	}
	if ev != nil {
		t.Errorf("want nil evidence when no queries, got %+v", ev)
	}
}

func TestInvestigate_topSeriesKeptDrops_lowPeakOnes(t *testing.T) {
	// Build a response with 5 series: peaks 10, 8, 6, 4, 2. We keep top-3.
	peaks := []float64{10, 8, 6, 4, 2}
	var sb strings.Builder
	sb.WriteString(`{"status":"success","data":{"resultType":"matrix","result":[`)
	for i, p := range peaks {
		if i > 0 {
			sb.WriteString(",")
		}
		sb.WriteString(`{"metric":{"k":"`)
		sb.WriteString(string(rune('a' + i)))
		sb.WriteString(`"},"values":[[1000,"0.1"],[1010,"`)
		sb.WriteString(strconvFloat(p))
		sb.WriteString(`"]]}`)
	}
	sb.WriteString(`]}}`)

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Write([]byte(sb.String()))
	}))
	defer srv.Close()

	inv := New(srv.URL, "")
	raw := alertmanagerPayload("http://x?g0.expr=" + url.QueryEscape("up"))
	ev, err := inv.Investigate(context.Background(),
		incident.Incident{TriggeredAt: time.Unix(1010, 0), Window: 30 * time.Second,
			Signal: incident.Signal{Raw: raw}})
	if err != nil {
		t.Fatalf("Investigate: %v", err)
	}
	if len(ev) != maxSeriesPerQuery {
		t.Fatalf("got %d evidence, want %d (top-N cap)", len(ev), maxSeriesPerQuery)
	}
}

func TestExprFromGeneratorURL(t *testing.T) {
	for _, tc := range []struct {
		name, in, want string
		ok             bool
	}{
		{"standard", "http://p/graph?g0.expr=" + url.QueryEscape("rate(errors[5m])") + "&g0.tab=1", "rate(errors[5m])", true},
		{"missing expr", "http://p/graph?g0.tab=1", "", false},
		{"not a url", "::::::", "", false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got, ok := exprFromGeneratorURL(tc.in)
			if ok != tc.ok || got != tc.want {
				t.Errorf("got %q,%v want %q,%v", got, ok, tc.want, tc.ok)
			}
		})
	}
}

func TestQueryRangeResponseShape(t *testing.T) {
	// Sanity: confirm the decode path handles numeric timestamp + string value.
	var r queryRangeResponse
	body := `{"status":"success","data":{"resultType":"matrix","result":[{"metric":{},"values":[[123.0,"1.5"]]}]}}`
	if err := json.Unmarshal([]byte(body), &r); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if r.Status != "success" || len(r.Data.Result) != 1 {
		t.Fatalf("unexpected decode: %+v", r)
	}
}

func strconvFloat(f float64) string {
	// keep dep surface tiny
	return jsonNumber(f)
}

func jsonNumber(f float64) string {
	b, _ := json.Marshal(f)
	return string(b)
}
