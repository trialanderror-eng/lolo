package logs

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/trialanderror-eng/lolo/internal/incident"
)

// lokiResponseStreams renders a Loki matrix response with the given streams.
// Each stream is (labels map, entries as [ts_ns_string, line] pairs).
func lokiResponseStreams(streams []map[string]any) string {
	var sb strings.Builder
	sb.WriteString(`{"status":"success","data":{"resultType":"streams","result":[`)
	for i, s := range streams {
		if i > 0 {
			sb.WriteString(",")
		}
		fmt.Fprintf(&sb, `{"stream":{`)
		first := true
		for k, v := range s["labels"].(map[string]string) {
			if !first {
				sb.WriteString(",")
			}
			first = false
			fmt.Fprintf(&sb, `%q:%q`, k, v)
		}
		sb.WriteString(`},"values":[`)
		for j, v := range s["values"].([][2]string) {
			if j > 0 {
				sb.WriteString(",")
			}
			fmt.Fprintf(&sb, `[%q,%q]`, v[0], v[1])
		}
		sb.WriteString(`]}`)
	}
	sb.WriteString(`]}}`)
	return sb.String()
}

func TestInvestigate_emitsEvidencePerTopStream(t *testing.T) {
	var capturedQuery, capturedStart, capturedEnd string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/loki/api/v1/query_range" {
			t.Errorf("unexpected path %s", r.URL.Path)
		}
		capturedQuery = r.URL.Query().Get("query")
		capturedStart = r.URL.Query().Get("start")
		capturedEnd = r.URL.Query().Get("end")
		w.Write([]byte(lokiResponseStreams([]map[string]any{
			{
				"labels": map[string]string{"namespace": "prod", "pod": "api-hot"},
				"values": [][2]string{
					{"1776429000000000000", "ERROR connection refused to db"},
					{"1776429060000000000", "ERROR timeout"},
					{"1776429120000000000", "ERROR timeout"},
				},
			},
			{
				"labels": map[string]string{"namespace": "prod", "pod": "api-quiet"},
				"values": [][2]string{
					{"1776429030000000000", "WARN something odd errored out"},
				},
			},
		})))
	}))
	defer srv.Close()

	inv := New(srv.URL, "")
	inc := incident.Incident{
		ID:          "i-1",
		TriggeredAt: time.Unix(1776429300, 0).UTC(),
		Window:      5 * time.Minute,
		Scope:       incident.Scope{Namespaces: []string{"prod"}},
	}
	ev, err := inv.Investigate(context.Background(), inc)
	if err != nil {
		t.Fatalf("Investigate: %v", err)
	}

	if capturedQuery != `{namespace="prod"} |~ "(?i)error|exception|panic|fatal|failed"` {
		t.Errorf("query = %q, want namespace-filtered error regex", capturedQuery)
	}
	if capturedStart == "" || capturedEnd == "" {
		t.Errorf("start/end missing: %q %q", capturedStart, capturedEnd)
	}

	if len(ev) != 2 {
		t.Fatalf("got %d evidence, want 2 (one per stream)", len(ev))
	}
	// First emitted should be the higher-count stream
	if c, _ := ev[0].Data["count"].(int); c != 3 {
		t.Errorf("top evidence count = %v, want 3 (hot stream first)", ev[0].Data["count"])
	}
	if pod := ev[0].Data["pod"]; pod != "api-hot" {
		t.Errorf("top pod = %v, want api-hot", pod)
	}
	if ev[0].Kind != "error_pattern" {
		t.Errorf("Kind = %q, want error_pattern", ev[0].Kind)
	}
	if !strings.Contains(ev[0].Summary, "connection refused") {
		t.Errorf("Summary missing sample line: %q", ev[0].Summary)
	}
	if len(ev[0].Links) != 1 || !strings.Contains(ev[0].Links[0].URL, "/explore") {
		t.Errorf("missing explore Link: %+v", ev[0].Links)
	}
}

