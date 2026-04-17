// Package logs is an Investigator that searches a logging backend for
// error-like lines inside the incident window. v0.4.0 MVP supports Loki
// via its query_range API; Elasticsearch can drop in behind the same
// Searcher contract later.
package logs

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"net/url"
	"sort"
	"strconv"
	"time"

	"github.com/trialanderror-eng/lolo/internal/evidence"
	"github.com/trialanderror-eng/lolo/internal/incident"
)

const (
	defaultErrorPattern = `(?i)error|exception|panic|fatal|failed`
	maxStreamsReported  = 3
	queryLimit          = 500
)

type Investigator struct {
	baseURL string
	token   string
	http    *http.Client
}

// New constructs a Loki-backed logs investigator. baseURL is the Loki API
// root (e.g., http://loki.monitoring.svc:3100). The host is intentionally
// NOT taken from the alert payload — same SSRF reasoning as prometheus.
func New(baseURL, token string) *Investigator {
	return &Investigator{
		baseURL: baseURL,
		token:   token,
		http:    &http.Client{Timeout: 15 * time.Second},
	}
}

func (*Investigator) Name() string { return "logs" }

func (i *Investigator) Investigate(ctx context.Context, inc incident.Incident) ([]evidence.Evidence, error) {
	if i.baseURL == "" {
		log.Printf("logs: skipping — no LOLO_LOKI_URL configured")
		return nil, nil
	}
	if len(inc.Scope.Namespaces) == 0 {
		log.Printf("logs: no namespaces in scope; skipping")
		return nil, nil
	}

	var out []evidence.Evidence
	for _, ns := range inc.Scope.Namespaces {
		ev, err := i.investigateNamespace(ctx, inc, ns)
		if err != nil {
			log.Printf("logs: %s: %v", ns, err)
			continue
		}
		out = append(out, ev...)
	}
	return out, nil
}

type stream struct {
	labels  map[string]string
	entries []logEntry
}

type logEntry struct {
	t    time.Time
	line string
}

type lokiResponse struct {
	Status string `json:"status"`
	Data   struct {
		ResultType string `json:"resultType"`
		Result     []struct {
			Stream map[string]string `json:"stream"`
			Values [][2]string       `json:"values"`
		} `json:"result"`
	} `json:"data"`
}

func (i *Investigator) investigateNamespace(ctx context.Context, inc incident.Incident, ns string) ([]evidence.Evidence, error) {
	query := fmt.Sprintf(`{namespace=%q} |~ %q`, ns, defaultErrorPattern)
	streams, err := i.queryRange(ctx, query, inc.Start(), inc.End())
	if err != nil {
		return nil, err
	}
	return evidenceFromStreams(i.baseURL, query, ns, streams, inc), nil
}

func (i *Investigator) queryRange(ctx context.Context, query string, start, end time.Time) ([]stream, error) {
	params := url.Values{}
	params.Set("query", query)
	params.Set("start", strconv.FormatInt(start.UnixNano(), 10))
	params.Set("end", strconv.FormatInt(end.UnixNano(), 10))
	params.Set("limit", strconv.Itoa(queryLimit))
	params.Set("direction", "backward")

	endpoint := i.baseURL + "/loki/api/v1/query_range?" + params.Encode()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, endpoint, nil)
	if err != nil {
		return nil, err
	}
	if i.token != "" {
		req.Header.Set("Authorization", "Bearer "+i.token)
	}
	resp, err := i.http.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode/100 != 2 {
		return nil, fmt.Errorf("loki %s: %s", endpoint, resp.Status)
	}
	var body lokiResponse
	if err := json.NewDecoder(resp.Body).Decode(&body); err != nil {
		return nil, err
	}
	if body.Status != "success" {
		return nil, fmt.Errorf("loki status=%s", body.Status)
	}

	out := make([]stream, 0, len(body.Data.Result))
	for _, r := range body.Data.Result {
		s := stream{labels: r.Stream}
		for _, v := range r.Values {
			ts, err := strconv.ParseInt(v[0], 10, 64)
			if err != nil {
				continue
			}
			s.entries = append(s.entries, logEntry{t: time.Unix(0, ts), line: v[1]})
		}
		if len(s.entries) > 0 {
			out = append(out, s)
		}
	}
	return out, nil
}

func evidenceFromStreams(baseURL, query, ns string, streams []stream, inc incident.Incident) []evidence.Evidence {
	// Highest-count streams first — those are the strongest signals of
	// "something's broken here" rather than background noise.
	sort.SliceStable(streams, func(i, j int) bool {
		return len(streams[i].entries) > len(streams[j].entries)
	})
	if len(streams) > maxStreamsReported {
		streams = streams[:maxStreamsReported]
	}

	out := make([]evidence.Evidence, 0, len(streams))
	for _, s := range streams {
		count := len(s.entries)
		sample := s.entries[0].line // direction=backward → [0] is most recent
		if len(sample) > 200 {
			sample = sample[:200] + "..."
		}
		pod := s.labels["pod"]
		if pod == "" {
			pod = "?"
		}

		out = append(out, evidence.Evidence{
			Source:     "logs",
			Kind:       "error_pattern",
			At:         s.entries[0].t,
			Confidence: confidenceForCount(count),
			Summary:    fmt.Sprintf("%s/%s: %d error-like log lines — %s", ns, pod, count, sample),
			Data: map[string]any{
				"namespace": ns,
				"pod":       pod,
				"count":     count,
				"sample":    sample,
				"labels":    s.labels,
			},
			Links: []evidence.Link{{
				Label: "logs",
				URL:   fmt.Sprintf("%s/explore?left=%%7B%%22queries%%22:%%5B%%7B%%22expr%%22:%q%%7D%%5D%%7D", baseURL, query),
			}},
		})
	}
	return out
}

// confidenceForCount buckets raw match count into a confidence band.
// A handful of errors might be noise; dozens are a strong signal.
func confidenceForCount(n int) float64 {
	switch {
	case n >= 51:
		return 0.85
	case n >= 6:
		return 0.7
	default:
		return 0.5
	}
}
