package manual

import (
	"strings"
	"testing"
	"time"
)

func TestParse_fullRequest(t *testing.T) {
	body := `{
		"summary": "Checkout latency elevated",
		"window": "2h",
		"scope": {"services": ["checkout"], "namespaces": ["prod"], "repos": ["acme/checkout"]}
	}`
	before := time.Now().UTC().Add(-time.Second)
	inc, err := Parse(strings.NewReader(body))
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	if inc.Signal.Source != "manual" {
		t.Errorf("Source = %q, want manual", inc.Signal.Source)
	}
	if inc.Signal.Summary != "Checkout latency elevated" {
		t.Errorf("Summary = %q", inc.Signal.Summary)
	}
	if inc.Window != 2*time.Hour {
		t.Errorf("Window = %v, want 2h", inc.Window)
	}
	if inc.TriggeredAt.Before(before) {
		t.Errorf("TriggeredAt = %v, want ~now", inc.TriggeredAt)
	}
	if !strings.HasPrefix(inc.ID, "manual-") {
		t.Errorf("ID = %q, want manual-<ts> prefix", inc.ID)
	}
	if len(inc.Scope.Services) != 1 || inc.Scope.Services[0] != "checkout" {
		t.Errorf("Scope.Services = %v", inc.Scope.Services)
	}
	if len(inc.Scope.Namespaces) != 1 || inc.Scope.Namespaces[0] != "prod" {
		t.Errorf("Scope.Namespaces = %v", inc.Scope.Namespaces)
	}
	if len(inc.Scope.Repos) != 1 || inc.Scope.Repos[0] != "acme/checkout" {
		t.Errorf("Scope.Repos = %v", inc.Scope.Repos)
	}
}

func TestParse_defaultsWhenFieldsMissing(t *testing.T) {
	body := `{"scope": {"namespaces": ["prod"]}}`
	inc, err := Parse(strings.NewReader(body))
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	if inc.Signal.Summary != "manual investigation" {
		t.Errorf("Summary fallback = %q", inc.Signal.Summary)
	}
	if inc.Window != DefaultWindow {
		t.Errorf("Window = %v, want default %v", inc.Window, DefaultWindow)
	}
}

func TestParse_rejectsBadWindow(t *testing.T) {
	_, err := Parse(strings.NewReader(`{"window": "not-a-duration"}`))
	if err == nil {
		t.Fatal("Parse: expected error for bad window")
	}
	if !strings.Contains(err.Error(), "window") {
		t.Errorf("err = %v, want message mentioning window", err)
	}
}

func TestParse_rejectsBadJSON(t *testing.T) {
	_, err := Parse(strings.NewReader(`{not-json`))
	if err == nil {
		t.Fatal("Parse: expected error for bad JSON")
	}
}
