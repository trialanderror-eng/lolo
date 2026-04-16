// Package prometheus is an Investigator that pulls metric trajectories
// for queries referenced by the firing alert (via Alertmanager's
// generatorURL) and reports the direction and magnitude of change.
package prometheus

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"math"
	"net/http"
	"net/url"
	"sort"
	"strconv"
	"time"

	"github.com/trialanderror-eng/lolo/internal/evidence"
	"github.com/trialanderror-eng/lolo/internal/incident"
)

const maxSeriesPerQuery = 3

type Investigator struct {
	baseURL string
	token   string
	http    *http.Client
}

// New constructs the investigator. baseURL is a Prometheus API URL
// (e.g., http://prometheus.monitoring.svc:9090). The host is NOT taken
// from the alert payload — doing so would be an SSRF vector since the
// payload is attacker-controllable.
func New(baseURL, token string) *Investigator {
	return &Investigator{
		baseURL: baseURL,
		token:   token,
		http:    &http.Client{Timeout: 10 * time.Second},
	}
}

func (*Investigator) Name() string { return "prometheus" }

func (i *Investigator) Investigate(ctx context.Context, inc incident.Incident) ([]evidence.Evidence, error) {
	if i.baseURL == "" {
		log.Printf("prometheus: skipping — no base URL configured (LOLO_PROMETHEUS_URL)")
		return nil, nil
	}
	queries := extractQueries(inc.Signal.Raw)
	if len(queries) == 0 {
		return nil, nil
	}

	var out []evidence.Evidence
	for _, q := range queries {
		series, err := i.queryRange(ctx, q, inc.Start(), inc.End())
		if err != nil {
			log.Printf("prometheus: query %q: %v", q, err)
			continue
		}
		out = append(out, evidenceFromSeries(q, series, inc)...)
	}
	return out, nil
}

// extractQueries walks the Alertmanager payload's alerts[].generatorURL
// fields and returns the PromQL expressions, de-duplicated.
func extractQueries(raw map[string]any) []string {
	seen := map[string]bool{}
	var out []string
	alerts, _ := raw["alerts"].([]any)
	for _, a := range alerts {
		m, ok := a.(map[string]any)
		if !ok {
			continue
		}
		urlStr, _ := m["generatorURL"].(string)
		if urlStr == "" {
			continue
		}
		expr, ok := exprFromGeneratorURL(urlStr)
		if !ok || seen[expr] {
			continue
		}
		seen[expr] = true
		out = append(out, expr)
	}
	return out
}

func exprFromGeneratorURL(s string) (string, bool) {
	u, err := url.Parse(s)
	if err != nil {
		return "", false
	}
	// Prometheus UI formats: ?g0.expr=<urlencoded promql>
	expr := u.Query().Get("g0.expr")
	if expr == "" {
		return "", false
	}
	return expr, true
}

type sample struct {
	t time.Time
	v float64
}

type series struct {
	labels  map[string]string
	samples []sample
}

type queryRangeResponse struct {
	Status string `json:"status"`
	Data   struct {
		ResultType string `json:"resultType"`
		Result     []struct {
			Metric map[string]string `json:"metric"`
			Values [][]any           `json:"values"`
		} `json:"result"`
	} `json:"data"`
}