func TestInvestigate_topNCap(t *testing.T) {
	// Seed 5 streams; we keep top 3.
	streams := make([]map[string]any, 0, 5)
	for i := 5; i >= 1; i-- {
		values := make([][2]string, 0, i)
		for j := 0; j < i; j++ {
			values = append(values, [2]string{"1776429000000000000", "error"})
		}
		streams = append(streams, map[string]any{
			"labels": map[string]string{"namespace": "prod", "pod": fmt.Sprintf("pod-%d", i)},
			"values": values,
		})
	}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Write([]byte(lokiResponseStreams(streams)))
	}))
	defer srv.Close()

	inv := New(srv.URL, "")
	ev, err := inv.Investigate(context.Background(),
		incident.Incident{Scope: incident.Scope{Namespaces: []string{"prod"}}, Window: time.Minute, TriggeredAt: time.Now()})
	if err != nil {
		t.Fatalf("Investigate: %v", err)
	}
	if len(ev) != maxStreamsReported {
		t.Fatalf("got %d, want %d (top-N cap)", len(ev), maxStreamsReported)
	}
	for i, e := range ev {
		want := 5 - i
		if c, _ := e.Data["count"].(int); c != want {
			t.Errorf("ev[%d].count = %v, want %d (descending by count)", i, e.Data["count"], want)
		}
	}
}

func TestInvestigate_noBaseURLIsNoOp(t *testing.T) {
	inv := New("", "")
	ev, err := inv.Investigate(context.Background(),
		incident.Incident{Scope: incident.Scope{Namespaces: []string{"prod"}}})
	if err != nil || ev != nil {
		t.Errorf("got ev=%v err=%v, want nil,nil", ev, err)
	}
}

func TestInvestigate_noNamespacesIsNoOp(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		t.Error("Loki should not be called when no namespaces in scope")
	}))
	defer srv.Close()
	inv := New(srv.URL, "")
	ev, err := inv.Investigate(context.Background(), incident.Incident{})
	if err != nil || ev != nil {
		t.Errorf("got ev=%v err=%v, want nil,nil", ev, err)
	}
}

func TestInvestigate_emptyStreamsReturnsNoEvidence(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Write([]byte(`{"status":"success","data":{"resultType":"streams","result":[]}}`))
	}))
	defer srv.Close()
	inv := New(srv.URL, "")
	ev, err := inv.Investigate(context.Background(),
		incident.Incident{Scope: incident.Scope{Namespaces: []string{"prod"}}, Window: time.Minute, TriggeredAt: time.Now()})
	if err != nil {
		t.Fatalf("Investigate: %v", err)
	}
	if len(ev) != 0 {
		t.Errorf("got %d evidence, want 0 (empty streams)", len(ev))
	}
}

func TestInvestigate_forwardsBearerToken(t *testing.T) {
	var gotAuth string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotAuth = r.Header.Get("Authorization")
		w.Write([]byte(`{"status":"success","data":{"resultType":"streams","result":[]}}`))
	}))
	defer srv.Close()

	inv := New(srv.URL, "loki-tok")
	_, _ = inv.Investigate(context.Background(),
		incident.Incident{Scope: incident.Scope{Namespaces: []string{"prod"}}, Window: time.Minute, TriggeredAt: time.Now()})

	if gotAuth != "Bearer loki-tok" {
		t.Errorf("Authorization = %q, want Bearer loki-tok", gotAuth)
	}
}

func TestConfidenceForCount(t *testing.T) {
	cases := []struct {
		n    int
		want float64
	}{{1, 0.5}, {5, 0.5}, {6, 0.7}, {50, 0.7}, {51, 0.85}, {500, 0.85}}
	for _, c := range cases {
		if got := confidenceForCount(c.n); got != c.want {
			t.Errorf("confidenceForCount(%d) = %v, want %v", c.n, got, c.want)
		}
	}
}
