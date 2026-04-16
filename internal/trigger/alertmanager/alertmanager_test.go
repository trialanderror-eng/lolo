package alertmanager

import (
	"strings"
	"testing"
	"time"
)

func TestParse_fullPayload(t *testing.T) {
	body := `{
		"version": "4",
		"groupKey": "{}:{alertname=\"HighErrorRate\"}",
		"status": "firing",
		"commonLabels": {"alertname": "HighErrorRate", "service": "payments", "namespace": "prod", "cluster": "us-central1"},
		"commonAnnotations": {"summary": "Error rate exceeded threshold"},
		"alerts": [
			{"status": "firing", "startsAt": "2026-04-16T17:00:00Z", "labels": {"severity": "critical"}},
			{"status": "firing", "startsAt": "2026-04-16T17:02:00Z", "labels": {"severity": "critical"}}
		]
	}`
	inc, err := Parse(strings.NewReader(body))
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	want := time.Date(2026, 4, 16, 17, 0, 0, 0, time.UTC)
	if !inc.TriggeredAt.Equal(want) {
		t.Errorf("TriggeredAt = %v, want %v (earliest of the two alerts)", inc.TriggeredAt, want)
	}
	if inc.Signal.Summary != "Error rate exceeded threshold" {
		t.Errorf("Summary = %q, want annotation summary", inc.Signal.Summary)
	}
	if len(inc.Scope.Services) != 1 || inc.Scope.Services[0] != "payments" {
		t.Errorf("Scope.Services = %v, want [payments]", inc.Scope.Services)
	}
	if len(inc.Scope.Namespaces) != 1 || inc.Scope.Namespaces[0] != "prod" {
		t.Errorf("Scope.Namespaces = %v, want [prod]", inc.Scope.Namespaces)
	}
	if !strings.HasPrefix(inc.ID, "HighErrorRate-") {
		t.Errorf("ID = %q, want HighErrorRate-<ts> prefix", inc.ID)
	}
}

func TestParse_fallsBackToNowWhenNoStartsAt(t *testing.T) {
	body := `{"commonLabels": {"alertname": "NoTimestamp"}, "alerts": [{"status": "firing"}]}`
	before := time.Now().UTC().Add(-time.Second)
	inc, err := Parse(strings.NewReader(body))
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	if inc.TriggeredAt.Before(before) {
		t.Errorf("TriggeredAt should fall back to ~now, got %v", inc.TriggeredAt)
	}
}

func TestParse_missingSummaryUsesAlertname(t *testing.T) {
	body := `{"commonLabels": {"alertname": "CPUHot"}, "alerts": [{"startsAt": "2026-04-16T17:00:00Z"}]}`
	inc, err := Parse(strings.NewReader(body))
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	if inc.Signal.Summary != "CPUHot" {
		t.Errorf("Summary = %q, want alertname fallback CPUHot", inc.Signal.Summary)
	}
}
