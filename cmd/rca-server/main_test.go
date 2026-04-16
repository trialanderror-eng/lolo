package main

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func newMux() *http.ServeMux {
	mux := http.NewServeMux()
	mux.HandleFunc("/healthz", func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(http.StatusOK) })
	mux.HandleFunc("/webhook/alertmanager", func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusAccepted)
	})
	return mux
}

func TestAuthMiddleware_healthAlwaysOpen(t *testing.T) {
	for _, token := range []string{"", "secret"} {
		h := authMiddleware(token, newMux())
		req := httptest.NewRequest(http.MethodGet, "/healthz", nil)
		w := httptest.NewRecorder()
		h.ServeHTTP(w, req)
		if w.Code != http.StatusOK {
			t.Errorf("token=%q: /healthz = %d, want 200", token, w.Code)
		}
	}
}

func TestAuthMiddleware_emptyTokenAllowsWebhook(t *testing.T) {
	h := authMiddleware("", newMux())
	req := httptest.NewRequest(http.MethodPost, "/webhook/alertmanager", strings.NewReader(`{}`))
	w := httptest.NewRecorder()
	h.ServeHTTP(w, req)
	if w.Code != http.StatusAccepted {
		t.Errorf("dev mode: /webhook = %d, want 202 (handler accepts)", w.Code)
	}
}

func TestAuthMiddleware_correctBearerAccepts(t *testing.T) {
	h := authMiddleware("s3cret", newMux())
	req := httptest.NewRequest(http.MethodPost, "/webhook/alertmanager", strings.NewReader(`{}`))
	req.Header.Set("Authorization", "Bearer s3cret")
	w := httptest.NewRecorder()
	h.ServeHTTP(w, req)
	if w.Code != http.StatusAccepted {
		t.Errorf("good token: /webhook = %d, want 202", w.Code)
	}
}

func TestAuthMiddleware_rejectsBadAndMissingTokens(t *testing.T) {
	cases := []struct {
		name, header string
	}{
		{"missing header", ""},
		{"wrong scheme", "Basic s3cret"},
		{"wrong token", "Bearer wrong"},
		{"empty bearer", "Bearer "},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			h := authMiddleware("s3cret", newMux())
			req := httptest.NewRequest(http.MethodPost, "/webhook/alertmanager", strings.NewReader(`{}`))
			if c.header != "" {
				req.Header.Set("Authorization", c.header)
			}
			w := httptest.NewRecorder()
			h.ServeHTTP(w, req)
			if w.Code != http.StatusUnauthorized {
				t.Errorf("got %d, want 401", w.Code)
			}
			if got := w.Header().Get("WWW-Authenticate"); !strings.Contains(got, "Bearer") {
				t.Errorf("WWW-Authenticate = %q, want Bearer challenge", got)
			}
		})
	}
}

func TestSplitCSV(t *testing.T) {
	cases := []struct {
		in   string
		want []string
	}{
		{"", nil},
		{"a", []string{"a"}},
		{"a,b,c", []string{"a", "b", "c"}},
		{" a , , b ", []string{"a", "b"}},
	}
	for _, c := range cases {
		got := splitCSV(c.in)
		if len(got) != len(c.want) {
			t.Errorf("splitCSV(%q) = %v, want %v", c.in, got, c.want)
			continue
		}
		for i := range got {
			if got[i] != c.want[i] {
				t.Errorf("splitCSV(%q)[%d] = %q, want %q", c.in, i, got[i], c.want[i])
			}
		}
	}
}
