// Package manual parses ad-hoc /investigate requests. Lets an SRE run
// an investigation on demand without waiting for an Alertmanager page.
package manual

import (
	"encoding/json"
	"fmt"
	"io"
	"time"

	"github.com/trialanderror-eng/lolo/internal/incident"
)

const DefaultWindow = 30 * time.Minute

// Request is the JSON body accepted on POST /investigate.
type Request struct {
	Summary string `json:"summary"`
	Window  string `json:"window"` // Go duration string (e.g., "2h", "30m"); default 30m
	Scope   struct {
		Services   []string `json:"services"`
		Namespaces []string `json:"namespaces"`
		Clusters   []string `json:"clusters"`
		Repos      []string `json:"repos"`
	} `json:"scope"`
}

func Parse(r io.Reader) (incident.Incident, error) {
	var req Request
	if err := json.NewDecoder(r).Decode(&req); err != nil {
		return incident.Incident{}, fmt.Errorf("decode: %w", err)
	}
	summary := req.Summary
	if summary == "" {
		summary = "manual investigation"
	}
	window := DefaultWindow
	if req.Window != "" {
		d, err := time.ParseDuration(req.Window)
		if err != nil {
			return incident.Incident{}, fmt.Errorf("window %q: %w", req.Window, err)
		}
		window = d
	}
	now := time.Now().UTC()

	raw := map[string]any{}
	if b, err := json.Marshal(req); err == nil {
		_ = json.Unmarshal(b, &raw)
	}

	return incident.Incident{
		ID:          fmt.Sprintf("manual-%d", now.Unix()),
		TriggeredAt: now,
		Window:      window,
		Scope: incident.Scope{
			Services:   req.Scope.Services,
			Namespaces: req.Scope.Namespaces,
			Clusters:   req.Scope.Clusters,
			Repos:      req.Scope.Repos,
		},
		Signal: incident.Signal{
			Source:  "manual",
			Summary: summary,
			Raw:     raw,
		},
	}, nil
}
