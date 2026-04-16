// Package alertmanager parses Alertmanager webhook payloads into Incidents.
// Payload schema: https://prometheus.io/docs/alerting/latest/configuration/#webhook_config
package alertmanager

import (
	"encoding/json"
	"fmt"
	"io"
	"time"

	"github.com/trialanderror-eng/lolo/internal/incident"
)

type Payload struct {
	Version           string            `json:"version"`
	GroupKey          string            `json:"groupKey"`
	Status            string            `json:"status"`
	Receiver          string            `json:"receiver"`
	GroupLabels       map[string]string `json:"groupLabels"`
	CommonLabels      map[string]string `json:"commonLabels"`
	CommonAnnotations map[string]string `json:"commonAnnotations"`
	ExternalURL       string            `json:"externalURL"`
	Alerts            []Alert           `json:"alerts"`
}

type Alert struct {
	Status       string            `json:"status"`
	Labels       map[string]string `json:"labels"`
	Annotations  map[string]string `json:"annotations"`
	StartsAt     time.Time         `json:"startsAt"`
	EndsAt       time.Time         `json:"endsAt"`
	GeneratorURL string            `json:"generatorURL"`
	Fingerprint  string            `json:"fingerprint"`
}

const DefaultWindow = 30 * time.Minute

// Parse reads an Alertmanager webhook body and builds an Incident.
func Parse(r io.Reader) (incident.Incident, error) {
	var p Payload
	if err := json.NewDecoder(r).Decode(&p); err != nil {
		return incident.Incident{}, fmt.Errorf("decode alertmanager payload: %w", err)
	}
	return FromPayload(p), nil
}

// FromPayload maps a decoded Payload into an Incident.
func FromPayload(p Payload) incident.Incident {
	triggered := earliestStart(p.Alerts)
	summary := p.CommonAnnotations["summary"]
	if summary == "" {
		summary = p.CommonLabels["alertname"]
	}
	if summary == "" {
		summary = "alertmanager webhook"
	}

	raw := map[string]any{}
	b, _ := json.Marshal(p)
	_ = json.Unmarshal(b, &raw)

	return incident.Incident{
		ID:          incidentID(p, triggered),
		TriggeredAt: triggered,
		Window:      DefaultWindow,
		Scope:       scopeFromLabels(p.CommonLabels),
		Signal: incident.Signal{
			Source:  "alertmanager",
			Summary: summary,
			Raw:     raw,
		},
	}
}

func earliestStart(alerts []Alert) time.Time {
	t := time.Time{}
	for _, a := range alerts {
		if a.StartsAt.IsZero() {
			continue
		}
		if t.IsZero() || a.StartsAt.Before(t) {
			t = a.StartsAt
		}
	}
	if t.IsZero() {
		return time.Now().UTC()
	}
	return t
}

func incidentID(p Payload, t time.Time) string {
	name := p.CommonLabels["alertname"]
	if name == "" {
		name = "alert"
	}
	return fmt.Sprintf("%s-%d", name, t.Unix())
}

func scopeFromLabels(l map[string]string) incident.Scope {
	s := incident.Scope{}
	if v := l["service"]; v != "" {
		s.Services = []string{v}
	}
	if v := l["namespace"]; v != "" {
		s.Namespaces = []string{v}
	}
	if v := l["cluster"]; v != "" {
		s.Clusters = []string{v}
	}
	return s
}
