// Package slack is a Sink that posts RCA reports to Slack via an
// Incoming Webhook URL.
package slack

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"time"

	"github.com/trialanderror-eng/lolo/internal/evidence"
	"github.com/trialanderror-eng/lolo/internal/hypothesis"
	"github.com/trialanderror-eng/lolo/internal/incident"
)

const (
	maxHypotheses = 5
	maxEvidence   = 3
)

type Sink struct {
	webhookURL string
	http       *http.Client
}

func New(webhookURL string) *Sink {
	return &Sink{
		webhookURL: webhookURL,
		http:       &http.Client{Timeout: 5 * time.Second},
	}
}

func (s *Sink) Emit(ctx context.Context, inc incident.Incident, hs []hypothesis.Hypothesis) error {
	if s.webhookURL == "" {
		return nil
	}
	payload := buildPayload(inc, hs)
	body, err := json.Marshal(payload)
	if err != nil {
		return err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, s.webhookURL, bytes.NewReader(body))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json")
	resp, err := s.http.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode/100 != 2 {
		return fmt.Errorf("slack webhook: %s", resp.Status)
	}
	return nil
}

// Payload matches the subset of Slack Block Kit we emit. Exported for tests.
type Payload struct {
	Text   string  `json:"text"` // fallback text for notifications
	Blocks []Block `json:"blocks"`
}

type Block struct {
	Type     string    `json:"type"`
	Text     *TextObj  `json:"text,omitempty"`
	Elements []TextObj `json:"elements,omitempty"`
}

type TextObj struct {
	Type string `json:"type"`
	Text string `json:"text"`
}

func buildPayload(inc incident.Incident, hs []hypothesis.Hypothesis) Payload {
	fallback := fmt.Sprintf("RCA for %s: %s", inc.ID, inc.Signal.Summary)

	blocks := []Block{
		{Type: "header", Text: &TextObj{Type: "plain_text", Text: "RCA report"}},
		{Type: "section", Text: &TextObj{Type: "mrkdwn", Text: incidentHeader(inc)}},
		{Type: "divider"},
	}

	if len(hs) == 0 {
		blocks = append(blocks, Block{
			Type: "section",
			Text: &TextObj{Type: "mrkdwn", Text: "_No hypotheses produced — all investigators returned empty._"},
		})
		return Payload{Text: fallback, Blocks: blocks}
	}

	n := len(hs)
	if n > maxHypotheses {
		n = maxHypotheses
	}
	for i, h := range hs[:n] {
		blocks = append(blocks,
			Block{Type: "section", Text: &TextObj{Type: "mrkdwn", Text: hypothesisBody(i+1, h)}},
		)
		if links := evidenceContext(h.Evidence); links != "" {
			blocks = append(blocks, Block{
				Type:     "context",
				Elements: []TextObj{{Type: "mrkdwn", Text: links}},
			})
		}
	}
	if len(hs) > maxHypotheses {
		blocks = append(blocks, Block{
			Type: "context",
			Elements: []TextObj{{
				Type: "mrkdwn",
				Text: fmt.Sprintf("_%d more hypothesis%s omitted_", len(hs)-maxHypotheses, plural(len(hs)-maxHypotheses)),
			}},
		})
	}
	return Payload{Text: fallback, Blocks: blocks}
}

func incidentHeader(inc incident.Incident) string {
	var b strings.Builder
	fmt.Fprintf(&b, "*Incident:* `%s`\n", inc.ID)
	fmt.Fprintf(&b, "*Summary:* %s\n", inc.Signal.Summary)
	fmt.Fprintf(&b, "*Triggered:* <!date^%d^{date_short_pretty} {time}|%s> via `%s`",
		inc.TriggeredAt.Unix(), inc.TriggeredAt.UTC().Format(time.RFC3339), inc.Signal.Source)
	return b.String()
}

func hypothesisBody(rank int, h hypothesis.Hypothesis) string {
	var b strings.Builder
	fmt.Fprintf(&b, "*#%d — %s* _(score: %.2f)_", rank, h.Summary, h.Score)
	if h.Reasoning != "" {
		fmt.Fprintf(&b, "\n%s", h.Reasoning)
	}
	if len(h.Remediation) > 0 {
		fmt.Fprint(&b, "\n*Suggested:*")
		for _, r := range h.Remediation {
			fmt.Fprintf(&b, "\n• %s", r)
		}
	}
	return b.String()
}

func evidenceContext(ev []evidence.Evidence) string {
	if len(ev) == 0 {
		return ""
	}
	n := len(ev)
	if n > maxEvidence {
		n = maxEvidence
	}
	parts := make([]string, 0, n)
	for _, e := range ev[:n] {
		summary := e.Summary
		if len(e.Links) > 0 {
			summary = fmt.Sprintf("<%s|%s>", e.Links[0].URL, summary)
		}
		parts = append(parts, fmt.Sprintf("`%s`: %s", e.Source, summary))
	}
	if len(ev) > maxEvidence {
		parts = append(parts, fmt.Sprintf("_+%d more_", len(ev)-maxEvidence))
	}
	return strings.Join(parts, " · ")
}

func plural(n int) string {
	if n == 1 {
		return ""
	}
	return "es"
}
