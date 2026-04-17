package openai

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

func TestNarrate_happyPath(t *testing.T) {
	var gotBody chatRequest
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v1/chat/completions" {
			t.Errorf("path = %q", r.URL.Path)
		}
		if got := r.Header.Get("Authorization"); got != "Bearer tok" {
			t.Errorf("Auth = %q", got)
		}
		b, _ := io.ReadAll(r.Body)
		if err := json.Unmarshal(b, &gotBody); err != nil {
			t.Errorf("decode req: %v", err)
		}
		w.Header().Set("Content-Type", "application/json")
		w.Write([]byte(`{"choices":[{"message":{"role":"assistant","content":"  The pod failed to pull its image.  "}}]}`))
	}))
	defer srv.Close()

	c := New(srv.URL, "llama3.1:8b", "tok")
	got, err := c.Narrate(context.Background(), "sys", "user")
	if err != nil {
		t.Fatalf("Narrate: %v", err)
	}
	if got != "The pod failed to pull its image." {
		t.Errorf("got %q, want trimmed content", got)
	}
	if gotBody.Model != "llama3.1:8b" {
		t.Errorf("model = %q", gotBody.Model)
	}
	if len(gotBody.Messages) != 2 || gotBody.Messages[0].Role != "system" || gotBody.Messages[1].Role != "user" {
		t.Errorf("messages = %+v", gotBody.Messages)
	}
	if gotBody.MaxTokens != defaultMaxTokens {
		t.Errorf("max_tokens = %d, want %d", gotBody.MaxTokens, defaultMaxTokens)
	}
	if gotBody.Stream {
		t.Errorf("stream should be false")
	}
}

func TestNarrate_noAuthHeaderWhenAPIKeyEmpty(t *testing.T) {
	var authSeen string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		authSeen = r.Header.Get("Authorization")
		w.Write([]byte(`{"choices":[{"message":{"content":"ok"}}]}`))
	}))
	defer srv.Close()

	_, _ = New(srv.URL, "m", "").Narrate(context.Background(), "s", "u")
	if authSeen != "" {
		t.Errorf("Authorization = %q, want empty when apiKey is unset", authSeen)
	}
}

func TestNarrate_nonSuccessStatus(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
		w.Write([]byte(`{"error":{"message":"model not loaded"}}`))
	}))
	defer srv.Close()

	_, err := New(srv.URL, "m", "").Narrate(context.Background(), "s", "u")
	if err == nil {
		t.Fatal("expected error")
	}
	if !strings.Contains(err.Error(), "model not loaded") {
		t.Errorf("err = %v, want it to include server error message", err)
	}
}

func TestNarrate_noChoices(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Write([]byte(`{"choices":[]}`))
	}))
	defer srv.Close()

	_, err := New(srv.URL, "m", "").Narrate(context.Background(), "s", "u")
	if err == nil || !strings.Contains(err.Error(), "no choices") {
		t.Errorf("err = %v, want 'no choices' error", err)
	}
}

func TestNarrate_honorsContextCancel(t *testing.T) {
	// Handler is slow; context times out first. Handler returns a little
	// later so httptest.Server.Close() doesn't wait forever for drain.
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		time.Sleep(500 * time.Millisecond)
		w.Write([]byte(`{"choices":[{"message":{"content":"too late"}}]}`))
	}))
	defer srv.Close()

	ctx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
	defer cancel()
	start := time.Now()
	_, err := New(srv.URL, "m", "").Narrate(ctx, "s", "u")
	if err == nil {
		t.Fatal("expected context deadline error")
	}
	if dur := time.Since(start); dur > 400*time.Millisecond {
		t.Errorf("Narrate took %v; context should have cancelled ~50ms", dur)
	}
}