func (i *Investigator) queryRange(ctx context.Context, query string, start, end time.Time) ([]series, error) {
	step := stepFor(start, end)
	endpoint := fmt.Sprintf("%s/api/v1/query_range?query=%s&start=%d&end=%d&step=%s",
		i.baseURL, url.QueryEscape(query), start.Unix(), end.Unix(), step)

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
		return nil, fmt.Errorf("prometheus %s: %s", endpoint, resp.Status)
	}
	var body queryRangeResponse
	if err := json.NewDecoder(resp.Body).Decode(&body); err != nil {
		return nil, err
	}
	if body.Status != "success" {
		return nil, fmt.Errorf("prometheus status=%s", body.Status)
	}
	out := make([]series, 0, len(body.Data.Result))
	for _, r := range body.Data.Result {
		s := series{labels: r.Metric}
		for _, v := range r.Values {
			if len(v) != 2 {
				continue
			}
			ts, ok1 := v[0].(float64)
			val, ok2 := v[1].(string)
			if !ok1 || !ok2 {
				continue
			}
			f, err := strconv.ParseFloat(val, 64)
			if err != nil || math.IsNaN(f) {
				continue
			}
			s.samples = append(s.samples, sample{t: time.Unix(int64(ts), 0), v: f})
		}
		if len(s.samples) > 0 {
			out = append(out, s)
		}
	}
	return out, nil
}

func stepFor(start, end time.Time) string {
	span := end.Sub(start)
	switch {
	case span <= 15*time.Minute:
		return "15s"
	case span <= 1*time.Hour:
		return "30s"
	default:
		return "1m"
	}
}

func evidenceFromSeries(query string, ss []series, inc incident.Incident) []evidence.Evidence {
	// Keep the top-N by peak value — noisy queries returning many series
	// would otherwise drown the report.
	sort.SliceStable(ss, func(i, j int) bool { return peak(ss[i]) > peak(ss[j]) })
	if len(ss) > maxSeriesPerQuery {
		ss = ss[:maxSeriesPerQuery]
	}
	out := make([]evidence.Evidence, 0, len(ss))
	for _, s := range ss {
		first := s.samples[0].v
		last := s.samples[len(s.samples)-1].v
		p := peak(s)
		conf := spikeConfidence(first, p)

		labels := labelString(s.labels)
		summary := fmt.Sprintf("%s: %s first=%.4g last=%.4g peak=%.4g", query, labels, first, last, p)

		out = append(out, evidence.Evidence{
			Source:     "prometheus",
			Kind:       "metric_series",
			At:         s.samples[len(s.samples)-1].t,
			Confidence: conf,
			Summary:    summary,
			Data: map[string]any{
				"query":  query,
				"labels": s.labels,
				"first":  first,
				"last":   last,
				"peak":   p,
				"delta":  last - first,
				"points": len(s.samples),
				"window": inc.Window.String(),
			},
		})
	}
	return out
}

func peak(s series) float64 {
	var p float64
	for i, x := range s.samples {
		if i == 0 || x.v > p {
			p = x.v
		}
	}
	return p
}

// spikeConfidence rewards large multiplicative jumps. Flat series get a
// low floor so they don't wash out real findings.
func spikeConfidence(first, peak float64) float64 {
	if peak == 0 {
		return 0.3
	}
	ratio := peak
	if first > 0 {
		ratio = peak / first
	} else if first == 0 && peak > 0 {
		ratio = math.Inf(1)
	}
	switch {
	case math.IsInf(ratio, 1) || ratio >= 5:
		return 0.9
	case ratio >= 2:
		return 0.75
	case ratio >= 1.25:
		return 0.55
	default:
		return 0.35
	}
}

func labelString(l map[string]string) string {
	if len(l) == 0 {
		return "{}"
	}
	keys := make([]string, 0, len(l))
	for k := range l {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	parts := make([]string, 0, len(keys))
	for _, k := range keys {
		parts = append(parts, fmt.Sprintf("%s=%q", k, l[k]))
	}
	return "{" + joinWith(parts, ", ") + "}"
}

func joinWith(ss []string, sep string) string {
	switch len(ss) {
	case 0:
		return ""
	case 1:
		return ss[0]
	}
	n := len(sep) * (len(ss) - 1)
	for _, s := range ss {
		n += len(s)
	}
	b := make([]byte, 0, n)
	b = append(b, ss[0]...)
	for _, s := range ss[1:] {
		b = append(b, sep...)
		b = append(b, s...)
	}
	return string(b)
}
