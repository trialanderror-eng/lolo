package hypothesis

import (
	"context"
	"fmt"
	"sort"
	"strings"

	"github.com/trialanderror-eng/lolo/internal/evidence"
	"github.com/trialanderror-eng/lolo/internal/incident"
)

// CorrelatingRanker groups evidence by inferred scope (service, namespace,
// repo, pod) and boosts the confidence of buckets that contain evidence from
// multiple investigators. This is the v1 "real" ranker — it surfaces the
// classic SRE correlation: a deploy + a k8s crashloop + a metric spike all
// in the same service is a single high-confidence hypothesis, not three
// disconnected rows.
//
// Evidence with no extractable scope is reported as one hypothesis per item
// at its original confidence — those signals are still useful, just not
// correlated.
type CorrelatingRanker struct {
	TopN int // 0 means no cap
}

func (r CorrelatingRanker) Rank(_ context.Context, _ incident.Incident, ev []evidence.Evidence) ([]Hypothesis, error) {
	if len(ev) == 0 {
		return nil, nil
	}

	groups := map[string][]evidence.Evidence{}
	var orphans []evidence.Evidence
	for _, e := range ev {
		key := bucketKey(e)
		if key == "" {
			orphans = append(orphans, e)
			continue
		}
		groups[key] = append(groups[key], e)
	}

	out := make([]Hypothesis, 0, len(groups)+len(orphans))
	for key, group := range groups {
		out = append(out, hypothesisForGroup(key, group))
	}
	for _, e := range orphans {
		out = append(out, hypothesisForOrphan(e))
	}

	sort.SliceStable(out, func(i, j int) bool { return out[i].Score > out[j].Score })
	if r.TopN > 0 && len(out) > r.TopN {
		out = out[:r.TopN]
	}
	return out, nil
}

// bucketKey extracts a single scope hint from evidence.Data with priority
// service > namespace > repo > pod. Returns "" for evidence we can't place
// (those become per-item orphan hypotheses).
func bucketKey(e evidence.Evidence) string {
	if e.Data == nil {
		return ""
	}
	for _, k := range []string{"service", "namespace", "repo", "pod"} {
		if v := stringFromMap(e.Data, k); v != "" {
			return k + ":" + v
		}
		// Also check nested labels (prometheus puts service/namespace there).
		if labels, ok := e.Data["labels"]; ok {
			if v := stringFromMap(labels, k); v != "" {
				return k + ":" + v
			}
		}
	}
	return ""
}

// stringFromMap accepts either map[string]string or map[string]any so the
// ranker stays correct whether Data was built in-process or round-tripped
// through JSON.
func stringFromMap(m any, key string) string {
	switch m := m.(type) {
	case map[string]string:
		return m[key]
	case map[string]any:
		if v, ok := m[key].(string); ok {
			return v
		}
	}
	return ""
}

func hypothesisForGroup(key string, ev []evidence.Evidence) Hypothesis {
	sources := uniqueSources(ev)
	maxConf := 0.0
	for _, e := range ev {
		if e.Confidence > maxConf {
			maxConf = e.Confidence
		}
	}

	boost := 1.0
	switch {
	case len(sources) >= 3:
		boost = 1.4
	case len(sources) == 2:
		boost = 1.2
	}
	score := maxConf * boost
	if score > 1.0 {
		score = 1.0
	}

	sort.SliceStable(ev, func(i, j int) bool { return ev[i].At.Before(ev[j].At) })

	return Hypothesis{
		Summary:   fmt.Sprintf("%s — %d signal(s) from %s", prettyKey(key), len(ev), strings.Join(sources, ", ")),
		Score:     score,
		Evidence:  ev,
		Reasoning: reasoningFor(key, sources, len(ev)),
	}
}

func hypothesisForOrphan(e evidence.Evidence) Hypothesis {
	return Hypothesis{
		Summary:   e.Summary,
		Score:     e.Confidence,
		Evidence:  []evidence.Evidence{e},
		Reasoning: fmt.Sprintf("uncorrelated signal from %s", e.Source),
	}
}

func uniqueSources(ev []evidence.Evidence) []string {
	seen := map[string]bool{}
	var out []string
	for _, e := range ev {
		if !seen[e.Source] {
			seen[e.Source] = true
			out = append(out, e.Source)
		}
	}
	sort.Strings(out)
	return out
}

func prettyKey(k string) string {
	parts := strings.SplitN(k, ":", 2)
	if len(parts) != 2 {
		return k
	}
	return fmt.Sprintf("%s `%s`", parts[0], parts[1])
}

func reasoningFor(key string, sources []string, count int) string {
	if len(sources) >= 2 {
		return fmt.Sprintf(
			"%d evidence items from %d different sources (%s) all reference %s — strong cross-source correlation.",
			count, len(sources), strings.Join(sources, ", "), prettyKey(key),
		)
	}
	return fmt.Sprintf("%d evidence items from %s, all referencing %s.", count, sources[0], prettyKey(key))
}
